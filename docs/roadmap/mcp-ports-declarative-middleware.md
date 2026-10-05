# Declarative Middleware for MCP and `ports` — `api/mcp`, `adapters/mcpgo`, `ports`

> **Status:** Design draft — not yet implemented. Spun out of
> `docs/roadmap/declarative-middleware.md` (now DELETED, no remaining link —
> its REST/events/reqreply content was fully superseded by
> [D-0001](../design/d-0001-rest-middleware-workflow-simplification.md)/
> [D-0002](../design/d-0002-pubsub-workflow-simplification.md)/
> [D-0003](../design/d-0003-codec-declared-middlewares.md)/
> [D-0004](../design/d-0004-reqreply-workflow-simplification.md); its
> remaining MCP/`ports` scope moved here wholesale, including the L5/
> L11/L12/L14 "Known limitations" findings, their numbering preserved
> verbatim).
>
> **This doc REDESIGNS that moved content**, not just relocates it — the
> original sketch predated
> [D-0003](../design/d-0003-codec-declared-middlewares.md)'s shipped
> `Declaration[In,Out]`/`Middleware[In,Out]`/two-phase declare-then-
> dispatch pattern and proposed a call-time-only decorator shape
> instead. This round re-reads the ACTUAL shipped `api/rest`/`api/events`/
> `api/reqreply` mechanism plus D-0003's OWN already-existing ports
> feasibility analysis (its "Feasibility of a full `.Use()`-based
> `ports.Middleware[In,Out]`" section) and aligns MCP/`ports` with it,
> rather than carrying the older, now-superseded sketch forward
> unchanged.
> [← Back to Roadmap](index.md)
>
> **See also**: [Declarative Middleware as Partial Route/Channel
> Definitions](../design/d-0007-declarative-middleware-layering.md) — proposes a shared
> `middleware.DecodeLayer`/`EncodeLayer` mechanism for
> `api/rest`/`api/events`/`api/reqreply`'s EXISTING two-phase
> declare-dispatch `Middleware[In,Out]` shape. Independent of this doc:
> MCP/`ports` use the single-phase attachment model described below, not
> that shape, so the two designs don't compete or overlap.

## Motivation

`api/rest`, `api/events`, and `api/reqreply` all converged on the SAME
cross-cutting-concern mechanism: a codec-typed `Middleware[In,Out]`
(wrapping the shared `middleware.Declaration[In,Out]`), declared once at
builder time (`.Use(mw)`, spec-contributing, coverage-checked) and
dispatched at register/attach time (`HandleMW`/`ClientMW`/`Transform`/
`ClientTransform`). This is now go-codex's proven, singular answer to
"how does a caller attach security/observability/enrichment to a
boundary" — three independent implementations, zero divergence.

`api/mcp`/`adapters/mcpgo` and `ports` (`File`/`Cache`/`SQL`/`Dir`) are
the two remaining Layer 2 boundaries with NO such mechanism at all —
MCP has zero attachment point beyond an `Options.Observer` field;
`ports.File`/`Cache`/`SQL`/`Dir` have NO cross-cutting-concern hook of
ANY kind, not even observability-via-options. This doc designs both,
explicitly reusing the reference pattern's actual shape rather than
inventing a fourth, bespoke mechanism.

## Core finding this round: both boundaries have a genuine two-phase shape, just like the reference

A correction to the superseded doc's blanket claim ("decorator-shaped,
one attachment point, no spec to feed") — confirmed via fresh code
reading, BOTH boundaries have a real declare-once value a `.Use()`-style
method can attach to, mirroring `rest.NewRoute(...).Use(mw)`/
`events.NewChannel(...).Use(mw)` structurally:

- **`ports.File`/`Cache`/`Dir`** (`NewFile(...)`/`NewCache(...)`) are
  ALREADY declare-once values, confirmed in
  [D-0003](../design/d-0003-codec-declared-middlewares.md)'s own
  "Feasibility of a full `.Use()`-based `ports.Middleware[In,Out]`"
  section (written alongside D-0003's REST/events design, never
  implemented) — see "`ports` design" below, which builds directly on
  that analysis rather than re-deriving it.
- **`api/mcp.Tool[In,Out]`** (`NewTool(...)` + `.Register(b)` →
  `*ToolHandle[In,Out]`) is STRUCTURALLY IDENTICAL to
  `rest.NewRoute(...).Register(builder)`/`events.NewChannel(...)` — a
  genuine two-phase declare/dispatch split already exists (confirmed via
  `api/mcp/builder.go`), a NEW finding this round (D-0003 never analyzed
  MCP). `Resource[V,T]`/`Prompt` share the identical `NewX(...).Register(b)`
  shape.

**What's genuinely different from REST/events, confirmed and accepted,
not worked around:**
- Neither boundary's spec object (`MCPSpec`'s `ToolSpec`/`ResourceSpec`/
  `PromptSpec`, or `ports`' complete absence of a spec at all for
  `File`/`Cache`/`Dir`) has a `Security`/middleware-metadata FIELD to
  populate — so there is no `CheckCoverage`-equivalent drift check, and
  Security middleware can declare but can never be spec-rendered. This
  is why MCP Security stays permanently N/A (unchanged, confirmed
  out of scope by the MCP skill's own Gotchas) and why `ports`' Security
  (`RequireScopes`-style, in scope per this round) is enforced but never
  spec-checked for drift — both ACCEPTED, not bugs.
- `ports.File`/`Cache`/`Dir`'s `Read`/`Write`/`Get`/`Set`/`List` have
  **no `ctx context.Context` parameter today** (confirmed via code —
  only an optional `opts.Context` used solely for observer lookup) — a
  real, additive signature change this doc scopes explicitly (see
  "Signature changes required" below), not a free lunch.
- `ports.SQL` is confirmed **metadata-only** (`SQLPattern`) — there is
  no `ports.SQL[T]` handle/builder value at all (SQL query text and
  placeholders are driver-specific closures owned directly by
  `adapters/sql`'s constructors). Scoped OUT of this doc's Phase 1 as a
  genuine limit case (see "Out of scope" below), not silently dropped.
- `ports.Dir` has no user-typed `T` to merge fields into (`List` returns
  fixed `[]DirEntry` values) — like MCP, it gets the AGNOSTIC-only path
  (no merge-field method), not a bug, a structural non-applicability.

## Scope decisions

| In scope (this doc, Phase 1 design) | Out of scope |
|---|---|
| `api/mcp.Tool`/`Resource`/`Prompt` gain `.Use(mws ...middleware.RouteMiddleware)`; `adapters/mcpgo`'s `ToolHandler`/`ResourceHandler`/`PromptHandler` dispatch them | MCP Security (permanent N/A, unchanged — no host-auth-bypassing mechanism will ever be added) |
| `ports.File`/`Cache`/`Dir` gain `.Use(mws ...middleware.RouteMiddleware)` at declare time + dispatch inside `Read`/`Write`/`Get`/`Set`/`Del`/`List` | `ports.SQL` (metadata-only, no handle to attach `.Use()` to — needs its own future design or an accepted permanent exception, see below) |
| `ports`' `ctx context.Context` signature addition to `Read`/`Write`/`Get`/`Set`/`Del`/`List` (additive, required for middleware `Fn`s needing real ctx — timeouts, `ContextField` access) | Breaking/removing the existing no-ctx call shape — additive variants only, mirroring how every other boundary's migrations in this codebase stayed additive-first |
| `ports.RequireScopes[T]`-style Security (`File`/`Cache`/`Dir`), reusing the ALREADY-SHARED `middleware.SecurityScheme`/`CheckScopes` | A new `ports`-specific security-declaration type — explicitly rejected, see "Security design" below |
| Observability middleware for MCP (`mcpgo.Observability`, fan-out-aware per L12) and `ports` (`Observability[T]` per port, same fan-out fix generalized) | Layer 3 `forge.Registry`/pipelines — tracked in [`forge-pipeline-middleware.md`](forge-pipeline-middleware.md), unaffected |
| Pattern-bound ports (`SourcePort`/`SinkPort`/`IOPort`/`ToolPort` with `RESTPattern`/`EventPattern`) — confirmed by D-0003 to ALREADY reach `.Use()` via the Pattern's own `Opts` field; this doc only needs to confirm/document that, no new mechanism | Re-designing Pattern-bound ports' middleware path — already solved |

## Reference pattern being mirrored (confirmed identical across REST/events/reqreply)

- A per-boundary `Middleware[In,Out]` struct embedding
  `middleware.Declaration[In,Out]` (`Name`, `InCodec`, `OutCodec`).
- Boundary-specific merge-field methods (`With*`) where a decomposed
  field vocabulary genuinely exists (REST: header/cookie/query;
  events/reqreply: topic-var/property) PLUS an agnostic path
  (`WithReceive`/`WithSend`) for when it doesn't.
- 5 identical error types: `MiddlewareInputError`, `MiddlewareError`,
  `MiddlewareOutputError`, `DuplicateMiddlewareNameError`,
  `AmbiguousMiddlewareAttachmentError`.
- `RouteMiddlewareMarker()` — the exported marker method making a value
  `.Use()`-able (`middleware.RouteMiddleware` interface).
- Security via the SHARED `middleware.SecurityScheme`/
  `SecurityDeclaration`/`CheckScopes`/`UnsatisfiedScopesError` — zero
  per-boundary reinvention today; this doc keeps that unbroken for
  `ports`.

## `api/mcp` design — `ToolMiddleware[In,Out]` (and Resource/Prompt siblings)

**Merge-field vocabulary: NONE** — confirmed via `adapters/mcpgo.
ToolHandler`, MCP arguments are a single flat `map[string]any` decoded
directly into `In` via `handle.Decode(args)`; there is no separate
header/cookie/query-equivalent channel to merge additional fields from.
`ToolMiddleware[In,Out]` therefore gets ONLY the agnostic path:

```go
// package apimcp
type ToolMiddleware[In, Out any] struct {
    middleware.Declaration[In, Out]
    receive func(ctx context.Context, in In) (Out, error) // WithReceive
    send    func(ctx context.Context) (In, error)          // WithSend
}

func NewToolMiddleware[In, Out any](decl middleware.Declaration[In, Out]) ToolMiddleware[In, Out] {
    return ToolMiddleware[In, Out]{Declaration: decl}
}
func (m ToolMiddleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) ToolMiddleware[In, Out] {
    m.receive = fn
    return m
}
func (m ToolMiddleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) ToolMiddleware[In, Out] {
    m.send = fn
    return m
}
func (ToolMiddleware[In, Out]) RouteMiddlewareMarker() {}
```

**Forward-looking footnote (added during a later, unrelated design
review)**: `ToolMiddleware[In,Out]` (and `FileMiddleware`/
`CacheMiddleware`/`DirMiddleware[In,Out]` below) safely embed
`middleware.Declaration[In,Out]` ANONYMOUSLY here because there is no
second, "bound" role to accidentally conflict with — this doc's design
is agnostic-only by choice (see "Merge-field vocabulary: NONE," above).
IF a bound/paired variant is EVER added to any of these types in the
future (e.g. a `ToolMiddleware` needing access to the specific Tool's
own input struct, mirroring a route-bound need), it should NOT embed
`Middleware[In,Out]`-equivalent state anonymously — see
[`docs/design/d-0003-codec-declared-middlewares.md's Addendum 7`](../design/d-0003-codec-declared-middlewares.md)'s
resolved internal-layout decision: anonymous embedding promotes ALL of
the embedded type's methods, which would silently make the NEW bound
type ALSO satisfy whatever interface its agnostic sibling uses for
`.Use()` attachment — reintroducing the exact ambiguity bug that doc
exists to eliminate. Use a NAMED field instead.

**Attachment — mirrors `rest.Route.Use`/`events.Channel`'s declare-time
shape literally, not the superseded doc's call-time variadic sketch:**

```go
// Tool[In, Out] gains .Use, mirroring rest.Route.Use/events.Channel's
// chained-method shape (considered and preferred over passing
// middleware as another NewTool ToolOpt — keeps parity with the
// reference pattern's actual method name/shape, not just MCP's own
// existing ToolOpt-variadic idiom).
func (t Tool[In, Out]) Use(mws ...middleware.RouteMiddleware) Tool[In, Out] {
    t.tb.middlewareHandlers = append(t.tb.middlewareHandlers, buildToolMiddlewareHandlers(mws)...)
    return t
}
```

`Tool.Register(b)` resolves attached middleware into `ToolHandle`'s new
unexported `middlewareHandlers []middleware.MiddlewareHandler` field
(mirrors `RouteHandle.MiddlewareHandlers` exactly) — **no spec
contribution** (`ToolSpec`/`ResourceSpec`/`PromptSpec` gain no new
field; `MCPSpec()` output is unaffected), since there is no
OpenAPI/AsyncAPI-equivalent `securitySchemes` slot to populate and MCP
Security remains permanently N/A. `adapters/mcpgo.ToolHandler`/
`ResourceHandler`/`PromptHandler` dispatch `handle.middlewareHandlers`
at call time, the same pre-handler point security enforcement already
runs at on every other boundary.

**Observability — generalizes L5/L12's already-resolved design exactly
(carried forward verbatim from the superseded doc, unchanged except for
now attaching via `.Use()` at declare time instead of a `ToolHandler`-
call-time `mws` parameter):**

```go
// mcpgo.Observability carries a raw stats.Observer value INSIDE a
// middleware.Middleware wrapper's Fn — ToolHandler/ResourceHandler/
// PromptHandler already contain the full observability logic inline
// (TraceObserver span, RecordRequest("<kind>", name, status, duration)
// on every path); only the SOURCE of obs changes.
func Observability(obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{Name: "observability", Fn: obs}
}
```

```go
// adapters/mcpgo — collects EVERY stats.Observer-typed Fn attached via
// .Use(), fanning out via the ALREADY-SHIPPED stats.NewFanout when more
// than one is found (L12's fix — avoids silently dropping all but the
// first, the exact class of bug this whole mechanism exists to
// eliminate). Falls back to stats.ObserverFromContext(ctx) when none attached.
var observers []stats.Observer
for _, h := range handle.middlewareHandlers {
    if o, ok := h.Fn.(stats.Observer); ok {
        observers = append(observers, o)
    }
}
var obs stats.Observer
switch len(observers) {
case 0:
    obs = stats.ObserverFromContext(ctx)
case 1:
    obs = observers[0]
default:
    obs = stats.NewFanout(observers...)
}
```

`ToolHandler`/`ResourceHandler`/`PromptHandler`'s `Options.Observer`
field is REMOVED (replaced by `.Use(mcpgo.Observability(obs))` at
declare time) — an explicit breaking change, matching how `SecurityFunc`/
`CredentialFunc`-style ad hoc fields were removed everywhere else this
mechanism shipped (REST/events/reqreply all did the same).

## `ports` design — builds directly on D-0003's existing feasibility analysis, not re-derived

D-0003 already analyzed this extension in depth (see its "Feasibility
of a full `.Use()`-based `ports.Middleware[In,Out]`" section) and
concluded it is realistic: the prerequisite generic param constructors
already exist (`ports.NewCacheKeyParam[T,V]`/`ports.NewFilePathParam[T,V]`,
mirroring `rest.NewRequiredHeaderParam[T,V]` exactly), and
`NewFile(...)`/`NewCache(...)` are ALREADY declare-once values with a
real two-phase shape available to exploit. This doc turns that analysis
into a concrete design.

**Merge-field vocabulary — genuinely available for `File`/`Cache`, NOT
`Dir`:**

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

`CacheMiddleware[In,Out]` is the MECHANICALLY IDENTICAL sibling,
`WithCacheKey` in place of `WithPathVar` (reusing `ports.
NewCacheKeyParam[T,V]`). `DirMiddleware[In,Out]` gets ONLY the agnostic
path (`WithReceive`/`WithSend`) — `Dir.List` returns fixed `[]DirEntry`
values, confirmed no user-typed `T` exists to merge a path var into.

**Declare-time attachment, dispatch at the real operation:**

```go
// File[T] gains .Use, mirroring Tool[In,Out]/rest.Route's declare-time
// shape. No spec exists for File/Cache/Dir at all (unlike MCP's
// ToolSpec), so there is doubly no coverage-check concern here.
func (fh File[T]) Use(mws ...middleware.RouteMiddleware) File[T] {
    fh.middlewareHandlers = append(fh.middlewareHandlers, buildFileMiddlewareHandlers[T](mws)...)
    return fh
}
```

**Signature changes required (additive, confirmed real cost by
D-0003, not free)**: `Read`/`Write`/`Get`/`Set`/`Del`/`List` gain a
leading `ctx context.Context` parameter — needed for any attached
middleware `Fn` requiring real cancellation/timeout or
`middleware.ContextField` access (an HTTP-call-backed credential check,
for instance). Existing no-ctx call sites keep compiling via an
additive variant (mirrors how this codebase has handled every prior
ctx-introduction: additive first, never a silent breaking swap) —
exact naming (`ReadCtx` vs. overloading `Read` with a ctx-free
compatibility shim) is an open design decision, see below.

## Security design — reuses `middleware.SecurityScheme`/`CheckScopes` directly, no new type

**Cross-reference**: [`docs/design/d-0003-codec-declared-middlewares.md`](../design/d-0003-codec-declared-middlewares.md)'s
Addendum 3 separately evaluates (and resolves) whether Security should
ever fold into the codec-backed `Declaration[In,Out]` family for
REST/events/reqreply — this design's choice below (reuse the shared
mechanism directly, don't fold) is independently-derived evidence
consistent with that resolution (Security permanently stays on its own
dedicated type), not a presupposed answer for it.

`ports.RequireScopes[T]`-style security middleware is built the SAME
way REST/events/reqreply already build theirs — via the EXISTING,
shared `middleware.SecurityScheme(schemeName, scheme, scopes, codec)`
constructor and `middleware.CheckScopes(reqs, granted)` check, NOT a
new `ports`-specific security-declaration type:

```go
// ports — EXTRACTION-ONLY Fn (no pass/fail decision inside Fn itself);
// File.Read/Write collects grants from every attached security-shaped
// Fn in a first pass, merges, runs ONE middleware.CheckScopes, THEN
// calls the real operation — avoiding the AND-requirement bug REST's
// own history hit by keeping authentication (extraction) and
// authorization (the one final check) separate, even when MULTIPLE
// security-shaped Fns are attached. Carried forward from the
// superseded doc's sketch, unchanged — this part was already correct.
func RequireScopes[T any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, vars map[string]string) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:     "require-scopes:" + schemeName,
        Security: &middleware.SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn:       extract,
    }
}
```

No coverage-drift check exists here (no spec to check against — see
"Core finding" above) — a declared-but-unenforced `ports` security
requirement cannot occur structurally, since declare (`.Use()`) and the
operation it gates are always the SAME call chain, never two
independently-reachable phases the way a REST route's `.Use()` (builder
time) and `HandleMW` (adapter Register time) can drift apart.

## `ports.SQL` — confirmed out of scope, permanent limit case (not silently dropped)

Confirmed via code: `ports.SQLPattern` is metadata-only (`ports/
sql_meta.go`) — there is no `ports.SQL[T]` handle/builder value, query
text and placeholders are driver-specific closures owned directly by
`adapters/sql`'s constructors (`QueryAdapter`/`DrainInsertAdapter`/
`QueryEachAdapter`). There is no declare-once value to attach `.Use()`
to under the CURRENT architecture. Two honest paths forward, neither
designed here:
1. A future `adapters/sql`-local decorator wrapping `QueryAdapter`/etc.
   directly at the adapter-constructor call site (NOT a `ports.SQL[T]`
   type, since none exists) — a genuinely different attachment point
   than every other boundary in this doc.
2. Accept this as a PERMANENT exception, same spirit as `SQLPattern`
   already being a deliberately different, metadata-only shape vs.
   `FilePattern`/`EventPattern`/`RESTPattern`.

## Known limitations (carried forward from the superseded doc, L-numbers preserved verbatim)

### L5 — MCP coverage: Resources and Prompts

**Status:** RESOLVED (superseded doc, unchanged by this round) — MCP
needs NO decorator/wrapping mechanism beyond the ONE declare/dispatch
split designed above, applied identically to `ToolHandler`/
`ResourceHandler`/`PromptHandler` (all 3 share the IDENTICAL inline
observability pattern, confirmed via code — only the `<kind>` string
and identifying name differ).

### L11 — Unreconciled overlap with `dynamic-port-rebinding.md`

**Status:** RESOLVED (superseded doc, unchanged by this round).
`ports`' credential-rollover story is solved by per-call-reachable
`.Use()`-attached security middleware (swappable by rebuilding the
`File[T]`/`Cache[T]` value with different `.Use()` args between calls,
zero new mechanism) — `dynamic-port-rebinding.md`'s `Rebind` remains
necessary ONLY for swapping the underlying TRANSPORT ADAPTER itself, a
different concern. Both docs cross-reference each other (confirmed:
`dynamic-port-rebinding.md`'s Motivation section already links back
here). REST/events/reqreply's immutable `RouteHandle`/`ChannelHandle`
middleware hot-swap remains an acknowledged, unaddressed gap in both
docs.

### L12 — MCP's observability resolution silently drops all but the first matching middleware

**Status:** RESOLVED (superseded doc, unchanged by this round) — fixed
by reusing the ALREADY-SHIPPED `stats.NewFanout` (see the Observability
design above). This round additionally GENERALIZES the fix to
`ports`' own `Observability[T]` decorator (not scoped in the superseded
doc, which only covered MCP) — the identical silent-drop risk exists
the moment `ports` middleware supports attaching MULTIPLE
`Observer`-typed `Fn`s via `.Use()`, which this redesign now allows (the
superseded doc's call-time-variadic sketch had the same risk but never
named it for `ports` specifically).

