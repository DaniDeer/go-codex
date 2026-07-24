# Error Path Ergonomics Unification — `api/rest`, adapters, ports

> **Status:** Phases 1A–1D and Phase 2 (items 1–5) shipped. REST
> `ErrorStatus`/`ErrorPattern` (with full `respond`/`handle`/`log` action
> selector), events `ErrorChannel`, websocket `ErrorFrame`, SQL/Cache/File
> `handle`/`log` composition, ports parity tests, the cross-adapter docs
> matrix, reqreply runtime error-pattern matching (`mqtt5`/`zeromq`
> Serve/ServeRouter), MCP tool error-pattern declaration, and `mqtt`/`zeromq`
> pub/sub `ErrorResponseFor` adoption are all in place, with runnable example
> coverage across REST/events/websocket/store-IO boundaries. Remaining
> scoped follow-up (deferred, not a regression): client-side error decode
> parity (`nethttp.Call`) — no concrete driving use case yet;
> `UnexpectedStatusError.Body` already works as an escape hatch.
> [← Back to Roadmap](index.md)

## Motivation

go-codex already provides a strong happy-path workflow: declare request/response
codecs once, wire adapters, run. Error-path ergonomics are still fragmented:
HTTP no-pipeline usually maps in `ErrorHandler`, HTTP pipeline can additionally
map with `ErrorStatus`, MQTT/ZeroMQ/MCP rely on callback-style
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

| Family | Examples | Default action | Status | Notes |
|---|---|---|---|---|
| REST request/response | `nethttp`, `chi`, `ToolPort + PipelineAdapter` | `respond` | **Shipped (1A)** | `rest.ErrorStatus`/`rest.ErrorPattern`; response header/cookie parity; ports parity via `PluginRESTPattern`. |
| ReqReply | `mqtt5` call/serve, `zeromq` call/serve | `respond` | **Shipped** | Dedicated reply-error schema/channel rendering (`reqreply.ErrorReplyMeta`). |
| MCP tool-like | `mcpgo` tool pipeline boundaries | `respond` | Open | Treated as req/reply-like for error output; not yet retrofitted with a dedicated typed-error declaration. |
| Events pub/sub | `mqtt5`, `mqtt`, `zeromq` (all wired) | `respond` via error channel | **Shipped (1B + 2)** | `events.ErrorChannel`; full `respond`/`handle`/`log` action model; ports parity via `PluginEventPattern`; all three PublishAdapters consult `ErrorResponseFor`. |
| WebSocket stream | Duplex socket boundaries | `respond` via error channel (broadcast) | **Shipped (1B)** | `websocket.ErrorFrame` + `DuplexSocketAdapterOptions.ErrorFrames`; broadcast is the notification path (no dedicated topic). |
| SQL store IO/sink | Insert/Drain patterns | `handle` / `log` | **Shipped (1C)** | Existing `OnError` IS `handle`; composes with `events.ErrorChannel` for `respond`-equivalent. No new SQL-specific API. |
| Cache IO/sink | Redis Set/DrainSet patterns | `handle` / `log` | **Shipped (1C)** | Same composition pattern as SQL. |
| File IO/sink | Write/DrainWrite patterns | `handle` / `log` | **Shipped (1C)** | Same composition pattern as SQL. |

### 1) HTTP server routes (`api/rest`, `adapters/nethttp`, `adapters/chi`)

```go
// Unified route-level error declaration.
func ErrorResponse[E error, B any](
    status int,
    body codex.Codec[B],
    opts ...ErrorResponseOpt[E, B],
) RouteOpt

// Status-only convenience wrapper (no typed body contract).
func ErrorStatus[E error](status int) RouteOpt // sugar for ErrorResponse with no body codec
```

Decision: no compatibility alias was kept — this feature has no external
consumers yet, so `PipelineErrorStatus` was never shipped as public API.
`ErrorStatus` is the single canonical name from the start.

