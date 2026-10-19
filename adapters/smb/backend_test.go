package smb

import (
	"context"
	"errors"
	"os"
	"testing"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_ReadBytes_NotFound_SatisfiesOsErrNotExist(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	_, err := b.ReadBytes(context.Background(), "missing.json")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want errors.Is(err, os.ErrNotExist), got %v", err)
	}
}

func TestBackend_WriteBytes_Strict_UsesCreateExcl(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()

	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{Strict: true}); err != nil {
		t.Fatalf("first Strict write: %v", err)
	}
	_, err := b.WriteBytes(ctx, "widget.json", []byte("v2"), apifile.WriteOptions{Strict: true})
	var already apifile.FileAlreadyExistsError
	if !errors.As(err, &already) {
		t.Fatalf("want FileAlreadyExistsError on 2nd Strict write, got %v (%T)", err, err)
	}

	data, readErr := b.ReadBytes(ctx, "widget.json")
	if readErr != nil {
		t.Fatalf("ReadBytes: %v", readErr)
	}
	if string(data) != "v1" {
		t.Fatalf("want unmodified v1 content after rejected Strict overwrite, got %q", data)
	}
}

func TestBackend_WriteBytes_Overwrite_NonStrict(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v2"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	data, err := b.ReadBytes(ctx, "widget.json")
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if string(data) != "v2" {
		t.Fatalf("want v2, got %q", data)
	}
}

// TestBackend_WriteBytes_ExplicitPerm_AppliedViaChmod is a regression
// proof for a real bug found via a deep-dive-bug-hunt live-reproduction
// test: Share.Create takes no mode parameter at all (unlike
// os.WriteFile's own perm argument), so an explicitly-requested
// WriteOptions.Perm (or an attach-time NativePOSIXMode Capability) used
// to be SILENTLY DROPPED on every ordinary (non-Strict) write — not just
// on overwrite, like adapters/file's own equivalent bug, but on EVERY
// write including the very first one. Fixed via a best-effort Chmod
// call after a successful write, applied only when Perm was explicitly
// requested (mirrors NativePOSIXMode's own documented degraded-
// capability precedent elsewhere in this adapter).
func TestBackend_WriteBytes_ExplicitPerm_AppliedViaChmod(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()

	want := os.FileMode(0o600)
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{Perm: want}); err != nil {
		t.Fatalf("write: %v", err)
	}
	fake.mu.Lock()
	got := fake.nodes["widget.json"].mode
	fake.mu.Unlock()
	if got != want {
		t.Fatalf("want mode %o applied via Chmod, got %o", want, got)
	}
}

func TestBackend_DeleteBytes_Idempotent(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	existed, err := b.DeleteBytes(ctx, "widget.json", apifile.DeleteOptions{})
	if err != nil || !existed {
		t.Fatalf("want existed=true nil err, got existed=%v err=%v", existed, err)
	}
	existed, err = b.DeleteBytes(ctx, "widget.json", apifile.DeleteOptions{})
	if err != nil || existed {
		t.Fatalf("want idempotent existed=false nil err on 2nd delete, got existed=%v err=%v", existed, err)
	}
}

func TestBackend_ListDir_ExcludesControlFiles(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	for _, name := range []string{"a.json", "a.json.meta.json", "a.json.lock", "a.json.tmp-123"} {
		if _, err := b.WriteBytes(ctx, name, []byte("x"), apifile.WriteOptions{}); err != nil {
			t.Fatalf("WriteBytes(%s): %v", name, err)
		}
	}
	entries, err := b.ListDir(ctx, "")
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "a.json" {
		t.Fatalf("want exactly [a.json], got %+v", entries)
	}
}

func TestBackend_Stat_ReturnsWeakETag(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	entry, err := b.Stat(ctx, "widget.json")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	// SMB has no server-computed content hash, but NEITHER does the
	// filesystem backend — both derive a WEAK (ModTime+Size) ETag
	// locally, so the standard Exists-then-IfMatch workflow works
	// identically across backends (see toEntry's own doc comment for
	// the implementation-time correction this represents).
	want := apifile.WeakETag(entry.ModTime, entry.Size)
	if entry.ETag != want {
		t.Fatalf("want weak ETag %q, got %q", want, entry.ETag)
	}
	if entry.Size != 2 {
		t.Fatalf("want size 2, got %d", entry.Size)
	}
}

