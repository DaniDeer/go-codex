---
name: review-go-codex
description: >
  Systematic consistency review of the go-codex library: codecs (Layer 1), REST/event/MCP API contracts
  (Layer 2), and forge pipelines (Layer 3). Use when asked to "review go-codex", "consistency audit",
  "check for inconsistencies", "simple workflow review", "audit API surface", or after a significant
  round of refactoring. Reviews cross-layer naming parity, param types, builder methods, error shapes,
  structured errors, observer pattern, unit test coverage, and example correctness.
---

# review-go-codex

Systematic consistency audit of the go-codex library across all three layers. Reads key source files,
applies the checklist in `references/checklist.md`, collects findings, categorises by severity, and
plans a fix round.

## User Experience North Star

Every finding and fix must be evaluated against one question:

> **Does this make the library more declarative, simple, and consistent for the user?**

The three layers form a single workflow. A user should be able to move between them without context
switching or learning a new mental model:

| Layer                      | User action                                           | How it should feel                                                     |
| -------------------------- | ----------------------------------------------------- | ---------------------------------------------------------------------- |
| **Layer 1 — Codec**        | Define `codex.Codec[T]`                               | Declare shape + constraints once; derive encode/decode/schema for free |
| **Layer 2 — API contract** | `NewRoute` / `NewChannel` / `NewTool` / `NewResource` | Declare the contract as a value; pass it around; register it anywhere  |
| **Layer 3 — Pipeline**     | `forge.NewFunction` + `Registry`                      | Declare computation contracts; compose; register; govern               |

The workflow across layers is always: **declare → compose → register**. If a finding breaks this
pattern — forces the user to use imperative calls, repeat themselves, or learn layer-specific
vocabulary — it is at minimum a `small` finding.

Concrete implications for every review:

- **Declarative**: API objects (`RouteHandle`, `ChannelHandle`, `ToolHandle`, `ResourceHandle`,
  `PromptHandle`, `FunctionSpec`) are values, not builder side-effects. Users pass and store them.
  No magic, no global state.
- **Simple**: one method does one thing. Avoid overloaded methods. Param defaults should be safe.
- **Consistent**: same concept, same name. `Meta` structs, `Opt` interfaces, `Builder`/`Registry`
  fluent methods, error type shapes — all should follow the same pattern across layers. If Layer 2
  has `WithCodec`, Layer 2 events should have it too; if Layer 3 adds governance fields, their
  names should mirror Layer 2's `Meta` naming.

## When to Use This Skill

- User says "review go-codex", "consistency audit", "check go-codex API", "audit"
- User says "consistent, declarative, simple workflow"
- After a significant feature addition or refactoring round
- When something "feels off" but the user cannot pinpoint it

## Step-by-Step Workflow

### Phase 1 — Read key files in parallel

Read all of these before opening any finding:

| File                                            | Why                                                                      |
| ----------------------------------------------- | ------------------------------------------------------------------------ |
| `api/rest/builder.go`                           | Layer 2 REST: all param types, builder methods, error types              |
| `api/events/builder.go`                         | Layer 2 events: ChannelHandle, TopicParam, Builder                       |
| `api/mcp/builder.go`                            | Layer 2 MCP: ToolHandle, ResourceHandle, PromptHandle, Builder, MCPSpec  |
| `api/mcp/errors.go`                             | MCP error types: ToolInputError, ResourceParamError, PromptArgError, … |
| `forge/forge.go`                                | Layer 3: PipelineInfo, FunctionMeta, Registry, error types               |
| `render/pipeline/pipeline.go`                   | Pipeline YAML renderer                                                   |
| `render/asyncapi/v3/document.go`                | AsyncAPI renderer                                                        |
| `render/openapi/openapi.go`                     | OpenAPI renderer                                                         |
| `render/jsonschema/jsonschema.go`               | JSON Schema renderer: schema.Schema → json.RawMessage for MCP            |
| `adapters/nethttp/adapter.go`                   | Adapter: observer calls, security enforcement                            |
| `adapters/nethttp/stream.go`                    | HTTP stream bridges: HandlerLatest, HandlerIngest, PipelineHandler       |
| `adapters/nethttp/client.go`                    | Client adapter: `Call`, `CallOptions`, typed transport errors            |
| `adapters/chi/adapter.go`                       | Adapter: same as nethttp                                                 |
| `adapters/chi/stream.go`                        | Chi stream bridges: same API as nethttp, returns http.HandlerFunc        |
| `adapters/mqtt/adapter.go`                      | Adapter: subscribe/publish, observer calls                               |
| `adapters/mqtt/stream.go`                       | MQTT stream bridges: SubscribeStream, DrainPublish                       |
| `adapters/mqtt5/adapter.go`                     | MQTT5 adapter: Subscribe, Publish, makeSubscribeMessageHandler           |
| `adapters/mqtt5/stream.go`                      | MQTT5 stream bridges: SubscribeStream, DrainPublish, AsPipelineFunc, CallStream |
| `adapters/zeromq/stream.go`                     | ZeroMQ stream bridges: SubscribeStream, DrainPublish, AsPipelineFunc, CallStream, ServeLatest |
| `adapters/mcpgo/adapter.go`                     | MCP adapter: ToolHandler/ResourceHandler/PromptHandler, observer, errors |
| `adapters/mcpgo/stream.go`                      | MCP stream bridges: ToolLatestHandler, ToolPipelineHandler               |
| `adapters/sql/stream.go`                        | SQL stream bridges: QueryStream, DrainInsert                             |
| `adapters/file/stream.go`                       | File stream bridges: ScanStream, WatchStream, DrainWrite (new package)  |
| `.github/instructions/go-codex.instructions.md` | Design contract and prior decisions                                      |

