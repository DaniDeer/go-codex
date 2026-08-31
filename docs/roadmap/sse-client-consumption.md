# SSE Client Consumption — findings

> **Status:** Findings only — no proposal, no driver yet. Spun out from
> [Middleware Workflow Simplification](../design/middleware-workflow-simplification.md)'s
> final critical review pass (item 6: `SSERoute`'s full chainable method
> set) while confirming what a client-side SSE story would need.
> **Deliberately DEFERRED**: the plan is to implement the REST
> declarative-middleware redesign FIRST, then return to this doc.
> [← Back to Roadmap](index.md)
>
> This doc captures ONLY what was confirmed via code inspection — no
> design decisions have been made, unlike `middleware-workflow-
> simplification.md`'s fully-resolved state. Treat every section below
> as "here is the starting point," not "here is the plan," same
> convention as [Events/ReqReply/Ports Workflow
> Simplification](events-reqreply-ports-workflow-simplification.md).

---

## Why this exists

While closing a gap in `middleware-workflow-simplification.md` (spelling
out `SSERoute`'s full new chainable method set — `WithHandler`/
`HandleMW`/`Implement`/`WithOptions`/`Register`), it became clear that
doc could only resolve the SERVER side of SSE (serving events to
connected clients) — because **no first-class, declarative SSE
CLIENT-consumption mechanism exists anywhere in go-codex today.** Unlike
security/client-credentials (which HAD an old, pre-`middleware`-package
design to redesign), this would be a GENUINELY NEW capability with no
existing shape to mirror.

## Confirmed current state (via code inspection, not assumption)

- **`examples/adapters-sse/main.go` reads its own SSE responses via a
  hand-rolled helper**, not a reusable mechanism:
  ```go
  // ── Helper: read all SSE data lines from a response ──────────────
  func readSSELines(resp *http.Response) []string
  ```
  Raw line-by-line parsing of the HTTP response body — no codec
  validation, no typed `Event` decoding, no reconnect/retry handling.
- **`adapters/nethttp/stream_errors.go` still carries two SSE-client-
  shaped error types whose doc comments reference a function that
  doesn't exist:**
  ```go
  // SSEConnectError is sent to [Stream.Errors] by [SSEClientStream] when an HTTP
  // connection attempt to the SSE endpoint fails. The stream retries after backoff;
  // this error is informational per reconnect attempt.
  type SSEConnectError struct { URL string; Attempt int; Err error }

  // SSEParseError is sent to [Stream.Errors] by [SSEClientStream] when an SSE
  // data line cannot be decoded using the provided format — malformed JSON, failed
  // codec validation, or other decode failure.
  type SSEParseError struct { /* ... */ }
  ```
  `SSEClientStream` — confirmed via repo-wide grep — does NOT exist
  anywhere in the current codebase. This is an apparent leftover from an
  earlier, since-removed stream-bridge helper (this repo has a
  documented history of removing old stream-bridge helpers in favor of
  port adapters — see `adapters/nethttp/binding.go`'s existing
  `SourceAdapter`/`SinkAdapter` pattern) that was never given a
  declarative replacement.
- **Both error types already reference `[Stream.Errors]`** — i.e.
  `stream.Stream[T]`'s existing `.Errors` channel, the SAME shape every
  other streaming consumption mechanism in this codebase already uses
  (mqtt5/zeromq subscribe bridges, etc.). This is a strong, pre-existing
  clue for what the eventual design's return shape should look like —
  not a decision, but a signal worth preserving for the next round.
- **The server side (`adapters/nethttp.SSEAdapter`, a `ports.SinkAdapter`)
  is unaffected and unrelated** — it SERVES events out over SSE to
  connected clients; it has nothing to do with CONSUMING a remote SSE
  endpoint as a client, which is the gap this doc is about.

## What a design round would need to answer (not yet explored)

None of the below are decided. Recorded as a starting checklist for
whoever picks this doc back up.

- **Return shape**: does a hypothetical `nethttp.CallSSE[Req, Event](ctx,
  caller, sseRoute, req) stream.Stream[Event]` (reusing the existing
  `stream.Stream[T]` type, `.Values`/`.Errors` channels) fit naturally,
  given `SSEConnectError`/`SSEParseError` already reference
  `[Stream.Errors]`? Or does SSE's connection lifecycle (long-lived,
  reconnect-with-backoff, one HTTP request producing many decoded
  values) need something else?
- **Client-side credentials**: does `SSERoute` need its own
  `.ClientMW(mw, fn)`, mirroring `Route.ClientMW` exactly, for the
  initial HTTP request's Authorization/credential headers? Presumably
  yes (an SSE client still needs to authenticate the same way a regular
  `Call` does), but not yet confirmed against `SSERoute`'s actual
  connection-establishment mechanics.
- **Reconnect/backoff policy**: `SSEConnectError.Attempt` implies a
  retry-with-backoff loop was already planned once — what's the
  reconnect policy (fixed backoff? exponential? caller-configurable?),
  and does a dropped/reconnected stream need a way to signal "resuming
  from scratch" vs. "resuming from the last received event" (SSE's own
  `Last-Event-ID` header) to the caller?
- **Where the codec-decode step lives**: `SSEParseError` implies
  decode failures are reported per-event via the stream's `.Errors`
  channel (not aborting the whole stream) — consistent with how other
  streaming bridges in this codebase already behave, but worth
  explicitly confirming rather than assuming.
- **Relationship to `nethttp.Call`**: is this a completely separate
  function family, or does it share machinery with the new unified
  `Call`/`Caller` (e.g. the SAME `*Caller` value works for both regular
  `Call` and a hypothetical `CallSSE`)? Given `SSERoute` and `Route` are
  different types, some sharing is likely possible but not yet explored.

## Explicitly out of scope for this doc, for now

- No API surface is proposed anywhere above — everything is "a
  starting point for the next round," not a decision.
- Sequencing: this doc is intentionally NOT worked further until
  `middleware-workflow-simplification.md`'s implementation ships — real
  implementation experience there should inform this doc's eventual
  design round, not the other way around, same rationale already
  established for [Events/ReqReply/Ports Workflow
  Simplification](events-reqreply-ports-workflow-simplification.md).
