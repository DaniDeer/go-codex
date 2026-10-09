package rest

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/router"
)

// routable is an internal alias for [router.Routable][*Server] — kept
// under its pre-consolidation unexported name so every existing call site
// in this package (ClientHandleOpt, Route.ClientHandle, etc.) keeps
// compiling unchanged. See docs/design/d-0009-internalize-shared-mechanics.md's
// Phase 1 for the full consolidation this package's Router now sits on
// top of.
type routable = router.Routable[*Server]

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
// Router is a THIN WRAPPER around the shared [router.Router][*Server]
// core (not a type alias — see docs/design/d-0009-internalize-shared-mechanics.md's
// Phase 1 Design Decision #2 for why a pure alias is incompatible with
// RouterEntry's per-package Method/Role/neither field shape). Every
// method below except Walk/Routes is a trivial forward.
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
type Router struct{ inner router.Router[*Server] }

// NewRouter declares a Router with a STATIC path prefix (no `{var}`
// placeholders for v1 — see docs/roadmap/declarative-router-groups.md's
// "Out of scope (Phase 2+)"). opts is reserved for future extension; no
// concrete [RouterOpt] implementations ship yet.
func NewRouter(prefix string, opts ...RouterOpt) Router {
	rt := Router{inner: router.NewRouter[*Server](prefix, joinRouterPath, wrapRouterPrefixError)}
	for _, o := range opts {
		o.applyRouter(&rt)
	}
	return rt
}

// RouterOpt configures a [Router] at construction time — reserved for
// future extension; no concrete RouterOpt implementations ship yet.
type RouterOpt interface{ applyRouter(*Router) }

// Use returns a NEW Router with mws appended to its own accumulated,
// permanent middleware list — dispatched BEFORE every grouped leaf's own
// middleware (Router-first, outer-to-inner ordering), and before any
// nested [Router.Mount]/[Router.Group] child's own mws.
func (rt Router) Use(mws ...middleware.RouteMiddleware) Router {
	return Router{inner: rt.inner.Use(mws...)}
}

// Tags returns a NEW Router with tags appended to its own accumulated
// tag list — combined with (never overwriting) every grouped leaf's own
// declared [RouteMeta.Tags].
func (rt Router) Tags(tags ...string) Router {
	return Router{inner: rt.inner.Tags(tags...)}
}

// With returns a NEW Router with mws staged as ONE-SHOT, pending
// middleware — consumed (and cleared) by the VERY NEXT [Router.Route]
// call only; a following [Router.Mount]/[Router.Group] call DISCARDS any
// still-pending mws rather than silently leaking them onto a later,
// unrelated `.Route()` call.
func (rt Router) With(mws ...middleware.RouteMiddleware) Router {
	return Router{inner: rt.inner.With(mws...)}
}

// Route returns a NEW Router with r attached as a leaf child.
func (rt Router) Route(r routable) Router {
	return Router{inner: rt.inner.Route(r)}
}

// Mount returns a NEW Router with sub attached as a nested child — a NEW
// path segment (rt's prefix + sub's own prefix) and a FRESH middleware
// stack for everything under sub (rt's own mws run first, then sub's own,
// then each leaf's own — outer-to-inner, outermost-declared-first).
func (rt Router) Mount(sub Router) Router {
	return Router{inner: rt.inner.Mount(sub.inner)}
}

// Group returns a NEW Router with fn's built-up child incorporated — SAME
// prefix as rt (NO new path segment), just a scoped middleware subset for
// a SUBSET of routes sharing rt's own path (chi's own Group — "useful for
// a group of handlers along the same routing path that use an additional
// set of middlewares"). fn receives an EMPTY child Router and MUST
// explicitly return its built-up value (e.g. `return sub.Route(a).
// Route(b)`), since Router is immutable.
//
// rt.Group(fn) RETURNS rt ITSELF (the PARENT, updated), NOT the child fn
// received — a deliberate divergence from chi's own Group (which returns
// the child) for consistency with every other method here, which all
// chain off the receiver.
func (rt Router) Group(fn func(sub Router) Router) Router {
	return Router{inner: rt.inner.Group(func(sub router.Router[*Server]) router.Router[*Server] {
		return fn(Router{inner: sub}).inner
	})}
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
	return rt.inner.Walk(func(e router.RouterEntry) error {
		return fn(RouterEntry{
			Method:          e.Method,
			Path:            e.Path,
			MiddlewareNames: e.MiddlewareNames,
			Tags:            e.Tags,
		})
	})
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
	return rt.inner.Register(b)
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

// wrapRouterPrefixError is the [router.PrefixErrorFunc] supplied to
// [router.NewRouter] — does exactly what this package's inline
// asInvalidPathError + RouterPrefixError{...} construction did before
// this consolidation, now relocated into a closure.
func wrapRouterPrefixError(prefix, composed string, err error) (error, bool) {
	var pathErr InvalidPathError
	if asInvalidPathError(err, &pathErr) {
		return RouterPrefixError{Prefix: prefix, ComposedPath: composed, Err: err}, true
	}
	return nil, false
}

// asInvalidPathError reports whether err is (or wraps) an
// [InvalidPathError] — a tiny local helper so [wrapRouterPrefixError]
// doesn't need to import "errors" solely for one errors.As call.
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
	transformed, _ := r.WithRouterPrefix(o.rt.inner.Prefix(), o.rt.inner.OwnMiddleware(), o.rt.inner.OwnTags())
	return transformed
}

