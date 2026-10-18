package smb

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_WriteBytesIfMatch_StrongETagMatchCommits(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	strong := apifile.StrongETag([]byte("v1"))
	if _, err := b.WriteBytesIfMatch(ctx, "widget.json", []byte("v2"), strong, apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytesIfMatch: %v", err)
	}
	data, err := b.ReadBytes(ctx, "widget.json")
	if err != nil || string(data) != "v2" {
		t.Fatalf("want v2, got data=%q err=%v", data, err)
	}
}

func TestBackend_WriteBytesIfMatch_WeakETagMatchCommits(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	entry, err := b.Stat(ctx, "widget.json")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	weak := apifile.WeakETag(entry.ModTime, entry.Size)
	if _, err := b.WriteBytesIfMatch(ctx, "widget.json", []byte("v2"), weak, apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytesIfMatch with weak ETag (the realistic Exists/Stat-derived case): %v", err)
	}
}

func TestBackend_WriteBytesIfMatch_MismatchRejectsNoWrite(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	_, err := b.WriteBytesIfMatch(ctx, "widget.json", []byte("v2"), "bogus-etag", apifile.WriteOptions{})
	var mismatch apifile.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
	data, readErr := b.ReadBytes(ctx, "widget.json")
	if readErr != nil || string(data) != "v1" {
		t.Fatalf("want unmodified v1 content, got data=%q err=%v", data, readErr)
	}
}

func TestBackend_WriteBytesIfMatch_NonexistentTargetRejects(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	_, err := b.WriteBytesIfMatch(context.Background(), "missing.json", []byte("v1"), "etag", apifile.WriteOptions{})
	var mismatch apifile.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError against a nonexistent target, got %v (%T)", err, err)
	}
	if mismatch.Actual != "" {
		t.Fatalf("want Actual=\"\" for a nonexistent target, got %q", mismatch.Actual)
	}
}

func TestBackend_WriteBytesIfMatch_ReportsNativeFalse(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	if b.NativeConditionalWrite() {
		t.Fatalf("want native=false (no SMB2 compare-and-swap primitive)")
	}
}

func TestBackend_WriteBytesIfMatch_ConcurrentWriters_OnlyOneCommits(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	strong := apifile.StrongETag([]byte("v1"))

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = b.WriteBytesIfMatch(ctx, "widget.json", []byte("writer"), strong, apifile.WriteOptions{})
		}(i)
	}
	wg.Wait()

	successCount := 0
	for _, err := range results {
		if err == nil {
			successCount++
		}
	}
	if successCount != 1 {
		t.Fatalf("want exactly 1 writer to commit (both raced against the SAME strong ETag, only the first should match), got %d successes: %v", successCount, results)
	}
}

// TestBackend_WriteBytesIfMatch_InternalDefaultTimeout_NoDeadlineCtx
// proves the bounded-retry mechanism's "whichever bound is shorter wins"
// rule by pre-holding the lock file and supplying a ctx deadline MUCH
// shorter than the internal ~5s default ceiling — the retry loop must
// give up at ctx's own (shorter) deadline, not block for the full ~5s.
func TestBackend_WriteBytesIfMatch_InternalDefaultTimeout_NoDeadlineCtx(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	// Pre-create the lock file directly so acquireConditionalWriteLock's
	// own O_CREATE|O_EXCL open fails immediately and the retry loop runs
	// until shortCtx's OWN (shorter) deadline fires.
	if _, err := fake.Create(ctx, "widget.json.lock"); err != nil {
		t.Fatalf("pre-create lock file: %v", err)
	}
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := b.WriteBytesIfMatch(shortCtx, "widget.json", []byte("v2"), apifile.StrongETag([]byte("v1")), apifile.WriteOptions{})
	elapsed := time.Since(start)
	var timeout apifile.ConditionalWriteLockTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("want ConditionalWriteLockTimeoutError when ctx's own deadline is shorter than the internal ~5s ceiling, got %v (%T)", err, err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("want the SHORTER ctx deadline (50ms) to win, not the ~5s internal default; took %v", elapsed)
	}
}

func TestBackend_WriteBytesIfMatch_StaleLock_NotAutoRemoved(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	if _, err := fake.Create(ctx, "widget.json.lock"); err != nil {
		t.Fatalf("pre-create stale lock: %v", err)
	}
	shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := b.WriteBytesIfMatch(shortCtx, "widget.json", []byte("v1"), "etag", apifile.WriteOptions{})
	var timeout apifile.ConditionalWriteLockTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("want ConditionalWriteLockTimeoutError against a stale, never-auto-removed lock, got %v (%T)", err, err)
	}
	if _, ok := fake.nodes["widget.json.lock"]; !ok {
		t.Fatalf("want the stale lock file to remain (no auto-recovery), but it was removed")
	}
}
