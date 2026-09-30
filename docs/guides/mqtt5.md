# MQTT 5 Examples

> See also: [`adapters/mqtt5` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt5) · [`api/reqreply`](../concepts/api-contracts.md) · [`api/events`](../concepts/api-contracts.md) · [Feature: Metrics Observer](../features/observer.md) · [Feature: Protocol-Native Capabilities](../features/capabilities.md) (QoS/Retained) · [MQTT 3.1.1 Examples](mqtt.md)
>
> **Runnable demo**: [`examples/events-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api) — a multi-adapter project covering mqtt5 alongside mqtt v3/zeromq. `demo_client_attach_workflow.go` leads with the PREFERRED `Client.Attach` + `Client.Publish`/`.Subscribe` workflow (spec printed for free from the same client); `demo_user_property_middleware.go` showcases the handle-based escape hatch for User Properties, UserPropertyParam validation, and ContentType auto-format; `demo_connect_level_security.go` covers `mqtt5.NewSecuredClient`. Request-Reply lives in its own dedicated project: [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api).

`adapters/mqtt5` provides codec-backed adapters for **MQTT 5.0** using the [`paho.golang`](https://github.com/eclipse/paho.golang) library. It follows the same **declare → register → handle → adapt** pattern as `adapters/mqtt`, `adapters/nethttp`, and `adapters/zeromq`.

## MQTT 5.0 vs 3.1.1 — what's new

| Feature | MQTT 3.1.1 (`adapters/mqtt`) | MQTT 5.0 (`adapters/mqtt5`) |
|---|---|---|
| PUB/SUB | ✅ | ✅ (unchanged API) |
| Request-Reply | ❌ | ✅ `reqreply.Client`/`.Server` `Attach` workflow (see [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api)) |
| User Properties | ❌ `validateSecurityCredentials` no-op | ✅ Per-message key-value metadata |
| Content-Type | ❌ Format agreed out-of-band | ✅ Auto format selection from message property |
| Message Expiry | ❌ | Phase 2 |
| Shared Subscriptions | ❌ | ✅ via `SharedReplyTopic` builder |

---

## Prerequisites

### Install the library

`adapters/mqtt5` uses `github.com/eclipse/paho.golang` — **pure Go**, no CGO required:

```bash
go get github.com/eclipse/paho.golang
```

### Broker setup (Mosquitto)

MQTT 5.0 requires broker support. Enable it in `mosquitto.conf`:

```
listener 1883
allow_anonymous true
# MQTT 5.0 is on by default in Mosquitto 2.x
```

Start:
```bash
mosquitto -c mosquitto.conf
```

### Client setup

`paho.golang` uses a lower-level API than paho.mqtt.golang v1: you create a `*paho.Client` from a `net.Conn`:

```go
import (
    "net"
    "github.com/eclipse/paho.golang/paho"
)

conn, err := net.Dial("tcp", "localhost:1883")
router := paho.NewStandardRouter()
client := paho.NewClient(paho.ClientConfig{
    Conn:   conn,
    Router: router,
    OnClientError:      func(err error) { log.Error("client error", "err", err) },
    OnServerDisconnect: func(d *paho.Disconnect) { log.Warn("disconnected") },
})

// Connect
if _, err := client.Connect(ctx, &paho.Connect{
    KeepAlive:  60,
    ClientID:   "my-service",
    CleanStart: true,
}); err != nil {
    log.Fatal(err)
}
```

---

## PUB/SUB — unchanged from MQTT 3.1.1

The `api/events.NewChannel` declaration is **identical**. Only the adapter import and library change:

```go
import (
    "context"

    mqtt5adapter "github.com/DaniDeer/go-codex/adapters/mqtt5"
    "github.com/DaniDeer/go-codex/api/events"
    "github.com/DaniDeer/go-codex/stats"
)

// NewSubscribeTransport/NewPublishTransport — the spec-free, no-*Client-needed,
// handle-based call surface Decision 7 inverted into api/events itself
// (docs/design/d-0002-pubsub-workflow-simplification.md). Fully typed generic
// constructors, no reflection; use these for custom OnError/Observer/security
// impls or wildcard topics. The simple case uses Client.Attach/.Subscribe below
// instead.
subTransport := mqtt5adapter.NewSubscribeTransport[SensorReading](client, router,
    mqtt5adapter.SubscribeOptions{Observer: obs})

sub := contract.ReadingsChannel.WithSubscribe(events.Subscribe{})
if err := events.SubscribeHandle(ctx, sub, subTransport,
    func(ctx context.Context, r SensorReading) error {
        return store.Save(ctx, r)
    },
); err != nil {
    log.Fatal(err)
}

// Publish
pubTransport := mqtt5adapter.NewPublishTransport[SensorReading](client,
    mqtt5adapter.PublishOptions[SensorReading]{
        Observer:    obs,
        ContentType: "application/json", // sets MQTT 5 ContentType property
        UserProperties: []mqtt5adapter.UserProperty{
            {Key: "TenantID", Value: "acme"},
        },
    },
)
pub := contract.ReadingsChannel.WithPublish(events.Publish{})
err := events.PublishHandle(ctx, pub, pubTransport, reading)
```

`events.PublishHandle`/`events.SubscribeHandle` + each adapter's `NewPublishTransport[T]`/
`NewSubscribeTransport[T]` are the spec-free, handle-based call surface Decision 7 of
`docs/design/d-0002-pubsub-workflow-simplification.md` inverted into `api/events` itself (mirroring
`Client.Attach`'s own inversion). The OLD per-adapter `SubscribeWithHandle`/`Publish`/
`PublishHandle` primitives (once kept public as a Decision 6 exception) are now unexported
(`subscribeWithHandle`/`publish`/`publishHandle`) — their logic lives inside each transport's
`Subscribe`/`Publish` method. Every OTHER lower-level call-time primitive (`Caller`/`NewCaller`,
the `*Caller`-based `Subscribe` convenience, `ServeOneSubscriber`, `NewPublisherFor`/
`PublisherFor` — REMOVED entirely) is now unexported or deleted; alongside this handle-based
workflow, the transport-agnostic, application-facing `Client.Attach` +
`Client.Publish`/`.Subscribe`/`.ServeSubscribers` workflow (Decision 5) remains equally valid, see
below.

### `Client.Attach` — the inverted-control workflow

`client.Attach(mqtt5.NewTransport(mqtt5.TransportOptions{Client: mqttClient, Router: router}))` binds mqttClient+router to `client` as its
`events.Transport` — the "attach the adapter to the client" step. From there, call
`client.Publish`/`client.Subscribe` directly on the `*events.Client` value itself:

```go
client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
if err := client.Attach(mqtt5.NewTransport(mqtt5.TransportOptions{Client: mqttClient, Router: router})); err != nil {
    log.Fatal(err)
}

sub := contract.ReadingsChannel.WithSubscribe(events.Subscribe{})
pub := contract.ReadingsChannel.WithPublish(events.Publish{})

go func() {
    _ = client.Subscribe(ctx, sub, func(ctx context.Context, r SensorReading) error {
        log.Printf("received: %+v", r)
        return nil
    })
}()

err := client.Publish(ctx, pub, reading) // "one struct, one call"
```

Since `Client.Publish`/`Client.Subscribe` are ordinary Go methods (not generic — Go forbids a
method from introducing its own type parameters), arguments are passed as `any` and their
concrete types are recovered internally via reflection; a mismatch surfaces as
`events.TransportTypeMismatchError` at CALL time. See
`docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 5 for the full design.
`Client.Publish`/`Client.Subscribe` are FULL-FEATURED for this adapter (`docs/roadmap/
capability-requirement-composition.md`'s Phase 4e closed the former "v1 scope" narrowing):
declared Capabilities, per-call format overrides (`events.ClientPublishOptions`/
`events.ClientSubscribeOptions`), declarative SubscribeMW/PublishMW security enforcement,
codec-backed Middleware/Transform dispatch, and a declared `OnError` callback are all honored
— there is no remaining reason to reach for `mqtt5.NewSubscribeTransport`/
`mqtt5.NewPublishTransport` directly except the general escape-hatch case.

### AsyncAPI spec

Use the existing `api/events.Client` with `Protocol: "mqtt5"` — no changes needed:

```go
eventsClient := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
eventsClient.AddServer("mqtt5", events.Server{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
handle, _ := ReadingsChannel.WithSubscribe(events.Subscribe{}).Handle(eventsClient)
spec, _ := eventsClient.AsyncAPISpec()
```

---

## Request-Reply (MQTT 5 only)

MQTT 5.0 introduces `ResponseTopic` and `CorrelationData` message properties, enabling typed request-reply over pub/sub infrastructure.

> **Preferred workflow**: `reqreply.NewServer()`/`reqreply.NewClient()` +
> `mqtt5adapter.NewServerTransport`/`NewClientTransport` mirror `events.Client`'s
> `Attach` + `.Publish`/`.Subscribe` workflow — one `Server.Attach`/`Client.Attach`
> call, then plain `Client.Call`/`Client.CallAsync` and `Server.Serve`, no further
> `mqtt5adapter.*` calls needed at the call site. Per-route customization (custom
> security implementation Fn, per-call `Observer` overrides, non-default
> `ReplyTopicPrefix`/`Timeout`) is configured via `ServerTransportOptions.Serve`/
> `ClientTransportOptions.Call` at attach time. See
> [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api)
> for the full `Attach`-based workflow, dual-mode `Client.Call`, concurrent
> multi-route dispatch, `CallAsync`/`Future`, and AsyncAPI spec printing.

**How it works:**
1. Requester generates a unique reply topic: `replies/<uuid>`
2. Requester publishes to the service topic with `ResponseTopic=replies/<uuid>` and `CorrelationData`
3. Responder subscribes to the service topic, calls the handler, and publishes the reply to `ResponseTopic`
4. Requester receives the reply (matched by `CorrelationData`) and returns the decoded value

### Route declaration (shared contract — same as ZMQ)

```go
// Static topic — no template variables.
var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add",
    computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
)

// Template topic — {tenantID} is validated AND auto-merged into/from
// ComputeReq.TenantID via NewTopicParam (assumes ComputeReq has a
// TenantID string field) — the client derives the topic from the
// request struct automatically; the server receives it already merged.
var TenantComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/{tenantID}/add",
    computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
    reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
        func(r ComputeReq) string { return r.TenantID },
        func(r *ComputeReq, v string) { r.TenantID = v },
    ),
)
```

`reqreply.TopicParam` mirrors `events.TopicParam` for MQTT channel subscriptions — same field structure, same `.WithCodec(c)` method, same error types.

### Preferred: `Server`/`Client` + `Attach`

```go
// Server side: WithHandler + Register is ONE fluent chain — the route is
// dispatchable the moment it's registered, no separate Handle step.
server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
server.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
handle, err := ComputeRoute.WithHandler(func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
    return ComputeResp{Sum: req.X + req.Y}, nil
}).Register(server)
if err != nil {
    log.Fatal(err)
}
if err := server.Attach(mqtt5adapter.NewServerTransport(mqtt5adapter.ServerTransportOptions{Client: client, Router: router})); err != nil {
    log.Fatal(err)
}
go server.Serve(ctx) // dispatches every registered route concurrently, blocks until ctx is cancelled

// Client side: Attach once, then plain Call/CallAsync — no *RouteHandle needed
// for a raw Route call (GlobalSecurity is invisible in that mode, same
// accepted limitation as REST's own Route.ClientHandle()).
reqreplyClient := reqreply.NewClient()
if err := reqreplyClient.Attach(mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{Client: client, Router: router})); err != nil {
    log.Fatal(err)
}
respAny, err := reqreplyClient.Call(ctx, ComputeRoute, ComputeReq{X: 3, Y: 4})
resp := respAny.(ComputeResp)

// Async: CallAsync returns a *reqreply.Future[ComputeResp] immediately;
// resolve it later, from a different call site if needed.
futureAny, err := reqreplyClient.CallAsync(ctx, ComputeRoute, ComputeReq{X: 10, Y: 20})
future := futureAny.(*reqreply.Future[ComputeResp])
// ... do other independent work ...
resp2, err := future.Wait(ctx)
```

Use an already-registered `*RouteHandle` (instead of the raw `Route`) with
`Client.Call`/`CallAsync` when `GlobalSecurity` needs to be visible and
enforced client-side — see [`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api)'s
dual-mode demo for the side-by-side contrast.

### Per-route/per-call customization

There is no separate lower-level escape hatch anymore — `NewServerTransport`/
`NewClientTransport` are the SOLE entry points (docs/roadmap/
capability-requirement-composition.md's "zero backdoor between the api
layer and the adapters" directive). Customize dispatch (a security
implementation Fn, `Observer` overrides, non-default
`ReplyTopicPrefix`/`Timeout`/`ReplyTopicBuilder`) via
`ServerTransportOptions.Serve`/`ClientTransportOptions.Call` at attach time
— configuration applies uniformly to every route dispatched through that
transport.

### Responder

```go
transport := mqtt5adapter.NewServerTransport(mqtt5adapter.ServerTransportOptions{
    Client: client, Router: router,
    Serve: mqtt5adapter.ServeOptions{Observer: obs},
})
if err := server.Attach(transport); err != nil {
    log.Fatal(err)
}
go server.Serve(ctx)
```

### Caller

```go
transport := mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{
    Client: client, Router: router,
    Call: mqtt5adapter.CallOptions{
        ReplyTopicPrefix: "replies",    // generates: "replies/<uuid>"
        Timeout:          5 * time.Second,
        Observer:         obs,
    },
})
if err := reqreplyClient.Attach(transport); err != nil {
    log.Fatal(err)
}

// Static topic — no template vars involved.
respAny, err := reqreplyClient.Call(ctx, ComputeRoute, ComputeReq{X: 3, Y: 4})
if err != nil {
    var reqErr mqtt5adapter.CallError
    if errors.As(err, &reqErr) && reqErr.Kind == mqtt5adapter.KindTimeout {
        log.Warn("request timed out")
    }
}
resp := respAny.(ComputeResp)

// Template topic — TenantComputeRoute's NewTopicParam merge field derives
// the topic from the request struct automatically; no separate vars
// argument needed. Setting req.TenantID is enough:
respAny, err = reqreplyClient.Call(ctx, TenantComputeRoute, ComputeReq{X: 3, Y: 4, TenantID: "acme"})
// On validation failure: CallError wrapping reqreply.RouteParamError
// or reqreply.MissingRouteParamError — both errors.As-navigable.
```

### Custom reply topics

By default, `Call` generates `"replies/<uuid>"` for both the MQTT 5 `ResponseTopic` property and the broker subscription. Use `ReplyTopicBuilder` in `ClientTransportOptions.Call` to override this with a built-in constructor or a custom function.

```go
// Built-in default — explicit form (identical to not setting ReplyTopicBuilder)
transport := mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{
    Client: client, Router: router,
    Call: mqtt5adapter.CallOptions{
        ReplyTopicBuilder: mqtt5adapter.UUIDReplyTopic("replies"),
    },
})

// Shared subscription — scale reply consumers horizontally.
// The ResponseTopic sent to the responder is "replies/<uuid>" (plain publish topic).
// The local subscribe uses "$share/gateway-pool/replies/<uuid>".
// The broker delivers each reply to exactly one subscriber in the group.
transport = mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{
    Client: client, Router: router,
    Call: mqtt5adapter.CallOptions{
        ReplyTopicBuilder: mqtt5adapter.SharedReplyTopic("replies", "gateway-pool"),
    },
})

