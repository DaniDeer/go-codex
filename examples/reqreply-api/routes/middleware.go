package routes

import (
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// BearerCodec validates a raw bearer token string's FORMAT (non-empty) —
// shared by every security declaration below, mirroring
// examples/rest-api/routes/middleware.go's identical BearerCodec.
var BearerCodec = codex.String().Refine(validate.NonEmptyString)

// BearerAuthMw declares the "bearerAuth" scheme — attached via
// .Use(BearerAuthMw) on every route that requires it (Phase 1 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum), REPLACING the older
// reqreply.WithSecurityScheme + manual RouteMeta.Security declaration
// pattern still shown on routes.SecuredComputeRoute's own (deprecated)
// path — mirrors routes.ProfileScopeMw/AdminScopeMw's identical role in
// examples/rest-api.
var BearerAuthMw = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, &BearerCodec)

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

// OAuthMw declares an OAuth2 "oauth2Compute" scheme — via
// route.OAuth2Scheme, the SAME route.SecurityScheme/middleware.
// SecurityScheme mechanism BearerAuthMw uses above. Demonstrates
// docs/features/security.md's "Sharing a security scheme declaration
// across REST/events/reqreply" pattern: this EXACT Go value is
// attachable via .Use() to a REST route, an events channel, OR a
// reqreply route — see demo_cross_api_oauth2_sharing.go, which attaches
// it to BOTH a zeromq reqreply route (real, served, called) and a
// locally-declared REST route (spec-only, proving the declaration is
// byte-for-byte shared, not just superficially similar).
var oauthComputeFlows = route.OAuthFlows{
	ClientCredentials: &route.OAuthFlow{
		TokenURL: oauthAuthServerBaseURL + oauthGrantEndpointPath,
		Scopes:   map[string]string{OAuthComputeWriteScope: oauthComputeWriteScopeDescription},
	},
}

var OAuthMw = middleware.SecurityScheme("oauth2Compute", route.OAuth2Scheme(oauthComputeFlows),
	[]string{OAuthComputeWriteScope}, &OAuthCodec)

// ── Codec-declared enrichment middleware (docs/roadmap/reqreply-codec- ──
// ── declared-middleware.md) — a DIFFERENT kind of declaration than the ──
// ── security schemes above: non-security enrichment, not credential   ──
// ── verification. Kept in this SAME file (not routes.go) because it's ──
// ── STILL a pure, adapter-agnostic DECLARATION — its IMPLEMENTATION   ──
// ── (the Transform fn body) lives in handlers/middleware.go, mirroring──
// ── the SAME declare/implement split BearerAuthMw/VerifyBearer above  ──
// ── already establishes for security middleware.                     ──

// TenantPropertyMw declares the property vocabulary axis
// (WithRequestProperty/WithResponseProperty) attached to
// PropertyAxisComputeRoute via reqreply.Transform in BOTH
// mqtt5server.Build AND zeromqserver.Build — the SAME Go value,
// registered against two completely different transports, with ZERO
// adapter-specific changes to the declaration itself (see
// demo_property_axis_middleware.go for what actually differs at
// runtime between the two).
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
var TenantPropertyMw = reqreply.NewMiddleware(middleware.NewDeclaration("tenant-property-axis", TenantInCodec, TenantAckCodec)).
	WithRequestProperty(reqreply.NewOptionalPropertyParam("X-Tenant-Id", codex.String(),
		func(v TenantIn) string { return v.TenantID },
		func(v *TenantIn, s string) { v.TenantID = s },
	)).
	WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
		func(v TenantAck) string { return v.Ack },
		func(v *TenantAck, s string) { v.Ack = s },
	))
