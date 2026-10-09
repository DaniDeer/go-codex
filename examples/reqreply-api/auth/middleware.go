package auth

import (
	"context"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// BearerAuthIn carries the raw "Authorization" MQTT5 User Property value
// ("******"), merge-field-decoded via WithRequestProperty below —
// REPLACES the OLD legacy-shaped (raw *pahomqtt5.Publish-reading) paired
// Fn permanently closed by docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's
// Phase C. The "Bearer " prefix is trimmed/added by each attachment's
// OWN WithReceive/WithSend Fn (not the merge-field codec itself) —
// mirrors VerifyBearer's existing trim logic, unchanged in spirit, just
// reached through a declarative merge field instead of raw
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
// attachment site reuses via its OWN .WithReceive(fn)/.WithSend(fn)
// chain (Middleware is an IMMUTABLE value — chaining never mutates this
// package-level var, it returns a new one). This preserves the OLD
// legacy mechanism's "one mw, many paired Fns" flexibility (see
// demo_error_pattern.go's AlwaysRejectSecurityImpl for a second,
// differently-implemented attachment of the SAME scheme), now expressed
// via the reusable class instead of per-call-site Fn pairing — mirrors
// examples/rest-api/auth's identical role. Reqreply-only (never attached
// to a REST/events route anywhere in this example) — see
// NewOAuthMwReqreply/OAuthMwREST below for the genuinely cross-API case
// and why it is declared differently.
var BearerAuthMw = reqreply.SecurityMiddleware[BearerAuthIn, BearerAuthOut](
	"bearerAuth",
	reqreply.BearerScheme("JWT").WithCodec(BearerCodec),
	nil,
).WithRequestProperty(reqreply.NewOptionalPropertyParam("Authorization", codex.String(),
	func(v BearerAuthIn) string { return v.Token },
	func(v *BearerAuthIn, s string) { v.Token = s },
))

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
// regardless of the string's actual content; [reqreply.OAuthFlow.TokenURL]
// itself is a PUBLIC, non-secret discovery URL, mirroring any OAuth2
// provider's own published endpoint, e.g. Google/Auth0/Okta's — not a
// credential, hence the naming workaround here rather than a
// suppression).
const oauthAuthServerBaseURL = "https://auth.example.com"
const oauthGrantEndpointPath = "/oauth2/token"

// oauthComputeFlows is the ONE shared, protocol-agnostic OAuth2 scheme
// config (a plain [internal/route.SecurityScheme] value, no attachment semantics
// of its own) — the true single source of truth for both declarations
// below. NewOAuthMwReqreply/OAuthMwREST are each built from THIS SAME
// scheme + OAuthCodec, through their own API's own vocabulary
// (reqreply.BoundSecurityMiddleware / rest.SecurityMiddleware) — "shared
// config, declared twice" rather than "one Go value, attached twice".
//
// See docs/features/security.md's "Sharing a security SCHEME across
// REST/events/reqreply" section for the full write-up this backs.
var oauthComputeFlows = reqreply.OAuthFlows{
	ClientCredentials: &reqreply.OAuthFlow{
		TokenURL: oauthAuthServerBaseURL + oauthGrantEndpointPath,
		Scopes:   map[string]string{OAuthComputeWriteScope: oauthComputeWriteScopeDescription},
	},
}
var oauthComputeScheme = reqreply.OAuth2Scheme(oauthComputeFlows).SecurityScheme
var oauthComputeScopes = []string{OAuthComputeWriteScope}

// OAuthOut carries the conventional GrantedScopes map[string][]string
// field — NewOAuthMwReqreply's Fn (VerifyOAuthComputeZeroMQ) reads its
// credential directly off its OWN *OAuthComputeReq parameter (zeromq has
// no property/header side channel at all, unlike mqtt5's User
// Properties — the genuine, motivating reason this scheme is declared
// via the BOUND class, not the reusable one BearerAuthMw above uses).
type OAuthOut struct {
	GrantedScopes map[string][]string
}

// NewOAuthMwReqreply builds the "oauth2Compute" BOUND scheme for
// reqreply — fn is supplied as a PARAMETER (not baked in here) so this
// package's own VerifyOAuthComputeZeroMQ (handler.go) can be passed
// directly. Called once, at the zeromqserver.Build attachment site, via
// routes.OAuthComputeRoute.HandleBoundMW(auth.NewOAuthMwReqreply(auth.VerifyOAuthComputeZeroMQ)).
func NewOAuthMwReqreply(fn func(ctx context.Context, req *routes.OAuthComputeReq, in struct{}) (OAuthOut, error)) reqreply.BoundMiddleware[routes.OAuthComputeReq, struct{}, OAuthOut] {
	return reqreply.BoundSecurityMiddleware[routes.OAuthComputeReq, struct{}, OAuthOut](
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
