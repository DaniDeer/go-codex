# Protocol-Agnostic Pipeline Wiring — `ports`

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [Forge Pipelines concept](../concepts/pipelines.md) · [Wiring Guide](../guides/ports.md) · [Roadmap: Inside-Out Pipeline Wiring](../roadmap/inside-out-pipeline-wiring.md)
>
> Runnable demo: [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — one coherent use case wiring MQTT, SQL, file, and HTTP adapters to all four port types (`SourcePort`/`SinkPort`/`IOPort`/`ToolPort`), each declared with its `Pattern`; see its README for the full data-flow diagram.

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
The communication pattern (topic, method+path, params) is declared **once**, directly
on the port, via [`Pattern`](#pattern--declare-the-wire-shape-once); the port builds
its own handle, builder-free — no separate `events.NewChannel`/`Register` step:

```go
// domain/pipeline.go — no adapter imports; pattern declared once, here
var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.EventPattern{
                Topic: "sensors/{sensorID}/data",
                Opts:  []events.ChannelOpt{events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec)},
            },
        },
    }))

func StartPipeline(ctx context.Context) {
    sensors := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, sensors, oeeCalcFn, gstream.ApplyOptions{})
    // ...
}

// main.go — the only place that knows about MQTT5; handle is derived, not hand-built
handle, _ := ports.EventHandle[SensorReading](domain.SensorReadings)
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, handle, 0, fmt, opts))
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
var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Buffer: 8,
        Patterns: []ports.Pattern{
            ports.EventPattern{Topic: "sensors/{sensorID}/data", Opts: []events.ChannelOpt{
                events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec),
            }},
        },
    }))

// main.go — derive handles from the port, bind one adapter or several for fan-in:
eventHandle, _ := ports.EventHandle[SensorReading](domain.SensorReadings)
domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, eventHandle, 0, fmt, opts))
domain.SensorReadings.Bind(ctx, nethttp.IngestAdapter(mux, ingestHandle, opts)) // fan-in (still handle-first — see note below)

sensors := domain.SensorReadings.Stream(ctx) // gstream.Stream[SensorReading]
```

> `ports.EventPattern` covers pub/sub (MQTT/ZeroMQ). REST ingest (`nethttp.IngestAdapter`)
> still takes a hand-built `*rest.RouteHandle[Req, struct{}]` — REST ingest/SSE Pattern
> support is tracked as follow-up work (see the roadmap doc's Phase 4 section).

`Stream(ctx)` must be called after all `Bind` calls. It returns the merged stream;
adapter and codec validation errors are routed to `Stream.Errors`.

## `SinkPort[T]`

Declares an outbound boundary. Bind one or more `SinkAdapter[T]` implementations; every
item fed into the port is broadcast (fan-out) to all bound adapters. A failure in one
adapter does not stop delivery to the others.

```go
var OEEResults = codex.Must(ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{
    Buffer: 8,
    Patterns: []ports.Pattern{
        ports.EventPattern{Topic: "alerts/{sensorID}", Opts: []events.ChannelOpt{
            events.TopicParam{Name: "sensorID"},
        }},
    },
}))

alertHandle, _ := ports.EventHandle[OEE](domain.OEEResults)
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
    "calibration", ReadingCodec, calibratedCodec, ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.RESTPattern{Method: "GET", Path: "/calibration/{sensorID}"},
        },
    }))

calibrated := domain.Calibration.Connect(ctx, sensors) // gstream.Stream[CalibratedReading]

// main.go — pick exactly one; handle is derived from the port for REST:
calibHandle, _ := ports.RESTHandle[SensorReading, CalibratedReading](domain.Calibration)
domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, calibHandle, callOpts))
// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(calibCodec, queryFn, opts))     // no Pattern — file/sql use Params
// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine, opts))
```

`NewSourcePort`, `NewSinkPort`, `NewIOPort`, and `NewToolPort` all return
`(*Port, error)` — a declared `Pattern` is built eagerly via `Register` (fail-fast)
and can fail (unknown param name, path/topic constraint failure, duplicate name on
a shared `reqreply`/`mcp` builder). Wrap with `codex.Must(...)` for package-level
declarations, as shown throughout this page.

`Connect` returns a stream carrying `PortNoAdapterError` in `Stream.Errors` if no
adapter was bound before the pipeline started.

## `ToolPort[In,Out]`

Declares a server-side request/response boundary — the complement of `IOPort` (which is
client-side). Set the pipeline function once with `SetPipeline`, then bind it to one or
more transports. The **same pipeline logic** can serve MCP, HTTP, and ZeroMQ simultaneously.

```go
var OEETool = codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.RESTPattern{Method: "POST", Path: "/oee/calc"},
            ports.ReqReplyPattern{Topic: "oee/calc"},
            ports.MCPPattern{Name: "oee-calc", Opts: []apimcp.ToolOpt{
                apimcp.ToolMeta{Description: "Calculates OEE from sensor data"},
            }},
        },
    }))

func init() {
    OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
        return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
    })
}

// main.go — serve the same pipeline on three transports; each handle is derived
// from the ONE Pattern declared above — no separate builder/Register calls:
mcpHandle, _ := ports.MCPHandle[OEEIn, OEEResult](domain.OEETool)
httpHandle, _ := ports.RESTHandle[OEEIn, OEEResult](domain.OEETool)
zmqHandle, _ := ports.ReqReplyHandle[OEEIn, OEEResult](domain.OEETool)
domain.OEETool.Bind(ctx, mcpgo.ToolPipelineAdapter(mcpServer, mcpHandle, mcpgo.Options{}))
domain.OEETool.Bind(ctx, nethttp.PipelineAdapter(mux, httpHandle, nethttp.PipelineAdapterOptions{}))
domain.OEETool.Bind(ctx, zeromq.ServeAdapter(repSock, zmqHandle, zeromq.ServeOptions{}))

// Optionally, build the OpenAPI/AsyncAPI/MCP spec FROM the same binding:
restBuilder := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
ports.RegisterREST[OEEIn, OEEResult](restBuilder, domain.OEETool) //nolint:errcheck
```

`Bind` returns `PortBindError` wrapping `PortNoPipelineError` if `SetPipeline` was not
called first.

---

## `Pattern` — declare the wire shape once

`ports.Pattern` is the **primary** way to declare a port's communication pattern —
method+path, topic, or MCP tool name, plus routing params — directly on the port,
reusing the exact same option vocabulary as `rest.NewRoute`/`events.NewChannel`/
`reqreply.NewRoute`/`apimcp.NewTool` (`PathParam`, `QueryParam`, `TopicParam`, …).
No new param types, no separate `events.NewChannel(...).Register(builder)` call
written by hand: the port makes that call **internally**.

| Pattern | Protocol family | Wraps |
|---------|------------------|-------|
| `RESTPattern{Method, Path, Opts}` | HTTP (nethttp, chi) | `rest.RouteOpt` |
| `EventPattern{Topic, Opts}` | pub/sub (mqtt, mqtt5, zeromq) | `events.ChannelOpt` |
| `ReqReplyPattern{Topic, Opts}` | request/reply (mqtt5, zeromq) | `reqreply.RouteOpt` |
| `MCPPattern{Name, Opts}` | MCP tool (mcpgo) | `apimcp.ToolOpt` |
| `FilePattern{Path, Format, Opts}` | typed files (file) | `format.FileOpt` |
| `SQLPattern{Table, Op}` | SQL (sql) | — (metadata-only) |

A port declares one `Pattern` entry **per protocol family** it will be bound to — a
`ToolPort` exposed over HTTP + MQTT 5 + MCP simultaneously (as in the `OEETool`
example above) declares three. Each is built into a handle at construction time and
retrieved with the matching accessor:

| Accessor | Returns |
|----------|---------|
| `ports.RESTHandle[Req,Resp](port)` | `(*rest.RouteHandle[Req,Resp], bool)` |
| `ports.EventHandle[T](port)` | `(*events.ChannelHandle[T], bool)` |
| `ports.ReqReplyHandle[Req,Resp](port)` | `(*reqreply.RouteHandle[Req,Resp], bool)` |
| `ports.MCPHandle[In,Out](port)` | `(*apimcp.ToolHandle[In,Out], bool)` |
| `ports.FileHandle[T](port)` | `(format.File[T], bool)` |
| `ports.SQLMeta(port)` | `(ports.SQLPattern, bool)` |

Each accessor returns `(nil, false)` — not an error, not a panic — when the port
declared no matching `Pattern`.

### One construction path — `Register`, always

Internally, a `Pattern` is turned into a handle via **exactly the same**
`Route`/`Channel`/`Tool.Register(builder)` call a hand-declared route makes —
`ports` never calls the weaker, builder-free `ClientHandle()`. This makes a
`Pattern`-derived handle **indistinguishable** from one built by calling
`Register` directly: `ports.EventHandle[T](someSourcePort)` and
`events.NewChannel[T](topic, codec, opts...).Register(myBuilder)` produce the
same `*events.ChannelHandle[T]`, and any adapter (`mqtt5.SubscribeAdapter`, etc.)
that receives either cannot tell which one it got.

Supply your own `*Builder` via `PortOptions` to get full parity with a
hand-registered route — security schemes, global security, and whole-path/topic
format constraints all become available, and the port's route/channel/tool
accumulates directly into *your* spec document:

```go
restBuilder := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
restBuilder.AddSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})
restBuilder.AddGlobalSecurity(route.SecurityRequirement{"bearerAuth": {}})

domain.OEETool, err := ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{
        Patterns:    []ports.Pattern{ports.RESTPattern{Method: "POST", Path: "/oee/calc"}},
        RESTBuilder: restBuilder, // <- full parity: security schemes now enforced
    })

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
    // MissingPatternError if the port declared no RESTPattern
}
spec, _ := b.OpenAPISpec()
```

> **Scope note:** `RESTPattern` for `SourcePort`/`SinkPort` (HTTP ingest/SSE, which
> need an asymmetric `Req`/`Resp` shape a single-codec port can't express directly)
> is a documented open item — see the roadmap doc's Phase 4/5 sections for the
> full design and rationale. `NewIOPort`/`NewToolPort`/`NewSourcePort`/`NewSinkPort`
> all now return `(*Port, error)` — `Register` is fallible (unknown param names,
> path/topic constraint failures, duplicate names on `reqreply`/`mcp`) in ways the
> old builder-free construction wasn't.

### `FilePattern` — typed files as sink or intermediate IO

`FilePattern` gives the file adapter the same declare-once story: the path
template, wire format, and path-param codecs live on the port; the built
`format.File` handle comes back out via `ports.FileHandle`. `Format` is a
`FileFormatKind` enum — `FileFormatJSON` (default), `FileFormatYAML`, or
`FileFormatTOML` — applied to the port's own codec (a custom `format.Format[T]`
can't sit in a non-generic struct; for those, build the `format.File` by hand).

- On a **`SinkPort[T]`** the handle is a `format.File[T]` of the payload type —
  pairs with `file.DrainWriteFileAdapter`.
- On an **`IOPort[Req,Resp]`** the handle is a `format.File[Resp]` of the
  **response** type (the file's content *is* the port's response) — pairs with
  `file.ReadAdapter`, the 2-type per-item read. (The 3-type
  `file.ReadEachAdapter`, with its independent file-content type and `combine`
  func, stays handle-first for enrichment cases.)

```go
// domain — per-item calibration lookup as intermediate IO, zero adapter imports
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibrationData](
    "calibration", readingCodec, calibrationCodec,
    ports.PortOptions{Patterns: []ports.Pattern{
        ports.FilePattern{
            Path: "data/{sensorID}/calibration.json", // JSON is the default Format
            Opts: []format.FileOpt{format.FilePathParam{Name: "sensorID"}.WithCodec(uuidCodec)},
        },
    }}))

// main.go — handle derived from the port; swap file → SQL → HTTP freely
calibFile, _ := ports.FileHandle[CalibrationData](domain.Calibration)
domain.Calibration.Bind(ctx, file.ReadAdapter(calibFile,
    func(r SensorReading) map[string]string { return map[string]string{"sensorID": r.SensorID} },
    file.ReadEachAdapterOptions{}))
```

There is no `RegisterFile` — files have no spec document concept
(`File.PathParamSchemas()` already serves doc tooling), and `format.NewFile` is
infallible, so a `FilePattern` never causes the port constructor to error.

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
var Readings = codex.Must(ports.NewSinkPort[db.Reading]("sql/readings", readingCodec,
    ports.PortOptions{Patterns: []ports.Pattern{
        ports.SQLPattern{Table: "readings", Op: "insert_reading"},
    }}))

// main.go — Table/Op no longer repeated in the options struct
domain.Readings.Bind(ctx, sql.DrainInsertAdapter(readingCodec, insertFn, sql.DrainInsertOptions{}))
```

---

## `IOParam` — protocol-agnostic parameters (handle-less adapters)

`PortOptions.Params` is the enforcement mechanism for adapters with **no** protocol-level
builder of their own — `file.ReadEachAdapter`, `file.DrainWriteFileAdapter` (their
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

`PortOptions{Patterns, Params, Buffer, Observer}` configures all four port constructors.
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
`api/reqreply`) — those still generate OpenAPI/AsyncAPI/MCP specs and produce the typed
`RouteHandle`/`ChannelHandle`/`ToolHandle` that adapters use internally. With `Pattern`,
ports **build those handles internally** (builder-free, via each type's `ClientHandle`
method) from a declaration made once on the port — pipeline code never needs to import
the handle's owning transport package directly, and there is no second, separate
`NewRoute`/`NewChannel`/`Register` step. When you also want an OpenAPI/AsyncAPI/MCP
spec document, `RegisterREST`/`RegisterEvent`/`RegisterReqReply`/`RegisterMCP` replay
the same declared `Pattern` against a real `Builder` — building the spec **from** the
binding rather than the other way around.

Declaring a route/channel/tool directly via the builders (`rest.NewRoute(...).Register(b)`,
etc.) and passing the resulting handle straight into an adapter constructor remains fully
supported — useful when sharing one handle across a port-based binding and a separate,
standalone adapter call, or when the port itself doesn't need a `Pattern` (handle-less
adapters like `file`/`sql` use `Params` instead; see below).

Standalone (non-pipeline) use of adapters — `mqtt5.Subscribe`, `nethttp.Call`,
`zeromq.Serve`, etc. — remains fully supported and unaffected by `ports`.
