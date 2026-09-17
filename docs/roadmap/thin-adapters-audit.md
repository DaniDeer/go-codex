# Thin Adapters Audit — moving misplaced dispatch logic into `api/rest`/`api/events`/`api/reqreply`

> **Status:** Design draft — findings recorded, NOT yet designed in detail or implemented.
> [← Back to Roadmap](index.md)
>
> **Relationship to [Protocol-Native Features](protocol-native-features.md)
> — stated up front, not buried:** that doc's "Why adapter-owned capability
> declaration doesn't violate the thin-adapter, protocol-agnostic-
> declaration principle" section already defines the exact test this doc
> reuses: a capability/helper belongs in shared core `api/*` only if it
> clears BOTH (1) compatible mechanical shape across every adapter that
> implements the boundary, and (2) uniform-enough support across those
> adapters — `Security` is that doc's own worked example of something that
> clears both bars and lives in core. **This document is the MIRROR-IMAGE
> investigation**: instead of asking "does a NEW protocol-native capability
> clear the bar for core-layer, protocol-agnostic declaration," it asks
> "does EXISTING adapter-owned dispatch logic ALREADY clear that bar,
> unnoticed, and is therefore misplaced today." Findings F1–F3 below are
> confirmed to clear both bars (the same reasoning that puts `Security` in
> core); F4 is the interesting overlap/test case — see its own section.

## Motivation

