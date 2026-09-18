package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIRequestBodyReadsInvocationInput(t *testing.T) {
	body, isJSON, err := readAPIRequestBody(context.Background(), "-", "", "", strings.NewReader(`{"enabled":false}`))
	if err != nil || !isJSON || string(body) != `{"enabled":false}` {
		t.Fatalf("body=%s json=%v err=%v", body, isJSON, err)
	}
	if _, err := readLimitedInput(context.Background(), "-", strings.NewReader("12345"), 4); err == nil {
		t.Fatal("expected invocation input size limit")
	}
}

func TestAPIRequestDryRunResolvesOrgAndQuery(t *testing.T) {
	command := testDefinition(newAPIRequestCmd())
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"PATCH", "/v3/orgs/{org}/widgets", "--org", "org one", "--query", "state=open", "--data", `{"enabled":true}`, "--dry-run"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	request, ok := result.Request.(map[string]any)
	if !ok {
		t.Fatalf("request = %#v", result.Request)
	}
	if got, _ := request["url"].(string); got != "https://api.bitwave.io/v3/orgs/org%20one/widgets?state=open" {
		t.Fatalf("url = %q", got)
	}
	if result.Status != "preview" || result.Organization != "org one" {
		t.Fatalf("result = %#v", result)
	}
}

func TestAPIRequestRequiresConfirmationForWrites(t *testing.T) {
	command := testDefinition(newAPIRequestCmd())
	command.SetOut(&bytes.Buffer{})
	command.SetErr(&bytes.Buffer{})
	command.SetArgs([]string{"DELETE", "/v3/orgs/{org}/widgets/one", "--org", "org-1"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "without --yes") {
		t.Fatalf("error = %v", err)
	}
}

func TestGraphQLMutationDetection(t *testing.T) {
	if graphqlMayWrite("# mutation ignored\nquery Widgets { widgets { id } }") {
		t.Fatal("query identified as mutation")
	}
	if !graphqlMayWrite("mutation UpdateWidget { updateWidget { id } }") {
		t.Fatal("mutation not identified")
	}
}

func TestAppendAPIQueryPreservesExistingValues(t *testing.T) {
	got, err := appendAPIQuery("/widgets?state=open", []string{"tag=a", "tag=b"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "/widgets?state=open&tag=a&tag=b" {
		t.Fatalf("path = %s", got)
	}
}

func TestParseAPIHeadersBlocksCredentialOverrides(t *testing.T) {
	if _, err := parseAPIHeaders([]string{"Authorization=stolen"}); err == nil {
		t.Fatal("expected Authorization override to fail")
	}
	headers, err := parseAPIHeaders([]string{"Accept=text/csv", "X-Mode=fast"})
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get("Accept") != "text/csv" || headers.Get("X-Mode") != "fast" {
		t.Fatalf("headers = %#v", headers)
	}
}

func TestGraphQLResponseError(t *testing.T) {
	err := graphqlResponseError([]byte(`{"data":null,"errors":[{"message":"Missing Execution Org"}]}`))
	if err == nil || !strings.Contains(err.Error(), "Missing Execution Org") {
		t.Fatalf("error = %v", err)
	}
	if err := graphqlResponseError([]byte(`{"data":{"widgets":[]}}`)); err != nil {
		t.Fatalf("error = %v", err)
	}
}
