package reqreply

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/DaniDeer/go-codex/middleware"
)

// routable is satisfied by [Route] — reqreply's own, separately compiled
// copy of [api/rest]'s identical resolution (see that package's router.go
// doc comment for the shared rationale: Go forbids a method from
// introducing new type parameters beyond its receiver's own, so
// Router.Route cannot itself be generic over Req/Resp).
//
// Unlike REST (`routeMethod() string`, HTTP method axis) and events
// (`role() string`, subscribe/publish axis), reqreply's routable has
// NEITHER — a single Route[Req,Resp] IS ALREADY the complete, final leaf
// (topic + both codecs + handler, one Register call); there is no second
// axis to disambiguate multiple leaves sharing one topic.
type routable interface {
	// withRouterPrefix returns a NEW leaf value (same concrete type) with
	// prefix prepended to its own topic string, mws PREPENDED to its own
	// accumulated opts (so Router-contributed middleware dispatches
	// BEFORE the leaf's own), and tags APPENDED as a merge-opt (so
	// Router-contributed tags combine with, rather than being overwritten
	// by, the leaf's own RouteMeta.Tags — see [Router.Tags]'s doc comment
	// for why tags must be appended, not prepended, unlike mws) — plus
	// the leaf's resulting, fully-composed topic, so callers never need a
	// second accessor to learn it.
	withRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string)
	// middlewareNames reports the leaf's OWN (directly .Use()-attached,
	// pre-Router) reusable-class middleware names, in attachment order.
	middlewareNames() []string
	// tags reports the leaf's OWN (directly RouteMeta-declared,
	// pre-Router) tags, for RouterEntry.Tags.
	tags() []string
	// registerAny performs the SAME work [Route.Register] would,
	// discarding the returned *RouteHandle — a caller needing the handle
	// back attaches [WithHandleCallback] to the leaf directly instead;
	// Router itself never exposes one.
	registerAny(b *Server) error
}

// middlewareNameOf extracts a human-readable name from mw for
// RouterEntry.MiddlewareNames — reqreply's own copy of [api/rest]'s
// identical helper (not shared across packages — see [routable]'s doc
// comment).
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

// joinRouterTopic joins a prefix and a topic with exactly ONE "/" between
// them, regardless of whether either side already has a leading/trailing
// separator. An empty prefix or topic contributes nothing. Like
// [api/events]'s own copy (and unlike [api/rest]'s joinRouterPath), the
// result NEVER gets a forced leading "/" — MQTT/ZeroMQ-style topics don't
// use a leading separator.
func joinRouterTopic(prefix, topic string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	topic = strings.TrimPrefix(topic, "/")
	switch {
	case prefix == "" && topic == "":
		return ""
	case prefix == "":
		return topic
	case topic == "":
		return prefix
	default:
		return prefix + "/" + topic
	}
}

// RouterOpt configures a [Router] at construction time — reserved for
// future extension; no concrete RouterOpt implementations ship yet. See
// [api/rest.RouterOpt]'s doc comment for the shared rationale.
type RouterOpt interface{ applyRouter(*Router) }

// routerChild is one entry in a [Router]'s accumulated children — EITHER a
// leaf (via [Router.Route]) OR a nested [Router] (via [Router.Mount] or
// [Router.Group], which share this SAME representation).
type routerChild struct {
	leaf    routable
	leafMws []middleware.RouteMiddleware // this Router's own + any .With() one-shot mws, captured at .Route() time
	sub     *Router
}

// Router declares a topic PREFIX once and groups any number of
// independently-declared [Route] values under it, optionally attaching
// reusable-class [middleware.RouteMiddleware] to every leaf registered
// under it in one declaration — reqreply's own copy of [api/rest.Router],
// adapted for request/reply's SINGLE-leaf shape (no method/role axis). See
// that type's doc comment for the shared rationale (chi-inspired, fully
// immutable value type).
//
// [Router.Group] keeps chi's ORIGINAL, baseline semantics here (no special
// structural axis to target, unlike REST's method-scoped or events'
// role-scoped Group) — an arbitrary, user-chosen subset of routes sharing
// a topic-prefix needing extra scoped middleware.
//
//	rt := reqreply.NewRouter("compute").
//	    Use(authMiddleware).
//	    Route(addRoute).
//	    Route(subtractRoute)
//	handle, err := rt.Register(builder) // NOTE: see Router.Register's doc comment
type Router struct {
	prefix     string
	mws        []middleware.RouteMiddleware
	pendingMws []middleware.RouteMiddleware
	tags       []string
	children   []routerChild
}

