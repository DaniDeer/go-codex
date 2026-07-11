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

## Stream Bridge Guardrail

Stream bridges connect adapters to `stream.Stream[T]`. Every bridge must be checked against these rules.

### Source bridges (adapter → `stream.Stream[T]`)

#### Rule B1 — Full validation pipeline must run

Every source bridge must apply the **same validation pipeline** as the underlying non-stream adapter function. Do not bypass param codecs, security, or observer calls to speed up the bridge.

| Bridge | Required validation | How to verify |
|--------|--------------------|-|
| `mqtt.SubscribeStream` | `SubscribeHandler` called internally (format priority, security, payload decode, observer, topic-var errors) | Must use `SubscribeHandler(ctx, handle, fn, innerOpts, fmt)` — not a hand-rolled handler |
| `mqtt5.SubscribeStream` | `makeSubscribeMessageHandler` called internally (ContentType, UserPropertyParams, security, payload decode, observer) | Must use `makeSubscribeMessageHandler(...)` — not a raw `msg.Payload` handler |
| `zeromq.SubscribeStream` | `SetSubscription(handle.Topic)` called; `ErrTimeout` loop with ctx-check | ZMQ filters at socket level — no per-message topic codec needed |
| `nethttp.HandlerLatest` | Full `Handler(handle, fn, opts)` wrapping — all 9 HTTP codec layers run | Must call `Handler(...)` not `http.HandlerFunc(func(...){...})` directly |
| `nethttp.HandlerIngest` | Full `Handler(handle, fn, opts)` wrapping | Must call `Handler(...)` |
| `nethttp.PipelineHandler` | Full `Handler(handle, fn, opts)` wrapping | Must call `Handler(...)` |
| `chi.*` | Same as nethttp equivalents | Must call chi `Handler(...)` |
| `sql.QueryStream` | `Validate(codec, row, opts)` called per row | Must use `adapters/sql.Validate` |
| `mcpgo.ToolLatestHandler` | `ToolHandler(handle, fn, opts)` wrapping — input schema validation runs | Must call `ToolHandler(...)` |
| `mcpgo.ToolPipelineHandler` | `ToolHandler(handle, fn, opts)` wrapping — input schema validation runs; each call starts a fresh stream pipeline | Must call `ToolHandler(...)` |

**If a source bridge hand-rolls validation logic** instead of delegating to the underlying adapter function, file a `bug` finding.

#### Rule B2 — Errors from validation must reach `Stream.Errors`

Source bridge errors must be routed to `Stream.Errors` as typed errors so callers can use `stream.MapErr` / `stream.Retry` to handle them. Do not silently discard errors.

| Bridge | Error routing | Error types expected |
|--------|--------------|---------------------|
| `mqtt.SubscribeStream` | `innerOpts.OnError = func(e SubscribeError) { errCh <- e }` | `mqtt.SubscribeError{Kind, Topic, Err}` |
| `mqtt5.SubscribeStream` | Same pattern via `innerOpts.OnError` | `mqtt5.SubscribeError{Kind, Topic, Err}` |
| `zeromq.SubscribeStream` | Socket errors terminate goroutine → channels close → stream ends | `gstream.StreamDecodeError` for payload errors |
| HTTP bridges | Error handler called (400/503/500) — errors do NOT go to a stream channel | HTTP response is the error signal |
| `sql.QueryStream` | `QueryStreamError` → `Stream.Errors` | `sql.QueryStreamError{Table, Op, Err}` |

**If a source bridge discards errors** (drops to `default` only, no `errCh <- e`) without routing to `Stream.Errors`, file a `bug` finding.

### Sink bridges (`stream.Stream[T]` → adapter)

#### Rule B3 — Static vs per-item Vars limitation must be documented

`Vars map[string]string` in sink bridge options applies the **same map to every item**. Per-item topic var substitution (e.g., `{sensorID}` from each payload) is not supported. Callers who need per-item vars must use `stream.Drain` with `Publish` directly.

Check: `DrainPublish` options in `mqtt`, `mqtt5`, `zeromq` must have a godoc note explaining the static-only limitation. Missing note = `trivial` finding.

#### Rule B4 — `AsPipelineFunc` wraps the fn, not the serve loop

`AsPipelineFunc` in `mqtt5` and `zeromq` must wrap the **handler function** passed to `Serve`/`ServeRouter`, not add a new `Serve` variant. This keeps all codec validation, observer calls, and error reply logic in the existing adapter. Verify:
- `mqtt5.AsPipelineFunc` returns `func(context.Context, Req) (Resp, error)` wrapping `stream.Single + stream.Collect`
- `zeromq.AsPipelineFunc` same pattern
- `PipelineNoResponseError{Topic}` returned when pipeline emits zero values

### HTTP codec layer verification

All HTTP bridge helpers must apply **all 9 codec layers** by delegating to `Handler`. Verify the exact call path:

```
Request:  body codec → ValidateQuery → ValidateCookies → ValidateHeaders → ValidatePathParams → security
Response: handle.Encode → ValidateResponseHeaders → ValidateResponseCookies
```

