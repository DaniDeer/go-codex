# `adapters/amqp` — AMQP 0.9.1 (RabbitMQ) Adapter

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)

### Motivation

go-codex already supports MQTT 3.1.1, MQTT 5.0, and ZeroMQ. AMQP 0.9.1 (RabbitMQ) is the third major async transport used in industrial and enterprise workloads. An AMQP adapter follows the same declare → register → handle → adapt workflow as the existing adapters.

### Scope: AMQP 0.9.1 only

AMQP 0.9.1 (RabbitMQ) and AMQP 1.0 (Azure Service Bus, ActiveMQ Artemis) share a name but are **incompatible protocols** with entirely different models. They need separate packages:

- `adapters/amqp` — AMQP 0.9.1, using `github.com/rabbitmq/amqp091-go`
- `adapters/amqp1` — AMQP 1.0, using `github.com/Azure/go-amqp` (deferred to Phase 2)

### Library: `github.com/rabbitmq/amqp091-go`

The official RabbitMQ-maintained successor to the now-archived `github.com/streadway/amqp`. The API is nearly identical — migration is typically just an import path change. No CGO, pure Go.

### AMQP 0.9.1 topology model

| Concept | MQTT | AMQP 0.9.1 |
|---|---|---|
| Address unit | Topic string | Exchange + routing key |
| Consumer setup | Subscribe to topic | Declare queue → bind to exchange → consume |
| Broadcast | Retained topic / `#` | Fanout exchange |
| Point-to-point | Specific topic | Direct exchange + routing key |
| Pattern routing | MQTT wildcard | Topic exchange + `#`/`*` routing key patterns |
| Message ack | QoS 1 auto | Explicit `Ack`/`Nack` per message |
| Server queues | None | Named durable/exclusive/auto-delete queues |

AMQP requires three idempotent setup steps before any message can flow:

1. **`ExchangeDeclare`** — declare the exchange type (direct / fanout / topic / headers)
2. **`QueueDeclare`** — declare the consumer queue; returns server-assigned name for exclusive queues
3. **`QueueBind`** — bind queue to exchange with a routing key

`Subscribe` runs all three automatically. `Publish` only needs `ExchangeDeclare`.

### Proposed API surface

```go
// AMQPChannel wraps the amqp091.Channel methods used by this adapter.
// *amqp091.Channel satisfies this interface.
type AMQPChannel interface {
    ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp091.Table) error
    QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp091.Table) (amqp091.Queue, error)
    QueueBind(queue, key, exchange string, noWait bool, args amqp091.Table) error
    Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp091.Table) (<-chan amqp091.Delivery, error)
    Publish(exchange, key string, mandatory, immediate bool, msg amqp091.Publishing) error
    Ack(tag uint64, multiple bool) error
    Nack(tag uint64, multiple, requeue bool) error
    Close() error
}

// ExchangeBinding describes the topology for a channel.
// Pass in SubscribeOptions or PublishOptions (Phase 1).
// Phase 2: promote to events.ChannelOpt to flow into AsyncAPI spec.
type ExchangeBinding struct {
    Exchange     string       // exchange name; "" = default exchange
    ExchangeType ExchangeType // Direct | Fanout | Topic | Headers
    RoutingKey   string       // routing key; "" = use channel address
    Queue        QueueConfig  // consumer-side queue
    Durable      bool
    AutoDelete   bool
    VHost        string       // defaults to "/"
}

type QueueConfig struct {
    Name       string         // "" = server-assigned (for exclusive queues)
    Durable    bool
    Exclusive  bool
    AutoDelete bool
    Args       amqp091.Table  // x-dead-letter-exchange, x-message-ttl, etc.
}

type ExchangeType string
const (
    Direct  ExchangeType = "direct"
    Fanout  ExchangeType = "fanout"
    Topic   ExchangeType = "topic"
    Headers ExchangeType = "headers"
)

// Subscribe declares topology, binds queue, and starts a consume loop.
// Cancelling ctx stops the loop and unregisters the consumer.
// When AutoAck is false (default), the adapter Acks on fn success and
// Nacks (with requeue) on fn error.
func Subscribe[T any](
    ctx context.Context,
    ch AMQPChannel,
    handle *events.ChannelHandle[T],
    fn func(context.Context, T) error,
    opts SubscribeOptions,
    formats ...format.Format[T],
) error

// Publish encodes msg and publishes it to the exchange with the routing key.
// ExchangeDeclare runs lazily on first call (idempotent).
func Publish[T any](
    ctx context.Context,
    ch AMQPChannel,
    handle *events.ChannelHandle[T],
    msg T,
    vars map[string]string,
    opts PublishOptions,
    formats ...format.Format[T],
) error

type SubscribeOptions struct {
    Binding     ExchangeBinding
    OnError     func(SubscribeError)
    Observer    stats.Observer
    AutoAck     bool   // default false — manual ack/nack
    NackRequeue bool   // default true — requeue on handler error
    ConsumerTag string // "" = server-assigned
    PrefetchCount int  // Qos prefetch; 0 = no limit
}

type PublishOptions struct {
    Binding      ExchangeBinding
    Observer     stats.Observer
    Mandatory    bool
    ContentType  string
    Headers      amqp091.Table
    DeliveryMode byte // 1=transient, 2=persistent (default)
}
```

