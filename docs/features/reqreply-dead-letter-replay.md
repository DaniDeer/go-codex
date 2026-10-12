# Request/Reply Dead-Letter Replay

> See also: [`api/reqreply` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply) · [`adapters/mqtt5` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt5) · [`adapters/zeromq` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq)
>
> Runnable demo: [`examples/reqreply-api`'s `demo_dead_letter_replay.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/reqreply-api/demo_dead_letter_replay.go)
>
> Guide: [reqreply Dead-Letter Replay](../guides/reqreply-dead-letter-replay.md)
>
> See also: [reqreply Forward/Bounce](reqreply-forward.md) — `ReplayDeadLetter` reuses Forward's own chain-following primitives directly.

A route's `reqreply.DeadLetter` declaration (see `docs/design/d-0005-error-handling.md`'s Topic 4) publishes a durable `reqreply.DeadLetterEnvelope{SourceTopic, Payload, Error, Timestamp, Properties}` whenever a request fails for ANY reason not already covered by a more specific `ErrorPattern` — a decode failure, or an unmatched business error. `Client.ReplayDeadLetter`/`Client.ReplayDeadLetterAsync` are the ops/replay side of that mechanism: given a decoded envelope, they resolve `SourceTopic` back against the registry and reissue the original request.

```go
import "github.com/DaniDeer/go-codex/api/reqreply"

client := reqreply.NewClient()
client.Attach(mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{Client: mqttClient, Router: router}))
client.RegisterRoute(sumRoute) // the route the dead-lettered message originally failed on

respAny, err := client.ReplayDeadLetter(ctx, envelope)
if err != nil {
    log.Fatal(err)
}
sum := respAny.(SumResp)
```

`envelope` above comes from decoding the raw bytes an ops/replay tool reads off the dead-letter topic via `reqreply.DecodeDeadLetterEnvelope(data []byte) (DeadLetterEnvelope, error)` — the single sanctioned decode path, sharing the SAME codec `DeadLetterFor` itself encodes with, so it can never drift out of sync:

```go
envelope, err := reqreply.DecodeDeadLetterEnvelope(dlqMessagePayload)
```

## Why `Payload` is republished verbatim, never re-decoded

`DeadLetter` fires for BOTH a decode failure and an unmatched business error. If `ReplayDeadLetter` tried to re-decode `Payload` as the route's own request type before republishing, it would fail IDENTICALLY for the decode-failure case — replay would be impossible for exactly the case most likely to need it. Instead, `ReplayDeadLetter` republishes `Payload` verbatim via the SAME single-hop `ClientTransport.CallRaw` primitive `Client.followForward` uses — "publish raw bytes to topic X, await reply, decode via the registry-resolved target's response codec."

## Why hop 1 doesn't use the SAME chain-following loop as Forward

`Client.followForward`'s shared chain-following loop unconditionally compares an "originating route"'s declared security requirements against every hop's target. Replay has no such originating route — it starts COLD from a `DeadLetterEnvelope`, with no prior authenticated context to compare against. Forcing the shared loop to run from hop 1 would reject replay against ANY route declaring security — almost certainly the most common real-world case a replay tool exists to recover.

`ReplayDeadLetter` therefore performs its OWN first-hop dispatch directly: resolves `envelope.SourceTopic` against the registry (`reqreply.UnrecognizedDeadLetterSourceError` on a miss — a distinct type from Forward's own `UnrecognizedForwardError`, even though the underlying condition is mechanically identical; `reqreply.DuplicateTopicTypeMismatchError` on a poisoned registry entry), calls `CallRaw` once, and classifies the reply (ordinary → decode; another `ForwardError` → hands off to the SAME shared chain-following loop for hop 2 onward; an `ErrorPattern`-matched reply → decoded via the resolved target's `DecodeErrorFor`). This mirrors `Client.Call`'s own shape exactly: hop 1 always happens outside the loop too; only a RECOGNIZED forward reply ever enters it.

If the replayed request's handler itself forwards or dead-letters again, it dispatches through the EXACT SAME underlying primitives as an ordinary `Call` — Forward/Bounce's own `MaxForwards` chain-depth cap already covers this automatically; Replay needs no cap of its own.

## Wire-level properties survive replay

A dead-lettered request's wire-level properties (e.g. an MQTT5 User Property delivering a credential) are captured into `DeadLetterEnvelope.Properties` at dead-letter time (`adapters/mqtt5`'s real capture; `adapters/zeromq`'s confirmed no-op, no property mechanism of its own) and threaded straight into `ReplayDeadLetter`'s own `CallRaw` call — the SAME mechanism [Forward/Bounce's own wire-level properties addendum](reqreply-forward.md#wire-level-properties) uses. Without this, a header-delivered credential on a dead-lettered request would be PERMANENTLY LOST — republishing `Payload` verbatim could never reconstruct it.

## Async variant

`Client.ReplayDeadLetterAsync` mirrors `Client.CallAsync`'s goroutine-wraps-a-synchronous-call shape — returns a `*reqreply.Future[Resp]` (as `any`) immediately, resolved later:

```go
futureAny, err := client.ReplayDeadLetterAsync(ctx, envelope)
future := futureAny.(*reqreply.Future[SumResp])
resp, err := future.Wait(ctx)
```

## Operational caution

Replaying a PERMANENTLY-broken dead-lettered message (one that will always fail the same decode/business check) could flood the dead-letter topic with repeat entries if automated replay tooling retries without limit. `Client.ReplayDeadLetter`/`ReplayDeadLetterAsync` are NOT safe to loop unconditionally against a message that will never succeed — any automated tooling built on top of this should track attempt counts or apply its own backoff/giveup policy.
