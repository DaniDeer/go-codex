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
// # File I/O — Read, Write, Update, Patch, PatchEncoded
//
// [File][T] is a declarative typed file descriptor — the pipeline-agnostic
// path-addressed counterpart to [Cache][T] for adapters/redis. Build one
// with [NewFile] (standalone, no port/pipeline involved) or declare it once
// on a port via [FilePattern] and retrieve it with [FileHandle] — both
// paths produce the identical [File][T] value. It supports five operations:
//
//   - [File.Read]   — full decode (reads entire file into T)
//   - [File.Write]  — full encode (overwrites file with T); use when you already have the decoded value
//   - [File.Update] — typed read-modify-write: fn(T) T; use when you need the latest file state first
//   - [File.Patch]  — partial field update (map[string]any); unknown fields dropped
//   - [PatchEncoded] — typed partial update via a separate patch codec (free function);
//     fields in patchCodec but NOT in the file codec are preserved in the output
//
// # Field survival rules for Patch and PatchEncoded
//
// Every write operation filters output through its codec. The rules differ:
//
//	// Patch: only file-codec fields survive; unknown keys in the patch map are dropped
//	configFile.Patch(nil, map[string]any{"port": 9090}, opts)
//	// → file codec fields updated/re-written; "port" updated; unknown keys dropped
//
//	// PatchEncoded: patchCodec fields survive even if not in the file codec
//	ports.PatchEncoded(configFile, nil, patchCodec, patchValue, opts)
//	// → file codec fields updated/re-written; patchCodec fields written (even extra ones)
//
// Field survival summary:
//
//	Field in file codec + field in patch map/patchCodec   → updated ✓
//	Field in file codec + absent from patch               → preserved ✓
//	Field in patchCodec only (not in file codec)          → written by PatchEncoded ✓
//	Field in neither codec                                → dropped by both Patch and PatchEncoded
//
// Key rule: use [PatchEncoded] to intentionally add new fields to a file by
// declaring them in the patch codec. Use [File.Patch] with an explicit
// map[string]any when unknown keys should be silently discarded.
//
// Patch and PatchEncoded are supported only for map-based formats (JSON, YAML, TOML, [format.New]).
// Check [format.Format.IsPatchable] before calling either when the format is not known at compile time.
//
// All file error types implement [slog.LogValuer] for structured logging:
//
//	var encErr ports.FileEncodeError
//	if errors.As(err, &encErr) {
//	    slog.Warn("encode failed", "error", encErr)  // structured output via LogValue()
//	}
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
