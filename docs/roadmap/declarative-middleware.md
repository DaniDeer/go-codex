# Declarative Middleware — remaining scope: MCP and `ports`

> **SUPERSEDED for REST (request/response AND SSE) — see
> [Middleware Workflow Simplification](../design/d-0001-rest-middleware-workflow-simplification.md)
> and [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md),
> BOTH IMPLEMENTED.** This doc's original REST design ("Revision 2 — the
> declare/implement split") shipped, was unified/simplified by d-0001
> (`HandleMW`/`ClientMW` replace `.Implement()`/`Wrap`; `Register`/
> `RegisterHandle`/`Serve`/`ServeSSE`/`ServeOne` replace the old four-door
> surface), and then gained a codec-backed Input/Output declaration
> mechanism via d-0003 (`rest.Middleware[In,Out]`, `Transform`/
> `ClientTransform`, plain `.Use(mw)`).
>
> **SUPERSEDED for events pub/sub — ALSO by D-0003.** This doc's
> "Events + ReqReply" coverage subsection originally proposed
> `events.WithMiddleware`/`mqtt5.RequireScopes[T]`-style APIs — those
> names were **never shipped**. `api/events` security shipped instead via
> [D-0002 — Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)
> (`FromSecurityScheme`/`CheckCoverage`), and its OWN codec-backed
> Input/Output middleware declaration (`events.Middleware[In,Out]`,
> `Transform`/`ClientTransform`, `.Use(mw)`) shipped via D-0003, mirroring
> REST's mechanism exactly. Do not follow this doc's own events/pub-sub
> code samples/naming as current.
>
> **`api/reqreply`'s own workflow (separate from pub/sub) is tracked in
> its own doc, not here**: see
> [ReqReply Workflow Simplification](reqreply-workflow-simplification.md)
> for up-to-date findings — this doc's older reqreply-specific proposals
> are superseded there, not maintained in parallel.
>
> **Remaining scope of THIS doc, narrowed accordingly: MCP
> (`api/mcp`/`mcpgo`, observability-only, Security stays permanently N/A)
> and `ports` (`File`/`Cache`/`SQL`/`Dir`, decorator-shaped, no spec to
> feed).** Both remain Phase 2+ DESIGN RATIONALE, not yet implemented —
> see "Coverage across every API/port boundary" below for the current,
> accurate scope table. Do not follow this doc's own REST/events code
> samples/signatures as current; the MCP/ports sketches below have NOT
> shipped and remain open design work. `docs/roadmap/forge-pipeline-middleware.md`
> tracks Layer 3 (`forge.Registry`/pipelines) separately — never in
> scope here.
> [← Back to Roadmap](index.md)

## Core thesis

Routes, channels, and ports should be reducible to what they
fundamentally ARE: a typed **input/request → output/response** contract,
nothing more. Cross-cutting concerns — security, rate-limiting, request
enrichment — do NOT belong baked into a route/channel/port's own
construction or `Options` struct as ad hoc, boundary-specific fields
(`SecurityFunc`, `CredentialFunc`, and any future one-off equivalent).
They belong attached SEPARATELY, via ONE shared, composable mechanism, to
WHICHEVER boundary needs them — REST route, event channel, or
`ports.File`/`Cache`/`SQL` alike.

This is what makes it easy to add authorization to `ports.File` — which
has NO security hook of ANY kind today (unlike REST/events, which
already gained one via d-0001/d-0003) — with the SAME vocabulary already
proven on those shipped boundaries, instead of inventing a bespoke
mechanism per boundary type as the need arises. See "Ports get the same
treatment" below for a concrete, structurally-proven sketch — the direct
test of whether this design is genuinely general, not a REST-specific
mechanism that happens to also apply elsewhere.

`stats.Observer` on `ports.File`/`Cache`/`SQL`/`Dir` is this doc's SECOND
remaining worked use case, proving the same point for observability —
see "`ports.File`'s equivalent — no ferry needed" below.

## Cross-cutting concerns and one-struct-one-call

