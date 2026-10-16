package file

import (
	"fmt"
	"slices"
	"strings"

	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/router"
)

// routable is an internal alias for [router.Routable][*Client] — mirrors
// [github.com/DaniDeer/go-codex/api/events]'s identical unexported alias.
type routable = router.Routable[*Client]

// joinRouterPath joins a prefix and a path with exactly ONE "/" between
// them, regardless of whether either side already has a leading/trailing
// separator — mirrors [github.com/DaniDeer/go-codex/api/events]'s
// joinRouterTopic (no forced leading "/", unlike api/rest's joinRouterPath
// — file paths, like MQTT topics, don't use one).
func joinRouterPath(prefix, p string) string {
	prefix = strings.TrimSuffix(prefix, "/")
	p = strings.TrimPrefix(p, "/")
	switch {
	case prefix == "" && p == "":
		return ""
	case prefix == "":
		return p
	case p == "":
		return prefix
	default:
		return prefix + "/" + p
	}
}

// Router declares a path PREFIX once and groups any number of
// independently-declared role values (Reader[T], Creator[T], ...) under
// it, optionally attaching reusable-class [RouteMiddleware] to every
// leaf registered under it in one declaration — api/file's own thin
// wrapper around the shared [router.Router][*Client] core, mirroring
// [github.com/DaniDeer/go-codex/api/events.Router] method-for-method.
//
// Two coexisting, equally valid workflows (neither supersedes the
// other — same precedent as adapters/mqtt5's own "two valid Attach
// workflows" doc comment):
//
//  1. Build-and-register: rt.Route(leaf) for every role, then
//     rt.Register(client) once — mirrors events/rest exactly.
//
//  2. Build-the-Handle-directly: pass a Router value to [WithRouter] at
//     an individual role's own `.Handle(client, file.WithRouter(rt))`
//     call — the mechanism docs/roadmap/file-api.md's "Router/Middleware
//     integration" section documents as api/file's ONE path-construction
//     point (no later call site can forget to re-apply Router-group
//     composition, since there is no later construction site at all).
//
//     rt := file.NewRouter("tenants/{tenant}").
//     Use(authMiddleware).
//     Route(notes.WithRead(file.Read{})).
//     Route(notes.WithCreate(file.Create{}))
//     err := rt.Register(client)
type Router struct{ inner router.Router[*Client] }

// RouterOpt configures a [Router] at construction time — reserved for
// future extension; no concrete RouterOpt implementations ship yet (see
// [github.com/DaniDeer/go-codex/api/events.RouterOpt]'s identical
// rationale).
type RouterOpt interface{ applyRouter(*Router) }

// NewRouter declares a Router with a STATIC path prefix (no `{var}`
// placeholders — mirrors events/rest's own v1 scope decision). opts is
// reserved for future extension.
func NewRouter(prefix string, opts ...RouterOpt) Router {
	rt := Router{inner: router.NewRouter[*Client](prefix, joinRouterPath, wrapRouterPrefixError)}
	for _, o := range opts {
		o.applyRouter(&rt)
	}
	return rt
}

// Use returns a NEW Router with mws appended to its own accumulated,
// permanent middleware list — dispatched BEFORE every grouped leaf's own
// middleware (Router-first, outer-to-inner ordering).
func (rt Router) Use(mws ...RouteMiddleware) Router {
	return Router{inner: rt.inner.Use(mws...)}
}

// Tags returns a NEW Router with tags appended to its own accumulated,
// permanent tag list — merged into every grouped leaf's own reported
// [RouterEntry.Tags] (Router's own tags first, then the leaf's own).
// Phase 1 has no spec surface to consume these yet — see roleCore.tags'
// doc comment.
func (rt Router) Tags(tags ...string) Router {
	return Router{inner: rt.inner.Tags(tags...)}
}

// With returns a NEW Router whose NEXT [Router.Route] call ONLY receives
// mws, in addition to rt's own permanent middleware — one-shot, not
// permanent (mirrors events/rest's identical accumulate-semantics
// contract).
func (rt Router) With(mws ...RouteMiddleware) Router {
	return Router{inner: rt.inner.With(mws...)}
}

