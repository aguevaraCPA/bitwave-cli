// Package store provides bitwave's workspace-aware storage implementations.
//
// The local store persists a workspace as a directory of plain-text files:
//   - accounts.ledger
//   - prices.ledger
//   - <name>.journal      (one or more)
//
// Each .journal file maps 1:1 to a Journal (id = filename stem, name = id
// title-cased). Entries are tagged with their journal during parse so
// SetEntryStatus can rewrite the correct file.
package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-cli/internal/bitwave/config"
)

// Filenames within a workspace directory.
const (
	AccountsFile = "accounts.ledger"
	PricesFile   = "prices.ledger"
	JournalExt   = ".journal"
)

// DefaultJournal is the journal id used when no journal is specified and
// none exists yet.
const DefaultJournal = "default"

// LocalWorkspace reads/writes a bitwave workspace as plain-text files.
type LocalWorkspace struct {
	Dir   string
	Cfg   *config.Config
	Files config.Filesystem
}

func (s *LocalWorkspace) files() config.Filesystem {
	if s.Files != nil {
		return s.Files
	}
	return config.OSFilesystem()
}

// OpenLocal opens an existing local workspace at dir. Returns
// config.ErrNotAWorkspace if the dir has no .bitwave.toml.
func OpenLocal(dir string) (*LocalWorkspace, error) {
	return OpenLocalFS(config.OSFilesystem(), dir)
}

func OpenLocalFS(files config.Filesystem, dir string) (*LocalWorkspace, error) {
	cfg, err := config.LoadFS(files, dir)
	if err != nil {
		return nil, err
	}
	if cfg.Mode != config.ModeLocal {
		return nil, fmt.Errorf("workspace at %s is in %s mode, not local", dir, cfg.Mode)
	}
	return &LocalWorkspace{Dir: dir, Cfg: cfg, Files: files}, nil
}

// InitLocal scaffolds an empty local workspace at dir. Refuses to clobber an
// existing .bitwave.toml.
func InitLocal(dir, name, baseCurrency string) (*LocalWorkspace, error) {
	return InitLocalFS(config.OSFilesystem(), dir, name, baseCurrency)
}

func InitLocalFS(files config.Filesystem, dir, name, baseCurrency string) (*LocalWorkspace, error) {
	if err := files.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if _, err := files.Stat(filepath.Join(dir, config.FileName)); err == nil {
		return nil, fmt.Errorf("workspace already initialized at %s", dir)
	}
	cfg := &config.Config{
		Mode:           config.ModeLocal,
		Name:           name,
		BaseCurrency:   baseCurrency,
		DefaultJournal: DefaultJournal,
	}
	if err := config.SaveFS(files, dir, cfg); err != nil {
		return nil, err
	}
	for _, f := range []string{AccountsFile, PricesFile} {
		if err := files.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
			return nil, err
		}
	}
	return &LocalWorkspace{Dir: dir, Cfg: cfg, Files: files}, nil
}

func (s *LocalWorkspace) path(name string) string { return filepath.Join(s.Dir, name) }

// JournalIds returns the ids of all .journal files in the workspace, sorted.
func (s *LocalWorkspace) JournalIds() ([]string, error) {
	entries, err := s.files().ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) != JournalExt {
			continue
		}
		ids = append(ids, strings.TrimSuffix(name, JournalExt))
	}
	sort.Strings(ids)
	return ids, nil
}

// EnsureJournal creates an empty <id>.journal file if it doesn't exist.
func (s *LocalWorkspace) EnsureJournal(ctx context.Context, id string) error {
	if err := ValidateJournalID(id); err != nil {
		return err
	}
	path := s.path(id + JournalExt)
	if _, err := s.files().Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return s.files().WriteFile(path, nil, 0o644)
}

// ValidateJournalID keeps journal IDs as filename stems, not paths. This also
// protects IDs decoded from entry prefixes and workspace config files.
func ValidateJournalID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00:") {
		return fmt.Errorf("invalid journal id %q: expected a filename stem", id)
	}
	return nil
}

