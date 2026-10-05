package routes

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than BearerAuthMw above (reusable-class, property-decoded). This    ──
// ── demo's credential lives IN-PAYLOAD (ComputeGSReq.Token — zeromq has  ──
// ── no property/header side channel), so NewGrantedScopesComputeMw below ──
// ── uses the BOUND class instead — a REAL credential type (AuthIn) and a ──
// ── REAL GrantedScopes-carrying Out (AuthOut), dispatched through        ──
// ── HandleBoundMW, with SetContextFieldFromOut propagating the           ──
// ── authenticated identity to the real handler.                         ──

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

// NewGrantedScopesComputeMw builds the "bearerAuthGS" BOUND scheme,
// requiring "compute:write" — fn is supplied as a PARAMETER (not baked in
// here) to avoid an import cycle, mirroring NewOAuthMwReqreply's/
// NewTenantPropertyMw's identical rationale: fn's real implementation
// (handlers.VerifyBearerGS) lives in the handlers package, which already
// imports routes.
func NewGrantedScopesComputeMw(fn func(ctx context.Context, req *ComputeGSReq, in AuthIn) (AuthOut, error)) reqreply.BoundMiddleware[ComputeGSReq, AuthIn, AuthOut] {
	return reqreply.BoundSecurityMiddleware[ComputeGSReq, AuthIn, AuthOut](
		"bearerAuthGS",
		reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, []string{"compute:write"},
		fn,
	).SetContextFieldFromOut(GrantedScopesUserIDField, func(out AuthOut) any { return out.Subject })
}

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

// ComputeGSRoute is declared PRISTINE — .HandleBoundMW(NewGrantedScopesComputeMw(...))
// at the attachment site (zeromqserver.Build) synthesizes the
// "bearerAuthGS" security spec entry automatically, no separate
// RouteMeta.Security/.Use() declaration needed.
var ComputeGSRoute = reqreply.NewRoute[ComputeGSReq, ComputeResp]("compute/gs",
	ComputeGSReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{
		OperationID: "computeGS",
		Summary:     "Compute (GrantedScopes + ContextField demo)",
	},
)
