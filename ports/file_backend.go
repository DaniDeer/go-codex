package ports

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// osBackend is a private, os.*-backed [apifile.Backend] implementation
// used ONLY internally by [File]'s delegation to api/file (see
// file.go's "Stage F" migration). It is a DELIBERATE, DISCLOSED
// near-duplicate of [github.com/DaniDeer/go-codex/adapters/file.Backend]
// — NOT a reuse of it — because ports cannot import adapters/file:
// adapters/file.binding.go itself imports ports (for the
// SourceAdapter/SinkAdapter/IOAdapter bindings built on top of
// [File]/[Dir]), so ports -> adapters/file would be an import cycle.
// Keeping this duplication small and private (unexported, no
// Copy/Move/Metadata support — ports.File never calls those) is the
// accepted tradeoff; see docs/roadmap/file-api.md's Stage F notes.
type osBackend struct{}

// BackendName implements [apifile.Backend].
func (osBackend) BackendName() string { return "ports(internal)" }

// ReadBytes implements [apifile.Backend].
func (osBackend) ReadBytes(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteBytes implements [apifile.Backend]. Reuses this package's own
// missingParentDirs (file.go) — the SAME multi-level ancestor-directory
// walk [File.Write] always reported — so createdDirs reports every
// missing level, not just the immediate parent.
func (osBackend) WriteBytes(_ context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error) {
	var createdDirs []string
	if opts.CreateDirs {
		dir := filepath.Dir(path)
		createdDirs = missingParentDirs(dir)
		if len(createdDirs) > 0 && !opts.DryRun {
			perm := opts.DirPerm
			if perm == 0 {
				perm = 0755
			}
			if err := os.MkdirAll(dir, perm); err != nil {
				return nil, err
			}
		}
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}

	if opts.Strict {
		// DryRun+Strict: a preview-only existence check (os.Stat) is
		// correct here since nothing is actually written — matches
		// ports.File.Write's own tested DryRun+Strict precedence (see
		// TestWrite_Strict_DryRun_ExistingFile_ReportsWithoutWriting).
		if opts.DryRun {
			if _, err := os.Stat(path); err == nil {
				return createdDirs, apifile.FileAlreadyExistsError{Path: path}
			} else if !os.IsNotExist(err) {
				return createdDirs, err
			}
			return createdDirs, nil
		}
		// Real write: use an ATOMIC O_CREATE|O_EXCL open rather than
		// stat-then-write, closing the TOCTOU race window a separate
		// Stat-then-WriteFile sequence would have between the
		// existence check and the actual write.
		f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if openErr != nil {
			if os.IsExist(openErr) {
				return createdDirs, apifile.FileAlreadyExistsError{Path: path}
			}
			return createdDirs, openErr
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			return createdDirs, writeErr
		}
		if closeErr != nil {
			return createdDirs, closeErr
		}
		return createdDirs, nil
	}

	if opts.DryRun {
		return createdDirs, nil
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return createdDirs, err
	}
	return createdDirs, nil
}

// DeleteBytes implements [apifile.Backend].
func (osBackend) DeleteBytes(_ context.Context, path string, opts apifile.DeleteOptions) (bool, error) {
	if opts.DryRun {
		_, statErr := os.Stat(path)
		existed := statErr == nil
		if !existed && opts.Strict {
			return false, os.ErrNotExist
		}
		return existed, nil
	}
	err := os.Remove(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		if opts.Strict {
			return false, err
		}
		return false, nil
	}
	return false, err
}

// ListDir implements [apifile.Backend]. ports.File never calls List, but
// the full [apifile.Backend] interface must still be satisfied.
func (osBackend) ListDir(_ context.Context, path string) ([]apifile.Entry, error) {
	if path == "" {
		path = "."
	}
	des, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]apifile.Entry, 0, len(des))
	for _, de := range des {
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, apifile.Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	return out, nil
}

// Stat implements [apifile.Backend].
func (osBackend) Stat(_ context.Context, path string) (apifile.Entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return apifile.Entry{}, err
	}
	return apifile.Entry{
		Name:    info.Name(),
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

// Mkdir implements [apifile.Backend].
func (osBackend) Mkdir(_ context.Context, path string, opts apifile.MkdirOptions) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}
	if opts.DryRun {
		return true, nil
	}
	perm := os.FileMode(0755)
	if opts.NativePOSIXMode != nil {
		perm = *opts.NativePOSIXMode
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return false, err
	}
	return true, nil
}

// WalkDir implements [apifile.WalkingBackend] via [filepath.WalkDir] —
// used internally by [Dir]'s recursive-listing/glob-discovery/
// recursive-delete-affected-path-collection, a deliberate near-duplicate
// of adapters/file.Backend.WalkDir (same import-cycle constraint as
// every other osBackend method).
func (b osBackend) WalkDir(ctx context.Context, root string, fn apifile.DirWalkFunc) error {
	return filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return fn(p, apifile.Entry{}, err)
		}
		info, infoErr := de.Info()
		if infoErr != nil {
			return fn(p, apifile.Entry{}, infoErr)
		}
		return fn(p, apifile.Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}, nil)
	})
}

// DeleteDir implements [apifile.DirDeletingBackend] — used internally by
// [Dir.Delete]. Mirrors Dir.Delete's existing, tested contract exactly
// (relocated from that method's own inline os.* calls, not redesigned).
func (b osBackend) DeleteDir(ctx context.Context, path string, recursive, dryRun bool) ([]string, error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return nil, statErr
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}

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
		if err := os.RemoveAll(path); err != nil {
			return nil, err
		}
		return affected, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if len(entries) > 0 {
		return nil, apifile.DirNotEmptyError{Path: path}
	}
	if dryRun {
		return []string{path}, nil
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return []string{path}, nil
}

var _ apifile.Backend = osBackend{}
var _ apifile.WalkingBackend = osBackend{}
var _ apifile.DirDeletingBackend = osBackend{}