Also scan: `api/rest/*_test.go`, `api/events/*_test.go`, `api/mcp/*_test.go`, `adapters/mcpgo/*_test.go`, `forge/*_test.go`, `render/pipeline/*_test.go`, `adapters/mqtt/stream_test.go`, `adapters/zeromq/stream_test.go`

Then read `references/history.md` to see what was already fixed. **Do not re-report these.**

### Phase 2 — Apply the checklist

Work through `references/checklist.md` section by section:

1. Cross-layer naming parity
2. Param type consistency
3. Builder method parity
4. Codec field godoc
5. Format API parity
6. Forge consistency
7. Error sentinel consistency (structured errors)
8. Observer pattern
9. Unit test coverage
10. Example correctness
11. Stream bridge consistency
12. Merge-field / boundary symmetry — one struct, one call

### Phase 3 — Record findings

For every issue found, assign:

| Field     | Values                           |
| --------- | -------------------------------- |
| ID        | `G<N>` (sequential)              |
| Severity  | `bug` / `small` / `trivial`      |
| Category  | one of the 10 checklist sections |
| File:line | exact location                   |
| Problem   | one sentence                     |
| Fix       | one sentence                     |

Present findings as a table, then group by priority: bugs first, then small, then trivial.

### Phase 4 — Produce a plan

Write findings and their priority order to the session plan.md. Do NOT start implementing until the
user confirms the plan or says "start" / "get to work".

### Phase 5 — Implement (after approval)

For each finding, in priority order:

1. Apply the fix
2. Run `go fmt ./...` — format immediately after each edit
3. Run `go build ./...` (must stay clean)
4. Run `go test ./...` (all packages must pass)
5. Run `just check` (staticcheck + gosec — no new warnings)
6. Update `.github/instructions/go-codex.instructions.md` if any exported API changed

After all fixes:

- Run every example: `for d in examples/*/; do go run ./$d; done` — each must exit 0
- If any example uses a stale pattern, update it to match the current API

### Phase 6 — Verify

Run in this order — each must be clean before proceeding to the next:

```bash
go fmt ./...          # format all files; no diff should remain
go build ./...        # must compile with zero errors
go test ./...         # all packages must pass
just check            # staticcheck + gosec; no new suppressions allowed
```

If `just check` surfaces a warning introduced by your changes, fix it before proceeding.
Do not add `//nolint` or `//gosec` suppressions to silence new findings.

### Phase 7 — Update History

Before producing the commit summary, append a new section to
`references/history.md` for this round. Follow the existing format:

```markdown
## Round <N> (<short title>)

- **<ID> — <finding title>**: one-sentence description of what was wrong and how it was fixed.
- ...

---
```

Rules:

- Include only findings that were actually implemented (not deferred/skipped).
- One bullet per finding; keep the description concise and factual.
- Insert the new section **above** Round <N-1> (newest round at top of file, below the header).

### Phase 8 — Commit Summary

After updating history.md, produce a commit-ready summary. **Do not commit** — present this to
the user and let them decide.

Format:

```
<short imperative title — max 72 chars>

Layer(s) affected: <Layer 1 / Layer 2 REST / Layer 2 events / Layer 2 MCP / Layer 2 MCP adapter / Layer 3 forge>
Round: R<N>

Findings fixed:
- <ID> [<severity>] <one-line description of fix>
- ...

Tests: <N> new / updated test(s) in <package(s)>
Examples: <"no changes needed" | list of updated examples>
Docs: .github/instructions/go-codex.instructions.md updated

Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>
```

Rules:

- Title must be imperative: "Fix ValidateTopicVars missing key check", not "Fixed…"
- List only findings that were actually implemented, not skipped or deferred
- If zero examples changed, write "Examples: no changes needed"
- Always include the `Co-authored-by` trailer

## Structured Errors Guardrail

go-codex uses typed errors, not bare strings. Check every error return site:

- **Server adapters** (nethttp/chi) must return typed errors: `rest.PathParamError`, `rest.QueryParamError`,
  `rest.HeaderParamError`, `rest.CookieParamError`, `rest.MissingPathVarError`,
  `rest.SecurityCredentialError`, `rest.SecurityError`.
- **Client adapter** (`nethttp.Call`) must return typed errors (all `errors.As`-navigable):
  - `rest.PathParamError`, `rest.QueryParamError`, `rest.CookieParamError`, `rest.HeaderParamError`,
    `rest.MissingPathVarError` — same as server (from pre-flight codec validation, no HTTP call sent)
  - `nethttp.UnexpectedStatusError{Method, Path, StatusCode, Body}` — non-2xx response
  - `nethttp.RequestBuildError{Err}` — `http.NewRequestWithContext` failure (invalid URL, cancelled ctx)
  - `nethttp.RequestError{Method, Path, Err}` — `http.Client.Do` failure (network, DNS, TLS, timeout)
  - `nethttp.ResponseBodyError{Err}` — `io.ReadAll` failure after successful connection
  - Bare `fmt.Errorf(...)` without wrapping a typed sentinel is a finding in `client.go`.
