// Package config handles the per-workspace .bitwave.toml file that records
// whether a workspace is local or cloud-backed.
//
// A bitwave workspace is a directory containing:
//   - .bitwave.toml        — this config (mode, currency, optional cloud ids)
//   - <name>.journal   — one or more journal files (local mode)
//   - accounts.ledger  — account declarations (local mode)
//   - prices.ledger    — price observations (local mode)
//
// In cloud mode the journal/account/price data lives in the cloud ledger; the local
// directory still holds .bitwave.toml as the workspace marker so cwd-aware
// commands can find it.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Filesystem is the file access capability supplied by a request. The SDK uses
// a rooted implementation; the terminal adapter retains ordinary OS access.
type Filesystem interface {
	Stat(string) (os.FileInfo, error)
	ReadFile(string) ([]byte, error)
	ReadDir(string) ([]os.DirEntry, error)
	MkdirAll(string, os.FileMode) error
	Create(string) (*os.File, error)
	OpenFile(string, int, os.FileMode) (*os.File, error)
	WriteFile(string, []byte, os.FileMode) error
}

type osFilesystem struct{}

func (osFilesystem) Stat(p string) (os.FileInfo, error)      { return os.Stat(p) }
func (osFilesystem) ReadFile(p string) ([]byte, error)       { return os.ReadFile(p) }
func (osFilesystem) ReadDir(p string) ([]os.DirEntry, error) { return os.ReadDir(p) }
func (osFilesystem) MkdirAll(p string, m os.FileMode) error  { return os.MkdirAll(p, m) }
func (osFilesystem) Create(p string) (*os.File, error)       { return os.Create(p) }
func (osFilesystem) OpenFile(p string, f int, m os.FileMode) (*os.File, error) {
	return os.OpenFile(p, f, m)
}
func (osFilesystem) WriteFile(p string, b []byte, m os.FileMode) error { return os.WriteFile(p, b, m) }

// OSFilesystem is solely for the legacy terminal entry points.
func OSFilesystem() Filesystem { return osFilesystem{} }

// FileName is the workspace marker file.
const FileName = ".bitwave.toml"

// Mode is "local" or "cloud".
type Mode string

const (
	ModeLocal Mode = "local"
	ModeCloud Mode = "cloud"
)

// Config is the on-disk per-workspace state.
type Config struct {
	Mode           Mode   `toml:"mode"`
	Name           string `toml:"name,omitempty"`
	BaseCurrency   string `toml:"base_currency"`
	OrgId          string `toml:"org_id,omitempty"`
	WorkspaceId    string `toml:"workspace_id,omitempty"`
	DefaultJournal string `toml:"default_journal,omitempty"`
}

// ErrNotAWorkspace is returned by Find/Load when no .bitwave.toml is present.
var ErrNotAWorkspace = errors.New("not a bitwave workspace (no .bitwave.toml)")

// legacyMarkers are workspace marker names from before the CLI was renamed
// to bitwave. The file format is identical — only the filename changed — so
// such workspaces just need the marker renamed.
var legacyMarkers = []string{".bwx.toml", ".wavie.toml"}

// Find walks up from start looking for a .bitwave.toml. Returns the workspace
// directory (the one containing the file). Returns ErrNotAWorkspace if none
// is found before the filesystem root; if a pre-rename marker is found along
// the way, the error says exactly how to adopt the workspace.
func Find(start string) (string, error) {
	return FindFS(OSFilesystem(), start)
}

// FindFS never probes through a filesystem capability's boundary.
func FindFS(files Filesystem, start string) (string, error) {
	dir := start
	for {
		if _, err := files.Stat(filepath.Join(dir, FileName)); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %v", ErrNotAWorkspace, err)
		}
		for _, legacy := range legacyMarkers {
			if _, err := files.Stat(filepath.Join(dir, legacy)); err == nil {
				return "", fmt.Errorf("%w; found pre-rename marker %s in %s — same format, only the filename changed: mv %s %s",
					ErrNotAWorkspace, legacy, dir, filepath.Join(dir, legacy), filepath.Join(dir, FileName))
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotAWorkspace
		}
		dir = parent
	}
}

// Load reads <dir>/.bitwave.toml.
func Load(dir string) (*Config, error) {
	return LoadFS(OSFilesystem(), dir)
}

func LoadFS(files Filesystem, dir string) (*Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := files.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotAWorkspace
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.BaseCurrency == "" {
		c.BaseCurrency = "USD"
	}
	return &c, nil
}

// Save writes the config to <dir>/.bitwave.toml with 0644 perms.
func Save(dir string, c *Config) error {
	return SaveFS(OSFilesystem(), dir, c)
}

func SaveFS(files Filesystem, dir string, c *Config) error {
	if err := files.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := files.Create(filepath.Join(dir, FileName))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return toml.NewEncoder(f).Encode(c)
}
