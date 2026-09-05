# Ports, Plugins, and Adapters

> See also: [`ports` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/ports) · [Ports feature page](../features/ports.md) · [Wiring Guide](../guides/ports.md) · [Forge Pipelines concept](pipelines.md)
>
> Runnable demos: [`examples/ports-plain-go`](https://github.com/DaniDeer/go-codex/tree/main/examples/ports-plain-go) (idiomatic Go, zero pipelines) · [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) (forge pipelines)

Every go-codex application answers the same question at its boundary: *how do I
talk to the outside world without letting a transport leak into my business
logic?* `ports` is the answer, and it works the same way whether your business
logic is a `forge` pipeline or a plain Go function.

## Three roles, one mechanism

| Role | What it is | Where it lives |
|---|---|---|
| **Port** | A typed, protocol-agnostic declaration of an IO boundary (`SourcePort[T]`, `SinkPort[T]`, `IOPort[Req,Resp]`, `ToolPort[In,Out]`, `LatestPort[T]`, `DuplexPort[In,Out]`) | `domain`/pipeline package — zero adapter imports |
| **Plugin** | A `Pattern` (`RESTPattern`, `EventPattern`, `ReqReplyPattern`, `MCPPattern`, ...) registered on a port via `PluginXxxPattern`, returning the typed handle the adapter needs | `main.go` — the only place that knows the wire shape |
| **Adapter** | A concrete transport implementation (`mqtt5.SubscribeAdapter`, `nethttp.CallAdapter`, `sql.QueryEachAdapter`, ...) bound to the port via `Bind` | `main.go` — the only place that knows the concrete broker/client |

The sequence is always **declare → plug in → bind**:

```go
// 1. Declare — domain/pipeline.go, no adapter imports
var Calibration = codex.Must(ports.NewIOPort[SensorReading, CalibratedReading](
    "calibration", ReadingCodec, calibratedCodec, ports.PortOptions{}))

// 2. Plug in — main.go, describes the wire shape and returns a typed handle
handle, err := Calibration.PluginRESTPattern(ports.RESTPattern{
    Method: "GET", Path: "/calibration/{sensorID}",
})

// 3. Bind — main.go, picks the concrete transport
Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, handle, callOpts))
```

Swap step 3 for `sql.QueryEachAdapter`, `file.ReadEachAdapter`, or
`zeromq.CallAdapter` and steps 1–2 never change. This is the same promise
`codex.Codec[T]` makes for data shape, extended to IO boundaries.

## The question this page answers: pipelines or plain Go?

A common misreading of `ports` is that it exists *for* forge pipelines, and
that using it without pipelines means falling back to calling an adapter
constructor directly. **That is not the design.** `declare → PluginXxxPattern
→ Bind` is identical regardless of what happens after `Bind` — only the
*consumption* of the bound port differs, and every port type has both styles
built in:

| Port | Pipeline-composed consumption | Plain-Go consumption |
|---|---|---|
| `SourcePort[T]` | `Stream(ctx)` piped into `gstream.Apply` | `Stream(ctx)` drained with [`stream.Drain`](pipelines.md) and a plain callback |
| `SinkPort[T]` | `Feed(ctx, stream)` from an upstream `gstream.Stream` | `Start(ctx)` / `Push(ctx, v)` / `Close()` from a plain goroutine or loop |
| `IOPort[Req,Resp]` | `Connect(ctx, stream)` — streams requests through the adapter | `Call(ctx, req)` — one request in, one response out |
| `ToolPort[In,Out]` | `SetPipeline(func(ctx, In) gstream.Stream[Out])` | `SetFunc(func(ctx, In) (Out, error))` |
| `LatestPort[T]` | `Feed(ctx, stream)` populates the cache | `Latest()` reads the cache directly |
| `DuplexPort[In,Out]` | `Feed(ctx, stream)` drives outbound frames | `Inbound(ctx)` + plain per-session handling |

Both columns call the **same bound adapter**, decode with the **same codec**,
and were built from the **same `Pattern`-derived handle**. `SetFunc` and
`Call` are not simplified re-implementations of `SetPipeline`/`Connect` — the
plain-Go path still goes through the adapter's real `Transform`/pipeline
function; `SetFunc` wraps your function once so it satisfies the same
internal contract as a pipeline function would, and `Call` runs a
single-item stream (`stream.Single` → `stream.Collect`) under the hood.

## Which should a user choose?

Neither is "more advanced." Choose based on what the port's *business logic*
naturally looks like, not on whether the application uses `forge` elsewhere:

- **Plain Go** (`SetFunc`, `Call`, `stream.Drain`, `Start`/`Push`/`Close`) —
  when the logic is a straightforward `func(In) (Out, error)` transform, a
  one-off request, or an imperative loop. No `forge`/`gstream` import needed
  at all. See [`examples/ports-plain-go`](https://github.com/DaniDeer/go-codex/tree/main/examples/ports-plain-go).
- **Forge pipelines** (`SetPipeline`, `Connect`, `gstream.Apply`, `Feed`) —
  when the logic benefits from composition (`Map`/`Filter`/`CombineLatest`/
  windowing), needs `forge.Registry` governance (named, versioned functions
  with observability), or fans multiple sources/sinks together via
  [`PipePort`](../features/ports.md#pipeportt). See
  [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service).

An application can freely **mix both** — one `ToolPort` using `SetFunc` for a
simple unit conversion, another using `SetPipeline` because it composes three
pipeline stages — without the `Pattern`/`Bind` declarations looking any
different from one another.

## Why this matters for migration

Because the declaration never changes, moving a port from plain Go to a forge
pipeline (or back) is a **local, mechanical edit** — replace `SetFunc` with
`SetPipeline` (or `stream.Drain` with `gstream.Apply`), leave the `Pattern`,
handle, and `Bind` call untouched. A user who starts with idiomatic Go and
later needs pipeline composition for one port never has to re-learn how
`ports` works, and never has to touch `main.go`'s wiring code to make that
switch.

## Relationship to adapters used directly

Every adapter package (`adapters/nethttp`, `adapters/mqtt5`,
`adapters/sql`, ...) also works WITHOUT any `ports` involvement at all — this
is unchanged and remains the right choice for one-off scripts or code that
has no reason to declare a reusable boundary. `ports` is for applications
that want the boundary declared once, independent of the transport choice,
and swappable in one place. Reaching for `ports` does not require adopting
`forge` — see the table above.

Used directly, an adapter is STILL never a raw bypass of `api/rest`/
`api/events`'s own vocabulary — this is an architectural constraint, not a
style preference. Every adapter entry point is one of exactly three shapes:

1. **A `ClientTransport`/`ServerTransport` (REST) or `Transport` (events)
   implementation**, attached via `rest.Client.Attach`/`rest.Server.Attach`/
   `events.Client.Attach` — e.g. `nethttp.Attach`, `nethttp.AttachMux`,
   `chi.AttachRouter`, `mqtt5.Attach`, `mqtt.Attach`, `zeromq.Attach`. This is
   the declarative, common-case path: `client.Call(...)`/`client.Consume(...)`
   for REST, `client.Publish(...)`/`client.Subscribe(...)` for events.
2. **A `ports.SourceAdapter`/`SinkAdapter`/`IOAdapter`/etc. implementation**,
   bound via `Port.Bind` — the pipeline path this page is about.
3. **A SANCTIONED, handle-based escape hatch** — for callers who need a
   pre-built `*rest.RouteHandle`/`*rest.SSERouteHandle`/`*events.ChannelHandle`
   directly rather than a `rest.Route`/`rest.SSERoute`/`events.Channel`
   value, or who need capability the declarative path doesn't expose (e.g.
   per-call retry/backoff control). `nethttp.CallWithHandle`,
   `nethttp.CallSSEAdapter`, and `mqtt5`/`mqtt`/`zeromq`'s
   `NewPublishTransport`/`NewSubscribeTransport` (paired with
   `events.PublishHandle`/`SubscribeHandle`) are the shipped examples. Every
   one of these STILL takes a handle built by `api/rest`/`api/events` — none
   of them decode/encode/dispatch independently of it.

There is no fourth shape: no adapter exposes a bare, non-handle-based
`Publish`/`Subscribe`/`Call`/`Serve` function that bypasses `RouteHandle`/
`ChannelHandle` entirely (an earlier design did — `nethttp.Call[Req,Resp]`,
removed per `docs/design/d-0002-pubsub-workflow-simplification.md`'s
Decision 6 — the unification onto `Client`/`ClientTransport` is what
replaced it). A NEW adapter must follow the same rule: see the
`add-a-new-adapter` skill's own architectural-boundary rule for the concrete
checklist.
