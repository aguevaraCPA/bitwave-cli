package operations

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestValidateExportDateRange(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		allDates bool
		wantErr  bool
	}{
		{"valid", "2026-01-01", "2026-06-30", false, false},
		{"same day", "2026-06-30", "2026-06-30", false, false},
		{"all dates", "", "", true, false},
		{"missing end", "2026-01-01", "", false, true},
		{"backwards", "2026-07-01", "2026-06-30", false, true},
		{"invalid calendar date", "2026-02-30", "2026-06-30", false, true},
		{"ambiguous all dates", "2026-01-01", "", true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateExportDateRange(test.from, test.to, test.allDates)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestResolveInventoryView(t *testing.T) {
	views := []orgreports.InventoryView{
		{ID: "view-1", Name: "Primary FIFO"},
		{ID: "view-2", Name: "Secondary"},
	}
	byID, err := resolveInventoryView("view-1", views)
	if err != nil || byID.ID != "view-1" {
		t.Fatalf("by ID = %#v err=%v", byID, err)
	}
	byName, err := resolveInventoryView("primary fifo", views)
	if err != nil || byName.ID != "view-1" {
		t.Fatalf("by name = %#v err=%v", byName, err)
	}
	if _, err := resolveInventoryView("missing", views); err == nil {
		t.Fatal("expected missing-view error")
	}
}

func TestResolveWalletAndSubsidiaryNames(t *testing.T) {
	walletIDs, err := resolveWalletRefs([]string{"treasury", "wallet-2"}, []orgreports.Wallet{{ID: "wallet-1", Name: "Treasury"}, {ID: "wallet-2", Name: "Trading"}})
	if err != nil || !reflect.DeepEqual(walletIDs, []string{"wallet-1", "wallet-2"}) {
		t.Fatalf("wallet IDs = %v err=%v", walletIDs, err)
	}
	subsidiaryIDs, err := resolveSubsidiaryRefs([]string{"parent"}, []orgreports.Subsidiary{{ID: "sub-1", Name: "Parent"}})
	if err != nil || !reflect.DeepEqual(subsidiaryIDs, []string{"sub-1"}) {
		t.Fatalf("subsidiary IDs = %v err=%v", subsidiaryIDs, err)
	}
}

func TestResolveWalletRejectsAmbiguousName(t *testing.T) {
	_, err := resolveWalletRefs([]string{"Treasury"}, []orgreports.Wallet{{ID: "wallet-1", Name: "Treasury"}, {ID: "wallet-2", Name: "treasury"}})
	if err == nil || !strings.Contains(err.Error(), "wallet-1, wallet-2") {
		t.Fatalf("err = %v", err)
	}
}

func TestActionOutputPaths(t *testing.T) {
	want := []string{"/tmp/actions-part-01.csv", "/tmp/actions-part-02.csv"}
	if got := actionOutputPaths("/tmp/actions.csv", 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %v", got)
	}
}

func TestWriteStreamAtomicRemovesPartialOnError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "export.csv")
	err := writeStreamAtomic(businessFileContext(t, target), target, func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return errors.New("stream failed")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target should not exist: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".export.csv-*.partial"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("partial files = %v err=%v", matches, err)
	}
}
