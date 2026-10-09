// Package middleware provides a declarative, composable enrichment/enforcement
// vocabulary shared across [api/rest], [api/events], and [api/reqreply] — it
// replaces the old, adapter-specific `Options.SecurityFunc`/
// `CallOptions.CredentialFunc` escape-hatch fields with ONE codec-backed
// declaration pattern every transport understands identically.
//
// DELIBERATELY scoped under internal/ (not the repo root) — this package
// is shared cross-pattern MECHANICS/vocabulary, never meant to be imported
// by an end user of go-codex directly. A user of api/rest/api/events/
// api/reqreply uses that pattern's OWN public wrapper around this
// vocabulary instead — e.g. rest.Middleware/rest.NewDeclaration/
// rest.SecurityDeclaration/rest.ContextField (events/reqreply have the
// SAME names) are thin, same-named aliases/forwarding constructors around
// this package's own types. Go's own internal/ import rule enforces this
// structurally: nothing outside the go-codex module tree can import this
// package at all, regardless of documentation/convention. See
// .github/instructions/go-codex.instructions.md's Design Philosophy
// section for the general rule this package is a reference example of.
//
// # Middleware — codec-declared Transform
//
// [Declaration] pairs a name with an In/Out codec pair (built via
// [NewDeclaration]); a [Middleware] value wraps a Declaration plus the
// concrete implementation (server-side [ServerImplementation], client-side
// [ClientImplementation]) attached at a route/channel declaration site.
// [RouteMiddleware] is the marker interface a [Middleware] satisfies so it
// can be passed directly as a route/channel option.
//
// # Security schemes
//
// [SecurityScheme] builds a [Middleware] from a [route.SecurityScheme] —
// the same bearer/apiKey/oauth2/openIdConnect vocabulary [route.SecurityScheme]
// already describes for OpenAPI/AsyncAPI specs, now paired with a runtime
// implementation Fn. [CheckScopes] verifies a security implementation's
// granted scopes satisfy a route's declared [route.SecurityRequirement]s,
// returning [UnsatisfiedScopesError] on a gap. [MiddlewareShapeError] is
// returned when an attached implementation Fn does not match one of its
// two recognized shapes (security-shaped or general-purpose wrapping).
//
// # Disposition — retry/requeue signaling
//
// Disposition (a small, transport-agnostic enum letting a handler signal
// "retry this message" or "dead-letter this message" back to its adapter
// without a transport-specific escape hatch) lives in [stats] now, not
// here — stats.Disposition/stats.SetDisposition/stats.DispositionFromContext/
// stats.ResolveDisposition. Relocated because it's consumed by
// stats.DispositionObserver, a PUBLIC, user-implementable extensibility
// interface with no per-pattern home — keeping it here would have blocked
// this package's own internal/ move (an external user implementing a
// custom Observer could no longer have named the type at all).
//
// # Context fields
//
// [ContextField] declares a typed, codec-validated value threaded through
// request/message context — [NewContextField] builds one, [EnsureContextFields]
// prepares a context to carry them, and [ContextField.Set]/[ContextField.Get]
// read/write the value. [ContextFieldNotPreparedError] is returned when Set/Get
// is called against a context [EnsureContextFields] never touched.
//
// # Param specs
//
// [HeaderParamSpec], [CookieParamSpec], [QueryParamSpec],
// [ResponseHeaderParamSpec], and [ResponseCookieParamSpec] are the plain,
// transport-agnostic shapes api/rest's codec-backed Middleware
// (WithRequestHeaderSpec/WithRequestCookieSpec/WithRequestQuerySpec/
// WithResponseHeaderSpec/WithResponseCookieSpec) and api/reqreply's
// RouteHandle.RequestHeaderParams/ResponseHeaderParams runtime fields
// build on.
package middleware
