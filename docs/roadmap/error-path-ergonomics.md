# Error Path Ergonomics Unification — `api/rest`, adapters, ports

> **Status:** In progress — req/reply dedicated AsyncAPI error-reply channels implemented; remaining phases open.
> [← Back to Roadmap](index.md)

## Motivation

go-codex already provides a strong happy-path workflow: declare request/response
codecs once, wire adapters, run. Error-path ergonomics are still fragmented:
HTTP no-pipeline usually maps in `ErrorHandler`, HTTP pipeline can additionally
map with `PipelineErrorStatus`, MQTT/ZeroMQ/MCP rely on callback-style
transport errors. This plan unifies the **mental model** so users can declare
error behavior with the same clarity they already use for req/resp contracts,
while keeping escape hatches.

Target UX:
- Error contracts are codec-first, like request/response contracts.
- A pipeline error can travel stream -> port -> adapter and be emitted as a
  declared typed error payload (direct match or mapped payload).

## Scope decisions (what's in Phase 1, what's deferred)

| In scope (Phase 1) | Out of scope |
|---|---|
| Additive unification layer (no breaking removals) | Removing `ErrorHandler` / `OnError` in Phase 1 |
| Declarative typed error mapping for HTTP plain + pipeline paths | Full migration of all examples in one pass |
| Ports-compatible declaration model for REST/ReqReply/MCP patterns | New transport adapters |
| Cross-adapter design for req/reply families (`nethttp` client, `mqtt5`, `zeromq`, `mcpgo`) | Pub/sub retry engine + DLQ policy DSL |
| Documentation matrix for happy/error workflows by adapter family | Protocol-specific advanced policies |

## Toolchain / dependency decisions

- No new external dependency.
- Reuse current contracts:
  - typed `RouteOpt` style declarations in `api/rest`,
  - structured errors implementing `slog.LogValuer`,
  - observer propagation through `stats.ReportErrors` and existing observer
    methods (`RecordRequest`, `RecordPublish`, `RecordSubscribe`).
- Keep current adapter-level escape hatches as supported fallback paths.

## API surface

Phase 1 introduces additive declarations and reuses existing runtime wiring.

### 0) Shared error-contract model (all ports/adapters)

```go
// Generic declaration shape (names may vary by package):
type ErrorPattern[E error, B any] struct {
    Codec codex.Codec[B]     // declared payload schema
    Map   func(E) (B, error) // optional mapping handle
}
```

Two modes:
1. Direct match: `E` itself matches the declared codec/schema.
2. Mapped payload: adapter uses `Map(E)` and encodes returned typed payload.

### 0.1) Shared error action model (respond / handle / log)

```go
type ErrorAction string

const (
    ErrorRespond ErrorAction = "respond" // emit typed error payload
    ErrorHandle  ErrorAction = "handle"  // run custom handling function
    ErrorLog     ErrorAction = "log"     // observer/log only
)
```

Intent:
- Keep one declaration shape across transports.
- Let adapters realize `respond` according to communication pattern capability.

Default action policy (when action not explicitly set):
- REST + ReqReply: `respond`.
- Events/pub-sub: `respond` via dedicated declared error channel/pattern.
- Other boundaries without response/error-output path: `log`.

Action composition rule:
- One matched pattern chooses one primary action only:
  - `respond` OR `handle` OR `log`.
- No implicit handle-then-respond chaining on one matched pattern.
- If both behaviors are wanted, users declare separate patterns with explicit
  precedence.

### 0.2) Adapter/port capability matrix (explicit coverage)

| Family | Examples | Default action | Notes |
|---|---|---|---|
| REST request/response | `nethttp`, `chi`, `ToolPort + PipelineAdapter` | `respond` | Mandatory typed error response path. |
| ReqReply | `mqtt5` call/serve, `zeromq` call/serve | `respond` | Dedicated reply-error schema/channel rendering. |
| MCP tool-like | `mcpgo` tool pipeline boundaries | `respond` | Treated as req/reply-like for error output. |
| Events pub/sub | `mqtt`, `mqtt5`, `zeromq` publish/subscribe | `respond` via error channel | No synchronous caller response; publish typed error payload. |
| WebSocket stream | Duplex/Broadcast/Ingest socket boundaries | `respond` via error channel | Event-like semantics over persistent session transport. |
| SQL store IO/sink | Query/Insert/Drain patterns | `handle` / `log` | Internal boundary; optional `respond` only via explicit error-output channel. |
| Cache IO/sink | Redis Get/Set/DrainSet patterns | `handle` / `log` | Internal boundary; optional `respond` only via explicit error-output channel. |
| File IO/sink | Scan/Watch/Read/Write/Drain patterns | `handle` / `log` | Internal boundary; optional `respond` only via explicit error-output channel. |

