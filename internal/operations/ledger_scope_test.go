package operations

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/operation"
	walletsync "github.com/bitwave-io/bitwave-wallet-sdk/sync"
	"github.com/bitwave-io/bitwave-wallet-sdk/wallet"
)

func ledgerRuntime(t *testing.T, opts operation.Options) context.Context {
	t.Helper()
	rt, err := operation.NewRuntime(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return operation.WithRuntime(context.Background(), rt)
}

func invokeLedger(ctx context.Context, d *operation.Definition, args ...string) (string, error) {
	var out bytes.Buffer
	if err := d.Flags().Parse(args); err != nil {
		return "", err
	}
	err := d.Invoke(operation.NewCall(ctx, d, nil, &out, &out), d.Flags().Args())
	return out.String(), err
}

func TestLedgerScopedLifecycleAndIsolation(t *testing.T) {
	for _, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			ctx := ledgerRuntime(t, operation.Options{WorkingDirectory: dir})
			if _, err := invokeLedger(ctx, newInitCmd(), "--name", name); err != nil {
				t.Fatal(err)
			}
			if _, err := invokeLedger(ctx, newExpenseNewCmd(), "--report", name, "--amount", "12", "--account", "Expenses:Travel", "--merchant", name); err != nil {
				t.Fatal(err)
			}
			out, err := invokeLedger(ctx, newExpenseReportCmd(), name)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, name) {
				t.Fatalf("missing scoped output: %s", out)
			}
			body, err := os.ReadFile(filepath.Join(dir, "default.journal"))
			if err != nil || !strings.Contains(string(body), "expense-report:"+name) {
				t.Fatalf("wrong scoped data: %s %v", body, err)
			}
		})
	}
}

func TestLedgerRejectsNestedJournalAndSymlinkEscapes(t *testing.T) {
	dir := t.TempDir()
	ctx := ledgerRuntime(t, operation.Options{WorkingDirectory: dir})
	if _, err := invokeLedger(ctx, newInitCmd()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../outside", "nested/journal", "nested\\journal"} {
		if _, err := invokeLedger(ctx, newJournalNewCmd(), id); err == nil {
			t.Fatalf("accepted journal path %q", id)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.journal")
	if err := os.WriteFile(outside, []byte("account Assets:Outside\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escaped.journal")); err != nil {
		t.Skip(err)
	}
	if _, err := invokeLedger(ctx, newPrintCmd()); err == nil {
		t.Fatal("read outside workspace through journal symlink")
	}
}

func TestLedgerImportIncludesUseScopedFilesystem(t *testing.T) {
	dir := t.TempDir()
	ctx := ledgerRuntime(t, operation.Options{WorkingDirectory: dir})
	if _, err := invokeLedger(ctx, newInitCmd()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child.ledger"), []byte("account Assets:Cash\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "import.ledger"), []byte("include child.ledger\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeLedger(ctx, newJEImportCmd(), "import.ledger"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "import.ledger"), []byte("include ../outside.ledger\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := invokeLedger(ctx, newJEImportCmd(), "import.ledger"); err == nil {
		t.Fatal("accepted outside include")
	}
}

func TestLedgerCloudConfigurationCannotChangeRequestOrganization(t *testing.T) {
	dir := t.TempDir()
	if err := config.Save(dir, &config.Config{Mode: config.ModeCloud, OrgId: "other", WorkspaceId: "workspace"}); err != nil {
		t.Fatal(err)
	}
	ctx := ledgerRuntime(t, operation.Options{WorkingDirectory: dir, OrganizationID: "allowed", Token: "fake-test-token"})
	if _, _, _, err := resolveStore(ctx); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected scope mismatch, got %v", err)
	}
}

func TestLedgerWalletFilesAndSyncStateAreScoped(t *testing.T) {
	dir := t.TempDir()
	ctx := ledgerRuntime(t, operation.Options{WorkingDirectory: dir})
	w := &wallet.Wallet{Id: "wlt_test", Name: "treasury"}
	path, err := saveWallet(ctx, dir, w)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := resolveWallet(ctx, dir, "treasury")
	if err != nil || got.Id != w.Id {
		t.Fatalf("wallet resolve: %+v %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("wallet permissions: %v %v", info, err)
	}
	s := walletsync.SyncState{WalletId: w.Id, Network: "ethereum", LastBlockTimeUnixMs: 123}
	if err := saveWalletSyncState(ctx, dir, s); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := loadWalletSyncState(ctx, dir, w.Id, s.Network)
	if err != nil || !ok || loaded != s {
		t.Fatalf("sync state: %+v %v %v", loaded, ok, err)
	}
	if _, _, err := resolveWallet(ctx, dir, filepath.Join(t.TempDir(), "wallet.json")); err == nil {
		t.Fatal("accepted outside wallet file")
	}
	if err := store.ValidateJournalID("../journal"); err == nil {
		t.Fatal("accepted escaped journal ID")
	}
}
