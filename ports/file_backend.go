package ports

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// osBackendControlFileSuffixes mirrors adapters/file.Backend's own
// isControlFile filter (deliberate near-duplicate, same import-cycle
// constraint as every other osBackend method) — ".lock" is genuinely
// load-bearing here now that osBackend implements WriteBytesIfMatch
// below (not just defensive consistency); ".meta.json" is kept for
// uniformity even though osBackend doesn't implement MetadataBackend
// today. See adapters/file's isControlFile doc comment for the full
// rationale (internal/templatematch.MatchNonWildcard's greedy capture
// would otherwise silently match a sidecar/lock file against a
// resource template).
var osBackendControlFileSuffixes = []string{".meta.json", ".lock"}

// osBackendControlFileInfix mirrors adapters/file.Backend's own
// controlFileInfix (Streaming's own pre-implementation gap-review found
// this applies retroactively to WriteBytesIfMatch's ALREADY-SHIPPED
// temp file too, not just OpenWrite's new one) — a RANDOM numeric
// suffix follows it, so this is a substring (Contains) check.
const osBackendControlFileInfix = ".tmp-"

func osBackendIsControlFile(name string) bool {
	for _, suffix := range osBackendControlFileSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return strings.Contains(name, osBackendControlFileInfix)
}

func osBackendEntryETag(isDir bool, modTime time.Time, size int64) string {
	if isDir {
		return ""
	}
	return apifile.WeakETag(modTime, size)
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
		if osBackendIsControlFile(de.Name()) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		out = append(out, apifile.Entry{
			Name:    de.Name(),
			IsDir:   de.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			ETag:    osBackendEntryETag(info.IsDir(), info.ModTime(), info.Size()),
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
		ETag:    osBackendEntryETag(info.IsDir(), info.ModTime(), info.Size()),
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
		// Control files (".meta.json"/".lock") are NEVER reported to fn
		// at all — see osBackendIsControlFile's own doc comment.
		if !de.IsDir() && osBackendIsControlFile(de.Name()) {
			return nil
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
			ETag:    osBackendEntryETag(de.IsDir(), info.ModTime(), info.Size()),
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

// Lock-file-based conditional-write mechanism — a DELIBERATE
// near-duplicate of adapters/file.Backend's own (same import-cycle
// constraint as every other osBackend method). See
// docs/roadmap/file-api.md's "Conditional writes" section, Fork 3, for
// the full design and its 3 explicitly confirmed tradeoffs.
const (
	osBackendLockRetryInitial = 10 * time.Millisecond
	osBackendLockRetryMax     = 200 * time.Millisecond
	osBackendLockDefaultBound = 5 * time.Second
)

func osBackendLockPath(path string) string { return path + ".lock" }

func osBackendAcquireConditionalWriteLock(ctx context.Context, path string) (release func(), err error) {
	lockPath := osBackendLockPath(path)
	deadline := time.Now().Add(osBackendLockDefaultBound)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	backoff := osBackendLockRetryInitial
	var lastErr error
	for {
		f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr == nil {
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(openErr) {
			return nil, openErr
		}
		lastErr = openErr
		if !time.Now().Before(deadline) {
			return nil, apifile.ConditionalWriteLockTimeoutError{Path: path, Err: lastErr}
		}
		// #nosec G404 -- jitter is pure backoff timing, not security-sensitive.
		jitter := time.Duration(rand.Int63n(int64(backoff/2 + 1)))
		select {
		case <-ctx.Done():
			return nil, apifile.ConditionalWriteLockTimeoutError{Path: path, Err: ctx.Err()}
		case <-time.After(backoff + jitter):
		}
		backoff *= 2
		if backoff > osBackendLockRetryMax {
			backoff = osBackendLockRetryMax
		}
	}
}

// WriteBytesIfMatch implements [apifile.ConditionalWriteBackend].
func (osBackend) WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts apifile.WriteOptions) ([]string, error) {
	release, err := osBackendAcquireConditionalWriteLock(ctx, path)
	if err != nil {
		return nil, err
	}
	defer release()

	current, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return nil, apifile.ConditionalWriteMismatchError{Path: path, Expected: ifMatch, Actual: ""}
		}
		return nil, readErr
	}
	actual := apifile.StrongETag(current)
	if actual != ifMatch {
		return nil, apifile.ConditionalWriteMismatchError{Path: path, Expected: ifMatch, Actual: actual}
	}

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
				return createdDirs, err
			}
		}
	}
	if opts.DryRun {
		return createdDirs, nil
	}

	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return createdDirs, err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return createdDirs, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return createdDirs, err
	}
	if err := os.Chmod(tmpPath, perm); err != nil {
		_ = os.Remove(tmpPath)
		return createdDirs, err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return createdDirs, err
	}
	return createdDirs, nil
}

var _ apifile.Backend = osBackend{}
var _ apifile.WalkingBackend = osBackend{}
var _ apifile.DirDeletingBackend = osBackend{}
var _ apifile.ConditionalWriteBackend = osBackend{}
var _ apifile.StreamReadBackend = osBackend{}
var _ apifile.StreamWriteBackend = osBackend{}

// OpenReader implements [apifile.StreamReadBackend] — a deliberate
// near-duplicate of adapters/file.Backend's own OpenReader (same
// import-cycle constraint as every other osBackend method). osBackend
// does NOT implement [apifile.RangeReadingBackend], matching
// adapters/file.Backend's own Phase 2 choice.
func (osBackend) OpenReader(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// osBackendStreamWriteCloser is osBackend's own near-duplicate of
// adapters/file's streamWriteCloser — SAME temp-file-in-same-directory
// + os.Rename commit mechanism, minus Metadata handling (osBackend
// implements no [apifile.MetadataBackend] — ports.File never needed
// metadata support, see this file's own package-level doc comment).
type osBackendStreamWriteCloser struct {
	tmp     *os.File
	tmpPath string
	path    string
	perm    os.FileMode
	strict  bool
	discard bool
	closed  bool
}

func (s *osBackendStreamWriteCloser) Write(p []byte) (int, error) {
	if s.discard {
		return len(p), nil
	}
	return s.tmp.Write(p)
}

func (s *osBackendStreamWriteCloser) Close() error {
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
	return nil
}

// OpenWriter implements [apifile.StreamWriteBackend].
func (osBackend) OpenWriter(_ context.Context, path string, opts apifile.WriteOptions) (io.WriteCloser, error) {
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
		return &osBackendStreamWriteCloser{discard: true}, nil
	}
	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, err
	}
	return &osBackendStreamWriteCloser{
		tmp: tmp, tmpPath: tmp.Name(), path: path, perm: perm, strict: opts.Strict,
	}, nil
}
