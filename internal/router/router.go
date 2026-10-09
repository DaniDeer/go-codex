// Package router provides the shared, pattern-agnostic Router/Mount/
// Group/Walk mechanics consolidated out of api/rest, api/events, and
// api/reqreply (see docs/design/d-0009-internalize-shared-mechanics.md's Phase 1
// and docs/design/d-0008-declarative-router-groups.md for the full design
// history). Each api package's OWN Router type is a thin wrapper around
// [Router][Target] (generic over the register-target type: *rest.Server,
// *events.Client, *reqreply.Server), never exposed directly to end users
// — see each package's own router.go for its wrapper shape.
//
// Package router is pure internal-implementation consolidation: it
// introduces zero NEW observable behavior for any existing caller of
// rest.Router/events.Router/reqreply.Router — every method here mirrors
// what those packages already implemented independently, 3 times, before
// this consolidation.
//
// DELIBERATELY scoped under internal/ (not the repo root) — this package
// is pure cross-pattern MECHANICS, never meant to be imported by an
// end user of go-codex directly (only by the api/rest, api/events,
// api/reqreply packages that wrap it, and any future in-module
// ports.Cache/ports.File consumer). Go's own internal/ import rule
// enforces this structurally: nothing outside the go-codex module tree
// can import this package at all, regardless of documentation/convention.
// See .github/instructions/go-codex.instructions.md's Design Philosophy
// section for the general rule this package is the reference example of.
package router

import (
	"fmt"

	"github.com/DaniDeer/go-codex/internal/middleware"
)

// Routable is satisfied by every leaf type a [Router] can hold, generic
// over Target (the register-target type: *rest.Server, *events.Client,
// *reqreply.Server) — deliberately limited to the 4 methods genuinely
// UNIFORM across all 3 api packages. See [MethodReporter] for the
// method/role-equivalent concept, which is NOT uniform (api/reqreply has
// no such concept at all — see docs/design/d-0009-internalize-shared-mechanics.md's
// Phase 1 Design Decision #4 for the full investigation that confirmed
// this).
type Routable[Target any] interface {
	// WithRouterPrefix returns a NEW leaf value (same concrete type) with
	// prefix prepended to its own path/topic string, mws PREPENDED to its
	// own accumulated middleware (so Router-contributed middleware
	// dispatches BEFORE the leaf's own), and tags APPENDED as a merge-opt
	// (so Router-contributed tags combine with, rather than being
	// overwritten by, the leaf's own declared tags — see [Router.Tags]'s
	// doc comment for why tags must be appended, not prepended, unlike
	// mws) — plus the leaf's resulting, fully-composed path/topic, so
	// callers never need a second accessor to learn it.
	WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (Routable[Target], string)
	// MiddlewareNames reports the leaf's OWN (directly .Use()-attached,
	// pre-Router) reusable-class middleware names, in attachment order.
	MiddlewareNames() []string
	// RouteTags reports the leaf's OWN (directly declared, pre-Router)
	// tags, for [RouterEntry.Tags].
	RouteTags() []string
	// RegisterAny performs the SAME work registering the leaf directly
	// would, discarding any returned handle — a caller needing the handle
	// back attaches a handle-callback opt to the leaf directly instead;
	// Router itself never exposes one.
	RegisterAny(target Target) error
}

// MethodReporter is an OPTIONAL interface a [Routable] leaf MAY also
// implement, when it has a method/role-equivalent concept to report
// (REST's HTTP verb, events' subscribe/publish role). [Router.Walk]/
// [Router.Routes] type-asserts for it, falling back to "" when a leaf
// doesn't implement it (api/reqreply's case — it has no such concept at
// all). Mirrors this codebase's existing optional-extension convention
// (stats.FileObserver/SQLObserver/etc.) rather than forcing every leaf
// type to implement a method it has no meaningful answer for.
type MethodReporter interface {
	RouteMethod() string
}

// JoinFunc joins a Router's accumulated prefix with a leaf's own path/
// topic — REST's own [JoinFunc] implementation always produces a
// leading-slash result; events'/reqreply's omit the leading slash
// (MQTT/ZeroMQ-style topics don't use one). Supplied once per
// instantiation via [NewRouter].
type JoinFunc func(prefix, leaf string) string

