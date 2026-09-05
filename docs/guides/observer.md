# Guide: Using the Observer Pattern

> Feature overview: [Metrics Observer](../features/observer.md)
>
> Runnable demos: [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) · [`examples/adapters-nethttp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) · [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) · [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch)

## End-to-end example: using all six interfaces

A single `stats.NewFanout` value implements all six observer interfaces. Pass it to HTTP, files,
forge pipelines, and codecs — no type assertions needed:

```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "sync"
    "time"

    "github.com/DaniDeer/go-codex/codex"
    "github.com/DaniDeer/go-codex/forge"
    "github.com/DaniDeer/go-codex/format"
    "github.com/DaniDeer/go-codex/stats"
    "github.com/DaniDeer/go-codex/validate"
)

// ── 1. Pure metrics observer — counts everything ───────────────────────────

type Metrics struct {
    mu          sync.Mutex
    requests    int
    applies     int
    fileReads   int
    fileWrites  int
    valErrors   int
    rejections  int
}

func (m *Metrics) RecordRequest(_, _ string, _ int, _ time.Duration) { m.mu.Lock(); m.requests++; m.mu.Unlock() }
func (m *Metrics) RecordSubscribe(_ string, _ bool, _ time.Duration)  {}
func (m *Metrics) RecordPublish(_ string, _ bool, _ time.Duration)    {}
func (m *Metrics) RecordValidationError(_, _, _ string)               { m.mu.Lock(); m.valErrors++; m.mu.Unlock() }
func (m *Metrics) RecordApply(_, _ string, _ bool, _ time.Duration)   { m.mu.Lock(); m.applies++; m.mu.Unlock() }
func (m *Metrics) RecordSecurityRejection(_, _ string)                { m.mu.Lock(); m.rejections++; m.mu.Unlock() }
func (m *Metrics) RecordFileRead(_ string, _ bool, _ time.Duration)   { m.mu.Lock(); m.fileReads++; m.mu.Unlock() }
func (m *Metrics) RecordFileWrite(_ string, _ bool, _ time.Duration)  { m.mu.Lock(); m.fileWrites++; m.mu.Unlock() }

// ── 2. Trace observer — records span names in memory ───────────────────────

type Tracer struct{ stats.NoopObserver; mu sync.Mutex; entries []string }

func (t *Tracer) StartSpan(ctx context.Context, op, name string) context.Context {
    t.mu.Lock(); t.entries = append(t.entries, op+":"+name); t.spans++; t.mu.Unlock()
    return ctx
}
func (t *Tracer) EndSpan(_ context.Context, _ error) {}

func main() {
    logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

    // ── 3. Compose all three into a single observer ────────────────────────

    metrics := &Metrics{}
    tracer  := &Tracer{}
    obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "app")), tracer)

    // ── 4. Codec validation (ValidationObserver) ───────────────────────────

    emailCodec := codex.String().Refine(validate.Email)
    _, err := emailCodec.Decode("not-an-email")
    stats.ReportErrors(obs, "config", err)

    // ── 5. File read (FileObserver + TraceObserver) ────────────────────────

    f := ports.NewFile("/tmp/test.json", format.JSON(codex.String()))
    _, err = f.Read(nil, ports.FileOptions{Observer: obs})
    // LoggingObserver: level=DEBUG msg="file read" path=... success=false
    // TraceObserver:   StartSpan("file.read", "/tmp/test.json")
    // Metrics:         fileReads++

    // ── 6. Forge pipeline (PipelineObserver + TraceObserver) ───────────────

    fn := forge.NewFunction("toUpper", "1.0.0",
        codex.String(),
        codex.String(),
        func(s string) (string, error) { return s, nil },
    )
    reg := forge.NewRegistry("demo", "1.0.0").WithObserver(obs)
    fn.Register(reg)
    fn.Apply("hello")
    // LoggingObserver: level=DEBUG msg="pipeline apply" ...
    // Metrics:         applies++

    // ── 7. Summary ─────────────────────────────────────────────────────────

    fmt.Printf("requests=%d applies=%d fileReads=%d fileWrites=%d valErrors=%d rejections=%d\n",
        metrics.requests, metrics.applies, metrics.fileReads, metrics.fileWrites,
        metrics.valErrors, metrics.rejections)
}
```

## Default observer: set once, use everywhere

Instead of passing `Observer: obs` on every call site, use
`stats.WithObserver(ctx, obs)` to store an observer in a context. Adapters,
stream bridges, and `ports.File` consult `stats.ObserverFromContext(ctx)` when
their `Options.Observer` field is nil — so one line at startup covers all
components that share the same context:

```go
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(slog.Default()))
ctx := stats.WithObserver(context.Background(), obs)

