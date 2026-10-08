package rest

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/DaniDeer/go-codex/middleware"
)

// routable is satisfied by every leaf type a [Router] can hold — currently
// [Route]/[SSERoute] — regardless of its own Req/Resp type parameters. Go
// forbids a method from introducing new type parameters beyond its
// receiver's own, so Router.Route cannot itself be generic over Req/Resp;
// this unexported, package-private interface is the same resolution
// docs/design/d-0007-declarative-middleware-layering.md's own
// BoundMiddleware attachment already established for the identical
// constraint — each leaf type satisfies it via methods added to its
// EXISTING receiver (Req/Resp are already in scope there).
type routable interface {
	// withRouterPrefix returns a NEW leaf value (same concrete type) with
	// prefix prepended to its own path string, mws PREPENDED to its own
	// accumulated opts (so Router-contributed middleware dispatches BEFORE
	// the leaf's own), and tags APPENDED as a merge-opt (so Router-
	// contributed tags combine with, rather than being overwritten by, the
	// leaf's own RouteMeta.Tags — see [Router.Tags]'s doc comment for why
	// tags must be appended, not prepended, unlike mws) — plus the leaf's
	// resulting, fully-composed path, so callers never need a second
	// accessor to learn it.
	withRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string)
	// routeMethod reports this leaf's HTTP method, for RouterEntry.Method.
	routeMethod() string
	// middlewareNames reports the leaf's OWN (directly .Use()-attached,
	// pre-Router) reusable-class middleware names, in attachment order.
	middlewareNames() []string
	// tags reports the leaf's OWN (directly RouteMeta-declared,
	// pre-Router) tags, for RouterEntry.Tags.
	tags() []string
	// registerAny performs the SAME work Register(b) would, discarding any
	// returned *RouteHandle — a caller needing the handle back attaches
	// WithHandleCallback to the leaf directly instead (see that function's
	// doc comment); Router itself never exposes one.
	registerAny(b *Server) error
}

// middlewareNameOf extracts a human-readable name from mw for
// RouterEntry.MiddlewareNames — mirrors the SAME extraction
// [routeMiddlewareOpt.applyRoute] already performs for legacy
// [middleware.Middleware] values (a plain Name field) and codec-backed
// ones (a MiddlewareName() string method), falling back to a type name
// for anything else so the list is never silently incomplete.
func middlewareNameOf(mw middleware.RouteMiddleware) string {
	switch v := mw.(type) {
	case middleware.Middleware:
		return v.Name
	default:
		if named, ok := mw.(interface{ MiddlewareName() string }); ok {
			return named.MiddlewareName()
		}
		return fmt.Sprintf("%T", mw)
	}
}

// joinRouterPath joins a prefix and a path with exactly ONE "/" between
// them, regardless of whether either side already has a leading/trailing
// separator — mirrors path.Join-style normalization, not naive string
// concatenation. An empty prefix or path contributes nothing. The
// result, if non-empty, always carries a leading "/" — REST paths are
// conventionally absolute (events'/reqreply's own per-package copy of
// this function omits this, since MQTT/ZeroMQ-style topics don't use a
// leading separator).
func joinRouterPath(prefix, path string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	path = strings.TrimPrefix(path, "/")
	var result string
	switch {
	case prefix == "" && path == "":
		return ""
	case prefix == "":
		result = path
	case path == "":
		result = prefix
	default:
		result = prefix + "/" + path
	}
	if !strings.HasPrefix(result, "/") {
		result = "/" + result
	}
	return result
}

// RouterOpt configures a [Router] at construction time — reserved for
// future extension (e.g. a Router-scoped fallback handler or OpenAPI tag
// auto-population); no concrete RouterOpt implementations ship yet.
type RouterOpt interface{ applyRouter(*Router) }

// routerChild is one entry in a [Router]'s accumulated children — EITHER a
// leaf (via [Router.Route]) OR a nested [Router] (via [Router.Mount] or
// [Router.Group], which share this SAME representation; see [Router.Group]'s
// own doc comment).
type routerChild struct {
	leaf    routable
	leafMws []middleware.RouteMiddleware // this Router's own + any .With() one-shot mws, captured at .Route() time
	sub     *Router
}

