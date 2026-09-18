package operations

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpenseNew_RequiresReport(t *testing.T) {
	setupWorkspace(t)
	c := testDefinition(newExpenseCmd())
	c.SetArgs([]string{
		"new",
		"--date", "2026-05-16",
		"--amount", "120",
		"--account", "Expenses:Travel",
	})
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	err := c.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected error for missing --report, got nil; output: %s", out.String())
	}
	if !strings.Contains(err.Error(), "--report") {
		t.Errorf("expected --report in error, got: %v", err)
	}
}

func TestExpenseNew_WritesTaggedEntry(t *testing.T) {
	dir := setupWorkspace(t)
	c := testDefinition(newExpenseCmd())
	c.SetArgs([]string{
		"new",
		"--report", "Q1-travel",
		"--date", "2026-05-16",
		"--amount", "120",
		"--account", "Expenses:Travel",
		"--merchant", "Acme",
		"--reimbursable",
	})
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	files, _ := filepath.Glob(filepath.Join(dir, "*.journal"))
	if len(files) == 0 {
		t.Fatal("no journal file written")
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"expense-report:Q1-travel", "merchant:Acme", "reimbursable:true", "Expenses:Travel"} {
		if !strings.Contains(body, want) {
			t.Errorf("journal missing %q in:\n%s", want, body)
		}
	}
}

func TestExpenseReport_FiltersByTag(t *testing.T) {
	setupWorkspace(t)
	// Add two entries with different reports
	add := func(report, account, amount, merchant string) {
		c := testDefinition(newExpenseCmd())
		c.SetArgs([]string{
			"new",
			"--report", report,
			"--date", "2026-05-16",
			"--amount", amount,
			"--account", account,
			"--merchant", merchant,
		})
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		if err := c.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("expense new: %v", err)
		}
	}
	add("Q1", "Expenses:Travel", "120", "Acme")
	add("Q2", "Expenses:Supplies", "50", "Office")

	c := testDefinition(newExpenseCmd())
	c.SetArgs([]string{"report", "Q1"})
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("report: %v\n%s", err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "Acme") {
		t.Errorf("report missing Q1 merchant: %s", s)
	}
	if strings.Contains(s, "Office") {
		t.Errorf("report leaked Q2 merchant: %s", s)
	}
}

func TestExpenseReport_FormatCSV(t *testing.T) {
	setupWorkspace(t)
	c := testDefinition(newExpenseCmd())
	c.SetArgs([]string{
		"new",
		"--report", "Q1",
		"--date", "2026-05-16",
		"--amount", "120",
		"--account", "Expenses:Travel",
		"--merchant", "Acme",
	})
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	c = testDefinition(newExpenseCmd())
	c.SetArgs([]string{"report", "Q1", "--format", "csv"})
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&out)
	if err := c.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "Date,Merchant,Category,Account,Amount,Currency,Reimbursable,Note,EntryId") {
		t.Errorf("CSV header missing:\n%s", out.String())
	}
}
