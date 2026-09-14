# `api/events` pub/sub `Observability[T]` — consolidate 3 duplicated adapter implementations

> **Status:** Idea only — no code written, no spike run yet. Spun out of
> shipping [`api/reqreply.Observability`](../features/observer.md#api-reqreplyobservability--a-shipped-declarative-observer-wrapper)
> while confirming the "thin adapters, thick api layer" architecture
> principle applies equally to Observer wiring.
> [← Back to Roadmap](index.md)

## Motivation

`adapters/mqtt`, `adapters/mqtt5`, and `adapters/zeromq` each ship their
OWN `Observability[T](obs) func(func(context.Context, T) error)
func(context.Context, T) error` — the general-purpose,
declare-time-attachable `.SubscribeMW(nil, ...)`/`.PublishMW(nil, ...)`
observer wrapper for `api/events` pub/sub. Confirmed via direct code
read that `adapters/zeromq/observability.go`'s implementation is
**already 100% adapter-agnostic** — it does nothing but
`stats.WithObserver`/`stats.WithDiagnostics` ctx injection plus a
post-`next` `Diagnostics` drain, with ZERO reference to any zeromq-
specific type. It could be moved into `api/events` itself, unchanged,
with no adapter left needing its own copy for that behavior.

`adapters/mqtt5`'s and `adapters/mqtt`'s versions additionally call
`stats.Observer.RecordSubscribe`/`RecordPublish` directly (plus a
`stats.TraceObserver` span), using each adapter's own
`MessageFromContext` (an adapter-specific, concretely-typed function
returning the RAW paho message) to detect subscribe vs. publish
direction — this part is genuinely adapter-specific and cannot move
into `api/events` unchanged.

This mirrors the SAME pattern `api/reqreply`'s shipped
`reqreply.Observability[Req, Resp]` helper confirmed for its own
general-purpose decorator: the reusable core (ctx injection +
Diagnostics drain, no self-recorded `RecordRequest`/span since the
adapter transport already does that) belongs in the API layer; only
genuinely transport-specific behavior (raw message access, direction
detection) stays adapter-owned.

## Candidate design (NOT scoped/decided — sketch only)

- Move the ctx-injection + Diagnostics-drain core into
  `events.Observability[T](obs stats.Observer) func(func(context.Context,
  T) error) func(context.Context, T) error` (mirrors
  `reqreply.Observability[Req, Resp]`'s exact shape/behavior, adapted to
  events' single-`T` pub/sub Fn shape).
- `adapters/zeromq.Observability[T]` becomes a thin, possibly-deprecated
  alias calling `events.Observability[T]` directly — or is removed
  entirely in favor of callers using `events.Observability[T]` directly
  (breaking change, needs a compat/migration decision).
- `adapters/mqtt5.Observability[T]`/`adapters/mqtt.Observability[T]` keep
  their OWN thin wrapper that calls `events.Observability[T](obs)`
  internally for the shared part, then ADDS the
  `RecordSubscribe`/`RecordPublish` + `TraceObserver` span +
  `MessageFromContext` direction detection on top (the genuinely
  adapter-specific remainder) — net effect: same public behavior,
  roughly half the code, single source of truth for the shared part.

## Open questions (none resolved — this is a landing place, not a plan)

- Is `adapters/zeromq.Observability[T]`'s current public signature
  already relied upon by real callers? If so, is a deprecated-but-
  functional alias acceptable, or does this need a hard breaking
  removal (mirrors this codebase's general willingness to accept
  breaking changes for simplicity, confirmed in several prior rounds —
  but not pre-decided here)?
- Should `RecordSubscribe`/`RecordPublish` + span direction detection
  become a small, adapter-supplied callback/interface parameter to a
  SHARED `events.Observability[T]`, rather than three independent
  adapter-level wrapper functions? (Sketch only — not evaluated for
  ergonomics yet.)
- Does this consolidation belong on the SAME timeline as any future
  `api/mcp`/`ports` Observer work, or is it fully independent?

Not implemented — investigation/planning only. No committed scope, no
priority assigned yet.
