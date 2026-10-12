# Guide: reqreply Forward/Bounce

This guide walks through "a handler signals a different route should answer this" step by step — see [the feature page](../features/reqreply-forward.md) for the full reference.

## 1. Declare both routes

Declare the target route first (so it can be referenced by value), then the route whose handler will forward to it.

```go
sumRoute := reqreply.NewRoute[SumReq, SumResp]("compute/sum", sumReqCodec, sumRespCodec)

legacyAddRoute := reqreply.NewRoute[LegacyAddReq, LegacyAddResp]("compute/legacy-add",
    legacyAddReqCodec, legacyAddRespCodec)
```

Unlike `rest.Redirect`, `reqreply.Forward` needs no "originating route" argument — there is no method/body-preservation constraint to validate, so `legacyAddRoute` doesn't need to be declared as a `var` up front just to be referenced by its own handler closure.

## 2. Attach handlers

```go
legacyAddRoute = legacyAddRoute.WithHandler(func(ctx context.Context, req LegacyAddReq) (LegacyAddResp, error) {
    var zero LegacyAddResp
    return zero, reqreply.Forward(sumRoute, SumReq{A: req.X, B: req.Y}, nil)
})
sumRoute = sumRoute.WithHandler(func(ctx context.Context, req SumReq) (SumResp, error) {
    return SumResp{Total: req.A + req.B}, nil
})
```

`legacyAddRoute`'s handler never constructs its own declared `LegacyAddResp` response directly — the `zero` value is discarded (only the reserved wire signal + the brand-new `SumReq` payload are ever sent).

## 3. Register and serve (either adapter)

```go
server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
legacyAddRoute.Register(server)
sumRoute.Register(server)

server.Attach(mqtt5.NewServerTransport(mqtt5.ServerTransportOptions{Client: mqttClient, Router: router}))
server.Serve(ctx) // blocks
```

No forward-specific server wiring is needed — `adapters/mqtt5`/`adapters/zeromq` recognize the `reqreply.ForwardError` a handler returns automatically.

## 4. Call it from the client, auto-following

```go
client := reqreply.NewClient()
client.Attach(mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router}))

// sumRoute is never called directly in this app — warm the registry
// explicitly so Call(legacyAddRoute, ...) can resolve it.
client.RegisterRoute(sumRoute)

respAny, err := client.Call(ctx, legacyAddRoute, LegacyAddReq{X: 3, Y: 4})
if err != nil {
    log.Fatal(err)
}
sum := respAny.(SumResp) // sumRoute's own response type
```

If `sumRoute` IS also called directly elsewhere in the same program, skip step 4's `RegisterRoute` call entirely — the first `client.Call(ctx, sumRoute, ...)` auto-populates the registry.

## 5. The escape hatch: inspecting the forward yourself

A caller using `reqreply.CallWithTransport` directly (no `*reqreply.Client`) — or one who wants to inspect the forward before deciding whether to follow it — gets the typed error back untouched:

```go
transport := mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router})
_, err := reqreply.CallWithTransport(ctx, transport, legacyAddHandle, LegacyAddReq{X: 5, Y: 6})

var fwdErr reqreply.ForwardError
if errors.As(err, &fwdErr) {
    fmt.Println("forwarded to", fwdErr.TargetTopic)
}
```

See [`examples/reqreply-api`'s `demo_forward.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/reqreply-api/demo_forward.go) for the complete, runnable version of this walkthrough.
