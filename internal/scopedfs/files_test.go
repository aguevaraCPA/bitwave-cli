package scopedfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testFiles(t *testing.T, dir string, unrestricted bool) *Files {
	t.Helper()
	files, err := New(dir, unrestricted)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := files.Close(); err != nil {
			t.Error(err)
		}
	})
	return files
}

func TestScopedFilesLegitimateOperations(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := testFiles(t, dir, false)
	if !files.Confined() {
		t.Fatal("hosted files must be confined")
	}
	if err := files.MkdirAll("nested/deeper", 0700); err != nil {
		t.Fatal(err)
	}
	if err := files.WriteFile("nested/data.txt", []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := files.ReadFile(filepath.Join(dir, "nested/data.txt"))
	if err != nil || string(data) != "first" {
		t.Fatalf("absolute in-root read: %q %v", data, err)
	}
	f, err := files.OpenFile("nested/data.txt", os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(" second"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = files.Open("nested/deeper/../data.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(f)
	if err != nil || string(data) != "first second" {
		t.Fatalf("open/read: %q %v", data, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = files.Create("nested/created.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := files.Rename("nested/data.txt", "nested/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Stat("nested/data.txt"); !os.IsNotExist(err) {
		t.Fatalf("old name still present: %v", err)
	}
	info, err := files.Stat("nested/renamed.txt")
	if err != nil || info.Size() != int64(len("first second")) {
		t.Fatalf("stat: %+v %v", info, err)
	}
	entries, err := files.ReadDir("nested")
	if err != nil || len(entries) != 3 {
		t.Fatalf("read directory: %+v %v", entries, err)
	}
	matches, err := files.Glob("nested/*.txt")
	if err != nil || len(matches) != 2 {
		t.Fatalf("glob: %v %v", matches, err)
	}
	for _, name := range matches {
		if !strings.HasPrefix(name, dir+string(filepath.Separator)) {
			t.Fatalf("glob returned unrooted path %q", name)
		}
	}
	tmp, err := files.CreateTemp("nested", "request-*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString("payload"); err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	data, err = files.ReadFile(tmpName)
	if err != nil || string(data) != "payload" {
		t.Fatalf("temporary file not usable through capability: %q %v", data, err)
	}
	if err := files.Remove(tmpName); err != nil {
		t.Fatal(err)
	}
	if err := files.Remove("nested/renamed.txt"); err != nil {
		t.Fatal(err)
	}
}

// deniedFileAccess checks all operations that would follow/read/write a file,
// not just string validation. A harmless external fixture must remain intact.
func deniedFileAccess(t *testing.T, files *Files, name string) {
	t.Helper()
	checks := []struct {
		name string
		run  func() error
	}{
		{"read", func() error { _, err := files.ReadFile(name); return err }},
		{"open", func() error {
			f, err := files.Open(name)
			if f != nil {
				f.Close()
			}
			return err
		}},
		{"open-file", func() error {
			f, err := files.OpenFile(name, os.O_RDWR|os.O_TRUNC, 0600)
			if f != nil {
				f.Close()
			}
			return err
		}},
		{"create", func() error {
			f, err := files.Create(name)
			if f != nil {
				f.Close()
			}
			return err
		}},
		{"write", func() error { return files.WriteFile(name, []byte("must not escape"), 0600) }},
		{"stat", func() error { _, err := files.Stat(name); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil {
				t.Fatalf("accepted outside file %q", name)
			}
		})
	}
}

func TestScopedFilesDenyTraversalAndAbsoluteOutsidePaths(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dir, outside := filepath.Join(parent, "workspace"), filepath.Join(parent, "outside")
	for _, path := range []string{dir, outside} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(outside, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	files := testFiles(t, dir, false)
	for _, path := range []string{"../outside/sentinel.txt", "nested/../../outside/sentinel.txt", sentinel} {
		t.Run(path, func(t *testing.T) { deniedFileAccess(t, files, path) })
	}
	for _, path := range []string{"../outside", outside} {
		if _, err := files.ReadDir(path); err == nil {
			t.Fatalf("read outside directory %q", path)
		}
		if _, err := files.Lstat(filepath.Join(path, "sentinel.txt")); err == nil {
			t.Fatalf("stat outside directory %q", path)
		}
		if err := files.MkdirAll(filepath.Join(path, "new-directory"), 0700); err == nil {
			t.Fatalf("mkdir outside %q", path)
		}
		if _, err := files.CreateTemp(path, "request-*"); err == nil {
			t.Fatalf("temporary file outside %q", path)
		}
		if _, err := files.Glob(filepath.Join(path, "*.txt")); err == nil {
			t.Fatalf("glob accepted outside pattern %q", path)
		}
		if err := files.Rename(filepath.Join(path, "sentinel.txt"), "stolen.txt"); err == nil {
			t.Fatalf("rename from outside %q", path)
		}
		if err := files.WriteFile("source.txt", []byte("source"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := files.Rename("source.txt", filepath.Join(path, "renamed.txt")); err == nil {
			t.Fatalf("rename to outside %q", path)
		}
		if err := files.Remove(filepath.Join(path, "sentinel.txt")); err == nil {
			t.Fatalf("remove outside %q", path)
		}
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("external fixture modified: %q %v", data, err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 {
		t.Fatalf("created files outside root: %+v %v", entries, err)
	}
}

func TestScopedFilesDenySymlinkEscapesAtIOBoundary(t *testing.T) {
	t.Parallel()
	dir, outside := t.TempDir(), t.TempDir()
	sentinel := filepath.Join(outside, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape-dir")); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink(sentinel, filepath.Join(dir, "escape-file")); err != nil {
		t.Fatal(err)
	}
	files := testFiles(t, dir, false)
	for _, name := range []string{"escape-file", "escape-dir/sentinel.txt"} {
		t.Run(name, func(t *testing.T) { deniedFileAccess(t, files, name) })
	}
	// Path is display/composition only: even an accepted lexical path must
	// still be rejected when subsequently opened through the capability.
	displayPath, err := files.Path("escape-file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files.ReadFile(displayPath); err == nil {
		t.Fatal("absolute display path bypassed symlink confinement")
	}
	if _, err := files.ReadDir("escape-dir"); err == nil {
		t.Fatal("read directory followed outside symlink")
	}
	if err := files.MkdirAll("escape-dir/new-directory", 0700); err == nil {
		t.Fatal("mkdir followed outside symlink")
	}
	if _, err := files.CreateTemp("escape-dir", "request-*"); err == nil {
		t.Fatal("temporary file followed outside symlink")
	}
	if err := files.WriteFile("source.txt", []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := files.Rename("source.txt", "escape-dir/renamed.txt"); err == nil {
		t.Fatal("rename destination followed outside symlink")
	}
	if err := files.Rename("escape-dir/sentinel.txt", "stolen.txt"); err == nil {
		t.Fatal("rename source followed outside symlink")
	}
	if err := files.Remove("escape-dir/sentinel.txt"); err == nil {
		t.Fatal("remove followed outside symlink")
	}
	for _, pattern := range []string{"escape-dir/*.txt", "escape-*/*.txt", "escape-file"} {
		matches, err := files.Glob(pattern)
		// io/fs.Glob is permitted to suppress directory-access errors.
		if err == nil && len(matches) != 0 {
			t.Fatalf("glob traversed symlink: %q => %v", pattern, matches)
		}
	}
	info, err := files.Lstat("escape-file")
	if err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Fatalf("safe symlink metadata unavailable: %+v %v", info, err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("external fixture modified: %q %v", data, err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 {
		t.Fatalf("created files outside root: %+v %v", entries, err)
	}
}

func TestScopedFilesAllowInternalRelativeSymlinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := testFiles(t, dir, false)
	if err := files.MkdirAll("inside", 0700); err != nil {
		t.Fatal(err)
	}
	if err := files.WriteFile("inside/data.txt", []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(dir, "link")); err != nil {
		t.Skip(err)
	}
	if err := files.WriteFile("link/data.txt", []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := files.ReadFile("inside/data.txt")
	if err != nil || string(data) != "after" {
		t.Fatalf("internal symlink did not work: %q %v", data, err)
	}
	matches, err := files.Glob("link/*.txt")
	if err != nil || len(matches) != 1 {
		t.Fatalf("internal symlink glob: %v %v", matches, err)
	}
}

func TestScopedFilesBlankDirectoryFailsClosed(t *testing.T) {
	t.Parallel()
	for _, unrestricted := range []bool{false, true} {
		files := testFiles(t, "", unrestricted)
		fixture := t.TempDir()
		target := filepath.Join(fixture, "sentinel.txt")
		if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
			t.Fatal(err)
		}
		deniedFileAccess(t, files, target)
		if _, err := files.Path(target); err == nil {
			t.Fatal("blank root returned path")
		}
		if _, err := files.ReadDir(fixture); err == nil {
			t.Fatal("blank root read ambient directory")
		}
		if err := files.MkdirAll(filepath.Join(fixture, "new-directory"), 0700); err == nil {
			t.Fatal("blank root created ambient directory")
		}
		if _, err := files.CreateTemp(fixture, "request-*"); err == nil {
			t.Fatal("blank root created ambient temporary file")
		}
		if _, err := files.Glob(filepath.Join(fixture, "*")); err == nil {
			t.Fatal("blank root globbed ambient directory")
		}
		if err := files.Rename(target, filepath.Join(fixture, "renamed.txt")); err == nil {
			t.Fatal("blank root renamed ambient file")
		}
		if err := files.Remove(target); err == nil {
			t.Fatal("blank root removed ambient file")
		}
	}
}

func TestScopedFilesRejectMalformedNames(t *testing.T) {
	t.Parallel()
	files := testFiles(t, t.TempDir(), false)
	deniedFileAccess(t, files, "bad\x00name")
	for _, pattern := range []string{"../bad-*", "nested/bad-*", `nested\bad-*`, "bad\x00*"} {
		if f, err := files.CreateTemp(".", pattern); err == nil {
			f.Close()
			t.Fatalf("accepted unsafe temporary pattern %q", pattern)
		}
	}
}

func TestScopedFilesExplicitTerminalCapabilityAllowsOutsideAccess(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dir, outside := filepath.Join(parent, "workspace"), filepath.Join(parent, "outside")
	for _, path := range []string{dir, outside} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := testFiles(t, dir, true)
	if files.Confined() {
		t.Fatal("explicit terminal capability unexpectedly confined")
	}
	if err := files.WriteFile("../outside/data.txt", []byte("terminal"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := files.ReadFile(filepath.Join(outside, "data.txt"))
	if err != nil || string(data) != "terminal" {
		t.Fatalf("explicit terminal outside read: %q %v", data, err)
	}
	if err := files.Rename("../outside/data.txt", "../outside/renamed.txt"); err != nil {
		t.Fatal(err)
	}
	if err := files.MkdirAll("../outside/nested", 0700); err != nil {
		t.Fatal(err)
	}
	tmp, err := files.CreateTemp("../outside/nested", "request-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatal(err)
	}
	matches, err := files.Glob(filepath.Join(outside, "*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("terminal glob: %v %v", matches, err)
	}
}
