package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type transactionSearchFlags struct {
	orgID, from, to, nextToken, sortBy, sortDirection string
	wallets, assets, types, states                    []string
	categorization, reconciliation, ignored           []string
	search, transactionIDs, fromAddresses             []string
	toAddresses, addresses, operations                []string
	includeCombined                                   bool
	full                                              bool
	limit                                             int
}

func newSearchOrgTransactionsCmd() *op.Definition {
	var f transactionSearchFlags
	cmd := &op.Definition{
		Use:   "search",
		Short: "Find organization transactions using UI-compatible filters",
		Long: `Find a small, paginated set of transactions before a mutation.

Address, wallet, asset, direction, status, and date filters are sent to the
same transaction-search API used by the product. Results default to 25 rows so
an LLM does not need to load the organization's entire transaction history.`,
		RunE: func(cmd *op.Call, _ []string) error { return runSearchOrgTransactions(cmd, f) },
	}
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&f.from, "from", "", "Inclusive start date (YYYY-MM-DD; requires --to)")
	cmd.Flags().StringVar(&f.to, "to", "", "Inclusive end date (YYYY-MM-DD; requires --from)")
	cmd.Flags().StringSliceVar(&f.wallets, "wallet", nil, "Wallet ID or exact name (repeatable)")
	cmd.Flags().StringSliceVar(&f.assets, "asset", nil, "Asset ID (repeatable)")
	cmd.Flags().StringSliceVar(&f.types, "type", nil, "Transaction type, such as send, receive, trade, or transfer")
	cmd.Flags().StringSliceVar(&f.operations, "operation", nil, "Transaction operation, such as Send or Receive")
	cmd.Flags().StringSliceVar(&f.states, "state", nil, "Transaction workflow state")
	cmd.Flags().StringSliceVar(&f.categorization, "categorization", nil, "Categorization status")
	cmd.Flags().StringSliceVar(&f.reconciliation, "reconciliation", nil, "Reconciliation status")
	cmd.Flags().StringSliceVar(&f.ignored, "ignored", nil, "Ignored status")
	cmd.Flags().StringSliceVar(&f.search, "search", nil, "Free-text search token (repeatable; maximum five)")
	cmd.Flags().StringSliceVar(&f.transactionIDs, "transaction", nil, "Transaction ID (repeatable)")
	cmd.Flags().StringSliceVar(&f.fromAddresses, "from-address", nil, "Exact sender address (repeatable)")
	cmd.Flags().StringSliceVar(&f.toAddresses, "to-address", nil, "Exact recipient address (repeatable)")
	cmd.Flags().StringSliceVar(&f.addresses, "address", nil, "Address on either side (repeatable)")
	cmd.Flags().BoolVar(&f.includeCombined, "include-combined", false, "Include combined transaction children")
	cmd.Flags().BoolVar(&f.full, "full", false, "Return complete transaction objects instead of compact LLM summaries")
	cmd.Flags().IntVar(&f.limit, "limit", 25, "Page size (1-100)")
	cmd.Flags().StringVar(&f.nextToken, "next-token", "", "Opaque next-page token from the previous response")
	cmd.Flags().StringVar(&f.sortBy, "sort-by", "timestamp", "Server transaction sort field")
	cmd.Flags().StringVar(&f.sortDirection, "sort-direction", "desc", "Sort direction: asc or desc")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func runSearchOrgTransactions(cmd *op.Call, f transactionSearchFlags) error {
	if f.limit < 1 || f.limit > 100 {
		return errors.New("--limit must be between 1 and 100")
	}
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
	if f.sortDirection != "asc" && f.sortDirection != "desc" {
		return errors.New("--sort-direction must be asc or desc")
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
	wallets, err := client.Wallets(cmd.Context(), orgID)
	if err != nil {
		return fmt.Errorf("discover wallets: %w", err)
	}
	walletIDs, err := resolveWalletRefs(f.wallets, wallets)
	if err != nil {
		return err
	}
	request := orgreports.TransactionSearchRequest{
		Timezone: org.Timezone, Limit: f.limit, NextToken: f.nextToken, SortBy: f.sortBy, SortDirection: f.sortDirection,
		Filters: orgreports.TransactionExportFilters{
			DateRange: optionalDateRange(f.from, f.to), WalletIDs: walletIDs,
			AssetIDs: uniqueNonEmpty(f.assets), TransactionTypes: uniqueNonEmpty(f.types), Operations: uniqueNonEmpty(f.operations),
			States: uniqueNonEmpty(f.states), CategorizationStatuses: uniqueNonEmpty(f.categorization),
			ReconciliationStatuses: uniqueNonEmpty(f.reconciliation), IgnoredStatuses: uniqueNonEmpty(f.ignored),
			SearchTokens: uniqueNonEmpty(f.search), TransactionIDs: uniqueNonEmpty(f.transactionIDs),
			FromAddresses: uniqueNonEmpty(f.fromAddresses), ToAddresses: uniqueNonEmpty(f.toAddresses), Addresses: uniqueNonEmpty(f.addresses),
			IncludeCombinedTransactions: f.includeCombined,
		},
	}
	result, err := client.SearchTransactions(cmd.Context(), orgID, request)
	if err != nil {
		return fmt.Errorf("search transactions: %w", err)
	}
	var transactions any = compactTransactions(result.Transactions)
	if f.full {
		transactions = result.Transactions
	}
	return writeJSON(cmd.OutOrStdout(), map[string]any{
		"schemaVersion": "1", "organization": orgID, "request": request,
		"count": len(result.Transactions), "transactions": transactions, "resultShape": map[bool]string{true: "full", false: "compact"}[f.full],
		"nextToken": result.NextToken, "prevToken": result.PrevToken,
		"warning": "Review the returned transaction IDs before passing them to a command that changes the organization.",
	})
}

type compactTransaction struct {
	ID                   string                   `json:"id"`
	Timestamp            string                   `json:"timestamp,omitempty"`
	TransactionType      string                   `json:"transactionType,omitempty"`
	State                string                   `json:"state,omitempty"`
	CategorizationStatus string                   `json:"categorizationStatus,omitempty"`
	ReconciliationStatus string                   `json:"reconciliationStatus,omitempty"`
	Ignored              bool                     `json:"ignored"`
	IsEditable           bool                     `json:"isEditable"`
	LineCount            int                      `json:"lineCount"`
	Lines                []compactTransactionLine `json:"lines,omitempty"`
}

type compactTransactionLine struct {
	Line               int             `json:"line"`
	Amount             json.RawMessage `json:"amount,omitempty"`
	AmountCurrencyID   string          `json:"amountCurrencyId,omitempty"`
	AmountCurrencyName string          `json:"amountCurrencyName,omitempty"`
	From               string          `json:"from,omitempty"`
	To                 string          `json:"to,omitempty"`
	WalletID           string          `json:"walletId,omitempty"`
	Operation          string          `json:"operation,omitempty"`
	NetworkID          string          `json:"networkId,omitempty"`
}

func compactTransactions(items []json.RawMessage) []compactTransaction {
	result := make([]compactTransaction, 0, len(items))
	for _, item := range items {
		var transaction struct {
			ID                   string                   `json:"id"`
			Timestamp            string                   `json:"timestamp"`
			TransactionType      string                   `json:"transactionType"`
			State                string                   `json:"state"`
			CategorizationStatus string                   `json:"categorizationStatus"`
			ReconciliationStatus string                   `json:"reconciliationStatus"`
			Ignored              bool                     `json:"ignored"`
			IsEditable           bool                     `json:"isEditable"`
			Lines                []compactTransactionLine `json:"lines"`
		}
		if json.Unmarshal(item, &transaction) != nil {
			continue
		}
		lines := transaction.Lines
		if len(lines) > 5 {
			lines = lines[:5]
		}
		result = append(result, compactTransaction{
			ID: transaction.ID, Timestamp: transaction.Timestamp, TransactionType: transaction.TransactionType,
			State: transaction.State, CategorizationStatus: transaction.CategorizationStatus,
			ReconciliationStatus: transaction.ReconciliationStatus, Ignored: transaction.Ignored,
			IsEditable: transaction.IsEditable, LineCount: len(transaction.Lines), Lines: lines,
		})
	}
	return result
}

type transactionCreateCommon struct {
	transactionMutationFlags
	wallet, systemID, at, memo, description string
	fromAddress, toAddress                  string
	categoryID, contactID, connectionID     string
}

func newCreateOrgTransactionCmd() *op.Definition {
	cmd := &op.Definition{Use: "create", Short: "Create simple, trade, or internal-transfer transactions"}
	cmd.AddCommand(newCreateSimpleTransactionCmd(), newCreateTradeTransactionCmd(), newCreateInternalTransferCmd())
	return cmd
}

func addCreateCommonFlags(cmd *op.Definition, f *transactionCreateCommon) {
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().StringVar(&f.wallet, "wallet", "", "Wallet ID or exact name (required)")
	cmd.Flags().StringVar(&f.systemID, "system-id", "", "Caller-controlled unique source ID (required)")
	cmd.Flags().StringVar(&f.at, "at", "", "Transaction time in RFC3339 (required)")
	cmd.Flags().StringVar(&f.memo, "memo", "", "Optional memo")
	cmd.Flags().StringVar(&f.description, "description", "", "Optional description")
	cmd.Flags().StringVar(&f.fromAddress, "from-address", "", "Optional sender address")
	cmd.Flags().StringVar(&f.toAddress, "to-address", "", "Optional recipient address")
	cmd.Flags().StringVar(&f.categoryID, "category", "", "Optional category ID; requires contact and accounting connection")
	cmd.Flags().StringVar(&f.contactID, "contact", "", "Optional contact ID; requires category and accounting connection")
	cmd.Flags().StringVar(&f.connectionID, "accounting-connection", "", "Accounting connection used with category/contact")
}

type simpleCreateFlags struct {
	transactionCreateCommon
	direction, amount, asset, blockchainID, fee, feeAsset string
}

func newCreateSimpleTransactionCmd() *op.Definition {
	var f simpleCreateFlags
	cmd := &op.Definition{Use: "simple", Short: "Create one deposit or withdrawal", RunE: func(cmd *op.Call, _ []string) error { return runCreateSimple(cmd, f) }}
	addCreateCommonFlags(cmd, &f.transactionCreateCommon)
	cmd.Flags().StringVar(&f.direction, "direction", "", "inflow or outflow (required)")
	cmd.Flags().StringVar(&f.amount, "amount", "", "Positive asset amount as a decimal string (required)")
	cmd.Flags().StringVar(&f.asset, "asset", "", "Asset ticker (required)")
	cmd.Flags().StringVar(&f.blockchainID, "blockchain-id", "", "Optional blockchain transaction hash")
	cmd.Flags().StringVar(&f.fee, "fee", "", "Optional positive fee amount")
	cmd.Flags().StringVar(&f.feeAsset, "fee-asset", "", "Fee ticker; required with --fee")
	return cmd
}

func runCreateSimple(cmd *op.Call, f simpleCreateFlags) error {
	operation := "create-simple"
	if err := validateCreateCommon(f.transactionCreateCommon); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	if err := positiveDecimal("--amount", f.amount); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	if f.asset == "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--asset is required"))
	}
	typeName := map[string]string{"inflow": "deposit", "outflow": "withdrawal"}[strings.ToLower(f.direction)]
	if typeName == "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--direction must be inflow or outflow"))
	}
	if (f.fee == "") != (f.feeAsset == "") {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--fee and --fee-asset must be supplied together"))
	}
	if f.fee != "" {
		if err := positiveDecimal("--fee", f.fee); err != nil {
			return mutationError(cmd, operation, f.jsonOutput, err)
		}
	}
	orgID, walletID, at, err := resolveCreateContext(cmd, f.transactionCreateCommon)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	body := []orgreports.CreateTransaction{{SystemID: f.systemID, Time: at, AccountID: walletID, Amount: f.amount, AmountTicker: f.asset, TransactionType: typeName, BlockchainID: f.blockchainID, Fee: f.fee, FeeTicker: f.feeAsset, Memo: f.memo, Description: f.description, FromAddress: f.fromAddress, ToAddress: f.toAddress, CategoryID: f.categoryID, ContactID: f.contactID, AccountingConnectionID: f.connectionID}}
	return executeCreateTransactions(cmd, operation, orgID, body, f.transactionMutationFlags)
}

