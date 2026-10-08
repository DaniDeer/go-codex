# Shared Router Module — `router`

> **Status:** Design draft — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

`api/rest`, `api/events`, and `api/reqreply` each ship their OWN, independently-maintained
`Router` implementation (`api/rest/router.go` 663 lines, `api/events/router.go` 603 lines,
`api/reqreply/router.go` 547 lines — 1813 lines total). These are not merely similar — they are
near-line-for-line duplicates: identical type names (`Router`, `routerChild`, `RouterEntry`,
`RouterPrefixError`), identical method names and logic (`NewRouter`, `Use`, `Tags`, `With`,
`Route`, `Mount`, `Group`, `Walk`, `Routes`, `Register`/`register`, `cloneMws`/`cloneChildren`/
`cloneTags`). `api/events/router.go` and `api/reqreply/router.go`'s own doc comments admit this
outright — events' `Router` is documented as "events' own copy of `api/rest.Router`, adapted for
the subscribe/publish role axis"; reqreply's as "reqreply's own copy of `api/rest.Router`,
adapted for request/reply's single-leaf shape." This is confirmed, intentional, documented
copy-paste, not an oversight.

This mirrors exactly the situation `middleware.RouteMiddleware` (the exported marker interface)
+ `middleware.Declaration[In,Out]` already solved for cross-API middleware mechanics — raising
the natural question (asked directly by the user): should Router's shared, pattern-agnostic
MECHANICS (prefix-joining, pendingMws/tags accumulation, Mount/Group merge semantics, Walk
traversal, RouterEntry construction) be factored into a shared `router` module the same way,
with each api package's `Route`/`SSERoute`/`Subscriber`/`Publisher` types implementing a shared,
exported interface instead of each owning a private, duplicated copy of the whole mechanism?

## Why this is a DIFFERENT, more tractable problem than the just-rejected security-scheme-sharing case

