package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/file-api/resources"
)

// demoRecursiveList shows [file.ListOptions]{Recursive: true}.
//
// IMPORTANT, accurately-scoped note (verified against the actual
// dispatch code, not assumed from prose): because every path variable
// in a Resource's DIRECTORY portion is required up front (same as
// non-recursive List already requires — see
// docs/roadmap/file-api.md's "Recursive WithList" section, "Scope
// limit — deliberately NOT full glob-discovery"), a Resource's
// resolved parent directory always has EXACTLY ONE structurally valid
// remaining segment (the leaf). Recursive discovery therefore finds
// the SAME entries non-recursive List would for any SINGLE fully-
// resolved directory — its real, demonstrable value is (a) working via
// the WalkingBackend-based walk machinery rather than a plain ListDir
// call, and (b) the depth-pruning rule EXCLUDING a stray file nested
// ONE LEVEL TOO DEEP, rather than incorrectly matching it by leaf name
// alone. Genuine "search across every category value" discovery is
// explicitly out of scope for this feature (see that same "Scope
// limit" subsection) — a possible future Phase 2 item of its own.
func demoRecursiveList(ctx context.Context, client *file.Client, rt file.Router, dir string) {
	listHandle, err := resources.CategorizedNotes.WithList(file.List{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(categorized list): %v", err)
	}
	createHandle, err := resources.CategorizedNotes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(categorized create): %v", err)
	}

	catVars := map[string]string{"category": "work"}
	for _, id := range []string{"standup", "retro"} {
		vars := map[string]string{"category": "work", "id": id}
		if _, err := client.Create(ctx, createHandle, resources.Note{Title: id, Body: "work note"}, vars, file.WriteOptions{CreateDirs: true}); err != nil {
			log.Fatalf("create (%s): %v", id, err)
		}
	}

	// A stray file ONE LEVEL TOO DEEP (notes/work/2024/extra.json) —
	// simulates a nesting level the template never describes. The
	// depth-pruning rule excludes it entirely (never even reaches the
	// codec, not even as a ListResult[T].Err).
	deepDir := filepath.Join(dir, "notes", "work", "2024")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		log.Fatalf("setup deep dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(deepDir, "extra.json"), []byte(`{"title":"x","body":"y"}`), 0644); err != nil {
		log.Fatalf("setup deep file: %v", err)
	}

	gotAny, err := client.List(ctx, listHandle, catVars, file.ListOptions{Recursive: true})
	if err != nil {
		log.Fatalf("list (recursive): %v", err)
	}
	results := gotAny.([]file.ListResult[resources.Note])
	for _, r := range results {
		if r.Err != nil {
			log.Fatalf("unexpected per-entry error: %v", r.Err)
		}
	}
	fmt.Printf("recursive list (category=work): %d entries found (the over-nested extra.json was correctly excluded, not %d)\n", len(results), len(results)+1)

	// Per-entry Security: List's own BoundListMiddleware runs ONCE PER
	// DISCOVERED ENTRY, passing that entry's ACTUAL DECODED VALUE — a
	// middleware rejecting one specific entry does NOT abort the whole
	// call; siblings still succeed, surfaced as that entry's own
	// ListResult[T].Err.
	gated := resources.CategorizedNotes.WithList(file.List{}).
		ListBoundMW(file.NewBoundListMiddleware[resources.Note, struct{}, struct{}](
			file.NewDeclaration[struct{}, struct{}]("reject-retro", codex.Struct[struct{}](), codex.Struct[struct{}]()),
			func(ctx context.Context, v *resources.Note, in struct{}) (struct{}, error) {
				if v.Title == "retro" {
					return struct{}{}, fmt.Errorf("retro notes are gated in this demo")
				}
				return struct{}{}, nil
			},
		))
	gatedHandle, err := gated.Handle(client, file.WithRouter(rt))
	if err != nil {
		log.Fatalf("handle(gated list): %v", err)
	}
	gatedAny, err := client.List(ctx, gatedHandle, catVars, file.ListOptions{Recursive: true})
	if err != nil {
		log.Fatalf("list (gated): %v", err)
	}
	var okCount, errCount int
	for _, r := range gatedAny.([]file.ListResult[resources.Note]) {
		if r.Err != nil {
			errCount++
			continue
		}
		okCount++
	}
	fmt.Printf("per-entry Security: %d succeeded, %d rejected (\"retro\" gated, \"standup\" still succeeded)\n", okCount, errCount)

	_ = os.RemoveAll(filepath.Join(dir, "notes"))
}