### Request/Reply pattern (`Serve` / `Call`)

AMQP 0.9.1 has native request/reply support via two message properties:

| AMQP property | Role | MQTT 5 equivalent |
|---|---|---|
| `Publishing.ReplyTo` | Queue name where the caller listens for the reply | `ResponseTopic` |
| `Publishing.CorrelationId` | Per-call UUID to match reply to request | `CorrelationData` |

The responder reads `delivery.ReplyTo` and publishes the response to the **default exchange** (`""`) with `RoutingKey = delivery.ReplyTo`. The default exchange routes to any queue whose name equals the routing key — a RabbitMQ guarantee.

#### Reply queue management

AMQP makes this simpler than MQTT5: instead of generating a UUID-based topic string, the caller declares an **exclusive, auto-delete, server-named queue** before publishing. RabbitMQ manages the queue lifetime — it is deleted automatically when the channel closes. No `ReplyTopicBuilder` equivalent is needed; the broker provides uniqueness.

```
Caller declares:  QueueDeclare("", durable=false, autoDelete=true, exclusive=true)
                  → returns Queue{Name: "amq.gen-Xyz..."} (server-assigned unique name)
Caller publishes: Publishing{ReplyTo: "amq.gen-Xyz...", CorrelationId: "uuid-42"}
                  → to the request exchange / routing key
Caller consumes:  Consume("amq.gen-Xyz...", autoAck=true)
                  → waits for delivery.CorrelationId == "uuid-42"

Responder reads:  delivery.ReplyTo = "amq.gen-Xyz..."
                  delivery.CorrelationId = "uuid-42"
Responder replies: Publish(exchange="", key="amq.gen-Xyz...", CorrelationId="uuid-42")
```

#### `Serve` and `Call` API

Mirrors `adapters/mqtt5` `Serve`/`Call` and uses `api/reqreply.RouteHandle` — the same handle works across AMQP, MQTT5, and ZeroMQ.

