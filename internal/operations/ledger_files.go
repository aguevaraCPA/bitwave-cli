package operations

import (
	"context"
	"io/fs"
	"path/filepath"

	"github.com/bitwave-io/bitwave-accounting-sdk/format"
	"github.com/bitwave-io/bitwave-accounting-sdk/model"
	"github.com/bitwave-io/bitwave-cli/internal/operation"
	"github.com/bitwave-io/bitwave-cli/internal/scopedfs"
)

type ledgerFilesystem struct{ files *scopedfs.Files }

func (f ledgerFilesystem) Open(name string) (fs.File, error) {
	return f.files.Open(filepath.FromSlash(name))
}

func parseLedgerFile(ctx context.Context, name string) (*model.Project, error) {
	files := operation.RuntimeFrom(ctx).Files
	abs, err := files.Path(name)
	if err != nil {
		return nil, err
	}
	if !files.Confined() {
		// Only the terminal adapter enables unrestricted file access.
		return format.ParseFile(abs)
	}
	rel, err := filepath.Rel(files.Directory, abs)
	if err != nil {
		return nil, err
	}
	return format.ParseFS(ledgerFilesystem{files}, filepath.ToSlash(rel))
}
