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
//   - PUB/SUB (and PUSH/PULL) — via [api/events] channel declarations + [Subscribe]/[Publish]
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
//	zeromq.Subscribe(ctx, sock, handle, fn, opts)
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
