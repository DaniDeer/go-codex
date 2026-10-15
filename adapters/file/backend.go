package file

import (
	"context"
	"io"
	"os"
	"path/filepath"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// Backend is an [apifile.Backend] implementation backed directly by the
// local filesystem's os.* calls — the ONE Phase 1 backend, proving the
// api/file abstraction against a REAL adapter. ALSO implements
// [apifile.CopyingBackend] (via io.Copy) and [apifile.MovingBackend]
// (via os.Rename) — a genuine fast path for both. Deliberately does NOT
// implement [apifile.BatchDeletingBackend]: a POSIX filesystem has no
// native batch-delete syscall, so a filesystem-side "implementation"
// would be the IDENTICAL sequential loop the generic fallback already
// runs — see docs/roadmap/file-api.md's "Files to create" table for the
// full rationale.
type Backend struct{}

// NewBackend returns a new filesystem-backed [apifile.Backend].
func NewBackend() *Backend { return &Backend{} }

// BackendName implements [apifile.Backend].
func (*Backend) BackendName() string { return "adapters/file" }

// ReadBytes implements [apifile.Backend].
func (*Backend) ReadBytes(_ context.Context, path string) ([]byte, error) {
	return os.ReadFile(path)
}

// WriteBytes implements [apifile.Backend].
func (*Backend) WriteBytes(_ context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error) {
	var createdDirs []string
	if opts.CreateDirs {
		dir := filepath.Dir(path)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			perm := opts.DirPerm
			if perm == 0 {
				perm = 0755
			}
			if !opts.DryRun {
				if err := os.MkdirAll(dir, perm); err != nil {
					return nil, err
				}
			}
			createdDirs = append(createdDirs, dir)
		}
	}
	if opts.DryRun {
		return createdDirs, nil
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	if opts.Strict {
		if _, err := os.Stat(path); err == nil {
			return createdDirs, apifile.FileAlreadyExistsError{Path: path}
		}
	}
	return createdDirs, os.WriteFile(path, data, perm)
}

// DeleteBytes implements [apifile.Backend].
func (*Backend) DeleteBytes(_ context.Context, path string, opts apifile.DeleteOptions) (bool, error) {
	if opts.DryRun {
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return false, nil
		}
		return err == nil, nil
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

// ListDir implements [apifile.Backend].
func (*Backend) ListDir(_ context.Context, path string) ([]apifile.Entry, error) {
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
func (*Backend) Stat(_ context.Context, path string) (apifile.Entry, error) {
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
func (*Backend) Mkdir(_ context.Context, path string, opts apifile.MkdirOptions) (bool, error) {
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

// CopyBytes implements [apifile.CopyingBackend] via io.Copy between 2
// open *os.File handles — not a true reflink/CoW fast path in Phase 1,
// but still avoids a full decode/re-encode round-trip through the
// caller.
func (*Backend) CopyBytes(_ context.Context, srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// MoveBytes implements [apifile.MovingBackend] via os.Rename — genuinely
// atomic on a single filesystem.
func (*Backend) MoveBytes(_ context.Context, srcPath, dstPath string) error {
	return os.Rename(srcPath, dstPath)
}

var _ apifile.Backend = (*Backend)(nil)
var _ apifile.CopyingBackend = (*Backend)(nil)
var _ apifile.MovingBackend = (*Backend)(nil)
