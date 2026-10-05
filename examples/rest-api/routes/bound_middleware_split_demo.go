package routes

import (
	"context"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
)

// ── Bound-middleware-split demo (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) ──
//
// This file is a DIRECT, side-by-side contrast of the two classes the
// split introduced, on top of the "bearerAuth" scope-check mechanism
// already shipped in middleware.go:
//
//  1. REUSABLE ALONE (Class 1) — [ReusableRequestIDMw] below: a
//     [rest.Middleware] attached via plain .Use() — generic across ANY
//     Req type, since its Fn never inspects Req at all. See
//     [ReusableAloneRoute]'s own attachment in chiserver/server.go.
//  2. BOUND ALONE (Class 2) — ALREADY fully demonstrated by
//     middleware.go's [BoundScopeServerMW]/[BoundScopeClientMW],
//     attached to 6 DIFFERENT Req types (CreateUserReq/GetUserReq/
//     UpdateUserReq/ListUsersReq/ProfileReq/AdminActionReq) in
//     nethttpserver/server.go, chiserver/server.go, and client/client.go
//     — not re-demonstrated here to avoid duplicating that existing,
//     already-tested coverage.
//  3. STACKED (both together, ONE route) — [StackedDemoRoute] below:
//     .Use(ReusableRequestIDMw) (a generic, cross-cutting concern — logs
//     a request-correlation ID) run FIRST, then
//     .HandleBoundMW(BoundScopeServerMW[StackedDemoReq](...)) (the
//     route-specific "profile" scope check, reusing the SAME bound
//     helper middleware.go's 6 routes already use) run SECOND — proving
//     a reusable and a bound attachment compose on one route, in
//     declaration order, exactly like any other pair of RouteOpts.

// ReqIDIn/ReqIDOut are [ReusableRequestIDMw]'s vocabulary — a generic,
// NON-Security middleware (no scheme, no GrantedScopes) that merges an
// optional "X-Demo-Request-Id" header and simply logs it. Out is empty:
// this middleware's only purpose is the side effect (logging), not
// producing a value any downstream code reads.
type ReqIDIn struct{ RequestID string }
type ReqIDOut struct{}

var (
	reqIDInCodec = codex.Struct[ReqIDIn](
		codex.OptionalField("requestID", codex.String(),
			func(r ReqIDIn) string { return r.RequestID },
			func(r *ReqIDIn, v string) { r.RequestID = v },
		),
	)
	reqIDOutCodec = codex.Struct[ReqIDOut]()
)

// ReusableRequestIDMw is Class 1 (reusable) standalone: a plain
// [rest.Middleware], attached via .Use() (see chiserver/server.go), that
// reads the optional "X-Demo-Request-Id" header and logs it — generic
// across ANY route's Req type, since receiveFn never touches Req. This
// SAME value could be .Use()'d on every route in this example without
// any per-route wrapping (unlike the bound class, which needs one
// instantiation per Req type) — the headline "reusable" property.
var ReusableRequestIDMw = rest.NewMiddleware(
	middleware.NewDeclaration[ReqIDIn, ReqIDOut]("logRequestID", reqIDInCodec, reqIDOutCodec),
).
	WithRequestHeader(rest.NewOptionalHeaderParam("X-Demo-Request-Id", codex.String(),
		func(in ReqIDIn) string { return in.RequestID },
		func(in *ReqIDIn, v string) { in.RequestID = v },
	)).
	WithReceive(func(_ context.Context, in ReqIDIn) (ReqIDOut, error) {
		if in.RequestID != "" {
			println("  [ReusableRequestIDMw] X-Demo-Request-Id =", in.RequestID)
		}
		return ReqIDOut{}, nil
	})

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
// [ReusableRequestIDMw] (via .Use(), in chiserver/server.go), declares
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
// [ReusableRequestIDMw] (.Use(), generic) AND the EXISTING
// [BoundScopeServerMW]/[BoundScopeClientMW] "bearerAuth" scope check
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