- **events adapter** must return: `events.TopicParamError`, `events.MissingTopicVarError`.
- **forge** must return: `forge.InputError`, `forge.OutputError`, `forge.ApplyError`,
  `forge.RefinementError`.
- **api/mcp** typed errors (all `errors.As`-navigable):
  - `mcp.ToolInputError{Name, Err}` — from `ToolHandle.Decode`
  - `mcp.ToolOutputError{Name, Err}` — from `ToolHandle.Encode`
  - `mcp.ResourceEncodeError{URI, Err}` — from `ResourceHandle.Encode`
  - `mcp.ResourceParamError{Name, Value, Err}` — from `ResourceHandle.BuildURI`/`ValidateURIVars`
  - `mcp.MissingResourceVarError{Name}` — from `ResourceHandle.BuildURI`/`ValidateURIVars`
  - `mcp.InvalidResourceParamError{Name, URITemplate}` — from `Resource.Register`
  - `mcp.PromptArgError{Name, Err}` — from `PromptHandle.ValidateArgs`
  - `mcp.MissingPromptArgError{Name}` — from `PromptHandle.ValidateArgs`
- **adapters/mcpgo** error behavior — **different from REST/events adapters**:
  - Input decode/validation failures → `mcp.NewToolResultError(err.Error())` returned as `*mcp.CallToolResult` with `IsError: true` (not a Go error). LLM sees the error text.
  - Output encode failures → protocol-level Go error `(nil, err)` (server contract violation).
  - `fmt.Errorf("...")` wrapping a typed sentinel is fine; bare `fmt.Errorf` without a typed wrapper is a finding.
- `fmt.Errorf("...")` wrapping a structured error is fine; a bare `fmt.Errorf` without wrapping a
  typed sentinel is a finding.
- Callers must be able to `errors.As` / type-switch on every error for structured logging.

## Observer Pattern Guardrail

`stats.Observer` has four interfaces; adapters must call them correctly:

| Interface                  | Who calls it                        | When                                                      |
| -------------------------- | ----------------------------------- | --------------------------------------------------------- |
| `stats.ValidationObserver` | codecs (internal)                   | on codec validation failure                               |
| `stats.Observer`           | adapters (nethttp, chi, mqtt, mcpgo)| on decode/encode start+finish, errors                     |
| `stats.PipelineObserver`   | forge `Registry.Apply`              | on each function apply                                    |
| `stats.SecurityObserver`   | adapters                            | on security rejection — **type-asserted, never embedded** |

Rules:

- `SecurityObserver` must be guarded: `if so, ok := obs.(stats.SecurityObserver); ok { so.RecordSecurityRejection(...) }`
- `PipelineObserver.RecordApply` must be called for every function in a pipeline, including
  wrapped collection ops (Map, Filter, etc.)
- Adapter `Observer` must be called on every code path, including early-exit error paths
- **`adapters/nethttp` client (`Call`) observer rules**:
  - `RecordRequest(method, path, statusCode, duration)` called on **every** code path — status 0 when no HTTP call was sent (pre-flight validation failure, `CredentialFunc` error, `RequestBuildError`)
  - `stats.ReportErrors(obs, location, err)` called for param validation failures before `RecordRequest` (location: `"path"`, `"query"`, `"cookie"`, `"header"`, `"body"`)
  - `RecordRequest` uses the route **path template** (e.g. `"/users/{id}"`), not the concrete URL — this allows observers to group metrics by route
  - Status 0 is the sentinel for "call attempted but no HTTP request reached the network" — document this in any `Observer` godoc for client-side code
- **`adapters/mcpgo` observer rules**:
  - `RecordRequest("tool"|"resource"|"prompt", name, statusCode, duration)` — called on every code path (200 success, 400 input error, 500 handler/output error)
  - `stats.ReportErrors(obs, "input", err)` — called before `RecordRequest` when codec validation fails (fires `RecordValidationError` per field)
  - No `SecurityObserver` at the mcpgo adapter level — MCP security is handled outside the adapter (no `SecurityFunc` field on `mcpgo.Options`)
  - Observer location values: `"input"` for tool argument decode/validation; `"prompt.args"` for prompt arg codec failures

### Default observer via context

Adapters use `stats.WithObserver(ctx, obs)` and `stats.ObserverFromContext(ctx)` to
allow a single observer to be set once for all components. When reviewing:

- **Nil-guard pattern for direct-ctx functions** (Subscribe, Publish, Apply, Drain, etc.):
  ```go
  obs := opts.Observer
  if obs == nil {
      obs = stats.ObserverFromContext(ctx)  // ← correct
  }
  ```
  **Do NOT flag `ObserverFromContext(ctx)` as wrong.** It returns `NoopObserver{}` when no
  context observer is stored — identical behaviour to the old `NoopObserver{}` default.

- **HTTP/MCP handler closures** (`nethttp.Handler`, `chi.Handler`, `mcpgo.ToolHandler`, etc.)
  are constructor functions that return closures. obs is resolved **inside the closure**,
  not at construction time:
  ```go
  // Inside the returned closure — per-request/per-call:
  obs := opts.Observer
  if obs == nil {
      obs = stats.ObserverFromContext(r.Context())
  }
  ```
  This is **correct** — it enables per-request observer injection via `ObserverMiddleware`.
  Do NOT flag as inconsistency with functions that resolve at call time.

