package operations

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

const manualImportTemplateURL = "https://docs.google.com/spreadsheets/d/1Y-ADZtWRSEffVdubNyog2im9HzoKnFsUgI8gJFzhVco/edit#gid=945001329"

var manualImportColumns = []string{
	"id", "remoteContactId", "amount", "amountTicker", "cost", "costTicker",
	"fee", "feeTicker", "time", "blockchainId", "memo", "transactionType",
	"accountId", "contactId", "categoryId", "taxExempt", "tradeId", "description",
	"fromAddress", "toAddress", "groupId",
}

var importRowPrefix = regexp.MustCompile(`(?i)^\s*row\s+\d+\s*:\s*`)

type importWaitFlags struct {
	transactionMutationFlags
	noWait       bool
	timeout      time.Duration
	pollInterval time.Duration
}

type importFileInfo struct {
	Path        string `json:"-"`
	Name        string `json:"name"`
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"contentType"`
}

func newOrgImportsCmd() *op.Definition {
	cmd := &op.Definition{
		Use:     "import",
		Aliases: []string{"imports"},
		Short:   "Import manual transaction CSVs into the active Bitwave organization",
		Long: `Operate the same Manual Transaction Imports workflow as Bitwave's /import page.

The end-to-end command creates an import, uploads one CSV, validates every row,
and only submits it when validation passes. One --yes approves that complete
workflow without requiring separate confirmations for each lifecycle step.

This is organization product data, not the local ledger import provided by
"bitwave je import". A failed run is not atomic: some rows may already have
created transactions. Inspect rowsImported/rowsTotal and transaction data before
retrying a failed or cancelled import.`,
	}
	cmd.AddCommand(
		newImportTemplateCmd(),
		newImportsListCmd(),
		newImportStatusCmd(),
		newImportPreviewCmd(),
		newImportErrorsCmd(),
		newImportUploadCmd(),
		newImportValidateCmd(),
		newImportRunCmd(),
		newImportTransactionsCmd(),
	)
	return cmd
}

func newImportPreviewCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use:   "preview IMPORT_ID",
		Short: "Show legacy import preview rows when the organization uses the v1 workflow",
		Long: `Show pre-validation preview rows for organizations using Bitwave's legacy
manual-import engine. The current Temporal engine returns an empty list because
its validation result is the pre-commit review.`,
		Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			resolvedOrg, client, err := importClient(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			status, rows, err := client.ImportPreview(cmd.Context(), resolvedOrg, args[0])
			if err != nil {
				return fmt.Errorf("get import %s preview: %w", args[0], err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "importId": args[0], "status": status, "previewLines": rows, "temporalPreviewEmpty": len(rows) == 0})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newImportTemplateCmd() *op.Definition {
	var jsonOutput bool
	cmd := &op.Definition{
		Use:   "template",
		Short: "Show the official manual transaction CSV template",
		Args:  op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			result := map[string]any{
				"schemaVersion":    "1",
				"templateUrl":      manualImportTemplateURL,
				"fileType":         "csv",
				"supportedColumns": manualImportColumns,
				"timeFormat":       "ISO 8601 with a timezone, for example 2026-07-01T09:00:00Z",
				"nextCommand":      "bitwave import transactions ./transactions.csv --mode auto --json",
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), manualImportTemplateURL)
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit machine-readable JSON")
	return cmd
}

func newImportsListCmd() *op.Definition {
	var orgID string
	var offset, limit int
	cmd := &op.Definition{
		Use:   "list",
		Short: "List manual transaction import history",
		Args:  op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			if offset < 0 {
				return errors.New("--offset must be zero or greater")
			}
			if limit < 1 || limit > 500 {
				return errors.New("--limit must be between 1 and 500")
			}
			resolvedOrg, client, err := importClient(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			page, err := client.ImportsPage(cmd.Context(), resolvedOrg, offset, limit)
			if err != nil {
				return fmt.Errorf("list imports: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"schemaVersion": "1", "organization": resolvedOrg,
				"imports": page.Items, "offset": offset, "limit": limit, "total": page.Total,
			})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().IntVar(&offset, "offset", 0, "Zero-based import offset")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum imports to return (1-500)")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newImportStatusCmd() *op.Definition {
	var orgID string
	var watch bool
	var timeout, pollInterval time.Duration
	cmd := &op.Definition{
		Use:   "status IMPORT_ID",
		Short: "Get an import's status, progress, warnings, and failure details",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			if err := validateImportTiming(timeout, pollInterval); err != nil {
				return err
			}
			resolvedOrg, client, err := importClient(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			var record *orgreports.ImportRecord
			if watch {
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()
				record, err = waitForImport(ctx, client, resolvedOrg, args[0], pollInterval, importStable)
			} else {
				record, err = client.Import(cmd.Context(), resolvedOrg, args[0])
			}
			if err != nil {
				return fmt.Errorf("get import %s: %w", args[0], err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "import": record})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().BoolVar(&watch, "watch", false, "Poll until the import reaches a stable status")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Maximum time to watch")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", 2*time.Second, "Delay between status checks")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func newImportErrorsCmd() *op.Definition {
	var orgID, stage, format, out string
	var offset, limit int
	var all bool
	cmd := &op.Definition{
		Use:   "errors IMPORT_ID",
		Short: "List or export validation/import row errors",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			stage = strings.ToLower(strings.TrimSpace(stage))
			if stage != "validate" && stage != "run" {
				return errors.New("--stage must be validate or run")
			}
			format = strings.ToLower(strings.TrimSpace(format))
			if format != "json" && format != "csv" {
				return errors.New("--format must be json or csv")
			}
			if offset < 0 || limit < 1 || limit > 1000 {
				return errors.New("--offset must be zero or greater and --limit must be between 1 and 1000")
			}
			resolvedOrg, client, err := importClient(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			errorsList, err := loadImportErrors(cmd.Context(), client, resolvedOrg, args[0], stage, offset, limit, all || format == "csv")
			if err != nil {
				return fmt.Errorf("get %s errors for import %s: %w", stage, args[0], err)
			}
			if format == "json" {
				if out != "" && out != "-" {
					data, marshalErr := marshalIndented(map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "importId": args[0], "stage": stage, "errors": errorsList})
					if marshalErr != nil {
						return marshalErr
					}
					return writeFileAtomic(cmd.Context(), out, data)
				}
				return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "importId": args[0], "stage": stage, "errors": errorsList})
			}
			data, err := importErrorsCSV(errorsList)
			if err != nil {
				return err
			}
			if out != "" && out != "-" {
				if err := writeFileAtomic(cmd.Context(), out, data); err != nil {
					return fmt.Errorf("save error report: %w", err)
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "saved=%s rows=%d\n", out, len(errorsList))
				return nil
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&stage, "stage", "validate", "Error stage: validate or run")
	cmd.Flags().IntVar(&offset, "offset", 0, "Zero-based row-error offset")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum row errors to return (1-1000)")
	cmd.Flags().BoolVar(&all, "all", false, "Fetch every row error (up to 500,000)")
	cmd.Flags().StringVar(&format, "format", "json", "Output format: json or csv")
	cmd.Flags().StringVarP(&out, "out", "o", "", "Save output to a file instead of stdout")
	return cmd
}

func newImportUploadCmd() *op.Definition {
	var f transactionMutationFlags
	var timeout time.Duration
	cmd := &op.Definition{
		Use:   "upload FILE",
		Short: "Create an import record and upload one CSV without validating it",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runImportUploadCommand(cmd, args[0], f, timeout)
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum time to create and upload the CSV")
	return cmd
}

func newImportValidateCmd() *op.Definition {
	var f importWaitFlags
	cmd := &op.Definition{
		Use:   "validate IMPORT_ID",
		Short: "Validate an uploaded CSV and optionally wait for the result",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runImportValidateCommand(cmd, args[0], f)
		},
	}
	addImportWaitFlags(cmd, &f)
	return cmd
}

func newImportRunCmd() *op.Definition {
	var f importWaitFlags
	cmd := &op.Definition{
		Use:   "run IMPORT_ID",
		Short: "Commit a validated import and optionally wait for completion",
		Long: `Commit a validated import and create its transactions.

The operation is not atomic. If processing fails or is cancelled, some rows may
already have created transactions; inspect the returned partial counts before
retrying.`,
		Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runImportCommitCommand(cmd, args[0], f)
		},
	}
	addImportWaitFlags(cmd, &f)
	return cmd
}

func newImportTransactionsCmd() *op.Definition {
	var f importWaitFlags
	var validateOnly bool
	var mode string
	cmd := &op.Definition{
		Use:   "transactions FILE",
		Short: "Plan or run direct and staged transaction CSV workflows",
		Long: `Plan or import a user-provided CSV into the active Bitwave organization.

--mode auto inspects the file and returns a machine-readable recommendation
without changing the organization. Direct mode is fastest for small, clean,
single-wallet files whose rows map unambiguously and have stable unique IDs.
Staged mode creates an import-history record, uploads the CSV, validates every
row, exposes warnings/errors, and only runs after validation passes. It is the
safer choice for large, messy, ambiguous, mixed-wallet, or review-sensitive
files. An explicit --mode direct or --mode staged overrides the recommendation
when the selected workflow can represent the data.

This is operational workflow guidance only. The CLI does not decide accounting
treatment. Both write modes preserve backend authorization and closed-period
checks, require --yes, support --dry-run, and use caller-controlled IDs for
idempotency. --validate-only applies to staged mode.`,
		Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runImportTransactionsModeCommand(cmd, args[0], mode, f, validateOnly)
		},
	}
	addImportWaitFlags(cmd, &f, false)
	cmd.Flags().StringVar(&mode, "mode", importModeAuto, "Workflow mode: auto, direct, or staged")
	cmd.Flags().BoolVar(&validateOnly, "validate-only", false, "Validate the CSV but do not create transactions")
	return cmd
}

func runImportTransactionsModeCommand(cmd *op.Call, path, mode string, f importWaitFlags, validateOnly bool) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != importModeAuto && mode != importModeDirect && mode != importModeStaged {
		return importMutationError(cmd, "import-transactions", f.jsonOutput, errors.New("--mode must be auto, direct, or staged"))
	}
	if validateOnly && mode != importModeStaged {
		return importMutationError(cmd, "import-transactions", f.jsonOutput, errors.New("--validate-only requires --mode staged"))
	}
	// Explicit staged mode must remain usable for non-canonical files. Bitwave's
	// native validation service, rather than the direct-mode planner, owns that
	// file's row-level acceptance and review.
	if mode == importModeStaged {
		return runImportTransactionsCommand(cmd, path, f, validateOnly)
	}
	file, err := inspectImportFile(cmd.Context(), path)
	if err != nil {
		return importMutationError(cmd, "import-transactions", f.jsonOutput, err)
	}
	orgID, client, err := importClient(cmd.Context(), f.orgID)
	if err != nil {
		return importMutationError(cmd, "import-transactions", f.jsonOutput, err)
	}
	wallets, err := client.Wallets(cmd.Context(), orgID)
	if err != nil {
		return importMutationError(cmd, "import-transactions", f.jsonOutput, fmt.Errorf("discover target wallets: %w", err))
	}
	analysis, analysisErr := analyzeTransactionImport(cmd.Context(), file.Path, mode, wallets)
	if analysisErr != nil {
		return importMutationError(cmd, "plan-transaction-import", f.jsonOutput, analysisErr)
	}
	if mode == importModeAuto {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{
			SchemaVersion: "1", Status: "recommendation", Operation: "plan-transaction-import",
			Organization: orgID, Result: map[string]any{"plan": analysis.plan},
		})
	}
	if mode == importModeDirect {
		return runDirectTransactionImport(cmd, orgID, analysis, f)
	}
	return errors.New("unreachable import mode")
}

func addImportWaitFlags(cmd *op.Definition, f *importWaitFlags, allowNoWait ...bool) {
	addMutationFlags(cmd, &f.transactionMutationFlags)
	if len(allowNoWait) == 0 || allowNoWait[0] {
		cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "Return after starting the asynchronous operation")
	}
	cmd.Flags().DurationVar(&f.timeout, "timeout", 30*time.Minute, "Maximum time for the workflow")
	cmd.Flags().DurationVar(&f.pollInterval, "poll-interval", 2*time.Second, "Delay between status checks")
}

func runImportUploadCommand(cmd *op.Call, path string, f transactionMutationFlags, timeout time.Duration) error {
	operation := "upload-transaction-import"
	file, err := inspectImportFile(cmd.Context(), path)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	if timeout <= 0 {
		return importMutationError(cmd, operation, f.jsonOutput, errors.New("--timeout must be greater than zero"))
	}
	orgID, client, err := importClient(cmd.Context(), f.orgID)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	preview := importWorkflowPreview(orgID, file, false)
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return importMutationError(cmd, operation, f.jsonOutput, errors.New("refusing to create and upload an organization import without --yes (use --dry-run to preview)"))
	}
	client.HTTPClient.Timeout = timeout
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	created, err := createAndUploadImport(ctx, client, orgID, file)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	result := map[string]any{"importId": created.ID, "file": file, "status": "uploaded", "nextCommand": "bitwave import validate " + created.ID + " --yes --json"}
	return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("uploaded import %s\n", created.ID))
}

func runImportValidateCommand(cmd *op.Call, importID string, f importWaitFlags) error {
	operation := "validate-transaction-import"
	if err := validateImportTiming(f.timeout, f.pollInterval); err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	orgID, client, err := importClient(cmd.Context(), f.orgID)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	preview := map[string]any{"operation": "validateImport", "importId": importID, "asynchronous": true}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return importMutationError(cmd, operation, f.jsonOutput, errors.New("refusing to validate an organization import without --yes"))
	}
	client.HTTPClient.Timeout = f.timeout
	if err := client.ValidateImport(cmd.Context(), orgID, importID); err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, fmt.Errorf("start validation: %w", err))
	}
	if f.noWait {
		result := map[string]any{"importId": importID, "status": "validation-started", "nextCommand": "bitwave import status " + importID + " --watch --json"}
		return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("validation started for import %s\n", importID))
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
	defer cancel()
	record, err := waitForImport(ctx, client, orgID, importID, f.pollInterval, importValidationSettled)
	if err != nil {
		return importIndeterminateError(cmd, operation, f.jsonOutput, orgID, importID, "validation", err)
	}
	if !importValidationPassed(record) {
		return importValidationFailure(cmd, operation, f.jsonOutput, orgID, record)
	}
	return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: record}, fmt.Sprintf("import %s is ready to run\n", importID))
}

func runImportCommitCommand(cmd *op.Call, importID string, f importWaitFlags) error {
	operation := "run-transaction-import"
	if err := validateImportTiming(f.timeout, f.pollInterval); err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	orgID, client, err := importClient(cmd.Context(), f.orgID)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	preview := map[string]any{"operation": "runImport", "importId": importID, "createsTransactions": true, "nonAtomicOnFailure": true}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return importMutationError(cmd, operation, f.jsonOutput, errors.New("refusing to create organization transactions without --yes"))
	}
	client.HTTPClient.Timeout = f.timeout
	current, err := client.Import(cmd.Context(), orgID, importID)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, fmt.Errorf("check import before run: %w", err))
	}
	status := normalizeImportStatus(current.Status)
	if importRunDone(current) {
		return outputImportRunResult(cmd, operation, f.jsonOutput, orgID, current)
	}
	if status != "running" && status != "importing" {
		if !importValidationPassed(current) {
			return importValidationFailure(cmd, operation, f.jsonOutput, orgID, current)
		}
		if err := client.RunImport(cmd.Context(), orgID, importID); err != nil {
			return importMutationError(cmd, operation, f.jsonOutput, fmt.Errorf("start import: %w", err))
		}
	}
	if f.noWait {
		result := map[string]any{"importId": importID, "status": "run-started", "nextCommand": "bitwave import status " + importID + " --watch --json"}
		return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("import run started for %s\n", importID))
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
	defer cancel()
	record, err := waitForImport(ctx, client, orgID, importID, f.pollInterval, importRunDone)
	if err != nil {
		return importIndeterminateError(cmd, operation, f.jsonOutput, orgID, importID, "run", err)
	}
	return outputImportRunResult(cmd, operation, f.jsonOutput, orgID, record)
}

func runImportTransactionsCommand(cmd *op.Call, path string, f importWaitFlags, validateOnly bool) error {
	operation := "import-transactions"
	file, err := inspectImportFile(cmd.Context(), path)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	if err := validateImportTiming(f.timeout, f.pollInterval); err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	orgID, client, err := importClient(cmd.Context(), f.orgID)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	preview := importWorkflowPreview(orgID, file, !validateOnly)
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return importMutationError(cmd, operation, f.jsonOutput, errors.New("refusing to upload and import organization transactions without --yes (use --dry-run to preview)"))
	}
	client.HTTPClient.Timeout = f.timeout
	ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
	defer cancel()
	created, err := createAndUploadImport(ctx, client, orgID, file)
	if err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, err)
	}
	if err := client.ValidateImport(ctx, orgID, created.ID); err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, fmt.Errorf("import %s uploaded but validation could not start: %w", created.ID, err))
	}
	record, err := waitForImport(ctx, client, orgID, created.ID, f.pollInterval, importValidationSettled)
	if err != nil {
		return importIndeterminateError(cmd, operation, f.jsonOutput, orgID, created.ID, "validation", err)
	}
	if !importValidationPassed(record) {
		return importValidationFailure(cmd, operation, f.jsonOutput, orgID, record)
	}
	if validateOnly {
		result := map[string]any{"import": record, "file": file, "transactionsCreated": false, "nextCommand": "bitwave import run " + created.ID + " --yes --json"}
		return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("import %s passed validation; no transactions created\n", created.ID))
	}
	if err := client.RunImport(ctx, orgID, created.ID); err != nil {
		return importMutationError(cmd, operation, f.jsonOutput, fmt.Errorf("import %s passed validation but its run could not start: %w", created.ID, err))
	}
	record, err = waitForImport(ctx, client, orgID, created.ID, f.pollInterval, importRunDone)
	if err != nil {
		return importIndeterminateError(cmd, operation, f.jsonOutput, orgID, created.ID, "run", err)
	}
	return outputImportRunResult(cmd, operation, f.jsonOutput, orgID, record)
}

func importClient(ctx context.Context, orgID string) (string, *orgreports.Client, error) {
	resolvedOrg, err := resolveReportOrg(ctx, orgID)
	if err != nil {
		return "", nil, err
	}
	return resolvedOrg, newReportsClient(ctx, resolvedOrg), nil
}

func inspectImportFile(ctx context.Context, path string) (importFileInfo, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || !strings.EqualFold(filepath.Ext(path), ".csv") {
		return importFileInfo{}, errors.New("manual transaction imports require one .csv file")
	}
	info, err := op.RuntimeFrom(ctx).Files.Stat(path)
	if err != nil {
		return importFileInfo{}, fmt.Errorf("inspect CSV: %w", err)
	}
	if !info.Mode().IsRegular() {
		return importFileInfo{}, errors.New("import path must be a regular CSV file")
	}
	if info.Size() == 0 {
		return importFileInfo{}, errors.New("import CSV is empty")
	}
	abs, err := op.RuntimeFrom(ctx).Files.Path(path)
	if err != nil {
		abs = path
	}
	return importFileInfo{Path: abs, Name: info.Name(), Bytes: info.Size(), ContentType: "text/csv"}, nil
}

func createAndUploadImport(ctx context.Context, client *orgreports.Client, orgID string, file importFileInfo) (*orgreports.CreateImportResult, error) {
	input, err := op.RuntimeFrom(ctx).Files.Open(file.Path)
	if err != nil {
		return nil, fmt.Errorf("open import CSV: %w", err)
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect import CSV: %w", err)
	}
	created, err := client.CreateImport(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("create import: %w", err)
	}
	if err := client.UploadImportReader(ctx, created.FileUploadURL, input, info.Size()); err != nil {
		return nil, fmt.Errorf("import %s was created but its CSV upload failed: %w", created.ID, err)
	}
	return created, nil
}

func importWorkflowPreview(orgID string, file importFileInfo, createsTransactions bool) map[string]any {
	steps := []map[string]any{
		{"step": "create", "operation": "createImport", "typed": false},
		{"step": "upload", "method": "PUT", "authentication": "signed-url-only", "file": file},
		{"step": "validate", "operation": "validateImport", "blocksRunOnRowErrors": true},
	}
	if createsTransactions {
		steps = append(steps, map[string]any{"step": "run", "operation": "runImport", "createsTransactions": true, "nonAtomicOnFailure": true})
	}
	return map[string]any{"organization": orgID, "workflow": "manual-transaction-import", "steps": steps}
}

func validateImportTiming(timeout, pollInterval time.Duration) error {
	if timeout <= 0 {
		return errors.New("--timeout must be greater than zero")
	}
	if pollInterval <= 0 {
		return errors.New("--poll-interval must be greater than zero")
	}
	return nil
}

func waitForImport(ctx context.Context, client *orgreports.Client, orgID, importID string, interval time.Duration, done func(*orgreports.ImportRecord) bool) (*orgreports.ImportRecord, error) {
	consecutiveFailures := 0
	for {
		record, err := client.Import(ctx, orgID, importID)
		if err != nil {
			if !retryableImportPollError(err) || consecutiveFailures >= 4 {
				return nil, err
			}
			consecutiveFailures++
			if err := waitImportPoll(ctx, interval*time.Duration(consecutiveFailures)); err != nil {
				return nil, err
			}
			continue
		}
		consecutiveFailures = 0
		if done(record) {
			return record, nil
		}
		if err := waitImportPoll(ctx, interval); err != nil {
			return nil, fmt.Errorf("waiting for import %s: %w", importID, err)
		}
	}
}

func waitImportPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func retryableImportPollError(err error) bool {
	var apiError *apierr.Error
	if errors.As(err, &apiError) {
		return apiError.Status == http.StatusTooManyRequests || apiError.Status >= 500
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func normalizeImportStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func importStable(record *orgreports.ImportRecord) bool {
	switch normalizeImportStatus(record.Status) {
	case "", "new", "pending", "queued", "preview-ready", "preview-generating", "validating", "running", "importing":
		return false
	default:
		return true
	}
}

func importValidationSettled(record *orgreports.ImportRecord) bool {
	switch normalizeImportStatus(record.Status) {
	case "", "new", "pending", "queued", "preview-ready", "preview-generating", "validating":
		return false
	default:
		return true
	}
}

func importValidationPassed(record *orgreports.ImportRecord) bool {
	if record == nil || (record.RowErrorCount != nil && record.RowErrorCount.Validate > 0) {
		return false
	}
	switch normalizeImportStatus(record.Status) {
	case "ready-to-import", "running", "importing", "completed", "completed-with-warnings", "completed-with-errors", "imported", "success":
		return true
	default:
		return false
	}
}

func importRunDone(record *orgreports.ImportRecord) bool {
	switch normalizeImportStatus(record.Status) {
	case "completed", "completed-with-warnings", "completed-with-errors", "imported", "success", "failed", "cancelled", "error":
		return true
	default:
		return false
	}
}

func outputImportRunResult(cmd *op.Call, operation string, jsonOutput bool, orgID string, record *orgreports.ImportRecord) error {
	status := normalizeImportStatus(record.Status)
	if status == "failed" || status == "cancelled" || status == "error" {
		message := "import failed; some transactions may already have been created"
		if record.RowsImported != nil && record.RowsTotal != nil {
			message = fmt.Sprintf("import stopped after creating %d of %d rows; review imported transactions before retrying", *record.RowsImported, *record.RowsTotal)
		}
		err := errors.New(message)
		if jsonOutput {
			_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "error", Operation: operation, Organization: orgID, Result: record, Error: &reportError{Code: "import_failed", Message: message, Retryable: false, Suggestion: "Review run-stage errors and imported transactions before creating a corrected import."}})
		}
		return err
	}
	envelopeStatus := "success"
	nextCommand := ""
	if status == "completed-with-errors" {
		envelopeStatus = "partial_success"
		nextCommand = "bitwave import errors " + record.ID + " --stage run --format csv --out import-errors.csv"
	} else if status == "completed-with-warnings" {
		envelopeStatus = "success_with_warnings"
	}
	result := map[string]any{"import": record, "nextCommand": nextCommand}
	return outputMutation(cmd, jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: envelopeStatus, Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("import %s completed with status %s\n", record.ID, record.Status))
}

func importIndeterminateError(cmd *op.Call, operation string, jsonOutput bool, orgID, importID, phase string, cause error) error {
	nextCommand := "bitwave import status " + importID + " --watch --json"
	message := fmt.Sprintf("import %s %s was started but its current status could not be confirmed; do not create a new import or retry the CSV until status is checked", importID, phase)
	if jsonOutput {
		_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{
			SchemaVersion: "1", Status: "indeterminate", Operation: operation, Organization: orgID,
			Result: map[string]any{"importId": importID, "phase": phase, "nextCommand": nextCommand, "doNotRetry": true},
			Error:  &reportError{Code: "import_status_unknown", Message: message + ": " + cause.Error(), Retryable: true, Suggestion: nextCommand},
		})
	}
	return errors.New(message)
}

func importValidationFailure(cmd *op.Call, operation string, jsonOutput bool, orgID string, record *orgreports.ImportRecord) error {
	count := 0
	if record != nil && record.RowErrorCount != nil {
		count = record.RowErrorCount.Validate
	}
	message := "import did not pass validation; no transactions were created"
	if count > 0 {
		message = fmt.Sprintf("import has %d validation row error(s); no transactions were created", count)
	}
	if jsonOutput {
		_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "error", Operation: operation, Organization: orgID, Result: record, Error: &reportError{Code: "validation_failed", Message: message, Retryable: false, Suggestion: "Run `bitwave import errors " + record.ID + " --stage validate --format csv --out import-errors.csv`, correct the source CSV, and create a new import."}})
	}
	return errors.New(message)
}

func importMutationError(cmd *op.Call, operation string, jsonOutput bool, err error) error {
	if jsonOutput {
		explicitOrg, _ := cmd.Flags().GetString("org")
		orgID, _ := resolveReportOrg(cmd.Context(), explicitOrg)
		detail := &reportError{Code: "invalid_request", Message: err.Error(), Retryable: false, Suggestion: "Run `bitwave import --help` and use --dry-run to preview the workflow."}
		var apiError *apierr.Error
		if errors.As(err, &apiError) {
			detail.Code = "api_error"
			detail.HTTPStatus = apiError.Status
			detail.Retryable = apiError.Status == http.StatusTooManyRequests || apiError.Status >= 500
		}
		if strings.Contains(err.Error(), "without --yes") {
			detail.Code = "confirmation_required"
		}
		_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "error", Operation: operation, Organization: orgID, Error: detail})
	}
	return err
}

func loadImportErrors(ctx context.Context, client *orgreports.Client, orgID, importID, stage string, offset, limit int, all bool) ([]orgreports.ImportRowError, error) {
	if !all {
		return client.ImportRowErrors(ctx, orgID, importID, stage, offset, limit)
	}
	const pageSize = 1000
	const maxPages = 500
	result := make([]orgreports.ImportRowError, 0)
	for page := 0; page < maxPages; page++ {
		rows, err := client.ImportRowErrors(ctx, orgID, importID, stage, page*pageSize, pageSize)
		if err != nil {
			return nil, err
		}
		result = append(result, rows...)
		if len(rows) < pageSize {
			return result, nil
		}
	}
	return result, errors.New("row-error export reached the 500,000-row safety limit")
}

func importErrorsCSV(rows []orgreports.ImportRowError) ([]byte, error) {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write([]string{"Row", "Errors"}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		rowNumber := 0
		if row.RowIndex != nil {
			rowNumber = *row.RowIndex + 2
		}
		messages := make([]string, 0, len(row.Errors))
		for _, message := range row.Errors {
			messages = append(messages, importRowPrefix.ReplaceAllString(strings.TrimSpace(message), ""))
		}
		if err := writer.Write([]string{strconv.Itoa(rowNumber), spreadsheetSafeCell(strings.Join(messages, "; "))}); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func spreadsheetSafeCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func marshalIndented(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := writeJSON(&buffer, value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
