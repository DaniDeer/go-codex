package file

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// Backend is an [apifile.Backend] implementation backed directly by the
// local filesystem's os.* calls — the ONE Phase 1 backend, proving the
// api/file abstraction against a REAL adapter. ALSO implements
// [apifile.CopyingBackend] (via io.Copy), [apifile.MovingBackend] (via
// os.Rename), and [apifile.MetadataBackend] (via a JSON sidecar file —
// see metadataPath's doc comment) — genuine fast/real paths for all
// three. Deliberately does NOT implement [apifile.BatchDeletingBackend]:
// a POSIX filesystem has no native batch-delete syscall, so a
// filesystem-side "implementation" would be the IDENTICAL sequential
// loop the generic fallback already runs — see
// docs/roadmap/file-api.md's "Files to create" table for the full
// rationale.
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
func (b *Backend) WriteBytes(ctx context.Context, path string, data []byte, opts apifile.WriteOptions) ([]string, error) {
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
	if err := os.WriteFile(path, data, perm); err != nil {
		return createdDirs, err
	}
	if opts.Metadata != nil {
		if err := b.WriteMetadata(ctx, path, opts.Metadata); err != nil {
			return createdDirs, err
		}
	}
	return createdDirs, nil
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

// metadataPath returns path's JSON sidecar file path — e.g.
// "notes/alice.json" -> "notes/alice.json.meta.json". A sidecar file,
// NOT real OS extended attributes: keeps this adapter's stdlib-only
// toolchain decision intact (no golang.org/x/sys/unix dependency), is
// portable across Linux/Windows/macOS identically, and arguably mirrors
// S3's OWN "metadata lives beside the object, not inside it" model more
// closely than real xattrs would.
func metadataPath(path string) string { return path + ".meta.json" }

// ReadMetadata implements [apifile.MetadataBackend]. Returns (nil, nil)
// — NOT an error — when path has no sidecar file, mirroring every other
// optional-extension's "harmless absence" idiom in this package.
func (*Backend) ReadMetadata(_ context.Context, path string) (map[string]string, error) {
	data, err := os.ReadFile(metadataPath(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var md map[string]string
	if err := json.Unmarshal(data, &md); err != nil {
		return nil, err
	}
	return md, nil
}

// WriteMetadata implements [apifile.MetadataBackend] — overwrites path's
// sidecar file entirely (not a merge), mirroring the interface's own
// documented "overwriting... entirely" contract.
func (*Backend) WriteMetadata(_ context.Context, path string, md map[string]string) error {
	data, err := json.Marshal(md)
	if err != nil {
		return err
	}
	return os.WriteFile(metadataPath(path), data, 0644)
}

var _ apifile.Backend = (*Backend)(nil)
var _ apifile.CopyingBackend = (*Backend)(nil)
var _ apifile.MovingBackend = (*Backend)(nil)
var _ apifile.MetadataBackend = (*Backend)(nil)
