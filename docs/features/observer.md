# Observer Pattern

> See also: [`stats` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats) · [Guide: Using the Observer Pattern](../guides/observer.md) · [http-trace-span-propagation example](https://github.com/DaniDeer/go-codex/tree/main/examples/http-trace-span-propagation)
>
> Runnable demos: [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) · [`examples/adapters-nethttp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) · [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) · [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) · [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — multi-adapter observer across HTTP, MQTT, and SQL in one fanout

The observer pattern is go-codex's unified observability layer across **all layers** of the library: codecs, adapters, formats (files), forge, and SQL. It provides structured hooks for three observability signals — **metrics, logging, and distributed tracing** — with no library dependency.

The user decides which signals to use (any, all, or none) and implements the corresponding interfaces. Implementations are fully swappable between development stubs, Prometheus counters, OTel tracing, or any other backend.

## The seven observer interfaces

| Interface | Signal | Methods | Layer |
|---|---|---|---|
| `stats.ValidationObserver` | metrics + logging | `RecordValidationError(loc, constraint, field)` | **Codecs** — direct codec calls |
| `stats.Observer` | metrics + logging | embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish` | **Adapters** — HTTP, MQTT, MCP |
| `stats.PipelineObserver` | metrics + logging | `RecordApply(name, version, success, dur)` | **Forge** — computation pipelines |
| `stats.FileObserver` | metrics + logging | `RecordFileRead` · `RecordFileWrite` | **Formats** — file I/O |
| `stats.SecurityObserver` | metrics + logging | `RecordSecurityRejection(location, scheme)` | **Adapters** — security rejection events |
| `stats.SQLObserver` | metrics + logging | `RecordValidation(table, op, dur, err)` · `RecordMigration(op, name, version, dur, err)` | **SQL Adapter** — row validation + migrations |
| `stats.TraceObserver` | distributed tracing | `StartSpan(ctx, operation, name) ctx` · `EndSpan(ctx, err)` | **Adapters, forge, file, SQL** — spans |

Every interface is optional. Implement only what you need.

## Built-in types

| Type | Implements | Purpose |
|---|---|---|
| `stats.NoopObserver` | all seven | Zero-cost default — every Option field defaults to this |
| `stats.LoggingObserver` | all six **except** TraceObserver | Logs every event via slog — configure handler for dev/prod/OTel |
| `stats.NewFanout(observers...)` | all seven | Fans out to multiple observers — compose metrics + logging + tracing |

## Composition

Pass a single `stats.NewFanout` value to every layer. No type assertions needed at call sites:

```go
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger), tracer)

// Same value — works on every layer:
stats.ReportErrors(obs, "config", err)          // codec
nethttp.Register(mux, route, handler, nethttp.Options{Observer: obs}) // adapter
configFile.Read(nil, format.FileOptions{Observer: obs})                // format/file
forge.NewRegistry("P", "1.0.0").WithObserver(obs)                      // forge
```

### Context propagation for trace spans

`TraceObserver.StartSpan` returns a `context.Context` that carries the active span. Adapters pass this context to the application handler, enabling parent-child span relationships:

```
HTTP client:    nethttp.Call(ctx, ...)          → traceparent header
Downstream:     handler(ctx, req)               → ctx carries incoming span
  ├─ forge:     ApplyContext(ctx, in)           → child of HTTP span
  └─ file:      FileOptions{Context: ctx}       → child of HTTP span
```

Server adapters (nethttp, chi, mqtt) **always pass the incoming ctx** to `StartSpan`. They do not detect whether a parent span exists — the `TraceObserver` implementation decides:

- When a parent span is present (e.g. extracted from an HTTP `traceparent` header by OTel middleware) → **child span**.
- When no parent is present (`context.Background()`, direct test call) → **root span**.

```go
// OTel — parent decision handled automatically by the SDK:
func (t *OTelTracer) StartSpan(ctx context.Context, op, name string) context.Context {
    ctx, span := otel.Tracer("go-codex").Start(ctx, op, // parent or nil from ctx
        otel.WithAttributes(attribute.String("name", name)),
    )
    return ctx
}
```

The library provides the hook; the user's implementation controls span parenting.

All adapter entry points accept `context.Context`:
- `nethttp.Call(ctx, ...)` — HTTP client, propagates downstream
- `nethttp.Handler` — HTTP server, ctx from `*http.Request.Context()`
- `mqtt.Publish(ctx, ...)` — MQTT publish
- `mqtt.SubscribeHandler(ctx, ...)` — MQTT subscribe, ctx flows to handler

To propagate into forge and file:
- Use **`forge.Function.ApplyContext(ctx, in)`** instead of `Apply(in)`
- Set **`format.FileOptions.Context`** to the handler's context

## Per-layer behavior

| Layer | How the observer is injected | Events emitted |
|---|---|---|
| **Codec** | `stats.ReportErrors(obs, location, err)` | `RecordValidationError` per failing field |
| **Adapter (HTTP/MQTT/MCP)** | `Options{Observer: obs}` | `RecordRequest`/`RecordSubscribe`/`RecordPublish`, validation errors, security rejections, trace spans |
| **Format (file I/O)** | `FileOptions{Observer: obs}` | `RecordFileRead`/`RecordFileWrite`, validation errors, trace spans |
| **Forge** | `Registry.WithObserver(obs)` | `RecordApply` per function call, trace spans |

For per-adapter code examples, OpenTelemetry tracing, Prometheus wiring, location values, and a full end-to-end walk-through, see the [Guide: Using the Observer Pattern](../guides/observer.md).
