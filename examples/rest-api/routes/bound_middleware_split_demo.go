package routes

import (
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ── Bound-middleware-split demo (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) ──
//
// This file is a DIRECT, side-by-side contrast of the two classes the
// split introduced, on top of the "bearerAuth" scope-check mechanism
// already shipped in auth/middleware.go:
//
//  1. REUSABLE ALONE (Class 1) — [requestid.ReusableRequestIDMw]
//     (examples/rest-api/requestid, a SELF-CONTAINED reusable-middleware
//     module): a [rest.Middleware] attached via plain .Use() — generic
//     across ANY Req type, since its Fn never inspects Req at all. See
//     [ReusableAloneRoute]'s own attachment in chiserver/server.go.
//  2. BOUND ALONE (Class 2) — ALREADY fully demonstrated by
//     auth/middleware.go's [auth.BoundScopeServerMW]/[auth.BoundScopeClientMW],
//     attached to 6 DIFFERENT Req types (CreateUserReq/GetUserReq/
//     UpdateUserReq/ListUsersReq/ProfileReq/AdminActionReq) in
//     nethttpserver/server.go, chiserver/server.go, and client/client.go
//     — not re-demonstrated here to avoid duplicating that existing,
//     already-tested coverage.
//  3. STACKED (both together, ONE route) — [StackedDemoRoute] below:
//     .Use(requestid.ReusableRequestIDMw) (a generic, cross-cutting
//     concern — logs a request-correlation ID) run FIRST, then
//     .HandleBoundMW(auth.BoundScopeServerMW[StackedDemoReq](...)) (the
//     route-specific "profile" scope check, reusing the SAME bound
//     helper middleware.go's 6 routes already use) run SECOND — proving
//     a reusable and a bound attachment compose on one route, in
//     declaration order, exactly like any other pair of RouteOpts.

// StackedDemoReq/StackedDemoResp are deliberately trivial — this route's
// entire point is demonstrating the STACKED (reusable + bound) attach
// pattern, not the business payload.
type StackedDemoReq struct{ Value int }
type StackedDemoResp struct{ Doubled int }

var stackedDemoReqCodec = codex.Struct[StackedDemoReq](
	codex.RequiredField("value", codex.Int(), func(r StackedDemoReq) int { return r.Value }, func(r *StackedDemoReq, v int) { r.Value = v }),
)
var stackedDemoRespCodec = codex.Struct[StackedDemoResp](
	codex.RequiredField("doubled", codex.Int(), func(r StackedDemoResp) int { return r.Doubled }, func(r *StackedDemoResp, v int) { r.Doubled = v }),
)

// ReusableAloneRoute — POST /demo-reusable-alone — attaches ONLY
// [requestid.ReusableRequestIDMw] (via .Use(), in chiserver/server.go), declares
// NO security requirement, and demonstrates the reusable class in total
// isolation from the bound class. POST (not GET), so StackedDemoReq's
// "value" body field is actually sent/decoded — REST client bodies are
// only encoded for POST/PUT/PATCH (see adapters/nethttp/client.go).
var ReusableAloneRoute = rest.NewRoute[StackedDemoReq, StackedDemoResp]("POST", "/demo-reusable-alone",
	stackedDemoReqCodec, stackedDemoRespCodec,
	rest.RouteMeta{
		OperationID: "demoReusableAlone",
		Summary:     "Reusable middleware class, attached alone (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)",
		Tags:        []string{"bound-middleware-split"},
	},
)

// StackedDemoRoute — POST /stacked-demo — attaches BOTH
// [requestid.ReusableRequestIDMw] (.Use(), generic) AND the EXISTING
// [auth.BoundScopeServerMW]/[auth.BoundScopeClientMW] "bearerAuth" scope check
// (.HandleBoundMW()/.ClientBoundMW(), route-specific) on ONE route —
// see chiserver/server.go/client/client.go for the actual attach-order
// call chain (`.Use(reusable).HandleBoundMW(bound)`).
var StackedDemoRoute = rest.NewRoute[StackedDemoReq, StackedDemoResp]("POST", "/stacked-demo",
	stackedDemoReqCodec, stackedDemoRespCodec,
	rest.RouteMeta{
		OperationID: "stackedDemo",
		Summary:     "Reusable + bound middleware STACKED on one route (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7)",
		Tags:        []string{"bound-middleware-split"},
	},
)
