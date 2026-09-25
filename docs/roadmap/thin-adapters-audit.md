# Thin Adapters Audit — moving misplaced dispatch logic into `api/rest`/`api/events`/`api/reqreply`

> **Status:** Design finalized across TWO rounds — an initial findings +
> detailed-design pass, then a critical re-examination (see "Critical
> review round") that revised 2 of the first pass's decisions (F4 split
> into F4a/F4b; `MiddlewareDispatchError` export reversed) and confirmed
> the rest. See "Detailed design" and "Resolved design decisions" below
> for the current, final state. Implementation has NOT started; it is
> intentionally deferred to a separate future session — see "Next step"
> at the end of this document.
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
misplacement. **This document originally recorded findings only**; a
follow-up design round (see "Detailed design" and "Resolved design
decisions" below) has since resolved the detailed API surface, migration
sequencing, and open questions this document's first pass deliberately
deferred. Implementation itself remains out of scope — see "Next step"
at the end of this document.

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
| F4 | `api/rest` | `adapters/nethttp/adapter.go`+`serve.go`, `adapters/chi/adapter.go`+`serve.go` | `validateSecurityCredentials`, `extractCredential`, `runSecurityMiddlewareReflect` | Byte-for-byte identical between nethttp and chi | **NUANCED — see its own section below.** Operates on `*http.Request` (stdlib, not a specific broker/protocol choice, but NOT currently imported by `api/rest`'s non-test files either). **Later SPLIT into F4a/F4b during the Critical review round below** — the 3 functions do not all share the same resolution. |

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
`net/http` in non-test files — **resolved below, in TWO parts, not one**
(see "Critical review round" and "Resolved design decisions" #1, plus
F4a/F4b's "Detailed design" sections): the 3 functions listed above do
NOT share one resolution. `extractCredential`/`validateSecurityCredentials`
(pure string extraction + codec validation) move directly into `api/rest`
with ZERO `net/http` dependency, once refactored to accept pre-extracted
values instead of `*http.Request`. `runSecurityMiddlewareReflect` (which
dispatches to a user-provided Fn whose documented CONTRACT is
`*http.Request`-shaped) stays adapter-tier, in a new
`adapters/internal/httpsecurity` package.

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

## Critical review round — reconsidering the first design pass

Before implementation, the first design pass's 5 decisions were
critically re-examined against the ACTUAL function bodies (not just
re-reading the reasoning that produced them). This surfaced 2 revisions
and confirmed 2 others with an added refinement, recorded here for
traceability rather than silently overwriting the original reasoning:

1. **F4 is NOT homogeneous — it bundles two genuinely different
   concerns, and treating them identically was a mistake.**
   `extractCredential`/`validateSecurityCredentials` are pure
   header/query/cookie STRING extraction + codec validation — the exact
   same primitive operation the adapter already performs elsewhere for
   ordinary header/query/cookie param decoding. This piece COULD be
   refactored to take pre-extracted string values (or a small
   adapter-supplied extraction closure) instead of `*http.Request`
   directly, eliminating the `net/http` dependency ENTIRELY — no shared
   package needed at all, it can move straight into `api/rest`. Verified
   feasible: every call site (`adapters/nethttp/adapter.go` ×2,
   `serve.go`, `binding.go`) already has `r *http.Request` in scope at
   the call site, so building a small extraction closure there instead
   of passing `r` through is a mechanical, local change.
   `runSecurityMiddlewareReflect` is different in KIND, not just degree —
   it dispatches to a USER-PROVIDED `middleware.ServerImplementation.Fn`
   whose CONTRACT (documented in `middleware/middleware.go`'s own godoc)
   is explicitly `func(ctx, raw *http.Request, req *Req) (map[string][]string, error)`.
   This genuinely cannot drop `*http.Request` — it IS the extensibility
   contract, not an implementation detail. **F4 is therefore SPLIT into
   F4a (moves to `api/rest`, zero `net/http`) and F4b (stays shared
   adapter-tier, `*http.Request` is unavoidable)** — see the revised
   Detailed design below.
2. **The "zero `net/http` imports" purity argument for F4 was weaker than
   originally presented.** `middleware.ServerImplementation.Fn`'s own
   godoc — in a package `api/rest` already imports — hard-codes the
   concrete `*http.Request` shape BY NAME in a doc comment, for
   type-assertion by adapters. The "`api/rest` has zero `net/http`
   imports" invariant is therefore already nominal (no compiled import)
   rather than conceptual (no `net/http`-shaped commitment at all). This
   doesn't change F4a's resolution (still moves cleanly, no `net/http`
   needed either way), but it DOES mean F4b's continued adapter-tier
   placement is not "preserving purity" so much as "avoiding a compiled
   import of a stdlib package `api/rest` doesn't otherwise need" — a
   real but narrower justification than the original text implied.
3. **Decision 5 (exporting `MiddlewareDispatchError`) is REVERSED.** The
   original design's own text already noted "callers today only ever see
   the WRAPPED `MiddlewareError`, never this dispatch-classification type
   directly" — meaning exporting it adds public API surface with NO
   identified consumer. Reversed to keep it unexported in all 3 core
   packages, consistent with this project's own "don't invent new API
   surface without a concrete need" principle. Revisit if/when a concrete
   external use case for inspecting the dispatch-classification directly
   (rather than the wrapped business error) actually appears.
4. **Decision 2 (reflection-in-core precedent) is CONFIRMED, with an
   added guardrail.** The reasoning (narrower than the generic-
   instantiation reflection `Server.RouteEntries()`/`SSEEntries()` avoid)
   holds up under re-examination. Added an explicit scope-creep guardrail
   (see the Detailed design F2/F3 sections below): the new
   `transform_dispatch.go` files are declared the ONLY reflection allowed
   in `api/rest`/`api/reqreply`; any future addition needing `reflect`
   in either package must justify itself against this same "already-
   type-erased `Fn any` only, never a generic instantiation" test, not
   simply point at this file as precedent for broader use.
5. **A deeper cross-API consolidation (one shared dispatch loop in the
   `middleware` package, instead of 3 parallel per-API copies) was
   evaluated and DECLINED, not left silently unconsidered.** Diffing all
   3 dispatch bodies directly (`adapters/nethttp/serve.go`'s
   `runMiddlewareHandlersReflect`, `adapters/mqtt5/transform_dispatch.go`'s
   `dispatchSubscribeMiddlewareHandlers`, `adapters/mqtt5/
   reqreply_transport.go`'s `dispatchServerMiddlewareHandlers`) confirms
   they share a common SHAPE (decode-in loop → `reflect.Value.Call` →
   error classification) but diverge meaningfully in return shape: REST
   collects `[]any` outs for later response composition; events is
   fire-and-forget (no `Out`/reply channel to encode into); reqreply
   merges topic/property vars PER-HANDLER inside the loop and returns
   named values with string `failKind` classification instead of a
   wrapped error type. Forcing these into one shared, heavily-
   parameterized helper (callback hooks for the merge behavior, an
   interface for the 3 different "what do I do with a decoded Out"
   strategies) would likely be MORE complex than the current design's 3
   parallel, per-API-typed copies — each already only ~25-45 lines. The
   3-copies design (one per API, matching each API's own
   `MiddlewareHandler` type) remains the chosen approach; this is
   recorded so a future reviewer doesn't re-propose the merge without
   knowing it was evaluated.

## Detailed design (final state, reflecting both rounds — see decisions table below)

All findings' package/file destinations, exported-type shapes, and
migration mechanics are now resolved (F4 as F4a/F4b, per the Critical
review round above). Implementation is intentionally NOT part of this
document — see "Next step" at the end.

### F1 → `api/events`

- New file `api/events/transform_dispatch.go` (mirrors the adapter file
  name, signals a direct migration).
- `events.middlewareDispatchError{...}` stays UNEXPORTED (**revised
  during the Critical review round — see decision #5 below**: no
  identified external consumer exists for this dispatch-classification
  type; callers only ever see the wrapped `events.MiddlewareError`).
  Carries the same fields the current unexported struct has, verified
  during implementation.
- The dispatch functions themselves ALSO become exported (unlike the
  error type above), as a
  Go-language necessity of moving cross-package (not a separate design
  preference — see "Corrections found" below):
  `events.DispatchSubscribeMiddlewareHandlers`,
  `events.DispatchPublishMiddlewareHandlers`, `events.OverrideDerivedVars`.
- mqtt v3's minor signature variant is resolved: ONE shared function,
  mqtt v3's adapter.go call site passes `nil`/an empty property map
  instead of getting its own signature (decision #3 below) — a one-line
  call-site change, confirmed no deeper divergence exists.
- All 3 adapters (`mqtt`, `mqtt5`, `zeromq`) delete their own copy of
  `transform_dispatch.go`'s logic and call the `api/events`-owned
  versions instead.

**Reflection scope-creep guardrail (applies to F2 and F3 both — see
decision #2 in "Resolved design decisions" below):** the new
`transform_dispatch.go` files this section describes are declared the
ONLY reflection allowed in `api/reqreply`/`api/rest`. Any future addition
wanting `reflect` in either package must independently justify itself
against the SAME "already-type-erased `Fn any` only, never a generic
instantiation" test these files satisfy — this file existing is not, by
itself, license for broader reflection use later.

### F2 → `api/reqreply`

- New file `api/reqreply/transform_dispatch.go`.
- `reqreply.middlewareDispatchError{...}` stays UNEXPORTED (**revised
  during the Critical review round — see decision #5 below**: no
  identified external consumer exists for this dispatch-classification
  type; callers only ever see the wrapped `reqreply.MiddlewareError`).
- Exported dispatch functions (cross-package-call necessity, unlike the
  error type above — Go's visibility rules require the FUNCTIONS the
  adapters call to be exported even though the error type they return
  internally does not need to be):
  `reqreply.DispatchServerMiddlewareHandlers`,
  `reqreply.DispatchClientMiddlewareIn`, `reqreply.DispatchClientMiddlewareOut`,
  `reqreply.MergeVarsOverride` — confirm during implementation whether
  `mergeVarsOverride` is ONLY ever called by the other 3 internally, in
  which case it can stay unexported inside the new file rather than
  exported; this is a call-graph check for implementation time, not a
  design blocker.
- This gives `api/reqreply` its first internal, unexported
  `reflect`-based dispatch helper (approved — decision #2 below), a
  deliberate departure from its current reflection-free public surface.
- Both `adapters/mqtt5`/`adapters/zeromq`'s `reqreply_transport.go`
  delete their copies and call the `api/reqreply`-owned versions.

### F3 → `api/rest`

- New file `api/rest/transform_dispatch.go` — this becomes `api/rest`'s
  FIRST file importing `"reflect"` (a first for the package, approved —
  decision #2 below; explicitly a NARROWER use of reflection than the
  generic-instantiation reflection `Server.RouteEntries()`/`SSEEntries()`
  exist specifically to avoid, since this only reflects over
  already-type-erased `middleware.ServerImplementation.Fn any`/
  `MiddlewareHandler.Fn any` values, never a generic function
  instantiation).
- `rest.middlewareDispatchError{...}` stays UNEXPORTED (same reversal as
  F2 above — decision #5 below).
- Exported dispatch functions: `rest.DispatchMiddlewareHandlers` (naming
  TBD at implementation time — the current unexported name embeds
  "Reflect" as an implementation detail a caller doesn't need to know
  about; propose dropping the suffix in the exported name) and
  `rest.CallObserveErrorResponseFor`.
- **Correction found this round** (see "Corrections found" below):
  both `adapters/nethttp`/`adapters/chi`'s `serve_sse.go` ALSO reference
  `runMiddlewareHandlersReflect`/`middlewareDispatchError`, not just
  `serve.go` — the original findings above only named `serve.go` as the
  file. F3's move touches a 4th call-site file per adapter total
  (`serve.go` + `serve_sse.go`, both the plain-server and SSE-server
  dispatch paths).
- Both adapters delete their copies and call the `api/rest`-owned
  versions from all call sites (`serve.go` AND `serve_sse.go`).

### F4a → `api/rest` (credential extraction + validation)

**Revised during the Critical review round** — F4 is NOT a single
destination; `extractCredential`/`validateSecurityCredentials` resolve
differently from `runSecurityMiddlewareReflect` (F4b, below).

- These 2 functions are pure header/query/cookie STRING extraction +
  codec validation — no genuine coupling to `*http.Request` itself, only
  to the OPERATION of reading a named header/query-param/cookie as a
  string, which the adapter already performs elsewhere for ordinary
  param decoding.
- Refactor the signature to accept a small extraction abstraction instead
  of `r *http.Request` directly — e.g. a `func(location, name string) (value string, ok bool)`
  closure the ADAPTER builds from its own `r.Header.Get`/
  `r.URL.Query().Get`/`r.Cookie()` calls (exact shape TBD at
  implementation time; a closure vs. a small `map[string]string` per
  location are both viable, whichever composes best with
  `rest.SecurityScheme`'s existing `Type`/`In`/`Name` fields).
- Once refactored this way, `ValidateSecurityCredentials` (exported, same
  cross-package necessity as F1/F2/F3) moves DIRECTLY into `api/rest` —
  no new package needed, and ZERO `net/http` import, satisfying `api/rest`'s
  "transport-agnostic REST API builder" design goal for REAL (a compiled
  invariant), not just nominally.
- Both `adapters/nethttp`/`adapters/chi`'s `adapter.go` delete their
  copies, build the small extraction closure from their own `r`, and call
  `rest.ValidateSecurityCredentials(closure, reqs, schemes)`.

### F4b → new `adapters/internal/httpsecurity` package (security-implementation dispatch)

- `runSecurityMiddlewareReflect` is different in KIND from F4a, not just
  degree — it dispatches to a USER-PROVIDED
  `middleware.ServerImplementation.Fn`, whose documented CONTRACT
  (`middleware/middleware.go`'s own godoc) is explicitly
  `func(ctx, raw *http.Request, req *Req) (map[string][]string, error)`.
  This genuinely cannot drop `*http.Request` — it IS the extensibility
  contract a user's own security implementation relies on, not an
  implementation detail this refactor can abstract away.
- New package `adapters/internal/httpsecurity` — scoped UNDER `adapters/`
  (Go's nested-`internal/` visibility restricts it to code rooted at
  `adapters/`), NOT at the repo root the way `internal/templatematch` is.
  This is a DELIBERATE divergence from that precedent's location, not an
  oversight: `internal/templatematch` is genuinely cross-protocol
  (shared by `mqtt`/`mqtt5`/`zeromq`, none of which are HTTP-based),
  while `httpsecurity` will NEVER be needed outside the net/http-family
  adapter tree (`nethttp`, `chi`, and any future adapter built on
  `net/http`) — the tighter `adapters/internal/` scope more precisely
  matches its actual, permanent audience.
- Contains: `httpsecurity.RunSecurityMiddlewareReflect` (exported, same
  cross-package necessity as elsewhere) — now the ONLY function needing a
  shared location, since F4a's other 2 functions moved to `api/rest`
  directly.
- Both `adapters/nethttp`/`adapters/chi`'s `serve.go` delete their copies
  and call `httpsecurity.RunSecurityMiddlewareReflect` instead.
- This is a narrower, more honest justification than the original design
  pass's framing: NOT "preserving `api/rest`'s purity" (that's F4a's
  achievement, for real) but "avoiding a compiled `net/http` import in a
  package (`api/rest`) that doesn't otherwise need one, for logic that is
  genuinely, permanently transport-coupled." The alternative considered
  and rejected: accepting `net/http` as a pragmatic stdlib exception
  directly in `api/rest` for this one function — rejected because F4a's
  clean resolution proves the exception isn't NECESSARY, only this one
  narrower piece is genuinely unavoidable.

### Corrections found during detailed-design review

Two gaps in the original findings, caught while confirming exact call
sites before finalizing this design (recorded here so a future reviewer
doesn't need to re-discover them):

- **F3 has a 4th caller file per adapter.** `serve_sse.go` (in both
  `adapters/nethttp` and `adapters/chi`) ALSO calls
  `runMiddlewareHandlersReflect`/`middlewareDispatchError`, not just
  `serve.go` as the original Findings table implied. Both files must be
  updated when F3 is implemented.
- **The dispatch FUNCTIONS themselves need exporting, not just the error
  type.** The original "Open design decisions" only asked whether
  `middlewareDispatchError` should be exported once moved to core — it
  didn't separately note that Go's package-visibility rules ALSO require
  the dispatch functions themselves (`dispatchSubscribeMiddlewareHandlers`,
  `dispatchServerMiddlewareHandlers`, `runMiddlewareHandlersReflect`,
  `validateSecurityCredentials`, etc.) to become exported once they move
  to a package the adapters merely IMPORT rather than define — this is a
  mechanical consequence of the move, not an independent design choice,
  but is spelled out explicitly per-finding above so implementation
  doesn't silently rediscover it as a surprise.

### Verified still accurate (this round's re-investigation)

This session re-diffed F1–F4 against current `HEAD` and found NO drift
from the original findings — all 4 remain byte-for-byte (or
near-byte-for-byte, for F1's mqtt v3 variant) duplicated exactly as
originally recorded. Two additional same-named-but-unrelated functions
were checked and confirmed NOT to be new findings, so a future reviewer
doesn't need to re-investigate them:

- `adapters/mqtt5/security.go`/`adapters/mqtt/adapter.go` each define
  their OWN `validateSecurityCredentials` — same name as F4's, but a
  DIFFERENT signature/domain (`events.SecurityScheme`, MQTT message
  types) with no cross-adapter duplication of its own (mqtt5's and
  mqtt's versions differ; they are not copies of each other the way
  F1–F4's functions are).
- `adapters/nethttp/client_middleware.go`'s generic
  `dispatchClientMiddlewareIn[Req any]` shares a name with F2's reqreply
  function but is REST's own client-side dispatch, with no chi-side
  duplicate to compare against (chi has no REST client component) — not
  a duplication finding.

## Out of scope (this round)

- Actually writing any code — this document records findings and a
  detailed design, but implementation is deferred to a separate future
  session (see "Next step" at the end of this document).
- `api/mcp`/`adapters/mcpgo` and `adapters/websocket` deep-audits (single
  adapter each today — no duplication possible, lower urgency).
- Any change to `adapters/*/topicvars.go` (confirmed already clean).
- Re-litigating already-fixed items (`ErrorPatternAs`/`HandleErrorPattern`/
  `Case`, `route.FirstSchemeName`, `rest.DiagnosticObserver`) — those are
  DONE, not part of this audit's scope.

## Resolved design decisions (previously open, now closed)

The 4 questions originally posed here have all been resolved — decision
#1 was REVISED during the Critical review round above (split, not a
single destination); a 5th decision (exporting `MiddlewareDispatchError`)
was added then REVERSED in the same round. Recorded as a table for quick
reference; rationale for each is expanded below.

| # | Question | Decision |
|---|---|---|
| 1 | F4's `net/http`-dependency tension | **REVISED — split, not one destination.** `extractCredential`/`validateSecurityCredentials` (F4a) move directly into `api/rest`, refactored to avoid `*http.Request` entirely — zero new package needed. `runSecurityMiddlewareReflect` (F4b) — genuinely `*http.Request`-coupled via its user-Fn contract — moves to a new `adapters/internal/httpsecurity` package (NOT the originally-proposed repo-root `internal/httpsecurity`; scoped tighter under `adapters/` since it's net/http-only forever) |
| 2 | Reflection-in-core precedent | **Approved, with an added guardrail** — `api/rest`/`api/reqreply` gain their first internal, unexported `reflect`-based dispatch helpers, explicitly declared the ONLY reflection allowed in either package |
| 3 | mqtt v3's minor signature variant (F1) | **Unify onto ONE shared `api/events`-owned function** — mqtt v3 always passes an empty/nil property map, no separate signature |
| 4 | Migration sequencing and breaking-change scope | **Confirmed**: zero external API impact for F1–F4 — every candidate function's call sites were grepped across the whole repo and found to be internal-only |
| 5 | Export `MiddlewareDispatchError`? (added, then reversed, same round) | **REVERSED — keep unexported** in all 3 core packages; no identified external consumer exists today |

### 1. F4's `net/http`-dependency tension — revised (split, not one destination)

The original design pass treated F4's 3 functions as one indivisible
unit and chose a single shared package for all of them. The Critical
review round found this was a mistake: `extractCredential`/
`validateSecurityCredentials` are pure string extraction + codec
validation, with NO genuine coupling to `*http.Request` beyond the
mechanical convenience of not having to build an extraction closure —
refactoring them to accept pre-extracted values instead lets them move
DIRECTLY into `api/rest` (F4a), achieving the "zero `net/http` imports"
goal for REAL rather than by relocating the dependency to an adjacent
package. `runSecurityMiddlewareReflect` (F4b) is different in kind — its
job IS to dispatch to a user-provided Fn whose documented contract is
`*http.Request`-shaped, so it cannot be refactored away the same way; it
moves to `adapters/internal/httpsecurity`, scoped under `adapters/`
(tighter than the originally-proposed repo-root `internal/httpsecurity`,
since — unlike `internal/templatematch`, genuinely used by non-HTTP
mqtt/zeromq adapters — this package will NEVER be needed outside the
net/http-family adapter tree). See F4a/F4b's Detailed design sections
above for the concrete layouts.

### 2. Reflection-in-core precedent — approved, with an added guardrail

`api/reqreply` and `api/rest` will each gain their first internal,
unexported `reflect`-based dispatch helper. This is explicitly NOT the
same class of reflection `Server.RouteEntries()`/`SSEEntries()` exist to
avoid (reflective instantiation of a GENERIC function) — F2/F3's moved
code reflects over an ALREADY-TYPE-ERASED `Fn any` value
(`middleware.ServerImplementation.Fn`/`MiddlewareHandler.Fn`), a
narrower, already-necessary use that doesn't compromise either package's
own reflection-free PUBLIC surface (the new files/functions stay
internal to core, called only by adapters, never re-exposed as a public
reflection-based API). **Confirmed during the Critical review round,
with one addition**: the new `transform_dispatch.go` files in both
packages are explicitly declared the ONLY reflection allowed in
`api/rest`/`api/reqreply` — a guardrail against future scope creep. Any
later addition wanting `reflect` in either package must independently
pass the SAME "already-type-erased `Fn any` only, never a generic
instantiation" test, not simply cite this file as blanket precedent.

### 3. mqtt v3's minor signature variant — resolved

Unify onto ONE shared function in `api/events`; mqtt v3's adapter.go call
site passes `nil`/an empty property map rather than requiring its own
distinct function signature. Confirmed during this round's
re-investigation that `dispatchSubscribeMiddlewareHandlers`'s existing
structure already treats the property map as an optional/nilable
parameter internally, so no deeper divergence beyond the call site needs
resolving.

### 4. Migration sequencing and breaking-change scope — confirmed

All candidate functions across F1–F4 were grepped by name across the
ENTIRE repository's non-test `.go` files. Every call site is within the
SAME adapter package that defines the function (or a same-named,
unrelated sibling in another adapter package — see "Verified still
accurate" above). None of the 13 candidate functions/types are
externally reachable via another exported wrapper. Moving them is a pure
internal refactor with ZERO external API impact for all 4 findings —
sequencing between F1/F2/F3/F4a/F4b can therefore be chosen purely for
implementation convenience, not constrained by any compatibility
concern.

### 5. Export `MiddlewareDispatchError`? — added, then reversed, same round

The original design pass proposed exporting `MiddlewareDispatchError` in
all 3 core packages once moved, reasoning it would become "a natural,
useful `errors.As`-navigable type." The Critical review round reversed
this: the design pass's OWN text already noted "callers today only ever
see the WRAPPED `MiddlewareError`... never this dispatch-classification
type directly" — meaning exporting it adds public API surface with NO
identified consumer, which cuts against this project's own "don't invent
new API surface without a concrete need" principle (echoed elsewhere in
this codebase's own review guardrails). `MiddlewareDispatchError` stays
unexported/internal to each of `api/rest`/`api/events`/`api/reqreply`.
Revisit if/when a concrete external use case for inspecting the
dispatch-classification directly (rather than the wrapped business
error) actually appears — nothing here is a permanent prohibition, just
a decision not to speculate ahead of a real need.

### A cross-API consolidation opportunity — considered and declined

A question NOT posed by the original design pass, raised during the
Critical review round: instead of moving F1/F2/F3 into 3 SEPARATE,
per-API `transform_dispatch.go` files, could their dispatch loops be
consolidated ONE level deeper, into a single shared helper in the
`middleware` package (which all 3 of `api/rest`/`api/events`/`api/reqreply`
already depend on)? Diffing all 3 actual bodies directly —
`runMiddlewareHandlersReflect` (REST), `dispatchSubscribeMiddlewareHandlers`
(events), `dispatchServerMiddlewareHandlers` (reqreply) — confirms they
share a common SHAPE (decode-in loop → `reflect.Value.Call` → error
classification) but diverge meaningfully in what they DO with the
result: REST collects `[]any` outs for later response composition;
events is fire-and-forget (subscribe has no `Out`/reply channel to
encode into); reqreply merges topic/property vars PER-HANDLER inside the
loop and returns named values with a string `failKind` classification
instead of a wrapped error type. Forcing these into one shared, heavily-
parameterized helper (callback hooks for the merge behavior, an
interface abstracting the 3 different "what do I do with a decoded Out"
strategies) would likely be MORE complex than the current design's 3
parallel, per-API-typed copies (each already only ~25–45 lines short).
**Declined** — the 3-copies design (one per API, matching each API's own
`MiddlewareHandler` type) remains the chosen approach. Recorded here so
a future reviewer doesn't re-propose the merge without knowing it was
evaluated and why it wasn't pursued.

## Forward-looking guardrail: adapters as pure protocol shims (preparing for Protocol-Native Features)

F1–F4/F4a/F4b above are a RETROSPECTIVE audit — fixing existing
misplaced logic. This section is the PROSPECTIVE counterpart: a
permanent guardrail preventing NEW misplaced logic (or a competing,
ad-hoc capability mechanism) from being introduced going forward,
especially during F1–F4a/F4b's own eventual implementation and any
future adapter/API/port addition.

> **Adapters are thin protocol shims:** wrap exactly one external SDK,
> contain zero declarative/business logic, and expose any
> adapter-specific extension (MQTT QoS, User Properties, ...) as a
> SEALED, adapter-owned `Capability` interface supplied only at
> `Attach`/`Bind` time — never baked into an `api/*` declaration. All
> declaration, validation, and dispatch logic lives in `api/*`/`ports`.
> A user's entire vocabulary is `api/*`/`ports`: declare, compose,
> attach — the adapter package is touched only to `Attach`/`Bind` and
> supply its `Options`/`Capability` values, never as a second
> business-logic API.

### Already shipped vs. genuinely new

Most of this guardrail is not new — it restates two ALREADY-MANDATORY
rules, cross-referenced rather than re-derived here:

- **`add-a-new-adapter/SKILL.md`'s Step 5c** ("no bare escape hatch —
  every adapter entry point is one of 3 handle-based shapes") already
  enforces "an adapter contains zero independent decode/encode/dispatch
  logic."
- **`add-a-new-adapter/SKILL.md`'s Step 5d** ("convenience helpers belong
  in `api/*`, not adapters") already enforces "user-facing/business
  logic lives in `api/*`/`ports`, never in an adapter package."
- Both are written up user-facing in
  [`docs/concepts/ports-and-adapters.md`](../concepts/ports-and-adapters.md)'s
  "Relationship to adapters used directly" and "Convenience helpers
  belong in `api/*`, not adapters" sections.

The genuinely NEW piece is the sealed `Capability` interface mechanism —
designed in [Protocol-Native Features](protocol-native-features.md)'s
§2.1/§2.2 (mirrors `ports.Pattern`'s own sealing technique, one sealed
interface PER adapter package, supplied at `Attach`/`Bind` time,
Go-compiler-enforced rejection of a mismatched adapter's capability
value) but **NOT YET IMPLEMENTED**. Stating it here, ahead of its own
implementation, exists to prevent an interim adapter change from
inventing a competing, non-sealed, ad-hoc "capability-like" toggle
(e.g. a loose `bool`/string-ID field on an `Options` struct for a new
protocol-specific behavior) that would need undoing once the real
mechanism ships.

### Watch-list entry — the one confirmed existing exception

A fresh, repo-wide grep sweep (`api/rest`/`api/events`/`api/reqreply`
non-test files, searching for protocol-specific vocabulary such as
`MQTT`/`AMQP`/`ZeroMQ`) confirms exactly ONE existing instance of the
anti-pattern this guardrail targets: **`api/events/mqtt_qos.go`**
(`MQTTQoS`, `Subscribe.QoS`, `Publish.SubscribeQoS`) — MQTT-specific
vocabulary living inside the transport-agnostic `api/events` package,
consumed by `adapters/mqtt`/`adapters/mqtt5` as a fallback default and
silently ignored by `adapters/zeromq`. `api/rest`/`api/reqreply` are
CONFIRMED CLEAN (only doc-comment mentions of `validate.MQTTPublishTopic`
as an example value, not actual core-type leakage).

This is not a NEW discovery — it is already fully cataloged, with its
fix already designed, in
[Protocol-Native Features](protocol-native-features.md)'s §5.1 ("MQTT
QoS — shared by two-of-three adapters"): once `Capability` ships,
`mqtt_qos.go` is deleted, and QoS becomes `adapters/mqtt.QoS`/
`adapters/mqtt5.QoS` — two SEPARATE sealed capability types, not one
shared enum, even though today's values are numerically identical
between the two MQTT versions. Recorded here as a cross-reference (not
re-derived in full) so a reader of THIS document has the one confirmed
exception in hand without needing to search the considerably longer
`protocol-native-features.md` to find it. Its actual FIX is deferred to
that document's own implementation — not part of this round's scope.

## Next step

This document's design is now finalized (findings confirmed accurate
against current code, all open questions resolved with concrete
package/file/function-level detail, PLUS a forward-looking guardrail for
future adapter/API/port work). **Implementation is intentionally NOT
part of this round** — actually moving the code (F1–F4a/F4b, in whatever
order implementation convenience dictates, per decision #4 above) is
planned as a separate future session, which should read this document in
full before starting, follow its Detailed design section per-finding,
and treat the "Forward-looking guardrail" section as a standing
constraint on the implementation itself (do not introduce a competing,
non-sealed capability mechanism while moving F1–F4b's own code).