A direct user question about `examples/reqreply-api/demo_error_pattern.go`
("why do I need `mqtt5adapter.Case` — why isn't there a transport-agnostic
`client.Case`?") surfaced a confirmed, real design-guardrail violation:
`ErrorPatternAs`/`HandleErrorPattern`/`Case` were duplicated (byte-for-byte,
for reqreply) across `adapters/nethttp`/`adapters/mqtt5`/`adapters/zeromq`
even though they touch ONLY core `api/*` types (a shared
`ErrorPatternValuer` interface) and have zero protocol-specific logic. That
fix (see `docs/design/d-0005-error-handling.md`'s Topic 6
"Design guardrail" subsection and `docs/concepts/ports-and-adapters.md`'s
"Convenience helpers belong in `api/*`, not adapters" section) established
the permanent rule:

> Adapters implement wire protocols only. A user works ENTIRELY in the
> `api/*`/`ports` abstraction — declaring routes/channels/patterns/ports —
> and ATTACHES an adapter only for the concrete IO implementation of a
> protocol already chosen. Any helper that only touches core `api/*` types
> belongs in `api/*`, never in `adapters/*`, even when only one adapter
> implements that boundary today.

This roadmap doc is the result of applying that same test systematically
across `api/rest`+`adapters/nethttp`/`chi`, `api/events`+`adapters/mqtt`/
`mqtt5`/`zeromq`, and `api/reqreply`+`adapters/mqtt5`/`zeromq` — looking
specifically for OTHER dispatch/business logic that (a) touches only core
`api/*` types, and (b) is duplicated verbatim (or near-verbatim) across 2+
adapter packages within the same API, the strongest possible signal of
misplacement. **This is a findings/audit document only** — detailed API
surface, migration sequencing, and open questions below are intentionally
left for a follow-up design pass, per the user's explicit request.

## Method

For each API, every same-purpose file across its adapter packages (e.g.
`transform_dispatch.go` in `adapters/mqtt`/`mqtt5`/`zeromq`) was diffed
after normalizing package-name/doc-comment differences. A near-zero diff
on a function that takes ONLY core `api/*` types (never a transport SDK
type like `*pahomqtt5.Publish`, `*http.Request`, or a `FramedSocket`) was
treated as a strong misplacement signal — equivalently, this is
[protocol-native-features.md](protocol-native-features.md)'s two-part
test (compatible mechanical shape + uniform-enough support) being
satisfied for something CURRENTLY adapter-owned. Files that already
delegate to a shared internal package (e.g. `adapters/*/topicvars.go`,
which all 3 events adapters already reduce to a thin "extract the topic
string from the transport-specific message" wrapper around the shared
`internal/templatematch` matcher) were confirmed CLEAN and are recorded
here so they are not re-investigated later.

## Findings

| ID | API | Files | Functions | Duplication | Touches only core types? |
|---|---|---|---|---|---|
| F1 | `api/events` | `adapters/mqtt/transform_dispatch.go`, `adapters/mqtt5/transform_dispatch.go`, `adapters/zeromq/transform_dispatch.go` | `dispatchSubscribeMiddlewareHandlers`, `dispatchPublishMiddlewareHandlers`, `overrideDerivedVars`, `middlewareDispatchError` | mqtt5 ↔ zeromq: functionally identical (doc comments literally say "Mirrors adapters/mqtt5's identical function" 3×); mqtt (v3): same shape, minor signature variant (single `vars` map instead of separate topic/property maps, since mqtt v3 has no property mechanism) | YES — `events.MiddlewareHandler`/`ClientMiddlewareHandler`/`MiddlewareError` only, dispatched via `reflect.Value.Call` |
| F2 | `api/reqreply` | `adapters/mqtt5/reqreply_transport.go`, `adapters/zeromq/reqreply_transport.go` | `dispatchServerMiddlewareHandlers`, `dispatchClientMiddlewareIn`, `dispatchClientMiddlewareOut`, `mergeVarsOverride` | Byte-for-byte identical between mqtt5 and zeromq (doc comments say "Mirrors adapters/mqtt5's identical dispatch function" 2×) | YES — `reqreply.MiddlewareHandler`/`ClientMiddlewareHandler`/`MiddlewareError` only, dispatched via `reflect.Value.Call` |
| F3 | `api/rest` | `adapters/nethttp/serve.go`, `adapters/chi/serve.go` | `runMiddlewareHandlersReflect`, `middlewareDispatchError`, `callObserveErrorResponseFor` | Byte-for-byte identical between nethttp and chi (confirmed via direct diff — only 1 doc-comment line differs, purely descriptive) | YES — `rest.MiddlewareHandler`, `stats.Observer`, `rest.ErrorPatternResponse` only, via reflection, NO I/O (response-writing stays in `tryRespondErrorPattern`, correctly adapter-owned) |
| F4 | `api/rest` | `adapters/nethttp/adapter.go`+`serve.go`, `adapters/chi/adapter.go`+`serve.go` | `validateSecurityCredentials`, `extractCredential`, `runSecurityMiddlewareReflect` | Byte-for-byte identical between nethttp and chi | **NUANCED — see its own section below.** Operates on `*http.Request` (stdlib, not a specific broker/protocol choice, but NOT currently imported by `api/rest`'s non-test files either) |

### F1–F3 against the two-part test

Both bars from `protocol-native-features.md` are cleanly satisfied for
all three:

1. **Compatible mechanical shape**: identical (or near-identical, for
   mqtt v3's single-map variant) across every adapter that implements the
   boundary — proven via direct diff, not asserted.
2. **Uniform-enough support**: every adapter within the same API needs
   the SAME dispatch (any channel/route declaring a `Middleware[In,Out]`
   needs this exact dispatch loop, regardless of transport) — there is no
   "attach without it" case to gracefully degrade for, unlike (say) MQTT5
   User Properties, which MQTT v3/ZeroMQ simply don't have at all.

This is the SAME two-bar clearance that justifies `Security` living in
core `middleware`/`api/events` today (per `protocol-native-features.md`'s
own worked example) — F1–F3 are not "just duplicated code," they are
capabilities that already clear the bar for core-layer, protocol-agnostic
ownership and simply haven't been moved there yet.

### F4 — the overlap/test case

`validateSecurityCredentials`/`extractCredential`/
`runSecurityMiddlewareReflect` are ALSO byte-for-byte identical between
nethttp and chi, so bar 1 (compatible mechanical shape) is trivially
satisfied. Bar 2 (uniform-enough support) is more interesting: EVERY
realistic REST server adapter in the Go ecosystem is built on
`net/http` (chi, gorilla/mux, echo's underlying request type all wrap
`*http.Request`) — arguably passing bar 2 as cleanly as `Security` does.
If both bars are satisfied the same way `Security`'s are, that argues FOR
moving this into `api/rest` despite `api/rest` not currently importing
`net/http` in non-test files — see Open design decision #1 below, which
poses this exact question without resolving it.

### Confirmed CLEAN (already correctly factored — do not re-investigate)

- `adapters/{mqtt,mqtt5,zeromq}/topicvars.go` — each adapter's
  `TopicVarsFromMessage` is a THIN, genuinely transport-specific wrapper
  (extracting the topic string from `*pahomqtt5.Publish` / a raw ZeroMQ
  frame / etc.) around the ALREADY-SHARED `internal/templatematch`
  package, which owns 100% of the actual template-matching logic. No
  further consolidation opportunity here — the remaining per-adapter code
  cannot be anything BUT transport-specific (there is no core-layer
  concept of "a topic string" independent of how a given broker delivers
  one) — this correctly FAILS bar 2 in reverse (the extraction mechanism
  itself has no shared shape across brokers), so it stays adapter-owned,
  the same way `protocol-native-features.md`'s own QoS/addressing
  examples correctly fail the bar and stay adapter-owned/sealed.
- `api/rest`/`adapters/nethttp`/`adapters/chi`'s existing
  `tryRespondErrorPattern` (writes the HTTP response body/headers) is
  correctly adapter-owned — it performs real I/O
  (`http.ResponseWriter.Write`/`.WriteHeader`), unlike the
  `callObserveErrorResponseFor` piece it calls internally (F3 above),
  which does no I/O and IS misplaced.
- `route.FirstSchemeName`, `rest.DiagnosticObserver`/`Report*Errors` — both
  ALREADY correctly live in shared/core locations (previous review
  rounds), confirmed still true, not re-flagged.
- `api/mcp` + `adapters/mcpgo` — only ONE adapter exists, so no
  duplication is structurally possible today; not deep-audited this round
  (lower priority — revisit if/when a second MCP-shaped adapter appears,
  applying the SAME two-part test proactively at that time rather than
  retroactively).
- `adapters/websocket` + `adapters/chi`'s socket shim — chi's websocket
  support already delegates to `adapters/websocket` via a
  `swapHandler`-as-`Mux` shim (confirmed intentional in existing review
  history) — not re-audited in depth this round.

## Proposed direction (to be designed in detail later)

For F1–F3 (the clear-cut cases — pure reflection dispatch over core
`api/*` types, zero transport SDK references, zero I/O, both
protocol-native-features.md bars cleared):

- Move `dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers`/
  `overrideDerivedVars` into `api/events` (generic, `[T any]`, taking
  `events.MiddlewareHandler`/`ClientMiddlewareHandler` slices) — mqtt v3's
  minor signature variant (no property map) can likely be handled by
  always passing an empty/nil property map rather than a separate
  function signature, but this needs confirming during detailed design.
- Move `dispatchServerMiddlewareHandlers`/`dispatchClientMiddlewareIn`/
  `dispatchClientMiddlewareOut`/`mergeVarsOverride` into `api/reqreply`
  (reflection-based, taking `reflect.Value` — mirrors how `api/rest`'s
  OWN `serve.go` reflection helpers are already adapter-owned today, so
  this may set a NEW precedent of `api/reqreply` itself holding a
  reflection-based dispatch helper, worth flagging explicitly during
  design since it's a departure from `api/reqreply`'s current
  reflection-free public surface).
- Move `runMiddlewareHandlersReflect`/`middlewareDispatchError`/
  `callObserveErrorResponseFor` into `api/rest` (same reflection-based
  shape) — `api/rest` would gain its FIRST `reflect`-using file, same
  precedent question as above.
- `middlewareDispatchError` itself is currently unexported per-package —
  moving it to core makes it a natural candidate to EXPORT (as
  `events.MiddlewareDispatchError`/`reqreply.MiddlewareDispatchError`/
  `rest.MiddlewareDispatchError`?) or keep unexported-but-shared within
  the core package — an open question for detailed design (currently
  leaning toward keeping it unexported/internal-only, since callers
  today only ever see the WRAPPED `events.MiddlewareError`/etc., never
  this dispatch-classification type directly).

For F4 (the nuanced case), see Open design decisions below — no
direction proposed yet, this is exactly the kind of question flagged for
later, more careful design.

## Out of scope (this round)

- Actually writing any code — this document only records findings.
- `api/mcp`/`adapters/mcpgo` and `adapters/websocket` deep-audits (single
  adapter each today — no duplication possible, lower urgency).
- Any change to `adapters/*/topicvars.go` (confirmed already clean).
- Re-litigating already-fixed items (`ErrorPatternAs`/`HandleErrorPattern`/
  `Case`, `route.FirstSchemeName`, `rest.DiagnosticObserver`) — those are
  DONE, not part of this audit's scope.

## Open design decisions (to resolve during detailed design)

1. **F4's `net/http`-dependency tension, reframed against
   `protocol-native-features.md`'s two-part test**:
   `validateSecurityCredentials`/`extractCredential`/
   `runSecurityMiddlewareReflect` operate on `*http.Request` — a stdlib
   type shared by BOTH `nethttp` and `chi` (chi is itself built on
   `net/http`), but `api/rest` currently has ZERO `net/http` imports in
   its non-test files, consistent with its stated "transport-agnostic
   REST API builder" design. As argued above, this arguably clears BOTH
   of `protocol-native-features.md`'s bars the same way `Security` does.
   Two candidate directions, neither obviously correct without more
   discussion:
   - **(a)** Accept `net/http` as a pragmatic, permanent exception in
     `api/rest` — argument: `net/http.Request` is a STANDARD LIBRARY type,
     not a specific broker/vendor choice (unlike `*pahomqtt5.Publish` or a
     ZeroMQ socket), and every realistic REST server adapter in the Go
     ecosystem is built on it — so this wouldn't actually compromise
     transport-independence in practice, mirroring how `Security` living
     in core doesn't compromise `api/events`'s transport-independence
     despite every adapter's OWN enforcement mechanics differing.
   - **(b)** Introduce a new shared internal package (e.g.
     `adapters/internal/nethttpshared` or similar) that BOTH `nethttp` and
     `chi` import, keeping `api/rest` importing nothing beyond the
     standard library's absence of `net/http`. Downside: a THIRD package
     in the dependency graph for what's conceptually "core" logic, and a
     departure from the "core lives in `api/*`" rule this whole document
     is about — arguably just moves the duplication problem sideways
     rather than solving it, unless `api/rest` truly must never import
     `net/http` under any circumstance.
   - This decision should be made ONCE (it will recur for any future
     `net/http`-based REST adapter), not per-finding.
2. **Reflection-in-core precedent**: F2 and F3's proposed moves would give
   `api/reqreply` and `api/rest` their first internal, unexported
   `reflect`-based dispatch helpers (today, `reflect` usage is entirely
   adapter-owned — `api/rest`'s own public surface is confirmed
   reflection-free via `Server.RouteEntries()`/`SSEEntries()` accessors
   specifically to keep the CORE package reflection-free for its own
   heterogeneous-collection needs). Moving `runMiddlewareHandlersReflect`
   et al. into `api/rest` would NOT reflectively instantiate a generic
   function (the thing `Server.RouteEntries()` was designed to avoid) —
   it reflects over an ALREADY-TYPE-ERASED `middleware.ServerImplementation.Fn
   any`/`MiddlewareHandler.Fn any`, which is a different, narrower use of
   `reflect` than what `api/rest` avoids elsewhere. Worth an explicit,
   deliberate call during design on whether this is an acceptable
   precedent or a reason to keep these 3 functions adapter-owned despite
   satisfying the "core types only" test.
3. **mqtt v3's minor signature variant (F1)**: confirm whether unifying
   mqtt v3's single-`vars`-map signature with mqtt5/zeromq's two-map
   (topic + property) signature is possible via "mqtt v3 always passes an
   empty property map" (as `dispatchSubscribeMiddlewareHandlers` already
   does structurally) without changing any OTHER adapter's call sites, or
   whether mqtt v3 genuinely needs its own thin wrapper around a
   shared core implementation.
4. **Migration sequencing and breaking-change scope**: unlike the
   `ErrorPatternAs` fix (additive from the CALLER's perspective — call
   sites just changed their import), these dispatch functions are
   INTERNAL/unexported today, so moving them has ZERO external API
   impact — purely an internal refactor. Confirm this remains true for
   all of F1–F3 before starting implementation (i.e. none of these
   functions are accidentally reachable/relied upon externally via
   another exported wrapper).
