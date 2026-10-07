package auth

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── GrantedScopes + ContextField demo (docs/design/d-0007-declarative-   ──
// ── middleware-layering.md) — a DIFFERENT style of security declaration ──
// ── than BearerAuthMw above (reusable-class, property-decoded). This    ──
// ── demo's credential lives IN-PAYLOAD (ComputeGSReq.Token — zeromq has  ──
// ── no property/header side channel), so NewGrantedScopesComputeMw below ──
// ── uses the BOUND class instead — a REAL credential type               ──
// ── (GrantedScopesAuthIn) and a REAL GrantedScopes-carrying Out          ──
// ── (GrantedScopesAuthOut), dispatched through HandleBoundMW, with       ──
// ── SetContextFieldFromOut propagating the authenticated identity to    ──
// ── the real handler. ComputeGSRoute is an AUTH-FLOW demo route (its     ──
// ── entire purpose is demonstrating this mechanism, not a plain business ──
// ── route that merely attaches auth) — it lives here in auth/, not      ──
// ── routes/, unlike SecuredComputeRoute/GlobalOnlyComputeRoute/etc.      ──

// GrantedScopesAuthIn is NewGrantedScopesComputeMw's credential
// vocabulary — EMPTY here because zeromq has no property/header side
// channel to decode a merge field FROM: the bound HandleMW Fn reads the
// credential directly off its OWN *Req parameter instead (mirrors
// VerifyOAuthComputeZeroMQ's established in-payload model) — a
// documented, accepted zeromq limitation, not a bug. SecurityMiddleware's
// Out is still genuinely generalized (GrantedScopesAuthOut, below),
// proving the GrantedScopes half of the mechanism end-to-end even though
// In's own merge-field half has nothing to decode for THIS transport.
type GrantedScopesAuthIn struct{}

// GrantedScopesAuthOut carries the conventional GrantedScopes
// map[string][]string field, merged into the SAME middleware.CheckScopes
// call every Security attachment uses, PLUS a genuine, non-GrantedScopes
// response field (Subject) — proving the convention doesn't foreclose
// real response data on the SAME Out value.
type GrantedScopesAuthOut struct {
	GrantedScopes map[string][]string
	Subject       string
}

// GrantedScopesUserIDField is a middleware.ContextField[string] — the
// authenticated identity published by NewGrantedScopesComputeMw's paired
// HandleMW Fn (VerifyBearerGS) and consumed by MakeComputeGSHandler via
// Get(ctx), with ZERO manual re-decoding (docs/design/
// d-0007-declarative-middleware-layering.md's Phase 3). Published via
// SetContextFieldFromOut (not FromIn) — deliberately, since zeromq has no
// side channel to decode a credential INTO In at all (see
// GrantedScopesAuthIn's own doc comment); reqreply's full duplex symmetry
// means Out works as the propagation source just as well, unlike events'
// Subscribe-only asymmetry.
var GrantedScopesUserIDField = middleware.NewContextField(codex.String())

// NewGrantedScopesComputeMw builds the "bearerAuthGS" BOUND scheme,
// requiring "compute:write" — fn is supplied as a PARAMETER (not baked in
// here) so this package's own VerifyBearerGS (handler.go) can be passed
// directly.
func NewGrantedScopesComputeMw(fn func(ctx context.Context, req *ComputeGSReq, in GrantedScopesAuthIn) (GrantedScopesAuthOut, error)) reqreply.BoundMiddleware[ComputeGSReq, GrantedScopesAuthIn, GrantedScopesAuthOut] {
	return reqreply.BoundSecurityMiddleware[ComputeGSReq, GrantedScopesAuthIn, GrantedScopesAuthOut](
		"bearerAuthGS",
		reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, []string{"compute:write"},
		fn,
	).SetContextFieldFromOut(GrantedScopesUserIDField, func(out GrantedScopesAuthOut) any { return out.Subject })
}

// ComputeGSReq embeds a Token field directly (zeromq's in-payload
// credential model, like routes.OAuthComputeReq) — routes.ComputeResp is
// reused unchanged (no response-side credential field needed for this
// demo).
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
// RouteMeta.Security/.Use() declaration needed. Declared with a
// RELATIVE topic ("gs") — Mounted under the shared "compute" Router
// prefix at the zeromqserver.Build attachment site, composing back to
// "compute/gs", byte-identical to its original, pre-Mount topic.
var ComputeGSRoute = reqreply.NewRoute[ComputeGSReq, routes.ComputeResp]("gs",
	ComputeGSReqCodec, routes.ComputeRespCodec,
	reqreply.RouteMeta{
		OperationID: "computeGS",
		Summary:     "Compute (GrantedScopes + ContextField demo)",
	},
)
