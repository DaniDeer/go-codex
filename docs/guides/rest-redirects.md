# Guide: REST Redirects

This guide walks through the "POST-redirect-GET" pattern step by step — see [the feature page](../features/rest-redirects.md) for the full reference.

## 1. Declare both routes

Declare the target route first (so it can be referenced by value), then the route whose handler will redirect to it.

```go
getOrderRoute := rest.NewRoute[GetOrderReq, Order]("GET", "/orders/{id}",
    getOrderReqCodec, orderCodec,
    rest.NewPathParam("id", codex.String(),
        func(r GetOrderReq) string { return r.ID },
        func(r *GetOrderReq, v string) { r.ID = v }))

var createOrderRoute rest.Route[CreateOrderReq, Order]
createOrderRoute = rest.NewRoute[CreateOrderReq, Order]("POST", "/orders",
    createOrderReqCodec, orderCodec)
```

`createOrderRoute` is declared as a `var` first so its own handler closure (below) can reference it as the redirect's `from` argument — `rest.Redirect` needs the *originating* route's declared method to validate a 307/308 method match.

## 2. Attach handlers

```go
createOrderRoute = createOrderRoute.WithHandler(func(ctx context.Context, req CreateOrderReq) (Order, error) {
    id := createOrder(req)
    var zero Order
    return zero, rest.Redirect(http.StatusSeeOther, createOrderRoute, getOrderRoute,
        map[string]string{"id": id})
})
getOrderRoute = getOrderRoute.WithHandler(func(ctx context.Context, req GetOrderReq) (Order, error) {
    return lookupOrder(req.ID)
})
```

`createOrderRoute`'s handler never constructs its own declared `Order` response directly — the `zero` value is discarded by the client (or the server, on the wire: only the status + `Location` header are ever sent).

## 3. Register and serve (either adapter)

```go
b := rest.NewServer(rest.Info{Title: "Orders API", Version: "1.0.0"})
createOrderRoute.Register(b)
getOrderRoute.Register(b)

mux := http.NewServeMux()
b.Attach(nethttp.NewServerTransport(nethttp.ServerTransportOptions{Mux: mux, Addr: addr}))
b.Serve(ctx) // blocks
```

No redirect-specific server wiring is needed — `adapters/nethttp`/`adapters/chi` recognize the `rest.RedirectError` a handler returns automatically.

## 4. Call it from the client, auto-following

```go
client := rest.NewClient()
client.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: hc, BaseURL: baseURL}))

// getOrderRoute is never called directly in this app — warm the
// registry explicitly so Call(createOrderRoute, ...) can resolve it.
client.RegisterRoute(getOrderRoute)

respAny, err := client.Call(ctx, createOrderRoute, CreateOrderReq{Item: "Widget"})
if err != nil {
    log.Fatal(err)
}
order := respAny.(Order) // getOrderRoute's own response type
```

If `getOrderRoute` IS also called directly elsewhere in the same program, skip step 4's `RegisterRoute` call entirely — the first `client.Call(ctx, getOrderRoute, ...)` auto-populates the registry.

## 5. The escape hatch: inspecting the redirect yourself

A caller using `rest.CallWithTransport` directly (no `*rest.Client`) — or one who wants to inspect the redirect before deciding whether to follow it — gets the typed error back untouched:

```go
transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: hc, BaseURL: baseURL})
_, err := rest.CallWithTransport(ctx, transport, createOrderHandle, CreateOrderReq{Item: "Gadget"})

var redirErr rest.RedirectError
if errors.As(err, &redirErr) {
    fmt.Println("redirected to", redirErr.Location, "status", redirErr.Status)
}
```

See [`examples/rest-redirect`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-redirect) for the complete, runnable version of this walkthrough.
