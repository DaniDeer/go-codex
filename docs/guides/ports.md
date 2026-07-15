# Wiring Pipelines with Ports

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) · [Ports feature page](../features/ports.md) · [Roadmap: Inside-Out Pipeline Wiring](../roadmap/inside-out-pipeline-wiring.md)

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
// domain/pipeline.go — no adapter imports; topic + params declared once, here
var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec,
    ports.PortOptions{
        Buffer: 8,
        Patterns: []ports.Pattern{
            ports.EventPattern{
                Topic: "sensors/{sensorID}/data",
                Opts:  []events.ChannelOpt{events.TopicParam{Name: "sensorID"}.WithCodec(sensorIDCodec)},
            },
        },
    }))

func StartPipeline(ctx context.Context) {
    readings := SensorReadings.Stream(ctx)
    oeeStream := gstream.Apply(ctx, readings, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — all protocol decisions here; handle derived from the port, not hand-built
sensorHandle, _ := ports.EventHandle[SensorReading](domain.SensorReadings)
domain.SensorReadings.Bind(ctx,
    mqtt5.SubscribeAdapter(client, router, sensorHandle, 0,
        format.JSON(domain.ReadingCodec),
        mqtt5.SubscribeAdapterOptions{TopicFilter: "sensors/+/data"}))

// Fan-in: add a second source without touching pipeline code
// (REST ingest still takes a hand-built handle — see the "Pattern" section below)
domain.SensorReadings.Bind(ctx,
    nethttp.IngestAdapter(mux, ingestHandle, nethttp.IngestAdapterOptions{Buffer: 8}))
```

### `SinkPort[T]` — outbound boundary

```go
// domain/pipeline.go
var OEEResults = codex.Must(ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{
    Buffer: 8,
    Patterns: []ports.Pattern{
        ports.EventPattern{Topic: "alerts/{sensorID}"},
    },
}))

// main.go — fan-out: both adapters receive every item
alertHandle, _ := ports.EventHandle[OEE](domain.OEEResults)
domain.OEEResults.Bind(ctx,
    mqtt5.PublishAdapter(client, alertHandle,
        format.JSON(domain.OEECodec), mqtt5.MQTT5DrainPublishOptions{}))
domain.OEEResults.Bind(ctx,
    nethttp.SSEAdapter(mux, sseHandle, nethttp.SSEAdapterOptions{}))
```

### `IOPort[Req, Resp]` — intermediate IO

Swap the enrichment source without changing pipeline code:

```go
// domain/pipeline.go — declare the call pattern once; NewIOPort returns (port, error)
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec,
    ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.RESTPattern{Method: "GET", Path: "/calibration/{sensorID}"},
        },
    }))

func StartPipeline(ctx context.Context) {
    raw        := SensorReadings.Stream(ctx)
    calibrated := Calibration.Connect(ctx, raw)       // ← IOPort in the middle
    oeeStream  := gstream.Apply(ctx, calibrated, oeeCalcFn, gstream.ApplyOptions{})
    go OEEResults.Feed(ctx, oeeStream)
}

// main.go — choose ONE enrichment source; REST handle is derived from the port:
handle, _ := ports.RESTHandle[SensorReading, CalibratedReading](domain.Calibration)
domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, "http://calib-svc", handle, callOpts))
// domain.Calibration.Bind(ctx, sql.QueryEachAdapter(db, calibCodec, queryFn, opts))       // no Pattern — file/sql use Params instead
// domain.Calibration.Bind(ctx, file.ReadEachAdapter(calibFile, varsFor, combine, opts))
// domain.Calibration.Bind(ctx, mqtt5.CallAdapter(client, router, reqReplyHandle, callOpts)) // ports.ReqReplyPattern + ports.ReqReplyHandle
// domain.Calibration.Bind(ctx, zeromq.CallAdapter(sock, reqReplyHandle, opts))
```

### `ToolPort[In, Out]` — server-side request/response

The complement of `IOPort`: instead of the pipeline calling out, an external caller
triggers the pipeline and waits for a response. Set the pipeline function once with
`SetPipeline`, then bind it to one or more transports — the **same pipeline logic**
can serve MCP, HTTP, and ZeroMQ simultaneously.

```go
// domain/pipeline.go — no adapter imports; declare all three transport patterns once
var OEETool = codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{
        Patterns: []ports.Pattern{
            ports.RESTPattern{Method: "POST", Path: "/oee/calc"},
            ports.ReqReplyPattern{Topic: "oee/calc"},
            ports.MCPPattern{Name: "oee-calc"},
        },
    }))

func init() {
    OEETool.SetPipeline(func(ctx context.Context, req OEEIn) gstream.Stream[OEEResult] {
        return gstream.Apply(ctx, gstream.Single(ctx, req), oeeCalcFn, gstream.ApplyOptions{})
    })
}

