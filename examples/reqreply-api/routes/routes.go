// Package routes declares every reqreply.Route used by this example — pure
// spec values, no handler/transport attached yet (that happens in
// mqtt5server/zeromqserver/zeromqrouterserver via route.WithHandler(fn).
// Register(server), mirroring examples/rest-api/routes' identical
// separation of concerns).
package routes

import (
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

// ComputeReq/ComputeResp are the shared request/response types for every
// compute route declared below.
type ComputeReq struct {
	X int
	Y int
}

type ComputeResp struct {
	Sum int
}

var ComputeReqCodec = codex.Struct[ComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r ComputeReq) int { return r.X },
		func(r *ComputeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r ComputeReq) int { return r.Y },
		func(r *ComputeReq, v int) { r.Y = v },
	),
)

var ComputeRespCodec = codex.Struct[ComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r ComputeResp) int { return r.Sum },
		func(r *ComputeResp, v int) { r.Sum = v },
	),
)

// ComputeRoute is the baseline, unsecured request-reply contract used by
// Demo 1 (basic call+serve), Demo 2 (dual-mode Call), and Demo 7 (spec
// printing). Registered on mqtt5server's Server, which ALSO declares
// Server.AddGlobalSecurity("bearerAuth") for GlobalOnlyComputeRoute's own
// demo — ComputeRoute must explicitly OPT OUT via an EMPTY (non-nil)
// Security slice, per [reqreply.RouteHandle.Security]'s own documented
// contract ("nil means inherit GlobalSecurity, an empty non-nil slice
// means explicitly no auth required"). Before Phase 1 (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum)
// added a mandatory adapter-Serve-time
// [reqreply.CheckCoverage] check, this route silently INHERITED
// GlobalSecurity with NO enforcement at all (the old credential-FORMAT
// check only ran for schemes with a registered [reqreply.
// WithSecurityScheme] on THAT SPECIFIC route, which ComputeRoute never
// declared) — a latent gap CheckCoverage's introduction correctly
// surfaces as a hard MissingSecurityMiddlewareError instead of silently
// ignoring it.
var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers.", Security: []route.SecurityRequirement{}},
)

// DoubleRoute and TripleRoute exist ALONGSIDE ComputeRoute purely to give
// Demo 4 (concurrent multi-route dispatch) 3+ distinct routes registered
// against ONE Server, each independently answering calls concurrently.
var DoubleRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/double",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeDouble", Summary: "Double the sum of two integers."},
)

var TripleRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/triple",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeTriple", Summary: "Triple the sum of two integers."},
)

// SecuredComputeRoute is declared PRISTINE — no Security, no scheme —
// exactly like ComputeRoute above. Its OWN security requirement is
// declared declaratively via .Use(BearerAuthMw) (see middleware.go),
// applied by the SERVER at registration time (mqtt5server/server.go) and
// by the CLIENT at call time (demo_route_level_security_credential_error.go),
// each producing a SEPARATE Route variant sharing this same base value —
// mirrors examples/rest-api's routes.CreateUserRoute (declared plain,
// secured via .Use() at each call site) rather than baking Security into
// the base declaration itself (Phase 1, see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum
// — REPLACES the OLD manual RouteMeta.Security +
// reqreply.WithSecurityScheme pattern, which cannot be paired against a
// HandleMW/ClientMW implementation).
var SecuredComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/secured-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "securedComputeAdd", Summary: "Add two integers — requires a bearer token."},
)

// GlobalOnlyComputeRoute is ALSO declared PRISTINE (no Security, no
// scheme) — it only ever becomes secured via Server.AddGlobalSecurity
// (mqtt5server/server.go) PLUS a server-side .Use(BearerAuthMw) applied
// at THAT SAME registration call (required for
// [reqreply.CheckCoverage] to find a matching implementation — a
// declared-but-unimplemented scheme now fails loudly, Phase 1's
// intentional tightening). Demo 2 shows the confirmed dual-mode
// Client.Call difference: THIS pristine var, called raw, never sees
// GlobalSecurity (identical to REST's own accepted limitation, since
// Security here is nil AND never declared via .Use() either) — while a
// SEPARATE .Use()+.ClientMW()'d variant (built in the demo itself) does.
var GlobalOnlyComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/global-secured-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "globalSecuredComputeAdd", Summary: "Add two integers — secured only via Server.AddGlobalSecurity."},
)

