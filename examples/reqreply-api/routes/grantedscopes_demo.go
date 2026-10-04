package routes

import (
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than BearerAuthMw/OAuthMwReqreply above. Those use the LEGACY shape: ──
// ── reqreply.SecurityMiddleware[struct{}, struct{}] paired with an      ──
// ── adapter-shaped implementation Fn (handlers.VerifyBearer/            ──
// ── VerifyOAuthComputeZeroMQ). GrantedScopesComputeMw below instead uses ──
// ── the GENERALIZED form — a REAL credential type (AuthIn) and a REAL   ──
// ── GrantedScopes-carrying Out (AuthOut) — dispatched through HandleMW's ──
// ── bound path, with SetContextFieldFromIn propagating the authenticated ──
// ── token to the real handler (reqreply is fully duplex, unlike events' ──
// ── Subscribe/Publish asymmetry — SetContextFieldFromOut is ALSO        ──
// ── available symmetrically, just not needed by this simple demo, since ──
// ── AuthOut carries nothing beyond GrantedScopes itself).

// AuthIn is GrantedScopesComputeMw's credential vocabulary — EMPTY here
// because zeromq has no property/header side channel to decode a merge
// field FROM: the bound HandleMW Fn reads the credential directly off
// its OWN *Req parameter instead (mirrors handlers.
// VerifyOAuthComputeZeroMQ's established in-payload model) — a
// documented, accepted zeromq limitation, not a bug. SecurityMiddleware's
// Out is still genuinely generalized (AuthOut, below) — proving the
// GrantedScopes half of the mechanism end-to-end even though In's own
// merge-field half has nothing to decode for THIS transport.
type AuthIn struct{}

// AuthOut carries the conventional GrantedScopes map[string][]string
// field, merged into the SAME middleware.CheckScopes call the legacy
// HandleMW path above already uses, PLUS a genuine, non-GrantedScopes
// response field (Subject) — proving the convention doesn't foreclose
// real response data on the SAME Out value.
type AuthOut struct {
	GrantedScopes map[string][]string
	Subject       string
}

// GrantedScopesUserIDField is a middleware.ContextField[string] — the
// authenticated identity published by GrantedScopesComputeMw's paired
// HandleMW Fn (handlers.VerifyBearerGS) and consumed by
// handlers.MakeComputeGSHandler via Get(ctx), with ZERO manual
// re-decoding (docs/design/d-0007-declarative-middleware-layering.md's
// Phase 3). Published via SetContextFieldFromOut (not FromIn) —
// deliberately, since zeromq has no side channel to decode a credential
// INTO In at all (see AuthIn's own doc comment); reqreply's full duplex
// symmetry means Out works as the propagation source just as well,
// unlike events' Subscribe-only asymmetry.
var GrantedScopesUserIDField = middleware.NewContextField(codex.String())

// GrantedScopesComputeMw declares the "bearerAuthGS" scheme, requiring
// "compute:write" — built via the GENERALIZED reqreply.SecurityMiddleware[
// In, Out] (not [struct{},struct{}] like BearerAuthMw/OAuthMwReqreply
// above).
var GrantedScopesComputeMw = reqreply.SecurityMiddleware[AuthIn, AuthOut]("bearerAuthGS",
	reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, []string{"compute:write"},
).SetContextFieldFromOut(GrantedScopesUserIDField, func(out AuthOut) any { return out.Subject })

// ComputeGSReq embeds a Token field directly (zeromq's in-payload
// credential model, like OAuthComputeReq above) — ComputeResp is reused
// unchanged (no response-side credential field needed for this demo).
type ComputeGSReq struct {
	X, Y  int
	Token string
}

var ComputeGSReqCodec = codex.Struct[ComputeGSReq](
	codex.RequiredField("x", codex.Int(), func(r ComputeGSReq) int { return r.X }, func(r *ComputeGSReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(), func(r ComputeGSReq) int { return r.Y }, func(r *ComputeGSReq, v int) { r.Y = v }),
	codex.RequiredField("token", codex.String(), func(r ComputeGSReq) string { return r.Token }, func(r *ComputeGSReq, v string) { r.Token = v }),
)

// ComputeGSRoute declares its security requirement via RouteMeta.Security
// DIRECTLY — deliberately NOT via .Use(GrantedScopesComputeMw). See the
// "Known gap" callout in docs/features/security.md's reqreply section:
// pairing .Use(mw) with a bound HandleMW(mw, fn) for the SAME
// Security-only mw currently throws DuplicateMiddlewareNameError
// (confirmed cross-package, also affects api/rest, tracked not yet
// fixed). Declaring RouteMeta.Security directly is the documented
// workaround — sufficient for CheckCoverage/CheckScopes correctness.
var ComputeGSRoute = reqreply.NewRoute[ComputeGSReq, ComputeResp]("compute/gs",
	ComputeGSReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{
		OperationID: "computeGS",
		Summary:     "Compute (GrantedScopes + ContextField demo)",
		Security:    []route.SecurityRequirement{route.Require("bearerAuthGS", "compute:write")},
	},
)