// Router declares a path PREFIX once and groups any number of
// independently-declared [Route]/[SSERoute] values under it, optionally
// attaching reusable-class [middleware.RouteMiddleware] to every leaf
// registered under it in one declaration — a THIRD, group-level construct
// distinct from [Middleware], modeled on chi's own Router/Mount/Group/With/
// Routes/Walk (go-chi/chi/v5, the strongest available prior art for this
// exact problem).
//
// Router is a fully IMMUTABLE VALUE type, matching [Route]/[Channel]/
// [Subscriber]/[Publisher] exactly — every method below returns a NEW
// value, never mutates the receiver. This is a DELIBERATE choice: Router
// is an intermediate, composable declarative value (the SAME tier as
// Route/Channel), not a terminal builder like [Server]/[Client] — making it
// immutable means concurrent reads/derivations of the same value need NO
// synchronization at all, by construction.
//
// The codec-declaration step itself (NewRoute(...).WithHandler(fn)) does
// not change at all — Router is purely an additive, optional grouping/
// assembly layer on top of an unchanged declaration step. The common,
// linear-chain usage:
//
//	rt := rest.NewRouter("/api/v1").
//	    Use(authMiddleware).
//	    Route(getUsers).
//	    Route(createUser)
//	err := rt.Register(server)
type Router struct {
	prefix     string
	mws        []middleware.RouteMiddleware
	pendingMws []middleware.RouteMiddleware
	tags       []string
	children   []routerChild
}

// NewRouter declares a Router with a STATIC path prefix (no `{var}`
// placeholders for v1 — see docs/roadmap/declarative-router-groups.md's
// "Out of scope (Phase 2+)"). opts is reserved for future extension; no
// concrete [RouterOpt] implementations ship yet.
func NewRouter(prefix string, opts ...RouterOpt) Router {
	rt := Router{prefix: prefix}
	for _, o := range opts {
		o.applyRouter(&rt)
	}
	return rt
}

// Use returns a NEW Router with mws appended to its own accumulated,
// permanent middleware list — dispatched BEFORE every grouped leaf's own
// middleware (Router-first, outer-to-inner ordering), and before any
// nested [Router.Mount]/[Router.Group] child's own mws.
func (rt Router) Use(mws ...middleware.RouteMiddleware) Router {
	rt.mws = append(cloneMws(rt.mws), mws...)
	return rt
}

// Tags returns a NEW Router with tags appended to its own accumulated,
// permanent tag list — merged into every grouped leaf's OWN
// [RouteMeta.Tags] at [Router.Walk]/[Router.Register] time (Router's own
// tags first, then the leaf's own — same ordering convention as
// [Router.Use]'s middleware). Repeated `.Tags(a).Tags(b)` calls
// ACCUMULATE, mirroring [Router.Use]'s identical semantics — no
// one-shot/[Router.With]-equivalent exists for tags in this first pass.
//
// Unlike middleware (dispatch-order-sensitive, so Router-contributed mws
// must run BEFORE the leaf's own via a PREPENDED opt), tags are merged
// AFTER the leaf's own [RouteMeta] opt resolves — [RouteMeta.applyRoute]
// is a whole-struct overwrite (`rb.meta = m`), so a prepended tags opt
// would be silently discarded by a leaf's own [RouteMeta]; an APPENDED
// merge-opt (see withRouterPrefix) reads the already-resolved
// `rb.meta.Tags` and safely combines instead.
func (rt Router) Tags(tags ...string) Router {
	rt.tags = append(cloneTags(rt.tags), tags...)
	return rt
}

// With returns a NEW Router whose NEXT [Router.Route] call ONLY receives
// mws, in addition to (never in place of) rt's own permanent middleware —
// one-shot, not permanent: a SUBSEQUENT .Route() call (made on the value
// .Route() itself returns, which has pendingMws already cleared) never
// sees mws again. Achieved via ordinary immutable value flow (no special
// type needed, unlike an earlier draft of this design): rt itself is
// NEVER mutated, so `rt.Route(otherLeaf)` called on the ORIGINAL rt still
// never sees mws either. Repeated `.With(mw1).With(mw2)` calls ACCUMULATE
// (never silently overwrite) — rt.With(mw1).With(mw2) pends BOTH.
//
// With is [Router.Route]-SCOPED ONLY — it pairs with the IMMEDIATELY NEXT
// .Route() call, never a [Router.Mount]/[Router.Group]. `.With(mw).
// Mount(sub)` does NOT apply mw to sub's leaves; [Router.Mount] discards
// any still-pending mws at that point (same as .Route() consuming them),
// so they are silently DROPPED rather than leaking forward onto whatever
// unrelated .Route() call happens to follow later in the chain.
func (rt Router) With(mws ...middleware.RouteMiddleware) Router {
	rt.pendingMws = append(cloneMws(rt.pendingMws), mws...)
	return rt
}

