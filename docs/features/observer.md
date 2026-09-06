# Observer Pattern

> See also: [`stats` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats) · [Guide: Using the Observer Pattern](../guides/observer.md) · [http-trace-span-propagation example](https://github.com/DaniDeer/go-codex/tree/main/examples/http-trace-span-propagation)
>
> Runnable demos: [`examples/stats-observer`](https://github.com/DaniDeer/go-codex/tree/main/examples/stats-observer) · [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) · [`examples/adapters-mqtt`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt) · [`examples/flat-key-patch`](https://github.com/DaniDeer/go-codex/tree/main/examples/flat-key-patch) · [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — multi-adapter observer across HTTP, MQTT, and SQL in one fanout

The observer pattern is go-codex's unified observability layer across **all layers** of the library: codecs, adapters, formats (files), forge, and SQL. It provides structured hooks for three observability signals — **metrics, logging, and distributed tracing** — with no library dependency.

The user decides which signals to use (any, all, or none) and implements the corresponding interfaces. Implementations are fully swappable between development stubs, Prometheus counters, OTel tracing, or any other backend.

## The nine observer interfaces

| Interface | Signal | Methods | Layer |
|---|---|---|---|
| `stats.ValidationObserver` | metrics + logging | `RecordValidationError(loc, constraint, field)` | **Codecs** — direct codec calls |
| `stats.Observer` | metrics + logging | embeds `ValidationObserver` + `RecordRequest`, `RecordSubscribe`, `RecordPublish` | **Adapters** — HTTP, MQTT, MCP |
| `stats.PipelineObserver` | metrics + logging | `RecordApply(name, version, success, dur)` | **Forge** — computation pipelines |
| `stats.FileObserver` | metrics + logging | `RecordFileRead` · `RecordFileWrite` | **Formats** — file I/O |
| `stats.SecurityObserver` | metrics + logging | `RecordSecurityRejection(location, scheme)` | **Adapters** — security rejection events |
| `stats.SQLObserver` | metrics + logging | `RecordValidation(table, op, dur, err)` · `RecordMigration(op, name, version, dur, err)` | **SQL Adapter** — row validation + migrations |
| `stats.StreamObserver` | metrics + logging | `RecordStreamItem(function, success, dur)` | **Stream** — per-item throughput via `stream.Apply` |
| `stats.CacheObserver` | metrics + logging | `RecordCacheHit(key, dur)` · `RecordCacheMiss(key, dur)` · `RecordCacheWrite(key, op, success, dur)` | **Redis Adapter** — cache lifecycle events |
| `stats.TraceObserver` | distributed tracing | `StartSpan(ctx, operation, name) ctx` · `EndSpan(ctx, err)` | **Adapters, forge, file, SQL, stream** — spans |

Every interface is optional. Implement only what you need.

## Built-in types

| Type | Implements | Purpose |
|---|---|---|
| `stats.NoopObserver` | all nine | Zero-cost default — every Option field defaults to this |
| `stats.LoggingObserver` | all eight **except** TraceObserver | Logs every event via slog — configure handler for dev/prod/OTel |
| `stats.NewFanout(observers...)` | all nine | Fans out to multiple observers — compose metrics + logging + tracing |

## Composition

Pass a single `stats.NewFanout` value to every layer. No type assertions needed at call sites:

```go
obs := stats.NewFanout(metrics, stats.NewLoggingObserver(logger), tracer)

// Same value — works on every layer:
stats.ReportErrors(obs, "config", err)          // codec
route.WithHandler(handler).WithOptions(nethttp.Options{Observer: obs}) // adapter
configFile.Read(nil, ports.FileOptions{Observer: obs})                // format/file
forge.NewRegistry("P", "1.0.0").WithObserver(obs)                      // forge
```

### Context propagation for trace spans

`TraceObserver.StartSpan` returns a `context.Context` that carries the active span. Adapters pass this context to the application handler, enabling parent-child span relationships:

```
HTTP client:    client.Call(ctx, ...)           → traceparent header
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
- `rest.Client.Call(ctx, ...)`/`CallWithHandle(ctx, ...)` — HTTP client, propagates downstream
- `rest.Server.Serve(ctx)`/`nethttp.ServeOne` — HTTP server, ctx from `*http.Request.Context()`
- `mqtt.NewPublishTransport[T](...).Publish(ctx, ...)` (via `events.PublishHandle`) — MQTT publish
- `mqtt.NewSubscribeTransport[T](...).Subscribe(ctx, ...)` (via `events.SubscribeHandle`) — MQTT subscribe, ctx flows to handler

To propagate into forge and file:
- Use **`forge.Function.ApplyContext(ctx, in)`** instead of `Apply(in)`
- Set **`ports.FileOptions.Context`** to the handler's context

## Per-layer behavior

| Layer | How the observer is injected | Events emitted |
|---|---|---|
| **Codec** | `stats.ReportErrors(obs, location, err)` | `RecordValidationError` per failing field |
| **Adapter (HTTP/MQTT/MCP)** | `Options{Observer: obs}` (or ctx default) | `RecordRequest`/`RecordSubscribe`/`RecordPublish`, validation errors, security rejections, trace spans |
| **HTTP client** (`nethttp.Call`, `CallHandle`) | `CallOptions{Observer: obs}` (or ctx default) | `RecordRequest` (status 0 = pre-flight validation failure, no HTTP call sent), validation errors, security rejections, trace spans |
| **Format (file I/O)** | `FileOptions{Observer: obs}` | `RecordFileRead`/`RecordFileWrite`, validation errors, trace spans |
| **Forge** | `Registry.WithObserver(obs)` | `RecordApply` per function call, trace spans |
| **Stream** | `ApplyOptions{Observer: obs}` (or ctx default) | `RecordStreamItem` per item via `stream.Apply`, trace spans |
| **SQL Adapter** | `ValidateOptions{Observer: obs}` (or ctx default) | `RecordValidation`, `RecordMigration`, trace spans |
| **Redis Adapter (cache)** | ctx default (`stats.ObserverFromContext`) | `RecordCacheHit`/`RecordCacheMiss`/`RecordCacheWrite` |

## Default observer via context

Instead of passing `Observer: obs` on every call site, attach an observer once to a
`context.Context` and let every adapter pick it up automatically when no explicit
observer is configured:

```go
obs := stats.NewFanout(metricsObserver, stats.NewLoggingObserver(slog.Default()))
ctx := stats.WithObserver(context.Background(), obs)

// All adapters now use obs when Options.Observer is nil:
events.SubscribeHandle(ctx, sub, mqtt.NewSubscribeTransport[T](client, 1, mqtt.SubscribeOptions{}), fn)
stream.Apply(ctx, s, fn, stream.ApplyOptions{})
route.WithOptions(nethttp.Options{}) // resolved per-request (see below)
```

### API

```go
// WithObserver returns a copy of ctx carrying obs as the default observer.
func stats.WithObserver(ctx context.Context, obs Observer) context.Context

// ObserverFromContext retrieves the stored observer, or returns NoopObserver{} if none.
func stats.ObserverFromContext(ctx context.Context) Observer
```

### Precedence

```
opts.Observer != nil  →  use opts.Observer                (explicit — highest priority)
opts.Observer == nil  →  use stats.ObserverFromContext(ctx) (context default)
                      →  returns NoopObserver{} if nothing stored
```

Explicit always wins. Per-component overrides continue to work:

```go
route.WithOptions(nethttp.Options{Observer: auditObserver}) // explicit, no lookup
```

### How each layer resolves the context observer

| Layer | ctx source | Resolution |
|-------|-----------|------------|
| **HTTP adapters** (`nethttp.Serve`/`ServeOne`/`ServeSSE`, chi mirrors) | `r.Context()` per-request | Resolved inside the request closure — a server middleware can inject per-request observers |
| **HTTP client** (`Client.Call`/`Client.Consume`, `CallWithHandle`, `CallSSEAdapter`) | ctx passed to function | Resolved at call time — SAME mechanism as MQTT/ZeroMQ below; see the callout after this table for what this means for client wrapper packages |
| **SSE stream bridges** (`SSEFromStream`, `SSEFromHub`) | ctx from each SSE connection | Resolved inside the per-connection closure |
| **MQTT adapters** (`Subscribe`, `Publish`) | ctx passed to function | Resolved at call time |
| **ZeroMQ adapters** (`Subscribe`, `Publish`, `Serve`, `Call`) | ctx passed to function | Resolved at call time |
| **MCP adapters** (`ToolHandler`, `ResourceHandler`, `PromptHandler`) | ctx from each tool/resource/prompt call | Resolved inside the per-call closure |
| **Stream bridges** (`stream.Apply`, `stream.FromCodec`, `stream.Drain`) | ctx passed to function | Resolved at call time |
| **`ports.File`** (`Read`, `Write`, `Update`) | `FileOptions.Context` | Resolved from `opts.Context` when non-nil |
| **`forge.Registry`** | not applicable | No context integration — uses explicit `.WithObserver(obs)` builder |
| **`sql.Validate`** | not applicable | No ctx parameter — falls back to `NoopObserver{}` only |

> **Client wrapper packages inherit this for free.** Any package that
> builds its own typed client on top of `rest.Client.Call`/`CallWithHandle`
> internally (a generated API client, a registry client, an SDK, etc.)
> automatically supports `stats.WithObserver(ctx, obs)` with **zero extra
> code**, as long as it doesn't hard-code a non-nil default into its own
> `CallOptions.Observer`. A caller gets full `RecordRequest` metrics for
> every underlying HTTP call the wrapper makes just by attaching an
> observer to the `ctx` it passes through — no bespoke `WithObserver`
> option required on the wrapper's own API (though adding one, mirroring
> `nethttp.CallOptions.Observer`'s own explicit-overrides-context
> precedence, is still a reasonable convenience for a per-call override).
> See [`examples/go-edge-models`](https://github.com/DaniDeer/go-codex/tree/main/examples/go-edge-models)'s
> `docker/registry` package for a concrete example of both: the ctx-based
> path works out of the box, and it ALSO offers an explicit
> `registry.WithObserver(obs)` option for one-off overrides.

### HTTP middleware pattern

For HTTP servers, set the observer per-request via a middleware:

```go
// Server-side middleware — injects obs into every request's context:
func ObserverMiddleware(obs stats.Observer) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ctx := stats.WithObserver(r.Context(), obs)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// Wire:
mux := http.NewServeMux()
route.WithOptions(nethttp.Options{}) // no explicit Observer
http.ListenAndServe(":8080", ObserverMiddleware(obs)(mux))
```

For per-service (not per-request) wiring, pass `nethttp.Options{Observer: obs}` directly — this is simpler and slightly cheaper (no per-request context lookup).

For per-adapter code examples, OpenTelemetry tracing, Prometheus wiring, location values, and a full end-to-end walk-through, see the [Guide: Using the Observer Pattern](../guides/observer.md).