type tradeCreateFlags struct {
	transactionCreateCommon
	acquireAmount, acquireAsset, disposeAmount, disposeAsset, feeAmount, feeAsset string
}

func newCreateTradeTransactionCmd() *op.Definition {
	var f tradeCreateFlags
	cmd := &op.Definition{Use: "trade", Short: "Create an acquire/dispose trade", RunE: func(cmd *op.Call, _ []string) error { return runCreateTrade(cmd, f) }}
	addCreateCommonFlags(cmd, &f.transactionCreateCommon)
	cmd.Flags().StringVar(&f.acquireAmount, "acquire-amount", "", "Positive acquired amount (required)")
	cmd.Flags().StringVar(&f.acquireAsset, "acquire-asset", "", "Acquired asset ticker (required)")
	cmd.Flags().StringVar(&f.disposeAmount, "dispose-amount", "", "Positive disposed amount (required)")
	cmd.Flags().StringVar(&f.disposeAsset, "dispose-asset", "", "Disposed asset ticker (required)")
	cmd.Flags().StringVar(&f.feeAmount, "fee-amount", "", "Optional positive fee amount")
	cmd.Flags().StringVar(&f.feeAsset, "fee-asset", "", "Fee asset ticker; required with --fee-amount")
	return cmd
}

func runCreateTrade(cmd *op.Call, f tradeCreateFlags) error {
	operation := "create-trade"
	if err := validateCreateCommon(f.transactionCreateCommon); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	for name, value := range map[string]string{"--acquire-amount": f.acquireAmount, "--dispose-amount": f.disposeAmount} {
		if err := positiveDecimal(name, value); err != nil {
			return mutationError(cmd, operation, f.jsonOutput, err)
		}
	}
	if f.acquireAsset == "" || f.disposeAsset == "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--acquire-asset and --dispose-asset are required"))
	}
	if (f.feeAmount == "") != (f.feeAsset == "") {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--fee-amount and --fee-asset must be supplied together"))
	}
	if f.feeAmount != "" {
		if err := positiveDecimal("--fee-amount", f.feeAmount); err != nil {
			return mutationError(cmd, operation, f.jsonOutput, err)
		}
	}
	orgID, walletID, at, err := resolveCreateContext(cmd, f.transactionCreateCommon)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	base := orgreports.CreateTransaction{Time: at, AccountID: walletID, TradeID: f.systemID, Memo: f.memo, Description: f.description, FromAddress: f.fromAddress, ToAddress: f.toAddress, CategoryID: f.categoryID, ContactID: f.contactID, AccountingConnectionID: f.connectionID}
	acquire, dispose := base, base
	acquire.SystemID, acquire.Amount, acquire.AmountTicker, acquire.TransactionType = f.systemID+"-acquire", f.acquireAmount, f.acquireAsset, "tradeAcquire"
	dispose.SystemID, dispose.Amount, dispose.AmountTicker, dispose.TransactionType = f.systemID+"-dispose", f.disposeAmount, f.disposeAsset, "tradeDispose"
	body := []orgreports.CreateTransaction{acquire, dispose}
	if f.feeAmount != "" {
		fee := base
		fee.SystemID, fee.Amount, fee.AmountTicker, fee.TransactionType = f.systemID+"-fee", f.feeAmount, f.feeAsset, "tradeFee"
		body = append(body, fee)
	}
	return executeCreateTransactions(cmd, operation, orgID, body, f.transactionMutationFlags)
}

