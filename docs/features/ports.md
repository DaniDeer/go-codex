# Protocol-Agnostic Pipeline Wiring — `ports`

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [Forge Pipelines concept](../concepts/pipelines.md) · [Wiring Guide](../guides/ports.md) · [App — Application Lifecycle](app.md)
>
> Runnable demo: [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — one coherent use case wiring MQTT, SQL, file, and HTTP adapters to `SourcePort`/`SinkPort`/`IOPort`/`ToolPort`/`PipePort`, each declared with a `Pattern` plugged in at wiring time; its MQTT ingestion/egress boundary is a plain `SourcePort`/`SinkPort` connected directly to internal `PipePort` stages via `Chain`/`ChainStream`, with the SQL persistence hop modeled as its own `Chain`/`ChainStream` edge and the pipeline shape derived via `ports.PipelineSpec`; see its README for the full data-flow diagram.

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
`ports`, the pipeline only knows about a typed port — the adapter is bound separately.
The declarative model is the same **three steps** across every port type: (1) declare
the port's structural shape, (2) plug in a [`Pattern`](#pattern--plug-in-the-wire-shape)
describing the wire shape (topic, method+path, params) — a single `PluginXxxPattern`
call registers the Pattern AND returns its typed handle, no separate
`events.NewChannel`/`Register` step — and (3) bind a concrete adapter using that handle:

```go
// domain/pipeline.go — no adapter imports; port declared with just its shape
var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{}))

// SensorReadingsPattern is a standalone, reusable value — declared once,
// independent of any specific port-construction call.
var SensorReadingsPattern = ports.EventPattern{
    Topic: "sensors/{sensorID}/data",
    Opts:  []events.ChannelOpt{events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec)},
}

func StartPipeline(ctx context.Context) {
    sensors := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, sensors, oeeCalcFn, gstream.ApplyOptions{})
    // ...
}

// main.go — the only place that knows about MQTT5; PluginEventPattern registers
// the pattern AND returns its typed handle in one call.
handle, err := domain.SensorReadings.PluginEventPattern(domain.SensorReadingsPattern)
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, handle, 0, fmt, opts))
```

---

## Seven port types

| Port | Direction | Cardinality | Use for |
|------|-----------|-------------|---------|
| [`SourcePort[T]`](#sourceportt) | External → pipeline | Fan-in (many adapters merge) | MQTT subscribe, HTTP ingest, SQL poll, file scan/watch, WS ingest |
| [`SinkPort[T]`](#sinkportt) | Pipeline → external | Fan-out (broadcast to all adapters) | MQTT publish, SSE, file write, SQL insert, cache write, WS broadcast |
| [`IOPort[Req,Resp]`](#ioportreqresp) | Pipeline ↔ external | Exactly one adapter | HTTP call, SQL per-item query, file per-item read, cache get/set, MQTT5/ZeroMQ request-reply |
| [`ToolPort[In,Out]`](#toolportinout) | External request → pipeline → response | Exactly one pipeline fn, N adapters | MCP tool, HTTP endpoint, ZeroMQ REP, MQTT5 request-reply server |
| [`LatestPort[T]`](#latestportt) | Stream → cache → external request | One cache, N serving adapters | "Current state" endpoints: latest reading over HTTP GET, ZeroMQ REP, MCP tool — no per-request pipeline run |
| [`DuplexPort[In,Out]`](#duplexportinout) | External ↔ pipeline (sessions) | Exactly one adapter | WebSocket endpoints: session-tagged inbound commands, targeted replies + broadcast |
| [`PipePort[T]`](#pipeportt) | Pipeline ↔ pipeline | Fan-in + fan-out (N input SourcePorts → M output SinkPorts) | Computation stage segmentation via `ports.Chain`/`ports.ChainStream`; multi-consumer broadcast; convenience wrapper for IO/adapter fan-in/fan-out |

---

## `SourcePort[T]`

Declares an inbound boundary. Bind one or more `SourceAdapter[T]` implementations; their
outputs are merged (fan-in) into a single stream.

```go
var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{Buffer: 8}))

var SensorReadingsPattern = ports.EventPattern{Topic: "sensors/{sensorID}/data", Opts: []events.ChannelOpt{
    events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
}}

// main.go — plug in the pattern to get the handle, bind one adapter or several for fan-in:
eventHandle, err := domain.SensorReadings.PluginEventPattern(domain.SensorReadingsPattern)
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, eventHandle, 0, fmt, opts))
domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(mux, ingestHandle, opts)) // fan-in (still handle-first — see note below)

sensors := domain.SensorReadings.Stream(ctx) // gstream.Stream[SensorReading]
```

> `ports.EventPattern` covers pub/sub (MQTT/ZeroMQ). HTTP ingest is covered by
> `ports.RESTPattern` on the `SourcePort`: `PluginRESTPattern` builds a
> `RouteHandle[T, struct{}]` (request body = payload, empty response) and
> returns it directly — pass to `nethttp.IngestAdapter` unchanged.

`Stream(ctx)` must be called after all `Bind` calls. It returns the merged stream;
adapter and codec validation errors are routed to `Stream.Errors`.

## `SinkPort[T]`

Declares an outbound boundary. Bind one or more `SinkAdapter[T]` implementations; every
item fed into the port is broadcast (fan-out) to all bound adapters. A failure in one
adapter does not stop delivery to the others.

```go
var OEEResults = codex.Must(ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{Buffer: 8}))

var OEEResultsPattern = ports.EventPattern{Topic: "alerts/{sensorID}", Opts: []events.ChannelOpt{
    events.TopicParam{Name: "sensorID"},
}}

alertHandle, err := domain.OEEResults.PluginEventPattern(domain.OEEResultsPattern)
domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, fmt, publishOpts))
domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, sseOpts)) // fan-out (still handle-first)

go domain.OEEResults.Feed(ctx, oeeStream) // blocks until oeeStream terminates
```

## `IOPort[Req,Resp]`

Declares an intermediate transform boundary — the pipeline sends a `Req` out and
receives a `Resp` back. Exactly one `IOAdapter[Req,Resp]` may be bound; swapping the
adapter (HTTP → SQL → file) never touches pipeline code.

```go
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec, ports.PortOptions{}))

var CalibrationPattern = ports.RESTPattern{Method: "GET", Path: "/calibration/{sensorID}"}

calibrated := domain.Calibration.Connect(ctx, sensors) // gstream.Stream[CalibratedReading]

// main.go — plug in the pattern to get the handle:
calibHandle, err := domain.Calibration.PluginRESTPattern(domain.CalibrationPattern)
domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, calibHandle, callOpts))
// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(calibCodec, queryFn, opts))     // no Pattern — file/sql use Params
// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine, opts))
```

Alternatively, `ports.NewRestPort` combines port construction and Plugin into one
call — pure sugar over the two-step form above:

```go
calibration, calibHandle := codex.Must2(ports.NewRestPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec, CalibrationPattern, ports.PortOptions{}))
```

`NewSourcePort`, `NewSinkPort`, `NewIOPort`, and `NewToolPort` all return
`(*Port, error)` (construction never involves a Pattern anymore — it's just the
port's structural shape). Wrap with `codex.Must(...)` for package-level
declarations, as shown throughout this page. Every `PluginXxxPattern` call
returns `(handle, error)` and can fail (unknown param name, path/topic
constraint failure, duplicate name on a shared `reqreply`/`mcp` builder, or
calling the same Plugin method twice on one port).

`Connect` returns a stream carrying `PortNoAdapterError` in `Stream.Errors` if no
adapter was bound before the pipeline started.

## `ToolPort[In,Out]`

Declares a server-side request/response boundary — the complement of `IOPort` (which is
client-side). Set the pipeline function once with `SetPipeline`, then bind it to one or
more transports. The **same pipeline logic** can serve MCP, HTTP, and ZeroMQ simultaneously.

```go
var OEETool = codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{}))

var OEERESTPattern = ports.RESTPattern{Method: "POST", Path: "/oee/calc"}
var OEEReqReplyPattern = ports.ReqReplyPattern{Topic: "oee/calc"}
var OEEMCPPattern = ports.MCPPattern{Name: "oee-calc", Opts: []apimcp.ToolOpt{
    apimcp.ToolMeta{Description: "Calculates OEE from sensor data"},
}}

func init() {
    OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
        return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
    })
}

// main.go — serve the same pipeline on three transports; each Plugin call
// registers its own Pattern and returns the typed handle:
mcpHandle, err := domain.OEETool.PluginMCPPattern(domain.OEEMCPPattern)
httpHandle, err := domain.OEETool.PluginRESTPattern(domain.OEERESTPattern)
zmqHandle, err := domain.OEETool.PluginReqReplyPattern(domain.OEEReqReplyPattern)
domain.OEETool.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, mcpHandle, mcpgo.Options{}))
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))
domain.OEETool.Bind(ctx, zeromq.ServeAdapter(repSock, zmqHandle, zeromq.ServeOptions{}))
```

Alternatively, `ports.NewRestToolPort`/`NewMCPToolPort` combine construction
and Plugin into one call for a `ToolPort` serving a single transport — pure
sugar over the two-step form above; for a `ToolPort` serving multiple
transports (as above), plug in each Pattern explicitly.

Optionally, build the OpenAPI/AsyncAPI/MCP spec FROM an already-plugged-in
binding, against a different `Builder`:

```go
restBuilder := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
ports.RegisterREST[OEEIn, OEEResult](restBuilder, domain.OEETool) //nolint:errcheck
```

`Bind` returns `PortBindError` wrapping `PortNoPipelineError` if `SetPipeline` was not
called first.

---

## `LatestPort[T]`

Declares a reactive-cache boundary: `Feed` drains a stream into an atomic
cell; bound `LatestAdapter[T]` implementations answer every request from that
cell — no per-request pipeline run, no DB query. The cache outlives the
stream: when the source terminates, adapters keep serving the last value.

```go
// domain — declared like every other boundary
var Latest = codex.Must(ports.NewLatestPort[db.Reading]("rest/latest", readingCodec,
    ports.PortOptions{}))

var LatestPattern = ports.RESTPattern{Method: "GET", Path: "/readings/latest"}

// main.go — plug in the pattern, then wire
handle, err := domain.Latest.PluginRESTPattern(domain.LatestPattern)
must(domain.Latest.Bind(ctx, nethttp.LatestAdapter(mux, handle, nethttp.Options{})))
go domain.Latest.Feed(ctx, readings)

// programmatic read side
v, ok := domain.Latest.Latest()
```

Patterns use the request codec `codex.Struct[struct{}]()` automatically —
requests carry no payload; the response is always the cached value.
`RESTPattern` (HTTP GET), `ReqReplyPattern` (ZeroMQ REP), and `MCPPattern`
(MCP tool) are all supported via `PluginRESTPattern`/`PluginReqReplyPattern`/
`PluginMCPPattern`.

Empty-cache behavior is per-transport: HTTP responds 503 +
`NoLatestValueError`, ZeroMQ sends an error reply, MCP returns an error
result. Serving adapters: `nethttp.LatestAdapter`, `zeromq.LatestAdapter`,
`mcpgo.LatestAdapter`. (The stream-owning `nethttp.HandlerLatest`/
`zeromq.ServeLatest` functions remain for non-port use; the former
`mcpgo.ToolLatestAdapter` has been removed in favor of this port.)

---

## `DuplexPort[In,Out]`

Declares a bidirectional session boundary: external peers send `In` frames
and receive `Out` frames over persistent, identified sessions. Every frame
is a `Framed[T]{Session, Payload}` — inbound frames carry the sender's
session; outbound frames target one session, or broadcast when `Session` is
zero. Exactly one adapter (like `IOPort`).

```go
// domain — declared like every other boundary
var Live = codex.Must(ports.NewDuplexPort[Command, Update]("live",
    commandCodec, updateCodec, ports.PortOptions{Buffer: 8}))

var LivePattern = ports.SocketPattern{Path: "/live/{room}"}

// main.go — plug in the pattern, then wire
hub := websocket.NewHub(0)
handle, err := domain.Live.PluginSocketPattern(domain.LivePattern)
must0(domain.Live.Bind(ctx, websocket.DuplexSocketAdapter(mux, hub, upgrader, handle, opts)))

// pipeline: session-preserving Map yields targeted replies
replies := stream.Map(ctx, domain.Live.Inbound(ctx),
    func(f ports.Framed[Command]) (ports.Framed[Update], error) {
        return ports.Framed[Update]{Session: f.Session, Payload: process(f.Payload)}, nil
    }, stream.MapOptions{Name: "ack"})
go domain.Live.Feed(ctx, replies)
```

Session routing composes with the stream operators — `stream.GroupBy` by
`Framed.Session` yields per-client sub-streams. Only [`SocketPattern`](#socketpattern--path-addressed-duplex-sockets)
is accepted; any other pattern kind fails construction. See the
[WebSocket adapter](websocket.md) for the transport side.

For one-struct convenience on long-lived connections, add
`SocketPattern.InOpts`/`OutOpts` with
`ports.NewRequiredSocketInParam`/`NewOptionalSocketInParam` and
`ports.NewRequiredSocketOutParam`/`NewOptionalSocketOutParam`. WebSocket
adapters merge connection vars into each frame automatically.

---

## `PipePort[T]`

A named pipeline stage boundary for **computation segmentation only** — a
thin wrapper over `gstream`, declared flexibly at setup time and never
mutated at runtime. `ports.ChainStream`/`ports.Chain` connect stages; side
observers tap into any stage without changing the pipeline logic.

```go
// Declare PipePorts as internal, computation-only stage boundaries.
var Raw   = codex.Must(ports.NewPipePort[SensorReading]("raw", readingCodec, ports.PortOptions{}))
var Clean = codex.Must(ports.NewPipePort[ValidatedReading]("clean", validCodec, ports.PortOptions{}))

// Chain wires Raw → validate → Clean in one call (Stream+Map+Feed).
ports.Chain(ctx, Raw, validate, Clean)

// Side observers: tap into any stage
Raw.OutputPort("log").Bind(ctx, ports.ChanSinkAdapter(logCh))

Raw.Connect(ctx)
Clean.Connect(ctx)
```

**`Chain`/`ChainStream` are generalized to also accept boundary ports at
either end** — `from` can be a `*PipePort[In]` OR a `*SourcePort[In]`;
`to` can be a `*PipePort[Out]` OR a `*SinkPort[Out]`. This makes the data
flow directly visible from the declaration, top to bottom, using the exact
same call shape whether the endpoint is an internal stage or a real IO
boundary:

```go
// SourcePort -> Chain -> PipePort -> ChainStream -> SinkPort, one
// declaration, no separate wrapper functions or IO-bridging sub-ports.
var Sensors = codex.Must(ports.NewSourcePort[MQTTPayload]("sensors", payloadCodec, ports.PortOptions{}))
var Params  = codex.Must(ports.NewPipePort[InsertParams]("params", paramsCodec, ports.PortOptions{}))
var Alerts  = codex.Must(ports.NewSinkPort[SensorAlert]("alerts", alertCodec, ports.PortOptions{}))

ports.Chain(ctx, Sensors, buildInsertParams, Params)
ports.ChainStream(ctx, Params, func(s gstream.Stream[InsertParams]) gstream.Stream[SensorAlert] {
    saved := persist.Connect(ctx, s)
    above := gstream.Filter(ctx, saved, shouldAlert)
    return gstream.Map(ctx, above, buildAlert, gstream.MapOptions{})
}, Alerts)
```

`fn`/`transform` need not be wrapped in `forge.Function` — pass a plain Go
function directly unless the step genuinely needs `forge`'s contract-hash/
signing governance (most internal glue-mapping steps don't).

**`ports.ChainStream[In, Out](ctx, from, transform, to)` is the general
stage connector; `ports.Chain[In, Out](ctx, from, fn, to)` is its single-Map
special case** — not a separate mechanism. `ChainStream` accepts ANY
`func(gstream.Stream[In]) gstream.Stream[Out]`, so a multi-step
sub-pipeline (several `Map`/`Filter`/etc. calls) connects two stages
with the SAME `(ctx, from, to)` call shape as a single-step transition.
`Chain` is defined literally in terms of `ChainStream`:

```go
func Chain[In, Out any](ctx context.Context, from *PipePort[In], fn func(In) (Out, error), to *PipePort[Out]) {
    ChainStream(ctx, from, func(s gstream.Stream[In]) gstream.Stream[Out] {
        return gstream.Map(ctx, s, fn, gstream.MapOptions{})
    }, to)
}
```

**Multi-step sub-pipelines use `ChainStream` directly** — no hand-written
wrapper function needed:

```go
// Valid → Calibrated needs three sequential steps: calibrate, classify,
// annotate. ChainStream's transform can chain as many Map/Filter calls as
// the stage needs — the same primitive as the single-step Chain above.
ports.ChainStream(ctx, Valid, func(s gstream.Stream[ValidatedReading]) gstream.Stream[CalibratedReading] {
    s = gstream.Map(ctx, s, calibrateReading, gstream.MapOptions{})
    s = gstream.Map(ctx, s, classifyReading, gstream.MapOptions{})
    return gstream.Map(ctx, s, annotateReading, gstream.MapOptions{})
}, Calibrated)
```

Use `Stream()` directly only when you need something `ChainStream` doesn't
cover (e.g. fanning the same stream into multiple downstream pipes with
custom logic in between).

**Modular pipeline composition**: `Chain` and `ChainStream` calls compose
identically inside a top-level pipeline builder — a multi-step transition
is exactly as first-class a call as a single-step one:

```go
func BuildPipeline(ctx context.Context) PipelineIO {
    ports.Chain(ctx, Raw, validateReading, Valid)
    ports.ChainStream(ctx, Valid, calibrationTransform, Calibrated)
    // ... observers, adapter binding, Connect calls, return PipelineIO ...
}
```

This mirrors
[`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service)'s
own `pipeline.Build(ctx, ...)` convention: small, ctx-scoped wiring calls
assembled by one top-level builder, never one monolithic wiring block.
`PipePort`/codec/type declarations stay `var`s (no side effects);
`BuildPipeline` stays a function (it starts goroutines and needs `ctx` —
see the ordering rule below).

**One ordering rule**: register all `InputPort`/`OutputPort`/`Stream`/
`Chain`/`ChainStream` calls for a pipe before that pipe's `Connect()`.
`Push(ctx, v)` has no such restriction — it works at any time, before or
after `Connect()`; items simply buffer until `Connect()`'s consumer
goroutine starts draining them. `Chain`/`ChainStream` only need to precede
the **upstream** pipe's `Connect` — the downstream pipe's `Connect` may
happen before or after.

**Side-observer taps only** (not IO boundaries): `InputPort(name)` returns
a `*SourcePort[T]` and `OutputPort(name)` returns a `*SinkPort[T]` — same
name → same instance, input/output names scoped independently, built with
`PortOptions{Buffer, Params, Observer}` only. Use these for taps like
tee'ing a stage's output to a logger; a **real IO boundary** (MQTT, HTTP,
SQL, file, …) should be a genuine `SourcePort`/`SinkPort`/`IOPort`/`ToolPort`
declared and connected directly via `Chain`/`ChainStream`, as shown above —
PipePort itself carries no `Pattern` of its own.

**Observer + tracing on the `Connect` data path**: the Push-consumer
goroutine calls `RecordSubscribe`; `fanOut` calls `RecordPublish` per
destination (success and failure). `Chain`/`ChainStream` wrap their
edge-setup (not per-item) in a `"pipe.chain"` `TraceObserver` span —
matching the cost/benefit precedent of `port.bind`'s adapter-lifetime span.

**Lifecycle supervision**: `Done() <-chan struct{}` closes only after
`Connect`'s internal goroutines fully exit — a real teardown-complete
signal (never closes if `Connect` was never called). Pairs with
[`app.App.Supervise`](app.md) for supervising a fire-and-forget `PipePort`
without racing `ctx.Done()` against actual completion:

```go
app.Supervise("raw-pipe", func(ctx context.Context) <-chan struct{} {
    Raw.Connect(ctx)
    return Raw.Done()
})
```

**Pipeline spec generation — derived, not hand-typed**:
`ports.PipelineSpec(title, version string, pipes ...PipeSpecSource) gstream.TopologySpec`
builds a documentation spec by *reading* the actual wiring, not by
re-describing it in parallel strings:

```go
spec := ports.PipelineSpec("Sensor Pipeline", "1.0.0", Raw, Valid, Calibrated)
yamlBytes, err := streamrender.Render(spec)
```

Derived automatically: pipe names, `Buffer()`, every bound adapter's real
`AdapterName()` (via `SourcePort.BoundAdapters()`/`SinkPort.BoundAdapters()`),
and every `Chain`/`ChainStream` edge — including the transform's real Go
function identity, captured via reflection (`"main.validateReading"` for a
named function; an honestly closure-opaque `"main.BuildPipeline.func1"` for
an inline `ChainStream` transform — never fabricated). Only `title`,
`version`, and the pipes' *ordering* are caller-supplied.

`PipeSpecSource` is a minimal interface (`Name() string`), satisfied by
`*PipePort[T]` for any `T` AND by boundary ports (`*SourcePort[T]`,
`*SinkPort[T]`) — one `PipelineSpec` call accepts a heterogeneous mix of
internal PipePort stages and real IO boundary ports in ordering position,
each contributing whatever richer detail (buffer size, bound adapters,
chain edges) it implements, via optional type-asserted extensions:

```go
ports.PipelineSpec("Sensor Service MQTT Pipeline", "1.0.0",
    ioports.Sensors, pipeline.Params, pipeline.Saved, ioports.Alerts)
// Sensors/Alerts are plain SourcePort/SinkPort; Params/Saved are PipePorts —
// all four appear as real, named nodes in the derived spec.
```

> See [`examples/pipeline-segmentation`](https://github.com/DaniDeer/go-codex/tree/main/examples/pipeline-segmentation)
> for a 3-stage computation pipeline with side observers and derived spec generation.

---

## `SinkPort` Push — request-scoped submission

`Feed` is stream-driven and one-shot. When a request/response pipeline needs
to drop individual items into a sink (e.g. a REST-triggered export writing a
file), use the port-owned lifecycle instead of hand-rolling a channel + Feed
goroutine:

```go
exports.Bind(appCtx, file.DrainWriteFileAdapter(exportFile, varsFor, opts))
exports.Start(appCtx)                    // port-owned channel + drain goroutine
…
_ = exports.Push(ctx, snapshot)          // from anywhere; blocks with backpressure
…
must(exports.Close(), "close exports")   // waits for in-flight Push + adapter drain
```

`Push` returns `PortNotStartedError` before `Start`, after `Close`, or on a
`Feed`-driven port (the two lifecycles are mutually exclusive); it returns
`ctx.Err()` when cancelled while blocked. `Start` on an already-owned port is
a no-op; `Close` is idempotent.

---

## `Pattern` — plug in the wire shape

`ports.Pattern` is the **primary** way to declare a port's communication pattern —
method+path, topic, or MCP tool name, plus routing params — as a standalone,
reusable value, plugged into a port at wiring time, reusing the exact same
option vocabulary as `rest.NewRoute`/`events.NewChannel`/`reqreply.NewRoute`/
`apimcp.NewTool` (`PathParam`, `QueryParam`, `TopicParam`, …). No new param
types, no separate `events.NewChannel(...).Register(builder)` call written
by hand: the port's `PluginXxxPattern` method makes that call **internally**.

| Pattern | Protocol family | Wraps |
|---------|------------------|-------|
| `RESTPattern{Method, Path, Opts}` | HTTP (nethttp, chi) | `rest.RouteOpt` |
| `EventPattern{Topic, Opts}` | pub/sub (mqtt, mqtt5, zeromq) | `events.ChannelOpt` |
| `ReqReplyPattern{Topic, Opts}` | request/reply (mqtt5, zeromq) | `reqreply.RouteOpt` |
| `MCPPattern{Name, Opts}` | MCP tool (mcpgo) | `apimcp.ToolOpt` |
| `FilePattern{Path, Format, CustomFormat, Opts}` | typed files (file) | `ports.FileOpt` |
| `SQLPattern{Table, Op}` | SQL (sql) | — (metadata-only) |
| `CachePattern{Key, TTL, Format, CustomFormat, Opts}` | key/value cache (redis) | `ports.CacheOpt` (`CacheKeyParam` — per-key-var codecs) |
| `SocketPattern{Path, Subprotocols, Format, CustomFormat, Opts, InOpts, OutOpts}` | duplex socket (websocket) | `rest.RouteOpt` (upgrade-time) + `SocketInOpt`/`SocketOutOpt` (connection-var merge into inbound/outbound payload structs) |

A port plugs in one `Pattern` **per protocol family** it will be bound to — a
`ToolPort` exposed over HTTP + MQTT 5 + MCP simultaneously (as in the `OEETool`
example above) plugs in three. Each `PluginXxxPattern` method registers the
Pattern AND returns its typed handle in one call — no separate lookup step:

| Method | Returns |
|--------|---------|
| `port.PluginRESTPattern(pattern)` | `(*rest.RouteHandle[Req,Resp], error)` |
| `port.PluginEventPattern(pattern)` | `(*events.ChannelHandle[T], error)` |
| `port.PluginReqReplyPattern(pattern)` | `(*reqreply.RouteHandle[Req,Resp], error)` |
| `port.PluginMCPPattern(pattern)` | `(*apimcp.ToolHandle[In,Out], error)` |
| `port.PluginFilePattern(pattern)` | `(ports.File[T], error)` |
| `port.PluginSQLPattern(pattern)` | `(ports.SQLPattern, error)` (metadata only, propagated via ctx) |
| `port.PluginCachePattern(pattern)` | `(ports.Cache[T], error)` |
| `port.PluginSocketPattern(pattern)` | `(ports.Socket[In,Out], error)` |

Calling the same `PluginXxxPattern` method twice on one port returns
`PatternRegisterError` (duplicate-registration detection) — not a silent
overwrite.

### One construction path — `Register`, always

Internally, a `Pattern` is turned into a handle via **exactly the same**
`Route`/`Channel`/`Tool.Register(builder)` call a hand-declared route makes —
`ports` never calls the weaker, builder-free `ClientHandle()`. This makes a
`Pattern`-derived handle **indistinguishable** from one built by calling
`Register` directly: `port.PluginEventPattern(pattern)` and
`events.NewChannel[T](topic, codec, opts...).Register(myBuilder)` produce the
same kind of `*events.ChannelHandle[T]`, and any adapter (`mqtt5.SubscribeAdapter`, etc.)
that receives either cannot tell which one it got.

Supply your own `*Builder` via `PortOptions` to get full parity with a
hand-registered route — security schemes, global security, and whole-path/topic
format constraints all become available, and the port's route/channel/tool
accumulates directly into *your* spec document:

```go
restBuilder := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
restBuilder.AddSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})
restBuilder.AddGlobalSecurity(route.SecurityRequirement{"bearerAuth": {}})

oeeTool := codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{RESTBuilder: restBuilder}))

_, err := oeeTool.PluginRESTPattern(ports.RESTPattern{
    Method: "POST",
    Path:   "/oee/calc",
})
if err != nil {
    panic(err)
}

// spec already contains /oee/calc — no separate registration needed:
spec, _ := restBuilder.OpenAPISpec()
```

When no `Builder` is supplied (the common case), `ports` registers against a
private, single-use `Builder` with zero `Info` for that one call — the same
zero-ceremony default as before, through the **identical** `Register` code path
(there's no separate "simple" construction — just an auto-created `Builder`
instead of a shared one).

| `PortOptions` field | Applies to | Gives you |
|---|---|---|
| `RESTBuilder *rest.Builder` | `RESTPattern` | Security schemes, global security, `rest.WithPathConstraints`, shared OpenAPI spec |
| `EventBuilder *events.Builder` | `EventPattern` | Security schemes, global security, `events.WithTopicConstraints`, shared AsyncAPI spec |
| `ReqReplyBuilder *reqreply.Builder` | `ReqReplyPattern` | Duplicate-topic detection, shared registration |
| `MCPBuilder *apimcp.Builder` | `MCPPattern` | Duplicate-name detection, shared MCP spec |

> **Correctness note:** before this, every `Pattern`-derived handle had an empty
> `SecuritySchemes` map and `nil` `GlobalSecurity` — any `RouteMeta.Security`/
> `Subscribe.Security`/`Publish.Security` requirement on a `Pattern`-based port was
> silently unenforced (the credential check simply skips unknown scheme names).
> Supplying a `Builder` with `AddSecurityScheme`/`AddGlobalSecurity` fixes this.

**If you already supplied a `Builder`**, the port's route/channel/tool is already
registered with it — calling `RegisterREST`/`RegisterEvent`/`RegisterReqReply`/
`RegisterMCP` with that *same* builder afterward is redundant (harmless for
`rest`/`events`, which don't detect duplicates; returns a duplicate error for
`reqreply`/`mcp`, which do). Use `Register*` only when you did **not** supply a
`Builder` up front and want to add the already-bound port to a *different* spec
document afterward:

```go
b := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
if err := ports.RegisterREST[OEEIn, OEEResult](b, domain.OEETool); err != nil {
    // MissingPatternError if the port never plugged in a RESTPattern
}
spec, _ := b.OpenAPISpec()
```

> **Shape note:** `RESTPattern` is role-aware on single-codec ports:
> `SourcePort[T]` builds the HTTP-ingest shape `RouteHandle[T, struct{}]`
> via `PluginRESTPattern` (request body = payload, empty response);
> `SinkPort[T]` builds the SSE shape `SSERouteHandle[struct{}, T]` (always
> GET — any other `Method` fails Plugin, replay with `RegisterSSE`).
> The outbound-client sink (`nethttp.DrainCallAdapter`) needs an independent
> response codec the single-codec port can't supply and stays handle-first.
> `NewIOPort`/`NewToolPort`/`NewSourcePort`/`NewSinkPort`
> all now return `(*Port, error)` — `Register` is fallible (unknown param names,
> path/topic constraint failures, duplicate names on `reqreply`/`mcp`) in ways the
> old builder-free construction wasn't.

**Binary/custom formats, declared inline:** `rest.RequestFormats(...)` and
`rest.Formats(...)` are `RouteOpt`s — the same interface `PathParam`/`RouteMeta`
implement — so they slot directly into `RESTPattern.Opts` with zero changes to
the `ports` package:

```go
ports.RESTPattern{
    Method: "GET", Path: "/images/{id}",
    Opts: []rest.RouteOpt{
        rest.Formats(format.Binary(pngCodec).WithContentType("image/png")),
    },
}
```

This is the one-step equivalent of retrieving the handle and calling
`handle.WithFormats(...)` afterward — both work, `Opts` is just declarable
alongside the rest of the pattern. A type mismatch (formats for a type that
doesn't match the port's codec) returns `rest.FormatOptError` from
`Route.Register` (surfacing as `PatternRegisterError` from the port
constructor). `events.Formats`/`SubscribeFormats`/`PublishFormats` and
`reqreply.RequestFormats`/`Formats` are the `EventPattern`/`ReqReplyPattern`
equivalents — see [`docs/guides/mqtt.md`](../guides/mqtt.md) for a worked
MQTT example.

### `FilePattern` — typed files as sink or intermediate IO

`FilePattern` gives the file adapter the same declare-once story: the path
template, wire format, and path-param codecs live on the port; the built
`ports.File` handle comes back out via `ports.FileHandle`. `Format` is a
`FileFormatKind` enum — `FileFormatJSON` (default), `FileFormatYAML`, or
`FileFormatTOML` — applied to the port's own codec.

For binary or custom formats the enum can't express (Gob, raw `[]byte`
blobs like PNG/PDF, protobuf, msgpack, anything built with `format.NewTyped`/
`format.NewStreamed`), set **`CustomFormat`** instead — a pre-built
`format.Format[T]` value (matching the port's payload/response type),
stored type-erased and resolved generically at build time. `CustomFormat`
overrides `Format` entirely when non-nil:

```go
ports.FilePattern{
    Path:         "images/{id}.png",
    CustomFormat: format.Binary(pngCodec).WithContentType("image/png"),
}
ports.FilePattern{
    Path:         "cache/{id}.bin",
    CustomFormat: format.Gob(myStructCodec), // no map[string]any intermediate
}
```

A type mismatch (`CustomFormat` holding the wrong `format.Format[T]`) returns
`PatternRegisterError` at construction — the same error `SocketPattern`'s
port-role rejection already uses. `CachePattern` and `SocketPattern` carry
the identical `CustomFormat` field with the same precedence rule; see
[`examples/pattern-custom-format`](https://github.com/DaniDeer/go-codex/tree/main/examples/pattern-custom-format).

- On a **`SinkPort[T]`** the handle is a `ports.File[T]` of the payload type —
  pairs with `file.DrainWriteFileAdapter`.
- On an **`IOPort[Req,Resp]`** the handle is a `ports.File[Resp]` of the
  **response** type (the file's content *is* the port's response) — pairs with
  `file.ReadAdapter`, the 2-type per-item read. (The 3-type
  `file.ReadEachAdapter`, with its independent file-content type and `combine`
  func, stays handle-first for enrichment cases.)

For **partial updates** (patch semantics) instead of a whole-file overwrite,
pair a hand-built `ports.File[T]` with `file.DrainPatchAdapter` (untyped
`map[string]any` patch, JSON Merge Patch semantics via `ports.File.Patch`)
or `file.DrainPatchEncodedAdapter` (typed partial update via
`ports.PatchEncoded`, so patch-codec-only fields still persist). Both stay
handle-first — the patch item's type is deliberately different from the
port's own payload type, the same reason `file.ReadEachAdapter` stays
handle-first for its independent content type. Both require a map-based
format (JSON/YAML/TOML/`format.New`); `ports.FilePatchNotSupportedError` is
passed through to `OnError` unchanged for Gob/Binary/`NewTyped`/`NewStreamed`.

```go
// domain — per-item calibration lookup as intermediate IO, zero adapter imports
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibrationData](
    "calibration", readingCodec, calibrationCodec, ports.PortOptions{}))

var CalibrationFilePattern = ports.FilePattern{
    Path: "data/{sensorID}/calibration.json", // JSON is the default Format
    Opts: []ports.FileOpt{ports.FilePathParam{Name: "sensorID"}.WithCodec(uuidCodec)},
}

// main.go — plug in the pattern to get the handle; swap file → SQL → HTTP freely
calibFile, err := domain.Calibration.PluginFilePattern(domain.CalibrationFilePattern)
domain.Calibration.Bind(ctx, file.ReadAdapter(calibFile,
    func(r SensorReading) map[string]string { return map[string]string{"sensorID": r.SensorID} },
    file.ReadEachAdapterOptions{}))
```

There is no `RegisterFile` — files have no spec document concept
(`File.PathParamSchemas()` already serves doc tooling). A `FilePattern` CAN
cause `PluginFilePattern` to error — a `CustomFormat` type mismatch returns
`PatternRegisterError` (the enum-only path — no `CustomFormat` — always
succeeds).

### `SQLPattern` — metadata-only, by design

SQL has no path/topic template: query text and bind-parameter syntax are
driver-specific and stay in your typed `queryFn`/`insertFn` closures. The
declarative surface is therefore just **`Table` and `Op`**, declared once on
the port instead of repeated in every adapter options struct. They feed error
context (`QueryStreamError`/`InsertStreamError`/`SQLValidationError`) and
observer location strings.

Propagation mirrors `Params`: every `Bind` (and `IOPort.Connect`) wraps the
adapter context via `ports.WithSQLMeta`; `sql.QueryAdapter`,
`sql.QueryEachAdapter`, and `sql.DrainInsertAdapter` default their options'
`Table`/`Op` from `ports.SQLMetaFromContext` when the explicit fields are
empty — explicit values always win.

```go
var Readings = codex.Must(ports.NewSinkPort[db.Reading]("sql/readings", readingCodec, ports.PortOptions{}))

var ReadingsSQLPattern = ports.SQLPattern{Table: "readings", Op: "insert_reading"}

// main.go — plug in the pattern first, Table/Op no longer repeated in the options struct
_, err := domain.Readings.PluginSQLPattern(domain.ReadingsSQLPattern)
domain.Readings.Bind(ctx, sql.DrainInsertAdapter(readingCodec, insertFn, sql.DrainInsertOptions{}))
```

---

## Extracting information from a discovered path

`File.BuildPath` only goes forward (known vars → concrete path). `File.MatchPath`
is the missing inverse — given an already-discovered path (e.g. from your own
`filepath.WalkDir`/`filepath.Glob`), it matches the path against the template
and returns the extracted variable values, validated against each registered
`FilePathParam.Codec` — mirroring `mqtt.TopicVarsFromMessage`'s existing
pattern for MQTT topics. A `{varName}` placeholder may share a segment with
literal text (e.g. `{date}.json` correctly extracts `2024-01-15`, not
`2024-01-15.json`):

```go
vars, err := readingFile.MatchPath("readings/sensor-42/2024-01-15.json")
// vars == map[string]string{"sensorID": "sensor-42", "date": "2024-01-15"}
```

`ports.NewFilePathParam[T]` declares BOTH the `FilePathParam` (spec/
validation, unchanged) AND a merge field in one call. `File.ReadMerged`
and `ports.WriteHandle` are the single-call convenience built on top —
mirroring `events.ChannelHandle.DecodeMerged`/`mqtt5.PublishHandle` for
the file boundary:

```go
var readingFile = ports.NewFile("readings/{sensorID}/{date}.json", format.JSON(valueCodec),
    ports.NewFilePathParam("sensorID", codex.String().Refine(validate.NonEmptyString),
        func(r ReadingMeta) string { return r.SensorID },
        func(r *ReadingMeta, v string) { r.SensorID = v }),
    ports.NewFilePathParam("date", codex.String().Refine(validate.Date),
        func(r ReadingMeta) string { return r.Date },
        func(r *ReadingMeta, v string) { r.Date = v }),
)

// Write: derive the path from meta's own fields — no manual vars map.
err := ports.WriteHandle(readingFile, meta, ports.FileOptions{})

// Read: given a discovered path's extracted vars, ReadMerged decodes the
// body AND merges the SAME vars into the returned struct in one call.
vars, err := readingFile.MatchPath(discoveredPath)
meta, err := readingFile.ReadMerged(vars, ports.FileOptions{})
// meta.SensorID/meta.Date are populated from the path; meta.Value from the body.
```

`File.Read`/`File.Write` remain available as the lower-level escape hatch
for callers that build the vars map themselves (e.g. no merge-capable
path params declared, or vars come from a non-struct source).
`adapters/file`'s `ReadEachAdapter`/`ReadAdapter` call `ReadMerged`
internally (merging vars already known from their `varsFor(In)` closure);
`DrainWriteFileAdapter`'s `varsFor` may be left `nil` when the file
declares merge fields, deriving vars per-item automatically via
`WriteHandle`.

`NewFilePathParam` is the PRIMARY, recommended way to declare a path
variable — but not the sole way: the plain `FilePathParam{...}.WithCodec(...)`
struct literal remains available as the low-level escape hatch for
validate-only variables with no merge need. See
[Concepts: Codec — Reusing Field declarations](../concepts/codec.md#reusing-field-declarations-for-pathtopicheaderquery-vars)
for the shared mechanism (`codex.DecodeVars`/`EncodeVars`) this builds on.

---

## Cache key vars with automatic merge — `NewCacheKeyParam`

`ports.NewCacheKeyParam[T]` mirrors `ports.NewFilePathParam[T]` exactly —
it declares BOTH the `CacheKeyParam` (spec/validation, unchanged) AND a
merge field in one call. `redis.GetMerged` and `redis.SetHandle` are the
single-call convenience built on top, mirroring `File.ReadMerged`/
`ports.WriteHandle` for the cache boundary:

```go
var userCache = ports.NewCache("user:{id}", format.JSON(userCodec),
    ports.NewCacheKeyParam("id", codex.String().Refine(validate.UUID),
        func(u User) string { return u.ID },
        func(u *User, v string) { u.ID = v }),
)

// Set: derive the key from user's own ID field — no manual vars map.
err := redis.SetHandle(ctx, client, userCache, user, redis.SetOptions{})

// Get: GetMerged looks up like Get, then merges the SAME key vars into
// the returned value — the id used to look it up is populated for free.
user, ok, err := redis.GetMerged(ctx, client, userCache, map[string]string{"id": userID}, redis.GetOptions{})
```

`redis.Get`/`redis.Set` remain available as the lower-level escape hatch
for callers that build the vars map themselves. `adapters/redis`'s
`GetAdapter` calls `GetMerged` internally; `SetAdapter`/`DrainSetAdapter`'s
`keyFn` may be left `nil` when the cache declares merge fields, deriving
key vars per-item automatically via `SetHandle`.

---

## `IOParam` — protocol-agnostic parameters (handle-less adapters)

`PortOptions.Params` is the enforcement mechanism for adapters with **no** protocol-level
builder of their own — `file.ReadEachAdapter`, `file.DrainWriteFileAdapter`,
`file.DrainPatchAdapter`, `file.DrainPatchEncodedAdapter` (their
`varsFor` function extracts a `map[string]string`). The port propagates `Params` via
context (`ports.WithParams`), and the adapter calls
`ports.ValidateParams(ports.ParamsFromContext(ctx), vars)` before using the extracted
values. A validation failure surfaces as `ReadError`/`WriteError` wrapping
`codex.ValidationErrors`.

For handle-backed adapters (REST/events/reqreply/MCP), use `Pattern` instead — `Params`
is not consulted there since the derived handle already validates fully.

```go
ports.IOParam{Name: "sensorID", Description: "Sensor identifier", Required: true}.WithCodec(sensorIDCodec)
```

`PortOptions{Params, Buffer, Observer, RESTBuilder, EventBuilder, ReqReplyBuilder, MCPBuilder}`
configures every port constructor (there is no `Patterns` field — Patterns
are plugged in after construction via `PluginXxxPattern`). `Buffer` only
applies to `SourcePort`/`SinkPort` (`IOPort`/`ToolPort` have no internal
channel to buffer). `Observer` receives a `"port.bind"` `RecordRequest`
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

| Transport | Source | Sink | IO | Tool | Latest |
|-----------|--------|------|-----|------|--------|
| MQTT5 | `SubscribeAdapter` | `PublishAdapter` | `CallAdapter` | `ServeAdapter` | — |
| MQTT | `SubscribeAdapter` | `PublishAdapter` | — | — | — |
| HTTP (nethttp) | `IngestAdapter`, `PollAdapter` | `SSEAdapter`, `DrainCallAdapter` | `CallAdapter` | `PipelineAdapter` | `LatestAdapter` |
| HTTP (chi) | `IngestAdapter` | `SSEAdapter` | — | `PipelineAdapter` | `LatestAdapter` |
| ZeroMQ | `SubscribeAdapter` | `PublishAdapter` | `CallAdapter` | `ServeAdapter` | `LatestAdapter` |
| File | `ScanAdapter`, `WatchAdapter` | `DrainWriteAdapter`, `DrainWriteFileAdapter`, `DrainPatchAdapter`, `DrainPatchEncodedAdapter` | `ReadAdapter`, `ReadEachAdapter` | — | — |
| SQL | `QueryAdapter` | `DrainInsertAdapter` | `QueryEachAdapter` | — | — |
| MCP (mcpgo) | — | — | — | `ToolPipelineAdapter` | `LatestAdapter` |

> **Merge-field per-item vars derivation:** for every `Sink`/`IO` adapter
> above whose options carry a `Vars map[string]string` field
> (`nethttp.DrainCallAdapter`/`CallAdapter`, `mqtt5.PublishAdapter`/`CallAdapter`,
> `zeromq.PublishAdapter`/`CallAdapter`, `mqtt.PublishAdapter`), leaving `Vars`
> `nil` derives path/topic vars PER-ITEM from that item's own merge-field-declared
> struct fields (via the transport's `CallHandle`/`PublishHandle`) — the "one
> struct, one call" convenience applies through `ports.SinkPort`/`ports.IOPort`
> just as it does calling the transport function directly. Set `Vars` to a
> non-nil map (even an empty one) to keep the same, static vars for every
> item instead — the escape hatch, unchanged from before this was added.

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
`api/reqreply`) — those still generate OpenAPI/AsyncAPI/MCP specs and produce the typed
`RouteHandle`/`ChannelHandle`/`ToolHandle` that adapters use internally. With `Pattern`,
ports **build those handles internally** (builder-free, via each type's `ClientHandle`
method) from a declaration made once on the port — pipeline code never needs to import
the handle's owning transport package directly, and there is no second, separate
`NewRoute`/`NewChannel`/`Register` step. When you also want an OpenAPI/AsyncAPI/MCP
spec document, `RegisterREST`/`RegisterEvent`/`RegisterReqReply`/`RegisterMCP`/
`RegisterSocket` replay the same declared `Pattern` against a real `Builder` —
building the spec **from** the binding rather than the other way around
(`RegisterSocket` renders a `SocketPattern` as an AsyncAPI channel; see the
[WebSocket adapter](websocket.md)).

Declaring a route/channel/tool directly via the builders (`rest.NewRoute(...).Register(b)`,
etc.) and passing the resulting handle straight into an adapter constructor remains fully
supported — useful when sharing one handle across a port-based binding and a separate,
standalone adapter call, or when the port itself doesn't need a `Pattern` (handle-less
adapters like `file`/`sql` use `Params` instead; see below).

Standalone (non-pipeline) use of adapters — `mqtt5.Subscribe`, `nethttp.Call`,
`zeromq.Serve`, etc. — remains fully supported and unaffected by `ports`.

## Design pattern: declarative descriptor + plain function

Every building block in go-codex that supports standalone (non-pipeline)
use follows the same two-part shape, for the same three reasons:

1. **Reuse** — declare the shape/address/format once, call it from many
   places, instead of repeating the same parameters at every call site.
2. **Consistency** — the exact same declared value works whether you're
   building a full pipeline or writing a handler with no `ports`/`stream`
   involvement at all; switching between the two doesn't change your
   declaration.
3. **Key-var codec validation** — when the descriptor addresses something
   via a `{var}` template (a path, a topic, a cache key), each variable can
   carry its own `codex.Codec[string]` that validates the substituted value
   before use — not just checks that it's present.

| Package | Declarative descriptor | Plain-function implementation | Key-var codec validation |
|---|---|---|---|
| `ports` + `adapters/file` (file) | `ports.NewFile(path, fmt, opts...)` | `File.Read`/`.Write`/`.Update`/`.Patch` | `ports.FilePathParam.WithCodec` |
| `ports` + `adapters/redis` (cache) | `ports.NewCache(key, fmt, opts...)` | `redis.Get`/`redis.Set`/`redis.Seed` | `ports.CacheKeyParam.WithCodec` |
| `api/rest` | `route.ClientHandle()` | `nethttp.Call` | `rest.PathParam.WithCodec` |
| `api/events` | `channel.ClientHandle()` | `mqtt.Publish`/`Subscribe` | `events.TopicParam.WithCodec` |
| `adapters/sql` | `codex.Codec[T]` (the codec itself — no wrapper needed) | `sql.Validate`, or declared once via `sql.DecorateInput`/`DecorateOutput` | **N/A — no templated key exists** (see below) |

`ports.File[T]` and `ports.Cache[T]` both "have a" `format.Format[T]` field
but ARE themselves protocol-agnostic addressing descriptors — bound to a
port via `FilePattern`/`CachePattern`, used by exactly one adapter family
(`adapters/file`/`adapters/redis`) — which is why both live in `ports`
rather than `format` (only `format.Format[T]` itself, plus `JSON`/`YAML`/
`TOML`/`Gob`/`Binary`/`New`/`NewTyped`/`NewStreamed`, stay in `format`).

**Neither `ports.NewFile` nor `ports.NewCache` requires a `ports.NewIOPort`/
`SinkPort`/`Bind` anywhere in the call path** — both are protocol-agnostic
descriptor VALUES, not ports themselves; you import `ports` for the type,
but never declare an actual port or touch the `stream` package to use them:

```go
// File — no ports.NewIOPort, no Bind, no stream.Stream anywhere.
cfgFile := ports.NewFile("config.json", format.JSON(configCodec))
cfg, err := cfgFile.Read(nil, ports.FileOptions{})

// Cache — no ports.NewIOPort anywhere, even though Cache[T] lives in `ports`.
userCache := ports.NewCache("user:{id}", format.JSON(userCodec),
    ports.CacheKeyParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)))
u, ok, err := redis.Get(ctx, client, userCache, map[string]string{"id": userID}, redis.GetOptions{})
```

**Why `sql` doesn't have a `CacheKeyParam`/`FilePathParam` equivalent.**
Cache/File/REST/Events all address a resource via a **templated string**
(`"user:{id}"`, `"data/{date}/{sensor}.json"`, `"/users/{id}"`,
`"sensors/{id}/data"`) that needs per-`{var}` validation before
substitution. SQL has no templated key at all — `sqlc`'s generated methods
take strongly-typed Go parameters directly (`queries.GetUser(ctx, id)`),
and those parameters are already validated by the exact same
`codec.Validate` mechanism as the row itself (pre-query validation, see
[SQL Adapter](sql.md#two-usage-modes)). `Table`/`Op` — the closest thing SQL
has to "key" metadata — are plain descriptive labels for error/observer
context only, never parsed or expanded, so there is nothing for a per-var
codec to attach to. `sql.DecorateInput`/`DecorateOutput` (see
[SQL Adapter](sql.md#declare-once--decorateinputdecorateoutput)) still give
SQL the "reuse" half of this pattern — declare a validated function once
instead of repeating `Table`/`Op`/`Validate()` at every call site — just
without a key-var codec, because there's no key to have one.
