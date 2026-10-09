// Package propertyaxis is this example's SELF-CONTAINED property-
// vocabulary-axis module — a non-security, codec-declared enrichment
// middleware (docs/design/d-0003-codec-declared-middlewares.md's
// Addendum) bundled together with its own In/Out vocabulary, codecs,
// and implementation — modeling how a real service would factor out a
// reusable enrichment concern (e.g. multi-tenancy stamping) into its own
// library, mirroring this project's `auth/`/`observer/` packages'
// identical "declaration + implementation together" precedent. Kept
// SEPARATE from `auth/` because it is NOT a security/credential concern
// — it is a property-vocabulary enrichment demo, unrelated to
// authentication.
package propertyaxis

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// TenantIn/TenantAck are the property-axis Middleware's own In/Out types
// (docs/design/d-0003-codec-declared-middlewares.md's Addendum) —
// INDEPENDENT of routes.ComputeReq/ComputeResp, mirroring how a declared
// Middleware[In,Out] carries its OWN vocabulary alongside (not instead
// of) the route's own request/response types.
type TenantIn struct {
	TenantID string
}

type TenantAck struct {
	Ack string
}

var TenantInCodec = codex.Struct[TenantIn](
	codex.RequiredField("tenantId", codex.String(),
		func(v TenantIn) string { return v.TenantID },
		func(v *TenantIn, s string) { v.TenantID = s },
	),
)

var TenantAckCodec = codex.Struct[TenantAck](
	codex.RequiredField("ack", codex.String(),
		func(v TenantAck) string { return v.Ack },
		func(v *TenantAck, s string) { v.Ack = s },
	),
)

// NewTenantPropertyMw builds the property vocabulary axis
// (WithRequestProperty/WithResponseProperty) PLUS the route-BOUND *Req
// access (fn additionally receives the route's own *routes.ComputeReq)
// — attached to routes.PropertyAxisComputeRoute via .HandleBoundMW in
// BOTH mqtt5server.Build AND zeromqserver.Build. fn is supplied as a
// PARAMETER (not baked in here) to avoid an import cycle — its real
// implementation (ProcessTenant, handler.go) lives in THIS package
// (unlike the OLD split across routes/+handlers/, this module bundles
// declaration+implementation together, per the auth/observer precedent)
// — mirrors auth.NewOAuthMwReqreply's identical "fn supplied by the
// caller" rationale, just with zero import-cycle risk now that both
// halves live in the same package.
//
// The request property ("X-Tenant-Id") is deliberately declared
// OPTIONAL (NewOptionalPropertyParam, not NewPropertyParam) — a
// required property would make Register/Serve succeed on mqtt5 (which
// carries it as a real MQTT5 User Property) but every CALL over
// zeromq would fail with reqreply.MiddlewareInputError, since zeromq
// has no property mechanism at all and always supplies an empty
// property map. Declaring it optional instead makes THIS SAME route
// genuinely portable: mqtt5 callers who supply the property get full
// enrichment; zeromq callers (who structurally CANNOT supply it) still
// get a valid response, with TenantIn.TenantID left at its zero value.
func NewTenantPropertyMw(fn func(ctx context.Context, req *routes.ComputeReq, in TenantIn) (TenantAck, error)) reqreply.BoundMiddleware[routes.ComputeReq, TenantIn, TenantAck] {
	return reqreply.NewBoundMiddleware[routes.ComputeReq](
		reqreply.NewDeclaration("tenant-property-axis", TenantInCodec, TenantAckCodec),
		fn,
	).
		WithRequestProperty(reqreply.NewOptionalPropertyParam("X-Tenant-Id", codex.String(),
			func(v TenantIn) string { return v.TenantID },
			func(v *TenantIn, s string) { v.TenantID = s },
		)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
			func(v TenantAck) string { return v.Ack },
			func(v *TenantAck, s string) { v.Ack = s },
		))
}
