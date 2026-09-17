package operations

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

const reportSchemaVersion = "1"

type reportChoice struct {
	Label string         `json:"label"`
	Value string         `json:"value"`
	Meta  map[string]any `json:"meta,omitempty"`
}

type reportField struct {
	Name        string         `json:"name"`
	Flag        string         `json:"flag"`
	Type        string         `json:"type"`
	Description string         `json:"description"`
	Required    bool           `json:"required,omitempty"`
	Multiple    bool           `json:"multiple,omitempty"`
	DependsOn   []string       `json:"dependsOn,omitempty"`
	Choices     []reportChoice `json:"choices,omitempty"`
	ChoiceState string         `json:"choiceState,omitempty"`
}

type reportOptionsEnvelope struct {
	SchemaVersion string        `json:"schemaVersion"`
	Command       string        `json:"command"`
	Organization  string        `json:"organization"`
	Fields        []reportField `json:"fields"`
	Warnings      []string      `json:"warnings,omitempty"`
}

type reportResultEnvelope struct {
	SchemaVersion string         `json:"schemaVersion"`
	Status        string         `json:"status"`
	Report        string         `json:"report"`
	Organization  string         `json:"organization,omitempty"`
	OutputFiles   []string       `json:"outputFiles,omitempty"`
	RowCount      *int           `json:"rowCount,omitempty"`
	Filters       map[string]any `json:"filters,omitempty"`
	Warnings      []string       `json:"warnings,omitempty"`
	Error         *reportError   `json:"error,omitempty"`
}

