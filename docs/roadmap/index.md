# Roadmap — Features in Planning

This section contains implementation plans for features that have been fully researched and architecturally specified but are not yet implemented. Each page captures the design intent, API surface, error model, and key decisions so that implementation can start without re-doing the research.

These are **living design documents** — they reflect the current best thinking at the time of writing. API surface may change slightly during implementation as new constraints are discovered.

---

## Planned features

| Feature | Package | Status | Summary |
|---------|---------|--------|---------|
| [Stream Bridge Completeness](stream-bridge-completeness.md) | all adapters | **Design evaluation** | Full audit of every bridge (MQTT, MQTT5, ZeroMQ, HTTP, MCP, SQL, File) against the declarative I/O vision. Identifies 4 gaps: `nethttp.CallStream` (high), MQTT `SubscribeStream` ergonomic fix (medium), `sql.QueryEachStream` (medium), `file.ReadEachStream` (low) |
| [Declarative I/O Steps](declarative-io-steps.md) | `adapters/nethttp`, `adapters/zeromq`, `adapters/mqtt5` | **Design complete** | `transport.CallStream` pattern — explicit declarative I/O steps in stream pipelines using route handles with full codec machinery. `zeromq.CallStream` and `mqtt5.CallStream` exist; `nethttp.CallStream` is the priority gap |
| [HTTP Enrichment Pipeline](http-enrichment-pipeline.md) | `adapters/nethttp`, `stream`, `forge`, `format` | **Design evaluation** | REST-triggered pipeline with mid-pipeline HTTP enrichment call + file I/O (read/write/patch). All steps work today except HTTP intermediate step — requires `nethttp.CallStream` (see Declarative I/O Steps) |
| [Ports — Post-Phase-6 Gaps](ports-post-phase6-gaps.md) | `ports`, `stream`, `forge`, adapters | **Phases A+B+C implemented** | Shipped: `LatestPort[T]` cache port (G1, fifth port type — `nethttp`/`zeromq`/`mcpgo` `LatestAdapter`s; `ToolLatestAdapter` removed), `SinkPort.Push`/`Start`/`Close` (G2), role-aware `RESTPattern` for HTTP ingest + SSE (G3 — `SSEHandle`/`RegisterSSE`), topology `StepKindPort`/`WithPort` (G5), `stream.Map` (G6). Remaining: **D** `forge.App` (G4, needs design pass); G7 deferred |
| [Stream — Phase 4 (FlatMap, GroupBy, CombineLatestN)](stream-phase4.md) | `stream` | Design needed | FlatMap sub-stream variant (goroutine pool), GroupBy (per-key sub-streams), CombineLatest5+ (code-gen or nested composition) |
| [AMQP 0.9.1 Adapter](amqp-adapter.md) | `adapters/amqp` | Design complete | PUB/SUB + Request/Reply over RabbitMQ — exchange/queue topology, Ack/Nack, `ReplyTo`/`CorrelationId` RPC, structured errors, observer integration |
| [TCP Adapter](tcp-adapter.md) | `adapters/tcp` | Design complete | Request/Reply + streaming over raw TCP — pluggable `FramedConn` framing, built-in length-prefix framer, stdlib-only, no CGO |

### Deferred — not planned for immediate implementation

| Feature | Package | Why deferred |
|---------|---------|-------------|
| `zeromq.CallDealerStream` | `adapters/zeromq` | Requires adding correlation-ID frames (`[seq_bytes, payload]`) to DEALER framing AND matching changes to `ServeRouter` — protocol-level breaking change, not a standalone stream bridge addition. The sequential `CallStream` (REQ socket) covers most use cases. |

---

## How to read these plans

Each plan page covers:

- **Motivation** — why this feature belongs in go-codex
- **Scope decisions** — what is in and out of Phase 1
- **API surface** — exact type signatures, options structs, interface definitions
- **Structured errors** — every new error type with `slog.LogValuer` attributes
- **Observer integration** — which observer hooks fire and when
- **AsyncAPI / OpenAPI spec** — how the feature integrates with spec generation
- **Files to create** — concrete file list with responsibilities
- **Usage sketch** — end-to-end code example
