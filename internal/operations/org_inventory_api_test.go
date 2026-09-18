package operations

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInventoryAPICoversGeneratedInventorySurface(t *testing.T) {
	required := []string{
		"views-list", "view-get", "view-create", "view-properties-update", "view-delete", "view-rows",
		"view-updates-list", "view-update-trigger", "view-update-enhanced", "view-update-incremental",
		"view-update-cancel", "view-update-download-links",
		"view-balance", "view-balance-paginated", "view-counts",
		"action-columns", "action-column-values", "view-actions",
		"lot-columns", "lot-column-values", "view-lots",
		"inventory-groups-list", "inventory-group-get", "inventory-group-create", "inventory-group-update",
		"inventory-group-delete", "inventories-list", "inventory-create", "inventory-delete",
		"inventory-groups-ui-list", "inventory-group-ui-create", "inventory-group-ui-update",
		"view-config-update", "label-definitions", "reallocation-report-run", "view-reallocation-trigger",
		"actions-je-report", "actions-summary-report", "actions-tb-report", "advanced-actions-report",
		"advanced-cost-basis-roll-forward", "balance-check-report", "balance-check-report-paginated",
		"balance-check-report-job", "cost-basis-roll-forward", "enhanced-gain-loss", "je-reclass",
		"ripple-cost-basis-roll-forward", "sage-erp-je-report", "soda-report", "wallet-balance-report",
		"gain-loss-summary", "inventory-export-get",
	}
	operations := inventoryAPIOperations()
	seen := make(map[string]bool, len(operations))
	for _, operation := range operations {
		if seen[operation.Name] {
			t.Fatalf("duplicate inventory operation %q", operation.Name)
		}
		seen[operation.Name] = true
	}
	for _, name := range required {
		if !seen[name] {
			t.Errorf("missing inventory operation %q", name)
		}
	}
	if len(operations) != len(required) {
		t.Fatalf("inventory operations=%d; required=%d", len(operations), len(required))
	}
}

func TestInventoryAPICapabilitiesAreMachineReadable(t *testing.T) {
	cmd := testDefinition(newOrgInventoryAPICmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"capabilities", "--area", "lots", "--json"})
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
	if result.Total != 3 || len(result.Operations) != 3 {
		t.Fatalf("capabilities = %#v", result)
	}
	if result.Operations[0]["path"] == "" || result.Operations[0]["method"] != "GET" {
		t.Fatalf("operation = %#v", result.Operations[0])
	}
}

func TestInventoryAPIMutationDryRunBuildsExactEndpoint(t *testing.T) {
	cmd := testDefinition(newOrgInventoryAPICmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"views", "properties-update", "view one", "--org", "org one",
		"--data", `{"name":"Books"}`, "--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	request := result.Request.(map[string]any)
	if request["method"] != "PATCH" || request["url"] != "https://api.bitwave.io/orgs/org%20one/inventory-views/view%20one" {
		t.Fatalf("request = %#v", request)
	}
}

func TestInventoryAPIWritesRequireConfirmation(t *testing.T) {
	cmd := testDefinition(newOrgInventoryAPICmd())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"groups", "delete", "group-1", "--org", "org-1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestInventoryAPIGraphQLConfigMutationUsesViewID(t *testing.T) {
	cmd := testDefinition(newOrgInventoryAPICmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"configuration", "update", "view-1", "--org", "org-1",
		"--data", `{"inventoryConfig":{"assetValuationStrategies":[]}}`, "--dry-run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"inventoryId": "view-1"`) || !strings.Contains(out.String(), "updateInventoryViewConfig") {
		t.Fatalf("output = %s", out.String())
	}
}
