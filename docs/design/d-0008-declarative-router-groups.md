# D-0008 — Declarative Router Groups — `api/rest`, `api/events`, `api/reqreply`

> **Status:** Implemented — architectural foundation. Phases A/B/C fully
> SHIPPED (`api/rest`, `api/events`, `api/reqreply` + the deferred-item
> review, see "Phase C, sub-step 2" below — all 6 items confirmed
> correctly deferred, zero new work promoted; item 3 spun off a separate
> idea-stage doc, [`typed-router-groups.md`](../roadmap/typed-router-groups.md))
> — see `api/rest/router.go`/`api/events/router.go`/`api/reqreply/router.go`,
> `docs/features/router-groups.md`.
> PROMOTED from `docs/roadmap/declarative-router-groups.md` — this doc
> establishes a pattern all 3 messaging APIs (`rest`/`events`/`reqreply`)
> now follow (declarative, chi-inspired path/topic-prefix grouping +
> group-wide middleware), qualifying it as architectural foundation
> rather than a single-feature roadmap, mirroring
> [D-0007](d-0007-declarative-middleware-layering.md)'s own identical
> promotion precedent.

## Motivation

go-codex's existing workflow is **declare → compose → register**: declare a
`Route`/`Channel` (path/topic + codecs), compose it with `Middleware`/
`BoundMiddleware` (see [`d-0007-declarative-middleware-layering.md`](../design/d-0007-declarative-middleware-layering.md)),
then register it with a `Server`/`Client`. This works well for ONE route/channel
at a time, but has no answer for a THIRD, group-level concern a real API always
has: "this whole family of routes lives under `/api/v1/users`, and ALL of them
need the same security middleware" — today a user must either repeat the full
path string and the same `.Use(mw)` call on every single route (copy-paste,
error-prone, no single source of truth for the prefix), or invent their own ad
hoc Go helper to stitch path strings together outside go-codex's own API.

Investigated what today's declarative middleware mechanism (shipped by
[`d-0007-declarative-middleware-layering.md`](../design/d-0007-declarative-middleware-layering.md), now fully rolled out
across all 3 patterns) already provides toward this, confirmed via code — see
"What exists today" below. **Nothing exists today that solves prefix
composition or group-wide middleware attachment** — this is net-new surface,
not a gap in an existing mechanism.

This is explicitly modeled on Traefik's own Router/Middleware separation (a
Router matches a path/rule and attaches Middlewares to everything it matches;
Middleware itself stays a separate, reusable concern) and, more directly
relevant prior art in Go, `chi.Router.Route(pattern, fn)`/`.Mount(pattern,
subRouter)`/`.Group(fn)` — though (confirmed via code) `adapters/chi` never
actually uses chi's own native grouping internally, so there is no adapter-level
shortcut to lean on either; this has to be a genuinely new `api/*`-level
construct, mirroring how `Middleware`/`SecurityScheme`/`ErrorPattern` are each
their own `api/*`-level concept with per-pattern siblings, not an
adapter-level one.

## What exists today (confirmed via code, not assumed)

| Mechanism | What it solves | What it does NOT solve |
|---|---|---|
| `rest.NewRoute[Req,Resp](method, path, ...)` / `events.NewChannel[T](topic, ...)` / `reqreply.NewRoute[Req,Resp](topic, ...)` | Declares ONE route/channel's full path/topic string | No prefix composition — `path`/`topic` is always a complete string, set once, at construction |
| `rest.Path` / `events.Topic` (reusable template+params) | Reuses the SAME path/topic shape across 2+ routes with DIFFERENT Req/Resp types (e.g. GET+DELETE on one resource) | No prefix semantics — still a complete, standalone template, not a composable prefix+suffix pair |
| `Server`/`Client` (reqreply) | Holds a FLAT `[]routeEntry`/equivalent list, mutex-guarded for concurrent `Register` calls | No hierarchy, no grouping, no notion of "these N entries share a prefix" |
| `middleware.Middleware[In,Out]` + `.Use(mw)` | ONE reusable-class middleware value attachable to any number of routes/channels, individually, one `.Use()` call per route | No "attach to every route under this prefix in one declaration" — still one call per route |
| `RouteMeta.Tags []string` | OpenAPI spec-only grouping metadata (cosmetic, for generated docs) | Not a routing/registration mechanism at all — purely descriptive |
| `adapters/chi` | Wires each route individually onto a flat `gochi.Router` via `r.Method(...)` | Never uses chi's own native `Mount`/`Route`/`Group` — no adapter-level prior art to borrow |

**Conclusion**: the user's own framing is confirmed accurate — "before
registering the routes/channels, paths/topics are split across many packages
or the user invents their own method to assemble prefixes." A Router construct
is a genuinely missing piece, not a refinement of something partially there.

## Chi framework survey (reference prior art)