```go
// Serve consumes from the request queue, decodes each delivery, calls fn,
// encodes the response, and publishes it to delivery.ReplyTo.
// Topology setup (ExchangeDeclare + QueueDeclare + QueueBind) runs automatically.
// When fn or encoding fails, an error reply is published so the caller does not block.
func Serve[Req, Resp any](
    ctx context.Context,
    ch AMQPChannel,
    handle *reqreply.RouteHandle[Req, Resp],
    fn func(context.Context, Req) (Resp, error),
    opts ServeOptions,
) error

// Call encodes req, publishes it to the request exchange with ReplyTo and CorrelationId set,
// then waits for a matching reply on the auto-declared exclusive reply queue.
// On success, returns the decoded response.
// On timeout, returns CallError{Kind: KindTimeout}.
// On server error reply, returns CallError{Kind: KindHandler}.
func Call[Req, Resp any](
    ctx context.Context,
    ch AMQPChannel,
    handle *reqreply.RouteHandle[Req, Resp],
    req Req,
    opts CallOptions,
) (Resp, error)

type ServeOptions struct {
    // Binding describes the topology for the request queue.
    Binding     ExchangeBinding
    OnError     func(ServeError)
    Observer    stats.Observer
    // PrefetchCount limits concurrent in-flight requests. Default 1 (serialise).
    PrefetchCount int
}

type CallOptions struct {
    // Binding describes the exchange and routing key to publish the request to.
    Binding     ExchangeBinding
    Observer    stats.Observer
    Timeout     time.Duration        // default 30s
    Headers     amqp091.Table        // optional AMQP headers on outgoing request
    // Vars substitutes {varName} placeholders in the route topic before publishing.
    Vars        map[string]string
}

// ServeError — per-request decode/handler/encode failure on the responder side.
type ServeError struct {
    Kind       ErrorKind // KindDecode | KindHandler | KindEncode
    RoutingKey string
    Err        error
}
// LogValue emits: {kind, routing_key, err}

// CallError — caller-side failure: encode, timeout, server error, decode.
type CallError struct {
    Kind ErrorKind // KindEncode | KindTimeout | KindHandler | KindDecode
    Err  error
}
// LogValue emits: {kind, err}
```

`KindTimeout` and `KindEncode` added to `ErrorKind` for the request/reply path (not needed for PUB/SUB).

#### Error reply convention

When `fn` or encoding fails on the responder side, the responder must still publish a reply to `delivery.ReplyTo` — otherwise the caller blocks until timeout. Convention (matching ZeroMQ and MQTT5):

```
Error reply: amqp091.Publishing{
    Headers: amqp091.Table{"x-error": "true"},
    Body:    []byte(err.Error()),
    CorrelationId: delivery.CorrelationId,
}
```

The caller checks `delivery.Headers["x-error"]` and returns `CallError{Kind: KindHandler}`.

#### Observer integration for request/reply

```
Serve:  RecordRequest("AMQP-REP", routingKey, 200/0, dur)
Call:   RecordRequest("AMQP-REQ", routingKey, 200/0/500, dur)
```

Trace operations: `"amqp.serve"`, `"amqp.call"`.

#### Usage sketch — request/reply

```go
// Layer 2: declare route (same as MQTT5 / ZeroMQ)
var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute.add",
    computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
)

server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
server.AddServer("rabbitmq", reqreply.ServerEntry{URL: "amqp://localhost:5672", Protocol: "amqp"})
handle, _ := ComputeRoute.Register(server)

// Responder — runs in a goroutine
go amqpadapter.Serve(ctx, ch, handle,
    func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
        return ComputeResp{Sum: req.X + req.Y}, nil
    },
    amqpadapter.ServeOptions{
        Binding: amqpadapter.ExchangeBinding{
            Exchange:     "compute",
            ExchangeType: amqpadapter.Direct,
            RoutingKey:   "compute.add",
            Queue: amqpadapter.QueueConfig{
                Name:    "compute-add-workers",
                Durable: true,
            },
            Durable: true,
        },
        Observer:      obs,
        PrefetchCount: 4, // handle up to 4 concurrent requests
    },
)

// Caller
resp, err := amqpadapter.Call(ctx, ch, handle,
    ComputeReq{X: 3, Y: 4},
    amqpadapter.CallOptions{
        Binding: amqpadapter.ExchangeBinding{
            Exchange:     "compute",
            ExchangeType: amqpadapter.Direct,
            RoutingKey:   "compute.add",
            Durable:      true,
        },
        Timeout:  5 * time.Second,
        Observer: obs,
    },
)
// resp.Sum == 7
```