// routerTagsOpt is the RouteOpt a [Router] APPENDS (never prepends) to a
// leaf's own opts list via [Router.Tags] — see that method's doc comment
// for why tags must be merged AFTER the leaf's own [RouteMeta] opt
// resolves, unlike middleware (which is prepended to run BEFORE).
type routerTagsOpt struct{ tags []string }

func (o routerTagsOpt) applyRoute(rb *routeBuilder) {
	rb.meta.Tags = append(append([]string{}, o.tags...), rb.meta.Tags...)
}

// WithRouterPrefix implements [router.Routable] for [Route].
func (r Route[Req, Resp]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
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

// RouteMethod implements [router.MethodReporter] for [Route]. Named
// RouteMethod, not Method, because Route already has an unexported
// `method` FIELD — Go forbids a method and a field sharing one identifier
// on the same type.
func (r Route[Req, Resp]) RouteMethod() string { return r.method }

// MiddlewareNames implements [router.Routable] for [Route] — reports
// every reusable-class middleware name directly .Use()-attached to r,
// BEFORE any Router involvement, in attachment order.
func (r Route[Req, Resp]) MiddlewareNames() []string {
	var names []string
	for _, opt := range r.opts {
		switch o := opt.(type) {
		case routeMiddlewareOpt:
			for _, mw := range o.mws {
				names = append(names, middlewareNameOf(mw))
			}
		case boundHandleMWOpt:
			names = append(names, o.name)
		case boundClientAttachOpt:
			names = append(names, o.name)
		}
	}
	return names
}

// RouteTags implements [router.Routable] for [Route] — reports r's OWN,
// directly-declared [RouteMeta.Tags], BEFORE any Router involvement.
func (r Route[Req, Resp]) RouteTags() []string {
	// Resolves r's own opts through a scratch [routeBuilder] (read-only;
	// never mutates r) — NOT a naive per-opt iteration — because
	// [RouteMeta.applyRoute] is a WHOLE-STRUCT OVERWRITE (`rb.meta = m`):
	// if a route declares 2+ separate RouteMeta opts, only the LAST one's
	// Tags survive into the real registered spec. Replaying through a
	// scratch routeBuilder guarantees this accessor reports EXACTLY what
	// Register will produce, instead of incorrectly merging every
	// declared RouteMeta's Tags together — a confirmed, previously-real
	// discrepancy between Router's Routes()/Walk() preview and the actual
	// registered result. Mirrors [api/reqreply]'s identical mechanism.
	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}
	return rb.meta.Tags
}

// RegisterAny implements [router.Routable] for [Route].
func (r Route[Req, Resp]) RegisterAny(b *Server) error { return r.Register(b) }

// WithRouterPrefix implements [router.Routable] for [SSERoute].
func (s SSERoute[Req, Event]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	s.path = joinRouterPath(prefix, s.path)
	if len(mws) > 0 {
		s.opts = append([]RouteOpt{routeMiddlewareOpt{mws: mws}}, s.opts...)
	}
	if len(tags) > 0 {
		// See [Route.WithRouterPrefix]'s identical comment above.
		s.opts = append(slices.Clone(s.opts), routerTagsOpt{tags: tags})
	}
	return s, s.path
}

// RouteMethod implements [router.MethodReporter] for [SSERoute] — SSE
// routes are always GET.
func (s SSERoute[Req, Event]) RouteMethod() string { return "GET" }

// MiddlewareNames implements [router.Routable] for [SSERoute].
func (s SSERoute[Req, Event]) MiddlewareNames() []string {
	var names []string
	for _, opt := range s.opts {
		switch o := opt.(type) {
		case routeMiddlewareOpt:
			for _, mw := range o.mws {
				names = append(names, middlewareNameOf(mw))
			}
		case boundHandleMWOpt:
			names = append(names, o.name)
		case boundClientAttachOpt:
			names = append(names, o.name)
		}
	}
	return names
}

// RouteTags implements [router.Routable] for [SSERoute] — reports s's
// OWN, directly-declared [RouteMeta.Tags], BEFORE any Router involvement.
func (s SSERoute[Req, Event]) RouteTags() []string {
	// See [Route.RouteTags]'s identical doc comment above — same
	// scratch-routeBuilder-replay rationale applies unchanged.
	var rb routeBuilder
	for _, opt := range s.opts {
		opt.applyRoute(&rb)
	}
	return rb.meta.Tags
}

// RegisterAny implements [router.Routable] for [SSERoute].
func (s SSERoute[Req, Event]) RegisterAny(b *Server) error { return s.Register(b) }
