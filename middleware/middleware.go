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
//     construct one directly: rest.Route.HandleMW(mw, fn)/
//     events.Subscriber.SubscribeMW/reqreply.Route.HandleMW build it
//     internally from whatever mw/fn they receive. mw nil marks a
//     GENERAL-PURPOSE implementation (logging, rate limiting,
//     observability, request enrichment) with an empty Satisfies that
//     always runs regardless of the route's declared Security — this is
//     the ONLY remaining accepted shape for a non-nil mw's Security field
//     too: a Security-carrying mw is now REJECTED outright (see
//     docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 8) — the former
//     "mw non-nil with Security set PAIRS fn against a previously-
//     .Use()'d declaration" mechanism was retired in favor of
//     BoundSecurityMiddleware/HandleBoundMW (and its SubscribeBoundMW/
//     reqreply equivalents), which fuse declare+implement into one call.
//   - [ClientImplementation] is a REGISTER-TIME, CLIENT-side value — the
//     client-side mirror of ServerImplementation, with the IDENTICAL
//     retirement: rest.Route.ClientMW/events.Publisher.PublishMW/
//     reqreply.Route.ClientMW accept ONLY a nil-mw, general-purpose
//     implementation now — a Security-carrying mw is rejected; supply a
//     client-side credential via BoundSecurityClientMiddleware +
//     ClientBoundMW (and its PublishBoundMW/reqreply equivalent) instead.
//
// See docs/design/d-0001-rest-middleware-workflow-simplification.md for the full
// design rationale and resolution history (supersedes the earlier,
// now-deleted docs/roadmap/declarative-middleware.md "Revision 2 — the
// declare/implement split," which introduced Middleware/ServerImplementation's
// split but predates HandleMW/ClientMW's unification described above).
package middleware

