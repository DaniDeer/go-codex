// Package main demonstrates [file.Client], go-codex's new transport-agnostic
// file-resource API (Phase 1 of docs/roadmap/file-api.md), including its
// full Router + Declarative Middleware parity with api/rest/api/events.
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
//   - [file.Router]/[file.WithRouter] — a genuine Router value type
//     (mirrors events.Router) composes the resource's runtime directory
//     prefix ONCE, at Handle-build time — no manual filepath.Join anywhere
//     in this file
//   - [file.Reader.ReadBoundMW]/[file.BoundSecurityReadMiddleware] — REAL,
//     dispatch-time-executed Declarative Middleware: a Security gate that
//     rejects BEFORE any Backend call, proven live against a genuine
//     Note value returned as a properly-typed Go struct (not a raw `any`)
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

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
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

// grantedScopes is a BoundReadMiddleware Out type — the conventionally-
// named GrantedScopes field (map[string][]string), read via reflection
// by api/file's dispatch core, exactly mirroring api/rest/api/events'
// own Security-attachment convention.
type grantedScopes struct {
	GrantedScopes map[string][]string
}

func main() {
	dir, err := os.MkdirTemp("", "file-api-example-*")
	if err != nil {
		log.Fatalf("setup: %v", err)
	}
	defer os.RemoveAll(dir)

	// 1. Declare the resource: WHAT a note looks like (path template +
	// codec). The template is RELATIVE ("{id}.json", no directory baked
	// in) — the runtime temp directory is supplied entirely via the
	// Router below, step 2.
	//
	// file.PathParam.WithCodec validates every {id} path variable against
	// a codex.Codec[string] BEFORE the path is built — mirrors
	// rest.PathParam.WithCodec/events.TopicParam.WithCodec exactly. Here,
	// every id must be a URL-friendly slug (lowercase alphanumeric +
	// hyphens); an invalid id never reaches the backend at all.
	notes := file.NewResource[Note]("{id}.json", format.JSON(noteCodec()),
		file.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.Slug)),
	)

	// 2. Declare a Router whose prefix IS the runtime temp directory —
	// replaces manual filepath.Join entirely. A real application would
	// use this exact mechanism for a multi-tenant path prefix
	// ("tenants/{tenant}") or any other runtime-determined directory.
	rt := file.NewRouter(dir)

	// 3. Attach a backend: WHERE notes actually live. Swapping this one
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

	// 4. Build each role's Handle ONCE, through the Router — every
	// subsequent Client call below reuses these SAME Handles, so the
	// temp-dir prefix is resolved at exactly one construction point (the
	// dual-accept design's whole purpose: a caller can never forget to
	// re-apply Router composition at a later call site, because there is
	// no later construction site at all).
	readHandle, err := notes.WithRead(file.Read{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(read): %v", err)
	}
	createHandle, err := notes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(create): %v", err)
	}
	listHandle, err := notes.WithList(file.List{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(list): %v", err)
	}
	existsHandle, err := notes.WithExists(file.Exists{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(exists): %v", err)
	}
	copyHandle, err := notes.WithCopy(file.Copy{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(copy): %v", err)
	}
	moveHandle, err := notes.WithMove(file.Move{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(move): %v", err)
	}
	batchDeleteHandle, err := notes.WithBatchDelete(file.BatchDelete{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(batchdelete): %v", err)
	}
	deleteHandle, err := notes.WithDelete(file.Delete{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(delete): %v", err)
	}

	// 5. Create.
	alice := map[string]string{"id": "alice"}
	bob := map[string]string{"id": "bob"}
	if _, err := client.Create(ctx, createHandle, Note{Title: "Alice's note", Body: "Hello"}, alice); err != nil {
		log.Fatalf("create alice: %v", err)
	}
	if _, err := client.Create(ctx, createHandle, Note{Title: "Bob's note", Body: "Hi"}, bob); err != nil {
		log.Fatalf("create bob: %v", err)
	}
	fmt.Println("created 2 notes")

	// 6. Read — returns a properly-typed Note, not just a raw `any`: the
	// dual-accept Client.Read always returns `any` by design (mirrors
	// reqreply.Client.Call — Go generics can't add a type param to a
	// method), but asserting it ONCE into a named, typed variable right
	// after the call is the idiomatic way to work with it from here on.
	aliceAny, err := client.Read(ctx, readHandle, alice)
	if err != nil {
		log.Fatalf("read alice: %v", err)
	}
	aliceNote := aliceAny.(Note)
	fmt.Printf("read: %+v\n", aliceNote)

	// 7. Read a missing note — typed FileNotFoundError.
	_, err = client.Read(ctx, readHandle, map[string]string{"id": "ghost"})
	var notFound file.FileNotFoundError
	if errors.As(err, &notFound) {
		fmt.Println("confirmed: missing note reports FileNotFoundError")
	}

	// A path variable failing its PathParam.WithCodec constraint never
	// reaches the backend at all — rejected at path-build time.
	_, err = client.Create(ctx, createHandle, Note{Title: "x", Body: "y"}, map[string]string{"id": "Not Valid!"})
	var pathParamErr file.FilePathParamError
	if errors.As(err, &pathParamErr) {
		fmt.Println("confirmed: invalid id (\"Not Valid!\") rejected by PathParam.WithCodec(validate.Slug)")
	} else {
		log.Fatalf("expected FilePathParamError for invalid id, got: %v", err)
	}

	// 8. Declarative Middleware: a Security-gated Read. Attached to a
	// DIFFERENT Reader value (via ReadBoundMW before .Handle), so the
	// plain readHandle above stays ungated — proves the gate is additive,
	// not global. The bound Fn's Out carries GrantedScopes; api/file's
	// dispatch core extracts it via reflection and checks it against the
	// declared Security requirement BEFORE calling the Backend at all.
	securedReader := notes.WithRead(file.Read{}).
		ReadBoundMW(file.BoundSecurityReadMiddleware[Note, struct{}, grantedScopes](
			"apiKey", file.SecurityScheme{}, []string{"read"},
			func(ctx context.Context, v *Note, in struct{}) (grantedScopes, error) {
				// A real implementation would validate a credential here
				// and grant scopes accordingly; this demo always grants
				// "read", so the gate succeeds.
				return grantedScopes{GrantedScopes: map[string][]string{"apiKey": {"read"}}}, nil
			},
		)).
		WithSecurity(file.SecurityRequirement{"apiKey": {"read"}})
	securedHandle, err := securedReader.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(secured read): %v", err)
	}
	securedAny, err := client.Read(ctx, securedHandle, alice)
	if err != nil {
		log.Fatalf("secured read: %v", err)
	}
	fmt.Printf("secured read (Security satisfied): %+v\n", securedAny.(Note))

	// 9. List — enumerates the resource's parent directory.
	listAny, err := client.List(ctx, listHandle, nil)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	fmt.Printf("list: %d notes found\n", len(listAny.([]Note)))

	// 10. Exists.
	_, exists, err := client.Exists(ctx, existsHandle, alice)
	if err != nil {
		log.Fatalf("exists: %v", err)
	}
	fmt.Printf("exists(alice) = %v\n", exists)

	// 11. Patch — partial update via a 2nd, smaller codec.
	type bodyPatch struct{ Body *string }
	patchCodec := codex.PartialStruct[bodyPatch](
		codex.PartialField("body", codex.String(),
			func(p bodyPatch) *string { return p.Body },
			func(p *bodyPatch, v *string) { p.Body = v }),
	)
	newBody := "Updated hello"
	patchHandle, err := file.WithPatch(notes, patchCodec, file.Patch{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(patch): %v", err)
	}
	if err := client.Patch(ctx, patchHandle, bodyPatch{Body: &newBody}, alice); err != nil {
		log.Fatalf("patch: %v", err)
	}
	patchedAny, _ := client.Read(ctx, readHandle, alice)
	fmt.Printf("after patch: %+v\n", patchedAny.(Note))

	// 12. Copy — server-side copy, source untouched.
	carol := map[string]string{"id": "carol"}
	if err := client.Copy(ctx, copyHandle, alice, carol); err != nil {
		log.Fatalf("copy: %v", err)
	}
	fmt.Println("copied alice -> carol")

	// 13. Move — atomic rename on the filesystem backend.
	dave := map[string]string{"id": "dave"}
	if err := client.Move(ctx, moveHandle, carol, dave); err != nil {
		log.Fatalf("move: %v", err)
	}
	fmt.Println("moved carol -> dave")

	// 14. Mkdir — first-class directory creation, not just an implicit
	// WriteOptions.CreateDirs side effect. The declared resource's path
	// template IS the directory to create (no {var} placeholder needed
	// here, unlike notes' per-item template) — "archive" is relative,
	// resolved against the SAME Router prefix.
	archiveDir := file.NewResource[Note]("archive", format.JSON(noteCodec()))
	archiveHandle, err := archiveDir.WithMkdir(file.Mkdir{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(mkdir): %v", err)
	}
	created, err := client.Mkdir(ctx, archiveHandle, nil)
	if err != nil {
		log.Fatalf("mkdir: %v", err)
	}
	fmt.Printf("mkdir archive: created=%v\n", created)

	// 15. BatchDelete — per-key results, partial failure allowed.
	results, err := client.BatchDelete(ctx, batchDeleteHandle, []map[string]string{
		bob, dave, {"id": "never-existed"},
	})
	if err != nil {
		log.Fatalf("batch delete: %v", err)
	}
	for _, r := range results {
		fmt.Printf("batch delete %s: existed=%v err=%v\n", r.Path, r.Existed, r.Err)
	}

	// 16. Delete — idempotent single-key delete.
	existed, err := client.Delete(ctx, deleteHandle, alice)
	if err != nil {
		log.Fatalf("delete alice: %v", err)
	}
	fmt.Printf("deleted alice: existed=%v\n", existed)
}
