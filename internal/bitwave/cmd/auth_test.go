package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
)

func TestAuthHelpHidesUnfinishedAuthenticationChoices(t *testing.T) {
	var output bytes.Buffer
	command := newAuthCmd()
	command.SetOut(&output)
	command.SetArgs([]string{"--help"})

	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	help := output.String()
	if !strings.Contains(help, "login") {
		t.Fatalf("expected browser login in auth help: %s", help)
	}
	for _, unfinished := range []string{"delegate", "resume", "agent"} {
		if strings.Contains(help, unfinished) {
			t.Fatalf("unfinished command %q appeared in auth help: %s", unfinished, help)
		}
	}
}

func TestAuthLoginHelpDocumentsAgentOrgFlag(t *testing.T) {
	var output bytes.Buffer
	command := newAuthLoginCmd()
	command.SetOut(&output)
	command.SetArgs([]string{"--help"})

	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "--orgId") {
		t.Fatalf("expected --orgId in login help: %s", output.String())
	}
}

func TestSelectAuthenticatedOrgVerifiesRequestedOrg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/orgs/org-123" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"id":"org-123","name":"Acme"}`))
	}))
	defer server.Close()

	restoreAuthTestGlobals(t, server.URL)
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	if err := selectAuthenticatedOrg(command, "org-123"); err != nil {
		t.Fatal(err)
	}
	assertActiveOrg(t, "org-123", "Acme")
}

func TestSelectAuthenticatedOrgPromptsAfterLogin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_TOKEN", "test-token")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v3/orgs" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"orgs":[{"id":"org-1","name":"First"},{"id":"org-2","name":"Second"}]}`))
	}))
	defer server.Close()

	restoreAuthTestGlobals(t, server.URL)
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	command.SetIn(strings.NewReader("2\n"))
	if err := selectAuthenticatedOrg(command, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Select an organization") {
		t.Fatalf("expected organization picker: %s", output.String())
	}
	assertActiveOrg(t, "org-2", "Second")
}

func restoreAuthTestGlobals(t *testing.T, baseURL string) {
	t.Helper()
	previousBaseURL := defaultCoreBaseURL
	previousTokenFlag := tokenFlag
	defaultCoreBaseURL = baseURL
	tokenFlag = ""
	t.Cleanup(func() {
		defaultCoreBaseURL = previousBaseURL
		tokenFlag = previousTokenFlag
	})
}

func assertActiveOrg(t *testing.T, id, name string) {
	t.Helper()
	active, err := orgctx.Load()
	if err != nil {
		t.Fatal(err)
	}
	if active.OrgID != id || active.OrgName != name {
		t.Fatalf("unexpected active org: %#v", active)
	}
}