- **`sql.Validate` exception**: has no `ctx` parameter → stays `NoopObserver{}`. This is
  by design. Do not flag as inconsistency.

- **`forge.Registry.WithObserver`**: explicit builder API by design — no context integration.
  Do not flag as missing context observer.

- **`ports.File` two-step guard**: uses `opts.Context` (not a direct `ctx` param):
  ```go
  obs := opts.Observer
  if obs == nil && opts.Context != nil {
      obs = stats.ObserverFromContext(opts.Context)
  }
  if obs == nil {
      obs = stats.NoopObserver{}
  }
  ```
  This is correct — `FileOptions.Context` is optional.

## Unit Test Coverage

For each exported type or method added, there must be at least one test covering:

- Happy path (valid input → expected output)
- Error path (invalid input → correct typed error)
- For builder methods: that the method returns the right shape (field set correctly)

Spot-check:

```bash
grep -n "func Test" api/rest/builder_test.go api/events/builder_test.go forge/forge_test.go render/pipeline/pipeline_test.go
```

If a new exported symbol has no corresponding `TestX` function, file a `trivial` finding.

## Example Correctness

Every `examples/*/main.go` must:

1. Build cleanly: `go build .`
2. Demonstrate the feature its directory name implies
3. Use current API (no stale patterns):
   - Must use `.WithCodec(codec)` not `Codec: &codec`
   - Must use `NewRoute` / `NewSSERoute` / `NewChannel` declarative constructors
   - Must not call deleted or renamed methods

Run them all:

```bash
for d in examples/*/; do
  echo "=== $d ===" && cd $d && timeout 5 go run . &
  cd - >/dev/null
done
wait
```

If an example panics or uses a stale pattern, file a finding.

## Boundary Symmetry Guardrail — one struct, one call

For every `api/*` builder-backed boundary with a request/response shape or
a duplex role pair (publisher/subscriber, requestor/replier, client/server),
check whether a caller on EITHER side can do the ENTIRE encode-or-decode
direction with **one struct value in (or out), one call** — this is the
headline promise, not an optional nicety. When reviewing a boundary that
was just added or touched, verify all five:

1. **Declare-once constructors** exist for every var-boundary (path/topic/
   query/header/cookie/key) mirroring `rest.NewPathParam[T]`/
   `NewRequiredQueryParam[T]`/etc. — registering BOTH the spec Param AND a
   `codex.FieldCodec[T]` merge field in one call.
2. **Escape hatch preserved** — plain validate-only Param structs still
   work unchanged, mixable with the merge-capable constructors on the same
   route/channel/tool.
3. **Encode/decode symmetry** — decode-side flat-union merge AND
   encode-side role-aware accessors (`PathMergeFields()`/`QueryMergeFields()`/
   etc., never one flat list) both exist. A boundary that only has
   decode-merge (e.g. `DecodeMerged` with no `PathMergeFields`-style
   accessors) is an INCOMPLETE implementation — file at least a `small`
   finding.
4. **Role symmetry** — both roles of the boundary have the convenience:
   server AND client; publisher AND subscriber; requestor AND replier. A
   response/reply direction with no merge-field support when the request
   direction has it is an asymmetry — file a finding (severity depends on
   how core the boundary is: `bug` for a shipped, documented-as-complete
   feature; `small` for a boundary already flagged as a known gap in the
   roadmap).
5. **Single-call convenience wrapper** exists on the encode side
   (`CallHandle`-equivalent) and the decode side is wired into the
   adapter's `Handler`/`Register`-style entry point automatically. Stopping
   at the accessors/constructors without the wrapper is incomplete — the
   wrapper IS the promise made concrete.

