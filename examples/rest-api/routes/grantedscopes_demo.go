package routes

import (
	"context"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than the "bearerAuth" scheme in middleware.go. Both now use the     ──
// ── SAME underlying mechanism — [rest.BoundMiddleware]/                 ──
// ── [rest.BoundClientMiddleware] (docs/design/d-0003-codec-declared-    ──
// ── middlewares.md's Addendum 7) — a REAL credential type (AuthIn) and  ──
// ── a REAL GrantedScopes-carrying Out (AuthOut), dispatched via         ──
// ── HandleBoundMW/ClientBoundMW. This demo additionally publishes the   ──
// ── decoded token via a middleware.ContextField, which middleware.go's ──
// ── scheme doesn't need.                                               ──

// AuthIn is GrantedScopesComputeServerMW's credential vocabulary —
// decoded from the raw "Authorization" header value via the required
// header merge field below, exactly like any other codec-declared
// middleware's In. Shared by the "bearerAuth" scheme in middleware.go
// too (same vocabulary, different scheme name).
type AuthIn struct{ Token string }

// AuthOut carries the conventional GrantedScopes map[string][]string
// field the adapter reads via reflection and merges into the SAME
// middleware.CheckScopes call every Security attachment in this example
// uses — see docs/features/security.md's "Codec-backed Security" section.
type AuthOut struct {
	GrantedScopes map[string][]string
}

// GrantedScopesUserIDField is a middleware.ContextField[string] — the
// authenticated token published by GrantedScopesComputeServerMW's
// embedded Fn (handlers.VerifyBearerGS) and consumed by
// handlers.MakeComputeGSHandler via Get(ctx), with ZERO manual
// re-decoding inside the business handler. Declared once, shared by
// every producer/consumer that needs this same piece of cross-cutting
// data (docs/design/d-0007-declarative-middleware-layering.md's Phase 3).
var GrantedScopesUserIDField = middleware.NewContextField(codex.String())

// grantedScopesGSScheme is shared by both bound constructors below.
var grantedScopesGSScheme = rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT"), Codec: &BearerCodec}

// GrantedScopesComputeServerMW builds the SERVER-side bound "bearerAuthGS"
// Security middleware for POST /compute-gs — fn is supplied by the
// CALLER (handlers.VerifyBearerGS) rather than embedded here, keeping
// routes/ free of a dependency on handlers/ (handlers/ already imports
// routes/, the opposite direction). SetContextFieldFromIn publishes the
// decoded token so the real handler can read it without touching the
// header itself.
func GrantedScopesComputeServerMW(fn func(ctx context.Context, req *ComputeGSReq, in AuthIn) (AuthOut, error)) rest.BoundMiddleware[ComputeGSReq, AuthIn, AuthOut] {
	return rest.BoundSecurityMiddleware[ComputeGSReq, AuthIn, AuthOut]("bearerAuthGS", grantedScopesGSScheme, []string{"compute:write"}, fn).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", BearerCodec,
			func(in AuthIn) string { return in.Token },
			func(in *AuthIn, v string) { in.Token = v },
		)).
		SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Token })
}

// GrantedScopesComputeClientMW is [GrantedScopesComputeServerMW]'s
// client/credential-supplying sibling.
func GrantedScopesComputeClientMW(fn func(ctx context.Context, req ComputeGSReq) (AuthIn, error)) rest.BoundClientMiddleware[ComputeGSReq, AuthIn, AuthOut] {
	return rest.BoundSecurityClientMiddleware[ComputeGSReq, AuthIn, AuthOut]("bearerAuthGS", grantedScopesGSScheme, []string{"compute:write"}, fn).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", BearerCodec,
			func(in AuthIn) string { return in.Token },
			func(in *AuthIn, v string) { in.Token = v },
		))
}

// ComputeGSReq/ComputeGSResp are deliberately trivial — this demo's
// entire point is the security/ContextField mechanism, not the business
// payload.
type ComputeGSReq struct{ X, Y int }
type ComputeGSResp struct{ Sum int }

var computeGSReqCodec = codex.Struct[ComputeGSReq](
	codex.RequiredField("x", codex.Int(), func(r ComputeGSReq) int { return r.X }, func(r *ComputeGSReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(), func(r ComputeGSReq) int { return r.Y }, func(r *ComputeGSReq, v int) { r.Y = v }),
)
var computeGSRespCodec = codex.Struct[ComputeGSResp](
	codex.RequiredField("sum", codex.Int(), func(r ComputeGSResp) int { return r.Sum }, func(r *ComputeGSResp, v int) { r.Sum = v }),
)

// ComputeGSRoute declares NO security requirement itself — HandleBoundMW/
// ClientBoundMW (via GrantedScopesComputeServerMW/
// GrantedScopesComputeClientMW, attached in server.go/client.go)
// populate rb.meta.Security/rb.securitySchemes automatically on whichever
// concrete route value they attach to (docs/design/d-0003-codec-declared-
// middlewares.md's Addendum 7 — BoundMiddleware.applyBoundRoute
// contributes to rb.middlewares, which both Register's
// applySecurityDeclarations AND ClientHandle's
// applyMiddlewareSecurityForClient already read) — the PRIOR manual
// RouteMeta.Security workaround (documented in an earlier revision of
// this file) is no longer needed; declare-here/implement-there no longer
// requires choosing between .Use() and a manual RouteMeta.Security escape
// hatch.
var ComputeGSRoute = rest.NewRoute[ComputeGSReq, ComputeGSResp]("POST", "/compute-gs",
	computeGSReqCodec, computeGSRespCodec,
	rest.RouteMeta{
		OperationID: "computeGS",
		Summary:     "Compute (GrantedScopes + ContextField demo)",
		Tags:        []string{"granted-scopes"},
	},
)
