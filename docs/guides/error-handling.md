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
| Adapter (HTTP server) | `nethttp` / `chi` route errors | `Options.ErrorHandler`; for pipeline stream errors also `rest.ErrorStatus[...]` |
| Adapter (MQTT/MQTT5/ZeroMQ subscribe/serve) | adapter callback errors | `SubscribeOptions.OnError` / `ServeOptions.OnError` |
| Adapter (MQTT/MQTT5/ZeroMQ call/publish) | returned `error` | `errors.As` into typed `CallError` / `PublishEncodeError` / route param errors |
| Ports boundary | `SourcePort.Stream().Errors`, `SinkPort.Feed(...)` forwarding, bind/connect errors | drain `.Errors` explicitly and unwrap typed errors (`PortBindError`, `PortNoAdapterError`, `PortNoPipelineError`) |
| Pipeline (`stream`) | `gstream.Stream.Errors` | `stream.Drain(..., onErr, ...)`, `MapErr`, `Retry` |

### Quick adapter examples

HTTP (route handler + custom body/status policy):

```go
route = route.WithHandler(fn).WithOptions(nethttp.Options{
    ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
        var conflict domainConflictError
        if errors.As(err, &conflict) {
            status = http.StatusConflict
        }
        w.WriteHeader(status)
    },
})
route.Register(b)
if err := nethttp.AttachMux(b, mux, addr); err != nil {
    log.Fatal(err)
}
_ = b.Serve(ctx) // blocks, owns its own http.Server
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

## Store/IO boundaries (SQL, Cache, File) — `handle`/`log` by default

SQL, Cache (Redis), and File are **internal boundaries with no caller to
respond to** — unlike REST/ReqReply/MCP (respond) or Events/WebSocket
(respond via declared error channel/frame), these adapters default to the
`handle`/`log` half of the shared action model:

- **`handle`** — every sink-side adapter (`sql.DrainInsertAdapter`,
  `redis.SetAdapter`/`DrainSetAdapter`, `file.DrainWriteAdapter`/
  `DrainWriteFileAdapter`) already accepts an `OnError func(error)` callback.
  This callback IS the `handle` action — it fully owns the error, with no
  automatic fallback behavior.
- **`log`** — leaving `OnError` nil is the `log` default: the error is only
  observed via the adapter's `stats.Observer` calls (`RecordValidationError`,
  etc.), never surfaced anywhere else.
- **`respond` via explicit error-output channel** — since these boundaries
  have no channel/topic of their own, "respond" is achieved by *composing*
  the existing `OnError` hook with a declared
  [`events.ErrorChannel`](../features/events.md#error-path-ergonomics-errorchannel)
  from a pub/sub channel you already publish to elsewhere in the
  application — no new adapter API is needed:

```go
// A companion error channel, declared once, reused by any boundary's OnError.
errHandle, _ := events.NewChannel[Order]("orders/create", orderCodec,
    events.ErrorChannel[ValidationError, ErrorPayload](
        "orders/create/errors", errorPayloadCodec,
        func(e ValidationError) (ErrorPayload, error) {
            return ErrorPayload{Code: "validation", Message: e.Error()}, nil
        },
    ),
).Register(b)

sql.DrainInsertAdapter(db, "orders", format.JSON(orderCodec), sql.DrainInsertOptions{
    OnError: func(err error) {
        if resp, matched, mapErr := errHandle.ErrorResponseFor(err); matched && mapErr == nil &&
            resp.Action == events.ErrorRespond {
            _ = mqttClient.Publish(ctx, &paho.Publish{Topic: resp.Topic, Payload: resp.Body})
            return
        }
        slog.Warn("insert failed", "error", err) // handle/log fallback
    },
})
```

The same composition works for `redis.SetAdapter`/`DrainSetAdapter` and
`file.DrainWriteAdapter`/`DrainWriteFileAdapter` `OnError` callbacks — the
declarative pattern lives entirely in `api/events` (or `api/rest` for a
caller-facing REST error response further up the pipeline); the store/IO
adapter only needs its existing `OnError` hook to reach it.

## Examples

- [examples/error-types](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) — every error type demonstrated with `errors.As` and slog
- [examples/decode-errors](https://github.com/DaniDeer/go-codex/tree/main/examples/decode-errors) — multi-field `ValidationErrors` with HTTP 400 response patterns
