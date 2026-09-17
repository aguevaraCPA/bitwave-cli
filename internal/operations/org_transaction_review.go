package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type transactionReviewAnalyzeFlags struct {
	orgID, network, nextToken string
	limit                     int
}

type transactionReviewSummaryFlags struct {
	orgID, from, to       string
	wallets, subsidiaries []string
}

type transactionReviewMutationFlags struct {
	transactionMutationFlags
	network string
}

type reviewValue struct {
	CurrencyID string `json:"currencyId"`
	Value      string `json:"value"`
}

type reviewRate struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Rate    string `json:"rate"`
	PriceID string `json:"priceId,omitempty"`
}

type reviewLine struct {
	Operation    string      `json:"operation"`
	Amount       reviewValue `json:"amount"`
	WalletID     string      `json:"walletId"`
	From         string      `json:"from,omitempty"`
	To           string      `json:"to,omitempty"`
	ExchangeRate *reviewRate `json:"exchangeRate,omitempty"`
}

type pendingReviewRate struct {
	ChangeReason   string     `json:"changeReason,omitempty"`
	ChangeAuditLog string     `json:"changeAuditLog,omitempty"`
	ExchangeRate   reviewRate `json:"exchangeRate"`
}

type transactionReviewDetail struct {
	State struct {
		State string `json:"state"`
		Txn   struct {
			TransactionID   string       `json:"transactionId"`
			TransactionType string       `json:"txnType"`
			WalletIDs       []string     `json:"walletIds"`
			Lines           []reviewLine `json:"txnLines"`
		} `json:"txn"`
		NeedsReview *struct {
			PendingWalletIDs     []string            `json:"pendingWalletIds"`
			PendingLines         []reviewLine        `json:"pendingTxnLines"`
			PendingExchangeRates []pendingReviewRate `json:"pendingExchangeRates"`
		} `json:"needsReview"`
		Categorization struct {
			Method  string `json:"categorizationMethod"`
			RuleID  string `json:"categorizeByRuleId"`
			Details struct {
				Type string `json:"type"`
			} `json:"categorizationDetails"`
		} `json:"categorization"`
	} `json:"state"`
}

type transactionReviewAnalysis struct {
	TransactionID        string   `json:"transactionId"`
	State                string   `json:"state"`
	TransactionType      string   `json:"transactionType,omitempty"`
	CategorizationType   string   `json:"categorizationType,omitempty"`
	CategorizationMethod string   `json:"categorizationMethod,omitempty"`
	RuleID               string   `json:"ruleId,omitempty"`
	CurrentLineCount     int      `json:"currentLineCount"`
	PendingLineCount     int      `json:"pendingLineCount"`
	PendingChangeReasons []string `json:"pendingChangeReasons,omitempty"`
	LinesEquivalent      bool     `json:"transactionLinesEquivalent"`
	RatesEquivalent      bool     `json:"exchangeRatesEquivalent"`
	WalletsEquivalent    bool     `json:"walletsEquivalent"`
}

func newTransactionReviewCmd() *op.Definition {
	cmd := &op.Definition{
		Use:   "review",
		Short: "Analyze or resolve transactions in Needs Review",
		Long: `Inspect and resolve the pending changes shown in Bitwave's Needs Review queue.

Accept applies pending lines, wallets, or pricing and may remove an existing
categorization. Ignore dismisses only the pending changes and preserves the
current transaction and categorization; it does not ignore the transaction.`,
	}
	cmd.AddCommand(newTransactionReviewSummaryCmd())
	cmd.AddCommand(newTransactionReviewAnalyzeCmd())
	cmd.AddCommand(newTransactionReviewResolveCmd("accept", orgreports.TransactionReviewAccept))
	cmd.AddCommand(newTransactionReviewResolveCmd("ignore", orgreports.TransactionReviewIgnore))
	return cmd
}

