# Roadmap — Features in Planning

This section contains implementation plans for features that have been fully researched and architecturally specified but are not yet implemented. Each page captures the design intent, API surface, error model, and key decisions so that implementation can start without re-doing the research.

These are **living design documents** — they reflect the current best thinking at the time of writing. API surface may change slightly during implementation as new constraints are discovered.

---

## Planned features

| Feature | Package | Status | Summary |
|---------|---------|--------|---------|
| [Stream — FlatMap](stream-flatmap.md) | `stream` | Awaiting use case | FlatMap sub-stream variant (semaphore pool, unordered merge; main IO driver now covered by `ports.IOPort` + 1→N adapters) — deferred until a concrete driver appears. Also records will-not-implement decisions (`RetryWithBackoff`). GroupBy/Switch routing SHIPPED in `stream/route.go`; CombineLatest5+ resolved via nested composition (stream guide) |
| [AMQP 0.9.1 Adapter](amqp-adapter.md) | `adapters/amqp` | Design complete | PUB/SUB + Request/Reply over RabbitMQ — exchange/queue topology, Ack/Nack, `ReplyTo`/`CorrelationId` RPC, structured errors, observer integration |
| [TCP Adapter](tcp-adapter.md) | `adapters/tcp` | Design complete | Request/Reply + streaming over raw TCP — pluggable `FramedConn` framing, built-in length-prefix framer, stdlib-only, no CGO |
| [WebSocket — Deferred](websocket-deferred.md) | `adapters/websocket` | Deferred | Phases 1+2 SHIPPED (server adapters + `DuplexPort` + `SocketPattern`; client dial adapters with auto-reconnect + gap SocketErrors + session generations; chi variants via swapHandler delegation; `RegisterSocket` AsyncAPI channel). Remaining: `ConnectionObserver` extension, dynamic subprotocol negotiation. Will-not-implement: MQTT-over-WS, universal StreamPattern, outbound queueing across gaps |
| [Redis — Phase 2: Pub/Sub](redis-pubsub.md) | `adapters/redis` | Design draft | `SubscribeAdapter` (SourcePort) + `PublishAdapter` (SinkPort) via existing `EventPattern`; new narrow `PubSubCommands` interface + fake; at-most-once semantics documented loudly (offline subscribers LOSE messages — closer to ZeroMQ than MQTT); `PSUBSCRIBE` glob derivation from `{var}` templates; reuses `RecordSubscribe`/`RecordPublish` (no new stats extension). Redis Streams = Phase 3 candidate; still deferred from Phase 1: DelAdapter, CachePattern spec rendering. Per-var key codecs SHIPPED — see [Redis Cache Adapter](../features/redis.md#per-key-variable-codecs--portscachekeyparam) |
| [Webhook Adapter](webhook-adapter.md) | `adapters/webhook` | Design complete | `ReceiveAdapter` (SourcePort) + `DeliverAdapter` (SinkPort) — HMAC-SHA256 signature verification/signing over raw payload bytes, the one mechanism generic `nethttp`/`chi` can't do today (`SecurityFunc`/`CredentialFunc` run after body decode). Zero core changes: body-preserving verify middleware wraps `nethttp.Handler`; `handle.EncodeRequest` gives raw bytes to sign before `nethttp.Call`. Reuses `RESTPattern` (OpenAPI) + optional `EventPattern` (AsyncAPI) unchanged; retry-with-backoff mirrors `adapters/websocket`'s reconnect loop; no new stats extension (`SecurityObserver`/`Observer` reused) |
| [Fuzz & Benchmark Testing Infrastructure](fuzz-benchmark-testing.md) | `validate`, `codex`, `format` | Design complete | Internal quality initiative, no new API. Fuzz targets for every hand-rolled string/byte parser (9 `validate` regex constraints, `codex.HexColor`'s byte-level hex parser, JSON/TOML format-boundary decode) — zero fuzz targets exist today. Benchmarks for hot paths (`Struct` encode/decode, `SliceOf`/`StringMap`, `Refine` chains, `format.JSON` round trip) with `b.ReportAllocs()` — zero benchmarks exist today. New CI `fuzz` job (`-fuzztime=30s` per target); `benchstat`-comparable baseline for future regression checks |
| [Dynamic Port Rebinding (Hot-Swap Adapters)](dynamic-port-rebinding.md) | `ports` | Design draft | Live adapter cutover on running ports without process restart. Phase 1 scopes explicit `BindRef`/`Rebind`/`Unbind` on single-binding ports (`IOPort`, `DuplexPort`, `LatestPort`) with generation-based lifecycle tracking, typed `slog.LogValuer` errors, and observer reporting on all transition paths. |
| [Typed State Machine Contracts & Runtime](typed-state-machine.md) | `state`, `stream`, `forge` | Design draft | Codec-validated state payload + typed transition graph + runtime reducer so state can progress with explicit, forward-safe rules. Phase 1 includes both contract surface and runtime execution helper (no external FSM dependency). |
| [Vector Store Adapter (RAG retrieval)](vector-store-adapter.md) | `adapters/vectorstore`, `adapters/openai` (embeddings) | Awaiting use case | Placeholder for RAG's retrieval half — a narrow `VectorStore` interface (mirrors `adapters/redis.Commands`) for native vector DBs (Qdrant/Weaviate/Pinecone/Redis-search), plus the `adapters/openai` embeddings endpoint the (now-shipped) LLM Integration feature deferred. Notes what already works with ZERO new code today: pgvector similarity search via the existing generic `adapters/sql.QueryEachAdapter`, and embedding vectors via `codex.SliceOf(codex.Float32())`. Not yet designed in detail — captured as a reminder pending a concrete driving use case. |
| [OpenAI Multimodal Content](openai-multimodal-content.md) | `api/llm`, `adapters/openai` | Awaiting use case | Placeholder for image/audio/video input support — today `adapters/openai`'s wire shape is text/JSON-only (`chatMessage.Content` is always a plain string), with no path to OpenAI's multimodal `content` array (`image_url`/`input_audio` parts). Sketches a new `llm.CallOpt` for attaching media alongside the typed request codec and the `chatMessage.Content` union-type change needed on the adapter side. Not yet designed in detail — captured as a reminder pending a concrete driving use case. |

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
