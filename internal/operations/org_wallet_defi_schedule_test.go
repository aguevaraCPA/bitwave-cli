package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"
)

func TestDefiScheduleAllDryRunTargetsNetworkEndpoint(t *testing.T) {
	cmd := testDefinition(newOrgWalletDefiScheduleCmd())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--org", "org-1", "--all", "--network", "Monad", "--dry-run"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decode output %q: %v", out.String(), err)
	}
	request := result.Request.(map[string]any)
	if request["url"] != "https://api.bitwave.io/v3/orgs/org-1/defi/wallet-schedules" || request["method"] != "POST" {
		t.Fatalf("request = %#v", request)
	}
	if body := request["body"].(map[string]any); body["networkId"] != "monad" {
		t.Fatalf("body = %#v", body)
	}
}

func TestDefiScheduleRequiresConfirmation(t *testing.T) {
	cmd := testDefinition(newOrgWalletDefiScheduleCmd())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--org", "org-1", "--all", "--network", "monad"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected confirmation error, got %v", err)
	}
}

func TestDefiScheduleOneWalletResolvesVaultAndPosts(t *testing.T) {
	var posted map[string]any
	runtime, err := op.NewRuntime(op.Options{
		WorkingDirectory: t.TempDir(), OrganizationID: "org-1", Token: "token", CoreBaseURL: "https://unit.invalid",
		HTTPClient: &http.Client{Transport: businessTransport(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/orgs/org-1/wallets":
				// The REST list omits networkId for DeFi wallets (observed in
				// production); the operation must fall back to GraphQL.
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"items":[
					{"id":"w-plain","name":"Treasury","networkId":"monad","address":"0xabc"},
					{"id":"w-stake","name":"Monad Staking","address":"0xabc","vaultAddress":"0x0000000000000000000000000000000000001000","protocol":"MonadStaking"}
				]}`))}, nil
			case r.Method == http.MethodPost && r.URL.Path == "/graphql":
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"data":{"wallets":[
					{"id":"w-plain","name":"Treasury","networkId":"monad"},
					{"id":"w-stake","name":"Monad Staking","networkId":"Monad"}
				]}}`))}, nil
			case r.Method == http.MethodPost && r.URL.Path == "/v3/orgs/org-1/wallets/w-stake/defi/wallet-schedule":
				if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"scheduleId":"defi-position-sync-w-stake","protocol":"MonadStaking","status":"SCHEDULED","message":"created"}`))}, nil
			}
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	definition := newOrgWalletDefiScheduleCmd()
	var output bytes.Buffer
	call := op.NewCall(op.WithRuntime(context.Background(), runtime), definition, nil, &output, io.Discard)
	if err := definition.Flags().Parse([]string{"--yes", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := definition.Invoke(call, []string{"monad staking"}); err != nil {
		t.Fatalf("%v (output: %s)", err, output.String())
	}
	if posted["walletAddress"] != "0xabc" || posted["contractAddress"] != "0x0000000000000000000000000000000000001000" || posted["networkId"] != "monad" {
		t.Fatalf("posted = %#v", posted)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("decode %q: %v", output.String(), err)
	}
	if result.Status != "scheduled" {
		t.Fatalf("status = %q", result.Status)
	}
}

func TestDefiScheduleNetworkOverrideSkipsGraphQL(t *testing.T) {
	var posted map[string]any
	runtime, err := op.NewRuntime(op.Options{
		WorkingDirectory: t.TempDir(), OrganizationID: "org-1", Token: "token", CoreBaseURL: "https://unit.invalid",
		HTTPClient: &http.Client{Transport: businessTransport(func(r *http.Request) (*http.Response, error) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/orgs/org-1/wallets":
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"items":[{"id":"w-stake","name":"Monad Staking","address":"0xabc","vaultAddress":"0x1000"}]}`))}, nil
			case r.Method == http.MethodPost && r.URL.Path == "/v3/orgs/org-1/wallets/w-stake/defi/wallet-schedule":
				if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"scheduleId":"s","protocol":"MonadStaking","status":"ALREADY_EXISTS"}`))}, nil
			}
			t.Fatalf("unexpected request %s %s (GraphQL must not be consulted when --network is given)", r.Method, r.URL.Path)
			return nil, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	definition := newOrgWalletDefiScheduleCmd()
	var output bytes.Buffer
	call := op.NewCall(op.WithRuntime(context.Background(), runtime), definition, nil, &output, io.Discard)
	if err := definition.Flags().Parse([]string{"--yes", "--json", "--network", "Monad"}); err != nil {
		t.Fatal(err)
	}
	if err := definition.Invoke(call, []string{"w-stake"}); err != nil {
		t.Fatalf("%v (output: %s)", err, output.String())
	}
	if posted["networkId"] != "monad" {
		t.Fatalf("posted = %#v", posted)
	}
	var result mutationEnvelope
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "already_exists" {
		t.Fatalf("status = %q", result.Status)
	}
}

func TestDefiScheduleRejectsPlainWallet(t *testing.T) {
	runtime, err := op.NewRuntime(op.Options{
		WorkingDirectory: t.TempDir(), OrganizationID: "org-1", Token: "token", CoreBaseURL: "https://unit.invalid",
		HTTPClient: &http.Client{Transport: businessTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(`{"items":[{"id":"w-plain","name":"Treasury","networkId":"monad","address":"0xabc"}]}`))}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	definition := newOrgWalletDefiScheduleCmd()
	call := op.NewCall(op.WithRuntime(context.Background(), runtime), definition, nil, io.Discard, io.Discard)
	if err := definition.Flags().Parse([]string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	err = definition.Invoke(call, []string{"Treasury"})
	if err == nil || !strings.Contains(err.Error(), "not a DeFi position wallet") {
		t.Fatalf("expected DeFi wallet error, got %v", err)
	}
}
