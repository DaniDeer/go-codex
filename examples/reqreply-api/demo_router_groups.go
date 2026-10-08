package main

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── fixtures for demoRouterGroups — self-contained, no dependency on the
// routes/handlers/mqtt5server/zeromqserver/client sub-packages the rest of
// this project uses, since Router's own value is in TOPIC/MIDDLEWARE
// ASSEMBLY, independent of any live transport. ────────────────────────────

type routerDemoAddReq struct{ X, Y int }

var routerDemoAddReqCodec = codex.Struct[routerDemoAddReq](
	codex.RequiredField("x", codex.Int(),
		func(r routerDemoAddReq) int { return r.X },
		func(r *routerDemoAddReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r routerDemoAddReq) int { return r.Y },
		func(r *routerDemoAddReq, v int) { r.Y = v },
	),
)

type routerDemoResult struct{ Result int }

var routerDemoResultCodec = codex.Struct[routerDemoResult](
	codex.RequiredField("result", codex.Int(),
		func(r routerDemoResult) int { return r.Result },
		func(r *routerDemoResult, v int) { r.Result = v },
	),
)

func routerDemoAdd(_ context.Context, req routerDemoAddReq) (routerDemoResult, error) {
	return routerDemoResult{Result: req.X + req.Y}, nil
}

func routerDemoSubtract(_ context.Context, req routerDemoAddReq) (routerDemoResult, error) {
	return routerDemoResult{Result: req.X - req.Y}, nil
}

// demoRouterGroups shows docs/roadmap/declarative-router-groups.md's
// Phase C (api/reqreply) end to end:
//   - routes declared completely independently (exactly as every OTHER
//     demo in this project declares them) — Router never changes the
//     codec-declaration step itself;
//   - a nested Router assembles a topic prefix ("compute/v1") with
//     group-wide middleware attached via ONE `.Use(...)` call, instead of
//     repeating it per-route;
//   - `Routes()` prints the FINAL, holistic, pre-registration view of how
//     the whole API assembles — every topic and the middleware that will
//     dispatch for it — before a single byte is registered;
//   - unlike api/rest's RouterEntry (which carries a Method) or
//     api/events' (which carries a Role), reqreply's RouterEntry has
//     NEITHER — a single Route[Req,Resp] is already the complete leaf.
func demoRouterGroups() {
	fmt.Println("=== Router groups: declare independently, assemble with ONE Router ===")

	// Each route declares only its OWN, relative topic — "v1" (the
	// group's own prefix, contributed once below) is NOT repeated here;
	// this is exactly the "declared independently, prefix assembled
	// later by the Router" workflow the whole feature exists for.
	addRoute := reqreply.NewRoute[routerDemoAddReq, routerDemoResult]("add",
		routerDemoAddReqCodec, routerDemoResultCodec,
	).WithHandler(routerDemoAdd)

	subtractRoute := reqreply.NewRoute[routerDemoAddReq, routerDemoResult]("subtract",
		routerDemoAddReqCodec, routerDemoResultCodec,
	).WithHandler(routerDemoSubtract)

	// A reusable-class, spec-only middleware, attached ONCE at the
	// Router level — every leaf grouped under it (add/subtract) gets it,
	// with ZERO repetition across 2 separate .Use() calls.
	auditMiddleware := middleware.Middleware{Name: "audit-log"}

	// A single, flat Router — not nested via Mount — so the SAME Router
	// value can also drive WithRouter below. [reqreply.WithRouter] applies
	// a Router's OWN prefix field only (not any ancestor's); for a
	// multi-level Mount hierarchy the innermost Router's own prefix would
	// need to already be the FULL composed path for WithRouter to match
	// what Register produces — easiest achieved, as here, by keeping the
	// Router flat when a client-side handle is also needed.
	computeRouter := reqreply.NewRouter("compute/v1").
		Use(auditMiddleware).
		Route(addRoute).
		Route(subtractRoute)

	fmt.Println("-- Routes() — holistic, pre-registration view --")
	for _, e := range computeRouter.Routes() {
		fmt.Printf("  %-18s middleware=%v\n", e.Path, e.MiddlewareNames)
	}

	server := reqreply.NewServer(reqreply.Info{Title: "Router Groups Demo", Version: "1.0.0"})
	if err := computeRouter.Register(server); err != nil {
		fmt.Printf("  register error: %v\n", err)
		return
	}
	fmt.Println("-- Registered successfully; every leaf above is now live on server --")

	// Client-side: avoiding a silent path mismatch. ClientHandle has no
	// way to know a Router grouped the SAME route under a prefix
	// elsewhere — WithRouter applies the SAME composition Register did.
	addHandle := addRoute.ClientHandle(reqreply.WithRouter(computeRouter))
	fmt.Printf("-- ClientHandle(WithRouter(...)).Topic = %q (matches the registered topic) --\n", addHandle.Topic)
	fmt.Println()
}

