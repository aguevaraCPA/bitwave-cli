package operations

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResolveInventoryUpdateDate(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	date, err := resolveInventoryUpdateDate("", "UTC", now)
	if err != nil || date != "2026-08-13" {
		t.Fatalf("date = %q err=%v", date, err)
	}
	if _, err := resolveInventoryUpdateDate("2026-08-14", "UTC", now); err == nil {
		t.Fatal("expected current date to be rejected")
	}
}

func TestInventoryCreateUsesOrganizationDefaultPricing(t *testing.T) {
	request, err := useOrgDefaultInventoryPricing(json.RawMessage(`{"name":"Books","config":{"impairmentMethodology":"daily-low"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(request, &decoded); err != nil {
		t.Fatal(err)
	}
	config := decoded["config"].(map[string]any)
	if config["impairmentMethodology"] != "org-default" {
		t.Fatalf("config = %#v", config)
	}
}
