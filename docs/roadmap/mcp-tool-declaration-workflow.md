# MCP Tool Declaration Workflow — Declarative Middleware + Client-Bridge Reuse

> **Status:** Design draft — not yet implemented.
>
> This doc merges two previously-separate efforts, both about simplifying/enriching how
> `api/mcp.Tool`/`Resource`/`Prompt` are declared and dispatched:
>
> - **Section A** — Declarative Middleware for `api/mcp.Tool`/`Resource`/`Prompt`, SLICED
>   from the now-superseded `mcp-ports-declarative-middleware.md` (its `ports`-only content
>   moved to the sibling [`ports-declarative-middleware.md`](ports-declarative-middleware.md)
>   instead — the two boundaries don't belong in one file, see that doc's own header for
>   why). Section A's own content below is carried forward VERBATIM from that doc except
>   where noted.
> - **Section B** — Client-bridge reuse for `api/rest`/`api/file` client-backed MCP tools, a
>   NEW finding from reviewing `examples/go-edge-models`'s real, production MCP tool
>   declarations against `adapters/mcprest`'s existing (but under-used) REST→MCP bridge.
>
> [← Back to Roadmap](index.md)

## Motivation

`api/rest`, `api/events`, and `api/reqreply` all converged on the SAME cross-cutting-concern
mechanism: a codec-typed `Middleware[In,Out]` (wrapping the shared
`middleware.Declaration[In,Out]`), declared once at builder time (`.Use(mw)`) and dispatched
at register/attach time. `api/mcp` has NO such mechanism at all — zero attachment point
beyond an `Options.Observer` field (Section A's motivation).

Separately, `examples/go-edge-models`'s REAL, production MCP tools that proxy an
`api/rest`/`api/file` client call do NOT use the ALREADY-SHIPPED `adapters/mcprest` bridge
(`ToolHandler`/`MappedToolHandler`) at all — they hand-roll closures over a held `Dispatcher`
(REST, per-registry-host resolution) or `usecase.Store`/`Scope` (File, per-request `BasePath`
binding) instead. Root cause, confirmed via code: `mcprest`'s existing bridge builds its OWN
one-off transport from raw `*http.Client`+`baseURL` — it cannot reuse an app's ALREADY-
ATTACHED, already-wired `*rest.Client` at all, even in the simplest static case, and has no
slot for per-call DYNAMIC resolution (which registry host? which basePath?) either
(Section B's motivation).

## How Sections A and B relate (confirmed orthogonal, composable — not competing designs)

Section A gives `Tool[In,Out]` its OWN `.Use()`-attachable middleware chain (primarily for
Observability today, potentially other cross-cutting concerns later). Section B is about
HOW a Tool's handler is BUILT when it proxies an `api/rest`/`api/file` client call — it does
not touch `Tool`'s own declaration shape at all. A Tool built via Section B's `RegisterClientTool`
convenience returns the SAME kind of `*ToolHandle[Req,Resp]` `Tool.Register` already returns —
Section A's `.Use()` mechanism attaches identically regardless of how the Tool's handler was
constructed. The two stack cleanly: a REST-client-backed tool (Section B) can ALSO have
`.Use(mcpgo.Observability(obs))` attached (Section A) with zero interaction between them.

---

# Section A — Declarative Middleware for Tool/Resource/Prompt

## Core finding

Confirmed via `api/mcp/builder.go`: `api/mcp.Tool[In,Out]` (`NewTool(...)` + `.Register(b)` →
`*ToolHandle[In,Out]`) is STRUCTURALLY IDENTICAL to `rest.NewRoute(...).Register(builder)`/
`events.NewChannel(...)` — a genuine two-phase declare/dispatch split already exists.
`Resource[V,T]`/`Prompt` share the identical `NewX(...).Register(b)` shape.

**What's genuinely different from REST/events, confirmed and accepted, not worked around:**
MCP's spec object (`MCPSpec`'s `ToolSpec`/`ResourceSpec`/`PromptSpec`) has no
`Security`/middleware-metadata FIELD to populate — so there is no `CheckCoverage`-equivalent
drift check, and Security middleware can declare but can never be spec-rendered. This is why
MCP Security stays permanently N/A (unchanged, confirmed out of scope by the MCP skill's own
Gotchas) — not a bug this doc fixes.

## Merge-field vocabulary: NONE

Confirmed via `adapters/mcpgo.ToolHandler`: MCP arguments are a single flat `map[string]any`
decoded directly into `In` via `handle.Decode(args)` — there is no separate header/cookie/
query-equivalent channel to merge additional fields from. `ToolMiddleware[In,Out]` therefore
gets ONLY the agnostic path:

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

**Internal-layout note**: `ToolMiddleware[In,Out]` (and its `ResourceMiddleware`/
`PromptMiddleware` siblings) safely embed `middleware.Declaration[In,Out]` ANONYMOUSLY
because there is no second, "bound" role to accidentally conflict with (this design is
agnostic-only by choice). IF a bound/paired variant is EVER added in the future, it should
NOT embed `Middleware[In,Out]`-equivalent state anonymously — see
[D-0003's Addendum 7](../design/d-0003-codec-declared-middlewares.md)'s resolved
internal-layout decision (anonymous embedding would promote ALL of the embedded type's
methods, silently making the new bound type ALSO satisfy whatever interface its agnostic
sibling uses for `.Use()` attachment).

## Attachment — mirrors `rest.Route.Use`/`events.Channel`'s declare-time shape

```go
// Tool[In, Out] gains .Use, mirroring rest.Route.Use/events.Channel's chained-method
// shape (considered and preferred over passing middleware as another NewTool ToolOpt —
// keeps parity with the reference pattern's actual method name/shape).
func (t Tool[In, Out]) Use(mws ...middleware.RouteMiddleware) Tool[In, Out] {
    t.tb.middlewareHandlers = append(t.tb.middlewareHandlers, buildToolMiddlewareHandlers(mws)...)
    return t
}
```

`Tool.Register(b)` resolves attached middleware into `ToolHandle`'s new unexported
`middlewareHandlers []middleware.MiddlewareHandler` field (mirrors `RouteHandle
.MiddlewareHandlers` exactly) — **no spec contribution** (`ToolSpec`/`ResourceSpec`/
`PromptSpec` gain no new field; `MCPSpec()` output is unaffected). `adapters/mcpgo
.ToolHandler`/`ResourceHandler`/`PromptHandler` dispatch `handle.middlewareHandlers` at call
time, the same pre-handler point security enforcement already runs at on every other
boundary.

## Observability — generalizes the already-resolved L5/L12 design exactly

```go
// mcpgo.Observability carries a raw stats.Observer value INSIDE a middleware.Middleware
// wrapper's Fn — ToolHandler/ResourceHandler/PromptHandler already contain the full
// observability logic inline (TraceObserver span, RecordRequest("<kind>", name, status,
// duration) on every path); only the SOURCE of obs changes.
func Observability(obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{Name: "observability", Fn: obs}
}
```

```go
// adapters/mcpgo — collects EVERY stats.Observer-typed Fn attached via .Use(), fanning
// out via the ALREADY-SHIPPED stats.NewFanout when more than one is found (avoids
// silently dropping all but the first). Falls back to stats.ObserverFromContext(ctx) when
// none attached.
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

`ToolHandler`/`ResourceHandler`/`PromptHandler`'s `Options.Observer` field is REMOVED
(replaced by `.Use(mcpgo.Observability(obs))` at declare time) — an explicit breaking
change, matching how `SecurityFunc`/`CredentialFunc`-style ad hoc fields were removed
everywhere else this mechanism shipped (REST/events/reqreply all did the same).

## Known limitations (carried forward, L-numbers preserved verbatim from the superseded doc's own numbering)

### L5 — MCP coverage: Resources and Prompts

**Status:** RESOLVED. MCP needs NO decorator/wrapping mechanism beyond the ONE
declare/dispatch split designed above, applied identically to `ToolHandler`/
`ResourceHandler`/`PromptHandler` (all 3 share the IDENTICAL inline observability pattern,
confirmed via code — only the `<kind>` string and identifying name differ).

### L12 — MCP's observability resolution silently drops all but the first matching middleware

**Status:** RESOLVED — fixed by reusing the ALREADY-SHIPPED `stats.NewFanout` (see the
Observability design above). The `ports`-side generalization of this same fix lives in the
sibling [`ports-declarative-middleware.md`](ports-declarative-middleware.md), not
duplicated here.

## Open design decision A1 — `Tool.Use(...)` vs. a `ToolOpt`-conforming middleware value

This doc picks `Tool.Use(mws ...)` (literal mirror of `rest.Route.Use`'s chained-method
shape) over passing middleware as another variadic `ToolOpt` into `NewTool(...)` (which
would fit MCP's existing `ToolOpt`-variadic idiom better), for closer alignment with the
reference pattern. Revisit if implementation reveals the chained-method shape fights
`Tool[In,Out]`'s value-type (not pointer) semantics awkwardly.

## Open design decision A2 — a prefix-less `ToolGroup` grouping mechanism

[D-0008 (Declarative Router Groups)](../design/d-0008-declarative-router-groups.md) designs
a path/topic-PREFIX grouping construct (`rest.Router`/`events.Router`/`reqreply.Router`) for
REST/events/reqreply, explicitly scoped OUT of `api/mcp` there — `mcp.Tool[In,Out]` has a
flat `Name string`, no hierarchical path, so a prefix-COMPOSING "Router" genuinely doesn't
fit. A SMALLER, adjacent idea: a prefix-LESS middleware-grouping convenience — attach ONE
middleware set to N tools in a single declaration, with NO path/name composition at all:

```go
// ToolGroup (name TBD) — groups tools for SHARED middleware only; no prefix/Mount/Walk
// concept, unlike D-0008's Router.
func NewToolGroup() ToolGroup
func (g ToolGroup) Use(mws ...middleware.RouteMiddleware) ToolGroup
func (g ToolGroup) Tool(t routable) ToolGroup   // routable: minimal middlewareNames()/
                                                 // registerAny() only — NO withRouterPrefix,
                                                 // unlike D-0008's leaves
func (g ToolGroup) Register(b *Builder) error
```

**Genuine prerequisite, already verified**: `Tool.Use()` (decision A1 above) must ship FIRST
— `ToolGroup` has nothing to attach to until it exists. Not designed further this round; no
driver exists yet beyond this sketch.

---

# Section B — Client-Bridge Reuse for REST/File-Backed Tools

## The old approach, in full, with concrete evidence

### There ALREADY IS a REST→MCP bridge: `adapters/mcprest` (shipped, D-0006 Phase 5a)

`adapters/mcprest/bridge.go` provides exactly 2 generic functions:

```go
// The general form — ToolIn/ToolOut can differ from the route's own Req/Resp.
func MappedToolHandler[ToolIn, ToolOut, Req, Resp any](
    client *http.Client, baseURL string, handle *rest.RouteHandle[Req, Resp],
    opts rest.ClientCallOptions,
    toReq func(ToolIn) (Req, error), fromResp func(Resp) (ToolOut, error),
) mcpgo.HandlerFunc[ToolIn, ToolOut]

// The zero-boilerplate case — ToolIn/ToolOut IS Req/Resp (identity mappers).
func ToolHandler[Req, Resp any](
    client *http.Client, baseURL string, handle *rest.RouteHandle[Req, Resp],
    opts rest.ClientCallOptions,
) mcpgo.HandlerFunc[Req, Resp]
```

Mechanism: builds ONE `nethttp.NewClientTransport` internally, calls
`rest.CallWithTransport(ctx, transport, handle, req, opts)` per invocation. The returned
function is simultaneously a valid `mcpgo.HandlerFunc` AND a `ports.ToolPort.SetFunc` — no
new plumbing needed to also expose the same logic as a REST endpoint or reqreply endpoint.
This bridge IS demonstrated correctly in `examples/go-edge-models/main.go` (~lines 927-1018)
for the simple, static `registry.GetTagsRoute` case.

### But go-edge-models' REAL, production REST-client MCP tools do NOT use this bridge

`app/registry/dispatcher.go`'s `Dispatcher.NewGetTagsToolHandler()`/
`NewGetImageMetadataToolHandler()` are plain, hand-written closures over a held
`*Dispatcher`. `main.go` itself says so explicitly in its own comment (~lines 1033-1041):

> "Unlike the ToolHandler/MappedToolHandler sections above (which wrap GetTagsRoute
> directly via adapters/mcprest — fixed to ONE registry's baseURL),
> registry.GetTagsTool/GetImageMetadataTool pair with registryapp.Dispatcher's own
> NewGetTagsToolHandler()/NewGetImageMetadataToolHandler() methods — registry-agnostic...
> **No mcprest bridge is involved: these are plain closures over a HELD Dispatcher.**"

**Root cause, confirmed via code**: `mcprest.MappedToolHandler` takes a SINGLE, FIXED
`*http.Client`+`baseURL`+`*RouteHandle`, built ONCE outside the hot path — and, more
fundamentally, it builds its OWN one-off transport internally rather than accepting an
ALREADY-ATTACHED `*rest.Client`. `Dispatcher` must resolve, per call, WHICH of 3-4 different
concrete per-registry clients (dockerhub/ghcr/mcr/generic-fallback) to dispatch to, based on
parsing the tool's own `ImageURL` field — and even `app/registry/internal.RegistryClient`'s
own ALREADY-ATTACHED, already-security-wired `*rest.Client` (with `BearerAuthClientMW`
pre-attached) could not be reused by `mcprest`'s bridge even for the SIMPLEST, static,
single-host case — the bridge would force building a SECOND, duplicate, credential-less
transport from scratch.

### There is NO equivalent bridge for `api/file` at all

Confirmed via direct search — `adapters/mcpfile` does not exist. `api/file` ships the exact
same "declare role, attach client, dispatch" shape `api/rest` has (see
[D-0011](../design/d-0011-file-api-backend-transparency.md)) — structurally, a bridge
analogous to `mcprest` is just as plausible to build for it; it simply was never designed.
`go-edge-models`' file-backed MCP tools (`modulesummary.ReadTool`, `updatemoduleimage`'s
tool) follow the identical hand-written shape for the identical underlying reason: a
long-lived `*usecase.Store` holds the attached `*file.Client`, and `Store.For(basePath)`
binds ONE request's `BasePath` into a fresh `*Scope` per call — the EXACT per-request-binding
need `Dispatcher.resolve` has, independently reinvented.

## The real structural reason both bridges are missing: per-call/per-request BINDING

`mcprest`'s bridge assumes ONE fixed `(transport, route)` pair, built once, reused forever.
Both go-edge-models use cases violate that assumption identically: REST needs to resolve
WHICH registry host per call (from `ImageURL`); File needs to resolve WHICH `BasePath` root
per call. Neither is a "credential" concern (mcprest's doc comment already has a recipe for
per-call CREDENTIALS via ctx/session lookup) — this is a per-call TRANSPORT/BACKEND
RESOLUTION concern, a structurally different axis mcprest's current API has no slot for.

## Design — Path 1: handler-based reuse (the flexible primitive)

`api/mcp.Tool` stays hand-declared exactly as today (own Req/Resp shape, own `ToolMeta`,
free to differ from the domain Route/Resource's wire shape for LLM-friendliness) — but its
BACKING HANDLER reuses an ALREADY-ATTACHED `*rest.Client` directly, confirmed via
`rest.ClientTransport.Call(ctx, route any, req any, opts...) (any, error)` (the interface
`*rest.Client` implements internally):

```go
// ClientToolHandler — the STATIC case: client is an ALREADY-ATTACHED *rest.Client (the
// SAME one the app already built via its own normal declare->register->attach workflow —
// e.g. app/registry/internal.RegistryClient's own rc.client) and handle is already
// registered on it. No new transport is built; the tool call goes through the EXACT SAME
// attached client every other REST call made by the app uses — same credentials, same
// security wiring (any .Use()-attached ClientMW already runs inside client.Call), same
// connection reuse.
func ClientToolHandler[Req, Resp any](
    client *rest.Client, handle *rest.RouteHandle[Req, Resp],
) mcpgo.HandlerFunc[Req, Resp] {
    return func(ctx context.Context, req Req) (Resp, error) {
        var zero Resp
        respAny, err := client.Call(ctx, handle, req)
        if err != nil {
            return zero, err
        }
        resp, ok := respAny.(Resp)
        if !ok {
            return zero, rest.TransportTypeMismatchError{Expected: fmt.Sprintf("%T", zero), Got: fmt.Sprintf("%T", respAny)}
        }
        return resp, nil
    }
}

// DynamicClientToolHandler — the Dispatcher/Scope case: resolve picks WHICH
// already-attached client+handle to use PER CALL (Dispatcher.resolve's own existing
// per-host logic becomes this closure directly), plus optional toReq/fromResp mappers for
// when the tool's own shape differs from the route's (mirrors MappedToolHandler's split).
func DynamicClientToolHandler[ToolIn, ToolOut, Req, Resp any](
    resolve func(ToolIn) (*rest.Client, *rest.RouteHandle[Req, Resp], error),
    toReq func(ToolIn) (Req, error),
    fromResp func(Resp) (ToolOut, error),
) mcpgo.HandlerFunc[ToolIn, ToolOut] {
    return func(ctx context.Context, in ToolIn) (ToolOut, error) {
        var zero ToolOut
        client, handle, err := resolve(in)
        if err != nil {
            return zero, err
        }
        req, err := toReq(in)
        if err != nil {
            return zero, ToolRequestMapError{Err: err}
        }
        respAny, err := client.Call(ctx, handle, req)
        if err != nil {
            return zero, err
        }
        resp, ok := respAny.(Resp)
        if !ok {
            return zero, rest.TransportTypeMismatchError{Expected: fmt.Sprintf("%T", *new(Resp)), Got: fmt.Sprintf("%T", respAny)}
        }
        out, err := fromResp(resp)
        if err != nil {
            return zero, ToolResponseMapError{Err: err}
        }
        return out, nil
    }
}
```

`registry.Dispatcher.NewGetTagsToolHandler()` would collapse to one call into
`DynamicClientToolHandler`, with `resolve` doing exactly what `Dispatcher.resolve` already
does today (parse `ImageURL`, pick the right already-attached per-host client + its
already-registered tags handle) — eliminating the `GetTags`/`GetTagsFiltered` method-call
indirection PURELY for the MCP-tool dispatch path (the plain Go-API methods stay unchanged
for non-MCP callers).

## Design — Path 2: one-call declarative convenience (built ON TOP of Path 1)

For the common case (Tool's In/Out IS the Route's own Req/Resp, or close enough that reusing
the SAME codec is desirable — already mechanically possible today, since `mcp.NewTool`
accepts any `codex.Codec`), a higher-level constructor collapses today's 3-5 separate steps
(hand-declare `ToolReq`+`ToolReqCodec`+`Tool`, `Register`, build handler, wire via
`mcpgo.ToolHandler`) into ONE declarative call:

```go
// RegisterClientTool — the ONE-CALL convenience: derives the Tool's schema directly from
// handle's own Req/Resp codecs (no separate ToolReq/ToolReqCodec needed), registers it on
// mcpBuilder, and backs it with client via ClientToolHandler — all in one call. Returns
// the built *ToolHandle (same as Tool.Register would) plus the ready-to-wire
// mcpgo.HandlerFunc.
func RegisterClientTool[Req, Resp any](
    mcpBuilder *apimcp.Builder, name string, client *rest.Client,
    handle *rest.RouteHandle[Req, Resp], toolOpts ...apimcp.ToolOpt,
) (*apimcp.ToolHandle[Req, Resp], mcpgo.HandlerFunc[Req, Resp], error)
```

This is OPTIONAL sugar, not a replacement for hand-declaring a Tool with its own mapped
shape (`MappedToolHandler`'s/`DynamicClientToolHandler`'s role) when an LLM-friendlier shape
is wanted — mirrors how `ToolHandler` (identity) and `MappedToolHandler` (general) already
coexist today, just one layer higher (declare+register+handle, not just handle).

## `api/file` equivalent (`adapters/mcpfile`) — Future work, outline depth only

`api/file.Client`'s dispatch is inherently multi-verb (Read/Create/Patch/Delete/List/...),
unlike REST's single `Call(route, req)` shape — so a 1:1 mirror of `ClientToolHandler`
isn't quite right; the faithful mirror is per-ROLE, parameterized over the same
"resolve a per-call binding" idea `Store.For(basePath)` already embodies:

```go
// One constructor per role-shape family, mirroring Client's own method split. Likely only
// Read/Create/Patch/List/Delete are needed for real MCP-tool usage — confirm against
// actual go-edge-models usage before building all 11 roles.
func ReadToolHandler[ToolIn, ToolOut, T any](
    resolve func(ToolIn) (*file.Client, file.Reader[T], map[string]string, error),
    fromResp func(T) (ToolOut, error),
) mcpgo.HandlerFunc[ToolIn, ToolOut]
```

`api/file.Client`'s dispatch never auto-merges vars INTO `Req` the way REST's `PathParam`
does (vars is always a separate, explicit argument), so this family needs one more explicit
accessor than REST's does — not a gap, a structural difference, already accounted for in the
signature above (the `resolve` closure returns the vars map directly).

**Open question, deliberately not resolved here**: is a generic per-role bridge actually
less code than today's hand-written closures, given each `app/iotedge` tool handler already
composes MULTIPLE `Scope` method calls (not a single verb dispatch, e.g.
`readModuleSummary`'s 3+ chained reads + 2 domain-merge functions)? The honest answer may be
"no" for a COMPOSED tool — the value is highest for a SIMPLE tool (one Resource, one verb).
Validate against a real simple case before committing to a broader rollout.

## Scope decisions

| In scope (this doc, Phase 1 design) | Out of scope |
|---|---|
| Section A: `api/mcp.Tool`/`Resource`/`Prompt` gain `.Use(mws ...middleware.RouteMiddleware)` | `ports` (own doc, [`ports-declarative-middleware.md`](ports-declarative-middleware.md)) |
| Section B: `adapters/mcprest` gains `ClientToolHandler`/`DynamicClientToolHandler`/`RegisterClientTool`, full REST rigor | `adapters/mcpfile` (File-side equivalent) — outline depth only, own future roadmap item |
| `registry.Dispatcher` rewired to use the new Section B functions (proof of value) | `api/mcp` core redesign beyond `.Use()`/`ToolGroup` — Tool stays a flat, non-hierarchical declaration |
| Existing `ToolHandler`/`MappedToolHandler` kept unchanged (still valid for a caller with no pre-attached client) | MCP Security (permanent N/A, unchanged) |

## Structured errors

Section A reuses the 5 existing Declarative-Middleware error types, scoped to `api/mcp`:
`apimcp.MiddlewareInputError`/`MiddlewareError`/`MiddlewareOutputError`/
`DuplicateMiddlewareNameError`/`AmbiguousMiddlewareAttachmentError` (mirrors
`rest`/`events`/`reqreply`'s own byte-for-byte). Section B reuses `mcprest`'s EXISTING
`ToolRequestMapError`/`ToolResponseMapError` and `rest.TransportTypeMismatchError` — no new
error type needed for either section.

## Observer integration

Section A: `stats.Observer` via `.Use(mcpgo.Observability(obs))`, fan-out-aware (see L12
above). Section B: relies entirely on `client.Call`'s own EXISTING Observer/TraceObserver
recording — the bridge adds no new Observer surface. Both coexist on the same Tool without
conflict (confirmed orthogonal, see "How Sections A and B relate" above).

## Unit test plan

| Test | Section | Verifies |
|---|---|---|
| T1 | A | `.Use()` + `ToolHandler` dispatch, happy path |
| T2 | A | Two `Observability` middlewares fan out via `stats.NewFanout`, neither dropped (L12 regression test) |
| T3 | A | `ResourceHandler`/`PromptHandler` share identical dispatch (L5) |
| T4 | B | `ClientToolHandler` reuses an already-attached `*rest.Client` — same credentials/connection as a plain `client.Call` |
| T5 | B | `DynamicClientToolHandler` resolves per call, dispatches to the correct client+handle |
| T6 | B | `DynamicClientToolHandler`'s `resolve` error propagates before any call is made |
| T7 | B | `DynamicClientToolHandler`'s `toReq` mapper error wraps as `ToolRequestMapError` |
| T8 | B | `RegisterClientTool` derives the Tool's schema from `handle`'s own codec, registers, returns a working handler |
| T9 | B | `registry.Dispatcher`'s `get_tags`/`get_image_metadata` MCP tools behave identically end-to-end after rewiring (regression proof) |

## Files to create/touch

| File | Responsibility |
|---|---|
| `api/mcp/middleware_declaration.go` | `ToolMiddleware[In,Out]`/`ResourceMiddleware`/`PromptMiddleware`, 5 error types (Section A) |
| `api/mcp/transform.go` (or extend `builder.go`) | `.Use()` wiring, `middlewareHandlers` resolution at `Register` time (Section A) |
| `adapters/mcpgo/middleware.go` | `Observability(obs)`, fan-out-aware resolution loop (Section A) |
| `adapters/mcprest/bridge.go` | `ClientToolHandler`/`DynamicClientToolHandler`/`RegisterClientTool` (Section B) |
| `adapters/mcprest/bridge_test.go` | New tests T4-T8 |
| `adapters/mcprest/doc.go` | Document the new, preferred attached-client shape alongside the existing raw-client+baseURL one |
| `examples/go-edge-models/app/registry/dispatcher.go` | Rewire `NewGetTagsToolHandler`/`NewGetImageMetadataToolHandler` to use `DynamicClientToolHandler` |

## Out of scope

- `ports` — entirely out of scope, see the sibling [`ports-declarative-middleware.md`](ports-declarative-middleware.md).
- `adapters/mcpfile` — the File-side equivalent of Section B stays at outline depth (see
  above); a full Go-signature design is a future roadmap item once Section B's REST
  implementation has proven the pattern.
- Any `api/mcp` core redesign beyond `.Use()`/`ToolGroup` — Tool stays a flat,
  non-hierarchical declaration; no Router/prefix-composition concept is introduced.

## Open design decisions

1. **A1** — `Tool.Use(...)` vs. a `ToolOpt`-conforming middleware value (see above).
2. **A2** — the prefix-less `ToolGroup` grouping mechanism, gated on A1 shipping first.
3. **B1** — exact naming: `ClientToolHandler`/`DynamicClientToolHandler`/`RegisterClientTool`
   are working names, not final.
4. **B2** — whether `RegisterClientTool` needs its OWN Dynamic variant NOW (a
   `RegisterDynamicClientTool`-style one-call convenience over `DynamicClientToolHandler`)
   or can wait until a concrete driver (e.g. the `registry.Dispatcher` rewiring itself)
   demonstrates the need.
5. **B3** — whether Path 2 (`RegisterClientTool`) belongs in `adapters/mcprest`
   (adapter-owned, mirrors today's `ToolHandler`'s placement) or should be proposed as an
   `api/mcp`-core convenience instead (a bigger, cross-cutting decision, likely needing its
   own dedicated resolution round before implementation).