### Structured errors (all implement `slog.LogValuer`)

```go
type ErrorKind int
const (
    KindDecode  ErrorKind = iota // payload could not be decoded
    KindHandler                  // application handler returned error
    KindEncode                   // response/outgoing payload failed encode
    KindTimeout                  // no reply received within deadline (Call only)
    KindAck                      // Delivery.Ack/Nack failed after successful handler
)

// SubscribeError — per-message failure during consume loop.
// Carries exchange, queue, and routing key for correlation with topology.
type SubscribeError struct {
    Kind       ErrorKind
    Exchange   string
    Queue      string
    RoutingKey string
    Err        error
}
// LogValue emits: {kind, exchange, queue, routing_key, err}

// PublishEncodeError — payload encode failure before the message is sent.
type PublishEncodeError struct {
    Exchange   string
    RoutingKey string
    Err        error
}
// LogValue emits: {exchange, routing_key, err}

// BrokerError — AMQP infrastructure failure (topology setup or publish/consume).
type BrokerError struct {
    Op  string // "exchange_declare" | "queue_declare" | "queue_bind" | "consume" | "publish"
    Err error
}
// LogValue emits: {op, err}
```

`KindAck` is AMQP-specific: when `Delivery.Ack()` or `Nack()` fails *after* `fn` succeeds, the message may be redelivered. The adapter logs this at Warn level but does not propagate it as a fatal error.

### Observer integration

```
PUB/SUB:
  RecordSubscribe(queue, success, dur)                        — per consumed message
  RecordPublish(exchange+"/"+routingKey, success, dur)        — per published message
  RecordValidationError("payload", constraint, field)         — per codec field failure

Request/Reply:
  RecordRequest("AMQP-REP", routingKey, 200/0, dur)           — Serve: per request handled
  RecordRequest("AMQP-REQ", routingKey, 200/0/500, dur)       — Call: per call made
  RecordValidationError("body", constraint, field)            — Serve: payload decode failure
```

Trace operations (type-asserted `stats.TraceObserver`):
- PUB/SUB: `"amqp.subscribe"`, `"amqp.publish"`
- Request/Reply: `"amqp.serve"`, `"amqp.call"`

### Security/Middleware dispatch