// Route returns a NEW Router with leaf attached — leaf's own prefix/
// middleware composition happens later, during [Router.Walk]/
// [Router.Register].
func (rt Router) Route(leaf routable) Router {
	return Router{inner: rt.inner.Route(leaf)}
}

// Mount returns a NEW Router with sub attached as a nested child — a NEW
// path segment (rt's prefix + sub's own prefix) and a FRESH middleware
// stack for everything under sub.
func (rt Router) Mount(sub Router) Router {
	return Router{inner: rt.inner.Mount(sub.inner)}
}

// Group returns a NEW Router with fn's built-up child incorporated — SAME
// prefix as rt (no new path segment), just a scoped middleware subset for
// a SUBSET of resources sharing rt's own path prefix.
func (rt Router) Group(fn func(sub Router) Router) Router {
	return Router{inner: rt.inner.Group(func(sub router.Router[*Client]) router.Router[*Client] {
		return fn(Router{inner: sub}).inner
	})}
}

// RouterEntry describes one leaf's FINAL, fully-assembled view, returned
// by [Router.Routes]/[Router.Walk].
type RouterEntry struct {
	// Role is this leaf's role ("read", "create", "patch", "delete",
	// "list", "exists", "copy", "move", "mkdir", "batchdelete").
	Role string
	// Path is the FINAL, prefix-applied path template.
	Path string
	// MiddlewareNames lists every middleware name that will apply to
	// this leaf, in dispatch order — Router-contributed names first,
	// then the leaf's own.
	MiddlewareNames []string
	// Tags lists every tag that will apply to this leaf — currently
	// inert (see roleCore.tags' doc comment).
	Tags []string
}

// WalkFunc is called once per LEAF, after full prefix+middleware
// composition. A non-nil error stops the walk immediately.
type WalkFunc func(entry RouterEntry) error

// Walk is the one true primitive — recurses through every Mount/Group,
// composing prefixes and middleware exactly as [Router.Register] would.
func (rt Router) Walk(fn WalkFunc) error {
	return rt.inner.Walk(func(e router.RouterEntry) error {
		return fn(RouterEntry{Role: e.Method, Path: e.Path, MiddlewareNames: e.MiddlewareNames, Tags: e.Tags})
	})
}

// Routes is a convenience wrapper over [Router.Walk] — collects every
// leaf into a flat, declaration-ordered slice. Walkable WITHOUT calling
// [Router.Register] (no [Client] needed).
func (rt Router) Routes() []RouterEntry {
	var entries []RouterEntry
	_ = rt.Walk(func(e RouterEntry) error {
		entries = append(entries, e)
		return nil
	})
	return entries
}

// Register walks the tree, composes every leaf's final prefix+
// middleware, and registers each one with c. Since api/file has no
// server-side spec-building/persistent-registration step (unlike REST's
// OpenAPI spec or events' ServeSubscribers), each leaf's own RegisterAny
// performs the closest faithful equivalent: a fail-fast CAPABILITY-
// COVERAGE check (via `.Handle(c)`, discarding the handle) — catching a
// Capability-coverage mismatch BEFORE the first real dispatch call,
// rather than lazily on first use. This is a genuinely useful check, not
// a meaningless stub — see each role's own RegisterAny doc comment.
func (rt Router) Register(c *Client) error {
	return rt.inner.Register(c)
}

// WithRouter builds a [HandleOpt] from rt's accumulated prefix+
// middleware — the "build-the-Handle-directly" workflow (see [Router]'s
// own doc comment for how this coexists with Route+Register). Reads
// rt's already-exported [router.Router.Prefix]/[router.Router.OwnMiddleware]
// accessors directly — no Route/Walk/Register involved.
func WithRouter(rt Router) HandleOpt {
	return withRouterOpt{prefix: rt.inner.Prefix(), mws: rt.inner.OwnMiddleware(), tags: rt.inner.OwnTags()}
}

