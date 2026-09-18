package operations

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestReportCenterExposesEveryReportsCenterCard(t *testing.T) {
	want := []string{
		"balance-report", "expanded-balance-report", "expanded-gl-summary-report",
		"expanded-gl-detailed-report", "expanded-val-rollforward-report",
		"expanded-pricing-summary-report", "transaction-actions-comparison-report",
		"fusion-custom-je-import-report", "xrp-custom-fusion-je-report",
		"xrp-custom-fusion-ecb-report", "xrp-custom-fusion-je-elimination-report",
		"xrp-custom-net-change-bigquery-report", "xrp-custom-purchased-xrp-balances-report",
		"expanded-balance-recon-report", "transactions-export", "transactions-export-lrt",
		"transactions-export-datawarehouse", "deleted-ignored-transactions-export",
		"journal-report", "expanded-journal-report", "rolled-up-journal-report", "ledger-report",
		"balance-check-report", "cost-basis-roll-forward", "cost-basis-roll-forward-expanded",
		"actions", "actions-summary-report", "reclass", "actions-je-report", "actions-tb-report",
		"trial-balance-report", "sage-erp-je-import", "expanded-actions-report",
		"expanded-cost-basis-report", "rolled-up-actions-je-sync", "rolled-up-actions-je-report",
	}
	seen := make(map[string]bool, len(reportCenterCatalog))
	for _, item := range reportCenterCatalog {
		seen[item.Name] = true
	}
	for _, name := range want {
		if !seen[name] {
			t.Errorf("Reports Center card %q is missing", name)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("catalog contains %d entries; want %d", len(seen), len(want))
	}
}

func TestReportCenterCommandExposesEndpointFamilies(t *testing.T) {
	cmd := testDefinition(newReportCenterCmd())
	for _, path := range [][]string{
		{"catalog"}, {"filter-options"}, {"run"}, {"runs"}, {"status"}, {"get"},
		{"download"}, {"cancel"}, {"expand"}, {"legacy", "list"}, {"legacy", "run"},
		{"legacy", "get"}, {"inventory", "list"}, {"inventory", "run"},
		{"export", "start"}, {"export", "saved", "list"}, {"export", "saved", "get"},
		{"export", "get"}, {"rolled-up-je", "generate"},
	} {
		found, _, err := cmd.Find(path)
		if err != nil || found == cmd {
			t.Errorf("missing command %q: %v", strings.Join(path, " "), err)
		}
	}
}

func TestReportCenterCatalogJSONIsMachineReadable(t *testing.T) {
	cmd := testDefinition(newReportCenterCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"catalog", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Reports            []reportCenterEntry `json:"reports"`
		InventoryEndpoints map[string]string   `json:"inventoryEndpoints"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Reports) != 36 || len(result.InventoryEndpoints) < 10 {
		t.Fatalf("catalog reports=%d inventory endpoints=%d", len(result.Reports), len(result.InventoryEndpoints))
	}
}

func TestParseReportInputsPreservesValues(t *testing.T) {
	inputs, err := parseReportInputs([]string{"endDate=2026-07-31", `walletIds=["a","b"]`, "memo=a=b"})
	if err != nil {
		t.Fatal(err)
	}
	if inputs[1]["value"] != `["a","b"]` || inputs[2]["value"] != "a=b" {
		t.Fatalf("inputs = %#v", inputs)
	}
	if _, err := parseReportInputs([]string{"missing"}); err == nil {
		t.Fatal("expected invalid input error")
	}
}

func TestInventoryReportPathsMatchReportsCenterAPI(t *testing.T) {
	want := map[string]string{
		"actions-summary-report":     "/reports/actions-summary-report",
		"reclass":                    "/reports/je-reclass",
		"expanded-actions-report":    "/reports/advanced-action-report",
		"cost-basis-roll-forward-v3": "/reports/advanced-cost-basis-roll-forward",
		"expanded-cost-basis-report": "/reports/ripple-cost-basis-roll-forward",
		"balance-check-report":       "/inventory-views/reports/balance-check-report",
	}
	for name, suffix := range want {
		if !strings.Contains(inventoryReportPaths[name], suffix) {
			t.Errorf("%s path = %q; want suffix %q", name, inventoryReportPaths[name], suffix)
		}
	}
}