import (
	"fmt"
	"log/slog"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

// RouteMiddleware is a marker interface any attach-time middleware value
// can implement to become passable to a route/channel's own .Use(...)
// method. [Middleware] implements it via [Middleware.RouteMiddlewareMarker]
// below — EVERY existing .Use(someSecurityScheme)-style call site in this
// codebase keeps compiling unchanged, since Go's structural typing already
// satisfies the interface. [Declaration]-derived, per-pattern generic
// types (such as api/rest's Middleware[In, Out]) implement it too — see
// docs/design/d-0003-codec-declared-middlewares.md for the full design
// this interface exists to support: a codec-backed, per-pattern middleware
// declaration mechanism layered ADDITIVELY alongside this package's
// existing, unchanged Middleware/SecurityScheme machinery.
//
// The marker method is EXPORTED, unlike [ports.Pattern]'s own
// unexported-method sealing technique — deliberately so, since Go's
// unexported-method interface satisfaction is scoped PER PACKAGE: a type
// declared in api/rest (or api/events) can NEVER satisfy an interface
// whose unexported method is declared in package middleware, regardless
// of the method's name matching textually (confirmed the hard way — an
// earlier revision of this design used an unexported isRouteMiddleware(),
// which compiled right up until an actual cross-package value was passed
// to .Use(), at which point Go correctly rejected it as a distinct,
// package-scoped identifier). RouteMiddleware therefore trades strict
// sealing for the ability to be implemented from ANY package — an
// acceptable trade since this is an additive attachment marker, not a
// closed value space that needs exhaustive-switch safety.
type RouteMiddleware interface{ RouteMiddlewareMarker() }

// RouteMiddlewareMarker makes Middleware satisfy [RouteMiddleware] — added
// purely so a route/channel's .Use(...) parameter type can widen from
// ...Middleware to ...RouteMiddleware without breaking any existing call
// site. Carries no behavior.
func (Middleware) RouteMiddlewareMarker() {}

// SecurityCarrier is implemented by any [RouteMiddleware] value that MAY
// carry a [SecurityDeclaration] — [Middleware] (legacy) and every
// per-pattern codec-backed `Middleware[In, Out]` (api/rest/api/events/
// api/reqreply, via their own embedded [Declaration]) all implement it,
// letting the shared pairing dispatch (HandleMW/ClientMW/SubscribeMW/
// PublishMW) extract a Security declaration UNIFORMLY, without a
// per-concrete-type switch — part of the middleware-consolidation effort
// (docs/design/d-0003-codec-declared-middlewares.md) folding Security into the
// codec-backed family. A plain [RouteMiddleware] value that does NOT
// implement this (e.g. a future non-Security-capable attachment) is
// always treated as general-purpose (no Security) by the type-assertion
// callers use — see [Middleware.SecurityDeclaration].
type SecurityCarrier interface {
	RouteMiddleware
	SecurityDeclaration() *SecurityDeclaration
}

// SecurityDeclaration makes Middleware satisfy [SecurityCarrier] —
// returns the legacy value's own Security field directly.
func (m Middleware) SecurityDeclaration() *SecurityDeclaration { return m.Security }

// Declaration is a minimal, pattern-agnostic DECLARE-TIME-ONLY core for a
// codec-backed middleware: a name plus an Input and Output codec, exactly
// mirroring how a route/channel itself declares its Req/Resp (or Item)
// shape via a codec — giving a middleware declaration the SAME
// self-documenting/schema-able/validated status every other Layer 2
// declaration already has.
//
// Declaration is deliberately NOT itself attachable via .Use(...) — it
// carries no per-pattern merge-field vocabulary (REST's header/cookie/
// query extraction, say) and no Fn. Each API pattern embeds Declaration
// inside its OWN generic type (e.g. api/rest's Middleware[In, Out]) that
// adds exactly the merge machinery relevant to that pattern's boundary —
// see docs/design/d-0003-codec-declared-middlewares.md for the full
// design — originally motivated by the now-deleted
// docs/roadmap/common-middleware-architecture.md's finding (superseded
// by that doc): a single shared middleware type carrying pattern-specific
// fields unused by every OTHER pattern importing it.
//
// [SecurityDeclaration] is intentionally NOT retrofitted onto Declaration
// — security's shape (a raw credential string, a route.SecurityScheme,
// scopes) is protocol-driven and already shipped/stable; Declaration is
// reserved for NEW, non-security cross-cutting concerns (the first being
// REST response-cookie policy attributes).
type Declaration[In, Out any] struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// InCodec validates/schemas the middleware's own input value —
	// independent of any route/channel's own Req codec.
	InCodec codex.Codec[In]

	// OutCodec validates/schemas the middleware's own output value —
	// independent of any route/channel's own Resp codec.
	OutCodec codex.Codec[Out]

	// Security, when non-nil, is a COMPLETE security scheme + requirement
	// declaration for the attaching route/channel — nothing is inferred
	// from this Declaration's mere presence. Mirrors [Middleware.Security]
	// exactly; folded in here (not re-declared per-pattern) so
	// rest.Middleware[In,Out]/events.Middleware[In,Out]/
	// reqreply.Middleware[In,Out] all gain it for free via embedding.
	// Security-only values use In=Out=struct{} (no var-boundary to
	// decode) — see [NewSecurityDeclaration].
	Security *SecurityDeclaration
}

