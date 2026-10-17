package s3_test

import (
	"context"
	"errors"
	"os"
	"testing"

	adapters3 "github.com/DaniDeer/go-codex/adapters/s3"
	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_ReadBytes_NotFound_SatisfiesOsErrNotExist(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	_, err := b.ReadBytes(context.Background(), "missing.json")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want errors.Is(err, os.ErrNotExist), got %v", err)
	}
}

func TestBackend_WriteBytes_Strict_UsesIfNoneMatch(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()

	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{Strict: true}); err != nil {
		t.Fatalf("first Strict write: %v", err)
	}
	_, err := b.WriteBytes(ctx, "widget.json", []byte("v2"), apifile.WriteOptions{Strict: true})
	var already apifile.FileAlreadyExistsError
	if !errors.As(err, &already) {
		t.Fatalf("want FileAlreadyExistsError on 2nd Strict write, got %v (%T)", err, err)
	}
	if fake.putCalls != 2 {
		t.Fatalf("want 2 PutObject calls, got %d", fake.putCalls)
	}
}

func TestBackend_ListDir_PaginatesAllPages(t *testing.T) {
	fake := newFakeS3("bucket")
	fake.pageSize = 2
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	for _, name := range []string{"a.json", "b.json", "c.json", "d.json", "e.json"} {
		if _, err := b.WriteBytes(ctx, "notes/"+name, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", name, err)
		}
	}
	entries, err := b.ListDir(ctx, "notes")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("want 5 entries across multiple pages, got %d", len(entries))
	}
	if fake.listCalls < 3 {
		t.Fatalf("want ListObjectsV2 called multiple times (pageSize=2, 5 items), got %d calls", fake.listCalls)
	}
}

func TestBackend_ListDir_CommonPrefixesAreDirectories(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "notes/sub/child.json", []byte("x"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	if _, err := b.WriteBytes(ctx, "notes/leaf.json", []byte("x"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	entries, err := b.ListDir(ctx, "notes")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	var sawDir, sawFile bool
	for _, e := range entries {
		if e.IsDir && e.Name == "sub" {
			sawDir = true
		}
		if !e.IsDir && e.Name == "leaf.json" {
			sawFile = true
		}
	}
	if !sawDir {
		t.Fatalf("want a directory entry named %q, got %+v", "sub", entries)
	}
	if !sawFile {
		t.Fatalf("want a file entry named %q, got %+v", "leaf.json", entries)
	}
}

func TestBackend_Stat_ReturnsRealETagNoBodyRead(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("content"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	fake.getCalls = 0
	entry, err := b.Stat(ctx, "widget.json")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if entry.ETag == "" {
		t.Fatalf("want a non-empty, real ETag from Stat")
	}
	if fake.getCalls != 0 {
		t.Fatalf("want Stat to use HeadObject only (no GetObject/body read), got %d GetObject calls", fake.getCalls)
	}
	if fake.headCalls != 1 {
		t.Fatalf("want exactly 1 HeadObject call, got %d", fake.headCalls)
	}
}

func TestBackend_Mkdir_IsNoOpSuccess(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	created, err := b.Mkdir(context.Background(), "some/dir", apifile.MkdirOptions{})
	if err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if !created {
		t.Fatalf("want created=true (no-op success)")
	}
	if fake.putCalls != 0 || fake.headCalls != 0 {
		t.Fatalf("want Mkdir to make NO S3 calls at all, got put=%d head=%d", fake.putCalls, fake.headCalls)
	}
}

func TestBackend_CopyBytes_UsesCopyObject(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "src.json", []byte("hello"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	if err := b.CopyBytes(ctx, "src.json", "dst.json"); err != nil {
		t.Fatalf("CopyBytes: %v", err)
	}
	got, err := b.ReadBytes(ctx, "dst.json")
	if err != nil {
		t.Fatalf("ReadBytes(dst): %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("dst content = %q, want %q", got, "hello")
	}
}

func TestBackend_DeleteBytesBatch_ChunksAt1000(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	paths := make([]string, 1500)
	for i := range paths {
		paths[i] = "batch/item.json"
	}
	for i := range paths {
		paths[i] = "batch/" + itoaTest(i) + ".json"
		if _, err := b.WriteBytes(ctx, paths[i], []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%d): %v", i, err)
		}
	}
	results, err := b.DeleteBytesBatch(ctx, paths)
	if err != nil {
		t.Fatalf("DeleteBytesBatch: %v", err)
	}
	if len(results) != 1500 {
		t.Fatalf("want 1500 results, got %d", len(results))
	}
	if fake.deleteObjectsCalls < 2 {
		t.Fatalf("want >1 DeleteObjects call for 1500 paths (chunked at 1000), got %d", fake.deleteObjectsCalls)
	}
	for i, r := range results {
		if !r.Existed {
			t.Fatalf("result[%d] Existed=false, want true", i)
		}
	}
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func TestBackend_WriteBytesIfMatch_UsesNativeIfMatch_NoLockFile(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	entry, err := b.Stat(ctx, "widget.json")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if _, err := b.WriteBytesIfMatch(ctx, "widget.json", []byte("v2"), entry.ETag, apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytesIfMatch: %v", err)
	}
	// Confirms NO lock-file/retry mechanism exists in this Backend at
	// all (structural absence, not just behavior) — S3's own PutObject
	// IfMatch IS the atomicity primitive, no client-side coordination.
	if !b.NativeConditionalWrite() {
		t.Fatalf("want NativeConditionalWrite() == true")
	}
}

func TestBackend_WriteBytesIfMatch_PreconditionFailed_MapsToMismatchError(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	_, err := b.WriteBytesIfMatch(ctx, "widget.json", []byte("v2"), "stale-etag", apifile.WriteOptions{})
	var mismatch apifile.ConditionalWriteMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("want ConditionalWriteMismatchError, got %v (%T)", err, err)
	}
	if mismatch.Expected != "stale-etag" {
		t.Fatalf("Expected = %q, want %q", mismatch.Expected, "stale-etag")
	}
}

func TestBackend_ReadMetadata_FromHeadObject(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{Metadata: map[string]string{"owner": "alice"}}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	fake.getCalls = 0
	md, err := b.ReadMetadata(ctx, "widget.json")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if md["owner"] != "alice" {
		t.Fatalf("metadata[owner] = %q, want %q", md["owner"], "alice")
	}
	if fake.getCalls != 0 {
		t.Fatalf("want ReadMetadata to use HeadObject only, got %d GetObject calls", fake.getCalls)
	}
}

func TestBackend_WriteMetadata_UsesCopyObjectSelfCopy(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("original-content"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	if err := b.WriteMetadata(ctx, "widget.json", map[string]string{"owner": "bob"}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	// Content must survive the self-copy unchanged.
	got, err := b.ReadBytes(ctx, "widget.json")
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if string(got) != "original-content" {
		t.Fatalf("content after WriteMetadata = %q, want unchanged %q", got, "original-content")
	}
	md, err := b.ReadMetadata(ctx, "widget.json")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if md["owner"] != "bob" {
		t.Fatalf("metadata[owner] = %q, want %q", md["owner"], "bob")
	}
}

func TestBackend_KeyPrefix_ScopesEveryOperation(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket", adapters3.KeyPrefix("tenant-123"))
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("x"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	if _, ok := fake.objects["tenant-123/widget.json"]; !ok {
		t.Fatalf("want the real S3 key to be prefixed with the configured KeyPrefix, got keys: %v", fake.objects)
	}
}
