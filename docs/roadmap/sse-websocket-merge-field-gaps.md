# SSE & WebSocket Merge-Field Gaps — connection-level vars for long-lived boundaries

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> Consolidates two previously-deferred, structurally-identical open
> questions into one plan: `rest.SSERouteHandle`'s Event-side merge (was
> `docs/roadmap/merge-field-remaining-gaps.md`'s G1) and
> `adapters/websocket`'s per-connection path vars (was
> `docs/roadmap/file-cache-merge-field-gaps.md`'s G3). Both source docs
> are now deleted — their other items all shipped (see
> `.github/skills/review-go-codex/references/history.md`'s Rounds
> covering the merge-field/templatematch/File-Cache work) or were
> permanently closed as declined design decisions (see
> `docs/concepts/api-contracts.md`'s "one struct, one call" reference
> table).

## Motivation

Every REQUEST/RESPONSE and PUB/SUB-style boundary in go-codex now has the
full "one struct, one call" convenience: `api/rest`, `api/events`,
`api/reqreply`, the `ports.Pattern` binding layer, `api/mcp` Resources
(partial), `ports.File`, and `ports.Cache` (see
`docs/concepts/api-contracts.md`'s reference table for the complete,
current list). Two boundaries were deliberately left out because they
don't fit the request/response OR pub/sub shape at all — they are
**long-lived CONNECTIONS carrying MANY messages from ONE initial
request**:

