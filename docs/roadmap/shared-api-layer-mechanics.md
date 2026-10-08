# Shared API-Layer Mechanics — `router`, Bound Middleware, Declaration/Dispatch Core, and Error-Pattern Client Helpers

> **Status:** Design draft — not yet implemented.
> [← Back to Roadmap](index.md)

## Motivation

`api/rest`, `api/events`, and `api/reqreply` each independently implement three families of
mechanics that are — confirmed by direct code comparison, not assumption — near-identical,
hand-copied 3 times, differing only in each pattern's own merge-field vocabulary (REST:
header/cookie/query; events/reqreply: topic/property) and role-shape (REST/reqreply: one fused
`Route` carrying both server+client roles; events: a separate `Subscriber`/`Publisher` pair):

1. **Router** (`router.go` in each package, ~1800 lines combined) — `Router`, `Mount`, `Group`,
   `Walk`, prefix/topic-joining, middleware/tag accumulation.
2. **Bound Middleware** (`bound_middleware.go`, ~1500 lines combined) — `BoundMiddleware`/
   `BoundClientMiddleware` (or `BoundSubscribeMiddleware`/`BoundPublishMiddleware` for events),
   `HandleBoundMW`/`ClientBoundMW`, the `BoundSecurityMiddleware` family, and their shared
   `boundContributor`/`boundNamed`/mismatch-error plumbing.
3. **Declaration/Dispatch core** (`middleware_declaration.go` + `transform.go` +
   `transform_dispatch.go`, ~2000 lines combined) — the REUSABLE-class `Middleware[In,Out]`,
   `NewMiddleware`, context-field wiring, `MiddlewareHandler`/`ClientMiddlewareHandler`, and their
   dispatch functions.
4. **Error-Pattern client helpers** (`error_pattern_client.go`, ~200 lines combined, `api/rest` +
   `api/reqreply` ONLY) — `ErrorPatternAs[B]`/`HandleErrorPattern`/`Case[T]`, confirmed
   structurally identical and provably interchangeable (see Phase 4 below for why).
A fifth candidate was investigated alongside these four — **Security-credential validation**
(`api/rest/security_dispatch.go`'s pattern, not yet built for events/reqreply: a gap, not a
duplicate — REST has a shared, transport-agnostic `ValidateSecurityCredentials`/
`CredentialExtractor` abstraction every REST adapter reuses for free, events/reqreply don't, so
`adapters/mqtt5` reinvented its own private copy). Per the maintainer's direction, its design now
lives in [`docs/roadmap/amqp-adapter.md`](amqp-adapter.md) instead of here — see Phase 5's entry
below for the full reasoning.