// All of the below use obs because Options.Observer is nil:
events.SubscribeHandle(ctx, sub, mqtt.NewSubscribeTransport[T](client, 1, mqtt.SubscribeOptions{}), fn)
stream.Apply(ctx, s, fn, stream.ApplyOptions{})
zeromq.Call(ctx, sock, handle, req, zeromq.CallOptions{})
```

**Explicit always wins:** if `opts.Observer` is non-nil, the context observer is
never consulted. Per-component overrides still work:

```go
route.WithOptions(nethttp.Options{Observer: auditObs}) // explicit, no lookup
```

**For HTTP servers** — the context observer is resolved *per-request* from
`r.Context()`, not at handler-construction time. Inject it via middleware:

```go
mux.Handle("/api/", ObserverMiddleware(obs)(apiHandler))

func ObserverMiddleware(obs stats.Observer) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            next.ServeHTTP(w, r.WithContext(stats.WithObserver(r.Context(), obs)))
        })
    }
}
```

Alternatively, pass `nethttp.Options{Observer: obs}` directly for service-level
(not per-request) wiring — simpler and slightly cheaper.

**For `ports.File`** — set `FileOptions.Context` to the context carrying the observer:

```go
value, err := configFile.Read(nil, ports.FileOptions{Context: ctx})
// observer resolved from ctx via FileOptions.Context
```

**forge.Registry** uses the explicit `.WithObserver(obs)` builder — no context
integration by design (registry is long-lived, set up at startup):

```go
reg := forge.NewRegistry("P", "1.0.0").WithObserver(obs) // explicit, no context
```

**`sql.Validate`** has no `ctx` parameter and falls back to `NoopObserver{}` only —
pass `ValidateOptions{Observer: obs}` directly.

See [Feature: Observer Pattern — Default observer via context](../features/observer.md#default-observer-via-context) for the full API reference and per-layer resolution table.

---

## When to use which observer wiring mechanism

Three mechanisms exist. Choose based on scope and lifecycle:

| Mechanism | How | Scope | When to use |
|-----------|-----|-------|-------------|
| `Options{Observer: obs}` | Pass per-call | Per-call or per-component | When different components need different observers (e.g. audit observer for one route, metrics observer for another) |
| `forge.Registry.WithObserver(obs)` | Builder at startup | Registry lifetime — all functions registered in that registry | Wiring the PipelineObserver for a governed forge pipeline |
| `stats.WithObserver(ctx, obs)` | Context | ctx lifetime — all adapter calls that receive this ctx | Service-wide default at startup; or per-request via HTTP middleware |

**Use `Options{Observer: obs}`** when you need a per-call override or when the
function call has no `ctx` (e.g. `sql.Validate`, `config.FromEnv`).

**Use `Registry.WithObserver(obs)`** when wiring a forge pipeline. The Registry is
the natural "set once" point for governed computations.

**Use `stats.WithObserver(ctx, obs)`** for everything else — it is the simplest
"set once at startup" approach and eliminates the need to pass `Observer: obs`
on every MQTT/ZeroMQ/stream call. For HTTP servers, pair with a middleware that
injects it per-request:

```go
// At startup:
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(slog.Default()))
ctx := stats.WithObserver(context.Background(), obs)

// For forge — explicit builder (no context integration):
reg := forge.NewRegistry("P", "1.0.0").WithObserver(obs)

