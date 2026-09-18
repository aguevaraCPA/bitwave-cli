package orgreports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

const importListFields = `
  id status createdMS progress importErrors
  createdByUser { userId name email }
  rowErrorCount { validate run }
  warningRowCount
`

const importsPageQuery = `query ImportsPage($orgId: ID!, $offset: Int, $limit: Int) {
  importsPage(orgId: $orgId, offset: $offset, limit: $limit) {
    items {` + importListFields + `}
    total
  }
}`

const importsQuery = `query Imports($orgId: ID!) {
  imports(orgId: $orgId) {` + importListFields + `}
}`

const importStatusQuery = `query GetImport($orgId: ID!, $id: ID!) {
  getImport(orgId: $orgId, id: $id) {
    id status createdMS progress importErrors
    createdByUser { userId name email }
    rowErrorCount { validate run }
    warningRowCount rowsImported rowsTotal
  }
}`

const importStatusLegacyQuery = `query GetImport($orgId: ID!, $id: ID!) {
  getImport(orgId: $orgId, id: $id) {
    id status createdMS progress importErrors
    createdByUser { userId name email }
    rowErrorCount { validate run }
    warningRowCount
  }
}`

const createImportMutation = `mutation CreateImport($orgId: ID!) {
  createImport(orgId: $orgId) { id fileUploadUrl }
}`

const validateImportMutation = `mutation ValidateImport($orgId: ID!, $id: ID!) {
  validateImport(orgId: $orgId, id: $id)
}`

const runImportMutation = `mutation RunImport($orgId: ID!, $id: ID!) {
  runImport(orgId: $orgId, id: $id)
}`

const importRowErrorsQuery = `query ImportRowErrors($orgId: ID!, $id: ID!, $stage: String, $offset: Int, $limit: Int) {
  getImport(orgId: $orgId, id: $id) {
    rowErrors(stage: $stage, offset: $offset, limit: $limit) {
      rowIndex rowId stage errors
    }
  }
}`

const importPreviewQuery = `query ImportPreview($orgId: ID!, $id: ID!) {
  getImport(orgId: $orgId, id: $id) {
    id status
    previewLines {
      index errors hasErrors
      data {
        id blockchainId transactionType timeISO accountId
        amount amountTicker cost costTicker fee feeTicker
        contactId categoryId remoteContactId memo
      }
    }
  }
}`

type ImportUserPointer struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Email  string `json:"email,omitempty"`
}

type ImportRowErrorCount struct {
	Validate int `json:"validate"`
	Run      int `json:"run"`
}

// ImportRecord mirrors the manual transaction import shown at /import.
type ImportRecord struct {
	ID              string               `json:"id"`
	Status          string               `json:"status"`
	CreatedMS       int64                `json:"createdMS,omitempty"`
	Progress        *float64             `json:"progress,omitempty"`
	ImportErrors    []string             `json:"importErrors,omitempty"`
	CreatedByUser   *ImportUserPointer   `json:"createdByUser,omitempty"`
	RowErrorCount   *ImportRowErrorCount `json:"rowErrorCount,omitempty"`
	WarningRowCount *int                 `json:"warningRowCount,omitempty"`
	RowsImported    *int                 `json:"rowsImported,omitempty"`
	RowsTotal       *int                 `json:"rowsTotal,omitempty"`
}

type ImportsPage struct {
	Items []ImportRecord `json:"items"`
	Total int            `json:"total"`
}

type CreateImportResult struct {
	ID            string `json:"id"`
	FileUploadURL string `json:"fileUploadUrl"`
}

type ImportRowError struct {
	RowIndex *int     `json:"rowIndex"`
	RowID    string   `json:"rowId,omitempty"`
	Stage    string   `json:"stage,omitempty"`
	Errors   []string `json:"errors"`
}

type ImportPreviewLine struct {
	Index     *int           `json:"index"`
	Errors    []string       `json:"errors,omitempty"`
	HasErrors bool           `json:"hasErrors"`
	Data      map[string]any `json:"data,omitempty"`
}

