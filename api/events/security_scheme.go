package events

import "github.com/DaniDeer/go-codex/internal/route"

// This file is api/events's own, PUBLIC front door onto the shared,
// internal-only security-scheme vocabulary (internal/route) — part of
// go-codex's "shared cross-pattern MECHANICS live in internal/, pattern-
// specific access lives in the api/port layer" design rule (see
// .github/instructions/go-codex.instructions.md's Design Philosophy
// section, and docs/design/d-0009-internalize-shared-mechanics.md for the full
// rationale). A user of api/events NEVER imports internal/route directly
// — everything below is a thin, same-named wrapper around it, returning
// events's OWN SecurityScheme (the composite with Codec) where that's
// the natural shape, or events's own alias of the bare vocabulary types
// otherwise.

// SecurityRequirement maps a scheme name to its required OAuth 2.0 scopes
// — see [internal/route.SecurityRequirement] for the full doc comment
// (identical shape, aliased here for direct use without importing
// internal/route).
type SecurityRequirement = route.SecurityRequirement

// OAuthFlow describes a single OAuth 2.0 flow — see
// [internal/route.OAuthFlow] for the full doc comment.
type OAuthFlow = route.OAuthFlow

// OAuthFlows holds the OAuth 2.0 flow definitions for an oauth2 security
// scheme — see [internal/route.OAuthFlows] for the full doc comment.
type OAuthFlows = route.OAuthFlows

// SecuritySchemeType identifies the type of an AsyncAPI security scheme —
// see [internal/route.SecuritySchemeType] for the full doc comment.
type SecuritySchemeType = route.SecuritySchemeType

const (
	SecuritySchemeAPIKey        = route.SecuritySchemeAPIKey
	SecuritySchemeHTTP          = route.SecuritySchemeHTTP
	SecuritySchemeOAuth2        = route.SecuritySchemeOAuth2
	SecuritySchemeOpenIDConnect = route.SecuritySchemeOpenIDConnect
)

// BearerScheme returns an HTTP bearer token [SecurityScheme] (Codec
// unset — use [SecurityScheme.WithCodec] to add format validation).
// bearerFormat is informational (e.g. "JWT"); pass an empty string to
// omit it.
func BearerScheme(bearerFormat string) SecurityScheme {
	return SecurityScheme{SecurityScheme: route.BearerScheme(bearerFormat)}
}

// BasicScheme returns an HTTP basic authentication [SecurityScheme].
func BasicScheme() SecurityScheme {
	return SecurityScheme{SecurityScheme: route.BasicScheme()}
}

// APIKeyScheme returns an API key [SecurityScheme]. name is the header/
// query/cookie key; in is "header", "query", or "cookie".
func APIKeyScheme(name, in string) SecurityScheme {
	return SecurityScheme{SecurityScheme: route.APIKeyScheme(name, in)}
}

// OAuth2Scheme returns an OAuth 2.0 [SecurityScheme] with the given flows.
func OAuth2Scheme(flows OAuthFlows) SecurityScheme {
	return SecurityScheme{SecurityScheme: route.OAuth2Scheme(flows)}
}

// OpenIDConnectScheme returns an OpenID Connect [SecurityScheme]. url is
// the well-known OpenID Connect discovery URL.
func OpenIDConnectScheme(url string) SecurityScheme {
	return SecurityScheme{SecurityScheme: route.OpenIDConnectScheme(url)}
}

// Require returns a [SecurityRequirement] for the named scheme with
// optional OAuth 2.0 scopes. For non-OAuth schemes pass no scopes.
//
//	events.Require("bearerAuth")                    // bearer — no scope restriction
//	events.Require("oauth2", "read:users", "admin") // oauth2 with required scopes
func Require(scheme string, scopes ...string) SecurityRequirement {
	return route.Require(scheme, scopes...)
}

// ConnectSecurityScheme is the bare spec-metadata-only scheme shape
// [Client.AddConnectSecurityScheme] takes — see [internal/route.SecurityScheme]
// for the full doc comment. Named "ConnectSecurityScheme", not the bare
// "SecurityScheme", because [SecurityScheme] is already taken by this
// package's own composite (which ALSO carries an optional runtime
// [codex.Codec]) — AddConnectSecurityScheme declares connection-level
// spec metadata only, with no channel-level credential-format validation
// to carry, so the bare vocabulary type is the right shape here, not the
// composite. Construct one directly (e.g.
// `events.ConnectSecurityScheme{Type: events.SecuritySchemeHTTP, Scheme:
// "basic"}`) or via [BasicScheme]/[BearerScheme]/etc.'s own
// `.SecurityScheme` field (every [SecurityScheme] embeds one).
type ConnectSecurityScheme = route.SecurityScheme
