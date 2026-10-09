# D-0009 — Internalizing Shared Cross-Pattern Mechanics — `internal/router`, `internal/route`, `internal/middleware`

> **Status:** Implemented — architectural foundation. `router`, `route`,
> and `middleware` were relocated from public top-level packages to
> `internal/router`, `internal/route`, `internal/middleware`; each of
> `api/rest`/`api/events`/`api/reqreply` gained its own thin public
> wrapper (type aliases + forwarding constructors) exposing the
> identical vocabulary under its own name. This is the retrospective
> record of that change and the standing design rule it establishes for
> future shared mechanics — graduated from `docs/roadmap/` to
> `docs/design/` since it establishes a pattern every future shared
> mechanic (current or future `api/*`/`ports` packages) is expected to
> follow, codified in `.github/instructions/go-codex.instructions.md`'s
> Design Philosophy, 2 skills' Gotchas sections, and the
> `review-go-codex` checklist's own §16.

## Motivation

`shared-api-layer-mechanics.md`'s Phase 1 (Router) and Phase 2 (Bound
Middleware) consolidated cross-pattern MECHANICS (the Router state
machine; the Bound-middleware dispatch interfaces) out of 3
independently-hand-copied implementations in `api/rest`/`api/events`/
`api/reqreply`, into shared `router`/`middleware` packages. Reflecting on
that work surfaced a gap: those packages were still PUBLIC, top-level,
directly importable by an external go-codex user — nothing but
convention stopped `import "github.com/DaniDeer/go-codex/router"` and
constructing a `router.Router[SomeType]` directly, bypassing
`rest.NewRouter`/`events.NewRouter`/`reqreply.NewRouter` entirely. The
project's own stated design philosophy is that a user works ENTIRELY in
the `api/*`/`ports` abstraction — this was enforced only by documentation
for `router`, and not even documented as a rule at all for `route`/
`middleware`, both of which were ALSO genuinely shared, pattern-agnostic
vocabulary (security schemes; declarative middleware) with 35+ example
files importing them directly.

The fix: move all 3 under Go's `internal/` mechanism, which makes
"importable only by code rooted at the parent of `internal/`" a
COMPILER-enforced fact rather than a documentation convention — since
`internal/router`/`internal/route`/`internal/middleware` sit at the repo
root, every in-module consumer (the whole go-codex module) keeps
importing them unchanged, while an external user who imports go-codex as
a dependency cannot reach them at all.

## Scope decisions

| In scope | Out of scope |
|---|---|
| Relocate `router`→`internal/router`, `route`→`internal/route`, `middleware`→`internal/middleware` | Renaming/restructuring any of the 3 packages' internal implementation — this is a PURE relocation, zero behavior change |
| Per-pattern public wrapper (type aliases + forwarding constructors) in `rest`/`events`/`reqreply` for every symbol a user previously imported directly | Wrapping symbols with ZERO real-world example/test usage differently from symbols with heavy usage — every public symbol gets the same treatment for completeness |
| Relocating `Disposition`/`ResolveDisposition`/`EnsureDispositionBox`/`SetDisposition`/`DispositionFromContext` from `middleware` to `stats` | Any OTHER `stats.Observer`-family interface restructuring — this was the one, isolated, pre-existing blocker found |
| Migrating all ~35 example files + ~100 internal-module consumer files to the new structure | Changing example BEHAVIOR — every example demonstrates the exact same thing, through the exact same mechanism, just via the new import path/wrapper |
| Updating `.github/instructions/go-codex.instructions.md`, `plan-a-new-codex-feature`/`add-a-new-adapter`/`review-go-codex` skills, `docs/concepts/ports-and-adapters.md`, `docs/reference/project-structure.md`, and the design docs (`d-0003`/`d-0006`/`d-0007`/`d-0008`) to codify this as a standing rule for FUTURE shared mechanics | A line-by-line historical rewrite of every `middleware.X`/`route.X` mention across design docs' own "Decision N" narrative sections — those correctly describe what was true AT THE TIME of those decisions; only CURRENT-reality-facing sections (package tables, import lists, checklists) were updated |