type internalTransferFlags struct {
	transactionMutationFlags
	fromWallet, toWallet, amount, asset, at, memo string
}

func newCreateInternalTransferCmd() *op.Definition {
	var f internalTransferFlags
	cmd := &op.Definition{Use: "internal-transfer", Short: "Create an internal transfer between organization wallets", RunE: func(cmd *op.Call, _ []string) error { return runCreateInternalTransfer(cmd, f) }}
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().StringVar(&f.fromWallet, "from-wallet", "", "Source wallet ID or exact name (required)")
	cmd.Flags().StringVar(&f.toWallet, "to-wallet", "", "Destination wallet ID or exact name (required)")
	cmd.Flags().StringVar(&f.amount, "amount", "", "Positive asset amount (required)")
	cmd.Flags().StringVar(&f.asset, "asset", "", "Asset ticker (required)")
	cmd.Flags().StringVar(&f.at, "at", "", "Transaction time in RFC3339 (required)")
	cmd.Flags().StringVar(&f.memo, "memo", "", "Optional memo")
	return cmd
}

func runCreateInternalTransfer(cmd *op.Call, f internalTransferFlags) error {
	operation := "create-internal-transfer"
	if f.fromWallet == "" || f.toWallet == "" || f.asset == "" || f.at == "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--from-wallet, --to-wallet, --asset, and --at are required"))
	}
	if err := positiveDecimal("--amount", f.amount); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	at, err := time.Parse(time.RFC3339, f.at)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("--at must be RFC3339: %w", err))
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	client := newReportsClient(cmd.Context(), orgID)
	wallets, err := client.Wallets(cmd.Context(), orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("discover wallets: %w", err))
	}
	fromIDs, err := resolveWalletRefs([]string{f.fromWallet}, wallets)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	toIDs, err := resolveWalletRefs([]string{f.toWallet}, wallets)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	if fromIDs[0] == toIDs[0] {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("source and destination wallets must be different"))
	}
	body := orgreports.InternalTransferInput{FromWalletID: fromIDs[0], ToWalletID: toIDs[0], Coin: f.asset, Amount: f.amount, CreatedSEC: at.Unix(), Memo: f.memo}
	preview := map[string]any{"method": "POST", "path": "/graphql", "operation": "CreateInternalTransfer", "variables": map[string]any{"orgId": orgID, "input": body}}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}
	result, err := client.CreateInternalTransfer(cmd.Context(), orgID, body)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, "created internal transfer\n")
}