// For HTTP — per-request via middleware:
mux.Handle("/", observerMiddleware(obs)(mux))
// or keep nethttp.Options{Observer: obs} — both work
```

These three mechanisms cover different layers of the same service and can all
be active simultaneously with the same `obs` value.

---

## Per-adapter usage

### Codec-level (ValidationObserver)

```go
type ConfigMetrics struct{ errors int }
func (o *ConfigMetrics) RecordValidationError(_, _, _ string) { o.errors++ }

metrics := &ConfigMetrics{}
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(slog.Default()))

val, err := appConfigCodec.Decode(rawData)
stats.ReportErrors(obs, "config", err)
```

`stats.ConstraintName(err)` extracts a stable label: `ConstraintError.Name`, `"type-mismatch"`, `"required"`, or `""`.

### HTTP adapter (Observer)

```go
type CountingObserver struct {
    mu             sync.Mutex
    total          int
    byStatus       map[int]int
    valErrorsByLoc map[string]int
    latencies      []time.Duration
}

func (o *CountingObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
    o.mu.Lock()
    defer o.mu.Unlock()
    o.total++
    if o.byStatus == nil { o.byStatus = make(map[int]int) }
    o.byStatus[statusCode]++
    o.latencies = append(o.latencies, d)
}

func (o *CountingObserver) RecordValidationError(location, constraintName, field string) {
    o.mu.Lock()
    defer o.mu.Unlock()
    if o.valErrorsByLoc == nil { o.valErrorsByLoc = make(map[string]int) }
    o.valErrorsByLoc[location]++
}

