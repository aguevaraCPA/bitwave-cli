package operations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestFilterInvoicesMatchesUIDropdownEligibility(t *testing.T) {
	items := []orgreports.Invoice{
		{ID: "ac-1.invoice-1", AccountingConnectionID: "ac-1", Type: "Receiving", Status: "AwaitingPayment", DueAmount: json.Number("12.34")},
		{ID: "ac-1.invoice-2", AccountingConnectionID: "ac-1", Type: "Receiving", Status: "Paid", DueAmount: json.Number("0")},
		{ID: "ac-1.bill-1", AccountingConnectionID: "ac-1", Type: "Paying", Status: "AwaitingPayment", DueAmount: json.Number("50")},
		{ID: "ac-2.invoice-1", AccountingConnectionID: "ac-2", Type: "Receiving", Status: "AwaitingPayment", DueAmount: json.Number("10")},
	}
	result := filterInvoices(items, "ac-1", "Receiving", "AwaitingPayment")
	if len(result) != 1 || result[0].ID != "ac-1.invoice-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestInvoiceDirectionAndStatusAliases(t *testing.T) {
	if value, err := invoiceTypeForDirection("deposit"); err != nil || value != "Receiving" {
		t.Fatalf("direction = %q err=%v", value, err)
	}
	if value, err := invoiceStatusValue("unpaid"); err != nil || value != "AwaitingPayment" {
		t.Fatalf("status = %q err=%v", value, err)
	}
	if _, err := invoiceTypeForDirection("sideways"); err == nil {
		t.Fatal("expected invalid direction")
	}
}

func TestInvoiceCommandRequiresContact(t *testing.T) {
	cmd := testDefinition(newOrgInvoiceListCmd())
	cmd.SetArgs([]string{"--org", "org-1"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected --contact validation error")
	}
}

func TestUniqueInvoiceContactRequiresUnambiguousSelection(t *testing.T) {
	contacts := []orgreports.Contact{
		{ID: "ac-1.one", Name: "Acme"},
		{ID: "ac-1.two", Name: "Acme Labs"},
	}
	contact, _, err := uniqueInvoiceContact("Acme", contacts, "test")
	if err != nil || contact.ID != "ac-1.one" {
		t.Fatalf("contact=%#v err=%v", contact, err)
	}
	if _, _, err := uniqueInvoiceContact("Ac", contacts, "test"); err == nil {
		t.Fatal("expected ambiguous contact error")
	}
}

func TestBuildInvoiceCategorizationBodyUsesAuthoritativeTransactionState(t *testing.T) {
	version := 3
	context := &orgreports.TransactionCategorizationContext{}
	context.State.State = "priced"
	context.State.Price.TransactionPriceVersion = &version
	context.State.Price.ExchangeRates = map[string]orgreports.TransactionExchangeRate{
		"COIN.10": {ID: "COIN.10", From: "COIN.10", To: "FIAT.1", Type: "successfully-priced", Rate: "2", PriceID: "price-1"},
	}
	context.State.Transaction.TransactionID = "txn-1"
	context.State.Transaction.TransactionType = "receive"
	context.State.Transaction.Lines = []orgreports.TransactionCategorizationLine{{
		TxnLineID: 0, Operation: "DEPOSIT", WalletID: "wallet-1",
		Amount: orgreports.TransactionAssetValue{CurrencyID: "COIN.10", Value: "5"},
		Value:  orgreports.TransactionAssetValue{CurrencyID: "FIAT.1", Value: "10"},
	}}
	context.Assets = append(context.Assets, struct {
		CurrencyID string `json:"currencyId"`
		Ticker     string `json:"ticker"`
		Unit       string `json:"unit,omitempty"`
	}{CurrencyID: "FIAT.1", Ticker: "USD"})
	contact := orgreports.Contact{ID: "ac-1.contact-1", Name: "Customer"}
	invoice := &orgreports.Invoice{
		ID: "ac-1.invoice-1", Title: "INV-1", Type: "Receiving", Status: "AwaitingPayment",
		DueAmount: json.Number("10"), Currency: "USD", Enabled: true, ContactID: contact.ID, AccountingConnectionID: "ac-1",
	}
	body, resolution, err := buildInvoiceCategorizationBody("txn-1", contact, invoice, context, invoiceCategorizeFlags{})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["type"] != "invoice-v2" || decoded["exchangeRateVersion"] != float64(3) || resolution.SourceLine.WalletID != "wallet-1" {
		t.Fatalf("body=%s resolution=%#v", body, resolution)
	}
}

func TestBuildInvoiceCategorizationBodyRefusesOverwriteWithoutForce(t *testing.T) {
	context := &orgreports.TransactionCategorizationContext{}
	context.State.Transaction.TransactionID = "txn-1"
	context.State.Categorization = json.RawMessage(`{"type":"invoice"}`)
	_, _, err := buildInvoiceCategorizationBody("txn-1", orgreports.Contact{}, &orgreports.Invoice{}, context, invoiceCategorizeFlags{})
	if err == nil || !strings.Contains(err.Error(), "already categorized") {
		t.Fatalf("err = %v", err)
	}
}
