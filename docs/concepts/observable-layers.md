# Codec Layers as Observable Layers

> See also: [Feature: Metrics Observer](../features/observer.md) · [`stats` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats)

go-codex has three codec layers — domain types, API contracts, and forge pipelines. Each layer boundary is simultaneously an **observable boundary**: a natural instrumentation point for logging, metrics, and distributed tracing. One observer value, injected once per layer, covers all three signals without any changes to business logic.

## Every layer boundary is an observable boundary

```
┌─────────────────────────────────────────────────────────────────────────────┐
│  LAYER 1 — CODECS (codex/)                                                  │
│                                                                             │
│  codex.String().Refine(validate.Email)      ← decode & encode run here      │
│                                                                             │
│  Observable:  stats.ValidationObserver                                      │
│  Signal:      metrics / logging                                             │
│  Event:       RecordValidationError(location, constraint, field)            │
│  Inject via:  stats.ReportErrors(obs, "body", err)                          │
├─────────────────────────────────────────────────────────────────────────────┤
│  LAYER 2 — API ADAPTERS (api/rest · api/events · api/mcp)                   │
│                                                                             │
│  nethttp.Register  ·  mqtt.SubscribeHandler  ·  mcpgo.ToolHandler           │
│                                                                             │
│  Observable:  stats.Observer (embeds ValidationObserver)                    │
│               + stats.SecurityObserver (optional extension)                 │
│  Signal:      metrics / logging / distributed tracing                       │
│  Events:      RecordRequest(method, path, statusCode, duration)             │
│               RecordSubscribe(topic, success, duration)                     │
│               RecordPublish(topic, success, duration)                       │
│               RecordSecurityRejection(location, scheme)                     │
│               StartSpan / EndSpan   ← via stats.TraceObserver               │
│  Inject via:  nethttp.Options{Observer: obs}                                │
│               mqtt.SubscribeOptions{Observer: obs}                          │
│               mcpgo.Options{Observer: obs}                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│  LAYER 2b — FILE I/O (format.File)                                          │
│                                                                             │
│  format.NewFile(template, fmt)  ·  file.Read · Write · Update · Patch       │
│                                                                             │
│  Observable:  stats.FileObserver (optional extension)                       │
│  Signal:      metrics / logging / distributed tracing                       │
│  Events:      RecordFileRead(path, success, duration)                       │
│               RecordFileWrite(path, success, duration)                      │
│               StartSpan / EndSpan   ← via stats.TraceObserver               │
│  Inject via:  format.FileOptions{Observer: obs}                             │
├─────────────────────────────────────────────────────────────────────────────┤
│  LAYER 3 — FORGE PIPELINES (forge/)                                         │
│                                                                             │
│  forge.NewFunction · forge.Compose · forge.Registry                         │
│                                                                             │
│  Observable:  stats.PipelineObserver                                        │
│  Signal:      metrics / logging / distributed tracing                       │
│  Event:       RecordApply(name, version, success, duration)                 │
│               StartSpan / EndSpan   ← via stats.TraceObserver               │
│  Inject via:  forge.NewRegistry(...).WithObserver(obs)                      │
└─────────────────────────────────────────────────────────────────────────────┘
```

`TraceObserver` is the one cross-cutting interface — it participates in every layer and provides distributed tracing spans without any changes to codec, adapter, or forge code.

## The four observation layers

Each layer has a different observation mechanism and a different concept of "what is observed":

| Layer | What is observed | Mechanism | Observer interface | When to use |
|-------|-----------------|-----------|-------------------|------------|
| **Transport** | Request lifecycle — one event per HTTP request, MQTT message, ZeroMQ call | `Options{Observer: obs}` or `stats.WithObserver(ctx, obs)` | `stats.Observer` | Infrastructure metrics per transport boundary |
| **Stream item** | Each item flowing through a reactive pipeline operator | `stream.ApplyOptions{Observer: obs}` or context | `stats.StreamObserver` | Throughput, latency per stream operator |
| **Computation** | Each forge function apply — success/failure/duration | `forge.Registry.WithObserver(obs)` | `stats.PipelineObserver` | Governed KPI computation telemetry |
| **Domain event** | Business-significant values in the stream — typed, per-value | `stream.Tap(ctx, src, func(v T) {...})` | User callback (no interface) | Domain-level observation: dashboards, audit logs, alerts |

The four layers compose: a single `stream.Apply` call fires **both** `PipelineObserver.RecordApply`
(from the forge layer) **and** `StreamObserver.RecordStreamItem` (from the stream layer)
if both are present.

### Forge functions are pure — observer calls belong in the adapter/stream layer

Forge functions (`func(In) (Out, error)`) are pure domain computations: they receive
typed inputs, return typed outputs or structured errors, and have no knowledge of
observers, transports, or streams. **Do not call observer methods inside a forge
function body.**