// Fully custom builder — client-ID + monotonic counter, no uuid dependency.
var seq int64
transport = mqtt5adapter.NewClientTransport(mqtt5adapter.ClientTransportOptions{
    Client: client, Router: router,
    Call: mqtt5adapter.CallOptions{
        ReplyTopicBuilder: func() (string, string) {
            t := fmt.Sprintf("replies/gw-1/%d", atomic.AddInt64(&seq, 1))
            return t, t
        },
    },
})
```

**`ReplyTopicBuilder` contract:**
- Returns `(responseTopic, subscribeFilter string)`.
- `responseTopic` — written into the MQTT 5 `ResponseTopic` property; must be a plain publish topic (no wildcards, no `$share` prefix).
- `subscribeFilter` — passed to `client.Subscribe`; for shared subscriptions it carries the `$share/<group>/` prefix.
- Return equal strings for regular (non-shared) subscriptions.
- Empty `subscribeFilter` falls back to `responseTopic`.
- Empty `responseTopic` returns `CallError{Kind: KindEncode}`.

---

### AsyncAPI spec for request-reply

Use `api/reqreply.Server` (transport-agnostic — the same server works for ZMQ):

```go
server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
server.AddServer("mqtt5", reqreply.ServerEntry{URL: "mqtt://broker:1883", Protocol: "mqtt5"})
handle, _ := ComputeRoute.Register(server)

