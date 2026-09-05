# SSE & Streaming

> See also: [`api/rest` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`adapters/templ` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/templ)
>
> Runnable demos: [`examples/adapters-sse`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-sse) · [`examples/adapters-streaming-sse-templ`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-streaming-sse-templ)

## Server-Sent Events (SSE)

`rest.NewSSERoute[Req, Event]` registers a typed SSE route — always GET — with an event codec that validates every value before it is serialised to `data: ...\n\n`. SSE lives in `api/rest` (not `api/events`) because its wire protocol is plain HTTP — see [API Contracts: "Why SSE lives in `api/rest`, not `api/events`"](../concepts/api-contracts.md#why-sse-lives-in-apirest-not-apievents) for the full rationale.

```go
import (
    nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
    "github.com/DaniDeer/go-codex/api/rest"
    "github.com/DaniDeer/go-codex/codex"
    "github.com/DaniDeer/go-codex/validate"
)

sensorIDCodec := codex.String().Refine(validate.NonEmptyString)

// Declare the SSE route as a value, handler attached inline.
sensorRoute := rest.NewSSERoute[struct{}, SensorReading](
    "/sensors/{id}/readings",
    codex.Empty, sensorReadingCodec,
    rest.RouteMeta{OperationID: "streamSensor"},
    rest.PathParam{Name: "id", Description: "Sensor ID"}.WithCodec(sensorIDCodec),
).WithHandler(func(ctx context.Context, _ struct{}, send func(SensorReading) error) error {
        r, _ := nethttp.RequestFromContext(ctx)
        sensorID := r.PathValue("id")
        for {
            select {
            case <-ctx.Done():
                return nil  // client disconnected — context cancelled
            default:
            }
            reading := svc.Read(sensorID)
            if err := send(reading); err != nil {
                return err  // codec rejected the value — nothing written to stream
            }
            time.Sleep(time.Second)
        }
}).WithOptions(nethttp.Options{Observer: obs})
sensorRoute.Register(b)

// Wire onto net/http (AttachMux wires plain AND SSE routes together).
if err := nethttp.AttachMux(b, mux, addr); err != nil {
    log.Fatal(err)
}
_ = b.Serve(ctx) // blocks, owns its own http.Server
```

**Key properties:**
- `send(event)` validates via the event codec, encodes to JSON, writes `data: <json>\n\n`, and flushes. If the codec rejects the event, `send` returns an error **without writing anything** — the stream remains clean.
- `ctx.Done()` signals client disconnects; handlers should return `nil` on context cancellation.
- `sensorRoute.BuildPath(vars)` validates path variables before assembling the URL — same contract as `RouteHandle.BuildPath`.
- The route appears in the OpenAPI spec as `GET /sensors/{id}/readings` with `Content-Type: text/event-stream`.
- Works identically with `chiadapter.ServeSSE`; use `chi.URLParam(r, "id")` for path vars.
- The stats observer receives `RecordValidationError("response", constraint, "event")` for each rejected event — use this to count codec validation failures per event type.
- The stats observer receives `RecordValidationError("response", constraint, "event")` for each rejected event.

## One struct, one call for SSE events

SSE now supports the same declare-once merge pattern as REST requests and
responses. Declare connection-level vars once with
`rest.NewRequiredSSEEventParam` / `rest.NewOptionalSSEEventParam`; each
`send(event)` call then auto-merges request path/query/header/cookie values
into the event struct before encode:

```go
type Event struct {
    MachineID string
    Tenant    string
    Payload   Reading
}

route := rest.NewSSERoute[struct{}, Event](
    "/machines/{machineID}/events",
    codex.Empty, eventCodec,
    rest.NewRequiredSSEEventParam("machineID", codex.String(),
        func(e Event) string { return e.MachineID },
        func(e *Event, v string) { e.MachineID = v }),
    rest.NewOptionalSSEEventParam("tenant", codex.String(),
        func(e Event) string { return e.Tenant },
        func(e *Event, v string) { e.Tenant = v }),
).WithHandler(streamFn)
route.Register(b)
if err := nethttp.AttachMux(b, mux, addr); err != nil {
    log.Fatal(err)
}
_ = b.Serve(ctx) // blocks, owns its own http.Server

// send(Event{Payload: ...}) -> machineID/tenant merged automatically.
```

Escape hatch stays unchanged: skip `NewRequiredSSEEventParam` and set the
field manually from request context before `send`. The runnable
`examples/adapters-sse` now shows both paths side-by-side.

## SSE client consumption

`rest.Client.Consume` is the CLIENT-side counterpart to `Client.Call` —
the SAME declared `rest.SSERoute` used to serve events (above) is passed
directly to `Consume` to consume them back, no separate client-side
declaration needed. `Client.Consume` is `ClientTransport`-based (via
`nethttp.Attach`), mirroring `Client.Call` exactly and fully-featured:
path/query/header/cookie param derivation, security/credential `ClientMW`,
per-call format overrides, and general-purpose `ClientMW` wrapping are ALL
supported (see `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s
addendum on `Client.Call`/`Client.Consume` full `ClientTransport` parity):

```go
import (
    "github.com/DaniDeer/go-codex/api/rest"
    nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
)

client := rest.NewClient()
_ = nethttp.Attach(client, httpClient, "https://api.example.com")

err := client.Consume(ctx, sensorRoute, struct{}{},
    func(ctx context.Context, reading SensorReading) error {
        return handleReading(reading)
    })
```

**Key properties:**
- `Consume` BLOCKS until `ctx` is cancelled or a fatal setup error occurs (e.g. a merge-field encoding failure) — mirrors `zeromq.Subscribe`'s blocking shape; run it via `go client.Consume(...)` for a background consumer.
- Path/query/header/cookie values are derived from the `Req` struct via the route's declared merge fields (`rest.NewRequiredPathParam[T]`/etc.) — same "one struct in" mechanics as `Call`. A route with no merge-capable params for a value it needs cannot supply that value from `Req` — declare merge fields the same way you would for a regular `rest.Route`.
- Connection drops trigger automatic reconnect with exponential backoff (250ms initial step, doubling, capped at 30s) — mirrors `adapters/websocket`'s reconnect loop. The merge-field vars AND any `rest.SSERoute.ClientMW`-declared credential are RE-DERIVED on every reconnect attempt, never cached.
- `fn`'s returned error is silently dropped (non-fatal) — `Client.Consume` has no `OnError`/`OnCredentialRejected` hook; a caller needing per-failure-kind callbacks uses `nethttp.CallSSEAdapter` directly instead (below).
- A route declaring multiple `Formats` has its decode format resolved ONCE per connection (from the response's `Content-Type`), reused for every event on that connection — symmetric with the server's own connect-once `Accept`-negotiation. See ["SSE with HTML fragments"](#sse-with-html-fragments-htmx-style) below for the one format neither client-side path can ever decode by design.
- `rest.Formats(...)` declared INLINE as an `NewSSERoute` opt applies identically whether the route is registered with a `Builder` (`RegisterHandle`) or consumed client-only via `client.Consume`/`CallSSEAdapter` (both derive a `ClientHandle()` internally, which applies the SAME declared `Formats` `registerHandle` does). To override the decode format for ONE specific `client.Consume` call without changing the route's declaration, pass `rest.ClientConsumeOptions` (type-erased `Formats any`) — wins over the route-declared format for that call only:
  ```go
  err := client.Consume(ctx, sensorRoute, struct{}{}, handleReading, rest.ClientConsumeOptions{
      Formats: []format.Format[SensorReading]{format.YAML(sensorReadingCodec)},
  })
  ```

For finer per-call control (`OnError`, `OnCredentialRejected`, `MaxBackoff`, `ExtraHeaders`) or a `ports.SourcePort`/pipeline consumer instead of a direct callback, use `nethttp.CallSSEAdapter` — the full-featured escape hatch, taking a pre-built `*rest.SSERouteHandle` directly and delegating to its own internal reconnect loop:

```go
port, _ := ports.NewSourcePort[SensorReading]("sensorEvents", sensorReadingCodec, ports.PortOptions{})
port.Bind(ctx, nethttp.CallSSEAdapter(httpClient, baseURL, sensorHandle, struct{}{}, nethttp.ConsumeOptions{
    OnError: func(err error) { slog.Warn("sse error", "err", err) },
}))
events := port.Stream(ctx)
```
## Chunked streaming responses

For routes that stream a response body (not SSE), use `format.NewStreamed`:

```go
import (
    adapttempl "github.com/DaniDeer/go-codex/adapters/templ"
    "github.com/DaniDeer/go-codex/format"
)

// Chunked streaming HTML page (validates props before committing headers)
dashRoute := rest.NewRoute[struct{}, DashboardProps]("GET", "/dashboard",
    codex.Empty, dashPropsCodec, rest.RouteMeta{},
    rest.Formats(
        adapttempl.StreamingFormat(dashPropsCodec, dashboardPage), // chunked HTML
        format.JSON(dashPropsCodec),                               // JSON fallback
    ),
)
```

The adapter detects `IsStreamable() == true` and calls `MarshalTo(props, w)` — the component writes directly to `ResponseWriter` without buffering. Headers are committed only after validation passes.

## templ SSR format plug-in

`adapters/templ` bridges a [templ](https://templ.guide/) component into the existing content negotiation pipeline. Add `adapttempl.Format` to a route's `Formats` and the same handler serves HTML to browser clients and JSON to API clients — no separate route, no separate handler.

```go
import adapttempl "github.com/DaniDeer/go-codex/adapters/templ"

// Declare both formats inline on one route — same handler, same route.
articleRoute := rest.NewRoute[struct{}, ArticleProps]("GET", "/article",
    codex.Empty, articlePropsCodec, rest.RouteMeta{},
    rest.Formats(
        adapttempl.Format(articlePropsCodec, ArticleCard), // Accept: text/html
        format.JSON(articlePropsCodec),                     // Accept: application/json
    ),
).WithHandler(func(ctx context.Context, _ struct{}) (ArticleProps, error) {
    return svc.GetArticle(ctx)
}).WithOptions(nethttp.Options{Observer: obs})

// One handler, one route — the adapter picks the format from the Accept header.
articleRoute.Register(b)
if err := nethttp.AttachMux(b, mux, addr); err != nil {
    log.Fatal(err)
}
_ = b.Serve(ctx) // blocks, owns its own http.Server
```

**Key properties:**
- Props are validated via the response codec's `Refine` constraints **before** the component renders. Invalid props return HTTP 500; the template is never reached with bad data.
- Works with both `adapters/nethttp` and `adapters/chi` — no adapter-specific variant needed.
- `adapttempl.DecodeNotSupportedError` is returned by the format's `Unmarshal`; use `errors.As` to detect it.
- Components written with `templ.ComponentFunc` require no code generation — self-contained in any `.go` file.

## SSE with HTML fragments (HTMX-style)

Combine `rest.NewSSERoute` and `adapttempl.Format` to stream HTML fragments over SSE — the HTML-over-the-wire / HTMX `sse-swap` pattern:

```go
// Each SSE event's data: field contains a rendered HTML fragment.
notifHandle, _ := rest.NewSSERoute[struct{}, NotifProps]("/sse/notifications",
    codex.Empty, notifCodec, rest.RouteMeta{},
).WithHandler(notifFn).RegisterHandle(b)
// SSE event Formats are set post-registration on the returned handle:
notifHandle.WithFormats(
    adapttempl.Format(notifCodec, notifFragment), // data: <li class="notif-warn">...</li>
)
```

Events with invalid props are rejected by the codec **before** the fragment component renders — no malformed HTML is ever sent to the client.

**Not consumable by `client.Consume`/`CallSSEAdapter`.** HTMX-over-SSE is
designed for a **browser's native `EventSource`** + DOM-swap — not for
go-codex's own typed Go client. `adapttempl.Format`'s decode direction
always returns `adapttempl.DecodeNotSupportedError` (HTML has no
meaningful "decode back into a struct" direction). A `client.Consume`/
`CallSSEAdapter` call against an HTML-formatted route will resolve that
format (matched by the connection's negotiated `Content-Type`), attempt
to decode every event, get `DecodeNotSupportedError` every time (silently
dropped by `client.Consume`; reported as a non-fatal `SSEParseError` via
`ConsumeOptions.OnError` for `CallSSEAdapter`) —
`fn`/the port's `dst` channel is never reached for that route. This is
correct, intentional behavior, not a bug: point a Go client at a
JSON/YAML/TOML-formatted route instead when you need a decoded value
back.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — SSE and templ SSR in the full HTTP server guide
- [Feature: Formats & Serialization](formats.md) — `format.NewStreamed` reference
- [examples/adapters-sse](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-sse) — full roundtrip: BuildPath, invalid event rejection, observer, AND client-side consumption via `Consume`/`CallSSEAdapter`
- [examples/adapters-streaming-sse-templ](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-streaming-sse-templ) — chunked streaming + SSE HTML fragments
- [examples/adapters-templ](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-templ) — content negotiation: HTML + JSON from one route
