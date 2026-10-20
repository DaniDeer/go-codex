package main

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoStreamingIO shows both Streaming/chunked I/O mechanisms (see
// docs/design/d-0011-file-api-backend-transparency.md's "Streaming/chunked reads and writes"
// section):
//
//  1. Mechanism 1 — automatic format-level write streaming: a
//     format.NewStreamed resource's Create streams directly into the
//     Backend's own writer, with ZERO declare-time changes.
//  2. Mechanism 2 — codec-bypassing raw byte streams:
//     Client.OpenWrite/OpenRead, whole-file AND a byte-range read.
//
// adapters/file.Backend does NOT implement RangeReadingBackend in
// Phase 2 (see the "Backend semantic differences" table — GetObject's
// native Range support is adapters/s3's own eventual job) — this demo
// therefore exercises the APPROXIMATED fallback tier for real, and
// notes that the NATIVE tier is a future adapters/s3 concern rather
// than inventing a fake native backend just to exercise it.
func demoStreamingIO(ctx context.Context, client *file.Client, rt file.Router) {
	// Mechanism 1 — automatic.
	createHandle, err := resources.StreamableNotes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(streamable create): %v", err)
	}
	readHandle, err := resources.StreamableNotes.WithRead(file.Read{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(streamable read): %v", err)
	}
	vars := map[string]string{"id": "streamed-note"}
	if _, err := client.Create(ctx, createHandle, resources.StreamableNote{Title: "Streamed", Body: "written via MarshalTo, never buffered as []byte"}, vars, file.WriteOptions{CreateDirs: true}); err != nil {
		log.Fatalf("create (streamable): %v", err)
	}
	got, err := client.Read(ctx, readHandle, vars)
	if err != nil {
		log.Fatalf("read (streamable): %v", err)
	}
	fmt.Printf("Mechanism 1 (automatic format-level streaming): %+v\n", got)

	// Mechanism 2 — raw byte streams, whole-file round trip.
	streamWriteHandle, err := resources.Notes.WithStreamWrite(file.StreamWrite{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(stream write): %v", err)
	}
	streamReadHandle, err := resources.Notes.WithStreamRead(file.StreamRead{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(stream read): %v", err)
	}
	rawVars := map[string]string{"id": "raw-blob"}
	content := []byte(`{"Title":"Raw","Body":"bytes never touched the codec at all"}`)
	w, err := client.OpenWrite(ctx, streamWriteHandle, rawVars)
	if err != nil {
		log.Fatalf("OpenWrite: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		log.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		log.Fatalf("Close (commits atomically): %v", err)
	}

	r, err := client.OpenRead(ctx, streamReadHandle, rawVars)
	if err != nil {
		log.Fatalf("OpenRead (whole file): %v", err)
	}
	full, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		log.Fatalf("ReadAll: %v", err)
	}
	fmt.Printf("Mechanism 2 (whole-file round trip): %d bytes read back unchanged\n", len(full))

	// Mechanism 2 — byte-range read. adapters/file.Backend lacks
	// RangeReadingBackend, so this is served by the discard-then-limit
	// StreamReadBackend fallback tier (approximated, not native) —
	// RecordFileRangeRead would report native=false for this call.
	ranged, err := client.OpenRead(ctx, streamReadHandle, rawVars, file.StreamReadOptions{Offset: 1, Length: 7})
	if err != nil {
		log.Fatalf("OpenRead (ranged): %v", err)
	}
	partial, err := io.ReadAll(ranged)
	_ = ranged.Close()
	if err != nil {
		log.Fatalf("ReadAll (ranged): %v", err)
	}
	fmt.Printf("Mechanism 2 (byte-range read, approximated fallback tier): %q\n", partial)
}