func newTransactionReviewSummaryCmd() *op.Definition {
	var f transactionReviewSummaryFlags
	cmd := &op.Definition{
		Use:   "summary",
		Short: "Summarize organization transaction health",
		Long: `Return the main transaction review counts used by Bitwave's transaction
and Close Dashboard surfaces, plus focused pricing, sync, and ignored counts.

The output is factual and does not recommend accounting or review actions. Use
transaction count for follow-up questions across any supported UI filter.`,
		Args: op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error { return runTransactionReviewSummary(cmd, f) },
	}
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&f.from, "from", "", "Inclusive start date (YYYY-MM-DD; requires --to)")
	cmd.Flags().StringVar(&f.to, "to", "", "Inclusive end date (YYYY-MM-DD; requires --from)")
	cmd.Flags().StringSliceVar(&f.wallets, "wallet", nil, "Wallet ID or exact name (repeatable)")
	cmd.Flags().StringSliceVar(&f.subsidiaries, "subsidiary", nil, "Subsidiary ID (repeatable)")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func runTransactionReviewSummary(cmd *op.Call, f transactionReviewSummaryFlags) error {
	if (f.from == "") != (f.to == "") {
		return errors.New("--from and --to must be supplied together")
	}
	if f.from != "" {
		if err := validateExportDateRange(f.from, f.to, false); err != nil {
			return err
		}
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	client := newReportsClient(cmd.Context(), orgID)
	org, err := client.Org(cmd.Context(), orgID)
	if err != nil {
		return fmt.Errorf("load organization settings: %w", err)
	}
	var walletIDs []string
	if len(uniqueNonEmpty(f.wallets)) > 0 {
		wallets, err := client.Wallets(cmd.Context(), orgID)
		if err != nil {
			return fmt.Errorf("discover wallets: %w", err)
		}
		walletIDs, err = resolveWalletRefs(f.wallets, wallets)
		if err != nil {
			return err
		}
	}
	baseFilters := orgreports.TransactionExportFilters{
		DateRange: optionalDateRange(f.from, f.to), WalletIDs: walletIDs,
		SubsidiaryIDs: uniqueNonEmpty(f.subsidiaries), IgnoredStatuses: []string{"Unignored"},
	}
	request := orgreports.TransactionOverviewRequest{Timezone: org.Timezone, Filters: baseFilters}
	overview, err := client.TransactionOverview(cmd.Context(), orgID, request)
	if err != nil {
		return fmt.Errorf("load transaction overview: %w", err)
	}
	stateSpecs := map[string]string{
		"awaitingPricing": "ready-to-price",
		"failedPricing":   "failed-to-price",
		"readyToSync":     "ready-to-sync",
		"syncing":         "syncing",
		"synced":          "synced",
		"failedSync":      "failed-to-sync",
		"markedSynced":    "marked-synced",
		"ignored":         "ignored",
	}
	stateCounts := make(map[string]int, len(stateSpecs))
	type stateCountResult struct {
		name  string
		count int
		err   error
	}
	results := make(chan stateCountResult, len(stateSpecs))
	for name, state := range stateSpecs {
		go func(name, state string) {
			filters := baseFilters
			filters.States = []string{state}
			if state == "ignored" {
				filters.IgnoredStatuses = nil
			}
			count, err := client.TransactionCount(cmd.Context(), orgID, orgreports.TransactionOverviewRequest{Timezone: org.Timezone, Filters: filters})
			results <- stateCountResult{name: name, count: count, err: err}
		}(name, state)
	}
	for range stateSpecs {
		result := <-results
		if result.err != nil {
			return fmt.Errorf("count %s transactions: %w", result.name, result.err)
		}
		stateCounts[result.name] = result.count
	}
	categorized := overview.All - overview.NeedsCategorization
	complete := overview.All - overview.NeedsCategorization - overview.ToBeReconciled
	if categorized < 0 {
		categorized = 0
	}
	if complete < 0 {
		complete = 0
	}
	return writeJSON(cmd.OutOrStdout(), map[string]any{
		"schemaVersion": "1", "organization": orgID,
		"scope": map[string]any{"from": f.from, "to": f.to, "walletIds": walletIDs, "subsidiaryIds": uniqueNonEmpty(f.subsidiaries)},
		"transactions": map[string]any{
			"all": overview.All, "categorized": categorized, "uncategorized": overview.NeedsCategorization,
			"toBeReconciled": overview.ToBeReconciled, "complete": complete,
			"needsReview": overview.NeedsReview, "ignored": stateCounts["ignored"],
		},
		"pricing": map[string]any{
			"awaitingPricing": stateCounts["awaitingPricing"], "failedPricing": stateCounts["failedPricing"],
		},
		"sync": map[string]any{
			"readyToSync": stateCounts["readyToSync"], "syncing": stateCounts["syncing"],
			"synced": stateCounts["synced"], "failedSync": stateCounts["failedSync"],
			"markedSynced": stateCounts["markedSynced"],
		},
		"firstTransactionDate": overview.FirstRecordDate,
		"followUp":             "Use `bitwave transaction count --help` to count any wallet, asset, ticker, address, type, status, amount, FMV, date, subsidiary, or metadata-method filter without downloading transactions.",
	})
}

func newTransactionReviewAnalyzeCmd() *op.Definition {
	var f transactionReviewAnalyzeFlags
	cmd := &op.Definition{
		Use:   "analyze [TRANSACTION_ID...]",
		Short: "Compare current and pending transaction data",
		Long: `Return a bounded, machine-readable comparison for a client LLM.

With no IDs, the command discovers the Needs Review queue first. It reports
current and pending lines, wallets, exchange rates, and existing categorization
without selecting an accept or ignore action for the caller.`,
		Args: op.ArbitraryArgs,
		RunE: func(cmd *op.Call, args []string) error {
			return runTransactionReviewAnalyze(cmd, args, f)
		},
	}
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&f.network, "network", "", "Bitwave network prefix for unqualified transaction hashes")
	cmd.Flags().IntVar(&f.limit, "limit", 25, "Maximum Needs Review transactions to analyze (1-100)")
	cmd.Flags().StringVar(&f.nextToken, "next-token", "", "Opaque next-page token from the previous analysis")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func runTransactionReviewAnalyze(cmd *op.Call, args []string, f transactionReviewAnalyzeFlags) error {
	if f.limit < 1 || f.limit > 100 {
		return errors.New("--limit must be between 1 and 100")
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	client := newReportsClient(cmd.Context(), orgID)
	transactionIDs := make([]string, 0, len(args))
	for _, arg := range uniqueNonEmpty(args) {
		transactionID, err := normalizeTransactionID(arg, f.network)
		if err != nil {
			return err
		}
		transactionIDs = append(transactionIDs, transactionID)
	}
	nextToken := ""
	if len(transactionIDs) == 0 {
		result, err := client.SearchTransactions(cmd.Context(), orgID, orgreports.TransactionSearchRequest{
			Limit: f.limit, NextToken: f.nextToken, SortBy: "timestamp", SortDirection: "desc",
			Filters: orgreports.TransactionExportFilters{States: []string{"open-needs-review", "new-needs-review"}},
		})
		if err != nil {
			return fmt.Errorf("discover Needs Review transactions: %w", err)
		}
		nextToken = result.NextToken
		for _, raw := range result.Transactions {
			var item struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &item) == nil && item.ID != "" {
				transactionIDs = append(transactionIDs, item.ID)
			}
		}
	}
	analyses := make([]transactionReviewAnalysis, 0, len(transactionIDs))
	for _, transactionID := range transactionIDs {
		raw, err := client.TransactionReviewDetail(cmd.Context(), orgID, transactionID)
		if err != nil {
			return fmt.Errorf("load review detail for %s: %w", transactionID, err)
		}
		analysis, err := analyzeTransactionReview(raw)
		if err != nil {
			return fmt.Errorf("analyze %s: %w", transactionID, err)
		}
		analyses = append(analyses, analysis)
	}
	return writeJSON(cmd.OutOrStdout(), map[string]any{
		"schemaVersion": "1", "organization": orgID, "count": len(analyses),
		"reviews": analyses, "nextToken": nextToken,
		"actionGuidance": map[string]string{
			"accept": "Apply pending changes; an existing categorization may be removed and require re-categorization.",
			"ignore": "Dismiss pending changes and preserve the current transaction and categorization; this does not ignore the transaction.",
		},
	})
}

func analyzeTransactionReview(raw json.RawMessage) (transactionReviewAnalysis, error) {
	var detail transactionReviewDetail
	if err := json.Unmarshal(raw, &detail); err != nil {
		return transactionReviewAnalysis{}, err
	}
	if detail.State.Txn.TransactionID == "" {
		return transactionReviewAnalysis{}, errors.New("review response did not include a transaction id")
	}
	result := transactionReviewAnalysis{
		TransactionID: detail.State.Txn.TransactionID, State: detail.State.State,
		TransactionType:      detail.State.Txn.TransactionType,
		CategorizationType:   detail.State.Categorization.Details.Type,
		CategorizationMethod: detail.State.Categorization.Method,
		RuleID:               detail.State.Categorization.RuleID,
		CurrentLineCount:     len(detail.State.Txn.Lines),
	}
	if detail.State.NeedsReview == nil {
		return result, nil
	}
	pending := detail.State.NeedsReview
	result.PendingLineCount = len(pending.PendingLines)
	result.LinesEquivalent = canonicalReviewLines(detail.State.Txn.Lines) == canonicalReviewLines(pending.PendingLines)
	result.RatesEquivalent = pendingRatesMatchCurrent(detail.State.Txn.Lines, pending.PendingExchangeRates)
	result.WalletsEquivalent = len(pending.PendingWalletIDs) == 0 || equalStringSets(detail.State.Txn.WalletIDs, pending.PendingWalletIDs)
	result.PendingChangeReasons = reviewChangeReasons(pending.PendingExchangeRates)
	return result, nil
}

func canonicalReviewLines(lines []reviewLine) string {
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		rateFrom, rateTo, rate := "", "", ""
		if line.ExchangeRate != nil {
			rateFrom, rateTo, rate = line.ExchangeRate.From, line.ExchangeRate.To, normalizeReviewDecimal(line.ExchangeRate.Rate)
		}
		items = append(items, strings.Join([]string{
			line.Operation, line.Amount.CurrencyID, normalizeReviewDecimal(line.Amount.Value),
			line.WalletID, line.From, line.To, rateFrom, rateTo, rate,
		}, "\x1f"))
	}
	sort.Strings(items)
	return strings.Join(items, "\x1e")
}