// main.go — serve the same pipeline on three transports; handles derived from the port:
mcpToolHandle, _ := ports.MCPHandle[OEEIn, OEEResult](domain.OEETool)
httpHandle, _ := ports.RESTHandle[OEEIn, OEEResult](domain.OEETool)
zmqHandle, _ := ports.ReqReplyHandle[OEEIn, OEEResult](domain.OEETool)
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

Derive the handle the adapter needs with the matching accessor — `(nil, false)`, not
a panic, when the port declared no matching `Pattern`:

```go
handle, ok := ports.RESTHandle[Req, Resp](domain.SomePort)     // *rest.RouteHandle[Req, Resp]
handle, ok := ports.EventHandle[T](domain.SomePort)            // *events.ChannelHandle[T]
handle, ok := ports.ReqReplyHandle[Req, Resp](domain.SomePort) // *reqreply.RouteHandle[Req, Resp]
handle, ok := ports.MCPHandle[In, Out](domain.SomePort)        // *apimcp.ToolHandle[In, Out]
```

### One construction path, whether you supply a `Builder` or not

Internally, a `Pattern` always becomes a handle via the **same**
`Route`/`Channel`/`Tool.Register(builder)` call a hand-declared route makes —
never the weaker, builder-free `ClientHandle()`. Supply your own `*Builder` via
`PortOptions` to get full parity with a hand-registered route (security schemes,
global security, path/topic format constraints, shared spec accumulation); when
you don't, `ports` registers against a private, single-use `Builder` instead —
same zero-ceremony default, identical code path:

```go
restBuilder := rest.NewBuilder(rest.Info{Title: "OEE Service", Version: "1.0.0"})
restBuilder.AddSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")})
restBuilder.AddGlobalSecurity(route.SecurityRequirement{"bearerAuth": {}})

domain.OEETool := codex.Must(ports.NewToolPort[OEEIn, OEEResult]("oee-calc", oeeInCodec, oeeResultCodec,
    ports.PortOptions{
        Patterns:    []ports.Pattern{ports.RESTPattern{Method: "POST", Path: "/oee/calc"}},
        RESTBuilder: restBuilder, // <- security scheme now actually enforced
    }))

// restBuilder already has /oee/calc registered — spec generation needs no
// separate step:
spec, _ := restBuilder.OpenAPISpec()
```

| `PortOptions` field | Pattern | Gives you |
|---|---|---|
| `RESTBuilder *rest.Builder` | `RESTPattern` | Security schemes, global security, `rest.WithPathConstraints` |
| `EventBuilder *events.Builder` | `EventPattern` | Security schemes, global security, `events.WithTopicConstraints` |
| `ReqReplyBuilder *reqreply.Builder` | `ReqReplyPattern` | Duplicate-topic detection |
| `MCPBuilder *apimcp.Builder` | `MCPPattern` | Duplicate-name detection |

> **Before this, every `Pattern`-derived handle silently had no security
> enforcement** — `SecuritySchemes` was always an empty map (the credential check
> skips unknown scheme names rather than rejecting), so any `RouteMeta.Security`/
> `Subscribe.Security`/`Publish.Security` requirement declared on a `Pattern`-based
> port had no effect. Supply a `Builder` with `AddSecurityScheme`/`AddGlobalSecurity`
> to fix this for a given port.

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

`RegisterEvent`, `RegisterReqReply`, and `RegisterMCP` do the same for their builders.

`NewSourcePort`, `NewSinkPort`, `NewIOPort`, and `NewToolPort` all return
`(*Port, error)` — a `Pattern` is built eagerly at construction time via `Register`
(fail-fast) and can fail (unknown param name, path/topic constraint failure,
duplicate name on a shared `reqreply`/`mcp` builder) — wrap with `codex.Must(...)`
for package-level declarations, as shown throughout this guide.

> REST ingest (`SourcePort`) and SSE (`SinkPort`) need an asymmetric `Req`/`Resp`
> shape a single-codec port can't express directly with `RESTPattern` yet — these
> still take a hand-built handle. See the roadmap doc's Phase 4/5 sections for the
> full design and this open item.

## `IOParam` — protocol-agnostic parameters (handle-less adapters)

`PortOptions.Params` is the enforcement mechanism for adapters with **no**
`Pattern`/handle of their own — `file.ReadEachAdapter` and `file.DrainWriteFileAdapter`
(their `varsFor` function extracts a `map[string]string`):

```go
// Declare once on the port — the adapter validates via context, not a hand-built handle
ports.IOParam{Name: "sensorID", Required: true}.WithCodec(sensorIDCodec)
```

The port propagates `Params` via context (`ports.WithParams`) and the adapter calls
`ports.ValidateParams` against each item's extracted `varsFor` map, surfacing
failures as `ReadError`/`WriteError` wrapping `codex.ValidationErrors`. For
handle-backed adapters, use `Pattern` instead — `Params` is not consulted there
since the derived handle already validates fully.

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
