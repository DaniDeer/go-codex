# Declarative Middleware for `ports`

> **Status:** Design draft — not yet implemented.
>
> SLICED from the now-superseded `mcp-ports-declarative-middleware.md`, which combined this
> `ports`-only content with `api/mcp`'s own Declarative Middleware design. The two boundaries
> don't belong in one file — `ports` (`File`/`Cache`/`Dir`) and `api/mcp` (`Tool`/`Resource`/
> `Prompt`) are independent concerns that only shared a doc because both were originally
> spun out of the SAME now-deleted predecessor, `declarative-middleware.md`. The `api/mcp`
> content now lives in [`mcp-tool-declaration-workflow.md`](mcp-tool-declaration-workflow.md)'s
> own Section A.
>
> This doc's content below is carried forward VERBATIM from the superseded doc (no new
> investigation this round) except where explicitly noted.
>
> [← Back to Roadmap](index.md)

## Motivation

`api/rest`, `api/events`, and `api/reqreply` all converged on the SAME cross-cutting-concern
mechanism: a codec-typed `Middleware[In,Out]` (wrapping the shared
`middleware.Declaration[In,Out]`), declared once at builder time (`.Use(mw)`, spec-
contributing, coverage-checked) and dispatched at register/attach time (`HandleMW`/
`ClientMW`/`Transform`/`ClientTransform`). This is now go-codex's proven, singular answer to
"how does a caller attach security/observability/enrichment to a boundary" — three
independent implementations, zero divergence.

`ports.File`/`Cache`/`SQL`/`Dir` have NO cross-cutting-concern hook of ANY kind, not even
observability-via-options. This doc designs it, explicitly reusing the reference pattern's
actual shape rather than inventing a bespoke mechanism.

## Core finding: `ports.File`/`Cache`/`Dir` already have the genuine two-phase shape the reference pattern needs

Confirmed via [D-0003](../design/d-0003-codec-declared-middlewares.md)'s own "Feasibility of
a full `.Use()`-based `ports.Middleware[In,Out]`" section (written alongside D-0003's
REST/events design, never implemented): `ports.File`/`Cache`/`Dir` (`NewFile(...)`/
`NewCache(...)`) are ALREADY declare-once values, with the prerequisite generic param
constructors already existing (`ports.NewCacheKeyParam[T,V]`/`ports.NewFilePathParam[T,V]`,
mirroring `rest.NewRequiredHeaderParam[T,V]` exactly). This doc turns that prior analysis
into a concrete design.

**What's genuinely different from REST/events, confirmed and accepted, not worked around:**
- `ports.File`/`Cache`/`Dir`'s spec surface has no `Security`/middleware-metadata FIELD to
  populate (there is no spec at all for `File`/`Cache`/`Dir`, unlike MCP's `ToolSpec`) — so
  there is no `CheckCoverage`-equivalent drift check, and a declared-but-unenforced
  requirement cannot occur structurally (declare `.Use()` and the operation it gates are
  always the SAME call chain, never two independently-reachable phases that could drift
  apart the way a REST route's `.Use()` (builder time) and `HandleMW` (adapter Register
  time) can).
