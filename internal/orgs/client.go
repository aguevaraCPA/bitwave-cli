// Package orgs is a thin HTTP client for the Bitwave core API /v3/orgs endpoints. It
// powers `bw org list / create` and the picker in `bw org switch`.
package orgs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Org is the minimal shape used by the CLI.
type Org struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Client is a stateless HTTP wrapper.
type Client struct {
	BaseURL       string
	TokenResolver func() (string, error)
	HTTPClient    *http.Client
}

// New returns a Client with a 30s default timeout.
func New(baseURL string, tokenResolver func() (string, error)) *Client {
	return &Client{
		BaseURL:       baseURL,
		TokenResolver: tokenResolver,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(method, path string, body any) ([]byte, error) {
	tok, err := c.TokenResolver()
	if err != nil {
		return nil, err
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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

// List calls GET /v3/orgs and returns the user's org memberships.
func (c *Client) List() ([]Org, error) {
	data, err := c.do("GET", "/v3/orgs", nil)
	if err != nil {
		return nil, err
	}
	// the Bitwave core API may return either {orgs: [...]} or [...] — accept both.
	var wrapper struct {
		Orgs []Org `json:"orgs"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Orgs != nil {
		return wrapper.Orgs, nil
	}
	var direct []Org
	if err := json.Unmarshal(data, &direct); err == nil {
		return direct, nil
	}
	return nil, fmt.Errorf("unrecognized /v3/orgs response: %s", string(data))
}

// Get returns one organization without enumerating every organization the
// identity can access. This is the preferred onboarding check because an
// org-scoped token may be allowed to read its org while /v3/orgs is not.
func (c *Client) Get(id string) (*Org, error) {
	data, err := c.do("GET", "/v3/orgs/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	var org Org
	if err := json.Unmarshal(data, &org); err == nil && org.ID != "" {
		return &org, nil
	}
	var wrapper struct {
		Org Org `json:"org"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Org.ID != "" {
		return &wrapper.Org, nil
	}
	return nil, fmt.Errorf("unrecognized /v3/orgs/%s response: %s", id, string(data))
}

// CreateRequest mirrors the minimum POST /v3/orgs body.
type CreateRequest struct {
	Name string `json:"name"`
}

// Create calls POST /v3/orgs and returns the new org.
func (c *Client) Create(req CreateRequest) (*Org, error) {
	data, err := c.do("POST", "/v3/orgs", req)
	if err != nil {
		return nil, err
	}
	var o Org
	if err := json.Unmarshal(data, &o); err != nil {
		return nil, fmt.Errorf("parse create response: %w", err)
	}
	if o.ID == "" {
		// Some endpoints wrap as {org: {...}}
		var wrapper struct {
			Org Org `json:"org"`
		}
		if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Org.ID != "" {
			return &wrapper.Org, nil
		}
		return nil, fmt.Errorf("create returned no org id: %s", string(data))
	}
	return &o, nil
}
