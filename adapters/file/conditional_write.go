package file

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// Lock-file-based conditional-write mechanism — see
// docs/roadmap/file-api.md's "Conditional writes" section, Fork 3, for
// the full design and its 3 explicitly confirmed tradeoffs (atomicity
// scoped to cooperating conditional writers only; no auto-recovery from
// a crashed lock holder; bounded retry+backoff with an internal default
// ceiling). This mechanism is DELIBERATELY DUPLICATED in
// ports/file_backend.go's own osBackend (not shared code) — the SAME
// import-cycle-driven precedent every other Backend method in this
// package already follows (ports cannot import adapters/file, since
// adapters/file/binding.go itself imports ports).
const (
	conditionalWriteLockRetryInitial = 10 * time.Millisecond
	conditionalWriteLockRetryMax     = 200 * time.Millisecond
	conditionalWriteLockDefaultBound = 5 * time.Second
)

func conditionalWriteLockPath(path string) string { return path + ".lock" }

// acquireConditionalWriteLock attempts to acquire path's lock file
// (O_CREATE|O_EXCL — pure stdlib, no new dependency), retrying with
// capped exponential backoff + jitter until EITHER it succeeds, ctx is
// cancelled/times out, OR an internal default bound (~5s) elapses —
// whichever comes FIRST. The internal bound exists so the loop NEVER
// blocks indefinitely even when the caller's ctx has no deadline of its
// own (e.g. context.Background()); the caller's own ctx can still cut
// the wait shorter, never longer. Returns a release func (idempotent,
// safe to call even after an error) and a ConditionalWriteLockTimeoutError
// on failure to acquire within the bound.
func acquireConditionalWriteLock(ctx context.Context, path string) (release func(), err error) {
	lockPath := conditionalWriteLockPath(path)
	deadline := time.Now().Add(conditionalWriteLockDefaultBound)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	backoff := conditionalWriteLockRetryInitial
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
		if backoff > conditionalWriteLockRetryMax {
			backoff = conditionalWriteLockRetryMax
		}
	}
}

// WriteBytesIfMatch implements [apifile.ConditionalWriteBackend]. See
// the package-level doc comment above for the lock-file mechanism's
// full design and tradeoffs.
func (b *Backend) WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts apifile.WriteOptions) ([]string, error) {
	release, err := acquireConditionalWriteLock(ctx, path)
	if err != nil {
		return nil, err
	}
	defer release()

	current, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			// Nothing to match — mirrors S3's own real If-Match
			// behavior against a nonexistent object.
			return nil, apifile.ConditionalWriteMismatchError{Path: path, Expected: ifMatch, Actual: ""}
		}
		return nil, readErr
	}
	// Accept EITHER the strong (content-hash) ETag OR the weak
	// (ModTime+Size-derived) one: a caller's ONLY practical way to
	// obtain an ETag BEFORE writing is Client.Exists/Stat, which never
	// reads content and therefore can only ever report WeakETag (see
	// Entry.ETag's own doc comment) — if WriteBytesIfMatch compared
	// against the strong ETag alone, that real, documented
	// Exists-then-IfMatch workflow could never succeed. Accepting
	// either preserves S3's own real If-Match semantics (an exact
	// content match) for a caller who DOES have the bytes in hand,
	// while making the Exists/Stat-based workflow actually functional.
	actualStrong := apifile.StrongETag(current)
	matched := actualStrong == ifMatch
	actualWeak := actualStrong
	if !matched {
		if info, statErr := os.Stat(path); statErr == nil {
			actualWeak = apifile.WeakETag(info.ModTime(), info.Size())
			matched = actualWeak == ifMatch
		}
	}
	if !matched {
		return nil, apifile.ConditionalWriteMismatchError{Path: path, Expected: ifMatch, Actual: actualStrong}
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
	// Commit via a temp file in the SAME directory + os.Rename — atomic
	// with respect to concurrent READERS (a partial write is never
	// observable). This is a DIFFERENT mechanism than the Strict-write
	// path's O_CREATE|O_EXCL (which exists to PREVENT an overwrite, not
	// to make an in-place overwrite's COMMIT atomic) — the two serve
	// different purposes, not duplicated logic.
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
	if opts.Metadata != nil {
		if err := b.WriteMetadata(ctx, path, opts.Metadata); err != nil {
			return createdDirs, err
		}
	}
	return createdDirs, nil
}
