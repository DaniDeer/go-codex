# Go Library as Contract

Instead of an OpenAPI or AsyncAPI document as the cross-service contract, a shared Go package defines types, codecs, and channel/route specs. Both services import it. **The Go compiler enforces the contract**: any field rename, type change, or constraint modification breaks compilation on both sides immediately — no stale YAML, no schema drift, no code-generation step.

## Pattern

```
contract/
  contract.go   ← shared types, codecs, route/channel specs

producer/
  main.go       ← imports contract/, calls events.PublishHandle / rest.Client.Call

consumer/
  main.go       ← imports contract/, calls events.SubscribeHandle / nethttp.AttachMux
```

## HTTP example

```go
// contract/contract.go
var CreateUser = rest.NewRoute[CreateUserReq, User](
    "POST", "/users", createUserReqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser"},
)

// server/main.go
route := contract.CreateUser.WithHandler(myHandler).WithOptions(opts)
route.Register(builder)
nethttp.AttachMux(builder, mux, addr)
go func() { _ = builder.Serve(ctx) }()

// client/main.go — same Route, no duplication
client := rest.NewClient()
nethttp.Attach(client, http.DefaultClient, serverURL)
userAny, err := client.Call(ctx, contract.CreateUser, req)
user := userAny.(User)
```

## MQTT example

```go
// contract/contract.go
var ReadingsChannel = events.NewChannel[SensorReading](
    "sensors/{sensorID}/readings", sensorReadingCodec,
    events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
)

// producer/main.go — events.PublishHandle auto-derives topic vars from reading's
// declared merge fields, no manual vars map needed
pub := contract.ReadingsChannel.WithPublish(events.Publish{...})
transport := adaptermqtt.NewPublishTransport[SensorReading](client, 1, false, adaptermqtt.PublishOptions[SensorReading]{})
err := events.PublishHandle(ctx, pub, transport, reading)

// consumer/main.go
sub := contract.ReadingsChannel.WithSubscribe(events.Subscribe{...})
subTransport := adaptermqtt.NewSubscribeTransport[SensorReading](client, 1, opts)
go func() { _ = events.SubscribeHandle(ctx, sub, subTransport, fn) }()
```

## One struct, one call — for free

Because the merge-field mechanism lives in the codec+builder layer (not in
the contract-sharing pattern itself), a shared contract package
automatically gets the "one struct, one call" convenience on BOTH sides —
producer and consumer, client and server — with zero extra code beyond
declaring the route/channel once. See
[API Contracts — Design principle: one struct, one call](api-contracts.md#design-principle-one-struct-one-call)
for the underlying promise and its per-boundary coverage — REST is the
reference implementation, but events/req-reply/WebSocket/SSE all ship the
same single-call convenience today (`events.PublishHandle` + each
adapter's `NewPublishTransport` for pub/sub — Decision 7 of
`docs/roadmap/pubsub-workflow-simplification.md`; `mqtt5.CallHandle`/
`zeromq.CallHandle` for req/reply).

## When to use this pattern

Use the shared Go contract when:
- All communicating services are written in Go
- Compile-time contract enforcement matters more than cross-language interoperability
- You want to avoid the drift between code and a separate spec file

For external-facing APIs (consumed by non-Go clients), generate an OpenAPI/AsyncAPI spec from the same codec definitions — both are supported simultaneously.

## Examples

- [adapters-nethttp-client](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client) — HTTP shared contract
- [adapters-mqtt-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-contract) — MQTT shared contract
- [gob-contract](https://github.com/DaniDeer/go-codex/tree/main/examples/gob-contract) — binary gob encoding contract