**`api/rest`/`api/events`/`api/reqreply` all SHIPPED the five above** (see
`.github/instructions/go-codex.instructions.md`'s "Declarative Var
Extraction & Merge" section), **AND so did the `ports.Pattern` binding
layer**: `DrainCallAdapter`/`PublishAdapter`/`CallAdapter` across
`nethttp`/`mqtt5`/`zeromq`/`mqtt` delegate to `CallHandle`/`PublishHandle`
and derive vars PER-ITEM whenever their `Vars` option is left `nil` (a
non-nil map remains the static-vars escape hatch). `adapters/zeromq`'s own
pub/sub `Subscribe`/`Publish` and `adapters/mqtt` (v3) events also received
merge-field wiring. `ports.File`/`adapters/file` and `ports.Cache`/
`adapters/redis` also shipped the same convenience (`File.ReadMerged`/
`ports.WriteHandle`, `redis.GetMerged`/`redis.SetHandle`). Only flag a NEW
port-binding adapter as a finding if it reproduces the OLD
static-`Vars`-only gap instead of delegating to its own Handle-suffixed
wrapper. SSE/WebSocket connection-level merge and hardening are now shipped;
use `docs/features/sse-streaming.md` and `docs/features/websocket.md` as
reference coverage.

REST (`api/rest` + `adapters/nethttp`/`chi`) is the reference — use it to
judge every other boundary's completeness. See `docs/concepts/api-contracts.md`
("one struct, one call" design principle) and the `add-a-new-adapter`
skill's Step 5b for the full checklist this section mirrors.

**Not JSON-specific, not flat-struct-specific.** A boundary's merge-field
example/test that ONLY covers JSON body + flat top-level fields is
INCOMPLETE — file at least a `small` finding. Check that: (a) body
decode/encode is treated as orthogonal to var-merge (any `format.Format[T]`
should compose, not just JSON/YAML/TOML), and (b) at least one merge field
demonstrates nested sub-struct access (`func(r Req) string { return r.Meta.X }`),
not only top-level fields. `examples/rest-nested-binary` and
`api/rest/builder_test.go`'s `TestGobBodyFormat_ComposesWithNestedMergeFields`/
`TestNestedStructMergeFields_GetSetReachIntoSubstruct` are the reference
pattern — a boundary lacking equivalent coverage when it claims to satisfy
the "one struct, one call" mandate is an incomplete implementation.

## Port Adapter Guardrail

Pipelines are connected to transports via **port adapters** — `ports.SourceAdapter[T]`,
`ports.SinkAdapter[T]`, `ports.IOAdapter[Req,Resp]`, and `ports.ToolAdapter[In,Out]`
(server-side request/response, complement of `IOAdapter`) implementations in each
adapter package's `binding.go`. Every adapter must be checked against these rules.

**`Pattern` is the primary declaration surface** (Phase 4/5) for handle-backed
adapters — `ports.RESTPattern`/`EventPattern`/`ReqReplyPattern`/`MCPPattern`, set via
`PortOptions.Patterns`. A port builds its own handle internally by always calling
`Route`/`Channel`/`Tool.Register(builder)` — never the weaker `ClientHandle()` — so a
`Pattern`-derived handle is indistinguishable from one built by hand with the same
builder. `PortOptions.RESTBuilder`/`EventBuilder`/`ReqReplyBuilder`/`MCPBuilder` let
the caller supply a shared `*Builder` for security schemes/global security/path-topic
constraints/spec accumulation; nil uses a private single-use builder (same
zero-ceremony default). Do not flag `NewSourcePort`/`NewSinkPort`/`NewIOPort`/
`NewToolPort` returning `(*Port, error)` as an inconsistency — this is intentional
(`Register` is fallible in ways the old builder-free construction wasn't). `Params`/
`IOParam` remain the enforcement mechanism only for handle-less adapters (`file`) —
do not expect `Pattern`-backed adapters to also consult `Params`.

### Rule B1 — Adapter must use the underlying adapter function, not hand-roll IO

Every `SourceAdapter.Activate` must call the same underlying machinery as the non-stream
adapter function (`SubscribeHandler`, `makeSubscribeMessageHandler`, `gstream.FromCodec`, etc.).
Do not bypass param codecs, security, or observer calls.

| Adapter | Required machinery |
|---------|-------------------|
| `mqtt.SubscribeAdapter` | Must call `SubscribeHandler(ctx, handle, fn, innerOpts, fmt)` |
| `mqtt5.SubscribeAdapter` | Must call `makeSubscribeMessageHandler(ctx, handle, fmts, fn, obs, opts)` |
| `zeromq.SubscribeAdapter` | Must call `sock.SetSubscription`, `sock.RecvFrames`, `gstream.FromCodec` |
| `nethttp.IngestAdapter` | Must call `Handler(handle, fn, opts)` via internal ingest logic |
| `sql.QueryAdapter` | Must call `Validate(codec, row, opts)` per row |
| `mcpgo.ToolLatestHandler` | Must call `ToolHandler(handle, fn, opts)` |
| `mcpgo.ToolPipelineHandler` | Must call `ToolHandler(handle, fn, opts)` |

### Rule B2 — Errors from validation must reach `Stream.Errors` (source adapters) or `OnError` (sink adapters)

Source adapter errors must be routed to the `errs chan<- error` passed to `Activate`:

| Adapter | Error routing |
|---------|--------------|
| `mqtt.SubscribeAdapter` | `innerOpts.OnError → errs channel` → `PortBindError` via BrokerError |
| `mqtt5.SubscribeAdapter` | Same via `innerOpts.OnError` |
| `zeromq.SubscribeAdapter` | Socket errors terminate goroutine → channels close |
| `sql.QueryAdapter` | `QueryStreamError` → `errs` channel |
| `file.ScanAdapter` | `ScanError` → `errs` channel |

### Rule B3 — `AsPipelineFunc` wraps the fn, not the serve loop

`AsPipelineFunc` in `mqtt5` and `zeromq` must wrap the **handler function** passed to `Serve`/`ServeRouter`. Verify:
- Returns `func(context.Context, Req) (Resp, error)` wrapping `stream.Collect`
- `PipelineNoResponseError{Topic}` returned when pipeline emits zero values
- No new `Serve` variant added

### HTTP codec layer verification

All HTTP server stream helpers must apply **all 9 codec layers** by delegating to `Handler`:

```
Request:  body codec → ValidateQuery → ValidateCookies → ValidateHeaders → ValidatePathParams → security
Response: handle.Encode → ValidateResponseHeaders → ValidateResponseCookies
```

`nethttp.IngestAdapter` uses `Handler` internally. Path/query/cookie/header param VALUES are
validated but NOT included in the channel item — design limitation, not a bug.

### Error types in port adapters

All adapter error types must implement `slog.LogValuer`. Verify for each package:

| Package | Error types |
|---------|-------------|
| `adapters/nethttp` | `NoLatestValueError{Path}`, `PipelineFullError{Path,Capacity}`, `PipelineNoResponseError{Path}`, `SSEWriteError{Path,Err}` |
| `adapters/chi` | Same as nethttp |
| `adapters/zeromq` | `ServeLatestError{Op,Err}`, `NoLatestValueError{Topic}`, `CorrelationError{Seq,Err}`, `PipelineNoResponseError{Topic}` |
| `adapters/mqtt5` | `PipelineNoResponseError{Topic}`, `BrokerError{Op,Err}` |
| `adapters/sql` | `QueryStreamError{Table,Op,Err}`, `InsertStreamError{Table,Op,Err}`, `RowValidationError{Table,Op,Err}` |
| `adapters/file` | `ScanError{Path,Err}`, `WatchError{Dir,Err}`, `WriteError{Path,Err}`, `ReadError{Err}` |
| `ports` | `PortBindError{Port,Adapter,Err}`, `PortNoAdapterError{Port}` |

Rule: errors with an inner `Err` field implement `Unwrap()`; terminal errors do not.

### `stream.Single[T]` usage pattern

`stream.Single(ctx, v T) Stream[T]` is the canonical per-request pipeline entry point:
- Emits `v` exactly once, then closes
- Never writes to `Stream.Errors`
- Uses a buffered channel of size 1 internally

Used correctly in: `PipelineHandlerFunc`, `AsPipelineFunc`. Do not flag as an issue.

---

## Gotchas

- **Do not re-report R1-Rxx items.** See `references/history.md` for the full list of what is already fixed.
- **`FunctionKindScalar` is `""` (empty string).** `NewFunction`/`Compose` never write `Kind` — scalar functions have `Kind==""` by design. The `render/pipeline` renderer omits `kind:` for scalar. This is correct.
- **`rest.Builder.AddServer` discards `name` after description fallback.** OpenAPI servers are a keyless ordered array. `events.Builder.AddServer` stores the name (AsyncAPI servers are keyed). Same call site, different semantics.
- **`PathParam` and `TopicParam` have no `Required` field.** This is by design — OpenAPI mandates path params are always required; topic vars must always be present. Godoc explains this.
- **`ResourceParam` has no `Required` field.** URI vars in a template must always be present (same rationale as PathParam/TopicParam). `PromptArg` DOES have `Required bool` — prompt args are optional by default.
- **`SecurityScheme` codec is spec-only.** No adapter enforces it unless `SecurityFunc` does so explicitly.
- **`SubscribeFormats` / `PublishFormats` take priority over `Formats`.** Adapters must check these before falling back to `handle.Formats`.
- **`ToolHandle.Decode` takes `any`, not `[]byte`.** MCP protocol already deserializes arguments to `map[string]any` before the adapter calls `BindArguments`. Re-encoding to bytes and back would be wasteful. This is correct and intentional — do not flag as inconsistency with REST/events `Decode([]byte)`.
- **MCP input errors are `IsError: true` results, not Go errors.** `adapters/mcpgo.ToolHandler` returns `mcp.NewToolResultError(...)` with `IsError: true` for codec validation failures. This is the MCP protocol's way of reporting tool errors to the LLM. Do not flag as missing typed error return.
- **`mcp.Info` uses `Name` not `Title`.** REST uses `Info{Title}` (OpenAPI spec), events uses `Info{Title}` (AsyncAPI spec), MCP uses `Info{Name}` (MCP protocol). All are protocol-driven — this is correct, not an inconsistency.
- **`api/mcp` has no security methods.** No `AddSecurityScheme`, `AddGlobalSecurity`, or `SecurityFunc` — MCP security is handled separately and not part of the core `api/mcp` builder. This is by design.
- **Do not invent new API surface** during a review. Findings should fix inconsistencies in existing API, not design new features.
- **Update `go-codex.instructions.md` after every code change.** The instructions file is the single source of design truth — it must stay in sync.
- **Stream bridge helpers (`SubscribeStream`, `DrainPublish`, `CallStream`, `HandlerIngest`, etc.) have been removed.** Replaced by port adapters in `binding.go` files. If you see calls to these removed functions, they are stale code — replace with `ports.SourcePort.Bind(mqtt.SubscribeAdapter(...))` etc.
- **Port adapter wiring belongs in main.go / application layer, not pipeline code.** `domain/pipeline.go` must have zero imports from `adapters/*`. Only `ports`, `stream`, `forge`, `codex`, `format` are allowed in pipeline code.
- **`nethttp.IngestAdapter` only delivers body `Req` to the port.** Path/query/cookie/header param values are validated (errors → HTTP 400) but not included in the pipeline item. Design limitation, not a bug.
- **`zeromq.CallAdapter`/`mqtt5.CallAdapter` carry `Vars` in `CallStreamOptions`.** These are static vars (same map for every item). For per-item var substitution use `gstream.Drain` + `Call` directly.
- **`AsPipelineFunc` does NOT add a new `Serve` variant.** It wraps the `fn` argument only. `Serve` API is unchanged. This is correct by design.
- **`ports.IOPort` accepts exactly one adapter.** A second `Bind` call returns `PortBindError`. Only `SourcePort` supports fan-in; only `SinkPort` supports fan-out.
- **`ports` calling `Register` instead of `ClientHandle` is intentional, not a missed optimization.** `Register` is a strict superset of `ClientHandle` (adds duplicate-name checks for `reqreply`/`mcp`, unknown-param-name checks, path/topic codec validation, security scheme/global security population) — there is no case where `ClientHandle` would be preferable inside `ports`. Do not suggest reverting to `ClientHandle` for a "simpler" default path.
- **`rest.Route.Register`/`events.Channel.Register` do NOT detect duplicate routes/topics** — only `reqreply.Route.Register`/`apimcp.Tool.Register` do. Calling `ports.RegisterREST`/`RegisterEvent` twice with the same builder silently adds a duplicate spec entry (not an error); `RegisterReqReply`/`RegisterMCP` do error. This asymmetry is a property of the underlying `api/*` packages, not a `ports` bug.
- **`ports.SQLPattern` is metadata-only BY DESIGN.** SQL query text/placeholders are driver-specific typed closures owned by the adapter constructor — no template, no handle, no spec. Do not flag the asymmetry with `RESTPattern`/`EventPattern`/`FilePattern` as an incomplete implementation. Propagation is via `WithSQLMeta`/`SQLMetaFromContext` (mirrors `WithParams`); sql adapters default `Table`/`Op` from context, explicit values win.
- **`ports.FilePattern.Format` is a `FileFormatKind` enum (JSON/YAML/TOML), not a `format.Format[T]`.** A generic `Format[T]` cannot live in the non-generic `Pattern` struct; the kind is applied to the port's own codec inside the build fns. Custom formats stay handle-first (`ports.NewFile` by hand) — documented fallback, not a gap. On `IOPort[Req,Resp]` the built handle uses the RESPONSE codec (`ports.File[Resp]`) — intentional: the file content IS the port's response.
- **`file.ReadAdapter` (2-type) and `file.ReadEachAdapter` (3-type) coexist intentionally.** `ReadAdapter[In,Resp]` pairs with `FilePattern` (file content = response); `ReadEachAdapter[In,T,Resp]` with independent content type + `combine` serves enrichment and stays handle-first. Not duplication.
- **`RESTPattern` is role-aware on single-codec ports — do not flag the shape divergence.** On `SourcePort[T]` `PluginRESTPattern` builds the HTTP-ingest shape `RouteHandle[T, struct{}]` (the type params express it, no dedicated accessor); on `SinkPort[T]` it builds the SSE shape `SSERouteHandle[struct{}, T]` (always GET — non-GET `Method` fails Plugin; replay via `RegisterSSE`). The build fn takes an unexported `portRole` param. Only `nethttp.DrainCallAdapter` (outbound client, independent Resp codec) remains handle-first — by design, same asymmetry category as `ReadEachAdapter` enrichment.
- **Stream bridge errors go to `Stream.Errors`, not a separate callback.** In source bridges, `subOpts.OnError` is overridden internally to route errors to the error channel. Callers who set `OnError` in `subOpts` before calling `SubscribeStream` will have it overridden — documented in godoc.
- **Static `Vars` in `DrainPublish`.** Same map applied to every item. Per-item topic var substitution requires `stream.Drain` + `Publish` directly. Do not flag as bug — documented limitation.
- **`stream.Single` uses a size-1 buffered channel.** This allows `deliver(handler, payload); cancel()` test patterns to work without goroutine leaks. Do not flag the buffered channel as inconsistency with `From` (which is unbuffered).
- **`PipelineHandler` response headers are reference-type safe for sequential pipelines.** `WithResponseHeaders(ctx, ...)` mutates the map stored in `ctx` — writes happen-before `stream.Collect` returns, so `Handler` reads correct values. Concurrent writes from parallel operators (CombineLatest, Merge) could race — documented limitation.
- **`mcpgo.ToolPipelineHandler` is the per-call trigger; `ToolLatestHandler` is the reactive cache.** Both wrap `ToolHandler`. `ToolPipelineHandler` runs a fresh stream per tool call (`stream.Single → Collect`); `ToolLatestHandler` runs a background stream and returns the latest value. Do not flag either as missing the other's pattern.
- **`nethttp.HandlerLatest` validates `Req` even though it's discarded.** All codec layers run (body decode, query/cookie/header/path params, security). This ensures only well-formed requests receive cached responses. Do not flag as waste.
- **`ObserverFromContext(ctx)` in nil-guards is correct — do not flag.** The nil-guard pattern was updated from `obs = stats.NoopObserver{}` to `obs = stats.ObserverFromContext(ctx)` as part of the default context observer feature. `ObserverFromContext` returns `NoopObserver{}` when no context observer is stored, so behaviour is identical when no context observer is present. This is not an inconsistency — it is the correct implementation.
- **HTTP/MCP handler closures resolve observer inside closure, not at construction.** `nethttp.Handler`, `chi.Handler`, `mcpgo.ToolHandler`, etc. are constructor functions. obs is resolved per-request/per-call from `r.Context()`/call ctx inside the returned closure. This is intentional (enables per-request middleware injection). Do not flag as inconsistency with functions that resolve at call time.
- **`sql.Validate` keeps `NoopObserver{}` fallback.** It has no `ctx` parameter and cannot participate in the context observer lookup. Do not flag as missing `ObserverFromContext`. Pass `ValidateOptions{Observer: stats.ObserverFromContext(ctx)}` explicitly when observability is required.
- **`forge.Registry.WithObserver` keeps explicit builder API.** No context integration — registry is long-lived startup configuration. This is by design and must not be changed.
- **`ports.File` uses two-step guard with `opts.Context`.** `FileOptions` has no direct `ctx` param but has an optional `Context` field. The nil-guard is `if obs == nil && opts.Context != nil { obs = ObserverFromContext(opts.Context) }` followed by `if obs == nil { obs = NoopObserver{} }`. This is correct and intentional — do not flag as inconsistent.
- **WebSocket client-side (Phase 2) design is intentional — do not flag:** dial adapters auto-reconnect with exponential backoff and emit a gap `SocketError` per failed dial AND per drop (no silent loss); session generations `c1`,`c2`,… mark reconnects; outbound frames while down are DROPPED with `ErrFrameDropped` — including during initial connection establishment (pump/buffer upstream); chi socket adapters delegate to adapters/websocket through a swapHandler-as-Mux shim (zero duplication, `AdapterName` overridden); `events.Builder.AddChannelItem` skips the topic codec by design (HTTP upgrade paths); `RegisterSocket`: Subscribe=In (app receives), Publish=Out (app sends), struct{} side skipped.
- **WebSocket adapter + DuplexPort design is intentional — do not flag:** `DuplexPort` binds exactly ONE adapter (IOPort precedent); `DuplexAdapter.Activate(ctx, dst, errs, src)` — port owns all channels, `Feed` closes the outbound pair; slow client = per-session frame DROP with `SocketError`+`ErrFrameDropped` (queue default 16); decode failure keeps connection open; `SocketPattern` rejected on IOPort/LatestPort/ToolPort and is the ONLY pattern a DuplexPort accepts; upgrade extracts ALL template vars for `Hub.SessionInfo` but validates only declared PathParams; keepalive shim-owned, gorilla only in socket.go; no ConnectionObserver; not an MQTT broker.
- **Format `RouteOpt`/`ChannelOpt` constructors (`RequestFormats`/`Formats`/`SubscribeFormats`/`PublishFormats` in `api/rest`/`api/events`/`api/reqreply`) are intentional — do not flag:** these are API symmetry with `CustomFormat` (R59), not a duplicate — REST/Event/ReqReply never needed a Pattern struct field since their handles already accept any `format.Format[T]`; zero `ports` package changes (they just implement existing `RouteOpt`/`ChannelOpt` interfaces); type-erased `any` storage resolved generically in `Register`, mismatch only detectable at Register time (not the call site) — this is a Go-generics limitation, not a missed check; new `FormatOptError` types deliberately have `LogValue` even though older sibling errors in the same files don't (improvement, not inconsistency).
- **`Pattern` `CustomFormat` escape hatch is intentional — do not flag:** no dedicated `FileFormatGob` enum value (J/Y/T share a map[string]any intermediate construction shape, Gob doesn't — belongs in the general escape hatch); `CustomFormat` stores a pre-built `format.Format[T]` value, not a factory closure; `fileFormatFor`/`resolveFormat` intentionally fallible now (CustomFormat mismatch → `PatternRegisterError`, enum-only path stays infallible); `SocketPattern`'s unused `struct{}` side is exempt from the CustomFormat type assertion (real bug fix, not an inconsistency); `CustomFormat` silently wins over `Format` when both set.
- **Redis adapter design is intentional — do not flag:** `Commands` narrow interface with `NewCommands` as the only go-redis import (fake-based tests); `GetAdapter` miss SKIPS by default (`MissIsError` opts into error); `SetAdapter` passes items through even on write failure (cache failure never drops pipeline data); NO `redis.LatestAdapter` (Serve is read-only — durable LatestPort = SetAdapter tee + `Seed` merge); `CachePattern` REJECTED on SourcePort/ToolPort with `PatternRegisterError{Kind:"cache"}` (intentional strictness); `CacheKeyError` has no Unwrap (no inner error); key vars are plain strings in Phase 1.
- **Stream routing (`stream/route.go`) design is intentional — do not flag:** `Switch`/`SwitchKey` send non-matches AND src errors ONLY to rest (single error ownership) and PANIC on malformed cases (empty/dup `Name`, nil `When`, dup keys — programming errors at wiring time); `GroupBy` blocks like `SinkPort.Feed`, runs `onKey` on the dispatch goroutine ("start, don't run"), keeps unbounded keys, and fans errors out NON-BLOCKING to active keys; `OfType` drops non-matches silently with observer from ctx (no Options struct); `SwitchType3` is direct dispatch (composing `SwitchType2`+`OfType` would race two readers on one channel); `SplitEither` has no rest (closed sum, errors to both branches). Routing adds NO new error types.

## References

- [`references/history.md`](references/history.md) — findings fixed in Rounds
- [`references/checklist.md`](references/checklist.md) — full cross-layer consistency checklist
- [`.github/instructions/go-codex.instructions.md`](../../instructions/go-codex.instructions.md) — design contract
