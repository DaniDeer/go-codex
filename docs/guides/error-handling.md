# Guide: Error Handling

For the full reference of all error types, `errors.As` patterns, and slog integration, see the feature page.

**Feature:** [Error Handling](../features/error-handling.md)

## Key pattern: errors.As + named slog.Logger

```go
logger := slog.Default().With("transport", "http-client")

var pathErr rest.PathParamError
if errors.As(err, &pathErr) {
    logger.Warn("param rejected (no request sent)",
        "param", pathErr.Name,
        "value", pathErr.Value,
        "cause", pathErr.Err,
    )
}
```

Every error type implements `slog.LogValuer` — pass them directly to `slog.Any(...)` for structured key-value output.

## Where to handle errors (adapters, ports, pipelines)

Use this as the consistent decision map:

| Layer | Primary error surface | Main escape hatch |
|---|---|---|
| Adapter (HTTP server) | `nethttp` / `chi` route errors | `Options.ErrorHandler`; for pipeline stream errors also `rest.PipelineErrorStatus[...]` |
| Adapter (MQTT/MQTT5/ZeroMQ subscribe/serve) | adapter callback errors | `SubscribeOptions.OnError` / `ServeOptions.OnError` |
| Adapter (MQTT/MQTT5/ZeroMQ call/publish) | returned `error` | `errors.As` into typed `CallError` / `PublishEncodeError` / route param errors |
| Ports boundary | `SourcePort.Stream().Errors`, `SinkPort.Feed(...)` forwarding, bind/connect errors | drain `.Errors` explicitly and unwrap typed errors (`PortBindError`, `PortNoAdapterError`, `PortNoPipelineError`) |
| Pipeline (`stream`) | `gstream.Stream.Errors` | `stream.Drain(..., onErr, ...)`, `MapErr`, `Retry` |

### Quick adapter examples

HTTP (route handler + custom body/status policy):

```go
nethttp.Register(mux, route, fn, nethttp.Options{
    ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
        var conflict domainConflictError
        if errors.As(err, &conflict) {
            status = http.StatusConflict
        }
        w.WriteHeader(status)
    },
})
```

MQTT5 subscribe/serve callback:

```go
mqtt5adapter.Subscribe(ctx, client, router, handle, 1, fn, mqtt5adapter.SubscribeOptions{
    OnError: func(e mqtt5adapter.SubscribeError) {
        var propErr mqtt5adapter.UserPropertyError
        if errors.As(e, &propErr) {
            slog.Warn("bad user property", "error", e)
        }
    },
})
```

Pipeline stream drain:

```go
stream.Drain(ctx, out, publishFn, func(err error) {
    var applyErr stream.StreamApplyError
    if errors.As(err, &applyErr) {
        slog.Warn("apply failed", "error", applyErr)
    }
}, stream.DrainOptions{})
```

See also:
- [Ports guide](ports.md#error-surfaces-and-escape-hatches)
- [HTTP server guide](http-server.md#pipeline-handlers-mapping-stream-errors-to-http-status)
- [MQTT 5 guide](mqtt5.md#error-handling)
- [ZeroMQ guide](zeromq.md#error-handling)
- [Stream guide](stream.md#error-handling-patterns)

## Examples

- [examples/error-types](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) — every error type demonstrated with `errors.As` and slog
- [examples/decode-errors](https://github.com/DaniDeer/go-codex/tree/main/examples/decode-errors) — multi-field `ValidationErrors` with HTTP 400 response patterns
