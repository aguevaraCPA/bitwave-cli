package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/workspaces"
)

// Cloud is the cloud-backed implementation of Store. It uses gl-svc's current
// workspace-scoped /v1 surface. Do not route cloud operations through the
// accounting SDK's legacy /api/v1/orgs/{org}/ledger/workspaces surface: the
// gateway no longer registers those item routes.
type Cloud struct {
	workspaces *workspaces.Client
	baseURL    string
	wsId       string
	token      func() (string, error)
	httpClient *http.Client
}

// NewCloud builds a bitwave cloud store.
func NewCloud(baseURL, orgId, workspaceId string, tokenResolver func() (string, error)) *Cloud {
	return &Cloud{
		workspaces: workspaces.New(baseURL, orgId, tokenResolver),
		baseURL:    strings.TrimRight(baseURL, "/"),
		wsId:       workspaceId,
		token:      tokenResolver,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// SetRequestContext injects the caller's transport and cancellation into all
// workspace and ledger requests made by this short-lived store.
func (c *Cloud) SetRequestContext(ctx context.Context, client *http.Client) {
	c.workspaces.Context = ctx
	if client != nil {
		c.httpClient = client
		c.workspaces.HTTPClient = client
	}
}

func (c *Cloud) Project(ctx context.Context) (*model.Project, error) {
	var workspace workspaceDTO
	if err := c.get(ctx, c.workspacePath(), &workspace); err != nil {
		return nil, fmt.Errorf("load cloud workspace: %w", err)
	}

	var accounts []accountDTO
	if err := c.get(ctx, c.ledgerPath("accounts"), &accounts); err != nil {
		return nil, fmt.Errorf("load cloud accounts: %w", err)
	}
	var commodities []commodityDTO
	if err := c.get(ctx, c.ledgerPath("commodities"), &commodities); err != nil {
		return nil, fmt.Errorf("load cloud commodities: %w", err)
	}
	var entries []entryDTO
	if err := c.get(ctx, c.ledgerPath("entries"), &entries); err != nil {
		return nil, fmt.Errorf("load cloud entries: %w", err)
	}
	var prices []priceDTO
	if err := c.get(ctx, c.ledgerPath("prices"), &prices); err != nil {
		return nil, fmt.Errorf("load cloud prices: %w", err)
	}

	project := &model.Project{Name: workspace.Name, BaseCurrency: workspace.BaseCurrency}
	accountNames := make(map[string]string, len(accounts))
	for _, account := range accounts {
		accountNames[account.ID] = account.Name
		project.Accounts = append(project.Accounts, model.Account{
			Name: account.Name,
			Type: model.AccountType(account.Type),
			Note: account.Note,
		})
	}
	for _, commodity := range commodities {
		project.Commodities = append(project.Commodities, model.Commodity{
			Symbol: commodity.Symbol, Note: commodity.Note, Format: commodity.Format,
			NoMarket: commodity.NoMarket, IsDefault: commodity.IsDefault,
		})
	}
	for _, wireEntry := range entries {
		entry, err := entryFromDTO(wireEntry, accountNames, workspace.BaseCurrency)
		if err != nil {
			return nil, err
		}
		project.Entries = append(project.Entries, entry)
	}
	for _, wirePrice := range prices {
		price, err := priceFromDTO(wirePrice)
		if err != nil {
			return nil, err
		}
		project.Prices = append(project.Prices, price)
	}
	project.SortEntries()
	return project, nil
}

func (c *Cloud) Journals(ctx context.Context) ([]string, error) {
	js, err := c.workspaces.ListJournals(c.wsId)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(js))
	for i, j := range js {
		out[i] = j.Id
	}
	return out, nil
}

func (c *Cloud) EnsureJournal(ctx context.Context, journalId string) error {
	if journalId == "" {
		return fmt.Errorf("journal id is required")
	}
	js, err := c.workspaces.ListJournals(c.wsId)
	if err != nil {
		return err
	}
	for _, j := range js {
		if j.Id == journalId {
			return nil
		}
	}
	_, err = c.workspaces.CreateJournal(c.wsId, workspaces.CreateJournalRequest{
		Id:   journalId,
		Name: titleFromId(journalId),
	})
	return err
}

func (c *Cloud) AddAccount(ctx context.Context, a model.Account) error {
	return c.post(ctx, c.ledgerPath("accounts"), accountDTO{
		Name: a.Name, Type: string(a.Type), Note: a.Note,
	}, nil)
}

func (c *Cloud) AddPrice(ctx context.Context, p model.Price) error {
	wire := priceDTO{
		PriceDate: p.Date.Format("2006-01-02"), Asset: p.Commodity,
		QuoteCurrency: p.QuoteCurrency, Price: p.Price.FloatString(8),
	}
	if p.HasTime {
		wire.PriceTime = p.Date.Format("15:04:05Z07:00")
	}
	return c.post(ctx, c.ledgerPath("prices"), wire, nil)
}

func (c *Cloud) AddEntry(ctx context.Context, journalId string, e model.Entry) (string, error) {
	wire := entryToDTO(e)
	var response entryDTO
	path := c.ledgerPath("journals", journalId, "entries")
	if err := c.post(ctx, path, wire, &response); err != nil {
		return "", err
	}
	if response.ID == "" {
		return "", fmt.Errorf("add cloud entry: response did not include an id")
	}
	return response.ID, nil
}

func (c *Cloud) SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error {
	return fmt.Errorf("changing cloud entry status is not supported by the current workspace API")
}

func (c *Cloud) Import(ctx context.Context, journalId string, p *model.Project) error {
	var buf strings.Builder
	if err := format.Print(&buf, p); err != nil {
		return err
	}
	u, err := url.Parse(c.baseURL + c.ledgerPath("import"))
	if err != nil {
		return err
	}
	query := u.Query()
	query.Set("journalId", journalId)
	u.RawQuery = query.Encode()
	return c.postURL(ctx, u.String(), map[string]string{"text": buf.String()}, nil)
}

func (c *Cloud) workspacePath(parts ...string) string {
	path := "/v1/workspaces/" + url.PathEscape(c.wsId)
	for _, part := range parts {
		path += "/" + url.PathEscape(part)
	}
	return path
}

func (c *Cloud) ledgerPath(parts ...string) string {
	return c.workspacePath(append([]string{"ledger"}, parts...)...)
}

func (c *Cloud) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, c.baseURL+path, nil, out)
}

