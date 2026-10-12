# `api/reqreply` Forward/Bounce — Design Complete, Not Yet Implemented

> **Status:** Design complete — API surface, wire signal, structured
> errors, and an 8-combination dispatch-path inventory are all settled.
> **Blocked on [the registry doc](reqreply-client-registry.md) (Phase 0)
> shipping first** — this doc's client-side resolution step is the
> registry's first real consumer, and implementing Forward against a
> not-yet-proven registry would risk reworking both at once. An in-depth
> review pass closed 5 further findings beyond the original design: a
> new `ForwardSecuritySchemeMismatchError` (going further than REST's own
> precedent), Observer-per-hop made explicit, client-side failure
> surfacing made explicit (direct/unwrapped, no retry, no `DeadLetter`
> role), a Disposition/ack-interaction note flagged for a future AMQP
> round, and `ForwardTargetVarError`'s wrapping pattern verified
> consistent with `rest.Redirect`'s own precedent. Implementation is a
> distinct, future step requiring its own explicit go-ahead (the unit
> test plan, the 8-combination dispatch-path inventory executed one by
> one). [← Back to Roadmap](index.md)

## Relationship to the other 2 reqreply roadmap docs

This doc was split out of a single combined
`reqreply-client-registry.md` doc once all 3 phases reached "design
complete." The 3 docs are sequenced, not independent:

1. **[`reqreply-client-registry.md`](reqreply-client-registry.md)**
   (Phase 0) — registry infrastructure. Must ship FIRST; this doc's
   client-side resolution step is the registry's first real consumer.
2. **This doc** (Phase 1) — forward/bounce. The highest-risk, highest-
   complexity phase (new wire protocol in 2 adapters, 8-combination
   dispatch-path inventory, new security validation, `CallAsync`/
   `Future` chaining).
3. **[`reqreply-dead-letter-replay.md`](reqreply-dead-letter-replay.md)**
   (Phase 2) — dead-letter replay. Depends on THIS doc's low-level
   republish-and-decode primitive existing first; needs zero new wire
   mechanism of its own.

## Why this exists

A handler can respond "actually, route X should handle this" — pub/sub
has no broker-level "redirect" the way HTTP does, so the design has the
SERVER signal "ask topic X instead" within the SAME correlation-ID round
trip, and the CLIENT does the actual re-publish (confirmed direction —
NOT a server-to-server relay). This was confirmed, during the combined
doc's design discussion, as the REAL driver for giving
`api/reqreply.Client` a registry at all (see the registry doc's "Why
this exists"/"Candidate motivations" sections for the full history of
how this was identified) — Forward is motivation 1 of 3 originally
considered, the one that turned out to need the registry as its own
required infrastructure, not the other way around.

## Wire-level signal — mirrors the EXISTING `ErrorPattern` discriminator precedent exactly, no new envelope type needed

- **`adapters/mqtt5`** already distinguishes "this reply is not a normal
  `Resp`" via a reserved `ContentType` (`"application/mqtt5-error"`,
  confirmed at `adapters/mqtt5/reqreply.go`'s `isErrorReply`) + a reserved
  User Property (`"x-error-code"`, `errorCodeFromUserProperties`) for a
  matched `ErrorPattern`. Forward mirrors this with its OWN reserved
  `ContentType` (e.g. `"application/mqtt5-forward"`) + a reserved User
  Property carrying the target's already-`BuildTopic`-resolved CONCRETE
  topic.
- **`adapters/zeromq`** already uses MULTI-FRAME replies (`[status,
  payload]` normally; `[status, code, body]` for a matched `ErrorPattern`,
  confirmed at `adapters/zeromq/reqreply_transport.go`). Forward adds a
  THIRD `frames[0]` status value (e.g. `"forward"`) + `frames[1]` = the
  target topic.
