package middleware

// BoundRouteBuilder is the shared, narrow interface every pattern's
// Bound*Middleware attach method (api/rest's BoundMiddleware/
// BoundClientMiddleware, api/events' BoundSubscribeMiddleware/
// BoundPublishMiddleware, api/reqreply's BoundMiddleware/
// BoundClientMiddleware) calls through, instead of touching each
// pattern's own internal declare-time builder/role type (routeBuilder/
// Subscriber[T]/Publisher[T]) fields directly.
//
// Found via direct investigation (docs/roadmap/shared-api-layer-
// mechanics.md's Phase 2): despite each builder/role type being mutated
// by 20-33+ unrelated declare-time call sites overall, a Bound attach's
// OWN footprint on it is only these 4 operations — a narrow, stable
// surface, not a leaky whole-builder exposure. This narrow interface is
// what would have made api/events' Security-declaration-population gap
// (found and fixed as a direct result of this same investigation — see
// api/events/bound_middleware.go's applyBoundSubscriber/
// applyBoundPublisher) structurally impossible to introduce: a single
// shared code path enforces all 4 steps identically, rather than each
// pattern separately remembering to replicate them by hand.
//
// AppendSpecContribution is a NO-OP for a pattern with no spec-
// contribution concept of its own (api/events — its MiddlewareHandler
// already carries every spec-relevant field directly, unlike api/rest's/
// api/reqreply's separate middlewareSpecContribution list; see that
// package's own BoundRouteBuilder implementation for the full rationale).
type BoundRouteBuilder interface {
	// AppendMiddlewareHandler appends a server/receiving-role dispatch
	// handler (concretely a [MiddlewareHandler]-shaped value — type-
	// erased here since Go forbids a shared interface method from naming
	// a type that doesn't exist in this package).
	AppendMiddlewareHandler(h any)
	// AppendClientMiddlewareHandler appends a client/sending-role
	// dispatch handler (concretely a [ClientMiddlewareHandler]-shaped
	// value) — only meaningful where a client/sending role exists.
	AppendClientMiddlewareHandler(h any)
	// AppendSpecContribution appends a spec-relevant param contribution
	// (concretely a middlewareSpecContribution-shaped value, pattern-
	// internal) — a no-op for patterns with no such concept.
	AppendSpecContribution(c any)
	// AppendSecurityDeclaration records a Security-carrying attachment's
	// declaration under name, so the pattern's own coverage check/spec
	// renderer can see it exactly as a plain `.Use()`-attached
	// [Middleware] would.
	AppendSecurityDeclaration(name string, sec *SecurityDeclaration)
}

// BoundContributor is satisfied by a pattern's own Bound*Middleware
// RECEIVING/SERVER-role type (rest.BoundMiddleware, events.
// BoundSubscribeMiddleware, reqreply.BoundMiddleware) for its own Req
// type parameter only — the EXPORTED equivalent of each package's former,
// independently-defined, structurally-identical unexported
// boundContributor[Req]/[T] interface.
//
// BoundReqWitness is a DELIBERATE, never-called no-op method whose SOLE
// purpose is making Req appear in a method SIGNATURE — without it,
// ApplyBoundRoute's signature (func(rb BoundRouteBuilder)) never mentions
// Req at all, so EVERY BoundMiddleware[X,...] would satisfy
// BoundContributor[Y] for ANY X, Y (a real bug, originally caught only by
// an actual mismatch test in api/rest's Phase A implementation — carried
// forward here from the start in every pattern, not rediscovered per
// pattern).
type BoundContributor[Req any] interface {
	ApplyBoundRoute(rb BoundRouteBuilder)
	BoundReqWitness(Req)
}

// BoundClientContributor is [BoundContributor]'s SENDING/CLIENT-role
// mirror (rest.BoundClientMiddleware, events.BoundPublishMiddleware,
// reqreply.BoundClientMiddleware) — satisfied for its own Req only.
type BoundClientContributor[Req any] interface {
	ApplyBoundClientRoute(rb BoundRouteBuilder)
	BoundReqWitness(Req)
}

// BoundNamed is a Req-FREE interface a bound middleware's name can be
// extracted through even when it's the WRONG Req (so a pattern's own
// req-mismatch error can still name the middleware, when possible).
type BoundNamed interface {
	MiddlewareName() string
}

// BoundNameOf extracts bm's name via the Req-free [BoundNamed] interface,
// returning "" when bm doesn't implement it.
func BoundNameOf(bm any) string {
	if n, ok := bm.(BoundNamed); ok {
		return n.MiddlewareName()
	}
	return ""
}
