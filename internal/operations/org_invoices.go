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

func newOrgInvoicesCmd() *op.Definition {
	cmd := &op.Definition{
		Use:     "invoice",
		Aliases: []string{"invoices", "bill", "bills"},
		Short:   "Find Bitwave invoices and bills for transaction categorization",
		Long: `Find invoices and bills imported from an accounting connection.

Invoice discovery is contact-scoped, matching the Bitwave transaction UI: select
an accounting connection and contact first, then list the eligible AR invoices
for an inflow or AP bills for an outflow. Use the returned stable IDs in an
invoice-v2 transaction categorization payload.`,
	}
	cmd.AddCommand(newOrgInvoiceListCmd(), newOrgInvoiceGetCmd(), newOrgInvoiceCategorizeCmd())
	return cmd
}

func newOrgInvoiceListCmd() *op.Definition {
	var orgID, contactID, connectionID, direction, status, pageToken, invoiceNumber string
	var limit int
	var includeDisabled bool
	cmd := &op.Definition{
		Use:   "list",
		Short: "List contact-scoped invoices or bills eligible for categorization",
		Args:  op.NoArgs,
		RunE: func(cmd *op.Call, _ []string) error {
			contactID = strings.TrimSpace(contactID)
			if contactID == "" {
				return errors.New("--contact is required; select a contact before loading its invoices")
			}
			if limit <= 0 || limit > 100 {
				return errors.New("--limit must be between 1 and 100")
			}
			invoiceType, err := invoiceTypeForDirection(direction)
			if err != nil {
				return err
			}
			invoiceStatus, err := invoiceStatusValue(status)
			if err != nil {
				return err
			}
			connectionID = strings.TrimSpace(connectionID)
			if connectionID != "" {
				contactConnectionID := invoiceResourceConnectionID(contactID)
				if contactConnectionID != "" && contactConnectionID != connectionID {
					return fmt.Errorf("contact %q belongs to accounting connection %q, not %q", contactID, contactConnectionID, connectionID)
				}
			}

			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			if strings.TrimSpace(invoiceNumber) != "" {
				invoice, err := client.FindInvoiceForContact(cmd.Context(), resolvedOrg, orgreports.FindInvoiceInput{
					ContactID: contactID, Title: invoiceNumber, PageSize: 100, IncludeDisabled: includeDisabled,
				})
				if err != nil {
					return fmt.Errorf("find invoice %q for contact %s: %w", invoiceNumber, contactID, err)
				}
				invoices := filterInvoices([]orgreports.Invoice{*invoice}, connectionID, invoiceType, invoiceStatus)
				return writeJSON(cmd.OutOrStdout(), map[string]any{
					"schemaVersion": "1", "organization": resolvedOrg, "contactId": contactID,
					"accountingConnectionId": connectionID, "direction": normalizedInvoiceDirection(direction), "status": normalizedInvoiceStatus(status),
					"invoiceNumber": invoiceNumber, "count": len(invoices), "invoices": invoices,
					"warning": "The exact invoice number was searched only within the selected contact, matching the Bitwave UI workflow.",
				})
			}

			page, err := client.Invoices(cmd.Context(), resolvedOrg, orgreports.InvoiceListInput{
				ContactID: contactID, PageToken: strings.TrimSpace(pageToken), PageSize: limit, IncludeDisabled: includeDisabled,
			})
			if err != nil {
				return fmt.Errorf("list invoices for contact %s: %w", contactID, err)
			}
			invoices := filterInvoices(page.Records, connectionID, invoiceType, invoiceStatus)
			return writeJSON(cmd.OutOrStdout(), map[string]any{
				"schemaVersion": "1", "organization": resolvedOrg, "contactId": contactID,
				"accountingConnectionId": connectionID, "direction": normalizedInvoiceDirection(direction), "status": normalizedInvoiceStatus(status),
				"count": len(invoices), "invoices": invoices, "previousPageToken": page.PreviousPageToken, "nextPageToken": page.NextPageToken,
				"warning": "Only invoices for the selected contact are returned. Match inflows to Receiving invoices and outflows to Paying bills.",
			})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&contactID, "contact", "", "Exact Bitwave contact ID (required)")
	cmd.Flags().StringVar(&connectionID, "accounting-connection", "", "Expected accounting connection ID")
	cmd.Flags().StringVar(&direction, "direction", "all", "Transaction direction: inflow, outflow, or all")
	cmd.Flags().StringVar(&status, "status", "awaiting-payment", "Invoice status: awaiting-payment, paid, draft, other, or all")
	cmd.Flags().StringVar(&invoiceNumber, "invoice-number", "", "Exact invoice number/title; automatically follows contact-scoped pagination")
	cmd.Flags().StringVar(&pageToken, "next-token", "", "Opaque next-page token from the previous response")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum remote records to inspect (1-100)")
	cmd.Flags().BoolVar(&includeDisabled, "include-disabled", false, "Include disabled invoices")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

type invoiceCategorizeFlags struct {
	transactionMutationFlags
	invoiceNumber string
	contactID     string
	contactQuery  string
	network       string
	paidAmount    string
	memo          string
	force         bool
}

func newOrgInvoiceCategorizeCmd() *op.Definition {
	var f invoiceCategorizeFlags
	cmd := &op.Definition{
		Use:     "categorize TRANSACTION_ID",
		Aliases: []string{"categorise", "match"},
		Short:   "Find an invoice through its contact and categorize a transaction",
		Long: `Find and apply an invoice or bill using the same contact-first workflow as the Bitwave UI.

The command resolves a contact, searches only that contact's imported invoices,
loads the transaction's authoritative wallet, asset, pricing, and price-version
state, validates the match, and constructs the complete invoice-v2 payload.
External agents do not need to know Bitwave's internal categorization schema.

Use --contact with a stable Bitwave contact ID when known. Otherwise use
--contact-query with a contact name; exactly one contact must resolve. Run with
--dry-run first to inspect the invoice, contact, transaction, and request. A
write requires --yes.

The typed workflow intentionally refuses ambiguous contacts, multiple payment
lines, partial allocations, missing pricing, and foreign-currency allocations
instead of guessing.`,
		Args: op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			return runInvoiceCategorize(cmd, strings.TrimSpace(args[0]), f)
		},
	}
	addMutationFlags(cmd, &f.transactionMutationFlags)
	cmd.Flags().StringVar(&f.invoiceNumber, "invoice-number", "", "Exact invoice number/title (required)")
	cmd.Flags().StringVar(&f.contactID, "contact", "", "Exact Bitwave contact ID")
	cmd.Flags().StringVar(&f.contactQuery, "contact-query", "", "Contact name or ID query; must resolve uniquely")
	cmd.Flags().StringVar(&f.network, "network", "", "Bitwave network prefix for an unqualified transaction hash (Solana signatures are detected automatically)")
	cmd.Flags().StringVar(&f.paidAmount, "paid-amount", "", "Invoice-currency payment amount (defaults to transaction value when it fully fits the invoice)")
	cmd.Flags().StringVar(&f.memo, "memo", "", "Categorization memo (defaults to the invoice number)")
	cmd.Flags().BoolVar(&f.force, "force", false, "Replace an existing transaction categorization")
	return cmd
}

type invoiceCategorizationResolution struct {
	Contact                 orgreports.Contact                       `json:"contact"`
	Invoice                 orgreports.Invoice                       `json:"invoice"`
	TransactionID           string                                   `json:"transactionId"`
	TransactionState        string                                   `json:"transactionState"`
	TransactionType         string                                   `json:"transactionType"`
	TransactionPriceVersion int                                      `json:"transactionPriceVersion"`
	SourceLine              orgreports.TransactionCategorizationLine `json:"sourceLine"`
	ContactSearchMode       string                                   `json:"contactSearchMode,omitempty"`
}

func runInvoiceCategorize(cmd *op.Call, transactionID string, f invoiceCategorizeFlags) error {
	const operation = "invoice-categorize"
	transactionID, err := normalizeTransactionID(transactionID, f.network)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	f.invoiceNumber = strings.TrimSpace(f.invoiceNumber)
	if f.invoiceNumber == "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("--invoice-number is required"))
	}
	if strings.TrimSpace(f.contactID) == "" && strings.TrimSpace(f.contactQuery) == "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("use --contact with an exact Bitwave contact ID or --contact-query with a contact name"))
	}
	if strings.TrimSpace(f.contactID) != "" && strings.TrimSpace(f.contactQuery) != "" {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("use exactly one of --contact or --contact-query"))
	}

	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	client := newReportsClient(cmd.Context(), orgID)
	contact, searchMode, err := resolveInvoiceContact(cmd, client, orgID, f.contactID, f.contactQuery)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	invoice, err := client.FindInvoiceForContact(cmd.Context(), orgID, orgreports.FindInvoiceInput{
		ContactID: contact.ID, Title: f.invoiceNumber, PageSize: 100,
	})
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("find invoice %q for contact %s: %w", f.invoiceNumber, contact.ID, err))
	}
	context, err := client.TransactionCategorizationContext(cmd.Context(), orgID, transactionID)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("get transaction categorization context: %w", err))
	}
	body, resolution, err := buildInvoiceCategorizationBody(transactionID, contact, invoice, context, f)
	if err != nil {
		return mutationError(cmd, operation, f.jsonOutput, err)
	}
	resolution.ContactSearchMode = searchMode
	preview := map[string]any{
		"method": "PATCH", "path": fmt.Sprintf("/v3/orgs/%s/transactions/%s", orgID, transactionID),
		"body": body, "resolution": resolution,
	}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation, Organization: orgID, DryRun: true, Request: preview})
	}
	if !f.yes {
		return mutationError(cmd, operation, f.jsonOutput, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}
	if err := client.CategorizeTransaction(cmd.Context(), orgID, transactionID, body); err != nil {
		return mutationError(cmd, operation, f.jsonOutput, fmt.Errorf("categorize transaction %s to invoice %s: %w", transactionID, invoice.Title, err))
	}
	result := map[string]any{
		"transactionId": transactionID, "invoiceId": invoice.ID, "invoiceNumber": invoice.Title,
		"contactId": contact.ID, "accountingConnectionId": invoice.AccountingConnectionID,
	}
	return outputMutation(cmd, f.jsonOutput, mutationEnvelope{SchemaVersion: "1", Status: "success", Operation: operation, Organization: orgID, Result: result}, fmt.Sprintf("categorized transaction %s to invoice %s\n", transactionID, invoice.Title))
}

