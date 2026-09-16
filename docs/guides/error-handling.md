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

## Middleware error paths — REST, events, reqreply side-by-side

This section is about the codec-declared `Transform`/`ClientTransform`
middleware mechanism (`Middleware[In, Out]`) — the ONLY 3 APIs with this
mechanism are `api/rest`, `api/events`, and `api/reqreply` (`api/mcp` and
`adapters/websocket` have declarative `ErrorPattern`/`ErrorFrame` for
route/tool-level errors, but no `Transform`-style middleware chain — see
their own feature pages for their error handling).

### The rule: middleware failure short-circuits, the handler never runs

A `Transform`-attached middleware's dispatch runs at the SAME pre-handler
point security enforcement already runs at, BEFORE the route/channel's own
handler — a middleware failure (either half of the request-decode
direction: `DecodeIn`, or the business-logic `Fn`) means the route/channel
handler is **NEVER CALLED AT ALL**. This holds identically across all 3
APIs and is easy to miss since each API's own feature page states it only
in passing — stated here explicitly as the one cross-cutting rule.

### The 3-error-type shape, unified across all 3 APIs

Every API has EXACTLY 3 structured error types for its middleware chain,
all with the SAME `{Name, Err}` shape and the SAME `Error()`/`Unwrap()`/
`LogValue()` methods — only the package differs (`rest.`/`events.`/
`reqreply.`):

| Type | Fires when | `errors.As` recovers |
|---|---|---|
| `MiddlewareInputError{Name, Err}` | The middleware's `In` struct fails to decode/validate — the server/subscriber DECODING an incoming `In` (all 3 APIs); reqreply's CLIENT ENCODING an outgoing `In` is the SAME struct's other direction, but is NOT yet wrapped in this type today (see "known asymmetry" below) | `Name` — which middleware, by `Declaration.Name` |
| `MiddlewareError{Name, Err}` | The middleware's own `Fn` returns a business error, UNMATCHED by any declared `ErrorPattern`/`ErrorChannel` | `Name` |
| `MiddlewareOutputError{Name, Err}` | The middleware's `Out` struct fails to encode/decode — covers BOTH the server/publisher ENCODING an outgoing `Out` and the reqreply CLIENT DECODING an incoming `Out` from the reply (both directions of the SAME struct) | `Name` |

