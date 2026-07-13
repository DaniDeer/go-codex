// Package ports provides protocol-agnostic IO enforcement points for go-codex
// stream pipelines. Ports are the bridge between domain logic and the outside world.
//
// # Inside-out development
//
// Ports enable the inside-out development workflow: declare domain types, forge
// functions, and pipeline IO — all without any transport imports. Bind concrete
// transport adapters at startup (in main.go), not in pipeline code.
//
//	// domain/pipeline.go — zero adapter imports
//	var SensorReadings = ports.NewSourcePort[SensorReading]("sensor-readings", ReadingCodec, ports.PortOptions{})
//	var Calibration    = ports.NewIOPort[SensorReading, CalibratedReading]("calibration", ReadingCodec, calibratedCodec, ports.PortOptions{})
//	var OEEResults     = ports.NewSinkPort[OEE]("oee-results", OEECodec, ports.PortOptions{})
//
//	// main.go — all protocol decisions here
//	domain.SensorReadings.Bind(ctx, mqtt5.SubscribeAdapter(client, router, sensorHandle, 0, fmt, opts))
//	domain.Calibration.Bind(ctx, nethttp.CallAdapter(httpClient, baseURL, calibHandle, callOpts))
//	domain.OEEResults.Bind(ctx, mqtt5.PublishAdapter(client, alertHandle, fmt, publishOpts))
//	domain.OEEResults.Bind(ctx, nethttp.SSEAdapter(mux, sseHandle, sseOpts)) // fan-out
//
// # Three port types
//
//   - [SourcePort] — inbound boundary (external → pipeline). Multiple adapters = fan-in merge.
//   - [SinkPort] — outbound boundary (pipeline → external). Multiple adapters = fan-out broadcast.
//   - [IOPort] — intermediate transform (pipeline ↔ external service/store). One adapter.
//
// # IOParam — protocol-agnostic parameters
//
// Each port carries [IOParam] declarations that document routing parameters and
// carry validation codecs. Adapters map IOParam names to their protocol-specific
// param types (PathParam, TopicParam, FilePathParam, etc.) at binding time.
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
