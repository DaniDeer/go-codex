package file_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	fileadapter "github.com/DaniDeer/go-codex/adapters/file"
	apifile "github.com/DaniDeer/go-codex/api/file"
)

// ── WalkingBackend ───────────────────────────────────────────────────────

func TestBackend_WalkDir_VisitsRootAndEveryDescendant(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "f1.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "f2.txt"), []byte("y"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	backend := fileadapter.NewBackend()
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

	want := []string{
		dir,
		filepath.Join(dir, "a"),
		filepath.Join(dir, "a", "b"),
		filepath.Join(dir, "a", "b", "f2.txt"),
		filepath.Join(dir, "a", "f1.txt"),
	}
	if len(visited) != len(want) {
		t.Fatalf("visited = %v, want %v", visited, want)
	}
	for i, w := range want {
		if visited[i] != w {
			t.Errorf("visited[%d] = %q, want %q", i, visited[i], w)
		}
	}
}

func TestBackend_WalkDir_SkipDirHonored(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "skip", "inner"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skip", "inner", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	backend := fileadapter.NewBackend()
	var visited []string
	err := backend.WalkDir(context.Background(), dir, func(path string, entry apifile.Entry, err error) error {
		if err != nil {
			return err
		}
		visited = append(visited, path)
		if entry.IsDir && filepath.Base(path) == "skip" {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	for _, v := range visited {
		if v == filepath.Join(dir, "skip", "inner") {
			t.Fatalf("visited %q, want it skipped via filepath.SkipDir", v)
		}
	}
}

func TestBackend_WalkDir_NonExistentRoot_ReturnsError(t *testing.T) {
	backend := fileadapter.NewBackend()
	err := backend.WalkDir(context.Background(), filepath.Join(t.TempDir(), "nope"), func(path string, entry apifile.Entry, err error) error {
		return err
	})
	if err == nil {
		t.Fatal("expected error for non-existent root, got nil")
	}
}

// ── DirDeletingBackend ───────────────────────────────────────────────────

func TestBackend_DeleteDir_NonRecursive_RemovesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "empty")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	affected, err := backend.DeleteDir(context.Background(), target, false, false)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if len(affected) != 1 || affected[0] != target {
		t.Fatalf("affected = %v, want [%s]", affected, target)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s to be removed", target)
	}
}

func TestBackend_DeleteDir_NonRecursive_NonEmpty_ReturnsDirNotEmptyError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "full")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	_, err := backend.DeleteDir(context.Background(), target, false, false)
	var notEmpty apifile.DirNotEmptyError
	if !errors.As(err, &notEmpty) {
		t.Fatalf("DeleteDir: err = %v, want DirNotEmptyError", err)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("non-empty dir must NOT be removed: %v", statErr)
	}
}

func TestBackend_DeleteDir_Recursive_RemovesEverything(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "sub", "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	affected, err := backend.DeleteDir(context.Background(), target, true, false)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if len(affected) != 3 {
		t.Fatalf("affected = %v, want 3 entries (tree, tree/sub, tree/sub/f.txt)", affected)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("expected %s to be removed", target)
	}
}

func TestBackend_DeleteDir_Recursive_DryRun_ReportsWithoutRemoving(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "tree")
	if err := os.MkdirAll(filepath.Join(target, "sub"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	affected, err := backend.DeleteDir(context.Background(), target, true, true)
	if err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if len(affected) != 2 {
		t.Fatalf("affected = %v, want 2 entries (tree, tree/sub)", affected)
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Fatalf("DryRun must NOT remove anything: %v", statErr)
	}
}

func TestBackend_DeleteDir_NotADirectory_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	backend := fileadapter.NewBackend()
	_, err := backend.DeleteDir(context.Background(), target, false, false)
	if err == nil {
		t.Fatal("expected error for non-directory path, got nil")
	}
}

func TestBackend_DeleteDir_NonExistent_ReturnsNotExistError(t *testing.T) {
	backend := fileadapter.NewBackend()
	_, err := backend.DeleteDir(context.Background(), filepath.Join(t.TempDir(), "nope"), false, false)
	if !os.IsNotExist(err) {
		t.Fatalf("err = %v, want os.IsNotExist", err)
	}
}

// ── Strict-write atomicity ────────────────────────────────────────────────

func TestBackend_WriteBytes_Strict_UsesAtomicOpen_NotStatThenWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "item.json")
	backend := fileadapter.NewBackend()

	// First Strict write succeeds (path doesn't exist yet).
	if _, err := backend.WriteBytes(context.Background(), path, []byte("first"), apifile.WriteOptions{Strict: true}); err != nil {
		t.Fatalf("first Strict write: %v", err)
	}

	// Second Strict write against the SAME path must fail with
	// FileAlreadyExistsError via the atomic O_CREATE|O_EXCL open path
	// (not a separate stat-then-write race window).
	_, err := backend.WriteBytes(context.Background(), path, []byte("second"), apifile.WriteOptions{Strict: true})
	var existsErr apifile.FileAlreadyExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("second Strict write: err = %v, want FileAlreadyExistsError", err)
	}

	// Original content must be untouched.
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != "first" {
		t.Fatalf("content = %q, want %q (Strict must not overwrite)", data, "first")
	}
}
