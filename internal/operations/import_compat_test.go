package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
)

// TestImportCompat_Fixtures drives `bitwave je import` over every compat
// fixture that's expected to parse cleanly. For each fixture we:
//
//  1. Init a fresh local workspace in a temp dir.
//  2. Run `bitwave je import <fixture>` against that workspace.
//  3. Read back the workspace's default journal as canonical ledger text.
//  4. Re-parse, compute balances, and diff against the fixture's sidecar.
//
// This is the end-to-end "open a workspace created by another tool" test
// the user asked for. It exercises the parser + balance checker + store
// writer + canonical-print emitter in one shot.
//
// Fixtures whose .expect sidecar marks them must_fail or skip are excluded.
func TestImportCompat_Fixtures(t *testing.T) {
	compatRoot, err := findCompatTestdata()
	if err != nil {
		t.Skipf("compat fixtures not reachable from this test binary: %v", err)
	}

	for _, sub := range []string{"ledger", "hledger", "beancount", "shared"} {
		sub := sub
		root := filepath.Join(compatRoot, sub)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		t.Run(sub, func(t *testing.T) {
			fixtures, err := collectFixtures(root)
			if err != nil {
				t.Fatalf("collect fixtures: %v", err)
			}
			for _, f := range fixtures {
				f := f
				t.Run(f.name, func(t *testing.T) {
					runImportCompat(t, f)
				})
			}
		})
	}
}

type fixtureRef struct {
	name     string // basename without extension
	path     string // absolute fixture path
	balances map[string]string
}

func runImportCompat(t *testing.T, f fixtureRef) {
	t.Helper()
	dir := t.TempDir()
	if _, err := store.InitLocal(dir, "compat", "USD"); err != nil {
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

	cmd := testDefinition(newJEImportCmd())
	cmd.SetArgs([]string{f.path})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("bitwave je import: %v\n%s", err, out.String())
	}

	// Read back the workspace's default journal. InitLocal creates one
	// named "default"; bitwave je import writes into it.
	journalPath := filepath.Join(dir, "default.journal")
	data, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	proj, err := format.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, data)
	}
	proj.BaseCurrency = "USD"
	if f.balances != nil {
		adapter := importedProject{}
		for _, e := range proj.Entries {
			ie := importedEntry{}
			for _, p := range e.Postings {
				ie.ps = append(ie.ps, importedPosting{
					account:   p.Account,
					commodity: p.Amount.Commodity,
					qty:       p.Amount.Quantity,
				})
			}
			adapter.es = append(adapter.es, ie)
		}
		got := computeBalances(adapter)
		if diff := diffBalances(f.balances, got); diff != "" {
			t.Errorf("post-import balances disagree with fixture sidecar:\n%s\n--- imported journal ---\n%s",
				diff, data)
		}
	}
}

// collectFixtures walks a testdata/<subdir>/ tree and returns one fixtureRef
// per importable source file (skipping must_fail / skip per .expect, and
// skipping include children).
func collectFixtures(root string) ([]fixtureRef, error) {
	var out []fixtureRef
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".journal", ".ledger", ".dat", ".test", ".beancount", ".bean":
		default:
			return nil
		}
		base := strings.TrimSuffix(filepath.Base(path), ext)
		// Optional .expect sidecar may mark this fixture as must_fail/skip.
		expectPath := filepath.Join(filepath.Dir(path), base+".expect")
		if data, rerr := os.ReadFile(expectPath); rerr == nil {
			var exp struct {
				MustFail bool   `json:"must_fail"`
				Skip     string `json:"skip"`
			}
			if jerr := json.Unmarshal(data, &exp); jerr == nil {
				if exp.MustFail || exp.Skip != "" {
					return nil
				}
			}
		} else if !errors.Is(rerr, os.ErrNotExist) {
			return rerr
		}
		ref := fixtureRef{
			name: strings.ReplaceAll(base, " ", "_") + "_" + strings.TrimPrefix(ext, "."),
			path: path,
		}
		// Optional balances sidecar.
		balancesPath := filepath.Join(filepath.Dir(path), base+".balances.json")
		if data, rerr := os.ReadFile(balancesPath); rerr == nil {
			var b map[string]string
			if jerr := json.Unmarshal(data, &b); jerr == nil {
				ref.balances = b
			}
		}
		out = append(out, ref)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out, nil
}

// findCompatTestdata locates the compat testdata directory by walking up
// from the test binary's cwd. This lets the test live under cli/internal/bitwave/cmd/
// but read fixtures from cli/internal/ledger/format/compat/testdata/.
func findCompatTestdata() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidate := filepath.Join(cwd, "..", "..", "ledger", "format", "compat", "testdata")
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("compat testdata at %s: %w", abs, err)
	}
	return abs, nil
}

// --- local copies of the compat harness balance helpers ---
//
// We deliberately don't import the compat package from a bitwave test to avoid
// pulling a test-only sibling under internal/ledger into bitwave tests. The
// compute/diff logic is small enough to duplicate.

// computeBalances returns per-account closing balances, formatted as
// "<amount> <commodity>" or comma-joined per commodity. Mirrors the compat
// package's ComputeBalances.
func computeBalances(proj importedProject) map[string]string {
	type key struct {
		Account   string
		Commodity string
	}
	sums := map[key]*big.Rat{}
	for _, e := range proj.entries() {
		for _, p := range e.postings() {
			if p.qty == nil {
				continue
			}
			k := key{Account: p.account, Commodity: p.commodity}
			if sums[k] == nil {
				sums[k] = new(big.Rat)
			}
			sums[k].Add(sums[k], p.qty)
		}
	}
	perAccount := map[string][]string{}
	for k, v := range sums {
		if v.Sign() == 0 {
			continue
		}
		perAccount[k.Account] = append(perAccount[k.Account], fmt.Sprintf("%s %s", v.FloatString(2), k.Commodity))
	}
	out := map[string]string{}
	for acct, parts := range perAccount {
		sort.Strings(parts)
		out[acct] = strings.Join(parts, ", ")
	}
	return out
}

// importedProject is a small adapter so computeBalances doesn't reach into
// the format package's exact types from this file. It's filled in by the
// caller using the format.Project struct.
type importedProject struct {
	es []importedEntry
}

func (p importedProject) entries() []importedEntry { return p.es }

type importedEntry struct {
	ps []importedPosting
}

func (e importedEntry) postings() []importedPosting { return e.ps }

type importedPosting struct {
	account   string
	commodity string
	qty       *big.Rat
}

func diffBalances(want, got map[string]string) string {
	all := map[string]bool{}
	for k := range want {
		all[k] = true
	}
	for k := range got {
		all[k] = true
	}
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var diff strings.Builder
	for _, k := range keys {
		w := normalizeBalanceStr(want[k])
		g := normalizeBalanceStr(got[k])
		if w != g {
			fmt.Fprintf(&diff, "  %s: want %q, got %q\n", k, w, g)
		}
	}
	return diff.String()
}

func normalizeBalanceStr(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "$") {
		s = strings.TrimPrefix(s, "$") + " USD"
	} else if strings.HasPrefix(s, "-$") {
		s = "-" + strings.TrimPrefix(s, "-$") + " USD"
	}
	return strings.Join(strings.Fields(s), " ")
}
