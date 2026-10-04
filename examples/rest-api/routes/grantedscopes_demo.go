package routes

import (
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than ProfileScopeMw/AdminScopeMw above. Those use the LEGACY shape: ──
// ── rest.SecurityMiddleware[struct{}, struct{}] + a separate            ──
// ── middleware.ServerImplementation (handlers.ScopesImpl) paired via    ──
// ── HandleMW(&mw, impl.Fn). GrantedScopesComputeMw below instead uses   ──
// ── the GENERALIZED form — a REAL credential type (AuthIn) and a REAL   ──
// ── GrantedScopes-carrying Out (AuthOut) — dispatched through the SAME  ──
// ── HandleMW/ClientMW methods via fn's own reflected signature. This is ──
// ── the flagship capability Rollout Phase A's "Security generalization" ──
// ── shipped but no example in this repo had exercised until now.

// AuthIn is GrantedScopesComputeMw's credential vocabulary — decoded from
// the raw "Authorization" header value via the required header merge
// field below (WithRequestHeader), exactly like any other codec-declared
// middleware's In.
type AuthIn struct{ Token string }

// AuthOut carries the conventional GrantedScopes map[string][]string
// field the adapter reads via reflection and merges into the SAME
// middleware.CheckScopes call the legacy ServerImplementation path above
// already uses — see docs/features/security.md's "Codec-backed Security"
// section.
type AuthOut struct {
	GrantedScopes map[string][]string
}

// GrantedScopesUserIDField is a middleware.ContextField[string] — the
// authenticated token published by GrantedScopesComputeMw's paired
// HandleMW Fn (handlers.VerifyBearerGS) and consumed by
// handlers.MakeComputeGSHandler via Get(ctx), with ZERO manual
// re-decoding inside the business handler. Declared once, shared by
// every producer/consumer that needs this same piece of cross-cutting
// data (docs/design/d-0007-declarative-middleware-layering.md's Phase 3).
var GrantedScopesUserIDField = middleware.NewContextField(codex.String())

// GrantedScopesComputeMw declares the "bearerAuthGS" scheme, requiring
// "compute:write" — built via the GENERALIZED rest.SecurityMiddleware[In,
// Out] (not [struct{},struct{}] like ProfileScopeMw/AdminScopeMw above).
// SetContextFieldFromIn publishes the decoded token so the real handler
// can read it without touching the header itself.
var GrantedScopesComputeMw = rest.SecurityMiddleware[AuthIn, AuthOut]("bearerAuthGS",
	rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT"), Codec: &BearerCodec}, []string{"compute:write"},
).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", BearerCodec,
	func(in AuthIn) string { return in.Token },
	func(in *AuthIn, v string) { in.Token = v },
)).SetContextFieldFromIn(GrantedScopesUserIDField, func(in AuthIn) any { return in.Token })

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

// ComputeGSRoute declares its security requirement via RouteMeta.Security
// DIRECTLY — deliberately NOT via .Use(GrantedScopesComputeMw) — see the
// "Known gap" callout in docs/features/security.md's "Codec-backed
// Security" section: pairing .Use(mw) with a bound HandleMW(mw, fn) for
// the SAME Security-only mw currently throws DuplicateMiddlewareNameError
// (confirmed cross-package, also affects api/reqreply, tracked not yet
// fixed). Declaring RouteMeta.Security directly is the documented
// workaround — sufficient for CheckCoverage/CheckScopes correctness
// (which is all a route needs for runtime enforcement), though
// OpenAPISpec() won't auto-register the scheme via this path (a
// separate, spec-rendering-only concern, irrelevant to this demo).
var ComputeGSRoute = rest.NewRoute[ComputeGSReq, ComputeGSResp]("POST", "/compute-gs",
	computeGSReqCodec, computeGSRespCodec,
	rest.RouteMeta{
		OperationID: "computeGS",
		Summary:     "Compute (GrantedScopes + ContextField demo)",
		Tags:        []string{"granted-scopes"},
		Security:    []route.SecurityRequirement{route.Require("bearerAuthGS", "compute:write")},
	},
)