### L14 — forge/Registry (Layer 3 pipelines) — out of scope, tracked separately

**Status:** RESOLVED (superseded doc, unchanged by this round) —
tracked entirely in [`forge-pipeline-middleware.md`](forge-pipeline-middleware.md),
no sequencing dependency either way.

## Structured errors (all implement `slog.LogValuer`)

Reuse the 5 existing error types VERBATIM, scoped to the new packages
— no new error taxonomy:
- `apimcp.MiddlewareInputError`/`MiddlewareError`/`MiddlewareOutputError`/
  `DuplicateMiddlewareNameError`/`AmbiguousMiddlewareAttachmentError`
  (mirrors `rest`/`events`/`reqreply`'s own byte-for-byte).
- `ports.MiddlewareInputError`/`MiddlewareError`/`MiddlewareOutputError`/
  `DuplicateMiddlewareNameError`/`AmbiguousMiddlewareAttachmentError`
  (same).
- `middleware.MiddlewareShapeError`/`UnsatisfiedScopesError` — reused
  directly from the shared `middleware` package, zero duplication (same
  as every other boundary).

## Observer integration

- MCP: `stats.Observer` via `.Use(mcpgo.Observability(obs))`,
  fan-out-aware (L12). `stats.TraceObserver` span/`RecordRequest` logic
  is UNCHANGED (already inline in `ToolHandler`/etc.) — only obs's
  SOURCE changes.
- `ports`: `stats.FileObserver`/`CacheObserver`/`SQLObserver` via
  `.Use(ports.Observability[T](obs))` per port, same fan-out-aware
  resolution generalized from L12.
- Both: type-assertion guard pattern, `nil`-Observer-safe, matching
  every other boundary in this codebase.

## Unit test plan

| Test | Boundary | Verifies |
|---|---|---|
| T1 | MCP | `.Use()` + `ToolHandler` dispatch, happy path |
| T2 | MCP | Two `Observability` middlewares fan out via `stats.NewFanout`, neither dropped (L12 regression test) |
| T3 | MCP | `ResourceHandler`/`PromptHandler` share identical dispatch (L5) |
| T4 | `ports.File` | `.Use()` + `Read`/`Write` dispatch, happy path, `WithPathVar` merge |
| T5 | `ports.File` | `RequireScopes` — multiple security `Fn`s merge grants before ONE `CheckScopes` call (no AND-requirement bug) |
| T6 | `ports.Cache`/`Dir` | Mechanical extension parity with `File` |
| T7 | all | `nil`/plain Observer → no panic, graceful fallback |
| T8 | all | `errors.As` chain + `LogValue()` shape for all 5 error types |
| T9 | `ports` | ctx-threading: a `ContextField`-based middleware round-trips correctly through the new `ctx` parameter |

## Files to create

| File | Responsibility |
|---|---|
| `api/mcp/middleware_declaration.go` | `ToolMiddleware[In,Out]`/`ResourceMiddleware`/`PromptMiddleware`, 5 error types |
| `api/mcp/transform.go` (or extend `builder.go`) | `.Use()` wiring, `middlewareHandlers` resolution at `Register` time |
| `adapters/mcpgo/middleware.go` | `Observability(obs)`, fan-out-aware resolution loop |
| `ports/middleware_declaration.go` | `FileMiddleware`/`CacheMiddleware`/`DirMiddleware[In,Out]`, `RequireScopes[T]`, 5 error types |
| `ports/file.go`/`cache.go`/`dir.go` (extend) | `.Use()`, ctx-threaded `Read`/`Write`/`Get`/`Set`/`Del`/`List` variants |

## Out of scope (Phase 2+)

- `ports.SQL` (see dedicated section above — permanent limit case or a
  future, differently-shaped design).
- Any new `stats` type — `stats.NewFanout` already covers the fan-out
  need completely.
- Pattern-bound ports' OWN middleware path — already solved via the
  Pattern's `Opts` field (D-0003's finding), nothing to redesign.