func resolveInvoiceContact(cmd *op.Call, client *orgreports.Client, orgID, contactID, query string) (orgreports.Contact, string, error) {
	contactID = strings.TrimSpace(contactID)
	if contactID != "" {
		return orgreports.Contact{ID: contactID, AccountingConnectionID: invoiceResourceConnectionID(contactID), Enabled: true}, "exact-id", nil
	}
	query = strings.TrimSpace(query)
	contacts, indexedErr := client.SearchContacts(cmd.Context(), orgID, query, 25)
	mode := "indexed-name-search"
	if indexedErr != nil || len(contacts) == 0 {
		all, err := client.Contacts(cmd.Context(), orgID)
		if err != nil {
			if indexedErr != nil {
				return orgreports.Contact{}, "", fmt.Errorf("search contacts: %v; fallback list: %w", indexedErr, err)
			}
			return orgreports.Contact{}, "", fmt.Errorf("list contacts after indexed search returned no matches: %w", err)
		}
		needle := strings.ToLower(query)
		for _, contact := range all {
			haystack := strings.ToLower(strings.Join([]string{contact.ID, contact.RemoteID, contact.Name}, " "))
			if contact.Enabled && strings.Contains(haystack, needle) {
				contacts = append(contacts, contact)
			}
		}
		mode = "paginated-fallback"
	}
	return uniqueInvoiceContact(query, contacts, mode)
}

