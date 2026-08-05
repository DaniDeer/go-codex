# Wiring Pipelines with Ports

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) · [Ports feature page](../features/ports.md) · [App — Application Lifecycle](../features/app.md)

go-codex pipelines are wired to the outside world using **port adapters** — a declarative,
protocol-agnostic binding pattern that keeps domain/pipeline code free of transport imports.

## Inside-out development

Define domain logic first; connect to transports last:

```
Step 1 — Domain core (no adapter imports)
    codex.Codec[T]                       ← validated domain types
    forge.NewFunction[In, Out](...)      ← governed pure computation
    ports.NewSourcePort / SinkPort /     ← IO enforcement points
        IOPort / ToolPort / PipePort

Step 2 — Wiring (main.go only)
    port.Bind(ctx, transport.XxxAdapter(...))  ← connect to real transport
```

## Seven port types

## Two consumption styles, one declaration mechanism

`declare → PluginXxxPattern → Bind` never changes based on how you consume the
port afterward. Plain idiomatic Go — no `forge`/`gstream` composition — is a
first-class way to use `ports`, not a stepping stone toward pipelines:

```go
// Same declaration and Bind either way:
handle, err := domain.OEETool.PluginRESTPattern(domain.OEERESTPattern)
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, handle, nethttp.PipelineAdapterOptions{}))

// Pipeline-style pipeline function:
domain.OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
    return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
})

// Plain-Go style — identical wiring, no gstream:
domain.OEETool.SetFunc(func(ctx context.Context, req OEEIn) (OEEResult, error) {
    return oeeCalcFn(ctx, req)
})
```

Per-port escape hatch for plain Go:

| Port | Plain-Go method | Stream-composed equivalent |
|---|---|---|
| `SourcePort[T]` | `Stream(ctx)` + `stream.Drain` callback | `Stream(ctx)` + `gstream.Apply` |
| `SinkPort[T]` | `Start`/`Push`/`Close` | `Feed(ctx, stream)` |
| `IOPort[Req,Resp]` | `Call(ctx, req)` | `Connect(ctx, stream)` |
| `ToolPort[In,Out]` | `SetFunc(fn)` | `SetPipeline(fn)` |
| `LatestPort[T]` | `Latest()` | `Feed(ctx, stream)` to populate |
| `DuplexPort[In,Out]` | `Inbound(ctx)` + per-session handling | `Feed(ctx, stream)` |

See `examples/ports-plain-go` for a full application using only the
plain-Go column — same `Pattern`/`Bind` lines as `examples/sensor-service`,
zero `forge`/`gstream` imports.

## Error surfaces and escape hatches

Ports keep transport imports out of pipeline code, but error handling still has
clear interception points. Use this matrix first:

| Port/pipeline point | Error surface | Handle here |
|---|---|---|
| `SourcePort[T]` inbound adapters | `src := port.Stream(ctx)` then `src.Errors` | `stream.Drain(..., onErr, ...)` or explicit `for err := range src.Errors` |
| `SinkPort[T]` outbound adapters | upstream `Feed` errors forwarded to each bound sink stream | adapter `OnError` hook (if adapter has one) + upstream drain handler |
| `IOPort[Req,Resp]` call-style adapters | returned `error` from adapter transform path | `errors.As` for typed adapter/route errors |
| `ToolPort[In,Out]` pipeline serving | bind/setup errors + adapter route errors | `Bind` error (`PortNoPipelineError` wrapped in `PortBindError`), then adapter-specific hooks (HTTP `ErrorHandler`, MQTT5/ZeroMQ `OnError`) |
| `PipePort[T]` stage wiring | stream channel errors (`gstream.Stream.Errors`) | `stream.Drain`/`MapErr`/`Retry` in pipeline |

Common bind/setup typed errors:
- `PortBindError` (bind failure wrapper)
- `PortNoAdapterError` (IO/Latest/Duplex connect without adapter)
- `PortNoPipelineError` (ToolPort bind before `SetPipeline`)

