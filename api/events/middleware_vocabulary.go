package events

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
)

// This file is api/events's own, PUBLIC front door onto the shared,
// internal-only middleware vocabulary (internal/middleware) — part of
// go-codex's "shared cross-pattern MECHANICS live in internal/, pattern-
// specific access lives in the api/port layer" design rule (see
// .github/instructions/go-codex.instructions.md's Design Philosophy
// section, and docs/design/d-0009-internalize-shared-mechanics.md for the full
// rationale). A user of api/events NEVER imports internal/middleware
// directly — everything below is a thin, same-named alias/forwarding
// constructor around it.

// SecurityDeclaration is a COMPLETE, explicit security scheme +
// requirement declaration carried by a [Middleware][In,Out] value's own
// Security field — see [internal/middleware.SecurityDeclaration] for the
// full doc comment. Build via [NewSecurityDeclaration] or (more commonly)
// via [SecurityMiddleware], which constructs one internally from a
// [SecurityScheme] + scopes.
type SecurityDeclaration = middleware.SecurityDeclaration

// NewSecurityDeclaration builds a [SecurityDeclaration] from scheme (this
// package's OWN composite [SecurityScheme], carrying both the spec
// descriptor and an optional Codec).
func NewSecurityDeclaration(schemeName string, scheme SecurityScheme, scopes []string) *SecurityDeclaration {
	return middleware.NewSecurityDeclaration(schemeName, scheme.SecurityScheme, scopes, scheme.Codec)
}

// Declaration pairs a name with an In/Out codec pair — the minimal,
// pattern-agnostic shared core [Middleware][In,Out] embeds. See
// [internal/middleware.Declaration] for the full doc comment.
type Declaration[In, Out any] = middleware.Declaration[In, Out]

// NewDeclaration builds a [Declaration] from a name and an In/Out codec
// pair.
func NewDeclaration[In, Out any](name string, inCodec codex.Codec[In], outCodec codex.Codec[Out]) Declaration[In, Out] {
	return middleware.NewDeclaration(name, inCodec, outCodec)
}

// ContextField declares a typed, codec-validated value threaded through
// request context — see [internal/middleware.ContextField] for the full
// doc comment.
type ContextField[V any] = middleware.ContextField[V]

// NewContextField builds a [ContextField] from a codec.
func NewContextField[V any](c codex.Codec[V]) ContextField[V] {
	return middleware.NewContextField(c)
}

// ContextFieldSetter is the type-erased interface every [ContextField][V]
// satisfies REGARDLESS of V — see
// [internal/middleware.ContextFieldSetter] for the full doc comment.
type ContextFieldSetter = middleware.ContextFieldSetter

// RouteMiddleware is the marker interface any attach-time value
// implements to become `.Use(...)`-able — see
// [internal/middleware.RouteMiddleware] for the full doc comment.
type RouteMiddleware = middleware.RouteMiddleware

// AttachedMiddleware is the raw, non-generic marker value — just a Name
// and an optional Security declaration — satisfying [RouteMiddleware] in
// its simplest form, as attached via `.Use(...)`. Named
// "AttachedMiddleware", not the bare "Middleware", because [Middleware]
// is already taken by this package's own typed GENERIC composite
// ([Middleware][In, Out]) — the two are unrelated: [Middleware][In, Out]
// is what a caller BUILDS via [NewMiddleware]; AttachedMiddleware is the
// type-erased value the router/dispatch machinery actually stores once
// attached. See [internal/middleware.Middleware] for the full doc
// comment.
type AttachedMiddleware = middleware.Middleware

// ServerImplementation is a REGISTER-TIME-ONLY, SERVER-side implementation
// value — see [internal/middleware.ServerImplementation] for the full doc
// comment.
type ServerImplementation = middleware.ServerImplementation

// ClientImplementation is [ServerImplementation]'s client/sending-role
// mirror — see [internal/middleware.ClientImplementation] for the full
// doc comment.
type ClientImplementation = middleware.ClientImplementation

// CheckScopes performs the ONE centralized authorization check verifying
// a merged set of granted scopes satisfies every declared
// [SecurityRequirement] — see [internal/middleware.CheckScopes] for the
// full doc comment.
func CheckScopes(reqs []SecurityRequirement, granted map[string][]string) error {
	return middleware.CheckScopes(reqs, granted)
}

// MiddlewareShapeError indicates a middleware's own Fn doesn't match the
// shape an attachment point expects — e.g. a Subscribe-side Fn attached
// via a Publish-only pairing, or a general-purpose Fn attached where a
// Security-shaped Fn is required. Returned at Register/Attach/Call time
// (never silently dropped) by every adapter's dispatch path — see
// [internal/middleware.MiddlewareShapeError] for the full doc comment.
// This alias exists so a caller can `errors.As(err, &shapeErr)` without
// ever importing internal/middleware directly.
type MiddlewareShapeError = middleware.MiddlewareShapeError

// ContextFieldNotPreparedError is returned by [ContextField.Set] when the
// owning adapter never called the dispatch-internal context-field
// preparation step before a middleware's own Fn tried to write into a
// [ContextField] — see [internal/middleware.ContextFieldNotPreparedError]
// for the full doc comment.
type ContextFieldNotPreparedError = middleware.ContextFieldNotPreparedError

// UnsatisfiedScopesError is returned by [CheckScopes] (and reachable via
// errors.As through [SecurityError], which already implements Unwrap)
// when the combined granted scopes across every attached security Fn do
// not satisfy the route's declared [SecurityRequirement]s — see
// [internal/middleware.UnsatisfiedScopesError] for the full doc comment.
type UnsatisfiedScopesError = middleware.UnsatisfiedScopesError
