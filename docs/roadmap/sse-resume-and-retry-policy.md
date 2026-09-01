# SSE Resume & Retry Policy — `adapters/nethttp`, `api/rest`

> **Status:** Findings only — no proposal, no driver yet.
> Spun out from [SSE Client Consumption](sse-client-consumption.md)'s "Out
> of scope (Phase 2)" once both sub-features turned out to be genuinely
> CLIENT+SERVER, not client-only follow-ons — too large a scope to keep
> as a two-bullet aside in that doc.
> [← Back to Roadmap](index.md)
>
> This doc captures ONLY what was confirmed via code inspection — no
> design decisions have been made, unlike `sse-client-consumption.md`'s
> fully-resolved state. Treat every section below as "here is the
> starting point," not "here is the plan," same convention that doc
> itself used before its own resolution.

---

## Why this exists

[SSE Client Consumption](sse-client-consumption.md) originally deferred
two items to "Phase 2" as small, client-side-only asides: `Last-Event-ID`
resume, and a pluggable retry policy. Investigating what either would
actually require revealed BOTH are genuinely bigger, and genuinely
CLIENT+SERVER, not client-only:

- **Confirmed via code** (`adapters/nethttp/serve_sse.go`,
  `adapters/chi/serve_sse.go`, `adapters/nethttp/adapter.go`,
  `adapters/chi/adapter.go`): the ONLY SSE protocol line ever written by
  this codebase's SSE writers, anywhere, is `data: %s\n\n` — the standard
  SSE fields `id:`, `event:`, and `retry:` are NEVER emitted. Resuming
  from a `Last-Event-ID` therefore needs NEW server-side machinery (an
  `id:` per event, plus something to replay from), not just a client-side
  reconnect header.
- **Confirmed via code** (`stream/broadcast.go`'s `BroadcastHub`): a
  newly-`Subscribe`d consumer only ever receives events emitted AFTER it
  subscribes — there is NO buffer of past events anywhere. A server-side
  Last-Event-ID replay mechanism does not exist today in any form.

## Confirmed current state (via code inspection, not assumption)

- **No `id:`/`event:`/`retry:` SSE protocol fields are ever written.**
  Grepped every SSE writer in both `adapters/nethttp` and `adapters/chi` —
  each one writes exactly `fmt.Fprintf(sw, "data: %s\n\n", data)` and
  nothing else. A standards-compliant browser `EventSource` client
  already tracks the LAST received `id:` automatically and resends it as
  a `Last-Event-ID` request header on its own automatic reconnect — but
  since no `id:` is ever sent, this built-in browser behavior is
  currently a no-op against any go-codex SSE server.
- **`stream.BroadcastHub` has no past-event buffer.** `Subscribe()`
  registers a new subscriber and returns a `Stream[T]` that only receives
  values the hub forwards AFTER that call — confirmed via reading
  `NewBroadcastHub`/`Subscribe`'s implementation in `stream/broadcast.go`.
  There is no ring buffer, no persisted history, nothing to replay from.
- **`sse-client-consumption.md`'s `Consume`/`CallSSEAdapter` design (as
  resolved) has a FIXED exponential backoff** (`ConsumeOptions.MaxBackoff`,
  250ms initial step, doubling, capped) — no caller-supplied policy
  function, no distinction between pre-first-event and post-first-event
  failure counts, no give-up threshold.
- **`docs/roadmap/webhook-adapter.md`'s own "Retry policy shape" open
  decision** already sketches an IDENTICAL-SHAPE idea for a different
  context: `RetryPolicy func(attempt int, err error) (retry bool, wait
  time.Duration)` for webhook OUTBOUND delivery retries (finite,
  give-up-eventually) — worth cross-referencing as a shape precedent, NOT
  assuming the same implementation serves both (webhook retries are
  finite/give-up; SSE reconnection is naturally infinite/never-give-up
  today).

## Scope sketch (two independent sub-features — not required to ship together)

### A. `Last-Event-ID` resume

- **Server**: assign each emitted event an ID (monotonic counter? caller-
  supplied per-event ID from the domain event itself? both are plausible,
  not yet decided), write it as an `id: <id>\n` line before `data:`,
  maintain a bounded in-memory buffer of the N most recent (id, encoded
  event) pairs per `BroadcastHub`, and — when a NEW connection arrives
  carrying a `Last-Event-ID` request header — replay every buffered event
  after that ID before switching to live forwarding.
- **Client**: `Consume`/`CallSSEAdapter` tracks the last `id:` line seen
  from ANY event, and sends it as a `Last-Event-ID` request header on
  every (re)connect attempt (not just the first) — mirrors how a browser
  `EventSource` already behaves natively, but go-codex's OWN `Consume`
  loop needs to do this explicitly (it is not `net/http`-native
  behavior).
