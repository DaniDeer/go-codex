# Guide: Error Handling

For the full reference of all error types, `errors.As` patterns, and slog integration, see the feature page.

**Feature:** [Error Handling](../features/error-handling.md)

> **Recommended: declare it, don't dispatch it.** For a route/channel
> HANDLER's own business error (as opposed to go-codex's own internal
> typed errors covered below), the RECOMMENDED mechanism is the
> declarative `rest.ErrorPattern` / `events.ErrorChannel` /
> `reqreply.ErrorPattern` trio — see
> ["Sibling mechanism: declared HANDLER errors"](#sibling-mechanism-declared-handler-errors-client-side-decode)
> further down this guide. It replaces hand-rolled `errors.As` dispatch
> inside `Options.ErrorHandler`/`OnError` callbacks (still shown below as
> the lower-level escape hatch) with ONE declaration that drives the
> typed server response/publish AND the typed client-side recovery
> simultaneously, self-documents in the OpenAPI/AsyncAPI spec, and reports
> observability for free. See the "Runnable demos" list below for a
> complete, guided tour across all 3 mechanisms in each API.

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
- [Feature: REST API](../features/rest-api.md#codec-backed-middleware-transformclienttransform) · [Feature: Event Channels](../features/events.md#codec-backed-middleware-transformclienttransform) · [Feature: Codec-Declared Middleware](../features/codec-declared-middleware.md) — the cross-API mechanism reference, including `api/reqreply`

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
its own status code" caveat (now enforced at `Register` time, not just
recommended).

### `ErrorPattern`/`ErrorChannel` covers EVERY dispatch failure, not just the handler

A declared `ErrorPattern`/`ErrorChannel` is eligible at every point in a
route/channel's dispatch where a typed error could occur — not just the
handler's own return. Body/payload decode, path/query/cookie/header param
validation, middleware `DecodeIn`/`EncodeOut`, security middleware, and
response/merge-field encode failures are ALL `ErrorPattern`/`ErrorChannel`-
eligible, exactly like a handler error — the declarative model doesn't
care WHICH dispatch step produced the error, only which TYPE it is.

### Security-related patterns: prefer Mapped mode, never reuse an internal error type wholesale

Now that security middleware Fn failures are `ErrorPattern`/`ErrorChannel`-
eligible (the row directly above), it becomes easy to declare one for a
security-related error type — but doing so carelessly can leak more than
intended. **Direct mode** (no `mapFn`, `E` and `B` the SAME type) serializes
the underlying error's ENTIRE STRUCTURED VALUE via its own codec — every
field, including any the type happens to carry for internal/logging
purposes only. For an ORDINARY business error this is the whole point
(richer data reaching the caller); for a SECURITY error specifically it is
often a footgun:

- Security error messages are frequently deliberately vague on purpose —
  e.g. never distinguishing "user not found" from "wrong password," to
  prevent account-enumeration attacks.
- An internal security error type may carry EXTRA fields never meant for
  external disclosure (a wrapped credential-store error, internal
  validation context, a stack trace, etc.).

**Recommendation: declare security-related `ErrorPattern`/`ErrorChannel`
values in Mapped mode**, with an explicit `mapFn` that DELIBERATELY
constructs a minimal, safe payload:

```go
// Prefer this — Mapped mode, minimal deliberate payload:
rest.ErrorPattern[internalSecurityError, PublicErrorBody](401, publicErrorCodec,
    func(e internalSecurityError) (PublicErrorBody, error) {
        return PublicErrorBody{Code: "unauthorized"}, nil // no internal detail leaks
    },
)

// Avoid this — Direct mode reusing an internal type wholesale:
rest.ErrorPattern[internalSecurityError, internalSecurityError](401, internalSecurityCodec)
```

This is guidance, not a new mechanism or restriction — `ErrorPattern`/
`ErrorChannel` themselves gain no new constraint; the risk is entirely in
HOW a caller chooses to declare the pattern, identical in kind to any
other codec-declared struct's information-disclosure surface. It is worth
flagging explicitly for security-related errors specifically, since they
are so routinely under-specified elsewhere in API design for good reason.

**`DeadLetter` has no equivalent redaction escape hatch — treat its
destination as an ops-only, access-controlled topic.** Unlike
`ErrorPattern`/`ErrorChannel`'s Mapped mode above, `DeadLetterEnvelope.
Error` ALWAYS calls `err.Error()` verbatim, with no mapping/redaction
option at all — by the time a failure reaches `DeadLetter` (either
because it's a decode-class failure with no business type yet, or
because it matched no more specific declared pattern), there is no
typed value left to selectively redact. Since `DeadLetter` fires for
Tier-2/handler-class failures too (including an unmatched security
middleware Fn rejection), a security-related error's full `.Error()`
string can flow into the dead-letter envelope and reach anyone
subscribed to that destination topic. Two mitigations:

- **Declare a more specific `ErrorChannel`/`ErrorPattern` in Mapped
  mode for security-related error types** — a MATCHED pattern always
  wins over `DeadLetter` (strict fallback-tier ordering), so intercepting
  the error earlier with a deliberately minimal payload prevents it from
  ever reaching the dead-letter destination at all.
- **Treat the `DeadLetter` destination itself as an ops-only topic**,
  access-controlled the same way you would any internal diagnostics
  channel — not a general-purpose broadcast a wide audience can
  subscribe to.

### Observing declared error patterns

`stats.ErrorPatternObserver` (`RecordErrorPatternMatch(location, code,
action string)` / `RecordErrorPatternMiss(location string)`) is an
optional `stats.Observer` extension that turns a declared `ErrorPattern`/
`ErrorChannel` catalogue into a live observability signal — hit-rate per
error type, and (via `RecordErrorPatternMiss`) a coverage signal for "this
location keeps failing in a way nothing declared here anticipated."
`stats.SpanTagger` (`TagSpan(ctx, key, value string)`) is a separate,
optional extension for tagging the active trace span with which pattern
fired.

You never call either directly — `RouteHandle.ObserveErrorResponseFor(ctx,
obs, err)` (REST) / `ChannelHandle.ObserveErrorResponseFor(ctx, obs, err)`
(events) is the RECOMMENDED single call site: it performs the same
`errors.As` match as `ErrorResponseFor`, but ALSO reports match/miss/
span-tag observability internally — a handler/middleware author who
declares an `ErrorPattern`/`ErrorChannel` and simply returns the domain
error gets full observability for free, with zero instrumentation code of
their own.

### Dead-letter fallback: when nothing else claimed the failure

`events.DeadLetter(topic, opts...)` / `reqreply.DeadLetter(topic, opts...)`
declare an OPTIONAL, LAST-RESORT sink for a channel/route's own dispatch
failures — attempted immediately after `ErrorChannel`/`ErrorPattern` fails
to match (or none is declared at all). Unlike `ErrorChannel`/`ErrorPattern`
(a codec-backed, TYPED response), a dead-letter's payload is always the
SAME fixed envelope, because by the time nothing else has claimed the
failure there is no reliable business type left to encode:

```go
type DeadLetterEnvelope struct {
    SourceTopic string    // the original channel/route's topic
    Payload     []byte    // the original, undecoded message bytes
    Error       string     // err.Error() — a typed value doesn't exist here
    Timestamp   time.Time
}
```

Declare it once per channel/route, or set an application-wide default via
`Client.AddGlobalDeadLetter`/`Server.AddGlobalDeadLetter` — a channel/route
that declares NOTHING inherits the global default; declaring
`DeadLetter("")` (empty topic) explicitly opts out, mirroring
`AddGlobalSecurity`'s own nil-inherit/empty-override convention exactly:

```go
b := events.NewClient(events.WithInfo(events.Info{Title: "Sensors", Version: "1.0.0"}))
b.AddGlobalDeadLetter("dlq/sensors") // every channel inherits this unless it overrides

handle, _ := events.NewChannel[Reading]("sensors/readings", readingCodec,
    events.DeadLetter("sensors/readings/dlq"), // channel-level override wins
).WithSubscribe(events.Subscribe{}).Handle(b)
```

A dead-letter is attempted at every Category-A dispatch failure point
(decode, topic/property-var merge, User Property param validation
(mqtt5), security middleware `Fn`, `Transform` middleware
`DecodeIn`/`Fn`/`EncodeOut`, handler error) — on BOTH the subscribe/serve
side AND a FAILED publish/reply (the message never reached the broker,
or the reply's own encode failed) — across `adapters/mqtt`,
`adapters/mqtt5`, and `adapters/zeromq`.

**Reachability differs by transport.** MQTT (v3 and 5) has one shared
client used for every topic, so a dead-letter topic is always reachable
via the SAME `client.Publish` the channel itself uses — no extra wiring
needed. ZeroMQ's REQ/REP reqreply transport is point-to-point (one socket
per route, no broker to address an arbitrary topic through) — the
declared dead-letter topic MUST have its OWN entry in the `sockets` map
passed to `zeromq.AttachServer`/`AttachRouterServer` (typically a PUSH
socket feeding a dead-letter consumer). When no such entry exists, the
dead-letter is silently skipped — it is NEVER sent back over the route's
own REP/ROUTER socket, since an extra, unsolicited message there would
violate REQ/REP's strict one-reply-per-request protocol invariant.
ZeroMQ's pub/sub `Publish`/`Subscribe` has no such restriction (a SUB
socket can receive on any topic its filter matches), so its dead-letter
wiring works the same as MQTT's.

```go
if err := zeromq.AttachServer(server, map[string]zeromq.FramedSocket{
    "compute/add":     repSock,
    "compute/add/dlq": dlqPushSock, // required for the dead-letter to be reachable
}); err != nil {
    log.Fatal(err)
}
```

### Runnable demos: the full ErrorPattern/ErrorChannel/DeadLetter mechanism

Each mini-project example has a single, consolidated `demo_error_pattern.go`
covering EVERY facet of its API's mechanism end-to-end — declaration
modes (Direct/Mapped, plus REST's `ErrorStatus`), the 3 `ErrorAction`
values (`Respond`/`Handle`/`Log`, where applicable), all 3 client-side
recovery mechanisms (`ErrorPatternAs[B]`, `HandleErrorPattern`+`Case`, and
the declaration value's own `.Match` method), a security-middleware
combination (proving the pattern intercepts a middleware Fn failure, not
just a handler failure), and — for events/reqreply — the `DeadLetter`
two-tier fallback:

- [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) — `demo_error_pattern.go`: `ErrorStatus` vs `ErrorPattern` Direct vs Mapped, the 3 `ErrorAction`s, all 3 client-match mechanisms, security-middleware combo, and the port/stream-adapter (`nethttp.IngestAdapter`) dispatch proof. `demo_login.go` shows a REAL primary-flow `ErrorPattern` (invalid-credentials → typed 401).
- [examples/events-api](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api) — `demo_error_pattern.go`: `ErrorChannel` Direct vs Mapped AND the 3 `ErrorAction`s, EACH demoed on BOTH the publish side (upstream pipeline error) AND the subscribe side (handler error), a downstream consumer decoding the typed error-output topic as an ordinary channel, the `DeadLetter` two-tier fallback (matched vs genuinely-unmatched, side-by-side, subscribe side), and a `SubscribeMW` security combo (subscribe side only, by design — a publish-side security-Fn rejection is a pre-transmission Category-C validation failure, permanently out of Category-A scope, same as REST's client-side credential validation).
- [examples/reqreply-api](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) — `demo_error_pattern.go`: `ErrorPattern` Direct vs Mapped (reqreply has NO `ErrorAction` — a match always replies), all 3 client-match mechanisms, `DeadLetter` (fires ALONGSIDE the reply, never instead of it — reqreply always owes the caller a response), a security-middleware combo, and the same mechanism bound through `ports.ToolPort` + `mqtt5.ServeAdapter` instead of direct `AttachServer`.

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
- [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api)/[events-api](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api)/[reqreply-api](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api)'s `demo_error_pattern.go` — the full declarative `ErrorPattern`/`ErrorChannel`/`DeadLetter` mechanism per API, see ["Runnable demos"](#runnable-demos-the-full-errorpatternerrorchanneldeadletter-mechanism) above
