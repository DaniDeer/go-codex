package rest

import "github.com/DaniDeer/go-codex/internal/route"

// This file is api/rest's own, PUBLIC front door onto the shared,
// internal-only security-scheme vocabulary (internal/route) — part of
// go-codex's "shared cross-pattern MECHANICS live in internal/, pattern-
// specific access lives in the api/port layer" design rule (see
// .github/instructions/go-codex.instructions.md's Design Philosophy
// section, and docs/design/d-0009-internalize-shared-mechanics.md for the full
// rationale). A user of api/rest NEVER imports internal/route directly —
// everything below is a thin, same-named wrapper around it, returning
// rest's OWN SecurityScheme (the composite with Codec) where that's the
// natural shape, or rest's own alias of the bare vocabulary types
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

// SecuritySchemeType identifies the type of an OpenAPI security scheme —
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
//	rest.Require("bearerAuth")                    // bearer — no scope restriction
//	rest.Require("oauth2", "read:users", "admin") // oauth2 with required scopes
func Require(scheme string, scopes ...string) SecurityRequirement {
	return route.Require(scheme, scopes...)
}

// RouteDescriptor is the live, protocol-agnostic route descriptor backing
// [RouteHandle.Descriptor]/[SSERouteHandle.Descriptor] — see
// [internal/route.Route] for the full doc comment. Named "RouteDescriptor",
// not the bare "Route", because [Route] is already taken by this
// package's own fluent BUILDER type ([Route][Req, Resp]) — the two are
// unrelated: [Route][Req, Resp] is what a caller constructs a route WITH;
// RouteDescriptor is the resulting spec/metadata snapshot [RouteHandle]
// carries.
type RouteDescriptor = route.Route
