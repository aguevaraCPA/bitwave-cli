package operations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/apierr"
	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type transactionMutationFlags struct {
	orgID        string
	yes          bool
	dryRun       bool
	jsonOutput   bool
	bulkActionID string
	noWait       bool
	timeout      time.Duration
}

type mutationEnvelope struct {
	SchemaVersion string       `json:"schemaVersion"`
	Status        string       `json:"status"`
	Operation     string       `json:"operation"`
	Organization  string       `json:"organization"`
	DryRun        bool         `json:"dryRun,omitempty"`
	Request       any          `json:"request,omitempty"`
	Result        any          `json:"result,omitempty"`
	Error         *reportError `json:"error,omitempty"`
}

func newOrgTransactionsCmd() *op.Definition {
	cmd := &op.Definition{
		Use:     "transaction",
		Aliases: []string{"transactions", "txn"},
		Short:   "Read or change transactions in the active Bitwave organization",
		Long: `Change product transactions in a Bitwave organization.

These commands mutate the organization and are separate from local ledger
entries. Every write requires --yes. Use --dry-run to inspect the exact HTTP
method, path, and JSON body without sending a mutation.`,
	}
	cmd.AddCommand(newTransactionStateCmd("ignore", orgreports.TransactionStateIgnore))
	cmd.AddCommand(newTransactionStateCmd("unignore", orgreports.TransactionStateUnignore))
	cmd.AddCommand(newGetOrgTransactionCmd())
	cmd.AddCommand(newSearchOrgTransactionsCmd())
	cmd.AddCommand(newCountOrgTransactionsCmd())
	cmd.AddCommand(newTransactionReviewCmd())
	cmd.AddCommand(newTransactionSummaryCmd())
	cmd.AddCommand(newCreateOrgTransactionCmd())
	cmd.AddCommand(newCategorizeTransactionCmd())
	cmd.AddCommand(newBulkCategorizeTransactionsCmd())
	cmd.AddCommand(newCategorizationOptionsCmd())
	return cmd
}

func newTransactionSummaryCmd() *op.Definition {
	cmd := &op.Definition{
		Use:   "summary",
		Short: "Read the wallet, counterparty, and asset tables from Transaction Summary",
		Long: `Read the same aggregate datasets used by Bitwave's Transaction Summary dashboard.

These bounded endpoints are useful before exporting individual transactions.
Interacting addresses are returned in full and are never shortened by the CLI.`,
	}
	cmd.AddCommand(newTransactionSummaryWalletsCmd(), newTransactionSummaryAddressesCmd(), newTransactionSummaryAssetsCmd())
	return cmd
}