// PrefixErrorFunc lets each api package decide WHETHER and HOW to wrap a
// leaf's registration failure into its own RouterPrefixError type —
// called at the exact point of failure inside [Router.register], with
// the Router's own accumulated prefix, the leaf's final composed path/
// topic, and the raw error RegisterAny returned. Returning ok=false
// propagates err COMPLETELY UNWRAPPED (every failure mode that ISN'T "this
// leaf's final, prefixed path/topic failed its own validation").
// Supplied once per instantiation via [NewRouter].
type PrefixErrorFunc func(prefix, composed string, err error) (wrapped error, ok bool)

// middlewareNameOf extracts a human-readable name from mw for
// [RouterEntry.MiddlewareNames] — mirrors the identical extraction each
// api package's own middleware-opt application already performs for
// legacy [middleware.Middleware] values (a plain Name field) and
// codec-backed ones (a MiddlewareName() string method), falling back to a
// type name for anything else so the list is never silently incomplete.
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

// RouterOpt configures a [Router] at construction time — reserved for
// future extension (e.g. a Router-scoped fallback handler or OpenAPI tag
// auto-population); no concrete RouterOpt implementations ship in any api
// package yet.
type RouterOpt[Target any] interface{ applyRouter(*Router[Target]) }

// routerChild is one entry in a [Router]'s accumulated children — EITHER
// a leaf (via [Router.Route]) OR a nested [Router] (via [Router.Mount] or
// [Router.Group], which share this SAME representation; see
// [Router.Group]'s own doc comment).
type routerChild[Target any] struct {
	leaf    Routable[Target]
	leafMws []middleware.RouteMiddleware // this Router's own + any .With() one-shot mws, captured at .Route() time
	sub     *Router[Target]
}

// Router declares a path/topic PREFIX once and groups any number of
// independently-declared leaves under it, optionally attaching
// reusable-class [middleware.RouteMiddleware] to every leaf registered
// under it in one declaration — modeled on chi's own Router/Mount/Group/
// With/Routes/Walk (go-chi/chi/v5, the strongest available prior art for
// this exact problem).
//
// Router is a fully IMMUTABLE VALUE type — every method below returns a
// NEW value, never mutates the receiver. This is a DELIBERATE choice:
// Router is an intermediate, composable declarative value, not a terminal
// builder — making it immutable means concurrent reads/derivations of the
// same value need NO synchronization at all, by construction.
//
// Router itself is never used directly by an api package's own caller —
// each api package (rest/events/reqreply) wraps [Router][Target] in its
// own, package-local Router type (a thin wrapper, not a type alias — see
// docs/design/d-0009-internalize-shared-mechanics.md's Phase 1 Design Decision #2
// for why a pure alias doesn't work here) with its own NewRouter
// constructor supplying a pattern-specific [JoinFunc]/[PrefixErrorFunc].
type Router[Target any] struct {
	prefix     string
	mws        []middleware.RouteMiddleware
	pendingMws []middleware.RouteMiddleware
	tags       []string
	children   []routerChild[Target]

	join          JoinFunc
	wrapPrefixErr PrefixErrorFunc
}

// NewRouter declares a Router with a STATIC path/topic prefix (no
// `{var}` placeholders for v1), a pattern-specific join strategy, and a
// pattern-specific prefix-error-wrapping strategy. opts is reserved for
// future extension; no concrete [RouterOpt] implementations ship yet.
func NewRouter[Target any](prefix string, join JoinFunc, wrapPrefixErr PrefixErrorFunc, opts ...RouterOpt[Target]) Router[Target] {
	rt := Router[Target]{prefix: prefix, join: join, wrapPrefixErr: wrapPrefixErr}
	for _, o := range opts {
		o.applyRouter(&rt)
	}
	return rt
}

// Use returns a NEW Router with mws appended to its own accumulated,
// permanent middleware list — dispatched BEFORE every grouped leaf's own
// middleware (Router-first, outer-to-inner ordering), and before any
// nested [Router.Mount]/[Router.Group] child's own mws.
func (rt Router[Target]) Use(mws ...middleware.RouteMiddleware) Router[Target] {
	rt.mws = append(cloneMws(rt.mws), mws...)
	return rt
}

// Tags returns a NEW Router with tags appended to its own accumulated
// tag list — combined with (never overwriting) every grouped leaf's own
// declared tags.
func (rt Router[Target]) Tags(tags ...string) Router[Target] {
	rt.tags = append(cloneTags(rt.tags), tags...)
	return rt
}

