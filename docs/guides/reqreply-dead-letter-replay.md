# Guide: reqreply Dead-Letter Replay

This guide walks through recovering a dead-lettered message step by step — see [the feature page](../features/reqreply-dead-letter-replay.md) for the full reference.

## 1. Declare a route with `DeadLetter`

```go
sumRoute := reqreply.NewRoute[SumReq, SumResp]("compute/sum", sumReqCodec, sumRespCodec,
    reqreply.DeadLetter("compute/sum/dlq"),
).WithHandler(func(ctx context.Context, req SumReq) (SumResp, error) {
    return SumResp{Total: req.A + req.B}, nil
})
```

Any failure not already covered by a more specific declared `ErrorPattern` — a decode failure, or an unmatched business error — publishes a `reqreply.DeadLetterEnvelope` to `"compute/sum/dlq"`, ALONGSIDE the caller's own ordinary error reply (never instead of it).

## 2. Capture the envelope off the wire

An ops/replay tool subscribes to the dead-letter topic directly and decodes the published bytes via `reqreply.DecodeDeadLetterEnvelope` — the single sanctioned decode path, guaranteed to stay in sync with the encode side `DeadLetterFor` itself uses (no hand-rolled JSON struct to keep manually in sync with `DeadLetterEnvelope`'s own private field names):

```go
envelope, err := reqreply.DecodeDeadLetterEnvelope(dlqMessagePayload)
if err != nil {
    log.Fatal(err)
}
```

## 3. Register the source route and replay

```go
client := reqreply.NewClient()
client.Attach(mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router}))

// sumRoute must be registered so ReplayDeadLetter can resolve
// envelope.SourceTopic back to its response codec.
client.RegisterRoute(sumRoute)

respAny, err := client.ReplayDeadLetter(ctx, envelope)
if err != nil {
    log.Fatal(err)
}
sum := respAny.(SumResp)
```

If `sumRoute` is already called/registered elsewhere in the same program, this `RegisterRoute` call is unnecessary — any prior `Call`/`CallAsync`/`RegisterRoute` against it already warmed the registry.

## 4. An unregistered `SourceTopic` fails fast

```go
_, err := client.ReplayDeadLetter(ctx, reqreply.DeadLetterEnvelope{SourceTopic: "compute/never-registered", Payload: payload})

var unrecognized reqreply.UnrecognizedDeadLetterSourceError
if errors.As(err, &unrecognized) {
    fmt.Println("no route registered for", unrecognized.SourceTopic)
}
```

## 5. A header-delivered credential survives the round trip

If the ORIGINAL request carried a wire-level property (e.g. an MQTT5 User Property delivering a credential), it is captured into `envelope.Properties` at dead-letter time and threaded straight through `ReplayDeadLetter`'s own republish — no extra step needed on the caller's part.

See [`examples/reqreply-api`'s `demo_dead_letter_replay.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/reqreply-api/demo_dead_letter_replay.go) for the complete, runnable version of this walkthrough (a stateful handler fails once, dead-lettering a request with a real credential attached, then succeeds on replay with the credential intact).

## 6. Async variant

```go
futureAny, err := client.ReplayDeadLetterAsync(ctx, envelope)
future := futureAny.(*reqreply.Future[SumResp])
resp, err := future.Wait(ctx)
```
