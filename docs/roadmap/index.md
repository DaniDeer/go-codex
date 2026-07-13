# Roadmap — Features in Planning

This section contains implementation plans for features that have been fully researched and architecturally specified but are not yet implemented. Each page captures the design intent, API surface, error model, and key decisions so that implementation can start without re-doing the research.

These are **living design documents** — they reflect the current best thinking at the time of writing. API surface may change slightly during implementation as new constraints are discovered.

---

## Planned features

| Feature | Package | Status | Summary |
|---------|---------|--------|---------|
| [Default Observer via Context](default-observer.md) | `stats` + all adapters | **Phase 0 ready** — pre-flight fixes before context observer | Phase 0: fix 3 observer wiring gaps (`DrainWrite` missing Observer, sink bridges not threading observer to `gstream.Drain`, misleading `stats.Observer` godoc). Phase 1: `stats.WithObserver(ctx, obs)` context-scoped default observer |
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
