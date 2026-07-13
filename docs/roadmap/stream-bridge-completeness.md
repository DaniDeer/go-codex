# Stream Bridge Completeness — Declarative I/O Evaluation

> **Status:** ✅ Fully implemented. All 5 gaps closed (nethttp.CallStream ✅, sql.QueryEachStream ✅, file.TapWriteFile/DrainWriteFile ✅, mqtt+mqtt5 SubscribeStream ergonomic fix ✅, file.ReadEachStream ✅).
> [← Back to Roadmap](index.md)

---

## Design vision

The user programs inside-out:

```
Inside:  Domain types (codex) → Forge functions → Stream pipeline
                                                          ↕
Outside: Transport bridges connect the inside to the outside world
```

Every bridge should be **declarative**: you pass a typed handle (which carries the schema,
codecs, path/topic params, and error types), and the bridge wires up automatically — no
custom goroutines, no handler registration boilerplate, no protocol-specific manual steps.

### Three bridge positions

```
[Source/Trigger]   → [Pure stream pipeline] → [Sink/Drain]
                              ↑
                    [Intermediate I/O step]
```

| Position | Declarative pattern | What the handle carries |
|----------|-------------------|------------------------|
| **A — Source** | `transport.SubscribeStream(ctx, client, handle, opts) → Stream[T]` | Topic/path, message codec, topic-var codecs |
| **B — Intermediate** | `transport.CallStream(ctx, client, handle, src, opts) → Stream[Resp]` | Req codec, Resp codec, path/topic params, security |
| **C — Sink** | `transport.DrainPublish(ctx, client, handle, src, opts)` | Topic/path, message codec, publish options |

---

## Adapter-by-adapter evaluation

### MQTT (v3/v3.1.1) — `adapters/mqtt`

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| A — Source | `SubscribeStream` | ✅ | Fully declarative: accepts client + qos + `TopicFilter`; subscribes internally; returns `Stream[T]` only |
| B — Intermediate | — | — | MQTT has no native request-reply protocol |
| C — Sink | `DrainPublish` | ✅ | Declarative: passes `*ChannelHandle[T]`, full codec applied |

**Ergonomic gap — MQTT `SubscribeStream`:**

The non-stream `Subscribe` takes the MQTT client and subscribes internally. `SubscribeStream`
forces the caller to register the returned handler manually:

```go
// Current — NOT fully declarative:
s, handler := mqtt.SubscribeStream(ctx, sensorHandle, fmt, srcOpts, subOpts)
mqttClient.Subscribe("sensors/+/data", 0, handler) // caller must do this

// Fully declarative would be:
s := mqtt.SubscribeStream(ctx, mqttClient, sensorHandle, qos, fmt, srcOpts, subOpts)
// bridge subscribes internally, no handler returned
```

**Root cause:** The MQTT client needs a wildcard filter (`sensors/+/data`) but the API
handle stores a template topic (`sensors/{sensorID}/data`). The caller currently provides
the MQTT-specific wildcard separately.

**Proposed fix:** Add `TopicFilter string` to `SubscribeOptions`. When set, the bridge
uses it for `client.Subscribe(filter, qos, handler)` internally. When empty, falls back
to `handle.Topic` (works for static topics). `SubscribeStream` takes the MQTT client:

```go
// Proposed — fully declarative:
type SubscribeOptions struct {
    TopicFilter string // MQTT wildcard, e.g. "sensors/+/data". Default: handle.Topic.
    // ... existing fields
}

func SubscribeStream[T any](
    ctx     context.Context,
    client  pahomqtt.Client,   // NEW — subscribes internally
    handle  *events.ChannelHandle[T],
    qos     byte,              // NEW — needed to subscribe
    fmt     format.Format[T],
    srcOpts gstream.SourceOptions,
    subOpts SubscribeOptions,
) gstream.Stream[T]            // no handler returned — bridge owns subscription lifecycle
```

---

### MQTT5 (v5.0) — `adapters/mqtt5`

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| A — Source | `SubscribeStream` | ✅ | Fully declarative: accepts client + router + qos + `TopicFilter`; subscribes + registers internally; returns `Stream[T]` only |
| B — Intermediate | `CallStream` | ✅ | Passes `*reqreply.RouteHandle[Req,Resp]`, full codec |
| C — Sink | `DrainPublish` | ✅ | Declarative |
| Server pipeline | `AsPipelineFunc` | ✅ | Adapts forge pipeline fn for `Serve` |