doc, _ := server.AsyncAPISpec()  // AsyncAPI 3.0 with reply: block
```

---

## User Properties for authentication

MQTT 5.0 User Properties expose per-message key-value pairs. Attach a
security-shaped Fn via `Subscriber.SubscribeMW` for runtime authentication
(the `SubscribeOptions.SecurityFunc` field was removed — see
[Feature: Security & Auth](../features/security.md)):

```go
scheme := route.SecurityScheme{Type: "http", Scheme: "bearer"}
sub := contract.ReadingsChannel.WithSubscribe(events.Subscribe{}).
    SubscribeMW(events.FromSecurityScheme("bearerAuth", scheme, nil),
        func(ctx context.Context, msg *paho.Publish, r *SensorReading) (map[string][]string, error) {
            for _, p := range msg.Properties.User {
                if p.Key == "Authorization" {
                    return verifyJWT(strings.TrimPrefix(p.Value, "Bearer "), []route.SecurityRequirement{{"bearerAuth": nil}})
                }
            }
            return nil, errors.New("missing Authorization User Property")
        })

transport := mqtt5adapter.NewSubscribeTransport[SensorReading](client, router,
    mqtt5adapter.SubscribeOptions{})
err := events.SubscribeHandle(ctx, sub, transport, fn)

// Access User Properties inside the handler:
func(ctx context.Context, r SensorReading) error {
    props, ok := mqtt5adapter.UserPropertiesFromContext(ctx)
    if ok {
        tenantID := ""
        for _, p := range props {
            if p.Key == "TenantID" {
                tenantID = p.Value
            }
        }
    }
    return nil
}
```

---

## User Property codec validation

`UserPropertyParam` lets you validate MQTT 5 User Properties with codecs — the same mechanism as `rest.HeaderParam` for HTTP request headers. Define params in `SubscribeOptions.UserPropertyParams` (or `ServeOptions.UserPropertyParams` for request-reply responders).

```go
transport := mqtt5adapter.NewSubscribeTransport[SensorReading](client, router,
    mqtt5adapter.SubscribeOptions{
        UserPropertyParams: []mqtt5adapter.UserPropertyParam{
            // Required bearer token — validated with a codec:
            mqtt5adapter.UserPropertyParam{Name: "Authorization", Required: true}.
                WithCodec(codex.String().Refine(validate.BearerToken)),
            // Optional tenant ID — present must be non-empty:
            mqtt5adapter.UserPropertyParam{Name: "TenantID", Required: false}.
                WithCodec(codex.String().Refine(validate.NonEmptyString)),
        },
    })
