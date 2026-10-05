# Declarative Router Groups — `api/rest`, `api/events`, `api/reqreply`

> **Status:** Design draft — not yet implemented.
> [← Back to Roadmap](index.md)

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

## Scope decisions (confirmed via `ask_user`)

| In scope | Out of scope (this doc) |
|---|---|
| `rest.Router` (path prefixes), `events.Router` (topic prefixes), `reqreply.Router` (topic prefixes) — all 3 patterns designed now, mirroring `d-0007-declarative-middleware-layering.md`'s own all-3-patterns precedent | `api/mcp` — tool names/resource URI templates don't have the same hierarchical-path shape; flagged as an **open item** in [`mcp-ports-declarative-middleware.md`](mcp-ports-declarative-middleware.md)'s "Open design decisions" instead (see that doc's update, not designed here) |
| Nesting — a Router may `Mount` sub-Routers (prefixes compose: parent + child) | Dynamic/runtime router mutation after `Register()` — mirrors the existing "no hot-adding routes to an already-`Serve`'d mux" limitation (`docs/roadmap/dynamic-port-rebinding.md`'s own scope) |
| STATIC prefixes only (e.g. `"/api/v1"`, `"tenants/eu"`) — no `{var}` placeholders in a Router's own prefix for v1 | Variable prefixes (e.g. `"/tenants/{tenantID}"`) merging into every grouped route's own `Req`/`T` — deferred; see "Out of scope (Phase 2+)" |
| Reusable-class `Middleware[In,Out]`/`Declaration[In,Out]` attachment at the Router level (`.Use(mw)`-equivalent, group-wide) | Bound-class (`BoundMiddleware[Req,...]`) attachment at the Router level — structurally impossible to express group-wide (a `BoundMiddleware` is tied to ONE concrete `Req`/`T` type; a Router groups routes of DIFFERENT `Req`/`T` types) — bound middlewares stay route-specific, unchanged |
| A `Router.Routes()`-style inspection accessor — the "final, holistic view of the assembled path/topic tree" the user explicitly asked for | Spec-rendering changes (OpenAPI/AsyncAPI tag auto-assignment from Router prefix) — noted as a nice-to-have, not committed |

## Reference pattern being designed (one shape, 3 packages)

```go
// api/rest
type Router struct { /* ... */ }
func NewRouter(prefix string, opts ...RouterOpt) *Router
func (rt *Router) Use(mws ...middleware.RouteMiddleware) *Router
func (rt *Router) Route(r routable) *Router   // attach a leaf Route[Req,Resp]/SSERoute[Req,Resp]
func (rt *Router) Mount(sub *Router) *Router  // attach a nested sub-Router
func (rt *Router) Register(b *Server) error   // walk the tree, compose prefixes+mws, Register every leaf
func (rt *Router) Routes() []RouterEntry      // inspect the fully-assembled path tree BEFORE Register

// api/events (topic prefixes instead of path prefixes — same shape)
type Router struct{ /* ... */ }
func NewRouter(prefix string, opts ...RouterOpt) *Router
func (rt *Router) Use(mws ...middleware.RouteMiddleware) *Router
func (rt *Router) Route(r routable) *Router   // attach a Subscriber[T]/Publisher[T]
func (rt *Router) Mount(sub *Router) *Router
func (rt *Router) Register(client *Client) error
func (rt *Router) Routes() []RouterEntry

// api/reqreply (topic prefixes, mirrors events' Router exactly, Server/Builder-scoped)
type Router struct{ /* ... */ }
func NewRouter(prefix string, opts ...RouterOpt) *Router
func (rt *Router) Use(mws ...middleware.RouteMiddleware) *Router
func (rt *Router) Route(r routable) *Router   // attach a Route[Req,Resp]
func (rt *Router) Mount(sub *Router) *Router
func (rt *Router) Register(b *Builder) error
func (rt *Router) Routes() []RouterEntry
```

### How a leaf `Route[Req,Resp]`/`Channel`/etc. attaches without a new generic method type-parameter

Go forbids a method from introducing new type parameters beyond its
receiver's own — `Router.Route` cannot be `func (rt *Router) Route[Req, Resp
any](r Route[Req, Resp]) *Router`. The resolution mirrors
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
type routable interface {
    // withRouterPrefix returns a NEW leaf value (same concrete type) with
    // prefix prepended to its own path/topic string and mws PREPENDED to its
    // own accumulated opts (router-level middleware runs BEFORE the route's
    // own — see "Open design decisions" for the alternative ordering).
    withRouterPrefix(prefix string, mws []middleware.RouteMiddleware) routable
    // registerAny performs the SAME work as Register(b)/Handle(client) would,
    // discarding any returned *RouteHandle/*ChannelHandle — see "Open design
    // decisions" for whether/how a caller recovers the typed handle anyway.
    registerAny(b any) error
}
```

Confirmed via code that discarding the handle is the OVERWHELMINGLY common
case already — `must(route.WithHandler(fn).Register(b), "...")`-style calls
dominate every example across all 3 patterns (the handler is already embedded
via `WithHandler`/an opt BEFORE `Register`/`Handle` is ever called) — but it is
NOT universal (a small number of call sites DO keep the returned handle for a
later, separate need). This asymmetry is captured as an explicit "Open design
decision" below, not silently resolved.

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

## `Router.Routes()` — the "final, holistic assembled view"