**Proposed fix for MQTT5 `SubscribeStream`** — same pattern as MQTT: accept client + router, subscribe internally:

```go
// Proposed:
func SubscribeStream[T any](
    ctx     context.Context,
    client  MQTTClient,    // NEW
    router  MQTTRouter,    // NEW — registers handler internally
    handle  *events.ChannelHandle[T],
    qos     byte,
    fmt     format.Format[T],
    srcOpts gstream.SourceOptions,
    subOpts SubscribeOptions,
) gstream.Stream[T]  // no raw handler returned
```

---

### ZeroMQ — `adapters/zeromq`

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| A — Source | `SubscribeStream` | ✅ | Takes `FramedSocket`, calls `SetSubscription` internally — fully declarative |
| B — Intermediate | `CallStream` | ✅ | Passes `*reqreply.RouteHandle[Req,Resp]` |
| C — Sink | `DrainPublish` | ✅ | |
| Reactive cache | `ServeLatest` | ✅ | Background stream → REP server |
| Server pipeline | `AsPipelineFunc` | ✅ | |

ZeroMQ is the most complete: all three positions are declarative and ergonomic.

---

### HTTP server — `adapters/nethttp` and `adapters/chi`

HTTP server bridges invert the pattern: the HTTP request IS the trigger. The bridge makes the stream/pipeline available to incoming requests.

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| Trigger → stream cache | `HandlerLatest` / `RegisterLatest` | ✅ | Passes `*rest.RouteHandle[Req,Resp]` + `Stream[Resp]` |
| Trigger → stream ingest | `HandlerIngest` / `RegisterIngest` | ✅ | Passes `*rest.RouteHandle[Req,struct{}]` + `chan<-Req` |
| Trigger → pipeline | `PipelineHandler` / `RegisterPipeline` | ✅ | Passes `*rest.RouteHandle[Req,Resp]` + `PipelineHandlerFunc` |
| Stream → SSE clients | `SSEFromStream`, `SSEFromHub` | ✅ | Passes `*rest.SSERouteHandle[Req,Event]` |
| `HandlerIngest` body-only gap | — | ⚠️ Design limitation | Path/query/cookie/header VALUES validated but not in channel item — documented |

---

### HTTP client — `adapters/nethttp`

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| A — Source (polling) | `PollStream` | ✅ | Passes `*rest.RouteHandle[Req,Resp]` |
| A — Source (SSE) | `SSEClientStream` | ✅ | Passes `*rest.SSERouteHandle[Req,Event]` |
| B — Intermediate | `CallStream` | ✅ | `CallStreamOptions{Vars, CallOpts, Buffer}` — full codec per item; see [Declarative I/O Steps](declarative-io-steps.md) |
| C — Sink | `DrainCall` | ✅ | Passes `*rest.RouteHandle[Req,Resp]` |

HTTP client is missing the intermediate position. `PollStream` and `DrainCall` are fully
declarative; `CallStream` is the gap.

---

### MCP — `adapters/mcpgo`

MCP is request-response (LLM calls a tool). Bridges connect stream computations to MCP tools.

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| Tool → latest stream value | `ToolLatestHandler` / `RegisterToolLatest` | ✅ | Passes `*apimcp.ToolHandle[In,Out]` + background `Stream[Out]` |
| Tool → per-call pipeline | `ToolPipelineHandler` / `RegisterToolPipeline` | ✅ | Passes `*apimcp.ToolHandle[In,Out]` + forge pipeline fn |

MCP is fully covered. The per-call pipeline pattern (`ToolPipelineHandler`) is the MCP
equivalent of `nethttp.PipelineHandler` — both are declarative trigger→pipeline→response.

---

### SQL — `adapters/sql`

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| A — Source | `QueryStream` | ⚠️ Semi-declarative | Takes `queryFn func(context.Context) ([]T, error)` — plain Go, not a declared handle |
| A — Per-item source | — | ❌ | No `QueryEachStream` for parameterized per-item lookup |
| B — Intermediate (per-item query) | `QueryEachStream` | ✅ | `QueryEachStream[In,T](ctx, codec, src, queryFn, QueryEachStreamOptions)` |
| C — Sink | `DrainInsert` | ⚠️ Semi-declarative | Takes `insertFn func(context.Context, T) error` — plain Go |

