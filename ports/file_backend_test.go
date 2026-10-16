package ports

// White-box tests for osBackend (private, api/file.Backend-implementing
// type used internally by File[T]/Dir's delegation — see file_backend.go's
// own doc comment). Package ports (not ports_test) is required since
// osBackend is unexported; this is the legitimate "testing an internal
// mechanism directly" case the plan-a-new-codex-feature skill's public-vs-
// internal test-vocabulary rule calls out as an exception.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestOsBackend_WalkDir_VisitsRootAndEveryDescendant(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	backend := osBackend{}
	var visited []string
	err := backend.WalkDir(context.Background(), dir, func(path string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		visited = append(visited, path)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	if len(visited) != 4 {
		t.Fatalf("visited = %v, want 4 entries (dir, a, a/b, a/f.txt)", visited)
	}
}

func TestOsBackend_DeleteDir_NonRecursive_NonEmpty_ReturnsDirNotEmptyError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "full")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := osBackend{}
	_, err := backend.DeleteDir(context.Background(), target, false, false)
	var notEmpty apifile.DirNotEmptyError
	if !errors.As(err, &notEmpty) {
		t.Fatalf("DeleteDir: err = %v, want DirNotEmptyError", err)
	}
}

func TestOsBackend_DeleteDir_Recursive_RemovesEverything(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := osBackend{}
	affected, err := backend.DeleteDir(context.Background(), target, true, false)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("affected = %v, want 2 entries (tree, tree/sub)", affected)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s to be removed", target)
	}
}

// TestOsBackend_ListDir_ExcludesTempFiles is a RETROACTIVE regression
// proof (same class as adapters/file's own TestBackend_ListDir_
// ExcludesTempFiles) — osBackend.WriteBytesIfMatch's ALREADY-SHIPPED
// commit temp file was NOT excluded from ListDir before this fix.
func TestOsBackend_ListDir_ExcludesTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json.tmp-823719035"), []byte("partial"), 0600); err != nil {
		t.Fatalf("setup temp file: %v", err)
	}

	backend := osBackend{}
	entries, err := backend.ListDir(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "item.json" {
		t.Fatalf("want only item.json listed (.tmp-* excluded), got %+v", entries)
	}
}

// TestOsBackend_WalkDir_ExcludesTempFiles is WalkDir's own version.
func TestOsBackend_WalkDir_ExcludesTempFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "item.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "item.json.tmp-823719035"), []byte("partial"), 0600); err != nil {
		t.Fatalf("setup temp file: %v", err)
	}

	backend := osBackend{}
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

func TestOsBackend_WriteBytes_Strict_UsesAtomicOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	backend := osBackend{}

	if _, err := backend.WriteBytes(context.Background(), path, []byte("first"), apifile.WriteOptions{Strict: true}); err != nil {
		t.Fatalf("first Strict write: %v", err)
	}
	_, err := backend.WriteBytes(context.Background(), path, []byte("second"), apifile.WriteOptions{Strict: true})
	var existsErr apifile.FileAlreadyExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("second Strict write: err = %v, want FileAlreadyExistsError", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != "first" {
		t.Fatalf("content = %q, want %q (Strict must not overwrite)", data, "first")
	}
}
