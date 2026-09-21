package orgreports

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bitwave-io/bitwave-cli/internal/apierr"
)

type OrgDetails struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Timezone     string `json:"timezone"`
	BaseCurrency any    `json:"baseCurrency"`
}

type InventoryView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Wallet and Subsidiary intentionally contain only discovery-safe fields used
// to resolve human labels to stable report filter IDs.
type Wallet struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	NetworkID    string   `json:"networkId,omitempty"`
	Address      string   `json:"address,omitempty"`
	Addresses    []string `json:"addresses,omitempty"`
	SubsidiaryID string   `json:"subsidiaryId,omitempty"`
	// VaultAddress is set only for DeFi position wallets: the pool / vault /
	// staking-contract address the position lives in. Address is then the
	// user's own wallet address on that network.
	VaultAddress string `json:"vaultAddress,omitempty"`
	// Protocol is the DeFi protocol label stored on a DeFi position wallet.
	Protocol string `json:"protocol,omitempty"`
}

type Subsidiary struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	SubType string `json:"subType,omitempty"`
}

type TransactionAssetRequest struct {
	Timezone string                   `json:"timezone,omitempty"`
	Limit    int                      `json:"limit"`
	Filters  TransactionExportFilters `json:"filters"`
}

type ColumnUniqueValues struct {
	Values        []any  `json:"values"`
	NextPageToken string `json:"nextPageToken,omitempty"`
}

type TransactionDateRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

type TransactionAmountRange struct {
	From *float64 `json:"from,omitempty"`
	To   *float64 `json:"to,omitempty"`
}

type TransactionExportFilters struct {
	DateRange                   *TransactionDateRange   `json:"dateRange,omitempty"`
	AmountRange                 *TransactionAmountRange `json:"amountRange,omitempty"`
	FMVAmountRange              *TransactionAmountRange `json:"fmvAmountRange,omitempty"`
	WalletIDs                   []string                `json:"walletIds,omitempty"`
	SubsidiaryIDs               []string                `json:"subsidiaryIds,omitempty"`
	AssetIDs                    []string                `json:"assetIds,omitempty"`
	AmountCurrencyNames         []string                `json:"amountCurrencyNames,omitempty"`
	TransactionTypes            []string                `json:"transactionTypes,omitempty"`
	MethodIDs                   []string                `json:"methodIds,omitempty"`
	Stage                       string                  `json:"stage,omitempty"`
	States                      []string                `json:"states,omitempty"`
	CategorizationStatuses      []string                `json:"categorizationStatuses,omitempty"`
	ReconciliationStatuses      []string                `json:"reconciliationStatuses,omitempty"`
	IgnoredStatuses             []string                `json:"ignoredStatuses,omitempty"`
	SearchTokens                []string                `json:"searchTokens,omitempty"`
	TransactionIDs              []string                `json:"transactionIds,omitempty"`
	FromAddresses               []string                `json:"fromAddresses,omitempty"`
	ToAddresses                 []string                `json:"toAddresses,omitempty"`
	Addresses                   []string                `json:"addresses,omitempty"`
	Operations                  []string                `json:"operations,omitempty"`
	IncludeCombinedTransactions bool                    `json:"includeCombinedTransactions,omitempty"`
	IsCombinedParent            *bool                   `json:"isCombinedParent,omitempty"`
	IsSplitChild                *bool                   `json:"isSplitChild,omitempty"`
	IsSplitParent               *bool                   `json:"isSplitParent,omitempty"`
	NeedsReview                 *bool                   `json:"needsReview,omitempty"`
	Errored                     *bool                   `json:"errored,omitempty"`
}

type TransactionExportRequest struct {
	Timezone      string                   `json:"timezone"`
	SortBy        string                   `json:"sortBy,omitempty"`
	SortDirection string                   `json:"sortDirection,omitempty"`
	Filters       TransactionExportFilters `json:"filters"`
}

type ActionsExportInput struct {
	From           string
	To             string
	Inventory      []string
	SubsidiaryIDs  []string
	Actions        []string
	Statuses       []string
	TransactionIDs []string
	Assets         []string
	AssetIDs       []string
	LineErrors     []string
}

type ExportResponse struct {
	FileType  string   `json:"fileType,omitempty"`
	ExportID  string   `json:"exportId,omitempty"`
	Filename  string   `json:"filename,omitempty"`
	ExportIDs []string `json:"exportIds,omitempty"`
}

func (r ExportResponse) IDs() []string {
	if r.ExportID != "" {
		return []string{r.ExportID}
	}
	return append([]string(nil), r.ExportIDs...)
}

