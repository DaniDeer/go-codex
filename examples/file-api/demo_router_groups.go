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

// namedMiddleware builds a NAME-ONLY, Security-less, general-purpose
// [file.Middleware] — used below purely to demonstrate [file.Router]'s
// own middleware-NAME bookkeeping (`.Use()`'s attachment-order tracking,
// surfaced via `Routes()`'s MiddlewareNames), independent of any real
// behavior.
func namedMiddleware(name string) file.Middleware[struct{}, struct{}] {
	return file.NewMiddleware(file.NewDeclaration[struct{}, struct{}](name, codex.Struct[struct{}](), codex.Struct[struct{}]()))
}

// demoRouterGroups shows a RICHER [file.Router] composition than the
// single-flat-prefix one every other demo uses:
//   - the Route+Register workflow: a BASE Router carrying a reusable,
//     permanent middleware (`.Use()`), with TWO fully-built sub-Routers
//     (their own prefix + routes attached BEFORE being passed to
//     [Router.Mount] — Mount's own doc comment: it returns the PARENT,
//     updated, NOT a view into sub's own namespace, so routes must be
//     attached to the sub value directly) nested under it via
//     [Router.Mount] — [Router.Routes]'s own declaration-ordered
//     inventory lists every leaf's FINAL prefixed path + composed
//     middleware names, and [Router.Register] runs the fail-fast
//     Capability-coverage check on each.
//   - the build-the-Handle-directly workflow ([file.WithRouter]) is a
//     SEPARATE, FLAT mechanism — it reads a Router's OWN top-level
//     accumulated prefix/middleware only, never walking Mounted
//     children — so 2 real dispatches into 2 DIFFERENT directories use
//     2 independently-built flat Routers with the FULL composed prefix
//     each, not a shared Mount tree.
func demoRouterGroups(ctx context.Context, client *file.Client, dir string) {
	subA := file.NewRouter("groupA").Use(namedMiddleware("groupA-only")).
		Route(resources.Notes.WithCreate(file.Create{})).
		Route(resources.Notes.WithRead(file.Read{}))
	subB := file.NewRouter("groupB").
		Route(resources.Notes.WithCreate(file.Create{})).
		Route(resources.Notes.WithRead(file.Read{}))

	base := file.NewRouter(dir).Use(namedMiddleware("audit-log")).
		Mount(subA).
		Mount(subB)

	// Routes() is walkable WITHOUT a Client — a pure declaration-time
	// inventory. base's own "audit-log" middleware composes BEFORE each
	// sub-router's own (outer-to-inner, outermost-declared-first).
	for _, e := range base.Routes() {
		fmt.Printf("router entry: %s %s middleware=%v\n", e.Role, e.Path, e.MiddlewareNames)
	}

	if err := base.Register(client); err != nil {
		log.Fatalf("base.Register: %v", err)
	}

	// 2 independently-built FLAT routers — the SAME final composed
	// prefix base.Routes() just reported above, built the
	// build-the-Handle-directly way.
	rtA := file.NewRouter(filepath.Join(dir, "groupA"))
	rtB := file.NewRouter(filepath.Join(dir, "groupB"))
	createA, err := resources.Notes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rtA))
	if err != nil {
		log.Fatalf("handle(createA): %v", err)
	}
	createB, err := resources.Notes.WithCreate(file.Create{}).Handle(client, file.WithRouter(rtB))
	if err != nil {
		log.Fatalf("handle(createB): %v", err)
	}

	// CreateDirs is required — groupA/groupB don't exist on disk yet
	// (mirrors os.WriteFile's own behavior: it never creates parent
	// directories unless explicitly asked to).
	vars := map[string]string{"id": "grouped"}
	writeOpts := file.WriteOptions{CreateDirs: true}
	if _, err := client.Create(ctx, createA, resources.Note{Title: "A", Body: "in groupA"}, vars, writeOpts); err != nil {
		log.Fatalf("create (groupA): %v", err)
	}
	if _, err := client.Create(ctx, createB, resources.Note{Title: "B", Body: "in groupB"}, vars, writeOpts); err != nil {
		log.Fatalf("create (groupB): %v", err)
	}

	// Prove the 2 notes landed in 2 DIFFERENT directories, even though
	// both were declared from the EXACT SAME resources.Notes value and
	// the SAME vars map.
	if _, err := os.Stat(filepath.Join(dir, "groupA", "grouped.json")); err != nil {
		log.Fatalf("expected groupA/grouped.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "groupB", "grouped.json")); err != nil {
		log.Fatalf("expected groupB/grouped.json: %v", err)
	}
	fmt.Println("confirmed: groupA and groupB notes landed in 2 DIFFERENT directories, same Resource/vars")

	// Clean up (so later demos' List calls over dir's own top level
	// aren't affected by groupA/groupB's own subdirectories).
	_ = os.RemoveAll(filepath.Join(dir, "groupA"))
	_ = os.RemoveAll(filepath.Join(dir, "groupB"))
}