### 1) HTTP server routes (`api/rest`, `adapters/nethttp`, `adapters/chi`)

```go
// General route-level mapping, usable by both Handler and PipelineHandler.
func ErrorStatus[E error](status int) RouteOpt

// Compatibility alias during migration.
// Deprecated: use ErrorStatus.
func PipelineErrorStatus[E error](status int) RouteOpt

// Codec-first typed error payload contract.
func ErrorPattern[E error, B any](
    status int,
    codec codex.Codec[B],
    mapFn ...func(E) (B, error), // optional mapper
) RouteOpt
```

REST parity requirement (happy path vs error path):
- Error responses must support the same one-struct-one-call ergonomics already
  used for happy-path response metadata (e.g. response headers/cookies declared
  via codecs/merge fields).
- Users should be able to declare typed error response body AND typed error
  response headers/cookies in one declarative contract, not imperative
  `ErrorHandler` map mutations only.

Route handle lookup:

```go
func (h *RouteHandle[Req, Resp]) ErrorStatusFor(err error) (status int, ok bool)
```

Adapter behavior target:
- `Handler` and `PipelineHandler` both consult `ErrorStatusFor`.
- `PipelineNoResponseError` keeps current default mapping (503) but remains
  overridable via route declaration.
- `ErrorPattern` controls typed status+payload selection before `ErrorHandler`.
- default action is `respond` (typed error response body).
- error response headers/cookies should be codec-declared and encoded through the
  same merge-field style as happy path (one-struct-one-call parity).
- `Options.ErrorHandler` remains final envelope/serialization escape hatch.

### 2) Req/reply adapter families (`nethttp` client, `mqtt5`, `zeromq`, `mcpgo`)

Phase 1 defines shared declaration vocabulary and transport-local rendering.

```go
type ErrorSpec[E error] struct {
    Code        string // e.g. "conflict", "validation", "internal"
    Description string
}
```

Planned mapping:
- HTTP client: richer typed non-2xx decode path (building on `UnexpectedStatusError`).
- MQTT5 / ZeroMQ req-reply: adapter-local error envelopes with shared semantics/docs,
  still surfaced by existing typed wrappers (`CallError`, `ServeError`) and tied
  to declared error reply schemas/channels.
- MCP: consistent typed tool-error payload mapping.
- default action is `respond` where a reply path exists.

### 3) Ports integration

`ports.RESTPattern`, `ports.ReqReplyPattern`, and `ports.MCPPattern` preserve
RouteOpt declaration compatibility through builder opts. In Phase 1, add
ports-focused helper wrappers on top for ergonomics (without removing RouteOpt path).

Required propagation model:
- stream/pipeline errors travel through existing error channels to port/adapter.
- first matching declared error pattern determines output payload contract.
- non-request-reply Source/Sink pub-sub boundaries emit to a dedicated declared
  error channel/pattern by default.
- same declaration also supports explicit `handle` (custom callback path) and
  `log` (observer/log only) actions.
- store/IO-only boundaries (SQL/Cache/File) typically propagate typed errors up
  to an outer responder (for example REST), which performs caller-facing status/body output.

## Structured errors (all implement `slog.LogValuer`)

Planned new types:

1. `rest.ErrorMappingConflictError`
   - Fields: `Route string`, `ErrorType string`, `Err error`
2. `rest.ErrorBodyEncodeError`
   - Fields: `Route string`, `Status int`, `Err error`
3. Adapter apply errors (e.g. `nethttp.ErrorSpecApplyError`, `chi.ErrorSpecApplyError`)
   - Fields: `Path string`, `Status int`, `Err error`

All wrappers include:
- `Error() string`
- `Unwrap() error`
- `LogValue() slog.Value`

## Observer integration

- Reuse existing observer interfaces in Phase 1.
- HTTP adapters should record the **final mapped status code** in `RecordRequest`.
- Existing validation reporting stays intact (`stats.ReportErrors` with current
  location vocabulary).
- Req/reply adapters continue using current request/publish/subscribe observer
  calls; mapped error outcomes propagate through those paths.

## Unit test plan