func (c *Client) Org(ctx context.Context, orgID string) (*OrgDetails, error) {
	data, err := c.do(ctx, http.MethodGet, "/v3/orgs/"+url.PathEscape(orgID), nil)
	if err != nil {
		return nil, err
	}
	var org OrgDetails
	if err := json.Unmarshal(data, &org); err == nil && org.ID != "" {
		return &org, nil
	}
	var wrapped struct {
		Org OrgDetails `json:"org"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("decode organization response: %w", err)
	}
	if wrapped.Org.ID == "" {
		return nil, fmt.Errorf("organization response did not include an id")
	}
	return &wrapped.Org, nil
}

func (c *Client) InventoryViews(ctx context.Context, orgID string) ([]InventoryView, error) {
	var response struct {
		Items []InventoryView `json:"items"`
	}
	path := "/orgs/" + url.PathEscape(orgID) + "/inventory-views"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	return response.Items, nil
}

// Wallets returns every enabled wallet, following the API's pagination token.
func (c *Client) Wallets(ctx context.Context, orgID string) ([]Wallet, error) {
	var result []Wallet
	var token string
	for {
		query := url.Values{"pageLimit": {"500"}, "nameSortOrder": {"asc"}}
		if token != "" {
			query.Set("paginationToken", token)
		}
		var response struct {
			Items    []Wallet `json:"items"`
			NextPage string   `json:"nextPage"`
		}
		path := "/orgs/" + url.PathEscape(orgID) + "/wallets?" + query.Encode()
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		result = append(result, response.Items...)
		if response.NextPage == "" || response.NextPage == token {
			return result, nil
		}
		token = response.NextPage
	}
}

func (c *Client) Subsidiaries(ctx context.Context, orgID string) ([]Subsidiary, error) {
	var result []Subsidiary
	path := "/orgs/" + url.PathEscape(orgID) + "/subsidiaries"
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// TransactionAssetIDs returns the transaction search facet. Supplying wallet
// IDs or a date range makes this a dependent choice list without downloading
// transaction rows into the LLM context.
func (c *Client) TransactionAssetIDs(ctx context.Context, orgID string, input TransactionAssetRequest) ([]string, error) {
	var response struct {
		AssetIDs []string `json:"assetIds"`
	}
	path := "/v3/orgs/" + url.PathEscape(orgID) + "/transactions/search"
	if err := c.doJSON(ctx, http.MethodPost, path, input, &response); err != nil {
		return nil, err
	}
	return response.AssetIDs, nil
}

// ActionColumnValues returns all unique values for one Actions column.
func (c *Client) ActionColumnValues(ctx context.Context, orgID, inventoryViewID, column, from, to string) ([]string, error) {
	var result []string
	var token string
	for {
		query := url.Values{"showEmptyLots": {"false"}, "startDate": {from}, "asOf": {to}}
		if token != "" {
			query.Set("pageToken", token)
		}
		path := "/orgs/" + url.PathEscape(orgID) + "/inventory-views/" + url.PathEscape(inventoryViewID) +
			"/actions/columns/" + url.PathEscape(column) + "/unique?" + query.Encode()
		var response ColumnUniqueValues
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
			return nil, err
		}
		for _, value := range response.Values {
			result = append(result, fmt.Sprint(value))
		}
		if response.NextPageToken == "" || response.NextPageToken == token {
			return result, nil
		}
		token = response.NextPageToken
	}
}

// StreamTransactionExport writes the V3 Transaction Export CSV to dst. The
// request uses the same filter contract as transaction search; pagination
// fields are deliberately absent because the export endpoint streams all rows.
func (c *Client) StreamTransactionExport(ctx context.Context, orgID string, input TransactionExportRequest, dst io.Writer) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	token, err := c.TokenResolver()
	if err != nil {
		return err
	}
	endpoint := c.BaseURL + "/v3/orgs/" + url.PathEscape(orgID) + "/transactions/export"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/csv")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return apierr.Format(resp.StatusCode, http.MethodPost, endpoint, body)
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("stream transaction export: %w", err)
	}
	return nil
}

func (c *Client) StartActionsExport(ctx context.Context, orgID, inventoryViewID string, input ActionsExportInput) (*ExportResponse, error) {
	query := url.Values{
		"startDate":     {input.From},
		"asOf":          {input.To},
		"exportResults": {"true"},
	}
	addAll := func(key string, values []string) {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				query.Add(key, value)
			}
		}
	}
	addAll("inventory", input.Inventory)
	addAll("subsidiaryId", input.SubsidiaryIDs)
	addAll("action", input.Actions)
	addAll("status", input.Statuses)
	addAll("txnId", input.TransactionIDs)
	addAll("asset", input.Assets)
	addAll("assetId", input.AssetIDs)
	addAll("lineError", input.LineErrors)

	path := "/orgs/" + url.PathEscape(orgID) + "/inventory-views/" + url.PathEscape(inventoryViewID) + "/actions?" + query.Encode()
	var response ExportResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &response); err != nil {
		return nil, err
	}
	if len(response.IDs()) == 0 {
		return nil, fmt.Errorf("actions export returned no export id")
	}
	return &response, nil
}

func (c *Client) ExportDownloadURL(ctx context.Context, orgID, exportID, exportType string) (string, error) {
	query := url.Values{"rawUrl": {"true"}}
	if exportType != "" {
		query.Set("exportType", exportType)
	}
	path := "/v2/orgs/" + url.PathEscape(orgID) + "/exports/" + url.PathEscape(exportID) + "?" + query.Encode()
	data, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var href string
	if json.Unmarshal(data, &href) != nil {
		href = strings.TrimSpace(string(data))
	}
	if href == "" {
		return "", fmt.Errorf("export %s returned an empty download URL", exportID)
	}
	parsed, err := url.Parse(href)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", fmt.Errorf("export %s returned an invalid download URL", exportID)
	}
	return href, nil
}