func uniqueInvoiceContact(query string, contacts []orgreports.Contact, mode string) (orgreports.Contact, string, error) {
	if len(contacts) == 0 {
		return orgreports.Contact{}, mode, fmt.Errorf("no enabled contact matched %q", query)
	}
	exact := make([]orgreports.Contact, 0)
	for _, contact := range contacts {
		if strings.EqualFold(strings.TrimSpace(contact.Name), strings.TrimSpace(query)) || strings.EqualFold(contact.ID, query) || strings.EqualFold(contact.RemoteID, query) {
			exact = append(exact, contact)
		}
	}
	if len(exact) == 1 {
		return exact[0], mode, nil
	}
	if len(contacts) == 1 {
		return contacts[0], mode, nil
	}
	sort.Slice(contacts, func(i, j int) bool { return contacts[i].Name < contacts[j].Name })
	choices := make([]string, 0, min(len(contacts), 10))
	for i, contact := range contacts {
		if i == 10 {
			break
		}
		choices = append(choices, fmt.Sprintf("%s (%s)", contact.Name, contact.ID))
	}
	return orgreports.Contact{}, mode, fmt.Errorf("contact query %q is ambiguous; use --contact with one exact ID. Choices: %s", query, strings.Join(choices, ", "))
}

func buildInvoiceCategorizationBody(transactionID string, contact orgreports.Contact, invoice *orgreports.Invoice, context *orgreports.TransactionCategorizationContext, f invoiceCategorizeFlags) (json.RawMessage, invoiceCategorizationResolution, error) {
	var resolution invoiceCategorizationResolution
	if context.State.Transaction.TransactionID != transactionID {
		return nil, resolution, fmt.Errorf("transaction context returned %q, expected %q", context.State.Transaction.TransactionID, transactionID)
	}
	if len(context.State.Categorization) > 0 && string(context.State.Categorization) != "null" && !f.force {
		return nil, resolution, errors.New("transaction is already categorized; pass --force to replace it")
	}
	if !invoice.Enabled || !strings.EqualFold(invoice.Status, "AwaitingPayment") {
		return nil, resolution, fmt.Errorf("invoice %s is not an enabled AwaitingPayment invoice", invoice.Title)
	}
	if invoice.ContactID == "" || invoice.ContactID != contact.ID {
		return nil, resolution, fmt.Errorf("invoice contact %q does not match selected contact %q", invoice.ContactID, contact.ID)
	}
	connectionID := invoice.AccountingConnectionID
	if connectionID == "" {
		connectionID = invoiceResourceConnectionID(invoice.ID)
	}
	if connectionID == "" || invoiceResourceConnectionID(contact.ID) != connectionID {
		return nil, resolution, errors.New("invoice and contact do not belong to the same accounting connection")
	}

	lines := make([]orgreports.TransactionCategorizationLine, 0)
	for _, line := range context.State.Transaction.Lines {
		if !strings.EqualFold(line.Operation, "FEE") && line.Amount.Value != "0" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		return nil, resolution, fmt.Errorf("typed invoice categorization currently requires exactly one non-fee transaction line; found %d", len(lines))
	}
	line := lines[0]
	invoiceType := "Invoice"
	if strings.EqualFold(invoice.Type, "Receiving") {
		if !strings.EqualFold(line.Operation, "DEPOSIT") {
			return nil, resolution, fmt.Errorf("receiving invoice %s requires an inflow/deposit transaction line", invoice.Title)
		}
	} else if strings.EqualFold(invoice.Type, "Paying") {
		invoiceType = "Bill"
		if !strings.EqualFold(line.Operation, "WITHDRAW") {
			return nil, resolution, fmt.Errorf("paying bill %s requires an outflow/withdraw transaction line", invoice.Title)
		}
	} else {
		return nil, resolution, fmt.Errorf("unsupported invoice type %q", invoice.Type)
	}
	if context.State.Price.TransactionPriceVersion == nil {
		return nil, resolution, errors.New("transaction pricing does not include transactionPriceVersion")
	}
	rate := line.ExchangeRate
	if rate == nil {
		if value, ok := context.State.Price.ExchangeRates[line.Amount.CurrencyID]; ok {
			rate = &value
		}
	}
	if rate == nil || rate.From == "" || rate.To == "" || rate.Type == "" || rate.Rate == "" {
		return nil, resolution, fmt.Errorf("transaction line %d is missing a complete exchange rate", line.TxnLineID)
	}
	if line.Value.CurrencyID == "" || line.Value.Value == "" {
		return nil, resolution, fmt.Errorf("transaction line %d is missing its accounting value", line.TxnLineID)
	}
	paidAssetID := ""
	for _, asset := range context.Assets {
		if strings.EqualFold(asset.Ticker, invoice.Currency) {
			paidAssetID = asset.CurrencyID
			break
		}
	}
	if paidAssetID == "" {
		return nil, resolution, fmt.Errorf("invoice currency %q was not present in the transaction asset context", invoice.Currency)
	}
	if paidAssetID != line.Value.CurrencyID {
		return nil, resolution, fmt.Errorf("invoice currency asset %s differs from transaction accounting-value asset %s; use the full JSON categorization command for foreign-currency allocation", paidAssetID, line.Value.CurrencyID)
	}
	paidAmount := strings.TrimSpace(f.paidAmount)
	if paidAmount == "" {
		paidAmount = line.Value.Value
	}
	if !equalDecimal(paidAmount, line.Value.Value) {
		return nil, resolution, fmt.Errorf("paid amount %s does not fully allocate transaction value %s; split/multiple invoice allocation requires the full JSON categorization command", paidAmount, line.Value.Value)
	}
	due, err := invoice.DueAmount.Float64()
	if err != nil || due <= 0 {
		return nil, resolution, fmt.Errorf("invoice %s has invalid due amount %q", invoice.Title, invoice.DueAmount)
	}
	paid, ok := new(big.Rat).SetString(paidAmount)
	if !ok || paid.Sign() <= 0 {
		return nil, resolution, fmt.Errorf("invalid paid amount %q", paidAmount)
	}
	dueRat, ok := new(big.Rat).SetString(invoice.DueAmount.String())
	if !ok || paid.Cmp(dueRat) > 0 {
		return nil, resolution, fmt.Errorf("payment amount %s exceeds invoice due amount %s", paidAmount, invoice.DueAmount.String())
	}

	exchangeRates := make([]map[string]any, 0, len(context.State.Price.ExchangeRates))
	keys := make([]string, 0, len(context.State.Price.ExchangeRates))
	for key := range context.State.Price.ExchangeRates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		x := context.State.Price.ExchangeRates[key]
		item := map[string]any{"fromAssetId": x.From, "toAssetId": x.To, "source": x.Type}
		if x.Rate != "" {
			item["rate"] = x.Rate
		}
		if x.PriceID != "" {
			item["priceId"] = x.PriceID
		}
		if x.Type == "manually-priced" {
			item["note"] = x.Reason
		}
		exchangeRates = append(exchangeRates, item)
	}
	if len(exchangeRates) == 0 {
		return nil, resolution, errors.New("transaction does not contain exchange rates")
	}
	memo := strings.TrimSpace(f.memo)
	if memo == "" {
		memo = "Invoice " + invoice.Title
	}
	bodyObject := map[string]any{
		"type": "invoice-v2", "categorizationMethod": 1, "forceCategorize": f.force,
		"accountingConnectionId": connectionID, "exchangeRates": exchangeRates,
		"exchangeRateVersion": *context.State.Price.TransactionPriceVersion,
		"invoices": []map[string]any{{
			"contactId": contact.ID, "invoiceId": invoice.ID,
			"paid":            map[string]string{"assetId": paidAssetID, "amount": paidAmount},
			"source":          map[string]string{"assetId": line.Amount.CurrencyID, "amount": line.Amount.Value},
			"forexCategoryId": "", "walletId": line.WalletID,
		}},
		"fees": []any{}, "invoiceType": invoiceType, "totalFmv": line.Value.Value, "memo": memo,
	}
	body, err := json.Marshal(bodyObject)
	if err != nil {
		return nil, resolution, err
	}
	resolution = invoiceCategorizationResolution{
		Contact: contact, Invoice: *invoice, TransactionID: transactionID,
		TransactionState: context.State.State, TransactionType: context.State.Transaction.TransactionType,
		TransactionPriceVersion: *context.State.Price.TransactionPriceVersion, SourceLine: line,
	}
	return body, resolution, nil
}

