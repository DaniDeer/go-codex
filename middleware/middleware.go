// Package middleware provides a shared, declarative enrichment/enforcement
// mechanism attached to a route/channel/tool/port at declaration or call
// time — replacing adapter-specific ad-hoc fields (such as the former
// Options.SecurityFunc/CallOptions.CredentialFunc) with one composable
// vocabulary reused across every boundary go-codex ships.
//
// Three distinct types model the three distinct roles in this vocabulary —
// this package deliberately follows the SAME "declare, then implement, as
// late as possible" discipline every route/channel/tool/port already
// follows (codecs declared once via NewRoute/NewChannel/etc.; the actual
// business handler supplied only later, at Register time):
//
//   - [Middleware] is a DECLARE-TIME-ONLY value — pure data, no Fn field at
//     all. Attached via a route's own declaration (e.g. rest.Route.Use).
//     It is the ONLY type that can contribute to a route's spec
//     (Security/RequestParams/ResponseParams) — "the server declares the
//     contract." [SecurityScheme] builds one from scratch;
//     rest.FromSecurityScheme bridges an existing rest.SecurityScheme
//     value.
//   - [ServerImplementation] is a REGISTER-TIME-ONLY, SERVER-side value —
//     pure runtime behavior, no spec fields at all. Callers never
//     construct one directly: rest.Route.HandleMW(mw, fn) builds it
//     internally from whatever mw/fn it receives — mw non-nil with
//     Security set PAIRS fn against a previously-.Use()'d declaration
//     (Satisfies derived from mw.Security.SchemeName); mw nil (or
//     Security nil) marks a general-purpose implementation (logging,
//     rate limiting, observability, request enrichment) with an empty
//     Satisfies that always runs regardless of the route's declared
//     Security.
//   - [ClientImplementation] is a REGISTER-TIME, CLIENT-side value — the
//     client-side mirror of ServerImplementation. Callers never construct
//     one directly either: rest.Route.ClientMW(mw, fn) builds it
//     internally, with the SAME mw-derived Satisfies-gating discipline —
//     "how does THIS calling application fulfill an already-declared
//     requirement" (e.g. supply a credential), gated to run only when its
//     Satisfies matches the route's declared security requirements.
//
// See docs/design/d-0001-rest-middleware-workflow-simplification.md for the full
// design rationale and resolution history (supersedes the earlier
// docs/roadmap/declarative-middleware.md "Revision 2 — the declare/
// implement split," which introduced Middleware/ServerImplementation's
// split but predates HandleMW/ClientMW's unification described above).
package middleware

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

// Middleware is a named, composable DECLARE-TIME-ONLY value, attached at
// route-declaration time (e.g. via rest.WithMiddleware/Route.Use). It is
// the ONLY type in this package that can contribute to a route's spec —
// see [ServerImplementation] for the server-side runtime counterpart and
// [ClientImplementation] for the client-side one, neither of which can.
//
// Middleware carries NO Fn field — it cannot run anything, ever. This is
// deliberate: mixing a "what does this route require" declaration with
// "how do we verify/handle it" runtime behavior in one value was the
// exact bundling this package's Revision 2 removed (see the former
// RequireScopes/RequireAPIKey/Observability, which no longer
// exist in this bundled shape).
type Middleware struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Security, when non-nil, is a COMPLETE security scheme + requirement
	// declaration for the attaching route/channel — nothing is inferred
	// from this Middleware's mere presence.
	Security *SecurityDeclaration

	// RequestHeaderParams/RequestCookieParams/RequestQueryParams contribute
	// additional request param spec entries this middleware itself needs
	// represented (e.g. an API-key middleware documenting "X-API-Key").
	// Typed — a wrong-shape value is a Go compile error, not a runtime one
	// (see [HeaderParamSpec]'s doc comment for why these are
	// middleware-package-local types, not [rest.HeaderParam] etc.
	// directly). Build one from scratch, or use api/rest's
	// FromHeaderParam/FromCookieParam/FromQueryParam to bridge an
	// existing rest.HeaderParam/CookieParam/QueryParam value.
	RequestHeaderParams []HeaderParamSpec
	RequestCookieParams []CookieParamSpec
	RequestQueryParams  []QueryParamSpec

	// ResponseHeaderParams/ResponseCookieParams mirror the RequestParams
	// fields above for response-side spec contributions. Bridge an
	// existing rest.ResponseHeaderParam/ResponseCookieParam value via
	// api/rest's FromResponseHeaderParam/FromResponseCookieParam.
	ResponseHeaderParams []ResponseHeaderParamSpec
	ResponseCookieParams []ResponseCookieParamSpec
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
	// ServerImplementation Fn runs.
	Codec *codex.Codec[string]
}

