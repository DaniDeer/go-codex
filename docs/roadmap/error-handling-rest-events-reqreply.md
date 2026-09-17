# Unified Error Handling — REST, Events, ReqReply (destined for D-0005)

> **Status:** Implemented and verified — all 7 phases shipped
> (`gofmt`/`go build`/`go test -count=1 ./...`/`just check` all clean),
> including a follow-up consistency-review round that closed several
> gaps found by cross-checking this document against the actual code:
> `DeadLetter`'s AsyncAPI channel registration (previously documented as
> "DECIDED" but not implemented), mqtt v3's Implementations-based
> security dispatch (previously bypassed `events.SecurityError`
> wrapping/`ErrorChannel` eligibility on its documented primary subscribe
> workflow), mqtt5 User Property param validation (now
> `ErrorChannel`/`ErrorPattern`/`DeadLetter`-eligible, closing an
> asymmetry with REST's own wired header-param validation), plus missing
> test coverage for `ObserveErrorResponseFor`/`HasErrorPatterns`/
> `ErrorPatternObserver` (core-layer AND real-dispatch) and Topic 7's
> publish-side exclusion invariant, and the security-disclosure guidance
> `docs/guides/error-handling.md` now documents. This document has NOT
> yet been physically moved/renamed to `docs/design/d-0005-error-handling.md`
> — ~55 files across the repo reference it by its current path, and that
> rename is a separate, optional follow-up (pure doc-reorganization, not
> a functional change) rather than something this implementation round
> addressed.
>
> **Scope widened in a later review round (H1)**: `adapters/nethttp` and
> `adapters/chi` each have a SEPARATE HTTP dispatch function,
> `handlerFunc[Req, Resp any]`, backing the port/stream-binding entry
> points `IngestAdapter`/`LatestAdapter`/`HandlerLatest`/`PipelineHandler`
> — distinct from `serve.go`'s `serve`, which this document's Topic 1/4/5
> fixes were originally scoped to ONLY. `handlerFunc` fully supports
> declared `ErrorPattern`s via `rest.RouteHandle` but never consulted them
> at any Category-A dispatch point except the handler's own business
> error (and even that used the bare, non-observability `ErrorResponseFor`
> instead of `ObserveErrorResponseFor`). This was closed identically to
> Topic 1's original fix (a new, non-reflection
> `tryRespondErrorPatternGeneric[Req,Resp]` helper, wired at the same 13
> Category-A points), purely additive, with regression tests proving a
> declared `ErrorPattern` is now consulted and observed via
> `IngestAdapter` (representative of the shared `handlerFunc` surface).
>
> **H2 (same review round)**: `PublishAdapter.Activate`'s
> `handleUpstreamError` closure in `adapters/mqtt`/`adapters/mqtt5`/
> `adapters/zeromq`'s `binding.go` (events' publish-side sink adapter,
> handling errors from an UPSTREAM pipeline stage rather than a publish
> failure) hand-rolled its own `events.ErrorChannel` dispatch via the
> bare `handle.ErrorResponseFor`, instead of reusing each package's own
> already-correct `tryPublishErrorChannel` helper (used by the subscribe
> side and `publish()`'s own internal error paths) — silently skipping
> `stats.ErrorPatternObserver`/`SpanTagger` observability on this one
> path. Fixed by delegating to `tryPublishErrorChannel` in all 3
> packages; error-channel replies from this path now always use QoS
> 0/non-retained (mqtt/mqtt5 only — matching every other error-channel
> dispatch site; zeromq had no such option to begin with, so its fix was
> a pure zero-behavior-change dedup). `DeadLetter` fallback intentionally
> NOT added here (no raw payload exists for an upstream, pre-publish
> pipeline error — same scope boundary as G3). All other events/reqreply
> port adapters (`SubscribeAdapter`, `CallAdapter`, `ServeAdapter`,
> `LatestAdapter`) were re-audited and confirmed to delegate straight to
> already-fully-wired dispatch functions — no further gaps found there.
> [← Back to Roadmap](index.md)

## Motivation

Error handling is one of go-codex's most cross-cutting concerns — every
API boundary (`api/rest`, `api/events`, `api/reqreply`) has independently
grown its own declarative error-handling mechanism over several design
rounds (D-0001, D-0003's Addenda, D-0004's Addendum). REST has always been
the most complete reference relative to events/reqreply's own starting
point — a route/middleware handler error is `errors.As`-matched against
declared `ErrorPattern`s, realized via a 3-way `ErrorAction`
(`ErrorRespond`/`ErrorHandle`/`ErrorLog`), and round-trips all the way to
the client via `nethttp.CallWithHandle`/`DecodeErrorFor` — though this
document's own Topic 1 finding shows REST had substantial gaps of its own
before this round (see below).

This document consolidates 7 related but previously-separate threads into
one design surface, so REST's reference workflow is deliberately and
completely applied to `api/events`/`api/reqreply`, and so the remaining
gaps (AsyncAPI error-reply modeling, dead-letter handling) get one
coherent design instead of three divergent ones:

1. **Apply REST's declarative handler-error workflow to reqreply, and to
   EVERY failure point uniformly** — already substantially true (see
   "Phase 0" below) for route/channel handlers and general-purpose
   middleware alike; this document is where the FULL picture (middleware
   errors + handler errors + client decode) is assembled and any
   remaining asymmetries are closed. **Confirmed and fully enumerated
   this round**: `ErrorPattern`/`ErrorChannel` is eligible for ONLY 2 of
   ~10+ distinct failure points per API today (handler errors +
   general-purpose middleware Fn errors) — security middleware Fn errors,
   decode/param-validation failures, `MiddlewareInputError`/
   `MiddlewareOutputError`, and response/merge-field encode failures are
   ALL currently ineligible, across all 3 APIs (see Topic 1's full
   enumeration). Codex's own error taxonomy and ports/adapter
   infrastructure errors are PERMANENTLY excluded by contrast (also
   Topic 1) — not gaps, structural boundaries.
2. **Ratify how reqreply error replies reach the client** — same reply
   topic as the happy path, or a separate one? (Answered below — same
   topic, already shipped, now formally ratified as a permanent decision.)
3. **AsyncAPI's OpenAPI-response-code equivalent** — does AsyncAPI 3.0
   have a way to document multiple reply "shapes" (success + N error
   variants) on ONE operation, the way OpenAPI documents `200`/`409`/`422`
   on one endpoint? (Answered below — yes, and today's implementation
   does not yet use it.)
4. **A declarative dead-letter queue/topic** for `api/events` and
   `api/reqreply` — a genuinely new, currently-nonexistent mechanism,
   complementary to (not a replacement for) `ErrorChannel`/`ErrorPattern`.
5. **Deep observability for declared error patterns** — a matched
   `ErrorPattern`/`ErrorChannel` is a live catalogue of every KNOWN,
   NAMED failure mode an API declares; today NONE of that is visible to
   `stats.Observer` on a successful match, only on the rarer
   mapper/encode-failure sub-case. (Answered below — a new, optional
   `stats.ErrorPatternObserver` extension, mirroring `SecurityObserver`'s
   established pattern.)
6. **Client-side ergonomics for a matched pattern** — declaring on the
   server is one concern; using the decoded, typed payload conveniently
   on the CLIENT is a distinct one. Today's client workflow is a manual
   `errors.As` + type-switch dance. (Sketched below — 3 open alternatives,
   not yet decided.)
7. **Refine pub/sub specifically**: confirm `ErrorChannel`/dead-letter
   apply consistently on the SUBSCRIBER (receiver) side, clarify the
   PUBLISHER (sender) side correctly stays excluded (same role REST/
   reqreply's clients already play), and add a small subscriber-side
   convenience for the unmatched-fallback case.

## Design Goal (North Star)

**The `ErrorPattern`/`ErrorChannel` mechanism is designed to be a single,
extensible enforcement point that does TWO things at once, for EVERY
failure point uniformly (route/channel handler, general-purpose
middleware, security middleware, decode, encode — see Topic 1's full
enumeration):**

1. **Declare** the typed error response/reply a caller sees — one
   `errors.As`-matched mapping from domain error type to wire shape,
   decided ONCE per route/channel, independent of which dispatch step
   produced the error.
2. **Observe** every error that flows through that SAME enforcement
   point — matched or unmatched — via `stats.ErrorPatternObserver`
   (Topic 5), with zero additional code anywhere else.

**This serves two concrete, load-bearing ends:**

- **Reduces observability effort in handlers** (Topic 5's design goal) —
  a handler/middleware author who declares an `ErrorPattern`/
  `ErrorChannel` and simply returns the domain error gets full
  observability (hit counts, structured logs, trace tags) for free. They
  never call `obs.RecordSomething(...)` themselves, mirroring how
  `RecordRequest`/`RecordSecurityRejection` already work today.
- **Keeps adapters thin** (Topic 1's Category A "simplification, not new
  adapter complexity" finding) — routing EVERY failure point through the
  SAME generic `ErrorResponseFor`/
  `DecodeErrorFor` consultation, instead of each failure point growing
  its own special-cased fixed-shape branch, collapses today's ~10+
  bespoke `if err != nil { errFn(status, err); return }` sites per API
  into ONE repeated pattern, reused everywhere. No adapter needs new
  awareness of any specific error type (codex's own or otherwise) to
  gain this — `ErrorResponseFor` is already fully generic, built entirely
  inside `api/rest`/`api/events`/`api/reqreply` from whatever type the
  caller declared.

**This is a governing principle for the whole document, not just Topics 1
and 5**: any NEW failure point discovered in a future round (in any of
the 3 APIs, or a future 4th) should, by default, be wired through this
SAME enforcement point — declare-and-observe together, in one place —
rather than growing its own bespoke error-response/observability branch.
Categories B (codex) and C (ports/adapter infrastructure) remain the
explicit, permanent exceptions (see Topic 1) — everywhere else, this is
the expected default, not an opt-in.

## Phase 0 — Already shipped (folded in from `reqreply-error-pattern-client-decode.md`)

The following was fully designed and implemented in a prior round —
summarized here as the foundation this document builds on, rather than
re-litigated. Full historical detail remains in
[D-0004's Addendum](../design/d-0004-reqreply-workflow-simplification.md)
once this document graduates (see "Out of scope"/graduation note at the
end).

- `reqreply.ErrorPatternResponse` gained `Code string` — the SAME value
  used to derive the AsyncAPI reply-error channel's operation ID
  (`ErrorPatternOpt.WithCode`/sanitized-type-name default), now ALSO
  transmitted on the wire (mqtt5: `x-error-code` User Property; zeromq:
  an extra frame) for the matched-pattern case only.
- `RouteHandle.DecodeErrorFor(code, body)` — the client-side counterpart
  of `ErrorResponseFor`, mirrors `rest.RouteHandle.DecodeErrorFor` but
  matches by `Code` (reqreply has no wire status to match against).
- `DuplicateErrorPatternCodeError` — 2+ `ErrorPattern`s sharing one `Code`
  are REJECTED at `Register` time (a stricter design than REST's own
  accepted, test-locked same-status ambiguity — reqreply had no existing
  behavior to preserve, so this ambiguity was closed from day one).
- New `mqtt5.ErrorPatternResponse`/`zeromq.ErrorPatternResponse` client
  types, wrapped in each package's existing `CallError` convention.

**What Phase 0 did NOT address** (the remaining scope of this document):
AsyncAPI's per-error-type SEPARATE reply channel modeling (still one
channel per error code today), and dead-letter handling (not addressed at
all).

## Breaking Changes Policy

**This project currently has a single maintainer/user. Breaking changes
are explicitly ACCEPTABLE, anywhere in this document, whenever they
improve implementation clarity or long-term maintainability** — no
opt-in flags, transition periods, deprecation cycles, or major-version
gating are required by default. This is a deliberate policy decision,
stated here prominently so it governs every topic below rather than
being re-litigated per topic. Where earlier drafting rounds of this
document treated backward compatibility as a hard constraint (most
notably: REST's same-status ambiguity, previously left unchanged
specifically because it was "test-locked" — see Topic 1's new
subsection below), those decisions are NOW explicitly REOPENED under
this policy. This does not mean every topic MUST introduce a breaking
change — additive-by-default remains good engineering discipline
independent of this policy (simpler to reason about, smaller diffs) —
it means breaking changes are no longer avoided PURELY to preserve
compatibility for its own sake.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `api/rest`, `api/events`, `api/reqreply` — the 3 APIs with a mature declarative `ErrorPattern`/`ErrorChannel` mechanism already | `api/mcp`/`adapters/websocket` — both have their own distinct `ErrorPattern`/`ErrorFrame` mechanisms, already documented per-feature-page; not folded into this document |
| Ratifying (not changing) reqreply's same-reply-topic behavior | — |
| **REOPENED under the Breaking Changes Policy**: making REST's `DecodeErrorFor`/`Register` reject 2+ `ErrorPattern`s sharing one status, mirroring reqreply's `DuplicateErrorPatternCodeError` — see Topic 1's new subsection | Changing REST's OpenAPI rendering — OpenAPI already has native per-status responses; no equivalent gap exists there |
| Migrating AsyncAPI reply-error rendering to "multiple messages on one reply channel" (`render/asyncapi/v3` + `api/reqreply/builder.go`) — a breaking spec-shape change, now uncontroversial under the Breaking Changes Policy | |
| A NEW declarative dead-letter mechanism for `api/events`/`api/reqreply`, scoped to the UNMATCHED-failure case only (per user decision) | Extending dead-lettering to matched-but-failed-to-encode cases (deferred — those still silently fall back to the existing plain-text/log path, unchanged) |
| Extending `ErrorPattern`/`ErrorChannel` eligibility to EVERY Category-A failure point (Topic 1): security middleware Fn errors, decode/param-validation failures, `MiddlewareInputError`/`MiddlewareOutputError`, and response/merge-field encode failures — matching the ALREADY-eligible handler + general-purpose middleware Fn path | `docs/roadmap/protocol-native-features.md`'s "Handler Disposition" (ack/nack/requeue signaling) — a distinct, complementary, separately-tracked mechanism; cross-referenced, not merged |
| | Category B (codex's own error taxonomy) and Category C (ports/adapter infrastructure errors — wiring-time or no-correlated-recipient) — PERMANENT structural exclusions, not gaps; see Topic 1 |

## Topic 1 — Applying REST's workflow to reqreply (status check)

**Guiding principle (stress-tested and BROADENED this round): every
typed, structural error on the response-writing path is
`ErrorPattern`/`ErrorChannel`-eligible — not just a route/channel's own
HANDLER and a GENERAL-PURPOSE middleware's Fn, but SECURITY middleware
Fns, body/param DECODE failures, and response/middleware ENCODE failures
alike.** A caller declaring a typed error response should not have to
care WHICH dispatch step produced the error — the whole point of the
declarative model is that "this error type maps to this response" is
decided ONCE, independent of where in the pipeline the error originated.
This is a strict superset of the previous round's finding (security
middleware only) — tracing the full adapter dispatch code surfaced that
the SAME gap exists at nearly every OTHER fixed-shape error-response call
site too.

| Layer | REST | events | reqreply | Gap? |
|---|---|---|---|---|
| Middleware input error | `MiddlewareInputError{Name,Err}` | `MiddlewareInputError{Name,Err}` | `MiddlewareInputError{Name,Err}` | None — identical shape, all 3 |
| Middleware business error | `MiddlewareError{Name,Err}` | `MiddlewareError{Name,Err}` | `MiddlewareError{Name,Err}` | None |
| Middleware output error | `MiddlewareOutputError{Name,Err}` | `MiddlewareOutputError{Name,Err}` | `MiddlewareOutputError{Name,Err}` | None (shipped 2 rounds ago) |
| Handler error declaration | `ErrorPattern` (status + codec) | `ErrorChannel` (topic + codec) | `ErrorPattern` (code + codec) | None — same `errors.As`/first-wins/3-way-action model |
| **General-purpose middleware Fn error → `ErrorPattern`-eligible?** | **Yes** — `runMiddlewareHandlersReflect`'s fn-error branch consults `ErrorResponseFor` before falling back to `MiddlewareError` (documented as "D2") | Confirmed same via `dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers` | Confirmed same via `dispatchServerMiddlewareHandlers` | None — already uniform |
| **Security middleware Fn error → `ErrorPattern`-eligible?** | **No** — bypasses `ErrorResponseFor` entirely | **No** — same | **No** — same | **Confirmed gap — see enumeration below** |
| **Body/param decode + validation failure → `ErrorPattern`-eligible?** | **No** — bypasses `ErrorResponseFor` entirely | **No** — same | **No** — same | **Confirmed gap — see enumeration below** |
| **Middleware `DecodeIn`/`EncodeOut` failure → `ErrorPattern`-eligible?** | **No** — already wrapped in `MiddlewareInputError`/`MiddlewareOutputError`, but neither is ever passed to `ErrorResponseFor` | **No** — same | **No** — same | **Confirmed gap — see enumeration below** |
| **Response body/merge-field encode failure → `ErrorPattern`-eligible?** | **No** — bypasses `ErrorResponseFor` entirely | **No** — same | **No** — same | **Confirmed gap — see enumeration below** |
| Handler error → client decode | `DecodeErrorFor` (status) → `ErrorPatternResponse` | N/A (no synchronous caller) | `DecodeErrorFor` (code) → `ErrorPatternResponse` | None (Phase 0) |
| AsyncAPI/OpenAPI modeling | Native per-status `responses` | One dedicated topic per `ErrorChannel` (already appropriate — no "reply" concept in pub/sub) | Separate reply channel per error code | **Gap — Topic 3** |
| Dead-letter / catch-all | N/A (HTTP always has SOME response) | None | None | **Gap — Topic 4** |

### Full failure-point enumeration — 3 categories

Every error that CAN occur while producing a response, across all 3 APIs,
falls into exactly one of 3 categories. Category A is this document's
newly-broadened scope; B and C are PERMANENT, structural exclusions, not
gaps.

#### Category A — in scope: every failure point on an already-dispatched, correlated request/message

These all occur AFTER a request/message has been matched to a specific
route/channel and a live response/reply correlation exists (an HTTP
`ResponseWriter`, an MQTT reply topic, a pub/sub error channel) — exactly
the precondition `ErrorPattern`/`ErrorChannel` needs to apply.

**REST** (`adapters/nethttp/serve.go`; `adapters/chi/serve.go` mirrors it
exactly — SSE is EXCLUDED: `rest.SSERoute.Register`/`RegisterHandle`
already reject `ErrorPattern`/`ErrorStatus` RouteOpts outright, via the
already-shipped `rest.SSEErrorPatternUnsupportedError` — SSE has no
"response" concept to shape a typed error into, so there is nothing for
this document's Category A enumeration to extend there):

| # | Failure point | Current fixed shape | Currently `ErrorPattern`-eligible? |
|---|---|---|---|
| 1 | Request body decode (`Unmarshal`) | raw `error`, 400 | No |
| 2 | Path param validation | `rest.PathParamError`, 400 | No |
| 3 | Query param validation | `rest.QueryParamError`, 400 | No |
| 4 | Cookie param validation | `rest.CookieParamError`, 400 | No |
| 5 | Header param validation | `rest.HeaderParamError`, 400 | No |
| 6 | Middleware `DecodeIn` failure | `rest.MiddlewareInputError`, 400 | No |
| 7 | Middleware Fn business error | `rest.MiddlewareError`, 400 | **Yes** (already shipped) |
| 8 | Handler business error | (caller's own error type), 500 default | **Yes** (already shipped) |
| 9 | Response header/cookie merge-field encode | raw `error`, 500 | No |
| 10 | Middleware `EncodeOut` failure | `rest.MiddlewareOutputError`, 500 | No |
| 11 | Response body encode (`Marshal`) | raw `error`, 500 | No |
| — | Security middleware Fn error (carried over from the prior round's finding) | `rest.SecurityError`, 401 | No |

Rows 1–6, 9–11, and the security row are ALL newly in scope for this
document — 10 of 12 distinct failure points. Only rows 7–8 (already
shipped) are currently eligible.

**events** (`adapters/mqtt5`/`zeromq`/`mqtt`'s `adapter.go`/`binding.go`,
subscribe side — publish side is structurally simpler, no decode step):

| # | Failure point | Current fixed shape | Currently eligible? |
|---|---|---|---|
| 1 | Payload decode | `SubscribeError{Kind: KindDecode}`, via `OnError` | No |
| 2 | Topic-var merge failure | Same | No |
| 3 | Property-var merge failure (mqtt5 only) | Same | No |
| 4 | Middleware `DecodeIn` failure | `events.MiddlewareInputError` via `OnError` | No |
| 5 | Middleware Fn business error (SUBSCRIBE side) | `events.MiddlewareError` via `OnError` | **Yes** (already shipped) |
| 6 | Handler business error (SUBSCRIBE side) | (caller's own error type) via `OnError` | **Yes** (already shipped) |
| — | Security middleware Fn error (SUBSCRIBE side, carried over) | `events.SecurityError` via `OnError` | No — **in scope, see below** |
| — | Middleware Fn/`EncodeOut` failure (PUBLISH side) | `events.MiddlewareError`/`MiddlewareOutputError`, returned directly | No — **correctly excluded, see Topic 7** (publisher plays the "client/sender" role, same as REST/reqreply's clients) |

**reqreply** (`adapters/mqtt5`/`zeromq`'s `reqreply_transport.go`,
server-side dispatch — request-decode side is structurally similar to
events' subscribe side, reply-encode side mirrors REST's response side):

| # | Failure point | Current fixed shape | Currently eligible? |
|---|---|---|---|
| 1 | Request payload decode | plain-text error reply | No |
| 2 | Topic-var/property-var merge failure | Same | No |
| 3 | Middleware `DecodeIn` failure | `reqreply.MiddlewareInputError`, plain-text reply | No |
| 4 | Middleware Fn business error | `reqreply.MiddlewareError` | **Yes** (already shipped) |
| 5 | Handler business error | (caller's own error type) | **Yes** (already shipped) |
| 6 | Middleware `EncodeOut` failure (building the reply) | `reqreply.MiddlewareOutputError`, plain-text reply | No |
| — | Security middleware Fn error (carried over) | `reqreply.SecurityError`, plain-text reply | No |

**Proposed fix** (to be fully speced in a follow-up round — same
mechanism for every row above, not a new one per row): before falling
back to each row's existing fixed shape, the adapter should ALSO consult
`ErrorResponseFor`/`DecodeErrorFor` first — mirroring the EXACT fallback
structure ALREADY used by rows 7–8/5–6 (REST/events/reqreply's
already-shipped handler+middleware-Fn rows): `ErrorResponseFor` first; on
no match, or a mapping failure, fall back to that row's existing fixed
shape UNCHANGED. This is purely additive — routes/channels declaring no
matching `ErrorPattern`/`ErrorChannel` for a given row's error type see
ZERO behavior change.

**Security consideration — Direct vs. Mapped mode for security-related
patterns (guidance, not a design flaw)**: today's fixed `SecurityError`
shape already exposes `err.Error()` — a FLATTENED STRING — via the
default JSON envelope (confirmed:
`errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})`).
Once security middleware failures become `ErrorPattern`-eligible, a
caller declaring the pattern in **Direct mode** (no `mapFn`, `E` and `B`
the SAME type) has the underlying error's ENTIRE STRUCTURED VALUE
serialized via its own codec — not just the flattened string today's
fallback produces. For ORDINARY business errors this is the whole point
(richer, structured data reaching the caller) — but for
SECURITY-RELATED errors specifically, this can be a real footgun:
security error messages are OFTEN deliberately vague on purpose (e.g.
never distinguishing "user not found" from "wrong password," to prevent
account-enumeration attacks), and an internal error type may carry
EXTRA fields (a wrapped credential-store error, internal validation
context, etc.) never intended for external disclosure. **Recommendation
for the eventual implementation/docs**: when declaring an `ErrorPattern`
for a security-related error type, prefer **Mapped mode** with an
explicit `mapFn` that DELIBERATELY constructs a minimal, safe payload
(e.g. `{Code: "unauthorized"}`) — never Direct mode reusing an internal
security error type wholesale. This is guidance to document prominently
(a callout in the eventual guide, not a new mechanism or constraint) —
`ErrorPattern` itself should not gain any new restriction; the risk is
entirely in HOW a caller chooses to declare the pattern, identical in
kind to any other codec-declared struct's information-disclosure
surface, just worth flagging explicitly for THIS specific row given how
routinely security errors are deliberately under-specified elsewhere in
API design.

**Why this is a SIMPLIFICATION, not new adapter complexity**: the adapter
requires ZERO new awareness of any SPECIFIC error type (codex's own or
otherwise) to implement this — `ErrorResponseFor` is already a fully
generic, caller-parameterized closure (built via `errors.As` against
whatever type `E` the CALLER declared at `Register` time, entirely inside
`api/rest`/`api/events`/`api/reqreply`). The adapter's job at every one of
the ~10 rows above is IDENTICAL regardless of which row it is: call the
SAME `ErrorResponseFor(err)` accessor it already calls for rows 7–8,
passing whatever error just occurred. Today's ~10 bespoke
`if err != nil { errFn(status, err); return }` branches — each currently
hard-coding its own status/shape — collapse into ONE repeated pattern
reused at every failure point, rather than N special-cased ones. This
makes the adapter THINNER, not thicker.

#### Category B — permanently excluded: codex's own error taxonomy

`codex.ValidationErrors`/`TypeMismatchError`/`ConstraintError`/etc. must
NEVER themselves gain awareness of `ErrorPattern` — `codex` is imported
BY `api/rest`/`api/events`/`api/reqreply`, never the reverse; giving codex
knowledge of `ErrorPattern` would invert the dependency graph. This
boundary is permanent, not a gap to close.

Critically — per the "thin adapter" point above — **this does NOT mean
the adapter needs any codex-specific awareness either.** A caller is
already free TODAY to declare
`rest.ErrorPattern[codex.ValidationErrors, MyValidationBody](422, ...)` —
`ErrorPatternOpt.applyRoute`'s generic `E error` type parameter accepts
ANY error type, including a codex one, with zero special-casing anywhere.
The only reason this doesn't already work end-to-end is Category A's
enumeration above: `Unmarshal`'s decode error (which for a decode
failure WOULD be `codex.ValidationErrors`) is never passed to
`ErrorResponseFor` at all today. Once Category A's fix lands, a caller
CAN match on `codex.ValidationErrors` (or any other codex error type)
exactly like any other declared pattern — no new mechanism, no new codex
awareness anywhere, just Category A's existing generic consultation
reaching one more failure point.

#### Category C — permanently excluded: ports/adapter infrastructure errors

`ports.PortBindError`/`PortNoAdapterError`/`PortNoPipelineError` (returned
by `Bind`/`Connect`, called ONCE at wiring/startup time — before any
request/message has ever been dispatched, with no route/topic context to
look up declared patterns against) and client-side transport errors
(`nethttp.RequestError`/`RequestBuildError`/`UnexpectedStatusError`,
`mqtt5.CallError`'s `KindTimeout`/network-layer cases — the CLIENT itself
IS the caller; there is no second, correlated party to route a typed
response TO) are structurally excluded from `ErrorPattern`. Its entire
premise — "map a business error to a wire response FOR AN ACTIVE,
CORRELATED CALLER" — does not apply to either: infrastructure failures
either predate any correlation existing, or have no correlated recipient
at all. This is not a gap; there is no coherent way to apply the
mechanism here.

**Conclusion**: Category A (every structural failure point on an
already-dispatched, correlated request/message) is a genuine, confirmed,
much larger gap than the prior round's security-middleware-only finding
— now fully enumerated per API. Categories B and C are permanent,
correctly-drawn boundaries, not gaps — codex remains fully layer-agnostic
forever, and infrastructure errors correctly keep their own separate
observability, with no coherent "correlated response" to route them
through in the first place.

### REOPENED under the Breaking Changes Policy: REST's same-status ambiguity

A prior round of this document documented REST's same-status
`ErrorPattern` ambiguity as a permanent, accepted caveat: 2+ patterns
sharing one HTTP status make `DecodeErrorFor` deterministically pick the
FIRST-declared one client-side, regardless of which the server actually
sent — intentional, tested (`TestDecodeErrorFor_FirstMatchWins_SameStatus`),
and left unchanged specifically because "REST's behavior is test-locked"
— i.e. purely a backward-compatibility concern, not a judgment that the
ambiguity itself was desirable. **Under the Breaking Changes Policy
above, that reasoning no longer applies** — this is now a genuine
improvement opportunity, not a permanent exclusion.

**Proposed fix**: mirror reqreply's ALREADY-SHIPPED
`DuplicateErrorPatternCodeError` exactly — reject 2+ `ErrorPattern`s
declared on the SAME route sharing one status at `Register`/
`RegisterHandle` time, via a new `rest.DuplicateErrorStatusError{Route,
Status, FirstType, SecondType}` (identical shape to reqreply's own error
type, adapted from `Code string` to `Status int`). This makes REST as
strict as reqreply from day one, rather than the two APIs permanently
diverging on this specific behavior for historical reasons alone.

**Consequences, now acceptable under the policy**:
- `TestDecodeErrorFor_FirstMatchWins_SameStatus` (the existing test
  documenting today's accepted behavior) gets its expected outcome
  CHANGED — from "first-declared wins silently" to "rejected at
  `Register` time with `DuplicateErrorStatusError`." This is the test
  actively being updated, not merely a caveat being documented around.
- Any EXISTING route (in this repo's own examples/tests) that
  currently declares 2+ `ErrorPattern`s sharing one status would need
  its test/example fixed to use distinct statuses — a quick grep-and-fix
  pass, not a design risk.
- `docs/features/rest-api.md`'s "same-status precedence" callout (added
  in an earlier round) becomes STALE once this ships — needs updating to
  describe the new rejection instead of the old accepted-ambiguity
  behavior.
- Symmetric bonus: REST's error-handling guide and reqreply's now read
  IDENTICALLY on this point — one fewer cross-API inconsistency for a
  reader to track, reinforcing this document's own Design Goal of
  uniform behavior across all 3 APIs.

## Topic 2 — Same reply topic (ratified, not changed)

**Decision: reqreply error replies stay on the SAME reply
topic/socket as the success reply — this document formally ratifies
today's already-shipped behavior as the permanent design, not a
transitional state.**

Confirmed via the actual runtime code (not just intent): `mqtt5`'s
`publishErrorReply`/`publishHandlerErrorReplyReflect` and `zeromq`'s
`sendErrorReply`/`sendHandlerErrorReplyReflect`/
`sendRouterHandlerErrorReplyReflect` ALL publish/send to the exact SAME
`responseTopic`/socket the success reply would use — distinguished only
by an envelope marker (mqtt5's `ContentType: "application/mqtt5-error"`;
zeromq's `"error"` status frame), never a separate topic/channel.

This matches how synchronous RPC-over-messaging is done in practice
industry-wide:

- **gRPC** — the status/error code is part of the SAME response
  envelope/trailers; there is no separate error stream.
- **AMQP RPC pattern** (`reply-to` + `correlation-id`) — the reply is
  published to the SAME reply-to queue; success/failure is distinguished
  by a header or the body's own envelope shape.
- **NATS request-reply** — the reply arrives on the SAME inbox subject
  (sometimes with a "no responders"/error status header); never a
  separate subject.

A dedicated error TOPIC only makes sense for fire-and-forget pub/sub,
where there is no synchronous caller waiting on a specific reply address
at all — which is exactly why `events.ErrorChannel`'s separate topic is
independently correct for events (there is no shared "reply address" to
reuse), while reqreply's synchronous, correlated request-reply shape has
a natural "same address" answer that pub/sub structurally lacks. These
are not inconsistent with each other; they are the correct answer for
each transport's actual shape.

**No code change for this topic** — it is a ratification, documented here
so a future contributor does not "fix" this into a separate-topic design
by analogy with `events.ErrorChannel` without understanding why the two
differ.

## Topic 3 — AsyncAPI reply-error modeling: migrate to multi-message channels

### Current state

`api/reqreply/builder.go`'s per-route registration loop generates a
SEPARATE `ChannelItem` (with its own channel key, address, and
`Subscribe` operation) for every declared `ErrorPattern`/`ErrorReplyMeta`
— e.g. a route with 2 declared error patterns produces 3 total reply
channels: the success reply channel (`computeAddReply` at
`compute/add/reply`) plus 2 error reply channels
(`computeAddReplyErrorConflict` at `compute/add/reply/error/conflict`,
etc.), each with its OWN operation and OWN single `Message`.

### AsyncAPI 3.0's native mechanism (confirmed via spec research)

AsyncAPI 3.0 supports a channel-level `messages` object (a map of named,
reusable message definitions) and an operation-level `messages` array
(references into that map) — this is the direct analogue of OpenAPI's
per-status `responses` object: ONE channel, ONE operation, MULTIPLE named
message variants. Per spec, when an operation's `messages` field is
omitted, ALL of the channel's declared messages apply to that operation —
so for reqreply's reply channel (where literally any declared shape,
success or error, might arrive), we do not even need to populate the
operation-level `messages` array explicitly; we only need the CHANNEL's
`messages` map to carry every variant.

**Confirmed via code** that `render/asyncapi/v3/document.go`'s
`buildChannelsAndOperations` already assembles a `messages` map per
channel today — but only ever from exactly ONE message (whichever the
channel's `Subscribe`/`Publish` operation declares). This needs to become
a LIST.

### Proposed API surface

```go
// render/asyncapi/v3/document.go

// Operation gains a plural Messages field, alongside the existing
// singular Message (kept for the common single-shape case — zero
// migration needed for every OTHER channel in this codebase that
// declares exactly one message).
type Operation struct {
	// ... existing fields unchanged ...

	// Message is the operation's sole message, when there is exactly one
	// (the common case — unaffected by this change).
	Message Message

	// Messages, when non-empty, lists MULTIPLE named message variants for
	// this operation (e.g. a reply channel's success shape plus N
	// declared ErrorPattern shapes) — takes priority over Message when
	// both are set. Each Message.Name (or SchemaName, falling back to a
	// generated key) becomes the channel's messages map key.
	Messages []Message
}
```

```go
// api/reqreply/builder.go — the per-route registration loop changes from
// "one reply channel per error pattern" to "one reply channel, N messages".
func (b *Server) registerRoute(...) {
	replyMessages := []asyncapi.Message{
		{Name: "Success", Schema: respSchema, SchemaName: meta.RespSchemaName, Headers: respHeaders},
	}
	for _, er := range errorReplies {
		name := "Error"
		if er.Code != "" {
			name += capitalise(topicToID(er.Code))
		}
		replyMessages = append(replyMessages, asyncapi.Message{
			Name: name, Schema: er.Schema, SchemaName: er.SchemaName,
		})
	}
	b.docBuilder.AddReplyChannel(replyChannelKey, asyncapi.ChannelItem{
		Address:    topic + "/reply",
		Parameters: params,
		Subscribe: &asyncapi.Operation{
			OperationID: recvOpID,
			Messages:    replyMessages, // was: Message: asyncapi.Message{...}
		},
	})
	// The per-error-type errReplyChannelKey/errReplyAddress loop is REMOVED
	// entirely — no more separate channels for error variants.
}
```

### Consequences

- **Breaking spec-shape change**: any existing consumer of a
  `reqreply`-produced AsyncAPI document that expects separate
  `<route>ReplyError<Code>` channel keys will see them disappear, folded
  into the single reply channel's `messages` map instead. This is a
  behavior change to the GENERATED SPEC, not to any Go API signature —
  existing `reqreply.ErrorPattern`/`ErrorReplyMeta` declarations need NO
  changes; only the rendered AsyncAPI document's shape changes.
- **Simpler, more idiomatic spec output**: N error types no longer cost N
  extra channels + N extra operations — they become N extra entries in
  ONE existing channel's `messages` map.
- **`events.ErrorChannel` is unaffected** — it correctly uses a genuinely
  separate topic (Topic 2's reasoning), which has nothing to do with this
  multi-message mechanism; `Operation.Messages` is available to `events`
  too (shared renderer), but there is no current use case for it there
  (an events channel's subscribe operation has exactly one message shape:
  the domain event itself — there is no "reply" to enumerate variants of).

### `messages` map key derivation — DECIDED this round

`buildChannelsAndOperations` derives the STRING KEY for each entry in a
channel's `messages` map from its `Message.Name`, falling back to
`SchemaName` when `Name` is empty. On an ultimate collision (two
messages in the same `Operation.Messages` slice landing on the same
derived key), rendering returns a new render-time error rather than
silently overwriting one entry with the other — a collision at this
point indicates a caller bug, since Topic 1's reopened
`DuplicateErrorStatusError` (REST) and reqreply's existing
`DuplicateErrorPatternCodeError` already prevent the realistic
naturally-occurring cases upstream, before this renderer code ever runs.

## Topic 4 — Declarative dead-letter queue/topic (events + reqreply)

### Motivation and scope (per user decision: unmatched-only)

Confirmed via repo-wide search: no declarative dead-letter mechanism
exists anywhere in go-codex today. The closest adjacent things are (a)
`stream.MapErr`/`LogOnError` — generic, IMPERATIVE, pipeline-level
dead-lettering, not a route/channel DECLARATION, and (b)
`docs/roadmap/protocol-native-features.md`'s "Handler Disposition" — a
DISTINCT, NOT YET IMPLEMENTED idea for a handler to signal ack/nack/
requeue outcomes (a per-message disposition signal, not a destination
policy for given-up-on messages — complementary, tracked separately, not
merged into this document).

`events.ErrorChannel`/`reqreply.ErrorPattern` are TYPE-MATCHED — they only
fire for a SPECIFIC declared business error type via `errors.As`. Per the
scope decision, this document's dead-letter mechanism covers ONLY the
genuinely UNMATCHED case: a decode failure before any business error
exists, or a business error whose type matches no declared
`ErrorChannel`/`ErrorPattern`. (Matched-but-failed-to-encode cases are
explicitly OUT of scope for this round — they keep today's existing
plain-text/log fallback behavior, unchanged.)

### How dead-letter queues actually work in practice (broker-native vs. application-level)

A question worth answering explicitly before proposing an API: is a
"dead-letter queue" a BROKER feature, or something the application
builds? The honest answer is **both — depending on the messaging
system** — and go-codex's current transports (mqtt, mqtt5, zeromq) fall
on the side that requires application-level work, which directly shapes
this design.

| Mechanism | Who moves the failed message | Configured how | Examples |
|---|---|---|---|
| **Broker-native** | The BROKER, automatically | Infrastructure/queue config, NOT application code — e.g. a queue argument set once at declare/bind time | RabbitMQ/AMQP's `x-dead-letter-exchange` queue argument; AWS SQS's redrive policy |
| **Application-level** | The APPLICATION, explicitly, at runtime | The consumer's own code publishes the failed message to a designated topic — which is just an ORDINARY topic, nothing special about it on the wire | Kafka (no native DLQ concept — a "dead letter topic" is purely a naming convention your code implements); **MQTT (v3/v5) — no native dead-letter concept in the protocol at all**; **ZeroMQ — no broker whatsoever (brokerless by design), so no broker feature could exist even in principle** |

Since go-codex's shipped pub/sub adapters are exclusively MQTT-family and
ZeroMQ — both squarely in the "application-level" column, with **no
broker feature to delegate to at all** — the design below (declare a
destination topic the SAME way a channel is declared; the ADAPTER's own
dispatch code explicitly publishes to it on an unmatched failure) is not
a simplification or a compromise relative to "real" DLQ semantics — it
**is** the correct, idiomatic implementation for these specific
transports, matching exactly how Kafka-based systems already do this.

**This distinction becomes directly relevant to a currently-separate
roadmap doc**: `docs/roadmap/amqp-adapter.md`'s already-sketched
`QueueConfig.Args` field explicitly anticipates
`x-dead-letter-exchange`/`x-message-ttl` as broker-native queue
arguments — meaning a FUTURE AMQP adapter could realize the exact SAME
declared `events.DeadLetter(topic, ...)`/`reqreply.DeadLetter(topic, ...)`
API (this document's proposal, below) via broker-native queue
configuration instead of runtime application code — zero per-message
publish cost, the broker does the work. See [`docs/roadmap/protocol-native-features.md`](protocol-native-features.md),
§6 "Concrete feature survey," new "AMQP dead-lettering" entry, for the
full analysis of why this is a genuinely NEW category for that document's
own two-part `Capability` test — a capability whose DECLARATION can be
shared uniformly across every adapter (unlike QoS/User Properties, which
get NO shared declaration at all) even though its REALIZATION diverges
completely by whether the underlying broker has native support.

### Proposed API surface

```go
// api/events — new ChannelOpt, declared alongside ErrorChannel on the
// SAME NewChannel call.
//
// DeadLetter declares a fallback destination topic for a message that
// could not be processed for ANY reason NOT already covered by a more
// specific declared ErrorChannel — a decode failure, or a business error
// whose type matches no declared ErrorChannel. Fires strictly AFTER every
// declared ErrorChannel has been tried and found no match (or was not
// declared at all) — never instead of a more specific match.
//
// Unlike ErrorChannel's codec-backed TYPED payload, DeadLetter's payload
// is a FIXED, generic envelope — there is no reliable business type to
// encode once processing has failed unmatched:
//
//	events.NewChannel[Reading]("sensors/{id}/data", readingCodec,
//	    events.ErrorChannel[domain.ValidationError, ErrorPayload](...), // specific, tried first
//	    events.DeadLetter("sensors/dead-letter"),                        // catch-all fallback
//	)
func DeadLetter(topic string, opts ...DeadLetterOpt) ChannelOpt

// DeadLetterEnvelope is the FIXED payload schema DeadLetter publishes —
// the same shape regardless of which channel/route dead-lettered it.
type DeadLetterEnvelope struct {
	// SourceTopic is the original channel's topic (or reqreply route's topic).
	SourceTopic string
	// Payload is the original, UNDECODED raw bytes — decode may itself be
	// what failed, so this is never re-encoded from a typed value.
	Payload []byte
	// Error is the failure's error string (Error(), not a typed value —
	// the whole point of DLQ is that no reliable type exists here).
	Error string
	// Timestamp is when the dead-letter was produced.
	Timestamp time.Time
}
```

```go
// api/reqreply — same shape, RouteOpt instead of ChannelOpt. Complementary
// to (not a replacement for) the synchronous error reply (Topic 2) — the
// caller STILL gets an error reply on the same reply topic regardless;
// DeadLetter ADDITIONALLY preserves a durable record for ops/replay. Both
// fire together on an unmatched failure, not as alternatives.
func DeadLetter(topic string, opts ...DeadLetterOpt) RouteOpt
```

### Design decisions — DECIDED this round

- **`DeadLetterOpt` surface**: mirrors `reqreply.ErrorPatternOpt`'s
  fluent pattern exactly — `WithCode`/`WithDescription`/`WithSchemaName`/
  `WithChannelAddress`/`WithOperationID`. `DeadLetter` DOES generate its
  own AsyncAPI channel entry, using the FIXED `DeadLetterEnvelope`
  schema for every declaration (a single reusable message shape, unlike
  `ErrorPattern`'s per-declaration `B` type).
- **Global default: YES.** `DeadLetter` supports a global default
  declared once at the `Client`/`Server`/`Builder` level, mirroring
  `GlobalSecurity`/`Security`'s established nil-inherit/empty-override
  precedent exactly: a channel/route with no explicit `DeadLetter(...)`
  opt inherits the builder-level default; an explicit
  `DeadLetter(topic, ...)` on the route/channel itself overrides it; an
  explicit opt-out (mirroring `Security: []route.SecurityRequirement{}`'s
  empty-slice convention) lets a route/channel disable dead-lettering
  even when a global default exists.
- **Which failures reach DeadLetter — confirmed via tracing
  `adapters/mqtt5`/`adapters/mqtt`'s actual subscribe dispatch code, in
  TWO TIERS, mirroring Topic 1's Category A enumeration exactly**:
  - **Tier 1 — no business error exists yet (decode-class), ALWAYS
    dead-lettered when declared, unconditionally**: payload decode
    failure, topic-var extraction/mismatch failure, topic-var merge
    failure, property-var merge failure, middleware `DecodeIn` failure.
    There is nothing for these to "not match" against — no
    `ErrorPattern`/`ErrorChannel` could ever apply before a business
    error exists — so DeadLetter is the ONLY declarative fallback tier
    available for this class, and fires every time one is declared.
  - **Tier 2 — a business error already exists (Fn/handler-class), only
    dead-lettered AFTER `ErrorResponseFor` finds no match**: middleware
    Fn error, route/channel handler error, security middleware Fn error
    (once Topic 1's fix makes this last one `ErrorPattern`-eligible).
    This preserves the fallback-tier ordering Topic 1/4 already
    establish: a more specific declared `ErrorPattern`/`ErrorChannel`
    always wins first; DeadLetter is strictly the LAST resort. **"No
    match" means a genuine TYPE non-match only** (session-review
    round-3 clarification, G4) — a declared `ErrorChannel` that DOES
    type-match via `errors.As` but resolves to a non-`ErrorRespond`
    action (`ErrorHandle`/`ErrorLog`) is STILL a match for this
    purpose, and must NOT also reach `DeadLetter`; only a pattern whose
    `errors.As` check itself fails (or a matched-but-failed-to-map/
    encode pattern, per the existing out-of-scope carve-out below) falls
    through to Tier 2's DeadLetter consultation.
  - **"Handler panic" dropped from scope entirely** — confirmed via
    `grep -rn "recover()"` that only `adapters/nethttp`/`adapters/chi`
    (REST) have any panic-recovery mechanism at all; `mqtt`/`mqtt5`/
    `zeromq`'s events and reqreply dispatch code has none, and adding
    one is out of scope for this document (REST is out of DeadLetter's
    scope entirely, per this document's own Motivation).
- **Observer integration**: `stats.ReportErrors(obs, "dead_letter", err)`
  — a new location string, following the established
  `"middleware:in"`/`"middleware:out"`/`"error_pattern"`/`"error_channel"`
  convention, fired whenever a message is actually dead-lettered.
- **`mqtt` (v3): YES, included from day one.** Confirmed via code
  (`adapters/mqtt/adapter.go`'s subscribe dispatch) that mqtt v3 has an
  IDENTICAL decode → topic-var → security → middleware → handler
  dispatch shape to mqtt5/zeromq (it simply lacks a User-Property/
  property-var stage) — the dead-letter envelope itself needs no
  protocol-specific metadata (it's a plain published message), so there
  is no blocker.
- **Failed publish (never reached the broker): YES, dead-letterable,
  reusing the SAME `DeadLetterEnvelope`/topic mechanism** — NOT a
  separate "retry queue" concept. A publish-side middleware Fn/encode
  failure, or a broker-level rejection, ADDITIONALLY gets published to
  the SAME declared dead-letter destination a subscribe-side failure
  would use, alongside (not instead of) the synchronous Go error the
  caller already receives from `Publish(...)` (Topic 7) — giving an
  operator a durable, replayable record of outbound failures without
  requiring the caller to build their own retry-queue plumbing. This is
  DELIBERATELY narrower than the subscribe side's full Category-A
  coverage — scoped specifically to middleware Fn/encode failures and
  broker-level rejection (a message that entered dispatch and then
  failed), NOT to `BuildTopic`/client-side security-Fn failures (a
  pre-transmission validation/authorization rejection of the caller's
  OWN outgoing message, structurally closer to REST's client-side
  param/credential validation — Category C, permanently excluded — than
  to a genuinely dispatched-and-then-failed message).
- **reqreply's server-side reply transmission gets the SAME failed-send
  coverage as events' publish side** (session-review round-3 addition):
  a broker/socket-level rejection of the FINAL, successfully-encoded
  reply (after the handler ran and the response encoded cleanly) is ALSO
  dead-lettered — mirrors the publish-side bullet above exactly,
  reusing `RouteHandle.DeadLetterFor`/`tryDeadLetterReflect` on the SAME
  request-side topic/payload every other Category-A failure point in
  reqreply's server dispatch already uses.

### Core-layer consolidation — DECIDED this round: `DeadLetterFor`, adapters only publish bytes

Applying the SAME "thin adapter" lens Topic 5 applies to
`ObserveErrorResponseFor`: the DECISION logic (is a `DeadLetter`
declared — globally or per-channel/route? build the envelope; report the
observer signal) is core-layer logic with NO transport-specific
dependency at all — only the actual act of PUBLISHING the built envelope
onto the wire is inherently adapter-specific (it needs the adapter's own
`client`/`socket`). Centralizing the former and leaving only the latter
to each adapter mirrors this document's own established discipline
(`ErrorResponseFor`/`ObserveErrorResponseFor` already work this way).

**Correction this round**: an earlier draft of this sketch had
`DeadLetterFor` pull the original raw payload out of `ctx` via an
invented `rawPayloadFromContext(ctx)` helper — this is NOT achievable.
Confirmed via code that each adapter (`mqtt5`, `mqtt`, `zeromq`) stores
its raw message using its OWN **private, unexported** `contextKey{}`
type (`type contextKey struct{}`, a different type per package) — a
CORE-layer function in `api/events`/`api/reqreply` has no access to any
adapter's private context key, and the raw message's Go TYPE differs per
adapter anyway (`*pahomqtt5.Publish` vs. `pahomqtt.Message` vs. raw
frames). The raw payload must instead be an EXPLICIT parameter, supplied
by the calling adapter (which already has it at hand at the point of a
decode-class failure — this is a small, honest cost, not a "zero adapter
involvement" claim):

```go
// api/events (ChannelHandle) — api/reqreply's RouteHandle mirrors this
// exactly (Topic string field, RouteOpt instead of ChannelOpt).
//
// DeadLetterFor builds the dead-letter envelope for a failure at
// sourceTopic (the channel's OWN topic, or reqreply's request topic),
// reports it to obs (stats.ReportErrors(obs, "dead_letter", err)), and
// returns the destination topic + already-ENCODED envelope bytes ready
// to publish — or ok=false when no DeadLetter is declared (globally or
// on this channel) for this handle, in which case the adapter does
// nothing further. rawPayload is the original, undecoded message bytes
// (the adapter's own msg.Payload/msg.Payload()/frame — passed explicitly
// since a core-layer function cannot retrieve it from ctx: each adapter
// stores its raw message under its OWN private context key type). Does
// NOT itself consult ErrorResponseFor/ErrorChannelFor — callers are
// responsible for calling this ONLY after a more specific declared
// pattern has already been tried and missed (Tier 2), or immediately for
// decode-class failures where no such pattern could ever apply (Tier 1)
// — see the two-tier rule above.
func (h *ChannelHandle[T]) DeadLetterFor(
	ctx context.Context, obs stats.Observer, sourceTopic string, rawPayload []byte, err error,
) (topic string, body []byte, ok bool) {
	dl, declared := h.deadLetter() // resolves per-channel override, else the Builder-level global default
	if !declared {
		return "", nil, false
	}
	envelope := DeadLetterEnvelope{
		SourceTopic: sourceTopic,
		Payload:     rawPayload,
		Error:       err.Error(),
		Timestamp:   time.Now().UTC(),
	}
	encoded, encErr := dl.codec.Marshal(envelope)
	if encErr != nil {
		stats.ReportErrors(obs, "dead_letter", encErr)
		return "", nil, false
	}
	stats.ReportErrors(obs, "dead_letter", err)
	return dl.topic, encoded, true
}
```

**Adapter call-site impact**: an adapter's dead-letter handling collapses
to `if topic, body, ok := handle.DeadLetterFor(ctx, obs, handle.Topic, msg.Payload, err); ok { client.Publish(ctx, topic, body) }`
— zero envelope-construction, encoding, or observer-reporting logic
remains adapter-side; the adapter's ONLY remaining responsibility is
passing the raw payload it already has, plus the transport-specific
`Publish` call itself, using whatever client/socket type it already has
in scope. This mirrors `ObserveErrorResponseFor`'s
own "adapter just dispatches based on a returned value" shape exactly —
consistent thinning discipline across BOTH of this document's new
mechanisms, not just one of them.

## Topic 5 — Observability: deep insight into which error pattern fired

### Motivation

Confirmed via code reading across all 3 APIs' adapter dispatch sites
(`adapters/nethttp`/`chi`'s `serve.go`, `adapters/mqtt5`/`zeromq`/`mqtt`'s
`adapter.go`/`binding.go`, `adapters/mqtt5`/`zeromq`'s
`reqreply_transport.go`): `stats.ReportErrors(obs, "error_pattern"/
"error_channel", mapErr)` is called ONLY when a MATCHED pattern's own
`mapFn`/encode itself fails — the rare sub-case. There is **no observer
signal at all** for the common, valuable case: "a declared `ErrorPattern`/
`ErrorChannel` successfully matched and fired." Today, callers can only
see the resulting numeric status code (e.g. HTTP 409, or a `RecordPublish`/
`RecordRequest` failure) — they cannot tell "this 409 was
`domain.EmailConflictError`" vs. "this 409 was some other declared
pattern" without inspecting response bodies out-of-band. REST's own
dispatch (`adapters/nethttp/serve.go`) has ZERO observer calls at either
of its two `ErrorResponseFor` call sites (middleware-fn error, handler
error) for the successful-match case — the most glaring instance of this
gap. The identical mechanism in `adapters/mcpgo` (MCP's own
`apimcp.ErrorPattern`) has the same gap, though MCP itself stays out of
this document's scope (see "Scope decisions") — noted here only as a
cross-reference, since a future MCP-focused round would likely want the
same fix using the same mechanism designed here.

This is a genuine "deep API insight" opportunity: a declared
`ErrorPattern`/`ErrorChannel` is, by construction, a catalogue of every
KNOWN, NAMED failure mode an API author has anticipated. Recording which
one fires, how often, and from where, turns that catalogue into a live
observability signal — hit-rate-per-error-type dashboards, alerting on an
unexpected spike in one declared error type, and (via `RecordErrorPatternMiss`,
below) a coverage signal for "this location keeps failing in ways NO
declared pattern anticipated — should one be added?".

### Design goal: zero-effort observability for handler authors

**The observer hook belongs at the DECLARATION/DISPATCH layer — never
inside the handler — specifically so a handler author gets this
observability for FREE, without writing a single line of instrumentation
code themselves.** This mirrors every other observer call site already in
go-codex (`RecordRequest`, `RecordSecurityRejection`, `RecordSubscribe`/
`RecordPublish`): the adapter's dispatch loop calls the observer on the
handler's behalf, because the dispatch loop is the one place that already
sees every outcome, for every handler, uniformly — a handler author never
calls `obs.RecordRequest` themselves today, and should never need to call
an error-pattern-specific equivalent either.

Concretely: a handler author who declares
`rest.ErrorPattern[domain.EmailConflictError, ErrorBody](409, ...)` and
simply `return ErrorBody{}, domain.EmailConflictError{...}` from their
handler gets FULL observability — hit counts, structured logs, trace
tags — the moment `RecordErrorPatternMatch` is wired into the adapter
dispatch loop, with ZERO changes to the handler function itself. The
alternative (asking every handler author to manually call
`obs.RecordSomething(...)` before returning a declared domain error) would
re-introduce exactly the imperative, repeated-per-handler boilerplate the
declarative `ErrorPattern`/`ErrorChannel` mechanism itself was designed to
eliminate — inconsistent with the "declare → compose → register" workflow
the whole library is built around (see this skill's "User Experience
North Star"). This is not a new principle invented for Topic 5 — it is
the SAME principle that already justifies `RecordRequest`/
`RecordSecurityRejection`/every other adapter-owned observer call
existing today — Topic 5 is simply the same principle, applied to a
declaration surface (`ErrorPattern`/`ErrorChannel`) that has, until now,
been the one gap where it wasn't yet fully honored.

A useful acceptance test for any future implementation of this topic:
**a handler function's source code should be IDENTICAL whether or not an
`Observer` is configured at all** — adding/removing observability must
never require touching a single handler.

### Proposed design

A new, optional `stats.Observer` extension — type-asserted at each
adapter call site, mirroring `SecurityObserver`'s established pattern
exactly (purely additive; existing `Observer` implementations are
unaffected):

```go
// stats/observer.go

// ErrorPatternObserver is an optional extension to [Observer] for
// declared-error-pattern observability — REST's ErrorPattern, events'
// ErrorChannel, and reqreply's ErrorPattern all share this ONE mechanism
// (their existing errors.As-matched, first-declared-wins, codec-backed
// design is already unified — see Topic 1). Adapters type-assert the
// configured Observer to ErrorPatternObserver before calling either
// method, so implementing this interface is purely additive.
type ErrorPatternObserver interface {
	// RecordErrorPatternMatch is called when a declared ErrorPattern/
	// ErrorChannel matches a failing operation's error via errors.As —
	// regardless of whether the matched pattern's OWN encode/mapFn then
	// succeeds or fails (that sub-case is separately reported via the
	// existing stats.ReportErrors(obs, "error_pattern"/"error_channel", ...)
	// mechanism, unchanged). location is the route path/topic template
	// (same convention as RecordRequest). code identifies which declared
	// pattern matched — REST: derived from status (e.g. "409"); events/
	// reqreply: the pattern's own Code (WithCode or the sanitized-type-
	// name default). action is the resolved action as a plain string
	// ("respond"/"handle"/"log" for REST/events; "" for reqreply, which
	// has no ErrorAction concept — always responds).
	RecordErrorPatternMatch(location, code, action string)

	// RecordErrorPatternMiss is called when a failing operation's error
	// matched NO declared ErrorPattern/ErrorChannel (the raw/plain-text
	// fallback path) — a coverage-analysis signal distinct from a match:
	// "this location keeps failing in a way nothing declared here
	// anticipated; should a pattern be added?"
	RecordErrorPatternMiss(location string)
}
```

**Why a plain `string` for `action`, not a shared typed value**: `stats`
is a low-level package `api/rest`/`api/events`/`api/reqreply` all import
— it cannot import any of them back (cyclic). Confirmed via code that
`reqreply.ErrorPattern` has NO `ErrorAction`/`WithAction` concept at all
(unlike REST/events' 3-way `ErrorRespond`/`ErrorHandle`/`ErrorLog`) —
reqreply always "responds," since it is always answering a correlated
caller, with no separate "handle via existing callback" alternative the
way REST/events have. A plain string sidesteps both problems: no import
cycle, and no forced-empty field for reqreply's callers.

**Why split into two methods, not one with a `matched bool` parameter**:
mirrors `SecurityObserver.RecordSecurityRejection`'s own precedent — a
purpose-named, single-outcome method, not a generic "check" method with a
boolean outcome flag. The two cases also have genuinely different useful
data: a match has a meaningful `code`+`action`; a miss has neither
(nothing matched) — forcing both into one signature would mean the miss
case passes a meaningless empty `code`/`action`. `RecordErrorPatternMiss`
also has standalone value as a coverage metric, independent of whether a
match ever happens elsewhere for that same location.

### Core-layer consolidation — DECIDED this round: `ObserveErrorResponseFor`, keeping adapters thin

**Confirmed real gap in this topic's OWN original design**: as sketched
above, EVERY one of Topic 1's ~26+ Category-A call sites across REST/
events/reqreply would need to independently type-assert `obs` to
`ErrorPatternObserver` (twice — match and miss) AND to `SpanTagger`,
compute `location`/`code`/`action` per-API, and gate
`RecordErrorPatternMiss` behind `HasErrorPatterns()` — all repeated
adapter-side boilerplate. This directly contradicts the Design Goal
("keeps adapters thin") and this document's own established precedent
elsewhere (`rest.DiagnosticObserver`/`Report*Errors`, `route.
FirstSchemeName`, `MiddlewareOutputError` wrapped ONCE in each API's own
`transform.go` rather than per-adapter) — Topic 5's first draft simply
had not yet applied the same discipline to its OWN new mechanism.

**Confirmed via code that every value this needs is already derivable
from the handle + the response `ErrorResponseFor` already returns** —
zero adapter-supplied arguments needed beyond `ctx`, `obs`, and `err`:

- `location`: REST derives it from `h.Descriptor.Path` (the SAME
  convention `RecordSecurityRejection` already uses at
  `adapters/nethttp/serve.go`); events/reqreply derive it from `h.Topic`
  (both already exported fields on `ChannelHandle`/`RouteHandle`).
- `code`: REST derives `strconv.Itoa(resp.Status)`; events/reqreply use
  `resp.Topic`/`resp.Code` directly (both already present on the
  response struct `ErrorResponseFor` already returns).
- `action`: REST/events use `string(resp.Action)`; reqreply always `""`
  (no `ErrorAction` concept).

**Fix: one new method per API**, added ALONGSIDE `ErrorResponseFor` (NOT
replacing it — `ErrorResponseFor` stays available standalone for
contexts with no observer, e.g. client-side reply decode). REST's own
body, worked out concretely (`api/events`/`api/reqreply` mirror this
exactly, substituting their own `location`/`code`/`action` derivation
per the bullets above):

```go
// api/rest/builder.go

// ObserveErrorResponseFor is the observability-aware counterpart of
// [RouteHandle.ErrorResponseFor]: it performs the SAME errors.As match,
// but ALSO reports the outcome to obs — RecordErrorPatternMatch on a
// match, RecordErrorPatternMiss on a miss (only when HasErrorPatterns()
// is true), and SpanTagger.TagSpan when both a match occurs AND obs
// implements SpanTagger — all type-asserted and derived INTERNALLY, so
// the adapter needs no knowledge of ErrorPatternObserver/SpanTagger/
// HasErrorPatterns at all. Returns the IDENTICAL 3-tuple
// ErrorResponseFor already returns, so an adapter's existing
// resp.Action-dispatch logic (write body, run ErrorHandle's callback,
// etc.) needs NO changes beyond swapping which method it calls.
func (h *RouteHandle[Req, Resp]) ObserveErrorResponseFor(
	ctx context.Context, obs stats.Observer, err error,
) (resp ErrorPatternResponse, matched bool, applyErr error) {
	resp, matched, applyErr = h.ErrorResponseFor(err)
	location := h.Descriptor.Path
	switch {
	case matched && applyErr == nil:
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMatch(location, strconv.Itoa(resp.Status), string(resp.Action))
		}
		if st, ok := obs.(stats.SpanTagger); ok {
			st.TagSpan(ctx, "error_pattern.code", strconv.Itoa(resp.Status))
		}
	case !matched && h.HasErrorPatterns():
		if po, ok := obs.(stats.ErrorPatternObserver); ok {
			po.RecordErrorPatternMiss(location)
		}
	}
	return resp, matched, applyErr
}
```

**Adapter call-site impact**: every one of Topic 1's ~26+ call sites
shrinks from "call `ErrorResponseFor`, then manually wire 2-3
type-asserts" down to a single call —
`handle.ObserveErrorResponseFor(ctx, obs, err)` — with the exact same
return shape adapters already consume today. Zero behavior change to
the adapter's OWN subsequent dispatch logic; only the observability
wiring is centralized. This also means `HasErrorPatterns()` becomes an
internal implementation detail of `ObserveErrorResponseFor` — adapters
never need to call it directly (it remains exported for the rare caller
who wants the raw signal without going through this wrapper).

### Structured logging expectations for `E` and `B` — DECIDED: recommend, do NOT hard-require

Metrics (`ErrorPatternObserver`) and traces (span tagging) are two of the
three observability pillars this document addresses — the third is
STRUCTURED LOGGING, via `slog.LogValuer`. This raised a natural question:
should `ErrorPattern[E error, B any]`'s two type parameters be required
to implement `slog.LogValuer`, mirroring go-codex's own internal
structured-error convention (every one of its own types —
`MiddlewareInputError`, `SecurityError`, `DuplicateErrorPatternCodeError`,
etc. — implements `Error()`+`Unwrap()`+`LogValue()` uniformly)?

**Investigated concretely — decision: recommend/document, do NOT hard-require, for either type parameter.**

**`E` (the domain error, matched via `errors.As`)** is already required to
implement `error` — that part is unchanged, enforced by the existing
generic constraint. The question is whether to ALSO require
`slog.LogValuer`. Confirmed via direct evidence this would be a real,
immediate breaking change: every domain error type currently used with
`ErrorPattern` in go-codex's OWN shipped examples —
`EmailConflictError` (`examples/adapters-nethttp-client`),
`insufficientCreditError` (`examples/error-types`), `ConflictError`
(`examples/reqreply-api`) — implements ONLY `Error() string`, none
implement `slog.LogValuer`. A hard requirement would fail to compile
against real, already-shipped code TODAY, not just hypothetical future
callers.

This also runs against how the wider Go ecosystem treats this exact kind
of interface. Checked `log/slog`'s own source
(`log/slog/value.go`): `LogValuer`'s `Resolve()` mechanism is built
ENTIRELY around duck-typing — it type-asserts whether a value implements
`LogValuer` and falls back to reflection-based default formatting
otherwise; the stdlib's OWN godoc frames it as an OPTIONAL enhancement
("may be used to defer expensive operations… or expand a single value
into a sequence of components"), never a requirement layered onto an
unrelated API. No comparable Go structured-logging interface (zap's
`zapcore.ObjectMarshaler`, zerolog's `LogObjectMarshaler`) is required by
an unrelated API anywhere in the broader ecosystem either — this would be
a novel departure, not an established pattern being extended.

Three further costs, independent of the breaking-change evidence:

1. **Logging-library coupling**: `ErrorPattern`/`stats.Observer` are
   otherwise pluggable and logging-library-agnostic (a caller may use
   `zap`, `zerolog`, plain `fmt`, or nothing at all) — hard-requiring
   `slog.LogValuer` specifically would privilege ONE logging package at
   the type-system level, for zero benefit to callers who don't use it.
2. **Forced boilerplate with no default-method escape hatch**: Go has no
   mechanism to auto-derive a sensible `LogValue()` from a struct's own
   fields — every caller would need to hand-write one for every domain
   error type, directly contradicting this document's own Design Goal
   (reduce boilerplate, not add it).
3. **No compile-time value for `B`** even if considered: `B` is a wire
   payload DTO, not a Go error value in the common "Mapped" mode — forcing
   `slog.LogValuer` (or `error`) onto it would be an arbitrary constraint
   with no corresponding ergonomic upside, for the same duck-typing
   reasons as `E`.

**Decision**: keep `slog.LogValuer` fully OPTIONAL and duck-typed for both
`E` and `B`, exactly as `log/slog` itself treats it everywhere else —
document/recommend it (mirroring go-codex's own internal convention) as
good practice for callers who want richer structured logs, with a short
example in a future guide update, but never enforce it via the generic
constraint. `B` additionally should NOT be required to implement `error`
at all (a wire DTO has no need for Go error semantics in the common
Mapped-mode case) — already noted, reaffirmed here for completeness.
`ErrorPatternResponse.LogValue()` (already shipped, all 3 client
packages) already calls `slog.Any("value", e.Value)`, which automatically
invokes `B`'s own `LogValue()` if present — so a caller who DOES choose to
implement it gets richer logs for free, with zero additional code needed
anywhere else in the mechanism.

### Trace integration

Superseded by the "Core-layer consolidation" subsection above:
`ObserveErrorResponseFor` already calls `SpanTagger.TagSpan` internally
on a match (type-asserted, exactly like `ErrorPatternObserver`) — an
adapter gets trace tagging "for free" from the SAME single call it makes
for match/miss recording, with no separate wiring step. This lets a
trace immediately show *why* a request failed (which declared error
type), not just *that* it failed (a bare status code) — the difference
between "500 errors spiked" and "EmailConflictError spiked,
InventoryShortageError did not" in a trace-driven investigation.

### Call sites (all 3 APIs — mirrors Topic 1's FULL enumeration, not just handler/middleware-Fn)

Every Category-A row in Topic 1's per-API enumeration tables is, by
construction, a future `ObserveErrorResponseFor` call site too — the
observer hook rides along with WHATEVER call sites Topic 1's fix
touches, at every one of them, not a separately-chosen subset. Per the
"Core-layer consolidation" subsection above, each row below now means
"replace this row's `ErrorResponseFor` call with `ObserveErrorResponseFor`"
— a one-line adapter change per row, not a multi-line type-assert
addition:

| API | Already-shipped call site(s) (ship alongside `ErrorPatternObserver` itself) | Newly in scope (ship alongside Topic 1's fix, same rollout) |
|---|---|---|
| REST | `adapters/nethttp`/`chi`'s `serve.go` — middleware-fn error branch AND handler error branch (2 sites, currently ZERO observer calls at either) | The other 10 Category-A rows (matches Topic 1's "10 of 12" count): body decode, path/query/cookie/header param validation, middleware `DecodeIn`/`EncodeOut`, response merge-field encode, response body encode, security middleware Fn |
| events | `adapter.go`/`binding.go` handler+middleware-Fn dispatch (mqtt5/zeromq/mqtt) | Payload decode, topic/property-var merge, middleware `DecodeIn`/`EncodeOut`, security middleware Fn |
| reqreply | `reqreply_transport.go`'s `publishHandlerErrorReplyReflect`/`sendHandlerErrorReplyReflect`/`sendRouterHandlerErrorReplyReflect` | Request decode, topic/property-var merge, middleware `DecodeIn`/`EncodeOut`, security middleware Fn |

This is a direct consequence of the "reduce observability effort in
handlers" design goal applied consistently and at full scope: closing
Topic 1's now-fully-enumerated gap and NOT ALSO wiring
`ErrorPatternObserver` into every one of those SAME call sites would
leave the vast majority of typed-response paths invisible to the SAME
observability this document otherwise guarantees for the 2 already-
shipped call sites — an inconsistency this table exists to prevent from
being introduced by omission. Category B (codex)/Category C
(ports/adapter infrastructure) errors correctly KEEP their own existing,
separate observer calls (`RecordValidationError`, `RecordRequest`
status=0, etc.) — Topic 5 does not touch or duplicate those; it only
adds the NEW signal for the response-shape-relevant subset.

### Design decisions — DECIDED this round

- **`TraceObserver`'s span-tagging hook**: a NEW, SEPARATE, optional
  interface — `stats.SpanTagger` — rather than a new method added
  directly to the existing `TraceObserver` interface:

  ```go
  // stats/observer.go

  // SpanTagger is an optional extension to [Observer]/[TraceObserver] —
  // implement it to tag the CURRENT active span with additional
  // key/value context (e.g. which declared ErrorPattern/ErrorChannel
  // fired). Callers do NOT type-assert this directly — per the
  // "Core-layer consolidation" subsection above, ObserveErrorResponseFor
  // is the ONLY caller of SpanTagger, mirroring SecurityObserver's
  // established type-assertion pattern internally — purely additive,
  // zero adapter-side wiring.
  type SpanTagger interface {
	  TagSpan(ctx context.Context, key, value string)
  }
  ```

  Confirmed via `grep -rn "func.*StartSpan(ctx context.Context"` that
  `TraceObserver` has ~13 existing concrete implementers today (test
  spies across 8 packages, `stats.fanout`/`NoopObserver`, and 2 real
  examples) — adding a required 3rd method directly to `TraceObserver`
  would break every one of them. A separate, independently
  type-asserted `SpanTagger` interface avoids this entirely: callers who
  want span tagging implement one more small interface; everyone else is
  unaffected.
- **`RecordErrorPatternMiss` fires ONLY when at least one
  `ErrorPattern`/`ErrorChannel` IS declared** on that route/channel — NOT
  for every unmatched error unconditionally. Confirmed via code
  (`RouteHandle.ErrorResponseFor`'s `errorPatternRules` slice is
  currently unexported, with no existing accessor) that this needs a new,
  small, exported accessor: `RouteHandle.HasErrorPatterns() bool`
  (`api/rest`, `api/reqreply`) / `ChannelHandle.HasErrorPatterns() bool`
  (`api/events`) — a one-line `len(h.errorPatternRules) > 0` wrapper.
  Per the "Core-layer consolidation" subsection above, this accessor is
  called INTERNALLY by `ObserveErrorResponseFor` — adapters never call
  it directly; it stays exported only for the rare caller who wants the
  raw signal outside that wrapper. This keeps the coverage signal
  meaningful: "this location keeps failing in a way NOTHING declared
  here anticipated" (a real signal worth alerting on) vs. "nobody
  declared anything for this location at all" (not a coverage gap,
  simply not applicable) stay distinguishable.
- **`LoggingObserver`/`NoopObserver`/`fanout` implementations**:
  mechanical, no separate design decision — these ship alongside the new
  `ErrorPatternObserver`/`SpanTagger` interfaces themselves, as part of
  the same rollout (mirrors every other optional extension's existing
  rollout checklist).
- **One-line MCP cross-reference**: `adapters/mcpgo`'s `apimcp.ErrorPattern`
  has the IDENTICAL "no signal for a successful match" gap — out of this
  document's scope, but a future MCP-focused round should reuse
  `stats.ErrorPatternObserver` unchanged rather than inventing a second
  mechanism.

## Topic 6 — Client-side ergonomics: working with matched patterns conveniently

### Motivation

Topics 1/Phase 0 close the SERVER-side declaration gap (every failure
point becomes `ErrorPattern`-eligible) and the wire round-trip (client
receives a typed, decoded payload via `nethttp.ErrorPatternResponse`/
`mqtt5.ErrorPatternResponse`/`zeromq.ErrorPatternResponse`). But "declare
on the server" and "use conveniently on the client" are DISTINCT
concerns — this topic is about the latter specifically.

Today's client-side workflow (unchanged since Phase 0) is a 2-step manual
dance:

```go
var patternResp nethttp.ErrorPatternResponse
if errors.As(err, &patternResp) {
    switch v := patternResp.Value.(type) {
    case domain.EmailConflictError:
        // ...
    case domain.ValidationError:
        // ...
    default:
        // unexpected/unmapped payload type
    }
}
```

This works and is already documented (`docs/guides/http-client.md`,
`docs/guides/asyncapi.md`'s "Client-side decode" section) — but requires
the caller to manually repeat `errors.As` + a type switch on `any` for
every call site, with no compile-time connection back to which
`ErrorPattern` declaration produced which case. This topic sketches 3
alternatives to make this more convenient — **all 3 are now DECIDED,
shipping together** (see the subsection immediately below).

### DECIDED this round: build all 3 alternatives together

All 3 alternatives below ship together — they are complementary, not
competing: Alternative 1 is the primary, zero-usage-shift mechanism;
Alternative 2 is a thin wrapper over it for callers who already keep
declarations as package-level vars; Alternative 3's original
"reflection-heavy, not recommended" framing turned out to be WRONG on
re-inspection this round (see below) — a clean, zero-`reflect`
implementation exists, so it ships too rather than staying deferred.

### Alternative 1 — `ErrorPatternAs`, a generic one-line decode helper (primary mechanism)

Renamed from the original placeholder `DecodeErrorPatternAs` →
`ErrorPatternAs` this round — "Decode" was misleading, since nothing is
decoded AT this call; the wire decode already happened earlier, inside
`DecodeErrorFor`. This function only extracts the already-decoded
`Value` — "As" mirrors `errors.As`/Topic 7's `SubscribeError.As` naming
family directly.

> **CORRECTED in a later review round**: this section originally placed
> `ErrorPatternAs` (and `HandleErrorPattern`/`Case`, Alternative 3 below)
> in EACH client adapter package (`adapters/nethttp`, `adapters/mqtt5`,
> `adapters/zeromq`), reasoning "3/2 near-identical copies... mirroring
> the existing precedent that each adapter keeps its OWN
> `ErrorPatternResponse`/`CallError` type." **That reasoning was a design
> mistake** — it conflated two different things. It IS correct that the
> CONCRETE response type (`nethttp.ErrorPatternResponse`,
> `mqtt5.ErrorPatternResponse`, `zeromq.ErrorPatternResponse`)
> legitimately differs per adapter (protocol-specific fields like
> `StatusCode` vs `Code`). But `ErrorPatternAs`/`HandleErrorPattern`/
> `Case` touch ONLY the shared, already-core-layer `ErrorPatternValuer`
> interface (Alternative 2 below already used this same interface
> correctly) — they have ZERO protocol-specific logic and never needed
> to live in an adapter at all. This violated this library's own "thin
> adapter" design guardrail (see the new subsection below) — a real,
> user-reported design gap, not a style preference. **Fixed**:
> `ErrorPatternAs`/`HandleErrorPattern`/`Case` now live in `api/rest` and
> `api/reqreply` (ONE implementation each, not one per adapter) — `mqtt5`
> and `zeromq` no longer each carry a byte-for-byte-identical copy. The
> code example below is kept for illustration but now describes the
> CORE-layer implementation, not an adapter-owned one.

```go
// api/rest and api/reqreply each gain this ONCE (not once per adapter)
// — NOT adapters/chi (no client exists there at all) and NOT api/events
// (no synchronous caller — Topic 2's reasoning applies identically here).
//
// ErrorPatternAs extracts a matched ErrorPattern's typed payload in one
// call, collapsing the errors.As + type-switch dance above into a
// single conditional. Works against ANY adapter's own error-pattern
// response type, since all of them implement ErrorPatternValuer (see
// Alternative 2) — this function never needs to know which adapter
// produced err.
func ErrorPatternAs[B any](err error) (B, bool) {
	var target ErrorPatternValuer // the SAME core-layer interface Alternative 2 uses
	if !errors.As(err, &target) {
		var zero B
		return zero, false
	}
	b, ok := target.ErrorPatternValue().(B)
	return b, ok
}
```

Usage (unchanged for callers, only the import path changed):

```go
if conflict, ok := rest.ErrorPatternAs[domain.EmailConflictError](err); ok {
	return promptDifferentEmail(conflict.Email) // conflict is fully typed
}
```

Low-risk, purely additive — no change to the existing `ErrorPatternResponse`
type or any existing behavior. ONE implementation per API (`api/rest`,
`api/reqreply`), reused transparently by every adapter that implements
that API's `ErrorPatternValuer` interface — not "one per adapter."

### Alternative 2 — `.Match` method on the declaration value (thin wrapper over Alternative 1)

```go
// api/rest (and api/reqreply's identical mirror) — ErrorPatternOpt[E,B],
// the value rest.ErrorPattern[E,B](...)/reqreply.ErrorPattern[E,B](...)
// already returns, gains a client-facing Match method.
func (o ErrorPatternOpt[E, B]) Match(err error) (B, bool) {
	// delegates to the SAME generic decode as Alternative 1, scoped to
	// THIS declaration's own B type — the compiler already knows B from
	// o's own type parameters, so no explicit [B] instantiation is
	// needed at the call site. Plain (B, bool) return, matching a
	// standard Go type-assertion idiom — NO dedicated error type, since
	// there is nothing more specific to report than "this err did not
	// come from this declaration."
}
```

Usage — the SAME value declares the pattern (server) AND matches it
(client), embodying go-codex's existing "declare once, use both
directions" philosophy directly:

```go
// Declared ONCE, at package scope:
var emailConflictPattern = rest.ErrorPattern[domain.EmailConflictError, domain.EmailConflictError](409, conflictCodec)

// Server: attached via NewRoute's variadic opts.
route := rest.NewRoute[CreateUserReq, User]("POST", "/users", reqCodec, userCodec,
	emailConflictPattern,
)

// Client: the SAME value matches the decoded response.
resp, err := nethttp.CallWithHandle(ctx, client, baseURL, handle, req, opts)
if conflict, ok := emailConflictPattern.Match(err); ok {
	// conflict is domain.EmailConflictError, fully typed
}
```

More self-documenting (the match call sits right next to the
declaration, no need to remember/import the exact payload type
separately) but requires callers to KEEP the declaration as a
package-level var instead of inlining `ErrorPattern(...)` directly into
`NewRoute`'s variadic args — a real usage-pattern shift from how most
existing examples/tests declare patterns today (inline, not as a
separate var); not a blocker, just a different, ADDITIONAL style — the
inline style keeps working unchanged. What `Match` does if `B` collides
with another declared pattern's `B` on the SAME route is moot in
practice now that Topic 1's reopened fix makes REST reject 2+
same-status `ErrorPattern`s at `Register` time
(`DuplicateErrorStatusError`), exactly mirroring reqreply's existing
`DuplicateErrorPatternCodeError` rejection — both APIs now hard-reject
the ambiguous case up front, so `Match` never has to arbitrate a
collision at call time; it is just a thinner FRONT END over the SAME
existing `DecodeErrorFor`/`ErrorResponseFor` machinery.

### Alternative 3 — `HandleErrorPattern`, a declarative dispatch table (CORRECTED this round: no reflection needed)

**Correction**: the original framing of this alternative claimed it was
"reflection-heavy" and needed `reflect.TypeOf` on each closure. Re-examined
this round — that claim was WRONG. A clean, zero-`reflect` Go-generics
implementation exists, using an interface + per-case type assertion (the
same "typed case" idiom used elsewhere for heterogeneous
type-parameterized collections in Go):

> **CORRECTED in a later review round**: same placement fix as
> Alternative 1 above — `HandleErrorPattern`/`Case` moved from
> `adapters/nethttp`/`adapters/mqtt5`/`adapters/zeromq` into `api/rest`/
> `api/reqreply` (ONE implementation each). The code below is kept for
> illustration but now describes the core-layer implementation.

```go
// api/rest and api/reqreply each gain this ONCE — same scope as
// Alternatives 1/2.

// errorCase is the internal, type-erased interface each Case[T] value
// implements — this is what makes a slice of heterogeneous Case[T]
// values possible without reflect.
type errorCase interface {
	tryHandle(value any) bool
}

type typedCase[T any] struct {
	fn func(T)
}

// Case declares one typed handler for HandleErrorPattern — T is
// inferred from fn's own parameter type, so no explicit [T]
// instantiation is needed at the call site.
func Case[T any](fn func(T)) errorCase {
	return typedCase[T]{fn: fn}
}

func (c typedCase[T]) tryHandle(value any) bool {
	v, ok := value.(T) // ordinary type assertion — NOT reflect.TypeOf
	if !ok {
		return false
	}
	c.fn(v)
	return true
}

// HandleErrorPattern extracts a matched ErrorPattern's typed payload
// ONCE (a single errors.As call, unlike each Case doing its own), then
// dispatches to the first Case whose T matches the payload's concrete
// type. Returns false when err carries no ErrorPatternValuer value at
// all, OR when it does but no Case's T matches its value's concrete
// type.
func HandleErrorPattern(err error, cases ...errorCase) bool {
	var target ErrorPatternValuer // the SAME core-layer interface Alternative 2 uses
	if !errors.As(err, &target) {
		return false
	}
	for _, c := range cases {
		if c.tryHandle(target.ErrorPatternValue()) {
			return true
		}
	}
	return false
}
```

Usage:

```go
handled := rest.HandleErrorPattern(err,
	rest.Case(func(e domain.EmailConflictError) { promptDifferentEmail(e.Email) }),
	rest.Case(func(e domain.ValidationError) { showValidationErrors(e) }),
)
if !handled {
	// no case matched — unmapped payload type, or no ErrorPattern matched at all
}
```

This gives the closest visual parity to a `switch`/`match` expression —
the type is inferred from each closure's own parameter, so a caller never
has to name it explicitly at the call site (unlike Alternative 1's
`[B]` instantiation). Real, remaining trade-offs versus Alternatives 1/2
(honestly assessed, not a reason to defer — just context for when to
reach for which):

- It is a fully SEPARATE mechanism, not a thin wrapper over Alternative 1
  (unlike Alternative 2) — genuinely new maintenance surface, though (per
  the correction above) only ONE implementation per API, not one per
  adapter.
- It is client-only — no "declare once, use both directions" angle the
  way Alternative 2 has (`ErrorPatternOpt.Match` reuses the SAME value
  the server declares with).
- `Case[T]` is intended for CONCRETE domain error/payload types (mirrors
  `ErrorPattern[E, B]`'s own typical usage) — using an interface type for
  `T` could make dispatch order matter if a value satisfies more than one
  registered `Case`'s interface; this is the same "first-declared-wins"
  precedent already established elsewhere in this document (Topic 1),
  not a new kind of ambiguity, but worth a one-line godoc callout when
  implemented.

### Design guardrail: adapters implement wire protocols only — client-side ergonomics belong in `api/*`

Added in the SAME later review round that corrected Alternatives 1/3
above, generalizing the specific mistake into a permanent, explicit rule
for this library (and for this skill's own review checklist, which now
cross-references it — see `.github/skills/review-go-codex/references/checklist.md`
§13):

> **Adapters implement wire protocols only.** Any user-facing convenience
> or ergonomic helper that touches ONLY core `api/*` types — codecs,
> handles, declared patterns, or a core-layer interface like
> `ErrorPatternValuer` — belongs in `api/*`, never in `adapters/*`. This
> holds EVEN WHEN, at the time of writing, only one adapter happens to
> implement that boundary (as REST's `nethttp` did before this fix) —
> "only one adapter exists today" is not a justification for placing
> transport-independent logic inside that one adapter; the test is
> whether the logic COULD be written using only `api/*` types, not how
> many adapters currently exist.

This is the client-side/consumption-side mirror of the "thin adapter"
principle Category A's server-side dispatch consolidation (Topic 1/5,
`ObserveErrorResponseFor`/`DeadLetterFor`) already established — that
work made adapters call ONE shared, generic core-layer method at every
Category-A dispatch point instead of hand-rolling per-adapter logic.
`ErrorPatternAs`/`HandleErrorPattern`/`Case` are the exact same shape of
mistake on the OTHER side of the boundary: convenience helpers a CALLER
uses after receiving a response, which — exactly like the server-side
dispatch helpers — only ever need `errors.As` against a core-layer
interface (`ErrorPatternValuer`), never anything adapter-specific.

**The user-experience promise this protects**: a caller should be able
to declare and consume a communication pattern using ONLY the `api/*`
abstraction — attaching a specific adapter is purely a protocol-selection
decision, never something that changes which helper functions/vocabulary
the caller reaches for. `rest.ErrorPatternAs`/`reqreply.ErrorPatternAs`
(not `nethttp.ErrorPatternAs`/`mqtt5.ErrorPatternAs`/`zeromq.ErrorPatternAs`)
is what makes the whole workflow genuinely protocol-independent, matching
this document's own "declare it, don't dispatch it" framing for the
server side.

**Audited against all 3 APIs this round** — confirmed `api/events` (pub/sub)
has NO equivalent gap: pub/sub has no synchronous caller to hand a
matched error back to (Topic 2's reasoning), so a downstream consumer
just subscribes to the declared error-output topic as an ordinary typed
channel — already core-layer, already protocol-independent, nothing to
move. `mcp.ErrorPattern` and `websocket.ErrorFrame` were also checked and
confirmed structurally different (MCP tool errors are structured
`CallToolResult` values, never a Go `error` the caller matches via
`errors.As`; `ErrorFrame` broadcasts to all sessions, it isn't a
call/response the caller receives a matched error back from) — neither
has an analogous client-side helper to misplace.

### Retry/idempotency guidance (documentation addition, no code change)

Worth stating explicitly and prominently once any of Alternatives 1–3
ship, since it's easy to get wrong: **a matched `ErrorPatternResponse` is
a BUSINESS DECISION the server made deliberately — never safe to blindly
retry** — fundamentally different from a transport-level failure like
`nethttp.RequestError`/`mqtt5.CallError{Kind: KindTimeout}`, which
usually IS safe to retry (network blip, DNS hiccup, broker unavailable).
Retrying an `EmailConflictError` or `InsufficientCreditError` response
verbatim will just produce the SAME business rejection again — the
caller needs to change something (a different email, wait for a balance
top-up) before retrying makes sense, if it ever does. Today's guide
(`docs/guides/http-client.md`) already states a similar "rule of thumb"
for `nethttp.RequestError` specifically, but doesn't yet call out the
CONTRASTING case for a matched `ErrorPatternResponse` explicitly — worth
adding alongside whichever Topic 6 alternative ships, so the two
"opposite" pieces of guidance sit next to each other rather than only
one being stated.

### Scope

Applies to: `adapters/nethttp` (REST client), `adapters/mqtt5`/
`adapters/zeromq` (reqreply client, both REQ/REP and ROUTER/DEALER for
zeromq). Does NOT apply to: `adapters/chi` (no client exists),
`api/events` (no synchronous caller to decode a reply for — reaffirms
Topic 2's reasoning).

### Design decisions — ALL DECIDED this round

- **Which alternative(s) to build**: all 3, together (see "DECIDED this
  round" above).
- **Naming**: `ErrorPatternAs[B any](err error) (B, bool)` (renamed from
  the placeholder `DecodeErrorPatternAs`).
- **`Match`'s (Alternative 2) return shape**: plain `(B, bool)`, no
  dedicated error type — simpler, idiomatic, mirrors a standard Go
  type-assertion.
- **Whether Alternative 1 and 2 ship together**: yes, both — Alternative
  2 is implemented as a thin wrapper calling Alternative 1 internally.
- **Alternative 3's status**: no longer "roughest, most speculative" —
  its original "reflection-heavy" framing was found to be inaccurate
  this round (a zero-`reflect` implementation exists, shown above); it
  ships alongside Alternatives 1/2 rather than staying deferred.

## Topic 7 — Pub/sub role clarification + subscriber-side error convenience

### Role clarification: publisher = client/sender, subscriber = receiver/server

Investigated whether pub/sub's PUBLISH side has the same `ErrorChannel`
gap as the subscribe side (Topic 1's enumeration). It does not — but the
reasoning is worth stating explicitly, since it resolves a real point of
confusion (initially mis-read as a gap while researching this topic).

Confirmed via code (mqtt5/zeromq `adapter.go`'s `publish` function): a
publish-side `ClientMiddlewareHandlers` Fn error is returned directly to
whoever called `Publish(...)`, NEVER consulting `ErrorResponseFor`. This
is architecturally CORRECT, not a gap — confirmed via REST's own
documented precedent (`adapters/nethttp/client_middleware.go`'s
`dispatchClientMiddlewareIn` doc comment): *"client-side fn errors are
NOT run through `ErrorResponseFor` — that mechanism shapes SERVER
responses; a client-side error is simply returned to the caller
directly."* Confirmed identical in reqreply's client-side dispatch.

**`ErrorPattern`/`ErrorChannel` is architecturally a RECEIVER/SERVER-side
concept** — it exists to shape a WIRE RESPONSE for someone else to
receive. Pub/sub's PUBLISHER plays the exact same "client/sender" role
REST's/reqreply's CLIENTS already play — correctly excluded, for the
identical reason. Pub/sub's SUBSCRIBER plays the "receiver/server" role —
where `ErrorChannel` correctly belongs, and where Topic 1's Category-A
fix + Topic 4's dead-letter mechanism already apply.

| Role | REST | reqreply | events | `ErrorPattern`/`ErrorChannel`-eligible? |
|---|---|---|---|---|
| Receiver/server | Route handler | Server handler | Subscribe handler | **Yes** (Topic 1) |
| Client/sender | `Client.Call`'s `ClientMiddlewareHandlers` | Same | Publish's `ClientMiddlewareHandlers` | **No, by design** — no wire response exists to shape |

**No code change needed here** — this is a confirmed, already-correct,
consistent design across all 3 APIs; stated explicitly so a future round
doesn't "fix" publish-side exclusion by mistaken analogy with the
subscribe-side gap Topic 1 closes.

### Subscriber-side error convenience: a thin, decode-free helper

Once Topic 1's Category-A fix lands, a subscribe-side failure that
matches NO declared `ErrorChannel` still reaches the caller's `OnError`
callback as a raw `SubscribeError{Kind, Topic, Err}` — the SAME kind of
"caller must manually `errors.As`/type-switch on `.Err`" ergonomic gap
Topic 6 identifies for REST/reqreply's CLIENT side, but with one key
difference: **no wire decode is involved**. The subscriber already HAS
the original Go error value natively (it was never encoded/decoded across
a wire boundary for this purpose) — so Topic 6's `ErrorPatternAs`
(which exists specifically to decode WIRE BYTES into a typed Go value)
does not apply here; what's needed instead is a much simpler convenience:
extracting a specific error type from the (already-Go-native)
`SubscribeError.Err` chain in one call instead of two.

```go
// adapters/mqtt5, adapters/zeromq, adapters/mqtt each gain this — mirrors
// Topic 6's naming, but semantically simpler: no decode step, purely a
// thin errors.As wrapper scoped to SubscribeError's own Err field.
func (e SubscribeError) As(target any) bool {
	return errors.As(e.Err, target)
}
```

Usage — collapses `errors.As(subErr.Err, &target)` into
`subErr.As(&target)`, a marginal but real ergonomic win, self-documenting
as "check what THIS subscribe failure actually was":

```go
opts.OnError = func(subErr mqtt5.SubscribeError) {
	var conflict domain.EmailConflictError
	if subErr.As(&conflict) {
		// ...
	}
}
```

This is a SMALL, low-risk addition (a one-line method, mirrors the
standard library's own `errors.As` naming convention directly) — flagged
here for completeness rather than as a major design point, since the
bulk of the subscriber-side convenience win already comes from Topic 1's
Category-A fix (most failures never reach `OnError` at all once a
matching `ErrorChannel` is declared — declare once, zero handler code,
per the Design Goal).

### Publisher side: no new mechanism needed

Confirmed: existing structured errors (`events.MiddlewareError`/
`MiddlewareOutputError`, already shipped) already give a publish caller
everything Topic 6-style ergonomics would otherwise provide — no wire
decode is involved (the error is a native Go value the SAME process just
produced), so there is nothing to add here beyond what already exists.

## Implementation planning

### Backward compatibility summary

Breaking-vs-additive status is scattered across each topic's own text —
consolidated here for a reviewer's quick reference. Per the **Breaking
Changes Policy** (above), breaking changes need NO special process
(no changelog callout, no major-version gating, no opt-in flag/transition
period) — this table is for AWARENESS of scope/impact, not because
breaking-ness gates any decision:

| Topic | Change | Breaking? |
|---|---|---|
| Phase 0 | reqreply client-side `ErrorPattern` decode | Already shipped, additive — no impact here |
| Topic 1 | Extend `ErrorPattern`/`ErrorChannel` eligibility to every Category-A failure point | **Additive** — a route/channel with NO matching declared pattern for a given failure point sees ZERO behavior change; only routes that ADD a new matching declaration gain new behavior |
| Topic 1 (reopened) | REST rejects 2+ `ErrorPattern`s sharing one status | **BREAKING**, narrowly scoped — changes `TestDecodeErrorFor_FirstMatchWins_SameStatus`'s expected outcome from silent first-match-wins to a hard `Register`-time rejection; any existing route/test/example declaring 2+ same-status patterns needs a quick fix (distinct statuses) |
| Topic 2 | Ratify same-reply-topic behavior | **No code change** — documentation only, behavior already shipped |
| Topic 3 | Migrate reqreply's AsyncAPI rendering to multi-message reply channels | **BREAKING to the GENERATED SPEC SHAPE** for any route with 2+ declared `ErrorPattern`s — the separate `<route>ReplyError<Code>` channel keys disappear, folded into the reply channel's `messages` map. No Go API signature changes; existing `ErrorPattern`/`ErrorReplyMeta` declarations need no caller-side changes — only the RENDERED spec output differs for consumers of that spec (codegen tools, doc sites) |
| Topic 4 | New `DeadLetter` mechanism | **Additive** — entirely new, opt-in `ChannelOpt`/`RouteOpt`; routes/channels declaring nothing see no change |
| Topic 5 | New `stats.ErrorPatternObserver` extension | **Additive** — optional, type-asserted interface extension; existing `Observer` implementations are unaffected |
| Topic 6 | New client-side decode helper(s) | **Additive** — new functions/methods, no existing API changes |
| Topic 7 | Role clarification + `SubscribeError.As` | **No code change** for the role clarification (documentation only, confirms already-correct behavior); `SubscribeError.As` is a **new, additive** method |

**Two topics now carry a genuine breaking change** (Topic 1's reopened
REST same-status fix, and Topic 3's AsyncAPI spec-shape migration) —
both narrowly scoped, both pre-approved under the Breaking Changes
Policy, both ship directly with no transition period. Everything else
is additive or documentation-only.

### Suggested phased implementation order

Not a strict requirement — but the topics have real dependencies worth
sequencing around:

1. **Topic 1 (full enumeration fix) + Topic 5 (observer wiring), together,
   per API** — ship these AS ONE UNIT, one API at a time (e.g. REST
   first, then events, then reqreply) rather than doing all of Topic 1
   before starting Topic 5. Topic 5's observer hooks ride along with
   WHATEVER call sites Topic 1's fix touches at each step — implementing
   them separately would mean either (a) temporarily wiring observer
   hooks into call sites `ErrorResponseFor` doesn't consult yet (dead
   code), or (b) a second pass through the same call sites shortly after
   the first (wasted rework). Doing both together, per call site, per
   API, avoids both.
2. **Topic 4 (dead-letter)** — independent of 1/5, can proceed in
   parallel or before/after; its own adapter-dispatch wiring touches
   DIFFERENT branches (the truly-unmatched fallback) than Topic 1/5's
   `ErrorResponseFor`-consultation branches, though they're adjacent code.
   Recommend doing Topic 1/5 FIRST per API, since Topic 4's dead-letter
   fires only once Topic 1's fuller `ErrorResponseFor` consultation has
   ALSO found no match — sequencing them in this order avoids threading
   the dead-letter fallback through call sites that don't have the
   `ErrorResponseFor` consultation wired yet.
3. **Topic 3 (AsyncAPI multi-message rendering)** — fully independent of
   1/4/5; touches `render/asyncapi/v3` + `api/reqreply/builder.go` only.
   Can ship anytime, including FIRST, if the breaking spec-shape change
   is more convenient to land before other reqreply-touching work is in
   flight (fewer merge-conflict surfaces).
4. **Topic 6 (client-side ergonomics)** — depends only on Phase 0
   (already shipped) — the client-side `ErrorPatternResponse`/
   `mqtt5.ErrorPatternResponse`/`zeromq.ErrorPatternResponse` types this
   topic's helpers wrap around already exist. Can ship anytime, fully
   independently of 1/3/4/5.
5. **Topic 2 (ratification) + Topic 7 (role clarification)** — pure
   documentation, no code dependency on anything; can ship whenever
   convenient, including alongside the doc's own eventual graduation to
   `docs/design/d-0005`.

## Unit test plan (sketch — to be expanded once API surface is finalized)

| Test | Verifies |
|---|---|
| `TestErrorPattern_DuplicateStatus_Rejected` (`api/rest`) | Topic 1's reopened fix: 2+ `ErrorPattern`s sharing one status on the SAME route are rejected at `Register`/`RegisterHandle` time with `rest.DuplicateErrorStatusError` — REPLACES `TestDecodeErrorFor_FirstMatchWins_SameStatus`'s old expected behavior |
| `TestAsyncAPI_ReplyChannel_MultipleMessages` (`api/reqreply`) | A route with 2+ `ErrorPattern`s renders ONE reply channel with a `messages` map containing all variants, no separate error channels |
| `TestAsyncAPI_ReplyChannel_SingleMessage_BackwardCompatible` (`api/reqreply`) | A route with ZERO declared `ErrorPattern`s renders identically to today (no regression for the common case) |
| `TestDeadLetter_UnmatchedError_Published` (`api/events`, `api/reqreply`) | An error matching no declared `ErrorChannel`/`ErrorPattern` gets published to the declared dead-letter topic with the correct envelope |
| `TestDeadLetter_MatchedError_NotPublished` (`api/events`, `api/reqreply`) | An error THAT DOES match a declared `ErrorChannel`/`ErrorPattern` does NOT ALSO reach the dead-letter topic (strict fallback-tier ordering) |
| `TestDeadLetter_DecodeFailure_Published` (`api/events`, `api/reqreply`) | A subscribe/request decode failure (no business error exists yet) reaches the dead-letter topic |
| `TestObserveErrorResponseFor_RecordsMatch` (`api/rest`, `api/events`, `api/reqreply`) | Core-layer consolidation: `ObserveErrorResponseFor` calls `RecordErrorPatternMatch` with the correct location/code/action on a match, returning the SAME 3-tuple `ErrorResponseFor` would |
| `TestObserveErrorResponseFor_RecordsMiss_OnlyWhenPatternsDeclared` (`api/rest`, `api/events`, `api/reqreply`) | `ObserveErrorResponseFor` calls `RecordErrorPatternMiss` when unmatched AND `HasErrorPatterns()` is true; does NOT call it when no patterns are declared at all |
| `TestObserveErrorResponseFor_TagsSpan_OnMatch` (`api/rest`, `api/events`, `api/reqreply`) | `ObserveErrorResponseFor` calls `SpanTagger.TagSpan` on a match when `obs` implements it |
| `TestObserveErrorResponseFor_PlainObserver_NoPanic` (`api/rest`, `api/events`, `api/reqreply`) | An `Observer` implementing NEITHER `ErrorPatternObserver` NOR `SpanTagger` causes no panic — both type-assertion guards live inside `ObserveErrorResponseFor`, adapters never need their own guard |
| `TestDeadLetterFor_UndeclaredHandle_ReturnsFalse` (`api/events`, `api/reqreply`) | `DeadLetterFor` returns `ok=false` (and does nothing else) when no `DeadLetter` is declared globally or per-channel/route |
| `TestDeadLetterFor_ReportsObserver_AndReturnsEncodedEnvelope` (`api/events`, `api/reqreply`) | `DeadLetterFor` reports `stats.ReportErrors(obs, "dead_letter", err)` and returns the correct topic + already-encoded envelope bytes when a `DeadLetter` IS declared |
| `Test<FailurePoint>_ErrorPatternMatched_RespondsTyped` (one per Category-A row per API — e.g. `TestBodyDecodeError_ErrorPatternMatched`, `TestSecurityMiddlewareError_ErrorPatternMatched`, `TestMiddlewareEncodeOutError_ErrorPatternMatched`, `TestResponseEncodeError_ErrorPatternMatched`) | A failure at THIS row matching a declared `ErrorPattern`/`ErrorChannel` produces the TYPED response — mirrors the already-shipped handler/middleware-Fn tests, applied to every remaining Category-A row |
| `Test<FailurePoint>_NoErrorPattern_FallsBackUnchanged` (same rows, same packages) | Same failure point with NO matching pattern still produces today's UNCHANGED fixed shape — regression guard for every existing caller, one per row |
| `TestErrorPattern_MatchesCodexErrorType` (`api/rest`) | A declared `rest.ErrorPattern[codex.ValidationErrors, Body](...)` matches a REAL body-decode failure once Category A's body-decode row is wired — proves Category B's "zero new codex awareness needed" claim end-to-end |
| `TestErrorPatternAs_Matches`/`_NoMatch` (`adapters/nethttp`, `adapters/mqtt5`, `adapters/zeromq`) | Topic 6 Alternative 1: the generic helper correctly extracts the typed payload on a match, returns `ok=false` on any non-`ErrorPatternResponse` error or a `Value` of the wrong concrete type |
| `TestErrorPatternOpt_Match_Matches`/`_NoMatch` (`api/rest`, `api/reqreply`) | Topic 6 Alternative 2: `.Match` correctly delegates to Alternative 1's decode logic, scoped to the declaration's own `B` type |
| `TestHandleErrorPattern_DispatchesFirstMatchingCase`/`_NoCaseMatches`/`_NotAnErrorPattern` (`adapters/nethttp`, `adapters/mqtt5`, `adapters/zeromq`) | Topic 6 Alternative 3: `HandleErrorPattern`/`Case` dispatch to the first matching case's `T`, return `false` when no case matches or `err` isn't an `ErrorPatternResponse` at all |
| `TestPublish_ClientMiddlewareFnError_NeverConsultsErrorChannel` (`adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt`) | Topic 7 regression guard: confirms the publisher/client role EXCLUSION stays correct — a publish-side middleware Fn error is returned directly, never checked against a declared `ErrorChannel`, even when one exists on the same channel |
| `TestSubscribeError_As_ExtractsUnderlyingErrorType` (`adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt`) | Topic 7: `SubscribeError.As(&target)` correctly delegates to `errors.As` on the wrapped `Err`, mirroring the standard library's own `As` semantics |
| `TestDeadLetter_GlobalDefault_Inherited`/`_OverriddenPerRoute`/`_OptedOut` (`api/events`, `api/reqreply`) | Topic 4: builder-level `DeadLetter` default is inherited when a route/channel declares none, overridden when one is declared explicitly, and can be opted out of via an explicit empty override (mirrors `GlobalSecurity`/`Security`'s precedent) |
| `TestDeadLetter_FailedPublish_Published` (`adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt`) | Topic 4: a publish-side failure (never reached the broker) is ALSO published to the declared dead-letter topic, alongside the synchronous error the caller receives from `Publish(...)` |
| `TestRouteHandle_HasErrorPatterns`/`TestChannelHandle_HasErrorPatterns` (`api/rest`, `api/events`, `api/reqreply`) | Topic 5: the new accessor correctly reports whether at least one `ErrorPattern`/`ErrorChannel` is declared, gating `RecordErrorPatternMiss` |
| `TestSpanTagger_TagSpan_CalledOnMatch`/`TestObserver_NotImplementingSpanTagger_NoPanic` (`api/rest`, `api/events`, `api/reqreply`) | Topic 5: the new `stats.SpanTagger` extension is called when implemented, and the type-assertion guard prevents a panic when it is not |

## Files to create/modify (sketch)

| File | Responsibility |
|---|---|
| `api/rest/builder.go` | Topic 1's reopened fix: new `DuplicateErrorStatusError` type + `Register`/`RegisterHandle`-time rejection of 2+ same-status `ErrorPattern`s (mirrors reqreply's `DuplicateErrorPatternCodeError`) |
| `api/rest/builder_test.go` | Update `TestDecodeErrorFor_FirstMatchWins_SameStatus`'s expected outcome to the new rejection behavior |
| `docs/features/rest-api.md` | Update the "same-status precedence" callout — now describes a hard rejection, not an accepted ambiguity |
| `render/asyncapi/v3/document.go` | `Operation.Messages []Message` field; `buildChannelsAndOperations` updated to collect from `Messages` when non-empty |
| `api/reqreply/builder.go` | Replace the per-error-type separate-channel loop with appending to one reply channel's `Messages` slice |
| `api/events/dead_letter.go` (new) | `DeadLetter`/`DeadLetterOpt` (`WithCode`/`WithDescription`/`WithSchemaName`/`WithChannelAddress`/`WithOperationID`, mirroring `reqreply.ErrorPatternOpt`)/`DeadLetterEnvelope`; `Builder`-level global default + per-channel override/opt-out (mirrors `GlobalSecurity`/`Security`); NEW core-layer `ChannelHandle.DeadLetterFor(ctx, obs, sourceTopic, rawPayload, err) (topic string, body []byte, ok bool)` — builds the envelope + reports `"dead_letter"` internally, so adapters only supply the raw payload they already have and publish the returned bytes |
| `api/reqreply/dead_letter.go` (new) | Same, `RouteOpt` variant + `Server`-level global default + `RouteHandle.DeadLetterFor` mirroring `ChannelHandle.DeadLetterFor` exactly |
| `adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt` | Per-adapter dispatch wiring for DeadLetter's two-tier fallback (Tier 1: decode-class, unconditional; Tier 2: business-error-class, only after `ObserveErrorResponseFor` misses) — events subscribe dispatch, events publish dispatch (failed-publish case), and reqreply serve dispatch. Thin per the core-layer consolidation: each site is `if topic, body, ok := handle.DeadLetterFor(ctx, obs, handle.Topic, msg.Payload, err); ok { client.Publish(ctx, topic, body) }` — zero envelope/encode/observer logic remains adapter-side |
| `docs/guides/asyncapi.md` | Update "Declaring dedicated req/reply error channels" section for the new multi-message rendering |
| `docs/guides/error-handling.md` | New "Dead-letter queue" section, cross-linking `stream.MapErr`/Handler Disposition as related-but-distinct; new "Observing declared error patterns" section for Topic 5, documenting `ObserveErrorResponseFor`/`DeadLetterFor` as the recommended single call sites |
| `stats/observer.go` | New `ErrorPatternObserver` interface; new, SEPARATE `SpanTagger` interface (`TagSpan(ctx, key, value string)` — additive, does NOT modify the existing `TraceObserver` interface); `LoggingObserver`/`fanout`/`NoopObserver` implementations for both new interfaces |
| `api/rest/builder.go`, `api/events/builder.go`, `api/reqreply/route.go` | New `HasErrorPatterns()` accessor on `RouteHandle`/`ChannelHandle` (one-line `len(h.errorPatternRules) > 0` wrapper, called internally by `ObserveErrorResponseFor` — not adapter-facing); NEW core-layer `ObserveErrorResponseFor(ctx, obs, err) (resp, matched, applyErr)` wrapping `ErrorResponseFor` + `ErrorPatternObserver`/`SpanTagger` internally |
| `adapters/nethttp/serve.go`, `adapters/chi/serve.go` | Topic 1's fix (FULL enumeration, ~11 call sites): body decode, path/query/cookie/header param validation, middleware `DecodeIn`, security middleware Fn, response merge-field encode, middleware `EncodeOut`, response body encode — each gains an `ObserveErrorResponseFor` call (replacing a bare `ErrorResponseFor` call) before its existing fixed-shape fallback, UNCHANGED when unmatched; Topic 5's match/miss/span-tag observability comes along for free, no separate adapter-side wiring needed |
| `adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt`'s `adapter.go`/`binding.go` | Same full-enumeration fix, SUBSCRIBE side only (Topic 7 confirms publish side correctly stays excluded): payload decode, topic/property-var merge, middleware `DecodeIn`, security middleware Fn — each calls `ObserveErrorResponseFor` |
| `adapters/mqtt5`, `adapters/zeromq`'s `reqreply_transport.go` | Same: request decode, topic/property-var merge, middleware `DecodeIn`/`EncodeOut`, security middleware Fn (server-side dispatch only — client-side stays excluded, same Topic 7 reasoning) — each calls `ObserveErrorResponseFor` |
| `docs/guides/observer.md` | New rows for `RecordErrorPatternMatch`/`RecordErrorPatternMiss`/`SpanTagger.TagSpan`/`"dead_letter"` in the observer location-value reference table |
| `adapters/mqtt5`, `adapters/zeromq`, `adapters/mqtt`'s `errors.go` | Topic 7: `SubscribeError.As(target any) bool` convenience method |
| `adapters/nethttp`, `adapters/mqtt5`, `adapters/zeromq` | Topic 6: `ErrorPatternAs[B any](err error) (B, bool)` (Alternative 1); `errorCase`/`typedCase[T]`/`Case[T]`/`HandleErrorPattern` (Alternative 3) |
| `api/rest/builder.go`, `api/reqreply/route.go` | Topic 6: `ErrorPatternOpt[E, B].Match(err error) (B, bool)` (Alternative 2, thin wrapper over Alternative 1) |
| `.github/instructions/go-codex.instructions.md` | `api/rest`/`api/events`/`api/reqreply`/`render/asyncapi/v3`/`stats` rows updated |

## Out of scope (this round)

- Handler Disposition (ack/nack/requeue) — tracked separately in
  `docs/roadmap/protocol-native-features.md`, cross-referenced only.
- Extending dead-lettering to matched-but-failed-to-encode cases.
- `api/mcp`/`adapters/websocket` error mechanisms.
- Changing REST's OpenAPI rendering (no equivalent gap exists).

## Graduation note

Once implemented and verified, this document is expected to graduate to
`docs/design/d-0005-error-handling.md` per the established policy (this
is a cross-cutting pattern spanning 3 APIs plus the shared AsyncAPI
renderer — clears the "bigger architecture rework" bar). `docs/roadmap/
reqreply-error-pattern-client-decode.md` (Phase 0's original source) has
ALREADY been deleted (its content was folded into this document's Phase
0 section when this document was first drafted, and its roadmap
index/nav entries were removed at the same time) — no further action
needed on that front at graduation time.
