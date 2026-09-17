package operations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/spf13/cobra"
)

func businessFileContext(t *testing.T, path string) context.Context {
	t.Helper()
	runtime, err := op.NewRuntime(op.Options{WorkingDirectory: filepath.Dir(path), OrganizationID: "org-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return op.WithRuntime(context.Background(), runtime)
}

func businessTestCall(t *testing.T, cmd *cobra.Command) *op.Call {
	t.Helper()
	definition := &op.Definition{Use: cmd.Use}
	definition.Flags().AddFlagSet(cmd.Flags())
	runtime, err := op.NewRuntime(op.Options{WorkingDirectory: t.TempDir(), OrganizationID: "org-1", Token: os.Getenv("BITWAVE_TOKEN"), CoreBaseURL: os.Getenv("BITWAVE_BASE_URL_CORE")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return op.NewCall(op.WithRuntime(cmd.Context(), runtime), definition, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

func TestOrganizationScopeCannotComeFromAmbientStateOrFlags(t *testing.T) {
	t.Setenv("BITWAVE_ORG_ID", "ambient-org")
	if _, err := resolveReportOrg(context.Background(), "flag-org"); err == nil {
		t.Fatal("explicit runtime scope is required even when flags/environment contain an org")
	}
	ctx := businessFileContext(t, filepath.Join(t.TempDir(), "input.json"))
	if _, err := resolveReportOrg(ctx, "other-org"); err == nil {
		t.Fatal("flags must not override the runtime organization")
	}
	if got, err := resolveReportOrg(ctx, ""); err != nil || got != "org-1" {
		t.Fatalf("resolved scope = %q, %v", got, err)
	}
}

type businessTransport func(*http.Request) (*http.Response, error)

func (f businessTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOrganizationOperationsRunConcurrentlyWithIsolatedIdentity(t *testing.T) {
	t.Setenv("BITWAVE_TOKEN", "ambient-token")
	t.Setenv("BITWAVE_ORG_ID", "ambient-org")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	release := make(chan struct{})
	closeRelease := sync.OnceFunc(func() { close(release) })
	defer closeRelease()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for _, suffix := range []string{"a", "b"} {
		dir := t.TempDir()
		runtime, err := op.NewRuntime(op.Options{
			WorkingDirectory: dir, OrganizationID: "org-" + suffix,
			Token: "token-" + suffix, CoreBaseURL: "https://unit.invalid",
			HTTPClient: &http.Client{Transport: businessTransport(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "Bearer token-"+suffix || r.URL.Path != "/v3/orgs/org-"+suffix+"/transactions/ETH.txn-1" {
					return nil, fmt.Errorf("scope mixup: %s %s", r.URL.Path, r.Header.Get("Authorization"))
				}
				started <- suffix
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"` + suffix + `"}`)), Request: r}, nil
			})},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		go func() {
			definition := newGetOrgTransactionCmd()
			var output bytes.Buffer
			call := op.NewCall(op.WithRuntime(ctx, runtime), definition, nil, &output, io.Discard)
			err := definition.Invoke(call, []string{"ETH.txn-1"})
			if err == nil && output.String() != `{"id":"`+suffix+`"}`+"\n" {
				err = fmt.Errorf("mixed result: %s", output.String())
			}
			results <- err
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case err := <-results:
			t.Fatalf("operation exited before overlap: %v", err)
		case <-ctx.Done():
			t.Fatal("operations serialized or failed to use injected transport")
		}
	}
	if got, err := os.Getwd(); err != nil || got != cwd {
		t.Fatalf("cwd changed during execution: %q %v", got, err)
	}
	if os.Getenv("BITWAVE_TOKEN") != "ambient-token" || os.Getenv("BITWAVE_ORG_ID") != "ambient-org" {
		t.Fatal("process environment changed during execution")
	}
	closeRelease()
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