// SecurityScheme builds a Middleware carrying ONLY a [SecurityDeclaration]
// — no runtime behavior at all. This is the declare-time half of a
// security requirement; pair it with a [ServerImplementation] (e.g. one
// built by an adapter's Scopes constructor) supplied SEPARATELY, at
// Register/Handler time, to actually enforce it.
//
// Use this directly (with no matching ServerImplementation ever supplied)
// for a route that documents a security requirement WITHOUT an enforcement
// mechanism this codebase provides — typically a route describing an
// EXTERNAL system's API (e.g. a Docker registry) that this codebase calls
// as a client but never implements/serves itself. If such a route is ever
// passed to an adapter's Register/Handler-equivalent WITH NO matching
// ServerImplementation supplied, the adapter's drift-closing coverage
// check correctly rejects it with a MissingSecurityMiddlewareError — this
// is the safety net that makes "declared but not enforced" a loud failure,
// not a silent one, exactly where it can first be detected: at Register
// time, once both the declaration AND the (absent) implementation are
// known.
func SecurityScheme(schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string]) Middleware {
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

// ServerImplementation is a named, composable REGISTER-TIME-ONLY,
// SERVER-side value — the runtime counterpart to [Middleware]. It carries
// NO Security/RequestParams/ResponseParams fields — it cannot contribute
// to a route's spec, ever; the spec is ALWAYS declared separately, via
// [Middleware] (see [SecurityScheme]).
//
// Built internally by rest.Route.HandleMW(mw, fn) — never constructed
// directly by callers. mw non-nil with Security set derives Satisfies
// from mw.Security.SchemeName (the PAIRED, security-verifying case,
// matched against a previously-.Use()'d declaration); mw nil (or Security
// nil) leaves Satisfies empty (the UNPAIRED, general-purpose case —
// logging, rate limiting, observability, request enrichment — that runs
// unconditionally regardless of whether the route declares any Security
// at all).
//
// Fn is deliberately untyped (any) — resolved by the SPECIFIC adapter
// function that consumes it, mirroring the type-erasure + call-site-
// assertion idiom already used elsewhere in this codebase (e.g.
// [ports.Pattern]'s CustomFormat). A ServerImplementation built for the
// wrong adapter/role fails LOUDLY with a typed [MiddlewareShapeError] at
// Register time — never silently.
type ServerImplementation struct {
	// Name identifies this implementation in errors and observability.
	Name string

	// Satisfies lists the security scheme name(s) (matching a
	// SecurityScheme/SecurityDeclaration name) this Fn VERIFIES. EMPTY
	// means general-purpose — Fn runs unconditionally, regardless of
	// whether the route declares any Security at all (this is how
	// logging/observability/rate-limiting/presence-only checks are
	// expressed, unified with security verification under this ONE
	// register-time type instead of two separate mechanisms).
	Satisfies []string

	// Fn is the adapter-specific closure. Never called directly by this
	// package. Two concrete shapes exist for adapters/nethttp+chi:
	// general-purpose func(http.Handler) http.Handler (Satisfies empty),
	// and security-verifying func(ctx, raw *http.Request, req *Req)
	// (map[string][]string, error) (Satisfies non-empty).
	Fn any
}

// ClientImplementation is a named, composable REGISTER-TIME, CLIENT-side
// value — the client-side mirror of [ServerImplementation]. It answers a
// DIFFERENT question than either server-side type does: not "what does
// this route require, and how do we verify it," but "how does THIS
// calling application fulfill an already-declared requirement" (e.g.
// supply a credential the server — or an external system this codebase
// merely calls — expects).
//
// Built internally by rest.Route.ClientMW(mw, fn) — never constructed
// directly by callers. mw non-nil with Security set derives Satisfies
// from mw.Security.SchemeName, GATING this implementation to run only
// when the route's declared security requirements include that scheme;
// mw nil (or Security nil) leaves Satisfies empty — general-purpose,
// always runs.
//
// Deliberately has NO Security/RequestParams/ResponseParams fields — a
// ClientImplementation can NEVER contribute to a route's spec. The spec
// is ALWAYS declared server-side, via [Middleware] (see [SecurityScheme]).
//
// Fn is deliberately untyped (any) for the SAME reason as
// [ServerImplementation.Fn] — resolved by the specific client adapter
// function that consumes it. adapters/mqtt5/mqtt/zeromq's Publish and
// adapters/nethttp's Call/CallWithHandle each recognize TWO concrete
// shapes: the credential-providing shape (satisfies-gated, per Satisfies
// above) and a general-purpose wrapping shape that composes around the
// adapter's own "encode and transmit"/"network round-trip" step,
// unconditionally, in attachment order (see
// docs/roadmap/rest-client-general-purpose-middleware.md for the REST
// side and adapters/mqtt5/adapter.go's wrapPublishGeneral for the
// pub/sub precedent it mirrors). adapters/nethttp's SSE
// Consume/CallSSEAdapter recognizes only the credential shape — its
// per-event dispatch shape doesn't match the general-purpose wrap shape.
// A ClientImplementation built for the wrong adapter/role fails LOUDLY
// with a typed [MiddlewareShapeError] at Call time — never silently.
type ClientImplementation struct {
	// Name identifies this implementation in errors and observability.
	Name string

	// Satisfies lists the security scheme name(s) this Fn supplies a
	// credential for. EMPTY means general-purpose — Fn runs
	// unconditionally.
	Satisfies []string

	// Fn is the adapter-specific closure. Never called directly by this
	// package.
	Fn any
}

// MiddlewareShapeError is returned when a [ServerImplementation.Fn]'s or
// [ClientImplementation.Fn]'s concrete type doesn't match what the consuming
// adapter/role expects (e.g. a general-purpose func(http.Handler)
// http.Handler value passed where a security-specific closure was
// required, or vice versa).
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