func normalizeReviewDecimal(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if number, ok := new(big.Rat).SetString(value); ok {
		return number.RatString()
	}
	return value
}

func pendingRatesMatchCurrent(lines []reviewLine, pending []pendingReviewRate) bool {
	current := make(map[string]string)
	for _, line := range lines {
		if line.ExchangeRate == nil {
			continue
		}
		key := line.ExchangeRate.From + "\x1f" + line.ExchangeRate.To
		current[key] = normalizeReviewDecimal(line.ExchangeRate.Rate)
	}
	for _, item := range pending {
		key := item.ExchangeRate.From + "\x1f" + item.ExchangeRate.To
		currentRate, ok := current[key]
		if !ok || currentRate != normalizeReviewDecimal(item.ExchangeRate.Rate) {
			return false
		}
	}
	return true
}

func equalStringSets(left, right []string) bool {
	a, b := append([]string(nil), left...), append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "\x1f") == strings.Join(b, "\x1f")
}

func reviewChangeReasons(rates []pendingReviewRate) []string {
	values := make([]string, 0, len(rates))
	for _, item := range rates {
		if item.ChangeReason != "" {
			values = append(values, item.ChangeReason)
		}
		if item.ChangeAuditLog != "" {
			values = append(values, item.ChangeAuditLog)
		}
	}
	return uniqueNonEmpty(values)
}

