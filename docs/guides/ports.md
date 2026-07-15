# Wiring Pipelines with Ports

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service)

go-codex pipelines are wired to the outside world using **port adapters** — a declarative,
protocol-agnostic binding pattern that keeps domain/pipeline code free of transport imports.

## Inside-out development

Define domain logic first; connect to transports last:

```
Step 1 — Domain core (no adapter imports)
    codex.Codec[T]                       ← validated domain types
    forge.NewFunction[In, Out](...)      ← governed pure computation
    ports.NewSourcePort / SinkPort /     ← IO enforcement points
        IOPort / ToolPort

Step 2 — Wiring (main.go only)
    port.Bind(ctx, transport.XxxAdapter(...))  ← connect to real transport
```

## Four port types

### `SourcePort[T]` — inbound boundary

```go
// domain/pipeline.go — no adapter imports
var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Params: []ports.IOParam{
            {Name: "sensorID", Required: true}.WithCodec(sensorIDCodec),
        },
        Buffer: 8,
    })

func StartPipeline(ctx context.Context) {
    readings := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, readings, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — all protocol decisions here
domain.SensorReadings.Bind(ctx,
    mqtt5.SubscribeAdapter(client, router, sensorHandle, 0,
        format.JSON(domain.ReadingCodec),
        mqtt5.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"}))

// Fan-in: add a second source without touching pipeline code
domain.SensorReadings.Bind(ctx,
    nethttp.IngestAdapter(mux, ingestHandle, nethttp.IngestAdapterOptions{Buffer: 8}))
```

### `SinkPort[T]` — outbound boundary

```go
// domain/pipeline.go
var OEEResults = ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{Buffer: 8})

// main.go — fan-out: both adapters receive every item
domain.OEEResults.Bind(ctx,
    mqtt5.PublishAdapter(client, alertHandle,
        format.JSON(domain.OEECodec), mqtt5.MQTT5DrainPublishOptions{}))
domain.OEEResults.Bind(ctx,
    nethttp.SSEAdapter(mux, sseHandle, nethttp.SSEAdapterOptions{}))
```

### `IOPort[Req, Resp]` — intermediate IO

Swap the enrichment source without changing pipeline code:

```go
// domain/pipeline.go
var Calibration = ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec,
    ports.PortOptions{Params: []ports.IOParam{{Name: "sensorID"}.WithCodec(sensorIDCodec)}})

func StartPipeline(ctx context.Context) {
    raw        := SensorReadings.Stream(ctx)
    calibrated := Calibration.Connect(ctx, raw)       // ← IOPort in the middle
    oeeStream  := gstream.Apply(ctx, calibrated, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — choose ONE enrichment source:
domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, "http://calib-svc", handle, callOpts))
// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(db, calibCodec, queryFn, opts))
// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine, opts))
// domain.Calibration.Bind(ctx, mqtt5.CallAdapter(client, router, handle, callOpts))
// domain.Calibration.Bind(ctx, zeromq.CallAdapter(sock, handle, opts))
```

### `ToolPort[In, Out]` — server-side request/response

The complement of `IOPort`: instead of the pipeline calling out, an external caller
triggers the pipeline and waits for a response. Set the pipeline function once with
`SetPipeline`, then bind it to one or more transports — the **same pipeline logic**
can serve MCP, HTTP, and ZeroMQ simultaneously.

```go
// domain/pipeline.go — no adapter imports
var OEETool = ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec, ports.PortOptions{})

func init() {
    OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
        return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
    })
}

// main.go — serve the same pipeline on three transports:
domain.OEETool.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, mcpToolHandle, mcpgo.Options{}))
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))
domain.OEETool.Bind(ctx, zeromq.ServeAdapter(repSock, zmqHandle, zeromq.ServeOptions{}))
```

`Bind` returns `PortBindError` wrapping `PortNoPipelineError` if `SetPipeline` was not
called first. Multiple `Bind` calls are allowed — each exposes the same pipeline on a
different transport concurrently.

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
| SQL | `sql.DrainInsertAdapter` | Validates and inserts each item via insertFn |

### IO adapters (for `IOPort`)

| Transport | Constructor | Cardinality | Description |
|-----------|-------------|-------------|-------------|
| HTTP | `nethttp.CallAdapter` | 1→1 | HTTP request per item, emits each response |
| MQTT5 | `mqtt5.CallAdapter` | 1→1 | MQTT5 request-reply per item |
| ZeroMQ | `zeromq.CallAdapter` | 1→1 | ZeroMQ REQ/REP per item |
| SQL | `sql.QueryEachAdapter` | 1→N | Parameterized SQL query per item |
| File | `file.ReadEachAdapter` | 1→1 | File read per item with path template vars |

### Tool adapters (for `ToolPort`)

| Transport | Constructor | Description |
|-----------|-------------|-------------|
| MCP | `mcpgo.ToolPipelineAdapter` | Registers the pipeline as an MCP tool; fresh run per call |
| MCP (cache) | `mcpgo.ToolLatestAdapter` | Registers an MCP tool backed by a reactive cache stream (ignores the pipeline fn — response comes from the stream) |
| HTTP (nethttp) | `nethttp.PipelineAdapter` | Registers the pipeline as an HTTP endpoint |
| HTTP (chi) | `chi.PipelineAdapter` | Same, via chi router |
| ZeroMQ | `zeromq.ServeAdapter` | Starts a REP loop running the pipeline (background goroutine) |
| MQTT5 | `mqtt5.ServeAdapter` | Starts a request/reply server running the pipeline (background goroutine) |

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

## `IOParam` — protocol-agnostic parameters

Ports carry `IOParam` declarations for routing parameters. What happens with them
at `Bind` time depends on the adapter:

| IOParam role | REST | MQTT/ZeroMQ | MQTT5 extra | File |
|---|---|---|---|---|
| Routing var | `PathParam {name}` (builder-validated) | `TopicParam {name}` (builder-validated) | — | `FilePathParam {name}` (`ports.ValidateParams`-validated) |
| Metadata | `HeaderParam`, `QueryParam` (builder-validated) | — | `UserPropertyParam` (builder-validated) | — |

```go
// Declare once on the port — adapters map names automatically
ports.IOParam{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)
```

Adapters backed by a protocol-level builder (`rest.Route`, `events.ChannelHandle`,
MQTT 5's `UserPropertyParam`) validate their own declarations at that layer — the
port's `Params` is descriptive there. `file.ReadEachAdapter` and
`file.DrainWriteFileAdapter` have no such builder: the port propagates `Params` via
context (`ports.WithParams`) and the adapter calls `ports.ValidateParams` against
each item's extracted `varsFor` map, surfacing failures as `ReadError`/`WriteError`.

## Cache patterns (not port-based)

These patterns are a different shape from `ToolPort` — they serve the **most recently
computed value** rather than running the pipeline per call. Use them directly (not via
`ports`) when the response should not block on a fresh computation:

| Pattern | Where it lives |
|---------|---------------|
| `nethttp.HandlerLatest` / `RegisterLatest` | HTTP GET endpoint serving latest stream value |
| `chi.HandlerLatest` / `RegisterLatest` | Same, via chi router |
| `zeromq.ServeLatest` | ZMQ REP loop serving latest stream value |
| `mcpgo.ToolLatestHandler` / `RegisterToolLatest` | MCP tool serving latest stream value (also available as `mcpgo.ToolLatestAdapter` for `ToolPort.Bind`) |

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
