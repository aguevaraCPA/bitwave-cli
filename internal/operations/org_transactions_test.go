package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestIgnoreDryRunDoesNotRequireConfirmation(t *testing.T) {
	cmd := testDefinition(newTransactionStateCmd("ignore", "ignore"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"txn-1", "txn-1", "txn-2", "--org", "org-1", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var envelope mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("output = %s err=%v", out.String(), err)
	}
	if envelope.Status != "preview" || envelope.Operation != "ignore" || !envelope.DryRun {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestBulkCategorizationTypedBody(t *testing.T) {
	body, err := bulkCategorizationBody(context.Background(), bulkCategorizeFlags{
		kind: "multivalue", transactionIDs: []string{"txn-1", "txn-1"}, accountingConnectionID: "ac-1",
		feeContactID: "contact-fee", feeCategoryID: "category-fee",
		sendContactID: "contact-send", sendCategoryID: "category-send",
		receiveContactID: "contact-receive", receiveCategoryID: "category-receive", overwrite: true,
	}, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Categorization struct {
			AccountingConnectionID string `json:"accountingConnectionId"`
			Multivalue             struct {
				TxnIDs []string `json:"txnIds"`
			} `json:"multivalue"`
		} `json:"categorization"`
		Options struct {
			Overwrite bool `json:"overwriteExistingCategorization"`
		} `json:"options"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Categorization.AccountingConnectionID != "ac-1" || len(decoded.Categorization.Multivalue.TxnIDs) != 1 || !decoded.Options.Overwrite {
		t.Fatalf("body = %s", body)
	}
}

func TestSingleCategorizationValidation(t *testing.T) {
	if err := validateSingleCategorization(json.RawMessage(`{"type":"trade","categorizationMethod":1,"accountingConnectionId":"ac-1","exchangeRates":[],"exchangeRateVersion":0}`)); err != nil {
		t.Fatal(err)
	}
	if err := validateSingleCategorization(json.RawMessage(`{"type":"trade","categorizationMethod":1,"exchangeRates":[],"exchangeRateVersion":0}`)); err == nil {
		t.Fatal("expected missing connection error")
	}
	if err := validateSingleCategorization(json.RawMessage(`{"type":"made-up","categorizationMethod":1,"accountingConnectionId":"ac-1","exchangeRates":[],"exchangeRateVersion":0}`)); err == nil {
		t.Fatal("expected unsupported type error")
	}
}

func TestCategorizationOptionFilters(t *testing.T) {
	categories := filterCategories([]orgreports.Category{
		{ID: "cat-1", Name: "Staking Revenue", Code: "4000", Enabled: true, AccountingConnectionID: "ac-1"},
		{ID: "cat-2", Name: "Staking Fees", Enabled: false, AccountingConnectionID: "ac-1"},
		{ID: "cat-3", Name: "Staking Revenue", Enabled: true, AccountingConnectionID: "ac-2"},
	}, "staking", "ac-1", false)
	if len(categories) != 1 || categories[0].ID != "cat-1" {
		t.Fatalf("categories = %#v", categories)
	}
	contacts := filterContacts([]orgreports.Contact{
		{ID: "contact-1", Name: "Validator", Enabled: true, AccountingConnectionID: "ac-1"},
		{ID: "contact-2", Name: "Validator", Enabled: true, AccountingConnectionID: "ac-2"},
	}, "valid", "ac-2", false)
	if len(contacts) != 1 || contacts[0].ID != "contact-2" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestCreateValidation(t *testing.T) {
	for _, value := range []string{"1", "0.000000000000000001", "123.45"} {
		if err := positiveDecimal("--amount", value); err != nil {
			t.Fatalf("%s: %v", value, err)
		}
	}
	for _, value := range []string{"", "0", "-1", "not-a-number"} {
		if err := positiveDecimal("--amount", value); err == nil {
			t.Fatalf("expected %q to fail", value)
		}
	}
	if err := validateCreateCommon(transactionCreateCommon{wallet: "Wallet", systemID: "llm-123", at: "2026-08-10T10:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := validateCreateCommon(transactionCreateCommon{wallet: "Wallet", systemID: "llm-123", at: "2026-08-10T10:00:00Z", categoryID: "cat"}); err == nil {
		t.Fatal("expected incomplete categorization tuple to fail")
	}
}

func TestCompactTransactionsBoundsLines(t *testing.T) {
	items := []json.RawMessage{json.RawMessage(`{"id":"txn-1","transactionType":"Receive","ignored":false,"lines":[{"line":0,"from":"0xabc"},{"line":1},{"line":2},{"line":3},{"line":4},{"line":5}]}`)}
	result := compactTransactions(items)
	if len(result) != 1 || result[0].ID != "txn-1" || result[0].LineCount != 6 || len(result[0].Lines) != 5 {
		t.Fatalf("result = %#v", result)
	}
}

func TestCompactTransactionAccountingDetailsOmitsLegacyNoise(t *testing.T) {
	view, err := compactTransactionAccountingDetails(json.RawMessage(`{"id":"txn-1","categorizationStatus":"Categorized","hasMatchedInvoices":false,"memo":"invoice test","accountingDetails":[{"invoice":{"invoices":[{"invoiceId":"inv-1"}]}}],"org":{"name":"must not leak into compact output"}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"invoiceId":"inv-1"`) || strings.Contains(string(encoded), "must not leak") {
		t.Fatalf("view = %s", encoded)
	}
}

func TestAnalyzeTransactionReviewReportsEquivalentFacts(t *testing.T) {
	raw := json.RawMessage(`{
  "state": {
    "state": "open-needs-review",
    "txn": {
      "transactionId": "BSC.0xabc",
      "txnType": "trade",
      "walletIds": ["wallet-1"],
      "txnLines": [
        {"operation":"DEPOSIT","amount":{"currencyId":"USDC","value":"4.0"},"walletId":"wallet-1","from":"from-a","to":"to-a","exchangeRate":{"from":"USDC","to":"USD","rate":"1.00"}},
        {"operation":"WITHDRAW","amount":{"currencyId":"TOKEN","value":"2"},"walletId":"wallet-1","from":"from-b","to":"to-b","exchangeRate":{"from":"TOKEN","to":"USD","rate":"2"}}
      ]
    },
    "needsReview": {
      "pendingWalletIds": ["wallet-1"],
      "pendingTxnLines": [
        {"operation":"WITHDRAW","amount":{"currencyId":"TOKEN","value":"2.0"},"walletId":"wallet-1","from":"from-b","to":"to-b","exchangeRate":{"from":"TOKEN","to":"USD","rate":"2.00"}},
        {"operation":"DEPOSIT","amount":{"currencyId":"USDC","value":"4"},"walletId":"wallet-1","from":"from-a","to":"to-a","exchangeRate":{"from":"USDC","to":"USD","rate":"1"}}
      ],
      "pendingExchangeRates": [
        {"changeReason":"new-pricing-provided","changeAuditLog":"New pricing.","exchangeRate":{"from":"USDC","to":"USD","rate":"1"}},
        {"changeReason":"new-pricing-provided","changeAuditLog":"New pricing.","exchangeRate":{"from":"TOKEN","to":"USD","rate":"2.0"}}
      ]
    },
    "categorization": {
      "categorizationMethod": "rule",
      "categorizeByRuleId": "rule-1",
      "categorizationDetails": {"type":"trade"}
    }
  }
}`)
	analysis, err := analyzeTransactionReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.LinesEquivalent || !analysis.RatesEquivalent || !analysis.WalletsEquivalent {
		t.Fatalf("analysis = %#v", analysis)
	}
	if analysis.CategorizationType != "trade" || analysis.RuleID != "rule-1" {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestAnalyzeTransactionReviewReportsRateChange(t *testing.T) {
	raw := json.RawMessage(`{
  "state": {
    "state":"open-needs-review",
    "txn":{"transactionId":"BSC.0xdef","walletIds":["wallet-1"],"txnLines":[{"operation":"DEPOSIT","amount":{"currencyId":"TOKEN","value":"1"},"walletId":"wallet-1","exchangeRate":{"from":"TOKEN","to":"USD","rate":"2"}}]},
    "needsReview":{"pendingTxnLines":[{"operation":"DEPOSIT","amount":{"currencyId":"TOKEN","value":"1"},"walletId":"wallet-1","exchangeRate":{"from":"TOKEN","to":"USD","rate":"3"}}],"pendingExchangeRates":[{"changeReason":"new-pricing-provided","exchangeRate":{"from":"TOKEN","to":"USD","rate":"3"}}]}
  }
}`)
	analysis, err := analyzeTransactionReview(raw)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.RatesEquivalent || analysis.LinesEquivalent {
		t.Fatalf("analysis = %#v", analysis)
	}
}

func TestTransactionReviewIgnoreDryRunUsesResolveEndpoint(t *testing.T) {
	cmd := testDefinition(newTransactionReviewResolveCmd("ignore", orgreports.TransactionReviewIgnore))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"BSC.0xabc", "--org", "org-1", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `action=ignore-pending-changes`) || !strings.Contains(out.String(), `"transactionId": "BSC.0xabc"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestTransactionCountRangesPreserveZeroBounds(t *testing.T) {
	cmd := testDefinition(newCountOrgTransactionsCmd())
	if err := cmd.Flags().Set("amount-min", "0"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("amount-max", "10.5"); err != nil {
		t.Fatal(err)
	}
	rangeValue, err := transactionCountRange(businessTestCall(t, cmd), "amount-min", "0", "amount-max", "10.5")
	if err != nil {
		t.Fatal(err)
	}
	if rangeValue == nil || rangeValue.From == nil || *rangeValue.From != 0 || rangeValue.To == nil || *rangeValue.To != 10.5 {
		t.Fatalf("range = %#v", rangeValue)
	}
}
