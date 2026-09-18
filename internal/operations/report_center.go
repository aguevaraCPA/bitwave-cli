package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type reportCenterEntry struct {
	Name    string `json:"name"`
	Family  string `json:"family"`
	RunType string `json:"runType,omitempty"`
}

var reportCenterCatalog = []reportCenterEntry{
	{Name: "balance-report", Family: "legacy", RunType: "BalanceReport"},
	{Name: "expanded-balance-report", Family: "legacy", RunType: "BalanceReport"},
	{Name: "expanded-gl-summary-report", Family: "legacy", RunType: "GLSummaryBigqueryReport"},
	{Name: "expanded-gl-detailed-report", Family: "legacy", RunType: "GLDetailedBigqueryReport"},
	{Name: "expanded-val-rollforward-report", Family: "legacy", RunType: "ValuationRollforwardBigqueryReport"},
	{Name: "expanded-pricing-summary-report", Family: "legacy", RunType: "PriceExceptionBigqueryReport"},
	{Name: "transaction-actions-comparison-report", Family: "legacy", RunType: "TxnActionCompareBigqueryReport"},
	{Name: "fusion-custom-je-import-report", Family: "legacy", RunType: "FusionCustomJeImportBigqueryReport"},
	{Name: "xrp-custom-fusion-je-report", Family: "legacy", RunType: "XRPCustomFusionJEBigqueryReport"},
	{Name: "xrp-custom-fusion-ecb-report", Family: "legacy", RunType: "XRPCustomFusionECBBigqueryReport"},
	{Name: "xrp-custom-fusion-je-elimination-report", Family: "legacy", RunType: "XRPCustomFusionJeEliminationBigqueryReport"},
	{Name: "xrp-custom-net-change-bigquery-report", Family: "legacy", RunType: "XRPCustomNetChangeBigqueryReport"},
	{Name: "xrp-custom-purchased-xrp-balances-report", Family: "legacy", RunType: "XRPCustomPurchasedXRPBalancesReport"},
	{Name: "expanded-balance-recon-report", Family: "legacy", RunType: "BalanceReconBigqueryReport"},
	{Name: "transactions-export", Family: "export"},
	{Name: "transactions-export-lrt", Family: "legacy", RunType: "TxnExportBigqueryReport"},
	{Name: "transactions-export-datawarehouse", Family: "export"},
	{Name: "deleted-ignored-transactions-export", Family: "export"},
	{Name: "journal-report", Family: "legacy", RunType: "JournalReport"},
	{Name: "expanded-journal-report", Family: "legacy", RunType: "JournalEntrySaveDownReport"},
	{Name: "rolled-up-journal-report", Family: "legacy", RunType: "RolledUpJournalReport"},
	{Name: "ledger-report", Family: "legacy", RunType: "LedgerReport"},
	{Name: "balance-check-report", Family: "inventory"},
	{Name: "cost-basis-roll-forward", Family: "inventory"},
	{Name: "cost-basis-roll-forward-expanded", Family: "inventory"},
	{Name: "actions", Family: "inventory"},
	{Name: "actions-summary-report", Family: "inventory"},
	{Name: "reclass", Family: "inventory"},
	{Name: "actions-je-report", Family: "inventory"},
	{Name: "actions-tb-report", Family: "inventory"},
	{Name: "trial-balance-report", Family: "report-run", RunType: "trial-balance-report"},
	{Name: "sage-erp-je-import", Family: "inventory"},
	{Name: "expanded-actions-report", Family: "inventory"},
	{Name: "expanded-cost-basis-report", Family: "inventory"},
	{Name: "rolled-up-actions-je-sync", Family: "ui-placeholder"},
	{Name: "rolled-up-actions-je-report", Family: "rolled-up-je"},
}

