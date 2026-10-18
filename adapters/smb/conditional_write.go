package smb

import (
	"context"
	"io"
	"math/rand"
	"os"
	"strconv"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

// Lock-file-based conditional-write mechanism — mirrors
// adapters/file/conditional_write.go's own mechanism EXACTLY (same
// retry/backoff constants, same ~5s internal default ceiling, same
// "atomic only among cooperating conditional writers" scope). SMB2 has
// no equivalent to S3's native If-Match/If-None-Match, but SMB2's own
// CREATE with FILE_CREATE disposition (the wire-level operation backing
// OpenFile(ctx, name, O_CREATE|O_EXCL|O_WRONLY, perm)) IS genuinely
// atomic at the protocol level — the SAME guarantee POSIX O_CREATE|
// O_EXCL provides locally, just server-enforced over the wire instead
// of kernel-enforced locally. See docs/roadmap/file-api.md's
// "adapters/smb" section, "2 further technical questions" subsection,
// for the full rationale. Deliberately DUPLICATED (not shared code)
// from adapters/file's own mechanism — the SAME import-cycle-driven
// precedent every Backend package in this repo already follows.
const (
	conditionalWriteLockRetryInitial = 10 * time.Millisecond
	conditionalWriteLockRetryMax     = 200 * time.Millisecond
	conditionalWriteLockDefaultBound = 5 * time.Second
)

func conditionalWriteLockPath(path string) string { return path + ".lock" }

// acquireConditionalWriteLock attempts to acquire path's lock file
// (OpenFile(O_CREATE|O_EXCL) — atomic at the SMB2 protocol level),
// retrying with capped exponential backoff + jitter until EITHER it
// succeeds, ctx is cancelled/times out, OR an internal default bound
// (~5s) elapses — whichever comes FIRST, so the loop never blocks
// indefinitely even when the caller's ctx has no deadline of its own.
func (b *Backend) acquireConditionalWriteLock(ctx context.Context, path string) (release func(), err error) {
	lockPath := conditionalWriteLockPath(path)
	deadline := time.Now().Add(conditionalWriteLockDefaultBound)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	backoff := conditionalWriteLockRetryInitial
	var lastErr error
	for {
		f, openErr := b.ops.OpenFile(ctx, lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if openErr == nil {
			_ = f.Close()
			return func() { _ = b.ops.Remove(ctx, lockPath) }, nil
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

// WriteBytesIfMatch implements [apifile.ConditionalWriteBackend]. The
// lock only serializes concurrent writers — it does NOT by itself
// verify ifMatch (a gap found during a pre-implementation re-review of
// this design, see docs/roadmap/file-api.md's own "Gap found" note).
// After acquiring the lock, this reads path's CURRENT content, computes
// its STRONG ETag, and compares it against ifMatch; on a mismatch it
// ALSO retries against the WEAK (ModTime+Size-derived) ETag via Stat
// before concluding a genuine mismatch — mirrors adapters/file's own
// "accept EITHER the strong OR the weak ETag" rule exactly (a caller's
// only realistic way to obtain an ETag BEFORE writing is
// Client.Exists/ports.File.Stat, which never reads content and can
// therefore only ever report a weak one).
func (b *Backend) WriteBytesIfMatch(ctx context.Context, path string, data []byte, ifMatch string, opts apifile.WriteOptions) ([]string, error) {
	release, err := b.acquireConditionalWriteLock(ctx, path)
	if err != nil {
		return nil, err
	}
	defer release()

	cf, readErr := b.ops.Open(ctx, path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			// Nothing to match — mirrors S3's own real If-Match
			// behavior against a nonexistent object.
			return nil, apifile.ConditionalWriteMismatchError{Path: path, Expected: ifMatch, Actual: ""}
		}
		return nil, readErr
	}
	current, readAllErr := io.ReadAll(cf)
	_ = cf.Close()
	if readAllErr != nil {
		return nil, readAllErr
	}

	actualStrong := apifile.StrongETag(current)
	matched := actualStrong == ifMatch
	actualWeak := actualStrong
	if !matched {
		if info, statErr := b.ops.Stat(ctx, path); statErr == nil {
			actualWeak = apifile.WeakETag(info.ModTime(), info.Size())
			matched = actualWeak == ifMatch
		}
	}
	if !matched {
		return nil, apifile.ConditionalWriteMismatchError{Path: path, Expected: ifMatch, Actual: actualStrong}
	}

	if opts.DryRun {
		return nil, nil
	}

	perm := opts.Perm
	if perm == 0 {
		perm = 0644
	}
	// Commit via a temp file in the SAME directory + Share.Rename —
	// atomic with respect to concurrent readers (a partial write is
	// never observable), the SAME atomicity story StreamWriteBackend's
	// own OpenWriter uses (see stream.go).
	tmpPath := path + ".tmp-" + randomSuffix()
	tmp, createErr := b.ops.Create(ctx, tmpPath)
	if createErr != nil {
		return nil, createErr
	}
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		_ = b.ops.Remove(ctx, tmpPath)
		return nil, writeErr
	}
	if closeErr := tmp.Close(); closeErr != nil {
		_ = b.ops.Remove(ctx, tmpPath)
		return nil, closeErr
	}
	if chmodErr := b.ops.Chmod(ctx, tmpPath, perm); chmodErr != nil {
		_ = b.ops.Remove(ctx, tmpPath)
		return nil, chmodErr
	}
	if renameErr := b.ops.Rename(ctx, tmpPath, path); renameErr != nil {
		_ = b.ops.Remove(ctx, tmpPath)
		return nil, renameErr
	}
	if opts.Metadata != nil {
		if err := b.WriteMetadata(ctx, path, opts.Metadata); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// NativeConditionalWrite implements
// [apifile.NativeConditionalWriteBackend] — always false: SMB2 has no
// compare-and-swap primitive, unlike adapters/s3's genuinely native
// PutObject If-Match.
func (b *Backend) NativeConditionalWrite() bool { return false }

// randomSuffix returns a short, non-cryptographic random numeric suffix
// for a temp-file name — collision risk is negligible for this purpose
// (a single in-flight commit per path, serialized by the lock file
// above) and does not need a CSPRNG.
func randomSuffix() string {
	// #nosec G404 -- a temp-file name suffix is not security-sensitive.
	return strconv.FormatInt(rand.Int63(), 36)
}

var (
	_ apifile.ConditionalWriteBackend       = (*Backend)(nil)
	_ apifile.NativeConditionalWriteBackend = (*Backend)(nil)
)