// wrapRouterPrefixError is the [router.PrefixErrorFunc] supplied to
// [router.NewRouter] — api/file's path templates have no separate
// "prefix validation" codec distinct from PathParam's own per-variable
// codecs (unlike events' topic-codec/rest's path-codec), so there is
// currently no error shape to specially unwrap here; every failure
// propagates completely unwrapped, exactly as it would from a direct,
// Router-less `.Handle()` call.
func wrapRouterPrefixError(prefix, composed string, err error) (error, bool) {
	return nil, false
}

// ── routingInfo / HandleOpt (consumed by every role's .Handle()) ───────

// routingInfo carries the resolved path PREFIX and Router-contributed
// middleware/tags a Handle was built with — the ONE construction point
// for Router-group composition via the "build-the-Handle-directly"
// workflow (see [Router]'s own doc comment): a Router-grouped role is
// built via `reader.Handle(client, file.WithRouter(rt))` ONCE, producing
// a Handle that already carries the resolved, prefixed path and merged
// middleware; every subsequent [Client] dispatch call reuses THAT
// Handle, never re-deriving the path or re-supplying WithRouter.
type routingInfo struct {
	prefix string
	mws    []middleware.RouteMiddleware
	tags   []string
}

// HandleOpt configures a role's .Handle(client, opts...) call.
type HandleOpt interface{ applyHandle(*routingInfo) }

type withRouterOpt struct {
	prefix string
	mws    []middleware.RouteMiddleware
	tags   []string
}

func (o withRouterOpt) applyHandle(r *routingInfo) {
	r.prefix = o.prefix
	r.mws = o.mws
	r.tags = o.tags
}

// resolvedTemplate substitutes vars into template, after applying r's
// prefix (if any) — the ONE place path-prefix composition happens for
// the "build-the-Handle-directly" workflow.
func (r routingInfo) resolvedTemplate(template string) string {
	if r.prefix == "" {
		return template
	}
	return joinRouterPath(r.prefix, template)
}

// applyRouting folds routing's prefix/mws/tags into c — called once, at
// Handle-build time, by every role's own .Handle() method, AFTER opts
// have populated the Handle's own routing field. Router-contributed mws
// are PREPENDED (Router-first, outer-to-inner ordering, mirrors
// events/rest); tags are APPENDED (mirrors events.Router.Tags' own
// ordering rationale).
func (c roleCore[T]) applyRouting(routing routingInfo) roleCore[T] {
	if len(routing.mws) > 0 {
		c.mws = append(append([]RouteMiddleware{}, routing.mws...), c.mws...)
	}
	if len(routing.tags) > 0 {
		c.tags = append(append([]string{}, c.tags...), routing.tags...)
	}
	return c
}

// middlewareNames reports c.mws' names, in attachment order — mirrors
// [github.com/DaniDeer/go-codex/api/events.Subscriber.MiddlewareNames]'s
// identical extraction (legacy [middleware.Middleware]'s Name field, or
// an optional MiddlewareName() string method, or a %T fallback).
func (c roleCore[T]) middlewareNames() []string {
	names := make([]string, 0, len(c.mws))
	for _, mw := range c.mws {
		names = append(names, middlewareNameOf(mw))
	}
	return names
}

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

// routeTags reports c's OWN, directly-accumulated tags (via Router.Tags
// composition) — currently inert (see roleCore.tags' doc comment).
func (c roleCore[T]) routeTags() []string { return slices.Clone(c.tags) }

// ── router.Routable[*Client] implementations, one block per role ───────
//
// Each role's WithRouterPrefix returns a NEW role value (same concrete
// type) whose underlying Resource's OWN path template carries prefix
// permanently prepended — the mechanism [Router.Route]/[Router.Walk]/
// [Router.Register] rely on, mirroring
// [github.com/DaniDeer/go-codex/api/events.Subscriber.WithRouterPrefix]'s
// identical "mutate a fresh copy's own template" approach. This is
// DISTINCT from routingInfo/[WithRouter]'s per-Handle-call composition
// (used by the "build-the-Handle-directly" workflow) — both achieve the
// same observable effect (a Router-group's prefix+middleware applied
// exactly once), via the two coexisting workflows [Router]'s own doc
// comment describes.

