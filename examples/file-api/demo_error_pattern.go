package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// NoteNotFoundResponse is the declared, codec-backed response payload
// for a Read against a missing note — mirrors api/rest/api/events/
// api/reqreply's own ErrorPattern precedent exactly.
type NoteNotFoundResponse struct {
	Code    string
	Message string
}

// demoErrorPattern shows [file.ErrorPattern] — AUTO-INVOKED by dispatch
// (not something the caller has to remember to check): a declared
// mapped pattern turns [file.FileNotFoundError] into a stable,
// application-level NoteNotFoundResponse, while errors.As still reaches
// the ORIGINAL error via Unwrap.
func demoErrorPattern(ctx context.Context, client *file.Client, rt file.Router) {
	pattern := file.NewMappedErrorPattern[file.FileNotFoundError, NoteNotFoundResponse](
		func(e file.FileNotFoundError) (NoteNotFoundResponse, error) {
			return NoteNotFoundResponse{Code: "NOTE_NOT_FOUND", Message: "no note at " + e.Path}, nil
		},
	)
	patterns := file.Append(file.ErrorPatterns[NoteNotFoundResponse]{}, pattern)

	reader := resources.Notes.WithRead(file.Read{}).ErrorPatterns(patterns)
	readHandle, err := reader.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(error-pattern read): %v", err)
	}

	_, err = client.Read(ctx, readHandle, map[string]string{"id": "ghost"})
	var matched file.ErrorPatternMatchedError
	if !errors.As(err, &matched) {
		log.Fatalf("expected ErrorPatternMatchedError, got: %v", err)
	}
	resp := matched.Payload.(NoteNotFoundResponse)
	fmt.Printf("ErrorPattern matched: %+v\n", resp)

	// errors.As still reaches the ORIGINAL error via Unwrap — additive,
	// not a replacement.
	var notFound file.FileNotFoundError
	if errors.As(err, &notFound) {
		fmt.Println("confirmed: errors.As still reaches the original FileNotFoundError via Unwrap")
	} else {
		log.Fatalf("expected the original FileNotFoundError to still be reachable via errors.As")
	}
}
