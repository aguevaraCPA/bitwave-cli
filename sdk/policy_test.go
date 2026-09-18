package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/operation"
)

type panicWriter struct{}

func (panicWriter) Write([]byte) (int, error) { panic("sensitive panic detail") }

func TestInvokeContainsPanicsAndReleasesWorkspace(t *testing.T) {
	directory := t.TempDir()
	client := NewClient(Options{WorkingDirectory: directory})
	// init commits files before reporting success. A panic in its output sink
	// must not claim rollback or cause retry, and the workspace lock must close.
	result, err := client.Invoke(context.Background(), Request{Operation: "bitwave_init", Output: panicWriter{}})
	if !errors.Is(err, ErrOperationPanic) || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("panic was not sanitized: %v", err)
	}
	if !reflect.DeepEqual(result, Result{Operation: "bitwave_init"}) {
		t.Fatalf("panic returned partial output: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(directory, config.FileName)); err != nil {
		t.Fatalf("fixture must demonstrate a committed side effect: %v", err)
	}
	workspaceCalls.mu.Lock()
	locks := len(workspaceCalls.entries)
	workspaceCalls.mu.Unlock()
	if locks != 0 {
		t.Fatalf("panic leaked %d workspace locks", locks)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.Invoke(ctx, Request{Operation: "bitwave_journal_list"}); err != nil {
		t.Fatalf("workspace unusable after recovered panic: %v", err)
	}
}

func TestInvokeAndArgvContainTransportPanicWithoutDetails(t *testing.T) {
	var filesystemClosed func() bool
	options := Options{WorkingDirectory: t.TempDir(), OrganizationID: "org-1", Token: "credential", CoreBaseURL: "https://unit.invalid",
		HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
			files := operation.RuntimeFrom(r.Context()).Files
			filesystemClosed = func() bool { _, err := files.Stat("."); return err != nil }
			panic("Authorization: Bearer sensitive-token")
		})}}
	result, err := NewClient(options).Invoke(context.Background(), Request{
		Operation: "bitwave_api_request", Arguments: json.RawMessage(`{"arguments":["GET","/orgs/{org}"]}`),
	})
	if !errors.Is(err, ErrOperationPanic) || result.Output != "" || result.Diagnostics != "" || len(result.Data) != 0 {
		t.Fatalf("unexpected panic result: %+v %v", result, err)
	}
	if filesystemClosed == nil || !filesystemClosed() {
		t.Fatal("panic did not close request filesystem")
	}
	argv := ExecuteWithOptions(context.Background(), ExecuteOptions{ClientOptions: options, Args: []string{"api", "request", "GET", "/orgs/{org}"}})
	if argv.ExitCode == 0 || strings.Contains(argv.Stderr, "sensitive-token") || !strings.Contains(argv.Stderr, "outcome may be unknown") {
		t.Fatalf("argv panic handling: %+v", argv)
	}
}

func TestSDKEndpointPolicyUsesDeclarationNotFlagName(t *testing.T) {
	for _, name := range []string{"url", "destination", "upstream"} {
		for _, allow := range []bool{false, true} {
			dispatched := false
			d := &operation.Definition{Use: "future", RunE: func(*operation.Call, []string) error {
				dispatched = true
				return nil
			}}
			d.Flags().String(name, "", "Network destination")
			if err := d.MarkEndpointParameter(name); err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"https://attacker.invalid", ""} {
				_, err := NewClient(Options{AllowEndpointOverrides: allow}).invoke(context.Background(), Request{
					Operation: "bitwave_future", Arguments: contractJSON(t, map[string]any{name: value}),
				}, d)
				if allow && (err != nil || !dispatched) || !allow && (err == nil || dispatched) {
					t.Fatalf("name=%s allow=%v dispatched=%v err=%v", name, allow, dispatched, err)
				}
			}
		}
	}
}

