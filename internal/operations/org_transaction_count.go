package operations

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type transactionCountFlags struct {
	orgID, from, to, stage, amountMin, amountMax, fmvMin, fmvMax string
	wallets, subsidiaries, assets, tickers, types, methodIDs     []string
	states, categorization, reconciliation, ignored              []string
	search, transactionIDs, fromAddresses, toAddresses           []string
	addresses, operations                                        []string
	includeCombined, combinedParent, splitChild, splitParent     bool
	needsReview, errored                                         bool
}

func newCountOrgTransactionsCmd() *op.Definition {
	var f transactionCountFlags
	cmd := &op.Definition{
		Use:   "count",
		Short: "Count transactions using UI-compatible filters",
		Long: `Return only a count for the selected transaction filters.

This exposes the product transaction count endpoint for inexpensive LLM
follow-up questions. It supports the same useful status, wallet, asset,
address, type, amount, FMV, date, subsidiary, and metadata-method dimensions
as the transaction UI without downloading matching transaction records.`,
		Args: op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error { return runCountOrgTransactions(cmd, f) },
	}
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&f.from, "from", "", "Inclusive start date (YYYY-MM-DD; requires --to)")
	cmd.Flags().StringVar(&f.to, "to", "", "Inclusive end date (YYYY-MM-DD; requires --from)")
	cmd.Flags().StringSliceVar(&f.wallets, "wallet", nil, "Wallet ID or exact name (repeatable)")
	cmd.Flags().StringSliceVar(&f.subsidiaries, "subsidiary", nil, "Subsidiary ID (repeatable)")
	cmd.Flags().StringSliceVar(&f.assets, "asset", nil, "Asset ID (repeatable)")
	cmd.Flags().StringSliceVar(&f.tickers, "ticker", nil, "Ticker shown in the transaction UI (repeatable)")
	cmd.Flags().StringSliceVar(&f.types, "type", nil, "Transaction type (repeatable)")
	cmd.Flags().StringSliceVar(&f.methodIDs, "method-id", nil, "Metadata method ID (repeatable)")
	cmd.Flags().StringVar(&f.stage, "stage", "", "Workflow stage: needs-categorization, to-be-synced, complete, ignored, or deleted")
	cmd.Flags().StringSliceVar(&f.states, "state", nil, "Transaction workflow state (repeatable)")
	cmd.Flags().StringSliceVar(&f.categorization, "categorization", nil, "Categorization status, such as Categorized or Uncategorized")
	cmd.Flags().StringSliceVar(&f.reconciliation, "reconciliation", nil, "Reconciliation status, such as Reconciled or Unreconciled")
	cmd.Flags().StringSliceVar(&f.ignored, "ignored", nil, "Ignored status, such as Ignored or Unignored")
	cmd.Flags().StringSliceVar(&f.search, "search", nil, "Free-text transaction ID/address search token (repeatable; maximum five)")
	cmd.Flags().StringSliceVar(&f.transactionIDs, "transaction", nil, "Transaction ID (repeatable)")
	cmd.Flags().StringSliceVar(&f.fromAddresses, "from-address", nil, "Exact sender address (repeatable)")
	cmd.Flags().StringSliceVar(&f.toAddresses, "to-address", nil, "Exact recipient address (repeatable)")
	cmd.Flags().StringSliceVar(&f.addresses, "address", nil, "Address on either side (repeatable)")
	cmd.Flags().StringSliceVar(&f.operations, "operation", nil, "Transaction line operation (repeatable)")
	cmd.Flags().StringVar(&f.amountMin, "amount-min", "", "Minimum token amount")
	cmd.Flags().StringVar(&f.amountMax, "amount-max", "", "Maximum token amount")
	cmd.Flags().StringVar(&f.fmvMin, "fmv-min", "", "Minimum base-currency FMV")
	cmd.Flags().StringVar(&f.fmvMax, "fmv-max", "", "Maximum base-currency FMV")
	cmd.Flags().BoolVar(&f.includeCombined, "include-combined", false, "Include combined transaction children")
	cmd.Flags().BoolVar(&f.combinedParent, "combined-parent", false, "Count only combined parent transactions")
	cmd.Flags().BoolVar(&f.splitChild, "split-child", false, "Count only split child transactions")
	cmd.Flags().BoolVar(&f.splitParent, "split-parent", false, "Count only split parent transactions")
	cmd.Flags().BoolVar(&f.needsReview, "needs-review", false, "Count transactions carrying pending Needs Review changes")
	cmd.Flags().BoolVar(&f.errored, "errored", false, "Count pricing or sync error states")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func runCountOrgTransactions(cmd *op.Call, f transactionCountFlags) error {
	if len(uniqueNonEmpty(f.search)) > 5 {
		return errors.New("--search may be supplied at most five times")
	}
	if (f.from == "") != (f.to == "") {
		return errors.New("--from and --to must be supplied together")
	}
	if f.from != "" {
		if err := validateExportDateRange(f.from, f.to, false); err != nil {
			return err
		}
	}
	amountRange, err := transactionCountRange(cmd, "amount-min", f.amountMin, "amount-max", f.amountMax)
	if err != nil {
		return err
	}
	fmvRange, err := transactionCountRange(cmd, "fmv-min", f.fmvMin, "fmv-max", f.fmvMax)
	if err != nil {
		return err
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
	filters := orgreports.TransactionExportFilters{
		DateRange: optionalDateRange(f.from, f.to), AmountRange: amountRange, FMVAmountRange: fmvRange,
		WalletIDs: walletIDs, SubsidiaryIDs: uniqueNonEmpty(f.subsidiaries), AssetIDs: uniqueNonEmpty(f.assets),
		AmountCurrencyNames: uniqueNonEmpty(f.tickers), TransactionTypes: uniqueNonEmpty(f.types),
		MethodIDs: uniqueNonEmpty(f.methodIDs), Stage: strings.TrimSpace(f.stage), States: uniqueNonEmpty(f.states),
		CategorizationStatuses: uniqueNonEmpty(f.categorization), ReconciliationStatuses: uniqueNonEmpty(f.reconciliation),
		IgnoredStatuses: uniqueNonEmpty(f.ignored), SearchTokens: uniqueNonEmpty(f.search),
		TransactionIDs: uniqueNonEmpty(f.transactionIDs), FromAddresses: uniqueNonEmpty(f.fromAddresses),
		ToAddresses: uniqueNonEmpty(f.toAddresses), Addresses: uniqueNonEmpty(f.addresses), Operations: uniqueNonEmpty(f.operations),
		IncludeCombinedTransactions: f.includeCombined,
	}
	filters.IsCombinedParent = changedBool(cmd, "combined-parent", f.combinedParent)
	filters.IsSplitChild = changedBool(cmd, "split-child", f.splitChild)
	filters.IsSplitParent = changedBool(cmd, "split-parent", f.splitParent)
	filters.NeedsReview = changedBool(cmd, "needs-review", f.needsReview)
	filters.Errored = changedBool(cmd, "errored", f.errored)
	request := orgreports.TransactionOverviewRequest{Timezone: org.Timezone, Filters: filters}
	count, err := client.TransactionCount(cmd.Context(), orgID, request)
	if err != nil {
		return fmt.Errorf("count transactions: %w", err)
	}
	return writeJSON(cmd.OutOrStdout(), map[string]any{
		"schemaVersion": "1", "organization": orgID, "count": count, "request": request,
	})
}

func transactionCountRange(cmd *op.Call, minName, minValue, maxName, maxValue string) (*orgreports.TransactionAmountRange, error) {
	var result orgreports.TransactionAmountRange
	if cmd.Flags().Changed(minName) {
		value, err := strconv.ParseFloat(strings.TrimSpace(minValue), 64)
		if err != nil {
			return nil, fmt.Errorf("--%s must be a number", minName)
		}
		result.From = &value
	}
	if cmd.Flags().Changed(maxName) {
		value, err := strconv.ParseFloat(strings.TrimSpace(maxValue), 64)
		if err != nil {
			return nil, fmt.Errorf("--%s must be a number", maxName)
		}
		result.To = &value
	}
	if result.From == nil && result.To == nil {
		return nil, nil
	}
	return &result, nil
}

func changedBool(cmd *op.Call, name string, value bool) *bool {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	return &value
}