// NewRouter declares a Router with a STATIC topic prefix (no `{var}`
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
// tags first, then the leaf's own). Repeated `.Tags(a).Tags(b)` calls
// ACCUMULATE, mirroring [Router.Use]'s identical semantics — no
// one-shot/[Router.With]-equivalent exists for tags in this first pass.
// See [api/rest.Router.Tags]'s doc comment for why tags are merged AFTER
// the leaf's own opts resolve (an append, not a prepend, unlike mws) —
// identical rationale, identical mechanism here.
func (rt Router) Tags(tags ...string) Router {
	rt.tags = append(cloneTags(rt.tags), tags...)
	return rt
}

// With returns a NEW Router whose NEXT [Router.Route] call ONLY receives
// mws, in addition to (never in place of) rt's own permanent middleware —
// one-shot, not permanent. See [api/rest.Router.With]'s doc comment for
// the full accumulate-semantics contract (identical here), including the
// Route-SCOPED-ONLY restriction — With does NOT pair with [Router.Mount]/
// [Router.Group]; a preceding .With(mw) is silently DROPPED at Mount time
// rather than leaking onto a later, unrelated .Route() call.
func (rt Router) With(mws ...middleware.RouteMiddleware) Router {
	rt.pendingMws = append(cloneMws(rt.pendingMws), mws...)
	return rt
}

// Route returns a NEW Router with leaf attached — leaf's own prefix/
// middleware composition happens later, during [Router.Walk]/
// [Router.Register], once every ancestor [Router.Mount]/[Router.Group]'s
// own contribution is known. Only rt's own PENDING (one-shot, from
// [Router.With]) mws are captured here — rt's own PERMANENT mws (from
// [Router.Use]) are applied later, once per level, during the walk
// itself.
func (rt Router) Route(leaf routable) Router {
	pending := rt.pendingMws
	rt.pendingMws = nil
	rt.children = append(cloneChildren(rt.children), routerChild{leaf: leaf, leafMws: pending})
	return rt
}

// Mount returns a NEW Router with sub attached as a nested child — a NEW
// topic segment (rt's prefix + sub's own prefix) and a FRESH middleware
// stack for everything under sub.
//
// Any still-pending [Router.With] mws on rt are DISCARDED here, exactly
// as [Router.Route] would consume (clear) them — see [Router.With]'s doc
// comment.
func (rt Router) Mount(sub Router) Router {
	rt.pendingMws = nil
	rt.children = append(cloneChildren(rt.children), routerChild{sub: &sub})
	return rt
}

// Group returns a NEW Router with fn's built-up child incorporated — SAME
// prefix as rt (NO new topic segment), just a scoped middleware subset
// for an arbitrary, user-chosen subset of routes sharing rt's own topic
// prefix. fn receives an EMPTY child Router and MUST explicitly return
// its built-up value. See [api/rest.Router.Group]'s doc comment for the
// shared rationale, including why rt.Group(fn) returns rt ITSELF (the
// parent), not the child.
func (rt Router) Group(fn func(sub Router) Router) Router {
	child := fn(Router{})
	return rt.Mount(child)
}