func newTransactionSummaryWalletsCmd() *op.Definition {
	var orgID, from, to string
	var page, limit int
	cmd := &op.Definition{
		Use: "wallets", Short: "List wallet-level transaction totals", Args: op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			items, err := client.TransactionSummaryWallets(cmd.Context(), resolvedOrg, strings.TrimSpace(from), strings.TrimSpace(to), page, limit)
			if err != nil {
				return fmt.Errorf("list transaction-summary wallets: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "wallets": items, "page": page, "limit": limit})
		},
	}
	addTransactionSummaryFlags(cmd, &orgID, &from, &to, &page, &limit)
	return cmd
}

func newTransactionSummaryAddressesCmd() *op.Definition {
	var orgID, walletID, from, to, sortField string
	var page, limit int
	cmd := &op.Definition{
		Use: "addresses", Short: "List interacting-address transaction totals", Args: op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			items, err := client.TransactionSummaryAddresses(cmd.Context(), resolvedOrg, strings.TrimSpace(walletID), strings.TrimSpace(from), strings.TrimSpace(to), strings.TrimSpace(sortField), page, limit)
			if err != nil {
				return fmt.Errorf("list transaction-summary addresses: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "addresses": items, "page": page, "limit": limit})
		},
	}
	addTransactionSummaryFlags(cmd, &orgID, &from, &to, &page, &limit)
	cmd.Flags().StringVar(&walletID, "wallet", "", "Exact wallet ID")
	cmd.Flags().StringVar(&sortField, "sort", "", "Dashboard field to sort descending")
	return cmd
}

func newTransactionSummaryAssetsCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use: "assets", Short: "List assets available in Transaction Summary", Args: op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			items, err := client.TransactionSummaryAssets(cmd.Context(), resolvedOrg)
			if err != nil {
				return fmt.Errorf("list transaction-summary assets: %w", err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "assets": items})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func addTransactionSummaryFlags(cmd *op.Definition, orgID, from, to *string, page, limit *int) {
	cmd.Flags().StringVar(orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(from, "from", "", "Inclusive dashboard start date")
	cmd.Flags().StringVar(to, "to", "", "Inclusive dashboard end date")
	cmd.Flags().IntVar(page, "page", 1, "Dashboard page number")
	cmd.Flags().IntVar(limit, "limit", 100, "Records per page (1-500)")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
}

func newGetOrgTransactionCmd() *op.Definition {
	var orgID, network string
	var accountingDetails bool
	cmd := &op.Definition{
		Use:   "get TRANSACTION_ID",
		Short: "Get one complete organization transaction for categorization planning",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			transactionID, err := normalizeTransactionID(args[0], network)
			if err != nil {
				return err
			}
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			var transaction json.RawMessage
			if accountingDetails {
				transaction, err = client.TransactionWithAccountingDetails(cmd.Context(), resolvedOrg, transactionID)
			} else {
				transaction, err = client.Transaction(cmd.Context(), resolvedOrg, transactionID)
			}
			if err != nil {
				return fmt.Errorf("get transaction %s: %w", transactionID, err)
			}
			if accountingDetails {
				view, err := compactTransactionAccountingDetails(transaction)
				if err != nil {
					return fmt.Errorf("compact transaction accounting details: %w", err)
				}
				return writeJSON(cmd.OutOrStdout(), view)
			}
			_, err = cmd.OutOrStdout().Write(append(transaction, '\n'))
			return err
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&network, "network", "", "Bitwave network prefix for an unqualified transaction hash (Solana signatures are detected automatically)")
	cmd.Flags().BoolVar(&accountingDetails, "accounting-details", false, "Include saved categorization details, including invoice references")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

type transactionAccountingDetailsView struct {
	ID                   string          `json:"id"`
	CategorizationStatus string          `json:"categorizationStatus,omitempty"`
	HasMatchedInvoices   bool            `json:"hasMatchedInvoices"`
	Memo                 string          `json:"memo,omitempty"`
	AccountingDetails    json.RawMessage `json:"accountingDetails"`
}

func compactTransactionAccountingDetails(transaction json.RawMessage) (*transactionAccountingDetailsView, error) {
	var view transactionAccountingDetailsView
	if err := json.Unmarshal(transaction, &view); err != nil {
		return nil, err
	}
	if view.ID == "" {
		return nil, errors.New("transaction accounting-details response did not include an id")
	}
	if len(view.AccountingDetails) == 0 {
		view.AccountingDetails = json.RawMessage("[]")
	}
	return &view, nil
}

func newTransactionStateCmd(name, transition string) *op.Definition {
	var f transactionMutationFlags
	cmd := &op.Definition{
		Use:   name + " TRANSACTION_ID [TRANSACTION_ID...]",
		Short: strings.ToUpper(name[:1]) + name[1:] + " one or more organization transactions",
		Args:  op.MinimumNArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runTransactionStateMutation(cmd, name, transition, uniqueNonEmpty(args), f)
		},
	}
	addMutationFlags(cmd, &f)
	cmd.Flags().StringVar(&f.bulkActionID, "bulk-action-id", "", "Optional idempotency key for the server workflow")
	cmd.Flags().BoolVar(&f.noWait, "no-wait", false, "Return immediately if the server starts an asynchronous workflow")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 15*time.Minute, "Maximum time to wait for an asynchronous workflow")
	return cmd
}

func addMutationFlags(cmd *op.Definition, f *transactionMutationFlags) {
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Confirm the organization mutation")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print the exact request without changing the organization")
	cmd.Flags().BoolVar(&f.jsonOutput, "json", false, "Emit machine-readable JSON")
}

func runTransactionStateMutation(cmd *op.Call, operation, transition string, transactionIDs []string, f transactionMutationFlags) error {
	if len(transactionIDs) == 0 {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("at least one transaction ID is required"))
	}
	if f.timeout <= 0 {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--timeout must be greater than zero"))
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	request := orgreports.BulkStateRequest{BulkActionID: f.bulkActionID, TransactionIDs: transactionIDs, Update: transition}
	preview := map[string]any{"method": "POST", "path": fmt.Sprintf("/v3/orgs/%s/transactions/bulk-state", orgID), "body": request}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}

	client := newReportsClient(cmd.Context(), orgID)
	result, err := client.BulkUpdateTransactionState(cmd.Context(), orgID, request)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("%s transactions: %w", operation, err))
	}
	if result.WorkflowID != "" && !f.noWait && strings.EqualFold(result.Status, "RUNNING") {
		ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
		defer cancel()
		result, err = waitForBulkStateWorkflow(ctx, client, orgID, result.WorkflowID)
		if err != nil {
			return mutationError(cmd, operation, f.jsonOutput, err)
		}
	}
	envelope := mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}
	if !result.Success && !(f.noWait && strings.EqualFold(result.Status, "RUNNING")) {
		envelope.Status = "partial_failure"
		_ = writeJSON(cmd.OutOrStdout(), envelope)
		return fmt.Errorf("%s processed %d transaction(s): %d succeeded, %d failed", operation, result.Processed, result.SuccessCount, len(result.Failed))
	}
	return outputMutation(cmd, f.jsonOutput, envelope, fmt.Sprintf("%s: %d transaction(s) succeeded\n", operation, result.SuccessCount))
}

