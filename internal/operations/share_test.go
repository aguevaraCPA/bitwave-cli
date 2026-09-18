package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/store"
)

func TestJournalShare_LocalMode_UploadsToShareEndpoint(t *testing.T) {
	dir := setupWorkspace(t)
	if _, err := store.OpenLocal(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "default.journal"), []byte("; my entries\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var gotPath, gotTo string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = r.ParseMultipartForm(32 << 20)
		gotTo = r.FormValue("to")
		_, _ = w.Write([]byte(`{"workspaceId":"ws-x","workflowId":"workspace-share-ws-x","workflowRunId":"run-1"}`))
	}))
	defer srv.Close()

	t.Setenv("BITWAVE_BASE_URL_GL", srv.URL)

	cmd := testDefinition(newShareCmd())
	cmd.SetArgs([]string{"--to", "a@b.co", "--journal", "default"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("share failed: %v\n%s", err, out.String())
	}
	if gotPath != "/v1/workspaces:share" {
		t.Errorf("expected POST to /v1/workspaces:share, got %s", gotPath)
	}
	if gotTo != "a@b.co" {
		t.Errorf("expected to=a@b.co, got %q", gotTo)
	}
}

func TestJournalShare_CloudMode_OmitsSnapshot(t *testing.T) {
	t.Setenv("BITWAVE_ORG_ID", "org-1")
	dir := setupWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "default.journal"), []byte("placeholder\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mode = config.ModeCloud
	cfg.OrgId = "org-1"
	cfg.WorkspaceId = "ws-1"
	if err := config.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cloud store may probe /v1/orgs/{org}/journals/{id} for EnsureJournal.
		// Return a minimal stub response for any GET; for the share POST capture the body.
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/shares") {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			_, _ = w.Write([]byte(`{
				"shareId":"sh-2","orgId":"org-1","journalId":"default","token":"tok2",
				"recipientEmail":"x@y.z","status":"ACTIVE",
				"expiresAt":"2099-01-01T00:00:00Z","viewCount":0,
				"createdAt":"2026-01-01T00:00:00Z","updatedAt":"2026-01-01T00:00:00Z",
				"url":"https://api.example.com/public/shares/tok2"
			}`))
			return
		}
		// Stub the journals listing (GET /v1/workspaces/{id}/ledger/journals).
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/ledger/journals") {
			_, _ = w.Write([]byte(`[{"id":"default","name":"default"}]`))
			return
		}
		// Anything else: minimal object stub.
		_, _ = w.Write([]byte(`{"id":"default","name":"default"}`))
	}))
	defer srv.Close()

	t.Setenv("BITWAVE_BASE_URL_GL", srv.URL)
	t.Setenv("BITWAVE_TOKEN", "tok")

	cmd := testDefinition(newShareCmd())
	cmd.SetArgs([]string{"--to", "x@y.z", "--journal", "default"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	if gotBody == nil {
		t.Fatal("share POST not received")
	}
	if _, hasSnapshot := gotBody["snapshot"]; hasSnapshot {
		t.Errorf("cloud mode should not include snapshot, got: %v", gotBody["snapshot"])
	}
}

func TestJournalShare_MissingTo_Errors(t *testing.T) {
	setupWorkspace(t)
	cmd := testDefinition(newShareCmd())
	cmd.SetArgs([]string{"--journal", "default"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected error for missing --to")
	}
}

func TestJournalShare_DryRun(t *testing.T) {
	dir := setupWorkspace(t)
	if err := os.WriteFile(filepath.Join(dir, "default.journal"), []byte("; entries\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cmd := testDefinition(newShareCmd())
	cmd.SetArgs([]string{"--to", "a@b.co", "--journal", "default", "--dry-run"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out.String())
	}
}