var inventoryReportPaths = map[string]string{
	"actions":                        "/orgs/{org}/inventory-views/{view}/actions",
	"actions-summary-report":         "/orgs/{org}/inventory-views/{view}/reports/actions-summary-report",
	"actions-je-report":              "/orgs/{org}/inventory-views/{view}/reports/actions-je-report",
	"actions-tb-report":              "/orgs/{org}/inventory-views/{view}/reports/actions-tb-report",
	"reclass":                        "/orgs/{org}/inventory-views/{view}/reports/je-reclass",
	"sage-erp-je-import":             "/orgs/{org}/inventory-views/{view}/reports/sage-erp-je-report",
	"expanded-actions-report":        "/orgs/{org}/inventory-views/{view}/reports/advanced-action-report",
	"cost-basis-roll-forward":        "/orgs/{org}/inventory-views/{view}/reports/cost-basis-roll-forward",
	"cost-basis-roll-forward-v3":     "/orgs/{org}/inventory-views/{view}/reports/advanced-cost-basis-roll-forward",
	"expanded-cost-basis-report":     "/orgs/{org}/inventory-views/{view}/reports/ripple-cost-basis-roll-forward",
	"balance-check-report":           "/orgs/{org}/inventory-views/reports/balance-check-report",
	"balance-check-report-paginated": "/orgs/{org}/inventory-views/reports/balance-check-report-paginated",
}

func newReportCenterCmd() *op.Definition {
	cmd := &op.Definition{Use: "center", Short: "Access every Reports Center endpoint"}
	cmd.AddCommand(newReportCenterCatalogCmd())
	cmd.AddCommand(newReportCenterFilterOptionsCmd())
	cmd.AddCommand(newReportCenterRunCmd())
	cmd.AddCommand(newReportCenterRunsCmd())
	cmd.AddCommand(newReportCenterRunReadCmd("status"))
	cmd.AddCommand(newReportCenterRunReadCmd("get"))
	cmd.AddCommand(newReportCenterDownloadCmd())
	cmd.AddCommand(newReportCenterCancelCmd())
	cmd.AddCommand(newReportCenterExpandCmd())
	cmd.AddCommand(newReportCenterLegacyCmd())
	cmd.AddCommand(newReportCenterInventoryCmd())
	cmd.AddCommand(newReportCenterExportCmd())
	cmd.AddCommand(newReportCenterRolledUpJECmd())
	return cmd
}

func newReportCenterCatalogCmd() *op.Definition {
	var jsonOutput bool
	cmd := &op.Definition{
		Use: "catalog", Short: "List reports exposed by the Reports Center",
		RunE: func(cmd *op.Call, _ []string) error {
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"reports": reportCenterCatalog, "inventoryEndpoints": inventoryReportPaths})
			}
			for _, report := range reportCenterCatalog {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-45s %s\n", report.Name, report.Family)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON")
	return cmd
}

func reportCenterClient(ctx context.Context, orgID string) *orgreports.Client {
	return newReportsClient(ctx, orgID)
}