func TestBackend_Stat_Directory_HasEmptyETag(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.Mkdir(ctx, "dir", apifile.MkdirOptions{}); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	entry, err := b.Stat(ctx, "dir")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if entry.ETag != "" {
		t.Fatalf("want empty ETag for a directory entry, got %q", entry.ETag)
	}
}

func TestBackend_Mkdir_CreateDirs_UsesMkdirAll(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	created, err := b.Mkdir(ctx, "a/b/c", apifile.MkdirOptions{})
	if err != nil || !created {
		t.Fatalf("want created=true nil err, got created=%v err=%v", created, err)
	}
	if fake.mkdirAllCalls != 1 {
		t.Fatalf("want exactly 1 MkdirAll call (native recursive creation), got %d", fake.mkdirAllCalls)
	}
	entry, statErr := b.Stat(ctx, "a/b/c")
	if statErr != nil || !entry.IsDir {
		t.Fatalf("want a/b/c to exist as a directory, got entry=%+v err=%v", entry, statErr)
	}
}

func TestBackend_CopyBytes_UsesIOCopy(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "src.json", []byte("hello"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := b.CopyBytes(ctx, "src.json", "dst.json"); err != nil {
		t.Fatalf("CopyBytes: %v", err)
	}
	data, err := b.ReadBytes(ctx, "dst.json")
	if err != nil || string(data) != "hello" {
		t.Fatalf("want dst.json=hello, got data=%q err=%v", data, err)
	}
}

func TestBackend_MovingBackend_UsesNativeRename(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "src.json", []byte("hello"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := b.MoveBytes(ctx, "src.json", "dst.json"); err != nil {
		t.Fatalf("MoveBytes: %v", err)
	}
	if fake.renameCalls != 1 {
		t.Fatalf("want exactly 1 Rename call, got %d", fake.renameCalls)
	}
	if _, err := b.ReadBytes(ctx, "src.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want src.json gone after move, got err=%v", err)
	}
	data, err := b.ReadBytes(ctx, "dst.json")
	if err != nil || string(data) != "hello" {
		t.Fatalf("want dst.json=hello, got data=%q err=%v", data, err)
	}
}

func TestBackend_MetadataBackend_UsesJSONSidecar(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	if err := b.WriteMetadata(ctx, "widget.json", map[string]string{"owner": "alice"}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	if _, ok := fake.nodes["widget.json.meta.json"]; !ok {
		t.Fatalf("want a widget.json.meta.json sidecar node to exist")
	}
	md, err := b.ReadMetadata(ctx, "widget.json")
	if err != nil {
		t.Fatalf("ReadMetadata: %v", err)
	}
	if md["owner"] != "alice" {
		t.Fatalf("want owner=alice, got %+v", md)
	}
}

func TestBackend_MetadataBackend_AbsentIsNilNotError(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	md, err := b.ReadMetadata(context.Background(), "nope.json")
	if err != nil {
		t.Fatalf("want nil error for absent metadata, got %v", err)
	}
	if md != nil {
		t.Fatalf("want nil map for absent metadata, got %+v", md)
	}
}

func TestBackend_NativePOSIXMode_WiredViaChmod(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	perm := os.FileMode(0700)
	if _, err := b.Mkdir(ctx, "dir", apifile.MkdirOptions{NativePOSIXMode: &perm}); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	entry, err := b.Stat(ctx, "dir")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	_ = entry // mode isn't surfaced on apifile.Entry itself; proof is via fake's own node.
	if fake.nodes["dir"].mode != perm {
		t.Fatalf("want mode 0700 wired through Mkdir's NativePOSIXMode, got %v", fake.nodes["dir"].mode)
	}
}
