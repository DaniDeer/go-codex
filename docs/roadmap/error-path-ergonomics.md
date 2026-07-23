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

### 1) HTTP server routes (`api/rest`, `adapters/nethttp`, `adapters/chi`)

```go
// General route-level mapping, usable by both Handler and PipelineHandler.
func ErrorStatus[E error](status int) RouteOpt

// Optional declarative body mapping for typed error envelopes.
func ErrorBody[E error, B any](
    status int,
    codec codex.Codec[B],
    mapFn func(E) B,
) RouteOpt
```

Route handle lookup:

```go
func (h *RouteHandle[Req, Resp]) ErrorStatusFor(err error) (status int, ok bool)
```

Adapter behavior target:
- `Handler` and `PipelineHandler` both consult `ErrorStatusFor`.
- `PipelineNoResponseError` keeps current default mapping (503) but remains
  overridable via route declaration.
- `Options.ErrorHandler` remains escape hatch and envelope shaper.

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
- MQTT5 / ZeroMQ req-reply: consistent encoded error envelope on responder side,
  still surfaced by existing typed wrappers (`CallError`, `ServeError`).
- MCP: consistent typed tool-error payload mapping.

### 3) Ports integration

`ports.RESTPattern`, `ports.ReqReplyPattern`, and `ports.MCPPattern` should
accept the same declaration style via existing builder opts, preserving the
declare -> plugin -> bind workflow for happy and error paths.

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
| EPU5 | Error body success | `ErrorBody` writes typed envelope |
| EPU6 | Error body encode failure | typed apply/encode error, fallback behavior |
| EPU7 | Errors.As chain | New error wrappers remain introspectable |
| EPU8 | LogValue group shape | Keys/kinds for new errors are stable |
| EPU9 | Ports parity | Same declaration path works via `PluginRESTPattern` |
| EPU10 | Req/reply cross-adapter consistency | `mqtt5`/`zeromq`/`mcpgo` mapping model stays coherent |

## Files to create

| File | Responsibility |
|---|---|
| `docs/roadmap/error-path-ergonomics.md` | Design source of truth |
| `api/rest/error_opts.go` | `ErrorStatus` / `ErrorBody` declarations and validation |
| `api/rest/error_opts_test.go` | Unit tests for declaration + lookup |
| `adapters/nethttp/error_mapping.go` | Apply route-level mapping in plain + pipeline handlers |
| `adapters/chi/error_mapping.go` | Same mapping behavior as nethttp |
| `adapters/nethttp/error_mapping_test.go` | Runtime status/body mapping tests |
| `adapters/chi/error_mapping_test.go` | Runtime parity tests |
| `docs/features/rest-api.md` | Unified happy-path + error-path declaration docs |
| `docs/features/error-handling.md` | Updated cross-adapter matrix |
| `docs/guides/http-server.md` | Step-by-step ergonomic workflow + migration notes |

## Out of scope (Phase 2)

- Unified pub/sub retry policy DSL (`retry`, `drop`, `dlq`) across adapters.
- Automatic client/server negotiation of error contracts.
- Mandatory retrofit of every existing example package in one release.
- Removal of existing adapter escape hatches before migration evidence.

## Open design decisions (to resolve before/during implementation)

1. Naming convergence:
   - keep `PipelineErrorStatus` and add `ErrorStatus`,
   - or converge to `ErrorStatus` with deprecation path.
2. `ErrorBody` timing:
   - include in Phase 1,
   - or ship status unification first and defer body contract.
3. Req/reply envelope placement:
   - core shared type,
   - or adapter-local envelope with shared semantic docs.
4. Ports ergonomics:
   - RouteOpt-only declaration through builders,
   - or dedicated ports helper wrappers.