func (o *CountingObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (o *CountingObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

func (o *CountingObserver) Print() {
    for loc, n := range o.valErrorsByLoc {
        fmt.Printf("  validation errors at %q: %d\n", loc, n)
    }
}

var _ stats.Observer = (*CountingObserver)(nil)

obs := stats.NewFanout(&CountingObserver{}, stats.NewLoggingObserver(logger))
createUser.WithHandler(handler).WithOptions(nethttp.Options{Observer: obs})
```

> **Context observer:** `nethttp.Serve`/`ServeOne`/`ServeSSE` resolve the observer
> per-request from `r.Context()` when `opts.Observer` is nil. Use the middleware pattern above
> to inject `obs` at request time, or pass `nethttp.Options{Observer: obs}` directly for
> service-level wiring.

### MQTT adapter

```go
subTransport := amqtt.NewSubscribeTransport[T](client, qos, amqtt.SubscribeOptions{Observer: obs})
events.SubscribeHandle(ctx, sub, subTransport, handler)

pubTransport := amqtt.NewPublishTransport[T](client, qos, retained, amqtt.PublishOptions[T]{Observer: obs})
events.PublishHandle(ctx, pub, pubTransport, msg)
```

> **Context observer:** `mqtt`/`mqtt5`/`zeromq`'s `NewSubscribeTransport`/`NewPublishTransport`
> transports, `mqtt5.Serve`, `mqtt5.Call`, and all ZeroMQ request-reply adapter functions
> resolve the observer from `ctx` when `opts.Observer` is nil. Pass a context from
> `stats.WithObserver(ctx, obs)` at the call site to use the context observer.

### Forge pipeline (PipelineObserver)

```go
type PipelineCounts struct {
    mu       sync.Mutex
    applies  int
    failures int
}

func (l *PipelineCounts) RecordApply(name, version string, ok bool, d time.Duration) {
    l.mu.Lock()
    defer l.mu.Unlock()
    l.applies++
    if !ok { l.failures++ }
}

obs := stats.NewFanout(&PipelineCounts{}, stats.NewLoggingObserver(logger))
reg := forge.NewRegistry("Pipeline", "1.0.0").WithObserver(obs)
```

> **Context observer:** `forge.Registry` uses the explicit `.WithObserver(obs)` builder — no
> context integration by design. The registry is long-lived and configured once at startup.

### FileObserver (ports.File)

`ports.File[T]` type-asserts the observer in `FileOptions` to `stats.FileObserver`:

```go
type FileMetrics struct {
    mu         sync.Mutex
    fileReads  int
    fileWrites int
}

func (o *FileMetrics) RecordFileRead(path string, ok bool, d time.Duration) {
    o.mu.Lock(); defer o.mu.Unlock(); o.fileReads++
}

func (o *FileMetrics) RecordFileWrite(path string, ok bool, d time.Duration) {
    o.mu.Lock(); defer o.mu.Unlock(); o.fileWrites++
}

var _ stats.FileObserver = (*FileMetrics)(nil)

obs := stats.NewFanout(&FileMetrics{}, stats.NewLoggingObserver(logger))
opts := ports.FileOptions{Observer: obs}
cfg, err := configFile.Read(nil, opts)
```

`path` is the concrete path after template substitution, never the template string.

> **Context observer:** `ports.File.Read/Write/Update/Patch` resolve the observer from
> `opts.Context` when `opts.Observer` is nil. Set `FileOptions{Context: ctx}` where `ctx`
> carries the observer via `stats.WithObserver`.

### SecurityObserver

Adapters type-assert `stats.SecurityObserver` — purely additive, existing implementations need not change:

```go
type MyObserver struct {
    CountingObserver
    securityRejections int
}

func (o *MyObserver) RecordSecurityRejection(location, scheme string) {
    o.securityRejections++
}
```

### TraceObserver (distributed tracing)

6th optional interface — type-asserted by adapters, never embedded.

```go
type TraceObserver interface {
    StartSpan(ctx context.Context, operation, name string) context.Context
    EndSpan(ctx context.Context, err error)
}
```

OpenTelemetry implementation:

```go
type OTelTracer struct{ stats.NoopObserver }

func (t *OTelTracer) StartSpan(ctx context.Context, operation, name string) context.Context {
    ctx, span := otel.Tracer("go-codex").Start(ctx, operation,
        otel.WithAttributes(attribute.String("name", name)),
    )
    return ctx
}

func (t *OTelTracer) EndSpan(ctx context.Context, err error) {
    span := trace.SpanFromContext(ctx)
    if err != nil { span.RecordError(err) }
    span.End()
}

obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger), &OTelTracer{})
```

> **Note**: `LoggingObserver` does **not** implement `TraceObserver` (slog has no tracing). Use a slog→OTel bridge for log-trace correlation.

## Context propagation through layers

TraceObserver spans form a parent-child tree. go-codex adapters propagate the traced
`context.Context` through the application, enabling full trace chains.

### Flow diagram

```
Service A (client)
  client.Call(ctx, ...)
  └─ traceparent header → Service B

Service B (server)
  handler(ctx, req)           ← ctx carries incoming span
  ├─ ApplyContext(ctx, in)    ← forge span becomes child of HTTP span
  └─ FileOptions{Context: ctx} ← file span becomes child of HTTP span
