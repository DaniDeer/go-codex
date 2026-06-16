# Metrics Observer

> See also: [`stats` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats)
>
> Runnable demos: [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) · [`examples/adapters-nethttp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) · [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt)

go-codex's observer pattern provides structured metrics hooks without any metrics library dependency. Implement the interface you need; swap in Prometheus or OpenTelemetry in production.

## Observer interfaces

| Interface | Methods | Used by |
|---|---|---|
| `stats.ValidationObserver` | `RecordValidationError(location, constraint, field string)` | Codecs directly (config validation, no adapter) |
| `stats.Observer` | embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish` | HTTP and MQTT adapters |
| `stats.PipelineObserver` | `RecordApply(name, version string, success bool, d time.Duration)` | `forge.Registry` |
| `stats.SecurityObserver` | `RecordSecurityRejection(location, scheme string)` | adapters — type-asserted, never embedded |
| `stats.FileObserver` | `RecordFileRead(path string, success bool, d time.Duration)` · `RecordFileWrite(path string, success bool, d time.Duration)` | `format.File[T]` — type-asserted, never embedded |

`stats.NoopObserver` satisfies all five interfaces at zero cost.

## Composing metrics and logging

**Keep metrics and logging separate.** The observer's job is counting events (swap for Prometheus); logging is a separate concern (configure via slog handler).

`stats.NewLoggingObserver` implements all five observer interfaces and logs every event via slog. `stats.NewFanout` fans out calls to multiple observers:

```go
// metrics: pure counters — swap for Prometheus.CounterVec in production
metrics := &MyMetricsObserver{}

// logging: slog — configure handler for dev/prod/OTel
logger := slog.Default().With("component", "api")

obs := stats.NewFanout(
    metrics,
    stats.NewLoggingObserver(logger),
)

// Both receive every call — metrics counts, logger emits structured events
nethttp.Register(mux, route, handler, nethttp.Options{Observer: obs})
```

`NewFanout` also fans out the optional [FileObserver], [SecurityObserver], and [PipelineObserver] interfaces — delegating to each inner observer that implements them:

```go
// LoggingObserver implements FileObserver — no extra wiring needed
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger))

configFile := format.NewFile("config.json", format.JSON(codec))
configFile.Read(nil, format.FileOptions{Observer: obs})
// → metrics.RecordFileRead called
// → logger emits: level=DEBUG msg="file read" path=config.json success=true
```

## Codec-level (ValidationObserver)

Use when calling codecs directly without any adapter:

```go
// Pure counter — no slog calls
type ConfigMetrics struct{ errors int }
func (o *ConfigMetrics) RecordValidationError(_, _, _ string) { o.errors++ }

// Combine with library logging observer
metrics := &ConfigMetrics{}
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(slog.Default()))

val, err := appConfigCodec.Decode(rawData)
stats.ReportErrors(obs, "config", err)
// → metrics.errors incremented
// → slog emits: level=WARN msg="codec validation error" location=config ...
```

`stats.ConstraintName(err)` extracts a stable label from any field-level error: `ConstraintError.Name`, `"type-mismatch"`, `"required"`, or `""`.

## HTTP adapter (Observer)

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
    fmt.Printf("  [observer] validation error — location=%q constraint=%q field=%q\n",
        location, constraintName, field)
}

