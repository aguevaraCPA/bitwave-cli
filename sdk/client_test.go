package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type contractTransport func(*http.Request) (*http.Response, error)

func (f contractTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func contractResponse(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func contractJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPublicCatalogDescribesSharedOperations(t *testing.T) {
	list, err := Operations()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 100 {
		t.Fatalf("unexpectedly small catalog: %d", len(list))
	}
	byName := make(map[string]Operation, len(list))
	for i, operation := range list {
		if operation.Name == "" || operation.Description == "" || len(operation.Path) == 0 {
			t.Fatalf("incomplete descriptor: %#v", operation)
		}
		if i > 0 && list[i-1].Name >= operation.Name {
			t.Fatalf("catalog is duplicated or unordered: %s", operation.Name)
		}
		if operation.InputSchema["additionalProperties"] != false {
			t.Fatalf("unbounded inputs: %s", operation.Name)
		}
		for name, raw := range operation.InputSchema["properties"].(map[string]any) {
			if _, present := raw.(map[string]any)["default"]; present {
				t.Fatalf("%s.%s schema default erases input presence", operation.Name, name)
			}
		}
		byName[operation.Name] = operation
	}
	for _, name := range []string{"bitwave_api_request", "bitwave_org_wallets_add", "bitwave_je_new", "bitwave_report_balance"} {
		if _, exists := byName[name]; !exists {
			t.Errorf("missing %s", name)
		}
	}
	for _, name := range []string{"bitwave_auth_login", "bitwave_upgrade", "bitwave_telemetry", "bitwave_org_use"} {
		if _, exists := byName[name]; exists {
			t.Errorf("terminal-owned operation exposed: %s", name)
		}
	}
	api := byName["bitwave_api_request"]
	if !reflect.DeepEqual(api.Path, []string{"api", "request"}) {
		t.Fatalf("API path=%v", api.Path)
	}
	properties := api.InputSchema["properties"].(map[string]any)
	positional := properties["arguments"].(map[string]any)
	if positional["minItems"] != 2 || positional["maxItems"] != 2 {
		t.Fatalf("API bounds=%v", positional)
	}
	if properties["yes"].(map[string]any)["type"] != "boolean" {
		t.Fatal("yes must be a typed boolean")
	}
	walletProps := byName["bitwave_org_wallets_add"].InputSchema["properties"].(map[string]any)
	if walletProps["sync-start-sec"].(map[string]any)["type"] != "integer" {
		t.Fatal("wallet timestamp must retain integer type")
	}
}

func TestPublicAdaptersExecuteConcurrentlyWithoutProcessStateChanges(t *testing.T) {
	t.Setenv("BITWAVE_ORG_ID", "ambient-org")
	t.Setenv("BITWAVE_TOKEN", "ambient-token")
	t.Setenv("BITWAVE_AGENT_TOKEN", "ambient-agent")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	started, done := make(chan string, 2), make(chan error, 2)
	release := make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	defer finish()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i, suffix := range []string{"typed", "argv"} {
		options := Options{WorkingDirectory: t.TempDir(), OrganizationID: "org-" + suffix, Token: "token-" + suffix, CoreBaseURL: "https://unit.invalid"}
		options.HTTPClient = &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/reports/view" || r.URL.Query().Get("orgId") != "org-"+suffix || r.Header.Get("Authorization") != "Bearer token-"+suffix {
				return nil, fmt.Errorf("scope mixed: %s %s", r.URL, r.Header.Get("Authorization"))
			}
			started <- suffix
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return contractResponse(r, `{"id":"run-`+suffix+`"}`), nil
		})}
		go func() {
			var output, diagnostics string
			var err error
			if i == 0 {
				result, e := NewClient(options).Invoke(ctx, Request{Operation: "bitwave_report_balance", Arguments: json.RawMessage(`{"as-of":"2026-06-30","no-wait":true}`)})
				output, diagnostics, err = result.Output, result.Diagnostics, e
			} else {
				result := ExecuteWithOptions(ctx, ExecuteOptions{Args: []string{"report", "balance", "--as-of", "2026-06-30", "--no-wait"}, ClientOptions: options})
				output, diagnostics = result.Stdout, result.Stderr
				if result.ExitCode != 0 {
					err = fmt.Errorf("argv failed: %+v", result)
				}
				if result.Directory != options.WorkingDirectory {
					err = fmt.Errorf("directory not preserved: %s", result.Directory)
				}
			}
			if err == nil && (output != "run-"+suffix+"\n" || !strings.Contains(diagnostics, "org=org-"+suffix) || !strings.Contains(diagnostics, "runId=run-"+suffix)) {
				err = fmt.Errorf("mixed stdout/diagnostics: %q %q", output, diagnostics)
			}
			done <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("call failed before overlap: %v", err)
		case <-ctx.Done():
			t.Fatal("calls did not overlap")
		}
	}
	if got, err := os.Getwd(); err != nil || got != cwd {
		t.Fatalf("cwd changed during in-flight calls: %q %v", got, err)
	}
	if os.Getenv("BITWAVE_ORG_ID") != "ambient-org" || os.Getenv("BITWAVE_TOKEN") != "ambient-token" || os.Getenv("BITWAVE_AGENT_TOKEN") != "ambient-agent" {
		t.Fatal("process environment changed during calls")
	}
	finish()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestPublicInvocationNeverFallsBackToAmbientCredentials(t *testing.T) {
	t.Setenv("BITWAVE_TOKEN", "ambient-token")
	t.Setenv("BITWAVE_AGENT_TOKEN", "ambient-agent")
	var calls atomic.Int32
	options := Options{OrganizationID: "org-1", CoreBaseURL: "https://unit.invalid", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) { calls.Add(1); return contractResponse(r, `{}`), nil })}}
	request := Request{Operation: "bitwave_api_request", Arguments: json.RawMessage(`{"arguments":["GET","/orgs/{org}"]}`)}
	if _, err := NewClient(options).Invoke(context.Background(), request); err == nil || !strings.Contains(err.Error(), "explicit token") {
		t.Fatalf("missing token error=%v", err)
	}
	result := ExecuteWithOptions(context.Background(), ExecuteOptions{Args: []string{"api", "request", "GET", "/orgs/{org}"}, ClientOptions: options})
	if result.ExitCode == 0 || !strings.Contains(result.Stderr, "explicit token") {
		t.Fatalf("argv borrowed ambient credentials: %+v", result)
	}
	if calls.Load() != 0 {
		t.Fatalf("unauthenticated requests sent: %d", calls.Load())
	}
}

func TestPublicScopeOverrideIsRejectedBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	options := Options{OrganizationID: "org-1", Token: "token", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) { calls.Add(1); return contractResponse(r, `{}`), nil })}}
	for _, org := range []string{"org-2", ""} {
		opts := options
		if org == "" {
			opts.OrganizationID = ""
		}
		_, err := NewClient(opts).Invoke(context.Background(), Request{Operation: "bitwave_api_request", Arguments: json.RawMessage(`{"arguments":["GET","/orgs/{org}"],"org":"org-2"}`)})
		if err == nil {
			t.Fatalf("accepted org override with runtime org=%q", opts.OrganizationID)
		}
		result := ExecuteWithOptions(context.Background(), ExecuteOptions{Args: []string{"api", "request", "GET", "/orgs/{org}", "--org", "org-2"}, ClientOptions: opts})
		if result.ExitCode == 0 {
			t.Fatalf("argv accepted org override with runtime org=%q", opts.OrganizationID)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("scope mismatch sent HTTP")
	}
}

func TestPublicAPIRequestPreservesJSONAndExplicitFalse(t *testing.T) {
	body := `{"id":9007199254740993,"enabled":false,"amount":0.000000000000000001}`
	var calls atomic.Int32
	options := Options{OrganizationID: "org-1", Token: "token", CoreBaseURL: "https://unit.invalid", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		actual, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		if string(actual) != body || r.Header.Get("Content-Type") != "application/json" {
			return nil, fmt.Errorf("request body changed: %s", actual)
		}
		return contractResponse(r, string(actual)), nil
	})}}
	client := NewClient(options)
	input := map[string]any{"arguments": []string{"POST", "/orgs/{org}/data"}, "data": body, "yes": false, "read-only": false}
	if _, err := client.Invoke(context.Background(), Request{Operation: "bitwave_api_request", Arguments: contractJSON(t, input)}); err == nil {
		t.Fatal("false confirmation authorized mutation")
	}
	if calls.Load() != 0 {
		t.Fatal("false confirmation performed HTTP")
	}
	input["yes"] = true
	result, err := client.Invoke(context.Background(), Request{Operation: "bitwave_api_request", Arguments: contractJSON(t, input)})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != body {
		t.Fatalf("typed output changed numeric precision: %s", result.Data)
	}
	argv := ExecuteWithOptions(context.Background(), ExecuteOptions{Args: []string{"api", "request", "POST", "/orgs/{org}/data", "--yes", "--data", body}, ClientOptions: options})
	if argv.ExitCode != 0 || strings.TrimSpace(argv.Stdout) != body {
		t.Fatalf("argv changed JSON: %+v", argv)
	}
	// A reused client must not retain the previous invocation's confirmation.
	input["yes"] = false
	if _, err := client.Invoke(context.Background(), Request{Operation: "bitwave_api_request", Arguments: contractJSON(t, input)}); err == nil {
		t.Fatal("previous invocation's confirmation leaked into explicit false")
	}
	delete(input, "yes")
	if _, err := client.Invoke(context.Background(), Request{Operation: "bitwave_api_request", Arguments: contractJSON(t, input)}); err == nil {
		t.Fatal("previous invocation's confirmation leaked into omitted flag")
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected mutation count: %d", calls.Load())
	}
}

