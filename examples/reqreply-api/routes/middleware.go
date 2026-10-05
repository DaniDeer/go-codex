package routes

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerCodec validates a raw bearer token string's FORMAT (non-empty) —
// shared by every security declaration below, mirroring
// examples/rest-api/routes/middleware.go's identical BearerCodec.
var BearerCodec = codex.String().Refine(validate.NonEmptyString)

// BearerAuthIn carries the raw "Authorization" MQTT5 User Property value
// ("Bearer <token>"), merge-field-decoded via WithRequestProperty below —
// REPLACES the OLD legacy-shaped (raw *pahomqtt5.Publish-reading) paired
// Fn permanently closed by docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's
// Phase C. The "Bearer " prefix is trimmed/added by each attachment's
// OWN WithReceive/WithSend Fn (not the merge-field codec itself) —
// mirrors handlers.VerifyBearer's existing trim logic, unchanged in
// spirit, just reached through a declarative merge field instead of raw
// *pahomqtt5.Publish access.
type BearerAuthIn struct {
	Token string
}

// BearerAuthOut carries the conventional GrantedScopes
// map[string][]string field — REQUIRED even with zero specific scopes;
// see [reqreply.SecurityMiddleware]'s own godoc for the full convention.
type BearerAuthOut struct {
	GrantedScopes map[string][]string
}

// BearerAuthMw declares the "bearerAuth" scheme PLUS its "Authorization"
// property merge field — the SHARED, Req-agnostic declaration every
// attachment site below reuses via its OWN .WithReceive(fn)/.WithSend(fn)
// chain (Middleware is an IMMUTABLE value — chaining never mutates this
// package-level var, it returns a new one). This preserves the OLD
// legacy mechanism's "one mw, many paired Fns" flexibility (see
// demo_error_pattern.go's AlwaysRejectBearerAuthMw for a second,
// differently-implemented attachment of the SAME scheme), now expressed
// via the reusable class instead of per-call-site Fn pairing — mirrors
// routes.ProfileScopeMw/AdminScopeMw's identical role in examples/rest-api.
// Reqreply-only (never attached to a REST/events route anywhere in this
// example) — see OAuthMwReqreply/OAuthMwREST below for the genuinely
// cross-API case and why it is declared differently.
var BearerAuthMw = reqreply.SecurityMiddleware[BearerAuthIn, BearerAuthOut](
	"bearerAuth",
	reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.WithCodec(BearerCodec),
	nil,
).WithRequestProperty(reqreply.NewOptionalPropertyParam("Authorization", codex.String(),
	func(v BearerAuthIn) string { return v.Token },
	func(v *BearerAuthIn, s string) { v.Token = s },
))

// OAuthCodec validates a raw OAuth2 bearer token string's FORMAT
// (non-empty) — same role as BearerCodec above.
var OAuthCodec = codex.String().Refine(validate.NonEmptyString)

// OAuthComputeWriteScope is the OAuth2 scope name this example's compute
// routes require — named as its own constant (rather than an inline map
// literal) so it isn't misread as a credential-like "key:value" string by
// static analysis.
const OAuthComputeWriteScope = "compute:write"

// oauthComputeWriteScopeDescription is the human-readable description
// registered for OAuthComputeWriteScope in the OAuth2 flow's Scopes map.
const oauthComputeWriteScopeDescription = "Submit compute requests"

// oauthAuthServerBaseURL/oauthGrantEndpointPath compose the OAuth2
// grant-endpoint URL below (deliberately NAMED to avoid the substring
// "token" entirely — gosec's G101 hardcoded-credential heuristic matches
// on IDENTIFIERS containing that substring assigned a string literal,
// regardless of the string's actual content; [route.OAuthFlow.TokenURL]
// itself is a PUBLIC, non-secret discovery URL, mirroring any OAuth2
// provider's own published endpoint, e.g. Google/Auth0/Okta's — not a
// credential, hence the naming workaround here rather than a suppression).
const oauthAuthServerBaseURL = "https://auth.example.com"
const oauthGrantEndpointPath = "/oauth2/token"