// HeaderParamComputeRoute demonstrates Phase 1b (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum)
// — the User-Property param-as-middleware
// mechanism. Declared PRISTINE here, exactly like SecuredComputeRoute
// above: the request-side "X-API-Key" User Property (via
// mqtt5adapter.FromUserPropertyParam) and the reply-side "X-Trace-Id"
// User Property (via mqtt5adapter.FromResponseUserPropertyParam) are
// BOTH mqtt5-specific, so attached in mqtt5server/server.go, not here —
// mirrors SecuredComputeRoute's own "pristine base, secured at the
// attachment site" separation. Like ComputeRoute above, it must
// explicitly OPT OUT of mqtt5server's Server.AddGlobalSecurity("bearerAuth")
// via an EMPTY (non-nil) Security slice — this demo is about the
// User-Property mechanism specifically, independent of bearer-auth
// security, and declares no HandleMW implementation for "bearerAuth" (it
// would otherwise fail [reqreply.CheckCoverage] at Serve time).
var HeaderParamComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/header-param-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "headerParamComputeAdd", Summary: "Add two integers — requires an X-API-Key User Property.", Security: []route.SecurityRequirement{}},
)

// TenantIn/TenantAck are the property-axis Middleware's own In/Out types
// (docs/design/d-0003-codec-declared-middlewares.md's Addendum) — INDEPENDENT of
// ComputeReq/ComputeResp, mirroring how a declared Middleware[In,Out]
// carries its OWN vocabulary alongside (not instead of) the route's own
// request/response types.
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

// PropertyAxisComputeRoute demonstrates docs/roadmap/reqreply-codec-
// declared-middleware.md's NEW property vocabulary axis
// (WithRequestProperty/WithResponseProperty) — declared PRISTINE here,
// exactly like HeaderParamComputeRoute above: `TenantPropertyMw`'s
// attachment (via reqreply.Transform) happens separately, ONCE PER
// ADAPTER (mqtt5server/server.go AND zeromqserver/server.go), from the
// SAME declared Middleware value + handlers.ProcessTenant implementation
// — this route is registered on BOTH transports to prove the
// declaration is genuinely portable, not mqtt5-specific. Explicitly
// opts OUT of mqtt5server's Server.AddGlobalSecurity ("bearerAuth") via
// an empty Security slice, same rationale as HeaderParamComputeRoute.
var PropertyAxisComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/property-axis-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "propertyAxisComputeAdd", Summary: "Add two integers — demonstrates the property vocabulary axis (WithRequestProperty/WithResponseProperty).", Security: []route.SecurityRequirement{}},
)

// RouterComputeRoute is dispatched over a ZMQ ROUTER/DEALER socket pair in
// Demo 8. MissingComputeRoute is registered on the SAME server but
// DELIBERATELY has no corresponding socket wired in
// zeromqrouterserver.Build, to surface the typed MissingSocketError.
var RouterComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/router-add",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "routerComputeAdd", Summary: "Add two integers over a ZMQ ROUTER/DEALER socket pair."},
)

// MissingSocketRoute is registered on the SAME router server as
// RouterComputeRoute but Demo 8 deliberately never wires a matching socket
// for it — demonstrating AttachRouterServer's upfront MissingSocketError.
var MissingSocketRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/router-add-missing",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "routerComputeAddMissing", Summary: "Deliberately has no matching socket — demonstrates MissingSocketError."},
)

// ConflictError is a domain business error handlers.AddOrConflict returns
// when X is negative — declared as a reqreply.ErrorPattern below so it
// round-trips to the CLIENT as a typed, errors.As-navigable value instead
// of an opaque error string (see docs/roadmap/error-handling-rest-events-reqreply.md's
// "Phase 0" section and docs/guides/asyncapi.md's "Client-side decode" section).
type ConflictError struct{ Reason string }

