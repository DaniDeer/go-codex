package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoDeclareAttach shows:
//   - [file.NewClient]/[file.Client.Attach] — EXCLUSIVE attach-time
//     backend wiring; a 2nd call returns [file.BackendAlreadyAttachedError]
//   - [file.PathParam.WithCodec] — an invalid path variable is rejected
//     BEFORE the path is even built, let alone any Backend call
func demoDeclareAttach(ctx context.Context, client *file.Client, rt file.Router) {
	// A 2nd Attach is rejected — exclusive, mirrors events.Client.Attach.
	if err := client.Attach(adapterfile.NewBackend()); err != nil {
		var already file.BackendAlreadyAttachedError
		if errors.As(err, &already) {
			fmt.Println("confirmed: 2nd Attach rejected as expected")
		} else {
			log.Fatalf("unexpected error on 2nd Attach: %v", err)
		}
	}

	// A path variable failing its PathParam.WithCodec constraint never
	// reaches the backend at all — rejected at path-build time.
	createHandle, err := resources.Notes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(create): %v", err)
	}
	_, err = client.Create(ctx, createHandle, resources.Note{Title: "x", Body: "y"}, map[string]string{"id": "Not Valid!"})
	var pathParamErr file.FilePathParamError
	if errors.As(err, &pathParamErr) {
		fmt.Println("confirmed: invalid id (\"Not Valid!\") rejected by PathParam.WithCodec(validate.Slug)")
	} else {
		log.Fatalf("expected FilePathParamError for invalid id, got: %v", err)
	}
}
