// Package scopedfs provides request-scoped file access without changing cwd.
// Hosted callers use os.Root so file and symlink traversal are enforced when
// the file is opened, not by guessing which arguments might contain paths.
package scopedfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Files struct {
	Directory    string
	root         *os.Root
	unrestricted bool
}

func New(directory string, unrestricted bool) (*Files, error) {
	f := &Files{unrestricted: unrestricted}
	if directory == "" {
		return f, nil
	}
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	f.Directory = abs
	if !unrestricted {
		f.root, err = os.OpenRoot(abs)
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}
func (f *Files) Close() error {
	if f != nil && f.root != nil {
		return f.root.Close()
	}
	return nil
}
func (f *Files) Confined() bool { return !f.unrestricted }
func (f *Files) name(path string) (string, error) {
	if f == nil || f.Directory == "" {
		return "", errors.New("file operations require an explicit working directory")
	}
	if strings.ContainsRune(path, 0) {
		return "", errors.New("file path contains NUL")
	}
	if f.unrestricted {
		if filepath.IsAbs(path) {
			return filepath.Clean(path), nil
		}
		return filepath.Join(f.Directory, path), nil
	}
	if filepath.IsAbs(path) {
		var err error
		path, err = filepath.Rel(f.Directory, path)
		if err != nil {
			return "", err
		}
	}
	path = filepath.Clean(path)
	if path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path is outside the scoped workspace: %q", path)
	}
	return path, nil
}

// Path is for display and path composition only. Open through Files to keep
// os.Root's symlink/race protection; never reopen this path with os.Open.
func (f *Files) Path(path string) (string, error) {
	n, e := f.name(path)
	if e != nil {
		return "", e
	}
	if f.unrestricted {
		return n, nil
	}
	return filepath.Join(f.Directory, n), nil
}
func (f *Files) Open(path string) (*os.File, error) { return f.OpenFile(path, os.O_RDONLY, 0) }
func (f *Files) Create(path string) (*os.File, error) {
	return f.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
}
func (f *Files) OpenFile(path string, flags int, mode fs.FileMode) (*os.File, error) {
	n, e := f.name(path)
	if e != nil {
		return nil, e
	}
	if f.unrestricted {
		return os.OpenFile(n, flags, mode)
	}
	return f.root.OpenFile(n, flags, mode)
}
func (f *Files) ReadFile(path string) ([]byte, error) {
	n, e := f.name(path)
	if e != nil {
		return nil, e
	}
	if f.unrestricted {
		return os.ReadFile(n)
	}
	return f.root.ReadFile(n)
}
func (f *Files) WriteFile(path string, b []byte, mode fs.FileMode) error {
	n, e := f.name(path)
	if e != nil {
		return e
	}
	if f.unrestricted {
		return os.WriteFile(n, b, mode)
	}
	return f.root.WriteFile(n, b, mode)
}
func (f *Files) Stat(path string) (fs.FileInfo, error) {
	n, e := f.name(path)
	if e != nil {
		return nil, e
	}
	if f.unrestricted {
		return os.Stat(n)
	}
	return f.root.Stat(n)
}
func (f *Files) Lstat(path string) (fs.FileInfo, error) {
	n, e := f.name(path)
	if e != nil {
		return nil, e
	}
	if f.unrestricted {
		return os.Lstat(n)
	}
	return f.root.Lstat(n)
}
func (f *Files) ReadDir(path string) ([]fs.DirEntry, error) {
	file, e := f.Open(path)
	if e != nil {
		return nil, e
	}
	defer file.Close()
	return file.ReadDir(-1)
}
func (f *Files) MkdirAll(path string, mode fs.FileMode) error {
	n, e := f.name(path)
	if e != nil {
		return e
	}
	if f.unrestricted {
		return os.MkdirAll(n, mode)
	}
	return f.root.MkdirAll(n, mode)
}
func (f *Files) Remove(path string) error {
	n, e := f.name(path)
	if e != nil {
		return e
	}
	if f.unrestricted {
		return os.Remove(n)
	}
	return f.root.Remove(n)
}
func (f *Files) Rename(old, new string) error {
	a, e := f.name(old)
	if e != nil {
		return e
	}
	b, e := f.name(new)
	if e != nil {
		return e
	}
	if f.unrestricted {
		return os.Rename(a, b)
	}
	return f.root.Rename(a, b)
}
func (f *Files) Glob(pattern string) ([]string, error) {
	n, e := f.name(pattern)
	if e != nil {
		return nil, e
	}
	if f.unrestricted {
		return filepath.Glob(n)
	}
	matches, e := fs.Glob(f.root.FS(), filepath.ToSlash(n))
	if e != nil {
		return nil, e
	}
	for i, m := range matches {
		matches[i] = filepath.Join(f.Directory, m)
	}
	return matches, nil
}
func (f *Files) CreateTemp(dir, pattern string) (*os.File, error) {
	if dir == "" {
		dir = "."
	}
	if strings.ContainsAny(pattern, "/\\") {
		return nil, errors.New("temporary file pattern must not contain path separators")
	}
	prefix, suffix := pattern, ""
	if i := strings.LastIndex(pattern, "*"); i >= 0 {
		prefix, suffix = pattern[:i], pattern[i+1:]
	}
	for range 100 {
		var b [12]byte
		if _, e := rand.Read(b[:]); e != nil {
			return nil, e
		}
		name := filepath.Join(dir, prefix+hex.EncodeToString(b[:])+suffix)
		file, e := f.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if !errors.Is(e, fs.ErrExist) {
			return file, e
		}
	}
	return nil, errors.New("unable to allocate temporary file")
}
