package file

import (
	"context"
	"io"
	"os"
	"path/filepath"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// OpenReader implements [apifile.StreamReadBackend] — a plain os.Open
// handle (Backend does NOT implement [apifile.RangeReadingBackend] in
// Phase 2: see docs/roadmap/file-api.md's "Backend semantic differences"
// table — a real os.File.Seek-based range read would be a reasonable,
// cheap future enhancement, not required to ship this design).
func (*Backend) OpenReader(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// streamWriteCloser backs [Backend.OpenWriter] — writes stream directly
// to a temp file in the SAME directory as the final path (no in-memory
// buffering, the whole point of Mechanism 2's memory benefit), Close
// commits atomically via os.Rename — the IDENTICAL mechanism
// WriteBytesIfMatch's own commit step uses (see conditional_write.go's
// own doc comment for why this differs from the Strict-write path's
// O_CREATE|O_EXCL).
type streamWriteCloser struct {
	ctx      context.Context
	backend  *Backend
	tmp      *os.File
	tmpPath  string
	path     string
	perm     os.FileMode
	strict   bool
	metadata map[string]string
	discard  bool // true for DryRun — writes are accepted but never committed
	closed   bool
}

func (s *streamWriteCloser) Write(p []byte) (int, error) {
	if s.discard {
		return len(p), nil
	}
	return s.tmp.Write(p)
}

func (s *streamWriteCloser) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.discard {
		return nil
	}
	if err := s.tmp.Close(); err != nil {
		_ = os.Remove(s.tmpPath)
		return err
	}
	if err := os.Chmod(s.tmpPath, s.perm); err != nil {
		_ = os.Remove(s.tmpPath)
		return err
	}
	// Strict's existence check happens as late as possible (right
	// before the commit rename) to minimize — though, consistent with
	// this round's own disclosed scope limits, not fully eliminate —
	// the window between check and commit; a fully atomic Strict+
	// streaming guarantee would need a different commit primitive than
	// rename (which unconditionally overwrites on POSIX) and is left
	// as a documented limitation, not designed further this round.
	if s.strict {
		if _, err := os.Stat(s.path); err == nil {
			_ = os.Remove(s.tmpPath)
			return apifile.FileAlreadyExistsError{Path: s.path}
		} else if !os.IsNotExist(err) {
			_ = os.Remove(s.tmpPath)
			return err
		}
	}
	if err := os.Rename(s.tmpPath, s.path); err != nil {
		_ = os.Remove(s.tmpPath)
		return err
	}
	if s.metadata != nil {
		if err := s.backend.WriteMetadata(s.ctx, s.path, s.metadata); err != nil {
			return err
		}
	}
	return nil
}

// OpenWriter implements [apifile.StreamWriteBackend].
func (b *Backend) OpenWriter(ctx context.Context, path string, opts apifile.WriteOptions) (io.WriteCloser, error) {
	if opts.CreateDirs {
		dir := filepath.Dir(path)
		if !opts.DryRun {
			perm := opts.DirPerm
			if perm == 0 {
				perm = 0755
			}
			if err := os.MkdirAll(dir, perm); err != nil {
				return nil, err
			}
		}
	}
	if opts.DryRun {
		// Mirrors WriteBytes' own DryRun contract: report what WOULD
		// happen without writing anything — a discard writer accepts
		// and drops every Write, Close is a no-op success.
		return &streamWriteCloser{discard: true}, nil
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, err
	}
	return &streamWriteCloser{
		ctx: ctx, backend: b, tmp: tmp, tmpPath: tmp.Name(), path: path,
		perm: perm, strict: opts.Strict, metadata: opts.Metadata,
	}, nil
}

var (
	_ apifile.StreamWriteBackend = (*Backend)(nil)
	_ apifile.StreamReadBackend  = (*Backend)(nil)
)