```

### Adapters (already propagate ctx)

| Entry point                                                         | ctx purpose                                           |
| ------------------------------------------------------------------- | ----------------------------------------------------- |
| `rest.Client.Call(ctx, route, req)` / `nethttp.CallWithHandle(ctx, client, baseURL, handle, req, opts)` | Creates child span, sends `traceparent` header |
| `events.SubscribeHandle(ctx, sub, mqttTransport, fn)`               | Parent for subscribe span, passed to `fn(ctx, value)` |
| `events.PublishHandle(ctx, pub, mqttTransport, msg)`                | Creates child span for publish                        |

### Forge (use ApplyContext)

```go
result, err := oeeCalc.ApplyContext(ctx, OEEIn{
    Availability: 0.9,
    Performance:  0.85,
    Quality:      0.95,
})
// forge.apply span is child of HTTP handler span
```

`Apply(in)` is unchanged — uses `context.Background()`. `ApplyContext(ctx, in)` was added to
enable context propagation without breaking existing callers.

### File I/O (set FileOptions.Context)

```go
opts := ports.FileOptions{
    Observer: metrics,
    Context:  ctx,  // file.read span is child of HTTP handler span
}
value, err := configFile.Read(nil, opts)
```

`FileOptions.Context` is optional — when nil, falls back to `context.Background()`.

### Full example: HTTP handler → forge → file

```go
func handler(ctx context.Context, req MyRequest) (MyResponse, error) {
    // ctx already carries the HTTP span

    // Step 1: forge computation as child span
    result, err := oeeCalc.ApplyContext(ctx, OEEIn{
        Availability: req.Availability,
        Performance:  req.Performance,
        Quality:      req.Quality,
    })
    if err != nil {
        return MyResponse{}, err
    }

    // Step 2: write result to file as child span
    err = resultFile.Write(nil, result, ports.FileOptions{
        Observer: obs,
        Context:  ctx,
    })
    if err != nil {
        return MyResponse{}, err
    }

    return MyResponse{OEE: result.OEE}, nil
}
```

## Operation values (TraceObserver)

| Operation          | Call site                                        |
| ------------------ | ------------------------------------------------ |
| `"http.request"`   | nethttp/chi — handler or client call             |
| `"mqtt.subscribe"` | mqtt — `NewSubscribeTransport`'s `Subscribe`     |
| `"mqtt.publish"`   | mqtt — `NewPublishTransport`'s `Publish`         |
| `"forge.apply"`    | forge — `Apply`                                  |
| `"file.read"`      | ports.File — `Read` / `Update`                  |
| `"file.write"`     | ports.File — `Write` / `Patch` / `PatchEncoded` |
| `"mcp.tool"`       | mcpgo — `ToolHandler`                            |
| `"mcp.resource"`   | mcpgo — `ResourceHandler`                        |
| `"mcp.prompt"`     | mcpgo — `PromptHandler`                          |

## Observer location values by adapter

| Location            | Adapter / use case                                              |
| ------------------- | --------------------------------------------------------------- |
| `"body"`            | nethttp/chi — request or response body decode/encode            |
| `"query"`           | nethttp/chi — query parameter validation                        |
| `"cookie"`          | nethttp/chi — request cookie parameter validation               |
| `"header"`          | nethttp/chi — request header parameter validation               |
| `"response_header"` | nethttp/chi — response header parameter validation              |
| `"response_cookie"` | nethttp/chi — response cookie parameter validation              |
| `"path"`            | nethttp/chi — path parameter validation                         |
| `"payload"`         | mqtt — message payload decode (subscribe) or encode (publish)   |
| `"topic_var"`       | mqtt — per-variable codec failure in topic template             |
| `"topic"`           | mqtt — topic-level codec failure                                |
| `"input"`           | mcpgo — tool argument decode/validation                         |
| `"prompt.args"`     | mcpgo — prompt argument codec failure                           |
| `"file"`            | ports.File — per-field codec failure during read/write         |
| any string          | codec-only: choose your own label (`"config"`, `"input"`, etc.) |

## Prometheus example

```go
type PrometheusObserver struct {
    requests   *prometheus.CounterVec   // labels: method, path, status
    subscribed *prometheus.CounterVec   // labels: topic, success
    published  *prometheus.CounterVec   // labels: topic, success
    valErrors  *prometheus.CounterVec   // labels: location, constraint, field
    latency    *prometheus.HistogramVec // labels: method, path
}