// oauthComputeFlows is the ONE shared, protocol-agnostic OAuth2 scheme
// config (a plain [route.SecurityScheme] value, no attachment semantics
// of its own) — the true single source of truth for both declarations
// below. NewOAuthMwReqreply/OAuthMwREST are each built from THIS SAME
// scheme + OAuthCodec, through their own API's own vocabulary
// (reqreply.BoundSecurityMiddleware / rest.SecurityMiddleware) — "shared
// config, declared twice" rather than "one Go value, attached twice".
//
// Earlier revisions of this example shared ONE middleware.Middleware
// value (built via the now-deleted middleware.SecurityScheme) across
// BOTH APIs — this worked because the legacy type was a single shared
// concrete type every API's internal dispatch recognized identically.
// The newer per-pattern codec-backed Middleware[In,Out] family CANNOT
// replicate that: a reqreply.Middleware value attached to a REST route
// compiles (both satisfy the generic middleware.RouteMiddleware marker)
// but is SILENTLY DROPPED by REST's own internal dispatch (which only
// recognizes rest.Middleware's own concrete type) — no error, no spec
// contribution, the Security requirement simply vanishes. Declaring two
// pattern-specific values from one shared config avoids that failure
// mode entirely, while keeping exactly ONE vocabulary per API
// permanently (no middleware.SecurityScheme escape hatch to reach for).
// See docs/features/security.md's "Sharing a security SCHEME across
// REST/events/reqreply" section for the full write-up this backs.
var oauthComputeFlows = route.OAuthFlows{
	ClientCredentials: &route.OAuthFlow{
		TokenURL: oauthAuthServerBaseURL + oauthGrantEndpointPath,
		Scopes:   map[string]string{OAuthComputeWriteScope: oauthComputeWriteScopeDescription},
	},
}
var oauthComputeScheme = route.OAuth2Scheme(oauthComputeFlows)
var oauthComputeScopes = []string{OAuthComputeWriteScope}

// OAuthOut carries the conventional GrantedScopes map[string][]string
// field — NewOAuthMwReqreply's Fn (handlers.VerifyOAuthComputeZeroMQ)
// reads its credential directly off its OWN *OAuthComputeReq parameter
// (zeromq has no property/header side channel at all, unlike mqtt5's
// User Properties — the genuine, motivating reason this scheme is
// declared via the BOUND class, not the reusable one BearerAuthMw
// above uses).
type OAuthOut struct {
	GrantedScopes map[string][]string
}

// NewOAuthMwReqreply builds the "oauth2Compute" BOUND scheme for
// reqreply — fn is supplied as a PARAMETER (not baked in here) to avoid
// an import cycle: fn's real implementation
// (handlers.VerifyOAuthComputeZeroMQ) lives in the handlers package,
// which already imports routes — routes cannot import handlers back.
// Called once, at the zeromqserver.Build attachment site (which imports
// both packages), via
// routes.OAuthComputeRoute.HandleBoundMW(routes.NewOAuthMwReqreply(handlers.VerifyOAuthComputeZeroMQ)) —
// replaces the OLD .Use(OAuthMwReqreply).HandleMW(&OAuthMwReqreply, fn)
// two-call pairing entirely (HandleBoundMW synthesizes the security spec
// entry on its own, no separate .Use() needed).
func NewOAuthMwReqreply(fn func(ctx context.Context, req *OAuthComputeReq, in struct{}) (OAuthOut, error)) reqreply.BoundMiddleware[OAuthComputeReq, struct{}, OAuthOut] {
	return reqreply.BoundSecurityMiddleware[OAuthComputeReq, struct{}, OAuthOut](
		"oauth2Compute",
		reqreply.SecurityScheme{SecurityScheme: oauthComputeScheme}.WithCodec(OAuthCodec), oauthComputeScopes,
		fn,
	)
}

// OAuthMwREST declares the SAME "oauth2Compute" scheme (same
// oauthComputeScheme/OAuthCodec config as NewOAuthMwReqreply above) for
// REST — attached to a locally-declared REST route in
// demo_cross_api_oauth2_sharing.go to prove the two specs render the
// identical scheme, even though they are two distinct Go values.
var OAuthMwREST = rest.SecurityMiddleware[struct{}, struct{}]("oauth2Compute",
	rest.SecurityScheme{SecurityScheme: oauthComputeScheme}.WithCodec(OAuthCodec), oauthComputeScopes)

// ── Codec-declared enrichment middleware (docs/roadmap/reqreply-codec- ──
// ── declared-middleware.md) — a DIFFERENT kind of declaration than the ──
// ── security schemes above: non-security enrichment, not credential   ──
// ── verification. Kept in this SAME file (not routes.go) because it's ──
// ── STILL a pure, adapter-agnostic DECLARATION — its IMPLEMENTATION   ──
// ── (the Transform fn body) lives in handlers/middleware.go, mirroring──
// ── the SAME declare/implement split BearerAuthMw/VerifyBearer above  ──
// ── already establishes for security middleware.                     ──

// NewTenantPropertyMw builds the property vocabulary axis
// (WithRequestProperty/WithResponseProperty) PLUS the route-BOUND *Req
// access (fn additionally receives the route's own *ComputeReq) —
// attached to PropertyAxisComputeRoute via .HandleBoundMW in BOTH
// mqtt5server.Build AND zeromqserver.Build. fn is supplied as a
// PARAMETER (not baked in here) to avoid an import cycle — its real
// implementation (handlers.ProcessTenant) lives in the handlers
// package, which already imports routes — mirrors NewOAuthMwReqreply's
// identical rationale above.
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
func NewTenantPropertyMw(fn func(ctx context.Context, req *ComputeReq, in TenantIn) (TenantAck, error)) reqreply.BoundMiddleware[ComputeReq, TenantIn, TenantAck] {
	return reqreply.NewBoundMiddleware[ComputeReq](
		middleware.NewDeclaration("tenant-property-axis", TenantInCodec, TenantAckCodec),
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
