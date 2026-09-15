# ZeroMQ Examples

> See also: [`adapters/zeromq` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq) · [`api/reqreply` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply) · [`api/events`](../concepts/api-contracts.md) · [`api/rest`](../concepts/api-contracts.md) · [Feature: Metrics Observer](../features/observer.md)

go-codex provides two packages behind ZeroMQ support:

| Package | Purpose |
|---|---|
| `adapters/zeromq` | Codec-backed adapters: `NewPublishTransport`/`NewSubscribeTransport` (pub/sub, consumed via `events.PublishHandle`/`events.SubscribeHandle`), `AttachServer`/`AttachClient`/`AttachRouterServer`/`AttachDealerClient` (request-reply, preferred), `Serve`/`Call`/`ServeRouter`/`CallDealer` (request-reply escape hatch) |
| `api/reqreply` | Transport-agnostic AsyncAPI 3.0 spec builder for request-reply contracts (`NewRoute`, `Server`, `Client`, `AsyncAPISpec`) — shared with `adapters/mqtt5`, not ZMQ-specific despite the historical `api/zeromq` name |

Both follow the same **declare → register → handle → adapt** pattern as the HTTP and MQTT adapters.

## Prerequisites: libzmq installation

`adapters/zeromq` uses the [`pebbe/zmq4`](https://github.com/pebbe/zmq4) binding, which requires **libzmq** (the ZeroMQ C library) to be installed on the host.

### Debian / Ubuntu

```bash
sudo apt install libzmq3-dev
```

### macOS (Homebrew)

```bash
brew install zeromq
```

### Windows

Install [vcpkg](https://github.com/microsoft/vcpkg) and run:

```bash
vcpkg install zeromq
```

Set the `PKG_CONFIG_PATH` environment variable to point to the vcpkg `pkgconfig` directory before building.

### CGO requirement

Because `pebbe/zmq4` uses CGO, cross-compilation to a platform that does not have libzmq requires setting up a cross-compiler toolchain that includes the ZMQ headers. For pure-Go environments (e.g. minimal Docker images), consider using `go-zeromq/zmq4` as the backing socket implementation instead, adapting the `FramedSocket` wrapper accordingly.

### Adding the dependency

```bash
go get github.com/pebbe/zmq4
```

---

## FramedSocket wrapper for pebbe/zmq4

`adapters/zeromq` accepts a [`FramedSocket`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq#FramedSocket) interface rather than a concrete socket type. Create a thin wrapper in your application:

```go
import (
    zmq "github.com/pebbe/zmq4"
    zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
)

type pebbeSocket struct{ s *zmq.Socket }

func WrapSocket(s *zmq.Socket) zeromq.FramedSocket { return &pebbeSocket{s: s} }

func (p *pebbeSocket) SendFrames(frames [][]byte) error {
    for i, f := range frames {
        flag := zmq.SNDMORE
        if i == len(frames)-1 {
            flag = 0
        }
        if _, err := p.s.SendBytes(f, flag); err != nil {
            return err
        }
    }
    return nil
}

func (p *pebbeSocket) RecvFrames() ([][]byte, error) {
    frames, err := p.s.RecvMessageBytes(0)
    if err != nil {
        if zmq.AsErrno(err) == zmq.EAGAIN {
            return nil, zeromq.ErrTimeout
        }
        return nil, err
    }
    return frames, nil
}

func (p *pebbeSocket) SetSubscription(topic string) error {
    return p.s.SetSubscribe(topic)
}

func (p *pebbeSocket) SetRecvTimeout(d time.Duration) error {
    return p.s.SetRcvtimeo(d)
}
```

---

## PUB/SUB — sensor readings

### Channel declaration (shared contract)

The channel declaration is identical to MQTT — only the adapter import changes.

```go
// contract/contract.go
import (
    "github.com/DaniDeer/go-codex/api/events"
    "github.com/DaniDeer/go-codex/codex"
    "github.com/DaniDeer/go-codex/validate"
)

type SensorReading struct {
    SensorID string
    Value    float64
}

var sensorCodec = codex.Struct[SensorReading](
    codex.RequiredField("sensor_id",
        codex.String().Refine(validate.UUID).WithTitle("SensorID"),
        func(r SensorReading) string { return r.SensorID },
        func(r *SensorReading, v string) { r.SensorID = v },
    ),
    codex.RequiredField("value",
        codex.Float64(),
        func(r SensorReading) float64 { return r.Value },
        func(r *SensorReading, v float64) { r.Value = v },
    ),
)

var ReadingsChannel = events.NewChannel[SensorReading](
    "sensors/{sensorID}/readings",
    sensorCodec,
    events.Subscribe{OperationID: "receiveSensorReading"},
    events.Publish{OperationID: "publishSensorReading"},
    events.TopicParam{Name: "sensorID"}.WithCodec(codex.String().Refine(validate.UUID)),
)
```

### Publisher (PUB socket)

```go
import (
    zmq "github.com/pebbe/zmq4"
    zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
    "github.com/DaniDeer/go-codex/api/events"
    "github.com/DaniDeer/go-codex/stats"
)

func main() {
    ctx := context.Background()

    pubSock, _ := zmq.NewSocket(zmq.PUB)
    defer pubSock.Close()
    pubSock.Bind("tcp://*:5555")

    sock := WrapSocket(pubSock)

    // Spec-free, handle-based Decision 7 call surface
    // (docs/design/d-0002-pubsub-workflow-simplification.md): NewPublishTransport
    // satisfies events.PublishTransport[T], consumed through events.PublishHandle
    // — no *events.Client/spec needed at all for this path.
    transport := zeromq.NewPublishTransport[SensorReading](sock, zeromq.PublishOptions[SensorReading]{Observer: obs})
    pub := contract.ReadingsChannel.WithPublish(events.Publish{})

    reading := SensorReading{SensorID: "f47ac10b-...", Value: 22.5}
    err := events.PublishHandle(ctx, pub, transport, reading)
    if err != nil {
        log.Fatal(err)
    }
}
```

### Subscriber (SUB socket)

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    eventsClient := events.NewClient(events.WithInfo(events.Info{Title: "Sensors", Version: "1.0.0"}))

    subSock, _ := zmq.NewSocket(zmq.SUB)
    defer subSock.Close()
    subSock.Connect("tcp://localhost:5555")

    sock := WrapSocket(subSock)
    if err := zeromq.Attach(eventsClient, sock); err != nil { // Decision 5: Client.Attach workflow
        log.Fatal(err)
    }
    sub := contract.ReadingsChannel.WithSubscribe(events.Subscribe{})
    if err := eventsClient.Subscribe(ctx, sub,
        func(ctx context.Context, r SensorReading) error {
            log.Printf("received: %+v", r)
            return nil
        }); err != nil {
        log.Fatal(err)
    }
}
```

`eventsClient.ServeSubscribers(ctx) error` (once `Attach`-bound) implements
`events.SubscriberServer` through the package's internal, unexported caller type — dispatches
every channel registered via `Subscriber[T].Register(client)` over one shared receive loop
on the socket (ZMQ sockets aren't safe for concurrent multi-goroutine use).
`eventsClient.Publish(ctx, pub, msg)` (once `Attach`-bound) is the SOLE, transport-agnostic
publish-side call shape for application code — the former `NewPublisherFor[T]`/`PublisherFor[T]`
were DELETED, with no separate binding type needed anymore.

### `Client.Attach` — the inverted-control workflow

`zeromq.Attach(client, sock)` binds sock to `client` as its `events.Transport` — the
"attach the adapter to the client" step. From there, call `client.Publish`/`client.Subscribe`
directly on the `*events.Client` value itself; there is no adapter-package-qualified call
needed at the usage site anymore, only at attach time:

```go
client := events.NewClient(events.WithInfo(events.Info{Title: "Sensor Network", Version: "1.0.0"}))
if err := zeromq.Attach(client, sock); err != nil {
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

err := client.Publish(ctx, pub, reading) // "one struct, one call" — topic derived
                                          // automatically from a merge-capable topic param
```

Since `Client.Publish`/`Client.Subscribe` are ordinary Go methods (not generic — Go forbids a
method from introducing its own type parameters), `pub`/`sub`/`msg`/`fn` are passed as `any` and
their concrete types are recovered internally via reflection; a mismatch surfaces as
`events.TransportTypeMismatchError` at CALL time rather than a compile error — an explicit,
narrowly-scoped trade-off for this one convenience surface. See
`docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 5 for the full design and its
documented v1 scope limits (no per-call format override, no non-default QoS, no general-purpose
SubscribeMW/PublishMW wrapping — use `events.SubscribeHandle`/`events.PublishHandle` with
`zeromq.NewSubscribeTransport`/`zeromq.NewPublishTransport` directly for those, per Decision 7
of `docs/design/d-0002-pubsub-workflow-simplification.md`; `Attach`'s internal transport wraps the
same underlying logic those transports expose).
[`examples/events-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api)'s
`demo_zeromq_pubsub_roundtrip.go` demonstrates this workflow end to end.

### AsyncAPI spec

The existing `api/events` client generates a valid AsyncAPI 3.0 document with `protocol: zmq`:

```go
spec, _ := eventsClient.AsyncAPISpec()
// emits:
// asyncapi: 3.0.0
// servers:
//   zmq:
//     host: tcp://localhost:5555
//     protocol: zmq
// channels:
//   sensors/{sensorID}/readings:
//     ...
```

---

## REQ/REP — typed RPC

Request-reply lives in `api/reqreply` (a transport-agnostic AsyncAPI 3.0
spec builder shared by `adapters/mqtt5` and `adapters/zeromq` — see
[`examples/reqreply-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api)
for the full runnable workflow across both transports, REQ/REP AND
ROUTER/DEALER).

### Route declaration

```go
var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add",
    computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd", Summary: "Add two integers."},
)
```

### Preferred: `Server`/`Client` + `Attach`

`zeromq.AttachServer`/`AttachClient` mirror `events.Client`'s
`Attach`+`.Publish`/`.Subscribe` workflow: one `Attach` call (against a
`topic → socket` map — REQ/REP sockets are point-to-point, unlike PUB/SUB's
topic-multiplexed SUB socket), then plain `Server.Serve`/`Client.Call`/
`Client.CallAsync`, no further `zeromq.*` calls needed at the call site.

```go
server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5556", Protocol: "zmq"})
handle, err := ComputeRoute.WithHandler(func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
    return ComputeResp{Sum: req.X + req.Y}, nil
}).Register(server)
if err != nil {
    log.Fatal(err)
}

rep, _ := zmq.NewSocket(zmq.REP)
rep.Bind("tcp://*:5556")
sockets := map[string]zeromq.FramedSocket{"compute/add": WrapSocket(rep)}
if err := zeromq.AttachServer(server, sockets); err != nil {
    log.Fatal(err) // e.g. zeromq.MissingSocketError if a registered route has no socket entry
}
go server.Serve(ctx) // dispatches every registered route concurrently, blocks until ctx cancelled

// Client
req, _ := zmq.NewSocket(zmq.REQ)
req.Connect("tcp://localhost:5556")
client := reqreply.NewClient()
if err := zeromq.AttachClient(client, map[string]zeromq.FramedSocket{"compute/add": WrapSocket(req)}); err != nil {
    log.Fatal(err)
}
respAny, err := client.Call(ctx, ComputeRoute, ComputeReq{X: 3, Y: 4})
resp := respAny.(ComputeResp)

// Async: CallAsync returns a *reqreply.Future[ComputeResp] immediately.
futureAny, err := client.CallAsync(ctx, ComputeRoute, ComputeReq{X: 10, Y: 20})
future := futureAny.(*reqreply.Future[ComputeResp])
resp2, err := future.Wait(ctx)

// AsyncAPI 3.0 spec, derived from the route declarations already made.
doc, _ := server.AsyncAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

The generated AsyncAPI spec includes `reply:` on the send operation, linking to a reply channel:

```yaml
operations:
  sendComputeAdd:
    action: send
    channel:
      $ref: '#/channels/computeAdd'
    reply:
      channel:
        $ref: '#/channels/computeAddReply'
  receiveComputeAddReply:
    action: receive
    channel:
      $ref: '#/channels/computeAddReply'
```

### Escape hatch: `Serve`/`Call` directly

Use these lower-level functions instead of `Attach` when you need a custom
per-call `Observer` override or other fine-grained control — they work
against the SAME route declarations shown above, and `Route.ClientHandle()`
gets a handle with no `Server` needed at all:

```go
// Server (REP socket)
handle := ComputeRoute.ClientHandle() // or Route.Register(server) if a spec is also needed
rep, _ := zmq.NewSocket(zmq.REP)
defer rep.Close()
rep.Bind("tcp://*:5556")
sock := WrapSocket(rep)
if err := zeromq.Serve(ctx, sock, handle, func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
    return ComputeResp{Sum: req.X + req.Y}, nil
}, zeromq.ServeOptions{Observer: obs}); err != nil {
    log.Fatal(err)
}

// Client (REQ socket)
req, _ := zmq.NewSocket(zmq.REQ)
defer req.Close()
req.Connect("tcp://localhost:5556")
sock = WrapSocket(req)
result, err := zeromq.Call(ctx, sock, handle, ComputeReq{X: 3, Y: 4},
    zeromq.CallOptions{Observer: obs})
if err != nil {
    log.Fatal(err)
}
log.Printf("sum: %d", result.Sum)
```

---

## Observer integration

Pass the same `stats.Observer` used for HTTP and MQTT — `stats.NewFanout` works unchanged:

```go
obs := stats.NewFanout(
    metricsObserver,
    stats.NewLoggingObserver(slog.Default()),
    tracer,
)

subTransport := zeromq.NewSubscribeTransport[T](sock, zeromq.SubscribeOptions[T]{Observer: obs})
err := events.SubscribeHandle(ctx, sub, subTransport, fn)

pubTransport := zeromq.NewPublishTransport[T](sock, zeromq.PublishOptions[T]{Observer: obs})
err = events.PublishHandle(ctx, pub, pubTransport, msg)

zeromq.Serve(ctx, sock, handle, fn, zeromq.ServeOptions{Observer: obs})
zeromq.Call(ctx, sock, handle, req, zeromq.CallOptions{Observer: obs})
```

| Event | Observer method | Operation |
|---|---|---|
| Message received (success) | `RecordSubscribe(topic, true, dur)` | |
| Message received (failure) | `RecordSubscribe(topic, false, dur)` | |
| Message published | `RecordPublish(topic, success, dur)` | |
| REP request processed | `RecordRequest("ZMQ-REP", path, status, dur)` | |
| REQ call completed | `RecordRequest("ZMQ-REQ", path, status, dur)` | |
| ROUTER request processed | `RecordRequest("ZMQ-ROUTER", path, status, dur)` | |
| DEALER call completed | `RecordRequest("ZMQ-DEALER", path, status, dur)` | |
| Trace span | `StartSpan(ctx, op, name)` / `EndSpan` | `"zmq.subscribe"`, `"zmq.publish"`, `"zmq.serve"`, `"zmq.request"` |

---

## Error handling

All errors are `errors.As`-navigable and implement `slog.LogValuer`:

Use this section for ZeroMQ-specific typed errors, and the unified map in
[Guide: Error Handling](error-handling.md#where-to-handle-errors-adapters-ports-pipelines)
for consistent placement of adapter vs port/pipeline error handling.

```go
// Subscribe / Serve — errors delivered to OnError callback
var subErr zeromq.SubscribeError
if errors.As(err, &subErr) {
    switch subErr.Kind {
    case zeromq.KindDecode:  // payload validation failed
    case zeromq.KindHandler: // application handler returned an error
    }
    slog.Warn("subscribe error", "error", subErr) // emits kind, topic, err
}

// Publish — encode errors returned directly
var encErr zeromq.PublishEncodeError
if errors.As(err, &encErr) {
    slog.Error("publish failed", "error", encErr) // emits topic, err
}

// Call — all call-side failures wrapped in CallError
var callErr zeromq.CallError
if errors.As(err, &callErr) {
    slog.Error("zmq call failed", "error", callErr) // emits err
}
```

---

## DEALER/ROUTER — concurrent REQ/REP

DEALER and ROUTER are the async variants of REQ and REP. The ROUTER server handles each request in its own goroutine; the DEALER client sends multiple requests concurrently.

| | REQ/REP | DEALER/ROUTER |
|---|---|---|
| Alternation | Strict (send→recv) | Free (async) |
| Concurrency | Serial | Per-request goroutine (ROUTER) |
| Framing | `[payload]` / `["ok", resp]` | `["", payload]` / `["", "ok", resp]` |
| API | `Serve` / `Call` | `ServeRouter` / `CallDealer` |

The frame layout adds an empty **delimiter frame** to separate the DEALER identity from the payload:

```
ROUTER receives: [identity, "", payload]
ROUTER replies:  [identity, "", "ok", encoded_response]
DEALER sends:    ["", payload]
DEALER receives: ["", "ok", encoded_response]
```

### Preferred: `Server`/`Client` + `Attach` (ROUTER/DEALER)

`zeromq.AttachRouterServer`/`AttachDealerClient` are the ROUTER/DEALER
counterparts of `AttachServer`/`AttachClient` above — same `topic → socket`
map, same `MissingSocketError` upfront check, same `Server.Serve`/
`Client.Call`/`Client.CallAsync` call sites once attached; the identity-
frame envelope below is handled internally, transparent to the caller:

```go
server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5557", Protocol: "zmq"})
handle, _ := RouterComputeRoute.WithHandler(fn).Register(server)

router, _ := zmq.NewSocket(zmq.ROUTER)
router.Bind("tcp://*:5557")
if err := zeromq.AttachRouterServer(server, map[string]zeromq.FramedSocket{"compute/router-add": WrapSocket(router)}); err != nil {
    log.Fatal(err)
}
go server.Serve(ctx)

dealer, _ := zmq.NewSocket(zmq.DEALER)
dealer.Connect("tcp://localhost:5557")
client := reqreply.NewClient()
if err := zeromq.AttachDealerClient(client, map[string]zeromq.FramedSocket{"compute/router-add": WrapSocket(dealer)}); err != nil {
    log.Fatal(err)
}
respAny, err := client.Call(ctx, RouterComputeRoute, ComputeReq{X: 3, Y: 4})
```

### Escape hatch: `ServeRouter`/`CallDealer` directly

`ServeRouter` and `CallDealer` reuse the same `ServeOptions`/`CallOptions` and `ServeError`/`CallError` types:

```go
// Server (ROUTER socket) — concurrent
go func() {
    router, _ := zmq.NewSocket(zmq.ROUTER)
    router.Bind("tcp://*:5557")
    zeromqadapter.ServeRouter(ctx, WrapSocket(router), handle, fn,
        zeromqadapter.ServeOptions{Observer: obs})
}()

// Client (DEALER socket) — concurrent calls from multiple goroutines
dealer, _ := zmq.NewSocket(zmq.DEALER)
dealer.Connect("tcp://localhost:5557")
sock := WrapSocket(dealer)

var wg sync.WaitGroup
for _, req := range reqs {
    wg.Add(1)
    go func(req ComputeReq) {
        defer wg.Done()
        resp, err := zeromqadapter.CallDealer(ctx, sock, handle, req,
            zeromqadapter.CallOptions{Observer: obs})
        // ...
    }(req)
}
wg.Wait()
```

---

## Security — CURVE and PLAIN

ZMQ CURVE and PLAIN authentication are configured at the socket level before passing the socket to the adapter. go-codex's `FramedSocket` interface carries no credential metadata.

### CURVE (public-key encryption)

```go
// Server
server, _ := zmq.NewSocket(zmq.ROUTER)
server.SetOption(zmq4.CURVE_SERVER, 1)
server.SetOption(zmq4.CURVE_SECRETKEY, serverSecretKey)
server.SetOption(zmq4.CURVE_PUBLICKEY, serverPublicKey)
server.Bind("tcp://*:5557")

// Client
client, _ := zmq.NewSocket(zmq.DEALER)
client.SetOption(zmq4.CURVE_SERVERKEY, serverPublicKey)
client.SetOption(zmq4.CURVE_PUBLICKEY, clientPublicKey)
client.SetOption(zmq4.CURVE_SECRETKEY, clientSecretKey)
client.Connect("tcp://localhost:5557")
```

Use `zmq.NewCurveKeypair()` to generate server and client key pairs.

### PLAIN (username/password)

```go
// Server
server.SetOption(zmq4.PLAIN_SERVER, 1)

// Client
client.SetOption(zmq4.PLAIN_USERNAME, "myuser")
client.SetOption(zmq4.PLAIN_PASSWORD, "mypass")
```

**AsyncAPI spec:** AsyncAPI 3.0 has no standard security scheme type for ZMQ CURVE or PLAIN. These authentication mechanisms are documented in prose and configured at the transport layer; they do not appear in the generated spec.

---

## See also

- [`adapters/zeromq` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq)
- [`api/reqreply` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/reqreply)
- [Concept: Codec Layers as Observable Layers](../concepts/observable-layers.md)
- [Concept: API Contracts](../concepts/api-contracts.md)
- [Feature: Metrics Observer](../features/observer.md)
- [examples/events-api](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api) — PUB/SUB demo (zeromq alongside mqtt v3/mqtt5)
- [examples/reqreply-api](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) — `api/reqreply`'s `Client.Attach`/`Server.Attach` workflow: REQ/REP AND ROUTER/DEALER, dual-mode `Client.Call`, concurrent multi-route dispatch, route-level + global security, `CallAsync`/`Future`, AsyncAPI spec printing
- [pebbe/zmq4](https://github.com/pebbe/zmq4) — recommended ZMQ Go binding