func (o *PrometheusObserver) RecordRequest(method, path string, code int, d time.Duration) {
    o.requests.WithLabelValues(method, path, strconv.Itoa(code)).Inc()
    o.latency.WithLabelValues(method, path).Observe(d.Seconds())
}
func (o *PrometheusObserver) RecordSubscribe(topic string, ok bool, _ time.Duration) {
    o.subscribed.WithLabelValues(topic, strconv.FormatBool(ok)).Inc()
}
func (o *PrometheusObserver) RecordPublish(topic string, ok bool, _ time.Duration) {
    o.published.WithLabelValues(topic, strconv.FormatBool(ok)).Inc()
}
func (o *PrometheusObserver) RecordValidationError(loc, constraint, field string) {
    o.valErrors.WithLabelValues(loc, constraint, field).Inc()
}
```

## OpenTelemetry tracing example

A `TraceObserver` implementation wrapping the OpenTelemetry SDK. Wire it alongside
metrics and logging via `stats.NewFanout`:

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/codes"
    "go.opentelemetry.io/otel/trace"
)

type OTelTracer struct{ stats.NoopObserver }

func (t *OTelTracer) StartSpan(ctx context.Context, op, name string) context.Context {
    ctx, span := otel.Tracer("go-codex").Start(ctx, op,
        otel.WithAttributes(attribute.String("name", name)),
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

// Wire alongside metrics and logging:
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger), &OTelTracer{})

// The same obs propagates traces across every layer:
route.WithHandler(handler).WithOptions(nethttp.Options{Observer: obs})
configFile.Read(nil, ports.FileOptions{Observer: obs})
forge.NewRegistry("Pipeline", "1.0.0").WithObserver(obs)
```

How it works:

1. **Server adapters** pass the incoming `*http.Request.Context()` to `StartSpan`. When
   an OTel middleware has extracted a `traceparent` header, the new span is a **child**.
   Without middleware, a **root** span is created.
2. **Client adapters** (`rest.Client.Call`, `events.Client.Publish`) create a child span from the
   user-provided `ctx`. For HTTP, the `traceparent` header propagates via the SDK's
   > **Note**: `LoggingObserver` does **not** implement `TraceObserver` (slog has no tracing
   > built-in). To correlate log output with trace IDs, configure the logging observer's
   > logger with an OTel slog handler:
   >
   > ```go
   > import (
   >     "log/slog"
   >     "go.opentelemetry.io/contrib/slog" // OTel slog handler
   >     "os"
   > )
   >
   > otelHandler := slogotel.NewHandler(slog.NewJSONHandler(os.Stdout), nil)
   > logger := slog.New(otelHandler)
   >
   > obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger), &OTelTracer{})
   > ```
   >
   > Every line emitted by `LoggingObserver` now carries `trace_id` and `span_id`
   > automatically — correlated with the active trace in your observability backend.
   > This is done by OTel SDK's global `TracerProvider` and `TextMapPropagator` being picked up by the handler at runtime.

## Using go-logx as the logger backend