The companion effort (`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s Addendum 8, originally tracked as a roadmap doc, now fully implemented and folded into that design doc) concluded that sharing ONE
security-declaration VALUE across 3 different api packages' `.Use()` calls has no real use case
and (more importantly) is barely achievable at all without a lowest-common-denominator type.
Router's duplication is a different shape of problem: it is not about sharing one VALUE across
packages, it is about sharing one IMPLEMENTATION (behavior/logic) that is currently hand-copied
3 times. Go generics can parameterize over an exported interface + a register-target type the
same way `middleware.RouteMiddleware`'s marker-interface trick already works for
`middleware.Middleware`/`rest.Middleware[In,Out]` — this is a solvable, moderate-sized
consolidation, not a fight against Go's type system.

## Scope decisions (what's in Phase 1, what's deferred)

| In scope | Out of scope |
|---|---|
| Consolidate the shared Router state machine (`routerChild`, prefix/topic-joining, pendingMws/tags accumulation, `Mount`/`Group` merge semantics, `Walk`/`Routes` traversal, `RouterPrefixError`) into a new `router` package, generic over an exported `Routable`-shaped leaf interface and a register-target type | Changing ANY of `Router`'s observable behavior/semantics in any of the 3 api packages — this is a pure internal-implementation consolidation, zero behavior change intended |
| Keep `rest.Router`, `events.Router`, `reqreply.Router` as the PUBLIC, source-compatible type names every existing caller already uses (likely as thin type aliases or embedding wrappers around the shared generic core) | Breaking ANY existing example/test's `rest.Router{...}`/`events.Router{...}`/`reqreply.Router{...}` usage — full backward compatibility is mandatory, not best-effort |
| Add a new exported `router.Routable[Target]`-shaped interface (or equivalent) that each api's own leaf type (`rest.Route[Req,Resp]`/`rest.SSERoute[Req,Event]`/`events.Subscriber[T]`/`events.Publisher[T]`/`reqreply.Route[Req,Resp]`) implements via newly-EXPORTED methods (promoting today's unexported `withRouterPrefix`/`routeMethod`/`middlewareNames`/`tags`/`registerAny`) | Changing `RouterOpt`/`ClientHandleOpt`/`WithRouter` call sites' public signatures beyond what's structurally required for the generic parameterization |
| Preserve the REST-specific (leading-slash) vs events/reqreply-specific (no leading slash) path/topic-joining difference — likely via a small per-instantiation join-strategy hook, not a behavior change | Unifying REST's path-joining and events/reqreply's topic-joining into ONE algorithm — these are genuinely different conventions, not accidental divergence |

## Toolchain / dependency decisions

None — pure internal Go generics refactor, no new external dependency.

## API surface (sketch — NOT final, see open design decisions)

```go
package router

// Routable is satisfied by every leaf type a [Router] can hold — the
// EXPORTED, cross-package equivalent of each api package's current
// unexported `routable` interface. Target is the register-time
// destination type (e.g. *rest.Server, *events.Client, *reqreply.Server).
type Routable[Target any] interface {
    WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (Routable[Target], string)
    RouteMethod() string
    MiddlewareNames() []string
    RouteTags() []string // named RouteTags, not Tags, to avoid any future collision with a leaf's own Tags-shaped builder option
    RegisterAny(target Target) error
}

// JoinFunc lets each api package supply its own prefix/topic-joining
// convention (REST: leading-slash path.Join-style; events/reqreply: no
// leading separator, MQTT/ZeroMQ topic style) without forking the
// shared Router logic itself.
type JoinFunc func(prefix, leaf string) string

type Router[Target any] struct { /* unexported fields, mirrors today's Router exactly */ }

func NewRouter[Target any](prefix string, join JoinFunc, opts ...RouterOpt[Target]) Router[Target]
func (rt Router[Target]) Use(mws ...middleware.RouteMiddleware) Router[Target]
func (rt Router[Target]) Tags(tags ...string) Router[Target]
func (rt Router[Target]) With(mws ...middleware.RouteMiddleware) Router[Target]
func (rt Router[Target]) Route(r Routable[Target]) Router[Target]
func (rt Router[Target]) Mount(sub Router[Target]) Router[Target]
func (rt Router[Target]) Group(fn func(sub Router[Target]) Router[Target]) Router[Target]
func (rt Router[Target]) Walk(fn WalkFunc) error
func (rt Router[Target]) Routes() []RouterEntry
func (rt Router[Target]) Register(target Target) error
```

Each api package then does (sketch):

```go
package rest

type Router = router.Router[*Server] // type alias — fully source-compatible
func NewRouter(prefix string, opts ...router.RouterOpt[*Server]) Router {
    return router.NewRouter[*Server](prefix, joinRouterPath, opts...)
}
```

plus newly-exported leaf methods on `Route[Req,Resp]`/`SSERoute[Req,Event]` implementing
`router.Routable[*Server]`.

## Structured errors

`RouterPrefixError` becomes `router.RouterPrefixError` (or stays per-package via a thin wrapper,
TBD — see open design decisions); same `slog.LogValuer` shape either way, no change to its
fields or meaning.

## Observer integration

None — Router is a pure declare-time composition mechanism, no runtime dispatch, no observer
hook today in any of the 3 packages; this consolidation doesn't change that.

## Unit test plan

| Test | Verifies |
|---|---|
| Port EVERY existing `Router`/`Mount`/`Group`/`Walk` test from all 3 packages' existing router_test.go files onto the shared generic core, confirming byte-identical pass/fail behavior | Zero behavior regression — this is a pure refactor, the existing test suites ARE the regression suite |
| New `router` package-level tests for the generic core in isolation (a minimal fake `Routable`/`Target`) | The shared core's own correctness, independent of any specific api package |
| `rest.Router`/`events.Router`/`reqreply.Router` type-alias compile-check (`var _ router.Router[*rest.Server] = rest.Router{}` style) | Source compatibility actually holds, not just "intended to" |

## Files to create / touch

| File | Responsibility |
|---|---|
| `router/router.go` (new package) | The consolidated generic `Router[Target]`/`Routable[Target]`/`RouterEntry`/`RouterPrefixError`/`WalkFunc`/`JoinFunc` |
| `router/router_test.go` (new) | Generic-core-only test coverage with a fake `Routable`/`Target` |
| `api/rest/router.go` | Reduced to: type alias/thin wrapper + `joinRouterPath` passed in + newly-exported `Route`/`SSERoute` leaf methods |
| `api/events/router.go` | Same, with `joinRouterTopic` + `Subscriber`/`Publisher` leaf methods |
| `api/reqreply/router.go` | Same, with `joinRouterTopic` + `Route` leaf methods |
| `.github/instructions/go-codex.instructions.md` | New `router` package row; update `api/rest`/`api/events`/`api/reqreply` rows' Router description |
| `docs/design/d-0008-declarative-router-groups.md` | New addendum documenting this consolidation (post-hoc factoring of an already-shipped, already-designed mechanism — NOT a design change) |

## Out of scope (Phase 2)

- Any NEW Router capability (fallback handlers, `ports.Pattern` composition hooks, etc.) — this
  roadmap is a pure internal consolidation of EXISTING, already-shipped behavior. New
  capabilities stay in their own already-tracked roadmap items
  (`typed-router-groups.md`, `mcp-ports-declarative-middleware.md`).
- Unifying REST's path-joining with events/reqreply's topic-joining — different, legitimate
  conventions, kept via `JoinFunc`, not merged into one algorithm.

## Open design decisions

1. **Exported leaf-method naming** — promoting `withRouterPrefix`/`routeMethod`/
   `middlewareNames`/`tags`/`registerAny` to exported names on `Route`/`SSERoute`/`Subscriber`/
   `Publisher` is NEW PUBLIC API surface on types users already construct directly
   (`rest.NewRoute(...)` returns a `Route[Req,Resp]` value users hold and call methods on) —
   confirmed no current name collisions (checked `Tags`/`RouteMethod` do not already exist as
   exported methods), but the exact names need a final naming pass before implementation,
   especially since these methods will now show up in GoDoc on every leaf type regardless of
   whether a caller ever uses `Router` at all — a documentation-noise consideration worth a
   second look (e.g. a documented "internal, Router-support methods" godoc convention, or
   keeping them grouped in one doc block).
2. **Type alias vs. embedding wrapper for `rest.Router`/`events.Router`/`reqreply.Router`** — a
   plain type alias (`type Router = router.Router[*Server]`) is the simplest and keeps 100%
   method-set compatibility automatically, but means `router.RouterOpt[*Server]`/
   `router.Routable[*Server]` leak into each api package's public API surface directly (a user
   importing `api/rest` now transitively sees `router` package types in `rest.Router`'s method
   signatures) — need to confirm this is acceptable per the "a user works entirely in the api/*
   abstraction" design principle, vs. a thin wrapper struct that re-exports only what's needed
   (more code, cleaner public surface).
3. **`RouterPrefixError` — promote to `router.RouterPrefixError` (one shared type) or keep 3
   per-package copies that just wrap/forward it?** Precedent elsewhere in this codebase
   (`LegacySecurityClientMWRemovedError`, `MiddlewareMisattachedError`) favors per-package
   error types even when the underlying mechanism is shared — needs a consistency decision
   against that precedent, or a documented reason this case differs (Router's error doesn't
   carry pattern-specific semantics the way middleware's errors do, so sharing it directly may
   be fine here).
4. **Migration order relative to the (now fully implemented) legacy-security-middleware
   retirement (`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s Addendum 8)** —
   that effort is DONE; this roadmap starts with a clean slate, no remaining sequencing
   dependency.
