// Package ports provides protocol-agnostic IO enforcement points for go-codex
// stream pipelines. Ports are the bridge between domain logic and the outside
// world: declare every IO boundary once — with its codec and communication
// [Pattern] — and bind concrete transport adapters in main().
//
// # Inside-out development
//
// Declare domain types, forge functions, and pipeline IO without any
// transport imports; all protocol decisions live in main():
//
//	// domain/pipeline.go — zero adapter imports; the Pattern IS the declaration
//	var SensorReadings = codex.Must(ports.NewSourcePort[SensorReading]("sensors", ReadingCodec,
//	    ports.PortOptions{Patterns: []ports.Pattern{
//	        ports.EventPattern{Topic: "sensors/{sensorID}/data"},
//	    }}))
//
//	// main.go — derive the handle from the port, pick the transport
//	handle, _ := ports.EventHandle[SensorReading](domain.SensorReadings)
//	domain.SensorReadings.Bind(ctx, mqtt.SubscribeAdapter(client, handle, 0, fmt, opts))
//
// # Six port types
//
//   - [SourcePort] — inbound boundary (external → pipeline). Multiple adapters = fan-in merge.
//   - [SinkPort] — outbound boundary (pipeline → external). Multiple adapters = fan-out
//     broadcast. Stream-fed via [SinkPort.Feed], or request-fed via the
//     [SinkPort.Start]/[SinkPort.Push]/[SinkPort.Close] lifecycle.
//   - [IOPort] — intermediate transform (pipeline ↔ external service/store). One adapter.
//   - [ToolPort] — server-side request/response: one pipeline function, exposed on N transports.
//   - [LatestPort] — reactive cache: [LatestPort.Feed] drains a stream into an atomic
//     cell; bound adapters serve every request from it (the cache outlives the stream).
//   - [DuplexPort] — bidirectional session boundary (external ↔ pipeline): peers send
//     In frames and receive Out frames over persistent, identified sessions
//     ([Framed] values tag each frame with its [Session]). One adapter.
//
// All constructors return (*Port, error) — a declared [Pattern] is built
// eagerly via Register and can fail; wrap package-level declarations with
// [codex.Must].
//
// # Pattern — the primary declaration surface
//
// [RESTPattern], [EventPattern], [ReqReplyPattern], [MCPPattern],
// [FilePattern], [SQLPattern], [CachePattern], and [SocketPattern] reuse the
// exact rest/events/reqreply/mcp/format option vocabulary. Handles come back
// out with [RESTHandle], [EventHandle], [ReqReplyHandle], [MCPHandle],
// [FileHandle], [SSEHandle], [CacheHandle], [SocketHandle]; SQL metadata
// with [SQLMeta]. Supply [PortOptions] builders to accumulate
// OpenAPI/AsyncAPI specs straight from the port declarations.
//
// # IOParam — protocol-agnostic parameters
//
// [IOParam] declarations give handle-less adapters (file vars) runtime
// validation via [ValidateParams]; handle-backed adapters validate through
// their Pattern-derived handle instead.
//
// # Test adapters
//
// [ChanSourceAdapter], [ChanSinkAdapter], and [FuncIOAdapter] allow testing
// pipelines without a real transport:
//
//	ch := make(chan SensorReading, 2)
//	ch <- reading1; close(ch)
//	domain.SensorReadings.Bind(ctx, ports.ChanSourceAdapter(ch))
package ports
