package operations

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-wallet-sdk/wallet"
)

func ledgerWalletContext(t *testing.T) context.Context {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return ledgerRuntime(t, operation.Options{WorkingDirectory: dir})
}

// fakeBroadcaster is the test seam for runWalletsSend.
type fakeBroadcaster struct {
	tip, feeCap *big.Int
	nonce       uint64
	sent        []*types.Transaction
}

func (f *fakeBroadcaster) SuggestFees(ctx context.Context) (*big.Int, *big.Int, error) {
	return f.tip, f.feeCap, nil
}
func (f *fakeBroadcaster) PendingNonce(ctx context.Context, from common.Address) (uint64, error) {
	return f.nonce, nil
}
func (f *fakeBroadcaster) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	return 21000, nil
}
func (f *fakeBroadcaster) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	f.sent = append(f.sent, tx)
	return nil
}
func (f *fakeBroadcaster) Close() {}

// setupWorkspace creates an empty bitwave local workspace in a temp dir and
// chdirs there for the test. Restores cwd on cleanup.
func setupWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := store.InitLocal(dir, "test", "USD"); err != nil {
		t.Fatalf("init: %v", err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	return dir
}

func TestWalletsNew_WritesKeystoreAndAccounts(t *testing.T) {
	dir := setupWorkspace(t)
	cmd := testDefinition(newWalletsCmd())
	cmd.SetArgs([]string{"new", "--name", "treasury"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "wallet-*.json"))
	if len(matches) != 1 {
		t.Fatalf("want 1 keystore, got %d (%v)", len(matches), matches)
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("keystore mode = %o, want 0600", info.Mode().Perm())
	}

	if !strings.Contains(out.String(), "Do NOT commit") {
		t.Fatalf("missing private-key warning in output:\n%s", out.String())
	}

	accountsData, err := os.ReadFile(filepath.Join(dir, store.AccountsFile))
	if err != nil {
		t.Fatal(err)
	}
	p, err := format.Parse(bytes.NewReader(accountsData))
	if err != nil {
		t.Fatalf("parse accounts: %v\n%s", err, accountsData)
	}
	if got := len(p.Accounts); got != 2 {
		t.Fatalf("want 2 declared accounts, got %d", got)
	}
	wantNames := map[string]bool{
		"Assets:Crypto:ethereum:treasury": true,
		"Assets:Crypto:base:treasury":     true,
	}
	for _, a := range p.Accounts {
		if !wantNames[a.Name] {
			t.Errorf("unexpected account %s", a.Name)
		}
		if a.Tags["wallet"] == "" {
			t.Errorf("account %s missing wallet tag", a.Name)
		}
		if a.Tags["address"] == "" {
			t.Errorf("account %s missing address tag", a.Name)
		}
		if a.Tags["network"] == "" {
			t.Errorf("account %s missing network tag", a.Name)
		}
	}
}

func TestWalletsNew_NetworksOverride(t *testing.T) {
	dir := setupWorkspace(t)
	cmd := testDefinition(newWalletsCmd())
	cmd.SetArgs([]string{"new", "--name", "ops", "--networks", "base"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, store.AccountsFile))
	p, _ := format.Parse(bytes.NewReader(data))
	if len(p.Accounts) != 1 {
		t.Fatalf("want 1 account, got %d", len(p.Accounts))
	}
	if p.Accounts[0].Name != "Assets:Crypto:base:ops" {
		t.Fatalf("name = %s", p.Accounts[0].Name)
	}
}

func TestWalletsNew_FailsOutsideWorkspace(t *testing.T) {
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	cmd := testDefinition(newWalletsCmd())
	cmd.SetArgs([]string{"new", "--name", "treasury"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected failure outside workspace")
	}
}

func TestWalletsList_GroupsTaggedAccountsOnly(t *testing.T) {
	dir := setupWorkspace(t)
	// Create one wallet via the command.
	mkCmd := testDefinition(newWalletsCmd())
	mkCmd.SetArgs([]string{"new", "--name", "treasury"})
	mkCmd.SetOut(new(bytes.Buffer))
	if err := mkCmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Add an unrelated, untagged account.
	if err := appendLine(filepath.Join(dir, store.AccountsFile), "account Assets:Cash\n"); err != nil {
		t.Fatal(err)
	}

	cmd := testDefinition(newWalletsCmd())
	cmd.SetArgs([]string{"list"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "Assets:Crypto:base:treasury") {
		t.Fatalf("missing tagged account in list output:\n%s", s)
	}
	if strings.Contains(s, "Assets:Cash") {
		t.Fatalf("list should not show untagged accounts:\n%s", s)
	}
}

func TestWalletsSend_DryRunWritesNothing(t *testing.T) {
	dir := setupWorkspace(t)
	mk := testDefinition(newWalletsCmd())
	mk.SetArgs([]string{"new", "--name", "treasury"})
	mk.SetOut(new(bytes.Buffer))
	if err := mk.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	bc := &fakeBroadcaster{
		tip:    big.NewInt(1_000_000_000),
		feeCap: big.NewInt(20_000_000_000),
		nonce:  0,
	}
	prev := broadcasterFactory
	broadcasterFactory = func(string) (wallet.Broadcaster, error) { return bc, nil }
	t.Cleanup(func() { broadcasterFactory = prev })

	var out bytes.Buffer
	err := runWalletsSend(ledgerWalletContext(t), &out, sendFlags{
		walletRef:   "treasury",
		networkName: "base",
		to:          "0x000000000000000000000000000000000000dEaD",
		amountEth:   "0.0001",
		category:    "Expenses:Uncategorized",
		dryRun:      true,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(bc.sent) != 0 {
		t.Fatalf("dry-run broadcast %d tx", len(bc.sent))
	}
	data, err := os.ReadFile(filepath.Join(dir, "default.journal"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "" {
		t.Fatalf("dry-run wrote to journal:\n%s", data)
	}
}

func TestWalletsSend_RealAppendsBalancedEntry(t *testing.T) {
	dir := setupWorkspace(t)
	mk := testDefinition(newWalletsCmd())
	mk.SetArgs([]string{"new", "--name", "treasury"})
	mk.SetOut(new(bytes.Buffer))
	if err := mk.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	bc := &fakeBroadcaster{
		tip:    big.NewInt(1_000_000_000),
		feeCap: big.NewInt(20_000_000_000),
		nonce:  3,
	}
	prev := broadcasterFactory
	broadcasterFactory = func(string) (wallet.Broadcaster, error) { return bc, nil }
	t.Cleanup(func() { broadcasterFactory = prev })

	err := runWalletsSend(ledgerWalletContext(t), new(bytes.Buffer), sendFlags{
		walletRef:   "treasury",
		networkName: "base",
		to:          "0x000000000000000000000000000000000000dEaD",
		amountEth:   "0.0001",
		category:    "Expenses:SaaS:Vercel",
		contact:     "Vercel Inc.",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(bc.sent) != 1 {
		t.Fatalf("want 1 broadcast, got %d", len(bc.sent))
	}
	data, err := os.ReadFile(filepath.Join(dir, "default.journal"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := format.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse journal: %v\n%s", err, data)
	}
	if len(p.Entries) != 1 {
		t.Fatalf("want 1 entry, got %d\n%s", len(p.Entries), data)
	}
	e := p.Entries[0]
	if e.Payee != "Vercel Inc." {
		t.Fatalf("payee = %q", e.Payee)
	}
	if len(e.Postings) != 3 {
		t.Fatalf("want 3 postings, got %d", len(e.Postings))
	}
	if !e.IsBalanced("ETH") {
		t.Fatalf("entry not balanced in ETH: %s", e.Balance("ETH").FloatString(18))
	}
	if !strings.Contains(e.Note, "txn:") || !strings.Contains(e.Note, "network:base") {
		t.Fatalf("missing tags in entry note: %q", e.Note)
	}
}

func TestWalletsSend_JournalOverride(t *testing.T) {
	dir := setupWorkspace(t)
	mk := testDefinition(newWalletsCmd())
	mk.SetArgs([]string{"new", "--name", "treasury"})
	mk.SetOut(new(bytes.Buffer))
	if err := mk.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	bc := &fakeBroadcaster{tip: big.NewInt(1), feeCap: big.NewInt(2_000_000_000), nonce: 0}
	prev := broadcasterFactory
	broadcasterFactory = func(string) (wallet.Broadcaster, error) { return bc, nil }
	t.Cleanup(func() { broadcasterFactory = prev })

	err := runWalletsSend(ledgerWalletContext(t), new(bytes.Buffer), sendFlags{
		walletRef:   "treasury",
		networkName: "base",
		to:          "0x000000000000000000000000000000000000dEaD",
		amountEth:   "0.0001",
		category:    "Expenses:Uncategorized",
		journalFlag: "alt",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "alt.journal")); err != nil {
		t.Fatalf("alt.journal missing: %v", err)
	}
}

func TestWalletsSend_BadCategoryRejectedBeforeBroadcast(t *testing.T) {
	setupWorkspace(t)
	mk := testDefinition(newWalletsCmd())
	mk.SetArgs([]string{"new", "--name", "treasury"})
	mk.SetOut(new(bytes.Buffer))
	if err := mk.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	bc := &fakeBroadcaster{tip: big.NewInt(1), feeCap: big.NewInt(2), nonce: 0}
	prev := broadcasterFactory
	broadcasterFactory = func(string) (wallet.Broadcaster, error) { return bc, nil }
	t.Cleanup(func() { broadcasterFactory = prev })

	err := runWalletsSend(ledgerWalletContext(t), new(bytes.Buffer), sendFlags{
		walletRef:   "treasury",
		networkName: "base",
		to:          "0x000000000000000000000000000000000000dEaD",
		amountEth:   "0.0001",
		category:    "Random:Whatever",
	})
	if err == nil {
		t.Fatal("expected error for bad category")
	}
	if len(bc.sent) != 0 {
		t.Fatalf("broadcast despite bad category")
	}
}

func TestWalletsSend_MissingFlags(t *testing.T) {
	setupWorkspace(t)
	err := runWalletsSend(ledgerWalletContext(t), new(bytes.Buffer), sendFlags{})
	if err == nil {
		t.Fatal("expected error for missing flags")
	}
	if !errors.Is(err, err) { // sanity: error is non-nil
		t.Fatal("unreachable")
	}
}

func appendLine(path, content string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}
