package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublicConcurrentLedgerWritesKeepOwnIDsAndContent(t *testing.T) {
	for _, precreate := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-journal=%v", precreate), func(t *testing.T) {
			directory := t.TempDir()
			alias := filepath.Join(t.TempDir(), "workspace-alias")
			if err := os.Symlink(directory, alias); err != nil {
				t.Fatal(err)
			}
			options := Options{WorkingDirectory: directory}
			client := NewClient(options)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := client.Invoke(ctx, Request{Operation: "bitwave_init"}); err != nil {
				t.Fatal(err)
			}
			if precreate {
				if _, err := client.Invoke(ctx, Request{Operation: "bitwave_journal_new", Arguments: json.RawMessage(`{"arguments":["default"]}`)}); err != nil {
					t.Fatal(err)
				}
			}
			const writers = 24
			type outcome struct {
				id, payee string
				err       error
			}
			results := make(chan outcome, writers)
			start := make(chan struct{})
			var group sync.WaitGroup
			for i := 0; i < writers; i++ {
				group.Add(1)
				go func(i int) {
					defer group.Done()
					<-start
					invocationOptions := options
					if i%2 == 0 {
						invocationOptions.WorkingDirectory = alias
					}
					// Separate clients/adapters must share the same workspace lock.
					invocationClient := NewClient(invocationOptions)
					payee := fmt.Sprintf("writer-%03d", i)
					input, _ := json.Marshal(map[string]any{"date": "2026-09-17", "payee": payee, "posting": []string{"Expenses:Food $1", "Assets:Cash -$1"}})
					result, err := invocationClient.Invoke(ctx, Request{Operation: "bitwave_je_new", Arguments: input})
					id := strings.TrimSpace(strings.TrimPrefix(result.Output, "Added entry "))
					if err == nil {
						// Clears rewrite the whole file and must not lose concurrent appends.
						clearInput, _ := json.Marshal(map[string]any{"arguments": []string{id}})
						_, err = invocationClient.Invoke(ctx, Request{Operation: "bitwave_je_clear", Arguments: clearInput})
					}
					results <- outcome{id: id, payee: payee, err: err}
				}(i)
			}
			close(start)
			group.Wait()
			close(results)
			seen := make(map[string]bool)
			for result := range results {
				if result.err != nil {
					t.Fatal(result.err)
				}
				if seen[result.id] {
					t.Fatalf("duplicate returned entry ID %s", result.id)
				}
				seen[result.id] = true
				input := contractJSON(t, map[string]any{"arguments": []string{result.id}})
				show, err := client.Invoke(ctx, Request{Operation: "bitwave_je_show", Arguments: input})
				if err != nil || !strings.Contains(show.Output, "* "+result.payee+"\n") {
					t.Fatalf("entry %s belongs to wrong writer or clear was lost: output=%q err=%v", result.id, show.Output, err)
				}
			}
			contents, err := os.ReadFile(filepath.Join(directory, "default.journal"))
			if err != nil || strings.Count(string(contents), "writer-") != writers {
				t.Fatalf("lost journal writes: err=%v contents=%s", err, contents)
			}
		})
	}
}

func TestWorkspaceLockCancellationAndIdleCleanup(t *testing.T) {
	directory := t.TempDir()
	ctx := context.Background()
	client := NewClient(Options{WorkingDirectory: directory})
	if _, err := client.Invoke(ctx, Request{Operation: "bitwave_init"}); err != nil {
		t.Fatal(err)
	}
	release, err := acquireWorkspaceCall(ctx, []string{"je", "new"}, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	waitCtx, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	defer cancel()
	_, err = client.Invoke(waitCtx, Request{Operation: "bitwave_journal_new", Arguments: json.RawMessage(`{"arguments":["must-not-exist"]}`)})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting call ignored cancellation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "must-not-exist.journal")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled call mutated workspace: %v", err)
	}
	release()
	key, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	workspaceCalls.mu.Lock()
	_, retained := workspaceCalls.entries[key]
	workspaceCalls.mu.Unlock()
	if retained {
		t.Fatal("idle workspace lock key was retained")
	}
}

func TestWorkspaceLockDoesNotSerializeUnrelatedOrRemoteOperations(t *testing.T) {
	directory := t.TempDir()
	release, err := acquireWorkspaceCall(context.Background(), []string{"je", "new"}, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, test := range []struct {
		directory string
		path      []string
	}{
		{t.TempDir(), []string{"je", "new"}},
		{directory, []string{"api", "request"}},
		{directory, []string{"report", "balance"}},
		{directory, []string{"org", "wallets", "add"}},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		unlock, err := acquireWorkspaceCall(ctx, test.path, test.directory)
		cancel()
		if err != nil {
			t.Fatalf("unrelated operation %v blocked: %v", test.path, err)
		}
		unlock()
	}
}