type reportError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	HTTPStatus int    `json:"httpStatus,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
}

type reportOptionsFlags struct {
	orgID         string
	walletRefs    []string
	inventoryView string
	from          string
	to            string
}

func newReportOptionsCmd() *op.Definition {
	var f reportOptionsFlags
	var jsonOutput bool
	cmd := &op.Definition{
		Use:   "options REPORT",
		Short: "Return an LLM-friendly schema and valid report filter choices",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runReportOptions(cmd, canonicalReportName(args[0]), f)
		},
	}
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringSliceVar(&f.walletRefs, "wallet", nil, "Narrow dependent asset choices by wallet ID or exact name")
	cmd.Flags().StringVar(&f.inventoryView, "inventory-view", "", "Inventory view ID or exact name for Actions choices")
	cmd.Flags().StringVar(&f.from, "from", "", "Start date used to narrow dependent choices")
	cmd.Flags().StringVar(&f.to, "to", "", "End date used to narrow dependent choices")
	cmd.Flags().BoolVar(&jsonOutput, "json", true, "Emit machine-readable JSON (the only supported options format)")
	return cmd
}

func canonicalReportName(value string) string {
	switch strings.ToLower(value) {
	case "transactions-export", "txn-export":
		return "transaction-export"
	default:
		return strings.ToLower(value)
	}
}

func runReportOptions(cmd *op.Call, report string, f reportOptionsFlags) error {
	if report != "balance" && report != "transaction-export" && report != "actions" {
		return fmt.Errorf("unknown organization report %q; run `bitwave report list --json`", report)
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	client := newReportsClient(cmd.Context(), orgID)
	wallets, err := client.Wallets(cmd.Context(), orgID)
	if err != nil {
		return fmt.Errorf("discover wallets: %w", err)
	}
	subsidiaries, err := client.Subsidiaries(cmd.Context(), orgID)
	if err != nil {
		return fmt.Errorf("discover subsidiaries: %w", err)
	}

	envelope := reportOptionsEnvelope{SchemaVersion: reportSchemaVersion, Command: "bitwave report " + report, Organization: orgID}
	walletChoices := choicesFromWallets(wallets)
	resolvedOptionWallets, resolveErr := resolveWalletRefs(f.walletRefs, wallets)
	if resolveErr != nil {
		return resolveErr
	}
	if len(resolvedOptionWallets) > 0 {
		walletChoices = choicesForValues(walletChoices, resolvedOptionWallets)
	}
	subsidiaryChoices := choicesFromSubsidiaries(subsidiaries)
	switch report {
	case "balance":
		envelope.Fields = balanceOptionFields(walletChoices, subsidiaryChoices)
		envelope.Warnings = append(envelope.Warnings, "Wallet filtering currently requires --report-api v3; the production-stable v1 download path only supports subsidiary filtering.")
	case "transaction-export":
		if (f.from == "") != (f.to == "") {
			return errors.New("--from and --to must be supplied together when narrowing asset choices")
		}
		if f.from != "" {
			if err := validateExportDateRange(f.from, f.to, false); err != nil {
				return err
			}
		}
		assetState := "complete"
		org, orgErr := client.Org(cmd.Context(), orgID)
		if orgErr != nil {
			return fmt.Errorf("load organization settings: %w", orgErr)
		}
		assetIDs, assetErr := client.TransactionAssetIDs(cmd.Context(), orgID, orgreports.TransactionAssetRequest{
			Timezone: org.Timezone, Limit: 1,
			Filters: orgreports.TransactionExportFilters{WalletIDs: resolvedOptionWallets, DateRange: optionalDateRange(f.from, f.to)},
		})
		if assetErr != nil {
			assetState = "unavailable"
			envelope.Warnings = append(envelope.Warnings, "Asset choices could not be loaded: "+assetErr.Error())
		}
		envelope.Fields = transactionOptionFields(walletChoices, subsidiaryChoices, stringChoices(assetIDs), assetState)
	case "actions":
		if len(f.walletRefs) > 0 {
			return errors.New("Actions does not support wallet filtering because the current backend endpoint ignores that filter")
		}
		views, viewErr := client.InventoryViews(cmd.Context(), orgID)
		if viewErr != nil {
			return fmt.Errorf("discover inventory views: %w", viewErr)
		}
		envelope.Fields = actionsOptionFields(choicesFromViews(views), subsidiaryChoices, nil, "requires_dependencies")
		if f.inventoryView == "" || f.from == "" || f.to == "" {
			envelope.Warnings = append(envelope.Warnings, "Pass --inventory-view, --from, and --to to populate Actions values from the selected inventory run.")
			break
		}
		if err := validateExportDateRange(f.from, f.to, false); err != nil {
			return err
		}
		view, resolveErr := resolveInventoryView(f.inventoryView, views)
		if resolveErr != nil {
			return resolveErr
		}
		columns := []string{"inventory", "subsidiaryId", "action", "status", "asset", "assetId", "lineError"}
		values := make(map[string][]string, len(columns))
		for _, column := range columns {
			columnValues, columnErr := client.ActionColumnValues(cmd.Context(), orgID, view.ID, column, f.from, f.to)
			if columnErr != nil {
				envelope.Warnings = append(envelope.Warnings, fmt.Sprintf("Actions %s choices could not be loaded: %v", column, columnErr))
				continue
			}
			values[column] = columnValues
		}
		envelope.Fields = actionsOptionFields(choicesFromViews(views), subsidiaryChoices, values, "complete")
	}
	return writeJSON(cmd.OutOrStdout(), envelope)
}

func balanceOptionFields(wallets, subsidiaries []reportChoice) []reportField {
	return []reportField{
		{Name: "asOf", Flag: "--as-of", Type: "date", Required: true, Description: "Balance date in the organization's calendar."},
		{Name: "groupBy", Flag: "--group-by", Type: "select", Description: "Report grouping.", Choices: stringChoices([]string{"wallet", "asset"})},
		{Name: "currency", Flag: "--currency", Type: "string", Description: "Fiat currency code; defaults to the organization base currency."},
		{Name: "wallets", Flag: "--wallet", Type: "select", Multiple: true, Description: "Wallet IDs or exact names.", Choices: wallets},
		{Name: "subsidiaries", Flag: "--subsidiary", Type: "select", Multiple: true, Description: "Subsidiary IDs or exact names.", Choices: subsidiaries},
		{Name: "includeIgnored", Flag: "--include-ignored", Type: "boolean", Description: "Include ignored transactions."},
		{Name: "excludeNFT", Flag: "--exclude-nft", Type: "boolean", Description: "Exclude NFT balances."},
		{Name: "skipPricing", Flag: "--skip-pricing", Type: "boolean", Description: "Skip fiat valuation."},
	}
}

func transactionOptionFields(wallets, subsidiaries, assets []reportChoice, assetState string) []reportField {
	return []reportField{
		{Name: "from", Flag: "--from", Type: "date", Required: true, Description: "Inclusive start date; omit only with --all-dates."},
		{Name: "to", Flag: "--to", Type: "date", Required: true, Description: "Inclusive end date; omit only with --all-dates."},
		{Name: "allDates", Flag: "--all-dates", Type: "boolean", Description: "Explicit unbounded export; mutually exclusive with dates."},
		{Name: "wallets", Flag: "--wallet", Type: "select", Multiple: true, Description: "Wallet IDs or exact names.", Choices: wallets},
		{Name: "assets", Flag: "--asset", Type: "select", Multiple: true, Description: "Asset IDs present in matching transactions.", DependsOn: []string{"wallets", "from", "to"}, Choices: assets, ChoiceState: assetState},
		{Name: "subsidiaries", Flag: "--subsidiary", Type: "select", Multiple: true, Description: "Subsidiary IDs or exact names.", Choices: subsidiaries},
		{Name: "transactionTypes", Flag: "--type", Type: "select", Multiple: true, Description: "Transaction direction/type.", Choices: stringChoices([]string{"send", "receive", "trade", "transfer", "contract-execution", "unknown"})},
		{Name: "states", Flag: "--state", Type: "select", Multiple: true, Description: "Transaction workflow state.", Choices: stringChoices(transactionStates())},
		{Name: "categorization", Flag: "--categorization", Type: "select", Multiple: true, Description: "Categorization bucket.", Choices: stringChoices([]string{"Categorized", "Uncategorized"})},
		{Name: "reconciliation", Flag: "--reconciliation", Type: "select", Multiple: true, Description: "Reconciliation bucket.", Choices: stringChoices([]string{"Reconciled", "Unreconciled"})},
		{Name: "ignored", Flag: "--ignored", Type: "select", Multiple: true, Description: "Ignored bucket.", Choices: stringChoices([]string{"Ignored", "Unignored"})},
		{Name: "search", Flag: "--search", Type: "string", Multiple: true, Description: "Transaction ID/address substring; maximum five."},
		{Name: "includeCombined", Flag: "--include-combined", Type: "boolean", Description: "Include combined transaction children."},
	}
}

func actionsOptionFields(views, subsidiaries []reportChoice, values map[string][]string, state string) []reportField {
	choice := func(column string) ([]reportChoice, string) {
		if values == nil {
			return nil, state
		}
		v, ok := values[column]
		if !ok {
			return nil, "unavailable"
		}
		return stringChoices(v), "complete"
	}
	inventories, inventoryState := choice("inventory")
	actions, actionState := choice("action")
	statuses, statusState := choice("status")
	assets, assetState := choice("asset")
	assetIDs, assetIDState := choice("assetId")
	lineErrors, lineErrorState := choice("lineError")
	deps := []string{"inventoryView", "from", "to"}
	return []reportField{
		{Name: "inventoryView", Flag: "--inventory-view", Type: "select", Required: true, Description: "Inventory view controlling the active run and accounting method.", Choices: views},
		{Name: "from", Flag: "--from", Type: "date", Required: true, Description: "Inclusive start date."},
		{Name: "to", Flag: "--to", Type: "date", Required: true, Description: "Inclusive as-of date."},
		{Name: "inventory", Flag: "--inventory", Type: "select", Multiple: true, Description: "Inventory value.", DependsOn: deps, Choices: inventories, ChoiceState: inventoryState},
		{Name: "subsidiaries", Flag: "--subsidiary", Type: "select", Multiple: true, Description: "Subsidiary ID.", Choices: subsidiaries},
		{Name: "actions", Flag: "--action", Type: "select", Multiple: true, Description: "Action type.", DependsOn: deps, Choices: actions, ChoiceState: actionState},
		{Name: "statuses", Flag: "--status", Type: "select", Multiple: true, Description: "Action status.", DependsOn: deps, Choices: statuses, ChoiceState: statusState},
		{Name: "transactions", Flag: "--transaction", Type: "string", Multiple: true, Description: "Transaction ID."},
		{Name: "assets", Flag: "--asset", Type: "select", Multiple: true, Description: "Asset ticker/name.", DependsOn: deps, Choices: assets, ChoiceState: assetState},
		{Name: "assetIDs", Flag: "--asset-id", Type: "select", Multiple: true, Description: "Stable asset ID.", DependsOn: deps, Choices: assetIDs, ChoiceState: assetIDState},
		{Name: "lineErrors", Flag: "--line-error", Type: "select", Multiple: true, Description: "Line error.", DependsOn: deps, Choices: lineErrors, ChoiceState: lineErrorState},
	}
}

func choicesFromWallets(items []orgreports.Wallet) []reportChoice {
	choices := make([]reportChoice, 0, len(items))
	for _, item := range items {
		meta := map[string]any{}
		if item.NetworkID != "" {
			meta["networkId"] = item.NetworkID
		}
		if item.SubsidiaryID != "" {
			meta["subsidiaryId"] = item.SubsidiaryID
		}
		choices = append(choices, reportChoice{Label: item.Name, Value: item.ID, Meta: meta})
	}
	return sortedChoices(choices)
}

func choicesFromSubsidiaries(items []orgreports.Subsidiary) []reportChoice {
	choices := make([]reportChoice, 0, len(items))
	for _, item := range items {
		choices = append(choices, reportChoice{Label: item.Name, Value: item.ID, Meta: map[string]any{"type": item.SubType}})
	}
	return sortedChoices(choices)
}

func choicesFromViews(items []orgreports.InventoryView) []reportChoice {
	choices := make([]reportChoice, 0, len(items))
	for _, item := range items {
		choices = append(choices, reportChoice{Label: item.Name, Value: item.ID})
	}
	return sortedChoices(choices)
}

func stringChoices(values []string) []reportChoice {
	seen := map[string]bool{}
	choices := make([]reportChoice, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			choices = append(choices, reportChoice{Label: value, Value: value})
		}
	}
	return sortedChoices(choices)
}

func sortedChoices(choices []reportChoice) []reportChoice {
	sort.SliceStable(choices, func(i, j int) bool { return strings.ToLower(choices[i].Label) < strings.ToLower(choices[j].Label) })
	return choices
}

func choicesForValues(choices []reportChoice, values []string) []reportChoice {
	wanted := make(map[string]bool, len(values))
	for _, value := range values {
		wanted[value] = true
	}
	filtered := make([]reportChoice, 0, len(values))
	for _, choice := range choices {
		if wanted[choice.Value] {
			filtered = append(filtered, choice)
		}
	}
	return filtered
}

func transactionStates() []string {
	return []string{"new", "ready-to-price", "failed-to-price", "priced", "categorized", "ready-to-sync", "open-needs-review", "new-needs-review", "syncing", "synced", "failed-to-sync", "marked-synced", "closed-needs-review", "ignored", "deleted", "combined", "pre-categorized"}
}

func optionalDateRange(from, to string) *orgreports.TransactionDateRange {
	if from == "" && to == "" {
		return nil
	}
	return &orgreports.TransactionDateRange{From: from, To: to}
}

func resolveWalletRefs(refs []string, wallets []orgreports.Wallet) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		found := ""
		for _, wallet := range wallets {
			if wallet.ID == ref {
				found = wallet.ID
				break
			}
		}
		if found == "" {
			var matches []string
			for _, wallet := range wallets {
				if strings.EqualFold(wallet.Name, ref) {
					matches = append(matches, wallet.ID)
				}
			}
			if len(matches) == 1 {
				found = matches[0]
			}
			if len(matches) > 1 {
				return nil, fmt.Errorf("wallet name %q is ambiguous; choose one of these IDs: %s", ref, strings.Join(matches, ", "))
			}
		}
		if found == "" {
			return nil, fmt.Errorf("wallet %q not found; run `bitwave report options transaction-export --json`", ref)
		}
		ids = append(ids, found)
	}
	return ids, nil
}

func resolveSubsidiaryRefs(refs []string, subsidiaries []orgreports.Subsidiary) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		found := ""
		for _, subsidiary := range subsidiaries {
			if subsidiary.ID == ref {
				found = subsidiary.ID
				break
			}
		}
		if found == "" {
			var matches []string
			for _, subsidiary := range subsidiaries {
				if strings.EqualFold(subsidiary.Name, ref) {
					matches = append(matches, subsidiary.ID)
				}
			}
			if len(matches) == 1 {
				found = matches[0]
			}
			if len(matches) > 1 {
				return nil, fmt.Errorf("subsidiary name %q is ambiguous; choose one of these IDs: %s", ref, strings.Join(matches, ", "))
			}
		}
		if found == "" {
			return nil, fmt.Errorf("subsidiary %q not found; run `bitwave report options transaction-export --json`", ref)
		}
		ids = append(ids, found)
	}
	return ids, nil
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func emitReportError(cmd *op.Call, report string, err error) error {
	apiError := new(apierr.Error)
	payload := reportError{Code: "invalid_request", Message: err.Error(), Retryable: false, Suggestion: "Run `bitwave report options " + report + "` to inspect required fields and valid choices."}
	if errors.As(err, &apiError) {
		payload.Code = "api_error"
		payload.HTTPStatus = apiError.Status
		payload.Retryable = apiError.Status == 429 || apiError.Status >= 500
	}
	_ = writeJSON(cmd.OutOrStdout(), reportResultEnvelope{SchemaVersion: reportSchemaVersion, Status: "error", Report: report, Error: &payload})
	return err
}

func emitReportSuccess(cmd *op.Call, report, orgID string, files []string, filters map[string]any) error {
	var rowCount *int
	if len(files) == 1 {
		if count, err := csvDataRowCount(cmd.Context(), files[0]); err == nil {
			rowCount = &count
		}
	}
	abs := make([]string, 0, len(files))
	for _, file := range files {
		path, err := op.RuntimeFrom(cmd.Context()).Files.Stat(file)
		if err != nil || path.IsDir() {
			continue
		}
		if absolute, err := op.RuntimeFrom(cmd.Context()).Files.Path(file); err == nil {
			file = absolute
		}
		abs = append(abs, file)
	}
	return writeJSON(cmd.OutOrStdout(), reportResultEnvelope{SchemaVersion: reportSchemaVersion, Status: "success", Report: report, Organization: orgID, OutputFiles: abs, RowCount: rowCount, Filters: filters})
}

func csvDataRowCount(ctx context.Context, path string) (int, error) {
	f, err := op.RuntimeFrom(ctx).Files.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(f)
	rows := -1
	for {
		_, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		rows++
	}
	if rows < 0 {
		rows = 0
	}
	return rows, nil
}
