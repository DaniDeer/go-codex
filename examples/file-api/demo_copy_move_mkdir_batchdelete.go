package main

import (
	"context"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoCopyMoveMkdirBatchDelete shows the "directory/bulk" verbs: Copy
// (server-side, source untouched), Move (atomic rename on the
// filesystem backend), Mkdir (first-class directory creation, not just
// an implicit WriteOptions.CreateDirs side effect), and BatchDelete
// (per-key results, partial failure allowed — mirrors S3's own
// DeleteObjects).
func demoCopyMoveMkdirBatchDelete(ctx context.Context, client *file.Client, rt file.Router) {
	copyHandle, err := resources.Notes.WithCopy(file.Copy{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(copy): %v", err)
	}
	moveHandle, err := resources.Notes.WithMove(file.Move{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(move): %v", err)
	}
	batchDeleteHandle, err := resources.Notes.WithBatchDelete(file.BatchDelete{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(batchdelete): %v", err)
	}

	alice := map[string]string{"id": "alice"}
	carol := map[string]string{"id": "carol"}
	dave := map[string]string{"id": "dave"}
	bob := map[string]string{"id": "bob"}

	// Copy — server-side copy, source untouched.
	if err := client.Copy(ctx, copyHandle, alice, carol); err != nil {
		log.Fatalf("copy: %v", err)
	}
	fmt.Println("copied alice -> carol")

	// Move — atomic rename on the filesystem backend.
	if err := client.Move(ctx, moveHandle, carol, dave); err != nil {
		log.Fatalf("move: %v", err)
	}
	fmt.Println("moved carol -> dave")

	// Mkdir — the declared resource's path template IS the directory to
	// create (no {var} placeholder needed here, unlike Notes' per-item
	// template) — "archive" is relative, resolved against the SAME
	// Router prefix.
	archiveHandle, err := resources.Archive.WithMkdir(file.Mkdir{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(mkdir): %v", err)
	}
	created, err := client.Mkdir(ctx, archiveHandle, nil)
	if err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	fmt.Printf("mkdir archive: created=%v\n", created)

	// BatchDelete — per-key results, partial failure allowed.
	results, err := client.BatchDelete(ctx, batchDeleteHandle, []map[string]string{
		bob, dave, {"id": "never-existed"},
	})
	if err != nil {
		log.Fatalf("batch delete: %v", err)
	}
	for _, r := range results {
		fmt.Printf("batch delete %s: existed=%v err=%v\n", r.Path, r.Existed, r.Err)
	}
}
