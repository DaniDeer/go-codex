package smb

import (
	"context"
	"io"
	"os"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// streamWriteCloser backs [Backend.OpenWriter] — writes stream DIRECTLY
// to an open temp-file handle in the SAME directory as the final path
// (no in-memory buffering — the whole point of Mechanism 2's memory
// benefit, no io.Pipe bridge needed at all since go-smb2/v2's bound file
// already IS an io.Writer), Close commits atomically via Share.Rename.
//
// This atomicity is REQUIRED by [apifile.StreamWriteBackend]'s own
// interface contract ("Close MUST commit the write atomically... a
// reader must never observe a partially-written file") — a gap found
// during a 4th pre-implementation review round of this design (the
// original sketch opened the FINAL path directly); fixed to mirror
// adapters/file.Backend.OpenWriter's own temp-file+rename shape exactly,
// using SMB2's native Rename instead of os.Rename.
type streamWriteCloser struct {
	ctx      context.Context
	backend  *Backend
	tmp      fileOps
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
		_ = s.backend.ops.Remove(s.ctx, s.tmpPath)
		return err
	}
	if err := s.backend.ops.Chmod(s.ctx, s.tmpPath, s.perm); err != nil {
		_ = s.backend.ops.Remove(s.ctx, s.tmpPath)
		return err
	}
	if s.strict {
		if _, err := s.backend.ops.Stat(s.ctx, s.path); err == nil {
			_ = s.backend.ops.Remove(s.ctx, s.tmpPath)
			return apifile.FileAlreadyExistsError{Path: s.path}
		} else if !os.IsNotExist(err) {
			_ = s.backend.ops.Remove(s.ctx, s.tmpPath)
			return err
		}
	}
	if err := s.backend.ops.Rename(s.ctx, s.tmpPath, s.path); err != nil {
		_ = s.backend.ops.Remove(s.ctx, s.tmpPath)
		return err
	}
	if s.metadata != nil {
		if err := s.backend.WriteMetadata(s.ctx, s.path, s.metadata); err != nil {
			return err
		}
	}
	return nil
}

// OpenWriter implements [apifile.StreamWriteBackend]. IfMatch is never
// set when this is called (dispatch falls back to the non-streaming
// conditional-write path instead).
func (b *Backend) OpenWriter(ctx context.Context, path string, opts apifile.WriteOptions) (io.WriteCloser, error) {
	if opts.DryRun {
		return &streamWriteCloser{discard: true}, nil
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	tmpPath := path + ".tmp-" + randomSuffix()
	tmp, err := b.ops.Create(ctx, tmpPath)
	if err != nil {
		return nil, err
	}
	return &streamWriteCloser{
		ctx: ctx, backend: b, tmp: tmp, tmpPath: tmpPath, path: path,
		perm: perm, strict: opts.Strict, metadata: opts.Metadata,
	}, nil
}

// OpenReader implements [apifile.StreamReadBackend] — a direct
// passthrough, genuinely streaming (no buffering).
func (b *Backend) OpenReader(ctx context.Context, path string) (io.ReadCloser, error) {
	return b.ops.Open(ctx, path)
}

// OpenReaderRange implements [apifile.RangeReadingBackend] — a NATIVE
// byte-range read via io.NewSectionReader over the bound file's own
// io.ReaderAt (ReadAt is a first-class SMB2 operation, unlike S3's
// dedicated wire-level Range header this doesn't need at all).
func (b *Backend) OpenReaderRange(ctx context.Context, path string, offset, length int64) (io.ReadCloser, error) {
	f, err := b.ops.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	if length <= 0 {
		info, statErr := f.Stat()
		if statErr != nil {
			_ = f.Close()
			return nil, statErr
		}
		length = info.Size() - offset
		if length < 0 {
			length = 0
		}
	}
	return &sectionReadCloser{
		SectionReader: io.NewSectionReader(f, offset, length),
		closer:        f,
	}, nil
}

// sectionReadCloser adapts io.NewSectionReader's *io.SectionReader (an
// io.Reader, not an io.ReadCloser) by delegating Close to the
// underlying bound file handle.
type sectionReadCloser struct {
	*io.SectionReader
	closer io.Closer
}

func (s *sectionReadCloser) Close() error { return s.closer.Close() }

var (
	_ apifile.StreamWriteBackend  = (*Backend)(nil)
	_ apifile.StreamReadBackend   = (*Backend)(nil)
	_ apifile.RangeReadingBackend = (*Backend)(nil)
)
