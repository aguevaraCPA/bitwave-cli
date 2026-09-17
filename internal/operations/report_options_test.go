package operations

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTransactionOptionsExposeDependentAssetChoices(t *testing.T) {
	fields := transactionOptionFields(
		[]reportChoice{{Label: "Treasury", Value: "wallet-1"}},
		[]reportChoice{{Label: "Parent", Value: "sub-1"}},
		[]reportChoice{{Label: "ETH", Value: "ETH"}},
		"complete",
	)
	var asset *reportField
	for i := range fields {
		if fields[i].Name == "assets" {
			asset = &fields[i]
			break
		}
	}
	if asset == nil || asset.ChoiceState != "complete" || len(asset.Choices) != 1 {
		t.Fatalf("asset field = %#v", asset)
	}
	if len(asset.DependsOn) != 3 || asset.DependsOn[0] != "wallets" {
		t.Fatalf("dependencies = %v", asset.DependsOn)
	}
}

func TestCSVDataRowCountHandlesQuotedNewlines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.csv")
	if err := os.WriteFile(path, []byte("id,note\n1,\"two\nlines\"\n2,ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	count, err := csvDataRowCount(businessFileContext(t, path), path)
	if err != nil || count != 2 {
		t.Fatalf("count = %d err=%v", count, err)
	}
}