- Resolving whether `ports` should EVER adopt `middleware.ContextField`
  — mechanism already works uniformly if/when a driver appears; no
  driver confirmed yet (carried forward from the superseded doc,
  unchanged).

## Open design decisions (to resolve before/during implementation)

1. **Exact ctx-introduction shape for `ports`** — new `ReadCtx`-style
   additive methods, or overload resolution via a variadic trailing
   `ctx` argument, or accept a one-time breaking signature change (this
   codebase's own precedent leans additive-first, but `ports.File`'s
   call sites are comparatively few — worth re-measuring the actual
   blast radius before deciding).
2. **`Tool.Use(...)` vs. a `ToolOpt`-conforming middleware value passed
   into `NewTool(...)`** — this doc picked the former (literal mirror
   of `rest.Route.Use`'s chained-method shape) over the latter (fits
   MCP's existing `ToolOpt`-variadic idiom better) for closer alignment
   with the reference pattern; revisit if implementation reveals the
   chained-method shape fights `Tool[In,Out]`'s value-type (not
   pointer) semantics awkwardly.
3. **`ports.SQL`'s eventual design** — deferred entirely, no driver yet
   (mirrors `forge-pipeline-middleware.md`'s own "idea only" status for
   a structurally similar reason: a real architectural gap with no
   concrete, demanded use case forcing a decision yet).
4. **Whether `api/mcp`/`ports` ever want a Router-style grouping
   mechanism** — [`declarative-router-groups.md`](declarative-router-groups.md)
   designs a path/topic-PREFIX grouping construct (`rest.Router`/
   `events.Router`/`reqreply.Router`) for REST/events/reqreply, explicitly
   scoped OUT of `api/mcp` there, since tool names and resource URI
   templates don't share REST/events/reqreply's hierarchical path shape.
   If a concrete need ever surfaces (e.g. grouping a family of related
   tools under a shared name-prefix convention + shared `Tool.Use(...)`
   middleware, or a `ports` binding wanting to attach the same adapter
   wiring to several declared patterns at once), that doc's resolved
   `routable`-interface pattern (unexported interface + receiver-scoped
   type parameters, since Go forbids new type parameters on a method) is
   the reference design to start from — not a reflection-based or
   ad hoc mechanism. No driver exists yet; not designed further here.