func (e ConflictError) Error() string { return "conflict: " + e.Reason }

// ConflictPayload is ConflictError's codec-backed wire shape — the SAME
// payload the server marshals into the error reply's body AND the client
// decodes it back into via RouteHandle.DecodeErrorFor.
type ConflictPayload struct {
	Code   string
	Reason string
}

var ConflictPayloadCodec = codex.Struct[ConflictPayload](
	codex.RequiredField("code", codex.String(),
		func(p ConflictPayload) string { return p.Code },
		func(p *ConflictPayload, v string) { p.Code = v },
	),
	codex.RequiredField("reason", codex.String(),
		func(p ConflictPayload) string { return p.Reason },
		func(p *ConflictPayload, v string) { p.Reason = v },
	),
)

// ConflictErrorPattern is exported (not inlined into
// ErrorPatternComputeRoute's opts) SPECIFICALLY so demo_error_pattern.go
// can call ConflictErrorPattern.Match(err) — one of the 3 client-side
// matching mechanisms (alongside reqreply.ErrorPatternAs/HandleErrorPattern).
var ConflictErrorPattern = reqreply.ErrorPattern[ConflictError, ConflictPayload](ConflictPayloadCodec,
	func(e ConflictError) (ConflictPayload, error) {
		return ConflictPayload{Code: "negative-x", Reason: e.Reason}, nil
	},
).WithCode("negative-x").WithDescription("X must not be negative.")

// ErrorPatternComputeRoute demonstrates the client-side ErrorPattern
// decode workflow (mqtt5 + zeromq): handlers.AddOrConflict returns
// ConflictError when X is negative, matched by the declared ErrorPattern
// below (Mapped mode) and round-tripped to the client as a typed
// ConflictPayload — see demo_error_pattern.go.
var ErrorPatternComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add-error-pattern",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAddErrorPattern", Summary: "Add two integers; rejects negative X with a typed ConflictError.", Security: []route.SecurityRequirement{}},
	ConflictErrorPattern,
)

// RateLimitError is returned directly by a handler AND is itself the
// typed error reply payload — reqreply.ErrorPattern's DIRECT mode (no
// mapFn, E assignable to B), the 2nd of reqreply's 2 declaration
// mechanisms (Mapped mode is ConflictError/ConflictPayload above). Unlike
// REST/events, reqreply has NO ErrorAction — a matched pattern ALWAYS
// replies with the typed payload (no Handle/Log alternative), since
// reqreply always owes the caller a reply.
type RateLimitError struct {
	RetryAfterSeconds int
}

func (e RateLimitError) Error() string { return "rate limit exceeded" }

var RateLimitErrorCodec = codex.Struct[RateLimitError](
	codex.RequiredField("retry_after_seconds", codex.Int(),
		func(e RateLimitError) int { return e.RetryAfterSeconds },
		func(e *RateLimitError, v int) { e.RetryAfterSeconds = v },
	),
)

// RateLimitComputeRoute demonstrates reqreply.ErrorPattern's Direct mode.
var RateLimitComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add-ratelimit-demo",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAddRateLimitDemo", Summary: "Add two integers (ErrorPattern Direct-mode demo).", Security: []route.SecurityRequirement{}},
	reqreply.ErrorPattern[RateLimitError, RateLimitError](RateLimitErrorCodec),
)

// TimeoutError is a domain error type DELIBERATELY NOT matched by any
// declared reqreply.ErrorPattern on DeadLetterComputeRoute — used to
// demonstrate the DeadLetter mechanism: an undeclared error type falls
// through to the generic error reply AND is additionally dead-lettered
// for a durable ops record (reqreply.DeadLetter fires ALONGSIDE the
// synchronous reply, never instead of it — unlike events/pub-sub, a
// reqreply caller always gets SOME reply).
type TimeoutError struct{}

func (TimeoutError) Error() string { return "downstream call timed out" }

// DeadLetterComputeRoute declares ONLY reqreply.DeadLetter (no
// ErrorPattern) — see demo_error_pattern.go's DLQ demo, which contrasts
// this route's undeclared-error-type behavior against
// ErrorPatternComputeRoute's matched-type behavior.
var DeadLetterComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add-deadletter-demo",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAddDeadLetterDemo", Summary: "Add two integers (DeadLetter demo).", Security: []route.SecurityRequirement{}},
	reqreply.DeadLetter("compute/add-deadletter-demo/dlq"),
)

