// Package store provides bitwave's workspace-aware storage interface plus its
// local file-backed and cloud (the cloud ledger HTTP) implementations.
//
// bitwave adds a per-call journal id everywhere a write happens, so a workspace
// with multiple journals can be driven from the same CLI surface.
package store

import (
	"context"

	"github.com/bitwave-io/bitwave-accounting-sdk/model"
)

// Store is the bitwave-flavored ledger store. The journal id is passed through
// on every write so callers don't have to construct a new store per journal.
type Store interface {
	// Project loads the full workspace view (all journals merged).
	Project(ctx context.Context) (*model.Project, error)

	// Journals lists the journal ids in the workspace, sorted.
	Journals(ctx context.Context) ([]string, error)

	// EnsureJournal creates the named journal if it doesn't exist.
	EnsureJournal(ctx context.Context, journalId string) error

	// AddAccount declares an account at the workspace scope.
	AddAccount(ctx context.Context, a model.Account) error

	// AddPrice records a price observation at the workspace scope.
	AddPrice(ctx context.Context, p model.Price) error

	// AddEntry appends an entry to the named journal. Returns the assigned id.
	AddEntry(ctx context.Context, journalId string, e model.Entry) (string, error)

	// SetEntryStatus flips an entry (or one posting) status. The journal is
	// recovered from the entry id by the implementation.
	SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error

	// Import appends a parsed ledger blob to the named journal.
	Import(ctx context.Context, journalId string, p *model.Project) error
}

// compile-time conformance checks
var (
	_ Store = (*LocalWorkspace)(nil)
	_ Store = (*Cloud)(nil)
)

// JournalOfEntryID recovers the journal id from a journal-prefixed entry id
// ("<journal>:<YYYYMMDD>-<seq>"). Stores merge every journal into one Project,
// so the report layer needs this to scope a merged view back to one journal.
func JournalOfEntryID(id string) (string, bool) {
	j, _, ok := splitEntryID(id)
	return j, ok
}
