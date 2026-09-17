// Package blockchainquery is a thin HTTP client for the Bitwave blockchain query API.
// It backs `bitwave wallets sync` — the per-wallet on-chain history pull that
// builds local ledger entries.
package blockchainquery

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client wraps the blockchain query API address-scan endpoint.
type Client struct {
	BaseURL       string
	TokenResolver func() (string, error)
	HTTPClient    *http.Client
	Context       context.Context
}

// New returns a Client with a 30s default timeout. TokenResolver may be nil
// for unauthenticated calls (e.g. when pointing at a local dev instance).
func New(baseURL string, tokenResolver func() (string, error)) *Client {
	return &Client{
		BaseURL:       baseURL,
		TokenResolver: tokenResolver,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ScanRequest is the address-scan input. Time bounds are optional (zero = unbounded).
type ScanRequest struct {
	Chain           string
	Address         string
	StartTimeUnixMs int64
	EndTimeUnixMs   int64
	Cursor          string
	Limit           int
	Order           string // "asc" or "desc"; empty == server default ("desc")
	FetchPayloads   bool
}

// ScanResponse mirrors the server's response. Payload is the raw aggregated
// BQ rows (JSON array) — see internal/adapters/bq for the row shape.
type ScanResponse struct {
	Results    []ScanResult `json:"results"`
	NextCursor string       `json:"nextCursor,omitempty"`
}

type ScanResult struct {
	PrimaryKey      string          `json:"primaryKey"`
	TxnHash         string          `json:"txnHash,omitempty"`
	BlockNumber     uint64          `json:"blockNumber,omitempty"`
	BlockTimeUnixMs int64           `json:"blockTimeUnixMs,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

func (c *Client) ScanAddress(req ScanRequest) (*ScanResponse, error) {
	q := url.Values{}
	if req.Cursor != "" {
		q.Set("cursor", req.Cursor)
	}
	if req.StartTimeUnixMs > 0 {
		q.Set("startTimeUnixMs", strconv.FormatInt(req.StartTimeUnixMs, 10))
	}
	if req.EndTimeUnixMs > 0 {
		q.Set("endTimeUnixMs", strconv.FormatInt(req.EndTimeUnixMs, 10))
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Order != "" {
		q.Set("order", req.Order)
	}
	if req.FetchPayloads {
		q.Set("fetchPayloads", "true")
	}
	path := fmt.Sprintf("/chains/%s/addresses/%s/transactions", req.Chain, req.Address)
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	data, err := c.do("GET", path)
	if err != nil {
		return nil, err
	}
	var out ScanResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse scan response: %w", err)
	}
	return &out, nil
}

func (c *Client) do(method, path string) ([]byte, error) {
	ctx := c.Context
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if c.TokenResolver != nil {
		tok, err := c.TokenResolver()
		if err != nil {
			return nil, err
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, apierr.Format(resp.StatusCode, method, c.BaseURL+path, data)
	}
	return data, nil
}