func TestPublicIntegerFlagsPreserveExactValues(t *testing.T) {
	const exact = "9007199254740993"
	options := Options{OrganizationID: "org-1"}
	result, err := NewClient(options).Invoke(context.Background(), Request{Operation: "bitwave_org_wallets_add", Arguments: json.RawMessage(`{"name":"Treasury","address":"0xabc","network":"eth","sync-start-sec":` + exact + `,"dry-run":true,"json":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, exact) {
		t.Fatalf("integer rounded: %s", result.Output)
	}
	argv := ExecuteWithOptions(context.Background(), ExecuteOptions{Args: []string{"org", "wallets", "add", "--name", "Treasury", "--address", "0xabc", "--network", "eth", "--sync-start-sec", exact, "--dry-run", "--json"}, ClientOptions: options})
	if argv.ExitCode != 0 || !strings.Contains(argv.Stdout, exact) {
		t.Fatalf("argv integer rounded: %+v", argv)
	}
}

func TestPublicRequestInputFeedsRuleDryRun(t *testing.T) {
	body := `{"transfer":{"name":"ETH inflows","disabled":true,"priority":1,"accountingConnectionId":"ac-1","action":{"type":"SimpleCategorization"}}}`
	options := Options{OrganizationID: "org-1"}
	result, err := NewClient(options).Invoke(context.Background(), Request{Operation: "bitwave_rule_create", Arguments: json.RawMessage(`{"input":"-","dry-run":true,"json":true}`), Input: strings.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Output, `"status": "preview"`) || !strings.Contains(result.Output, "ETH inflows") {
		t.Fatalf("request input lost: %s", result.Output)
	}
	argv := ExecuteWithOptions(context.Background(), ExecuteOptions{Args: []string{"rules", "create", "--input", "-", "--dry-run", "--json"}, ClientOptions: options, Input: strings.NewReader(body)})
	if argv.ExitCode != 0 || argv.Stdout != result.Output {
		t.Fatalf("adapters disagree: %+v typed=%s", argv, result.Output)
	}
}

func TestPublicCancellationReachesHTTPAndPreservesExitCode(t *testing.T) {
	for _, argv := range []bool{false, true} {
		t.Run(fmt.Sprintf("argv=%v", argv), func(t *testing.T) {
			started := make(chan struct{})
			options := Options{OrganizationID: "org-1", Token: "token", CoreBaseURL: "https://unit.invalid", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
				close(started)
				<-r.Context().Done()
				return nil, r.Context().Err()
			})}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if argv {
					result := ExecuteWithOptions(ctx, ExecuteOptions{Args: []string{"api", "request", "GET", "/orgs/{org}"}, ClientOptions: options})
					if result.ExitCode != 130 {
						done <- fmt.Errorf("exit code=%d, stderr=%s", result.ExitCode, result.Stderr)
					} else {
						done <- nil
					}
				} else {
					_, err := NewClient(options).Invoke(ctx, Request{Operation: "bitwave_api_request", Arguments: json.RawMessage(`{"arguments":["GET","/orgs/{org}"]}`)})
					if !errors.Is(err, context.Canceled) {
						done <- fmt.Errorf("cancellation not preserved: %v", err)
					} else {
						done <- nil
					}
				}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("request never started")
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation did not return")
			}
		})
	}
}
