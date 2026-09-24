package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/operation"
)

func TestAdaptersPreserveStructuredAPIFailure(t *testing.T) {
	options := Options{OrganizationID: "org-1", Token: "test-token", CoreBaseURL: "https://unit.invalid", HTTPClient: &http.Client{Transport: contractTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"Insufficient permissions"}`)), Request: r}, nil
	})}}
	_, typed := NewClient(options).Invoke(context.Background(), Request{Operation: "bitwave_close_list", Arguments: json.RawMessage(`{}`)})
	cli := ExecuteWithOptions(context.Background(), ExecuteOptions{ClientOptions: options, Args: []string{"close", "list"}})
	for _, err := range []error{typed, cli.Err} {
		var api *APIError
		if !errors.As(err, &api) || api.Status != 403 || api.Detail != "Insufficient permissions" {
			t.Fatalf("missing structured failure: %v", err)
		}
	}
	if cli.ExitCode == 0 || !strings.Contains(cli.Stderr, "HTTP 403") {
		t.Fatal("terminal diagnostics changed")
	}
	encoded, err := json.Marshal(struct {
		Error error `json:"-"`
	}{Error: cli.Err})
	if err != nil || string(encoded) != "{}" {
		t.Fatal("typed error must not be serialized")
	}
	encoded, err = json.Marshal(cli)
	if err != nil || strings.Contains(string(encoded), `"Err"`) {
		t.Fatal("CLI error field serialized")
	}
}

func TestInvocationReportsWhichStreamWasTruncated(t *testing.T) {
	for _, diagnostics := range []bool{false, true} {
		d := &operation.Definition{Use: "fixture", RunE: func(call *operation.Call, _ []string) error {
			writer := call.OutOrStdout()
			if diagnostics {
				writer = call.ErrOrStderr()
			}
			_, err := fmt.Fprint(writer, strings.Repeat("x", maxOutput+1))
			return err
		}}
		result, err := NewClient(Options{}).invoke(context.Background(), Request{Operation: "fixture"}, d)
		if err != nil || !result.Truncated || result.OutputTruncated == diagnostics || result.DiagnosticsTruncated != diagnostics {
			t.Fatalf("incorrect stream flags: %+v %v", result, err)
		}
	}
}
