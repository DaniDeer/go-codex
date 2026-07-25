# `adapters/redis` — Phase 2: Pub/Sub

> **Status:** Design draft — awaiting refinement / use case.
> [← Back to Roadmap](index.md)
>
> See also: [Redis Cache Adapter (Phase 1, shipped)](../features/redis.md) ·
> [`adapters/redis` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/redis)

## Motivation

Phase 1 shipped the typed cache boundary (`CachePattern`, `GetAdapter`/
`SetAdapter`/`DrainSetAdapter`/`Seed`). Redis is also a lightweight pub/sub
broker — services that already run Redis for caching often use it for
ephemeral fan-out (dashboards, presence, cache-invalidation signals) instead
of operating a second broker. Phase 2 adds `SubscribeAdapter` (SourcePort)
and `PublishAdapter` (SinkPort) via the existing `EventPattern`, mirroring
the MQTT/ZeroMQ adapters — same declaration, same codec validation, same
observer events, different transport semantics.

## Semantics — closer to ZeroMQ than MQTT (drives the whole design)

| Property | Redis pub/sub | MQTT | ZeroMQ PUB/SUB |
|---|---|---|---|
| Delivery | at-most-once, fire-and-forget | QoS 0/1/2 | at-most-once |
| Persistence / retained | none | retained messages | none |
| Offline subscribers | messages LOST | QoS 1+ queued | lost |
| Wildcards | glob via `PSUBSCRIBE` (`*`, `?`, `[...]`) | `+`/`#` | prefix match |
| Wildcard scope | `*` matches ACROSS `/` (multi-segment) | `+` single-level only | n/a |
| Ack | none | per-QoS | none |

Consequences to document loudly on every constructor:
- A subscriber that is down (or mid-reconnect) LOSES messages — go-redis's
  `PubSub` auto-reconnects but cannot replay. Use Redis Streams (Phase 3
  candidate) when at-least-once matters.
- No retained value: a late subscriber sees nothing until the next publish —
  pair with a Phase 1 `Seed`/`LatestPort` for "current state on connect".