func waitForBulkStateWorkflow(ctx context.Context, client *orgreports.Client, orgID, workflowID string) (*orgreports.BulkStateResponse, error) {
	delay := time.Second
	for {
		status, err := client.BulkTransactionStateStatus(ctx, orgID, workflowID)
		if err != nil {
			return nil, fmt.Errorf("check transaction workflow %s: %w", workflowID, err)
		}
		switch strings.ToUpper(status.Status) {
		case "COMPLETED":
			return status, nil
		case "RUNNING", "":
		default:
			return status, fmt.Errorf("transaction workflow %s ended with status %s", workflowID, status.Status)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("waiting for transaction workflow %s: %w", workflowID, ctx.Err())
		case <-timer.C:
		}
		if delay < 5*time.Second {
			delay *= 2
		}
	}
}

type categorizeFlags struct {
	transactionMutationFlags
	input   string
	network string
}

func newCategorizeTransactionCmd() *op.Definition {
	var f categorizeFlags
	cmd := &op.Definition{
		Use:     "categorize TRANSACTION_ID",
		Aliases: []string{"categorise"},
		Short:   "Apply a complete Bitwave categorization payload to one transaction",
		Args:    op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runCategorizeTransaction(cmd, args[0], f)
		},
	}
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().StringVarP(&f.input, "input", "i", "", "Categorization JSON file, or - for stdin (required)")
	cmd.Flags().StringVar(&f.network, "network", "", "Bitwave network prefix for an unqualified transaction hash (Solana signatures are detected automatically)")
	return cmd
}

func runCategorizeTransaction(cmd *op.Call, transactionID string, f categorizeFlags) error {
	operation := "categorize"
	transactionID, err := normalizeTransactionID(transactionID, f.network)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	body, err := readJSONObject(cmd.Context(), f.input, cmd.InOrStdin())
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	if err := validateSingleCategorization(body); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	preview := map[string]any{"method": "PATCH", "path": fmt.Sprintf("/v3/orgs/%s/transactions/%s", orgID, transactionID), "body": body}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}
	client := newReportsClient(cmd.Context(), orgID)
	if err := client.CategorizeTransaction(cmd.Context(), orgID, transactionID, body); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("categorize transaction %s: %w", transactionID, err))
	}
	envelope := mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: map[string]any{"transactionId": transactionID}}
	return outputMutation(cmd, f.jsonOutput, envelope, fmt.Sprintf("categorized transaction %s\n", transactionID))
}

type bulkCategorizeFlags struct {
	transactionMutationFlags
	input                  string
	kind                   string
	transactionIDs         []string
	accountingConnectionID string
	feeContactID           string
	feeCategoryID          string
	sendContactID          string
	sendCategoryID         string
	receiveContactID       string
	receiveCategoryID      string
	overwrite              bool
}