func (c *Client) ImportsPage(ctx context.Context, orgID string, offset, limit int) (*ImportsPage, error) {
	var response struct {
		Data struct {
			Page *ImportsPage `json:"importsPage"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	err := c.importGraphQL(ctx, "ImportsPage", importsPageQuery, map[string]any{
		"orgId": orgID, "offset": offset, "limit": limit,
	}, &response)
	if err == nil && response.Data.Page != nil {
		return response.Data.Page, nil
	}
	// Match the web UI: older deployments without importsPage fall back to the
	// full list and slice locally. Other failures must not trigger a heavier call.
	if err == nil || !isImportSchemaCompatibilityError(err) {
		if err == nil {
			err = errors.New("importsPage response was empty")
		}
		return nil, err
	}
	items, listErr := c.Imports(ctx, orgID)
	if listErr != nil {
		return nil, listErr
	}
	if offset > len(items) {
		offset = len(items)
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return &ImportsPage{Items: items[offset:end], Total: len(items)}, nil
}

func (c *Client) Imports(ctx context.Context, orgID string) ([]ImportRecord, error) {
	var response struct {
		Data struct {
			Imports []ImportRecord `json:"imports"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := c.importGraphQL(ctx, "Imports", importsQuery, map[string]any{"orgId": orgID}, &response); err != nil {
		return nil, err
	}
	return response.Data.Imports, nil
}

func (c *Client) Import(ctx context.Context, orgID, importID string) (*ImportRecord, error) {
	record, err := c.getImportWithQuery(ctx, orgID, importID, importStatusQuery)
	if err != nil && isImportSchemaCompatibilityError(err) {
		record, err = c.getImportWithQuery(ctx, orgID, importID, importStatusLegacyQuery)
	}
	return record, err
}

func (c *Client) getImportWithQuery(ctx context.Context, orgID, importID, query string) (*ImportRecord, error) {
	var response struct {
		Data struct {
			Import *ImportRecord `json:"getImport"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := c.importGraphQL(ctx, "GetImport", query, map[string]any{"orgId": orgID, "id": importID}, &response); err != nil {
		return nil, err
	}
	if response.Data.Import == nil || response.Data.Import.ID == "" {
		return nil, fmt.Errorf("import %q was not found", importID)
	}
	return response.Data.Import, nil
}

func (c *Client) CreateImport(ctx context.Context, orgID string) (*CreateImportResult, error) {
	var response struct {
		Data struct {
			Import *CreateImportResult `json:"createImport"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := c.importGraphQL(ctx, "CreateImport", createImportMutation, map[string]any{"orgId": orgID}, &response); err != nil {
		return nil, err
	}
	if response.Data.Import == nil || response.Data.Import.ID == "" || response.Data.Import.FileUploadURL == "" {
		return nil, errors.New("create import response did not include an id and upload URL")
	}
	return response.Data.Import, nil
}

func (c *Client) ValidateImport(ctx context.Context, orgID, importID string) error {
	return c.importMutation(ctx, "ValidateImport", validateImportMutation, orgID, importID, "validateImport")
}

func (c *Client) RunImport(ctx context.Context, orgID, importID string) error {
	return c.importMutation(ctx, "RunImport", runImportMutation, orgID, importID, "runImport")
}

func (c *Client) importMutation(ctx context.Context, operation, query, orgID, importID, field string) error {
	var response struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []graphQLError             `json:"errors"`
	}
	if err := c.importGraphQL(ctx, operation, query, map[string]any{"orgId": orgID, "id": importID}, &response); err != nil {
		return err
	}
	value := response.Data[field]
	if len(value) == 0 || string(value) == "null" {
		return fmt.Errorf("%s response was empty", field)
	}
	return nil
}

func (c *Client) ImportRowErrors(ctx context.Context, orgID, importID, stage string, offset, limit int) ([]ImportRowError, error) {
	var response struct {
		Data struct {
			Import *struct {
				RowErrors []ImportRowError `json:"rowErrors"`
			} `json:"getImport"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	variables := map[string]any{"orgId": orgID, "id": importID, "stage": stage, "offset": offset, "limit": limit}
	if err := c.importGraphQL(ctx, "ImportRowErrors", importRowErrorsQuery, variables, &response); err != nil {
		return nil, err
	}
	if response.Data.Import == nil {
		return nil, fmt.Errorf("import %q was not found", importID)
	}
	return response.Data.Import.RowErrors, nil
}

// ImportPreview exposes the legacy /import review rows. Temporal v2 imports
// intentionally return an empty list because validation is their pre-commit review.
func (c *Client) ImportPreview(ctx context.Context, orgID, importID string) (string, []ImportPreviewLine, error) {
	var response struct {
		Data struct {
			Import *struct {
				ID           string              `json:"id"`
				Status       string              `json:"status"`
				PreviewLines []ImportPreviewLine `json:"previewLines"`
			} `json:"getImport"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := c.importGraphQL(ctx, "ImportPreview", importPreviewQuery, map[string]any{"orgId": orgID, "id": importID}, &response); err != nil {
		return "", nil, err
	}
	if response.Data.Import == nil {
		return "", nil, fmt.Errorf("import %q was not found", importID)
	}
	return response.Data.Import.Status, response.Data.Import.PreviewLines, nil
}

func (c *Client) importGraphQL(ctx context.Context, operation, query string, variables map[string]any, dst any) error {
	request := map[string]any{"operationName": operation, "query": query, "variables": variables}
	data, err := c.doEndpoint(ctx, http.MethodPost, c.RulesMutationURL, request, true)
	if err != nil {
		return err
	}
	var envelope struct {
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode %s errors: %w", operation, err)
	}
	if err := graphqlErrors(envelope.Errors); err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode %s response: %w", operation, err)
	}
	return nil
}

func isImportSchemaCompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "Cannot query field") || strings.Contains(message, "Unknown field") ||
		strings.Contains(message, "Unknown argument") || strings.Contains(message, "is not defined")
}

// UploadImportFile streams a CSV to the signed URL returned by CreateImport.
// Authentication is deliberately omitted: the URL signature is the credential.
func (c *Client) UploadImportFile(ctx context.Context, uploadURL, path string) error {
	parsed, err := url.Parse(uploadURL)
	if err != nil || !allowedImportUploadURL(parsed) {
		return errors.New("import service returned an invalid signed upload URL")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	return c.UploadImportReader(ctx, uploadURL, file, info.Size())
}

// UploadImportReader uploads an already-opened invocation-scoped input. The
// caller owns the reader and its lifetime; this method never resolves a path.
func (c *Client) UploadImportReader(ctx context.Context, uploadURL string, reader io.Reader, size int64) error {
	parsed, err := url.Parse(uploadURL)
	if err != nil || !allowedImportUploadURL(parsed) {
		return errors.New("import service returned an invalid signed upload URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, reader)
	if err != nil {
		return errors.New("build signed import upload request")
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "text/csv")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		// Do not expose the signed query string in an error or log.
		return fmt.Errorf("upload CSV to %s: request failed", parsed.Host)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("upload CSV to %s: HTTP %d", parsed.Host, resp.StatusCode)
	}
	return nil
}

func allowedImportUploadURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Host == "" || parsed.User != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "https" && host == "storage.googleapis.com" {
		return true
	}
	// Local/self-hosted development may use an upload emulator. Never allow an
	// insecure non-loopback destination for a user-provided CSV.
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
