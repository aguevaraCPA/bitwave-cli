package orgreports

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestScheduleDefiWallet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/orgs/org-1/wallets/wallet-1/defi/wallet-schedule" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("missing bearer token: %q", r.Header.Get("Authorization"))
		}
		var body DefiScheduleRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.NetworkID != "monad" || body.ContractAddress != "0x0000000000000000000000000000000000001000" || body.WalletAddress != "0xabc" {
			t.Fatalf("unexpected body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scheduleId":"defi-position-sync-1","protocol":"MonadStaking","status":"SCHEDULED","message":"created"}`))
	}))
	defer server.Close()

	client := New(server.URL, func() (string, error) { return "token", nil })
	response, err := client.ScheduleDefiWallet(context.Background(), "org-1", "wallet-1", DefiScheduleRequest{
		WalletAddress: "0xabc", ContractAddress: "0x0000000000000000000000000000000000001000", NetworkID: "monad",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ScheduleID != "defi-position-sync-1" || response.Protocol != "MonadStaking" || response.Status != "SCHEDULED" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestScheduleDefiWalletsForNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v3/orgs/org-1/defi/wallet-schedules" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["networkId"] != "monad" {
			t.Fatalf("unexpected body: %#v", body)
		}
		if _, present := body["cadenceSeconds"]; present {
			t.Fatalf("cadenceSeconds should be omitted when not set: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scheduleId":"get-jobs-monad","runId":"run-1","status":"SCHEDULED"}`))
	}))
	defer server.Close()

	client := New(server.URL, func() (string, error) { return "token", nil })
	response, err := client.ScheduleDefiWalletsForNetwork(context.Background(), "org-1", "monad", 0)
	if err != nil {
		t.Fatal(err)
	}
	if response.ScheduleID != "get-jobs-monad" || response.RunID != "run-1" {
		t.Fatalf("unexpected response: %#v", response)
	}
}