func newReportCenterFilterOptionsCmd() *op.Definition {
	var orgID, out string
	cmd := &op.Definition{
		Use: "filter-options REPORT_TYPE", Short: "Get a report type's available filters", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			path := "/v2/orgs/" + url.PathEscape(org) + "/report-types/" + url.PathEscape(args[0]) + "/filter-options"
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
			if err != nil {
				return fmt.Errorf("get report filter options: %w", err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	addReportCenterReadFlags(cmd, &orgID, &out)
	return cmd
}

func newReportCenterRunCmd() *op.Definition {
	var orgID, out string
	var inputs []string
	cmd := &op.Definition{
		Use: "run REPORT_TYPE", Short: "Start any report-run report", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			parsed, err := parseReportInputs(inputs)
			if err != nil {
				return err
			}
			body := map[string]any{"reportType": args[0], "inputs": parsed}
			path := "/v2/orgs/" + url.PathEscape(org) + "/report-runs"
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodPost, path, body)
			if err != nil {
				return fmt.Errorf("start report: %w", err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().StringArrayVar(&inputs, "input", nil, "Report input as key=value (repeatable)")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Save response to a file")
	return cmd
}

func parseReportInputs(values []string) ([]map[string]string, error) {
	result := make([]map[string]string, 0, len(values))
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("invalid --input %q; expected key=value", value)
		}
		result = append(result, map[string]string{"key": strings.TrimSpace(key), "value": item})
	}
	return result, nil
}

func newReportCenterRunsCmd() *op.Definition {
	var orgID, out string
	var query []string
	cmd := &op.Definition{
		Use: "runs", Short: "List report runs", Args: op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			path, err := appendAPIQuery("/v2/orgs/"+url.PathEscape(org)+"/report-runs", query)
			if err != nil {
				return err
			}
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
			if err != nil {
				return fmt.Errorf("list report runs: %w", err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	addReportCenterQueryFlags(cmd, &orgID, &out, &query)
	return cmd
}

func newReportCenterRunReadCmd(kind string) *op.Definition {
	var orgID, out string
	cmd := &op.Definition{
		Use: kind + " RUN_ID", Short: map[string]string{"status": "Get report-run status", "get": "Get report-run results"}[kind], Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			path := "/v2/orgs/" + url.PathEscape(org) + "/report-runs/" + url.PathEscape(args[0])
			if kind == "status" {
				path += "/status"
			}
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
			if err != nil {
				return fmt.Errorf("get report %s: %w", kind, err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	addReportCenterReadFlags(cmd, &orgID, &out)
	return cmd
}

func newReportCenterDownloadCmd() *op.Definition {
	var orgID, out string
	cmd := &op.Definition{
		Use: "download RUN_ID", Short: "Download a report-run result", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			path := "/v2/orgs/" + url.PathEscape(org) + "/report-runs/" + url.PathEscape(args[0]) + "/download"
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
			if err != nil {
				return fmt.Errorf("download report: %w", err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	addReportCenterReadFlags(cmd, &orgID, &out)
	return cmd
}

func newReportCenterCancelCmd() *op.Definition {
	var orgID, out string
	var yes bool
	cmd := &op.Definition{
		Use: "cancel RUN_ID", Short: "Cancel a running report", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			if !yes {
				return errors.New("refusing to cancel a report without --yes")
			}
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			path := "/v2/orgs/" + url.PathEscape(org) + "/report-runs/" + url.PathEscape(args[0]) + "/cancel"
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodPost, path, map[string]any{})
			if err != nil {
				return fmt.Errorf("cancel report: %w", err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	addReportCenterReadFlags(cmd, &orgID, &out)
	cmd.Flags().BoolVar(&yes, "yes", false, "Confirm cancellation")
	return cmd
}

func newReportCenterExpandCmd() *op.Definition {
	var orgID, out, token, rowPath string
	cmd := &op.Definition{
		Use: "expand RUN_ID", Short: "Expand a paginated or nested report row", Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			if token == "" {
				return errors.New("--token is required")
			}
			org, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			var pathValue any = []int{}
			if strings.TrimSpace(rowPath) != "" {
				if err := json.Unmarshal([]byte(rowPath), &pathValue); err != nil {
					return fmt.Errorf("--row-path must be a JSON array: %w", err)
				}
			}
			path := "/v2/orgs/" + url.PathEscape(org) + "/report-runs/" + url.PathEscape(args[0]) + "/expand"
			data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodPost, path, map[string]any{"token": token, "rowPath": pathValue})
			if err != nil {
				return fmt.Errorf("expand report: %w", err)
			}
			return writeAPIResponse(cmd, out, data)
		},
	}
	addReportCenterReadFlags(cmd, &orgID, &out)
	cmd.Flags().StringVar(&token, "token", "", "Expansion token")
	cmd.Flags().StringVar(&rowPath, "row-path", "[]", "Row path as a JSON array")
	return cmd
}

func newReportCenterLegacyCmd() *op.Definition {
	cmd := &op.Definition{Use: "legacy", Short: "Access legacy asynchronous report endpoints"}
	var listOrg, listOut, reportType string
	list := &op.Definition{Use: "list", Short: "List legacy report runs", RunE: func(cmd *op.Call, _ []string) error {
		org, err := resolveReportOrg(cmd.Context(), listOrg)
		if err != nil {
			return err
		}
		path := "/v2/orgs/" + url.PathEscape(org) + "/reports/"
		if reportType != "" {
			path += "?type=" + url.QueryEscape(reportType)
		}
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		return writeAPIResponse(cmd, listOut, data)
	}}
	addReportCenterReadFlags(list, &listOrg, &listOut)
	list.Flags().StringVar(&reportType, "type", "", "Legacy report wire type")

	var runOrg, runOut string
	var query []string
	run := &op.Definition{Use: "run TYPE", Short: "Start any legacy Reports Center report", Args: op.ExactArgs(1), RunE: func(cmd *op.Call, args []string) error {
		org, err := resolveReportOrg(cmd.Context(), runOrg)
		if err != nil {
			return err
		}
		values := append([]string{"type=" + args[0], "orgId=" + org}, query...)
		path, err := appendAPIQuery("/reports/view", values)
		if err != nil {
			return err
		}
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		return writeAPIResponse(cmd, runOut, data)
	}}
	addReportCenterQueryFlags(run, &runOrg, &runOut, &query)

	var getOrg, getOut string
	var includeDownloads bool
	get := &op.Definition{Use: "get RUN_ID", Short: "Get a legacy report run", Args: op.ExactArgs(1), RunE: func(cmd *op.Call, args []string) error {
		org, err := resolveReportOrg(cmd.Context(), getOrg)
		if err != nil {
			return err
		}
		path := "/v2/orgs/" + url.PathEscape(org) + "/reports/" + url.PathEscape(args[0])
		if includeDownloads {
			path += "?includeDownloadUrls=true"
		}
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		return writeAPIResponse(cmd, getOut, data)
	}}
	addReportCenterReadFlags(get, &getOrg, &getOut)
	get.Flags().BoolVar(&includeDownloads, "include-download-urls", false, "Include result download links")
	cmd.AddCommand(list, run, get)
	return cmd
}

func newReportCenterInventoryCmd() *op.Definition {
	cmd := &op.Definition{Use: "inventory", Short: "Run Reports Center inventory-view reports"}
	var jsonOutput bool
	list := &op.Definition{Use: "list", Short: "List inventory report endpoints", RunE: func(cmd *op.Call, _ []string) error {
		if jsonOutput {
			return writeJSON(cmd.OutOrStdout(), inventoryReportPaths)
		}
		for name, path := range inventoryReportPaths {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%-38s %s\n", name, path)
		}
		return nil
	}}
	list.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON")
	var orgID, out, view string
	var query []string
	run := &op.Definition{Use: "run REPORT", Short: "Run an inventory-view report endpoint", Args: op.ExactArgs(1), RunE: func(cmd *op.Call, args []string) error {
		template, ok := inventoryReportPaths[args[0]]
		if !ok {
			return fmt.Errorf("unknown inventory report %q; run `bitwave report center inventory list`", args[0])
		}
		if strings.Contains(template, "{view}") && strings.TrimSpace(view) == "" {
			return errors.New("--inventory-view is required for this report")
		}
		org, err := resolveReportOrg(cmd.Context(), orgID)
		if err != nil {
			return err
		}
		path := strings.ReplaceAll(template, "{org}", url.PathEscape(org))
		path = strings.ReplaceAll(path, "{view}", url.PathEscape(view))
		path, err = appendAPIQuery(path, query)
		if err != nil {
			return err
		}
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return fmt.Errorf("run inventory report: %w", err)
		}
		return writeAPIResponse(cmd, out, data)
	}}
	addReportCenterQueryFlags(run, &orgID, &out, &query)
	run.Flags().StringVar(&view, "inventory-view", "", "Inventory view ID")
	cmd.AddCommand(list, run)
	return cmd
}

func newReportCenterExportCmd() *op.Definition {
	cmd := &op.Definition{Use: "export", Short: "Start and retrieve Reports Center exports"}
	cmd.AddCommand(newReportCenterExportStartCmd())
	cmd.AddCommand(newReportCenterSavedExportsCmd())

	var orgID, out string
	var rawURL bool
	get := &op.Definition{Use: "get EXPORT_ID", Short: "Get an export by ID", Args: op.ExactArgs(1), RunE: func(cmd *op.Call, args []string) error {
		org, err := resolveReportOrg(cmd.Context(), orgID)
		if err != nil {
			return err
		}
		path := "/v2/orgs/" + url.PathEscape(org) + "/exports/" + url.PathEscape(args[0])
		if rawURL {
			path += "?rawUrl=true"
		}
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		return writeAPIResponse(cmd, out, data)
	}}
	addReportCenterReadFlags(get, &orgID, &out)
	get.Flags().BoolVar(&rawURL, "raw-url", false, "Return the raw download URL")
	cmd.AddCommand(get)
	return cmd
}

func newReportCenterExportStartCmd() *op.Definition {
	var orgID, out, kind, input string
	var query []string
	cmd := &op.Definition{Use: "start", Short: "Start a transaction export", RunE: func(cmd *op.Call, _ []string) error {
		org, err := resolveReportOrg(cmd.Context(), orgID)
		if err != nil {
			return err
		}
		var path string
		switch kind {
		case "transactions":
			path = "/v2/export/transactions"
		case "deleted-ignored":
			path = "/v2/orgs/" + url.PathEscape(org) + "/deleted-ignored-transactions-export"
		case "saved":
			path = "/v2/orgs/" + url.PathEscape(org) + "/transactions:export"
		default:
			return fmt.Errorf("unknown export kind %q (use transactions, deleted-ignored, or saved)", kind)
		}
		path, err = appendAPIQuery(path, query)
		if err != nil {
			return err
		}
		var body any
		if input != "" {
			if !json.Valid([]byte(input)) {
				return errors.New("--data must be valid JSON")
			}
			body = json.RawMessage(input)
		}
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodPost, path, body)
		if err != nil {
			return fmt.Errorf("start transaction export: %w", err)
		}
		return writeAPIResponse(cmd, out, data)
	}}
	addReportCenterQueryFlags(cmd, &orgID, &out, &query)
	cmd.Flags().StringVar(&kind, "kind", "transactions", "Export kind: transactions, deleted-ignored, or saved")
	cmd.Flags().StringVar(&input, "data", "", "Optional inline JSON request body")
	return cmd
}

func newReportCenterSavedExportsCmd() *op.Definition {
	cmd := &op.Definition{Use: "saved", Short: "Access saved transaction exports"}
	var listOrg, listOut string
	list := &op.Definition{Use: "list", Short: "List saved transaction exports", RunE: func(cmd *op.Call, _ []string) error {
		org, err := resolveReportOrg(cmd.Context(), listOrg)
		if err != nil {
			return err
		}
		path := "/v2/orgs/" + url.PathEscape(org) + "/transactions:export"
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		return writeAPIResponse(cmd, listOut, data)
	}}
	addReportCenterReadFlags(list, &listOrg, &listOut)

	var getOrg, getOut string
	get := &op.Definition{Use: "get EXPORT_ID", Short: "Get a saved transaction export", Args: op.ExactArgs(1), RunE: func(cmd *op.Call, args []string) error {
		org, err := resolveReportOrg(cmd.Context(), getOrg)
		if err != nil {
			return err
		}
		path := "/v2/orgs/" + url.PathEscape(org) + "/transactions:export/" + url.PathEscape(args[0])
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		return writeAPIResponse(cmd, getOut, data)
	}}
	addReportCenterReadFlags(get, &getOrg, &getOut)
	cmd.AddCommand(list, get)
	return cmd
}

func newReportCenterRolledUpJECmd() *op.Definition {
	var orgID, out, runID, periodStart, periodEnd, configID, connectionID string
	cmd := &op.Definition{Use: "rolled-up-je", Short: "Generate the Rolled Up Actions Journal Entry report"}
	generate := &op.Definition{Use: "generate", Short: "Generate a rolled-up actions JE report", RunE: func(cmd *op.Call, _ []string) error {
		for name, value := range map[string]string{
			"--run-id": runID, "--period-start": periodStart, "--period-end": periodEnd,
			"--rollup-config": configID, "--connection": connectionID,
		} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s is required", name)
			}
		}
		org, err := resolveReportOrg(cmd.Context(), orgID)
		if err != nil {
			return err
		}
		body := map[string]string{
			"runId": runID, "periodStart": periodStart, "periodEnd": periodEnd,
			"rollupConfigId": configID, "connectionId": connectionID,
		}
		path := "/v2/orgs/" + url.PathEscape(org) + "/reports/rolledUpJe"
		data, err := reportCenterClient(cmd.Context(), org).RawRequest(cmd.Context(), orgreports.APIServiceCore, http.MethodPost, path, body)
		if err != nil {
			return fmt.Errorf("generate rolled-up actions JE report: %w", err)
		}
		return writeAPIResponse(cmd, out, data)
	}}
	addReportCenterReadFlags(generate, &orgID, &out)
	generate.Flags().StringVar(&runID, "run-id", "", "Inventory run ID")
	generate.Flags().StringVar(&periodStart, "period-start", "", "Period start date")
	generate.Flags().StringVar(&periodEnd, "period-end", "", "Period end date")
	generate.Flags().StringVar(&configID, "rollup-config", "", "Rollup JE configuration ID")
	generate.Flags().StringVar(&connectionID, "connection", "", "Accounting connection ID")
	cmd.AddCommand(generate)
	return cmd
}

func addReportCenterReadFlags(cmd *op.Definition, orgID, out *string) {
	cmd.Flags().StringVar(orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVarP(out, "out", "o", "", "Save response to a file")
}

func addReportCenterQueryFlags(cmd *op.Definition, orgID, out *string, query *[]string) {
	addReportCenterReadFlags(cmd, orgID, out)
	cmd.Flags().StringArrayVar(query, "query", nil, "Query parameter as key=value (repeatable)")
}
