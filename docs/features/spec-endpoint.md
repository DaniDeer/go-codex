# Self-Serving Spec Endpoint — `ServeSpec`

> See also: [`api/rest` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`api/reqreply` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply) · [`api/events` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/events)
>
> Runnable demos: [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) · [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) · [`examples/events-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api)

`rest.Server.OpenAPISpec()`/`reqreply.Server.AsyncAPISpec()`/`events.Client.AsyncAPISpec()`
already build a complete spec document in memory from a server's registered
routes/channels — `ServeSpec` goes one step further and **serves that
document itself**, natively, without any hand-rolled handler.

`ServeSpec` is "just another route/channel" internally — it goes through the
SAME dispatch, middleware, and observer machinery every other route/channel
already has. Attach `rest.WithSpecMiddleware`/`reqreply.WithSpecMiddleware`/
`events.WithSpecMiddleware` to compose it with a timing/observer middleware
exactly like any other route.

## `api/rest` — `Server.ServeSpec`

```go
b := rest.NewServer(rest.Info{Title: "My API", Version: "1.0.0"})
// ... register routes ...
if err := b.ServeSpec("/openapi.yaml"); err != nil {
    log.Fatal(err)
}
b.Attach(nethttp.NewServerTransport(...))
```

The document is computed lazily on the first request and cached thereafter.
Content negotiation is real: `Accept: application/yaml` (the default, used
for an empty or `*/*` Accept header) or `Accept: application/json` — the
SAME negotiation algorithm any other route's `RouteHandle.WithFormats`
declaration uses.

`path` is required — `ServeSpec` must be called BEFORE `Server.Attach`, like
every other `Register` call. A duplicate path is reported the same way any
other route collision is, by the adapter at `Attach` time.

## `api/reqreply` — `Server.ServeSpec`

```go
b := reqreply.NewServer(reqreply.Info{Title: "My API", Version: "1.0.0"})
// ... register routes ...
specHandle, err := b.ServeSpec("spec")
if err != nil {
    log.Fatal(err)
}
b.Attach(mqtt5transport.NewServerTransport(...))

// A caller fetches the spec via a normal Call, reusing specHandle:
respAny, err := client.Call(ctx, specHandle, reqreply.SpecReq{Format: "json"})
```

reqreply has no `Accept`-header equivalent, so format selection travels in
the request body instead: `SpecReq.Format` is `"yaml"` (default, used when
empty) or `"json"`. A body field — rather than an mqtt5-only User Property —
is used because reqreply routes are expected to stay portable across BOTH
mqtt5 (which has User Properties) AND zeromq (which has no property
mechanism at all).

`ServeSpec` returns the registered `*RouteHandle` — a reqreply caller needs
this handle to `Client.Call` the SAME route from the client side, exactly
like any other server-side handle in this package.

## `api/events` — `Client.ServeSpec`

```go
c := events.NewClient(events.WithInfo(events.Info{Title: "My API", Version: "1.0.0"}))
// ... register channels, Attach ...
if err := c.ServeSpec(ctx, "spec/yaml"); err != nil {
    log.Fatal(err)
}
if err := c.ServeSpec(ctx, "spec/json", events.WithSpecFormat(events.SpecFormatJSON)); err != nil {
    log.Fatal(err)
}
```

Pub/sub has no "next request" trigger the way REST's next request or
reqreply's next Call provides one, so `ServeSpec` publishes the CURRENT
document ONCE, immediately — no lazy caching. It also has no
format-negotiation mechanism at publish time, so `ServeSpec` publishes
exactly ONE format per call (YAML by default, or JSON via `WithSpecFormat`).
A caller wanting BOTH formats published calls `ServeSpec` TWICE, with two
different topics.

The published payload is raw, pre-marshaled text (via `format.Binary`/
`codex.Bytes`) — a caller **subscribing** to the topic must declare the SAME
format on its own `Subscriber`, or decoding fails (the default JSON encoding
of a `[]byte` base64-wraps it, which is wrong for reading the raw document):

```go
sub := events.NewChannel[[]byte]("spec/yaml", codex.Bytes()).WithSubscribe(events.Subscribe{})
err := subscriberClient.Subscribe(ctx, sub,
    func(ctx context.Context, body []byte) error {
        // body is the raw spec document text
        return nil
    },
    events.ClientSubscribeOptions{Formats: []format.Format[[]byte]{format.Binary(codex.Bytes())}},
)
```

`ServeSpec` must be called AFTER `Client.Attach` (a `Transport` must already
be present to publish against).

## Security

The spec document is public by default, even when the server/client
declares a global security requirement for every other route/channel —
`ServeSpec`'s internal route/channel declares an explicit, non-nil EMPTY
`Security` slice, opting out of inheriting that global requirement.

## See also

- [OpenAPI Spec Generation](openapi.md) — the underlying `OpenAPISpec()`/`render/openapi` document model
- [AsyncAPI Spec Generation](asyncapi.md) — the underlying `AsyncAPISpec()`/`render/asyncapi` document model
- [Declarative Router Groups](router-groups.md) — `ServeSpec` composes with the same middleware capabilities Router-grouped routes/channels use
