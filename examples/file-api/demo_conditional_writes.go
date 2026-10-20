package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoConditionalWrites shows ETag/If-Match optimistic concurrency:
// retrieve the current ETag via [file.Client.Exists], then supply it as
// [file.WriteOptions.IfMatch] on a later Patch to make the write
// CONDITIONAL — it only commits if the resource's content hasn't
// changed since. A STALE ETag (simulating a concurrent writer) is
// rejected with [file.ConditionalWriteMismatchError].
func demoConditionalWrites(ctx context.Context, client *file.Client, rt file.Router) {
	existsHandle, err := resources.Notes.WithExists(file.Exists{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(exists): %v", err)
	}
	patchHandle, err := resources.Notes.WithPatch(resources.BodyPatchCodec(), file.Patch{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(conditional patch): %v", err)
	}

	vars := map[string]string{"id": "alice"}
	entry, exists, err := client.Exists(ctx, existsHandle, vars)
	if err != nil {
		log.Fatalf("exists: %v", err)
	}
	if !exists {
		log.Fatalf("expected alice to exist for conditional-write demo")
	}
	currentETag := entry.ETag
	fmt.Printf("current ETag: %s\n", currentETag)

	// Matching ETag — the write commits.
	freshBody := "Conditionally updated"
	if err := client.Patch[resources.Note, resources.BodyPatch](ctx, patchHandle, resources.BodyPatch{Body: &freshBody}, vars, file.WriteOptions{IfMatch: &currentETag}); err != nil {
		log.Fatalf("conditional patch (matching ETag): %v", err)
	}
	fmt.Println("conditional patch succeeded (ETag matched)")

	// A STALE ETag (the one we captured before the patch above already
	// committed) is now rejected — someone else changed the note first.
	staleBody := "Should never commit"
	err = client.Patch[resources.Note, resources.BodyPatch](ctx, patchHandle, resources.BodyPatch{Body: &staleBody}, vars, file.WriteOptions{IfMatch: &currentETag})
	var mismatch file.ConditionalWriteMismatchError
	if errors.As(err, &mismatch) {
		fmt.Println("confirmed: stale ETag rejected with ConditionalWriteMismatchError")
	} else {
		log.Fatalf("expected ConditionalWriteMismatchError for a stale ETag, got: %v", err)
	}
}