// Prefix returns rt's OWN, single-level accumulated path/topic prefix
// segment — NOT composed with any ancestor [Router.Mount]/[Router.Group]
// parent's own prefix (mirrors this package's pre-consolidation
// behavior exactly: a Router's own `prefix` field was always
// single-level, never recursively pre-composed; composition only
// happens during [Router.Walk]/[Router.Register]'s own traversal). Used
// by each api package's own `WithRouter`-style `ClientHandleOpt` to apply
// rt's current, OWN composition to a route/channel declared outside this
// Router's own tree.
func (rt Router[Target]) Prefix() string { return rt.prefix }

// OwnMiddleware returns a COPY of rt's own accumulated, permanent
// middleware list (see [Router.Prefix]'s doc comment for the same
// single-level, non-ancestor-composed caveat).
func (rt Router[Target]) OwnMiddleware() []middleware.RouteMiddleware { return cloneMws(rt.mws) }

// OwnTags returns a COPY of rt's own accumulated tag list (see
// [Router.Prefix]'s doc comment for the same single-level caveat).
func (rt Router[Target]) OwnTags() []string { return cloneTags(rt.tags) }

// With returns a NEW Router with mws staged as ONE-SHOT, pending
// middleware — consumed (and cleared) by the VERY NEXT [Router.Route]
// call only; a following [Router.Mount]/[Router.Group] call DISCARDS any
// still-pending mws rather than silently leaking them onto a later,
// unrelated leaf.
func (rt Router[Target]) With(mws ...middleware.RouteMiddleware) Router[Target] {
	rt.pendingMws = append(cloneMws(rt.pendingMws), mws...)
	return rt
}

// Route returns a NEW Router with r attached as a leaf child, consuming
// (and clearing) any pending [Router.With] middleware.
func (rt Router[Target]) Route(r Routable[Target]) Router[Target] {
	pending := rt.pendingMws
	rt.pendingMws = nil
	rt.children = append(cloneChildren(rt.children), routerChild[Target]{leaf: r, leafMws: pending})
	return rt
}

// Mount returns a NEW Router with sub attached as a nested child — a NEW
// path segment (rt's prefix + sub's own prefix) and a FRESH middleware
// stack for everything under sub (rt's own mws run first, then sub's own,
// then each leaf's own — outer-to-inner, outermost-declared-first).
//
// Any still-pending [Router.With] mws on rt are DISCARDED here, exactly
// as [Router.Route] would consume (clear) them — Mount has no single leaf
// to attach a one-shot middleware to.
func (rt Router[Target]) Mount(sub Router[Target]) Router[Target] {
	rt.pendingMws = nil
	rt.children = append(cloneChildren(rt.children), routerChild[Target]{sub: &sub})
	return rt
}

// Group returns a NEW Router with fn's built-up child incorporated — SAME
// prefix as rt (NO new path/topic segment), just a scoped middleware
// subset for a SUBSET of routes sharing rt's own path (chi's own Group).
// fn receives an EMPTY child Router (inheriting rt's own join/
// wrapPrefixErr strategies) and MUST explicitly return its built-up
// value, since Router is immutable.
//
// rt.Group(fn) RETURNS rt ITSELF (the PARENT, updated), NOT the child fn
// received. Internally, Group and Mount share the SAME child
// representation (routerChild.sub) — Group's child simply contributes an
// empty prefix segment; there is no second, parallel tree-walking
// implementation.
func (rt Router[Target]) Group(fn func(sub Router[Target]) Router[Target]) Router[Target] {
	child := fn(Router[Target]{join: rt.join, wrapPrefixErr: rt.wrapPrefixErr})
	return rt.Mount(child)
}

// RouterEntry describes one leaf's FINAL, fully-assembled view, returned
// by [Router.Routes]/[Router.Walk] — the "holistic, pre-registration
// overview of how the API assembles" the design behind Router set out to
// provide.
//
// Method is populated via a [MethodReporter] type-assertion on the leaf,
// left "" when the leaf doesn't implement it (api/reqreply's case). Each
// api package's OWN, UNCHANGED RouterEntry type (Method for rest, Role for
// events, omitted entirely for reqreply) is built FROM this shared shape
// by that package's own Router wrapper's Walk/Routes — never exposed to
// callers verbatim under THIS package's name.
type RouterEntry struct {
	// Method is this leaf's method/role-equivalent, or "" when the leaf
	// doesn't implement [MethodReporter].
	Method string
	// Path is the FINAL, prefix-applied path/topic.
	Path string
	// MiddlewareNames lists every reusable-class middleware name that will
	// apply to this leaf, in dispatch order — Router-contributed names
	// first (outermost ancestor first), then the leaf's own.
	MiddlewareNames []string
	// Tags lists every tag that will apply to this leaf's spec entry —
	// Router-contributed tags first (outermost ancestor first), then the
	// leaf's own declared tags.
	Tags []string
}

