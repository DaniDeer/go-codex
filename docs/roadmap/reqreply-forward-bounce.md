# `api/reqreply` Forward/Bounce — Design Complete, Not Yet Implemented

> **Status:** Design complete — API surface, wire signal, structured
> errors, and a dispatch-path inventory are all settled, NOW GROUNDED
> AGAINST THE REAL, SHIPPED PHASE 0 CODE (registry + `CallRaw`,
> committed `e86bc99`) rather than assumptions, across 4 review rounds:
>
> 1. An in-depth review pass closed 5 findings: a new
>    `ForwardSecuritySchemeMismatchError` (going further than REST's own
>    precedent), Observer-per-hop made explicit, client-side failure
>    surfacing made explicit, a Disposition/ack-interaction note flagged
>    for a future AMQP round, and `ForwardTargetVarError`'s wrapping
>    pattern verified consistent with `rest.Redirect`'s precedent.
> 2. A post-split re-review closed 2 findings: resolution CENTRALIZED in
>    `api/reqreply.Client` via `ClientTransport.CallRaw` (adapters stay
>    registry-unaware), and the dispatch-path inventory corrected after
>    confirming `ports.Pattern`'s `Call`-equivalent is registry-free and
>    no `CallAsync`-equivalent `ports.Pattern` binding exists.
> 3. A third re-review closed 1 gap: "Client-side resolution" became an
>    explicit chain-following LOOP (mirrors `followRedirectHop`'s
>    `remaining`-counter shape) instead of a single hop, to detect
>    CHAINED forwards.
> 4. **A FOURTH re-review, conducted AFTER Phase 0 actually shipped**,
>    traced this doc's own remaining open items against the real code
>    for the first time and found 2 more: (a) the wire-signal
>    dispatch-path count was undercounted — 9 real insertion points (6
>    client-recognize + 3 server-publish), not the "1 per side" earlier
>    revisions implied, now that `CallRaw` is real, separate code with
>    its own reply-classification block; (b) `CallAsync`'s own
>    chain-following design, previously flagged as "needs tracing at
>    implementation time," was traced and found STRUCTURALLY BROKEN as
>    written (the adapter's own background goroutine resolves the Future
>    before `api/reqreply.Client` ever sees the result) — RESOLVED by
>    restructuring `Client.CallAsync` to wrap `Client.Call` directly and
>    REMOVING `CallAsync` from `ClientTransport` entirely (consistent
>    with this project's general preference for hard logic in `api/*`,
>    thin adapters) — see "`CallAsync`/`Future`" below for the full
>    resolution.
>
> **Blocked on [the registry doc](reqreply-client-registry.md) (Phase 0)
> shipping first** — ALREADY SATISFIED (Phase 0 shipped, commit
> `e86bc99`). Implementation is a distinct, future step requiring its
> own explicit go-ahead (the unit test plan, the 6 wire-signal
> recognition points executed one by one). [← Back to Roadmap](index.md)

## Relationship to the other 2 reqreply roadmap docs

This doc was split out of a single combined
`reqreply-client-registry.md` doc once all 3 phases reached "design
complete." The 3 docs are sequenced, not independent:

1. **[`reqreply-client-registry.md`](reqreply-client-registry.md)**
   (Phase 0) — registry infrastructure. Must ship FIRST; this doc's
   client-side resolution step is the registry's first real consumer.
2. **This doc** (Phase 1) — forward/bounce. The highest-risk, highest-
   complexity phase (new wire protocol across 3 adapter-transports, a
   9-point wire-signal dispatch inventory, new security validation, a
   `ClientTransport.CallAsync` removal + `Client.CallAsync` restructure).
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
  on BOTH the server-publish side and the client-recognize side —
  mirrors `rest.Redirect`'s own documented "recognized BEFORE
  `ErrorPattern`/`ErrorStatus` matching" precedence. A Forward is a
  deliberate instruction, not a failure — it skips `ErrorPattern`/
  `DeadLetter` entirely.