func newBulkCategorizeTransactionsCmd() *op.Definition {
	var f bulkCategorizeFlags
	cmd := &op.Definition{
		Use:     "bulk-categorize",
		Aliases: []string{"bulk-categorise"},
		Short:   "Categorize multiple transactions using Bitwave's bulk contract",
		Long: `Categorize multiple transactions as multivalue, trade, or transfer.

Use --input for the complete Bitwave bulk JSON contract, or use the typed flags.
The two input styles cannot be combined.`,
		RunE: func(cmd *op.Call, _ []string) error { return runBulkCategorizeTransactions(cmd, f) },
	}
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().StringVarP(&f.input, "input", "i", "", "Complete bulk categorization JSON file, or - for stdin")
	cmd.Flags().StringVar(&f.kind, "type", "", "Bulk categorization type: multivalue, trade, or transfer")
	cmd.Flags().StringSliceVar(&f.transactionIDs, "transaction", nil, "Transaction ID (repeatable or comma-separated)")
	cmd.Flags().StringVar(&f.accountingConnectionID, "accounting-connection", "", "Accounting connection ID")
	cmd.Flags().StringVar(&f.feeContactID, "fee-contact", "", "Fee contact ID")
	cmd.Flags().StringVar(&f.feeCategoryID, "fee-category", "", "Fee category ID")
	cmd.Flags().StringVar(&f.sendContactID, "send-contact", "", "Multivalue send contact ID")
	cmd.Flags().StringVar(&f.sendCategoryID, "send-category", "", "Multivalue send category ID")
	cmd.Flags().StringVar(&f.receiveContactID, "receive-contact", "", "Multivalue receive contact ID")
	cmd.Flags().StringVar(&f.receiveCategoryID, "receive-category", "", "Multivalue receive category ID")
	cmd.Flags().BoolVar(&f.overwrite, "overwrite", false, "Overwrite existing categorization")
	return cmd
}

func runBulkCategorizeTransactions(cmd *op.Call, f bulkCategorizeFlags) error {
	operation := "bulk-categorize"
	body, err := bulkCategorizationBody(cmd.Context(), f, cmd.InOrStdin())
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	preview := map[string]any{"method": "PUT", "path": fmt.Sprintf("/v3/orgs/%s/transactions", orgID), "body": body}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}
	client := newReportsClient(cmd.Context(), orgID)
	result, err := client.BulkCategorizeTransactions(cmd.Context(), orgID, body)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("bulk categorize transactions: %w", err))
	}
	failed := 0
	for _, item := range result {
		if !item.Success {
			failed++
		}
	}
	envelope := mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}
	if failed > 0 {
		envelope.Status = "partial_failure"
		_ = writeJSON(cmd.OutOrStdout(), envelope)
		return fmt.Errorf("bulk categorization returned %d failed result(s)", failed)
	}
	return outputMutation(cmd, f.jsonOutput, envelope, fmt.Sprintf("bulk categorized %d transaction(s)\n", len(result)))
}

func bulkCategorizationBody(ctx context.Context, f bulkCategorizeFlags, stdin io.Reader) (json.RawMessage, error) {
	usingFlags := f.kind != "" || len(f.transactionIDs) > 0 || f.accountingConnectionID != "" || f.feeContactID != "" || f.feeCategoryID != "" || f.sendContactID != "" || f.sendCategoryID != "" || f.receiveContactID != "" || f.receiveCategoryID != "" || f.overwrite
	if f.input != "" {
		if usingFlags {
			return nil, errors.New("--input cannot be combined with typed categorization flags")
		}
		body, err := readJSONObject(ctx, f.input, stdin)
		if err != nil {
			return nil, err
		}
		if err := validateBulkCategorization(body); err != nil {
			return nil, err
		}
		return body, nil
	}
	if f.kind != "multivalue" && f.kind != "trade" && f.kind != "transfer" {
		return nil, errors.New("--type must be multivalue, trade, or transfer")
	}
	ids := uniqueNonEmpty(f.transactionIDs)
	if len(ids) == 0 {
		return nil, errors.New("at least one --transaction is required")
	}
	if f.accountingConnectionID == "" {
		return nil, errors.New("--accounting-connection is required")
	}
	if f.feeContactID == "" || f.feeCategoryID == "" {
		return nil, errors.New("--fee-contact and --fee-category are required")
	}
	params := map[string]any{"txnIds": ids, "feeContactId": f.feeContactID, "feeCategoryId": f.feeCategoryID}
	if f.kind == "multivalue" {
		if f.sendContactID == "" || f.sendCategoryID == "" || f.receiveContactID == "" || f.receiveCategoryID == "" {
			return nil, errors.New("multivalue requires --send-contact, --send-category, --receive-contact, and --receive-category")
		}
		params["sendContactId"] = f.sendContactID
		params["sendCategoryId"] = f.sendCategoryID
		params["receiveContactId"] = f.receiveContactID
		params["receiveCategoryId"] = f.receiveCategoryID
	}
	body := map[string]any{"categorization": map[string]any{"accountingConnectionId": f.accountingConnectionID, f.kind: params}, "options": map[string]any{"overwriteExistingCategorization": f.overwrite}}
	data, err := json.Marshal(body)
	return data, err
}