func equalDecimal(left, right string) bool {
	l, ok := new(big.Rat).SetString(strings.TrimSpace(left))
	if !ok {
		return false
	}
	r, ok := new(big.Rat).SetString(strings.TrimSpace(right))
	return ok && l.Cmp(r) == 0
}

func newOrgInvoiceGetCmd() *op.Definition {
	var orgID string
	cmd := &op.Definition{
		Use:   "get INVOICE_ID",
		Short: "Get one imported invoice or bill",
		Args:  op.ExactArgs(1),
		RunE: func(cmd *op.Call, args []string) error {
			resolvedOrg, err := resolveReportOrg(cmd.Context(), orgID)
			if err != nil {
				return err
			}
			client := newReportsClient(cmd.Context(), resolvedOrg)
			invoice, err := client.Invoice(cmd.Context(), resolvedOrg, strings.TrimSpace(args[0]))
			if err != nil {
				return fmt.Errorf("get invoice %s: %w", args[0], err)
			}
			return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "organization": resolvedOrg, "invoice": invoice})
		},
	}
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID override")
	cmd.Flags().Bool("json", true, "Emit machine-readable JSON (the only supported format)")
	return cmd
}

func filterInvoices(items []orgreports.Invoice, connectionID, invoiceType, status string) []orgreports.Invoice {
	result := make([]orgreports.Invoice, 0, len(items))
	for _, item := range items {
		if connectionID != "" && item.AccountingConnectionID != connectionID {
			continue
		}
		if invoiceType != "" && !strings.EqualFold(item.Type, invoiceType) {
			continue
		}
		if status != "" && !strings.EqualFold(item.Status, status) {
			continue
		}
		if status == "AwaitingPayment" {
			due, err := item.DueAmount.Float64()
			if err != nil || due <= 0 {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

func invoiceTypeForDirection(value string) (string, error) {
	switch normalizedInvoiceDirection(value) {
	case "inflow":
		return "Receiving", nil
	case "outflow":
		return "Paying", nil
	case "all":
		return "", nil
	default:
		return "", errors.New("--direction must be inflow, outflow, or all")
	}
}

func normalizedInvoiceDirection(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inflow", "receive", "receiving", "deposit":
		return "inflow"
	case "outflow", "send", "paying", "withdraw":
		return "outflow"
	case "", "all":
		return "all"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func invoiceStatusValue(value string) (string, error) {
	switch normalizedInvoiceStatus(value) {
	case "awaiting-payment":
		return "AwaitingPayment", nil
	case "paid":
		return "Paid", nil
	case "draft":
		return "Draft", nil
	case "other":
		return "Other", nil
	case "all":
		return "", nil
	default:
		return "", errors.New("--status must be awaiting-payment, paid, draft, other, or all")
	}
}

func normalizedInvoiceStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "-")
	switch value {
	case "", "awaitingpayment", "awaiting-payment", "unpaid":
		return "awaiting-payment"
	default:
		return value
	}
}

func invoiceResourceConnectionID(id string) string {
	if index := strings.IndexByte(id, '.'); index > 0 {
		return id[:index]
	}
	return ""
}