- **Dispatch ordering**: checked BEFORE the existing error-reply check,
  on BOTH the server-publish side (confirmed real insertion point:
  `adapters/mqtt5/reqreply_transport.go`'s `if handlerErr != nil` branch,
  ~line 764, which currently runs `publishHandlerErrorReplyReflect` +
  `tryDeadLetterReflect`) and the client-recognize side (confirmed real
  insertion point: the SAME file's `case replyMsg := <-replyCh:` block,
  which currently checks `isErrorReply` first) — mirrors `rest.Redirect`'s
  own documented "recognized BEFORE `ErrorPattern`/`ErrorStatus`
  matching" precedence. A Forward is a deliberate instruction, not a
  failure — it skips `ErrorPattern`/`DeadLetter` entirely.

## API surface

Reqreply has no HTTP-style "redirect converts to a bodyless GET" escape
hatch — every `Call` always carries a request body. Confirmed design
choice: the target's `Req`/`Resp` types are BOTH free to differ from the
originating route's (NOT constrained the way 307/308 constrains HTTP's
method/body preservation) — the handler constructs and supplies a brand
NEW request value explicitly when forwarding:

```go
// api/reqreply — new function, returned as a handler's error value
// exactly like rest.Redirect. No "from" parameter needed (unlike
// rest.Redirect, which needs it ONLY to validate 307/308's method-
// preservation constraint — moot here, since Req/Resp are free to
// differ).
func Forward[TargetReq, TargetResp any](
    target Route[TargetReq, TargetResp],
    newReq TargetReq,
    vars map[string]string,
    opts ...ClientHandleOpt,
) error
```

Builds `target`'s concrete topic via `BuildTopic(vars)` (reusing the
SAME mechanism `rest.Redirect` uses for path vars), encodes `newReq` via
`target`'s OWN request codec, producing the returned sentinel:

```go
type ForwardError struct {
    TargetTopic string // already BuildTopic-resolved
    Payload     []byte // newReq, already encoded via target's own codec
}
```

The reply body legitimately carries `Payload` (unlike an initial "body
stays empty" read, which only would have held if Req types had been
constrained to match) — the reserved `ContentType`/User-Property (mqtt5)
or `frames[0]="forward"` (zeromq) marks the reply as a forward and
carries `TargetTopic`; the body IS `Payload`.

## Client-side resolution