// Route returns a NEW Router with r attached as a leaf — r's own prefix/
// middleware composition happens later, during [Router.Walk]/
// [Router.Register], once every ancestor [Router.Mount]/[Router.Group]'s
// own contribution is known. Only rt's own PENDING (one-shot, from
// [Router.With]) mws are captured here — rt's own PERMANENT mws (from
// [Router.Use]) are applied later, once per level, during the walk itself
// (capturing them here too would double-count them).
func (rt Router) Route(r routable) Router {
	pending := rt.pendingMws
	rt.pendingMws = nil
	rt.children = append(cloneChildren(rt.children), routerChild{leaf: r, leafMws: pending})
	return rt
}

// Mount returns a NEW Router with sub attached as a nested child — a NEW
// path segment (rt's prefix + sub's own prefix) and a FRESH middleware
// stack for everything under sub (rt's own mws run first, then sub's own,
// then each leaf's own — outer-to-inner, outermost-declared-first).
//
// Any still-pending [Router.With] mws on rt are DISCARDED here, exactly
// as [Router.Route] would consume (clear) them — Mount has no single leaf
// to attach a one-shot middleware to, so a preceding `.With(mw)` is
// simply dropped rather than silently leaking onto a later, unrelated
// `.Route()` call (see [Router.With]'s doc comment).
func (rt Router) Mount(sub Router) Router {
	rt.pendingMws = nil
	rt.children = append(cloneChildren(rt.children), routerChild{sub: &sub})
	return rt
}

// Group returns a NEW Router with fn's built-up child incorporated — SAME
// prefix as rt (NO new path segment), just a scoped middleware subset for
// a SUBSET of routes sharing rt's own path (chi's own Group — "useful for
// a group of handlers along the same routing path that use an additional
// set of middlewares"). fn receives an EMPTY child Router (rt's prefix
// inherited as "" contribution — i.e. no additional segment) and MUST
// explicitly return its built-up value (e.g. `return sub.Route(a).
// Route(b)`), since Router is immutable and there is no mutation for
// Group to observe otherwise — a small, deliberate ergonomic difference
// from chi's own mutation-style callback, accepted for Router's overall
// consistency with every other api/* declarative type.
//
// rt.Group(fn) RETURNS rt ITSELF (the PARENT, updated), NOT the child fn
// received — a deliberate divergence from chi's own Group (which returns
// the child) for consistency with every other method here, which all
// chain off the receiver. Internally, Group and Mount share the SAME
// child representation (routerChild.sub) — Group's child simply
// contributes an empty prefix segment; there is no second, parallel
// tree-walking implementation.
func (rt Router) Group(fn func(sub Router) Router) Router {
	child := fn(Router{})
	return rt.Mount(child)
}

// RouterEntry describes one leaf's FINAL, fully-assembled view, returned
// by [Router.Routes]/[Router.Walk] — the "holistic, pre-registration
// overview of how the API assembles" the design behind Router set out to
// provide.
type RouterEntry struct {
	// Method is this leaf's HTTP method.
	Method string
	// Path is the FINAL, prefix-applied path.
	Path string
	// MiddlewareNames lists every reusable-class middleware name that will
	// apply to this leaf, in dispatch order — Router-contributed names
	// first (outermost ancestor first), then the leaf's own.
	MiddlewareNames []string
	// Tags lists every tag that will apply to this leaf's spec entry —
	// Router-contributed tags first (outermost ancestor first), then the
	// leaf's own [RouteMeta.Tags].
	Tags []string
}

// WalkFunc is called once per LEAF (never per intermediate Router/Group),
// after full prefix + middleware composition. A non-nil error stops the
// walk immediately and is returned as-is (no wrapping) — mirrors chi's own
// Walk short-circuit behavior.
type WalkFunc func(entry RouterEntry) error

// Walk is the one true primitive — recurses through every Mount/Group,
// composing prefixes and middleware exactly as [Router.Register] would,
// calling fn once per leaf, in declaration order.
func (rt Router) Walk(fn WalkFunc) error {
	return rt.walk("", nil, nil, fn)
}

