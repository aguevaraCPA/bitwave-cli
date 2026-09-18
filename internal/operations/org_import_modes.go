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
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

const (
	importModeAuto               = "auto"
	importModeDirect             = "direct"
	importModeStaged             = "staged"
	directRecommendationRowLimit = 100
	directRequestBatchSize       = 100
)

type importPlanIssue struct {
	Row     int    `json:"row,omitempty"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type importRequestPlan struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Count       int    `json:"count"`
	Description string `json:"description"`
}

type transactionImportPlan struct {
	SchemaVersion      string              `json:"schemaVersion"`
	RequestedMode      string              `json:"requestedMode"`
	RecommendedMode    string              `json:"recommendedMode"`
	ExplicitOverride   bool                `json:"explicitOverride"`
	RowCount           int                 `json:"rowCount"`
	Reasons            []string            `json:"reasons"`
	ValidationIssues   []importPlanIssue   `json:"validationIssues"`
	TargetWallets      []string            `json:"targetWallets"`
	ExpectedRequests   []importRequestPlan `json:"expectedRequests"`
	ImportHistoryEntry bool                `json:"importHistoryRecordWillExist"`
	DirectEligible     bool                `json:"directEligible"`
	StableUniqueIDs    bool                `json:"stableUniqueIds"`
	PreservedColumns   []string            `json:"preservedColumns"`
	UnmappedColumns    []string            `json:"unmappedColumns,omitempty"`
}

type directImportAnalysis struct {
	plan transactionImportPlan
	rows []orgreports.CreateTransaction
}

type canonicalCSVRow struct {
	rowNumber int
	values    map[string]string
}

func analyzeTransactionImport(ctx context.Context, path, requestedMode string, wallets []orgreports.Wallet) (*directImportAnalysis, error) {
	rows, headers, err := readCanonicalImportCSV(ctx, path)
	if err != nil {
		return nil, err
	}
	knownWallets := make(map[string]struct{}, len(wallets))
	for _, wallet := range wallets {
		knownWallets[wallet.ID] = struct{}{}
	}
	knownColumns := make(map[string]struct{}, len(manualImportColumns))
	for _, column := range manualImportColumns {
		knownColumns[column] = struct{}{}
	}

	issues := make([]importPlanIssue, 0)
	unmappedSet := map[string]struct{}{}
	preservedSet := map[string]struct{}{}
	for _, header := range headers {
		if _, ok := knownColumns[header]; ok {
			preservedSet[header] = struct{}{}
		} else {
			unmappedSet[header] = struct{}{}
		}
	}
	for _, required := range []string{"id", "amount", "amountTicker", "time", "transactionType", "accountId"} {
		if _, ok := preservedSet[required]; !ok {
			issues = append(issues, importPlanIssue{Field: required, Code: "missing_column", Message: required + " is required for direct creation"})
		}
	}

	seenIDs := map[string]int{}
	targetWalletSet := map[string]struct{}{}
	directRows := make([]orgreports.CreateTransaction, 0, len(rows))
	reviewSensitive := false
	for _, row := range rows {
		value := func(name string) string { return strings.TrimSpace(row.values[name]) }
		id := value("id")
		if id == "" {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "id", Code: "missing_id", Message: "a stable unique id is required for direct creation"})
		} else if first, exists := seenIDs[id]; exists {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "id", Code: "duplicate_id", Message: fmt.Sprintf("id duplicates row %d", first)})
		} else {
			seenIDs[id] = row.rowNumber
		}

		amount := value("amount")
		if err := positiveDecimal("amount", amount); err != nil {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "amount", Code: "invalid_amount", Message: err.Error()})
		}
		if value("amountTicker") == "" {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "amountTicker", Code: "missing_asset", Message: "amountTicker is required"})
		}
		at := value("time")
		if _, err := time.Parse(time.RFC3339, at); err != nil {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "time", Code: "invalid_time", Message: "time must be RFC3339 with an explicit timezone"})
		}
		typeName := value("transactionType")
		if !directTransactionType(typeName) {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "transactionType", Code: "invalid_transaction_type", Message: "transactionType must be deposit, withdrawal, tradeFee, tradeAcquire, or tradeDispose"})
		}
		walletID := value("accountId")
		if walletID == "" {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "accountId", Code: "missing_wallet", Message: "accountId is required"})
		} else {
			targetWalletSet[walletID] = struct{}{}
			if _, ok := knownWallets[walletID]; !ok {
				issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "accountId", Code: "unknown_wallet", Message: "accountId is not an enabled wallet in the organization"})
			}
		}
		if !pairedOrEmpty(value("fee"), value("feeTicker")) {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "fee", Code: "incomplete_fee", Message: "fee and feeTicker must be supplied together"})
		} else if value("fee") != "" {
			if err := positiveDecimal("fee", value("fee")); err != nil {
				issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "fee", Code: "invalid_fee", Message: err.Error()})
			}
		}
		if !pairedOrEmpty(value("cost"), value("costTicker")) {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "cost", Code: "incomplete_cost", Message: "cost and costTicker must be supplied together"})
		} else if value("cost") != "" {
			if err := positiveDecimal("cost", value("cost")); err != nil {
				issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "cost", Code: "invalid_cost", Message: err.Error()})
			}
		}
		tradeID := value("tradeId")
		if strings.HasPrefix(typeName, "trade") && tradeID == "" {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "tradeId", Code: "missing_trade_id", Message: "tradeAcquire, tradeDispose, and tradeFee rows require tradeId"})
		} else if !strings.HasPrefix(typeName, "trade") && tradeID != "" {
			issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: "tradeId", Code: "trade_id_not_allowed", Message: "tradeId is only valid for tradeAcquire, tradeDispose, and tradeFee rows"})
		}
		for _, stagedOnly := range []string{"remoteContactId", "taxExempt", "contactId", "categoryId"} {
			if value(stagedOnly) != "" {
				reviewSensitive = true
				issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: stagedOnly, Code: "staged_only_field", Message: stagedOnly + " requires the staged import workflow"})
			}
		}
		for column := range unmappedSet {
			if value(column) != "" {
				issues = append(issues, importPlanIssue{Row: row.rowNumber, Field: column, Code: "unmapped_column", Message: "column has no canonical direct-creation mapping"})
			}
		}
		directRows = append(directRows, orgreports.CreateTransaction{
			SystemID: id, Time: at, AccountID: walletID, Amount: amount,
			AmountTicker: value("amountTicker"), TransactionType: typeName,
			TradeID: tradeID, GroupID: value("groupId"), BlockchainID: value("blockchainId"),
			Cost: value("cost"), CostTicker: value("costTicker"), Fee: value("fee"), FeeTicker: value("feeTicker"),
			Memo: value("memo"), Description: value("description"), FromAddress: value("fromAddress"), ToAddress: value("toAddress"),
		})
	}

	targetWallets := sortedKeys(targetWalletSet)
	unmapped := sortedKeys(unmappedSet)
	preserved := sortedKeys(preservedSet)
	directEligible := len(issues) == 0
	recommended := importModeDirect
	reasons := []string{"all rows map unambiguously to canonical Bitwave transactions", "stable unique IDs are present", "the destination wallet is known"}
	if !directEligible {
		recommended = importModeStaged
		reasons = []string{"one or more rows require server-side validation or review"}
	} else if len(rows) > directRecommendationRowLimit {
		recommended = importModeStaged
		reasons = []string{fmt.Sprintf("row count exceeds the %d-row direct recommendation threshold", directRecommendationRowLimit)}
	} else if len(targetWallets) != 1 {
		recommended = importModeStaged
		reasons = []string{"file targets multiple wallets"}
	} else if reviewSensitive {
		recommended = importModeStaged
		reasons = []string{"file contains review-sensitive fields"}
	}
	selected := recommended
	explicitOverride := requestedMode != importModeAuto && requestedMode != recommended
	if requestedMode != importModeAuto {
		selected = requestedMode
	}
	plan := transactionImportPlan{
		SchemaVersion: "1", RequestedMode: requestedMode, RecommendedMode: recommended,
		ExplicitOverride: explicitOverride, RowCount: len(rows), Reasons: reasons,
		ValidationIssues: issues, TargetWallets: targetWallets, DirectEligible: directEligible,
		StableUniqueIDs: stableUniqueIDs(rows, seenIDs), PreservedColumns: preserved, UnmappedColumns: unmapped,
		ImportHistoryEntry: selected == importModeStaged,
		ExpectedRequests:   expectedImportRequests(selected, len(rows)),
	}
	return &directImportAnalysis{plan: plan, rows: directRows}, nil
}

func readCanonicalImportCSV(ctx context.Context, path string) ([]canonicalCSVRow, []string, error) {
	file, err := op.RuntimeFrom(ctx).Files.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open CSV: %w", err)
	}
	defer func() { _ = file.Close() }()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	headers, err := reader.Read()
	if err != nil {
		return nil, nil, fmt.Errorf("read CSV header: %w", err)
	}
	if len(headers) > 0 {
		headers[0] = strings.TrimPrefix(headers[0], "\ufeff")
	}
	seenHeaders := map[string]struct{}{}
	for index := range headers {
		headers[index] = strings.TrimSpace(headers[index])
		if headers[index] == "" {
			return nil, nil, errors.New("CSV contains an empty header")
		}
		if _, exists := seenHeaders[headers[index]]; exists {
			return nil, nil, fmt.Errorf("CSV contains duplicate header %q", headers[index])
		}
		seenHeaders[headers[index]] = struct{}{}
	}
	rows := make([]canonicalCSVRow, 0)
	for rowNumber := 2; ; rowNumber++ {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read CSV row %d: %w", rowNumber, err)
		}
		values := make(map[string]string, len(headers))
		nonEmpty := false
		for index, header := range headers {
			if index < len(record) {
				values[header] = record[index]
				nonEmpty = nonEmpty || strings.TrimSpace(record[index]) != ""
			}
		}
		if nonEmpty {
			rows = append(rows, canonicalCSVRow{rowNumber: rowNumber, values: values})
		}
	}
	if len(rows) == 0 {
		return nil, nil, errors.New("import CSV has no data rows")
	}
	return rows, headers, nil
}

func directTransactionType(value string) bool {
	switch value {
	case "deposit", "withdrawal", "tradeFee", "tradeAcquire", "tradeDispose":
		return true
	default:
		return false
	}
}

func pairedOrEmpty(left, right string) bool { return (left == "") == (right == "") }

func stableUniqueIDs(rows []canonicalCSVRow, seen map[string]int) bool {
	return len(rows) > 0 && len(seen) == len(rows)
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func expectedImportRequests(mode string, rows int) []importRequestPlan {
	if mode == importModeDirect {
		count := (rows + directRequestBatchSize - 1) / directRequestBatchSize
		return []importRequestPlan{{Method: "POST", Path: "/txns/orgs/{org}/transactions?immediate=true", Count: count, Description: "create canonical transactions in batches"}}
	}
	return []importRequestPlan{
		{Method: "POST", Path: "/graphql createImport", Count: 1, Description: "create import-history record"},
		{Method: "PUT", Path: "signed upload URL", Count: 1, Description: "upload CSV without bearer authentication"},
		{Method: "POST", Path: "/graphql validateImport", Count: 1, Description: "start validation"},
		{Method: "POST", Path: "/graphql runImport", Count: 1, Description: "run only after validation passes"},
		{Method: "POST", Path: "/graphql getImport", Count: 2, Description: "minimum status polls; actual count depends on processing time"},
	}
}

func runDirectTransactionImport(cmd *op.Call, orgID string, analysis *directImportAnalysis, f importWaitFlags) error {
	const operation = "import-transactions-direct"
	if !analysis.plan.DirectEligible {
		return importPlanFailure(cmd, operation, orgID, analysis.plan, errors.New("file is not eligible for direct creation; use --mode staged or correct the validation issues"))
	}
	requests := make([]map[string]any, 0)
	for start := 0; start < len(analysis.rows); start += directRequestBatchSize {
		end := start + directRequestBatchSize
		if end > len(analysis.rows) {
			end = len(analysis.rows)
		}
		requests = append(requests, map[string]any{"method": "POST", "path": fmt.Sprintf("/txns/orgs/%s/transactions?immediate=true", orgID), "body": analysis.rows[start:end]})
	}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: map[string]any{"plan": analysis.plan, "requests": requests}})
	}
	if !f.yes {
		return importPlanFailure(cmd, operation, orgID, analysis.plan, errors.New("refusing to create organization transactions without --yes (use --dry-run to preview)"))
	}
	client := newReportsClient(cmd.Context(), orgID)
	if f.timeout > 0 {
		client.HTTPClient.Timeout = f.timeout
	}
	responses := make([]json.RawMessage, 0, len(requests))
	createdRows := 0
	for start := 0; start < len(analysis.rows); start += directRequestBatchSize {
		end := start + directRequestBatchSize
		if end > len(analysis.rows) {
			end = len(analysis.rows)
		}
		response, err := client.CreateTransactions(cmd.Context(), orgID, analysis.rows[start:end])
		if err != nil {
			message := fmt.Sprintf("direct creation stopped after submitting %d of %d rows; stable source IDs make the submitted rows idempotent, but verify them before retrying", createdRows, len(analysis.rows))
			envelopeStatus := "partial_success"
			code := "direct_create_failed"
			if createdRows == 0 {
				envelopeStatus = "error"
				code = "direct_create_rejected"
			}
			if f.jsonOutput {
				_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: envelopeStatus, Operation: operation, Organization: orgID, Result: map[string]any{"plan": analysis.plan, "createdRows": createdRows, "rowCount": len(analysis.rows), "responses": responses}, Error: &reportError{Code: code, Message: message + ": " + err.Error(), Retryable: false}})
			}
			return fmt.Errorf("%s: %w", message, err)
		}
		responses = append(responses, response)
		createdRows = end
	}
	result := map[string]any{"plan": analysis.plan, "createdRows": createdRows, "rowCount": len(analysis.rows), "responses": responses, "importHistoryRecord": false}
	return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("created %d transaction row(s) directly\n", createdRows))
}

func importPlanFailure(cmd *op.Call, operation, orgID string, plan transactionImportPlan, err error) error {
	_ = writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "error", Operation: operation, Organization: orgID, Result: map[string]any{"plan": plan}, Error: &reportError{Code: "direct_mode_not_safe", Message: err.Error(), Retryable: false, Suggestion: "Use --mode staged or correct the listed validation issues."}})
	return err
}
