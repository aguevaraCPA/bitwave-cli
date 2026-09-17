// Package workspaceshare is the bitwave HTTP client for the cloud ledger workspace
// share-and-adopt endpoints:
//
//   - POST /v1/workspaces:share          (multipart, unauthenticated)
//   - POST /v1/workspaces/{id}:accept    (JSON, bearer-authenticated)
//
// The share path is intentionally unauthenticated — bitwave in local mode has no
// org context to attach. The recipient email is the gate, and an out-of-band
// magic-link mailer is expected to deliver the viewer URL to the recipient.
package workspaceshare

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
)

// UploadResponse is the JSON body the cloud ledger returns from /v1/workspaces:share.
// The server blocks on the share workflow before responding, so the CLI sees
// the final delivery status — the resolved recipient and whether the invite
// email was sent — rather than just a workflow handle.
type UploadResponse struct {
	WorkspaceId    string `json:"workspaceId"`
	RecipientId    string `json:"recipientId"`
	EmailDelivered bool   `json:"emailDelivered"`
}

// AcceptResponse is the JSON body the cloud ledger returns from /v1/workspaces/{id}:accept.
type AcceptResponse struct {
	WorkspaceId   string `json:"workspaceId"`
	WorkflowId    string `json:"workflowId"`
	WorkflowRunId string `json:"workflowRunId"`
}

// Client wraps both share + adopt calls. Upload is anonymous; Adopt threads a
// bearer token through TokenResolver.
type Client struct {
	BaseURL       string
	TokenResolver func() (string, error)
	HTTPClient    *http.Client
	Files         config.Filesystem
}

// New constructs a client. Pass a TokenResolver only if you intend to call
// Adopt — Upload skips the Authorization header regardless.
func New(baseURL string, tokenResolver func() (string, error)) *Client {
	return &Client{
		BaseURL:       strings.TrimRight(baseURL, "/"),
		TokenResolver: tokenResolver,
		HTTPClient:    &http.Client{Timeout: 5 * time.Minute},
	}
}

// UploadAndShare zips the workspace directory and posts it as multipart form
// data. The server validates + stashes the zip + writes a PENDING workspace
// row; the returned URL is what the agent emails to the recipient (today it's
// informational only — the public viewer is a follow-up).
//
// Only `.bitwave.toml`, `accounts.ledger`, `prices.ledger`, `*.journal`, and
// `*.ledger` are included. Wallet/credential files are refused at the server
// allowlist gate too, but the client filters them first so a misnamed file
// doesn't blow the upload.
func (c *Client) UploadAndShare(ctx context.Context, workspaceDir, recipientEmail, message string) (*UploadResponse, error) {
	files := c.Files
	if files == nil {
		files = config.OSFilesystem()
	}
	zipBuf, err := buildZipFS(files, workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("build workspace zip: %w", err)
	}

	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	if err := mp.WriteField("to", recipientEmail); err != nil {
		return nil, fmt.Errorf("write to field: %w", err)
	}
	if message != "" {
		if err := mp.WriteField("message", message); err != nil {
			return nil, fmt.Errorf("write message field: %w", err)
		}
	}
	fw, err := mp.CreateFormFile("zip", "workspace.zip")
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write(zipBuf.Bytes()); err != nil {
		return nil, fmt.Errorf("write zip to form: %w", err)
	}
	if err := mp.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/workspaces:share", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, apierr.Format(resp.StatusCode, http.MethodPost, c.BaseURL+"/v1/workspaces:share", data)
	}
	var out UploadResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// Adopt accepts a shared workspace. Authentication is required — the
// recipient must be logged in as the principal that received the magic link.
func (c *Client) Adopt(ctx context.Context, workspaceId, newName string) (*AcceptResponse, error) {
	if c.TokenResolver == nil {
		return nil, fmt.Errorf("adopt requires a token resolver")
	}
	tok, err := c.TokenResolver()
	if err != nil {
		return nil, err
	}
	reqBody, err := json.Marshal(map[string]string{"newName": newName})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/v1/workspaces/%s:accept", c.BaseURL, workspaceId),
		bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, apierr.Format(resp.StatusCode, http.MethodPost, fmt.Sprintf("%s/v1/workspaces/%s:accept", c.BaseURL, workspaceId), data)
	}
	var out AcceptResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &out, nil
}

// buildZip walks workspaceDir and zips the files the server's allowlist
// accepts. The server enforces the same allowlist + size caps; filtering here
// just gives the operator a clearer error before paying upload cost.
func buildZip(workspaceDir string) (*bytes.Buffer, error) {
	return buildZipFS(config.OSFilesystem(), workspaceDir)
}

func buildZipFS(files config.Filesystem, workspaceDir string) (*bytes.Buffer, error) {
	entries, err := files.ReadDir(workspaceDir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	included := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !isWorkspaceFile(name) {
			continue
		}
		data, err := files.ReadFile(filepath.Join(workspaceDir, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		fw, err := zw.Create(name)
		if err != nil {
			return nil, fmt.Errorf("zip create %s: %w", name, err)
		}
		if _, err := fw.Write(data); err != nil {
			return nil, fmt.Errorf("zip write %s: %w", name, err)
		}
		included++
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if included == 0 {
		return nil, fmt.Errorf("no workspace files found in %s (need .bitwave.toml + at least one .journal/.ledger file)", workspaceDir)
	}
	return &buf, nil
}

// isWorkspaceFile decides whether a directory entry belongs in the share zip.
// Mirrors the server's workspaceingest allowlist; wallet-* and credentials*
// files are filtered out client-side so a misnamed file doesn't make the
// round trip just to be rejected.
func isWorkspaceFile(name string) bool {
	if strings.HasPrefix(name, "wallet-") || strings.HasPrefix(name, "credentials") {
		return false
	}
	if name == ".bitwave.toml" || name == "accounts.ledger" || name == "prices.ledger" {
		return true
	}
	if strings.HasSuffix(name, ".journal") || strings.HasSuffix(name, ".ledger") {
		return true
	}
	return false
}
