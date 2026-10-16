package main

import (
	"context"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoObservability exercises a handful of verbs purely to generate
// [stats.FileObserver] events — the attached Observer (a fanout of
// observer.CountingObserver + stats.LoggingObserver, wired into ctx by
// main.go BEFORE any demo ran) has already been recording every call
// made so far; this demo runs a few MORE, deliberately including a
// failure (RecordFileExists with success still true — absent is not a
// failure — and a genuine RecordFileDelete failure via Strict) so the
// final summary (printed once, at the very end of main.go, after every
// demo has run) shows a non-trivial mix.
func demoObservability(ctx context.Context, client *file.Client, rt file.Router) {
	existsHandle, err := resources.Notes.WithExists(file.Exists{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(exists): %v", err)
	}
	deleteHandle, err := resources.Notes.WithDelete(file.Delete{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(delete): %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, _, err := client.Exists(ctx, existsHandle, map[string]string{"id": "alice"}); err != nil {
			log.Fatalf("exists: %v", err)
		}
	}

	// A genuine Strict-delete failure (the path doesn't exist, Strict
	// requires it to) — deliberately generates a failed RecordFileDelete
	// event for the summary.
	_, err = client.Delete(ctx, deleteHandle, map[string]string{"id": "never-existed-at-all"}, file.DeleteOptions{Strict: true})
	if err == nil {
		log.Fatalf("expected a Strict-delete failure for a nonexistent file")
	}
	fmt.Println("generated several Observer events (Exists x3, a failed Strict Delete) — see the final summary")
}
