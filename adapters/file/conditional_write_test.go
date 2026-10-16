package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	apifile "github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

type cwRecordWithOwner struct {
	Name  string
	Value int
	Owner string
}

func cwRecordWithOwnerCodec() codex.Codec[cwRecordWithOwner] {
	return codex.Struct[cwRecordWithOwner](
		codex.RequiredField("name", codex.String(),
			func(r cwRecordWithOwner) string { return r.Name },
			func(r *cwRecordWithOwner, v string) { r.Name = v }),
		codex.RequiredField("value", codex.Int(),
			func(r cwRecordWithOwner) int { return r.Value },
			func(r *cwRecordWithOwner, v int) { r.Value = v }),
	)
}

func TestBackend_WriteBytesIfMatch_MatchCommits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.txt")
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	etag := apifile.StrongETag([]byte("v1"))

	if _, err := backend.WriteBytesIfMatch(context.Background(), path, []byte("v2"), etag, apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytesIfMatch: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "v2" {
		t.Fatalf("content = %q, want %q", got, "v2")
	}
}

func TestBackend_WriteBytesIfMatch_MismatchRejectsNoWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.txt")
	if err := os.WriteFile(path, []byte("v1"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()

	_, err := backend.WriteBytesIfMatch(context.Background(), path, []byte("v2"), "stale-etag", apifile.WriteOptions{})
	var mismatch apifile.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(got) != "v1" {
		t.Fatalf("content changed despite mismatch: got %q, want unchanged %q", got, "v1")
	}
}

func TestBackend_WriteBytesIfMatch_NonexistentTargetRejects(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.txt")
	backend := fileadapter.NewBackend()

	_, err := backend.WriteBytesIfMatch(context.Background(), path, []byte("v1"), "any-etag", apifile.WriteOptions{})
	var mismatch apifile.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError for nonexistent target, got %v (%T)", err, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("file should still not exist, stat err = %v", statErr)
	}
}

func TestBackend_WriteBytesIfMatch_ConcurrentWriters_OnlyOneCommits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.txt")
	if err := os.WriteFile(path, []byte("v0"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	etag := apifile.StrongETag([]byte("v0"))

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	for i := range 2 {
		i := i
		go func() {
			defer wg.Done()
			_, err := backend.WriteBytesIfMatch(context.Background(), path, []byte("v-from-writer"), etag, apifile.WriteOptions{})
			results[i] = err
		}()
	}
	wg.Wait()

	var successCount, mismatchCount int
	for _, err := range results {
		if err == nil {
			successCount++
			continue
		}
		var mismatch apifile.ConditionalWriteMismatchError
		if errors.As(err, &mismatch) {
			mismatchCount++
			continue
		}
		t.Fatalf("unexpected error: %v (%T)", err, err)
	}
	if successCount != 1 {
		t.Fatalf("want exactly 1 commit among concurrent conditional writers, got %d (mismatches=%d)", successCount, mismatchCount)
	}
}

func TestBackend_WriteBytesIfMatch_LockAlreadyHeld_RetriesThenTimesOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.txt")
	if err := os.WriteFile(path, []byte("v0"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	// Artificially hold the lock for the whole test.
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatalf("setup lock: %v", err)
	}
	defer os.Remove(lockPath)

	backend := fileadapter.NewBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := backend.WriteBytesIfMatch(ctx, path, []byte("v1"), apifile.StrongETag([]byte("v0")), apifile.WriteOptions{})
	elapsed := time.Since(start)

	var timeout apifile.ConditionalWriteLockTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("want ConditionalWriteLockTimeoutError, got %v (%T)", err, err)
	}
	if elapsed < 150*time.Millisecond {
		t.Fatalf("want the acquirer to have retried for close to the ctx deadline (~200ms), only elapsed %v", elapsed)
	}
}

func TestBackend_WriteBytesIfMatch_StaleLock_NotAutoRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.txt")
	if err := os.WriteFile(path, []byte("v0"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	lockPath := path + ".lock"
	// Simulate a crashed holder: the lock file exists, nobody will ever
	// release it.
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatalf("setup lock: %v", err)
	}
	defer os.Remove(lockPath)

	backend := fileadapter.NewBackend()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := backend.WriteBytesIfMatch(ctx, path, []byte("v1"), apifile.StrongETag([]byte("v0")), apifile.WriteOptions{}); err == nil {
		t.Fatalf("expected an error (lock still held)")
	}
	// The lock file must STILL exist — no auto-recovery.
	if _, statErr := os.Stat(lockPath); statErr != nil {
		t.Fatalf("lock file should NOT have been auto-removed: %v", statErr)
	}
}