Special case — `HandlerIngest` param value gap (by design, not a bug):
- Path/query/cookie/header param VALUES are validated (errors → 400) but NOT included in the channel item
- Only the body-decoded `Req` is pushed to `dst`
- **Do not flag as a bug** — documented design limitation
- Check that the godoc note is present: "For routes where param values must reach the pipeline, use `Handler` directly with `RequestFromContext(ctx)`"

### New error types in stream bridges

All new stream bridge error types must implement `slog.LogValuer`. Verify for each package:

| Package | Bridge error types |
|---------|-------------------|
| `adapters/nethttp` | `NoLatestValueError{Path}`, `PipelineFullError{Path,Capacity}`, `PipelineNoResponseError{Path}`, `SSEWriteError{Path,Err}`, `SSEConnectError{URL,Attempt,Err}`, `SSEParseError{URL,Line,Err}` |
| `adapters/chi` | `NoLatestValueError{Path}`, `PipelineFullError{Path,Capacity}`, `PipelineNoResponseError{Path}`, `SSEWriteError{Path,Err}` |
| `adapters/zeromq` | `ServeLatestError{Op,Err}`, `NoLatestValueError{Topic}`, `CorrelationError{Seq,Err}`, `PipelineNoResponseError{Topic}` |
| `adapters/mqtt5` | `PipelineNoResponseError{Topic}` |
| `adapters/sql` | `QueryStreamError{Table,Op,Err}`, `InsertStreamError{Table,Op,Err}` |
| `adapters/file` | `ScanError{Path,Err}`, `WatchError{Dir,Err}`, `WriteError{Path,Err}` |

Rule: errors with an inner `Err` field implement `Unwrap()`; terminal errors (no inner cause) do not. Violation = `small` finding.

### `stream.Single[T]` usage pattern

`stream.Single(ctx, v T) Stream[T]` is the canonical per-request pipeline entry point. It must:
- Emit `v` exactly once, then close
- Never write to `Stream.Errors`
- Use a buffered channel of size 1 internally (not unbuffered, to avoid goroutine leak on ctx cancel before consumer reads)

Used correctly in: `PipelineHandlerFunc`, `AsPipelineFunc`. Do not flag `Single` as an issue in these contexts.

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
- **`HandlerIngest` only pushes body `Req` to channel.** Path/query/cookie/header param values are validated (errors → 400) but not included in the channel item. This is documented design, not a bug. Do not flag.
- **`mqtt.SubscribeStream` must use `SubscribeHandler`, not a hand-rolled handler.** The previous hand-rolled handler bypassed security and topic-var validation — fixed in R35. If you see a raw `func(_ pahomqtt.Client, msg pahomqtt.Message) { rawCh <- msg.Payload() }` pattern in SubscribeStream, it is a `bug`.
- **`mqtt5.SubscribeStream` must use `makeSubscribeMessageHandler`.** Same reason — fixed in R35. Raw handler pushing `msg.Payload` directly is a `bug`.
- **`zeromq.CallStream` must include `Vars` in `CallStreamOptions`.** Fixed in R35. Without `Vars`, topic var codec validation is silently skipped per call.
- **`AsPipelineFunc` does NOT add a new `Serve` variant.** It wraps the `fn` argument only. `Serve` API is unchanged. This is correct by design.
- **Stream bridge errors go to `Stream.Errors`, not a separate callback.** In source bridges, `subOpts.OnError` is overridden internally to route errors to the error channel. Callers who set `OnError` in `subOpts` before calling `SubscribeStream` will have it overridden — documented in godoc.
- **Static `Vars` in `DrainPublish`.** Same map applied to every item. Per-item topic var substitution requires `stream.Drain` + `Publish` directly. Do not flag as bug — documented limitation.
- **`stream.Single` uses a size-1 buffered channel.** This allows `deliver(handler, payload); cancel()` test patterns to work without goroutine leaks. Do not flag the buffered channel as inconsistency with `From` (which is unbuffered).
- **`PipelineHandler` response headers are reference-type safe for sequential pipelines.** `WithResponseHeaders(ctx, ...)` mutates the map stored in `ctx` — writes happen-before `stream.Collect` returns, so `Handler` reads correct values. Concurrent writes from parallel operators (CombineLatest, Merge) could race — documented limitation.
- **`mcpgo.ToolPipelineHandler` is the per-call trigger; `ToolLatestHandler` is the reactive cache.** Both wrap `ToolHandler`. `ToolPipelineHandler` runs a fresh stream per tool call (`stream.Single → Collect`); `ToolLatestHandler` runs a background stream and returns the latest value. Do not flag either as missing the other's pattern.
- **`nethttp.HandlerLatest` validates `Req` even though it's discarded.** All codec layers run (body decode, query/cookie/header/path params, security). This ensures only well-formed requests receive cached responses. Do not flag as waste.

## References

- [`references/history.md`](references/history.md) — findings fixed in Rounds
- [`references/checklist.md`](references/checklist.md) — full cross-layer consistency checklist
- [`.github/instructions/go-codex.instructions.md`](../../instructions/go-codex.instructions.md) — design contract
