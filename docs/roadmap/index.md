# Roadmap — Features in Planning

This section contains implementation plans for features that have been fully researched and architecturally specified but are not yet implemented. Each page captures the design intent, API surface, error model, and key decisions so that implementation can start without re-doing the research.

These are **living design documents** — they reflect the current best thinking at the time of writing. API surface may change slightly during implementation as new constraints are discovered.

---

## Planned features

| Feature | Package | Status | Summary |
|---------|---------|--------|---------|
| [Stream — Phase 4 (FlatMap, GroupBy, CombineLatestN)](stream-phase4.md) | `stream` | Design needed | FlatMap sub-stream variant (goroutine pool), GroupBy (per-key sub-streams), CombineLatest5+ (code-gen or nested composition) |
| [Stream Bridges — Adapter Integration](stream-bridges.md) | `adapters/*`, `adapters/file` (new) | Design complete | Bridge helpers connecting all adapters (ZeroMQ, HTTP SSE server+client, MQTT pub/sub, MQTT5 pub/sub + reqreply, mcpgo, SQL, file) to `stream.Stream[T]`; includes source helpers (`SubscribeStream`, `PollStream`, `QueryStream`, `SSEClientStream`), sink helpers (`DrainPublish`, `DrainInsert`, `DrainWrite`, `SSEFromStream/Hub`), and reqreply helpers (`CallStream`, `CallDealerStream`, `ServeLatest`, `ToolLatestHandler`) |
| [AMQP 0.9.1 Adapter](amqp-adapter.md) | `adapters/amqp` | Design complete | PUB/SUB + Request/Reply over RabbitMQ — exchange/queue topology, Ack/Nack, `ReplyTo`/`CorrelationId` RPC, structured errors, observer integration |
| [TCP Adapter](tcp-adapter.md) | `adapters/tcp` | Design complete | Request/Reply + streaming over raw TCP — pluggable `FramedConn` framing, built-in length-prefix framer, stdlib-only, no CGO |

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
