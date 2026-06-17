# Observer Pattern

> See also: [`stats` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats) · [Guide: Using the Observer Pattern](../guides/observer.md)
>
> Runnable demos: [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) · [`examples/adapters-nethttp`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) · [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) · [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch)

The observer pattern is go-codex's unified observability layer across **all four layers** of the
library: codecs, adapters, formats (files), and forge. It provides structured hooks for three
observability signals — **metrics, logging, and distributed tracing** — with no library dependency.

The user decides which signals to use (any, all, or none) and implements the corresponding
interfaces. Implementations are fully swappable between development stubs, Prometheus counters,
OTel tracing, or any other backend.

## The six observer interfaces

| Interface | Signal | Methods | Layer |
|---|---|---|---|
| `stats.ValidationObserver` | metrics + logging | `RecordValidationError(loc, constraint, field)` | **Codecs** — direct codec calls |
| `stats.Observer` | metrics + logging | embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish` | **Adapters** — HTTP, MQTT, MCP |
| `stats.PipelineObserver` | metrics + logging | `RecordApply(name, version, success, dur)` | **Forge** — computation pipelines |
| `stats.FileObserver` | metrics + logging | `RecordFileRead` · `RecordFileWrite` | **Formats** — file I/O |
| `stats.SecurityObserver` | metrics + logging | `RecordSecurityRejection(location, scheme)` | **Adapters** — security rejection events |
| `stats.TraceObserver` | distributed tracing | `StartSpan(ctx, operation, name) ctx` · `EndSpan(ctx, err)` | **Adapters, forge, file** — spans |

Every interface is optional. Implement only what you need.

## Built-in types

| Type | Implements | Purpose |
|---|---|---|
| `stats.NoopObserver` | all six | Zero-cost default — every Option field defaults to this |
| `stats.LoggingObserver` | all five **except** TraceObserver | Logs every event via slog — configure handler for dev/prod/OTel |
| `stats.NewFanout(observers...)` | all six | Fans out to multiple observers — compose metrics + logging + tracing |

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

## Per-layer behavior

| Layer | How the observer is injected | Events emitted |
|---|---|---|
| **Codec** | `stats.ReportErrors(obs, location, err)` | `RecordValidationError` per failing field |
| **Adapter (HTTP/MQTT/MCP)** | `Options{Observer: obs}` | `RecordRequest`/`RecordSubscribe`/`RecordPublish`, validation errors, security rejections, trace spans |
| **Format (file I/O)** | `FileOptions{Observer: obs}` | `RecordFileRead`/`RecordFileWrite`, validation errors, trace spans |
| **Forge** | `Registry.WithObserver(obs)` | `RecordApply` per function call, trace spans |

For per-adapter code examples, OpenTelemetry tracing, Prometheus wiring, location values, and a
full end-to-end walk-through, see the [Guide: Using the Observer Pattern](../guides/observer.md).
