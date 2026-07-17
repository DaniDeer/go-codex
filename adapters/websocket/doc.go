// Package websocket provides server-side WebSocket adapters for go-codex
// ports: typed, codec-validated frame streams over persistent bidirectional
// connections.
//
// # Port mapping
//
//   - [IngestSocketAdapter] — [ports.SourceAdapter]: inbound-only socket,
//     frames from all connected clients feed the port.
//   - [BroadcastSocketAdapter] — [ports.SinkAdapter]: broadcast-only socket,
//     the WebSocket sibling of the SSE adapter.
//   - [DuplexSocketAdapter] — [ports.DuplexAdapter]: full duplex with
//     session-tagged frames ([ports.Framed]) — targeted replies + broadcast.
//
// Endpoints are declared once on the port with [ports.SocketPattern]
// (path template, subprotocols, frame format); the adapter receives the
// built [ports.Socket] handle. Upgrade requests are validated through the
// rest machinery (path vars, once per connection); every frame is
// decoded/encoded through the port's codec.
//
// # Delivery semantics (read this)
//
// WebSocket delivery is at-most-once with NO retained value: a client that
// is offline or reconnecting LOSES frames, and a late joiner sees nothing
// until the next frame. Pair with a LatestPort (send-on-connect) when
// "current state" matters. Slow clients: each session has a buffered
// outbound queue (default 16); a full queue DROPS the frame for that
// session only, reported as a [SocketError] wrapping [ErrFrameDropped] —
// a lagging client never blocks the pipeline or other sessions.
//
// # Sessions
//
// Construct one [Hub] per endpoint ([NewHub]) and pass it to the adapter.
// The hub is the session registry: [Hub.SessionInfo] exposes upgrade-time
// path vars (e.g. the {room} a session joined) to pipeline code, and
// [Hub.Sessions] lists connected peers. Session routing composes with the
// stream operators — stream.GroupBy by [ports.Framed].Session yields
// per-client sub-streams.
//
// # Narrow client interface
//
// Adapters accept [Upgrader]/[Socket] — three-method interfaces — never a
// gorilla type. [NewUpgrader] adapts gorilla/websocket (keepalive pings,
// pong deadlines, and read limits are owned by the shim); unit tests use
// hand-written fakes. socket.go is the only file importing gorilla.
//
// # Not MQTT-over-WebSocket
//
// MQTT clients connecting via ws:// tunnel MQTT frames inside WebSocket —
// that is a transport option of the MQTT CLIENT, already supported by
// passing a ws:// broker URL to paho (adapters/mqtt, adapters/mqtt5).
// This package is go-codex ITSELF as the WS server speaking its own typed
// frames; it does not implement an MQTT broker.
package websocket