Directly addresses the user's explicit ask: "a point where the user, in their
workflow, assembles the final paths/topics for the API — having the final
overview of how the API assembles, BEFORE registering." Returns a flat,
declaration-ordered list of every leaf's FINAL (prefix-applied) path/topic +
method (REST) + the list of middleware names that will apply to it — walkable
and printable WITHOUT calling `Register` (no builder/server/client needed),
so a caller can inspect/log/assert on the assembled tree in a test or at
startup, before committing to registration:

```go
type RouterEntry struct {
    Method string // REST only; empty for events/reqreply
    Path   string // the FINAL, prefix-applied path/topic string
    MiddlewareNames []string // every reusable-class middleware name that will
                              // apply to this leaf, in dispatch order —
                              // Router-contributed names first, then the
                              // leaf's own
}

func (rt *Router) Routes() []RouterEntry
```

## Structured errors (all implement `slog.LogValuer`)

```go
// RouterPrefixError is returned by Router.Register when a leaf's final,
// prefix-applied path/topic fails the SAME validation its own Register
// would apply standalone (e.g. REST's path-template syntax check,
// duplicate-path-param-name check) — surfaced with BOTH the leaf's own
// declared (pre-prefix) path AND the final, composed one, so the error
// message doesn't force the reader to mentally re-derive the join.
type RouterPrefixError struct {
    Prefix       string // the Router's own (or accumulated-nested) prefix
    LeafPath     string // the leaf's own, as-declared (pre-prefix) path/topic
    ComposedPath string // Prefix + LeafPath, after join-normalization
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
behavior (a genuine naming collision), not something to suppress.

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

## Files to create

| File | Responsibility |
|---|---|
| `api/rest/router.go` | `Router`, `NewRouter`, `RouterOpt`, `Use`/`Route`/`Mount`/`Register`/`Routes`, `routable` interface, `RouterPrefixError` |
| `api/events/router.go` | Same shape, topic-prefix composition, `Subscriber[T]`/`Publisher[T]` as leaves |
| `api/reqreply/router.go` | Same shape, topic-prefix composition, `Route[Req,Resp]` as the leaf |
| `api/rest/builder.go`, `api/rest/middleware.go` | Add `withRouterPrefix`/`registerAny` methods to `Route[Req,Resp]`/`SSERoute[Req,Resp]` (no new type params — Req/Resp already on the receiver) |
| `api/events/builder.go` | Same, for `Subscriber[T]`/`Publisher[T]` |
| `api/reqreply/route.go` | Same, for `Route[Req,Resp]` |
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

## Open design decisions (to resolve before/during implementation)

1. **Does `Router.Register` need to give the caller back typed handles
   afterward?** Confirmed via code that MOST call sites discard the handle
   (handler already embedded via `WithHandler` before `Register`), but a
   SMALL number of existing call sites DO keep it (e.g. for a later,
   separate SSE-serving or client-Call need). Options: (a) accept the
   information loss — a Router-grouped leaf that needs its handle back
   registers OUTSIDE the Router instead, by design; (b) add an optional
   per-leaf callback (`rt.Route(route, rest.WithHandleCallback(func(h
   *RouteHandle[Req,Resp]) { ... }))`) — but this re-introduces a generic
   type parameter problem at the call site (needs a free function, not a
   method, breaking fluent chaining) — unresolved, needs a decision before
   implementation starts.
2. **Naming**: `Router`/`Use`/`Route`/`Mount`/`Routes` vs. alternatives
   (`Group`/`Attach`/`Nest`) — chosen names mirror Traefik's own vocabulary
   (`Router`) blended with chi's (`Mount`/`Route`/`Use`), open to
   refinement.
3. **Middleware ordering default** (Router-first vs. leaf-first) — this doc
   recommends Router-first (outer-to-inner), matching Traefik's own
   documented precedence and `d-0007-declarative-middleware-layering.md`'s "stacked"
   convention, but this is a genuine design choice, not a forced one.
4. **Should a leaf be attachable to MORE THAN ONE Router** (shared across two
   different prefix groups)? Today's sketch assumes each leaf belongs to
   exactly one Router (or none); allowing a leaf to be `.Route()`'d into 2+
   Routers would need either deep-copy-per-attach (already implied by
   `withRouterPrefix` returning a NEW value) or an explicit "no, pick one"
   restriction enforced at `Register` time — needs a decision.
5. **Should `RouterOpt` exist at all for v1**, or is `NewRouter(prefix)` +
   `.Use()`/`.Route()`/`.Mount()` sufficient enough without a variadic-opts
   constructor? Sketched for symmetry with `NewRoute(..., opts...)`'s own
   convention, but may be unnecessary surface for a first cut.

## See also

- [`d-0007-declarative-middleware-layering.md`](../design/d-0007-declarative-middleware-layering.md) — the declarative
  middleware mechanism this Router composes with (unchanged); its
  `BoundMiddleware`/`BoundSubscribeMiddleware`/`BoundPublishMiddleware`
  attachment resolution (unexported interface + receiver-scoped type
  parameters, since Go forbids new type parameters on a method) is the
  direct precedent this doc's `routable` interface reuses.
- [`mcp-ports-declarative-middleware.md`](mcp-ports-declarative-middleware.md) —
  flagged with a forward-looking open item for whether `api/mcp`/`ports`
  ever wants an equivalent grouping mechanism.