func validateCreateCommon(f transactionCreateCommon) error {
	if f.wallet == "" || f.systemID == "" || f.at == "" {
		return errors.New("--wallet, --system-id, and --at are required")
	}
	provided := 0
	for _, value := range []string{f.categoryID, f.contactID, f.connectionID} {
		if value != "" {
			provided++
		}
	}
	if provided != 0 && provided != 3 {
		return errors.New("--category, --contact, and --accounting-connection must be supplied together")
	}
	return nil
}

func resolveCreateContext(cmd *op.Call, f transactionCreateCommon) (string, string, string, error) {
	at, err := time.Parse(time.RFC3339, f.at)
	if err != nil {
		return "", "", "", fmt.Errorf("--at must be RFC3339: %w", err)
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return "", "", "", err
	}
	client := newReportsClient(cmd.Context(), orgID)
	wallets, err := client.Wallets(cmd.Context(), orgID)
	if err != nil {
		return "", "", "", fmt.Errorf("discover wallets: %w", err)
	}
	ids, err := resolveWalletRefs([]string{f.wallet}, wallets)
	if err != nil {
		return "", "", "", err
	}
	return orgID, ids[0], at.Format(time.RFC3339), nil
}

func positiveDecimal(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	number, ok := new(big.Rat).SetString(value)
	if !ok || number.Sign() <= 0 {
		return fmt.Errorf("%s must be a positive decimal string", name)
	}
	return nil
}

func executeCreateTransactions(cmd *op.Call, operation, orgID string, body []orgreports.CreateTransaction, f transactionMutationFlags) error {
	preview := map[string]any{"method": "POST", "path": fmt.Sprintf("/txns/orgs/%s/transactions?immediate=true", orgID), "body": body}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}
	client := newReportsClient(cmd.Context(), orgID)
	result, err := client.CreateTransactions(cmd.Context(), orgID, body)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	var decoded any
	if err := json.Unmarshal(result, &decoded); err != nil {
		decoded = result
	}
	return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: decoded}, fmt.Sprintf("created %d transaction record(s)\n", len(body)))
}