The `Input`/`Output` naming follows the STRUCT (`In`/`Out`), not the
encode/decode DIRECTION — `MiddlewareOutputError` legitimately covers a
server ENCODE and a client DECODE (both act on `Out`) because BOTH
directions are already wrapped at the core layer. `MiddlewareInputError`'s
sibling client-ENCODE case is architecturally intended to work the SAME
way but isn't wired yet (an open follow-up, not a design decision).
`MiddlewareOutputError` is the newer of the 3 — added for symmetry with
`MiddlewareInputError` (previously an `Out` failure propagated as a bare,
unwrapped error with no recoverable `Name` at all; see
[D-0003's Addendum 2](../design/d-0003-codec-declared-middlewares.md#addendum-2-rest-conflict-detection-alignment--middlewareout-cross-adapter-parity)
for the full history, including a mislabeling bug this fixed in
`reqreply`'s client-side `DecodeOut`, which previously used
`MiddlewareInputError` by mistake).

### Observer location strings mirror the error types exactly

`stats.Observer`'s diagnostic location strings use the SAME 3-way split:
`"middleware:in"` / `"middleware:fn"` / `"middleware:out"` — see the
[Observer guide](observer.md#observer-location-value-reference) for the
full per-adapter table. A middleware failure is ALWAYS reported via
`stats.ReportErrors` before the error reaches the caller, regardless of
which API/adapter.

### Side-by-side: what exactly is returned, per API

| API / direction | `In` decode/encode failure | `Fn` business error | `Out` encode/decode failure |
|---|---|---|---|
| **REST** (server) | `MiddlewareInputError`, HTTP 400, no `ErrorPattern` consulted (no business error exists yet) | `ErrorPattern`-eligible; matched → pattern's own status+body; unmatched → `MiddlewareError`, HTTP 400 | `MiddlewareOutputError`, HTTP 500, no `ErrorPattern` consulted (REST's own encode fault, not the caller's) |
| **events** (subscribe) | `MiddlewareInputError`; `OnError`/`ErrorChannel`-eligible fallback | `ErrorChannel`-eligible; matched → declared response published; unmatched → `MiddlewareError` via `OnError` | N/A — subscribe has no `Out`/reply channel to encode into |
| **events** (publish) | N/A — publish has no incoming `In` to decode | `Fn` error aborts BEFORE publish, returned directly to the caller as `MiddlewareError` | `MiddlewareOutputError`, aborts BEFORE publish, returned directly to the caller |
| **reqreply** (server) | `MiddlewareInputError`; published as an error reply | `ErrorPattern`-eligible; matched → pattern's own reply; unmatched → `MiddlewareError` reply | `MiddlewareOutputError` (building the REPLY's `Out`), published as an error reply |
| **reqreply** (client) | raw, unwrapped codec error (client ENCODING the request's `In`) — NOT yet wrapped, see "known asymmetry" below | `MiddlewareError`, returned from `Call` | `MiddlewareOutputError` (client DECODING the reply's `Out`), returned from `Call` |

For REST specifically, the default JSON error envelope is
`{"error": "<err.Error()>"}` (via `defaultErrorHandler` — override with
`Options.ErrorHandler`) — so a `MiddlewareOutputError` on an unhandled
route now renders as
`{"error": "api/rest: middleware \"policy-name\": invalid output: <cause>"}`,
embedding the failing middleware's name directly in the response body,
recoverable structurally via `errors.As` on the SERVER side (for logging)
even though the CLIENT only sees the rendered string.

### Catching a middleware's business error with a declared pattern

A middleware `Fn`'s own business error is matched against the SAME
declared `ErrorPattern`/`ErrorChannel` a HANDLER error is — declare it
once, it catches errors from EITHER source:

```go
// REST
route := rest.NewRoute[Req, Resp]("POST", "/orders", reqCodec, respCodec,
    rest.ErrorPattern[InsufficientCreditError, ErrorBody](
        http.StatusPaymentRequired, errorBodyCodec, mapFn),
)
route = rest.Transform(route, creditPolicy, func(ctx context.Context, req *Req, in CreditIn) (CreditOut, error) {
    if !hasCredit(in) {
        return CreditOut{}, InsufficientCreditError{Available: in.Balance}
    }
    return CreditOut{Approved: true}, nil
})
// A handler returning the SAME error type matches the SAME pattern.
```

```go
// events
sub := events.NewChannel[Order]("orders/create", orderCodec,
    events.ErrorChannel[InsufficientCreditError, ErrorPayload](
        "orders/create/errors", errorPayloadCodec, mapFn),
).WithSubscribe(events.Subscribe{})
sub = events.Transform(sub, creditPolicy, func(ctx context.Context, msg *Order, in CreditIn) error {
    if !hasCredit(in) {
        return InsufficientCreditError{Available: in.Balance}
    }
    return nil
})
```

```go
// reqreply
route := reqreply.NewRoute[Req, Resp]("orders/create", reqCodec, respCodec,
    reqreply.ErrorPattern[InsufficientCreditError, ErrorPayload](errorPayloadCodec, mapFn),
)
route = reqreply.Transform(route, creditPolicy, func(ctx context.Context, req *Req, in CreditIn) (CreditOut, error) {
    if !hasCredit(in) {
        return CreditOut{}, InsufficientCreditError{Available: in.Balance}
    }
    return CreditOut{Approved: true}, nil
})
```

### Recovering the failing middleware's name from an encode/decode failure

```go
resp, err := nethttp.CallWithHandle(ctx, client, baseURL, handle, req, opts)
var outputErr rest.MiddlewareOutputError
if errors.As(err, &outputErr) {
    // outputErr.Name — which middleware's Out failed to encode/decode
    // outputErr.Err  — the underlying codec/refine error
    logger.Error("middleware output failed", "middleware", outputErr.Name, "error", outputErr.Err)
}
```

The IDENTICAL pattern works for `events.MiddlewareOutputError`/
`reqreply.MiddlewareOutputError` and for `MiddlewareInputError`/
`MiddlewareError` in all 3 packages — same fields, same methods.

### Known related asymmetry (not yet fixed)

The CLIENT-side `EncodeIn` closure (encoding the REQUEST's `In` before
sending, in `ClientTransform`) does NOT currently wrap its failures in
`MiddlewareInputError` in any of the 3 APIs — unlike `DecodeIn` (server
side), which already does. The observer location string is still
correctly reported as `"middleware:in"`, but the error VALUE reaching the
caller is a bare, unwrapped codec error with no recoverable `Name`. This
is the same class of gap `MiddlewareOutputError` fixed for the `Out`
struct, not yet addressed for this one remaining case — tracked as a
candidate follow-up, not yet scheduled.

### Runnable demos

- [examples/error-types](https://github.com/DaniDeer/go-codex/tree/main/examples/error-types) — demonstrates all 3 middleware error types side-by-side (REST) plus an events/reqreply variant
- [Feature: REST API](../features/rest-api.md#codec-backed-middleware-transformclienttransform) · [Feature: Event Channels](../features/events.md#codec-backed-middleware-transformclienttransform) · [Feature: ReqReply Middleware](../features/reqreply-middleware.md) — each API's own full error-type reference table

### Sibling mechanism: declared HANDLER errors, client-side decode

Everything above is about a MIDDLEWARE's own decode/business/encode failure.
A separate, closely-related mechanism exists for a ROUTE/CHANNEL/TOOL
HANDLER's own business error: `rest.ErrorPattern`/`events.ErrorChannel`/
`reqreply.ErrorPattern`/`mcp.ErrorPattern` let you declare "when the
handler's error matches type E, respond with this codec-backed payload"
(see [checklist reference — error-path ergonomics](https://github.com/DaniDeer/go-codex/blob/main/.github/skills/review-go-codex/references/checklist.md)
for the full per-boundary matrix). For REST specifically, this declaration
round-trips all the way to the CLIENT: `nethttp.CallWithHandle`/
`rest.Client.Call` automatically decode a matching response status via
`RouteHandle.DecodeErrorFor` and return a typed, `errors.As`-navigable
`nethttp.ErrorPatternResponse{StatusCode, Value, Body}` — one shared
`Route` declaration, zero client-side boilerplate. See
[Feature: REST API — client-side decode](../features/rest-api.md#client-side-decode--nethttpcallwithhandle-and-errorpatternresponse)
and the [HTTP Client guide](http-client.md#handling-the-response-happy-path-vs-error-path)
for the full workflow, including the important "give each `ErrorPattern`
its own status code" caveat.

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
