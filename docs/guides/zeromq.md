# ZeroMQ Examples

> See also: [`adapters/zeromq` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq) · [`api/events`](../concepts/api-contracts.md) · [`api/rest`](../concepts/api-contracts.md) · [Feature: Metrics Observer](../features/observer.md)

The `adapters/zeromq` package provides codec-backed adapters for ZeroMQ sockets. It follows the same **declare → register → handle → adapt** pattern as the HTTP and MQTT adapters — only the import changes.

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

    builder := events.NewBuilder(events.Info{Title: "Sensors", Version: "1.0.0"})
    builder.AddServer("zmq", events.Server{URL: "tcp://localhost:5555", Protocol: "zmq"})

    handle, _ := contract.ReadingsChannel.Register(builder)

    pub, _ := zmq.NewSocket(zmq.PUB)
    defer pub.Close()
    pub.Bind("tcp://*:5555")

    sock := WrapSocket(pub)
    reading := SensorReading{SensorID: "f47ac10b-...", Value: 22.5}
    err := zeromq.Publish(ctx, sock, handle, reading,
        map[string]string{"sensorID": "f47ac10b-..."},
        zeromq.PublishOptions{Observer: obs},
    )
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

    builder := events.NewBuilder(events.Info{Title: "Sensors", Version: "1.0.0"})
    handle, _ := contract.ReadingsChannel.Register(builder)

    sub, _ := zmq.NewSocket(zmq.SUB)
    defer sub.Close()
    sub.Connect("tcp://localhost:5555")

    sock := WrapSocket(sub)
    if err := zeromq.Subscribe(ctx, sock, handle, func(ctx context.Context, r SensorReading) error {
        log.Printf("received: %+v", r)
        return nil
    }, zeromq.SubscribeOptions{Observer: obs}); err != nil {
        log.Fatal(err)
    }
}
```

### AsyncAPI spec

The existing `api/events` builder generates a valid AsyncAPI 3.0 document with `protocol: zmq`:

```go
spec, _ := builder.AsyncAPISpec()
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

### Route declaration (shared contract)

```go
// contract/contract.go
var ComputeRoute = rest.NewRoute[ComputeReq, ComputeResp](
    "POST", "/compute",
    computeReqCodec, computeRespCodec,
    rest.RouteMeta{OperationID: "compute"},
)
```

### Server (REP socket)

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
    defer stop()

    builder := rest.NewBuilder(rest.Info{Title: "Compute", Version: "1.0.0"})
    handle, _ := contract.ComputeRoute.Register(builder)

    rep, _ := zmq.NewSocket(zmq.REP)
    defer rep.Close()
    rep.Bind("tcp://*:5556")

    sock := WrapSocket(rep)
    if err := zeromq.Serve(ctx, sock, handle, func(ctx context.Context, req ComputeReq) (ComputeResp, error) {
        return ComputeResp{Sum: req.X + req.Y}, nil
    }, zeromq.ServeOptions{Observer: obs}); err != nil {
        log.Fatal(err)
    }
}
```

### Client (REQ socket)

```go
func main() {
    // Use ClientHandle() for client-only scenario (no builder, no spec)
    handle := contract.ComputeRoute.ClientHandle()

    req, _ := zmq.NewSocket(zmq.REQ)
    defer req.Close()
    req.Connect("tcp://localhost:5556")

    sock := WrapSocket(req)
    result, err := zeromq.Call(ctx, sock, handle, ComputeReq{X: 3, Y: 4},
        zeromq.CallOptions{Observer: obs})
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("sum: %d", result.Sum)
}
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

zeromq.Subscribe(ctx, sock, handle, fn, zeromq.SubscribeOptions{Observer: obs})
zeromq.Publish(ctx, sock, handle, msg, vars, zeromq.PublishOptions{Observer: obs})
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
| Trace span | `StartSpan(ctx, op, name)` / `EndSpan` | `"zmq.subscribe"`, `"zmq.publish"`, `"zmq.serve"`, `"zmq.request"` |

---

## Error handling

All errors are `errors.As`-navigable and implement `slog.LogValuer`:

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

## See also

- [`adapters/zeromq` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq)
- [Concept: Codec Layers as Observable Layers](../concepts/observable-layers.md)
- [Concept: API Contracts](../concepts/api-contracts.md)
- [Feature: Metrics Observer](../features/observer.md)
- [examples/adapters-zeromq](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-zeromq) — PUB/SUB demo
- [examples/adapters-zeromq-reqrep](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-zeromq-reqrep) — REQ/REP demo
- [pebbe/zmq4](https://github.com/pebbe/zmq4) — recommended ZMQ Go binding
