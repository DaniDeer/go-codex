// Package middleware provides a shared, declarative enrichment/enforcement
// mechanism attached to a route/channel/tool/port at declaration or call
// time — replacing adapter-specific ad-hoc fields (such as the former
// Options.SecurityFunc/CallOptions.CredentialFunc) with one composable
// vocabulary reused across every boundary go-codex ships.
//
// See docs/roadmap/declarative-middleware.md for the full design rationale.
package middleware

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

// Middleware is a named, composable enrichment/enforcement unit, attached at
// Register (server) or Call (client) time.
//
// Fn is deliberately untyped (any) — resolved by the SPECIFIC adapter
// function that consumes it, mirroring the type-erasure + call-site-
// assertion idiom already used elsewhere in this codebase (e.g.
// [ports.Pattern]'s CustomFormat). A Middleware built for the wrong
// adapter/role fails LOUDLY with a typed [MiddlewareShapeError] at
// Register/Call time — never silently.
type Middleware struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Fn is the adapter/role-specific closure. Never called directly by
	// this package.
	Fn any

	// Satisfies lists the security scheme names (matching a
	// WithSecurityScheme-declared name) this middleware ENFORCES. Empty for
	// non-security middleware (logging, rate limiting, etc.).
	Satisfies []string

	// Security, when non-nil, is a COMPLETE security scheme + requirement
	// declaration for the attaching route/channel — nothing is inferred
	// from this Middleware's mere presence. Only meaningful when attached
	// via a spec-relevant RouteOpt/ChannelOpt (e.g. rest.WithMiddleware);
	// ignored, with no effect, if attached at a call-time-only point after
	// the spec has already been frozen.
	Security *SecurityDeclaration

	// RequestParams contributes additional header/cookie/query param spec
	// entries this middleware itself needs represented (e.g. an API-key
	// middleware documenting "X-API-Key"). Type-erased (any), resolved by
	// the consuming api/* package's own param types. Only meaningful via a
	// spec-relevant attachment point, same caveat as Security.
	RequestParams []any

	// ResponseParams mirrors RequestParams for response-side spec
	// contributions.
	ResponseParams []any
}

// SecurityDeclaration is a COMPLETE, explicit security scheme + requirement
// declaration carried by a Middleware value. Every field is supplied by the
// caller as ordinary constructor arguments — nothing is inferred from the
// Middleware's mere presence.
type SecurityDeclaration struct {
	// SchemeName is the scheme's name in the OpenAPI/AsyncAPI
	// components.securitySchemes map.
	SchemeName string

	// Scheme is the scheme's spec metadata (e.g. route.BearerScheme("JWT")).
	Scheme route.SecurityScheme

	// Scopes are the scopes this declaration requires for the attached
	// route — becomes one route.SecurityRequirement entry.
	Scopes []string

	// Codec, when non-nil, format-validates the raw credential before any
	// Fn runs.
	Codec *codex.Codec[string]
}

// MiddlewareShapeError is returned when a Middleware.Fn's concrete type
// doesn't match what the consuming adapter/role expects (e.g. a
// general-purpose func(http.Handler) http.Handler value passed where a
// security-specific closure was required, or vice versa).
type MiddlewareShapeError struct {
	Name     string
	Expected string
	Got      string
}

func (e MiddlewareShapeError) Error() string {
	return fmt.Sprintf("middleware: %q: expected Fn shape %s, got %s", e.Name, e.Expected, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e MiddlewareShapeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("name", e.Name),
		slog.String("expected", e.Expected),
		slog.String("got", e.Got),
	)
}

// RequireScopes builds a security Middleware that is BOTH the spec
// declaration (via Security) AND the runtime authentication step (via Fn).
// extract is PURE AUTHENTICATION — it returns the caller's granted scopes
// however obtained; it does NOT decide pass/fail against a route's declared
// requirements (no []route.SecurityRequirement parameter). Authorization (the
// mechanical scope-match) is performed exactly once by the consuming
// adapter, AFTER merging every attached security Fn's grants, via
// [CheckScopes] — see docs/roadmap/declarative-middleware.md's "L4" for why
// this split is necessary.
//
// Raw is the adapter's raw wire-request type (e.g. *http.Request for
// nethttp/chi). Req is the route/channel's decoded, already-merged request
// type — extract receives it as a pointer so it can also WRITE a
// derived/verified field back onto it.
func RequireScopes[Raw, Req any](
	schemeName string,
	scheme route.SecurityScheme,
	scopes []string,
	credentialCodec *codex.Codec[string],
	extract func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error),
) Middleware {
	return Middleware{
		Name:      "require-scopes:" + schemeName,
		Satisfies: []string{schemeName},
		Security: &SecurityDeclaration{
			SchemeName: schemeName,
			Scheme:     scheme,
			Scopes:     scopes,
			Codec:      credentialCodec,
		},
		Fn: func(ctx context.Context, raw Raw, req *Req) (map[string][]string, error) {
			return extract(ctx, raw, req)
		},
	}
}

// CheckScopes reports an error unless granted satisfies reqs, via
// [route.Satisfied]. Called ONCE by the consuming adapter after merging
// every attached security Fn's extracted grants — never per-Fn (see "L4" in
// docs/roadmap/declarative-middleware.md).
func CheckScopes(reqs []route.SecurityRequirement, granted map[string][]string) error {
	if route.Satisfied(reqs, granted) {
		return nil
	}
	return UnsatisfiedScopesError{Requirements: reqs, Granted: granted}
}

// UnsatisfiedScopesError is returned by [CheckScopes] when the combined
// granted scopes across every attached security Fn do not satisfy the
// route's declared requirements.
type UnsatisfiedScopesError struct {
	Requirements []route.SecurityRequirement
	Granted      map[string][]string
}

func (e UnsatisfiedScopesError) Error() string {
	return fmt.Sprintf("middleware: unsatisfied security requirements: need %v, granted %v", e.Requirements, e.Granted)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e UnsatisfiedScopesError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("requirements", e.Requirements),
		slog.Any("granted", e.Granted),
	)
}