- **Glob `*` is broader than MQTT `+`**: the derived pattern
  `"sensors/*/data"` also matches `"sensors/a/b/data"`. The subscribe
  adapter MUST re-match each inbound `Message.Channel` against the
  `{var}` template and silently DROP channels that don't fit the segment
  structure (mirrors MQTT's topic re-match before var extraction) —
  otherwise var extraction mis-parses. Literal `*`, `?`, `[` in a topic
  template must be escaped (`\*`) when deriving the PSUBSCRIBE pattern.

## Scope decisions

| In scope (Phase 2) | Out of scope |
|---|---|
| `SubscribeAdapter[T]` → `ports.SourceAdapter[T]` via `EventPattern` | Redis Streams / consumer groups (Phase 3 candidate — different semantics: persistent, at-least-once, XACK) |
| `PublishAdapter[T]` → `ports.SinkAdapter[T]` via `EventPattern` | Sharded pub/sub (`SSUBSCRIBE`, Redis 7 cluster) — revisit with a cluster use case |
| `PSUBSCRIBE` glob derivation from `{var}` topic templates | Keyspace notifications (`__keyspace@*__` — server-config-dependent; interesting for cache invalidation, needs its own review) |
| Narrow `PubSubCommands` interface + fake (same rule as Phase 1 `Commands`) | Pattern-free raw channel API (EventPattern is the declaration surface) |

Still deferred from Phase 1 (unchanged, no use case yet): `DelAdapter`,
per-var codecs on cache keys, spec rendering for `CachePattern`, client-side
caching (rueidis territory).

## Narrow interface extension — `PubSubCommands`

Phase 1's `Commands` stays untouched (cache ops only). Pub/sub gets its own
narrow interface — an implementation can satisfy either or both:

```go
// Message is one pub/sub delivery.
type Message struct {
    Channel string // concrete channel (useful under glob subscriptions)
    Payload []byte
}

// Subscription is a live subscription. Messages closes when the
// subscription is closed or the connection is permanently lost.
type Subscription interface {
    Messages() <-chan Message
    Close() error
}

// PubSubCommands is the narrow pub/sub surface. NewPubSubCommands adapts a
// go-redis UniversalClient (SUBSCRIBE for literal channels, PSUBSCRIBE when
// the pattern contains glob metacharacters).
type PubSubCommands interface {
    Publish(ctx context.Context, channel string, payload []byte) error
    Subscribe(ctx context.Context, pattern string) (Subscription, error)
}

func NewPubSubCommands(c goredis.UniversalClient) PubSubCommands
```

go-redis mapping: `client.Subscribe(ctx, ch)` / `client.PSubscribe(ctx, pat)`
→ `*redis.PubSub`, `pubsub.Channel()` → `<-chan *redis.Message`
(`Message.Payload` is a string — one copy to `[]byte`). `Publish` →
`client.Publish(ctx, ch, payload).Err()`.

## API surface

```go
// SubscribeAdapterOptions configures [SubscribeAdapter].
type SubscribeAdapterOptions struct {
    // ChannelPattern overrides the subscription pattern. When empty, derived
    // from the EventPattern topic by replacing each {var} placeholder with
    // the glob "*" (e.g. "sensors/{id}/data" -> "sensors/*/data") — the same
    // auto-derivation as mqtt.SubscribeAdapterOptions.TopicFilter.
    ChannelPattern string
    // Observer receives RecordSubscribe per message. Resolved from ctx when nil.
    Observer stats.Observer
}

// SubscribeAdapter returns a [ports.SourceAdapter] that subscribes to the
// EventPattern-derived channel pattern and decodes each message through the
// handle's format — the full events validation pipeline (topic vars, codec,
// observer), like mqtt/zeromq.SubscribeAdapter. Decode failures go to the
// port's Errors channel; messages during a reconnect window are LOST
// (at-most-once — documented, not fixable at this layer).
func SubscribeAdapter[T any](
    client PubSubCommands,
    handle *events.ChannelHandle[T],
    fmt format.Format[T],
    opts SubscribeAdapterOptions,
) ports.SourceAdapter[T]

// PublishAdapterOptions configures [PublishAdapter].
type PublishAdapterOptions struct {
    // OnError receives publish/encode errors (fire-and-forget transport —
    // there is no delivery error, only connection/encode errors).
    OnError func(error)
    // Observer receives RecordPublish per message. Resolved from ctx when nil.
    Observer stats.Observer
}

// PublishAdapter returns a [ports.SinkAdapter] that encodes each item through
// the handle's format and publishes it to the EventPattern-derived channel.
//
// varsFor extracts per-item topic-template vars (open decision 2, leaning
// ADOPTED: a constructor argument like Phase 1's keyFn and the file
// adapters' varsFor — a func(T) field cannot live in the non-generic
// options struct, and per-item vars beat the static Vars map that
// mqtt.MQTTDrainPublishOptions still carries as a documented limitation).
// Nil is valid for var-free topics.
func PublishAdapter[T any](
    client PubSubCommands,
    handle *events.ChannelHandle[T],
    varsFor func(T) map[string]string,
    fmt format.Format[T],
    opts PublishAdapterOptions,
) ports.SinkAdapter[T]
```

Declaration is the existing `EventPattern` — no new Pattern type:

```go
var Alerts = codex.Must(ports.NewSinkPort[Alert]("alerts", alertCodec, ports.PortOptions{}))
var AlertsPattern = ports.EventPattern{Topic: "alerts/{severity}"}
// main.go:
handle, _ := Alerts.PluginEventPattern(AlertsPattern)
Alerts.Bind(ctx, redis.PublishAdapter(pubsub, handle,
    func(a Alert) map[string]string { return map[string]string{"severity": a.Severity} },
    format.JSON(alertCodec), opts))
```

## Structured errors

Reuse Phase 1's shape — one new type, same conventions (`Error()`,
`Unwrap()`, `LogValue()`):

```go
// PubSubError wraps a publish or subscribe failure.
type PubSubError struct {
    Channel string // channel or pattern
    Op      string // "publish", "subscribe"
    Err     error
}
```

Decode failures on inbound messages reuse the events/codec error chain
(ValidationErrors → port Errors channel), mirroring mqtt.

## Observer integration