- **Insertion-point count — CORRECTED against the REAL, now-shipped
  Phase 0 code** (a prior revision of this doc, written before Phase 0
  existed, understated this to "one insertion point per side"; see
  "Dispatch-path inventory" below for the full, re-grounded count):
  **9 total insertion points, not 2.** Server-publish side: 3
  independent, non-delegating dispatch implementations each need their
  OWN `handlerErr != nil`-equivalent check
  (`adapters/mqtt5/reqreply_transport.go`'s `serverTransport.Serve`,
  confirmed still accurate at its `if handlerErr != nil` branch;
  `adapters/zeromq/reqreply_transport.go`'s `serverTransport.Serve` AND
  `routerServerTransport.Serve` — TWO separate server implementations
  the original wording never mentioned). Client-recognize side: 6
  independent reply-classification blocks, confirmed via direct
  inspection of the shipped code — `call()` and `CallRaw()` EACH have
  their OWN separate `case replyMsg := <-replyCh:`/`frames[0]=="error"`-
  style block, in EACH of mqtt5's `clientTransport`, zeromq's
  `clientTransport`, and zeromq's `dealerClientTransport` (3 × 2 = 6) —
  `CallRaw` didn't exist when this doc's wire-signal section was
  originally written, so its own separate reply-classification code
  (added by Phase 0) was never accounted for here.