func (c *Cloud) post(ctx context.Context, path string, body, out any) error {
	return c.postURL(ctx, c.baseURL+path, body, out)
}

func (c *Cloud) postURL(ctx context.Context, requestURL string, body, out any) error {
	return c.do(ctx, http.MethodPost, requestURL, body, out)
}

func (c *Cloud) do(ctx context.Context, method, requestURL string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	token, err := c.token()
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return apierr.Format(resp.StatusCode, method, requestURL, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, requestURL, err)
	}
	return nil
}

type workspaceDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	BaseCurrency string `json:"baseCurrency"`
}

type accountDTO struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Note string `json:"note,omitempty"`
}

type commodityDTO struct {
	Symbol    string `json:"symbol"`
	Note      string `json:"note,omitempty"`
	Format    string `json:"format,omitempty"`
	NoMarket  bool   `json:"nomarket,omitempty"`
	IsDefault bool   `json:"default,omitempty"`
}

type postingDTO struct {
	AccountID    string `json:"accountId,omitempty"`
	AccountName  string `json:"accountName,omitempty"`
	Quantity     string `json:"quantity"`
	Asset        string `json:"asset"`
	ExchangeRate string `json:"exchangeRate,omitempty"`
	Status       string `json:"status,omitempty"`
	Note         string `json:"note,omitempty"`
}

type entryDTO struct {
	ID          string       `json:"id,omitempty"`
	EntryDate   string       `json:"entryDate"`
	Payee       string       `json:"payee,omitempty"`
	Description string       `json:"description,omitempty"`
	Status      string       `json:"status,omitempty"`
	Postings    []postingDTO `json:"postings"`
}

type priceDTO struct {
	PriceDate     string `json:"priceDate"`
	PriceTime     string `json:"priceTime,omitempty"`
	Asset         string `json:"asset"`
	QuoteCurrency string `json:"quoteCurrency"`
	Price         string `json:"price"`
}