REST parity requirement (happy path vs error path):
- Error responses must support the same one-struct-one-call ergonomics already
  used for happy-path response metadata (e.g. response headers/cookies declared
  via codecs/merge fields).
- Users should be able to declare typed error response body AND typed error
  response headers/cookies in one declarative contract, not imperative
  `ErrorHandler` map mutations only.

`ErrorResponseOpt` decisions (closed for Phase 1):

- `WithErrorMapper(func(E) (B, error))` (optional)
  - absent => direct payload mode (`E` must be encodable by `body` codec).
  - present => mapped payload mode.
- `WithErrorAction(action ErrorAction)` (optional)
  - REST default remains `respond`.
  - `respond` => adapter writes typed error payload using `body` codec.
  - `handle` => adapter invokes custom handler callback and MUST NOT also run
    `respond` for same matched rule.
  - `log` => adapter only records/logs and then falls back to normal adapter
    `ErrorHandler` envelope behavior (no typed `ErrorResponse` body write).
- `WithErrorHandler(func(http.ResponseWriter, *http.Request, int, E))` (optional,
  only used when action=`handle`).
- `WithErrorHeaders(...ResponseHeaderParam)` / `WithErrorCookies(...ResponseCookieParam)` (optional)
  - same codec + merge-field semantics as happy-path response metadata.
  - applied only when the matched action writes a response (`respond` or `handle`).

Route handle lookup:

```go
func (h *RouteHandle[Req, Resp]) ErrorResponseFor(err error) (ErrorResponseMatch, bool)
```

Adapter behavior target:
- `Handler` and `PipelineHandler` both consult `ErrorResponseFor`.
- `PipelineNoResponseError` keeps current default mapping (503) but remains
  overridable via route declaration.
- `ErrorResponse` controls status+payload/action selection before fallback
  adapter `ErrorHandler`.
- default action is `respond` (typed error response body).
- error response headers/cookies should be codec-declared and encoded through the
  same merge-field style as happy path (one-struct-one-call parity).
- `Options.ErrorHandler` remains final envelope/serialization escape hatch.

Precedence + one-action semantics (resolved):

1. Candidate set = all declared `ErrorResponse` + wrappers (`ErrorStatus`,
   `ErrorStatus`) in declaration order.
2. Matching uses `errors.As`.
3. First match wins.
4. Matched rule executes exactly one primary action (`respond` OR `handle` OR
   `log`), never chained implicitly.
5. If no rule matches, keep existing adapter defaults (`500`, plus current
   `PipelineNoResponseError` default `503`).

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