- **Implementation burden stays small despite the 9-point count**: mqtt5
  already factors its error-reply check into ONE shared helper
  (`isErrorReply`) called from BOTH `call()` and `CallRaw()` — the SAME
  pattern should apply to Forward recognition (a new shared
  `isForwardReply`-style helper PER adapter-transport, called from both
  dispatch methods) — 3 shared helper functions total, not 6 independent
  reimplementations. The VERIFICATION burden (each of the 9 points needs
  its own trace + test, per `deep-dive-bug-hunt`'s own "N-way parity,
  not just 2" principle) stays real regardless of how much code is
  shared.

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

**CENTRALIZED in `api/reqreply.Client` — NOT resolved/republished inside
the adapter, a deliberate divergence from REST's own shipped mechanism
(see [Phase 0's "Adapter↔registry bridge"](reqreply-client-registry.md#adapterregistry-bridge--centralized-in-apireqreplyclient-not-mirroring-rests-adapter-side-resolution-found--resolved-during-review)
for the full rationale).** An adapter's `Call` (the ONLY remaining
adapter-level client entry point — see "`CallAsync`/`Future`" below for
why `CallAsync` was REMOVED from `ClientTransport` entirely) recognizes
the wire-level forward signal (above) but does NOT itself build a new
correlation ID, republish, or decode anything — it simply returns the
SAME `ForwardError{TargetTopic, Payload}` sentinel through its ORDINARY
return path, exactly as if a handler had returned it directly.

`api/reqreply.Client.Call` (the API-layer method, which owns
[Phase 0's registry](reqreply-client-registry.md)) catches that sentinel
and does the actual follow-up itself via an explicit CHAIN-FOLLOWING
LOOP (found missing from the first draft of this section during a
further re-review — see
[Phase 0's `CallRaw` contract](reqreply-client-registry.md) for why a
single hop isn't enough): resolve `TargetTopic` against the registry to
recover the registered `*RouteHandle[TargetReq,TargetResp]` — the client
has only a topic STRING and raw bytes at this point, no static
`TargetResp` type information, so this registry lookup is NOT optional —
then call the SAME attached `ClientTransport`'s `CallRaw(ctx,
TargetTopic, Payload)` method to publish `Payload` AS-IS (no re-encoding
— already encoded server-side) and await THAT reply.

`CallRaw` itself classifies that reply the SAME 3-way way an ordinary
`Call` would (see Phase 0's doc): an ordinary successful reply decodes
immediately via the resolved `RouteHandle`'s own `Resp` codec — DONE. A
reply that is ITSELF tagged as a forward returns ANOTHER `ForwardError`
through `CallRaw`'s own `error` return — the loop resolves the NEW
`TargetTopic` against the registry again and calls `CallRaw` again,
decrementing a `MaxForwards` counter each iteration (mirrors
`adapters/nethttp/redirect.go`'s own `followRedirectHop`'s `remaining`-
counter shape exactly), returning `ForwardChainTooDeepError` once
exhausted. A reply that matched a declared `ErrorPattern` surfaces as
`CallRaw`'s `ForwardRawErrorReply{Code, Body}` (SHIPPED — see [Phase 0's
`CallRaw` contract](reqreply-client-registry.md) for the extraction-
vs-decode split rationale) — the loop, already holding the resolved
target `*RouteHandle` from the registry lookup above, finishes the
decode itself via `targetHandle.DecodeErrorFor(Code, Body)`, producing
the SAME typed `ErrorPatternResponse` an ordinary (non-forwarded) `Call`
would — see "Client-side follow-up failure handling" below.

Since `TargetTopic` arrives ALREADY CONCRETE (resolved server-side via
`BuildTopic`, not a `{var}`-templated key the client must pattern-match),
each lookup is a plain exact-string map lookup — simpler than REST's own
`MatchNonWildcard`-based ambiguous-match resolution. Both adapters
(`mqtt5`, `zeromq`) implement ONLY `CallRaw`'s single-hop, 3-way-
classifying mechanics — neither needs its own copy of the chain-
following LOOP or the resolve+decode logic, both of which live once, in
`api/reqreply.Client`.

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

**Observer integration — every hop observed, ALREADY FREE, confirmed —
zero additional code, not merely "a dispatch-wiring fix."** Checked
precisely against the shipped `ClientCallOptions` struct: it has NO
`Observer` override field (unlike REST's `ClientCallOptions.Observer`) —
reqreply's `obs` is ALWAYS resolved via the attached `ClientTransport`'s
own construction-time `t.opts.Observer`, falling back to `ctx`. Since the
chain-following loop calls `CallRaw` on the EXACT SAME attached
`ClientTransport` instance the originating `Call` used, EVERY hop
automatically resolves the IDENTICAL `obs` — and `CallRaw` ALREADY calls
`obs.RecordRequest` on its own (Round 203's fix, shipped). So EVERY hop
(the original call AND each subsequent forward republish) is observed
AS IF it were its own `Call`, using the SAME `Observer` instance, with
NOTHING left to wire in `api/reqreply.Client` and NO new adapter code —
confirmed during a later thin-adapter review round, this is good news
for the "thin adapter" goal (one less thing to build), not a remaining
TODO. **Separately flagged, NOT fixed here**:
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

Before republishing, **`api/reqreply.Client` — never the adapter** (the
chain-following loop, which already holds BOTH the ORIGINATING route's
`RouteHandle` (being dispatched) and the TARGET's `RouteHandle` (just
resolved from [Phase 0's registry](reqreply-client-registry.md)) —
compares the two declared security requirements' SCHEME NAME(S). A match (or the target
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

## `CallAsync`/`Future` — CONFIRMED in scope from v1; design CORRECTED after tracing the real, shipped Phase 0 code

The forward-following CHAIN-FOLLOWING LOOP (recognize forward reply →
new correlation ID → `CallRaw` republish → classify the reply → repeat
if chained → decode, CENTRALIZED in `api/reqreply.Client` — see
"Client-side resolution" above) must resolve the SAME `Future` object
via the FINAL round trip's reply, not construct a new one.

**A prior revision of this section claimed "CallAsync's own background
goroutine... simply runs the SAME chain-following loop itself," flagging
only that this "needs tracing at implementation time." Traced against
the REAL, shipped Phase 0 code — it is NOT mechanically sound.**
Confirmed identical in all 3 implementations
(`adapters/mqtt5/reqreply_transport.go`'s `clientTransport.CallAsync`,
`adapters/zeromq/reqreply_transport.go`'s `clientTransport.CallAsync`
and `dealerClientTransport.CallAsync`):

```go
future, resolve := ff.NewFutureAny()
go func() {
    resp, err := t.call(ctx, routeAny, reqAny, o)
    resolve(resp, err)
}()
return future, nil
```

This goroutine lives ENTIRELY INSIDE THE ADAPTER — `api/reqreply.
Client.CallAsync` is a 1-line `return t.CallAsync(ctx, route, req,
opts...)` that returns the Future IMMEDIATELY, never itself seeing
`resp`/`err`. By the time anything outside the adapter could inspect
the result, `resolve(resp, err)` has already fired. If `t.call(...)`
returns `(nil, ForwardError{...})`, the Future resolves with that
UNRESOLVED error — there is no point at which `api/reqreply.Client`
(which owns the registry the chain-following loop needs) can intercept
it. The centralized design works for the SYNCHRONOUS `Call` path
(`Client.Call` directly receives `(resp, err)` from `t.Call(...)` and
can inspect/loop freely) but does NOT transfer to `CallAsync` as
originally structured.

**RESOLVED — `Client.CallAsync` is restructured; `CallAsync` is REMOVED
from `ClientTransport` entirely.** Consistent with this project's
general preference (confirmed during this review) for implementing hard
parts in the `api/*` layer and keeping adapters as thin as possible:

```go
// api/reqreply/client.go — Client.CallAsync no longer delegates to
// ClientTransport.CallAsync at all.
func (c *Client) CallAsync(ctx context.Context, route any, req any, opts ...ClientCallOptions) (any, error) {
    c.registerAutoFromCall(route)
    ff, ok := /* recover reqreply.FutureFactory from route — a PUBLIC
                 api/reqreply interface (future.go), already implemented
                 by RouteHandle.NewFutureAny — no adapter cooperation
                 needed, confirmed usable directly from api/reqreply */
    if !ok {
        return nil, TransportTypeMismatchError{Want: "FutureFactory", Got: fmt.Sprintf("%T", route)}
    }
    future, resolve := ff.NewFutureAny()
    go func() {
        resp, err := c.Call(ctx, route, req, opts...) // reuses Client.Call's OWN already-centralized chain-following loop directly
        resolve(resp, err)
    }()
    return future, nil
}
```

- `CallRaw` stays synchronous — no `CallRawAsync` counterpart needed,
  since the ASYNC wrapping now happens entirely at the `api/reqreply.
  Client` level, one layer above both `Call` and `CallRaw`.
- **Real, confirmed consequence**: `ClientTransport.CallAsync` (the
  interface method) is REMOVED, not left as dead code — confirmed via
  `grep` that it has EXACTLY ONE caller anywhere in the codebase today
  (the very 1-line delegation this restructuring replaces); no
  `CallAsyncWithTransport` escape hatch and no `ports.Pattern` binding
  depends on it. Phase 1's implementation deletes the method from the
  `ClientTransport` interface AND from all 3 adapters' own
  implementations — a breaking change to an already-shipped interface,
  but a safe one (nothing else calls it).
- **Scoping note — this is Phase 1 work, not an urgent fix to today's
  shipped Phase 0 code.** `Client.CallAsync`/`ClientTransport.CallAsync`
  work correctly today exactly as shipped — `Forward` doesn't exist yet,
  so there is no `ForwardError` to fail to intercept. This restructuring
  is a confirmed, now-settled REQUIREMENT of Phase 1's own
  implementation (recorded here for when that implementation begins),
  not something to retrofit into Phase 0 separately.

## Dispatch-path inventory — CORRECTED, smaller than originally stated once grounded in the actual `ports.Pattern` binding code

The `deep-dive-bug-hunt` Round 201 lesson (`.github/skills/
deep-dive-bug-hunt/references/../review-go-codex/references/history.md`
— the Typed HTTP Redirects bug hunt) means EVERY dispatch path needs
independent verification, not just the obvious primary one — but
checking the ACTUAL `ports.Pattern` binding code (not just assuming it
mirrors `Call`/`CallAsync`) during the post-split re-review found the
original "8 independent combinations" claim OVERSTATED, in 2 ways:

1. **`ports.Pattern` binding's `Call`-equivalent is REGISTRY-FREE BY
   DESIGN, mirroring REST's own identical, already-shipped precedent —
   not a 5th independent forward-following implementation to build.**
   Confirmed directly: `adapters/mqtt5/binding.go`/`adapters/zeromq/
   binding.go`'s own `ports.Pattern` binding dispatches via
   `reqreply.CallWithTransport` (the registry-free, `*Client`-free escape
   hatch — confirmed its implementation is a thin generic wrapper calling
   `transport.Call` directly, with NO registry access at all). This
   EXACTLY matches `docs/features/rest-redirects.md`'s own documented
   rule: *"`rest.CallWithTransport`/`nethttp.NewClientTransport` used
   directly (no `*rest.Client`) stay registry-free... This includes
   `adapters/nethttp`'s `ports.Pattern` bindings... all of these are ALSO
   registry-free (no `*rest.Client` to auto-follow against)."* Since
   Option B's centralized resolve+`CallRaw` logic lives in
   `api/reqreply.Client` (not in the adapter), and `CallWithTransport`
   never goes through `Client`, a forward reply surfacing through THIS
   path simply returns `ForwardError` directly to the caller, unresolved
   — exactly `rest.CallWithTransport`'s own accepted, documented
   behavior for `RedirectError`. **This path needs NO new forward-
   following logic at all** — only confirmation (via a real test, not
   assumption) that the sentinel propagates through untouched, the same
   small, already-precedented verification REST's own `ports.Pattern`
   bindings needed.
2. **No `CallAsync`-equivalent `ports.Pattern` binding or
   `CallAsyncWithTransport` escape hatch EXISTS today** — confirmed via
   `grep` across `api/reqreply/handletransport.go` and both adapters'
   `binding.go` files: zero matches. The original matrix's
   "`ports.Pattern` binding × `CallAsync`" cell doesn't correspond to any
   REAL, existing dispatch path — there is nothing to trace or test for
   a combination that isn't implemented today, in either adapter.

**FURTHER SIMPLIFIED after the `CallAsync` restructure above**: since
`CallAsync` is REMOVED from `ClientTransport` entirely and
`api/reqreply.Client.CallAsync` now wraps `Client.Call` directly (not a
per-adapter dispatch path anymore), the "`Call` vs `CallAsync`" axis
this matrix originally had COLLAPSES — there is only ONE adapter-level
client entry point left (`Call`/`CallRaw`), not two. The
CHAIN-FOLLOWING LOOP itself is tested EXACTLY ONCE, at the
`api/reqreply.Client.Call` level (both `Client.Call` and
`Client.CallAsync` exercise the IDENTICAL loop — `CallAsync` is a thin
goroutine wrapper around it) — not once per adapter and not twice per
adapter. What EACH adapter still needs independently traced/tested is
the WIRE-SIGNAL RECOGNITION itself (the 6 client-recognize + 3
server-publish points from "Wire-level signal" above) — a DIFFERENT,
narrower concern than "does the chain-following loop work," which is now
adapter-agnostic by construction.

The CORRECTED, grounded inventory:

| | `Client`-level (`ClientTransport.Call`, feeding the SHARED `api/reqreply.Client` loop) | `ports.Pattern` binding (`CallWithTransport`) |
|---|---|---|
| `adapters/mqtt5` | wire-signal recognition in `call()`+`CallRaw()` (2 of the 6 client-recognize points above) | registry-free by design — only needs sentinel-passthrough verification |
| `adapters/zeromq` (`clientTransport`) | wire-signal recognition in `call()`+`CallRaw()` (2 of the 6) | registry-free by design — only needs sentinel-passthrough verification |
| `adapters/zeromq` (`dealerClientTransport`) | wire-signal recognition in `call()`+`CallRaw()` (2 of the 6) | registry-free by design — only needs sentinel-passthrough verification |

— **6 wire-signal recognition points** (3 adapter-transports × `call()`/
`CallRaw()`, matching "Wire-level signal" above exactly) **plus 2 small,
already-precedented passthrough checks** (mqtt5/zeromq's `ports.Pattern`
`Call`-equivalent, registry-free — zeromq's DEALER transport has no
`ports.Pattern` binding of its own today, so only 2 passthrough checks
exist, not 3) **plus the chain-following loop itself, tested EXACTLY
ONCE at the `api/reqreply.Client` level** (used identically by both
`Client.Call` and `Client.CallAsync`) — a smaller, more precisely-scoped
total than any earlier revision of this count (which ranged from "8" to
"4+2" across prior reviews, each one grounded in progressively more of
the real code as it came to exist). Each of the 6 wire-signal points
still needs its OWN independent trace + test at implementation time, per
Round 201's own hard-won lesson ("a fix applied to ONE of these does NOT
imply the others got it").

A smaller, related lesson from the SAME round: a doc comment claiming two
separate implementations are "behaviorally-equivalent" (as
`adapters/nethttp`'s own `consumeSSE` doc comment claimed, about
`Client.Consume`'s reconnect loop — a claim that did NOT hold for
redirects) is a CLAIM TO VERIFY via a live, failing test — not to trust.
This design's own eventual unit test plan must carry the same explicit
verification step for each of the 6 wire-signal recognition points
above, not rely on "it probably works the same way."

## Next step

Forward/bounce has a SETTLED design, hardened across 4 review rounds —
the latest grounded against the REAL, shipped Phase 0 registry +
`CallRaw` code rather than assumptions — none of it is implemented yet.
**The Phase 0 blocking dependency is ALREADY SATISFIED** (shipped,
commit `e86bc99`) — turning this into an actual implementation plan
(unit test plan covering the 6 wire-signal recognition points, the
`Client.CallAsync` restructure + `ClientTransport.CallAsync` removal,
`ForwardSecuritySchemeMismatchError`, `MaxForwards` chain-depth capping)
is a distinct, future step requiring its own explicit go-ahead — the
same rigor Typed HTTP Redirects itself went through across its own
critical-review rounds before implementation began. No code should be
written against this design until that implementation plan exists and
is approved.