Read `go-chi/chi/v5` (v5.3.1, the project's own vendored dependency) directly
from source — confirmed it's the strongest available prior art for this
exact problem, and that its design separates MORE distinct concepts than
this doc's first draft accounted for:

| chi mechanism | Shape | What it maps to in this design |
|---|---|---|
| `Router.Mount(pattern, h)` / `Router.Route(pattern, fn) Router` | NEW path segment + a FRESH middleware stack for everything under it | `Router.Mount(sub Router)` (already in this doc's draft; `Router` is an immutable value in go-codex's own design — see "Foundational reconsideration" below — not a pointer as chi's own `Mux` is) |
| `Router.Group(fn func(r Router)) Router` | **SAME** path (no new segment) — just a scoped COPY of the current middleware stack, for a SUBSET of routes sharing one path (chi's own doc comment: "useful for a group of handlers along the same routing path that use an additional set of middlewares") | **Missing from the first draft** — added below as `Router.Group(fn func(sub Router) Router)` (go-codex's own immutable-value design requires `fn` to explicitly return its built-up value, unlike chi's own mutation-style callback) |
| `Router.With(mws ...) Router` | Ad hoc, ONE-OFF extra middleware for a SINGLE next-declared route — no permanent group, no new `Router` value kept around | **Missing from the first draft** — added below as `Router.With(mws ...middleware.RouteMiddleware) Router` |
| `Routes() []Route` + `chi.Walk(r Routes, walkFn WalkFunc) error` | `Walk` is a RECURSIVE VISITOR — it walks `Routes()`'s tree (which nests `SubRoutes` for every `Mount`), composing the full path AND the full middleware stack per leaf, calling `walkFn(method, fullPath, handler, middlewares...)` once per leaf. `Routes()` itself only returns ONE LEVEL of the tree (sub-mounts are opaque `Route.SubRoutes` entries) — `Walk` is what actually flattens/composes it. This is the EXACT mechanism chi's own `docgen` subpackage (OpenAPI/route-doc generation) builds on. | Confirms this doc's `Router.Routes()` idea is sound, but shows the right INTERNAL shape is a visitor (`Walk`), with the flat, pre-materialized `[]RouterEntry` as a convenience WRAPPER over it — not a parallel, independently-implemented mechanism. Added `Router.Walk(fn WalkFunc) error` below. |
| `Router.NotFound(h)` / `Router.MethodNotAllowed(h)` | Per-(sub)router fallback handler — a `Mount`-ed sub-chi-Router can have its OWN "nothing matched" behavior, distinct from the parent's | Not in this doc's scope decisions at all — flagged as a new Phase 2+ open question below (no committed design) |
| `middleware/` subpackage (`Logger`, `Recoverer`, `Throttle`, `BasicAuth`, `Compress`, `RequestID`, ~20 more) | Ready-made, OPERATIONAL `func(http.Handler) http.Handler` implementations | **Explicitly unrelated** — this is a middleware CATALOG (concrete behaviors), not a routing/grouping STRUCTURE; go-codex's own `middleware.Middleware[In,Out]` mechanism is the equivalent catalog-of-behaviors concern, already shipped, orthogonal to this doc entirely. No action. |

**Also confirmed**: stdlib `http.ServeMux` — what `adapters/nethttp` actually
wraps (not `adapters/chi`) — has **even less** structure than chi here: no
`Mount`/`Group`/`With`/`Walk` equivalent of any kind, just flat
`Handle(pattern, handler)` registration (Go 1.22+ added method+wildcard
PATTERN syntax, but nothing resembling grouping). This reinforces the
existing scope decision that a Router MUST live at the `api/*` level,
uniformly across all 3 patterns — there is no adapter to delegate to for
EITHER of go-codex's 2 HTTP adapters, let alone the non-HTTP ones (MQTT/
MQTT5/ZeroMQ have no router concept of their own at all).

## Scope decisions (confirmed via `ask_user`)

| In scope | Out of scope (this doc) |
|---|---|
| `rest.Router` (path prefixes), `events.Router` (topic prefixes), `reqreply.Router` (topic prefixes) — all 3 patterns designed now, mirroring `d-0007-declarative-middleware-layering.md`'s own all-3-patterns precedent | `api/mcp` — tool names/resource URI templates don't have the same hierarchical-path shape; flagged as an **open item** in [`mcp-ports-declarative-middleware.md`](../roadmap/mcp-ports-declarative-middleware.md)'s "Open design decisions" instead (see that doc's update, not designed here) |
| Nesting — a Router may `Mount` sub-Routers (prefixes compose: parent + child) | Dynamic/runtime router mutation after `Register()` — mirrors the existing "no hot-adding routes to an already-`Serve`'d mux" limitation (`docs/roadmap/dynamic-port-rebinding.md`'s own scope) |
| STATIC prefixes only (e.g. `"/api/v1"`, `"tenants/eu"`) — no `{var}` placeholders in a Router's own prefix for v1 | Variable prefixes (e.g. `"/tenants/{tenantID}"`) merging into every grouped route's own `Req`/`T` — deferred; see "Out of scope (Phase 2+)" |
| Reusable-class `Middleware[In,Out]`/`Declaration[In,Out]` attachment at the Router level (`.Use(mw)`-equivalent, group-wide) | Bound-class (`BoundMiddleware[Req,...]`) attachment at the Router level — structurally impossible to express group-wide (a `BoundMiddleware` is tied to ONE concrete `Req`/`T` type; a Router groups routes of DIFFERENT `Req`/`T` types) — bound middlewares stay route-specific, unchanged |
| A `Router.Routes()`-style inspection accessor — the "final, holistic view of the assembled path/topic tree" the user explicitly asked for | Spec-rendering changes (OpenAPI/AsyncAPI tag auto-assignment from Router prefix) — noted as a nice-to-have, not committed |

## Reference pattern being designed (one shape, 3 packages)

```go
// api/rest
// Router is a fully IMMUTABLE VALUE type — matching Route[Req,Resp]/
// Channel[T]/Subscriber[T]/Publisher[T] exactly (every chained method
// returns a NEW value, never mutates in place). Confirmed via a later
// review round: this is the CORRECT tier for Router (an intermediate,
// composable declarative value), not the mutable-pointer tier Server/
// Client occupy (the TERMINAL, final-stage builders) — Router had
// initially, incorrectly, followed the wrong tier's convention. Because
// of this, Router needs NO mutex anywhere — concurrent reads/derivations
// of the SAME immutable value need zero synchronization, by construction
// (the SAME property every other api/* declarative type already has,
// with zero special-casing ever needed for them either).
type Router struct { /* prefix string; mws, pendingMws []middleware.RouteMiddleware; children []routerChild */ }
func NewRouter(prefix string, opts ...RouterOpt) Router
func (rt Router) Use(mws ...middleware.RouteMiddleware) Router
func (rt Router) Route(r routable) Router   // attach a leaf Route[Req,Resp]/SSERoute[Req,Resp]
func (rt Router) Mount(sub Router) Router   // attach a nested sub-Router (NEW prefix segment + fresh mw stack)
func (rt Router) Group(fn func(sub Router) Router) Router // SAME prefix, scoped mw subset (chi's Group — no new
                                                            // segment); fn MUST explicitly return its built-up
                                                            // value (no mutation to observe otherwise, since
                                                            // Router is immutable) — rt.Group(fn) still returns
                                                            // rt itself (the PARENT, updated), diverging
                                                            // deliberately from chi's own Group (which returns
                                                            // the CHILD), for consistency with every other method
                                                            // here, all of which chain off rt
func (rt Router) With(mws ...middleware.RouteMiddleware) Router // one-shot extra mw for the NEXT .Route() call
                                                            // only — see "With" below for how immutability alone
                                                            // (no special type) achieves this
func (rt Router) Register(b *Server) error  // walk the tree, compose prefixes+mws, Register every leaf
func (rt Router) Routes() []RouterEntry     // inspect the fully-assembled path tree BEFORE Register (flat)
func (rt Router) Walk(fn WalkFunc) error    // recursive visitor; Routes() is a convenience wrapper over this

// api/events (topic prefixes instead of path prefixes — same shape)
type Router struct { /* prefix string; mws, pendingMws []middleware.RouteMiddleware; children []routerChild */ }
func NewRouter(prefix string, opts ...RouterOpt) Router
func (rt Router) Use(mws ...middleware.RouteMiddleware) Router
func (rt Router) Route(r routable) Router   // attach a Subscriber[T]/Publisher[T]
func (rt Router) Mount(sub Router) Router
func (rt Router) Group(fn func(sub Router) Router) Router // returns rt, same rationale as REST's above
func (rt Router) With(mws ...middleware.RouteMiddleware) Router
func (rt Router) Register(client *Client) error
func (rt Router) Routes() []RouterEntry
func (rt Router) Walk(fn WalkFunc) error

// api/reqreply (topic prefixes, mirrors events' Router exactly, Server-scoped —
// uses the current, non-deprecated *Server name; Route.Register itself still
// says *Builder today, a deprecated alias, not perpetuated in this new code)
type Router struct { /* prefix string; mws, pendingMws []middleware.RouteMiddleware; children []routerChild */ }
func NewRouter(prefix string, opts ...RouterOpt) Router
func (rt Router) Use(mws ...middleware.RouteMiddleware) Router
func (rt Router) Route(r routable) Router   // attach a Route[Req,Resp]
func (rt Router) Mount(sub Router) Router
func (rt Router) Group(fn func(sub Router) Router) Router // returns rt, same rationale as REST's above
func (rt Router) With(mws ...middleware.RouteMiddleware) Router
func (rt Router) Register(b *Server) error
func (rt Router) Routes() []RouterEntry
func (rt Router) Walk(fn WalkFunc) error
```

### How a leaf `Route[Req,Resp]`/`Channel`/etc. attaches without a new generic method type-parameter

Go forbids a method from introducing new type parameters beyond its
receiver's own — `Router.Route` cannot be `func (rt Router) Route[Req, Resp
any](r Route[Req, Resp]) Router`. The resolution mirrors
d-0007's own resolution for this identical problem (the interface-plus-receiver-scoped-type-params approach already shipped there):
define a small, unexported interface that `Route[Req,Resp]`/`SSERoute[Req,Resp]`
(REST), `Subscriber[T]`/`Publisher[T]` (events), and `reqreply.Route[Req,Resp]`
each satisfy via methods ADDED to their EXISTING receiver (Req/Resp/T are
already in scope on the receiver — no NEW type parameter is introduced):

```go
// routable is satisfied by every leaf type a Router can hold, regardless of
// its own Req/Resp/T type parameters — the SAME resolution
// d-0007's own "BoundMiddleware" attachment resolution already established
// for exactly this same "new type param on a method" problem.
//
// NOTE: this is NOT one single interface shared literally across all 3
// packages — each package (api/rest, api/events, api/reqreply) defines its
// OWN, separately-compiled, unexported `routable` interface, using ITS OWN
// concrete builder type for registerAny's parameter (*Server below is
// REST's/reqreply's own; api/events' own routable interface uses *Client
// instead — never `any`, since each is package-private and every
// implementer within that package already knows its own concrete type).
// The sketch below shows REST's own instantiation as the representative
// example; events'/reqreply's versions are structurally identical, just
// with their own pattern's types substituted throughout.
type routable interface {
    // withRouterPrefix returns a NEW leaf value (same concrete type) with
    // prefix prepended to its own path/topic string and mws PREPENDED to its
    // own accumulated opts (router-level middleware runs BEFORE the route's
    // own — see "Prefix + middleware composition semantics" below).
    withRouterPrefix(prefix string, mws []middleware.RouteMiddleware) routable
    // method reports this leaf's HTTP method, for RouterEntry.Method — needed
    // because Walk/Routes() has NO OTHER way to populate that field from a
    // type-erased routable value. SCOPED PER PACKAGE, same as this whole
    // interface (see this block's own NOTE above): this method() accessor
    // (and RouterEntry.Method below) exist ONLY on api/rest's own routable/
    // RouterEntry — api/events' own routable interface declares role()
    // INSTEAD (no method() at all, since events has no HTTP-method axis);
    // api/reqreply's own routable interface declares NEITHER (confirmed in
    // its own Design Decisions section: no second axis exists at all for
    // reqreply). Each pattern's interface/struct carries ONLY the accessor/
    // field that actually applies to it — never a dead, always-"" stand-in
    // for an axis that pattern doesn't have.
    method() string
    // middlewareNames reports the leaf's OWN (pre-Router, directly-attached
    // via .Use()) reusable-class middleware names, in attachment order —
    // trivially derivable since middleware.Middleware/declaration types
    // already carry a Name string field. Walk PREPENDS the Router's own
    // contributed names (tracked by Router itself, not the leaf) to this
    // leaf-reported list when populating RouterEntry.MiddlewareNames —
    // needed for the exact same reason method()/role() are: Walk has no
    // other way to inspect a type-erased routable value's own middleware.
    middlewareNames() []string
    // registerAny performs the SAME work as Register(b)/Handle(client) would
    // (b is the pattern's own concrete builder type — *Server here for
    // REST/reqreply's own routable; api/events' own routable interface uses
    // *Client instead, per this block's own NOTE above), discarding any
    // returned *RouteHandle/*ChannelHandle. A caller needing the typed
    // handle back uses WithHandleCallback (see Decision #1 below) instead —
    // Router itself never exposes one.
    registerAny(b *Server) error
}
```

Confirmed via code that discarding the handle is the OVERWHELMINGLY common
case already — `must(route.WithHandler(fn).Register(b), "...")`-style calls
dominate every example across all 3 patterns (the handler is already embedded
via `WithHandler`/an opt BEFORE `Register`/`Handle` is ever called) — but it is
NOT universal (a small number of call sites DO keep the returned handle for a
later, separate need). This asymmetry is RESOLVED, not left open — see
Decision #1 in "Design decisions — resolved for `api/rest`" below
(`WithHandleCallback`, a free-function `RouteOpt` attached directly to the
leaf, independent of Router entirely).

### Prefix + middleware composition semantics

- **Path/topic joining**: `prefix + leaf.path`, with exactly ONE `/` (REST) or
  topic-separator (events/reqreply) inserted between them regardless of
  whether either side already has/lacks a trailing/leading separator —
  mirrors `path.Join`-style normalization, not naive string concatenation.
  Nested `Mount` composes prefixes transitively: `rt.Mount(sub)` where
  `rt`'s own prefix is `"/api/v1"` and `sub`'s is `"/users"` produces an
  effective prefix of `"/api/v1/users"` for everything under `sub`.
- **Middleware composition**: `Router.Use(mw)`'s mws are PREPENDED to each
  grouped leaf's own `opts` slice (declaration order = dispatch order,
  the SAME established rule `d-0007-declarative-middleware-layering.md`'s "stacked"
  demonstration already relies on) — so a Router-level concern (e.g. a
  generic bearer-token presence check) runs BEFORE a route's own,
  route-specific `BoundMiddleware`-attached business logic. Nested `Mount`
  accumulates: a leaf under `rt.Mount(sub)` receives `rt`'s mws, then
  `sub`'s mws, then its own — outer-to-inner, outermost-declared-first.
- **Declaration-time vs. registration-time**: `Router.Route`/`Mount` are
  PURELY ACCUMULATING (mirrors every other `api/*` builder call — infallible,
  captures the spec only). ALL actual path/topic validation, middleware
  name-uniqueness checking (D6(b)), and codec/security coverage checking
  happen at `Router.Register(b)` time, by walking the tree and calling each
  leaf's own, UNCHANGED `Register`/`Handle` method on the prefix+mws-applied
  copy — **zero new validation logic duplicated from each leaf's own,
  already-correct `registerHandle`/`Register` internals.**
- **`Group` vs. `Mount` — SAME prefix, scoped middleware subset, no new
  segment**: mirrors chi's own `Group(fn func(r Router)) Router` in
  BEHAVIOR, not in return value or callback shape (see below). `rt.Group(fn)`
  passes `fn` a NEW `Router` value that inherits `rt`'s CURRENT prefix
  UNCHANGED (no path segment is appended) but starts with a COPY of `rt`'s
  accumulated mws that `fn` can append MORE to via ordinary chained calls
  — `fn` MUST explicitly `return` its built-up value (e.g. `return
  sub.Route(a).Route(b)`), since `Router` is immutable and there is no
  mutation for `Group` to observe otherwise (a small, deliberate ergonomic
  difference from chi's own mutation-style callback, accepted in exchange
  for `Router`'s overall immutability — see "Foundational reconsideration"
  below). Routes built up inside `fn` get the extra, scoped mws; routes
  declared on `rt` directly (outside `fn`) do not. This is the "auth only
  on POST/PUT/DELETE, GET stays open, same resource path" use case `Mount`
  cannot express (`Mount` always implies a NEW path segment). **`rt.Group(fn)`
  RETURNS `rt` ITSELF (the PARENT, updated with `fn`'s built-up child
  incorporated), NOT the child `fn` received** — confirmed via `ask_user`,
  a DELIBERATE divergence from chi's own `Group` (which returns the child
  `im`) for consistency with every other method in THIS doc's own sketch
  (`Use`/`Route`/`Mount`/`With` all chain off the receiver itself) —
  `rt.Group(fn1).Mount(other)` therefore chains onto `rt`, not onto `fn1`'s
  child. Internally, `Group` and `Mount` create the SAME child-node type
  (one unified tree representation, Design Decision #6 below) — `Group`'s
  child simply contributes an empty prefix segment.
- **`With` vs. `Use` — one-shot, not permanent, achieved via ORDINARY
  IMMUTABLE VALUE FLOW, no special type needed.** `rt.With(mw)` returns a
  NEW `Router` value (SAME type as everything else) with `pendingMws`
  set — `rt` ITSELF (the original value) is NEVER touched, so a
  SUBSEQUENT `rt.Route(otherLeaf)` (called on the ORIGINAL `rt`, not on
  `.With(mw)`'s returned value) never sees `mw` at all. `Route(leaf)`
  reads+incorporates `pendingMws` (if set) for THAT leaf specifically,
  then returns a NEW `Router` value with `pendingMws` RESET to nil — so
  `rt.With(mw).Route(leafA).Route(leafB)` applies `mw` to `leafA` ONLY
  (`leafB`'s call happens on the value `Route(leafA)` returned, which
  already had `pendingMws` cleared). **Repeated `.With()` calls
  ACCUMULATE, never silently overwrite**: `rt.With(mw1).With(mw2)`
  produces a value with BOTH `mw1` AND `mw2` pending — a clean,
  deliberate choice immutability makes safe to make either way (no race
  to consider, unlike a hypothetical shared-mutable-state design), chosen
  specifically to eliminate the "second call silently discards the first"
  footgun entirely.

### Foundational reconsideration: `Router` is a fully immutable VALUE type, not a mutable pointer

Found during a LATER review round (per explicit `ask_user` direction to
reconsider foundational/shipped design choices more broadly, not just the
`ClientHandle` finding) — re-examined Router's OWN most basic shape.
Confirmed via code: EVERY existing intermediate `api/*` declarative type
(`Route[Req,Resp]`, `SSERoute[Req,Resp]`, `Channel[T]`, `Subscriber[T]`,
`Publisher[T]`, `middleware.Middleware[In,Out]`, `BoundMiddleware`) is an
IMMUTABLE VALUE type — only `Server`/`Client` (the TERMINAL, final-stage
builders, used for the rest of the program's life) are mutable,
pointer-based, mutex-guarded accumulators. An EARLIER draft of this doc
made `Router` mutable/pointer-based — the FIRST intermediate type to break
this otherwise-universal 2-tier convention — which is DIRECTLY why that
earlier draft needed BOTH a mutex (to guard concurrent accumulation) AND a
separate `pendingRoute` type (to make `With`'s one-shot behavior race-free
against that same shared mutable state).

Making `Router` a plain, immutable VALUE type (matching `Route`/`Channel`
exactly) makes BOTH of those mechanisms UNNECESSARY, not just simpler:
an immutable value needs ZERO synchronization for concurrent reads/
derivations, by construction — the SAME property every other `api/*`
declarative type already has, with zero special-casing ever needed for
them either. This was a DELIBERATE choice (confirmed via `ask_user`,
weighed against a "hybrid" alternative that would have kept `Group`'s
chi-style mutation-only callback ergonomics at the cost of 2 distinct
types instead of 1) — full immutability was chosen for MAXIMUM
consistency with the rest of `api/*`, accepting `Group`'s small
explicit-return requirement as the one trade-off.

## `Router.Routes()`/`Router.Walk()` — the "final, holistic assembled view"

Directly addresses the user's explicit ask: "a point where the user, in their
workflow, assembles the final paths/topics for the API — having the final
overview of how the API assembles, BEFORE registering." Confirmed via chi's
own `Routes() []Route` + `chi.Walk(r Routes, walkFn WalkFunc) error` that the
right INTERNAL shape is a recursive VISITOR, with a flat, pre-materialized
slice as a convenience wrapper over it — NOT two independently-implemented
mechanisms (chi's own `Routes()` only returns one tree level, opaque
`SubRoutes` for nested `Mount`s; `Walk` is what actually recurses+composes).
This doc adopts the same split:

```go
// WalkFunc is called once per LEAF (never per intermediate Router/Group),
// after full prefix + middleware composition — mirrors chi.WalkFunc's own
// shape (method/path/middlewares), adapted to also report the DECLARING
// Router's prefix depth for callers that want to reconstruct the tree.
// SCOPED PER PACKAGE, same as routable/Router/RouterEntry themselves — each
// pattern declares its OWN WalkFunc taking its OWN RouterEntry shape.
type WalkFunc func(entry RouterEntry) error

// RouterEntry is shown here in its api/rest shape (the representative
// example) — SCOPED PER PACKAGE, same as routable/Router: api/events' own
// RouterEntry has a Role string field INSTEAD of Method (no Method field
// at all, since events has no HTTP-method axis); api/reqreply's own
// RouterEntry has NEITHER field — just Path and MiddlewareNames (confirmed
// via its own Design Decisions section: no second axis exists for
// reqreply at all). Each pattern's struct carries ONLY the field that
// actually applies to it — never a dead, always-empty stand-in for an
// axis that pattern doesn't have.
type RouterEntry struct {
    Method string // this leaf's HTTP method (api/rest's own field — see NOTE above
                   // for events'/reqreply's own, differently-shaped RouterEntry)
    Path   string // the FINAL, prefix-applied path/topic string
    MiddlewareNames []string // every reusable-class middleware name that will
                              // apply to this leaf, in dispatch order —
                              // Router-contributed names first, then the
                              // leaf's own
}

// Walk is the one true primitive — recurses through every Mount/Group,
// composing prefixes and middleware exactly as Register would, calling fn
// once per leaf, in declaration order. A non-nil error from fn stops the
// walk immediately and is returned as-is (no wrapping) — mirrors
// chi.Walk's own short-circuit behavior.
func (rt Router) Walk(fn WalkFunc) error

// Routes is a convenience wrapper over Walk — collects every leaf into a
// flat, declaration-ordered slice. Implemented (not hand-duplicated) as:
//
//	func (rt Router) Routes() []RouterEntry {
//	    var entries []RouterEntry
//	    _ = rt.Walk(func(e RouterEntry) error { entries = append(entries, e); return nil })
//	    return entries
//	}
//
// Walkable and printable WITHOUT calling Register (no builder/server/client
// needed), so a caller can inspect/log/assert on the assembled tree in a
// test or at startup, before committing to registration.
func (rt Router) Routes() []RouterEntry
```

## Structured errors (all implement `slog.LogValuer`)

```go
// RouterPrefixError is returned by Router.Register ONLY when a leaf's
// final, prefix-applied path/topic fails the SAME validation its own
// Register would apply standalone (e.g. REST's path-template syntax
// check, duplicate-path-param-name check).
//
// Carries ONLY Prefix+ComposedPath+Err — all of which Router already
// knows/computes WITHOUT any extra routable interface accessor. An
// earlier draft also carried a LeafPath field (the leaf's own,
// as-declared pre-prefix path), populated via a dedicated
// `declaredPath()` accessor every leaf type across all 3 patterns would
// have needed to implement — re-examined and DROPPED (confirmed via
// `ask_user`): LeafPath was single-purpose (no other mechanism in this
// design needed it) and its ONLY safe implementation (retaining the
// original value, NOT deriving it by trimming Prefix off ComposedPath —
// the join logic NORMALIZES, trimming an existing separator before
// inserting its own, so a naive index-based trim could be off by one
// character) required a whole extra interface method per pattern for a
// field that only made one error message marginally more convenient.
// Dropping it does not compromise declarativeness or any other feature —
// ComposedPath still fully identifies what actually failed.
//
// RouterPrefixError wraps ONLY this one failure mode. Every OTHER error a
// leaf's own Register can return (DuplicateMiddlewareNameError, security
// coverage failures, DuplicateRouteError, etc.) propagates COMPLETELY
// UNWRAPPED from Router.Register, exactly as it would from a direct,
// Router-less call — RouterPrefixError is not a catch-all wrapper for
// every possible Register failure. When 2 leaves' FINAL, composed paths
// collide (a Router-introduced duplicate neither leaf would have had
// standalone), the resulting DuplicateRouteError's ordering (which leaf's
// error surfaces first) follows Walk's own declaration-order traversal —
// deterministic, not implementation-accidental.
type RouterPrefixError struct {
    Prefix       string // the Router's own (or accumulated-nested) prefix
    ComposedPath string // the leaf's FINAL, prefix-applied path/topic that
                          // actually failed validation
    Err          error  // the underlying error from the leaf's own Register
}

func (e RouterPrefixError) Error() string { /* ... */ }
func (e RouterPrefixError) Unwrap() error { return e.Err }
func (e RouterPrefixError) LogValue() slog.Value { /* ... */ }
```

No NEW middleware-attachment error type is needed — a Router-contributed
`.Use(mw)` middleware composes onto each leaf's own, pre-existing `opts`
slice using the EXACT SAME mechanism a direct `.Use(mw)` call on the leaf
itself would use, so D6(b)'s existing `DuplicateMiddlewareNameError` already
fires correctly (and correctly) if a Router-level middleware's name collides
with one the leaf ALSO declared directly — confirmed this is DESIRABLE
behavior (a genuine naming collision), not something to suppress. This
error, like every other non-prefix-related `Register` failure, propagates
UNWRAPPED (see `RouterPrefixError`'s own doc comment above) — it is never
itself wrapped in `RouterPrefixError`.

## Observer integration

**No changes** — mirrors `d-0007-declarative-middleware-layering.md`'s own, already-confirmed
precedent exactly: `Router.Register`/`RouterPrefixError` are BUILDER-TIME
(spec-assembly) concerns, structurally identical in category to
`InvalidPathError`/`DuplicateMiddlewareNameError`/`BoundMiddlewareReqMismatchError`
— all of which are plain RETURNED errors, never routed through
`stats.Observer` (which only fires at REQUEST-DISPATCH time, confirmed via
grep of every `stats.ReportErrors`/`RecordSecurityRejection` call site —
all live in `adapters/*`, never in any `api/*` builder code). A Router simply
produces the SAME kind of `routeBuilder`/`channelBuilder`-populating opts a
direct `.Use()`/`HandleMW()` call already produces — once `Register()`
succeeds, the resulting handle is indistinguishable from one built without a
Router at all, so every existing Observer call site downstream (adapter
dispatch) is completely unaware a Router was ever involved.

## Unit test plan

| Test | Verifies |
|---|---|
| `TestRouter_Route_ComposesPrefixCorrectly` | `NewRouter("/api/v1").Route(route)`'s registered path is `"/api/v1" + route.path`, correctly `/`-joined regardless of either side's leading/trailing separator |
| `TestRouter_Mount_ComposesNestedPrefixesTransitively` | parent+child prefixes compose correctly across 2+ levels of nesting |
| `TestRouter_Use_AppliesToEveryGroupedLeaf` | a Router-level `.Use(mw)` middleware dispatches for EVERY route/channel registered under it, confirmed via an actual `Register`+dispatch, not just spec inspection |
| `TestRouter_Use_DeclarationOrderIsDispatchOrder` | Router-contributed middleware runs BEFORE a leaf's own route-specific middleware (stacked, mirrors `d-0007-declarative-middleware-layering.md`'s own stacked-demo precedent) |
| `TestRouter_Use_NestedMountAccumulatesOuterToInner` | parent Router's mws run before a nested sub-Router's own mws, which run before the leaf's own |
| `TestRouter_DuplicateMiddlewareName_ReturnsTypedError` | a Router-level mw name colliding with a leaf's own direct `.Use()` name correctly surfaces the EXISTING `DuplicateMiddlewareNameError`, unchanged |
| `TestRouter_InvalidComposedPath_ReturnsRouterPrefixError` | a leaf whose FINAL, prefix-composed path fails validation (not the leaf's own, pre-prefix path alone) surfaces `RouterPrefixError` wrapping the real cause, with both pre- and post-prefix paths populated |
| `TestRouter_Routes_ReflectsFinalAssembledTreeBeforeRegister` | `Routes()` returns the correct, fully-composed path/topic + middleware-name list WITHOUT ever calling `Register` |
| `TestRouter_Register_IndistinguishableFromDirectRegister` | a route registered via a Router produces a `*RouteHandle`/`*ChannelHandle` byte-for-byte equivalent (same dispatch behavior) to one registered directly, with no Router involved at all |
| `TestRouter_Group_SharesPrefixAddsScopedMiddlewareOnly` | `rt.Group(fn)`'s leaves get `rt`'s own prefix UNCHANGED (no new path segment) but an ADDITIONAL, scoped mw only `fn`'s own leaves receive — a sibling leaf declared on `rt` directly (outside `fn`) does NOT receive the Group's extra mw |
| `TestRouter_With_AppliesToNextRouteOnlyNotSiblings` | `rt.With(mw).Route(leafA)` applies `mw` to `leafA` only; a SUBSEQUENT `rt.Route(leafB)` (no `.With(...)` of its own) does NOT receive `mw` — confirms `With` never mutates `rt`'s own permanent mws |
| `TestRouter_Walk_VisitsEveryLeafExactlyOnceInDeclarationOrder` | `Walk` recurses through nested `Mount`/`Group` correctly, visiting every leaf exactly once, in declaration order, with fully-composed path+middleware-names per leaf |
| `TestRouter_Walk_StopsOnFirstError` | a non-nil error returned by the `WalkFunc` stops the walk immediately and is returned as-is (no wrapping), mirroring `chi.Walk`'s own short-circuit behavior |
| `TestRouter_Routes_EquivalentToWalkCollected` | `Routes()`'s output is byte-for-byte identical to manually collecting every `Walk` callback into a slice — confirms `Routes()` is a true wrapper, not a parallel implementation that could silently drift |
| `TestRouter_With_RepeatedCallsAccumulateNotOverwrite` | `rt.With(mw1).With(mw2)` applies BOTH `mw1` AND `mw2` to the next `.Route()` call — confirms repeated `.With()` calls accumulate, never silently discard a prior one |
| `TestRouter_Immutability_OriginalValueUnaffectedByChaining` | after `rt2 := rt.Use(mw)`, `rt` itself remains unchanged (no `mw`) — confirms `Router`'s chained methods never mutate the receiver, matching `Route[Req,Resp]`'s/`Channel[T]`'s own established immutable-value contract exactly (replaces an earlier, now-unnecessary mutex-specific concurrency test — immutable values need no dedicated concurrency test beyond Go's own memory-model guarantees for immutable data) |
| `TestClientHandle_WithRouter_MatchesRegisteredPath` (REST + reqreply, 2 tests) | `route.ClientHandle(WithRouter(rt))`'s composed path is byte-for-byte identical to what `rt.Route(route)` + `rt.Register(b)` actually registered — confirms the client/server topic-mismatch risk is genuinely closed, not just documented |

## Files to create

| File | Responsibility |
|---|---|
| `api/rest/router.go` | `Router` (immutable value type), `NewRouter`, `RouterOpt`, `Use`/`Route`/`Mount`/`Group`/`With`/`Register`/`Routes`/`Walk`, `routable` interface, `RouterPrefixError`, `ClientHandleOpt`/`WithRouter` |
| `api/events/router.go` | Same shape, topic-prefix composition, `Subscriber[T]`/`Publisher[T]` as leaves — NO `ClientHandleOpt`/`WithRouter` (confirmed N/A) |
| `api/reqreply/router.go` | Same shape, topic-prefix composition, `Route[Req,Resp]` as the leaf, includes its own `ClientHandleOpt`/`WithRouter` |
| `api/rest/builder.go`, `api/rest/middleware.go` | Add `withRouterPrefix`/`method`/`middlewareNames`/`registerAny` methods to `Route[Req,Resp]`/`SSERoute[Req,Resp]` (no new type params — Req/Resp already on the receiver; no `role` — REST only, per-pattern scoping) |
| `api/events/builder.go` | Same, for `Subscriber[T]`/`Publisher[T]` — `role` not `method` |
| `api/reqreply/route.go` | Same, for `Route[Req,Resp]` — NEITHER `method` nor `role` (no second axis) |
| `docs/features/router-groups.md` | User-facing feature doc, once shipped |
| `examples/{rest,events,reqreply}-api/demo_router_groups.go` | A worked example per pattern — nested Routers, group-wide Security, `Routes()` inspection printed at startup |

## Out of scope (Phase 2+)

- **Variable prefixes** (`"/tenants/{tenantID}"`) merging into every grouped
  leaf's own `Req`/`T` — this is a genuinely harder problem (every leaf's
  codec-declared merge-field vocabulary would need to ALSO accept
  Router-contributed vars, and `codex.ValidateParams`/`BuildFromParams`
  would need to compose params from 2+ sources) — deferred until a concrete
  need surfaces, confirmed via `ask_user` this round.
- **`api/mcp` Router** — tool names/resource URIs don't share REST/events/
  reqreply's hierarchical path shape; flagged as an open item in
  `mcp-ports-declarative-middleware.md` instead (this round's explicit
  direction), not designed here.
- **Bound-class middleware at the Router level** — structurally impossible
  (a `BoundMiddleware[Req,...]` is tied to ONE `Req`/`T`; a Router groups
  leaves of DIFFERENT types) — remains a route-specific-only concern,
  unchanged.
- **Spec-rendering auto-tagging** (OpenAPI/AsyncAPI `Tags`/channel-grouping
  auto-populated from a Router's prefix) — a plausible, low-risk follow-on
  once the core mechanism ships, not committed in this phase.
- **`ports.Pattern` integration** — whether `ports.RESTPattern`/`EventPattern`/
  `ReqReplyPattern` should gain a `Router`-aware variant is unexplored; today's
  `ports` machinery always calls `Register(builder)` directly on one handle at
  a time, with no grouping concept — flagged as a future question, not
  designed here.
- **Router-scoped fallback handler** (mirrors chi's own per-(sub)router
  `NotFound(h)`/`MethodNotAllowed(h)`) — "what happens when nothing under
  this Router's prefix matches" is a genuinely distinct idea from
  everything else in this doc (it's about UNMATCHED requests, not
  registered leaves) and REST already has its OWN, separate
  `ErrorPattern`/`ErrorChannel` convention for MATCHED-but-failed handler
  errors — whether/how a Router-scoped "nothing matched under this prefix"
  hook should exist, and how it would interact with (or replace) each
  adapter's own global not-found handling, is unexplored. Flagged here as
  a concrete idea worth a future dedicated look, not designed or committed
  in this phase.

## Design decisions — resolved for `api/rest` (closed this round, via `ask_user`)

Per the user's explicit direction, `api/rest` is designed and closed FIRST,
since it is closest to chi's own domain (HTTP routing) — `api/events`/
`api/reqreply` deliberately stay deferred (see "Deferred to events/reqreply"
below) until REST's design is validated (and ideally implemented), not
designed in lockstep. All 6 of the original "Open design decisions" are
resolved below, for `api/rest` specifically.

1. **Typed-handle recovery — resolved: `WithHandleCallback`, a general
   `Route[Req,Resp]` enhancement, NOT Router-specific surface at all.**
   Re-reading `routeBuilder`'s existing type-erasure pattern (its
   `requestFormats any`/`respFormats any` fields, resolved back to concrete
   types generically inside `registerHandle` where `Req`/`Resp` are known)
   revealed the clean resolution: add a FREE FUNCTION (Go allows free
   functions, never methods, to introduce new type parameters) —

   ```go
   // WithHandleCallback registers fn to run immediately after this route's
   // *RouteHandle is successfully constructed, inside Register/RegisterHandle
   // — regardless of whether Register was called directly OR via a Router's
   // Register (which just calls the SAME unchanged registerHandle through
   // the routable interface). Stored type-erased in routeBuilder exactly
   // like requestFormats/respFormats, resolved back to the concrete
   // *RouteHandle[Req,Resp] at the SAME point registerHandle already builds
   // it. A route declared with this opt needs NO special Router-aware
   // handling — composes for free with Router.Route(leaf) because
   // registerAny(b) always delegates to the leaf's own, unchanged Register.
   func WithHandleCallback[Req, Resp any](fn func(*RouteHandle[Req, Resp])) RouteOpt
   ```

   This means `Router` needs ZERO special "give me the handle back"
   mechanism — a caller who needs the handle back (SSE serving,
   `ports.Pattern` wiring) attaches `WithHandleCallback` directly to the
   `Route[Req,Resp]` value at declaration time, same whether it's later
   registered directly OR grouped into a Router. The overwhelmingly common
   case (discard the handle) needs zero opt at all, unchanged.

   **Found during a LATER review round (not this decision's original
   scope, but directly related): `Route.ClientHandle()` has its OWN,
   separate gap Router introduces — see "`ClientHandle`'s new `WithRouter` opt — resolving
   the Router/`ClientHandle` prefix-mismatch risk" below, added after
   initial closure.**
2. **Naming — confirmed**: `Router`/`Use`/`Route`/`Mount`/`Group`/`With`/
   `Routes`/`Walk`, no change — chi's own vocabulary, directly reused
   (not just "inspired by"), since `api/rest` is the pattern closest to
   chi's own domain. A REST user already familiar with `adapters/chi` (or
   chi itself, the most common Go HTTP router) recognizes every name
   immediately.
3. **Middleware ordering — confirmed: Router-first (outer-to-inner).**
   Router-contributed `.Use(mw)` middleware dispatches BEFORE a leaf's own
   `HandleMW`/`HandleBoundMW`-attached middleware — matches Traefik's own
   documented precedence and `d-0007-declarative-middleware-layering.md`'s
   existing "stacked" convention. Nested `Mount`/`Group` accumulate
   outer-to-inner, outermost-declared-first, all the way down to the
   leaf's own.
4. **Multi-Router attachment — resolved: no restriction needed, falls out
   of existing value semantics.** `Route[Req,Resp]` is ALREADY an
   immutable value type — every existing chained method (`.Use`,
   `.WithHandler`, `.HandleMW`, etc.) returns a NEW value, never mutates in
   place. `Router.Route(leaf)` composing a prefix+mws onto a COPY of
   `leaf` (via `withRouterPrefix`) means the SAME `leaf` value can already
   be passed to 2+ Routers with ZERO special-casing — this was never a
   genuine restriction to design, just an untested assumption in the first
   draft. No code or API surface needed to resolve this.
5. **`RouterOpt` — resolved: YES, keep the variadic parameter from day 1,
   empty-for-now, reserved for 3 already-identified future extensions.**
   `NewRouter(prefix string, opts ...RouterOpt) Router` ships with ZERO
   concrete `RouterOpt` implementations in Phase 1 (matching every other
   go-codex constructor's own established convention —
   `NewRoute`/`NewChannel`/`NewTopic`/`NewPath` all take variadic opts even
   when most callers pass none) — reserved for this doc's own "Out of
   scope (Phase 2+)" ideas, each of which would need exactly this
   extension point when/if picked up: `WithRouterNotFound(handler)` (the
   fallback-handler idea), `WithRouterTags(...)` (OpenAPI auto-tagging),
   `WithRouterPathParam(...)` (variable prefixes). Avoids a breaking
   constructor-signature change the day any of those 3 ideas ships.
6. **`Group` vs. `Mount("", sub)` — resolved: ONE unified internal tree
   representation (option b).** `Group` and `Mount` create the SAME
   internal child-node type; `Group`'s child simply contributes an empty
   prefix segment, so `Walk`/`Register` have exactly ONE recursion
   implementation, not two. The PUBLIC guarantee ("Group never adds a path
   segment") is enforced by `Group`'s own signature
   (`func(sub Router) Router`, no prefix parameter to pass one even
   accidentally), not by a second, parallel code path — this is the
   SIMPLER option specifically BECAUSE it minimizes the amount of new
   tree-walking logic `Walk`/`Register` need, at zero cost to the
   documented API contract.

### Design philosophy — why this stays "simple" for `api/rest` users

The explicit goal is: the codec-declaration step itself (`rest.NewRoute[Req,
Resp](method, path, reqCodec, respCodec, opts...)` + `.WithHandler(fn)`)
**does not change AT ALL**. A user who has never heard of `Router` writes
EXACTLY the same code as today. The ONLY new step is an OPTIONAL one:
instead of calling `.Register(b)` on each fully-declared `Route` value N
times, a user who wants shared prefixes/middleware wraps a BATCH of
already-fully-declared `Route` values in `rest.NewRouter(prefix).Use(mw).
Route(a).Route(b).Register(b)` once. This is the SAME grouping/prefix/
middleware-stack semantics chi itself offers — just elevated to operate on
go-codex's typed, codec-declared `Route[Req,Resp]` LEAF VALUES instead of
chi's own raw, untyped `http.Handler` leaves. Nothing about declaring a
route's path, codecs, or handler changes; Router is purely an additive,
optional grouping/assembly layer on top of an unchanged declaration step —
directly satisfying the request to keep the "simple codec declaration
workflow" for users while gaining chi's own routing/middleware-grouping
ergonomics.

### `ClientHandle`'s new `WithRouter` opt — resolving the Router/`ClientHandle` prefix-mismatch risk

Found during a LATER review round (not part of the original 6 decisions,
added here after REST's initial closure) — a MAJOR gap, confirmed via
code: TODAY, with no Router at all, a `Route[Req,Resp]` value's `path` is
the ONE single source of truth — both `.Register(b)` (server) and
`.ClientHandle()` (client) read from the SAME value, so they can never
disagree. `Route.ClientHandle()` takes NO builder/server argument at all
— confirmed via code, a pure accessor based purely on the leaf's own
fields. Router's `withRouterPrefix` is a ONE-WAY transformation producing
a NEW, prefixed COPY fed ONLY into `Router.Register`'s internal
`registerAny` call — the ORIGINAL, un-prefixed `Route` value remains
fully intact and independently usable. Since REST's whole model is "ONE
declared `var route = ...`, imported by BOTH a server package
(`.Register`) AND a client package (`.ClientHandle()`)," a realistic
pattern — server-side code groups `route` under a Router prefix;
client-side code (a different file/package, unaware of the Router) calls
`route.ClientHandle()` directly on the SAME var — produces **the client
calling the WRONG (pre-prefix) path while the server listens on the
prefixed one.** Silent mismatch: no compile error, no runtime error until
a request actually goes to the wrong place.

**Re-examined once more, considering whether to change ALREADY-SHIPPED
behavior instead of bolting on a new, separate mechanism** (per explicit
`ask_user` direction) — 4 alternatives considered and rejected before
confirming the final design:

1. *Bake the Router into `NewRoute(...)` itself* — rejected: directly
   contradicts this feature's founding premise (declare routes
   independently across many packages, assemble under a Router later).
2. *Mutate the original `Route` value when `.Route(leaf)` runs* —
   rejected: `Route[Req,Resp]` is a pervasive, immutable value type
   throughout the ENTIRE codebase; breaking this would be a massive,
   invasive change. Also structurally INCOMPATIBLE with Decision #4 (a
   leaf can belong to 2+ different Routers) — there is no single correct
   prefix to bake in if that's allowed.
3. *Explicit reassignment* (`route = route.Mount(rt)`) — rejected:
   reintroduces a Go package-init-ORDER correctness hazard (a different
   package's `init()` could read the stale, un-composed value before the
   reassignment runs) — a pure, stateless function has no such hazard.
4. *Narrowing Decision #4* (restrict a leaf to exactly one Router, to make
   alternative 2 viable) — rejected: a leaf mounted at 2+ prefixes (e.g. a
   stable path + a legacy-compat alias) is a legitimate, real pattern;
   restricting it ADDS validation/error-handling complexity, contradicting
   the simplicity goal.

**Final resolution**: fold the fix INTO `.ClientHandle()` itself via a
NEW, variadic, 100% NON-BREAKING parameter — not a separate top-level
free function living alongside the existing method. `ClientHandle`'s
EXISTING zero-argument call sites (confirmed throughout every existing
example/test) keep compiling UNCHANGED, since variadic parameters are
backward compatible:

```go
// ClientHandle, now variadic (was: zero parameters). Every existing
// call site (route.ClientHandle()) keeps compiling unchanged.
func (r Route[Req, Resp]) ClientHandle(opts ...ClientHandleOpt) *RouteHandle[Req, Resp]

// WithRouter tells ClientHandle to apply rt's CURRENT accumulated
// prefix+mws before building the handle — the EXACT SAME composition
// rt.Route(leaf) + rt.Register(b) would have produced for leaf's SERVER
// side — WITHOUT requiring the caller to separately track, reconstruct,
// or hand-type rt's own accumulated prefix anywhere. A client-side
// package calls `route.ClientHandle(rest.WithRouter(serverSideRouterVar))`
// (importing the SAME exported Router value the server-side package
// registers through) instead of a bare `route.ClientHandle()` — rt is the
// single, unambiguous source of truth for the prefix, eliminating the
// forgot-to-reapply-the-prefix-string risk entirely, chosen SPECIFICALLY
// over a raw-string-based alternative for this exact reason.
//
// Internally: ClientHandle checks for a WithRouter opt among its
// variadic args; if present, applies r.withRouterPrefix(rt's prefix,
// rt's mws) — the SAME transformation rt.Route(leaf) would apply —
// BEFORE proceeding with its existing (unchanged) construction logic.
// Does NOT require leaf to have actually been .Route()'d into rt — it is
// a pure composition convenience, not a validation that the pairing is
// registered (mirrors ClientHandle()'s own existing infallible,
// non-validating character). For a MULTI-LEVEL nested Mount/Group, pass
// the SPECIFIC (innermost) Router value leaf was (or will be) .Route()'d
// into — its own effective prefix already composes all ancestor Routers'
// contributions transitively, consistent with Register's own existing
// nested-composition behavior.
func WithRouter(rt Router) ClientHandleOpt
```

This qualifies as "changing already-shipped behavior" in the sense asked
— `ClientHandle`'s signature changes — but it is the SAFEST form of that
change: strictly additive, zero migration cost for existing callers, and
replaces what would otherwise have been TWO mechanisms (a method + a
separate free function) with ONE, consistent with go-codex's own
established "variadic opts" idiom used everywhere else (`NewRoute(...,
opts...)`, `NewChannel(..., opts...)`).

**Confirmed N/A for `api/events`**: events has no bare, zero-argument
accessor equivalent — `Subscriber.Handle(client)`/`Publisher.Handle(client)`
ALREADY take a builder/client argument, so this specific gap does not
apply there. **Applies to `api/reqreply` too** — see that pattern's own
section below for its own `WithRouter`.

## Design decisions — resolved for `api/events` (closed this round, via `ask_user`)

Continuing one-pattern-at-a-time (per the user's explicit direction):
`api/events` is next, since its `Channel[T]`/`Subscriber[T]`/`Publisher[T]`
builder internals were re-read directly (not assumed) to ground each
adaptation — `api/reqreply` remains deferred (see "Deferred to
`api/reqreply`" below).

1. **Typed-handle recovery — resolved: `WithHandleCallback`, adapted,
   fires once per role.** `channelBuilder.formats`/`subscribeFormats`/
   `publishFormats` is the SAME type-erasure precedent REST's resolution
   relies on, so the SAME free-function shape applies:

   ```go
   // WithHandleCallback registers fn to run immediately after a
   // *ChannelHandle[T] is successfully constructed, inside
   // Subscriber.Handle OR Publisher.Handle — regardless of whether Handle
   // was called directly or via a Router's registerAny. Stored
   // type-erased in channelBuilder exactly like
   // formats/subscribeFormats/publishFormats. Fires ONCE PER .Handle()
   // CALL — if the declaring Channel[T] forks into BOTH a Subscriber AND
   // a Publisher (two independent .Handle() calls), fn runs TWICE, once
   // per independently-constructed *ChannelHandle[T] — the SAME fn runs
   // for BOTH roles; use the role-targeted siblings below to run DIFFERENT
   // behavior per role instead.
   func WithHandleCallback[T any](fn func(*ChannelHandle[T])) ChannelOpt

   // WithSubscribeHandleCallback/WithPublishHandleCallback — resolved via
   // ask_user this round, addressing the "can a caller target ONLY one
   // role?" open question: fires ONLY in Subscriber.Handle /
   // ONLY in Publisher.Handle respectively, stored in 2 NEW
   // channelBuilder fields (subscribeHandleCallback any /
   // publishHandleCallback any), mirroring channelBuilder's existing
   // formats/subscribeFormats/publishFormats 3-way split precedent
   // exactly. Composable with WithHandleCallback (a caller can mix a
   // shared callback with a role-specific one if genuinely needed, though
   // the common case picks exactly one of the 3).
   func WithSubscribeHandleCallback[T any](fn func(*ChannelHandle[T])) ChannelOpt
   func WithPublishHandleCallback[T any](fn func(*ChannelHandle[T])) ChannelOpt
   ```
2. **Naming — confirmed, unchanged.** Same `Router`/`Use`/`Route`/`Mount`/
   `Group`/`With`/`Routes`/`Walk` vocabulary — these are generic grouping
   verbs, not HTTP-specific (chi itself uses "Route" for generic handler
   mounting, not just HTTP method dispatch), so no events-specific
   renaming is needed.
3. **Middleware ordering — confirmed Router-first, unchanged.** Pure
   declaration-order concept, protocol-agnostic — transfers directly from
   `api/rest`'s resolution with no new reasoning required.
4. **Multi-Router attachment — resolved identically, confirmed via code.**
   `Subscriber[T]`/`Publisher[T]` are ALREADY immutable value types (every
   chained method — `.Use`, `.WithHandler`, `.SubscribeMW`,
   `.SubscribeBoundMW`/`.PublishBoundMW` — returns a NEW value, never
   mutates in place) — the exact same property `api/rest`'s `Route[Req,
   Resp]` has. No restriction needed, same reasoning as REST's decision
   #4.
5. **`RouterOpt` — resolved: YES, reserved for 2 of REST's 3 ideas (NOT
   3).** `WithRouterTags`/`WithRouterTopicParam`-equivalents still apply
   (AsyncAPI channel-grouping auto-tagging; variable topic prefixes) —
   but `WithRouterNotFound` does NOT transfer: pub/sub has no "unmatched
   request" concept the way REST has 404s (a subscribe-side either
   receives messages matching its topic filter or it doesn't — there is
   no synchronous "nothing matched" response to customize a handler for).
   Confirmed via `ask_user` this round — dropped from events' reserved
   list entirely, not carried forward as an open question either.
6. **`Group` vs. `Mount("", sub)` — resolved identically, same unified
   internal representation.** Pure data-structure/implementation concern,
   protocol-agnostic — transfers directly, no events-specific
   reconsideration needed.
7. **NEW for events — `Group`'s MOTIVATING EXAMPLE is ROLE, not method
   (resolved via `ask_user`).** Events has no HTTP-method axis, but a
   `Channel[T]` forks into independent `Subscriber[T]`
   (`.WithSubscribe()`) and/or `Publisher[T]` (`.WithPublish()`) leaves
   from ONE shared topic — structurally analogous to REST's GET/POST
   forking from one shared path. `Group`'s events-equivalent motivating
   USE CASE is therefore ROLE-based: "apply extra, scoped middleware only
   to the SUBSCRIBE-role leaves under this topic-prefix; PUBLISH-role
   leaves stay unmodified" (or vice versa) — directly mirroring REST's
   "auth only on POST/PUT/DELETE, GET stays open" framing, with role
   substituting for method as the partition axis. **Important
   clarification**: `Group`'s own signature
   (`func (rt Router) Group(fn func(sub Router) Router) Router`) takes NO
   role/method parameter at all — `Group` has NO STRUCTURAL awareness of
   "role" (or "method," for REST) whatsoever; it is a completely generic,
   arbitrary-subset grouping primitive. "Role" (like REST's "method") is
   purely a NAMING/USAGE CONVENTION a caller chooses to follow, by only
   calling `.Route()` with `Subscriber[T]` values inside one particular
   `Group` block — the SAME generic mechanism works equally well for ANY
   OTHER arbitrary partition a caller invents (e.g., "only the
   PII-tagged subset," unrelated to role entirely). `api/events`' own
   `RouterEntry` has a `Role string` field (`"subscribe"`/`"publish"`) IN
   PLACE OF REST's `Method` field (per-pattern scoping — see the updated
   `routable`/`RouterEntry` sketches above; events' own `RouterEntry` has
   no `Method` field at all) so `Routes()`/`Walk()` can distinguish a
   topic's two role-forked leaves in the assembled tree view — populated
   via events' own `routable` interface's `role()` accessor, NOT via any
   Group-specific mechanism.

### Design philosophy — why this stays "simple" for `api/events` users

Identical promise to `api/rest`'s: the codec-declaration step itself
(`events.NewChannel[T](topic, codec, opts...)` + `.WithSubscribe(...)`/
`.WithPublish(...)` + `.WithHandler(fn)`) **does not change AT ALL**. The
ONLY new step is optional: instead of calling `.Handle(client)` on each
fully-declared `Subscriber[T]`/`Publisher[T]` value N times, a user who
wants shared topic-prefixes/middleware wraps a BATCH of already-declared
values in `events.NewRouter(prefix).Use(mw).Route(sub).Route(pub).
Register(client)` once. Nothing about declaring a channel's topic, codec,
or handler changes; Router is purely an additive, optional grouping/
assembly layer on top of an unchanged declaration step.

**No `ClientHandle`/`WithRouter` equivalent needed for `api/events`** (see
`api/rest`'s own section above for the full problem/resolution) —
confirmed N/A: events has no bare, zero-argument accessor the way REST's/
reqreply's `.ClientHandle()` is. `Subscriber.Handle(client)`/
`Publisher.Handle(client)` ALREADY take a builder/client argument, so
there is no "zero external input, can never reflect a Router's prefix"
accessor for this specific gap to apply to.

## Design decisions — resolved for `api/reqreply` (closed this round, via `ask_user`)

The LAST of the 3 patterns — `api/rest` and `api/events` closed in prior
rounds this session. `Route[Req,Resp]`/`routeBuilder` internals were
re-read directly to ground each adaptation, confirming a structural
difference from BOTH prior patterns.

1. **Typed-handle recovery — resolved: `WithHandleCallback`, adapted,
   SIMPLER than events' (fires once, no per-role duplication).**
   `routeBuilder.requestFormats`/`formats` is the SAME type-erasure
   precedent REST's/events' resolutions rely on:

   ```go
   // WithHandleCallback registers fn to run immediately after this
   // route's *RouteHandle is successfully constructed, inside
   // Register/RegisterHandle — regardless of whether Register was called
   // directly or via a Router's registerAny. Stored type-erased in
   // routeBuilder exactly like requestFormats/formats. Unlike events'
   // sibling (which can fire TWICE, once per role), this fires EXACTLY
   // ONCE — reqreply has no fork-into-multiple-leaves: a single
   // Route[Req,Resp] IS already the complete leaf, with exactly one
   // Register call.
   func WithHandleCallback[Req, Resp any](fn func(*RouteHandle[Req, Resp])) RouteOpt
   ```
2. **Naming — confirmed, unchanged.** Same generic vocabulary, no
   reqreply-specific renaming needed.
3. **Middleware ordering — confirmed Router-first, unchanged.** Pure
   declaration-order concept, protocol-agnostic.
4. **Multi-Router attachment — resolved identically, confirmed via code.**
   `Route[Req,Resp]` is confirmed an immutable value type (`.WithHandler`
   returns a new value) — same property REST's/events' leaf types have.
   No restriction needed.
5. **`RouterOpt` — resolved: YES, reserved for 2 ideas (NOT 3), same
   pattern as events.** `WithRouterTags`/`WithRouterTopicParam`-
   equivalents still apply — but `WithRouterNotFound` does NOT transfer,
   confirmed via `ask_user` this round, same reasoning as events: reqreply
   has no "unmatched topic" concept at the `api/reqreply` layer (that's an
   adapter/transport-level question — e.g. a ZeroMQ REP socket receiving a
   request for an unregistered topic — never modeled as an api-layer
   builder concern). Found and cross-referenced (NOT conflated with) an
   ADJACENT, already-adequate mechanism: `DeadLetter` — reqreply's
   existing fallback for a request that WAS matched to a registered route
   but failed for a reason not covered by `ErrorPattern` (same category as
   `ErrorPattern` itself, "matched but failed") — a structurally DIFFERENT
   concern from "nothing matched any topic at all," which remains
   unaddressed at this layer for all 3 patterns alike.
6. **`Group` vs. `Mount("", sub)` — resolved identically, same unified
   internal representation.** Pure data-structure/implementation concern,
   transfers unchanged.
7. **NEW for reqreply — `Group` has NO special structural axis (resolved
   via `ask_user`).** Confirmed via code: unlike REST (one path, many
   `Route` values differing by HTTP METHOD) or events (one `Channel[T]`
   forking into `Subscriber[T]`/`Publisher[T]` via ROLE), a single
   `reqreply.Route[Req,Resp]` IS ALREADY the complete, final leaf — topic
   + both codecs + handler, all in ONE declaration. `.Register(b)` wires
   the server side; `.ClientHandle()` gives the client side of the SAME
   declaration — but see "`ClientHandle`'s new `WithRouter` opt" below for a gap THIS
   parameterless accessor creates once a Router is involved; it is NOT as
   simply "Router-independent" as it may first appear. There is no second
   axis for `Group` to specifically target. `Group` therefore keeps chi's
   ORIGINAL, baseline semantics for reqreply: an arbitrary, user-chosen
   subset of routes sharing a topic-prefix needing extra scoped
   middleware — exactly as meaningful as chi's own `Group` is for plain
   HTTP routes that happen to share a path for reasons OTHER than method
   (chi's `Group` was never method-exclusive either; REST/events each
   found their OWN natural, domain-specific partitioning criterion ON TOP
   of this baseline, but the baseline itself never required one).

### `ClientHandle`'s new `WithRouter` opt — the SAME Router/`ClientHandle` prefix-mismatch risk as `api/rest`

Found during the SAME later review round that added this to `api/rest`'s
own section (see that section's full problem writeup, not repeated here
verbatim) — reqreply's `.ClientHandle()` has the IDENTICAL shape (bare,
zero-builder-argument accessor) and therefore the IDENTICAL risk:
server-side code groups a `Route[Req,Resp]` under a Router prefix;
client-side code (importing the SAME declared `var`, exactly reqreply's
own intended usage pattern) calls `.ClientHandle()` directly and gets the
WRONG, pre-prefix topic. Resolved identically:

```go
// WithRouter — reqreply's own instantiation of the SAME ClientHandleOpt
// api/rest's section defines; identical rationale, identical internal
// mechanism (ClientHandle applies rt's current prefix+mws via
// withRouterPrefix when a WithRouter opt is present).
func (r Route[Req, Resp]) ClientHandle(opts ...ClientHandleOpt) *RouteHandle[Req, Resp]
func WithRouter(rt Router) ClientHandleOpt
```

### Design philosophy — why this stays "simple" for `api/reqreply` users

Identical promise to `api/rest`'s and `api/events`'s: the codec-declaration
step itself (`reqreply.NewRoute[Req,Resp](topic, reqCodec, respCodec,
opts...)` + `.WithHandler(fn)`) **does not change AT ALL**. The ONLY new
step is optional: instead of calling `.Register(b)` on each fully-declared
`Route` value N times, a user who wants shared topic-prefixes/middleware
wraps a BATCH of already-declared values in `reqreply.NewRouter(prefix).
Use(mw).Route(a).Route(b).Register(b)` once — exactly mirroring `api/rest`'s
and `api/events`' own additive-only promise. `Route.ClientHandle()` itself
is never MUTATED by Router (the original, un-prefixed value is always
still there, untouched) — but a caller who NEEDS a prefix-aware client
handle uses the new `ClientHandle(WithRouter(rt))` above, not a bare `.ClientHandle()`
accessor directly, once a Router is involved. Router only wires the
REGISTRATION side (`.Register()`/`.Handle()` — covering BOTH REST's
server-only and events' dual subscribe+publish shape uniformly); it never
replaces or silently redirects any leaf's own separate, pre-existing
accessor used OUTSIDE the registration step.

## All 3 patterns now CLOSED — design phase complete

`api/rest`, `api/events`, and `api/reqreply` all have every decision
resolved as of this round. No pattern remains deferred. The next step for
this roadmap is implementation (Mode 3 of the `plan-a-new-codex-feature`
skill), triggered by a future explicit "implement it" request — not
assumed or started in this round.

## Phase C, sub-step 2 — deferred-item review (post-implementation)

Per the user's explicit direction, this review happens AFTER all 3
patterns' implementations shipped (Phases A/B/C sub-step 1), as a clean,
separate sub-step — not interleaved with reqreply's own implementation.
Each of the 6 "Out of scope (Phase 2+)" bullets above gets an explicit,
reasoned ship-now-vs-stay-deferred decision. A FIRST pass re-examined
these against what Phases A/B/C actually shipped/learned; a SECOND,
item-by-item discussion round (explicitly requested before closing to
Phase D) went deeper still — considering concrete alternative designs,
not just re-stating conclusions — and corrected one overstatement. Final
decisions below:

1. **Variable prefixes (`{tenantID}`-style Router prefixes) — STAYS
   DEFERRED, as a DELIBERATE simplicity-over-capability tradeoff, not
   just "no driver yet."** Would require `NewRouter` to accept a prefix
   TEMPLATE (not a static string) + param declarations (mirrors
   `PathParam`/`TopicParam`). The hard part: a Router-level `{tenantID}`
   var must ALSO exist as a field on EVERY grouped leaf's `Req`/`T` (via
   a merge field) for `BuildPath`/`BuildTopic` to populate it — but
   Router-level param registration and each leaf's own
   `codex.ValidateParams`/merge-field registration are currently two
   fully separate, non-communicating construction paths. Making them
   compose would mean threading a NEW param source into
   `codex.ValidateDeclaredParams`/`BuildFromParams`, which don't know
   about "Router" at all today — a change reaching into `codex/` itself,
   not just `api/*`. Explicitly decided: this would add real complexity
   to the CORE codec-declaration layer to support a feature with no
   concrete driver — directly against the "simple, declarative,
   consistent workflow" goal this whole effort exists for.
2. **`api/mcp` Router — STAYS OUT OF SCOPE**, but with a more precise
   follow-on: confirmed `mcp.Tool[In,Out]` has a flat `Name string` — no
   hierarchical path, so a prefix-COMPOSING "Router" genuinely doesn't
   fit. The adjacent, smaller idea — a prefix-LESS middleware-grouping
   convenience (attach one middleware set to N tools in one declaration,
   no path composition at all) — is a structurally DIFFERENT, simpler
   mechanism than this doc's "Router," and now has a concrete design
   sketch recorded in `mcp-ports-declarative-middleware.md` instead of
   this doc (keeps this doc's scope to the 3 patterns sharing the
   hierarchical-path/topic shape).
3. **Bound-class middleware at the Router level — PERMANENTLY OUT OF
   SCOPE for today's Router, confirmed via actually considering (and
   rejecting) alternatives, not just restating "structurally
   impossible."** The core conflict: `Router` is deliberately TYPE-
   ERASED/heterogeneous (one Router groups leaves of different
   `Req`/`Resp`/`T` pairs), but `BoundMiddleware[Req,Resp,...]` is tied
   to ONE concrete type pair by construction. Two alternatives were
   considered and rejected: (a) a reflection-based runtime type-check
   that applies a bound middleware only to matching leaves, silently
   skipping others — rejected because it would be the ONE place in this
   entire mechanism where a type mismatch becomes a silent skip instead
   of a loud, typed error, contradicting the `HandleCallbackTypeError`/
   `BoundMiddlewareReqMismatchError` precedent everywhere else in this
   doc; (b) a strongly-typed `Router[Req,Resp]` that only groups same-
   typed leaves — this is a fundamentally different, PARALLEL construct,
   not an incremental addition to today's Router, so it does NOT belong
   as a bullet in this doc. Flagged instead as its own, separate idea-
   stage roadmap doc: [`typed-router-groups.md`](../roadmap/typed-router-groups.md).
4. **Spec-rendering auto-tagging (`WithRouterTags`) — STAYS DEFERRED,
   now with a concrete design sketch AND a found gotcha that raises the
   effort estimate from LOW to MEDIUM.** All 3 shipped `RouterOpt`
   interfaces are confirmed real, reserved extension points sitting
   unused, and `RouteMeta.Tags`/`ChannelMeta.Tags`/`Subscribe.Tags`/
   `Publish.Tags`/reqreply's `RouteMeta.Tags` already exist and already
   render into each pattern's spec — so the RENDERING target is ready.
   But: `RouteMeta.applyRoute` is confirmed a WHOLE-STRUCT OVERWRITE
   (`rb.meta = m`, `api/rest/builder.go` line ~270), NOT an incremental
   merge — meaning the existing middleware-style trick (Router PREPENDS
   its own opt into the leaf's `opts`, so it runs BEFORE the leaf's own)
   does NOT generalize to Tags: if the leaf's OWN `RouteMeta{Tags: [...]}`
   opt runs afterward in the same opts loop, it would OVERWRITE
   `rb.meta` entirely, silently discarding the Router's contribution.
   Correct design direction (for a future implementer): Router-
   contributed tags must be merged AFTER the leaf's own opts loop fully
   resolves `rb.meta` — either a NEW `routable` interface method (e.g.
   `withRouterTags(tags []string) routable`) invoked separately from
   `withRouterPrefix` and consulted by each leaf's own `Register`/
   `Handle` body, or an extension to `withRouterPrefix`'s existing
   signature with the leaf appending tags post-opts-loop. Either way,
   this touches `Route.Register`/`Subscriber.Handle`/`Publisher.Handle`
   in all 3 packages, not just `router.go`. Accumulate semantics (Router
   tags + leaf's own, never replace) — consistent with `Use`/`With`.
   Still no concrete driving use case to build against, so NOT
   implemented this round — but the next attempt starts from this sketch,
   not from zero.
5. **`ports.Pattern` integration — STAYS a future question, with a
   CORRECTED (less optimistic) framing and a found blocker.** The prior
   pass's wording overstated this: "`ports.RESTPattern`/`EventPattern`/
   `ReqReplyPattern`'s existing `PluginXxxPattern` methods already work
   UNCHANGED for any Router-grouped leaf" is NOT accurate — verified via
   `ports/handle.go`'s pattern-building functions
   (`buildEventPatternHandles` and siblings): `Pattern` ALWAYS builds a
   brand-new leaf internally from `Method`/`Path`/`Opts` and registers
   THAT; there is no field or mechanism to pass in an EXISTING, already-
   Router-composed leaf, or a `Router` itself, at all. The accurate
   statement: `ports.Pattern` and `Router` are two fully INDEPENDENT,
   non-composable mechanisms today — using one doesn't break the other,
   but a caller cannot get BOTH Router-grouping AND `ports.Pattern`'s
   one-call convenience for the SAME route. A real blocker found this
   round: `Router.withRouterPrefix` (the mechanism that would need to
   compose a Pattern-built leaf with a Router's prefix+mws) is
   UNEXPORTED/package-private — `ports` is a different package and
   cannot call it directly. Implementing this would require Router to
   expose a NEW, EXPORTED composition hook (e.g. `Router.Prefix()
   string` + `Router.Middlewares() []middleware.RouteMiddleware`
   accessors, or a dedicated `Router.Apply(leaf) (composed, path)`
   method) in ALL 3 packages, BEFORE `ports` could consume it at all —
   genuine new public API surface, not internal wiring. Design
   direction: add a `Router *rest.Router`/`*events.Router`/
   `*reqreply.Router` field to each `Pattern` struct; when set, the
   pattern-building function composes the freshly-built leaf through the
   Router's (newly-exported) apply hook before registering. No concrete
   driver surfaced during implementation to justify building this now —
   stays deferred, with this corrected, more precise reasoning replacing
   the prior overstatement.
6. **Router-scoped fallback handler (`WithRouterNotFound`) — STAYS
   DEFERRED, with a materially STRONGER finding than "no driver yet."**
   Investigated each pattern's REAL dispatch mechanism rather than
   restating the prior "adjacent DeadLetter/ErrorPattern" answer:
   **REST** — confirmed via `adapters/nethttp`/`adapters/chi`: go-codex
   NEVER owns the mux; the caller always constructs their own
   `*http.ServeMux`/`chi.Router` externally and go-codex's adapters just
   BIND registered routes onto it. The underlying mux's OWN
   NotFound/MethodNotAllowed mechanism (`chi.Router.NotFound(h)`, or a
   catch-all `"/"` pattern on `http.ServeMux`) is ALREADY fully available
   to every caller TODAY, with ZERO new go-codex code — wrapping it in
   `api/rest.Router` would be pure, redundant duplication of a mechanism
   the caller already owns directly. **Events** — confirmed via
   `examples/events-api/demo_wildcard_subscription.go`: a wildcard-topic
   `Subscriber` (MQTT `#`/`+`) is ALREADY just an ordinary,
   Router-groupable leaf — "catch everything unmatched" already has a
   direct, existing answer, no gap. **Reqreply** — confirmed via
   `api/reqreply/builder.go`'s `Server.Serve`: dispatch is NOT a single,
   central mux lookup; `Serve` spins up ONE GOROUTINE PER REGISTERED
   TOPIC, each independently bound to the transport — there is
   structurally NOWHERE for an "unmatched topic" concept to even reach
   in the `api/reqreply` layer; it's answered entirely below this layer,
   per transport. **Net finding:** this isn't "nobody asked for it yet"
   — it's "the 3 patterns' OWN existing architectures already make a
   Router-level version either redundant (REST/events) or structurally
   impossible (reqreply)," a materially different and more conclusive
   finding than the original review's wording.

**Net outcome: all 6 items confirmed correctly deferred — zero new work
promoted into Phase C.** Item 3 spun off a separate idea-stage roadmap
doc (`typed-router-groups.md`); item 2's adjacent idea is now sketched in
`mcp-ports-declarative-middleware.md`; items 4 and 5 now carry concrete
design sketches (including found gotchas/blockers) for a future
implementer to start from, instead of a bare "deferred." The
deferred-item review itself is now closed; Phase D (documentation
graduation) is the only remaining step.

## See also

- [`d-0007-declarative-middleware-layering.md`](d-0007-declarative-middleware-layering.md) — the declarative
  middleware mechanism this Router composes with (unchanged); its
  `BoundMiddleware`/`BoundSubscribeMiddleware`/`BoundPublishMiddleware`
  attachment resolution (unexported interface + receiver-scoped type
  parameters, since Go forbids new type parameters on a method) is the
  direct precedent this doc's `routable` interface reuses.
- [`mcp-ports-declarative-middleware.md`](../roadmap/mcp-ports-declarative-middleware.md) —
  now carries a concrete, prefix-less `ToolGroup` design sketch (spun out
  of this doc's Phase C deferred-item review, item 2) for whether
  `api/mcp` ever wants an equivalent grouping mechanism.
- [`typed-router-groups.md`](../roadmap/typed-router-groups.md) — the
  strongly-typed `Router[Req,Resp]` idea spun out of this doc's Phase C
  deferred-item review (item 3), as a PARALLEL construct to this doc's
  heterogeneous `Router`, not a replacement for it.
