package operations

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type transactionExportFlags struct {
	from                   string
	to                     string
	allDates               bool
	walletIDs              []string
	subsidiaryIDs          []string
	assetIDs               []string
	transactionTypes       []string
	states                 []string
	categorizationStatuses []string
	reconciliationStatuses []string
	ignoredStatuses        []string
	searchTokens           []string
	includeCombined        bool
	out                    string
	orgID                  string
	jsonOutput             bool
}

type actionsReportFlags struct {
	from           string
	to             string
	inventoryView  string
	inventory      []string
	subsidiaryIDs  []string
	actions        []string
	statuses       []string
	transactionIDs []string
	assets         []string
	assetIDs       []string
	lineErrors     []string
	out            string
	orgID          string
	jsonOutput     bool
}

func newInventoryViewsCmd() *op.Definition {
	var orgID string
	var jsonOutput bool
	cmd := &op.Definition{
		Use:   "inventory-views",
		Short: "List inventory views available to inventory-backed reports",
		RunE: func(cmd *op.Call, _ []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			views, err := client.InventoryViews(cmd.Context(), resolvedOrg)
			if err != nil {
				return fmt.Errorf("list inventory views: %w", err)
			}
			if len(views) == 0 {
				if jsonOutput {
					return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": reportSchemaVersion, "organization": resolvedOrg, "inventoryViews": []any{}})
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "(no inventory views)")
				return nil
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": reportSchemaVersion, "organization": resolvedOrg, "inventoryViews": choicesFromViews(views)})
			}
			for _, view := range views {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-32s  %s\n", view.ID, view.Name)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON")
	return cmd
}

func newTransactionExportCmd() *op.Definition {
	var f transactionExportFlags
	cmd := &op.Definition{
		Use:     "transaction-export",
		Aliases: []string{"transactions-export", "txn-export"},
		Short:   "Download Bitwave's Transaction Export as CSV",
		Long: `Exports organization transactions through Bitwave's transaction-export endpoint.

--from and --to are inclusive local calendar dates evaluated in the
organization's configured timezone. Both are required unless --all-dates is
explicitly supplied. Omitting dates never silently creates an unbounded export.`,
		Example: `  bitwave report transaction-export --from 2026-01-01 --to 2026-06-30 --out transactions.csv
  bitwave report txn-export --all-dates --out all-transactions.csv`,
		RunE: func(cmd *op.Call, _ []string) error {
			err := runTransactionExport(cmd, &f)
			if err != nil && f.jsonOutput {
				return emitReportError(cmd, "transaction-export", err)
			}
			if err != nil || !f.jsonOutput {
				return err
			}
			orgID, _ := resolveReportOrg(cmd.Context(), f.orgID)
			return emitReportSuccess(cmd, "transaction-export", orgID, []string{f.out}, map[string]any{"from": dateLabel(f.from, f.allDates), "to": dateLabel(f.to, f.allDates), "wallets": f.walletIDs, "assets": f.assetIDs, "subsidiaries": f.subsidiaryIDs, "types": f.transactionTypes, "states": f.states, "categorization": f.categorizationStatuses, "reconciliation": f.reconciliationStatuses, "ignored": f.ignoredStatuses, "search": f.searchTokens, "includeCombined": f.includeCombined})
		},
	}
	cmd.Flags().StringVar(&f.from, "from", "", "Inclusive start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&f.to, "to", "", "Inclusive end date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&f.allDates, "all-dates", false, "Explicitly export all dates")
	cmd.Flags().StringSliceVar(&f.walletIDs, "wallet", nil, "Wallet ID filter (repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&f.subsidiaryIDs, "subsidiary", nil, "Subsidiary ID filter (repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&f.assetIDs, "asset", nil, "Asset ID filter (repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&f.transactionTypes, "type", nil, "Transaction type filter (repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&f.states, "state", nil, "Transaction state filter (repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&f.categorizationStatuses, "categorization", nil, "Categorization status filter")
	cmd.Flags().StringSliceVar(&f.reconciliationStatuses, "reconciliation", nil, "Reconciliation status filter")
	cmd.Flags().StringSliceVar(&f.ignoredStatuses, "ignored", nil, "Ignored status filter")
	cmd.Flags().StringSliceVar(&f.searchTokens, "search", nil, "Txn ID/address search token (maximum five)")
	cmd.Flags().BoolVar(&f.includeCombined, "include-combined", false, "Include combined transaction children")
	cmd.Flags().StringVarP(&f.out, "out", "o", "", "Output CSV (stdout when omitted)")
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().BoolVar(&f.jsonOutput, "json", false, "Emit a machine-readable result envelope (requires --out)")
	return cmd
}

func runTransactionExport(cmd *op.Call, f *transactionExportFlags) error {
	if f.jsonOutput && (f.out == "" || f.out == "-") {
		return errors.New("--json requires --out so stdout remains valid JSON")
	}
	if err := validateExportDateRange(f.from, f.to, f.allDates); err != nil {
		return err
	}
	if len(f.searchTokens) > 5 {
		return errors.New("--search accepts at most five values")
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	client := newReportsClient(cmd.Context(), orgID)
	if len(f.walletIDs) > 0 {
		wallets, discoverErr := client.Wallets(cmd.Context(), orgID)
		if discoverErr != nil {
			return fmt.Errorf("resolve wallets: %w", discoverErr)
		}
		f.walletIDs, err = resolveWalletRefs(f.walletIDs, wallets)
		if err != nil {
			return err
		}
	}
	if len(f.subsidiaryIDs) > 0 {
		subsidiaries, discoverErr := client.Subsidiaries(cmd.Context(), orgID)
		if discoverErr != nil {
			return fmt.Errorf("resolve subsidiaries: %w", discoverErr)
		}
		f.subsidiaryIDs, err = resolveSubsidiaryRefs(f.subsidiaryIDs, subsidiaries)
		if err != nil {
			return err
		}
	}
	org, err := client.Org(cmd.Context(), orgID)
	if err != nil {
		return fmt.Errorf("load organization settings: %w", err)
	}
	if org.Timezone == "" {
		return fmt.Errorf("organization %s has no timezone; refusing to apply ambiguous calendar dates", orgID)
	}

	filters := orgreports.TransactionExportFilters{
		WalletIDs:                   f.walletIDs,
		SubsidiaryIDs:               f.subsidiaryIDs,
		AssetIDs:                    f.assetIDs,
		TransactionTypes:            f.transactionTypes,
		States:                      f.states,
		CategorizationStatuses:      f.categorizationStatuses,
		ReconciliationStatuses:      f.reconciliationStatuses,
		IgnoredStatuses:             f.ignoredStatuses,
		SearchTokens:                f.searchTokens,
		IncludeCombinedTransactions: f.includeCombined,
	}
	if !f.allDates {
		filters.DateRange = &orgreports.TransactionDateRange{From: f.from, To: f.to}
	}
	request := orgreports.TransactionExportRequest{
		Timezone:      org.Timezone,
		SortBy:        "txnTimestamp",
		SortDirection: "asc",
		Filters:       filters,
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "source=bitwave-org-report org=%s report=transaction-export timezone=%s from=%s to=%s\n", orgID, org.Timezone, dateLabel(f.from, f.allDates), dateLabel(f.to, f.allDates))

	write := func(w io.Writer) error {
		return client.StreamTransactionExport(cmd.Context(), orgID, request, w)
	}
	if f.out == "" || f.out == "-" {
		return write(cmd.OutOrStdout())
	}
	if err := writeStreamAtomic(cmd.Context(), f.out, write); err != nil {
		return fmt.Errorf("save transaction export: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "saved=%s\n", f.out)
	return nil
}

func newActionsReportCmd() *op.Definition {
	var f actionsReportFlags
	cmd := &op.Definition{
		Use:   "actions",
		Short: "Download Bitwave's inventory-view Actions report",
		Long: `Downloads the report named "Actions" in Bitwave. It is calculated from the
selected inventory view's active run, so --inventory-view is always required.

--from and --to are inclusive dates interpreted by Bitwave in the organization's
timezone. The CLI accepts an inventory-view ID or an exact view name.`,
		Example: `  bitwave report inventory-views
  bitwave report actions --inventory-view "Primary FIFO" --from 2026-01-01 --to 2026-06-30 --out actions.csv`,
		RunE: func(cmd *op.Call, _ []string) error {
			err := runActionsReport(cmd, &f)
			if err != nil && f.jsonOutput {
				return emitReportError(cmd, "actions", err)
			}
			if err != nil || !f.jsonOutput {
				return err
			}
			orgID, _ := resolveReportOrg(cmd.Context(), f.orgID)
			return emitReportSuccess(cmd, "actions", orgID, actionResultPaths(cmd.Context(), f.out), map[string]any{"inventoryView": f.inventoryView, "from": f.from, "to": f.to, "inventory": f.inventory, "subsidiaries": f.subsidiaryIDs, "actions": f.actions, "statuses": f.statuses, "transactions": f.transactionIDs, "assets": f.assets, "assetIDs": f.assetIDs, "lineErrors": f.lineErrors})
		},
	}
	cmd.Flags().StringVar(&f.inventoryView, "inventory-view", "", "Inventory view ID or exact name (required)")
	cmd.Flags().StringVar(&f.from, "from", "", "Inclusive start date (YYYY-MM-DD, required)")
	cmd.Flags().StringVar(&f.to, "to", "", "Inclusive end/as-of date (YYYY-MM-DD, required)")
	cmd.Flags().StringSliceVar(&f.inventory, "inventory", nil, "Inventory filter (repeatable or comma-separated)")
	cmd.Flags().StringSliceVar(&f.subsidiaryIDs, "subsidiary", nil, "Subsidiary ID filter")
	cmd.Flags().StringSliceVar(&f.actions, "action", nil, "Action type filter")
	cmd.Flags().StringSliceVar(&f.statuses, "status", nil, "Action status filter")
	cmd.Flags().StringSliceVar(&f.transactionIDs, "transaction", nil, "Transaction ID filter")
	cmd.Flags().StringSliceVar(&f.assets, "asset", nil, "Asset ticker/name filter")
	cmd.Flags().StringSliceVar(&f.assetIDs, "asset-id", nil, "Asset ID filter")
	cmd.Flags().StringSliceVar(&f.lineErrors, "line-error", nil, "Line-error filter")
	cmd.Flags().StringVarP(&f.out, "out", "o", "", "Output path (required; multiple files get -part-N suffixes)")
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().BoolVar(&f.jsonOutput, "json", false, "Emit a machine-readable result envelope")
	return cmd
}

func runActionsReport(cmd *op.Call, f *actionsReportFlags) error {
	if strings.TrimSpace(f.inventoryView) == "" {
		return errors.New("--inventory-view is required")
	}
	if strings.TrimSpace(f.out) == "" || f.out == "-" {
		return errors.New("--out is required for Actions exports")
	}
	if err := validateExportDateRange(f.from, f.to, false); err != nil {
		return err
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	client := newReportsClient(cmd.Context(), orgID)
	if len(f.subsidiaryIDs) > 0 {
		subsidiaries, discoverErr := client.Subsidiaries(cmd.Context(), orgID)
		if discoverErr != nil {
			return fmt.Errorf("resolve subsidiaries: %w", discoverErr)
		}
		f.subsidiaryIDs, err = resolveSubsidiaryRefs(f.subsidiaryIDs, subsidiaries)
		if err != nil {
			return err
		}
	}
	views, err := client.InventoryViews(cmd.Context(), orgID)
	if err != nil {
		return fmt.Errorf("list inventory views: %w", err)
	}
	view, err := resolveInventoryView(f.inventoryView, views)
	if err != nil {
		return err
	}
	f.inventoryView = view.ID
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "source=bitwave-org-report org=%s report=actions inventoryView=%s inventoryViewName=%q from=%s to=%s\n", orgID, view.ID, view.Name, f.from, f.to)

	export, err := client.StartActionsExport(cmd.Context(), orgID, view.ID, orgreports.ActionsExportInput{
		From:           f.from,
		To:             f.to,
		Inventory:      f.inventory,
		SubsidiaryIDs:  f.subsidiaryIDs,
		Actions:        f.actions,
		Statuses:       f.statuses,
		TransactionIDs: f.transactionIDs,
		Assets:         f.assets,
		AssetIDs:       f.assetIDs,
		LineErrors:     f.lineErrors,
	})
	if err != nil {
		return fmt.Errorf("run Actions report: %w", err)
	}
	ids := export.IDs()
	paths := actionOutputPaths(f.out, len(ids))
	for i, id := range ids {
		exportType := "csv"
		if export.ExportID != "" && export.FileType != "" {
			exportType = export.FileType
		}
		href, err := client.ExportDownloadURL(cmd.Context(), orgID, id, exportType)
		if err != nil {
			return fmt.Errorf("resolve Actions export %s: %w", id, err)
		}
		data, err := client.DownloadLink(cmd.Context(), href)
		if err != nil {
			return fmt.Errorf("download Actions export %s: %w", id, err)
		}
		if err := writeFileAtomic(cmd.Context(), paths[i], data); err != nil {
			return fmt.Errorf("save Actions export %s: %w", id, err)
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "saved=%s bytes=%d exportId=%s\n", paths[i], len(data), id)
	}
	return nil
}

func resolveInventoryView(ref string, views []orgreports.InventoryView) (*orgreports.InventoryView, error) {
	for i := range views {
		if views[i].ID == ref {
			return &views[i], nil
		}
	}
	var matches []int
	for i := range views {
		if strings.EqualFold(views[i].Name, ref) {
			matches = append(matches, i)
		}
	}
	if len(matches) == 1 {
		return &views[matches[0]], nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("inventory view name %q is ambiguous; pass an ID from `bitwave report inventory-views`", ref)
	}
	return nil, fmt.Errorf("inventory view %q not found; run `bitwave report inventory-views`", ref)
}

func validateExportDateRange(from, to string, allDates bool) error {
	if allDates {
		if from != "" || to != "" {
			return errors.New("--all-dates cannot be combined with --from or --to")
		}
		return nil
	}
	if from == "" || to == "" {
		return errors.New("both --from and --to are required (or use --all-dates for Transaction Export)")
	}
	fromDate, err := time.Parse("2006-01-02", from)
	if err != nil || fromDate.Format("2006-01-02") != from {
		return errors.New("--from must be a valid calendar date in YYYY-MM-DD format")
	}
	toDate, err := time.Parse("2006-01-02", to)
	if err != nil || toDate.Format("2006-01-02") != to {
		return errors.New("--to must be a valid calendar date in YYYY-MM-DD format")
	}
	if fromDate.After(toDate) {
		return errors.New("--from must be on or before --to")
	}
	return nil
}

func dateLabel(value string, allDates bool) string {
	if allDates {
		return "all"
	}
	return value
}

func actionOutputPaths(path string, count int) []string {
	if count <= 1 {
		return []string{path}
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	paths := make([]string, count)
	for i := range paths {
		paths[i] = fmt.Sprintf("%s-part-%02d%s", stem, i+1, ext)
	}
	return paths
}

func actionResultPaths(ctx context.Context, path string) []string {
	if _, err := op.RuntimeFrom(ctx).Files.Stat(path); err == nil {
		return []string{path}
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	matches, _ := op.RuntimeFrom(ctx).Files.Glob(stem + "-part-*" + ext)
	sort.Strings(matches)
	return matches
}

func writeStreamAtomic(ctx context.Context, path string, write func(io.Writer) error) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := op.RuntimeFrom(ctx).Files.CreateTemp(dir, "."+base+"-*.partial")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = op.RuntimeFrom(ctx).Files.Remove(tmpPath)
	}
	if err := write(tmp); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = op.RuntimeFrom(ctx).Files.Remove(tmpPath)
		return err
	}
	if err := op.RuntimeFrom(ctx).Files.Rename(tmpPath, path); err != nil {
		_ = op.RuntimeFrom(ctx).Files.Remove(tmpPath)
		return err
	}
	return nil
}
