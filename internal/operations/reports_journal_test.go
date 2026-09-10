package operations

import (
	"context"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
)

// addScopedEntry writes one balanced two-posting entry into the named journal.
func addScopedEntry(t *testing.T, ctx context.Context, journal, date, payee, account, amount string) {
	t.Helper()
	if _, err := invokeLedger(ctx, newJENewCmd(),
		"--journal", journal,
		"--date", date,
		"--payee", payee,
		"--posting", account+" "+amount+" USD",
		"--posting", "Equity:Opening -"+amount+" USD",
	); err != nil {
		t.Fatalf("je new (%s): %v", journal, err)
	}
}

// seedTwoJournals builds a workspace holding wallet-ish activity in one journal
// and accounting-only activity in another — the layout --journal exists to
// separate.
func seedTwoJournals(t *testing.T) context.Context {
	t.Helper()
	dir := t.TempDir()
	ctx := ledgerRuntime(t, operation.Options{WorkingDirectory: dir})
	if _, err := invokeLedger(ctx, newInitCmd()); err != nil {
		t.Fatal(err)
	}
	addScopedEntry(t, ctx, "wallet", "2026-01-01", "Wallet activity", "Assets:Crypto:ETH", "100")
	addScopedEntry(t, ctx, "accounting-only", "2026-01-02", "Accrual creation", "Assets:Receivables", "25000")
	addScopedEntry(t, ctx, "accounting-only", "2026-01-03", "Partial settlement", "Assets:Receivables", "10000")
	return ctx
}

func declareScopedAccount(t *testing.T, ctx context.Context, name, note string) {
	t.Helper()
	if _, err := invokeLedger(ctx, newAcctAddCmd(), name, "--note", note); err != nil {
		t.Fatalf("acct add %s: %v", name, err)
	}
}

func TestLoadProject_NoJournalReturnsWholeWorkspace(t *testing.T) {
	ctx := seedTwoJournals(t)
	p, err := loadProject(ctx, "")
	if err != nil {
		t.Fatalf("loadProject: %v", err)
	}
	if got := len(p.Entries); got != 3 {
		t.Fatalf("unscoped load: got %d entries, want 3", got)
	}
}

func TestLoadProject_ScopesToOneJournal(t *testing.T) {
	ctx := seedTwoJournals(t)

	p, err := loadProject(ctx, "accounting-only")
	if err != nil {
		t.Fatalf("loadProject: %v", err)
	}
	if got := len(p.Entries); got != 2 {
		t.Fatalf("accounting-only: got %d entries, want 2", got)
	}
	for _, e := range p.Entries {
		if strings.Contains(e.Payee, "Wallet") {
			t.Errorf("wallet entry leaked into accounting-only scope: %q", e.Payee)
		}
	}

	p, err = loadProject(ctx, "wallet")
	if err != nil {
		t.Fatalf("loadProject: %v", err)
	}
	if got := len(p.Entries); got != 1 {
		t.Fatalf("wallet: got %d entries, want 1", got)
	}
}

// Scoping must not mutate the shared workspace view.
func TestLoadProject_ScopeDoesNotMutateWorkspace(t *testing.T) {
	ctx := seedTwoJournals(t)
	if _, err := loadProject(ctx, "wallet"); err != nil {
		t.Fatalf("scoped load: %v", err)
	}
	p, err := loadProject(ctx, "")
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if got := len(p.Entries); got != 3 {
		t.Fatalf("after scoped load: got %d entries, want 3", got)
	}
}

// A typo must fail loudly rather than render as an empty ledger.
func TestLoadProject_UnknownJournalErrors(t *testing.T) {
	ctx := seedTwoJournals(t)
	_, err := loadProject(ctx, "accounting")
	if err == nil {
		t.Fatal("expected an error for an unknown journal, got nil")
	}
	if !strings.Contains(err.Error(), "no journal") {
		t.Errorf("unhelpful error: %v", err)
	}
	if !strings.Contains(err.Error(), "accounting-only") {
		t.Errorf("error should list the available journals, got: %v", err)
	}
}

// Wallet-tagged declarations keep their wallet:/address:/network: metadata
// through an export; importing that into a fresh workspace registers a wallet
// there — inventory leaking into books that should have none.
func TestLoadProject_ScopesAccountDeclarationsToJournal(t *testing.T) {
	ctx := seedTwoJournals(t)
	declareScopedAccount(t, ctx, "Assets:Receivables", "accounting-only")
	declareScopedAccount(t, ctx, "Equity:Opening", "accounting-only")
	declareScopedAccount(t, ctx, "Assets:Crypto:ETH", "wallet-tagged, wallet journal only")
	declareScopedAccount(t, ctx, "Assets:Unused:Account", "declared but never posted to")

	p, err := loadProject(ctx, "accounting-only")
	if err != nil {
		t.Fatalf("loadProject: %v", err)
	}
	got := map[string]bool{}
	for _, a := range p.Accounts {
		got[a.Name] = true
	}
	for _, want := range []string{"Assets:Receivables", "Equity:Opening"} {
		if !got[want] {
			t.Errorf("scoped view is missing account %q it posts to", want)
		}
	}
	for _, unwanted := range []string{"Assets:Crypto:ETH", "Assets:Unused:Account"} {
		if got[unwanted] {
			t.Errorf("account %q leaked into the accounting-only scope", unwanted)
		}
	}
}

func TestLoadProject_NoJournalKeepsAllDeclarations(t *testing.T) {
	ctx := seedTwoJournals(t)
	declareScopedAccount(t, ctx, "Assets:Unused:Account", "declared but never posted to")

	p, err := loadProject(ctx, "")
	if err != nil {
		t.Fatalf("loadProject: %v", err)
	}
	for _, a := range p.Accounts {
		if a.Name == "Assets:Unused:Account" {
			return
		}
	}
	t.Error("unscoped view dropped a declared-but-unused account")
}
