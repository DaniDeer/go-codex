// Package zeromq provides codec-backed adapters for ZeroMQ sockets.
//
// The package follows the same declare → register → handle pattern as
// [api/rest] and [api/events]: codec contracts are defined once and the
// adapter handles encode/decode/validate at the transport boundary.
//
// # Patterns
//
// Four ZMQ patterns are supported:
//
//   - PUB/SUB (and PUSH/PULL) — via [api/events] channel declarations + [Attach]
//   - REQ/REP — via [api/rest] route declarations + [Serve]/[Call]
//   - ROUTER/DEALER (concurrent) — [ServeRouter]/[CallDealer]; same options and error types
//
// Channel and route declarations are identical to the MQTT and HTTP adapters.
// Only the adapter import changes.
//
// # Transport abstraction
//
// The adapter requires a [FramedSocket] interface rather than a concrete ZMQ
// socket type. This keeps the package free of CGO dependencies so unit tests
// can run without libzmq installed.
//
// Wire a [FramedSocket] to your ZMQ library of choice. See [FramedSocket] for
// a complete pebbe/zmq4 example:
//
//	sock := &pebbeSocket{s: zmqSocket}   // implements FramedSocket
//	subTransport := zeromq.NewSubscribeTransport[SensorReading](sock, zeromq.SubscribeOptions[SensorReading]{})
//	err := events.SubscribeHandle(ctx, sub, subTransport, fn)
//
// # Attach — the single-workflow entry point
//
// [Attach] binds a [FramedSocket] to an [api/events.Client] registry and
// returns an [api/events.Transport], giving the [api/events.Client] a
// literal `Publish(ctx, pub, msg)`/`Subscribe(ctx, sub, fn)`/
// `ServeSubscribers(ctx)` call shape — the single workflow this package
// exposes for pub/sub. Internally, an unexported caller type still
// bundles the [FramedSocket] with the [api/events.Client] registry; none
// of that is publicly reachable — call [Attach] and use the returned
// [api/events.Client] methods instead:
//
//	_ = zeromq.Attach(eventsClient, sock)
//	sub := SensorReadings.WithSubscribe(events.Subscribe{})
//	err := eventsClient.Subscribe(ctx, sub, fn)
//
// A caller who already owns a pre-built handle (e.g. [SubscribeAdapter])
// reaches the same underlying logic via [events.SubscribeHandle]/
// [events.PublishHandle] + [NewSubscribeTransport]/[NewPublishTransport] —
// Decision 7's (docs/design/d-0002-pubsub-workflow-simplification.md)
// handle-based path inverted into api/events itself, rather than any
// zeromq-exported primitive directly.
//
// A whole-[api/events.Client] consume-many-channels-at-once path is also
// available: declare each channel's handler at declare time via
// [api/events.Subscriber.WithHandler], register it via
// [api/events.Subscriber.Register], then call
// [api/events.Client.ServeSubscribers] (available once [Attach] has bound
// a transport) to start consuming every registered channel in one call
// over a SINGLE shared receive loop (see serve_subscribers.go's design
// note for why one shared loop is used instead of one goroutine per
// channel — ZMQ sockets are not safe for concurrent use, and the internal
// caller bundles exactly one).
//
// # Security and general-purpose middleware
//
// [api/events.Subscriber.SubscribeMW]/[api/events.Publisher.PublishMW]
// attach implementations recognized in TWO shapes: the security shape
// (func(context.Context, *T, []route.SecurityRequirement) error — SAME
// shape both directions, since ZeroMQ's [topic, payload] frames carry
// nothing beyond what's already decoded into T) via
// [SubscribeOptions.SecurityFunc]/[PublishOptions.CredentialFunc]'s
// per-call equivalent, and the general-purpose wrapping shape
// (func(next func(context.Context, T) error) func(context.Context, T) error)
// used by [Observability]. Both are validated EAGERLY (a malformed
// attachment is a hard [middleware.MiddlewareShapeError], never a silent
// no-op) at subscribe/publish construction/dispatch time, across both the
// [NewSubscribeTransport]/[NewPublishTransport] handle-based path and the
// internal ServeSubscribers path.
// ZeroMQ had NO message-level security mechanism before this — see
// [SubscribeOptions.SecurityFunc]'s doc comment.
//
// # TopicFilter — ZeroMQ prefix-filter bug fix
//
// ZeroMQ SUB-socket subscription filtering is a plain byte-prefix match
// (no MQTT-style wildcard syntax). A channel topic template like
// "sensors/{sensorID}/data" sent VERBATIM as the filter never matches a
// real published topic. [SubscribeOptions.TopicFilter] (and its
// ports-binding-layer equivalent, [SubscribeAdapterOptions.TopicFilter])
// let you override the filter explicitly; left empty (the common case),
// a prefix is derived automatically via the unexported deriveTopicPrefix
// helper (everything up to the first "{" placeholder).
//
// # Observer
//
// Pass a [stats.Observer] via the options structs to receive lifecycle events:
// [stats.Observer.RecordSubscribe] per message, [stats.Observer.RecordPublish]
// per publish, [stats.Observer.RecordRequest] per REQ/REP exchange. Implement
// [stats.TraceObserver] to receive per-operation distributed tracing spans with
// operation names "zmq.subscribe", "zmq.publish", "zmq.serve", "zmq.request".
//
// Combine multiple observers with [stats.NewFanout] — no changes needed when
// the same observer value is already used for HTTP or MQTT adapters.
//
// # libzmq installation
//
// pebbe/zmq4 requires libzmq. See the ZeroMQ setup guide in docs/guides/zeromq.md:
//
//   - Debian/Ubuntu: apt install libzmq3-dev
//   - macOS: brew install zeromq
//
// See also:
//   - [api/events] — channel declarations for PUB/SUB
//   - [api/rest] — route declarations for REQ/REP
//   - [stats] — observer interfaces
//   - https://github.com/pebbe/zmq4 — recommended ZMQ Go binding

package zeromq