Observer calls attach at the layers that surround the function:

```
stream.Apply(ctx, src, oeeCalcFn, opts)
    ↑ RecordStreamItem fires here (stream layer)
        oeeCalcFn.ApplyContext(ctx, in)
            ↑ RecordApply fires here (forge/registry layer)
                func(in OEEIn) (OEEResult, error) { ... }
                    ↑ pure — no observer, no ctx dependency
```

For domain-level observation of results, use `stream.Tap` after `stream.Apply`:

```go
results = stream.Tap(ctx, results, func(r OEEResult) {
    if r.OEE < 0.75 {
        slog.Warn("OEE below threshold", "oee", r.OEE)  // domain event — typed value
    }
})
```

For structured error observation, use `stream.MapErr` or the `Drain` error callback —
errors are typed (`forge.InputError`, `forge.ApplyError`, etc.) and implement
`slog.LogValuer` for zero-effort structured logging.

## Observer interface summary

| Interface | Signals | Methods | Layer |
|---|---|---|---|
| `stats.ValidationObserver` | metrics, logging | `RecordValidationError` | Codecs |
| `stats.Observer` | metrics, logging | embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish` | Adapters |
| `stats.SecurityObserver` | metrics, logging | `RecordSecurityRejection` | Adapters |
| `stats.FileObserver` | metrics, logging | `RecordFileRead`, `RecordFileWrite` | File I/O |
| `stats.PipelineObserver` | metrics, logging | `RecordApply` | Forge |
| `stats.TraceObserver` | distributed tracing | `StartSpan`, `EndSpan` | All layers |

Every interface is optional and additive: implement only what you need, and adapters type-assert before calling optional interfaces — existing `Observer` implementations never break.

## The three observability signals

### Logging

`stats.NewLoggingObserver` implements all five non-tracing interfaces. Every event is logged as a structured `slog` message. Configure the logger handler for your environment:

```go
import "log/slog"

// Development: human-readable text
obs := stats.NewLoggingObserver(slog.New(slog.NewTextHandler(os.Stderr, nil)))

// Production: structured JSON for log aggregation
obs := stats.NewLoggingObserver(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

// With component label
obs := stats.NewLoggingObserver(
    slog.Default().With("component", "api"),
)
```

`LoggingObserver` intentionally does **not** implement `TraceObserver` — logging and tracing are separate concerns. Wire them together via `NewFanout`.

### Metrics

Implement `stats.Observer` (and the optional extension interfaces) with your metrics backend:

```go
type PrometheusObserver struct {
    reqTotal    *prometheus.CounterVec
    validErrors *prometheus.CounterVec
    applyTotal  *prometheus.CounterVec
}

func (o *PrometheusObserver) RecordRequest(method, path string, code int, d time.Duration) {
    o.reqTotal.WithLabelValues(method, path, strconv.Itoa(code)).Inc()
}

func (o *PrometheusObserver) RecordValidationError(loc, constraint, field string) {
    o.validErrors.WithLabelValues(loc, constraint).Inc()
}

// Implement PipelineObserver as well — same struct, no embedding required.
func (o *PrometheusObserver) RecordApply(name, version string, success bool, _ time.Duration) {
    o.applyTotal.WithLabelValues(name, version, strconv.FormatBool(success)).Inc()
}
```

No library dependency on the metric backend is required — the `stats` package imports only stdlib.

### Distributed tracing

Implement `stats.TraceObserver` to receive span start and end events. Adapters pass the context through to your application handler, enabling parent-child span relationships:

```go
type OTelTracer struct{}

func (t *OTelTracer) StartSpan(ctx context.Context, operation, name string) context.Context {
    ctx, _ = otel.Tracer("go-codex").Start(ctx, operation,
        trace.WithAttributes(attribute.String("name", name)),
    )
    return ctx
}

func (t *OTelTracer) EndSpan(ctx context.Context, err error) {
    span := trace.SpanFromContext(ctx)
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
    }
    span.End()
}
```

`operation` values follow a fixed convention across all adapters:

| Operation | Source |
|---|---|
| `"http.request"` | `adapters/nethttp`, `adapters/chi` |
| `"mqtt.subscribe"` | `adapters/mqtt` |
| `"mqtt.publish"` | `adapters/mqtt` |
| `"zmq.subscribe"` | `adapters/zeromq` |
| `"zmq.publish"` | `adapters/zeromq` |
| `"zmq.serve"` | `adapters/zeromq` (REP socket) |
| `"zmq.request"` | `adapters/zeromq` (REQ socket) |
| `"mqtt5.subscribe"` | `adapters/mqtt5` |
| `"mqtt5.publish"` | `adapters/mqtt5` |
| `"mqtt5.serve"` | `adapters/mqtt5` (Serve) |
| `"mqtt5.request"` | `adapters/mqtt5` (Call) |
| `"mcp.tool"` | `adapters/mcpgo` |
| `"mcp.resource"` | `adapters/mcpgo` |
| `"mcp.prompt"` | `adapters/mcpgo` |
| `"forge.apply"` | `forge` |
| `"file.read"` | `format.File` |
| `"file.write"` | `format.File` |

`name` is the concrete identifier — route path template, MQTT topic, forge function name, or file path.

## Composition: one observer value across all layers

`stats.NewFanout` fans out every call to multiple observers, and delegates optional interfaces (`FileObserver`, `SecurityObserver`, `PipelineObserver`, `TraceObserver`) only to inner observers that implement them:

```go
obs := stats.NewFanout(
    metricsObserver,                                          // Prometheus
    stats.NewLoggingObserver(slog.Default()),                 // structured logging
    tracer,                                                   // OTel tracing
)