func entryToDTO(entry model.Entry) entryDTO {
	wire := entryDTO{
		ID: entry.ID, EntryDate: entry.Date.Format("2006-01-02"), Payee: entry.Payee,
		Description: entry.Note, Status: statusToWire(entry.Status),
	}
	for _, posting := range entry.Postings {
		p := postingDTO{AccountName: posting.Account, Status: "", Note: posting.Note}
		if posting.Amount.Quantity != nil {
			p.Quantity = posting.Amount.Quantity.FloatString(8)
			p.Asset = posting.Amount.Commodity
		}
		if posting.UnitPrice != nil && posting.UnitPrice.Quantity != nil {
			p.ExchangeRate = posting.UnitPrice.Quantity.FloatString(8)
		}
		if posting.Status != nil {
			p.Status = statusToWire(*posting.Status)
		}
		wire.Postings = append(wire.Postings, p)
	}
	return wire
}

func entryFromDTO(wire entryDTO, accountNames map[string]string, baseCurrency string) (model.Entry, error) {
	date, err := time.Parse("2006-01-02", wire.EntryDate)
	if err != nil {
		return model.Entry{}, fmt.Errorf("cloud entry %s date: %w", wire.ID, err)
	}
	status, err := model.ParseStatus(wire.Status)
	if err != nil {
		return model.Entry{}, fmt.Errorf("cloud entry %s status: %w", wire.ID, err)
	}
	entry := model.Entry{ID: wire.ID, Date: date, Payee: wire.Payee, Note: wire.Description, Status: status}
	for _, wirePosting := range wire.Postings {
		quantity, ok := new(big.Rat).SetString(wirePosting.Quantity)
		if !ok {
			return model.Entry{}, fmt.Errorf("cloud entry %s has invalid quantity %q", wire.ID, wirePosting.Quantity)
		}
		accountName := wirePosting.AccountName
		if accountName == "" {
			accountName = accountNames[wirePosting.AccountID]
		}
		if accountName == "" {
			return model.Entry{}, fmt.Errorf("cloud entry %s posting references unknown account %s", wire.ID, wirePosting.AccountID)
		}
		posting := model.Posting{
			Account: accountName,
			Amount:  model.Amount{Quantity: quantity, Commodity: wirePosting.Asset},
			Note:    wirePosting.Note,
		}
		if wirePosting.ExchangeRate != "" {
			rate, ok := new(big.Rat).SetString(wirePosting.ExchangeRate)
			if !ok {
				return model.Entry{}, fmt.Errorf("cloud entry %s has invalid exchange rate %q", wire.ID, wirePosting.ExchangeRate)
			}
			posting.UnitPrice = &model.Amount{Quantity: rate, Commodity: baseCurrency}
		}
		if wirePosting.Status != "" {
			postingStatus, err := model.ParseStatus(wirePosting.Status)
			if err != nil {
				return model.Entry{}, fmt.Errorf("cloud entry %s posting status: %w", wire.ID, err)
			}
			posting.Status = &postingStatus
		}
		entry.Postings = append(entry.Postings, posting)
	}
	return entry, nil
}

func priceFromDTO(wire priceDTO) (model.Price, error) {
	var (
		date    time.Time
		err     error
		hasTime bool
	)
	if wire.PriceTime == "" {
		date, err = time.Parse("2006-01-02", wire.PriceDate)
	} else {
		date, err = time.Parse("2006-01-02 15:04:05Z07:00", wire.PriceDate+" "+wire.PriceTime)
		hasTime = true
	}
	if err != nil {
		return model.Price{}, fmt.Errorf("cloud price date: %w", err)
	}
	value, ok := new(big.Rat).SetString(wire.Price)
	if !ok {
		return model.Price{}, fmt.Errorf("cloud price has invalid value %q", wire.Price)
	}
	return model.Price{
		Date: date, HasTime: hasTime, Commodity: wire.Asset,
		QuoteCurrency: wire.QuoteCurrency, Price: value,
	}, nil
}

func statusToWire(status model.Status) string {
	switch status {
	case model.StatusCleared:
		return "CLEARED"
	case model.StatusPending:
		return "PENDING"
	default:
		return "UNCLEARED"
	}
}

// titleFromId upper-cases the first rune of every "-" or " " separated word.
func titleFromId(id string) string {
	parts := strings.FieldsFunc(id, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}