func (rt Router) walk(ancestorPrefix string, ancestorMws []middleware.RouteMiddleware, ancestorTags []string, fn WalkFunc) error {
	prefix := joinRouterPath(ancestorPrefix, rt.prefix)
	mws := append(cloneMws(ancestorMws), rt.mws...)
	tags := append(cloneTags(ancestorTags), rt.tags...)
	for _, c := range rt.children {
		if c.sub != nil {
			if err := c.sub.walk(prefix, mws, tags, fn); err != nil {
				return err
			}
			continue
		}
		allMws := append(cloneMws(mws), c.leafMws...)
		transformed, composedPath := c.leaf.withRouterPrefix(prefix, allMws, tags)
		names := make([]string, 0, len(allMws)+len(c.leaf.middlewareNames()))
		for _, mw := range allMws {
			names = append(names, middlewareNameOf(mw))
		}
		names = append(names, c.leaf.middlewareNames()...)
		entryTags := append(cloneTags(tags), c.leaf.tags()...)
		entry := RouterEntry{
			Method:          transformed.routeMethod(),
			Path:            composedPath,
			MiddlewareNames: names,
			Tags:            entryTags,
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
	return nil
}

// Routes is a convenience wrapper over [Router.Walk] — collects every leaf
// into a flat, declaration-ordered slice. Walkable and printable WITHOUT
// calling [Router.Register] (no [Server] needed), so a caller can inspect/
// log/assert on the assembled tree in a test or at startup, before
// committing to registration.
func (rt Router) Routes() []RouterEntry {
	var entries []RouterEntry
	_ = rt.Walk(func(e RouterEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries
}

// Register walks the tree, composes every leaf's final prefix+middleware,
// and registers each one with b — the SAME, UNCHANGED Register/
// registerHandle each leaf's own type already implements; Router performs
// ZERO new validation logic. Once Register succeeds, the resulting handle
// is indistinguishable from one built without a Router at all.
//
// A leaf whose final, prefix-applied path fails the SAME validation its
// own Register would apply standalone surfaces [RouterPrefixError],
// wrapping the real cause. Every OTHER error a leaf's own Register can
// return (DuplicateMiddlewareNameError, security coverage failures,
// DuplicateRouteError, etc.) propagates COMPLETELY UNWRAPPED, exactly as
// it would from a direct, Router-less call.
func (rt Router) Register(b *Server) error {
	return rt.register("", nil, nil, b)
}

func (rt Router) register(ancestorPrefix string, ancestorMws []middleware.RouteMiddleware, ancestorTags []string, b *Server) error {
	prefix := joinRouterPath(ancestorPrefix, rt.prefix)
	mws := append(cloneMws(ancestorMws), rt.mws...)
	tags := append(cloneTags(ancestorTags), rt.tags...)
	for _, c := range rt.children {
		if c.sub != nil {
			if err := c.sub.register(prefix, mws, tags, b); err != nil {
				return err
			}
			continue
		}
		allMws := append(cloneMws(mws), c.leafMws...)
		transformed, composedPath := c.leaf.withRouterPrefix(prefix, allMws, tags)
		if err := transformed.registerAny(b); err != nil {
			var pathErr InvalidPathError
			if asInvalidPathError(err, &pathErr) {
				return RouterPrefixError{Prefix: prefix, ComposedPath: composedPath, Err: err}
			}
			return err
		}
	}
	return nil
}

// asInvalidPathError reports whether err is (or wraps) an
// [InvalidPathError] — a tiny local helper so [Router.register] doesn't
// need to import "errors" solely for one errors.As call.
func asInvalidPathError(err error, target *InvalidPathError) bool {
	type unwrapper interface{ Unwrap() error }
	for {
		if v, ok := err.(InvalidPathError); ok {
			*target = v
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
		if err == nil {
			return false
		}
	}
}

// RouterPrefixError is returned by [Router.Register] ONLY when a leaf's
// final, prefix-applied path fails the SAME validation its own Register
// would apply standalone (e.g. REST's path-template syntax check,
// duplicate-path-param-name check).
//
// Carries ONLY Prefix+ComposedPath+Err — all of which Router already
// knows/computes without any extra accessor on the leaf itself.
//
// RouterPrefixError wraps ONLY this one failure mode. Every OTHER error a
// leaf's own Register can return propagates COMPLETELY UNWRAPPED. When 2
// leaves' FINAL, composed paths collide (a Router-introduced duplicate
// neither leaf would have had standalone), the resulting
// [DuplicateRouteError]'s ordering (which leaf's error surfaces first)
// follows [Router.Walk]'s own declaration-order traversal — deterministic,
// not implementation-accidental.
type RouterPrefixError struct {
	Prefix       string // the Router's own (or accumulated-nested) prefix
	ComposedPath string // the leaf's FINAL, prefix-applied path that actually failed
	Err          error  // the underlying error from the leaf's own Register
}

func (e RouterPrefixError) Error() string {
	return fmt.Sprintf("rest: router: prefix %q: path %q: %s", e.Prefix, e.ComposedPath, e.Err.Error())
}

// Unwrap allows errors.As/errors.Is to traverse the underlying cause.
func (e RouterPrefixError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RouterPrefixError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("prefix", e.Prefix),
		slog.String("composed_path", e.ComposedPath),
		slog.Any("err", e.Err),
	)
}

// WithHandleCallback registers fn to run immediately after this route's
// *RouteHandle is successfully constructed, inside [Route.Register]/
// [Route.RegisterHandle] — regardless of whether Register was called
// directly OR via a [Router]'s Register (which just calls the SAME
// unchanged registerHandle through the [routable] interface). Stored
// type-erased in routeBuilder exactly like requestFormats/respFormats.
//
// A route declared with this opt needs NO special Router-aware handling —
// composes for free with Router.Route(leaf) because registerAny always
// delegates to the leaf's own, unchanged Register. The overwhelmingly
// common case (discard the handle) needs zero opt at all, unchanged.
//
// A free function (not a method) — Go forbids new type parameters on a
// method; this is the SAME resolution docs/design/
// d-0007-declarative-middleware-layering.md established for
// [BoundMiddleware]'s identical constraint.
func WithHandleCallback[Req, Resp any](fn func(*RouteHandle[Req, Resp])) RouteOpt {
	return handleCallbackOpt{fn: fn}
}

type handleCallbackOpt struct{ fn any }

func (o handleCallbackOpt) applyRoute(rb *routeBuilder) { rb.handleCallback = o.fn }

// HandleCallbackTypeError is returned by [Route.Register]/
// [Route.RegisterHandle] when a [WithHandleCallback] value's type doesn't
// match the Route's own Req/Resp — a caller programming error (mixing a
// callback built for one Route's Req/Resp into a different Route).
type HandleCallbackTypeError struct{ Err error }

func (e HandleCallbackTypeError) Error() string {
	return fmt.Sprintf("api/rest: handle callback: %v", e.Err)
}

// Unwrap allows errors.Is/errors.As to reach the underlying error.
func (e HandleCallbackTypeError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e HandleCallbackTypeError) LogValue() slog.Value {
	return slog.GroupValue(slog.Any("err", e.Err))
}

// ClientHandleOpt configures a [Route.ClientHandle] call — currently only
// [WithRouter]. Operates on the [routable] interface (the SAME one
// [Router] itself uses internally) rather than a generic Route[Req,Resp]
// directly, since an interface method cannot introduce new type
// parameters — [Route.ClientHandle] converts to/from routable internally.
type ClientHandleOpt interface {
	applyClientHandle(r routable) routable
}

// WithRouter tells [Route.ClientHandle] to apply rt's CURRENT accumulated
// prefix+middleware before building the handle — the EXACT SAME
// composition `rt.Route(route)` + `rt.Register(b)` would have produced
// for route's SERVER side. rt is the single, unambiguous source of truth
// for the prefix, eliminating the forgot-to-reapply-the-prefix-string
// risk a raw-string-based alternative would have had.
//
// Does NOT require route to have actually been [Router.Route]'d into rt
// — it is a pure composition convenience, not a validation that the
// pairing is registered (mirrors [Route.ClientHandle]'s own existing
// infallible, non-validating character). For a MULTI-LEVEL nested
// [Router.Mount]/[Router.Group], pass the SPECIFIC (innermost) Router
// value route was (or will be) [Router.Route]'d into — its own effective
// prefix already composes all ancestor Routers' contributions
// transitively, consistent with [Router.Register]'s own existing
// nested-composition behavior.
func WithRouter(rt Router) ClientHandleOpt { return withRouterOpt{rt: rt} }

type withRouterOpt struct{ rt Router }

func (o withRouterOpt) applyClientHandle(r routable) routable {
	transformed, _ := r.withRouterPrefix(o.rt.prefix, o.rt.mws, o.rt.tags)
	return transformed
}

func cloneMws(mws []middleware.RouteMiddleware) []middleware.RouteMiddleware {
	if len(mws) == 0 {
		return nil
	}
	out := make([]middleware.RouteMiddleware, len(mws))
	copy(out, mws)
	return out
}

func cloneChildren(children []routerChild) []routerChild {
	if len(children) == 0 {
		return nil
	}
	out := make([]routerChild, len(children))
	copy(out, children)
	return out
}

func cloneTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, len(tags))
	copy(out, tags)
	return out
}

// routerTagsOpt is the RouteOpt a [Router] APPENDS (never prepends) to a
// leaf's own opts list via [Router.Tags] — see that method's doc comment
// for why tags must be merged AFTER the leaf's own [RouteMeta] opt
// resolves, unlike middleware (which is prepended to run BEFORE).
type routerTagsOpt struct{ tags []string }

func (o routerTagsOpt) applyRoute(rb *routeBuilder) {
	rb.meta.Tags = append(append([]string{}, o.tags...), rb.meta.Tags...)
}

// withRouterPrefix implements [routable] for [Route].
func (r Route[Req, Resp]) withRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	r.path = joinRouterPath(prefix, r.path)
	if len(mws) > 0 {
		r.opts = append([]RouteOpt{routeMiddlewareOpt{mws: mws}}, r.opts...)
	}
	if len(tags) > 0 {
		// Clone first — r.opts may still be the leaf's ORIGINAL slice
		// (unreallocated, when the mws branch above didn't fire) with
		// spare capacity from a prior .Use() call's append-growth; an
		// unguarded append could write into that shared backing array.
		// Harmless today (every caller resolves Tags synchronously right
		// after this call, before any conflicting append could land) but
		// defensive against any future deferred-resolution path — mirrors
		// [api/events]'s already-defensive equivalent.
		r.opts = append(slices.Clone(r.opts), routerTagsOpt{tags: tags})
	}
	return r, r.path
}

// routeMethod implements [routable]'s routeMethod() requirement for
// [Route]. Named routeMethod, not method, because Route already has an
// unexported `method` FIELD — Go forbids a method and a field sharing one
// identifier on the same type.
func (r Route[Req, Resp]) routeMethod() string { return r.method }

// middlewareNames implements [routable] for [Route] — reports every
// reusable-class middleware name directly .Use()-attached to r, BEFORE
// any Router involvement, in attachment order.
func (r Route[Req, Resp]) middlewareNames() []string {
	var names []string
	for _, opt := range r.opts {
		if rmo, ok := opt.(routeMiddlewareOpt); ok {
			for _, mw := range rmo.mws {
				names = append(names, middlewareNameOf(mw))
			}
		}
	}
	return names
}

// tags implements [routable] for [Route] — reports r's OWN,
// directly-declared [RouteMeta.Tags], BEFORE any Router involvement.
func (r Route[Req, Resp]) tags() []string {
	var tags []string
	for _, opt := range r.opts {
		if rm, ok := opt.(RouteMeta); ok {
			tags = append(tags, rm.Tags...)
		}
	}
	return tags
}

// registerAny implements [routable] for [Route].
func (r Route[Req, Resp]) registerAny(b *Server) error { return r.Register(b) }

// withRouterPrefix implements [routable] for [SSERoute].
func (s SSERoute[Req, Event]) withRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	s.path = joinRouterPath(prefix, s.path)
	if len(mws) > 0 {
		s.opts = append([]RouteOpt{routeMiddlewareOpt{mws: mws}}, s.opts...)
	}
	if len(tags) > 0 {
		// See [Route.withRouterPrefix]'s identical comment above.
		s.opts = append(slices.Clone(s.opts), routerTagsOpt{tags: tags})
	}
	return s, s.path
}

// method implements [routable] for [SSERoute] — SSE routes are always GET.
func (s SSERoute[Req, Event]) routeMethod() string { return "GET" }

// middlewareNames implements [routable] for [SSERoute].
func (s SSERoute[Req, Event]) middlewareNames() []string {
	var names []string
	for _, opt := range s.opts {
		if rmo, ok := opt.(routeMiddlewareOpt); ok {
			for _, mw := range rmo.mws {
				names = append(names, middlewareNameOf(mw))
			}
		}
	}
	return names
}

// tags implements [routable] for [SSERoute] — reports s's OWN,
// directly-declared [RouteMeta.Tags], BEFORE any Router involvement.
func (s SSERoute[Req, Event]) tags() []string {
	var tags []string
	for _, opt := range s.opts {
		if rm, ok := opt.(RouteMeta); ok {
			tags = append(tags, rm.Tags...)
		}
	}
	return tags
}

// registerAny implements [routable] for [SSERoute].
func (s SSERoute[Req, Event]) registerAny(b *Server) error { return s.Register(b) }