// Pass the same value to every layer:
stats.ReportErrors(obs, "config", err)                        // codec layer
nethttp.Register(mux, route, handler, nethttp.Options{Observer: obs})  // adapter
mqtt.SubscribeHandler(ctx, handle, fn, mqtt.SubscribeOptions{Observer: obs})
file.Read(vars, format.FileOptions{Observer: obs})            // file I/O
forge.NewRegistry("P", "1.0.0").WithObserver(obs)            // forge
```

Each inner observer only sees the calls that match its interfaces. There is no boilerplate, no configuration file, and no changes to any existing business logic.

### Context-scoped default observer

As an alternative to per-call-site injection, attach the observer to a
`context.Context` once and have every adapter pick it up automatically:

```go
obs := stats.NewFanout(metricsObserver, stats.NewLoggingObserver(slog.Default()))
ctx := stats.WithObserver(context.Background(), obs)

// Adapters resolve obs from ctx when Options.Observer is nil:
mqtt.Subscribe(ctx, client, handle, 1, fn, mqtt.SubscribeOptions{})     // uses obs
stream.Apply(ctx, s, fn, stream.ApplyOptions{})                         // uses obs
file.Read(vars, format.FileOptions{Context: ctx})                       // uses obs
forge.NewRegistry("P", "1.0.0").WithObserver(obs)                       // explicit — no ctx
```

**Precedence:** explicit `opts.Observer` > context observer > `NoopObserver{}`.

HTTP adapters (`nethttp.Handler`, `chi.Handler`) resolve the observer per-request
from `r.Context()`, enabling per-request injection via a server middleware.
`forge.Registry` uses the explicit `.WithObserver(obs)` builder — no context
integration by design.

See [Feature: Observer Pattern — Default observer via context](../features/observer.md#default-observer-via-context) for the full API and per-layer resolution table.

## Distributed tracing across layers

When an HTTP request arrives carrying a `traceparent` header (propagated by OTel middleware), the adapter passes the incoming `context.Context` to `StartSpan`. The OTel SDK detects the parent span automatically and creates a child:

```
Incoming HTTP request (traceparent header present)
    └─ nethttp.Handler → StartSpan(ctx, "http.request", "/orders/{id}")
            └─ handler(ctx, req)
                    ├─ forge.Function.ApplyContext(ctx, in)
                    │       └─ StartSpan(ctx, "forge.apply", "availabilityCalc")
                    └─ file.Read(vars, FileOptions{Context: ctx, Observer: obs})
                            └─ StartSpan(ctx, "file.read", "/data/orders/42.json")
```

To propagate the handler context into forge and file I/O:

```go
// In your HTTP handler:
func handleOrder(ctx context.Context, req OrderReq) (OrderResp, error) {
    // forge: use ApplyContext instead of Apply
    avail, err := availabilityCalc.ApplyContext(ctx, req.Input)

    // file: pass ctx via FileOptions
    record, err := orderFile.Read(vars, format.FileOptions{
        Context:  ctx,
        Observer: obs,
    })
    return buildResponse(avail, record), err
}
```

When no parent span exists (direct test calls, `context.Background()`), the `TraceObserver` implementation receives an empty context and the OTel SDK creates a root span. The library does not decide — the implementation does.

## See also

- [Feature: Metrics Observer](../features/observer.md) — full API reference, Prometheus and OTel wiring, location values, and end-to-end examples
- [Guide: Observer Examples](../guides/observer.md) — runnable walkthroughs for all observer types
- [Concept: Codec as Domain Boundary](codec-as-domain-boundary.md) — the three-layer architecture this chapter builds on
- [Concept: Forge Pipelines](pipelines.md) — Layer 3 computation with `PipelineObserver`
- [`stats` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats) — interface definitions and `LoggingObserver`
- [examples/stats-observer](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) — runnable demo
- [examples/http-trace-span-propagation](https://github.com/DaniDeer/go-codex/tree/main/examples/http-trace-span-propagation) — OTel span propagation across HTTP + forge + file
