package sdk

import (
	"context"
	"path/filepath"
	"sync"
)

// Local ledger operations include read/modify/write transactions and synthetic
// entry IDs. Their entire invocation must be serialized for a workspace, not
// just individual file writes. Unrelated roots and remote-only operations do
// not contend. This coordinates this process only; callers sharing a workspace
// across processes/replicas must provide external coordination.
var workspaceCalls = workspaceLockSet{entries: make(map[string]*workspaceLock)}

type workspaceLock struct {
	available  chan struct{}
	references int
}

type workspaceLockSet struct {
	mu      sync.Mutex
	entries map[string]*workspaceLock
}

func localWorkspaceOperation(path []string) bool {
	if len(path) == 0 {
		return false
	}
	switch path[0] {
	case "workspace", "journal", "init", "je", "acct", "price", "wallets",
		"expense", "bal", "reg", "print", "accounts", "contacts", "commodities",
		"equity", "cleared", "csv", "stats", "migrate", "share", "shares":
		return true
	default:
		return false
	}
}

func acquireWorkspaceCall(ctx context.Context, path []string, directory string) (func(), error) {
	if directory == "" || !localWorkspaceOperation(path) {
		return func() {}, nil
	}
	key, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	// Canonicalize ordinary symlink aliases of a managed workspace. The file
	// capability still enforces the boundary when operations open each file.
	key, err = filepath.EvalSymlinks(key)
	if err != nil {
		return nil, err
	}
	return workspaceCalls.acquire(ctx, key)
}

func (s *workspaceLockSet) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	entry := s.entries[key]
	if entry == nil {
		entry = &workspaceLock{available: make(chan struct{}, 1)}
		entry.available <- struct{}{}
		s.entries[key] = entry
	}
	entry.references++
	s.mu.Unlock()
	dropReference := func() {
		s.mu.Lock()
		entry.references--
		if entry.references == 0 {
			delete(s.entries, key)
		}
		s.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		dropReference()
		return nil, ctx.Err()
	case <-entry.available:
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			entry.available <- struct{}{}
			dropReference()
		})
	}
	// A canceled waiter must not run merely because both select cases became
	// ready together.
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}
