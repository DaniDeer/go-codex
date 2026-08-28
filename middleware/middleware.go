// Package middleware provides a shared, declarative enrichment/enforcement
// mechanism attached to a route/channel/tool/port at declaration or call
// time — replacing adapter-specific ad-hoc fields (such as the former
// Options.SecurityFunc/CallOptions.CredentialFunc) with one composable
// vocabulary reused across every boundary go-codex ships.
//
// Two distinct types model the two distinct roles in this vocabulary:
//
//   - [Middleware] is a SERVER-side declaration — attached via a route's
//     own declaration RouteOpt (e.g. rest.WithMiddleware/Route.Use). It is
//     the ONLY type that can contribute to a route's spec (Security/
//     RequestParams/ResponseParams) — "the server declares the contract."
//     [RequireScopes] builds one carrying BOTH a Security declaration AND
//     a verifying Fn (a route THIS codebase serves and enforces);
//     [DeclareSecurity] builds one carrying Security ONLY, no Fn (a route
//     describing an requirement this codebase does NOT enforce — e.g. an
//     external system's API this codebase merely calls as a client).
//   - [ClientMiddleware] is a CLIENT-side declaration — attached via
//     Route.UseClient. It can NEVER contribute to the spec (the type has
//     no Security/RequestParams/ResponseParams fields at all) — its only
//     job is "how does THIS calling application fulfill an
//     already-declared requirement" (e.g. supply a credential). The same
//     already-declared Security, however it was declared, is what a
//     ClientMiddleware's Fn is expected to satisfy.
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

// Middleware is a named, composable SERVER-side enrichment/enforcement
// unit, attached at Register time (e.g. via rest.WithMiddleware/Route.Use).
// It is the ONLY type in this package that can contribute to a route's
// spec — see [ClientMiddleware] for the client-side counterpart, which
// deliberately cannot.
//
// Fn is deliberately untyped (any) — resolved by the SPECIFIC adapter
// function that consumes it, mirroring the type-erasure + call-site-
// assertion idiom already used elsewhere in this codebase (e.g.
// [ports.Pattern]'s CustomFormat). A Middleware built for the wrong
// adapter/role fails LOUDLY with a typed [MiddlewareShapeError] at
// Register time — never silently.
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

// ClientMiddleware is a named, composable CLIENT-side unit, attached at
// Call time (e.g. via Route.UseClient) — the counterpart to [Middleware].
// It answers a DIFFERENT question than Middleware does: not "what does
// this route require, and how do we verify it," but "how does THIS
// calling application fulfill an already-declared requirement" (e.g.
// supply a credential the server — or an external system this codebase
// merely calls — expects).
//
// Deliberately has NO Security/RequestParams/ResponseParams fields — a
// ClientMiddleware can NEVER contribute to a route's spec. The spec is
// ALWAYS declared server-side, via [Middleware] (see [RequireScopes] for
// "this codebase enforces it" and [DeclareSecurity] for "this codebase
// merely documents an external requirement it doesn't enforce").
//
// Fn is deliberately untyped (any) for the SAME reason as
// [Middleware.Fn] — resolved by the specific client adapter function that
// consumes it (e.g. nethttp.Call's credential-providing shape). A
// ClientMiddleware built for the wrong adapter/role fails LOUDLY with a
// typed [MiddlewareShapeError] at Call time — never silently.
type ClientMiddleware struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Fn is the adapter-specific closure. Never called directly by this
	// package.
	Fn any
}

// DeclareSecurity builds a spec-only [Middleware] — a [SecurityDeclaration]
// with NO Fn and an empty Satisfies. Use this for a route that documents a
// security requirement WITHOUT an enforcement mechanism this codebase
// provides — typically a route describing an EXTERNAL system's API (e.g.
// a Docker registry) that this codebase calls as a client but never
// implements/serves itself. Attach via [rest.WithMiddleware]/
// [rest.Route.Use] exactly like [RequireScopes]'s output; the only
// difference is the absent Fn/Satisfies.
//
// If a route using ONLY a DeclareSecurity-built Middleware is ever passed
// to Register() (i.e. someone DOES try to serve it), the drift-closing
// coverage check already run there correctly rejects it with
// MissingSecurityMiddlewareError — an empty Satisfies never covers a
// declared requirement. This is a deliberate safety net: DeclareSecurity
// is for client-only routes; a route that gains a real server
// implementation must switch to [RequireScopes] (or an equivalent
// carrying a real Fn) instead.
func DeclareSecurity(schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string]) Middleware {
	return Middleware{
		Name: "declare-security:" + schemeName,
		Security: &SecurityDeclaration{
			SchemeName: schemeName,
			Scheme:     scheme,
			Scopes:     scopes,
			Codec:      codec,
		},
	}
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
