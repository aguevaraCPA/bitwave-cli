package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type failedStream struct{ err error }

func (f failedStream) Write([]byte) (int, error) { return 0, f.err }

func TestPublicStreamingPropagatesSinkErrors(t *testing.T) {
	broken := errors.New("stream disconnected")
	for _, tc := range []struct {
		name   string
		writer io.Writer
		want   error
	}{
		{"failure", failedStream{broken}, broken},
		{"short-write", failedStream{}, io.ErrShortWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewClient(Options{}).Invoke(context.Background(), Request{Operation: "bitwave_status", Output: tc.writer})
			if !errors.Is(err, tc.want) {
				t.Fatalf("stream failure swallowed: %v", err)
			}
		})
	}
}

func TestParseCommandPreservesFlagLookingValues(t *testing.T) {
	for _, value := range []string{"--help", "-h", "--quiet", "--quiet=true"} {
		request, help, err := ParseCommand([]string{"api", "request", "POST", "/orgs/{org}", "--data", value, "--dry-run"})
		if err != nil || help != "" {
			t.Fatalf("value %q interpreted as adapter control: help=%q err=%v", value, help, err)
		}
		var args map[string]any
		if err := json.Unmarshal(request.Arguments, &args); err != nil {
			t.Fatal(err)
		}
		if args["data"] != value {
			t.Fatalf("value %q became %v", value, args["data"])
		}
	}
}

func TestPublicStreamingWritesCompleteResponseWithoutDuplicateCapture(t *testing.T) {
	body := strings.Repeat("x", maxOutput+1024)
	options := Options{OrganizationID: "org-1", Token: "token", CoreBaseURL: "https://unit.invalid", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
		return contractResponse(r, body), nil
	})}}
	var output, diagnostics bytes.Buffer
	result, err := NewClient(options).Invoke(context.Background(), Request{Operation: "bitwave_api_request", Arguments: json.RawMessage(`{"arguments":["GET","/orgs/{org}"]}`), Output: &output, Diagnostics: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(output.String(), "\n") != body || result.Output != "" || result.Diagnostics != "" || diagnostics.Len() != 0 {
		t.Fatalf("bad streaming result: emitted=%d captured=%d diagnostics=%q", output.Len(), len(result.Output), result.Diagnostics)
	}
	if !result.Truncated || len(result.Data) != 0 {
		t.Fatalf("capture truncation metadata incorrect: %+v", result)
	}
}

func TestPublicWalletSyncRejectsUntrustedEndpointOverride(t *testing.T) {
	var sent []string
	options := Options{WorkingDirectory: t.TempDir(), OrganizationID: "org-1", Token: "scope-secret", BlockchainQueryBaseURL: "https://trusted.invalid", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
		sent = append(sent, fmt.Sprintf("%s %s", r.URL.Host, r.Header.Get("Authorization")))
		return contractResponse(r, `{"results":[]}`), nil
	})}}
	client := NewClient(options)
	for _, request := range []Request{
		{Operation: "bitwave_init", Arguments: json.RawMessage(`{}`)},
		{Operation: "bitwave_wallets_add", Arguments: json.RawMessage(`{"arguments":["0x0000000000000000000000000000000000000001"],"name":"watch","networks":"ethereum"}`)},
	} {
		if _, err := client.Invoke(context.Background(), request); err != nil {
			t.Fatalf("prepare local fixture: %v", err)
		}
	}
	_, err := client.Invoke(context.Background(), Request{Operation: "bitwave_wallets_sync", Arguments: json.RawMessage(`{"wallet":"watch","network":"ethereum","base-url":"https://attacker.invalid","dry-run":true}`)})
	if err == nil || len(sent) != 0 {
		t.Fatalf("untrusted endpoint override reached transport: err=%v requests=%v", err, sent)
	}
	argv := ExecuteWithOptions(context.Background(), ExecuteOptions{ClientOptions: options, Args: []string{"wallets", "sync", "--wallet", "watch", "--network", "ethereum", "--base-url", "https://attacker.invalid", "--dry-run"}})
	if argv.ExitCode == 0 || len(sent) != 0 {
		t.Fatalf("argv endpoint override reached transport: result=%+v requests=%v", argv, sent)
	}
}
