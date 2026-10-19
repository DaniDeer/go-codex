package s3_test

import (
	"context"
	"io"
	"testing"
	"time"

	adapters3 "github.com/DaniDeer/go-codex/adapters/s3"
	apifile "github.com/DaniDeer/go-codex/api/file"
)

func TestBackend_OpenWriter_StreamsViaIOPipe_CommitsOnClose(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()

	w, err := b.OpenWriter(ctx, "stream.bin", apifile.WriteOptions{})
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	if _, err := w.Write([]byte("hello, ")); err != nil {
		t.Fatalf("Write part 1: %v", err)
	}
	// Before Close, the fake must NOT have received the committed
	// object yet — proves the SINGLE PutObject call only fires once
	// Close() signals EOF to the io.Pipe bridge's upload goroutine.
	// NOTE: as actually implemented, OpenWriter's own goroutine DOES
	// buffer the full write via io.ReadAll(pr) before that one
	// PutObject call (see Backend.OpenWriter's own doc comment for the
	// disclosed rationale — true zero-buffer PutObject streaming needs
	// AWS's "aws-chunked" transfer encoding, which the in-process
	// gofakes3 fake this package's own example uses does not support)
	// — this assertion proves COMMIT TIMING (nothing lands until
	// Close), not zero in-memory buffering; found stale during a
	// deep-dive-bug-hunt pass (the comment previously claimed the
	// opposite of what this test can actually prove).
	if _, ok := fake.objects["stream.bin"]; ok {
		t.Fatalf("want object NOT committed before Close()")
	}
	if _, err := w.Write([]byte("streaming world")); err != nil {
		t.Fatalf("Write part 2: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := b.ReadBytes(ctx, "stream.bin")
	if err != nil {
		t.Fatalf("ReadBytes: %v", err)
	}
	if string(got) != "hello, streaming world" {
		t.Fatalf("content = %q, want %q", got, "hello, streaming world")
	}
}

// TestBackend_OpenWriter_BackendOptions_AppliesPutOptions proves
// WriteOptions.BackendOptions also reaches the io.Pipe-bridged
// PutObject call inside OpenWriter — the 3rd and final real call site
// applyPutOptions is wired into (alongside WriteBytes and
// WriteBytesIfMatch, both proven in backend_test.go).
func TestBackend_OpenWriter_BackendOptions_AppliesPutOptions(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()

	opts := apifile.WriteOptions{BackendOptions: adapters3.PutOptions{ContentType: "image/png"}}
	w, err := b.OpenWriter(ctx, "stream-opts.bin", opts)
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	if _, err := w.Write([]byte("data")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := fake.objects["stream-opts.bin"].contentType; got != "image/png" {
		t.Errorf("contentType = %q, want image/png", got)
	}
}

func TestBackend_OpenWriter_CtxCancelled_UnblocksWrite(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx, cancel := context.WithCancel(context.Background())

	w, err := b.OpenWriter(ctx, "cancelled.bin", apifile.WriteOptions{})
	if err != nil {
		t.Fatalf("OpenWriter: %v", err)
	}
	cancel()
	// Give the ctx-watcher goroutine a chance to close the pipe; then a
	// blocked/future Write must return promptly (not deadlock).
	done := make(chan struct{})
	go func() {
		_, _ = w.Write([]byte("x"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Write did not unblock after ctx cancellation")
	}
}

func TestBackend_OpenReader_ReturnsGetObjectBodyDirectly(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	if _, err := b.WriteBytes(ctx, "blob.bin", []byte("direct passthrough"), apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	r, err := b.OpenReader(ctx, "blob.bin")
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "direct passthrough" {
		t.Fatalf("content = %q, want %q", got, "direct passthrough")
	}
}

func TestBackend_OpenReaderRange_SetsRangeHeader(t *testing.T) {
	fake := newFakeS3("bucket")
	b := adapters3.NewBackend(fake, "bucket")
	ctx := context.Background()
	content := []byte("hello, streaming world")
	if _, err := b.WriteBytes(ctx, "blob.bin", content, apifile.WriteOptions{}); err != nil {
		t.Fatalf("WriteBytes: %v", err)
	}
	r, err := b.OpenReaderRange(ctx, "blob.bin", 7, 9)
	if err != nil {
		t.Fatalf("OpenReaderRange: %v", err)
	}
	defer r.Close()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := content[7:16]
	if string(got) != string(want) {
		t.Fatalf("ranged content = %q, want %q", got, want)
	}
}