| ID | Name | Verifies |
|---|---|---|
| EPU1 | Plain handler mapping | `ErrorStatus` applies to `Handler` path |
| EPU2 | Pipeline handler mapping | `ErrorStatus` applies to `PipelineHandler` path |
| EPU3 | First-match precedence | Deterministic ordering for multiple mappings |
| EPU4 | No-response default + override | `PipelineNoResponseError` default and explicit override |
| EPU5 | Error payload success | direct-match and mapped `ErrorPattern` payload paths encode through declared codec |
| EPU6 | Error payload encode failure | typed apply/encode error and fallback path |
| EPU7 | Errors.As chain | New error wrappers remain introspectable |
| EPU8 | LogValue group shape | Keys/kinds for new errors are stable |
| EPU9 | Ports parity | Same declaration path works via `PluginRESTPattern` |
| EPU10 | Req/reply cross-adapter consistency | `mqtt5`/`zeromq`/`mcpgo` mapping model stays coherent |
| EPU11 | Non-req/reply error output channel | Source/Sink pub-sub boundaries emit codec-backed error payloads on declared error channel/pattern |
| EPU12 | Action default resolution | REST/ReqReply default `respond`; Events default `respond` via error channel; unsupported boundaries default `log` |
| EPU13 | Action override behavior | Explicit `handle` and `log` override defaults consistently across adapters |
| EPU14 | Single-action rule | Matched pattern executes exactly one action path (respond OR handle OR log) |
| EPU15 | REST error header/cookie parity | Typed error response can populate declared response headers/cookies via codec merge, mirroring happy path |

## Files to create

| File | Responsibility |
|---|---|
| `docs/roadmap/error-path-ergonomics.md` | Design source of truth |
| `api/rest/error_opts.go` | `ErrorStatus` + `ErrorPattern` declarations; `PipelineErrorStatus` alias/deprecation |
| `api/rest/error_opts_test.go` | Unit tests for declaration + lookup (direct + mapped error payload paths) |
| `adapters/nethttp/error_mapping.go` | Apply route-level mapping in plain + pipeline handlers |
| `adapters/chi/error_mapping.go` | Same mapping behavior as nethttp |
| `adapters/nethttp/error_mapping_test.go` | Runtime status/body mapping tests |
| `adapters/chi/error_mapping_test.go` | Runtime parity tests |
| `ports/*` (targeted) | Port-level error-pattern helper wrappers + propagation surfaces |
| `adapters/*` (targeted) | Per-adapter action resolution (`respond` / `handle` / `log`) with capability-aware defaults |
| `docs/features/rest-api.md` | Unified happy-path + error-path declaration docs |
| `docs/features/error-handling.md` | Updated cross-adapter matrix |
| `docs/guides/http-server.md` | Step-by-step ergonomic workflow + migration notes |

## Out of scope (Phase 2)

- Unified pub/sub retry policy DSL (`retry`, `drop`, `dlq`) across adapters.
- Automatic client/server negotiation of error contracts.
- Mandatory retrofit of every existing example package in one release.
- Removal of existing adapter escape hatches before migration evidence.

## Phased rollout (step-by-step, consistency-preserving)

1. **Phase 1A — Core semantics**
   - shared `ErrorPattern` + `ErrorAction` declarations
   - REST + ReqReply + MCP tool-like boundaries
2. **Phase 1B — Stream/event boundaries**
   - Events pub/sub + WebSocket + Source/Sink error-channel propagation
3. **Phase 1C — Store/IO boundaries**
   - SQL + Cache defaults (`handle`/`log`) + optional error-output channel path
4. **Phase 1D — Consistency lock**
   - cross-adapter docs matrix, parity tests, examples
   - same user workflow required before phase complete:
     declare pattern -> choose action -> optional mapper -> predictable output

## Design decisions (resolved)

1. Naming convergence:
   - `ErrorStatus` is primary.
   - `PipelineErrorStatus` remains as deprecated compatibility alias.
2. Codec-first error payload timing:
   - included in Phase 1 via `ErrorPattern` (direct + mapped modes).
3. Req/reply envelope placement:
   - adapter-local runtime envelopes with shared semantic docs.
4. Ports ergonomics:
   - keep RouteOpt-compatible builder path and add dedicated ports helper wrappers in Phase 1.
5. AsyncAPI error path:
   - dedicated reply-error channel/operation (implemented in reqreply path).
6. Non-request-reply boundaries:
   - emit codec-backed error payloads to dedicated declared error channel/pattern by default.
7. Default action policy:
   - REST + ReqReply default `respond`;
   - Events default `respond` via error channel;
   - otherwise default `log`.
8. Store/IO boundaries:
   - SQL/Cache/File default to `handle`/`log`;
   - caller-facing response happens at outer response boundary (e.g. REST).
9. Action composition:
   - one matched pattern executes one primary action only (`respond` OR `handle` OR `log`).