func readJSONObject(ctx context.Context, path string, stdin io.Reader) (json.RawMessage, error) {
	if path == "" {
		return nil, errors.New("--input is required")
	}
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(stdin, 4<<20))
	} else {
		data, err = op.RuntimeFrom(ctx).Files.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read categorization input: %w", err)
	}
	if len(data) == 0 || !json.Valid(data) {
		return nil, errors.New("categorization input must be valid JSON")
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, errors.New("categorization input must be a JSON object")
	}
	return json.RawMessage(data), nil
}

func validateSingleCategorization(body json.RawMessage) error {
	var object map[string]any
	_ = json.Unmarshal(body, &object)
	typeName, _ := object["type"].(string)
	if typeName == "" {
		return errors.New("single categorization JSON requires a string `type`")
	}
	valid := map[string]bool{"invoice": true, "invoice-v2": true, "multivalue": true, "trade": true, "transfer": true, "advance-defi": true, "intercompany-transfer": true}
	if !valid[typeName] {
		return fmt.Errorf("unsupported categorization type %q", typeName)
	}
	method, ok := object["categorizationMethod"].(float64)
	if !ok || method < 1 || method > 6 || method != float64(int(method)) {
		return errors.New("categorization JSON requires numeric `categorizationMethod` from 1 through 6")
	}
	if _, ok := object["exchangeRates"].([]any); !ok {
		return errors.New("categorization JSON requires an `exchangeRates` array")
	}
	if typeName != "invoice" {
		if value, _ := object["accountingConnectionId"].(string); value == "" {
			return errors.New("categorization JSON requires `accountingConnectionId`")
		}
		if _, ok := object["exchangeRateVersion"].(float64); !ok {
			return errors.New("categorization JSON requires numeric `exchangeRateVersion`")
		}
	}
	return nil
}

func validateBulkCategorization(body json.RawMessage) error {
	var object struct {
		Categorization map[string]any `json:"categorization"`
	}
	_ = json.Unmarshal(body, &object)
	if object.Categorization == nil {
		return errors.New("bulk categorization JSON requires `categorization`")
	}
	if value, _ := object.Categorization["accountingConnectionId"].(string); value == "" {
		return errors.New("bulk categorization JSON requires `categorization.accountingConnectionId`")
	}
	if object.Categorization["multivalue"] == nil && object.Categorization["trade"] == nil && object.Categorization["transfer"] == nil {
		return errors.New("bulk categorization JSON requires multivalue, trade, or transfer parameters")
	}
	return nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func mutationError(cmd *op.Call, operation string, jsonOutput bool, err error) error {
	if jsonOutput {
		explicitOrg, _ := cmd.Flags().GetString("org")
		orgID, _ := resolveReportOrg(cmd.Context(), explicitOrg)
		detail := &reportError{Code: "invalid_request", Message: err.Error(), Retryable: false, Suggestion: "Use --dry-run to preview writes and `bitwave transaction categorization-options --json` to discover categorization IDs."}
		var apiError *apierr.Error
		if errors.As(err, &apiError) {
			detail.Code = "api_error"
			detail.HTTPStatus = apiError.Status
			detail.Retryable = apiError.Status == 429 || apiError.Status >= 500
		}
		if strings.Contains(err.Error(), "without --yes") {
			detail.Code = "confirmation_required"
		}
		_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "error", Operation: operation, Organization: orgID, Error: detail})
	}
	return err
}

