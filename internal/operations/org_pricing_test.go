package operations

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestPricingCoversHistoryRulesContextsAndRateTables(t *testing.T) {
	required := []string{
		"available-tokens", "historic-prices", "transaction-token-lookups",
		"pricing-report-export", "price-cache-clear", "price-cache-clear-graphql",
		"price-get", "asset-price", "transaction-price",
		"pricing-rules-list", "default-pricing-rule-get", "pricing-rule-create",
		"pricing-rule-update", "default-pricing-rule-create", "pricing-rule-audit-log",
		"pricing-contexts-list", "pricing-context-create", "pricing-context-update",
		"pricing-context-status-update", "pricing-context-routes-list",
		"pricing-context-route-create", "pricing-context-route-update",
		"pricing-context-route-status",
		"rate-tables-list", "rate-table-create", "rate-table-rows-list",
		"rate-table-rows-create", "rate-table-rows-delete", "rate-table-export",
		"rate-table-load", "rate-table-data-sources", "rate-table-data-source-get",
		"rate-table-data-sources-legacy", "rate-table-data-source-get-legacy",
	}
	operations := pricingOperations()
	seen := make(map[string]bool, len(operations))
	for _, operation := range operations {
		if seen[operation.Name] {
			t.Fatalf("duplicate pricing operation %q", operation.Name)
		}
		seen[operation.Name] = true
	}
	for _, name := range required {
		if !seen[name] {
			t.Errorf("missing pricing operation %q", name)
		}
	}
	if len(operations) != len(required) {
		t.Fatalf("pricing operations=%d; required=%d", len(operations), len(required))
	}
}

func TestPricingCapabilitiesAreMachineReadable(t *testing.T) {
	cmd := testDefinition(newOrgPricingCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"capabilities", "--area", "rules", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Total      int              `json:"total"`
		Operations []map[string]any `json:"operations"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 6 || len(result.Operations) != 6 {
		t.Fatalf("capabilities = %#v", result)
	}
	for _, operation := range result.Operations {
		if operation["operation"] == "pricing-rule-audit-log" {
			if operation["service"] != "core" || operation["protocol"] != "rest" {
				t.Fatalf("operation = %#v", operation)
			}
			continue
		}
		if operation["service"] != "platform" || operation["protocol"] != "graphql" {
			t.Fatalf("operation = %#v", operation)
		}
	}
}

func TestPricingRateTableDryRunUsesAPI2(t *testing.T) {
	cmd := testDefinition(newOrgPricingCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"rate-tables", "rows", "rate table", "--org", "org one", "--query", "page=2", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"url": "https://api2.bitwave.io/v2/orgs/org%20one/rate-tables/rate%20table/paginated?page=2"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestPricingTransactionLookupUsesTransactionServiceAndDefaults(t *testing.T) {
	cmd := testDefinition(newOrgPricingCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"history", "token-lookups", "--org", "org-1", "--query", "startsWith=ETH", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	output := out.String()
	if !strings.Contains(output, "https://transactions.bitwave.io/orgs/org-1/lookups?") ||
		!strings.Contains(output, "fieldName=amountCurrencyName") || !strings.Contains(output, "startsWith=ETH") {
		t.Fatalf("output = %s", output)
	}
}

func TestPricingGraphQLUsesPlatformService(t *testing.T) {
	cmd := testDefinition(newOrgPricingCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"rules", "create", "--org", "org-1", "--data", `{"rule":{"assetId":"COIN.eth"}}`, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	output := out.String()
	if !strings.Contains(output, `"service": "platform"`) ||
		!strings.Contains(output, `"url": "https://api4.bitwave.io/graphql"`) ||
		!strings.Contains(output, `"orgId": "org-1"`) ||
		!strings.Contains(output, "createPricingRule") {
		t.Fatalf("output = %s", output)
	}
}

func TestPricingWritesRequireConfirmation(t *testing.T) {
	cmd := testDefinition(newOrgPricingCmd())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"history", "price-transaction", "SOL.hash", "--org", "org-1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("error = %v", err)
	}
}