func TestCatalogEndpointMetadataIsAuthoritativeAndIndependent(t *testing.T) {
	list, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, descriptor := range list {
		if len(descriptor.EndpointParameters) > 0 {
			got[descriptor.Name] = descriptor.EndpointParameters
		}
	}
	want := map[string][]string{"bitwave_wallets_send": {"rpc-url"}, "bitwave_wallets_sync": {"base-url"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("review endpoint metadata change: got %v want %v", got, want)
	}
	got["bitwave_wallets_send"][0] = "mutated"
	again, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range again {
		if descriptor.Name == "bitwave_wallets_send" && !reflect.DeepEqual(descriptor.EndpointParameters, []string{"rpc-url"}) {
			t.Fatal("catalog caller mutated authoritative endpoint policy")
		}
	}
}

// This human-readable snapshot is a review gate, including new parameters on
// EXISTING operations. Do not update blindly: review destination policy and
// hosted schemas whenever a parameter is added or its type/bounds change.
func TestCatalogParameterPolicyDrift(t *testing.T) {
	list, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot strings.Builder
	for _, descriptor := range list {
		fmt.Fprintf(&snapshot, "%s", descriptor.Name)
		properties := descriptor.InputSchema["properties"].(map[string]any)
		var names []string
		for name := range properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			shape := map[string]any{}
			for key, value := range properties[name].(map[string]any) {
				if key != "description" {
					shape[key] = value
				}
			}
			encoded, err := json.Marshal(shape)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&snapshot, " %s=%s", name, encoded)
		}
		fmt.Fprintf(&snapshot, " required=%v endpoints=%v\n", descriptor.InputSchema["required"], descriptor.EndpointParameters)
	}
	want, err := os.ReadFile("testdata/catalog_parameter_policy.txt")
	if err != nil || string(want) != snapshot.String() {
		t.Fatalf("catalog parameter policy changed; review before updating testdata/catalog_parameter_policy.txt: %v\nBEGIN_CATALOG_POLICY\n%sEND_CATALOG_POLICY", err, snapshot.String())
	}
}

func TestSDKCloudWorkspaceStillRequiresMatchingRequestOrg(t *testing.T) {
	directory := t.TempDir()
	if err := config.Save(directory, &config.Config{Mode: config.ModeCloud, OrgId: "workspace-org", WorkspaceId: "ws-1"}); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"", "other-org"} {
		calls := 0
		options := Options{WorkingDirectory: directory, OrganizationID: org, TokenResolver: func(context.Context, string) (string, error) {
			calls++
			return "credential", nil
		}, HTTPClient: &http.Client{Transport: contractTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("mismatched cloud workspace reached transport")
			return nil, nil
		})}}
		_, err := NewClient(options).Invoke(context.Background(), Request{Operation: "bitwave_journal_list"})
		if err == nil || !strings.Contains(err.Error(), "does not match") || calls != 0 {
			t.Fatalf("request org %q was not confined: calls=%d err=%v", org, calls, err)
		}
		argv := ExecuteWithOptions(context.Background(), ExecuteOptions{ClientOptions: options, Args: []string{"journal", "list"}})
		if argv.ExitCode == 0 || !strings.Contains(argv.Stderr, "does not match") || calls != 0 {
			t.Fatalf("argv request org %q was not confined: %+v calls=%d", org, argv, calls)
		}
	}
}

func TestSDKWorkspaceRebindingNeverAuthorizesOldOrganization(t *testing.T) {
	directory := t.TempDir()
	if err := config.Save(directory, &config.Config{Mode: config.ModeCloud, OrgId: "old-org", WorkspaceId: "old-workspace"}); err != nil {
		t.Fatal(err)
	}
	resolved, requested := 0, 0
	options := Options{WorkingDirectory: directory, OrganizationID: "target-org", GLBaseURL: "https://unit.invalid",
		TokenResolver: func(_ context.Context, org string) (string, error) {
			resolved++
			if org != "target-org" {
				t.Errorf("rebind authorized old organization %q", org)
			}
			return "target-token", nil
		}, HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
			requested++
			if r.URL.Path != "/v1/orgs/target-org/workspaces" || r.Header.Get("Authorization") != "Bearer target-token" {
				t.Errorf("rebind requested old workspace data: %s", r.URL)
			}
			return contractResponse(r, `[{"id":"target-workspace","orgId":"target-org","name":"target"}]`), nil
		})}}
	_, err := NewClient(options).Invoke(context.Background(), Request{Operation: "bitwave_workspace_use", Arguments: json.RawMessage(`{"arguments":["target-workspace"]}`)})
	if err != nil || resolved != 1 || requested != 1 {
		t.Fatalf("rebind failed: %v resolved=%d requested=%d", err, resolved, requested)
	}
	cfg, err := config.Load(directory)
	if err != nil || cfg.OrgId != "target-org" || cfg.WorkspaceId != "target-workspace" {
		t.Fatalf("rebind escaped target scope: %+v %v", cfg, err)
	}
}
