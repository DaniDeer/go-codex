package main

import (
	"context"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

type ownerIn struct{ Owner string }
type ownerOut struct{ Owner string }

// demoMetadataAxis shows the file-metadata merge-field axis — api/file's
// analogue of REST headers/MQTT User Properties (path vars are already
// fully covered by PathParam): Owner lives in the Backend's own sidecar
// metadata file, NEVER in the JSON body itself.
//
//  1. A resource-level MergedMetadataParam persists/retrieves Owner
//     automatically on Create/Read.
//  2. A BoundCreateMiddleware using WithMetadataIn/WithMetadataOut can
//     read the CALLER-intended metadata (pre-write) and override it.
func demoMetadataAxis(ctx context.Context, client *file.Client, rt file.Router) {
	createHandle, err := resources.MetadataNotes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(metadata create): %v", err)
	}
	readHandle, err := resources.MetadataNotes.WithRead(file.Read{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(metadata read): %v", err)
	}

	vars := map[string]string{"id": "plain"}
	if _, err := client.Create(ctx, createHandle, resources.MetadataNote{Title: "plain", Body: "hi", Owner: "alice"}, vars, file.WriteOptions{CreateDirs: true}); err != nil {
		log.Fatalf("create (metadata): %v", err)
	}
	gotAny, err := client.Read(ctx, readHandle, vars)
	if err != nil {
		log.Fatalf("read (metadata): %v", err)
	}
	got := gotAny.(resources.MetadataNote)
	fmt.Printf("resource-level metadata round trip: Owner=%q (never touched the JSON body)\n", got.Owner)

	// BoundCreateMiddleware with WithMetadataOut overrides the
	// caller-supplied Owner before it's persisted.
	bm := file.NewBoundCreateMiddleware[resources.MetadataNote, ownerIn, ownerOut](
		file.NewDeclaration[ownerIn, ownerOut]("owner-override", codex.Struct[ownerIn](), codex.Struct[ownerOut]()),
		func(ctx context.Context, v *resources.MetadataNote, in ownerIn) (ownerOut, error) {
			return ownerOut{Owner: "overridden-by-middleware"}, nil
		},
	).WithMetadataOut(file.NewMetadataParam("owner", codex.String(),
		func(o ownerOut) string { return o.Owner },
		func(o *ownerOut, v string) { o.Owner = v }))

	gatedVars := map[string]string{"id": "gated"}
	gatedCreate := resources.MetadataNotes.WithCreate(file.Create{}).CreateBoundMW(bm)
	gatedHandle, err := gatedCreate.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(gated metadata create): %v", err)
	}
	if _, err := client.Create(ctx, gatedHandle, resources.MetadataNote{Title: "gated", Body: "hi", Owner: "original"}, gatedVars); err != nil {
		log.Fatalf("create (gated metadata): %v", err)
	}
	gotGatedAny, err := client.Read(ctx, readHandle, gatedVars)
	if err != nil {
		log.Fatalf("read (gated metadata): %v", err)
	}
	fmt.Printf("BoundCreateMiddleware.WithMetadataOut overrode Owner to %q\n", gotGatedAny.(resources.MetadataNote).Owner)
}
