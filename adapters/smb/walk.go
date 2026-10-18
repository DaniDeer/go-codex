package smb

import (
	"context"
	"io/fs"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// WalkDir implements [apifile.WalkingBackend] via fs.WalkDir over the
// bound Share's own io/fs.FS adapter — the library's own io/fs bridge
// does the heavy lifting; honors filepath.SkipDir via fs.WalkDir's own
// contract (fs.SkipDir and filepath.SkipDir are the SAME sentinel
// value).
func (b *Backend) WalkDir(ctx context.Context, root string, fn apifile.DirWalkFunc) error {
	fsys := b.ops.WithContext(ctx)
	return fs.WalkDir(fsys, root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return fn(p, apifile.Entry{}, err)
		}
		if !de.IsDir() && isControlFile(de.Name()) {
			return nil
		}
		info, infoErr := de.Info()
		if infoErr != nil {
			return fn(p, apifile.Entry{}, infoErr)
		}
		return fn(p, toEntry(info), nil)
	})
}

// DeleteDir implements [apifile.DirDeletingBackend].
//
// recursive's affected list is ALWAYS collected via WalkDir (root
// included, per WalkDir's own documented contract) — Share.RemoveAll is
// called ONLY when !dryRun. A literal "recursive: Share.RemoveAll"
// mapping would be incomplete: it does not account for dryRun at all —
// a gap found during a pre-implementation re-review of this design
// (confirmed by reading adapters/file.Backend.DeleteDir directly, which
// uses this SAME walk-to-enumerate-then-conditionally-remove shape).
func (b *Backend) DeleteDir(ctx context.Context, path string, recursive, dryRun bool) ([]string, error) {
	if recursive {
		var affected []string
		if err := b.WalkDir(ctx, path, func(p string, _ apifile.Entry, err error) error {
			if err != nil {
				return err
			}
			affected = append(affected, p)
			return nil
		}); err != nil {
			return nil, err
		}
		if dryRun {
			return affected, nil
		}
		if err := b.ops.RemoveAll(ctx, path); err != nil {
			return nil, err
		}
		return affected, nil
	}

	// Non-recursive: path must be empty.
	entries, err := b.ops.ReadDir(ctx, path)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, apifile.DirNotEmptyError{Path: path}
	}
	if dryRun {
		return []string{path}, nil
	}
	if err := b.ops.Remove(ctx, path); err != nil {
		return nil, err
	}
	return []string{path}, nil
}

var (
	_ apifile.WalkingBackend     = (*Backend)(nil)
	_ apifile.DirDeletingBackend = (*Backend)(nil)
)