| ID | Name | Verifies | Status |
|---|---|---|---|
| EPU1 | Plain handler mapping | `ErrorStatus` applies to `Handler` path | ✅ `api/rest`, `adapters/nethttp`, `adapters/chi` |
| EPU2 | Pipeline handler mapping | `ErrorStatus` applies to `PipelineHandler` path | ✅ same packages |
| EPU3 | First-match precedence | Deterministic ordering for multiple mappings | ✅ REST + `events.ErrorChannel` (`TestErrorChannel_Precedence_FirstDeclaredWins`) |
| EPU4 | No-response default + override | `PipelineNoResponseError` default and explicit override | ✅ `adapters/nethttp`/`chi` |
| EPU5 | Error payload success | direct-match and mapped `ErrorPattern` payload paths encode through declared codec | ✅ REST + events (`TestErrorChannel_MappedPayload_MatchAndEncode`, `TestErrorChannel_DirectMode_TypeAssignable`) |
| EPU6 | Error payload encode failure | typed apply/encode error and fallback path | ✅ `TestErrorChannel_MapperError_ReturnsMatchedWithError` |
| EPU7 | Errors.As chain | New error wrappers remain introspectable | ✅ |
| EPU8 | LogValue group shape | Keys/kinds for new errors are stable | ✅ `ErrorFrameOptError`, `FormatOptError`-style errors |
| EPU9 | Ports parity | Same declaration path works via `PluginRESTPattern`/`PluginEventPattern` | ✅ `TestRESTPattern_ErrorStatus_ParityWithDirectRouteDeclaration`, `TestEventPattern_ErrorChannel_ParityWithDirectChannelDeclaration` |
| EPU10 | Req/reply cross-adapter consistency | `mqtt5`/`zeromq`/`mcpgo` mapping model stays coherent | Open (not blocking — dedicated reply-error AsyncAPI channels already shipped separately) |
| EPU11 | Non-req/reply error output channel | Source/Sink pub-sub boundaries emit codec-backed error payloads on declared error channel/pattern | ✅ `adapters/mqtt5` (`TestMQTT5PublishAdapter_ErrorChannelMatch_PublishesToDeclaredTopic`), `adapters/websocket` (`TestDuplex_ErrorFrame_Match_BroadcastsToAllSessions`) |
| EPU12 | Action default resolution | REST/ReqReply default `respond`; Events default `respond` via error channel; store/IO boundaries default `handle`/`log` | ✅ events (`ErrorRespond` default), SQL/Cache/File composition tests |
| EPU13 | Action override behavior | Explicit `handle` and `log` override defaults consistently across adapters | ✅ `TestErrorChannel_WithAction_Handle`, `TestErrorChannel_WithAction_Log`, `TestMQTT5PublishAdapter_ErrorChannelHandleAction_NoAutoPublish`, `TestDuplex_ErrorFrame_HandleAction_NoBroadcast` |
| EPU14 | Single-action rule | Matched pattern executes exactly one action path (respond OR handle OR log) | ✅ same tests as EPU13 assert no double-firing |
| EPU15 | REST error header/cookie parity | Typed error response can populate declared response headers/cookies via codec merge, mirroring happy path | ✅ `TestHandler_ErrorPattern_DirectWithResponseHeaderCookieParity_Chi` (and nethttp equivalent) |
| EPU16 | Store/IO composition | SQL/Cache/File `OnError` composes with `events.ErrorChannel` for a `respond`-equivalent, with no new per-adapter API | ✅ `TestDrainInsertAdapter_OnError_ComposesWithEventsErrorChannel`, `TestDrainSetAdapter_OnError_ComposesWithEventsErrorChannel`, `TestDrainWriteAdapter_OnError_ComposesWithEventsErrorChannel` |

## Files to create

| File | Responsibility |
|---|---|
| `docs/roadmap/error-path-ergonomics.md` | Design source of truth |
| `api/rest/error_opts.go` | `ErrorStatus` + `ErrorPattern` declarations; `ErrorStatus` alias/deprecation |
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

1. **Phase 1A — Core semantics (shipped)**
   - `rest.ErrorStatus[E](status)` / `rest.ErrorPattern[E,B](status, codec, mapFn...)`
     RouteOpt declarations; `RouteHandle.ErrorStatusFor`/`ErrorResponseFor` lookups;
     wired into `Handler` and `PipelineHandler` for both `adapters/nethttp` and
     `adapters/chi`; REST error-body response header/cookie parity via the same
     merge-field mechanism as the happy path.
   - Scope note: REST does not yet expose the full `respond`/`handle`/`log`
     action selector described in §0.1 above — a matched `ErrorPattern`
     always writes the typed body directly (`respond`-only). Extending REST
     with explicit action selection (to reach full parity with the Phase 1B
     events/websocket action model) is tracked as a Phase 1D follow-up, not a
     regression — no REST caller-facing behavior existed to preserve.