## The critical blocker: `stats.DispositionObserver`

`stats.DispositionObserver` — a PUBLIC, user-implementable extensibility
interface in the SAME family as `stats.Observer`/`stats.SecurityObserver`
— had a method `RecordDisposition(location string, disposition
middleware.Disposition)`. `Disposition` is a pattern-agnostic
observability/classification concept with no natural per-pattern home
(unlike `Middleware`/`SecurityDeclaration`, which at least have a
plausible "attach via rest/events/reqreply" framing). If `middleware`
moved wholesale to `internal/middleware`, an EXTERNAL user implementing
their own `stats.DispositionObserver` would be unable to even WRITE the
method signature — they cannot name an `internal/` type.

**Resolution**: relocate `Disposition`/`ResolveDisposition`/
`EnsureDispositionBox`/`SetDisposition`/`DispositionFromContext` from
`middleware` into `stats` itself (`stats.Disposition`, etc.) BEFORE
internalizing `middleware`. This is arguably a better home anyway —
Disposition is conceptually an observability/classification concept,
naturally closer to `stats`'s own Observer family than to the
declare/implement middleware vocabulary. Confirmed via grep: zero import
cycle risk (`middleware` never imported `stats`); only ~12 consumer
files, most already importing `stats` directly.

This is now the documented, general EXCEPTION to the `internal/` rule: a
pattern-agnostic concept referenced by a PUBLIC, user-implementable
`stats.Observer` extension is relocated to the package OWNING that
interface, not to `internal/`.

## The naming collision: `rest.Middleware[In,Out]` vs. the legacy shared `Middleware{Name,Security}`

