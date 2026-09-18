package operations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestImportAutoRecommendsDirectForCleanCanonicalRows(t *testing.T) {
	file := writeImportModeCSV(t, `id,amount,amountTicker,time,transactionType,accountId,blockchainId,fromAddress,toAddress,fee,feeTicker
txn-1,1.25,ETH,2026-08-26T07:37:35Z,withdrawal,wallet-1,0xhash,0xfrom,0xto,0.001,ETH
`)
	analysis, err := analyzeTransactionImport(businessFileContext(t, file), file, importModeAuto, []orgreports.Wallet{{ID: "wallet-1", Name: "Manual wallet"}})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.plan.RecommendedMode != importModeDirect || !analysis.plan.DirectEligible || analysis.plan.ImportHistoryEntry {
		t.Fatalf("plan = %#v", analysis.plan)
	}
	if analysis.rows[0].FromAddress != "0xfrom" || analysis.rows[0].ToAddress != "0xto" || analysis.rows[0].Fee != "0.001" {
		t.Fatalf("row did not preserve source fields: %#v", analysis.rows[0])
	}
}

func TestImportAutoRecommendsStagedForAmbiguousRows(t *testing.T) {
	file := writeImportModeCSV(t, `Transaction Hash,DateTime (UTC),Amount,From,To
0xhash,2026-08-26 07:37:35,1 ETH,0xfrom,0xto
`)
	analysis, err := analyzeTransactionImport(businessFileContext(t, file), file, importModeAuto, []orgreports.Wallet{{ID: "wallet-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.plan.RecommendedMode != importModeStaged || analysis.plan.DirectEligible || len(analysis.plan.ValidationIssues) == 0 {
		t.Fatalf("plan = %#v", analysis.plan)
	}
}

func TestImportExplicitDirectOverridesMixedWalletRecommendation(t *testing.T) {
	file := writeImportModeCSV(t, `id,amount,amountTicker,time,transactionType,accountId
txn-1,1,ETH,2026-08-26T07:37:35Z,deposit,wallet-1
txn-2,2,ETH,2026-08-26T07:38:35Z,deposit,wallet-2
`)
	analysis, err := analyzeTransactionImport(businessFileContext(t, file), file, importModeDirect, []orgreports.Wallet{{ID: "wallet-1"}, {ID: "wallet-2"}})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.plan.RecommendedMode != importModeStaged || !analysis.plan.DirectEligible || !analysis.plan.ExplicitOverride || analysis.plan.ImportHistoryEntry {
		t.Fatalf("plan = %#v", analysis.plan)
	}
}

func TestImportDuplicateIDsBlockDirectCreation(t *testing.T) {
	file := writeImportModeCSV(t, `id,amount,amountTicker,time,transactionType,accountId
txn-1,1,ETH,2026-08-26T07:37:35Z,deposit,wallet-1
txn-1,2,ETH,2026-08-26T07:38:35Z,deposit,wallet-1
`)
	analysis, err := analyzeTransactionImport(businessFileContext(t, file), file, importModeDirect, []orgreports.Wallet{{ID: "wallet-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.plan.DirectEligible || analysis.plan.StableUniqueIDs {
		t.Fatalf("plan = %#v", analysis.plan)
	}
	if len(analysis.plan.ValidationIssues) == 0 || analysis.plan.ValidationIssues[len(analysis.plan.ValidationIssues)-1].Code != "duplicate_id" {
		t.Fatalf("issues = %#v", analysis.plan.ValidationIssues)
	}
}

func TestImportTradeIDIsRestrictedToTradeRows(t *testing.T) {
	file := writeImportModeCSV(t, `id,amount,amountTicker,time,transactionType,accountId,tradeId
txn-1,1,ETH,2026-08-26T07:37:35Z,deposit,wallet-1,trade-1
txn-2,2,ETH,2026-08-26T07:38:35Z,tradeAcquire,wallet-1,
`)
	analysis, err := analyzeTransactionImport(businessFileContext(t, file), file, importModeDirect, []orgreports.Wallet{{ID: "wallet-1"}})
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range analysis.plan.ValidationIssues {
		codes[issue.Code] = true
	}
	if analysis.plan.DirectEligible || !codes["trade_id_not_allowed"] || !codes["missing_trade_id"] {
		t.Fatalf("plan = %#v", analysis.plan)
	}
}

func TestImportTradeIDIsPreservedForEveryTradeRowType(t *testing.T) {
	file := writeImportModeCSV(t, `id,amount,amountTicker,time,transactionType,accountId,tradeId
txn-1,1,ETH,2026-08-26T07:37:35Z,tradeAcquire,wallet-1,trade-1
txn-2,1000,USDC,2026-08-26T07:37:35Z,tradeDispose,wallet-1,trade-1
txn-3,0.001,ETH,2026-08-26T07:37:35Z,tradeFee,wallet-1,trade-1
`)
	analysis, err := analyzeTransactionImport(businessFileContext(t, file), file, importModeDirect, []orgreports.Wallet{{ID: "wallet-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !analysis.plan.DirectEligible || len(analysis.rows) != 3 {
		t.Fatalf("plan = %#v", analysis.plan)
	}
	for _, row := range analysis.rows {
		if row.TradeID != "trade-1" {
			t.Fatalf("trade id not preserved: %#v", row)
		}
	}
}

func TestImportAutoCommandReturnsMachineReadablePlan(t *testing.T) {
	file := writeImportModeCSV(t, `id,amount,amountTicker,time,transactionType,accountId
txn-1,1,ETH,2026-08-26T07:37:35Z,deposit,wallet-1
`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/orgs/org-1/wallets" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"wallet-1","name":"Manual wallet"}]}`))
	}))
	defer server.Close()
	t.Setenv("BITWAVE_BASE_URL_CORE", server.URL)
	t.Setenv("BITWAVE_TOKEN", "token")
	cmd := testDefinition(newImportTransactionsCmd())
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&strings.Builder{})
	cmd.SetArgs([]string{file, "--org", "org-1", "--mode", "auto", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Status string `json:"status"`
		Result struct {
			Plan transactionImportPlan `json:"plan"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out.String()), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Status != "recommendation" || envelope.Result.Plan.RecommendedMode != importModeDirect || envelope.Result.Plan.RowCount != 1 {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestDirectImportReportsRejectedFirstBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "closed period", http.StatusConflict)
	}))
	defer server.Close()
	t.Setenv("BITWAVE_BASE_URL_CORE", server.URL)
	t.Setenv("BITWAVE_TOKEN", "token")
	cmd := testDefinition(newImportTransactionsCmd())
	cmd.SetContext(context.Background())
	var out strings.Builder
	cmd.SetOut(&out)
	analysis := &directImportAnalysis{
		plan: transactionImportPlan{SchemaVersion: "1", RequestedMode: importModeDirect, RecommendedMode: importModeDirect, DirectEligible: true, RowCount: 1},
		rows: []orgreports.CreateTransaction{{SystemID: "txn-1", Time: "2026-08-26T07:37:35Z", AccountID: "wallet-1", Amount: "1", AmountTicker: "ETH", TransactionType: "deposit"}},
	}
	err := runDirectTransactionImport(businessTestCall(t, cmd), "org-1", analysis, importWaitFlags{transactionMutationFlags: transactionMutationFlags{yes: true, jsonOutput: true}})
	if err == nil {
		t.Fatal("expected direct rejection")
	}
	var envelope mutationEnvelope
	if jsonErr := json.Unmarshal([]byte(out.String()), &envelope); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if envelope.Status != "error" || envelope.Error == nil || envelope.Error.Code != "direct_create_rejected" {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func TestDirectImportReportsPartialFailureWithoutBlindRetry(t *testing.T) {
	rows := make([]orgreports.CreateTransaction, directRequestBatchSize+1)
	for index := range rows {
		rows[index] = orgreports.CreateTransaction{SystemID: "txn-" + string(rune(index+1)), Time: "2026-08-26T07:37:35Z", AccountID: "wallet-1", Amount: "1", AmountTicker: "ETH", TransactionType: "deposit"}
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 2 {
			http.Error(w, "closed period", http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"created":100}`))
	}))
	defer server.Close()
	t.Setenv("BITWAVE_BASE_URL_CORE", server.URL)
	t.Setenv("BITWAVE_TOKEN", "token")
	cmd := testDefinition(newImportTransactionsCmd())
	cmd.SetContext(context.Background())
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetOut(&strings.Builder{})
	analysis := &directImportAnalysis{plan: transactionImportPlan{SchemaVersion: "1", RequestedMode: importModeDirect, RecommendedMode: importModeDirect, DirectEligible: true, RowCount: len(rows)}, rows: rows}
	var out strings.Builder
	cmd.SetOut(&out)
	err := runDirectTransactionImport(businessTestCall(t, cmd), "org-1", analysis, importWaitFlags{transactionMutationFlags: transactionMutationFlags{yes: true, jsonOutput: true}})
	if err == nil || !strings.Contains(err.Error(), "100 of 101") {
		t.Fatalf("err = %v", err)
	}
	var envelope mutationEnvelope
	if jsonErr := json.Unmarshal([]byte(out.String()), &envelope); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if envelope.Status != "partial_success" || envelope.Error == nil || envelope.Error.Retryable {
		t.Fatalf("envelope = %#v", envelope)
	}
}

func writeImportModeCSV(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "transactions.csv")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}
