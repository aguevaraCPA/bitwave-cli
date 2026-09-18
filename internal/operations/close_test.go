package operations

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func executeCloseDryRun(t *testing.T, args ...string) mutationEnvelope {
	t.Helper()
	cmd := testDefinition(newCloseCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	return result
}

func closePreviewRequest(t *testing.T, result mutationEnvelope) map[string]any {
	t.Helper()
	request, ok := result.Request.(map[string]any)
	if !ok {
		t.Fatalf("request = %#v", result.Request)
	}
	return request
}

func TestCloseCreateDryRunUsesV2ChecklistEndpoint(t *testing.T) {
	cmd := testDefinition(newCloseCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetIn(strings.NewReader(`{"parameters":{"templateId":"close_period"},"version":2}`))
	cmd.SetArgs([]string{"create", "--org", "org one", "--input", "-", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	request := closePreviewRequest(t, result)
	if request["url"] != "https://api.bitwave.io/v3/orgs/org%20one/checklists" {
		t.Fatalf("url = %v", request["url"])
	}
	body := request["body"].(map[string]any)
	if body["version"] != float64(2) {
		t.Fatalf("body = %#v", body)
	}
}

func TestCloseRunDryRun(t *testing.T) {
	result := executeCloseDryRun(t, "run", "run one", "--org", "org one", "--task", "task-a,task-b", "--include-failed", "--dry-run")
	request := closePreviewRequest(t, result)
	if request["method"] != "POST" || request["url"] != "https://api.bitwave.io/v3/orgs/org%20one/checklists/run%20one/run" {
		t.Fatalf("request = %#v", request)
	}
	body := request["body"].(map[string]any)
	if body["includeFailed"] != true {
		t.Fatalf("body = %#v", body)
	}
	if got := body["taskIds"].([]any); len(got) != 2 {
		t.Fatalf("taskIds = %#v", got)
	}
}

func TestCloseRunPreservesExplicitFalse(t *testing.T) {
	result := executeCloseDryRun(t, "run", "run-1", "--org", "org-1", "--include-failed=false", "--dry-run")
	body := closePreviewRequest(t, result)["body"].(map[string]any)
	if value, present := body["includeFailed"]; !present || value != false {
		t.Fatalf("explicit false must be present in request: %#v", body)
	}
	result = executeCloseDryRun(t, "run", "run-1", "--org", "org-1", "--dry-run")
	body = closePreviewRequest(t, result)["body"].(map[string]any)
	if _, present := body["includeFailed"]; present {
		t.Fatalf("omitted include-failed must stay absent: %#v", body)
	}
}

func TestCloseTaskAcceptDryRun(t *testing.T) {
	result := executeCloseDryRun(t, "task", "accept", "run-1", "task-1", "--org", "org-1", "--reason", "Reviewed evidence", "--dry-run")
	request := closePreviewRequest(t, result)
	if request["url"] != "https://api.bitwave.io/v3/orgs/org-1/checklists/run-1/tasks/task-1" {
		t.Fatalf("url = %v", request["url"])
	}
	body := request["body"].(map[string]any)
	accept := body["accept"].(map[string]any)
	if accept["reason"] != "Reviewed evidence" {
		t.Fatalf("body = %#v", body)
	}
}

func TestCloseTemplateUpdateRequiresInput(t *testing.T) {
	cmd := testDefinition(newCloseCmd())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"template", "update", "tpl-1", "--org", "org-1", "--dry-run"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--input is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestCloseMutationsRequireConfirmation(t *testing.T) {
	cmd := testDefinition(newCloseCmd())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"delete", "run-1", "--org", "org-1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestCloseCommandDocumentsInterfaceBoundary(t *testing.T) {
	cmd := testDefinition(newCloseCmd())
	if !strings.Contains(cmd.Long, "does not decide") || !strings.Contains(cmd.Long, "Wavie") {
		t.Fatalf("close help does not explain interface boundary: %s", cmd.Long)
	}
	for _, name := range []string{"task", "template", "certification", "artifact", "delivery", "inventory-actions", "export", "rollup-config"} {
		if child, _, err := cmd.Find([]string{name}); err != nil || child == cmd {
			t.Fatalf("missing close command %q: %v", name, err)
		}
	}
}