AMQP plugs into `api/events`/`api/reqreply`'s EXISTING `Channel`/`Route`
`Register` flow (same pattern `adapters/mqtt5`/`adapters/zeromq`/
`adapters/mqtt` already use). It should dispatch against whichever
mechanism
[`docs/design/d-0003-codec-declared-middlewares.md's Addendum 7`](../design/d-0003-codec-declared-middlewares.md)
describes as CURRENT at implementation time (`Middleware[In,Out]`
reusable class, or its `BoundMiddleware`/`BoundSubscribeMiddleware`/
`BoundPublishMiddleware` bound classes) — not a hand-rolled, adapter-
specific mechanism. AMQP 0.9.1's `BasicProperties.Headers` is a genuine
property/header side-channel (unlike ZeroMQ's complete absence of one),
so AMQP's real Security needs almost certainly resolve via the REUSABLE
class alone (a property-decoded `In`, discarding `*T`) — mirroring
`adapters/mqtt5`'s pattern, not `adapters/zeromq`'s narrow in-payload
credential case (the one confirmed genuine use for the bound class
today).

#### Credential-format validation — build the shared `internal/middleware` core AMQP needs, don't hand-roll a 3rd private copy

A cross-cutting review (originally the now-retired `shared-api-layer-mechanics.md` roadmap's
Phase 5 investigation — its full record, including this finding, was folded into
[`docs/design/d-0009-internalize-shared-mechanics.md`](../design/d-0009-internalize-shared-mechanics.md)'s
Addendum when that roadmap doc was retired) found that `api/rest` has a shared,
transport-agnostic credential-format-validation core (`rest.ValidateSecurityCredentials` + a
`CredentialExtractor func(location, name string) string` abstraction in
`api/rest/security_dispatch.go`) that EVERY REST adapter (nethttp/chi/websocket/mcprest) reuses
for free — but `api/events`/`api/reqreply` have NO equivalent. `adapters/mqtt5/security.go` has
its OWN private `validateSecurityCredentials`, built directly around `pahomqtt5.UserProperties`
with no extractor-interface abstraction — fine for mqtt5 alone, but AMQP would otherwise have to
write a THIRD, independently-hand-rolled copy of the same format-validation logic (mirroring
exactly the kind of "same thing reinvented per adapter" pattern
`docs/roadmap/adapter-dispatch-unification.md` already warns about in a related context).

**This roadmap now explicitly includes building that shared core as part of AMQP's OWN security
design** — not deferred to a separate, generic-first roadmap with no concrete consumer to
validate it against (the sequencing the maintainer explicitly chose: design this unification
AS PART OF the AMQP adapter work, where it has a real second data point — mqtt5's user-properties
model plus AMQP's `BasicProperties.Headers` model — rather than generalizing from mqtt5 alone).

Per [D-0009](../design/d-0009-internalize-shared-mechanics.md)'s now-established convention
(a mechanism genuinely shared across 2+ of `api/rest`/`api/events`/`api/reqreply` that is pure
MECHANICS, not a pattern-specific concern, belongs in `internal/middleware` with each pattern
exposing a thin forwarding wrapper — exactly how `CheckScopes`/`SecurityDeclaration` and the
Error-Pattern client helpers already shipped), `events`' and `reqreply`'s validation logic would
be genuinely identical (both just need a `PropertyExtractor` abstraction over their own
message-property side-channel) — so this is designed as ONE `internal/middleware` core, not two
independently-hand-written copies:

Sketch (to be finalized once AMQP's own credential-location conventions are settled):

```go
package middleware // internal/middleware

// PropertyExtractor supplies a raw credential string from wherever an
// adapter's concrete message type stores it — name is the user-property/
// header key to read. Returns "" when absent. Mirrors rest.CredentialExtractor's
// role exactly: this abstraction is what lets ValidateSecurityCredentials
// live in internal/middleware with ZERO transport-specific import, the
// same way rest.ValidateSecurityCredentials has zero net/http import.
type PropertyExtractor func(name string) string

// ValidateSecurityCredentials extracts credentials via extract and
// validates them against the registered SecurityScheme codecs for the
// declared requirements — same contract as rest.ValidateSecurityCredentials.
func ValidateSecurityCredentials(extract PropertyExtractor, reqs []route.SecurityRequirement, schemes map[string]SecurityScheme) error
```

```go
package events // thin forwarding wrapper — reqreply gets the same shape

func ValidateSecurityCredentials(extract middleware.PropertyExtractor, reqs []SecurityRequirement, schemes map[string]SecurityScheme) error {
    return middleware.ValidateSecurityCredentials(extract, reqs, schemes)
}
```

`adapters/amqp` would build its `PropertyExtractor` from `amqp091.Delivery.Headers`/
`amqp091.Publishing.Headers` (AMQP's own property/header side-channel, directly analogous to
mqtt5's `UserProperties`); `adapters/mqtt5` COULD be migrated onto the same shared core in the
same change (low risk — it already has full test coverage) or left as-is and migrated later,
whichever is lower-friction when this work actually starts. The tiny, confirmed 3-way-duplicate
`SecurityScheme{route.SecurityScheme; Codec}` struct (`rest`/`events`/`reqreply` all have a
byte-identical copy) should be folded into whichever shared location this work settles on, at
the same time — too small to deserve its own design effort.

This section supersedes the now-retired `shared-api-layer-mechanics.md`'s own Phase 5, which
is documented in full (alongside Phases 1-4) in
[D-0009's Addendum](../design/d-0009-internalize-shared-mechanics.md) rather than duplicated
here.

### AsyncAPI spec

The AsyncAPI AMQP binding spec (v0.3.0) uses a `bindings.amqp` block on channels:

```yaml
channels:
  sensorReadings:
    address: "sensors.readings"    # routing key
    bindings:
      amqp:
        is: routingKey             # "routingKey" | "queue"
        exchange:
          name: sensor-events
          type: topic              # direct | fanout | topic | headers
          durable: true
          autoDelete: false
          vhost: /
        queue:
          name: sensor-readings-consumer
          durable: true
          exclusive: false
          autoDelete: false
          vhost: /
        bindingVersion: 0.3.0
```

**Phase 1** (initial implementation): topology described in `ChannelMeta.Description` prose. `ExchangeBinding` lives in `SubscribeOptions`/`PublishOptions` only — no render layer changes.

**Phase 2**: promote `ExchangeBinding` to `events.ChannelOpt`; extend `render/asyncapi/v3` to emit `bindings.amqp`; full spec round-trip from `events.NewChannel`.

### Files to create

| File | Contents |
|---|---|
| `adapters/amqp/adapter.go` | `AMQPChannel` interface, `Subscribe`, `Publish`, topology helpers, `SubscribeOptions`, `PublishOptions` |
| `adapters/amqp/reqreply.go` | `Serve`, `Call`, `ServeOptions`, `CallOptions`, reply queue management |
| `adapters/amqp/errors.go` | `BrokerError`, `SubscribeError`, `PublishEncodeError`, `ServeError`, `CallError`, `ErrorKind` |
| `adapters/amqp/doc.go` | Package overview |
| `adapters/amqp/adapter_test.go` | PUB/SUB tests with mock `AMQPChannel` |
| `adapters/amqp/reqreply_test.go` | Request/reply tests with mock `AMQPChannel` |

### Usage sketch

```go
import (
    amqpadapter "github.com/DaniDeer/go-codex/adapters/amqp"
    amqp091 "github.com/rabbitmq/amqp091-go"
    "github.com/DaniDeer/go-codex/api/events"
)

// Layer 1: declare codec
var SensorReadingCodec = codex.Struct[SensorReading](...)

// Layer 2: declare channel (same as MQTT)
var SensorChannel, _ = events.NewChannel[SensorReading](
    "sensors.readings",
    SensorReadingCodec,
    events.Subscribe{Summary: "Sensor readings from the field."},
    events.Publish{Summary: "Publish a sensor reading."},
).Register(eventsBuilder)

// Layer 3: connect and adapt
conn, _ := amqp091.Dial("amqp://guest:guest@localhost:5672/")
ch, _ := conn.Channel()

// Subscribe — topology setup runs automatically
err := amqpadapter.Subscribe(ctx, ch, SensorChannel,
    func(ctx context.Context, r SensorReading) error {
        return store.Save(ctx, r)
    },
    amqpadapter.SubscribeOptions{
        Binding: amqpadapter.ExchangeBinding{
            Exchange:     "sensor-events",
            ExchangeType: amqpadapter.Topic,
            RoutingKey:   "sensors.#",
            Queue: amqpadapter.QueueConfig{
                Name:    "sensor-readings-consumer",
                Durable: true,
            },
            Durable: true,
        },
        Observer: obs,
        OnError:  func(e amqpadapter.SubscribeError) { slog.Warn("subscribe error", "error", e) },
    },
)

// Publish — exchange declared lazily
err = amqpadapter.Publish(ctx, ch, SensorChannel, reading, nil,
    amqpadapter.PublishOptions{
        Binding: amqpadapter.ExchangeBinding{
            Exchange:     "sensor-events",
            ExchangeType: amqpadapter.Topic,
            RoutingKey:   "sensors.readings",
            Durable:      true,
        },
        Observer: obs,
    },
)
```

---