1. **SSE** (`rest.SSERouteHandle`, `adapters/nethttp`/`chi`'s `SSEAdapter`):
   a client opens ONE connection with a `Req` (path + query params at
   subscribe time, e.g. `GET /machines/{machineID}/events?since=...`), then
   receives MANY `Event` values pushed over that single connection. There
   is no per-`Event` "topic" or "request" the way MQTT/ZeroMQ pub/sub or
   REST request/response has — every `Event` shares the SAME connection
   context.
2. **WebSocket** (`ports.DuplexPort`, `adapters/websocket`): a client opens
   ONE connection via an upgrade request with path vars (e.g.
   `/live/{room}`), then exchanges MANY `In`/`Out` frames over that
   session. `Hub.SessionInfo(session)` already exposes ALL upgrade-time
   template vars per session — but nothing merges them into the
   per-message `In`/`Out` payloads automatically.

Both raise the EXACT SAME open design question, previously deferred
independently for each: **is repeating one connection's vars into every
message/event actually useful**, or does exposing the resolved
`Req`/session vars via the existing closure/`SessionInfo` API already
cover the practical need? This doc treats them as one design problem
since a resolution for one very likely resolves the other identically.

## Scope decisions

| Item | Boundary | Severity | In scope this round |
|---|---|---|---|
| S1 | `rest.SSERouteHandle` has zero merge support (no `MergeFields`/`DecodeMerged` for the pushed `Event` type) — connection's `Req` (path+query) never merges into pushed `Event`s | `api/rest` (SSE), `adapters/nethttp`/`chi` | Low — no concrete use case has appeared; existing closure-based access may already suffice | Design only — resolve the open question below BEFORE writing code |
| S2 | `adapters/websocket`'s upgrade path vars (`Hub.SessionInfo`) are available per-session but never merged into per-message `In`/`Out` frame payloads | `ports.DuplexPort`, `adapters/websocket` | Low — same open question as S1, no concrete use case | Design only — resolve together with S1 |

## API surface (sketches only — NOT resolved which shape, if any, to build)

**S1 — SSE Event-side merge**, mirroring `events.ChannelHandle.DecodeMerged`
but ENCODE-direction only (SSE never decodes an `Event` — the server always
produces it):

```go
// api/rest/builder.go
func (h *SSERouteHandle[Req, Event]) MergeFields() []codex.FieldCodec[Event]
// Registered via a hypothetical rest.NewEventMergeParam-style constructor,
// analogous to NewPathParam/NewTopicParam but for the PUSHED type.

// adapters/nethttp / adapters/chi — SSEAdapter/RegisterSSE would derive
// vars from the CONNECTION's Req ONCE (at subscribe time) and merge them
// into EVERY Event pushed over that connection via codex.EncodeVars(req,
// ...)+codex.DecodeVars(&event, vars, h.MergeFields()...) — a genuinely
// different shape from every other merge site in the codebase (derive
// ONCE, apply MANY times, rather than derive-and-apply once per call).
```

**S2 — WebSocket per-session frame merge**, mirroring the same shape for
`ports.DuplexPort`:

```go
// adapters/websocket/hub.go or binding.go — a hypothetical wiring that
// merges Hub.SessionInfo(session) vars into every outbound Framed[Out]
// payload (encode direction) and/or every inbound Framed[In] payload
// (decode direction) automatically, IF the port's In/Out type declares
// merge-capable fields via a new NewSocketPathParam-style constructor
// (mirrors rest.NewPathParam, registered on SocketPattern.Opts).
func (s Socket[In, Out]) MergeFields() []codex.FieldCodec[Out] // or In, or both
```

## Structured errors / Observer integration

Not designed — depends entirely on the open design decision below. If
pursued, would reuse `codex.VarEncodeTypeError`/`ValidationErrors` exactly
as every other merge site does; no new observer methods anticipated
(`RecordPublish`-equivalent already exists for both SSE and WebSocket
sends).

## Unit test plan

Not designed — deferred until the open design decision is resolved. If
pursued: S1 would need a happy-path test (connection Req vars appear on
every pushed Event) + a regression guard (no merge fields declared behaves
identically to today); S2 the same shape for `Hub.SessionInfo` vars merged
into `Framed[Out]`/`Framed[In]` payloads.

## Files to create/change

Not designed — deferred until the open design decision is resolved.
Candidates if pursued: `api/rest/builder.go` (S1 constructor + accessor),
`adapters/nethttp/{adapter,binding}.go`, `adapters/chi/{adapter,binding}.go`
(S1 wiring); `ports/pattern.go` (S2 constructor on `SocketPattern.Opts`),
`adapters/websocket/{hub,binding}.go` (S2 wiring).

## Open design decisions (must be resolved before implementation)

1. **Is per-connection merge (not per-message) actually useful?** Every
   `Event`/frame would repeat the SAME value (e.g. `machineID` from the
   path) — useful for a consumer that treats the `Event`/frame stream in
   isolation (e.g. logging, replay, a message queue relay that has no
   other way to know which connection an event came from) but redundant
   for a client that already knows what it subscribed/connected to. THIS
   IS THE CENTRAL QUESTION — resolve it before designing anything else.
   Concrete next step if resolving: survey real `examples/adapters-sse`
   and `examples/adapters-streaming-*`/websocket usages for a case where
   the consumer currently hand-rolls this merge themselves (that would be
   the concrete use case this doc has lacked so far).
2. **Is the existing escape hatch (closures for SSE, `Hub.SessionInfo` for
   WebSocket) already sufficient?** Both already let the application
   merge vars into every message ITSELF with a few lines of code at the
   call site (SSE: capture `req` in the closure passed to
   `gstream.NewBroadcastHub`; WebSocket: call `hub.SessionInfo(session)`
   before encoding each outbound frame). Leaning yes, UNLESS a real
   ergonomic pain point is demonstrated (e.g. many call sites duplicating
   the same merge boilerplate).
3. **If pursued, should S1 and S2 share one mechanism, or diverge?** SSE's
   `Req` is a full REST-shaped struct (path+query+header+cookie merge
   roles already exist); WebSocket's upgrade vars are ALL template vars in
   one flat map (`Hub.SessionInfo`), closer to `events.NewTopicParam`'s
   single-destination shape. Leaning: if pursued, S1 reuses REST's
   existing role-aware merge-field machinery (just applied to `Event`
   instead of `Resp`); S2 reuses the flat single-destination shape events/
   reqreply/file/cache already use. Two different call shapes, same
   underlying `codex.EncodeVars`/`DecodeVars` primitive.
4. **"Derive once, apply many times" is architecturally new — does the
   existing merge-field infrastructure (`codex.FieldCodec`,
   `EncodeVars`/`DecodeVars`) even support this shape cleanly, or does it
   need a new primitive?** Every existing merge site derives/applies vars
   ONCE per call (one struct in → one call → vars out, or vars in → one
   call → one struct out). SSE/WebSocket would derive vars ONCE (at
   connect time) then APPLY them repeatedly (once per pushed
   message) — verify `codex.DecodeVars`/`EncodeVars` compose cleanly when
   called in a loop with the SAME vars map before assuming this is a
   simple wiring exercise.

## Verification (once implementation is greenlit)

Same ritual as every prior round: gofmt, `go build ./...`, `go test
./...`, `-race` on touched packages, `just check`, all examples exit 0.