**Semi-declarative:** SQL bridges use `codex.Codec[T]` for row validation (which IS the
schema contract), but the query/insert functions are plain Go. The codec is declarative;
the database operation is not.

**Why SQL is intentionally different:** SQL queries vary enormously in structure (JOINs,
aggregations, CTEs). There is no natural "SQL query handle" equivalent to a REST route
or MQTT topic. The codec covers the output shape; the query implementation is deliberately
flexible.

**`QueryEachStream` gap:** For per-item parameterized lookups (e.g., fetch a config row
for each sensor), the current pattern is:

```go
// Today — each sensor spawns a query inside FlatMapSlice (not observed, not governed):
enriched := stream.FlatMapSlice(ctx, sensors, func(s Sensor) []Config {
    rows, _ := db.GetConfigBySensor(ctx, s.ID)
    return rows
})
```

A `QueryEachStream` bridge would validate each row via codec and route errors to
`Stream.Errors` with proper observer calls. Medium priority.

---

### File — `adapters/file` and `format.File`

| Position | Bridge | Status | Notes |
|----------|--------|--------|-------|
| A — Source (line-by-line) | `ScanStream` | ✅ | Passes path + `format.Format[T]` |
| A — Source (directory watch) | `WatchStream` | ✅ | Emits file paths; compose with `FlatMapSlice + format.File.Read` |
| B — Intermediate (per-item file read) | `ReadEachStream` | ✅ | `ReadEachStream[In,T,Out]` with `varsFor + combine`; errors → Stream.Errors as `ReadError` |
| C — Sink (line-by-line) | `DrainWrite` | ✅ | |
| C — Sink (whole file, overwrite) | `TapWriteFile` / `DrainWriteFile` | ✅ | `TapWriteFileOptions` / `DrainWriteFileOptions{OnError, Observer, FileOptions}` |
| C — Sink (whole file, patch) | `format.File.Patch` in `stream.Tap` | ⚠️ | Via `Tap` — `TapPatchFile` deferred (lower priority) |

**Whole-file sink wrapper evaluation — `TapWriteFile` / `DrainWriteFile`:**

Wrapping `format.File.Write` + `stream.Tap` into a first-class bridge makes the file
write declarative (pass `format.File[T]` as the handle) and consistent with the
`DrainPublish`/`DrainWrite`/`DrainInsert` family:

```go
// Today — manual Tap:
results = stream.Tap(ctx, results, func(r OEEResult) {
    if err := resultFile.Write(nil, r, format.FileOptions{Context: ctx}); err != nil {
        logErr(err)
    }
})

// Proposed — declarative Tap bridge (stream continues flowing):
results = file.TapWriteFile(ctx, resultFile, results, nil,
    file.TapWriteFileOptions{OnError: logErr})

// Proposed — declarative Drain bridge (terminal sink):
file.DrainWriteFile(ctx, resultFile, src, nil,
    file.DrainWriteFileOptions{OnError: logErr})
```

Proposed API:

```go
// adapters/file/stream.go (additions)

// TapWriteFile writes each stream item as a complete typed file using
// format.File.Write on every item. The stream continues flowing — use when
// file write is one of multiple side-effects (also publish, also dashboard).
//
// Errors are passed to opts.OnError as [format.FileWriteError] or [format.FileEncodeError].
func TapWriteFile[T any](
    ctx  context.Context,
    f    format.File[T],
    src  gstream.Stream[T],
    vars map[string]string,
    opts TapWriteFileOptions,
) gstream.Stream[T]

type TapWriteFileOptions struct {
    OnError     func(error)
    Observer    stats.Observer
    FileOptions format.FileOptions  // Perm, Context — merged with ctx observer
}

// DrainWriteFile writes each stream item as a complete typed file (terminal sink).
// Use when file write is the final step and the stream should be consumed.
func DrainWriteFile[T any](
    ctx  context.Context,
    f    format.File[T],
    src  gstream.Stream[T],
    vars map[string]string,
    opts DrainWriteFileOptions,
)

type DrainWriteFileOptions struct {
    OnError     func(error)
    Observer    stats.Observer
    FileOptions format.FileOptions
}
```

**`TapWriteFile` vs `DrainWriteFile` — which to use:**

| | `TapWriteFile` | `DrainWriteFile` |
|--|----------------|-----------------|
| Stream after call | ✅ Continues | ❌ Terminated |
| Use case | Write as one of multiple side-effects | File is the only sink |
| Pattern | `stream.Tap` | `stream.Drain` |