On recognizing a forward reply, the client's own dispatch (mirrors
`rest.Client`'s `followRedirect`/`followRedirectHop`) builds a NEW
correlation ID, publishes `Payload` to `TargetTopic` AS-IS (no
re-encoding — already encoded server-side), awaits THAT reply, and
decodes it by resolving `TargetTopic` against
**[Phase 0's registry](reqreply-client-registry.md)** to recover the
registered `*RouteHandle[TargetReq,TargetResp]` — the client has only a
topic STRING and raw bytes at this point, no static `TargetResp` type
information, so this registry lookup is NOT optional. Since
`TargetTopic` arrives ALREADY CONCRETE (resolved server-side via
`BuildTopic`, not a `{var}`-templated key the client must pattern-match),
the lookup is a plain exact-string map lookup — simpler than REST's own
`MatchNonWildcard`-based ambiguous-match resolution.

**Type-safety note, not a NEW problem**: `Client.Call`/`CallAsync` already
return `(any, error)` (confirmed:
`examples/reqreply-api/demo_basic_call_and_serve.go`'s
`resp := respAny.(routes.ComputeResp)`) — a caller already performs its
own type assertion after the fact. A forward resolving to a DIFFERENT
`Resp` type is the SAME "caller must already know what shape to expect"
contract `rest.Client.Call`'s own redirect auto-follow already relies on
— not a gap this decision introduces.

**Client-side follow-up failure handling — explicit.** If the client's
own follow-up publish itself fails — timeout, `UnrecognizedForwardError`,
`ForwardSecuritySchemeMismatchError` (below), `DuplicateTopicTypeMismatchError`
([Phase 0](reqreply-client-registry.md)), or exceeding `MaxForwards` —
the ORIGINATING `Client.Call`/`CallAsync` returns THAT error directly,
unwrapped, to the caller, as an ordinary Go error. `DeadLetter`, being a
SERVER-side-only mechanism, has NO role here at all — there is no
"server" in this client-side failure path to dead-letter anything. No
retry, no composite wrapper type.

**Observer integration — every hop observed.** EVERY hop (the original
call AND each subsequent forward republish) calls
`obs.RecordRequest(topic, ..., duration)` exactly as an ordinary `Call`
already does — a forward hop is observed AS IF it were its own `Call`,
using the SAME `Observer` instance the originating call resolved. No new
Observer extension/interface needed — a dispatch-wiring fix (call it at
each hop), not a new capability. **Separately flagged, NOT fixed here**:
`adapters/nethttp`'s ALREADY-SHIPPED redirect-following code
(`redirect.go`/`redirect_sse.go`) was found, while researching this
precedent, to have ZERO `stats.Observer` calls anywhere — every REST
follow-up hop is completely unobserved today. This is a genuine,
pre-existing gap in the shipped feature, tracked as its own separate
follow-up item (candidate for a future `deep-dive-bug-hunt` round on
`api/rest`), explicitly NOT addressed by this reqreply design.

**Handler Disposition / message-acknowledgement interaction — flagged for
a future round, not designed further now.** Low priority today
(confirmed: `mqtt5`/`zeromq`'s Disposition resolves to a no-op default,
no adapter has real ack/nack protocol yet). A `Forward` should be treated
as "this server handled the inbound message" (an implicit ack-equivalent)
for whichever adapter eventually has real ack semantics (AMQP) — noted
now so that future AMQP-adapter round doesn't have to rediscover this
interaction from scratch.

## Security-scheme mismatch check — a GENUINE DIVERGENCE from REST's own more permissive behavior

`docs/features/rest-redirects.md` documents that a security-scheme
mismatch across a redirect hop "is never silently bypassed or
fabricated — it surfaces as the target's own ordinary rejection," i.e.
REST discovers a mismatch only AFTER sending, over the wire. reqreply
goes further: because Forward's target can be an ENTIRELY DIFFERENT
route in an entirely different trust/feature domain (the whole point of
this doc's "free `Req`/`Resp`" decision), a genuine cross-domain scheme
mismatch is MORE LIKELY here than it ever was for REST's own redirect
targets (which in practice stay within the same resource).

Before republishing, the client — which already holds BOTH the
ORIGINATING route's `RouteHandle` (being dispatched) and the TARGET's
`RouteHandle` (just resolved from
[Phase 0's registry](reqreply-client-registry.md)) — compares the two
declared security requirements' SCHEME NAME(S). A match (or the target
requiring no security at all) proceeds exactly as today (credential
reuse, verbatim). A mismatch FAILS FAST, client-side, BEFORE the network
round-trip, with a NEW `ForwardSecuritySchemeMismatchError
{OriginatingScheme, TargetScheme}` — instead of letting it go over the
wire and surface as a confusing, generic rejection from the target. This
is scheme-NAME comparison only, never a value/credential comparison —
the client still never inspects or re-derives the actual credential from
the target, it only confirms the SHAPE of what it's about to reuse is
declared-compatible; a declared-compatible scheme whose ACTUAL credential
the target still rejects (e.g. an expired token) still surfaces as an
ordinary target-side rejection, same as REST — this check only catches
the STRUCTURAL mismatch class.

## Chain-depth cap

Direct port of `MaxRedirects`'s mechanics (recursive-call counter,
default 10) — reqreply's own option name TBD at implementation time
(`ClientCallOptions.MaxForwards`, most likely).

## Structured errors (mirrors REST's naming 1:1, "Redirect"→"Forward", PLUS 1 reqreply-specific addition found during review)

`ForwardError{TargetTopic, Payload}` (above), `ForwardTargetVarError{Topic,
Err}` (missing/invalid `vars` for the target's topic template),
`UnrecognizedForwardError{TargetTopic}` (client has no registered route
for the signaled topic), `ForwardChainTooDeepError{TargetTopic, Depth}`.
No `RedirectMethodMismatchError`/`RedirectToStreamUnsupportedError`/
`RedirectTargetNotStreamableError` equivalents needed — those existed for
REST's method-preservation constraint (moot here) and SSE-vs-Call
target-shape mismatch (reqreply has no SSE/streaming route concept).

**Reqreply-specific addition (no REST equivalent — found during this
design's in-depth review):**

- `ForwardSecuritySchemeMismatchError{OriginatingScheme, TargetScheme}` —
  the client-side, fail-fast security-scheme check REST doesn't have
  (see above).

(A second reqreply-specific addition, `DuplicateTopicTypeMismatchError`,
belongs to [Phase 0's registry](reqreply-client-registry.md) — this
doc's forward-resolution step is one of its 2 consumers, surfacing that
error lazily when a poisoned registry entry is resolved.)

**Verified consistent with REST precedent, not a gap**:
`ForwardTargetVarError` wraps `BuildTopic`'s already-typed error
(`RouteParamError`/`MissingRouteParamError`/`InvalidTopicError`) the SAME
way `rest.Redirect`'s own `buildRedirect` wraps `BuildPath`'s already-typed
error (`PathParamError`/`MissingPathVarError`/`InvalidPathError`) inside
`RedirectTargetVarError` — checked during the in-depth review and
confirmed NOT redundant over-wrapping, so this specific question doesn't
need re-investigating in a future review.

## `CallAsync`/`Future` — CONFIRMED in scope from v1 (not deferred)

The forward-following logic (recognize forward reply → new correlation
ID → republish → await second reply → decode) must run inside whatever
internal mechanism currently resolves a `*Future[Resp]` when its
ORIGINAL reply arrives — it must resolve the SAME `Future` object via the
SECOND round trip's reply, not construct a new one. Needs each adapter's
own "pending calls by correlation ID" bookkeeping traced at
implementation time to confirm this chaining is mechanically sound.

## Dispatch-path inventory — bigger than it first looks

The `deep-dive-bug-hunt` Round 201 lesson (`.github/skills/
deep-dive-bug-hunt/references/../review-go-codex/references/history.md`
— the Typed HTTP Redirects bug hunt) means EVERY dispatch path needs
independent verification, not just the obvious primary one. Because
`CallAsync` is ALSO in scope (previous section), the real inventory is a
cross product:

| | `Client`-level (`ClientTransport.Call`/`CallAsync`) | `ports.Pattern` binding primitive |
|---|---|---|
| `adapters/mqtt5` | needs forward support | needs forward support (separately) |
| `adapters/zeromq` | needs forward support | needs forward support (separately) |

— 2 adapters × 2 call shapes (`Call`/`CallAsync`) × 2 dispatch layers
(`Client`-level vs. `ports.Pattern` binding, confirmed present at
`adapters/mqtt5/binding.go`/`adapters/zeromq/binding.go`) = **up to 8
independent dispatch combinations**, each needing its OWN trace + test at
implementation time, per Round 201's own hard-won lesson ("a fix applied
to ONE of these does NOT imply the others got it").

A smaller, related lesson from the SAME round: a doc comment claiming two
separate implementations are "behaviorally-equivalent" (as
`adapters/nethttp`'s own `consumeSSE` doc comment claimed, about
`Client.Consume`'s reconnect loop — a claim that did NOT hold for
redirects) is a CLAIM TO VERIFY via a live, failing test — not to trust.
This design's own eventual unit test plan must carry the same explicit
verification step for each of the 8 combinations above, not rely on "it
probably works the same way."

## Next step

Forward/bounce has a SETTLED design, further hardened by an in-depth
review pass — none of it is implemented yet, and it is explicitly
BLOCKED on [the registry doc](reqreply-client-registry.md) (Phase 0)
shipping first. Once Phase 0 ships, turning this into an actual
implementation plan (unit test plan, the 8-combination dispatch-path
inventory executed one by one) is a distinct, future step requiring its
own explicit go-ahead — the same rigor Typed HTTP Redirects itself went
through across its own critical-review rounds before implementation
began. No code should be written against this design until that
implementation plan exists and is approved.