func applyWithRouterPrefix[T any](core roleCore[T], prefix string, mws []middleware.RouteMiddleware, tags []string) roleCore[T] {
	core.resource.template = joinRouterPath(prefix, core.resource.template)
	if len(mws) > 0 {
		core.mws = append(append([]RouteMiddleware{}, mws...), core.mws...)
	}
	if len(tags) > 0 {
		core.tags = append(append([]string{}, core.tags...), tags...)
	}
	return core
}

// Reader[T]

func (r Reader[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	r.core = applyWithRouterPrefix(r.core, prefix, mws, tags)
	return r, r.core.resource.template
}
func (r Reader[T]) MiddlewareNames() []string { return r.core.middlewareNames() }
func (r Reader[T]) RouteTags() []string       { return r.core.routeTags() }
func (r Reader[T]) RegisterAny(c *Client) error {
	_, err := r.Handle(c)
	return err
}
func (r Reader[T]) RouteMethod() string { return "read" }

// Creator[T]

func (c Creator[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	c.core = applyWithRouterPrefix(c.core, prefix, mws, tags)
	return c, c.core.resource.template
}
func (c Creator[T]) MiddlewareNames() []string { return c.core.middlewareNames() }
func (c Creator[T]) RouteTags() []string       { return c.core.routeTags() }
func (c Creator[T]) RegisterAny(client *Client) error {
	_, err := c.Handle(client)
	return err
}
func (c Creator[T]) RouteMethod() string { return "create" }

// Patcher[T, P]

func (p Patcher[T, P]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	p.core = applyWithRouterPrefix(p.core, prefix, mws, tags)
	return p, p.core.resource.template
}
func (p Patcher[T, P]) MiddlewareNames() []string { return p.core.middlewareNames() }
func (p Patcher[T, P]) RouteTags() []string       { return p.core.routeTags() }
func (p Patcher[T, P]) RegisterAny(client *Client) error {
	_, err := p.Handle(client)
	return err
}
func (p Patcher[T, P]) RouteMethod() string { return "patch" }

// Deleter[T]

func (d Deleter[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	d.core = applyWithRouterPrefix(d.core, prefix, mws, tags)
	return d, d.core.resource.template
}
func (d Deleter[T]) MiddlewareNames() []string { return d.core.middlewareNames() }
func (d Deleter[T]) RouteTags() []string       { return d.core.routeTags() }
func (d Deleter[T]) RegisterAny(client *Client) error {
	_, err := d.Handle(client)
	return err
}
func (d Deleter[T]) RouteMethod() string { return "delete" }

// Lister[T]

func (l Lister[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	l.core = applyWithRouterPrefix(l.core, prefix, mws, tags)
	return l, l.core.resource.template
}
func (l Lister[T]) MiddlewareNames() []string { return l.core.middlewareNames() }
func (l Lister[T]) RouteTags() []string       { return l.core.routeTags() }
func (l Lister[T]) RegisterAny(client *Client) error {
	_, err := l.Handle(client)
	return err
}
func (l Lister[T]) RouteMethod() string { return "list" }

// Exister[T]

func (e Exister[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	e.core = applyWithRouterPrefix(e.core, prefix, mws, tags)
	return e, e.core.resource.template
}
func (e Exister[T]) MiddlewareNames() []string { return e.core.middlewareNames() }
func (e Exister[T]) RouteTags() []string       { return e.core.routeTags() }
func (e Exister[T]) RegisterAny(client *Client) error {
	_, err := e.Handle(client)
	return err
}
func (e Exister[T]) RouteMethod() string { return "exists" }

// Copier[T]

func (c Copier[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	c.core = applyWithRouterPrefix(c.core, prefix, mws, tags)
	return c, c.core.resource.template
}
func (c Copier[T]) MiddlewareNames() []string { return c.core.middlewareNames() }
func (c Copier[T]) RouteTags() []string       { return c.core.routeTags() }
func (c Copier[T]) RegisterAny(client *Client) error {
	_, err := c.Handle(client)
	return err
}
func (c Copier[T]) RouteMethod() string { return "copy" }

// Mover[T]

func (m Mover[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	m.core = applyWithRouterPrefix(m.core, prefix, mws, tags)
	return m, m.core.resource.template
}
func (m Mover[T]) MiddlewareNames() []string { return m.core.middlewareNames() }
func (m Mover[T]) RouteTags() []string       { return m.core.routeTags() }
func (m Mover[T]) RegisterAny(client *Client) error {
	_, err := m.Handle(client)
	return err
}
func (m Mover[T]) RouteMethod() string { return "move" }

// MkdirCreator[T]

func (m MkdirCreator[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	m.core = applyWithRouterPrefix(m.core, prefix, mws, tags)
	return m, m.core.resource.template
}
func (m MkdirCreator[T]) MiddlewareNames() []string { return m.core.middlewareNames() }
func (m MkdirCreator[T]) RouteTags() []string       { return m.core.routeTags() }
func (m MkdirCreator[T]) RegisterAny(client *Client) error {
	_, err := m.Handle(client)
	return err
}
func (m MkdirCreator[T]) RouteMethod() string { return "mkdir" }

// BatchDeleter[T]

func (b BatchDeleter[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	b.core = applyWithRouterPrefix(b.core, prefix, mws, tags)
	return b, b.core.resource.template
}
func (b BatchDeleter[T]) MiddlewareNames() []string { return b.core.middlewareNames() }
func (b BatchDeleter[T]) RouteTags() []string       { return b.core.routeTags() }
func (b BatchDeleter[T]) RegisterAny(client *Client) error {
	_, err := b.Handle(client)
	return err
}
func (b BatchDeleter[T]) RouteMethod() string { return "batchdelete" }

// StreamReader[T]

func (sr StreamReader[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	sr.core = applyWithRouterPrefix(sr.core, prefix, mws, tags)
	return sr, sr.core.resource.template
}
func (sr StreamReader[T]) MiddlewareNames() []string { return sr.core.middlewareNames() }
func (sr StreamReader[T]) RouteTags() []string       { return sr.core.routeTags() }
func (sr StreamReader[T]) RegisterAny(client *Client) error {
	_, err := sr.Handle(client)
	return err
}
func (sr StreamReader[T]) RouteMethod() string { return "stream-read" }

// StreamWriter[T]

func (sw StreamWriter[T]) WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (routable, string) {
	sw.core = applyWithRouterPrefix(sw.core, prefix, mws, tags)
	return sw, sw.core.resource.template
}
func (sw StreamWriter[T]) MiddlewareNames() []string { return sw.core.middlewareNames() }
func (sw StreamWriter[T]) RouteTags() []string       { return sw.core.routeTags() }
func (sw StreamWriter[T]) RegisterAny(client *Client) error {
	_, err := sw.Handle(client)
	return err
}
func (sw StreamWriter[T]) RouteMethod() string { return "stream-write" }

// ── compile-time Routable conformance (all 12 roles) ───────────────────

type conformanceRecord struct{}

var (
	_ routable = Reader[conformanceRecord]{}
	_ routable = Creator[conformanceRecord]{}
	_ routable = Patcher[conformanceRecord, conformanceRecord]{}
	_ routable = Deleter[conformanceRecord]{}
	_ routable = Lister[conformanceRecord]{}
	_ routable = Exister[conformanceRecord]{}
	_ routable = Copier[conformanceRecord]{}
	_ routable = Mover[conformanceRecord]{}
	_ routable = MkdirCreator[conformanceRecord]{}
	_ routable = BatchDeleter[conformanceRecord]{}
	_ routable = StreamReader[conformanceRecord]{}
	_ routable = StreamWriter[conformanceRecord]{}
)