func newTransactionReviewResolveCmd(name, action string) *op.Definition {
	var f transactionReviewMutationFlags
	cmd := &op.Definition{
		Use:   name + " TRANSACTION_ID [TRANSACTION_ID...]",
		Short: map[string]string{"accept": "Apply pending Needs Review changes", "ignore": "Dismiss pending changes and preserve current categorization"}[name],
		Args:  op.MinimumNArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runTransactionReviewResolve(cmd, name, action, args, f)
		},
	}
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().StringVar(&f.network, "network", "", "Bitwave network prefix for unqualified transaction hashes")
	return cmd
}

func runTransactionReviewResolve(cmd *op.Call, operation, action string, args []string, f transactionReviewMutationFlags) error {
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, "review-"+operation, f.jsonOutput, err)
	}
	transactionIDs := make([]string, 0, len(args))
	for _, arg := range uniqueNonEmpty(args) {
		transactionID, err := normalizeTransactionID(arg, f.network)
		if err != nil {
			return mutationError(cmd, "review-"+operation, f.jsonOutput, err)
		}
		transactionIDs = append(transactionIDs, transactionID)
	}
	request := orgreports.TransactionReviewRequest{Transactions: make([]orgreports.TransactionReviewItem, 0, len(transactionIDs))}
	for _, transactionID := range transactionIDs {
		request.Transactions = append(request.Transactions, orgreports.TransactionReviewItem{TransactionID: transactionID})
	}
	preview := map[string]any{
		"method": "PUT", "path": fmt.Sprintf("/v3/orgs/%s/transactions/resolve?action=%s", orgID, action), "body": request,
	}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: "review-" + operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, "review-"+operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}
	client := newReportsClient(cmd.Context(), orgID)
	result, err := client.ResolveTransactionReviews(cmd.Context(), orgID, action, transactionIDs)
	if err != nil {
		return mutationError(cmd, "review-"+operation, f.jsonOutput, err)
	}
	envelope := mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: "review-" + operation, Organization: orgID, Request: preview, Result: result}
	if !result.Success {
		envelope.Status = "partial_failure"
		_ = writeJSON(cmd.OutOrStdout(), envelope)
		return fmt.Errorf("review-%s processed %d transaction(s): %d succeeded, %d failed", operation, result.Processed, result.SuccessCount, len(result.Failed))
	}
	return outputMutation(cmd, f.jsonOutput, envelope, fmt.Sprintf("review-%s: %d transaction(s) succeeded\n", operation, result.SuccessCount))
}
