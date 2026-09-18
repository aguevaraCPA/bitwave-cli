package operations

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestImportCommandExposesManualImportLifecycle(t *testing.T) {
	cmd := testDefinition(newOrgImportsCmd())
	for _, path := range [][]string{
		{"template"}, {"list"}, {"status"}, {"preview"}, {"errors"},
		{"upload"}, {"validate"}, {"run"}, {"transactions"},
	} {
		found, _, err := cmd.Find(path)
		if err != nil || found == cmd {
			t.Errorf("missing command %q: %v", strings.Join(path, " "), err)
		}
	}
}

func TestImportTemplatePublishesAllSupportedColumns(t *testing.T) {
	cmd := testDefinition(newImportTemplateCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		SupportedColumns []string `json:"supportedColumns"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"blockchainId", "fee", "memo", "description", "fromAddress", "toAddress", "groupId"} {
		found := false
		for _, column := range result.SupportedColumns {
			if column == field {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("supported columns missing %q: %#v", field, result.SupportedColumns)
		}
	}
}

func TestImportTransactionsDryRunIsOneApprovalWorkflow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "transactions.csv")
	if err := os.WriteFile(file, []byte("id,time,transactionType\na,2026-01-01T00:00:00Z,inflow\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := testDefinition(newImportTransactionsCmd())
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{file, "--org", "org-1", "--mode", "staged", "--dry-run", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "preview" || result.Operation != "import-transactions" || result.Organization != "org-1" {
		t.Fatalf("result = %#v", result)
	}
	request, ok := result.Request.(map[string]any)
	if !ok || request["workflow"] != "manual-transaction-import" {
		t.Fatalf("request = %#v", result.Request)
	}
	steps, ok := request["steps"].([]any)
	if !ok || len(steps) != 4 {
		t.Fatalf("steps = %#v", request["steps"])
	}
	if strings.Contains(out.String(), filepath.Dir(file)) {
		t.Fatalf("dry-run leaked absolute local path: %s", out.String())
	}
	if cmd.Flags().Lookup("no-wait") != nil {
		t.Fatal("combined workflow must stay attached; --no-wait would strand it before commit")
	}
}

func TestImportTransactionsRequiresConfirmation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "transactions.csv")
	if err := os.WriteFile(file, []byte("id\na\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := testDefinition(newImportTransactionsCmd())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{file, "--org", "org-1", "--mode", "staged"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestImportErrorsCSVUsesSpreadsheetRowsAndHumanMessages(t *testing.T) {
	index := 0
	index2 := 1
	data, err := importErrorsCSV([]orgreports.ImportRowError{
		{RowIndex: &index, Errors: []string{"Row 2: AccountID X does not exist", "bad ticker"}},
		{RowIndex: &index2, Errors: []string{"=HYPERLINK(\"bad\")"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Row,Errors") || !strings.Contains(text, `2,AccountID X does not exist; bad ticker`) || !strings.Contains(text, `3,"'=HYPERLINK`) {
		t.Fatalf("csv = %q", text)
	}
}

func TestImportStatusClassification(t *testing.T) {
	zero := 0
	if !importValidationPassed(&orgreports.ImportRecord{Status: "ready-to-import", RowErrorCount: &orgreports.ImportRowErrorCount{Validate: zero}}) {
		t.Fatal("ready-to-import should pass validation")
	}
	if importValidationPassed(&orgreports.ImportRecord{Status: "validation-failed", RowErrorCount: &orgreports.ImportRowErrorCount{Validate: 2}}) {
		t.Fatal("validation-failed should not pass validation")
	}
	if !importRunDone(&orgreports.ImportRecord{Status: "completed-with-errors"}) {
		t.Fatal("completed-with-errors is terminal")
	}
	if importStable(&orgreports.ImportRecord{Status: "running"}) {
		t.Fatal("running is not stable")
	}
}

func TestImportRunResultDistinguishesIssuesAndWarnings(t *testing.T) {
	for _, test := range []struct {
		serverStatus string
		wantStatus   string
	}{
		{"completed-with-errors", "partial_success"},
		{"completed-with-warnings", "success_with_warnings"},
		{"completed", "success"},
	} {
		cmd := testDefinition(newImportRunCmd())
		var out bytes.Buffer
		cmd.SetOut(&out)
		if err := outputImportRunResult(businessTestCall(t, cmd), "run-transaction-import", true, "org-1", &orgreports.ImportRecord{ID: "imp-1", Status: test.serverStatus}); err != nil {
			t.Fatal(err)
		}
		var result mutationEnvelope
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != test.wantStatus {
			t.Errorf("server status %s emitted %s; want %s", test.serverStatus, result.Status, test.wantStatus)
		}
	}
}