[go-logx](https://github.com/DaniDeer/go-logx) produces a `*slog.Logger` with rotating file output,
buffered writes, and static service/build attrs — a drop-in for `slog.NewTextHandler`:

```go
import "github.com/DaniDeer/go-logx/logx"

logger, cleanup, err := logx.New(logx.Config{
    Console:   true,
    Level:     slog.LevelDebug,
    File:      "/var/log/myapp.log",
    FileLevel: slog.LevelInfo,
    DefaultAttrs: []slog.Attr{
        slog.String("service", "order-api"),
        slog.String("env", "prod"),
    },
    Build: &logx.BuildInfo{Version: version, Commit: commit, Date: date},
})
defer cleanup()

slog.SetDefault(logger)

obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "http")))
```

Every log line carries `service=order-api env=prod build.version=...` automatically. Omitting `defer cleanup()` risks losing buffered lines on exit.

## Domain events vs infrastructure metrics

> This section assumes a stream pipeline (`stream.Tap`, `stream.MapErr`).
> For plain business functions with no pipeline at all, see
> [Business logic without pipelines](#business-logic-without-pipelines) below —
> same observer, same custom-interface pattern, no `stream` dependency.

go-codex makes a deliberate distinction between two kinds of observation:

| | Infrastructure metrics | Domain event observation |
|--|----------------------|------------------------|
| **What** | Request counts, latencies, error rates | Business-significant values (OEE computed, alert triggered, reading saved) |
| **Mechanism** | `stats.Observer` interfaces + `RecordRequest` / `RecordApply` / etc. | `stream.Tap(ctx, src, func(v T) {...})` |
| **Type safety** | Fixed method signatures per interface | Full generic type — `func(r OEEResult)` sees the actual domain type |
| **Concerns** | Infrastructure — transport and computation health | Domain — business rules, thresholds, dashboards |
| **Where it fires** | Adapter layer, forge registry | Stream pipeline, between operators |

### Infrastructure metrics — observer interfaces

These fire automatically at each layer boundary:

```go
obs := stats.NewFanout(
    metricsObserver,                            // RecordRequest, RecordApply, etc.
    stats.NewLoggingObserver(slog.Default()),
)
// Wire once — fires on every request, subscribe, publish, apply
```

### Domain event observation — stream.Tap

`Tap` is for business-level observation of values flowing through the pipeline.
It receives the fully typed, validated domain value — not a status code or duration:

```go
oeeResults = stream.Tap(ctx, oeeResults, func(r OEEResult) {
    // Business logic observation — full type safety
    if r.OEE < 0.75 {
        slog.Warn("OEE below threshold", "oee", r.OEE, "sensor", r.SensorID)
        dashboard.Publish(r)
    }
})
```

`Tap` does not transform the stream — items pass through unchanged.
Use it wherever you want to observe values without mixing with computation.

### Error observation — stream.MapErr + typed errors

Errors from forge functions are typed (`forge.ApplyError`, `forge.InputError`, etc.)
and implement `slog.LogValuer` — they carry structured context automatically.
Use `stream.MapErr` or the `Drain` error callback to observe them:

```go
stream.Drain(ctx, results,
    func(ctx context.Context, r OEEResult) error {
        return publish(ctx, r)
    },
    func(err error) {
        // Every error is typed and slog-compatible:
        var sae stream.StreamApplyError
        if errors.As(err, &sae) {
            // sae.LogValue() returns structured attributes
            slog.Error("OEE computation failed", "error", sae)
            customMetrics.RecordForgeFailure(sae.Function)
        }
    },
    stream.DrainOptions{},
)
```

This keeps the observation concern in the caller (stream pipeline wiring), not in
the forge function body. Forge functions remain pure: they return typed errors; the
pipeline layer decides how to observe them.

```go
// Want to recover and emit a sentinel instead of dropping the item?
results = stream.MapErr(ctx, results, func(err error) (OEEResult, bool, error) {
    var ae forge.ApplyError
    if errors.As(err, &ae) {
        customObs.RecordComputeFailure(ae.Function)   // custom domain observation
        return OEEResult{OEE: 0}, true, nil            // emit zero-OEE sentinel
    }
    return OEEResult{}, false, err                    // re-emit other errors
})
```

---

## Business logic without pipelines

Everything above wires the observer into an adapter, a `forge.Function`, or a
`stream` operator — but plain business/domain functions (no pipeline, no
adapter, just a regular Go function you call directly) can use the exact same
`obs` value with no new API. This section covers that path — the direct
counterpart to "Domain events vs infrastructure metrics" above, which assumes
a stream pipeline.

### Resolve the context observer directly

Any function with a `ctx context.Context` parameter can call
`stats.ObserverFromContext(ctx)` itself — this is not an adapter-only
mechanism, it's the same call every adapter makes internally:

```go
func placeOrder(ctx context.Context, order Order) error {
    obs := stats.ObserverFromContext(ctx) // same lookup adapters use
    // ... business logic ...
}
```

### Wrap the operation in a manual span

Use the `TraceObserver` type-assertion guard — the same idiom adapters use —
directly in your business function:

```go
func placeOrder(ctx context.Context, order Order) (err error) {
    obs := stats.ObserverFromContext(ctx)
    if to, ok := obs.(stats.TraceObserver); ok {
        ctx = to.StartSpan(ctx, "business.op", "placeOrder")
        defer func() { to.EndSpan(ctx, err) }()
    }
    // ... business logic; assign to the named `err` return so EndSpan sees it ...
    return nil
}
```

### Defining your own domain observer interface

None of the nine built-in interfaces (see [Metrics Observer](../features/observer.md#the-nine-observer-interfaces))
are meant to carry business-specific events (`order placed`, `alert
triggered`, `OEE below threshold`) — those are yours to define, following the
same shape as the built-in optional extensions (`SQLObserver`,
`CacheObserver`): a small interface, `RecordXxx` method naming, implemented
optionally, type-asserted at the call site:

```go
// OrderObserver is a domain-specific extension — not part of the stats
// package. Define it next to the business logic that uses it.
type OrderObserver interface {
    RecordOrderPlaced(orderID string, amount float64, d time.Duration)
}

func placeOrder(ctx context.Context, order Order) error {
    start := time.Now()
    obs := stats.ObserverFromContext(ctx)
    // ... business logic ...
    if oo, ok := obs.(OrderObserver); ok {
        oo.RecordOrderPlaced(order.ID, order.Amount, time.Since(start))
    }
    return nil
}
```

Implement `OrderObserver` on your own metrics/logging type exactly like any
other observer:

```go
type OrderMetrics struct{ stats.NoopObserver; placed int }

func (m *OrderMetrics) RecordOrderPlaced(id string, amount float64, d time.Duration) {
    m.placed++
    // e.g. prometheus.CounterVec.With(...).Inc(); histogram.Observe(amount)
}
```

### `stats.NewFanout` does NOT forward custom interfaces

**This is the one gotcha that catches everyone who tries this pattern for
the first time.** `stats.NewFanout(...)` returns a value that implements
exactly the nine built-in interfaces — the fan-out logic for each one is
hardcoded inside the `stats` package. A custom interface like `OrderObserver`
is invisible to it:

```go
orderMetrics := &OrderMetrics{}
obs := stats.NewFanout(orderMetrics, stats.NewLoggingObserver(slog.Default()))

// This ALWAYS fails — NewFanout's return value never implements your
// custom interface, even though orderMetrics (one of the fanned-out
// observers) does:
if oo, ok := obs.(OrderObserver); ok { // ok is always false here
    oo.RecordOrderPlaced(...)
}
```

Two safe patterns:

**(a) Type-assert against the concrete observer directly** — keep a
reference to your domain-observer instance alongside the fanout, and use it
directly for custom events while still passing the fanout everywhere else:

```go
orderMetrics := &OrderMetrics{} // keep this reference
obs := stats.NewFanout(orderMetrics, stats.NewLoggingObserver(slog.Default()))

ctx := stats.WithObserver(context.Background(), obs) // fanout for standard events

// For custom events, use orderMetrics directly — not obs:
orderMetrics.RecordOrderPlaced(order.ID, order.Amount, d)
```

**(b) Roll your own tiny multi-observer for the custom interface** — the
same one-line pattern `NewFanout` uses internally, scoped to your interface:

```go
type orderObservers []OrderObserver

func (os orderObservers) RecordOrderPlaced(id string, amount float64, d time.Duration) {
    for _, o := range os {
        o.RecordOrderPlaced(id, amount, d)
    }
}

var orderObs OrderObserver = orderObservers{orderMetrics, orderLogger}
```

Choose (a) when only one component needs the custom interface; choose (b)
when several domain observers need to fan out together, mirroring how
`stats.NewFanout` itself is built.

See `examples/stats-observer` for a runnable version of this whole section
(`placeOrder`/`OrderObserver`).

---

## See also

- [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) — codec-only `ValidationObserver`; business logic without pipelines (ctx-resolved observer, manual `TraceObserver` span, custom `OrderObserver` interface, `NewFanout` limitation)
- [`examples/adapters-nethttp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) — HTTP metrics via `NewFanout`
- [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — MQTT metrics via `NewFanout`
- [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) — `FileObserver` with `NewFanout`
- [`examples/forge-collection`](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-collection) — forge `PipelineObserver` with `NewFanout`
- [`examples/oee-chain`](https://github.com/DaniDeer/go-codex/tree/main/examples/oee-chain) — `PipelineObserver` across all three layers
- [`examples/http-trace-span-propagation`](https://github.com/DaniDeer/go-codex/tree/main/examples/http-trace-span-propagation) — end-to-end trace propagation: HTTP client → server → forge → file
