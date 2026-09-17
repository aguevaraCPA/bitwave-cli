package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
	"github.com/spf13/cobra"
)

type terminalTestReader func([]byte) (int, error)

func (r terminalTestReader) Read(p []byte) (int, error) { return r(p) }

func setupWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := store.InitLocal(dir, "test", "USD"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

func TestSDKTerminalArgumentTypesAndPresence(t *testing.T) {
	d := &operation.Definition{Use: "test"}
	d.Flags().Bool("enabled", true, "")
	d.Flags().Bool("omitted", false, "")
	d.Flags().Int64("timestamp", 0, "")
	d.Flags().Uint64("large", 0, "")
	d.Flags().StringArray("item", nil, "")
	d.Flags().IntSlice("numbers", nil, "")
	d.Flags().BoolSlice("checks", nil, "")
	if err := d.Flags().Parse([]string{"--enabled=false", "--timestamp=1704067200", "--large=18446744073709551615", "--item=a,b", "--item=c", "--numbers=1,2", "--checks=true,false"}); err != nil {
		t.Fatal(err)
	}
	data, err := sdkArguments(d, []string{"--literal-positional"})
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if input["enabled"] != false {
		t.Fatalf("false disappeared: %s", data)
	}
	if _, exists := input["omitted"]; exists {
		t.Fatalf("omitted field became explicit: %s", data)
	}
	if input["timestamp"] != json.Number("1704067200") || input["large"] != json.Number("18446744073709551615") {
		t.Fatalf("integer precision lost: %s", data)
	}
	items := input["item"].([]any)
	if len(items) != 2 || items[0] != "a,b" {
		t.Fatalf("array values changed: %s", data)
	}
	if input["arguments"].([]any)[0] != "--literal-positional" {
		t.Fatalf("positional interpreted as a flag: %s", data)
	}
}

func TestSDKTerminalRunsSharedLedgerAndPayloadOperations(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_QUIET", "1")
	t.Setenv("BITWAVE_AGENT_TOKEN", "")
	t.Setenv("BITWAVE_TOKEN", "fake-token")
	dir := setupWorkspace(t)
	run := func(args []string, input string) string {
		t.Helper()
		cmd := NewRootCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs(args)
		if err := cmd.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	run([]string{"acct", "add", "Assets:Terminal"}, "")
	data, err := os.ReadFile(filepath.Join(dir, store.AccountsFile))
	if err != nil || !strings.Contains(string(data), "Assets:Terminal") {
		t.Fatalf("ledger not written through SDK: %s %v", data, err)
	}
	out := run([]string{"api", "request", "POST", "/test", "--org", "org-1", "--body-file", "-", "--dry-run", "--read-only"}, `{"active":false}`)
	if !strings.Contains(out, `"active": false`) && !strings.Contains(out, `"active":false`) {
		t.Fatalf("stdin payload not passed through SDK: %s", out)
	}
}

func TestSDKTerminalResolvesExplicitAuthAndOrg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_AGENT_TOKEN", "")
	t.Setenv("BITWAVE_TOKEN", "environment-token")
	t.Setenv("BITWAVE_ORG_ID", "")
	previous := tokenFlag
	t.Cleanup(func() { tokenFlag = previous })
	tokenFlag = "flag-token"
	if err := orgctx.Save(&orgctx.Active{OrgID: "selected-org"}); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("org", "", "")
	if err := cmd.Flags().Set("org", "explicit-org"); err != nil {
		t.Fatal(err)
	}
	opts, err := terminalSDKOptions(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if opts.OrganizationID != "explicit-org" || !opts.UnrestrictedFiles {
		t.Fatalf("wrong terminal options: org=%s files=%v", opts.OrganizationID, opts.UnrestrictedFiles)
	}
	token, err := opts.TokenResolver(context.Background(), opts.OrganizationID)
	if err != nil || token != "flag-token" {
		t.Fatalf("token precedence: %q %v", token, err)
	}
	t.Setenv("BITWAVE_AGENT_TOKEN", "agent-token")
	opts, err = terminalSDKOptions(cmd)
	if err != nil {
		t.Fatal(err)
	}
	token, err = opts.TokenResolver(context.Background(), opts.OrganizationID)
	if err != nil || token != "agent-token" {
		t.Fatalf("agent token precedence: %q %v", token, err)
	}
}

func TestSDKTerminalStreamsLargeOutputAndInteractivePrompt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_AGENT_TOKEN", "")
	t.Setenv("BITWAVE_TOKEN", "fake-token")
	t.Setenv("BITWAVE_ORG_ID", "org-1")
	setupWorkspace(t)
	large := `{"content":"` + strings.Repeat("x", (1<<20)+1024) + `","tail":"END_OF_PAYLOAD"}`
	cmd := sdkCommand(sdkDefinition("api", "request"))
	cmd.SetArgs([]string{"POST", "/test", "--body-file", "-", "--dry-run"})
	cmd.SetIn(strings.NewReader(large))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if out.Len() <= 1<<20 || !strings.Contains(out.String(), "END_OF_PAYLOAD") {
		t.Fatalf("terminal output was truncated: length %d", out.Len())
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id":"workspace-1","orgId":"org-1","name":"test","baseCurrency":"USD"}]`)
	}))
	defer server.Close()
	t.Setenv("BITWAVE_BASE_URL_GL", server.URL)
	out.Reset()
	cmd = sdkCommand(sdkDefinition("workspace", "use"))
	cmd.SetArgs([]string{})
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	input := strings.NewReader("1\n")
	cmd.SetIn(terminalTestReader(func(p []byte) (int, error) {
		if !strings.Contains(out.String(), "Pick a workspace:") || !strings.HasSuffix(out.String(), "> ") {
			return 0, fmt.Errorf("workspace prompt was not streamed before input")
		}
		return input.Read(p)
	}))
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("interactive SDK adapter: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Active workspace: test (workspace-1)") {
		t.Fatalf("workspace selection failed: %s", out.String())
	}
}