// ResolveJournal picks the journal a write should target.
//
//   - explicit non-empty: returned as-is
//   - 0 journal files:    auto-create config.DefaultJournal (or DefaultJournal)
//     and return its id
//   - 1 journal file:     return that single id
//   - >=2 journal files:  return ErrAmbiguousJournal — caller must pass --journal
func (s *LocalWorkspace) ResolveJournal(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		if err := s.EnsureJournal(ctx, explicit); err != nil {
			return "", err
		}
		return explicit, nil
	}
	ids, err := s.JournalIds()
	if err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		id := s.Cfg.DefaultJournal
		if id == "" {
			id = DefaultJournal
		}
		if err := s.EnsureJournal(ctx, id); err != nil {
			return "", err
		}
		return id, nil
	case 1:
		return ids[0], nil
	default:
		return "", ErrAmbiguousJournal
	}
}

// ErrAmbiguousJournal is returned by ResolveJournal when the workspace has
// multiple .journal files and no explicit journal was passed.
var ErrAmbiguousJournal = errors.New("multiple journals in workspace; pass --journal to disambiguate")

// ParseJournalEntries returns the entries in a single journal file, with
// synthetic ids assigned. Used by `bitwave migrate` to push journals one at a
// time.
func (s *LocalWorkspace) ParseJournalEntries(id string) ([]model.Entry, error) {
	return s.parseJournal(id)
}

func (s *LocalWorkspace) parseJournal(id string) ([]model.Entry, error) {
	if err := ValidateJournalID(id); err != nil {
		return nil, err
	}
	data, err := s.files().ReadFile(s.path(id + JournalExt))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	p, err := format.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for i := range p.Entries {
		if p.Entries[i].ID == "" {
			p.Entries[i].ID = syntheticEntryID(id, p.Entries[i], i)
		}
	}
	return p.Entries, nil
}

func (s *LocalWorkspace) parseFile(name string) (*model.Project, error) {
	data, err := s.files().ReadFile(s.path(name))
	if err != nil {
		if os.IsNotExist(err) {
			return &model.Project{}, nil
		}
		return nil, err
	}
	return format.Parse(bytes.NewReader(data))
}

// Project parses every .ledger and .journal file and merges them into one
// in-memory project for reports.
func (s *LocalWorkspace) Project(ctx context.Context) (*model.Project, error) {
	out := &model.Project{
		Name:         s.Cfg.Name,
		BaseCurrency: s.Cfg.BaseCurrency,
	}
	for _, f := range []string{AccountsFile, PricesFile} {
		p, err := s.parseFile(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out.Accounts = append(out.Accounts, p.Accounts...)
		out.Prices = append(out.Prices, p.Prices...)
	}
	ids, err := s.JournalIds()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		ents, err := s.parseJournal(id)
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", id, JournalExt, err)
		}
		out.Entries = append(out.Entries, ents...)
	}
	return out, nil
}

// AddAccount appends to accounts.ledger.
func (s *LocalWorkspace) AddAccount(ctx context.Context, a model.Account) error {
	if a.Type == "" {
		a.Type = model.InferAccountType(a.Name)
	}
	tmp := &model.Project{Accounts: []model.Account{a}}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return err
	}
	line := strings.TrimRight(buf.String(), "\n") + "\n"
	return s.appendFile(s.path(AccountsFile), line)
}

// AddPrice appends to prices.ledger.
func (s *LocalWorkspace) AddPrice(ctx context.Context, p model.Price) error {
	tmp := &model.Project{Prices: []model.Price{p}}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return err
	}
	return s.appendFile(s.path(PricesFile), strings.TrimSpace(buf.String())+"\n")
}

