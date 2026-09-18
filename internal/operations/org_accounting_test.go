package operations

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

func TestAccountingStatusCountsActiveConnectionsAndAccounts(t *testing.T) {
	connections := []orgreports.AccountingConnection{{ID: "ac-1", Type: "manual"}}
	readiness := buildAccountingReadiness(connections, nil)
	if readiness.ConnectionCount != 2 || readiness.ChartAccountCount != 0 {
		t.Fatalf("readiness = %#v", readiness)
	}
	readiness = buildAccountingReadiness(connections, []orgreports.Category{{ID: "cat-1", Enabled: true, AccountingConnectionID: "ac-1"}})
	if readiness.ConnectionCount != 2 || readiness.ChartAccountCount != 1 {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestAccountingStatusIncludesImplicitManualConnection(t *testing.T) {
	readiness := buildAccountingReadiness(nil, []orgreports.Category{{ID: "cat-1", Enabled: true, AccountingConnectionID: implicitManualConnectionID}})
	if readiness.ConnectionCount != 1 || readiness.Connections[0].ID != implicitManualConnectionID || readiness.ChartAccountCount != 1 {
		t.Fatalf("readiness = %#v", readiness)
	}
}

func TestContactUpdateBodyPinsArgumentID(t *testing.T) {
	body, err := contactUpdateBody("contact-1", json.RawMessage(`{"enabled":false,"defaultRevenueCategoryId":"cat-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(body, &input); err != nil {
		t.Fatal(err)
	}
	if input["id"] != "contact-1" || input["enabled"] != false {
		t.Fatalf("input = %#v", input)
	}
	if _, err := contactUpdateBody("contact-1", json.RawMessage(`{"id":"contact-2"}`)); err == nil {
		t.Fatal("expected mismatched contact id to fail")
	}
}

func TestAccountingCommandsExposeCategoryAndContactParity(t *testing.T) {
	accounts := testDefinition(newOrgAccountingAccountsCmd())
	if len(accounts.Aliases) != 1 || accounts.Aliases[0] != "categories" {
		t.Fatalf("aliases = %v", accounts.Aliases)
	}
	for _, command := range []string{"enable", "disable", "disable-all"} {
		if found, _, err := accounts.Find([]string{command}); err != nil || found.Name() != command {
			t.Fatalf("accounts command %q missing: found=%v err=%v", command, found, err)
		}
	}
	contacts := testDefinition(newOrgAccountingContactsCmd())
	for _, command := range []string{"update", "enable", "disable", "disable-all"} {
		if found, _, err := contacts.Find([]string{command}); err != nil || found.Name() != command {
			t.Fatalf("contacts command %q missing: found=%v err=%v", command, found, err)
		}
	}
}

func TestChartAccountValidationAndImportShape(t *testing.T) {
	account := chartAccountInput{ConnectionID: "ac-1", ID: "4000", Name: "Revenue", Type: "Revenue", Code: "4000"}
	if err := validateChartAccount(account); err != nil {
		t.Fatal(err)
	}
	request := accountRequest(account)
	if request.Source != "manual" || request.Type != "revenue" || request.ID != "4000" {
		t.Fatalf("request = %#v", request)
	}
	loaded, err := loadChartAccounts(context.Background(), "-", strings.NewReader(`{"accounts":[{"connectionId":"ac-1","id":"5000","name":"Fees","type":"expense"}]}`))
	if err != nil || len(loaded) != 1 || loaded[0].ID != "5000" {
		t.Fatalf("loaded = %#v err=%v", loaded, err)
	}
	account.Type = "made-up"
	if err := validateChartAccount(account); err == nil {
		t.Fatal("expected invalid account type")
	}
}

func TestOrgAccountingHelpDescribesBitwaveResources(t *testing.T) {
	cmd := testDefinition(newOrgAccountingCmd())
	if !strings.Contains(cmd.Short, "Bitwave accounting connections") {
		t.Fatalf("help = %q", cmd.Short)
	}
}