// demoRouterWithScoping shows [reqreply.Router.With]'s one-shot
// middleware scoping in both its CORRECT use (scoped to exactly one
// route within a group) and its documented BOUNDARY (discarded, not
// leaked, across a [reqreply.Router.Mount]/[reqreply.Router.Group]
// call) — see this project's own Round 157 review
// (.github/skills/review-go-codex/references/history.md), which fixed a
// real bug where `.With(mw).Mount(sub).Route(leaf)` used to silently
// leak mw onto the UNRELATED leaf instead of discarding it.
func demoRouterWithScoping() {
	fmt.Println("=== Router.With(): one-shot middleware, scoped to ONE route — and its Mount boundary ===")

	sharedAudit := middleware.Middleware{Name: "audit-log"}
	oneShotTrace := middleware.Middleware{Name: "trace-sample"}

	// Part 1 — the CORRECT, intended use: a group-wide, PERMANENT
	// .Use(sharedAudit) applies to every leaf, while .With(oneShotTrace)
	// applies ONLY to the add route (the very next .Route() call) — the
	// subtract route right after it does NOT receive it.
	addRouteV2 := reqreply.NewRoute[routerDemoAddReq, routerDemoResult]("with-demo/add",
		routerDemoAddReqCodec, routerDemoResultCodec,
	).WithHandler(routerDemoAdd)

	subtractRouteV2 := reqreply.NewRoute[routerDemoAddReq, routerDemoResult]("with-demo/subtract",
		routerDemoAddReqCodec, routerDemoResultCodec,
	).WithHandler(routerDemoSubtract)

	scopedRouter := reqreply.NewRouter("compute/v2").
		Use(sharedAudit).
		With(oneShotTrace).Route(addRouteV2).
		Route(subtractRouteV2)

	fmt.Println("-- one-shot .With(oneShotTrace) applies ONLY to the add route, not subtract --")
	for _, e := range scopedRouter.Routes() {
		fmt.Printf("  %-24s middleware=%v\n", e.Path, e.MiddlewareNames)
	}

	// Part 2 — the Mount boundary: .With() pairs ONLY with the
	// immediately next .Route() call, never a .Mount()/.Group(). Fresh,
	// isolated fixtures below (NOT reusing scopedRouter from Part 1,
	// which already legitimately carries oneShotTrace on its add route)
	// — keeping this check unambiguous. The CORRECT way to scope
	// middleware to an entire sub-router is .Use() ON the sub-router
	// itself, BEFORE mounting it (see demoRouterGroups's computeRouter
	// above, had it been nested).
	oneShotForMount := middleware.Middleware{Name: "rate-limit-strict"}

	archiveSub := reqreply.NewRouter("archive").
		Route(reqreply.NewRoute[routerDemoAddReq, routerDemoResult]("with-demo/status",
			routerDemoAddReqCodec, routerDemoResultCodec,
		).WithHandler(routerDemoAdd))

	healthRoute := reqreply.NewRoute[routerDemoAddReq, routerDemoResult]("with-demo/health",
		routerDemoAddReqCodec, routerDemoResultCodec,
	).WithHandler(routerDemoAdd)

	misplacedRouter := reqreply.NewRouter("api/v2").
		With(oneShotForMount). // (incorrectly) intended for archiveSub below
		Mount(archiveSub).
		Route(healthRoute) // an UNRELATED route declared after the Mount

	fmt.Println("-- .With() before .Mount() is DISCARDED: absent from the mounted sub's leaves AND from the later, unrelated route --")
	for _, e := range misplacedRouter.Routes() {
		hasOneShot := false
		for _, n := range e.MiddlewareNames {
			if n == "rate-limit-strict" {
				hasOneShot = true
			}
		}
		fmt.Printf("  %-28s middleware=%v  (leaked rate-limit-strict=%v)\n", e.Path, e.MiddlewareNames, hasOneShot)
	}
	fmt.Println()
}