// NewDeclaration builds a [Declaration] from a name and its Input/Output
// codecs.
func NewDeclaration[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out]) Declaration[In, Out] {
	return Declaration[In, Out]{Name: name, InCodec: inCodec, OutCodec: outCodec}
}

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
//
// Middleware is now SECURITY-ONLY — the middleware-consolidation effort
// (docs/design/d-0006-protocol-native-capabilities.md) removed its former
// RequestHeaderParams/RequestCookieParams/RequestQueryParams/
// ResponseHeaderParams/ResponseCookieParams fields (and the
// FromHeaderParam/FromCookieParam/FromQueryParam/FromResponseHeaderParam/
// FromResponseCookieParam constructors that built them), fully replaced
// by each per-pattern codec-backed Middleware[In,Out] type's own
// WithRequestHeaderSpec/WithRequestCookieSpec/WithRequestQuerySpec/
// WithResponseHeaderSpec/WithResponseCookieSpec methods.
//
// This type survives, permanently, as the ONLY mechanism that lets a
// SINGLE Go value be attached to routes/channels across MULTIPLE
// patterns (REST/events/reqreply) — a per-pattern codec-backed
// Middleware[In,Out] value cannot be shared this way: each pattern's
// internal dispatch only recognizes its own concrete type, so a foreign
// pattern's value would compile (both satisfy [RouteMiddleware]) but be
// silently dropped, contributing nothing. See [SecurityScheme] and
// docs/features/security.md's "Sharing a security SCHEME across
// REST/events/reqreply" section for the (now config-level, not
// value-level) replacement guarantee this enables.
type Middleware struct {
	// Name identifies this middleware in errors and observability.
	Name string

	// Security, when non-nil, is a COMPLETE security scheme + requirement
	// declaration for the attaching route/channel — nothing is inferred
	// from this Middleware's mere presence.
	Security *SecurityDeclaration
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

// NewSecurityDeclaration builds a [SecurityDeclaration] value directly —
// the codec-backed-family equivalent of [SecurityScheme], returning just
// the declaration (not a full legacy [Middleware]) for attaching to a
// [Declaration]'s own Security field. Each API pattern's own
// SecurityMiddleware-style constructor (e.g. rest.SecurityMiddleware)
// wraps this into its own Middleware[struct{}, struct{}] value.
func NewSecurityDeclaration(schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string]) *SecurityDeclaration {
	return &SecurityDeclaration{
		SchemeName: schemeName,
		Scheme:     scheme,
		Scopes:     scopes,
		Codec:      codec,
	}
}

// SecurityScheme builds a Middleware carrying ONLY a [SecurityDeclaration]
// — no runtime behavior at all. This is the declare-time half of a
// security requirement, attached via a route/channel's own `.Use(...)`.
//
// Per docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 8, there is no
// longer a way to PAIR this declaration with a server/client
// implementation via HandleMW/ClientMW/SubscribeMW/PublishMW — those now
// reject a Security-carrying value outright. Use
// BoundSecurityMiddleware/BoundSecurityClientMiddleware (each api
// pattern's own) + HandleBoundMW/ClientBoundMW/SubscribeBoundMW/
// PublishBoundMW instead, which fuse declare+implement into ONE call —
// or `rest.SecurityMiddleware`/`events.SecurityMiddleware`/
// `reqreply.SecurityMiddleware` (the per-pattern, codec-backed
// equivalent of THIS constructor) for the reusable, `.Use()`-only
// declare-time style.
//
// This constructor's own remaining purpose is purely a route/channel that
// documents a security requirement WITHOUT any enforcement mechanism this
// codebase provides — typically describing an EXTERNAL system's API
// (e.g. a Docker registry) that this codebase calls as a client but never
// implements/serves itself — AND the rarer case of a single declared
// value genuinely needing to be `.Use()`'d across MULTIPLE different api
// patterns in the same program (the one thing a per-pattern
// SecurityMiddleware[In,Out] value structurally cannot do, since each
// pattern's own generic type is foreign to every other pattern's `.Use()`
// — see this package's own doc comment for [RouteMiddleware]). If such a
// route is ever passed to an adapter's Register/Handler-equivalent with
// no matching implementation supplied, the adapter's drift-closing
// coverage check correctly rejects it with a
// MissingSecurityMiddlewareError.
func SecurityScheme(schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string]) Middleware {
	return Middleware{
		Name:     "declare-security:" + schemeName,
		Security: NewSecurityDeclaration(schemeName, scheme, scopes, codec),
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
// adapters/nethttp's internal call dispatch each recognize TWO concrete
// shapes: the credential-providing shape (satisfies-gated, per Satisfies
// above) and a general-purpose wrapping shape that composes around the
// adapter's own "encode and transmit"/"network round-trip" step,
// unconditionally, in attachment order (see
// docs/design/d-0001-rest-middleware-workflow-simplification.md for the REST
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
// every attached security Fn's extracted grants — never per-Fn. Calling it
// per-Fn instead (each Fn checking only its OWN grants) is INCORRECT for an
// AND-combined security requirement spanning multiple schemes: no single
// Fn's own grants would ever satisfy the combined requirement, even when
// every Fn succeeds — see
// docs/design/d-0001-rest-middleware-workflow-simplification.md for the
// full resolution history of this exact bug.
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