err := events.SubscribeHandle(ctx, sub, transport, fn)
```

**Validation order** for each incoming message:
1. User Property params validated (before the security-shaped SubscribeMW Fn)
2. Security-shaped SubscribeMW Fn called (if the channel has security requirements)
3. Payload decoded
4. fn called

Missing required property → `SubscribeError{Kind: KindSecurity}` wrapping `MissingUserPropertyError{Name}`.
Codec failure → `SubscribeError{Kind: KindSecurity}` wrapping `UserPropertyError{Name, Value, Err}`.
Both are `errors.As`-navigable and implement `slog.LogValuer`.

```go
// Error handling:
opts.OnError = func(e mqtt5adapter.SubscribeError) {
    var missing mqtt5adapter.MissingUserPropertyError
    if errors.As(e, &missing) {
        slog.Warn("required property absent", "name", missing.Name)
        return
    }
    var propErr mqtt5adapter.UserPropertyError
    if errors.As(e, &propErr) {
        slog.Warn("property validation failed", "error", propErr)
        return
    }
}
```

Per-property validation errors are also reported via `obs.RecordValidationError("user_property", constraintName, propertyName)`.

---

## Content-Type auto format selection

When a message carries a ContentType property, the adapter auto-selects the matching format from the provided `formats` slice by comparing `format.Format.ContentType()`. No manual content-type switching needed:

```go
transport := mqtt5adapter.NewSubscribeTransport[SensorReading](client, router,
    mqtt5adapter.SubscribeOptions{},
    format.JSON(sensorCodec),   // ContentType: "application/json"
    format.YAML(sensorCodec),   // ContentType: "application/yaml"
)
err := events.SubscribeHandle(ctx, sub, transport, fn)
```

When the incoming message has `ContentType: "application/yaml"`, the YAML format is used automatically.

---

## Observer integration

All four instrument the full observer chain:

```go
obs := stats.NewFanout(
    metricsObserver,
    stats.NewLoggingObserver(slog.Default()),
    tracer,
)