func TestBackend_WriteBytesIfMatch_InternalDefaultTimeout_NoDeadlineCtx(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ~5s internal-default-timeout proof in -short mode")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "item.txt")
	if err := os.WriteFile(path, []byte("v0"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	lockPath := path + ".lock"
	if err := os.WriteFile(lockPath, nil, 0644); err != nil {
		t.Fatalf("setup lock: %v", err)
	}
	defer os.Remove(lockPath)

	backend := fileadapter.NewBackend()
	start := time.Now()
	// context.Background() has NO deadline — the acquirer must still
	// time out around the internal ~5s ceiling, never block forever.
	_, err := backend.WriteBytesIfMatch(context.Background(), path, []byte("v1"), apifile.StrongETag([]byte("v0")), apifile.WriteOptions{})
	elapsed := time.Since(start)

	var timeout apifile.ConditionalWriteLockTimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("want ConditionalWriteLockTimeoutError, got %v (%T)", err, err)
	}
	if elapsed > 8*time.Second {
		t.Fatalf("want the internal ~5s default ceiling to bound the wait, got %v", elapsed)
	}
}

// ── Control-file filtering (ListDir/WalkDir exclude .meta.json/.lock) ────

func TestBackend_ListDir_ExcludesMetadataSidecarFiles(t *testing.T) {
	dir := t.TempDir()
	res := apifile.NewResource[cwRecordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(cwRecordWithOwnerCodec()),
		apifile.NewPathParam("name", codex.String(),
			func(r cwRecordWithOwner) string { return r.Name },
			func(r *cwRecordWithOwner, v string) { r.Name = v }),
		apifile.NewMetadataParam("owner", codex.String(),
			func(r cwRecordWithOwner) string { return r.Owner },
			func(r *cwRecordWithOwner, v string) { r.Owner = v }),
	)
	client := apifile.NewClient()
	if err := client.Attach(fileadapter.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(apifile.Create{}), cwRecordWithOwner{Name: "widget", Value: 1, Owner: "alice"}, map[string]string{"name": "widget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Confirm the sidecar genuinely exists on disk before proving List
	// doesn't leak it.
	if _, err := os.Stat(filepath.Join(dir, "widget.json.meta.json")); err != nil {
		t.Fatalf("expected metadata sidecar file to exist: %v", err)
	}

	gotAny, err := client.List(ctx, res.WithList(apifile.List{}), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := gotAny.([]apifile.ListResult[cwRecordWithOwner])
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (sidecar excluded), got %d: %+v", len(got), got)
	}
	if got[0].Err != nil {
		t.Fatalf("unexpected per-entry error: %v", got[0].Err)
	}
	if got[0].Value.Name != "widget" {
		t.Fatalf("want Name=widget (not corrupted by the sidecar's own name), got %+v", got[0].Value)
	}
}

func TestBackend_ListDir_ExcludesLockFiles(t *testing.T) {
	dir := t.TempDir()
	res := apifile.NewResource[cwRecordWithOwner](filepath.Join(dir, "{name}.json"), format.JSON(cwRecordWithOwnerCodec()),
		apifile.NewPathParam("name", codex.String(),
			func(r cwRecordWithOwner) string { return r.Name },
			func(r *cwRecordWithOwner, v string) { r.Name = v }),
	)
	client := apifile.NewClient()
	if err := client.Attach(fileadapter.NewBackend()); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Create(ctx, res.WithCreate(apifile.Create{}), cwRecordWithOwner{Name: "widget", Value: 1}, map[string]string{"name": "widget"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Simulate a lock file left behind by an in-flight (or crashed)
	// conditional write.
	if err := os.WriteFile(filepath.Join(dir, "widget.json.lock"), nil, 0644); err != nil {
		t.Fatalf("setup lock file: %v", err)
	}

	gotAny, err := client.List(ctx, res.WithList(apifile.List{}), nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := gotAny.([]apifile.ListResult[cwRecordWithOwner])
	if len(got) != 1 {
		t.Fatalf("want exactly 1 entry (.lock excluded), got %d: %+v", len(got), got)
	}
}

func TestBackend_WalkDir_ExcludesControlFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json.meta.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json.lock"), nil, 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	backend := fileadapter.NewBackend()
	var visited []string
	err := backend.WalkDir(context.Background(), dir, func(path string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir {
			visited = append(visited, entry.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if len(visited) != 1 || visited[0] != "item.json" {
		t.Fatalf("want only item.json visited (control files excluded), got %+v", visited)
	}
}

// TestBackend_ListDir_ExcludesTempFiles is a RETROACTIVE regression
// proof for a gap found during Streaming's own pre-implementation
// gap-review (docs/roadmap/file-api.md's "Streaming/chunked reads and
// writes" section): WriteBytesIfMatch's ALREADY-SHIPPED commit temp
// file (os.CreateTemp(dir, base+".tmp-*")) was NOT excluded from
// ListDir — simulates a temp file left behind by an in-flight (or
// crashed) WriteBytesIfMatch/OpenWrite call, proving it's now excluded.
func TestBackend_ListDir_ExcludesTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json.tmp-823719035"), []byte("partial"), 0600); err != nil {
		t.Fatalf("setup temp file: %v", err)
	}

	backend := fileadapter.NewBackend()
	entries, err := backend.ListDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "item.json" {
		t.Fatalf("want only item.json listed (.tmp-* excluded), got %+v", entries)
	}
}

// TestBackend_WalkDir_ExcludesTempFiles is WalkDir's own version of the
// same retroactive proof above.
func TestBackend_WalkDir_ExcludesTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json.tmp-823719035"), []byte("partial"), 0600); err != nil {
		t.Fatalf("setup temp file: %v", err)
	}

	backend := fileadapter.NewBackend()
	var visited []string
	err := backend.WalkDir(context.Background(), dir, func(path string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir {
			visited = append(visited, entry.Name)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if len(visited) != 1 || visited[0] != "item.json" {
		t.Fatalf("want only item.json visited (.tmp-* excluded), got %+v", visited)
	}
}