Reviewed explicitly: does attaching security/observability via a
`ports`-side middleware mechanism threaten the "one-struct-one-call"
principle (`docs/concepts/api-contracts.md`) — a caller does the ENTIRE
encode-or-decode direction with one struct value in/out, one call?
**No.** `ports.File.Read`/`.Write` (and `Cache`/`SQL`/`Dir`'s
equivalents) remain single-struct-in/out calls — any attached
middleware value is an ADDITIONAL, purely opt-in, variadic parameter
alongside the one struct, never a replacement for it or a second struct
a caller must also assemble. REST and events already proved this same
non-threat for their own boundaries (see d-0001/d-0003); `ports` is
where it remains to be confirmed by actual implementation.

## `middleware.ContextField[V]` — the codec-typed, ctx-carried value bus

**Already SHIPPED** (`middleware/context_field.go`, alongside REST's
d-0001 rollout) — not a design proposal anymore. For data that should
NOT force every route's `Req` to carry security-specific fields just
because SOME deployment attaches an auth middleware: a shared, mutable
box pre-allocated on `ctx` (`EnsureContextFields`, called once by
`adapters/nethttp`/`chi`'s dispatch), with `ContextField[V].Set`/`.Get`
reading/writing through it — mirrors `nethttp.WithResponseHeaders`'
pre-allocation pattern, generalized to arbitrary codec-typed values.
Works from ANY `Fn` shape uniformly, and is bidirectional (a
general-purpose middleware can `Get` a value `Set` by an inner one,
since the box is a shared mutable object, not a sequence of immutable
ctx replacements).

Events adapters (`mqtt`/`mqtt5`/`zeromq`) do NOT call
`EnsureContextFields` — their security-shaped Fns already get direct
write access to the decoded payload (`*T`), serving the same
cross-cutting-data need via a different mechanism suited to pub/sub's
own shape (see `middleware/context_field.go`'s own doc comment).

**Remaining open question for THIS doc's scope (MCP/ports):** neither
`ports.File`/`Cache`/`SQL`/`Dir` nor `mcpgo`'s `ToolHandler`/
`ResourceHandler`/`PromptHandler` call `EnsureContextFields` today —
whether either boundary ever needs `ContextField` (as opposed to
writing derived data directly onto its own `T`/decorator return value)
is undecided and has no concrete driver yet. The mechanism itself needs
NO further design work to become available to them — `ContextField[V]`
already works uniformly from any `ctx`-carrying `Fn` shape.

## Ports get the same treatment — `ports.File[T]` (sketched, Phase 2 for implementation)

> **Note:** this sketch predates D-0003's codec-backed
> `Declaration[In,Out]`/`Middleware[In,Out]` pattern shipped for
> REST/events. D-0003 itself analyzes this exact ports extension in its
> "Feasibility of a full `.Use()`-based `ports.Middleware[In,Out]`"
> section and concludes it is realistic, reusing the SAME already-generic
> constructors (`ports.NewCacheKeyParam[T,V]`/`NewFilePathParam[T,V]`)
> this doc's own sketch below anticipated. A future implementation round
> should evaluate BOTH shapes (this doc's `middleware.Middleware`-based
> decorator below, and d-0003's `Declaration[In,Out]`-based alternative)
> rather than treating this sketch as the only remaining option.

`ports.File[T].Read`/`.Write` are ALREADY pure I/O — `Read(vars,
opts) (T, error)`, `Write(vars, v, opts) (createdDirs, error)` — with
**zero** existing security/authorization hook, unlike REST/events (which
already gained one via d-0001/d-0003). This is the direct test of the
Core thesis: does a shared middleware shape genuinely generalize to
ports, or is it secretly REST/HTTP-specific?

`File[T]`'s operations are plain method calls, not `http.Handler`-wrapped
— so the concrete `Fn` shape here is a DECORATOR, not a handler-wrapper,
but the STRUCTURE (name, type-erased `Fn`, optional `Satisfies`) mirrors
`middleware.Middleware`'s own shape:

```go
// ports.File[T].Read/.Write gain a variadic middleware.Middleware
// parameter, exactly mirroring nethttp.Register/Call's Phase 1 shape.
func (fh File[T]) Read(ctx context.Context, vars map[string]string, opts FileOptions, mws ...middleware.Middleware) (T, error) {
    next := func() (T, error) { return fh.readRaw(ctx, vars, opts) } // today's existing logic, unchanged
    for i := len(mws) - 1; i >= 0; i-- {
        fn, ok := mws[i].Fn.(func(context.Context, map[string]string, func() (T, error)) (T, error))
        if !ok {
            var zero T
            return zero, MiddlewareShapeError{Name: mws[i].Name, Expected: "file decorator", Got: fmt.Sprintf("%T", mws[i].Fn)}
        }
        prevNext, mw := next, fn
        next = func() (T, error) { return mw(ctx, vars, prevNext) }
    }
    return next()
}
```

A `RequireScopes`-shaped constructor for `ports.File`, reusing the EXACT
SAME `route.Satisfied` predicate this codebase's own security mechanisms
already use — proving the scope-matching logic is 100% shared, only the
wrap-shape differs per boundary. `Fn` here is EXTRACTION-ONLY (`func(ctx,
vars, req) (map[string][]string, error)`, no pass/fail decision) — a
SEPARATE Fn shape from the general-purpose decorator (`func(ctx, vars,
next) (T, error)`) used for observability, so authentication (extraction)
and authorization (the one final check) stay separate concerns even when
MULTIPLE security-shaped `Fn`s are attached for an AND-combined
requirement — a real bug REST hit and fixed (see d-0001) that this ports
sketch avoids from the start. `Read`/`Write` collect grants from every
attached security-shaped `Fn` in a FIRST pass (none of them call `next`
— they only extract), merge them, run ONE `middleware.CheckScopes`
check, and only THEN invoke the real operation (wrapped by any
general-purpose decorators, in the usual nested fashion):

```go
// ports — extraction only; no route.Satisfied check inside Fn itself
// (the caller does ONE combined check after merging every attached Fn's
// grants, avoiding the AND-requirement bug REST's own history hit).
func RequireScopes[T any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, vars map[string]string) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Security:  &middleware.SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn: func(ctx context.Context, vars map[string]string) (map[string][]string, error) {
            return extract(ctx, vars)
        },
    }
}
```

```go
// Usage — a caller reading a config file that should only be readable by
// callers with the "config:read" scope (however AuthN happened upstream —
// e.g. an OAuth2 Proxy sidecar or Keycloak/Envoy JWT filter in front of
// the process, leaving THIS check to verify authorization only).
configFile.Read(ctx, vars, ports.FileOptions{},
    ports.RequireScopes[Config]("apiKey", []route.SecurityRequirement{route.Require("apiKey", "config:read")}, extractGrantedScopes),
)
```

Since `ports.File` has NO existing `Security`/`SecuritySchemes` SPEC
concept at all (unlike a REST route, `ports.File` has no OpenAPI/AsyncAPI
document to declare against) there is no drift-closing-validation
equivalent to design here — `Satisfies` is carried for CONSISTENCY with
Phase 1's shape and future spec integration, but nothing currently reads
it for `ports.File`. This asymmetry is expected, not a gap: `ports.File`
was never spec-backed in the first place (see
`docs/concepts/declaring-apis-and-ports.md`'s "non-spec" workflow). For
the SAME reason, `ports.File`'s `Middleware` values NEVER carry
`Security`/`RequestParams`/`ResponseParams` (there is no `rest.WithMiddleware`-
equivalent RouteOpt here, and no spec for it to feed) — `ports.File`
stays PURE runtime-decorator-only, attached directly at the `Read`/`Write`
call site (there is only ONE attachment point for ports, unlike REST's
two, precisely because there is no earlier "declaration time" separate
from the call itself).

### Role symmetry — `Write` needs its OWN decorator shape

`Read`'s decorator shape (`next func() (T, error)`) does NOT fit
`Write` — `Read`'s `T` is an OUTPUT (produced by `next`), but `Write`'s
`T` is an INPUT (the caller already has `v` before calling `Write` at
all). Matching the SAME "both roles of the boundary" requirement that
already governs REST/events/reqreply's publisher/subscriber and
requestor/replier symmetry (see the review checklist's Boundary Symmetry
Guardrail), `Write` gets its OWN decorator shape, threading `v` THROUGH
each middleware instead of producing it:

```go
// ports.File[T].Write — v is an INPUT, so next takes T and can be
// inspected/transformed before the real write happens (e.g. a
// middleware that rejects an oversized v before touching the
// filesystem, or a security check that reads v's OWN fields to decide).
func (fh File[T]) Write(ctx context.Context, vars map[string]string, v T, opts FileOptions, mws ...middleware.Middleware) (createdDirs []string, err error) {
    next := func(v T) ([]string, error) { return fh.writeRaw(ctx, vars, v, opts) } // today's existing logic, unchanged
    for i := len(mws) - 1; i >= 0; i-- {
        fn, ok := mws[i].Fn.(func(context.Context, map[string]string, T, func(T) ([]string, error)) ([]string, error))
        if !ok {
            return nil, MiddlewareShapeError{Name: mws[i].Name, Expected: "file write decorator", Got: fmt.Sprintf("%T", mws[i].Fn)}
        }
        prevNext, mw := next, fn
        next = func(v T) ([]string, error) { return mw(ctx, vars, v, prevNext) }
    }
    return next(v)
}
```

A `RequireScopes`-for-`Write` sketch mirrors the `Read` version exactly
— EXTRACTION-ONLY, same authentication/authorization split (no pass/fail
decision inside `Fn`; `Write` collects grants from every attached
security-shaped `Fn` in a first pass, merges, runs ONE
`middleware.CheckScopes`, and only then calls the real write):

```go
func RequireScopesWrite[T any](schemeName string, scheme route.SecurityScheme, scopes []string, codec *codex.Codec[string], extract func(ctx context.Context, vars map[string]string, v T) (map[string][]string, error)) middleware.Middleware {
    return middleware.Middleware{
        Name:      "require-scopes:" + schemeName,
        Satisfies: []string{schemeName},
        Security:  &middleware.SecurityDeclaration{SchemeName: schemeName, Scheme: scheme, Scopes: scopes, Codec: codec},
        Fn: func(ctx context.Context, vars map[string]string, v T) (map[string][]string, error) {
            return extract(ctx, vars, v)
        },
    }
}
```

This is the same convenience the REST/events Middleware `Fn`s already
have: `extract` here can inspect `v`'s OWN fields directly (e.g.
`v.OwnerID`) instead of needing a separate raw-wire re-derivation, since
`Write`'s `v` is ALREADY the fully-typed value, never a partially-decoded
wire form.

**This sketch is proof, not a commitment to ship in Phase 1** — the
actual `Read`/`Write` signature changes, `MiddlewareShapeError` reuse,
and `ports.Cache`/`ports.SQL` equivalents remain Phase 2 for
IMPLEMENTATION.

## Coverage across every API/port boundary

Originally reviewed against EVERY Layer 2 (request/response or
per-call-invoked) boundary go-codex ships — REST, events, reqreply, MCP,
and ports. **REST and events are now OUT of this doc's scope — both
shipped their own codec-backed Input/Output middleware mechanism (see
the banner above)**; `reqreply` is tracked in its own doc
(`reqreply-workflow-simplification.md`), not here. What remains open,
and is what this doc's coverage table below actually tracks:

**This table does NOT cover Layer 3 (`forge.Registry`/pipelines)** — see
"L14" in "Known limitations" below; forge/pipeline middleware
integration is tracked separately in
[`docs/roadmap/forge-pipeline-middleware.md`](forge-pipeline-middleware.md).

| Boundary | Spec? | Attachment shape | `Security` available? | `Observability` available? |
|---|---|---|---|---|
| MCP (`api/mcp` + `mcpgo`) | MCP manifest/JSON schema | Decorator-shaped | **No — N/A, permanent design** (unchanged) | Design only, below — not yet implemented |
| `ports.File`/`Cache`/`SQL`/`Dir` | **None — not spec-backed** | Decorator-shaped | Design only — `File` sketched above, not yet implemented | Design only, below — not yet implemented |

Both remaining boundaries share the SAME **decorator-shaped** attachment
variant — no raw wire-request object exists as a distinct thing worth
exposing (MCP arguments are already `map[string]any`; ports vars are
already a plain `map[string]string`): `func(ctx, args/vars, *T, next
func(...) (T, error)) (T, error)`-style, the SAME shape `ports.File`'s
sketch above uses. (REST's HTTP-shaped and events/reqreply's
message-shaped variants — the other two of the three attachment shapes
this doc originally identified — are no longer relevant here; see
d-0001/d-0002/d-0003 and `reqreply-workflow-simplification.md` for their
actual shipped/tracked shapes.)

### MCP (decorator-shaped, observability ONLY — no Security)

Covers ALL THREE MCP surfaces uniformly — `ToolHandler`,
`ResourceHandler`, AND `PromptHandler` — confirmed via code to already
share the IDENTICAL observability pattern verbatim (resolve `obs`, start
a `TraceObserver` span, call `RecordRequest("<kind>", name, status,
duration)` on every path, differing only in the `<kind>`
`"tool"`/`"resource"`/`"prompt"` string and the identifying name). See
"L5" in "Known limitations" below for the full resolution.

- `mcpgo.Observability(obs) middleware.Middleware{Fn: obs}` —
  carries the RAW `stats.Observer` value directly; NO decorator/wrapping
  logic is needed at all, because `ToolHandler`/`ResourceHandler`/
  `PromptHandler` ALREADY contain the full observability logic inline —
  only the SOURCE of `obs` changes (a middleware slot instead of
  `Options.Observer`). **NO ctx-ferry needed either** — all three are
  SINGLE-STAGE decodes (unlike REST's 5-stage pipeline), so the terminal
  outcome is already visible without smuggling intermediate events out
  via `ctx`.
- `ToolHandler`/`ResourceHandler`/`PromptHandler` each gain a variadic
  `mws ...middleware.Middleware` parameter (replacing `Options.Observer`);
  `RegisterTool`/`RegisterResource`/`RegisterPrompt` forward it through
  unchanged.
- **Explicitly confirmed, NOT changed by this design**: NO
  `mcpgo.RequireScopes`/`Security` field use for MCP tools/resources/
  prompts — `api/mcp` has no security methods at all, by PERMANENT
  design (host-application-managed auth; see the skill's own
  Gotchas list). The middleware mechanism doesn't reopen this
  decision — it simply gives MCP its fair share of the OTHER capability
  (observability) via the SAME shared `middleware.Middleware` type,
  using the decorator shape (the only shape relevant to MCP/ports).
- **Attachment point**: ONE, not two, for ALL THREE surfaces — MCP has
  no separate "declaration time" builder snapshot comparable to REST's
  `.Register(builder)` spec-freezing (there is no security scheme
  concept for it to feed anyway), so middleware attaches directly at
  `mcpgo.ToolHandler`/`ResourceHandler`/`PromptHandler`/`RegisterTool`/
  `RegisterResource`/`RegisterPrompt` call time, exactly like
  `ports.File` below.

### Ports — `File`/`Cache`/`SQL`/`Dir` (decorator-shaped, `File`'s shape extends mechanically)

`ports.File`'s Read/Write decorator shape (sketched above) is the
TEMPLATE for every other port — confirmed via code inspection:
`ports.File`/`adapters/sql` already call `RecordValidationError` from a
SINGLE decode/validate call (not REST's multi-stage pipeline), so NO
`stats.Diagnostic` ferry is needed for ANY port — a plain decorator
wrapping the whole operation already sees the terminal `(T, error)`.

- `ports.Cache[T]` (`Get`/`Set`/`Del`), a SQL-port equivalent
  (`Query`/`Insert`), and `ports.Dir` (`List`) all get the MECHANICALLY
  IDENTICAL decorator shape as `File`'s `Read`/`Write` sketch above,
  parameterized by their own operation signature — no new design
  question per port, a straightforward mechanical extension.
- `ports.Observability[T]` — a decorator calling
  `RecordFileRead`/`RecordFileWrite` (or the `CacheObserver`/`SQLObserver`
  equivalent per port) directly around `next()` — no ctx-ferry, matching
  the SAME single-stage reasoning as MCP above.
- `ports.RequireScopes[T]` is ALREADY sketched for `File` above; it
  generalizes to `Cache`/`SQL`/`Dir` the same mechanical way.
- Same as MCP: ONE attachment point (no spec to feed) — middleware
  attaches directly at the `Read`/`Write`/`Get`/`Set`/`Query`/`List` call
  site.

### `ports.File`'s equivalent — no ferry needed

`FileObserver` is boundary-shaped only (`RecordFileRead`/`RecordFileWrite`
take `path`/`success`/`duration` — no field-level detail comparable to a
multi-stage decode pipeline's per-field validation errors), so
`ports.File`'s own observability middleware needs NO ctx-ferry mechanism
— it is a plain decorator, structurally identical to the
`RequireScopes[T]` sketch above, just calling `RecordFileRead`/
`RecordFileWrite` directly around `next()`:

```go
// ports — no ctx-ferry needed; FileObserver has no field-level events.
func Observability[T any](obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{
        Name: "observability",
        Fn: func(ctx context.Context, vars map[string]string, next func() (T, error)) (T, error) {
            start := time.Now()
            v, err := next()
            if fo, ok := obs.(stats.FileObserver); ok {
                fo.RecordFileRead(vars["path"], err == nil, time.Since(start))
            }
            return v, err
        },
    }
}
```

## Known limitations and open risks

This is an ACTIVE PUNCH LIST, not a closing critique — each item below
is planned to be tackled (eliminated or mitigated) in a dedicated future
round, one (or a few) at a time. Read this before implementing anything
in this doc; it exists to prevent the design's otherwise-affirmative
tone from hiding real, confirmed trade-offs.

**Numbering preserved from the doc's original, larger L1-L14 list** —
L1-L4, L6-L10, and L13 were all REST-specific findings, since resolved
and shipped (via d-0001/d-0003); they have been removed from this
trimmed doc as historical noise rather than renumbered, to avoid
breaking the existing `dynamic-port-rebinding.md` cross-reference to
"L11" specifically. Only the items still relevant to THIS doc's
narrowed MCP/ports scope remain below: L5, L11, L12, L14.

### L5 — MCP coverage: Resources and Prompts

**Status:** RESOLVED (this round) — simpler than expected: MCP needs NO
decorator/wrapping mechanism at all, just a change to WHERE the
`Observer` value is resolved from.

**Original problem:** confirmed via code (`adapters/mcpgo/adapter.go`)
— the "Coverage across every API/port boundary" section only addressed
`mcpgo.ToolHandler`. `api/mcp`/`adapters/mcpgo` ALSO has
`ResourceHandler`/`PromptHandler`, each with their OWN
`RecordRequest("resource"/"prompt", ...)` Observer call site — a real,
concrete omission, not a hypothetical one; the doc's coverage claim was
OVERSTATED.

**Confirmed via code: all three already share the IDENTICAL pattern,
verbatim** — `ToolHandler`, `ResourceHandler`, `PromptHandler` each do:

```go
obs := opts.Observer
if obs == nil {
    obs = stats.ObserverFromContext(ctx)
}
start := time.Now()
if to, ok := obs.(stats.TraceObserver); ok {
    ctx = to.StartSpan(ctx, "mcp.<kind>", name)
    defer func() { to.EndSpan(ctx, err) }()
}
// ... call fn, encode, on EVERY path:
obs.RecordRequest("<kind>", name, statusCode, time.Since(start))
```

— only the `<kind>` string (`"tool"`/`"resource"`/`"prompt"`) and the
identifying `name` differ. MCP's observability logic is ALREADY fully
written, uniform, and correct across all three surfaces — nothing was
functionally missing.

**Resolution — no decorator needed, just a resolution-source swap:**
since the observability LOGIC already lives inline in each of
`ToolHandler`/`ResourceHandler`/`PromptHandler` (unlike REST, which
needed a NEW `stats.Diagnostic` ctx-ferry for decode-intrinsic events
with no prior home), MCP needs ONLY a change to WHERE `obs` comes from —
not a new wrapping mechanism:

```go
// mcpgo.Observability carries a raw stats.Observer value
// INSIDE a middleware.Middleware wrapper — Fn IS the Observer itself,
// not a closure around it. No decorator/wrapping logic is needed,
// because ToolHandler/ResourceHandler/PromptHandler already CONTAIN the
// full observability logic inline; only the SOURCE of obs changes.
func Observability(obs stats.Observer) middleware.Middleware {
    return middleware.Middleware{Name: "observability", Fn: obs}
}
```

`ToolHandler`/`ResourceHandler`/`PromptHandler` each gain a variadic
`mws ...middleware.Middleware` parameter (replacing `Options.Observer`,
consistent with its removal everywhere else in this doc); internally,
the existing resolution becomes:

```go
var obs stats.Observer
for _, mw := range mws {
    if o, ok := mw.Fn.(stats.Observer); ok {
        obs = o
        break
    }
}
if obs == nil {
    obs = stats.ObserverFromContext(ctx)
}
```

EVERYTHING ELSE (the `TraceObserver` span, the `RecordRequest` calls, the
`<kind>`/`name` strings) is COMPLETELY UNCHANGED — a one-line resolution
swap applied IDENTICALLY in all three functions, not a redesign.
`RegisterTool`/`RegisterResource`/`RegisterPrompt` forward `mws` through
unchanged. The coverage claim now GENUINELY covers all three MCP
surfaces, not just tools.

### L11 — Unreconciled overlap with `dynamic-port-rebinding.md`

**Status:** RESOLVED.

**Problem:** confirmed via code — `docs/roadmap/dynamic-port-rebinding.md`
explicitly motivates itself with "credential rollover... without
process restart" for `ports` — DIRECTLY overlapping with what security
middleware in THIS doc is designed to solve. The two roadmap docs have
NEVER cross-referenced each other despite this motivating overlap.

**Why it matters:** since `ports`' middleware attaches PER-CALL (not
baked into an immutable handle — see "Ports get the same treatment"
above), it can ALREADY be swapped freely between calls with ZERO new
mechanism — `dynamic-port-rebinding.md`'s hot-swap concept may be
PARTIALLY REDUNDANT for the "rotate a port's security middleware"
use case specifically (though still needed for swapping the underlying
TRANSPORT adapter itself, a different concern). Meanwhile REST/events/
reqreply bake middleware into an IMMUTABLE `RouteHandle` at
`.Register(builder)` time — credential rollover for THOSE boundaries
has NO hot-swap story at all today, a genuine gap neither doc currently
owns.

**Resolution — lightweight cross-reference, no new mechanism (confirmed
with the user):**

- `ports`' credential-rollover story is ALREADY solved, today, by this
  doc's per-call middleware attachment (see "Ports get the same
  treatment" above) — a `ports.RequireScopes[T]`/credential-providing
  middleware value can be swapped between calls with ZERO new
  mechanism, since it is never baked into an immutable handle the way
  `RouteHandle` is. `dynamic-port-rebinding.md` remains necessary ONLY
  for swapping the underlying TRANSPORT ADAPTER itself (broker
  failover, endpoint rotation, phased migration) — a genuinely
  different concern from credential rotation, and `dynamic-port-
  rebinding.md` has been updated to say so explicitly (see its
  Motivation section).
- REST/events/reqreply's IMMUTABLE `RouteHandle.Middlewares` (frozen at
  `.Register(builder)` time) has NO hot-swap story today — this is an
  explicitly acknowledged, OUT-OF-SCOPE gap for BOTH docs, left for a
  future round if ever prioritized (not designed now — no user need for
  it currently, since REST/events/reqreply middleware rotation, if
  ever needed, would look like `dynamic-port-rebinding.md`'s own
  `Rebind`-style mechanism applied to a route/channel's handle, not a
  `ports`-specific concern).

No new mechanism was designed or implemented — this closes the gap
purely via cross-referencing and explicit scope acknowledgment between
the two roadmap docs.

### L12 — MCP's observability resolution silently drops all but the first matching middleware

**Status:** RESOLVED.

**Problem:** L5's resolution has `ToolHandler`/`ResourceHandler`/
`PromptHandler` scan `mws` for a `stats.Observer`-typed `Fn` and take
the FIRST match via `break`:

```go
var obs stats.Observer
for _, mw := range mws {
    if o, ok := mw.Fn.(stats.Observer); ok {
        obs = o
        break // ← the SECOND stats.Observer-typed middleware is silently ignored
    }
}
```

If a caller attaches TWO `mcpgo.Observability` values (e.g.
one wrapping a metrics collector, one wrapping a tracer, a very
plausible real setup mirroring how `chi`/`nethttp` freely stack
multiple general-purpose middlewares today), only the FIRST is ever
consulted — the second is silently dropped, with no error, no warning,
no merge.

**Why it matters:** this is the EXACT class of bug this entire design
exists to eliminate — silently dropping an attached cross-cutting
concern with no error, no warning. Every OTHER boundary this doc has
covered composes multiple attached middlewares of the same kind
correctly: HTTP general-purpose middlewares nest (each wraps the next,
none are dropped); `ports.File`'s decorator shape nests the same way;
security `Fn`s MERGE their grants (REST's own history — see
d-0001/d-0003); client-side credential `Fn`s MERGE their headers the
same way. MCP's shortcut ("no decorator needed, just swap the
resolution source") is the ONE place in the entire design where
attaching two of the same kind of middleware silently loses one of
them. **Correction (caught during Phase 1 implementation): `stats`
ALREADY HAS exactly this fan-out helper** — `stats.NewFanout(observers
...Observer) Observer` (`stats/observer.go`) already fans out
`RecordRequest`/`RecordValidationError` unconditionally and every
optional sub-interface (`SecurityObserver`, `TraceObserver`,
`FileObserver`, `SQLObserver`, `CacheObserver`,
`CredentialCacheObserver`, `PipelineObserver`, `StreamObserver`,
`ReloadObserver`, `InvalidateObserver`) via a per-call type-assertion
guard — shipped, tested, and already used elsewhere in this codebase
(see `stats/doc.go`). The original L12 discovery (missed this on first
pass) proposed inventing a NEW `stats.MultiObserver` type; that was
unnecessary duplication of existing, working code.

**Resolution — reuse the EXISTING `stats.NewFanout`, no new `stats`
type needed:** MCP's observability resolution simply collects EVERY
`stats.Observer`-typed `Fn` (not just the first) and passes them ALL to
`stats.NewFanout` when there's more than one, instead of `break`-ing on
the first match:

```go
// mcpgo — collects EVERY stats.Observer-typed Fn (not just the
// first), combining them via the EXISTING stats.NewFanout when more
// than one is found. Everything else in ToolHandler/ResourceHandler/
// PromptHandler is UNCHANGED — obs is still a single stats.Observer
// value by the time the existing TraceObserver/RecordRequest logic runs.
var observers []stats.Observer
for _, mw := range mws {
    if o, ok := mw.Fn.(stats.Observer); ok {
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

This is a strict superset of L5's original one-line swap (the zero-
and single-observer cases behave IDENTICALLY to before; only the N>1
case, previously silently dropped, is now handled). A caller can also
pre-build a `stats.NewFanout(...)` themselves and pass it as the SOLE
observability middleware, whenever they want explicit control over
fan-out order — both paths converge on the same result. `stats`
required ZERO new code for this fix — only `adapters/mcpgo`'s
resolution loop changes (Phase 2, when MCP middleware coverage is
implemented).

### L14 — forge/Registry (Layer 3 pipelines) — out of scope, tracked separately

**Status:** RESOLVED — spun out into
[`docs/roadmap/forge-pipeline-middleware.md`](forge-pipeline-middleware.md).
Layer 3 (`forge.Registry`/pipelines — `forge.NewFunction`, `Compose`,
`Registry.Apply`) was never covered by this doc and is not in scope here
or in its narrowed MCP/ports form — forge already has its own parallel
cross-cutting mechanism (`stats.PipelineObserver.RecordApply`, wired via
the explicit `Registry.WithObserver` builder method, deliberately without
context integration, by design). Whether/how forge should ALSO gain a
`middleware`-style attachment point is tracked entirely in the spun-out
doc, not here.

