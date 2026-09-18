package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/auth"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/orgctx"
)

func runTerminalOperation(t *testing.T, path []string, args ...string) string {
	t.Helper()
	cmd := sdkCommand(sdkDefinition(path...))
	cmd.SetArgs(args)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("%v: %v\n%s", path, err, out.String())
	}
	return out.String()
}

func TestTerminalCloudWorkspaceUsesBoundOrgAndMatchingToken(t *testing.T) {
	for _, activeOrg := range []string{"", "other-org"} {
		t.Run("active="+activeOrg, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("BITWAVE_ORG_ID", "")
			t.Setenv("BITWAVE_TOKEN", "")
			t.Setenv("BITWAVE_AGENT_TOKEN", "")
			previous := tokenFlag
			tokenFlag = ""
			t.Cleanup(func() { tokenFlag = previous })
			if activeOrg != "" {
				if err := orgctx.Save(&orgctx.Active{OrgID: activeOrg}); err != nil {
					t.Fatal(err)
				}
			}
			dir := setupWorkspace(t)
			if err := config.Save(dir, &config.Config{Mode: config.ModeCloud, OrgId: "workspace-org", WorkspaceId: "ws-bound"}); err != nil {
				t.Fatal(err)
			}
			// Workspace lookup must continue to work from a nested directory.
			child := filepath.Join(dir, "child")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			t.Chdir(child)
			if err := auth.SaveCredentials(&auth.Credentials{AccessToken: "old-token", RefreshToken: "refresh-fixture", ExpiresAt: time.Now().Add(time.Hour).Unix(), OrgID: activeOrg}); err != nil {
				t.Fatal(err)
			}
			var exchanges atomic.Int32
			authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				exchanges.Add(1)
				if err := r.ParseForm(); err != nil || r.URL.Path != "/api/oauth/token" || r.Form.Get("scope") != "openid orgId:workspace-org" {
					t.Errorf("wrong workspace token scope: path=%s form=%v err=%v", r.URL.Path, r.Form, err)
					http.Error(w, "wrong org", http.StatusBadRequest)
					return
				}
				fmt.Fprint(w, `{"access_token":"workspace-token","refresh_token":"rotated-fixture","expires_in":3600}`)
			}))
			defer authServer.Close()
			t.Setenv("BITWAVE_AUTH_URL", authServer.URL)
			ledger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer workspace-token" {
					t.Error("workspace request did not use the bound organization's token")
				}
				switch r.URL.Path {
				case "/v1/workspaces/ws-bound/ledger/journals":
					fmt.Fprint(w, `[{"id":"journal-bound","name":"bound journal"}]`)
				case "/v1/workspaces/ws-bound":
					fmt.Fprint(w, `{"id":"ws-bound","orgId":"workspace-org","url":"https://ledger.example/workspace"}`)
				default:
					t.Errorf("unexpected ledger path: %s", r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer ledger.Close()
			t.Setenv("BITWAVE_BASE_URL_GL", ledger.URL)
			if out := runTerminalOperation(t, []string{"journal", "list"}); !strings.Contains(out, "journal-bound") {
				t.Fatalf("journal list=%s", out)
			}
			if out := runTerminalOperation(t, []string{"workspace", "current"}); strings.TrimSpace(out) != "ws-bound" {
				t.Fatalf("workspace current=%s", out)
			}
			if out := runTerminalOperation(t, []string{"workspace", "url"}); strings.TrimSpace(out) != "https://ledger.example/workspace" {
				t.Fatalf("workspace URL=%s", out)
			}
			if exchanges.Load() != 1 {
				t.Fatalf("unexpected token exchanges: %d", exchanges.Load())
			}
			active, err := orgctx.Load()
			if activeOrg == "" && !errors.Is(err, orgctx.ErrNoActiveOrg) || activeOrg != "" && (err != nil || active.OrgID != activeOrg) {
				t.Fatalf("workspace operation changed active platform org: %+v %v", active, err)
			}
		})
	}
}

func TestTerminalWorkspaceRebindTargetsActiveOrgOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_ORG_ID", "target-org")
	t.Setenv("BITWAVE_TOKEN", "target-token")
	t.Setenv("BITWAVE_AGENT_TOKEN", "")
	previous := tokenFlag
	tokenFlag = ""
	t.Cleanup(func() { tokenFlag = previous })
	dir := setupWorkspace(t)
	if err := config.Save(dir, &config.Config{Mode: config.ModeCloud, OrgId: "old-org", WorkspaceId: "old-workspace", Name: "keep-name", BaseCurrency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	ledger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/v1/orgs/target-org/workspaces" || r.Header.Get("Authorization") != "Bearer target-token" {
			t.Errorf("rebind accessed wrong org/workspace: %s", r.URL)
			http.Error(w, "wrong target", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `[{"id":"new-workspace","orgId":"target-org","name":"target name","baseCurrency":"USD"}]`)
	}))
	defer ledger.Close()
	t.Setenv("BITWAVE_BASE_URL_GL", ledger.URL)
	runTerminalOperation(t, []string{"workspace", "use"}, "new-workspace")
	cfg, err := config.Load(dir)
	if err != nil || cfg.OrgId != "target-org" || cfg.WorkspaceId != "new-workspace" || cfg.Name != "keep-name" || cfg.BaseCurrency != "EUR" || requests.Load() != 1 {
		t.Fatalf("bad rebind: %+v err=%v requests=%d", cfg, err, requests.Load())
	}
}

func TestTerminalWorkspaceSelectionDoesNotOverridePlatformOrCreationOrg(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("BITWAVE_ORG_ID", "active-org")
	dir := setupWorkspace(t)
	if err := config.Save(dir, &config.Config{Mode: config.ModeCloud, OrgId: "workspace-org", WorkspaceId: "ws-bound"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range [][]string{{"org", "wallets", "list"}, {"report", "balance"}, {"workspace", "create"}, {"workspace", "list"}, {"workspace", "use"}, {"workspace", "adopt"}, {"init"}, {"migrate"}} {
		d := sdkDefinition(path...)
		opts, err := terminalSDKOptionsForOperation(sdkCommand(d), d)
		if err != nil || opts.OrganizationID != "active-org" {
			t.Fatalf("%v selected org %q: %v", path, opts.OrganizationID, err)
		}
	}
	d := sdkDefinition("api", "request")
	cmd := sdkCommand(d)
	if err := cmd.Flags().Set("org", "explicit-org"); err != nil {
		t.Fatal(err)
	}
	opts, err := terminalSDKOptionsForOperation(cmd, d)
	if err != nil || opts.OrganizationID != "explicit-org" {
		t.Fatalf("explicit platform org changed: %+v %v", opts, err)
	}
}
