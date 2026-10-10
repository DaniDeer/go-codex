# Typed HTTP Redirects

> See also: [`api/rest` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`adapters/nethttp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp)
>
> Runnable demo: [`examples/rest-redirect`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-redirect)
>
> Guide: [REST redirects](../guides/rest-redirects.md)

A handler declares "redirect to THIS route" by calling `rest.Redirect`/`rest.RedirectToSSE` with the **target route value**, never a raw URL string — the path is built from the target's own declared template + vars, the same way `BuildPath` always works. `rest.Client.Call`/`rest.Client.Consume` transparently follow the redirect and decode the *target* route's own response type, as long as the target has been seen by that `*rest.Client`.

```go
import "github.com/DaniDeer/go-codex/api/rest"

var getOrderRoute = rest.NewRoute[GetOrderReq, Order]("GET", "/orders/{id}",
    getOrderReqCodec, orderCodec,
    rest.NewPathParam("id", codex.String(),
        func(r GetOrderReq) string { return r.ID },
        func(r *GetOrderReq, v string) { r.ID = v }))

var createOrderRoute rest.Route[CreateOrderReq, Order]
createOrderRoute = rest.NewRoute[CreateOrderReq, Order]("POST", "/orders",
    createOrderReqCodec, orderCodec).WithHandler(
    func(ctx context.Context, req CreateOrderReq) (Order, error) {
        id := createOrder(req)
        var zero Order
        return zero, rest.Redirect(http.StatusSeeOther, createOrderRoute, getOrderRoute,
            map[string]string{"id": id})
    })
```

## Server side

A handler returns the `error` `rest.Redirect`/`rest.RedirectToSSE` produces (a `rest.RedirectError`) instead of its declared response. `adapters/nethttp` and `adapters/chi` both recognize this error *before* `ErrorPattern`/`ErrorStatus` matching and render a bare status + `Location` header, with no body — for BOTH plain REST routes and SSE routes (gated on whether the SSE stream has already committed to `200 text/event-stream`; a redirect returned after the first `send` is treated as an ordinary late error, not honored).

`rest.Redirect`/`rest.RedirectToSSE` validate at construction time:
- **307/308** preserve the originating method and body — the target route's declared method must match the originating route's, or `rest.RedirectMethodMismatchError` is returned instead.
- **301/302/303** always redirect to GET (RFC 9110) — no method constraint.
- Missing/invalid path vars for the target's template produce `rest.RedirectTargetVarError`.

## Client side (`adapters/nethttp`)

`rest.Client` keeps two parallel **redirect-target registries**: one for `Call`-decodable `Route`s, one for `Consume`-streamable `SSERoute`s. A route is indexed automatically the first time it's seen via `Client.Call`/`Client.Consume` — a route that's *only ever reached as a redirect target* needs explicit warming via `Client.RegisterRoute`:

```go
client := rest.NewClient()
client.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: hc, BaseURL: baseURL}))
client.RegisterRoute(getOrderRoute) // warm the registry — getOrderRoute is never called directly

respAny, err := client.Call(ctx, createOrderRoute, CreateOrderReq{Item: "Widget"})
order := respAny.(Order) // the TARGET route's response type, decoded transparently
```

On a 3xx:
- `Client.Call` resolves against the `routes` registry. A match in `sseRoutes` only returns `rest.RedirectToStreamUnsupportedError` (a stream target is fundamentally incompatible with `Call`'s single-decode contract). No match: `rest.UnrecognizedRedirectError`.
- `Client.Consume` resolves against `sseRoutes` first (and re-checks on **every reconnect attempt**, not just the first connection). A match in `routes` only returns `rest.RedirectTargetNotStreamableError`. The target SSE route's event type must be identical to the originating route's event type (the caller's `fn` is fixed to it) — a mismatch is treated as `rest.UnrecognizedRedirectError`.
- Both cap the chain depth (`rest.RedirectChainTooDeepError`) at `ClientCallOptions.MaxRedirects`/`ClientConsumeOptions.MaxRedirects` (0 = default 10, mirroring `net/http`'s own precedent) — covers both long chains and cycles.
- Auto-follow reuses the **originating call's own credentials/headers/cookies** — never re-derived from the registered target's own `ClientMW`. The target is consulted only for its decode logic/event type.

**`rest.CallWithTransport`/`nethttp.NewClientTransport` used directly (no `*rest.Client`) stay registry-free** — they return the typed `rest.RedirectError` directly for the caller to inspect or resolve manually, matching the escape-hatch contract the rest of `api/rest` already follows.

`NewClientTransport` overrides `CheckRedirect` on a shallow copy of the caller's own `*http.Client` (so a caller's own client is never mutated) to stop `net/http`'s silent built-in auto-follow — `rest.Client`'s own registry-aware auto-follow replaces it.

## `ResponseMeta.Headers`

A redirect's `Location` response header (or any other declared response header) can be documented in the OpenAPI spec via `rest.ResponseMeta.Headers`:

```go
rest.ResponseMeta{
    Status: "303",
    Headers: map[string]rest.ResponseMetaHeader{
        "Location": {Description: "The created order's URL.", Required: true},
    },
}
```