`api/rest` (and `api/events`/`api/reqreply`) already export
`Middleware[In, Out]` as the pattern's OWN codec-backed middleware type
(merge-field vocabulary — `WithRequestHeader`, etc.). The LEGACY,
cross-pattern-shared `middleware.Middleware{Name, Security}` (the one
type whose entire purpose is being attachable IDENTICALLY across
`rest`/`events`/`reqreply`'s own `.Use()`) cannot be aliased under the
same name `Middleware` in the same package — confirmed the collision
exists in all 3 patterns, not just `rest`.

**Resolution (at the time)**: the legacy type was exposed under the new
name `SharedMiddleware` in each pattern package —
`type SharedMiddleware = internalmiddleware.Middleware` — IDENTICAL
across all 3 (a type alias is still ONE type at the compiler level, so a
`SharedMiddleware` value built via any one pattern's constructor remained
attachable to any of the 3 patterns' `.Use()`, preserving the
cross-pattern-shareable property that is this type's entire reason to
exist). `FromSecurityScheme`'s return type was updated from
`middleware.Middleware` to the local `SharedMiddleware` alias (pure
rename, zero behavior change).

> **UPDATE — `SharedMiddleware`/`FromSecurityScheme` were SUBSEQUENTLY
> REMOVED entirely** (not merely relocated) in a later round of this same
> session — see the "Addendum — `SharedMiddleware`/`FromSecurityScheme`
> retired entirely" section below for the full story. The code example
> immediately following this paragraph is now HISTORICAL — it shows the
> shape that existed between this resolution and that later removal, not
> the current shipped API.

## Shipped per-pattern wrapper shape

Each pattern package gained two new files:

- `security_scheme.go` (route-sourced): `SecurityRequirement`/
  `OAuthFlow`/`OAuthFlows`/`SecuritySchemeType` type aliases + constants;
  `BearerScheme`/`BasicScheme`/`APIKeyScheme`/`OAuth2Scheme`/
  `OpenIDConnectScheme`/`Require` constructor functions wrapping
  `internal/route`'s equivalents and returning the pattern's own
  composite `SecurityScheme` type (which embeds the bare
  `internal/route.SecurityScheme` + a `Codec` field).
- `middleware_vocabulary.go` (middleware-sourced; originally named
  `shared_middleware.go` at ship time — renamed once `SharedMiddleware`
  itself was removed, see the Addendum below): `SecurityDeclaration`/
  `Declaration[In,Out]`/`ContextField[V]`/`ContextFieldSetter`/
  `RouteMiddleware`/`ServerImplementation`/`ClientImplementation` type
  aliases; `NewSecurityDeclaration`/`NewDeclaration`/`NewContextField`/
  `CheckScopes` forwarding constructors.

A caller who previously wrote:

```go
import (
    "github.com/DaniDeer/go-codex/middleware"
    "github.com/DaniDeer/go-codex/route"
)

scheme := rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}
mw := middleware.Middleware{Name: "bearer-auth", Security: &middleware.SecurityDeclaration{...}}
```

now writes (current, post-Addendum shape — see below; a `SharedMiddleware`
literal existed only transiently between this round and the next):

```go
import "github.com/DaniDeer/go-codex/api/rest"

scheme := rest.BearerScheme("JWT")
mw := rest.SecurityMiddleware[struct{}, struct{}]("bearerAuth", scheme, []string{"write:users"})
```

— one import instead of three, and the bare `internal/route.SecurityScheme`
type (needed only when a caller explicitly wants the SAME scheme shared
across 2 patterns, e.g. `examples/reqreply-api/auth/middleware.go`'s
`oauthComputeScheme`) is reached via `.SecurityScheme` field access on the
composite value returned by `BearerScheme`/etc. — Go's embedding makes
this a zero-friction one-liner, no separate named alias needed for a type
that is never spelled standalone in real usage.

## Migration reality (what actually moved)

- `router/` → `internal/router/` (zero external usage found — free move).
- `route/` → `internal/route/`: ~102 in-module consumer files repointed
  (mechanical import-path change), 21 example files migrated to
  per-pattern constructors.
- `middleware/` → `internal/middleware/`: ~87 in-module consumer files
  repointed, 14 example files migrated. `Disposition` family relocated to
  `stats` first (8 consumer files updated).
- Full verification after every phase: `gofmt -l .` clean, `go build
  ./...`/`go vet ./...`/`go test ./...` clean, `staticcheck ./...` clean,
  `gosec ./...` shows only PRE-EXISTING findings in untouched files, every
  example under `examples/*/` runs to exit 0.

## Standing design rule for future shared mechanics

Codified in `.github/instructions/go-codex.instructions.md`'s Design
Philosophy section, `docs/concepts/ports-and-adapters.md`'s new
"Guardrail: shared cross-pattern MECHANICS belong in `internal/`, not a
public package" section, the `plan-a-new-codex-feature` and
`add-a-new-adapter` skills' Gotchas/Step 5g, and
`review-go-codex`'s checklist §16:

> A mechanism genuinely shared across 2+ of `api/rest`/`api/events`/
> `api/reqreply` (or a future `ports` pattern) that is pure MECHANICS —
> not a single adapter's protocol IO — belongs in a repo-root
> `internal/<name>` package, never a new public top-level one, with each
> consuming pattern package exposing its own thin, same-named public
> wrapper around it. The one exception: a pattern-agnostic concept
> referenced by a PUBLIC, user-implementable `stats.Observer` extension
> is relocated to the package OWNING that interface instead, since an
> external implementer must be able to spell the type in their own
> method signature.

## Addendum — `SharedMiddleware`/`FromSecurityScheme` retired entirely

A follow-up round, later in the same session, asked a simple question
that exposed this: is `SharedMiddleware` actually used anywhere for its
ONE stated purpose — a literal Go value attached across 2+ different
patterns? A from-scratch, grep-verified investigation found the answer
was NO:

- Every real `SharedMiddleware` construction in the example suite
  (`rest-api`/`events-api`/`reqreply-api`'s `demo_router_groups.go`) was
  confined to ONE pattern each, and most were NAME-ONLY placeholders (no
  `Security` field at all) — demonstrating `Router.Use()`/`.With()`'s
  middleware-name bookkeeping, not security sharing.
- `FromSecurityScheme` was called ONLY from `adapters/mqtt5` test files,
  in a variable literally named `legacyMw`.
- The ONE narrow, genuine "declare-only, no enforcement" use case found
  (`examples/go-edge-models`'s OCI registry client, documenting an
  EXTERNAL system's auth requirement this codebase never enforces) turned
  out to have ALREADY migrated its real code onto the FUSED,
  codec-backed `rest.SecurityMiddleware` constructor (used `.Use()`-only,
  no `Fn` — which already covers this case perfectly). Only STALE COMMENT
  PROSE in that file still mentioned `rest.SharedMiddleware` — artifacts
  of this round's own earlier mechanical rename, not real code.
- The genuine "same scheme, 2 patterns" precedent already in the codebase
  (`examples/reqreply-api/auth/middleware.go`'s `NewOAuthMwReqreply`/
  `OAuthMwREST`) does NOT use `SharedMiddleware` at all — it shares only
  the underlying SCHEME CONFIG (a plain `SecurityScheme` + codec value),
  declared TWICE through each pattern's own fused `SecurityMiddleware`/
  `BoundSecurityMiddleware` constructor. "Shared config, declared twice"
  was already the established, tested, working pattern for genuine
  cross-pattern reuse — `SharedMiddleware`'s "one shared value" promise
  was solving a problem nothing actually had.

There was also a structural finding separate from usage: Go generics make
the fused, Fn-bundling `Middleware[In,Out]` mechanism and "one literal Go
value shared across patterns" fundamentally INCOMPATIBLE —
`Middleware[In,Out].WithReceive`/`WithSend` are methods defined
separately per pattern package, so even `rest.Middleware[struct{},
struct{}]` and `events.Middleware[struct{}, struct{}]` are different Go
types. Only a Fn-LESS, declare-only value can be the literal same type
across all 3 patterns — which is exactly (and only) what
`SharedMiddleware` was.

**What was removed**: `rest.SharedMiddleware`/`events.SharedMiddleware`/
`reqreply.SharedMiddleware` (the per-pattern public aliases) and
`rest.FromSecurityScheme`/`events.FromSecurityScheme` (reqreply never had
one). Also removed as a direct consequence: the now-provably-dead
`securityDeclarationOf`/Security-detecting branch inside each pattern's
`buildServerImplementation` (unreachable once `SharedMiddleware` could no
longer be constructed by an external user — the EARLIER
`routeMiddlewareContributor`/misattachment check already intercepts the
only remaining type, `Middleware[In,Out]`, that could ever carry
Security), and the `LegacySecurityHandleMWRemovedError`/
`LegacySecurityClientMWRemovedError` (rest) /
`LegacySecurityMWRemovedError` (events, reqreply) defensive rejection
errors and their RouteOpt wrappers — relying on the `internal/` import
boundary (a compiler-enforced fact) rather than a runtime check to make
the rejected scenario structurally unreachable for external users.

**What was KEPT, unchanged**: `SecurityDeclaration`/
`NewSecurityDeclaration` (the fused mechanism's `Declaration[In,Out]
.Security` field uses these directly — genuinely shared, not
legacy-only), `SecurityCarrier`, `ServerImplementation`/
`ClientImplementation` (still real, active, general-purpose middleware —
e.g. `HandleMW(nil, observabilityFn)`), `CheckScopes`/`CheckCoverage`
(used by every adapter's active dispatch loop), and — most importantly —
the underlying `internal/middleware.Middleware{Name, Security}` TYPE
itself, completely unchanged, as the internal bridging/synthesis currency
every pattern's own `.Use()` path still constructs internally from a
codec-backed `Middleware[In,Out]`'s Security declaration. Only the
PUBLIC door letting an external user construct one directly was closed.

Each pattern's `shared_middleware.go` was renamed to
`middleware_vocabulary.go` to match its post-removal contents (it no
longer defines anything named `SharedMiddleware`).

This is now the standing, narrower lesson for the general design rule
this doc establishes: when a shared mechanic's value proposition is
"lets one value be used identically across patterns," verify that
claim against REAL usage before shipping the public door for it — a
structurally-possible-but-unexercised convenience is a cost (one more
public symbol, one more thing to keep compiling) without a matching
benefit. See `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s
Addendum 9 and `docs/design/d-0003-codec-declared-middlewares.md`'s
Addendum 8 update for the parallel record in those design docs.

## Addendum — `shared-api-layer-mechanics.md` retired; full phase-by-phase record folded in here

`docs/roadmap/shared-api-layer-mechanics.md` was the roadmap doc that originally identified and
phased this whole consolidation effort — Router (Phase 1), Bound Middleware (Phase 2),
Declaration/Dispatch core (Phase 3), Error-Pattern client helpers (Phase 4), and
Security-Credential Validation (Phase 5, relocated to `docs/roadmap/amqp-adapter.md` before this
retirement). By the time this Addendum was written, every phase had been resolved (shipped,
rejected, or relocated) with nothing left open — the roadmap doc was retired (deleted) and its
full implementation record folded into this one place, since this doc already serves as the
living record of the `internal/`-mechanics effort those phases fed into. Every Go doc-comment
across `api/rest`/`api/events`/`api/reqreply`/`internal/router`/`internal/middleware` that used
to cite `docs/roadmap/shared-api-layer-mechanics.md`'s own Phase N now cites this Addendum
instead.

**Phase 1 — Router.** Fully covered by
[D-0008 — Declarative Router Groups](d-0008-declarative-router-groups.md)'s own "Addendum —
consolidated onto the shared `router` package" section — not duplicated here. Summary: the
~1800 combined lines of hand-copied `Router`/`Mount`/`Group`/`Walk` state-machine code across
`api/rest`/`api/events`/`api/reqreply` were consolidated into one generic `router.Router[Target]`
(now `internal/router`), with each pattern's own `Router` becoming a thin wrapper around it —
zero behavior change, every pre-consolidation test passed unchanged.

**Phase 2 — Bound Middleware.** `internal/middleware/bound.go` ships with
`BoundContributor[Req]`/`BoundClientContributor[Req]`/`BoundNamed`/`BoundNameOf`/
`BoundRouteBuilder` — NOT `BoundCore` (the original sketch's proposed shared field-holding
struct), which was found unbuildable once implementation started: each pattern's real
`BoundMiddleware[Req,In,Out]`/`BoundClientMiddleware[Req,In,Out]` holds a field of type
`Middleware[In,Out]` (that PATTERN's OWN codec-backed middleware type, with its own merge-field
vocabulary), not a bare, lower-level `Declaration[In,Out]` the original sketch assumed —
replacing that field would have silently broken every merge-field `With*` method. `BoundCore`
was dropped entirely; the consolidation's real value came entirely from the narrow
`BoundContributor`/`BoundClientContributor`/`BoundRouteBuilder` interfaces instead, which don't
care what a concrete type's own fields look like, only its method set. All 3 api packages'
`applyBoundRoute`/`applyBoundSubscriber`/`applyBoundPublisher`/`applyBoundClientRoute` were
renamed to the uniform `ApplyBoundRoute`/`ApplyBoundClientRoute` and now call through
`middleware.BoundRouteBuilder` instead of touching `routeBuilder`/`Subscriber[T]`/`Publisher[T]`
fields directly. A real, confirmed bug was found and fixed as a direct result of the
investigation that preceded this implementation: `api/events`'s `BoundSubscribeMiddleware`/
`BoundPublishMiddleware` were missing the Security-declaration step `api/rest`/`api/reqreply`'s
equivalents both have, meaning a `SubscribeBoundMW`/`PublishBoundMW`-only attachment (no
companion `.Use()` call) left the channel's published AsyncAPI spec silently omitting a security
requirement the dispatch logic was actually enforcing — confirmed via a live repro contrasting
all 3 packages before fixing (see `api/events/bound_middleware.go`'s `ApplyBoundRoute` and its
regression tests in `api/events/builder_test.go`). Full verification battery passed clean.

**Phase 3 — Declaration/Dispatch core: REJECTED, not deferred.** The original roadmap scoped this
as the LARGEST, most invasive phase — consolidating `Middleware[In,Out]`/`NewMiddleware`/
`MiddlewareHandler`/`ClientMiddlewareHandler` and their dispatch functions (~2000 combined
lines, the single most heavily-used type across all 3 packages' public APIs) — and deliberately
deferred detailed design until Phases 1+2 shipped and proved the pattern twice over. With Phases
1/2/4 now all shipped, a final review resolved this phase by REJECTING it outright rather than
proceeding to its own design spike: direct confirmation showed REST's merge-field `DecodeIn`/
`EncodeOut` shapes are `func(ctx, headerVars, cookieVars, queryVars map[string]string) (any,
error)` / `(headers, cookies map[string]string, err error)` (3-in/2-out), while events' are
`func(ctx, topicVars, propertyVars map[string]string) (any, error)` / `(topicVars, propertyVars
map[string]string, err error)` (2-in/2-out) — genuinely different arity, not interchangeable
without inventing a new, more abstract signature shape (e.g. a variadic or map-of-maps
convention) purely to force a shared core into existence. This is precisely the kind of
pattern-SPECIFIC concern this project's own design philosophy says belongs in each api/*
package, not centralized into `internal/` for its own sake — see this doc's own closing
"standing design rule" section above: `internal/` is for mechanics that are ALREADY the same
shape across patterns, not a mandate to force every mechanism into one shape regardless of real
differences. `Middleware[In,Out]`'s own merge-field vocabulary (REST's header/cookie/query vs.
events'/reqreply's topic/property) remains, permanently, each pattern's own concern.

**Phase 4 — Error-Pattern Client Helpers.** `internal/middleware/error_pattern.go` ships with
`ErrorPatternValuer`/`ErrorPatternAs[B]`/`ErrorCase`/`Case[T]`/`HandleErrorPattern`, ported
verbatim from byte-identical `api/rest`/`api/reqreply` copies, with ONE deliberate export
rename (`errorCase` → `ErrorCase`, needed since `Case[T]`'s return type must be nameable for
each pattern's own type-alias wrapper — a pure visibility change, zero behavior difference).
`api/rest/error_pattern_client.go`/`api/reqreply/error_pattern_client.go` are now thin
type-alias/forwarding wrappers (same pattern as `security_scheme.go`/`middleware_vocabulary.go`).
`api/events` correctly has NO equivalent — pub/sub's `Publish` is a ONE-WAY, fire-and-forget
operation with no reply channel to decode a typed payload from, a structural non-issue, not a
gap. A new cross-package test (`api/rest/error_pattern_crosspattern_test.go`) proves the
structural-interchangeability claim LIVE, not just by reading the code: a value is built once,
matched via `reqreply.ErrorPatternAs` AND `rest.ErrorPatternAs`, and dispatched via both
patterns' `HandleErrorPattern`/`Case`. Full verification battery passed clean.

**Phase 5 — Security-Credential Validation.** Relocated to `docs/roadmap/amqp-adapter.md`'s own
"Credential-format validation" section as part of that adapter's security design, sequenced
against AMQP (a real second transport data point) rather than generalized from `mqtt5` alone —
see that doc for the current design (updated for this doc's own `internal/middleware` +
per-pattern-wrapper convention after this retirement).

## Related documents

- [D-0008 — Declarative Router Groups](d-0008-declarative-router-groups.md)'s
  Addendum — the original per-package Router design, consolidated onto
  `router`, now `internal/router`.
- [D-0003 — Codec-Declared Middlewares](d-0003-codec-declared-middlewares.md)'s
  Addendum 8 and [D-0007 — Declarative Middleware Layering](d-0007-declarative-middleware-layering.md)'s
  Addendum — the `middleware` package's design history, now relocated.
- `docs/roadmap/amqp-adapter.md`'s "Credential-format validation" section — Phase 5's current
  home (see the Addendum above).
