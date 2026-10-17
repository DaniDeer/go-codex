package s3_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	adapters3 "github.com/DaniDeer/go-codex/adapters/s3"
	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_WalkDir_SynthesizesDirectoryEntriesFromKeySegments(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	for _, key := range []string{"root/a.json", "root/sub/b.json", "root/sub/deeper/c.json"} {
		if _, err := b.WriteBytes(ctx, key, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", key, err)
		}
	}
	var visited []string
	var rootSeenFirst bool
	first := true
	err := b.WalkDir(ctx, "root", func(path string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		if first {
			rootSeenFirst = path == "root" && entry.IsDir
			first = false
		}
		visited = append(visited, path)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if !rootSeenFirst {
		t.Fatalf("want root itself visited FIRST, got visited order: %v", visited)
	}
	wantPaths := map[string]bool{
		"root":                   true,
		"root/a.json":            true,
		"root/sub":               true,
		"root/sub/b.json":        true,
		"root/sub/deeper":        true,
		"root/sub/deeper/c.json": true,
	}
	for _, p := range visited {
		if !wantPaths[p] {
			t.Errorf("unexpected visited path %q", p)
		}
		delete(wantPaths, p)
	}
	if len(wantPaths) != 0 {
		t.Fatalf("missing expected visited paths: %v", wantPaths)
	}
}

func TestBackend_WalkDir_SkipDir_PrunesSubtree(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	for _, key := range []string{"root/keep.json", "root/skip/inner.json", "root/skip/deeper/x.json"} {
		if _, err := b.WriteBytes(ctx, key, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", key, err)
		}
	}
	var visited []string
	err := b.WalkDir(ctx, "root", func(path string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		visited = append(visited, path)
		if path == "root/skip" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	for _, p := range visited {
		if p == "root/skip/inner.json" || p == "root/skip/deeper" || p == "root/skip/deeper/x.json" {
			t.Fatalf("want skip/'s subtree pruned, but visited %q (full: %v)", p, visited)
		}
	}
}

func TestBackend_DeleteDir_NonRecursive_NonEmpty_ReturnsDirNotEmptyError(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "dir/item.json", []byte("x"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	_, err := b.DeleteDir(ctx, "dir", false, false)
	var notEmpty apifile.DirNotEmptyError
	if !errors.As(err, &notEmpty) {
		t.Fatalf("want DirNotEmptyError, got %v (%T)", err, err)
	}
}

func TestBackend_DeleteDir_Recursive_ChunkedDelete(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	for i := 0; i < 1200; i++ {
		if _, err := b.WriteBytes(ctx, "bigdir/"+itoaTest(i)+".json", []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%d): %v", i, err)
		}
	}
	affected, err := b.DeleteDir(ctx, "bigdir", true, false)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	// affected includes "bigdir" itself (root, synthetic) + 1200 files.
	if len(affected) != 1201 {
		t.Fatalf("want 1201 affected entries (root + 1200 files), got %d", len(affected))
	}
	var rootIncluded bool
	for _, p := range affected {
		if p == "bigdir" {
			rootIncluded = true
		}
	}
	if !rootIncluded {
		t.Fatalf("want affected to include the root path itself, got %v", affected[:5])
	}
	if fake.deleteObjectsCalls < 2 {
		t.Fatalf("want chunked DeleteObjects (>1000 keys), got %d calls", fake.deleteObjectsCalls)
	}
	remaining, err := b.ListDir(ctx, "bigdir")
	if err != nil {
		t.Fatalf("ListDir after delete: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("want bigdir fully emptied, got %d remaining entries", len(remaining))
	}
}

func TestBackend_DeleteDir_Recursive_DryRun_DoesNotDelete(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "dir/item.json", []byte("x"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	affected, err := b.DeleteDir(ctx, "dir", true, true)
	if err != nil {
		t.Fatalf("DeleteDir (dry-run): %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("want 2 affected entries (root + item.json), got %d: %v", len(affected), affected)
	}
	if _, err := b.ReadBytes(ctx, "dir/item.json"); err != nil {
		t.Fatalf("want item.json to still exist after DryRun delete, got %v", err)
	}
}
