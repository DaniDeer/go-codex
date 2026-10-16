package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoListAndExists shows:
//   - [file.Client.List]'s boxed return, []file.ListResult[T] — one
//     result PER DISCOVERED ENTRY, success or failure, never a plain
//     []T: a deliberately-invalid stray file written DIRECTLY (bypassing
//     the resource's own Create) proves a per-entry decode/match failure
//     never aborts the whole call — siblings still succeed.
//   - [file.Client.Exists] — the idempotent "absent" case is never an
//     error.
func demoListAndExists(ctx context.Context, client *file.Client, rt file.Router, dir string) {
	listHandle, err := resources.Notes.WithList(file.List{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(list): %v", err)
	}
	existsHandle, err := resources.Notes.WithExists(file.Exists{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(exists): %v", err)
	}

	// A stray file that does NOT conform to Note's own JSON codec —
	// written directly via os.WriteFile (bypassing Client.Create
	// entirely) to simulate a file some OTHER process left behind.
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("not valid json"), 0644); err != nil {
		log.Fatalf("setup stray file: %v", err)
	}

	listAny, err := client.List(ctx, listHandle, nil)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	results := listAny.([]file.ListResult[resources.Note])
	var okCount, errCount int
	for _, r := range results {
		if r.Err != nil {
			errCount++
			continue
		}
		okCount++
	}
	fmt.Printf("list: %d entries total (%d decoded ok, %d failed — corrupt.json is EXPECTED to fail, siblings still succeeded)\n", len(results), okCount, errCount)
	if errCount == 0 {
		log.Fatalf("expected at least 1 per-entry failure (corrupt.json), got 0")
	}

	// Clean up the stray file so later demos' List calls aren't affected.
	_ = os.Remove(filepath.Join(dir, "corrupt.json"))

	_, exists, err := client.Exists(ctx, existsHandle, map[string]string{"id": "alice"})
	if err != nil {
		log.Fatalf("exists: %v", err)
	}
	fmt.Printf("exists(alice) = %v\n", exists)
	_, exists, err = client.Exists(ctx, existsHandle, map[string]string{"id": "ghost"})
	if err != nil {
		log.Fatalf("exists(ghost): %v", err)
	}
	fmt.Printf("exists(ghost) = %v (absent is never an error)\n", exists)
}
