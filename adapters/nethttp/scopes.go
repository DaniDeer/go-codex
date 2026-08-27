package nethttp

import (
	"context"
	"net/http"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// RequireScopes builds a security [middleware.Middleware] that is BOTH the
// spec declaration (via Security, consumed by [rest.WithMiddleware]) AND the
// runtime authentication step (via Fn, consumed by [Handler]/[Register]) —
// ONE call produces both, so a route using this never declares
// WithSecurityScheme/RouteMeta.Security separately at all. Pins Raw to
// *http.Request; the ACTUAL logic lives in [middleware.RequireScopes].
//
// extract returns the caller's GRANTED scopes (however obtained — read from
// context set by an upstream net/http middleware translating an OAuth2
// Proxy/Keycloak/Envoy JWT filter's headers, a locally-verified JWT,
// anything) — PURE AUTHENTICATION, nothing more. AUTHORIZATION (the
// mechanical scope-match against the route's declared requirements) is NOT
// done here — it is done ONCE by the adapter, AFTER merging every attached
// security Fn's grants, via [middleware.CheckScopes].
//
// Generic over Req: extract receives the ALREADY-DECODED, ALREADY-MERGED
// request value (as *Req) alongside the raw *http.Request. A caller that
// only needs r/ctx (the common case) simply ignores req.
func RequireScopes[Req any](
	schemeName string,
	scheme route.SecurityScheme,
	scopes []string,
	credentialCodec *codex.Codec[string],
	extract func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error),
) middleware.Middleware {
	return middleware.RequireScopes[*http.Request, Req](schemeName, scheme, scopes, credentialCodec, extract)
}

// RequireAPIKey builds a middleware that contributes a header param
// declaration (headerName) to the route's spec via [rest.WithMiddleware]
// AND verifies the header's value at request time — a worked example of
// [middleware.Middleware.RequestParams] independent of
// [middleware.Middleware.Security] (this middleware declares NO security
// scheme; it is a plain presence/format check, contributing zero grants).
func RequireAPIKey[Req any](headerName string, verify func(ctx context.Context, key string) error) middleware.Middleware {
	return middleware.Middleware{
		Name: "require-api-key",
		RequestParams: []any{
			rest.HeaderParam{Name: headerName, Required: true},
		},
		Fn: func(ctx context.Context, r *http.Request, req *Req) (map[string][]string, error) {
			return nil, verify(ctx, r.Header.Get(headerName))
		},
	}
}
