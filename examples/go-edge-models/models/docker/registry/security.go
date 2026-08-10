package registry

import (
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// bearerAuthSecurity/bearerAuthScheme are part of GetTagsRoute/
// GetManifestRoute's DECLARED CONTRACT — which auth scheme those routes
// require — so they live here, in the declarative package, rather than
// alongside the concrete auth FLOW implementation (challenge parsing,
// token exchange, credential injection), which lives in app/registry's
// own auth.go. basicAuthSecurity/basicAuthScheme (used only by
// getTokenRoute, the token-exchange endpoint) stay in app/registry
// instead — getTokenRoute has no legitimate standalone caller outside
// that package's own authenticate() function, so it is auth-flow
// plumbing, not part of this package's externally-facing contract.

// bearerAuthSecurity declares that a route requires Bearer-token
// credentials — set as RouteMeta.Security on GetTagsRoute/GetManifestRoute
// so [nethttp.CallOptions.CredentialFunc] is invoked automatically by
// [nethttp.Call]/[nethttp.CallHandle], instead of the caller having to set
// the Authorization header by hand via CallOptions.ExtraHeaders.
var bearerAuthSecurity = []route.SecurityRequirement{{"bearerAuth": nil}}

// bearerAuthScheme declares the "bearerAuth" scheme's spec metadata and a
// non-empty-string format Codec, attached to GetTagsRoute/GetManifestRoute
// via rest.WithSecurityScheme — the ONLY way to declare a security scheme
// in go-codex (no Builder/spec involved here at all; WithSecurityScheme is
// a route-level RouteOpt, so it works identically through .ClientHandle()
// as it would through .Register(builder)). This gives app/registry's
// newAuthCredentialFunc a genuine extra safety net: nethttp.Call validates
// its returned Authorization header's bare token against this Codec
// before sending, on top of (not instead of) the fact that
// formatBearerToken/internal.BearerTokenCodec.Encode already construct
// that header from a codec — this catches an empty token specifically,
// which the encode-side codec alone does not.
var bearerAuthScheme = rest.SecurityScheme{
	SecurityScheme: route.BearerScheme(""),
}.WithCodec(c.String().Refine(validate.NonEmptyString))