subTransport := mqtt5adapter.NewSubscribeTransport[SensorReading](client, router, mqtt5adapter.SubscribeOptions{Observer: obs})
err := events.SubscribeHandle(ctx, sub, subTransport, fn)

pubTransport := mqtt5adapter.NewPublishTransport[SensorReading](client, mqtt5adapter.PublishOptions[SensorReading]{Observer: obs})
err = events.PublishHandle(ctx, pub, pubTransport, msg)

mqtt5adapter.Serve(ctx, client, router, handle, fn, mqtt5adapter.ServeOptions{Observer: obs})
mqtt5adapter.Call(ctx, client, router, handle, req, mqtt5adapter.CallOptions{Observer: obs})
```

| Event | Observer method | Trace op |
|---|---|---|
| Message received (success) | `RecordSubscribe(topic, true, dur)` | `"mqtt5.subscribe"` |
| Message received (failure) | `RecordSubscribe(topic, false, dur)` | |
| Message published | `RecordPublish(topic, success, dur)` | `"mqtt5.publish"` |
| REP request processed | `RecordRequest("MQTT5-REP", path, status, dur)` | `"mqtt5.serve"` |
| REQ call completed | `RecordRequest("MQTT5-REQ", path, status, dur)` | `"mqtt5.request"` |
| Security rejection | `RecordSecurityRejection(topic, scheme)` | |

---

## Error handling

All errors implement `Unwrap()` and `slog.LogValuer`:

Use this guide's section for MQTT5-specific typed errors, and the unified map in
[Guide: Error Handling](error-handling.md#where-to-handle-errors-adapters-ports-pipelines)
for when to handle at adapter callback vs port/stream drain points.

```go
// Subscribe / Serve — delivered to OnError callback
var subErr mqtt5.SubscribeError
if errors.As(err, &subErr) {
    switch subErr.Kind {
    case mqtt5.KindDecode:    // payload validation failed
    case mqtt5.KindHandler:   // application handler error
    case mqtt5.KindSecurity:  // SecurityFunc rejected the message
    }
    slog.Warn("subscribe failed", "error", subErr) // emits kind, topic, err
}