**Patch/Update variants:** `TapPatchFile` and `DrainPatchFile` follow the same pattern
using `format.File.Patch`. Lower priority — `stream.Tap` + `format.File.Patch` is already
concise for the partial-update case.

**Complexity:** Low — wraps existing `format.File.Write` + `stream.Tap`/`stream.Drain`.
No new codec machinery; errors are already typed (`FileWriteError`, `FileEncodeError`).

**`ReadEachStream` gap:** For per-item file reads with template path vars (e.g.,
`/config/{machineID}/thresholds.json` for each sensor), no declarative step exists.
The `WatchStream + CombineLatest2` pattern covers the DYNAMIC reload case. The
per-item-with-vars case requires `ReadEachStream`:

```go
// Proposed Phase 2:
func ReadEachStream[In, T, Out any](
    ctx     context.Context,
    f       format.File[T],
    src     gstream.Stream[In],
    varsFor func(In) map[string]string,
    combine func(In, T) Out,
    opts    ReadEachStreamOptions,
) gstream.Stream[Out]
```

The `combine` function is required because the bridge needs to pair the original stream
item with the file content — unlike `CallStream` where the response directly replaces
the request.

---

## Gaps priority matrix

| Priority | Gap | Affected adapter | Complexity | Unblocks |
|----------|-----|-----------------|------------|---------|
| ~~**High**~~ ✅ | `nethttp.CallStream` | `adapters/nethttp` | Implemented — `CallStreamOptions{Vars, CallOpts, Buffer}` | HTTP enrichment pipeline; forge purity |
| ~~**Medium**~~ ✅ | MQTT `SubscribeStream` ergonomic fix | `adapters/mqtt`, `adapters/mqtt5` | Implemented — `TopicFilter` in opts, client+router param, returns `Stream[T]` only | Fully declarative MQTT source wiring |
| ~~**Medium**~~ ✅ | `sql.QueryEachStream` | `adapters/sql` | Implemented — `QueryEachStream[In,T]` | Declarative SQL lookup per stream item |
| ~~**Low**~~ ✅ | `file.TapWriteFile` / `DrainWriteFile` | `adapters/file` | Implemented | Declarative whole-file sink bridge |
| ~~**Low**~~ ✅ | `file.ReadEachStream` | `adapters/file` | Implemented — `ReadEachStream[In,T,Out]` with `varsFor + combine` | Per-item file lookup; WatchStream+CombineLatest2 covers most cases |

---

## What is already complete

The following bridges are fully declarative and consistent with the inside-out design vision:

| Adapter | Position | Bridge |
|---------|----------|--------|
| ZeroMQ | A + B + C + server | `SubscribeStream`, `CallStream`, `DrainPublish`, `ServeLatest`, `AsPipelineFunc` |
| MQTT5 | A + B + C + server | `SubscribeStream`, `CallStream`, `DrainPublish`, `AsPipelineFunc` |
| HTTP server | Trigger A + SSE | `HandlerLatest`, `HandlerIngest`, `PipelineHandler`, `SSEFromStream`, `SSEFromHub` |
| HTTP client | A (poll/SSE) + B + C | `PollStream`, `SSEClientStream`, `CallStream`, `DrainCall` |
| MCP | Tool triggers | `ToolLatestHandler`, `ToolPipelineHandler` |
| SQL | A + B + C | `QueryStream`, `QueryEachStream` (per-item), `DrainInsert` |
| File | A + B + C (all) | `ScanStream`, `WatchStream`, `ReadEachStream`, `DrainWrite`, `TapWriteFile`, `DrainWriteFile` |

---

## Implementation order

Implement in this order to complete the declarative vision:

1. ~~**`nethttp.CallStream`**~~ ✅ — implemented
2. ~~**MQTT `SubscribeStream` ergonomic fix**~~ ✅ — implemented (`TopicFilter` in opts; client+router param; returns `Stream[T]` only; breaking change)
3. ~~**`file.TapWriteFile` / `DrainWriteFile`**~~ ✅ — implemented
4. ~~**`sql.QueryEachStream`**~~ ✅ — implemented
5. ~~**`file.ReadEachStream`**~~ ✅ — implemented (`ReadEachStream[In,T,Out]` with `varsFor + combine`; `ReadError` → `Stream.Errors`)