- Both directions require extending whatever parses/writes a raw SSE
  event block to handle MULTIPLE lines per event (`id:`, `data:`, and
  potentially `event:`/`retry:` — see "Out of scope" below for the
  `event:` field specifically) — today's writer/reader design (as
  resolved in `sse-client-consumption.md`) only ever handles a single
  `data:` line per event block. This is a bigger parsing-layer change
  than either doc originally assumed.

### B. Pluggable retry policy — two DISTINCT halves, not one shared mechanism

- **Client half** (go-codex's own `Consume`/`CallSSEAdapter`): a
  caller-supplied `RetryPolicy func(attempt int, err error) (retry bool,
  wait time.Duration)` replacing the fixed backoff — lets a caller do
  jittered backoff, per-error-type policies (e.g. give up immediately on
  a 404, retry forever on a network timeout), or a give-up-after-N
  threshold (directly answering `sse-client-consumption.md`'s own Open
  Design Decision 3).
- **Server half**: emit the standard SSE `retry: <milliseconds>\n\n`
  field, which spec-compliant `EventSource` browser clients honor
  NATIVELY as their own reconnect-delay hint — entirely independent of
  go-codex's own `Consume` (a browser tab using raw `EventSource` against
  a go-codex SSE server benefits from this even without ever touching
  `adapters/nethttp`'s client machinery at all). These two halves solve
  DIFFERENT problems for DIFFERENT consumers and are not one shared
  mechanism, even though both are colloquially "retry policy."

## Open questions (not yet explored)

- **Event ID source**: a monotonic per-`BroadcastHub` counter (simple,
  but meaningless across server restarts/multiple server instances behind
  a load balancer) vs. a caller-supplied ID extracted from the domain
  `Event` value itself (meaningful across restarts if the ID is
  content-derived, but requires a new declarative mechanism — something
  like `NewSSEEventIDParam`, mirroring the existing merge-field param
  constructors). Not decided.
- **Replay semantics when the requested `Last-Event-ID` has aged out of
  the buffer**: silently resume from the OLDEST buffered event (best-
  effort, may skip some), return an error/close the connection, or fall
  back to a full resync signal the client must handle? Not decided —
  each has different implications for at-least-once vs. best-effort
  delivery guarantees.
- **Buffer size / eviction policy**: fixed count per `BroadcastHub`? Time-
  window based? Configurable per route? Not decided.
- **Does `RetryPolicy`'s shape actually need to match
  `webhook-adapter.md`'s identical-looking idea**, or do the "infinite
  reconnect, no real give-up case" (SSE) vs. "finite delivery attempts,
  real give-up case" (webhook) semantics diverge enough that sharing one
  type would be a forced, leaky abstraction? Not decided — lean toward
  investigating whether a genuinely shared type is possible BEFORE
  assuming it is, given the semantic difference.
- **Does the server-side `retry:` field need to be caller-configurable**,
  or does a single fixed reasonable default (e.g. a few seconds) suffice
  for Phase 1 of THIS doc, once it has one? Not decided.
- **Is `id:` assignment/buffering scoped to `nethttp.SSEAdapter`
  specifically, or to `stream.BroadcastHub` itself** (making it available
  to any consumer of `BroadcastHub`, not just SSE)? `BroadcastHub` is a
  generic `stream` package primitive, not SSE-specific — buffering
  there could benefit other broadcast consumers too, but might also be
  scope creep beyond what THIS feature needs. Not decided.

## Out of scope (for this doc too, at least for now)

- **Persistent/durable cross-restart event buffer.** Even the modest
  in-memory `Last-Event-ID` replay buffer sketched above loses all
  history on a server restart. A durable buffer (backed by
  `ports.Cache`/`adapters/redis`, or a dedicated store) is a materially
  bigger feature with its own trade-offs (retention policy, storage cost,
  multi-instance consistency) — a plausible FUTURE follow-on to whatever
  this doc eventually designs, not part of it.
- **SSE's `event:` field (named/multiplexed event types).** A single SSE
  connection can carry several distinct named event "types" via the
  `event: <name>` line, letting one endpoint multiplex what go-codex
  today models as several separate `SSERoute`s (one `Event` type each).
  This is a related but SEPARATE capability from `Last-Event-ID`
  resume — noted here because it touches the same "parse/write multi-line
  SSE event blocks" machinery `Last-Event-ID` resume would also need, but
  it is a distinct feature with its own design questions (does one
  `SSERoute` become able to declare several `Event` types? how does the
  client dispatch by event name?) that this doc does not attempt to
  answer.

## Files to create

None yet — this doc is findings-only. Once resumed, this section should
follow `sse-client-consumption.md`'s eventual template (API surface,
structured errors, observer integration, unit test plan, files to
create) once the open questions above are resolved.