A separate review pass (prompted directly by this roadmap's own existence) also checked the
Observer pattern for the same kind of duplication — confirmed ALREADY fully centralized in
`stats/observer.go` with zero duplicate Observer interface definitions anywhere in `api/` or
`adapters/`. No action needed there; it is not part of this roadmap.

All four mirror a pattern this codebase has ALREADY solved once, successfully: this same kind
of duplication existed for the single, most primitive piece — `middleware.Declaration[In,Out]`
and the `middleware.RouteMiddleware` marker interface — and was consolidated into the shared
`middleware` package specifically BECAUSE it was pattern-agnostic MECHANICS, not a pattern-specific
VALUE. This roadmap applies the identical, now-proven technique to the larger mechanisms built on
top of that primitive, which were never carried through to the same conclusion.

## Why this is a different, more tractable problem than the legacy security-scheme-sharing case

A companion effort (`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s Addendum 8)
concluded that sharing ONE security-declaration VALUE across 3 different api packages' `.Use()`
calls has no real use case and is barely achievable without a lowest-common-denominator type —
that was correctly left alone. All three mechanisms in THIS roadmap are a different shape of
problem: none of them are about sharing one VALUE across packages. They are about sharing one
IMPLEMENTATION (behavior/logic) that is currently hand-copied 3 times. Go generics can
parameterize over an exported interface + type parameters the same way
`middleware.RouteMiddleware`'s marker-interface trick already works — a solvable, moderate-sized
consolidation each time, not a fight against Go's type system.

## Phasing

Four independent phases live here, implementable in any order, though Phase 4 → 1 → 2 → 3 (most
shovel-ready to least) is the recommended sequence per the critical review below. A fifth
candidate (Security-Credential Validation) was investigated here but its design now lives in
[`docs/roadmap/amqp-adapter.md`](amqp-adapter.md) instead — see Phase 5's entry below for why.
Each phase's own "Scope decisions" table defines what ships in that phase; cross-phase
dependencies are called out explicitly where they exist.

## Critical review — confidence per phase (added after a dedicated critical pass)

A later review deliberately stress-tested each phase's actual benefit, not just its stated one.
Net result: confidence varies significantly across phases — this roadmap is NOT five equally-safe
bets, and should not be read or sequenced as such.

| Phase | Confidence | Why |
|---|---|---|
| **4 — Error-Pattern client helpers** | **High — shovel-ready** | Small, zero unresolved design questions, confirmed structurally interchangeable TODAY (not a future hope), no builder/dispatch entanglement. |
| **1 — Router** | **High — shovel-ready** | Originally questioned (no 4th CURRENT consumer — confirmed `api/mcp` deliberately uses a smaller, different construct instead), but the maintainer has CONFIRMED concrete plans to apply the same path/key-PREFIX grouping to `ports.Cache` (redis) and `ports.File` — so Phase 1's generic `Router[Target]`/`Routable[Target]` design is validated against 2 near-term additional consumers beyond the 3 existing ones, not zero. The exported-method-surface cost (see Phase 1's own section) still applies and is worth keeping in mind, but the reuse argument now holds. All 4 of Phase 1's own open design decisions are now SETTLED (method naming, type-alias, per-package `RouterPrefixError`, and the `RouterEntry` Method/Role/neither mismatch via an optional `MethodReporter` interface) — nothing left blocking implementation start. |
| **5 — Security-credential validation** | **Relocated** | Originally flagged as premature abstraction (generalizing from the single real example, `mqtt5`, before a second transport exists to validate the shape). Per the maintainer's direction, this phase's design now lives in [`docs/roadmap/amqp-adapter.md`](amqp-adapter.md) instead, as part of AMQP's own security design — sequencing it against AMQP (a firm near-term next adapter) as the real second data point, rather than keeping it here as a consumer-less, standalone phase. |
| **2 — Bound Middleware** | **Medium-High — design settled, not yet scheduled** | Originally flagged Low/NOT-shovel-ready: `routeBuilder`/`channelBuilder` are mutated by 20-33+ unrelated declare-time call sites in `api/rest`/`api/events` alone, and generalizing the Bound-attach touchpoint without a leaky exported builder interface was a genuine, unresolved RESEARCH question. That investigation is now DONE: the actual footprint is only 3-4 plain `append()` calls, a narrow `BoundRouteBuilder` interface covering them is a validated, concrete design (not a leaky catch-all), `BoundCore.Mw`'s type is locked in as `Declaration[In,Out]` (keeping this phase independent of Phase 3), and the API-surface sketch's internal inconsistency (a stray type-erased `any` sketch contradicting the resolved design) has been corrected. A real `api/events` spec-population bug was also found and fixed as a direct result of this investigation. Implementation is still not SCHEDULED (Phase 1/4 take priority per the recommended sequence), but nothing design-level blocks starting it. |
| **3 — Declaration/Dispatch core** | **Deferred (by design)** | Already correctly scoped as a lighter sketch, deferred until Phases 1+2 ship and prove the pattern. No change from the critical review. |

**On the "reduces repeated-bug risk" argument**: the critical review also checked whether this
roadmap addresses the ACTUAL repeated-bug pattern this project has experienced (the SAME class of
dispatch gap independently rediscovered multiple times — see `docs/design/d-0001-...md`'s "Lessons
Learned" and `docs/design/d-0007-declarative-middleware-layering.md`'s "Post-ship regression
found and fixed"). That pattern lives in each ADAPTER's own runtime dispatch/merge plumbing
(`adapters/nethttp`/`mqtt5`/`mqtt`/`zeromq`), which NONE of this roadmap's 5 phases touch — this
roadmap's phases consolidate the comparatively stable, less-frequently-buggy DECLARATION/BUILDER
layer instead. **That separate, already-tracked problem is the explicit subject of
[`docs/roadmap/adapter-dispatch-unification.md`](adapter-dispatch-unification.md)** — this
roadmap and that one are COMPLEMENTARY, addressing different layers; this roadmap's phases should
be valued on DRY/maintainability/planned-reuse grounds (per the table above), not on "prevents the
next cross-adapter dispatch bug" grounds, which is that OTHER roadmap's job.

---

## Phase 1 — Router (`router` package)

**Confirmed future consumers beyond the 3 existing api packages**: the maintainer has concrete
plans to apply the SAME path/key-prefix grouping this Router generalizes to `ports.Cache`
(redis) and `ports.File` — validating `Routable[Target]`'s generic `Target` parameterization
against 2 additional, structurally different consumers (a cache key-prefix namespace and a
filesystem path-prefix namespace, neither of which is a `Register(*Server)`-style api handle the
way `rest`/`events`/`reqreply` are). This needs its own follow-up design check once started
(confirming `ports.Cache`/`ports.File` can genuinely satisfy `Routable[Target]`'s shape, or
whether they need a variant), but materially strengthens Phase 1's reuse case beyond "3 packages,
frozen forever" (the `api/mcp` precedent, which explicitly rejected this construct as a poor fit,
still stands as a confirmed NON-consumer — it's `ports.Cache`/`ports.File` specifically that
extend the reuse case, not every conceivable future api).

### Scope decisions

| In scope | Out of scope |
|---|---|
| Consolidate the shared Router state machine (`routerChild`, prefix/topic-joining, pendingMws/tags accumulation, `Mount`/`Group` merge semantics, `Walk`/`Routes` traversal, `RouterPrefixError`) into a new `router` package, generic over an exported `Routable`-shaped leaf interface and a register-target type | Changing ANY of `Router`'s observable behavior/semantics in any of the 3 api packages — pure internal-implementation consolidation, zero behavior change intended |
| Keep `rest.Router`, `events.Router`, `reqreply.Router` as the PUBLIC, source-compatible type names every existing caller already uses (type aliases or embedding wrappers around the shared generic core) | Breaking ANY existing example/test's `rest.Router{...}`/`events.Router{...}`/`reqreply.Router{...}` usage |
| Add a new exported `router.Routable[Target]`-shaped interface that each api's own leaf type implements via newly-EXPORTED methods (promoting today's unexported `withRouterPrefix`/`routeMethod`/`middlewareNames`/`tags`/`registerAny`) | Changing `RouterOpt`/`ClientHandleOpt`/`WithRouter` call sites' public signatures beyond what's structurally required |
| Preserve the REST-specific (leading-slash) vs events/reqreply-specific (no leading slash) path/topic-joining difference via a small per-instantiation join-strategy hook | Unifying REST's path-joining and events/reqreply's topic-joining into ONE algorithm |

### API surface (sketch)

```go
package router

type Routable[Target any] interface {
    WithRouterPrefix(prefix string, mws []middleware.RouteMiddleware, tags []string) (Routable[Target], string)
    RouteMethod() string
    MiddlewareNames() []string
    RouteTags() []string
    RegisterAny(target Target) error
}

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

### Structured errors

`RouterPrefixError` becomes `router.RouterPrefixError` (or stays per-package via a thin wrapper
— see Phase 1's open design decisions).

### Unit test plan

| Test | Verifies |
|---|---|
| Port EVERY existing `Router`/`Mount`/`Group`/`Walk` test from all 3 packages' router_test.go files onto the shared generic core | Zero behavior regression — the existing test suites ARE the regression suite |
| New `router` package-level tests with a minimal fake `Routable`/`Target` | The shared core's own correctness, independent of any specific api package |
| `rest.Router`/`events.Router`/`reqreply.Router` type-alias compile-check | Source compatibility actually holds |

### Files to create / touch

| File | Responsibility |
|---|---|
| `router/router.go` (new package) | Consolidated generic `Router[Target]`/`Routable[Target]`/`RouterEntry`/`RouterPrefixError`/`WalkFunc`/`JoinFunc` |
| `router/router_test.go` (new) | Generic-core-only test coverage |
| `api/rest/router.go`, `api/events/router.go`, `api/reqreply/router.go` | Reduced to a type alias/thin wrapper + join-strategy + newly-exported leaf methods |
| `.github/instructions/go-codex.instructions.md` | New `router` package row |
| `docs/design/d-0008-declarative-router-groups.md` | New addendum documenting this consolidation |

### Design decisions — ALL RESOLVED (1-4)

1. **Exported leaf-method naming** — SETTLED: `WithRouterPrefix`/`RouteMethod`/`MiddlewareNames`/
   `RouteTags`/`RegisterAny` (as already shown in the API surface sketch above) — confirmed zero
   collisions via grep; locked in as final, no further naming pass needed.
2. **Type alias vs. embedding wrapper** — SETTLED: type alias (`type Router = router.Router[*Server]`).
   Simpler, 100% automatic method-set compatibility, consistent with this project's general
   preference for the simplest solution that works. The "leaks `router` package types
   transitively" cost is minor and consistent with how `codex`/`route` types already freely
   appear in every api package's public signatures today — not a new kind of coupling.
3. **`RouterPrefixError` — one shared type or 3 per-package wrappers?** — SETTLED: per-package
   thin wrappers, matching the `MiddlewareMisattachedError`/`LegacySecurity*MWRemovedError`
   precedent already established and deliberately chosen in Rounds 164/165. Consistency with
   recent, deliberate precedent wins over the alternative's marginal simplicity.
4. **`RouterEntry`'s per-package `Method`/`Role`/neither mismatch — SETTLED.**
   `Routable[Target]`'s originally-sketched `RouteMethod() string` (and the implied single
   generic `RouterEntry` shape) assumed a uniformity that does NOT exist today: `api/rest`'s
   unexported `routable` interface has `routeMethod() string` (HTTP verb, feeding
   `RouterEntry.Method`); `api/events`'s has a DIFFERENTLY NAMED, differently-meaning `role()
   string` (`"subscribe"`/`"publish"`, feeding `RouterEntry.Role`); `api/reqreply`'s `routable`
   has **neither** — only 4 methods total (`withRouterPrefix`/`middlewareNames`/`tags`/
   `registerAny`), and `RouterEntry` there has only 3 fields (`Path`/`MiddlewareNames`/`Tags`),
   no method/role concept at all. Forcing reqreply's leaf types to implement a meaningless
   `RouteMethod()` just to satisfy one generic interface would be new, purposeless public
   surface.

   **Resolution (discussed with and confirmed by the maintainer, including a full pros/cons/
   consequences comparison of all 3 candidates): Option A — a separate, OPTIONAL marker
   interface.** `Routable[Target]` is
   trimmed to the 4 genuinely-uniform methods (`WithRouterPrefix`/`MiddlewareNames`/`RouteTags`/
   `RegisterAny`); a second, narrow, OPTIONAL interface is added:

   ```go
   package router

   // MethodReporter is implemented by a Routable leaf that has a
   // method/role-equivalent concept to report (REST's HTTP verb, events'
   // subscribe/publish role). Router.Walk/Routes type-asserts for it,
   // falling back to "" when a leaf doesn't implement it (reqreply's
   // case — it has no such concept at all).
   type MethodReporter interface {
       RouteMethod() string
   }
   ```

   Rejected alternatives and why:
   - **A 2nd generic type parameter** (`Router[Target, Entry any]`) was rejected: it adds a
     second type parameter to EVERY `Router[...]` instantiation across all 3 packages (and any
     future `ports.Cache`/`ports.File` consumer from Phase 1's own motivation) purely to solve a
     single optional field — disproportionate generic-surface cost for the problem.
   - **Renaming to a shared `RouterEntry.Kind` field** was rejected: it is a small but real,
     unforced observable breaking change to 2 of 3 packages' existing public struct fields
     (`RouterEntry.Method`/`RouterEntry.Role`), with no behavioral or ergonomic benefit over the
     optional-interface approach — not worth the churn.
   - The optional-interface approach is ALSO the one with the most direct precedent already in
     this codebase: `stats.FileObserver`/`SQLObserver`/`SecurityObserver`/`TraceObserver` are all
     optional, type-asserted extensions to a base interface for exactly this reason (not every
     implementer has every capability) — `router.MethodReporter` is the same shape of problem,
     solved the same, already-endorsed way, rather than inventing a new pattern.

   `api/rest`'s `Route[Req,Resp]`/`SSERoute[Req,Event]` and `api/events`'s `Subscriber[T]`/
   `Publisher[T]` implement `MethodReporter`; `api/reqreply`'s `Route[Req,Resp]` does not (its
   `RouterEntry.Method` stays absent, exactly as today — zero behavior change for reqreply
   callers). `RouterEntry.Method`/`RouterEntry.Role` keep their current PER-PACKAGE names (no
   rename) since each api package's thin wrapper still builds its own `RouterEntry` from the
   shared `router.Walk`'s callback, same as Phase 1's general type-alias approach already
   requires — the shared `router` package itself only needs a generic, pattern-agnostic entry
   accessor (e.g. `router.RouterEntry.Method string` populated via the optional
   `MethodReporter` type-assertion, left `""` when absent), which each api package's own
   `RouterEntry` type can then surface under its OWN already-existing field name if a thin
   wrapper type is used instead of a straight alias for `RouterEntry` specifically (`RouterEntry`
   was not covered by Phase 1's earlier type-alias-vs-wrapper decision, which was scoped to
   `Router` itself — this is a separate, now also decided, instance of the same question,
   resolved the same way: a thin per-package `RouterEntry` wrapper/rename-via-embedding is
   acceptable here precisely BECAUSE the underlying fields already differ (`Method` vs `Role`)
   and always will, unlike `Router`, which is 100% uniform across all 3 packages).

   **Minor, non-blocking note found alongside this**: the sketched `RouterOpt[Target]` generic
   type has ZERO current concrete implementations in any of the 3 packages to validate against
   today (`RouterOpt interface{ applyRouter(*Router) }` exists in all 3, but nothing implements
   it yet — an unused extension point). Not a blocker, but worth a quick sanity check once Phase
   1 implementation starts, since there's no real option type yet to confirm the generic form
   works cleanly against.

---

## Phase 2 — Bound Middleware (extend `middleware` package)

> **STATUS: design question RESOLVED; implementation still NOT scheduled.** The investigation
> this status note previously called for is done: `applyBoundRoute`/`applyBoundSubscriber`/
> `applyBoundPublisher`'s ACTUAL footprint on each pattern's internal builder/role type is small
> — 3-4 plain `append()` calls, not the large, unstructured surface the raw `routeBuilder`
> touchpoint COUNT (20-33+, counting EVERY declare-time call site, most unrelated to Bound
> attach specifically) suggested. A narrow exported interface covering exactly that footprint is
> feasible — see the resolved "Open question" below for the concrete design. Implementation is
> still not scheduled (Phase 1/4 take priority), but this is no longer an unresolved research
> question.
>
> **A real, confirmed bug was found and FIXED as a direct result of this investigation** (not a
> design question — a genuine spec-population gap): `api/events`'s `BoundSubscribeMiddleware`/
> `BoundPublishMiddleware` were missing the Security-declaration step `api/rest`/`api/reqreply`'s
> equivalents both have, meaning a `SubscribeBoundMW`/`PublishBoundMW`-only attachment (no
> companion `.Use()` call) left the channel's published AsyncAPI spec silently omitting a
> security requirement the dispatch logic was actually enforcing. Confirmed via a live repro
> contrasting all 3 packages (REST and reqreply both correctly populate `Descriptor.Security`/
> `SecuritySchemes` from a Bound-only attach; events did not) before fixing. See
> `applyBoundSubscriber`/`applyBoundPublisher` in `api/events/bound_middleware.go` for the fix,
> and the new regression tests in `api/events/builder_test.go` confirming it.

### Scope decisions

| In scope | Out of scope |
|---|---|
| Consolidate `BoundMiddleware[Req,In,Out]`/`BoundClientMiddleware[Req,In,Out]` (events: `BoundSubscribeMiddleware`/`BoundPublishMiddleware`, same shape under different names), `NewBoundMiddleware`/`NewBoundClientMiddleware`, the `boundContributor[Req]`/`boundClientContributor[Req]`/`boundNamed` interface family, `boundNameOf`, and the shared mismatch-error shape into `middleware` | Changing the `BoundSecurityMiddleware`/`BoundSecurityClientMiddleware` convenience constructors' per-pattern call signatures (`rest.BoundSecurityMiddleware`, `events.BoundSecuritySubscribeMiddleware`, etc. stay as thin, pattern-named wrappers) |
| Keep `HandleBoundMW`/`ClientBoundMW`/`SubscribeBoundMW`/`PublishBoundMW` as each pattern's OWN method (they differ in attachment point — `Route` vs `Subscriber`/`Publisher` — and in which merge-field vocabulary `With*` exposes) | Unifying REST's header/cookie/query vocabulary with events/reqreply's topic/property vocabulary into one generic set of `With*` methods — these remain genuinely different, pattern-specific extension methods |
| Keep `BoundMiddlewareReqMismatchError`/`MiddlewareMisattachedError`/the `LegacySecurity*MWRemovedError` family per-package (matches Phase 1's likely precedent and this codebase's existing convention) | Any behavior change to the Bound dispatch itself — pure internal consolidation, the just-completed legacy-security-middleware retirement's test suites (migrated in the same effort that produced this roadmap) are the regression suite |

### Why this phase is well-timed

The Bound-middleware mechanism was JUST extensively exercised (every `HandleBoundMW`/
`ClientBoundMW`/`SubscribeBoundMW`/`PublishBoundMW` call site in the repo was touched migrating
off the legacy security-pairing mechanism) — its current test coverage is unusually fresh and
thorough right now, lowering the risk of a silent regression during this consolidation.

### API surface (sketch)

```go
package middleware

// BoundCore is the pattern-agnostic shared state EVERY Bound*Middleware
// type embeds (named, not embedded as an anonymous field — see
// api/rest/bound_middleware.go's existing doc comment for why: avoiding
// accidental method promotion that would let a bound value satisfy the
// agnostic .Use() contributor interface).
type BoundCore[Req, In, Out any] struct {
    // Mw is Declaration[In, Out] — SETTLED, not Middleware[In,Out]. The
    // lower-level, ALREADY-shared primitive keeps Phase 2 independent of
    // Phase 3's (much larger, deferred) Middleware[In,Out] consolidation,
    // consistent with this roadmap's own Phase 4→1→2→3 sequencing —
    // depending on Middleware[In,Out] here would silently make Phase 2
    // depend on Phase 3 shipping first, which the sequencing explicitly
    // does not intend.
    Mw Declaration[In, Out]
    // Fn is intentionally NOT here — each pattern's own Bound*Middleware
    // wrapper holds its own differently-shaped Fn (server: func(ctx,
    // *Req, In) (Out, error); client: func(ctx, Req) (In, error); events'
    // publish role: func(ctx, T) (Out, error)) — BoundCore only unifies
    // the declaration half, not the Fn signature, which genuinely needs
    // to vary.
}

// BoundContributor/BoundClientContributor — EXPORTED equivalents of each
// package's current unexported boundContributor/boundClientContributor,
// with the SAME Req-witness discriminator trick. rb is typed as the
// narrow BoundRouteBuilder interface below (NOT a type-erased `any` with
// a runtime type-assertion escape hatch — an earlier sketch of this
// interface used `any` here before the "Open question" below was
// resolved; this is the corrected, single coherent design, not an
// alternative to it).
type BoundContributor[Req any] interface {
    ApplyBoundRoute(rb BoundRouteBuilder)
    BoundReqWitness(Req)
}

// BoundNamed, BoundMiddlewareReqMismatchError — straightforward ports of
// the existing per-package types.
```

**Open question — RESOLVED.** The `rb *routeBuilder`/`*channelBuilder`/`*Subscriber`/`*Publisher`
parameter in `applyBoundRoute`/`applyBoundClientRoute`/`applyBoundSubscriber`/
`applyBoundPublisher` is each pattern's OWN internal type, mutated by MANY other,
entirely-unrelated declare-time calls (`.Use()`, `.WithHandler()`, param declarations, etc.) —
but a direct read of EVERY ONE of these 6 methods (both roles, all 3 packages) confirms their
ACTUAL combined footprint is only 4 distinct operations, each a plain `append()`:

1. Append a `MiddlewareHandler`/`ClientMiddlewareHandler` (server/client role respectively).
2. Append a `middlewareSpecContribution` (rest/reqreply only — see item 1 under Phase 3's open
   items for why events' shape differs here too; not blocking, just a vocabulary difference).
3. Append a Security declaration (`middleware.Middleware{Name, Security}`) to the pattern's own
   `mws`/`middlewares` list — **this is the exact step `api/events` was found to be MISSING,
   fixed as part of this investigation** (see the Phase 2 status note above).

A narrow exported interface covering exactly these 3-4 operations is feasible and is the
recommended design once Phase 2 implementation starts:

```go
package middleware

type BoundRouteBuilder interface {
    AppendMiddlewareHandler(h any)
    AppendClientMiddlewareHandler(h any) // only where a client/sending role exists
    AppendSpecContribution(c any)
    AppendSecurityDeclaration(name string, sec *SecurityDeclaration)
}
```

Each pattern's own `routeBuilder`/`channelBuilder`/`Subscriber`/`Publisher` would implement this
narrow interface; `applyBoundRoute`/etc. call through it uniformly instead of touching named
fields directly — which would have made the events gap above structurally impossible to
introduce in the first place (a shared code path enforces all 3-4 steps identically, rather than
each pattern separately remembering to replicate them by hand).

### Unit test plan

| Test | Verifies |
|---|---|
| Port every existing Bound-middleware test (all 3 packages — freshly exercised by the legacy-security-middleware retirement) onto the shared core | Zero behavior regression |
| New `middleware` package-level tests for `BoundCore`/`BoundContributor` in isolation | Shared core correctness |

### Files to create / touch

| File | Responsibility |
|---|---|
| `middleware/bound.go` (new) | `BoundCore[Req,In,Out]`, `BoundContributor[Req]`/`BoundClientContributor[Req]`, `BoundNamed`, `BoundMiddlewareReqMismatchError`, the narrow `BoundRouteBuilder`-style interface |
| `api/rest/bound_middleware.go`, `api/events/bound_middleware.go`, `api/reqreply/bound_middleware.go` | Reduced to: pattern-specific `Bound*Middleware` wrapper types (merge-field `With*` methods only), `HandleBoundMW`/etc. methods, `BoundSecurity*Middleware` convenience constructors |
| `.github/instructions/go-codex.instructions.md` | Update `middleware` row |

---

## Phase 3 — Declaration/Dispatch Core (extend `middleware` package further) — lighter sketch, needs fleshing out before implementation

### Scope (preliminary)

`Middleware[In,Out]`/`NewMiddleware`/`SetContextFieldFromIn`/`SetContextFieldFromOut`/
`RouteMiddlewareMarker`/`SecurityDeclaration`/`MiddlewareName` (in `middleware_declaration.go`,
429/554/408 lines across rest/events/reqreply) and `MiddlewareHandler`/`ClientMiddlewareHandler`/
their dispatch functions (in `transform.go`/`transform_dispatch.go`, ~1000 lines combined) follow
the IDENTICAL duplication pattern Phases 1 and 2 already address — confirmed via direct
side-by-side comparison, not yet designed in detail.

This is the LARGEST and most invasive of the three phases — `Middleware[In,Out]` is the single
most heavily-used type across all 3 packages' public APIs (every `.Use()` call site, every
`WithRequestHeader`/`WithSubscribeTopic`-style builder chain). A consolidation here touches more
surface area than Phases 1+2 combined. **Recommendation: treat Phase 3 as a candidate for its own
follow-up design spike AFTER Phase 1 and Phase 2 ship and their consolidation pattern is proven
twice over** — do not start Phase 3's detailed design until then.

### Preliminary files identified

| File | rest | events | reqreply |
|---|---|---|---|
| `middleware_declaration.go` | 429 | 554 | 408 |
| `transform.go` | 462 | 282 | 378 |
| `transform_dispatch.go` | 105 | 191 | 139 |

### Open design decisions (preliminary — not exhaustive; 1 resolved now, 2 deliberately deferred — deferral explicitly discussed and confirmed with the maintainer, not just assumed)

1. **DELIBERATELY DEFERRED, not an oversight gap.** Same merge-field-vocabulary question Phase 2
   faces (header/cookie/query vs topic/property), at a LARGER scale (`Middleware[In,Out]` has
   more `With*` methods than any single `Bound*Middleware` type). This is EXPLICITLY not being
   resolved now: per this section's own recommendation above, Phase 3's detailed design doesn't
   start until Phase 1 and Phase 2 ship and the consolidation pattern is proven twice over — a
   decision made before that proof exists would be speculative, not informed. Revisit as the
   FIRST question of Phase 3's own design spike, armed with 2 real precedents (Phase 1's Router
   consolidation, Phase 2's Bound Middleware consolidation) instead of theorizing in the abstract.
2. **RESOLVED — NOT already shareable.** Confirmed via direct read: REST's
   `DecodeIn`/`EncodeOut` shapes are `func(ctx, headerVars, cookieVars, queryVars map[string]string)
   (any, error)` / returns `(headers, cookies map[string]string, err error)` (3-in/2-out);
   events' are `func(ctx, topicVars, propertyVars map[string]string) (any, error)` / returns
   `(topicVars, propertyVars map[string]string, err error)` (2-in/2-out) — different arity, not
   interchangeable without a redesign (e.g. a variadic or map-of-maps signature). This confirms
   Phase 3's deferral is correct for a concrete reason, not just caution — there IS real,
   unavoidable design work here, not a free consolidation waiting to be noticed.
3. **DELIBERATELY DEFERRED, not an oversight gap.** Whether to tackle `middleware_declaration.go`
   and `transform.go`/`transform_dispatch.go` as ONE consolidation or two further sub-phases —
   same rationale as item 1: this is a sequencing/scoping call best made once Phase 1+2's actual
   consolidation SIZE and effort are known data points, not guessed at from line counts alone.
   Explicitly held open until the Phase 3 design spike begins.

---

## Phase 4 — Error-Pattern Client Helpers (extend `middleware` package)

### Background

`ErrorPatternAs[B]`/`HandleErrorPattern`/`Case[T]` were ORIGINALLY per-adapter (one copy per
client package), moved into `api/rest`/`api/reqreply` specifically to fix that (per the
"convenience helpers belong in api/*, not adapters" design guardrail) — but the move stopped at
"2 copies instead of N adapter copies," never finishing the consolidation into ONE shared
location. Confirmed via direct comparison: `api/rest/error_pattern_client.go` (102 lines) and
`api/reqreply/error_pattern_client.go` (105 lines) are structurally identical — same function
names, same logic, ZERO pattern-specific coupling in either copy (both operate purely on
`error`/`errors.As` and a one-method `ErrorPatternValuer` interface).

**`api/events` correctly has NO equivalent** — confirmed via tracing the full mechanism:
`events.Client.Publish` is a ONE-WAY, fire-and-forget operation (pub/sub has no reply channel by
protocol design); `events.ErrorChannel[E,B]` republishes a failed message's error to a DIFFERENT
topic, consumed by a SEPARATE Subscriber, never round-tripping back to the original `Publish`
caller. No events-specific `ErrorPatternResponse`/`ErrorPatternValuer`-implementing type exists
anywhere (`adapters/mqtt5`'s and `adapters/zeromq`'s own `ErrorPatternValue()` methods are
explicitly documented as implementing `reqreply.ErrorPatternValuer`, for their REQREPLY half,
not events). This is a structural non-issue, not a gap — events is correctly excluded from this
phase.

**Confirmed interchangeable today**: `rest.ErrorPatternValuer` and `reqreply.ErrorPatternValuer`
have the IDENTICAL method set (`ErrorPatternValue() any`) — Go's structural interface typing
means any concrete type satisfying one already satisfies the other right now, with zero changes
needed to prove it.

### Scope decisions

| In scope | Out of scope |
|---|---|
| Move `ErrorPatternAs[B]`/`HandleErrorPattern`/`Case[T]`/`errorCase`/`typedCase[T]` into a shared location (`middleware` package, alongside the `ErrorPatternValuer` interface itself) | Any change to the adapter-specific `ErrorPatternResponse` types (`nethttp.ErrorPatternResponse`, `mqtt5.ErrorPatternResponse`, `zeromq.ErrorPatternResponse`, etc.) — these legitimately differ per protocol (status codes vs. property-based codes) and stay exactly as they are |
| Keep `rest.ErrorPatternAs`/`rest.HandleErrorPattern`/`rest.Case`/`rest.ErrorPatternValuer` and `reqreply`'s equivalents as PUBLIC, source-compatible re-exports (type aliases / thin forwarding functions) | Adding an events-side equivalent — confirmed structurally impossible/meaningless for one-way pub/sub, not deferred, REJECTED |

### API surface (sketch)

```go
package middleware

type ErrorPatternValuer interface {
    ErrorPatternValue() any
}

func ErrorPatternAs[B any](err error) (B, bool) { /* moved, unchanged logic */ }
func HandleErrorPattern(err error, cases ...ErrorCase) bool { /* moved, unchanged logic */ }
func Case[T any](fn func(T)) ErrorCase { /* moved, unchanged logic */ }
type ErrorCase interface{ /* unexported method, unchanged */ }
```

```go
package rest

type ErrorPatternValuer = middleware.ErrorPatternValuer // type alias — fully source-compatible
func ErrorPatternAs[B any](err error) (B, bool) { return middleware.ErrorPatternAs[B](err) }
func HandleErrorPattern(err error, cases ...middleware.ErrorCase) bool { return middleware.HandleErrorPattern(err, cases...) }
func Case[T any](fn func(T)) middleware.ErrorCase { return middleware.Case(fn) }
```

(`api/reqreply` gets the identical forwarding shim.)

### Unit test plan

| Test | Verifies |
|---|---|
| Port every existing `ErrorPatternAs`/`HandleErrorPattern`/`Case` test from both `api/rest` and `api/reqreply` onto the shared implementation | Zero behavior regression |
| A cross-package test: an `mqtt5.ErrorPatternResponse`-shaped value (reqreply) matched via `rest.ErrorPatternAs` and vice versa | Proves the structural-interchangeability claim above isn't just a reading of the code — confirms it live |

### Files to create / touch

| File | Responsibility |
|---|---|
| `middleware/error_pattern.go` (new) | `ErrorPatternValuer`, `ErrorPatternAs[B]`, `HandleErrorPattern`, `Case[T]`, `ErrorCase` |
| `api/rest/error_pattern_client.go`, `api/reqreply/error_pattern_client.go` | Reduced to type aliases + thin forwarding functions |
| `.github/instructions/go-codex.instructions.md` | Update `middleware` row |

---

## Phase 5 — Security-Credential Validation (relocated)

**This phase's full design now lives in [`docs/roadmap/amqp-adapter.md`](amqp-adapter.md)'s
"Credential-format validation" section, as part of that adapter's own security design** —
deliberately sequenced that way (per the maintainer's direction) so the shared
`events.ValidateSecurityCredentials`/`reqreply.ValidateSecurityCredentials` +
`PropertyExtractor` abstraction is validated against a REAL second transport (AMQP, a firm
near-term next adapter) rather than generalized from `mqtt5` alone — avoiding the classic
premature-generalization risk a standalone, consumer-less version of this phase would have
carried. See that doc for the current design, scope, and open questions.

---

## Toolchain / dependency decisions

None, for all four phases — pure internal Go generics refactors, no new external dependency
anywhere.

## Out of scope (all phases)

- Any NEW capability in Router, Bound middleware, or the declaration/dispatch core — Phases 1-3
  are pure internal consolidations of EXISTING, already-shipped behavior. New capabilities stay
  in their own already-tracked roadmap items (`typed-router-groups.md`,
  `mcp-ports-declarative-middleware.md`).
- Unifying REST's header/cookie/query vocabulary with events/reqreply's topic/property
  vocabulary — these are different, legitimate per-transport conventions, kept separate via
  pattern-specific extension methods in every phase.
- Re-introducing cross-pattern VALUE sharing in any form — explicitly rejected territory, see
  the "Why this is different" section above.
- Adding an `events` equivalent of `ErrorPatternAs`/`HandleErrorPattern`/`Case` (Phase 4) —
  confirmed structurally impossible/meaningless for one-way pub/sub (no reply channel to decode
  a typed payload from), not a deferred gap.
- The Observer pattern — checked, already fully centralized in `stats/`, not part of this
  roadmap at all.
- Security-credential validation (formerly Phase 5 here) — relocated to
  `docs/roadmap/amqp-adapter.md`, see that doc instead.
