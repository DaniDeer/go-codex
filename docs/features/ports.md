# Protocol-Agnostic Pipeline Wiring — `ports`

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [Forge Pipelines concept](../concepts/pipelines.md) · [Stream Bridge Guide](../guides/ports.md) · [Roadmap: Inside-Out Pipeline Wiring](../roadmap/inside-out-pipeline-wiring.md)
>
> Runnable demo: [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — wires MQTT, HTTP, and SQL adapters to `ports.SourcePort`/`SinkPort` around a shared pipeline.

`ports` is the protocol-agnostic wiring layer for go-codex pipelines. It lets you write
domain logic and pipeline composition with **zero adapter imports**, then decide the
transport (MQTT, HTTP, ZeroMQ, file, SQL, MCP) entirely in `main.go`.

---

## Motivation

Without a wiring layer, connecting a pipeline to the outside world means importing a
transport-specific package directly into the pipeline code — the domain logic and the
transport choice (MQTT5, in this illustration) become inseparable:

```go
// Illustrative — pipeline code that directly imports a transport package:
import "github.com/DaniDeer/go-codex/adapters/mqtt5"

func StartPipeline(ctx context.Context, client mqtt5.MQTTClient, router mqtt5.MQTTRouter) {
    // domain code now has a hard MQTT5 dependency — swapping transports means
    // rewriting this function.
}
```

Swapping MQTT for HTTP, or adding a second source, means editing the pipeline. With
`ports`, the pipeline only knows about a typed port — the adapter is bound separately:

```go
// domain/pipeline.go — no adapter imports
var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec, ports.PortOptions{})

func StartPipeline(ctx context.Context) {
    sensors := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, sensors, oeeCalcFn, gstream.ApplyOptions{})
    // ...
}

// main.go — the only place that knows about MQTT5
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, fmt, opts))
```

---

## Four port types

| Port | Direction | Cardinality | Use for |
|------|-----------|-------------|---------|
| [`SourcePort[T]`](#sourceportt) | External → pipeline | Fan-in (many adapters merge) | MQTT subscribe, HTTP ingest, SQL poll, file scan/watch |
| [`SinkPort[T]`](#sinkportt) | Pipeline → external | Fan-out (broadcast to all adapters) | MQTT publish, SSE, file write, SQL insert |
| [`IOPort[Req,Resp]`](#ioportreqresp) | Pipeline ↔ external | Exactly one adapter | HTTP call, SQL per-item query, file per-item read, MQTT5/ZeroMQ request-reply |
| [`ToolPort[In,Out]`](#toolportinout) | External request → pipeline → response | Exactly one pipeline fn, N adapters | MCP tool, HTTP endpoint, ZeroMQ REP, MQTT5 request-reply server |

---

## `SourcePort[T]`

Declares an inbound boundary. Bind one or more `SourceAdapter[T]` implementations; their
outputs are merged (fan-in) into a single stream.

```go
var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Params: []ports.IOParam{{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)},
        Buffer: 8,
    })

// main.go — bind one adapter, or several for fan-in:
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, fmt, opts))
domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(mux, ingestHandle, opts)) // fan-in

sensors := domain.SensorReadings.Stream(ctx) // gstream.Stream[SensorReading]
```

`Stream(ctx)` must be called after all `Bind` calls. It returns the merged stream;
adapter and codec validation errors are routed to `Stream.Errors`.

## `SinkPort[T]`

Declares an outbound boundary. Bind one or more `SinkAdapter[T]` implementations; every
item fed into the port is broadcast (fan-out) to all bound adapters. A failure in one
adapter does not stop delivery to the others.

```go
var OEEResults = ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{Buffer: 8})

domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, fmt, publishOpts))
domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, sseOpts)) // fan-out

go domain.OEEResults.Feed(ctx, oeeStream) // blocks until oeeStream terminates
```

## `IOPort[Req,Resp]`

Declares an intermediate transform boundary — the pipeline sends a `Req` out and
receives a `Resp` back. Exactly one `IOAdapter[Req,Resp]` may be bound; swapping the
adapter (HTTP → SQL → file) never touches pipeline code.

```go
var Calibration = ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec, ports.PortOptions{})

calibrated := domain.Calibration.Connect(ctx, sensors) // gstream.Stream[CalibratedReading]

// main.go — pick exactly one:
domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, calibHandle, callOpts))
// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(calibCodec, queryFn, opts))
// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine, opts))
```

`Connect` returns a stream carrying `PortNoAdapterError` in `Stream.Errors` if no
adapter was bound before the pipeline started.

## `ToolPort[In,Out]`

Declares a server-side request/response boundary — the complement of `IOPort` (which is
client-side). Set the pipeline function once with `SetPipeline`, then bind it to one or
more transports. The **same pipeline logic** can serve MCP, HTTP, and ZeroMQ simultaneously.

```go
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
called first.

---

## `IOParam` — protocol-agnostic parameters

Ports declare routing parameters once via `PortOptions.Params`. Enforcement depends
on the bound adapter:

- **Adapters with their own protocol-level builder** — `rest.Route` (`PathParam`,
  `QueryParam`, `HeaderParam`), `events.ChannelHandle` (`TopicParam`), MQTT 5
  (`UserPropertyParam`) — already validate their own declarations at that layer.
  `Params` is descriptive only here (available for future spec generation).
- **Adapters with no such builder** — `file.ReadEachAdapter`, `file.DrainWriteFileAdapter`
  (their `varsFor` function extracts a `map[string]string`) — get real runtime
  enforcement: the port propagates `Params` via context (`ports.WithParams`), and
  the adapter calls `ports.ValidateParams(ports.ParamsFromContext(ctx), vars)`
  before using the extracted values. A validation failure surfaces as `ReadError`/
  `WriteError` wrapping `codex.ValidationErrors`.

```go
ports.IOParam{Name: "sensorID", Description: "Sensor identifier", Required: true}.WithCodec(sensorIDCodec)
```

| IOParam role | REST (HTTP) | Events (MQTT/ZeroMQ) | MQTT5 extra | File |
|--------------|-------------|----------------------|-------------|------|
| Routing var  | `PathParam {name}` (builder-validated) | `TopicParam {name}` (builder-validated) | — | `FilePathParam {name}` (`ports.ValidateParams`-validated) |
| Metadata     | `HeaderParam`, `QueryParam` (builder-validated) | — | `UserPropertyParam` (builder-validated) | — |

`PortOptions{Params, Buffer, Observer}` configures all four port constructors.
`Buffer` only applies to `SourcePort`/`SinkPort` (`IOPort`/`ToolPort` have no
internal channel to buffer). `Observer` receives a `"port.bind"` `RecordRequest`
call (and `TraceObserver` span, when supported) wrapping every `Bind` call.

---

## Adapter interfaces

Each adapter package implements one or more of these interfaces via a `binding.go`
file. You normally use the provided constructors (`mqtt5.SubscribeAdapter`, etc.)
rather than implementing these directly.

| Interface | Method | Bound to |
|-----------|--------|---------|
| `SourceAdapter[T]` | `Activate(ctx, dst chan<-T, errs chan<-error)` | `SourcePort[T]` |
| `SinkAdapter[T]` | `Activate(ctx, src Stream[T])` | `SinkPort[T]` |
| `IOAdapter[Req,Resp]` | `Transform(ctx, src Stream[Req]) Stream[Resp]` | `IOPort[Req,Resp]` |
| `ToolAdapter[In,Out]` | `Bind(ctx, fn func(ctx,In)Stream[Out]) error` | `ToolPort[In,Out]` |

All four interfaces additionally require `AdapterName() string` for observability and
`PortBindError` context.

### Available adapters by transport

| Transport | Source | Sink | IO | Tool |
|-----------|--------|------|-----|------|
| MQTT5 | `SubscribeAdapter` | `PublishAdapter` | `CallAdapter` | `ServeAdapter` |
| MQTT | `SubscribeAdapter` | `PublishAdapter` | — | — |
| HTTP (nethttp) | `IngestAdapter`, `PollAdapter` | `SSEAdapter`, `DrainCallAdapter` | `CallAdapter` | `PipelineAdapter` |
| HTTP (chi) | `IngestAdapter` | `SSEAdapter` | — | `PipelineAdapter` |
| ZeroMQ | `SubscribeAdapter` | `PublishAdapter` | `CallAdapter` | `ServeAdapter` |
| File | `ScanAdapter`, `WatchAdapter` | `DrainWriteAdapter`, `DrainWriteFileAdapter` | `ReadEachAdapter` | — |
| SQL | `QueryAdapter` | `DrainInsertAdapter` | `QueryEachAdapter` | — |
| MCP (mcpgo) | — | — | — | `ToolPipelineAdapter`, `ToolLatestAdapter` |

---

## Test adapters

Test pipelines without a real transport:

```go
// Source: feed a plain channel
ch := make(chan SensorReading, 2)
ch <- reading1; close(ch)
domain.SensorReadings.Bind(ctx, ports.ChanSourceAdapter(ch))

// Sink: capture output in a plain channel
out := make(chan OEE, 8)
domain.OEEResults.Bind(ctx, ports.ChanSinkAdapter(out))

// IOPort: stub the enrichment call
domain.Calibration.Bind(ctx, ports.FuncIOAdapter(func(ctx context.Context, r SensorReading) (CalibratedReading, error) {
    return CalibratedReading{Reading: r, Offset: 0.0}, nil
}))
```

---

## Structured errors

All error types implement `Error()`, `Unwrap()` (where applicable), and `slog.LogValuer`.

| Type | Fields | Returned by |
|------|--------|-------------|
| `PortBindError` | `Port, Adapter, Err` | `IOPort.Bind`, `ToolPort.Bind` on adapter or pipeline failure |
| `PortNoAdapterError` | `Port` | `IOPort.Connect` when no adapter was bound |
| `PortNoPipelineError` | `Port` | `ToolPort.Bind` when `SetPipeline` was not called |

---

## Fan-in, fan-out, and error handling

- **`SourcePort`** — multiple `Bind` calls merge all adapter outputs into one stream (fan-in).
- **`SinkPort`** — multiple `Bind` calls broadcast every item to all adapters (fan-out). One
  adapter failing does not stop delivery to the others.
- **`IOPort`** — exactly one adapter; a second `Bind` call returns `PortBindError`.
- **`ToolPort`** — `SetPipeline` sets the domain logic once; multiple `Bind` calls expose the
  same pipeline on multiple transports concurrently.

---

## Relationship to existing APIs

Ports do not replace the API contract builders (`api/rest`, `api/events`, `api/mcp`,
`api/reqreply`) — those still generate OpenAPI/AsyncAPI/MCP specs and provide the
typed `RouteHandle`/`ChannelHandle`/`ToolHandle` that adapters use internally. Ports
add a protocol-agnostic wiring layer *on top of* those handles, so pipeline code
never needs to import the handle's owning transport package directly.

Standalone (non-pipeline) use of adapters — `mqtt5.Subscribe`, `nethttp.Call`,
`zeromq.Serve`, etc. — remains fully supported and unaffected by `ports`.