// WalkFunc is called once per LEAF (never per intermediate Router/Group),
// after full prefix + middleware composition. A non-nil error stops the
// walk immediately and is returned as-is (no wrapping) — mirrors chi's
// own Walk short-circuit behavior.
type WalkFunc func(entry RouterEntry) error

// Walk is the one true primitive — recurses through every Mount/Group,
// composing prefixes and middleware exactly as [Router.Register] would,
// calling fn once per leaf, in declaration order.
func (rt Router[Target]) Walk(fn WalkFunc) error {
	return rt.walk("", nil, nil, fn)
}

func (rt Router[Target]) walk(ancestorPrefix string, ancestorMws []middleware.RouteMiddleware, ancestorTags []string, fn WalkFunc) error {
	prefix := rt.join(ancestorPrefix, rt.prefix)
	mws := append(cloneMws(ancestorMws), rt.mws...)
	tags := append(cloneTags(ancestorTags), rt.tags...)
	for _, c := range rt.children {
		if c.sub != nil {
			// sub already carries its own join/wrapPrefixErr, set at its
			// own construction (NewRouter) time or inherited explicitly
			// in [Router.Group]'s case — always identical to rt's own
			// within one api package's Router instantiations.
			if err := c.sub.walk(prefix, mws, tags, fn); err != nil {
				return err
			}
			continue
		}
		allMws := append(cloneMws(mws), c.leafMws...)
		transformed, composedPath := c.leaf.WithRouterPrefix(prefix, allMws, tags)
		names := make([]string, 0, len(allMws)+len(c.leaf.MiddlewareNames()))
		for _, mw := range allMws {
			names = append(names, middlewareNameOf(mw))
		}
		names = append(names, c.leaf.MiddlewareNames()...)
		entryTags := append(cloneTags(tags), c.leaf.RouteTags()...)
		method := ""
		if mr, ok := transformed.(MethodReporter); ok {
			method = mr.RouteMethod()
		}
		entry := RouterEntry{
			Method:          method,
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

// Routes is a convenience wrapper over [Router.Walk] — collects every
// leaf into a flat, declaration-ordered slice. Walkable and printable
// WITHOUT calling [Router.Register] (no Target needed), so a caller can
// inspect/log/assert on the assembled tree in a test or at startup,
// before committing to registration.
func (rt Router[Target]) Routes() []RouterEntry {
	var entries []RouterEntry
	_ = rt.Walk(func(e RouterEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries
}

// Register registers every leaf under rt, in declaration order, applying
// rt's own accumulated prefix/middleware/tags composed with every
// ancestor Router's own. Returns the FIRST error encountered (declaration
// order), completely unwrapped UNLESS [PrefixErrorFunc] opts in to
// wrapping it.
func (rt Router[Target]) Register(target Target) error {
	return rt.register("", nil, nil, target)
}

func (rt Router[Target]) register(ancestorPrefix string, ancestorMws []middleware.RouteMiddleware, ancestorTags []string, target Target) error {
	prefix := rt.join(ancestorPrefix, rt.prefix)
	mws := append(cloneMws(ancestorMws), rt.mws...)
	tags := append(cloneTags(ancestorTags), rt.tags...)
	for _, c := range rt.children {
		if c.sub != nil {
			if err := c.sub.register(prefix, mws, tags, target); err != nil {
				return err
			}
			continue
		}
		allMws := append(cloneMws(mws), c.leafMws...)
		transformed, composedPath := c.leaf.WithRouterPrefix(prefix, allMws, tags)
		if err := transformed.RegisterAny(target); err != nil {
			if wrapped, ok := rt.wrapPrefixErr(prefix, composedPath, err); ok {
				return wrapped
			}
			return err
		}
	}
	return nil
}

func cloneMws(mws []middleware.RouteMiddleware) []middleware.RouteMiddleware {
	if len(mws) == 0 {
		return nil
	}
	out := make([]middleware.RouteMiddleware, len(mws))
	copy(out, mws)
	return out
}

func cloneChildren[Target any](children []routerChild[Target]) []routerChild[Target] {
	if len(children) == 0 {
		return nil
	}
	out := make([]routerChild[Target], len(children))
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
