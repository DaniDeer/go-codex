package smb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_WalkDir_HonorsSkipDir(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	for _, dir := range []string{"skipme", "keep"} {
		if _, err := b.Mkdir(ctx, dir, apifile.MkdirOptions{}); err != nil {
			t.Fatalf("Mkdir(%s): %v", dir, err)
		}
	}
	for _, name := range []string{"a.json", "skipme/b.json", "skipme/c.json", "keep/d.json"} {
		if _, err := b.WriteBytes(ctx, name, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", name, err)
		}
	}
	var visited []string
	err := b.WalkDir(ctx, "", func(p string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		visited = append(visited, p)
		if entry.IsDir && entry.Name == "skipme" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	for _, p := range visited {
		if p == "skipme/b.json" || p == "skipme/c.json" {
			t.Fatalf("want skipme/* excluded via SkipDir, got visited=%v", visited)
		}
	}
	found := false
	for _, p := range visited {
		if p == "keep/d.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want keep/d.json visited, got %v", visited)
	}
}

func TestBackend_WalkDir_ExcludesControlFiles(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	for _, name := range []string{"a.json", "a.json.meta.json", "a.json.lock"} {
		if _, err := b.WriteBytes(ctx, name, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", name, err)
		}
	}
	var visited []string
	err := b.WalkDir(ctx, "", func(p string, _ apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		visited = append(visited, p)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	sort.Strings(visited)
	for _, p := range visited {
		if p == "a.json.meta.json" || p == "a.json.lock" {
			t.Fatalf("want control files invisible to WalkDir, got visited=%v", visited)
		}
	}
}

func TestBackend_DeleteDir_NonRecursive_RemovesEmptyDir(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.Mkdir(ctx, "emptydir", apifile.MkdirOptions{}); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	affected, err := b.DeleteDir(ctx, "emptydir", false, false)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if len(affected) != 1 || affected[0] != "emptydir" {
		t.Fatalf("want affected=[emptydir], got %v", affected)
	}
	if _, statErr := b.Stat(ctx, "emptydir"); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("want emptydir gone after delete, got err=%v", statErr)
	}
}

func TestBackend_DeleteDir_NonRecursive_NonEmpty_ReturnsDirNotEmptyError(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.Mkdir(ctx, "dir", apifile.MkdirOptions{}); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if _, err := b.WriteBytes(ctx, "dir/file.json", []byte("x"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	_, err := b.DeleteDir(ctx, "dir", false, false)
	var notEmpty apifile.DirNotEmptyError
	if !errors.As(err, &notEmpty) {
		t.Fatalf("want DirNotEmptyError, got %v (%T)", err, err)
	}
}

func TestBackend_DeleteDir_Recursive_UsesNativeRemoveAll(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	for _, dir := range []string{"dir", "dir/sub"} {
		if _, err := b.Mkdir(ctx, dir, apifile.MkdirOptions{}); err != nil {
			t.Fatalf("Mkdir(%s): %v", dir, err)
		}
	}
	for _, name := range []string{"dir/a.json", "dir/sub/b.json"} {
		if _, err := b.WriteBytes(ctx, name, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", name, err)
		}
	}
	affected, err := b.DeleteDir(ctx, "dir", true, false)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if len(affected) < 2 {
		t.Fatalf("want affected to include at least the 2 files (root+children), got %v", affected)
	}
	if _, statErr := b.Stat(ctx, "dir"); statErr == nil {
		t.Fatalf("want dir gone after recursive delete")
	}
}

// TestBackend_DeleteDir_Recursive_DryRun_ReportsWithoutRemoving proves the
// gap-review fix: recursive DryRun collects affected via WalkDir and
// NEVER calls RemoveAll.
func TestBackend_DeleteDir_Recursive_DryRun_ReportsWithoutRemoving(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	for _, dir := range []string{"dir", "dir/sub"} {
		if _, err := b.Mkdir(ctx, dir, apifile.MkdirOptions{}); err != nil {
			t.Fatalf("Mkdir(%s): %v", dir, err)
		}
	}
	for _, name := range []string{"dir/a.json", "dir/sub/b.json"} {
		if _, err := b.WriteBytes(ctx, name, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", name, err)
		}
	}
	affected, err := b.DeleteDir(ctx, "dir", true, true)
	if err != nil {
		t.Fatalf("DeleteDir (dry run): %v", err)
	}
	if len(affected) < 2 {
		t.Fatalf("want affected to report what WOULD be removed, got %v", affected)
	}
	if _, statErr := b.Stat(ctx, "dir"); statErr != nil {
		t.Fatalf("want dir to STILL exist after a dry run, got err=%v", statErr)
	}
	if _, statErr := b.Stat(ctx, "dir/a.json"); statErr != nil {
		t.Fatalf("want dir/a.json to STILL exist after a dry run, got err=%v", statErr)
	}
}
