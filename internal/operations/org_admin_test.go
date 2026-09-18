package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAdminInputPreservesInvocationJSONNumbers(t *testing.T) {
	input, err := readAdminInput(context.Background(), "-", "", strings.NewReader(`{"sequence":9007199254740993,"enabled":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if input["sequence"] != json.Number("9007199254740993") || input["enabled"] != false {
		t.Fatalf("input=%#v", input)
	}
}

func TestAdminOperationCatalogIsCompleteAndUnique(t *testing.T) {
	operations := adminOperations()
	if len(operations) < 100 {
		t.Fatalf("expected at least 100 admin operations, got %d", len(operations))
	}
	wantAreas := []string{
		"organization", "subsidiaries", "accounting-setup", "billing",
		"connections", "system-jobs", "wallets", "users", "roles", "sso",
		"scim", "api-keys", "audit-config", "custom-labels", "sftp", "rolled-up-je",
	}
	seenAreas := map[string]bool{}
	seenNames := map[string]bool{}
	seenCommands := map[string]bool{}
	for _, operation := range operations {
		if operation.Area == "" || operation.Name == "" || operation.Use == "" || operation.Short == "" {
			t.Fatalf("incomplete operation: %#v", operation)
		}
		command := operation.Area + " " + operation.Use
		if seenNames[operation.Name] {
			t.Fatalf("duplicate operation name %q", operation.Name)
		}
		if seenCommands[command] {
			t.Fatalf("duplicate command %q", command)
		}
		seenNames[operation.Name] = true
		seenCommands[command] = true
		seenAreas[operation.Area] = true
	}
	for _, area := range wantAreas {
		if !seenAreas[area] {
			t.Errorf("missing Admin area %q", area)
		}
	}
}

func TestAdminDryRunEscapesPathAndBuildsStructuredRequest(t *testing.T) {
	operation := withInput(adminRESTOperation(
		"subsidiaries", "test-update", "update ENTITY_ID", "test", "PATCH",
		"/v3/orgs/{org}/structure/{entityId}",
	))
	operation.ArgumentNames = []string{"entityId"}

	var output bytes.Buffer
	cmd := testDefinition(newAdminOperationCmd(operation))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"division/one", "--org", "org/one", "--data", `{"name":"Updated"}`, "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output.String())
	}
	request := envelope["request"].(map[string]any)
	endpoint := request["url"].(string)
	if !strings.Contains(endpoint, "/org%2Fone/structure/division%2Fone") {
		t.Fatalf("path identifiers were not escaped: %s", endpoint)
	}
	if got := envelope["status"]; got != "preview" {
		t.Fatalf("status = %v, want preview", got)
	}
}

func TestAdminMutationRequiresConfirmation(t *testing.T) {
	operation := withInput(adminRESTOperation("organization", "test-write", "write", "test", "POST", "/v3/orgs/{org}/test"))
	var output bytes.Buffer
	cmd := testDefinition(newAdminOperationCmd(operation))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--org", "org-id", "--data", `{"name":"test"}`})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes error, got %v", err)
	}
}

func TestAdminGraphQLQueryDoesNotRequireWriteConfirmation(t *testing.T) {
	operation := adminGraphQLOperation("test", "test-query", "query", "test", `query Test($orgId: ID!) { org(id: $orgId) { id } }`)
	var output bytes.Buffer
	cmd := testDefinition(newAdminOperationCmd(operation))
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--org", "org-id", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("query incorrectly required write confirmation: %v", err)
	}
}

func TestNamedSystemJobSuppliesUIContractDefaults(t *testing.T) {
	var operation adminOperation
	for _, candidate := range adminOperations() {
		if candidate.Name == "system-job-ignore" {
			operation = candidate
			break
		}
	}
	if operation.Name == "" {
		t.Fatal("system-job-ignore not found")
	}
	if operation.Defaults["systemJobId"] != "bulk-transaction" || operation.Defaults["action"] != "ignore" {
		t.Fatalf("unexpected defaults: %#v", operation.Defaults)
	}
	if operation.Defaults["transactionType"] != "all" {
		t.Fatalf("transaction type default = %#v", operation.Defaults["transactionType"])
	}
}

func TestNetSuiteAdminSubresourcesAreFirstClass(t *testing.T) {
	want := map[string]bool{
		"netsuite-custom-segments": false, "netsuite-custom-fields": false,
		"netsuite-saved-searches": false, "netsuite-custom-records": false,
		"netsuite-metadata-mappers": false, "netsuite-subsidiary-routing": false,
	}
	for _, operation := range adminOperations() {
		if _, ok := want[operation.Name]; ok {
			want[operation.Name] = true
		}
	}
	for operation, found := range want {
		if !found {
			t.Errorf("missing %s", operation)
		}
	}
}

func TestEveryAdminOperationCanBuildADryRunRequest(t *testing.T) {
	for _, operation := range adminOperations() {
		operation := operation
		t.Run(operation.Name, func(t *testing.T) {
			var output bytes.Buffer
			cmd := testDefinition(newAdminOperationCmd(operation))
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			args := make([]string, len(operation.ArgumentNames))
			for index, name := range operation.ArgumentNames {
				args[index] = "test-" + name
			}
			args = append(args, "--org", "test-org", "--dry-run")
			if operation.InputRequired {
				args = append(args, "--data", `{"test":true}`)
			}
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("dry run failed: %v\n%s", err, output.String())
			}
			var envelope map[string]any
			if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
				t.Fatalf("invalid JSON response: %v\n%s", err, output.String())
			}
			if envelope["status"] != "preview" || envelope["operation"] != operation.Name {
				t.Fatalf("unexpected preview envelope: %#v", envelope)
			}
		})
	}
}