For adapter-specific hooks, see:
- [HTTP server guide](http-server.md#pipeline-handlers-mapping-stream-errors-to-http-status)
- [MQTT 5 guide](mqtt5.md#error-handling)
- [ZeroMQ guide](zeromq.md#error-handling)
- [Error handling guide](error-handling.md#where-to-handle-errors-adapters-ports-pipelines)
- [Error handling guide — store/IO boundaries (SQL/Cache/File)](error-handling.md#storeio-boundaries-sql-cache-file--handlelog-by-default) —
  `SinkAdapter.OnError` (SQL/Cache/File) already realizes the shared
  `handle`/`log` actions; compose it with a declared `events.ErrorChannel`
  for a `respond`-equivalent typed error publish.

### `SourcePort[T]` — inbound boundary

```go
// domain/pipeline.go — no adapter imports; port declared with just its shape
var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{Buffer: 8}))

// SensorReadingsPattern is a standalone, reusable value — declared once,
// independent of any specific port-construction call.
var SensorReadingsPattern = ports.EventPattern{
    Topic: "sensors/{sensorID}/data",
    Opts:  []events.ChannelOpt{events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec)},
}

func StartPipeline(ctx context.Context) {
    readings := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, readings, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — all protocol decisions here; PluginEventPattern registers the
// pattern AND returns its typed handle in one call.
sensorHandle, err := domain.SensorReadings.PluginEventPattern(domain.SensorReadingsPattern)
domain.SensorReadings.Bind(ctx,
    mqtt5.SubscribeAdapter(client, router, sensorHandle, 0,
        format.JSON(domain.ReadingCodec),
        mqtt5.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"}))

// Fan-in: add a second source without touching pipeline code
// (HTTP ingest: plug in ports.RESTPattern{Method: "POST", Path: ...} instead)
domain.SensorReadings.Bind(ctx,
    nethttp.IngestAdapter(mux, ingestHandle, nethttp.IngestAdapterOptions{Buffer: 8}))
```

### `SinkPort[T]` — outbound boundary

```go
// domain/pipeline.go
var OEEResults = codex.Must(ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{Buffer: 8}))

var OEEResultsPattern = ports.EventPattern{Topic: "alerts/{sensorID}"}

// main.go — fan-out: both adapters receive every item
alertHandle, err := domain.OEEResults.PluginEventPattern(domain.OEEResultsPattern)
domain.OEEResults.Bind(ctx,
    mqtt5.PublishAdapter(client, alertHandle,
        format.JSON(domain.OEECodec), mqtt5.MQTT5DrainPublishOptions{}))
domain.OEEResults.Bind(ctx,
    nethttp.SSEAdapter(mux, sseHandle, nethttp.SSEAdapterOptions{}))
```

### `IOPort[Req, Resp]` — intermediate IO

Swap the enrichment source without changing pipeline code:

```go
// domain/pipeline.go — declare the port shape once; NewIOPort returns (port, error)
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec, ports.PortOptions{}))

var CalibrationPattern = ports.RESTPattern{Method: "GET", Path: "/calibration/{sensorID}"}

func StartPipeline(ctx context.Context) {
    raw        := SensorReadings.Stream(ctx)
    calibrated := Calibration.Connect(ctx, raw)       // ← IOPort in the middle
    oeeStream  := gstream.Apply(ctx, calibrated, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — choose ONE enrichment source; PluginRESTPattern returns the handle:
handle, err := domain.Calibration.PluginRESTPattern(domain.CalibrationPattern)
domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, "http://calib-svc", handle, callOpts))
// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(db, calibCodec, queryFn, opts))       // no Pattern — file/sql use Params instead
// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine, opts))
// domain.Calibration.Bind(ctx, mqtt5.CallAdapter(client, router, reqReplyHandle, callOpts)) // via PluginReqReplyPattern
// domain.Calibration.Bind(ctx, zeromq.CallAdapter(sock, reqReplyHandle, opts))
```

Or, for a single-transport `IOPort`, use the convenience constructor that
combines the two steps: `port, handle := codex.Must2(ports.NewRestPort(...))`.

Plain Go without stream composition: `resp, err := domain.Calibration.Call(ctx, req)`
invokes the bound adapter directly — same declaration and `Bind` as above.
`Call` returns `PortNoResponseError` if the adapter produced zero items.

### `ToolPort[In, Out]` — server-side request/response

The complement of `IOPort`: instead of the pipeline calling out, an external caller
triggers the pipeline and waits for a response. Set the pipeline function once with
`SetPipeline`, then bind it to one or more transports — the **same pipeline logic**
can serve MCP, HTTP, and ZeroMQ simultaneously.

```go
// domain/pipeline.go — no adapter imports; declare the port shape once
var OEETool = codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{}))

var OEERESTPattern = ports.RESTPattern{Method: "POST", Path: "/oee/calc"}
var OEEReqReplyPattern = ports.ReqReplyPattern{Topic: "oee/calc"}
var OEEMCPPattern = ports.MCPPattern{Name: "oee-calc"}

func init() {
    OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
        return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
    })
}

// main.go — serve the same pipeline on three transports; each Plugin call
// registers its own Pattern and returns the typed handle:
mcpToolHandle, err := domain.OEETool.PluginMCPPattern(domain.OEEMCPPattern)
httpHandle, err := domain.OEETool.PluginRESTPattern(domain.OEERESTPattern)
zmqHandle, err := domain.OEETool.PluginReqReplyPattern(domain.OEEReqReplyPattern)
domain.OEETool.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, mcpToolHandle, mcpgo.Options{}))
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))
domain.OEETool.Bind(ctx, zeromq.ServeAdapter(repSock, zmqHandle, zeromq.ServeOptions{}))
```

`Bind` returns `PortBindError` wrapping `PortNoPipelineError` if `SetPipeline` was not
called first. Multiple `Bind` calls are allowed — each exposes the same pipeline on a
different transport concurrently.

Plain Go without `gstream`: replace the `SetPipeline` call with `SetFunc`,
registering a plain `func(context.Context, In) (Out, error)`. The three
`PluginXxxPattern`/`Bind` calls are unchanged. `SetFunc` and `SetPipeline`
are mutually exclusive — the later call wins.

### `LatestPort[T]` — reactive-cache boundary

Serves a continuously updated "current state" value to request/response
clients — no per-request pipeline run. `Feed` drains a stream into the port's
atomic cell; bound adapters answer from the cell, and keep answering after the
stream terminates (the cache outlives the pipeline).

```go
// domain — declared like every other boundary
var Latest = codex.Must(ports.NewLatestPort[db.Reading]("rest/latest", readingCodec,
    ports.PortOptions{}))

var LatestPattern = ports.RESTPattern{Method: "GET", Path: "/readings/latest"}

// main.go
handle, err := domain.Latest.PluginRESTPattern(domain.LatestPattern)
must(domain.Latest.Bind(ctx, nethttp.LatestAdapter(mux, handle, nethttp.Options{})))
go domain.Latest.Feed(ctx, readings)
```

Patterns use `codex.Struct[struct{}]()` as the request codec automatically —
`RESTPattern`, `ReqReplyPattern`, and `MCPPattern` are supported. Empty-cache
behavior is per-transport (HTTP 503 + `NoLatestValueError`, ZeroMQ error
reply, MCP error result).

### `DuplexPort[In, Out]` — bidirectional session boundary

External peers send `In` frames and receive `Out` frames over persistent,
identified sessions (WebSocket connections). Frames are session-tagged
`ports.Framed[T]` values — echo the inbound `Session` on an outbound frame
for a targeted reply, or leave it zero to broadcast. Exactly one adapter.

```go
// domain
var Live = codex.Must(ports.NewDuplexPort[Command, Update]("live",
    commandCodec, updateCodec, ports.PortOptions{}))

var LivePattern = ports.SocketPattern{Path: "/live/{room}"}

// main.go
hub := websocket.NewHub(0)
handle, err := domain.Live.PluginSocketPattern(domain.LivePattern)
must0(domain.Live.Bind(ctx, websocket.DuplexSocketAdapter(mux, hub, upgrader, handle, opts)))

// pipeline
replies := stream.Map(ctx, domain.Live.Inbound(ctx), ack, stream.MapOptions{Name: "ack"})
go domain.Live.Feed(ctx, replies)
```

`hub.SessionInfo(session)` exposes upgrade-time path vars (which `{room}` a
session joined); `stream.GroupBy` by `Framed.Session` gives per-client
sub-streams. See [`examples/websocket-duplex`](https://github.com/DaniDeer/go-codex/tree/main/examples/websocket-duplex).

For one-struct convenience, declare `SocketPattern.InOpts` / `OutOpts` with
`ports.NewRequiredSocketInParam` / `ports.NewRequiredSocketOutParam` (or the
optional variants). The WebSocket adapters then merge upgrade vars into each
inbound/outbound payload automatically.

### `PipePort[T]` — pipeline stage boundary

A named waypoint for **computation segmentation only** — a thin wrapper
over `gstream`, declared flexibly at setup and never mutated at runtime.
Use `ports.Chain`/`ports.ChainStream` to connect stages; side observers
tap into any stage without changing the logic.

```go
var Raw   = codex.Must(ports.NewPipePort[SensorReading]("raw", readingCodec, ports.PortOptions{}))
var Clean = codex.Must(ports.NewPipePort[ValidatedReading]("clean", validCodec, ports.PortOptions{}))

// Chain wraps Stream+Map+Feed into one call — the common case.
ports.Chain(ctx, Raw, validate, Clean)

Raw.OutputPort("log").Bind(ctx, ports.ChanSinkAdapter(logCh)) // side observer
Raw.Connect(ctx)
Clean.Connect(ctx)
```

**`Chain`/`ChainStream` are generalized to also accept boundary ports** —
`from` can be a `*PipePort[In]` OR a `*SourcePort[In]`; `to` can be a
`*PipePort[Out]` OR a `*SinkPort[Out]`. This is what makes the data flow
directly visible from the declaration, top to bottom, using the exact
same call shape for a real IO boundary and an internal stage alike:

```go
// SourcePort -> Chain -> PipePort -> ChainStream -> SinkPort, one
// declaration, no separate IO-bridging sub-ports.
ports.Chain(ctx, Sensors, buildInsertParams, Params)       // SourcePort -> PipePort
ports.ChainStream(ctx, Params, persistAndFilterAlerts, Alerts) // PipePort -> SinkPort
```

`fn`/`transform` need not be wrapped in `forge.Function` — pass a plain Go
function directly unless the step needs `forge`'s contract-hash/signing
governance.

**`ports.ChainStream[In, Out](ctx, from, transform, to)` is the general
stage connector — `ports.Chain` is its single-Map special case**, not a
separate mechanism (`Chain` is implemented in terms of `ChainStream`
internally). When a stage needs more than one step, call `ChainStream`
directly instead of writing a hand-rolled wrapper function:

```go
// Multi-step transition — ChainStream accepts ANY stream transform,
// so it takes as many Map/Filter calls as the stage needs, with the
// SAME (ctx, from, to) call shape as the single-step Chain above.
ports.ChainStream(ctx, Valid, func(s gstream.Stream[Validated]) gstream.Stream[Calibrated] {
    s2 := gstream.Map(ctx, s, calibrate, gstream.MapOptions{})
    s3 := gstream.Map(ctx, s2, classify, gstream.MapOptions{})
    return gstream.Map(ctx, s3, annotate, gstream.MapOptions{})
}, Calibrated)
```

`Stream()` is available directly for cases `ChainStream` doesn't cover
(e.g. fanning one stream into several independently-wired downstream
pipes). `Push(ctx, v)` feeds items into the pipe at any time — even before
`Connect()` (items buffer until Connect starts draining).
`InputPort(name)`/`OutputPort(name)` build plain `SourcePort`/`SinkPort`
sub-ports for side-observer taps only (no `Pattern` involved) — a real IO
boundary should be its own `SourcePort`/`SinkPort`/`IOPort`/`ToolPort`,
declared and Chain/ChainStream'd directly as shown above.

`Connect`'s data path is fully instrumented: `RecordSubscribe` fires on the
Push-consumer, `RecordPublish` fires per fan-out destination; `Chain`/
`ChainStream` wrap edge-setup in a `"pipe.chain"` `TraceObserver` span.

**Modular pipeline composition**: `Chain` and `ChainStream` calls compose
identically inside one top-level pipeline builder:

```go
func BuildPipeline(ctx context.Context) PipelineIO {
    ports.Chain(ctx, Raw, validate, Valid)
    ports.ChainStream(ctx, Valid, calibrationTransform, Calibrated)
    // ... observers, adapter binding, Connect calls, return PipelineIO ...
}
```

`PipePort`/codec/type declarations stay package `var`s — they have no side
effects, just like a `codex.Codec` or `rest.Route` declaration. Wiring
(`Chain`, `ChainStream`, `Connect`) stays in `ctx`-scoped functions — it
starts goroutines, so it needs a caller-supplied `ctx` and stays an
explicit function call, never a `var`. This mirrors
[`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service)'s
`pipeline.Build(ctx, ...)` convention exactly.

**One ordering rule**: register `InputPort`/`OutputPort`/`Stream`/`Chain`/
`ChainStream` for a pipe before that pipe's `Connect()`. `Push` has no
ordering restriction. `Chain`/`ChainStream` only need to precede the
upstream pipe's `Connect`.

**Spec generation, derived not hand-typed**: `ports.PipelineSpec(title, version, pipes...)`
reads pipe names, buffer sizes, bound adapter identities, and `Chain`/
`ChainStream` edges (with the transform's real function name, via
reflection) directly from the pipes — only `title`/`version`/ordering stay
manual:

```go
spec := ports.PipelineSpec("Sensor Pipeline", "1.0.0", Raw, Valid, Calibrated)
yamlBytes, _ := streamrender.Render(spec)
```

`*PipePort[T]` implements `PipeSpecSource` for any `T`, so heterogeneous
pipes (different payload types per stage) can be passed to one call.

See [`examples/pipeline-segmentation`](https://github.com/DaniDeer/go-codex/tree/main/examples/pipeline-segmentation)
for a full 3-stage demo including derived spec generation.

**Lifecycle supervision**: `Done() <-chan struct{}` closes only after
`Connect`'s internal goroutines fully exit — pair it with
[`app.App.Supervise`](app.md) instead of hand-rolling a fire-and-forget
goroutine:

```go
app.Supervise("raw-pipe", func(ctx context.Context) <-chan struct{} {
    Raw.Connect(ctx)
    return Raw.Done()
})
```

### `SinkPort` Push — request-scoped submission

When a request/response pipeline needs to drop individual items into a sink
(e.g. a REST-triggered export writing a file), use the port-owned lifecycle
instead of hand-rolling a channel + `Feed` goroutine:

```go
exports.Bind(appCtx, file.DrainWriteFileAdapter(exportFile, varsFor, opts))
exports.Start(appCtx)                  // port-owned channel + drain goroutine
_ = exports.Push(ctx, snapshot)        // from anywhere; blocks with backpressure
must(exports.Close(), "close exports") // waits for in-flight Push + adapter drain
```

`Push` returns `PortNotStartedError` before `Start`, after `Close`, or on a
`Feed`-driven port — the two lifecycles are mutually exclusive.

## Available adapters

### Source adapters (for `SourcePort`)

| Transport | Constructor | Description |
|-----------|-------------|-------------|
| MQTT5 | `mqtt5.SubscribeAdapter` | Subscribes to broker + router; full validation pipeline |
| MQTT | `mqtt.SubscribeAdapter` | MQTT v3/v3.1.1 subscription |
| HTTP (ingest, nethttp) | `nethttp.IngestAdapter` | Accepts POST requests as stream items |
| HTTP (ingest, chi) | `chi.IngestAdapter` | Same, via chi router |
| HTTP (poll) | `nethttp.PollAdapter` | Polls an endpoint at interval |
| ZeroMQ | `zeromq.SubscribeAdapter` | PUB/SUB or PULL socket receive loop |
| File (scan) | `file.ScanAdapter` | Reads a file line-by-line (NDJSON, CSV) |
| File (watch) | `file.WatchAdapter` | Emits paths for new files in a directory |
| SQL | `sql.QueryAdapter` | Polls a SQL query at interval |

### Sink adapters (for `SinkPort`)

| Transport | Constructor | Description |
|-----------|-------------|-------------|
| MQTT5 | `mqtt5.PublishAdapter` | Publishes each item via MQTT5 |
| MQTT | `mqtt.PublishAdapter` | Publishes each item via MQTT |
| HTTP (SSE, nethttp) | `nethttp.SSEAdapter` | Serves each item as an SSE event to all connected clients |
| HTTP (SSE, chi) | `chi.SSEAdapter` | Same, via chi router |
| HTTP (drain) | `nethttp.DrainCallAdapter` | POSTs each item; response discarded |
| ZeroMQ | `zeromq.PublishAdapter` | Publishes each item to a PUB/PUSH socket |
| File (line) | `file.DrainWriteAdapter` | Encodes each item as a line (NDJSON) |
| File (whole) | `file.DrainWriteFileAdapter` | Writes each item as a complete typed file |
| File (patch) | `file.DrainPatchAdapter` | Applies each item as an untyped `map[string]any` partial update |
| File (patch, typed) | `file.DrainPatchEncodedAdapter` | Applies each item as a typed partial update via a patch codec |
| SQL | `sql.DrainInsertAdapter` | Validates and inserts each item via insertFn |

### IO adapters (for `IOPort`)

| Transport | Constructor | Cardinality | Description |
|-----------|-------------|-------------|-------------|
| HTTP | `nethttp.CallAdapter` | 1→1 | HTTP request per item, emits each response |
| MQTT5 | `mqtt5.CallAdapter` | 1→1 | MQTT5 request-reply per item |
| ZeroMQ | `zeromq.CallAdapter` | 1→1 | ZeroMQ REQ/REP per item |
| SQL | `sql.QueryEachAdapter` | 1→N | Parameterized SQL query per item |
| File | `file.ReadAdapter` | 1→1 | File read per item — the file content **is** the response (pairs with `FilePattern`) |
| File | `file.ReadEachAdapter` | 1→1 | File read per item with independent content type + `combine` func (enrichment; handle-first) |

### Tool adapters (for `ToolPort`)

| Transport | Constructor | Description |
|-----------|-------------|-------------|
| MCP | `mcpgo.ToolPipelineAdapter` | Registers the pipeline as an MCP tool; fresh run per call |
| HTTP (nethttp) | `nethttp.PipelineAdapter` | Registers the pipeline as an HTTP endpoint |
| HTTP (chi) | `chi.PipelineAdapter` | Same, via chi router |
| ZeroMQ | `zeromq.ServeAdapter` | Starts a REP loop running the pipeline (background goroutine) |
| MQTT5 | `mqtt5.ServeAdapter` | Starts a request/reply server running the pipeline (background goroutine) |

### Latest adapters (for `LatestPort`)

| Transport | Constructor | Description |
|-----------|-------------|-------------|
| HTTP | `nethttp.LatestAdapter` | GET endpoint served from the port's cache cell (503 before first value) |
| HTTP (chi) | `chi.LatestAdapter` | Same semantics, on a chi router |
| ZeroMQ | `zeromq.LatestAdapter` | Blocking REP loop answering from the cell (error reply before first value) |
| MCP | `mcpgo.LatestAdapter` | MCP tool answering from the cell (error result before first value) |

## Test adapters

Test your pipeline without a real transport:

```go
// Test source
ch := make(chan SensorReading, 2)
ch <- reading1; ch <- reading2; close(ch)
domain.SensorReadings.Bind(ctx, ports.ChanSourceAdapter(ch))

// Test sink
out := make(chan OEE, 8)
domain.OEEResults.Bind(ctx, ports.ChanSinkAdapter(out))

// Test IO port
domain.Calibration.Bind(ctx, ports.FuncIOAdapter(func(ctx context.Context, r SensorReading) (CalibratedReading, error) {
    return CalibratedReading{Reading: r, Offset: 0.0}, nil
}))
```

## `Pattern` — declare the wire shape once

Every example above declares its communication pattern via `PortOptions.Patterns` —
this is the primary, recommended way to wire a handle-backed port (REST, events,
reqreply, MCP). It reuses the exact vocabulary you already know from
`rest.NewRoute`/`events.NewChannel`/`reqreply.NewRoute`/`apimcp.NewTool`
(`PathParam`, `QueryParam`, `TopicParam`, `RouteMeta`, …) — declared once, directly
on the port, instead of in a separate `Route`/`Channel`/`Tool` value that then has to
be `.Register()`ed with a builder and threaded into the adapter constructor by hand.

| Pattern | Protocol family |
|---------|------------------|
| `ports.RESTPattern{Method, Path, Opts}` | HTTP (nethttp, chi) |
| `ports.EventPattern{Topic, Opts}` | pub/sub (mqtt, mqtt5, zeromq) |
| `ports.ReqReplyPattern{Topic, Opts}` | request/reply (mqtt5, zeromq) |
| `ports.MCPPattern{Name, Opts}` | MCP tool (mcpgo) |
| `ports.FilePattern{Path, Format, CustomFormat, Opts}` | typed files (file) |
| `ports.SQLPattern{Table, Op}` | SQL (sql) — metadata-only |
| `ports.CachePattern{Key, TTL, Format, CustomFormat, Opts}` | key/value cache (redis) — key template + TTL; `Opts` = `CacheKeyParam` per-key-var codecs |
| `ports.SocketPattern{Path, Subprotocols, Format, CustomFormat, Opts, InOpts, OutOpts}` | duplex socket (websocket) — upgrade-time validation + connection-var merge for inbound/outbound payloads |

`CustomFormat` (on `FilePattern`/`CachePattern`/`SocketPattern`) is the
escape hatch for binary/custom wire formats the `Format` enum
(JSON/YAML/TOML) can't express — a pre-built `format.Format[T]` (`format.Gob`,
`format.Binary` for PNG/PDF, or any custom format), overriding `Format` when
non-nil:

```go
ports.FilePattern{Path: "images/{id}.png",
    CustomFormat: format.Binary(pngCodec).WithContentType("image/png")}
ports.CachePattern{Key: "session:{id}", CustomFormat: format.Gob(sessionCodec)}
```

A type mismatch returns `PatternRegisterError` at construction. See
[`examples/pattern-custom-format`](https://github.com/DaniDeer/go-codex/tree/main/examples/pattern-custom-format).

`RESTPattern`/`EventPattern`/`ReqReplyPattern` don't need a `CustomFormat`
field — their built handles already accept **any** `format.Format[T]`
(with real multi-format content negotiation, not a single fixed format) via
`WithRequestFormats`/`WithFormats`/`WithSubscribeFormats`/`WithPublishFormats`.
Declare them inline in `Opts` — `rest.RequestFormats(...)`/`rest.Formats(...)`
and `events.Formats(...)`/`SubscribeFormats(...)`/`PublishFormats(...)` and
`reqreply.RequestFormats(...)`/`Formats(...)` are `RouteOpt`/`ChannelOpt`
values, same interface `PathParam`/`TopicParam` implement, so `ports` needs
no changes to support them:

```go
ports.RESTPattern{Method: "PUT", Path: "/images/{id}",
    Opts: []rest.RouteOpt{rest.RequestFormats(format.Binary(pngCodec).WithContentType("image/png"))}}
```

A type mismatch returns `rest.FormatOptError`/`events.FormatOptError`/
`reqreply.FormatOptError` from `Register` (surfaces as `PatternRegisterError`
from the `PluginXxxPattern` call). See the [HTTP Server Examples](http-server.md#same-thing-declared-through-portsrestpattern)
and [MQTT Examples](mqtt.md#same-thing-declared-through-portseventpattern) guides.

Plug in the Pattern the adapter needs with the matching method — registers
AND returns the typed handle in one call:

```go
handle, err := domain.SomePort.PluginRESTPattern(pattern)     // *rest.RouteHandle[Req, Resp]
handle, err := domain.SomePort.PluginEventPattern(pattern)    // *events.ChannelHandle[T]
handle, err := domain.SomePort.PluginReqReplyPattern(pattern) // *reqreply.RouteHandle[Req, Resp]
handle, err := domain.SomePort.PluginMCPPattern(pattern)      // *apimcp.ToolHandle[In, Out]
file,   err := domain.SomePort.PluginFilePattern(pattern)     // ports.File[T]
meta,   err := domain.SomePort.PluginSQLPattern(pattern)      // ports.SQLPattern
cache,  err := domain.SomePort.PluginCachePattern(pattern)    // ports.Cache[T]
socket, err := domain.SomePort.PluginSocketPattern(pattern)   // ports.Socket[In, Out]
```

### One construction path, whether you supply a `Builder` or not

Internally, a `Pattern` always becomes a handle via the **same**
`Route`/`Channel`/`Tool.Register(builder)` call a hand-declared route makes —
never the weaker, builder-free `ClientHandle()`. Supply your own `*Builder` via
`PortOptions` to get full parity with a hand-registered route (global security,
path/topic format constraints, shared spec accumulation); when you don't,
`ports` registers against a private, single-use `Builder` instead — same
zero-ceremony default, identical code path. For REST, security SCHEMES are
declared directly on the `RESTPattern`'s own `Opts` via `rest.WithSecurityScheme`
(no builder-level scheme registry for REST):

```go
restBuilder := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
restBuilder.AddGlobalSecurity(route.SecurityRequirement{"bearerAuth": {}})

oeeTool := codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{RESTBuilder: restBuilder}))
_, err := oeeTool.PluginRESTPattern(ports.RESTPattern{
    Method: "POST",
    Path:   "/oee/calc",
    Opts: []rest.RouteOpt{
        rest.WithSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}),
    },
})
if err != nil {
    panic(err)
}

// restBuilder already has /oee/calc registered — spec generation needs no
// separate step:
spec, _ := restBuilder.OpenAPISpec()
```

| `PortOptions` field | Pattern | Gives you |
|---|---|---|
| `RESTBuilder *rest.Builder` | `RESTPattern` | Global security, `rest.WithPathConstraints` (security SCHEMES are declared on the Pattern's own `Opts`) |
| `EventBuilder *events.Builder` | `EventPattern` | Security schemes, global security, `events.WithTopicConstraints` |
| `ReqReplyBuilder *reqreply.Builder` | `ReqReplyPattern` | Duplicate-topic detection |
| `MCPBuilder *apimcp.Builder` | `MCPPattern` | Duplicate-name detection |

> **Before this, every `Pattern`-derived handle silently had no security
> enforcement** — `SecuritySchemes` was always an empty map (the credential check
> skips unknown scheme names rather than rejecting), so any `RouteMeta.Security`/
> `Subscribe.Security`/`Publish.Security` requirement declared on a `Pattern`-based
> port had no effect. For REST, declare `rest.WithSecurityScheme(...)` in the
> `RESTPattern`'s `Opts` (plus a `Builder` with `AddGlobalSecurity`); for events,
> supply a `Builder` with `AddSecurityScheme`/`AddGlobalSecurity` — either fixes
> this for a given port.

If you already supplied a `Builder`, the port's route/channel/tool is already
registered with it — calling `RegisterREST`/etc. with that *same* builder
afterward is redundant. Use `Register*` only when you did **not** supply a
`Builder` up front and want to add the already-bound port to a *different* spec
document after the fact:

```go
b := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
ports.RegisterREST[OEEIn, OEEResult](b, domain.OEETool) //nolint:errcheck
spec, _ := b.OpenAPISpec()
```

`RegisterEvent`, `RegisterReqReply`, and `RegisterMCP` do the same for their
builders. `RegisterSocket[In,Out](b *events.Builder, port)` renders a
`SocketPattern` as an AsyncAPI channel (Subscribe = In frames the app
receives, Publish = Out frames it sends) — the WebSocket spec story, since
OpenAPI cannot express socket frames.

`NewSourcePort`, `NewSinkPort`, `NewIOPort`, and `NewToolPort` all return
`(*Port, error)` — construction never involves a `Pattern` (just the port's
structural shape). Every `PluginXxxPattern` call returns `(handle, error)`
and can fail (unknown param name, path/topic constraint failure, duplicate
name on a shared `reqreply`/`mcp` builder, or a duplicate Plugin call) —
wrap `codex.Must(...)` around construction for package-level declarations,
as shown throughout this guide.

> `RESTPattern` is role-aware on single-codec ports: on a `SourcePort[T]` it
> declares HTTP ingest (`RouteHandle[T, struct{}]` via `PluginRESTPattern`,
> pairs with `nethttp/chi.IngestAdapter`); on a `SinkPort[T]` it declares SSE
> (`SSERouteHandle[struct{}, T]`, always GET, pairs with
> `nethttp/chi.SSEAdapter`; replay with `RegisterSSE`). Both register against
> `PortOptions.RESTBuilder` — ingest and SSE endpoints appear in the shared
> OpenAPI spec.

### `FilePattern` — file as sink or intermediate IO step

Declare the file (path template + wire format + path-param codecs) as a
standalone `Pattern` value; plug it in with `PluginFilePattern` to get the
built `ports.File`. `Format` is a `ports.FileFormatKind` enum —
`FileFormatJSON` (default), `FileFormatYAML`, `FileFormatTOML` — applied to
the port's own codec. On a `SinkPort[T]` the handle is `ports.File[T]`
(pairs with `file.DrainWriteFileAdapter`); on an `IOPort[Req,Resp]` it is
`ports.File[Resp]` — the file's content *is* the port's response —
pairing with the 2-type `file.ReadAdapter`:

For partial updates instead of a whole-file overwrite, pair a hand-built
`ports.File[T]` with `file.DrainPatchAdapter` (untyped `map[string]any`
patch) or `file.DrainPatchEncodedAdapter` (typed patch via a patch codec) —
both stay handle-first since the patch item's type deliberately differs from
the port's own payload type; both require a map-based format (JSON/YAML/TOML).

```go
// domain — intermediate IO step: read a calibration file per reading
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibrationData](
    "calibration", readingCodec, calibrationCodec, ports.PortOptions{}))

var CalibrationFilePattern = ports.FilePattern{
    Path: "data/{sensorID}/calibration.json",
    Opts: []ports.FileOpt{ports.FilePathParam{Name: "sensorID"}.WithCodec(uuidCodec)},
}

// main.go
calibFile, err := domain.Calibration.PluginFilePattern(domain.CalibrationFilePattern)
domain.Calibration.Bind(ctx, file.ReadAdapter(calibFile,
    func(r SensorReading) map[string]string { return map[string]string{"sensorID": r.SensorID} },
    file.ReadEachAdapterOptions{}))

// pipeline — identical whether the enrichment comes from a file, SQL, or HTTP
calibrated := domain.Calibration.Connect(ctx, readings)
```

For a custom `format.Format[T]` beyond JSON/YAML/TOML, or for the 3-type
enrichment shape (`file.ReadEachAdapter` with a `combine` func), build the
`ports.File` by hand — same as before.

### `SQLPattern` — declare table/op metadata once

SQL has no template to parse — queries stay typed, driver-specific closures.
`SQLPattern{Table, Op}` declares just the error/observability metadata,
plugged in with `PluginSQLPattern`; the sql adapters (`QueryAdapter`,
`QueryEachAdapter`, `DrainInsertAdapter`) default their options' `Table`/`Op`
from it via context when the explicit fields are empty (explicit values win):

```go
var Readings = codex.Must(ports.NewSinkPort[db.Reading]("sql/readings", readingCodec, ports.PortOptions{}))

var ReadingsSQLPattern = ports.SQLPattern{Table: "readings", Op: "insert_reading"}

// main.go — plug in first, no Table/Op repetition afterward
_, err := domain.Readings.PluginSQLPattern(domain.ReadingsSQLPattern)
domain.Readings.Bind(ctx, sql.DrainInsertAdapter(readingCodec, insertFn, sql.DrainInsertOptions{}))
```

Both patterns are demonstrated live in `examples/sensor-service` (see its
`ioports` package: `SQLPattern` on the `Readings` persistence, `History`
time-series, and `ExportQuery` `IOPort`s; `FilePattern` on the `Exports`
`SinkPort` — the REST export response path comes from the same declaration via
`FileHandle.BuildPath`). The same example also shows the recommended project
structure: pure forge functions in `pipeline/`, persistence and queries as
explicit port steps, adapters bound only in `main()`.

## `IOParam` — protocol-agnostic parameters (handle-less adapters)

`PortOptions.Params` is the enforcement mechanism for adapters with **no**
`Pattern`/handle of their own — `file.ReadEachAdapter`, `file.DrainWriteFileAdapter`,
`file.DrainPatchAdapter`, and `file.DrainPatchEncodedAdapter` (their `varsFor`
function extracts a `map[string]string`):

```go
// Declare once on the port — the adapter validates via context, not a hand-built handle
ports.IOParam{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)
```

The port propagates `Params` via context (`ports.WithParams`) and the adapter calls
`ports.ValidateParams` against each item's extracted `varsFor` map, surfacing
failures as `ReadError`/`WriteError` wrapping `codex.ValidationErrors`. For
handle-backed adapters, use `Pattern` instead — `Params` is not consulted there
since the derived handle already validates fully.

## Configuring pipeline functions from env vars

Env vars are **not** an IO boundary in the `ports` sense — they are a
construction-time concern. To parameterize a pipeline function (an alert
threshold, a batch size, …) from the environment, use the **validated-config
factory pattern**: load a typed config struct once in `main()` via
`config.FromEnv` (the codec is the env contract — names, coercion, constraints,
defaults), then pass it into a factory that closes over it. Zero `os.Getenv` in
pipeline code, fully testable. See
[Config guide — Passing env config into pipeline functions](config.md#passing-env-config-into-pipeline-functions)
and the live demonstration in `examples/sensor-service`
(`APP_ALERT_THRESHOLD=90 go run ./examples/sensor-service`).

## Lifecycle wiring with `app.App`

For services with several long-lived ports, let [`app`](../features/app.md)
own the root context and the teardown ordering instead of hand-rolling
context trees and done-channels in `main()`:

```go
a := app.New(app.Options{Observer: obs, Logger: logger})
ctx := a.Context() // observer pre-injected

exports.Bind(ctx, file.DrainWriteFileAdapter(exportFile, varsFor, opts))
exports.Start(ctx)
a.OnShutdown("exports", func(context.Context) error { return exports.Close() })

a.Go("alerts-feed", func(ctx context.Context) error {
    alerts.Feed(ctx, alertPayloads)
    return nil
})

return a.Run(context.Background()) // SIGINT/SIGTERM → hooks run LIFO
```

`examples/sensor-service` demonstrates this live (demo variant: it calls
`a.Shutdown()` directly instead of the signal-driven `Run`).

## Cache patterns (not port-based)

These patterns are a different shape from `ToolPort` — they serve the **most recently
computed value** rather than running the pipeline per call. Use them directly (not via
`ports`) when the response should not block on a fresh computation:

| Pattern | Where it lives |
|---------|---------------|
| `nethttp.HandlerLatest` / `RegisterLatest` | HTTP GET endpoint serving latest stream value |
| `chi.HandlerLatest` / `RegisterLatest` | Same, via chi router |
| `zeromq.ServeLatest` | ZMQ REP loop serving latest stream value |
| `mcpgo.ToolLatestHandler` / `RegisterToolLatest` | MCP tool serving latest stream value (for port-based wiring use `LatestPort` + `mcpgo.LatestAdapter`) |

## Underlying handler functions (used internally by Tool adapters)

`ToolPort`'s Tool adapters wrap these functions — use them directly only for standalone
(non-`ports`) wiring:

| Pattern | Where it lives |
|---------|---------------|
| `nethttp.PipelineHandler` / `RegisterPipeline` | HTTP trigger → pipeline → response |
| `chi.PipelineHandler` / `RegisterPipeline` | Same, via chi router |
| `zeromq.AsPipelineFunc` | Wraps a forge pipeline fn for `Serve`/`ServeRouter` |
| `mqtt5.AsPipelineFunc` | Wraps a forge pipeline fn for `Serve` |
| `mcpgo.ToolPipelineHandler` / `RegisterToolPipeline` | MCP tool trigger → pipeline → response |

`nethttp`/`chi` pipeline handlers support per-route stream-error status mapping
via `rest.ErrorStatus[...]`; `ToolPort + PipelineAdapter` inherits the
same behavior because adapters delegate to those handlers.