func outputMutation(cmd *op.Call, jsonOutput bool, envelope mutationEnvelope, human string) error {
	if jsonOutput {
		return writeJSON(cmd.OutOrStdout(), envelope)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), human)
	return err
}

func newCategorizationOptionsCmd() *op.Definition {
	var orgID, query, accountingConnectionID string
	var includeDisabled bool
	var limit int
	cmd := &op.Definition{
		Use:   "categorization-options",
		Short: "Return LLM-friendly category, contact, connection, and payload choices",
		RunE: func(cmd *op.Call, _ []string) error {
			if limit <= 0 || limit > 500 {
				return errors.New("--limit must be between 1 and 500")
			}
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			categories, err := client.Categories(cmd.Context(), resolvedOrg)
			if err != nil {
				return fmt.Errorf("list categories: %w", err)
			}
			contacts, err := client.Contacts(cmd.Context(), resolvedOrg)
			if err != nil {
				return fmt.Errorf("list contacts: %w", err)
			}
			connections, connectionErr := client.AccountingConnections(cmd.Context(), resolvedOrg)
			warnings := []string{}
			if connectionErr != nil {
				warnings = append(warnings, "Accounting connections unavailable: "+connectionErr.Error())
			}
			categoryTotal, contactTotal := len(categories), len(contacts)
			if query == "" && accountingConnectionID == "" {
				categories = nil
				contacts = nil
				warnings = append(warnings, "Category and contact choices are omitted by default to protect LLM context. Pass --query or --accounting-connection to return bounded choices.")
			} else {
				categories = filterCategories(categories, query, accountingConnectionID, includeDisabled)
				contacts = filterContacts(contacts, query, accountingConnectionID, includeDisabled)
				if len(categories) > limit {
					categories = categories[:limit]
					warnings = append(warnings, "Category results were truncated; narrow --query or --accounting-connection.")
				}
				if len(contacts) > limit {
					contacts = contacts[:limit]
					warnings = append(warnings, "Contact results were truncated; narrow --query or --accounting-connection.")
				}
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"schemaVersion": "1", "organization": resolvedOrg,
				"categories": categories, "contacts": contacts, "accountingConnections": connections,
				"counts":                     map[string]int{"categories": categoryTotal, "contacts": contactTotal},
				"filters":                    map[string]any{"query": query, "accountingConnectionId": accountingConnectionID, "includeDisabled": includeDisabled, "limit": limit},
				"singleCategorizationTypes":  []string{"invoice", "invoice-v2", "multivalue", "trade", "transfer", "advance-defi", "intercompany-transfer"},
				"bulkCategorizationTypes":    []string{"multivalue", "trade", "transfer"},
				"singleCommonFields":         map[string]any{"type": "one of singleCategorizationTypes", "categorizationMethod": 1, "accountingConnectionId": "stable connection ID", "exchangeRates": []any{}, "exchangeRateVersion": 0},
				"categorizationMethodValues": map[string]int{"manual": 1, "inferred": 2, "rule": 3, "systemSet": 4, "ops": 5, "unknown": 6},
				"warnings":                   warnings,
			})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&query, "query", "", "Case-insensitive category/contact name, code, or ID substring")
	cmd.Flags().StringVar(&accountingConnectionID, "accounting-connection", "", "Only choices belonging to this accounting connection ID")
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Include disabled categories and contacts")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum category choices and contact choices to return")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func filterCategories(items []orgreports.Category, query, connectionID string, includeDisabled bool) []orgreports.Category {
	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]orgreports.Category, 0)
	for _, item := range items {
		if !includeDisabled && !item.Enabled {
			continue
		}
		if connectionID != "" && item.AccountingConnectionID != connectionID {
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{item.ID, item.Name, item.Code, item.Type, item.Source}, " "))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		result = append(result, item)
	}
	return result
}

func filterContacts(items []orgreports.Contact, query, connectionID string, includeDisabled bool) []orgreports.Contact {
	query = strings.ToLower(strings.TrimSpace(query))
	result := make([]orgreports.Contact, 0)
	for _, item := range items {
		if !includeDisabled && !item.Enabled {
			continue
		}
		if connectionID != "" && item.AccountingConnectionID != connectionID {
			continue
		}
		haystack := strings.ToLower(strings.Join([]string{item.ID, item.Name, item.Type, item.Source}, " "))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		result = append(result, item)
	}
	return result
}