2. **Phase 1B — Stream/event boundaries (shipped)**
   - `events.ErrorChannel[E,B](topic, codec, mapFn...)` ChannelOpt +
     `ChannelHandle.ErrorResponseFor` lookup, with the full three-way
     `respond`/`handle`/`log` action model (`events.ErrorAction`).
   - `adapters/mqtt5.PublishAdapter` wired as the reference adapter: consults
     `handle.ErrorResponseFor` before falling back to `OnError` (`OnError`
     already realizes the `handle` action — no new adapter option needed).
     Other pub/sub adapters (`mqtt`, `zeromq`) can adopt the same lookup at
     their own publish/sink call sites as a follow-up (not blocking — the
     declarative surface lives entirely in `api/events`, adapter wiring is
     additive per adapter).
   - `websocket.ErrorFrame[E,Out](mapFn...) ErrorFrameRule[Out]` +
     `DuplexSocketAdapterOptions.ErrorFrames` (type-erased `any`, same
     pattern as events Formats) wired into `adapters/websocket`'s
     `DuplexSocketAdapter`: a matched `respond` rule broadcasts the mapped
     frame to every connected session (no dedicated error topic exists on a
     socket — broadcast IS the notification path); `handle` runs
     `.WithHandle(func(error))`; `log` and unmatched errors fall through to
     the existing default (forwarded unchanged to the port's Errors channel).
3. **Phase 1C — Store/IO boundaries (shipped)**
   - SQL/Cache/File default to `handle`/`log` via their EXISTING `OnError func(error)`
     hooks (`sql.DrainInsertOptions`, `redis.SetAdapterOptions`,
     `file.DrainWriteAdapterOptions`/`DrainWriteFileAdapterOptions`) — no new
     adapter API needed, since `OnError` already generically covers "handle"
     (nil = "log").
   - The "optional respond via explicit error-output channel" path is achieved
     by COMPOSING that `OnError` callback with a declared Phase 1B
     `events.ErrorChannel` — the declarative pattern lives entirely in
     `api/events`; the store/IO adapter's `OnError` is the only integration
     point needed. Locked by composition tests in `adapters/sql`,
     `adapters/redis`, and `adapters/file` `binding_test.go`.
   - This deliberately avoids introducing per-boundary
     `sql.ErrorChannel`/`redis.ErrorChannel`/`file.ErrorChannel` types — SQL/
     Cache/File have no channel/topic concept of their own, so duplicating
     the declaration surface per adapter would violate the "no speculative
     abstractions" rule for no added expressiveness.
4. **Phase 1D — Consistency lock (docs matrix + parity tests shipped; scoped follow-ups tracked)**
   - Cross-adapter capability matrix (§0.2 above) updated with shipped/open
     status per boundary — single source of truth for what exists today.
   - Ports parity locked with tests: `TestRESTPattern_ErrorStatus_ParityWithDirectRouteDeclaration`
     and `TestEventPattern_ErrorChannel_ParityWithDirectChannelDeclaration` prove
     a `Pattern`-declared error rule behaves identically to one declared
     directly via `rest.NewRoute`/`events.NewChannel` — no ports-specific
     wiring was needed since `PluginRESTPattern`/`PluginEventPattern` are thin
     `RouteOpt`/`ChannelOpt` pass-throughs.
   - Existing examples (`examples/adapters-nethttp`) already demonstrate the
     REST error-path workflow end to end (no-pipeline vs pipeline, custom
     `ErrorHandler`) and continue to build/run unchanged.
   - Same user workflow confirmed identical across adapter-only and
     ports+pipelines usage: declare pattern -> choose action -> optional
     mapper -> predictable output, whether the declaration is registered by
     hand (`rest.NewRoute(...).Register(b)` / `events.NewChannel(...).Register(b)`)
     or through a `Pattern` (`PluginRESTPattern`/`PluginEventPattern`).
   - **Explicitly scoped OUT of this round** (tracked as follow-up work, not
     regressions — no existing behavior was removed):
     - REST `respond`/`handle`/`log` action selector (REST is currently
       `respond`-only for `ErrorPattern`; see Phase 1A scope note).
     - `mcpgo` dedicated typed tool-error declaration (EPU10 partially open).
     - `mqtt`/`zeromq` adopting the same `ErrorResponseFor`-consulting wiring
       `adapters/mqtt5.PublishAdapter` already has (declarative surface is
       adapter-agnostic in `api/events`; each adapter's own publish/sink call
       site needs the same few lines of wiring).

## Design decisions (resolved)

1. Naming convergence (shipped, Phase 1A):
   - `rest.ErrorStatus[E](status)` is the status-only RouteOpt.
   - `rest.ErrorPattern[E,B](status, codec, mapFn...)` is the codec-backed
     typed-body RouteOpt (direct or mapped payload).
   - No compatibility alias was kept — this feature has no external
     consumers yet, so there is exactly one canonical name per concept from
     the start (see "REST parity requirement" above).
   - `events.ErrorChannel[E,B](topic, codec, mapFn...)` (Phase 1B, shipped)
     mirrors the same two-mode shape, adapted to the pub/sub boundary
     (publish to a declared error topic instead of an HTTP body).
   - `websocket.ErrorFrame[E,Out](mapFn...)` (Phase 1B, shipped) mirrors the
     same shape again, adapted to the duplex-socket boundary (broadcast a
     typed frame to every connected session instead of a declared topic).
2. Codec-first error payload timing:
   - included in Phase 1 via unified typed declarations (`ErrorResponse` on REST;
     `ErrorPattern`-style equivalents on other adapter families), with direct +
     mapped modes.
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
10. Deterministic precedence:
   - first matching declaration wins (including wrappers), in route option order.
11. REST error metadata parity:
   - error headers/cookies use explicit status-scoped declarations with the same
     merge-field + codec semantics as happy-path response metadata.

## Phase 2 (post-1D review findings + follow-up work)

A review of Phases 1A–1D against the actual shipped code (not just docs)
found the following gaps. Priority order below; items 1–3 are independent
and parallelizable, item 4 depends on none of them structurally but
demonstrates them, item 5 should land after 1–2 for full action-model
vocabulary consistency, item 6 is deferred/optional.

1. **ReqReply runtime error-pattern matching — SHIPPED.**
   `reqreply.ErrorReplyMeta` was previously spec-only (documented explicitly
   as "Runtime adapter behavior is unchanged"). Added `reqreply.ErrorPattern[E,B]`
   (mirrors `rest.ErrorPattern`: direct/mapped modes, `errors.As` matching,
   first-match precedence). `ErrorPattern` ALSO auto-generates the
   `ErrorReplyMeta`-equivalent AsyncAPI reply-error channel/operation entry
   (default `Code` derived from `%T` type name; `.WithCode`/`.WithDescription`/
   `.WithSchemaName`/`.WithChannelAddress`/`.WithOperationID` customize it) —
   one declaration now drives both the spec AND runtime dispatch.
   `RouteHandle.ErrorResponseFor(err) (ErrorPatternResponse, bool, error)` is
   the lookup accessor. Wired into `mqtt5.Serve` and `zeromq.Serve`/
   `ServeRouter` (ROUTER variant preserves identity framing) on handler and
   encode failure ONLY — decode failure has no business error to match yet
   and keeps its existing plain-text-only behavior. Unmatched errors fall
   back to the existing plain-text `err.Error()` reply unchanged (backward
   compatible). `ErrorReplyMeta` remains available unchanged for spec-only
   declarations with no runtime dispatch.
2. **MCP tool error-pattern declaration — SHIPPED.**
   `api/mcp` had no `ErrorPattern`-equivalent declaration surface at all —
   `adapters/mcpgo.ToolHandler`'s handler-error branch always returned
   `mcp.NewToolResultError(err.Error())` (plain text). Added
   `mcp.ErrorPattern[E,B]` (mirrors `rest.ErrorPattern`/`events.ErrorChannel`:
   direct/mapped modes, `errors.As` matching, first-match precedence) as a
   `ToolOpt` on `NewTool`. No status/topic concept needed (MCP tool results
   aren't HTTP or pub/sub). `ToolHandle.ErrorResponseFor(err)
   (ErrorPatternResponse, bool, error)` is the lookup accessor. Wired into
   `mcpgo.ToolHandler`'s handler-error branch ONLY (input-decode and
   output-encode errors are different concerns, untouched) — a match
   returns `mcp.NewToolResultStructured(...)` with `IsError: true` (a
   structured typed result, still reported as an error to the LLM); no
   match falls back to the existing plain-text behavior unchanged. Added
   the "Error-path ergonomics" section to `docs/features/mcp.md` (a new
   section — none existed before). Resources/Prompts errors remain
   out of scope (protocol-level, not application business errors).
3. **`mqtt`/`zeromq` pub/sub adopt `ErrorResponseFor` wiring — SHIPPED.**
   `mqtt.PublishAdapter` and `zeromq.PublishAdapter` now consult
   `handle.ErrorResponseFor(err)` before falling back to `OnError`, mirroring
   the exact pattern already shipped in `mqtt5.PublishAdapter` — same
   respond/handle/log semantics, same fallback behavior, no new options
   struct fields (the declaration lives entirely on the `events.ChannelHandle`,
   which every publish adapter already receives).
4. **Example rework — SHIPPED.**
   - `examples/adapters-mqtt5` — added a "Demo 1b" section: a `SensorOutOfRangeError`
     matched by a declared `events.ErrorChannel` on `ReadingsChannelWithErrors`,
     fed through a `ports.SinkPort` + `mqtt5.PublishAdapter`, publishing the
     typed payload to `sensors/{sensorID}/readings/errors`.
   - `examples/websocket-duplex` — added a `NegativeValueError` business rule
     in the pipeline's `Map` stage, re-emitted (not silenced) through
     `stream.MapErr`, caught by a declared `websocket.ErrorFrame` on
     `DuplexSocketAdapterOptions.ErrorFrames`, broadcast as a typed `Update`
     frame to the connected client.
   - `examples/redis-cache` — added "Section 6": a `CacheWriteError` from a
     failing `Commands` fake, composed inside `SetAdapterOptions.OnError`
     with a declared `events.ErrorChannel.ErrorResponseFor` lookup,
     demonstrating the SQL/Cache/File composition pattern from
     `docs/guides/error-handling.md` as a runnable program (previously only
     documented in prose/tests).
   - All three examples build and smoke-test cleanly (`go build` + `go run`
     with clean exit 0), with existing demonstrated behavior unchanged.
5. **REST action selector — SHIPPED.**
   `rest.ErrorPattern` now returns a chainable `ErrorPatternOpt[E,B]` (still
   satisfies `RouteOpt` — fully backward compatible, no call-site changes
   needed for existing usage) with `.WithAction(rest.ErrorAction)`. Added
   `rest.ErrorAction` (`ErrorRespond`/`ErrorHandle`/`ErrorLog`) as REST's own
   standalone type — NOT shared with `events.ErrorAction` (api/rest remains
   dependency-free from api/events per existing layering rules; each API
   layer keeps its own parallel-but-independent vocabulary, same pattern as
   `RouteMeta`/`ChannelMeta`). Default `ErrorRespond` preserves existing
   behavior exactly (auto-write typed body). `ErrorHandle`/`ErrorLog` both
   skip the auto-write and fall through to `Options.ErrorHandler` — REST has
   only ONE such hook (unlike adapters with a separate `OnError` callback),
   so the two actions are behaviorally identical for REST; kept as distinct
   values purely for cross-boundary vocabulary parity. Wired into both
   `adapters/nethttp` and `adapters/chi` (`Handler`, and `PipelineHandler`
   automatically since it wraps `Handler`). This closes the last piece of
   the three-way action-model parity goal: REST/Events/WebSocket all now
   support the same `respond`/`handle`/`log` vocabulary.
6. **Client-side error decode parity** — evaluated, deferred (no concrete
   driving use case; `nethttp.UnexpectedStatusError.Body` already works as
   an escape hatch). Revisit only if a real use case emerges.