No new stats extension — pub/sub maps onto the EXISTING transport hooks
(the whole point of `RecordSubscribe`/`RecordPublish` being
transport-agnostic):

- `RecordSubscribe(channel, success, duration)` per inbound message
- `RecordPublish(channel, success, duration)` per outbound message
- Payload validation failures → `stats.ReportErrors(obs, "payload", err)`
- Inbound topic-var failures (`ChannelHandle.ValidateTopicVars` after
  extraction from `Message.Channel`) → location `"topic_var"` — same split
  as the mqtt adapter
- Nil observer → `stats.ObserverFromContext(ctx)`

## Unit test plan (fake `PubSubCommands`, no live Redis)

| ID | Test | Verifies |
|---|---|---|
| P1 | Subscribe → decode → port stream | happy path, full validation pipeline |
| P2 | Glob derivation | `"a/{x}/b"` → `"a/*/b"`; explicit ChannelPattern wins |
| P3 | Decode failure → Errors channel | typed error, per-field ReportErrors location `"payload"` |
| P3b | Topic-var extraction + validation | vars from `Message.Channel` via the template; codec failure → `"topic_var"` report + Errors channel |
| P3c | Multi-segment glob over-match dropped | `"sensors/*/data"` delivery on `"sensors/a/b/data"` silently dropped (template re-match) |
| P4 | Subscription closed → adapter returns | no goroutine leak, channels NOT closed by adapter (SourceAdapter contract) |
| P5 | Publish encodes + sends | fake captures channel + payload |
| P6 | Publish failure → OnError | PubSubError{Op:"publish"}, errors.As chain |
| P7 | PubSubError.LogValue | KindGroup + keys channel/op/err |
| P8 | Observer subscribe/publish paths | success AND failure, nil observer safe |
| P9 | Message on non-matching decode continues stream | one bad message ≠ terminated subscription |
| — | ExampleSubscribeAdapter / ExamplePublishAdapter | deterministic, fake-backed |

## Files to touch

| File | Change |
|---|---|
| `adapters/redis/pubsub.go` | `Message`, `Subscription`, `PubSubCommands`, `NewPubSubCommands` shim |
| `adapters/redis/binding.go` (or `pubsub_binding.go`) | `SubscribeAdapter`, `PublishAdapter` + options |
| `adapters/redis/errors.go` | `PubSubError` |
| `adapters/redis/*_test.go` | P1–P9 + Examples, fake PubSubCommands |
| `ports/source_port.go` / `sink_port.go` | add constructors to "Implemented by" godoc lists |
| `docs/features/redis.md` | new Pub/Sub section (+ semantics table) |
| instructions / review-skill / example | usual three-surface + R-history sync |

## Open design decisions

1. **Reconnect visibility** — go-redis's `PubSub` reconnects silently; should
   the adapter surface reconnect events (e.g. a `PubSubError{Op:"subscribe"}`
   on the Errors channel per drop) so consumers KNOW a gap happened?
   Leaning: yes — silent gaps are the worst failure mode of at-most-once.
   **Interface implication**: the `Subscription` sketch carries only
   `Messages()` — surfacing gaps needs either an `Events() <-chan error`
   channel on `Subscription` or go-redis's typed `*redis.Subscription`
   control messages mapped in the shim. Resolve together with this decision;
   the two-method sketch is the no-gap-visibility variant.
2. ~~Per-item topic vars on publish~~ — **RESOLVED (review pass, 2026-07-17)**:
   `varsFor func(T) map[string]string` as a CONSTRUCTOR argument (a generic
   func field cannot live in the non-generic options struct — same
   constraint that shaped Phase 1's `keyFn`). API surface updated above.
3. **One `Commands` or two interfaces** — keep `PubSubCommands` separate
   (leaning: yes — cache-only users shouldn't fake pub/sub methods) or merge
   into a single grown interface.
4. **Sharded pub/sub option** — add `Sharded bool` to options now (SSUBSCRIBE
   under cluster) or wait. Leaning: wait for a cluster use case.
5. **Glob escaping ownership** — escape literal `*?[` during pattern
   derivation in the adapter (leaning: yes, adapter-owned — templates are
   protocol-agnostic and must not know PSUBSCRIBE syntax), or reject such
   topics at Bind with a `PubSubError`.