// RouterEntry describes one leaf's FINAL, fully-assembled view, returned
// by [Router.Routes]/[Router.Walk]. Unlike REST's (Method) or events'
// (Role), reqreply's RouterEntry has NO second-axis field — a single
// Route[Req,Resp] is already the complete leaf.
type RouterEntry struct {
	// Path is the FINAL, prefix-applied topic.
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
	prefix := joinRouterTopic(ancestorPrefix, rt.prefix)
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
		_, composedTopic := c.leaf.withRouterPrefix(prefix, allMws, tags)
		names := make([]string, 0, len(allMws)+len(c.leaf.middlewareNames()))
		for _, mw := range allMws {
			names = append(names, middlewareNameOf(mw))
		}
		names = append(names, c.leaf.middlewareNames()...)
		entryTags := append(cloneTags(tags), c.leaf.tags()...)
		entry := RouterEntry{
			Path:            composedTopic,
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
// calling [Router.Register] (no [Builder] needed).
func (rt Router) Routes() []RouterEntry {
	var entries []RouterEntry
	_ = rt.Walk(func(e RouterEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries
}

// Register walks the tree, composes every leaf's final prefix+middleware,
// and registers each one with b — the SAME, UNCHANGED [Route.Register]
// each leaf's own type already implements; Router performs ZERO new
// validation logic.
//
// A leaf whose final, prefix-applied topic fails the SAME validation its
// own Register would apply standalone surfaces [RouterPrefixError],
// wrapping the real cause. Every OTHER error a leaf's own Register can
// return (DuplicateRouteError, security coverage failures, etc.)
// propagates COMPLETELY UNWRAPPED, exactly as it would from a direct,
// Router-less call.
func (rt Router) Register(b *Server) error {
	return rt.register("", nil, nil, b)
}

func (rt Router) register(ancestorPrefix string, ancestorMws []middleware.RouteMiddleware, ancestorTags []string, b *Server) error {
	prefix := joinRouterTopic(ancestorPrefix, rt.prefix)
	mws := append(cloneMws(ancestorMws), rt.mws...)
	tags := append(cloneTags(ancestorTags), rt.tags...)
	for _, child := range rt.children {
		if child.sub != nil {
			if err := child.sub.register(prefix, mws, tags, b); err != nil {
				return err
			}
			continue
		}
		allMws := append(cloneMws(mws), child.leafMws...)
		transformed, composedTopic := child.leaf.withRouterPrefix(prefix, allMws, tags)
		if err := transformed.registerAny(b); err != nil {
			var topicErr InvalidTopicError
			if asInvalidTopicError(err, &topicErr) {
				return RouterPrefixError{Prefix: prefix, ComposedTopic: composedTopic, Err: err}
			}
			return err
		}
	}
	return nil
}

// asInvalidTopicError reports whether err is (or wraps) an
// [InvalidTopicError] — a tiny local helper so [Router.register] doesn't
// need to import "errors" solely for one errors.As call.
func asInvalidTopicError(err error, target *InvalidTopicError) bool {
	type unwrapper interface{ Unwrap() error }
	for {
		if v, ok := err.(InvalidTopicError); ok {
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
// final, prefix-applied topic fails the SAME validation its own Register
// would apply standalone (e.g. a [Builder]'s [WithTopicCodec]/
// [WithTopicConstraints] check).
//
// Carries ONLY Prefix+ComposedTopic+Err — all of which Router already
// knows/computes without any extra accessor on the leaf itself. Every
// OTHER error a leaf's own Register can return propagates COMPLETELY
// UNWRAPPED. See [api/rest.RouterPrefixError]'s doc comment for the
// shared rationale.
type RouterPrefixError struct {
	Prefix        string // the Router's own (or accumulated-nested) prefix
	ComposedTopic string // the leaf's FINAL, prefix-applied topic that actually failed
	Err           error  // the underlying error from the leaf's own Register
}

func (e RouterPrefixError) Error() string {
	return fmt.Sprintf("api/reqreply: router: prefix %q: topic %q: %s", e.Prefix, e.ComposedTopic, e.Err.Error())
}

// Unwrap allows errors.As/errors.Is to traverse the underlying cause.
func (e RouterPrefixError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RouterPrefixError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("prefix", e.Prefix),
		slog.String("composed_topic", e.ComposedTopic),
		slog.Any("err", e.Err),
	)
}

// WithHandleCallback registers fn to run immediately after this route's
// *RouteHandle is successfully constructed, inside [Route.Register] —
// regardless of whether Register was called directly OR via a [Router]'s
// Register (which just calls the SAME unchanged registerHandle through
// the [routable] interface). Stored type-erased in routeBuilder exactly
// like requestFormats/formats. Unlike events' sibling (which can fire
// TWICE, once per role), this fires EXACTLY ONCE — reqreply has no
// fork-into-multiple-leaves: a single Route[Req,Resp] IS already the
// complete leaf, with exactly one Register call.
//
// A free function (not a method) — Go forbids new type parameters on a
// method; mirrors [api/rest.WithHandleCallback]'s identical resolution.
func WithHandleCallback[Req, Resp any](fn func(*RouteHandle[Req, Resp])) RouteOpt {
	return handleCallbackOpt{fn: fn}
}

type handleCallbackOpt struct{ fn any }

func (o handleCallbackOpt) applyRoute(rb *routeBuilder) { rb.handleCallback = o.fn }

// HandleCallbackTypeError is returned by [Route.Register] when a
// [WithHandleCallback] value's type doesn't match the Route's own
// Req/Resp — a caller programming error (mixing a callback built for one
// Route's Req/Resp into a different Route).
type HandleCallbackTypeError struct{ Err error }

func (e HandleCallbackTypeError) Error() string {
	return fmt.Sprintf("api/reqreply: handle callback: %v", e.Err)
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
// Mirrors [api/rest.ClientHandleOpt] exactly.
type ClientHandleOpt interface {
	applyClientHandle(r routable) routable
}

// WithRouter tells [Route.ClientHandle] to apply rt's CURRENT accumulated
// prefix+middleware before building the handle — the EXACT SAME
// composition `rt.Route(route)` + `rt.Register(b)` would have produced
// for route's SERVER side. Mirrors [api/rest.WithRouter]'s doc comment
// exactly (same rationale, same non-validating/infallible character).
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
// Mirrors [api/rest]'s identical routerTagsOpt exactly.
type routerTagsOpt struct{ tags []string }

func (o routerTagsOpt) applyRoute(rb *routeBuilder) {
	rb.meta.Tags = append(append([]string{}, o.tags...), rb.meta.Tags...)
}

// withRouterPrefix implements [routable] for [Route]. mws are PREPENDED
// into r's own opts list, ahead of r's own existing opts — mirrors
// [api/rest]'s Route.withRouterPrefix exactly (reqreply's Route stores
// everything via opts, same as REST's, unlike events' Subscriber/
// Publisher's direct mws field). tags are APPENDED to the END of r's own
// opts list instead — see [routerTagsOpt]'s doc comment for why tags
// merge differently than mws.
func (r Route[Req, Resp]) withRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	r.topic = joinRouterTopic(prefix, r.topic)
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
	return r, r.topic
}

// middlewareNames implements [routable] for [Route] — resolves r's own
// opts through a scratch [routeBuilder] (read-only; never mutates r), the
// SAME mechanism [ValidateRoute] already uses.
func (r Route[Req, Resp]) middlewareNames() []string {
	// A direct type-switch over r's own opts — NOT a scratch-routeBuilder
	// replay (unlike [Route.tags] below) — because rb.middlewares is only
	// conditionally populated for bound middleware (ONLY when it carries
	// a Security declaration — see [BoundMiddleware.applyBoundRoute]) and
	// rb.middlewareSpecContributions (populated unconditionally for
	// bound/codec-backed middleware) has no comparable per-leaf accessor
	// here; reading each opt's OWN captured name directly (routeMiddlewareOpt's
	// mws, boundHandleMWOpt/boundClientAttachOpt's name field) is simpler
	// and complete for every attachment class, without depending on which
	// rb field a given middleware happens to touch.
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

// tags implements [routable] for [Route] — resolves r's own opts through
// a scratch [routeBuilder] (read-only; never mutates r), reporting r's
// OWN, directly-declared [RouteMeta.Tags], BEFORE any Router involvement.
func (r Route[Req, Resp]) tags() []string {
	var rb routeBuilder
	for _, opt := range r.opts {
		opt.applyRoute(&rb)
	}
	return rb.meta.Tags
}

// registerAny implements [routable] for [Route] — delegates to
// [Route.Register], discarding the returned handle.
func (r Route[Req, Resp]) registerAny(b *Server) error {
	_, err := r.Register(b)
	return err
}