// Call — returned directly
var reqErr mqtt5.CallError
if errors.As(err, &reqErr) {
    switch reqErr.Kind {
    case mqtt5.KindTimeout:   // no reply within deadline
    case mqtt5.KindDecode:    // reply could not be decoded
    case mqtt5.KindHandler:   // server returned an error
    case mqtt5.KindEncode:    // request encoding failed or subscribe failed
    }
    slog.Error("request failed", "error", reqErr)
}

// Publish — returned directly
var encErr mqtt5.PublishEncodeError
if errors.As(err, &encErr) {
    slog.Error("publish encode failed", "error", encErr) // emits topic, err
}
```

## See also

- [`adapters/mqtt5` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt5)
- [`api/reqreply` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply)
- [examples/events-api](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api) — runnable demo: Client.Attach (preferred), User Properties, UserPropertyParam codec validation, ContentType auto-format, connect-level security, AsyncAPI specs (mqtt5 alongside mqtt v3/zeromq)
- [examples/reqreply-api](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) — request-reply over MQTT 5 AND ZeroMQ: `Client.Attach`/`Server.Attach` workflow, dual-mode `Client.Call`, concurrent multi-route dispatch, route-level + global security, `CallAsync`/`Future`, AsyncAPI spec printing
- [MQTT 3.1.1 Examples](mqtt.md)
- [Concept: Codec Layers as Observable Layers](../concepts/observable-layers.md)
- [Feature: Metrics Observer](../features/observer.md)
- [paho.golang](https://github.com/eclipse/paho.golang) — MQTT 5.0 Go client
