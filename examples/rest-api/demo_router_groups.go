package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── fixtures for demoRouterGroups — self-contained, no dependency on the
// routes/handlers/chiserver/nethttpserver/client sub-packages the rest of
// this project uses, since Router's own value is in PATH/MIDDLEWARE
// ASSEMBLY, independent of any live transport. ───────────────────────────

type routerDemoItem struct {
	ID   string
	Name string
}

var routerDemoItemCodec = codex.Struct[routerDemoItem](
	codex.RequiredField("id", codex.String(),
		func(i routerDemoItem) string { return i.ID },
		func(i *routerDemoItem, v string) { i.ID = v },
	),
	codex.RequiredField("name", codex.String(),
		func(i routerDemoItem) string { return i.Name },
		func(i *routerDemoItem, v string) { i.Name = v },
	),
)

type routerDemoEmpty struct{}

var routerDemoEmptyCodec = codex.Struct[routerDemoEmpty]()

func routerDemoListItems(_ context.Context, _ routerDemoEmpty) ([]routerDemoItem, error) {
	return []routerDemoItem{{ID: "1", Name: "widget"}}, nil
}

func routerDemoListItemsCodec() codex.Codec[[]routerDemoItem] {
	return codex.SliceOf(routerDemoItemCodec)
}

func routerDemoCreateItem(_ context.Context, item routerDemoItem) (routerDemoItem, error) {
	return item, nil
}

func routerDemoDeleteItem(_ context.Context, _ routerDemoEmpty) (routerDemoEmpty, error) {
	return routerDemoEmpty{}, nil
}

// demoRouterGroups shows docs/roadmap/declarative-router-groups.md's
// Phase A (api/rest) end to end:
//   - routes declared completely independently (exactly as every OTHER
//     demo in this project declares them) — Router never changes the
//     codec-declaration step itself;
//   - a nested Router assembles a prefix ("/api/v1") + a sub-group
//     ("/items") with group-wide Security middleware attached via ONE
//     `.Use(...)` call, instead of repeating it per-route;
//   - `Routes()` prints the FINAL, holistic, pre-registration view of how
//     the whole API assembles — every path, method, and the middleware
//     that will dispatch for it — before a single byte is registered.
func demoRouterGroups() {
	fmt.Println("=== Router groups: declare independently, assemble with ONE Router ===")

	// Each route declares only its OWN, relative path — "/items" (the
	// group's own prefix, contributed once below) is NOT repeated here;
	// this is exactly the "declared independently, prefix assembled
	// later by the Router" workflow the whole feature exists for.
	listRoute := rest.NewRoute[routerDemoEmpty, []routerDemoItem]("GET", "",
		routerDemoEmptyCodec, routerDemoListItemsCodec(),
		rest.RouteMeta{OperationID: "listItems", Summary: "List items"},
	).WithHandler(routerDemoListItems)

	createRoute := rest.NewRoute[routerDemoItem, routerDemoItem]("POST", "",
		routerDemoItemCodec, routerDemoItemCodec,
		rest.RouteMeta{OperationID: "createItem", Summary: "Create an item"},
	).WithHandler(routerDemoCreateItem)

	deleteRoute := rest.NewRoute[routerDemoEmpty, routerDemoEmpty]("DELETE", "/{id}",
		routerDemoEmptyCodec, routerDemoEmptyCodec,
		rest.RouteMeta{OperationID: "deleteItem", Summary: "Delete an item"},
		rest.PathParam{Name: "id"},
	).WithHandler(routerDemoDeleteItem)

	// A reusable-class Security middleware, attached ONCE at the Router
	// level — every leaf grouped under it (list/create/delete) gets it,
	// with ZERO repetition across 3 separate .Use() calls.
	authMiddleware := middleware.Middleware{
		Name: "bearer-auth",
		Security: &middleware.SecurityDeclaration{
			SchemeName: "bearerAuth",
			Scheme:     route.BearerScheme("JWT"),
			Scopes:     []string{"items:write"},
		},
	}

	itemsRouter := rest.NewRouter("/items").
		Use(authMiddleware).
		Route(listRoute).
		Route(createRoute).
		Route(deleteRoute)

	apiRouter := rest.NewRouter("/api/v1").Mount(itemsRouter)

	fmt.Println("-- Routes() — holistic, pre-registration view --")
	for _, e := range apiRouter.Routes() {
		fmt.Printf("  %-7s %-24s middleware=%v\n", e.Method, e.Path, e.MiddlewareNames)
	}

	server := rest.NewServer(rest.Info{Title: "Router Groups Demo", Version: "1.0.0"})
	if err := apiRouter.Register(server); err != nil {
		fmt.Printf("  register error: %v\n", err)
		return
	}
	fmt.Println("-- Registered successfully; every leaf above is now live on server --")
	fmt.Println()
}