func TestImportIndeterminateTellsAgentNotToRetry(t *testing.T) {
	cmd := testDefinition(newImportRunCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := importIndeterminateError(businessTestCall(t, cmd), "run-transaction-import", true, "org-1", "imp-1", "run", contextDeadlineError{})
	if err == nil || !strings.Contains(err.Error(), "do not create a new import") {
		t.Fatalf("error = %v", err)
	}
	var result mutationEnvelope
	if jsonErr := json.Unmarshal(out.Bytes(), &result); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if result.Status != "indeterminate" || result.Error == nil || result.Error.Code != "import_status_unknown" {
		t.Fatalf("result = %#v", result)
	}
}

type contextDeadlineError struct{}

func (contextDeadlineError) Error() string { return "deadline exceeded" }

func TestImportTransactionsEndToEnd(t *testing.T) {
	file := filepath.Join(t.TempDir(), "transactions.csv")
	if err := os.WriteFile(file, []byte("id,time,transactionType\na,2026-01-01T00:00:00Z,inflow\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploaded, validated, ran := false, false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload" {
			if r.Header.Get("Authorization") != "" {
				t.Fatal("signed upload included Authorization")
			}
			uploaded = true
			return
		}
		if r.URL.Path != "/graphql" || r.Header.Get("Authorization") != "Bearer token" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			OperationName string `json:"operationName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.OperationName {
		case "CreateImport":
			_, _ = w.Write([]byte(`{"data":{"createImport":{"id":"imp-1","fileUploadUrl":"http://` + r.Host + `/upload?secret=hidden"}}}`))
		case "ValidateImport":
			validated = true
			_, _ = w.Write([]byte(`{"data":{"validateImport":"imp-1"}}`))
		case "RunImport":
			ran = true
			_, _ = w.Write([]byte(`{"data":{"runImport":"imp-1"}}`))
		case "GetImport":
			status := "ready-to-import"
			if ran {
				status = "completed"
			}
			_, _ = w.Write([]byte(`{"data":{"getImport":{"id":"imp-1","status":"` + status + `","rowErrorCount":{"validate":0,"run":0}}}}`))
		default:
			t.Fatalf("operation = %s", request.OperationName)
		}
	}))
	defer server.Close()
	t.Setenv("BITWAVE_BASE_URL_CORE", server.URL)
	t.Setenv("BITWAVE_TOKEN", "token")

	cmd := testDefinition(newImportTransactionsCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{file, "--org", "org-1", "--mode", "staged", "--yes", "--json", "--poll-interval", "1ms", "--timeout", "1s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}
	if !uploaded || !validated || !ran {
		t.Fatalf("uploaded=%t validated=%t ran=%t", uploaded, validated, ran)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" {
		t.Fatalf("result = %#v", result)
	}
}

func TestImportTransactionsStopsOnValidationErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "transactions.csv")
	if err := os.WriteFile(file, []byte("id\na\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/upload" {
			return
		}
		var request struct {
			OperationName string `json:"operationName"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		switch request.OperationName {
		case "CreateImport":
			_, _ = w.Write([]byte(`{"data":{"createImport":{"id":"imp-bad","fileUploadUrl":"http://` + r.Host + `/upload"}}}`))
		case "ValidateImport":
			_, _ = w.Write([]byte(`{"data":{"validateImport":"imp-bad"}}`))
		case "GetImport":
			_, _ = w.Write([]byte(`{"data":{"getImport":{"id":"imp-bad","status":"validation-failed","rowErrorCount":{"validate":2,"run":0}}}}`))
		case "RunImport":
			runCalled = true
		}
	}))
	defer server.Close()
	t.Setenv("BITWAVE_BASE_URL_CORE", server.URL)
	t.Setenv("BITWAVE_TOKEN", "token")
	cmd := testDefinition(newImportTransactionsCmd())
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{file, "--org", "org-1", "--mode", "staged", "--yes", "--json", "--poll-interval", "1ms", "--timeout", "1s"})
	err := cmd.Execute()
	if err == nil || runCalled {
		t.Fatalf("error=%v runCalled=%t", err, runCalled)
	}
	var result mutationEnvelope
	if jsonErr := json.Unmarshal(out.Bytes(), &result); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if result.Error == nil || result.Error.Code != "validation_failed" {
		t.Fatalf("result = %#v", result)
	}
}