// AddEntryToJournal appends e to <journalId>.journal. The synthetic id for the
// entry is returned so the caller can pass it back to SetEntryStatus.
func (s *LocalWorkspace) AddEntryToJournal(ctx context.Context, journalId string, e model.Entry) (string, error) {
	if e.Date.IsZero() {
		e.Date = time.Now()
	}
	if err := s.EnsureJournal(ctx, journalId); err != nil {
		return "", err
	}
	tmp := &model.Project{Entries: []model.Entry{e}}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return "", err
	}
	out := strings.TrimSpace(buf.String()) + "\n\n"
	if err := s.appendFile(s.path(journalId+JournalExt), out); err != nil {
		return "", err
	}
	ents, err := s.parseJournal(journalId)
	if err != nil {
		return "", err
	}
	if len(ents) == 0 {
		return "", nil
	}
	return ents[len(ents)-1].ID, nil
}

// SetEntryStatus rewrites the journal file containing entryID. The journal is
// derived from the id prefix (everything before the first ":").
func (s *LocalWorkspace) SetEntryStatus(ctx context.Context, entryID string, status model.Status, postingAccount string) error {
	journalId, _, ok := splitEntryID(entryID)
	if !ok {
		return fmt.Errorf("entry id %q does not embed a journal prefix", entryID)
	}
	ents, err := s.parseJournal(journalId)
	if err != nil {
		return err
	}
	hit := false
	for i := range ents {
		if ents[i].ID != entryID {
			continue
		}
		hit = true
		if postingAccount != "" {
			updated := false
			for j := range ents[i].Postings {
				if ents[i].Postings[j].Account == postingAccount {
					st := status
					ents[i].Postings[j].Status = &st
					updated = true
				}
			}
			if !updated {
				return fmt.Errorf("entry %s has no posting on account %s", entryID, postingAccount)
			}
		} else {
			ents[i].Status = status
		}
		break
	}
	if !hit {
		return fmt.Errorf("entry not found: %s", entryID)
	}
	tmp := &model.Project{Entries: ents}
	var buf bytes.Buffer
	if err := format.Print(&buf, tmp); err != nil {
		return err
	}
	return s.files().WriteFile(s.path(journalId+JournalExt), buf.Bytes(), 0o644)
}

// Journals satisfies the Store interface (alias for JournalIds, ignoring ctx).
func (s *LocalWorkspace) Journals(ctx context.Context) ([]string, error) {
	return s.JournalIds()
}

// AddEntry satisfies the Store interface; delegates to AddEntryToJournal.
func (s *LocalWorkspace) AddEntry(ctx context.Context, journalId string, e model.Entry) (string, error) {
	return s.AddEntryToJournal(ctx, journalId, e)
}

// Import satisfies the Store interface; delegates to AppendRaw.
func (s *LocalWorkspace) Import(ctx context.Context, journalId string, p *model.Project) error {
	return s.AppendRaw(ctx, journalId, p)
}

// AppendRaw imports a parsed project into journalId, appending accounts and
// prices to their canonical files.
func (s *LocalWorkspace) AppendRaw(ctx context.Context, journalId string, p *model.Project) error {
	for _, a := range p.Accounts {
		if err := s.AddAccount(ctx, a); err != nil {
			return err
		}
	}
	for _, pr := range p.Prices {
		if err := s.AddPrice(ctx, pr); err != nil {
			return err
		}
	}
	for _, e := range p.Entries {
		if _, err := s.AddEntryToJournal(ctx, journalId, e); err != nil {
			return err
		}
	}
	return nil
}

// syntheticEntryID returns a stable id for a parsed entry, prefixed with the
// journal so SetEntryStatus can locate the file. Format: "<journal>:<YYYYMMDD>-<seq>".
func syntheticEntryID(journalId string, e model.Entry, idx int) string {
	return fmt.Sprintf("%s:%s-%04d", journalId, e.Date.Format("20060102"), idx)
}

func splitEntryID(id string) (journal, rest string, ok bool) {
	i := strings.Index(id, ":")
	if i < 0 {
		return "", id, false
	}
	return id[:i], id[i+1:], true
}

func (s *LocalWorkspace) appendFile(path, content string) error {
	f, err := s.files().OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = f.WriteString(content)
	return err
}
