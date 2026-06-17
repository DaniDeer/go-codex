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

    f := format.NewFile("/tmp/test.json", format.JSON(codex.String()))
    _, err = f.Read(nil, format.FileOptions{Observer: obs})
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
nethttp.Register(mux, createUser, handler, nethttp.Options{Observer: obs})
```

### MQTT adapter

```go
amqtt.SubscribeHandler(ctx, channel, handler, amqtt.SubscribeOptions{Observer: obs})
amqtt.Publish(ctx, client, channel, qos, retained, msg, vars,
    amqtt.PublishOptions{Observer: obs})
```

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

### FileObserver (format.File)

`format.File[T]` type-asserts the observer in `FileOptions` to `stats.FileObserver`:

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
opts := format.FileOptions{Observer: obs}
cfg, err := configFile.Read(nil, opts)
```

`path` is the concrete path after template substitution, never the template string.

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
  nethttp.Call(ctx, ...)
  └─ traceparent header → Service B

Service B (server)
  handler(ctx, req)           ← ctx carries incoming span
  ├─ ApplyContext(ctx, in)    ← forge span becomes child of HTTP span
  └─ FileOptions{Context: ctx} ← file span becomes child of HTTP span
```

### Adapters (already propagate ctx)

| Entry point                                                         | ctx purpose                                           |
| ------------------------------------------------------------------- | ----------------------------------------------------- |
| `nethttp.Call(ctx, url, handle, req, vars, opts)`                   | Creates child span, sends `traceparent` header        |
| `mqtt.SubscribeHandler(ctx, handle, fn, opts)`                      | Parent for subscribe span, passed to `fn(ctx, value)` |
| `mqtt.Publish(ctx, client, handle, qos, retained, msg, vars, opts)` | Creates child span for publish                        |

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
opts := format.FileOptions{
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
    err = resultFile.Write(nil, result, format.FileOptions{
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
| `"mqtt.subscribe"` | mqtt — `SubscribeHandler`                        |
| `"mqtt.publish"`   | mqtt — `Publish`                                 |
| `"forge.apply"`    | forge — `Apply`                                  |
| `"file.read"`      | format.File — `Read` / `Update`                  |
| `"file.write"`     | format.File — `Write` / `Patch` / `PatchEncoded` |
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
| `"file"`            | format.File — per-field codec failure during read/write         |
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
nethttp.Register(mux, route, handler, nethttp.Options{Observer: obs})
configFile.Read(nil, format.FileOptions{Observer: obs})
forge.NewRegistry("Pipeline", "1.0.0").WithObserver(obs)
```

How it works:

1. **Server adapters** pass the incoming `*http.Request.Context()` to `StartSpan`. When
   an OTel middleware has extracted a `traceparent` header, the new span is a **child**.
   Without middleware, a **root** span is created.
2. **Client adapters** (`nethttp.Call`, `mqtt.Publish`) create a child span from the
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

## See also

- [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) — codec-only `ValidationObserver`
- [`examples/adapters-nethttp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) — HTTP metrics via `NewFanout`
- [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — MQTT metrics via `NewFanout`
- [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) — `FileObserver` with `NewFanout`
- [`examples/forge-collection`](https://github.com/DaniDeer/go-codex/tree/main/examples/forge-collection) — forge `PipelineObserver` with `NewFanout`
- [`examples/oee-chain`](https://github.com/DaniDeer/go-codex/tree/main/examples/oee-chain) — `PipelineObserver` across all three layers
- [`examples/http-trace-span-propagation`](https://github.com/DaniDeer/go-codex/tree/main/examples/http-trace-span-propagation) — end-to-end trace propagation: HTTP client → server → forge → file