- `ports.File`/`Cache`/`Dir`'s `Read`/`Write`/`Get`/`Set`/`List` have **no `ctx
  context.Context` parameter today** (confirmed via code — only an optional `opts.Context`
  used solely for observer lookup) — a real, additive signature change this doc scopes
  explicitly (see "Signature changes required" below), not a free lunch.
- `ports.SQL` is confirmed **metadata-only** (`SQLPattern`) — there is no `ports.SQL[T]`
  handle/builder value at all (SQL query text and placeholders are driver-specific closures
  owned directly by `adapters/sql`'s constructors). Scoped OUT of this doc's Phase 1 as a
  genuine limit case (see "ports.SQL" below), not silently dropped.
- `ports.Dir` has no user-typed `T` to merge fields into (`List` returns fixed `[]DirEntry`
  values) — gets the AGNOSTIC-only path (no merge-field method), a structural
  non-applicability, not a bug.

## Scope decisions

| In scope (this doc, Phase 1 design) | Out of scope |
|---|---|
| `ports.File`/`Cache`/`Dir` gain `.Use(mws ...middleware.RouteMiddleware)` at declare time + dispatch inside `Read`/`Write`/`Get`/`Set`/`Del`/`List` | `ports.SQL` (metadata-only, no handle to attach `.Use()` to — needs its own future design or an accepted permanent exception, see below) |
| `ports`' `ctx context.Context` signature addition to `Read`/`Write`/`Get`/`Set`/`Del`/`List` (additive, required for middleware `Fn`s needing real ctx — timeouts, `ContextField` access) | Breaking/removing the existing no-ctx call shape — additive variants only, mirroring how every other boundary's migrations in this codebase stayed additive-first |
| `ports.RequireScopes[T]`-style Security (`File`/`Cache`/`Dir`), reusing the ALREADY-SHARED `middleware.SecurityScheme`/`CheckScopes` | A new `ports`-specific security-declaration type — explicitly rejected, see "Security design" below |
| Observability middleware for `ports` (`Observability[T]` per port, fan-out-aware via `stats.NewFanout`, generalizing the same fix `api/mcp`'s own L12 needed) | `api/mcp`'s own Declarative Middleware — see the sibling [`mcp-tool-declaration-workflow.md`](mcp-tool-declaration-workflow.md) |
| Pattern-bound ports (`SourcePort`/`SinkPort`/`IOPort`/`ToolPort` with `RESTPattern`/`EventPattern`) — confirmed by D-0003 to ALREADY reach `.Use()` via the Pattern's own `Opts` field; this doc only needs to confirm/document that, no new mechanism | Re-designing Pattern-bound ports' middleware path — already solved |
| Layer 3 `forge.Registry`/pipelines | Tracked in [`forge-pipeline-middleware.md`](forge-pipeline-middleware.md), unaffected |

## Reference pattern being mirrored (confirmed identical across REST/events/reqreply)

- A per-boundary `Middleware[In,Out]` struct embedding `middleware.Declaration[In,Out]`
  (`Name`, `InCodec`, `OutCodec`).
- Boundary-specific merge-field methods (`With*`) where a decomposed field vocabulary
  genuinely exists, PLUS an agnostic path (`WithReceive`/`WithSend`) for when it doesn't.
- 5 identical error types: `MiddlewareInputError`, `MiddlewareError`, `MiddlewareOutputError`,
  `DuplicateMiddlewareNameError`, `AmbiguousMiddlewareAttachmentError`.
- `RouteMiddlewareMarker()` — the exported marker method making a value `.Use()`-able
  (`middleware.RouteMiddleware` interface).
- Security via the SHARED `middleware.SecurityScheme`/`SecurityDeclaration`/`CheckScopes`/
  `UnsatisfiedScopesError` — zero per-boundary reinvention today; this doc keeps that
  unbroken for `ports`.

## `ports` design — builds directly on D-0003's existing feasibility analysis, not re-derived

**Merge-field vocabulary — genuinely available for `File`/`Cache`, NOT `Dir`:**

```go
// package ports
type FileMiddleware[In, Out any] struct {
    middleware.Declaration[In, Out]
    pathVar func(in In) codex.FieldCodec[In] // WithPathVar — reuses File.MergeFields()'s own FieldCodec shape
    receive func(ctx context.Context, in In) (Out, error)
    send    func(ctx context.Context) (In, error)
}
func (m FileMiddleware[In, Out]) WithPathVar(p MergedFilePathParam[In]) FileMiddleware[In, Out] { /* mirrors rest.Middleware.WithRequestHeader */ }
func (m FileMiddleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) FileMiddleware[In, Out] { /* ... */ }
func (m FileMiddleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) FileMiddleware[In, Out] { /* ... */ }
func (FileMiddleware[In, Out]) RouteMiddlewareMarker() {}
```

`CacheMiddleware[In,Out]` is the MECHANICALLY IDENTICAL sibling, `WithCacheKey` in place of
`WithPathVar` (reusing `ports.NewCacheKeyParam[T,V]`). `DirMiddleware[In,Out]` gets ONLY the
agnostic path (`WithReceive`/`WithSend`) — `Dir.List` returns fixed `[]DirEntry` values,
confirmed no user-typed `T` exists to merge a path var into.

**Declare-time attachment, dispatch at the real operation:**

```go
// File[T] gains .Use, mirroring Tool[In,Out]/rest.Route's declare-time shape. No spec
// exists for File/Cache/Dir at all, so there is doubly no coverage-check concern here.
func (fh File[T]) Use(mws ...middleware.RouteMiddleware) File[T] {
    fh.middlewareHandlers = append(fh.middlewareHandlers, buildFileMiddlewareHandlers[T](mws)...)
    return fh
}
```

**Signature changes required (additive, confirmed real cost by D-0003, not free)**:
`Read`/`Write`/`Get`/`Set`/`Del`/`List` gain a leading `ctx context.Context` parameter —
needed for any attached middleware `Fn` requiring real cancellation/timeout or
`middleware.ContextField` access (an HTTP-call-backed credential check, for instance).
Existing no-ctx call sites keep compiling via an additive variant (mirrors how this codebase
has handled every prior ctx-introduction: additive first, never a silent breaking swap) —
exact naming (`ReadCtx` vs. overloading `Read` with a ctx-free compatibility shim) is an
open design decision, see below.

> **Cross-cutting note (added when this doc was sliced out of its predecessor)**: `api/file`
> (this repo's NEWER, canonical declarative file layer — see
> [D-0011](../design/d-0011-file-api-backend-transparency.md)) has shipped its OWN,
> independent Declarative Middleware mechanism (`Bound<Role>Middleware[T,In,Out]` per role)
> SINCE this `ports`-focused design was originally drafted — `ports.File`/`Dir` are now thin
> wrappers delegating to `api/file.Client` internally (Stage F of D-0011). Before
> implementing THIS doc's `ports`-level `FileMiddleware`/`.Use()` design, re-confirm it isn't
> now REDUNDANT with `api/file`'s own, already-shipped `BoundCreateMiddleware`/
> `BoundReadMiddleware`/etc. — a `ports.File` caller might already be able to reach
> equivalent functionality by using `api/file.Client` directly instead of `ports.File`'s own
> thinner wrapper. This re-confirmation is NOT done in this round — flagged as a
> PREREQUISITE check for whoever picks up this doc's implementation next, since the
> underlying architecture shifted significantly after this design was first drafted.

## Security design — reuses `middleware.SecurityScheme`/`CheckScopes` directly, no new type

**Cross-reference**: [D-0003's Addendum 3](../design/d-0003-codec-declared-middlewares.md)
separately evaluates (and resolves) whether Security should ever fold into the codec-backed
`Declaration[In,Out]` family for REST/events/reqreply — this design's choice below (reuse
the shared mechanism directly, don't fold) is independently-derived evidence consistent with
that resolution (Security permanently stays on its own dedicated type), not a presupposed
answer for it.

`ports.RequireScopes[T]`-style security middleware is built the SAME way REST/events/reqreply
already build theirs — via the EXISTING, shared `middleware.SecurityScheme(schemeName,
scheme, scopes, codec)` constructor and `middleware.CheckScopes(reqs, granted)` check, NOT a
new `ports`-specific security-declaration type:

```go
// ports — EXTRACTION-ONLY Fn (no pass/fail decision inside Fn itself); File.Read/Write
// collects grants from every attached security-shaped Fn in a first pass, merges, runs ONE
// middleware.CheckScopes, THEN calls the real operation — avoiding the AND-requirement bug
// REST's own history hit by keeping authentication (extraction) and authorization (the one
// final check) separate, even when MULTIPLE security-shaped Fns are attached.
func RequireScopes[T any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, vars map[string]string) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:     "require-scopes:" + schemeName,
        Security: &middleware.SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn:       extract,
    }
}
```

No coverage-drift check exists here (no spec to check against — see "Core finding" above).

## `ports.SQL` — confirmed out of scope, permanent limit case (not silently dropped)

Confirmed via code: `ports.SQLPattern` is metadata-only (`ports/sql_meta.go`) — there is no
`ports.SQL[T]` handle/builder value, query text and placeholders are driver-specific closures
owned directly by `adapters/sql`'s constructors (`QueryAdapter`/`DrainInsertAdapter`/
`QueryEachAdapter`). There is no declare-once value to attach `.Use()` to under the CURRENT
architecture. Two honest paths forward, neither designed here:
1. A future `adapters/sql`-local decorator wrapping `QueryAdapter`/etc. directly at the
   adapter-constructor call site (NOT a `ports.SQL[T]` type, since none exists) — a
   genuinely different attachment point than every other boundary in this doc.
2. Accept this as a PERMANENT exception, same spirit as `SQLPattern` already being a
   deliberately different, metadata-only shape vs. `FilePattern`/`EventPattern`/`RESTPattern`.

## Known limitations (carried forward, L-numbers preserved verbatim)

### L11 — Unreconciled overlap with `dynamic-port-rebinding.md`

**Status:** RESOLVED. `ports`' credential-rollover story is solved by per-call-reachable
`.Use()`-attached security middleware (swappable by rebuilding the `File[T]`/`Cache[T]`
value with different `.Use()` args between calls, zero new mechanism) — `dynamic-port-
rebinding.md`'s `Rebind` remains necessary ONLY for swapping the underlying TRANSPORT
ADAPTER itself, a different concern. Both docs cross-reference each other. REST/events/
reqreply's immutable `RouteHandle`/`ChannelHandle` middleware hot-swap remains an
acknowledged, unaddressed gap in both docs.

### L14 — forge/Registry (Layer 3 pipelines) — out of scope, tracked separately

**Status:** RESOLVED — tracked entirely in [`forge-pipeline-middleware.md`](forge-pipeline-middleware.md),
no sequencing dependency either way.

## Structured errors (all implement `slog.LogValuer`)

Reuse the 5 existing error types VERBATIM, scoped to `ports` — no new error taxonomy:
- `ports.MiddlewareInputError`/`MiddlewareError`/`MiddlewareOutputError`/
  `DuplicateMiddlewareNameError`/`AmbiguousMiddlewareAttachmentError` (mirrors
  `rest`/`events`/`reqreply`'s own byte-for-byte).
- `middleware.MiddlewareShapeError`/`UnsatisfiedScopesError` — reused directly from the
  shared `middleware` package, zero duplication.

## Observer integration

`ports`: `stats.FileObserver`/`CacheObserver`/`SQLObserver` via `.Use(ports
.Observability[T](obs))` per port, fan-out-aware resolution (same pattern as `api/mcp`'s own
L12 fix, generalized here). Type-assertion guard pattern, `nil`-Observer-safe, matching every
other boundary in this codebase.

## Unit test plan

| Test | Boundary | Verifies |
|---|---|---|
| T4 | `ports.File` | `.Use()` + `Read`/`Write` dispatch, happy path, `WithPathVar` merge |
| T5 | `ports.File` | `RequireScopes` — multiple security `Fn`s merge grants before ONE `CheckScopes` call (no AND-requirement bug) |
| T6 | `ports.Cache`/`Dir` | Mechanical extension parity with `File` |
| T7 | all | `nil`/plain Observer → no panic, graceful fallback |
| T8 | all | `errors.As` chain + `LogValue()` shape for all 5 error types |
| T9 | `ports` | ctx-threading: a `ContextField`-based middleware round-trips correctly through the new `ctx` parameter |

## Files to create

| File | Responsibility |
|---|---|
| `ports/middleware_declaration.go` | `FileMiddleware`/`CacheMiddleware`/`DirMiddleware[In,Out]`, `RequireScopes[T]`, 5 error types |
| `ports/file.go`/`cache.go`/`dir.go` (extend) | `.Use()`, ctx-threaded `Read`/`Write`/`Get`/`Set`/`Del`/`List` variants |

## Out of scope (Phase 2+)

- `ports.SQL` (see dedicated section above — permanent limit case or a future, differently-
  shaped design).
- Any new `stats` type — `stats.NewFanout` already covers the fan-out need completely.
- Pattern-bound ports' OWN middleware path — already solved via the Pattern's `Opts` field
  (D-0003's finding), nothing to redesign.
- Resolving whether `ports` should EVER adopt `middleware.ContextField` — mechanism already
  works uniformly if/when a driver appears; no driver confirmed yet.
- `api/mcp`'s own Declarative Middleware — see the sibling
  [`mcp-tool-declaration-workflow.md`](mcp-tool-declaration-workflow.md).

## Open design decisions (to resolve before/during implementation)

1. **Exact ctx-introduction shape** — new `ReadCtx`-style additive methods, or overload
   resolution via a variadic trailing `ctx` argument, or accept a one-time breaking
   signature change (this codebase's own precedent leans additive-first, but `ports.File`'s
   call sites are comparatively few — worth re-measuring the actual blast radius before
   deciding).
2. **`ports.SQL`'s eventual design** — deferred entirely, no driver yet (mirrors
   `forge-pipeline-middleware.md`'s own "idea only" status for a structurally similar
   reason: a real architectural gap with no concrete, demanded use case forcing a decision
   yet).
3. **(NEW, added when sliced out) Redundancy check against `api/file`'s own shipped
   Declarative Middleware** — see the cross-cutting note under "`ports` design" above; must
   be resolved BEFORE implementation starts, not deferred further once picked up.