// demoRouterWithScoping shows [rest.Router.With]'s one-shot middleware
// scoping in both its CORRECT use (scoped to exactly one route within a
// group) and its documented BOUNDARY (discarded, not leaked, across a
// [rest.Router.Mount]/[rest.Router.Group] call) — see this project's own
// Round 157 review (.github/skills/review-go-codex/references/history.md),
// which fixed a real bug where `.With(mw).Mount(sub).Route(leaf)` used to
// silently leak mw onto the UNRELATED leaf instead of discarding it.
func demoRouterWithScoping() {
	fmt.Println("=== Router.With(): one-shot middleware, scoped to ONE route — and its Mount boundary ===")

	strictValidation := middleware.Middleware{Name: "strict-validation"}
	sharedAuth := middleware.Middleware{Name: "bearer-auth"}

	// Part 1 — the CORRECT, intended use: a group-wide, PERMANENT
	// .Use(sharedAuth) applies to every leaf, while .With(strictValidation)
	// applies ONLY to the create route (the very next .Route() call) —
	// the list route right after it does NOT receive it.
	listRoute := rest.NewRoute[routerDemoEmpty, []routerDemoItem]("GET", "/with-demo",
		routerDemoEmptyCodec, routerDemoListItemsCodec(),
		rest.RouteMeta{OperationID: "listItemsWithDemo", Summary: "List items (With demo)"},
	).WithHandler(routerDemoListItems)

	createRoute := rest.NewRoute[routerDemoItem, routerDemoItem]("POST", "/with-demo",
		routerDemoItemCodec, routerDemoItemCodec,
		rest.RouteMeta{OperationID: "createItemWithDemo", Summary: "Create an item (With demo)"},
	).WithHandler(routerDemoCreateItem)

	scopedRouter := rest.NewRouter("/items").
		Use(sharedAuth).
		With(strictValidation).Route(createRoute).
		Route(listRoute)

	fmt.Println("-- one-shot .With(strictValidation) applies ONLY to the create route, not list --")
	for _, e := range scopedRouter.Routes() {
		fmt.Printf("  %-7s %-24s middleware=%v\n", e.Method, e.Path, e.MiddlewareNames)
	}

	// Part 2 — the Mount boundary: .With() pairs ONLY with the immediately
	// next .Route() call, never a .Mount()/.Group(). A `.With(mw).
	// Mount(sub)` call DISCARDS mw at the Mount — it is silently dropped,
	// never leaked onto some later, unrelated .Route() call either. The
	// CORRECT way to scope middleware to an entire sub-router is .Use()
	// ON the sub-router itself, BEFORE mounting it (see demoRouterGroups's
	// itemsRouter above).
	//
	// Fresh, isolated fixtures below (NOT reusing scopedRouter from Part 1,
	// which already legitimately carries strictValidation on its create
	// route) — keeping this check unambiguous: NEITHER leaf here has ever
	// been associated with oneShotForMount by any OTHER, correct path.
	oneShotForMount := middleware.Middleware{Name: "rate-limit-strict"}

	archiveSub := rest.NewRouter("/archive").
		Route(rest.NewRoute[routerDemoEmpty, routerDemoEmpty]("GET", "/status",
			routerDemoEmptyCodec, routerDemoEmptyCodec,
			rest.RouteMeta{OperationID: "archiveStatusWithDemo", Summary: "Archive status (With demo)"},
		).WithHandler(routerDemoDeleteItem))

	pingRoute := rest.NewRoute[routerDemoEmpty, routerDemoEmpty]("GET", "/ping",
		routerDemoEmptyCodec, routerDemoEmptyCodec,
		rest.RouteMeta{OperationID: "pingWithDemo", Summary: "Health check (With demo)"},
	).WithHandler(routerDemoDeleteItem)

	misplacedRouter := rest.NewRouter("/api/v2").
		With(oneShotForMount). // (incorrectly) intended for archiveSub below
		Mount(archiveSub).
		Route(pingRoute) // an UNRELATED route declared after the Mount

	fmt.Println("-- .With() before .Mount() is DISCARDED: absent from the mounted sub's leaves AND from the later, unrelated route --")
	for _, e := range misplacedRouter.Routes() {
		hasOneShot := false
		for _, n := range e.MiddlewareNames {
			if n == "rate-limit-strict" {
				hasOneShot = true
			}
		}
		fmt.Printf("  %-7s %-28s middleware=%v  (leaked rate-limit-strict=%v)\n", e.Method, e.Path, e.MiddlewareNames, hasOneShot)
	}
	fmt.Println()
}