// ── Security middleware + ErrorPattern combination ───────────────────────────

// SecurityRejectedPayload is the typed reply payload published when a
// security middleware Fn rejects a call — matched via
// reqreply.ErrorPattern[reqreply.SecurityError, SecurityRejectedPayload]
// on SecuredErrorPatternRoute, proving ErrorPattern intercepts a SECURITY
// MIDDLEWARE Fn failure (auto-wrapped in reqreply.SecurityError by the
// adapter), not just a handler business error.
type SecurityRejectedPayload struct {
	Code string
}

var SecurityRejectedPayloadCodec = codex.Struct[SecurityRejectedPayload](
	codex.RequiredField("code", codex.String(),
		func(p SecurityRejectedPayload) string { return p.Code },
		func(p *SecurityRejectedPayload, v string) { p.Code = v },
	),
)

// SecuredErrorPatternRoute pairs a HandleMW-attached security Fn (see
// demo_error_pattern.go, which ALWAYS rejects) with a declared
// reqreply.ErrorPattern[reqreply.SecurityError, SecurityRejectedPayload].
var SecuredErrorPatternRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add-security-errorpattern-demo",
	ComputeReqCodec, ComputeRespCodec,
	reqreply.RouteMeta{OperationID: "computeAddSecurityErrorPatternDemo", Summary: "Add two integers (security-middleware ErrorPattern demo)."},
	reqreply.ErrorPattern[reqreply.SecurityError, SecurityRejectedPayload](SecurityRejectedPayloadCodec,
		func(e reqreply.SecurityError) (SecurityRejectedPayload, error) {
			return SecurityRejectedPayload{Code: "security_rejected"}, nil
		},
	),
).Use(BearerAuthMw)

// OAuthComputeReq/OAuthComputeResp carry an in-payload Token field —
// zeromq's reqreply security Fn-shape (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum,
// SHIPPED) reads/writes this field directly, since zeromq has no
// raw-message side channel equivalent to MQTT5's User Properties (unlike
// ComputeReq/ComputeResp, used everywhere else in this example, which
// carry no credential field at all).
type OAuthComputeReq struct {
	X, Y  int
	Token string
}

type OAuthComputeResp struct {
	Sum int
}

var OAuthComputeReqCodec = codex.Struct[OAuthComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r OAuthComputeReq) int { return r.X },
		func(r *OAuthComputeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r OAuthComputeReq) int { return r.Y },
		func(r *OAuthComputeReq, v int) { r.Y = v },
	),
	codex.OptionalField("token", codex.String(),
		func(r OAuthComputeReq) string { return r.Token },
		func(r *OAuthComputeReq, v string) { r.Token = v },
	),
)

var OAuthComputeRespCodec = codex.Struct[OAuthComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r OAuthComputeResp) int { return r.Sum },
		func(r *OAuthComputeResp, v int) { r.Sum = v },
	),
)

// OAuthComputeRoute demonstrates the SAME OAuthMw declaration (see
// middleware.go) attached to a zeromq reqreply route, via .Use()+
// HandleMW()/ClientMW() — Demo 9 (demo_cross_api_oauth2_sharing.go) also
// attaches this EXACT Go value to a locally-declared REST route, proving
// one declaration is shareable across BOTH APIs. Declared PRISTINE here
// (no Security baked in, no GlobalSecurity to opt out of on the zeromq
// server) — mirrors HeaderParamComputeRoute/SecuredComputeRoute's own
// "pristine base, secured at the attachment site" separation.
var OAuthComputeRoute = reqreply.NewRoute[OAuthComputeReq, OAuthComputeResp](
	"compute/oauth-add",
	OAuthComputeReqCodec, OAuthComputeRespCodec,
	reqreply.RouteMeta{OperationID: "oauthComputeAdd", Summary: "Add two integers — requires an OAuth2 compute:write scope."},
)
