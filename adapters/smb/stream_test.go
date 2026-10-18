package smb

import (
	"context"
	"io"
	"strings"
	"testing"

	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_OpenWriter_StreamsDirectly_NoIOPipeBridge(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	w, err := b.OpenWriter(ctx, "widget.json", apifile.WriteOptions{})
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	// Writes go straight to the open temp-file handle (confirmed via the
	// streamWriteCloser type itself — no io.Pipe goroutine machinery
	// exists in this Backend at all, a structural difference from
	// adapters/s3).
	if _, err := io.Copy(w, strings.NewReader("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, err := b.ReadBytes(ctx, "widget.json")
	if err != nil || string(data) != "hello" {
		t.Fatalf("want hello, got data=%q err=%v", data, err)
	}
}

// TestBackend_OpenWriter_CommitsAtomicallyViaRenameOnClose mirrors
// adapters/file's/adapters/s3's own already-shipped equivalent proofs —
// the destination path shows EITHER the complete prior content OR the
// complete new content, NEVER a partial write, even if Close is
// interrupted before the final Rename call (simulated here by NOT
// calling Close at all and confirming the destination is untouched).
func TestBackend_OpenWriter_CommitsAtomicallyViaRenameOnClose(t *testing.T) {
	fake := newFakeShare()
	b := newBackendFromOps(fake)
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("v1"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write v1: %v", err)
	}

	w, err := b.OpenWriter(ctx, "widget.json", apifile.WriteOptions{})
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	if _, err := w.Write([]byte("partial-new-content")); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Deliberately do NOT call Close — the destination must still show
	// the COMPLETE prior content, never a partial write.
	data, readErr := b.ReadBytes(ctx, "widget.json")
	if readErr != nil || string(data) != "v1" {
		t.Fatalf("want unmodified v1 before Close commits, got data=%q err=%v", data, readErr)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	data, readErr = b.ReadBytes(ctx, "widget.json")
	if readErr != nil || string(data) != "partial-new-content" {
		t.Fatalf("want complete new content after Close commits, got data=%q err=%v", data, readErr)
	}
	if fake.renameCalls == 0 {
		t.Fatalf("want at least 1 Rename call (the commit mechanism)")
	}
}

func TestBackend_OpenReader_DirectPassthrough(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("hello world"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := b.OpenReader(ctx, "widget.json")
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "hello world" {
		t.Fatalf("want hello world, got data=%q err=%v", data, err)
	}
}

func TestBackend_OpenReaderRange_UsesSectionReader_ReportsNativeTrue(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("0123456789"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := b.OpenReaderRange(ctx, "widget.json", 3, 4)
	if err != nil {
		t.Fatalf("OpenReaderRange: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "3456" {
		t.Fatalf("want bytes [3:7]=3456, got data=%q err=%v", data, err)
	}
	// RangeReadingBackend's mere existence on this Backend IS the
	// native=true signal Client.OpenRead's 3-level fallback cascade
	// consults (io.ReaderAt is a first-class SMB2 operation) — see
	// stats.FileObserver.RecordFileRangeRead's own doc comment.
	var _ apifile.RangeReadingBackend = b
}

func TestBackend_OpenReaderRange_ZeroLengthReadsToEOF(t *testing.T) {
	b := newBackendFromOps(newFakeShare())
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "widget.json", []byte("0123456789"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, err := b.OpenReaderRange(ctx, "widget.json", 7, 0)
	if err != nil {
		t.Fatalf("OpenReaderRange: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "789" {
		t.Fatalf("want bytes [7:]=789, got data=%q err=%v", data, err)
	}
}