func (o *CountingObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (o *CountingObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

var _ stats.Observer = (*CountingObserver)(nil)

// Wire to HTTP adapter:
nethttp.Register(mux, createUser, handler, nethttp.Options{Observer: obs})

// Wire to HTTP client:
nethttp.Call(ctx, client, serverURL, handle, req, nil, nethttp.CallOptions{Observer: obs})
// Note: status 0 = pre-flight validation failure (no HTTP call sent)
```

## MQTT adapter

```go
amqtt.SubscribeHandler(ctx, channel, handler, amqtt.SubscribeOptions{Observer: obs})

amqtt.Publish(ctx, client, channel, qos, retained, msg, vars,
    amqtt.PublishOptions{Observer: obs})
```

## Forge pipeline (PipelineObserver)

```go
type PipelineLogger struct{}

func (PipelineLogger) RecordApply(name, version string, success bool, d time.Duration) {
    log.Printf("[forge] %s@%s ok=%v dur=%v", name, version, success, d)
}

registry := forge.NewRegistry("OEE Pipeline", "1.0.0").
    WithObserver(PipelineLogger{})
```

## FileObserver (format.File)

`format.File[T]` type-asserts the observer in `FileOptions` to `stats.FileObserver`. Implement it alongside your existing `Observer`:

```go
type TelemetryObserver struct {
    CountingObserver // embed for Observer methods
}

func (o *TelemetryObserver) RecordSecurityRejection(location, scheme string) {
    // security rejections
}

func (o *TelemetryObserver) RecordFileRead(path string, ok bool, d time.Duration) {
    slog.Info("file read", "path", path, "ok", ok, "dur", d)
}

func (o *TelemetryObserver) RecordFileWrite(path string, ok bool, d time.Duration) {
    slog.Info("file write", "path", path, "ok", ok, "dur", d)
}

var _ stats.FileObserver = (*TelemetryObserver)(nil)

// Wire to File:
opts := format.FileOptions{Observer: obs}
cfg, err := configFile.Read(nil, opts)
```

`path` in the callback is the concrete path after template substitution, never the template string.

## SecurityObserver

```go
type TelemetryObserver struct {
    CountingObserver // embed for Observer methods
}

func (o *TelemetryObserver) RecordSecurityRejection(location, scheme string) {
    // increment a Prometheus counter, emit a log line, etc.
}
```

Adapters type-assert `stats.SecurityObserver` — implementing it is purely additive; existing `Observer` implementations need not change.

## Observer location values

| Location | Adapter / use case |
|---|---|
| `"body"` | nethttp/chi — request or response body decode/encode |
| `"query"` | nethttp/chi — query parameter validation |
| `"cookie"` | nethttp/chi — request cookie parameter validation |
| `"header"` | nethttp/chi — request header parameter validation |
| `"response_header"` | nethttp/chi — response header parameter validation |
| `"response_cookie"` | nethttp/chi — response cookie parameter validation |
| `"path"` | nethttp/chi — path parameter validation |
| `"payload"` | mqtt — message payload decode (subscribe) or encode (publish) |
| `"topic_var"` | mqtt — per-variable codec failure in topic template |
| `"topic"` | mqtt — topic-level codec failure |
| `"input"` | mcpgo — tool argument decode/validation |
| `"prompt.args"` | mcpgo — prompt argument codec failure |
| `"file"` | format.File — per-field codec failure during read/write |
| any string | codec-only: choose a label meaningful to your domain (`"config"`, `"input"`, etc.) |

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

## Using go-logx as the logger backend

[go-logx](https://github.com/DaniDeer/go-logx) is a companion library that produces a `*slog.Logger`
with rotating file output, buffered writes, and static service/build attrs. Because `stats.NewLoggingObserver`
takes `*slog.Logger`, go-logx is a drop-in replacement for `slog.NewTextHandler` — no go-codex changes needed.

```go
import "github.com/DaniDeer/go-logx/logx"

logger, cleanup, err := logx.New(logx.Config{
    Console:   true,
    Level:     slog.LevelDebug,
    File:      "/var/log/myapp.log", // optional; omit for console-only
    FileLevel: slog.LevelInfo,       // file captures Info+; console captures Debug+
    DefaultAttrs: []slog.Attr{
        slog.String("service", "order-api"),
        slog.String("env", "prod"),
    },
    Build: &logx.BuildInfo{Version: version, Commit: commit, Date: date},
})
if err != nil {
    log.Fatal(err)
}
defer cleanup() // flushes the 8 KB file buffer — must not be omitted

slog.SetDefault(logger)

metrics := &MyMetricsObserver{}
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger.With("component", "http")))
```

Every log line emitted by `LoggingObserver` will carry `service=order-api env=prod build.version=...`
automatically via `DefaultAttrs`. The `cleanup()` function flushes the internal 8 KB `bufio.Writer`
and closes the rotating file — omitting `defer cleanup()` risks losing buffered lines on exit.

## See also

- [examples/stats-observer](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) — codec-only observer: `NewFanout(metrics, NewLoggingObserver)`
- [examples/adapters-nethttp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) — HTTP request metrics + logging via `NewFanout`
- [examples/adapters-mqtt](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) — MQTT subscribe + publish metrics + logging via `NewFanout`
- [examples/flat-key-patch](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) — FileObserver with `NewFanout`: metrics + logging fully separated
