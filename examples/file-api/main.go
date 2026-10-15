// Package main demonstrates [file.Client], go-codex's new transport-agnostic
// file-resource API (Phase 1 of docs/roadmap/file-api.md).
//
// Scenario: a small notes store exercising all 10 declared verbs against the
// local filesystem via [adapterfile.NewBackend] — the exact same
// [file.Resource] declarations would work unchanged against a future
// S3/SMB backend (Phase 2), since nothing here calls os.* directly.
//
// Key patterns shown:
//   - [file.NewResource] — declare once, use anywhere (mirrors rest.Route /
//     events.Channel / ports.File)
//   - [file.Client.Attach] — EXCLUSIVE attach-time backend wiring; a 2nd
//     call returns [file.BackendAlreadyAttachedError]
//   - Dual-accept dispatch: every [file.Client] method accepts either the
//     raw role builder ([file.Resource.WithRead] etc.) or a pre-built
//     Handle — this example uses the common, bare-builder form throughout
//   - All 10 verbs: Create, Read, List, Exists, Patch, Copy, Move, Mkdir,
//     BatchDelete, Delete
//   - [file.FileNotFoundError] — navigate via errors.As
//
// Run with: go run ./examples/file-api
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// Note is the domain type stored, one JSON file per note.
type Note struct {
	Title string
	Body  string
}

func noteCodec() codex.Codec[Note] {
	return codex.Struct[Note](
		codex.RequiredField("title", codex.String(),
			func(n Note) string { return n.Title },
			func(n *Note, v string) { n.Title = v }),
		codex.RequiredField("body", codex.String(),
			func(n Note) string { return n.Body },
			func(n *Note, v string) { n.Body = v }),
	)
}

func main() {
	dir, err := os.MkdirTemp("", "file-api-example-*")
	if err != nil {
		log.Fatalf("setup: %v", err)
	}
	defer os.RemoveAll(dir)

	// 1. Declare the resource: WHAT a note looks like (path template + codec).
	notes := file.NewResource[Note](filepath.Join(dir, "{id}.json"), format.JSON(noteCodec()),
		file.PathParam{Name: "id"},
	)

	// 2. Attach a backend: WHERE notes actually live. Swapping this one
	// line for a future adapters/s3.NewBackend()/adapters/smb.NewBackend()
	// is the entire migration a new backend requires — the Resource
	// declaration above never changes.
	client := file.NewClient()
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		log.Fatalf("attach: %v", err)
	}
	ctx := context.Background()

	// A 2nd Attach is rejected — exclusive, mirrors events.Client.Attach.
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		var already file.BackendAlreadyAttachedError
		if errors.As(err, &already) {
			fmt.Println("confirmed: 2nd Attach rejected as expected")
		}
	}

	// 3. Create.
	alice := map[string]string{"id": "alice"}
	bob := map[string]string{"id": "bob"}
	if err := client.Create(ctx, notes.WithCreate(file.Create{}), Note{Title: "Alice's note", Body: "Hello"}, alice); err != nil {
		log.Fatalf("create alice: %v", err)
	}
	if err := client.Create(ctx, notes.WithCreate(file.Create{}), Note{Title: "Bob's note", Body: "Hi"}, bob); err != nil {
		log.Fatalf("create bob: %v", err)
	}
	fmt.Println("created 2 notes")

	// 4. Read.
	gotAny, err := client.Read(ctx, notes.WithRead(file.Read{}), alice)
	if err != nil {
		log.Fatalf("read alice: %v", err)
	}
	fmt.Printf("read: %+v\n", gotAny.(Note))

	// 5. Read a missing note — typed FileNotFoundError.
	_, err = client.Read(ctx, notes.WithRead(file.Read{}), map[string]string{"id": "ghost"})
	var notFound file.FileNotFoundError
	if errors.As(err, &notFound) {
		fmt.Println("confirmed: missing note reports FileNotFoundError")
	}

	// 6. List — enumerates the resource's parent directory.
	listAny, err := client.List(ctx, notes.WithList(file.List{}), nil)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	fmt.Printf("list: %d notes found\n", len(listAny.([]Note)))

	// 7. Exists.
	_, exists, err := client.Exists(ctx, notes.WithExists(file.Exists{}), alice)
	if err != nil {
		log.Fatalf("exists: %v", err)
	}
	fmt.Printf("exists(alice) = %v\n", exists)

	// 8. Patch — partial update via a 2nd, smaller codec.
	type bodyPatch struct{ Body *string }
	patchCodec := codex.PartialStruct[bodyPatch](
		codex.PartialField("body", codex.String(),
			func(p bodyPatch) *string { return p.Body },
			func(p *bodyPatch, v *string) { p.Body = v }),
	)
	newBody := "Updated hello"
	patcher := file.WithPatch(notes, patchCodec, file.Patch{})
	if err := client.Patch(ctx, patcher, bodyPatch{Body: &newBody}, alice); err != nil {
		log.Fatalf("patch: %v", err)
	}
	patchedAny, _ := client.Read(ctx, notes.WithRead(file.Read{}), alice)
	fmt.Printf("after patch: %+v\n", patchedAny.(Note))

	// 9. Copy — server-side copy, source untouched.
	carol := map[string]string{"id": "carol"}
	if err := client.Copy(ctx, notes.WithCopy(file.Copy{}), alice, carol); err != nil {
		log.Fatalf("copy: %v", err)
	}
	fmt.Println("copied alice -> carol")

	// 10. Move — atomic rename on the filesystem backend.
	dave := map[string]string{"id": "dave"}
	if err := client.Move(ctx, notes.WithMove(file.Move{}), carol, dave); err != nil {
		log.Fatalf("move: %v", err)
	}
	fmt.Println("moved carol -> dave")

	// 11. Mkdir — first-class directory creation, not just an implicit
	// WriteOptions.CreateDirs side effect. The declared resource's path
	// template IS the directory to create (no {var} placeholder needed
	// here, unlike notes' per-item template).
	archiveDir := file.NewResource[Note](filepath.Join(dir, "archive"), format.JSON(noteCodec()))
	created, err := client.Mkdir(ctx, archiveDir.WithMkdir(file.Mkdir{}), nil)
	if err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	fmt.Printf("mkdir archive: created=%v\n", created)

	// 12. BatchDelete — per-key results, partial failure allowed.
	results, err := client.BatchDelete(ctx, notes.WithBatchDelete(file.BatchDelete{}), []map[string]string{
		bob, dave, {"id": "never-existed"},
	})
	if err != nil {
		log.Fatalf("batch delete: %v", err)
	}
	for _, r := range results {
		fmt.Printf("batch delete %s: existed=%v err=%v\n", r.Path, r.Existed, r.Err)
	}

	// 13. Delete — idempotent single-key delete.
	existed, err := client.Delete(ctx, notes.WithDelete(file.Delete{}), alice)
	if err != nil {
		log.Fatalf("delete alice: %v", err)
	}
	fmt.Printf("deleted alice: existed=%v\n", existed)
}
