package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoCrudVerbs walks the "core 4" verbs: Create, Read, Patch, Delete —
// including [file.FileNotFoundError] navigation via errors.As. Every
// Handle below is built EXACTLY ONCE, through rt, so the temp-dir
// Router prefix is resolved at exactly one construction point.
func demoCrudVerbs(ctx context.Context, client *file.Client, rt file.Router) {
	readHandle, err := resources.Notes.WithRead(file.Read{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(read): %v", err)
	}
	createHandle, err := resources.Notes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(create): %v", err)
	}
	deleteHandle, err := resources.Notes.WithDelete(file.Delete{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(delete): %v", err)
	}

	// 1. Create.
	alice := map[string]string{"id": "alice"}
	bob := map[string]string{"id": "bob"}
	if _, err := client.Create(ctx, createHandle, resources.Note{Title: "Alice's note", Body: "Hello"}, alice); err != nil {
		log.Fatalf("create alice: %v", err)
	}
	if _, err := client.Create(ctx, createHandle, resources.Note{Title: "Bob's note", Body: "Hi"}, bob); err != nil {
		log.Fatalf("create bob: %v", err)
	}
	fmt.Println("created 2 notes")

	// 2. Read — returns a properly-typed Note, not just a raw `any`: the
	// dual-accept Client.Read always returns `any` by design (mirrors
	// reqreply.Client.Call — Go generics can't add a type param to a
	// method), but asserting it ONCE into a named, typed variable right
	// after the call is the idiomatic way to work with it from here on.
	aliceAny, err := client.Read(ctx, readHandle, alice)
	if err != nil {
		log.Fatalf("read alice: %v", err)
	}
	aliceNote := aliceAny.(resources.Note)
	fmt.Printf("read: %+v\n", aliceNote)

	// 3. Read a missing note — typed FileNotFoundError.
	_, err = client.Read(ctx, readHandle, map[string]string{"id": "ghost"})
	var notFound file.FileNotFoundError
	if errors.As(err, &notFound) {
		fmt.Println("confirmed: missing note reports FileNotFoundError")
	}

	// 4. Patch — partial update via a 2nd, smaller codec.
	newBody := "Updated hello"
	patchHandle, err := file.WithPatch(resources.Notes, resources.BodyPatchCodec(), file.Patch{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(patch): %v", err)
	}
	if err := client.Patch(ctx, patchHandle, resources.BodyPatch{Body: &newBody}, alice); err != nil {
		log.Fatalf("patch: %v", err)
	}
	patchedAny, _ := client.Read(ctx, readHandle, alice)
	fmt.Printf("after patch: %+v\n", patchedAny.(resources.Note))

	// 5. Delete — idempotent single-key delete (bob stays, used by later
	// demos: List/Copy/Move/BatchDelete).
	existed, err := client.Delete(ctx, deleteHandle, map[string]string{"id": "does-not-exist"})
	if err != nil {
		log.Fatalf("delete nonexistent: %v", err)
	}
	fmt.Printf("delete nonexistent: existed=%v (idempotent, no error)\n", existed)
}
