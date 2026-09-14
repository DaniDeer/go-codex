# `api/events` codec-declared middleware — 2 pre-existing bugs in ALREADY-SHIPPED D-0003 code

> **Status:** Design draft — both bugs fully designed (root-caused,
> fix sketched, confirmed via direct code inspection), not yet
> implemented. Independently actionable — do NOT need to wait for
> [ReqReply Codec-Declared Middleware](reqreply-codec-declared-middleware.md)'s
> own Phase 0 (which ALSO plans to fix these same 2 bugs, since Phase 0
> already touches these exact code paths to add property-var support).
> If Phase 0 ships first, these fixes are already folded into its own
> "Side track" section — implementing them from THIS doc instead only
> makes sense if Phase 0 is delayed/deprioritized and these bugs are
> worth fixing sooner, standalone.
> [← Back to Roadmap](index.md)

## Motivation

Found while designing
[ReqReply Codec-Declared Middleware](reqreply-codec-declared-middleware.md)'s
Phase 0 (bringing `api/events`' own codec-declared `Middleware[In,Out]`
mechanism up to full parity with `api/reqreply`'s new property axis).
Confirming what full events parity required meant tracing
`api/events`' D-0003 implementation in `adapters/mqtt5`/`adapters/zeromq`
in detail — surfacing 2 REAL, PRE-EXISTING bugs in code that shipped
under D-0003 already, NOT introduced by that design work. Both
confirmed via direct code inspection, not speculation.

This doc extracts BOTH bugs into their own standalone, independently-
trackable roadmap item — so they aren't lost or indefinitely deferred
if Phase 0 (a larger, 2-package feature addition) ends up delayed,
descoped, or reprioritized. Both bugs are narrow, self-contained, and
have NO dependency on Phase 0's own property axis to fix.

## Bug 1 — publish-side value precedence is backwards (D3-equivalent)

[D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)'s
D3 decision states the precedence order should be: **explicit override
> middleware-derived > channel/route-own-derived** (middleware wins
over the channel's own value on a name collision) — confirmed as
REST's real, shipped order via `adapters/nethttp/client.go`'s
`overrideDerived` chain (`query = overrideDerived(query, mwQuery)` then
`opts.QueryParams = overrideDerived(query, explicitQuery)` — middleware
beats route-own, explicit beats middleware). D-0003's own test-plan
text independently confirms this is the INTENDED design for events too:
"Repeat for `events.ClientTransform`'s publish-side precedence
(explicit adapter Vars > middleware-derived > channel-derived)."

Events' publish-side code does the OPPOSITE — confirmed IDENTICAL in
BOTH `adapters/mqtt5/adapter.go` and `adapters/zeromq/adapter.go`:

```go
// Current (WRONG): channel-own vars win over middleware-derived vars.
vars = overrideDerivedVars(mwVars, vars)
```

`overrideDerivedVars(derived, explicit)` returns `explicit`-wins-on-
collision (confirmed via its own doc comment/implementation in
`adapters/mqtt5/transform_dispatch.go`, identically in
`adapters/zeromq/transform_dispatch.go`) — so passing `vars` (channel-
own) as the `explicit` argument makes channel-own beat middleware,
backwards relative to D3.

### Why this is NOT a 1-line fix

Traced the call chain via `adapters/mqtt5/binding.go`'s `PublishAdapter`
— when `a.opts.Vars == nil`, `publishHandle` derives `vars` from the
channel's own `NewTopicParam` merge fields (pure channel-own); when
`a.opts.Vars != nil`, the caller's EXPLICIT static override is passed
directly as `vars`. By the time `vars` reaches the `overrideDerivedVars`
call INSIDE `publish()`, the distinction between "this is channel-own"
and "this is a genuine explicit override" has ALREADY been erased —
both arrive as a plain `map[string]string`. A naive argument-order
reversal (`overrideDerivedVars(vars, mwVars)`, making `mwVars` always
win) would ALSO make middleware incorrectly beat a genuine EXPLICIT
override, violating the "explicit > middleware" half of D3's rule.

### The fix — a single boolean flag, not a parameter-shape overhaul

Traced ALL real callers of `publish()`/`publishHandle()` in
`adapters/mqtt5/binding.go` (confirmed IDENTICAL in
`adapters/zeromq/binding.go`): `PublishAdapter` branches `if
a.opts.Vars == nil { publishHandle(...) } else { publish(...,
a.opts.Vars, ...) }` — the two cases are ALREADY MUTUALLY EXCLUSIVE at
the call site (explicit `Vars` COMPLETELY REPLACES channel-own
derivation via `publishHandle`'s own `codex.EncodeVars(msg,
handle.MergeFields()...)` call — never both at once). Since a caller of
`publish()` is NEVER in both states simultaneously, a full parameter-
shape restructuring (e.g. splitting `vars` into two separate map
parameters) is MORE invasive than necessary — a SINGLE `isExplicitVars
bool` flag threaded through the SAME existing `vars map[string]string`
parameter is sufficient and minimal:

```go
// Sketch — publish() gains ONE new bool parameter distinguishing which
// precedence rule applies to its EXISTING vars parameter, rather than
// splitting vars into two separate parameters:
func publish[T any](
    ctx context.Context,
    client MQTTClient,
    handle *events.ChannelHandle[T],
    qos byte,
    retained bool,
    msg T,
    vars map[string]string, // UNCHANGED shape — either channel-own-derived (via publishHandle) or explicit (via PublishAdapter's direct call), never both
    isExplicitVars bool,    // NEW — true only when vars came from PublishOptions.Vars (the direct-call path)
    opts PublishOptions[T],
    formats ...format.Format[T],
) error {
    // ...
    if len(handle.ClientMiddlewareHandlers) > 0 {
        mwVars, mwErr := dispatchPublishMiddlewareHandlers(ctx, msg, handle.ClientMiddlewareHandlers)
        // ...
        if isExplicitVars {
            vars = overrideDerivedVars(mwVars, vars) // explicit ALWAYS wins over middleware
        } else {
            vars = overrideDerivedVars(vars, mwVars) // middleware wins over channel-own (the FIX)
        }
    }
    // ...
}
```

`publishHandle` calls `publish(..., vars, false, ...)` (channel-own
case); `PublishAdapter`'s direct-call branch calls `publish(...,
a.opts.Vars, true, ...)` (explicit case) — both call sites already know
unambiguously which case they're in, so threading the flag through
requires no new state, just one extra argument at 2 already-existing
call sites per adapter. Requires the SAME minimal change in
`adapters/zeromq`'s identical `publish()`/`PublishAdapter` pattern.

**No dependency on Phase 0's property axis** — this fix is entirely
self-contained; `vars`/`mwVars` stay single maps regardless of whether
the property axis ever ships.

## Bug 2 — missing Observer integration on both subscribe and publish middleware dispatch (D5-equivalent)

D-0003's D5 decision ("Observer/stats integration") explicitly states
"Events mirror, confirmed NOT REST-specific" — `events.Transform`/
`events.ClientTransform` should get the SAME `stats.ReportErrors(obs,
"middleware:in"/"middleware:fn", err)` calls REST's real
`runMiddlewareHandlersReflect`/client dispatch already has. But events'
actual dispatch functions — `dispatchSubscribeMiddlewareHandlers`
(subscribe-side) and `dispatchPublishMiddlewareHandlers` (publish-side),
both in `adapters/mqtt5/transform_dispatch.go` (confirmed IDENTICAL in
`adapters/zeromq/transform_dispatch.go`) — have ZERO
`stats.ReportErrors` calls for either failure class today, confirmed
via direct code inspection. This was simply never implemented, despite
D5 already declaring it should exist.

### The fix (standalone version — no property-axis dependency)

Add `stats.ReportErrors(obs, "middleware:in", err)` on a `DecodeIn`
failure and `stats.ReportErrors(obs, "middleware:fn", err)` on the fn's
own business error, at BOTH dispatch call sites (`adapters/mqtt5/adapter.go`'s
subscribe/publish handlers, which call
`dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers`)
— and the identical fix in `adapters/zeromq`'s equivalent dispatch
functions. Uses the SAME `obs`/`ObserverFromContext` nil-guard pattern
already established elsewhere in these adapters.

**If implemented standalone (independent of Phase 0)**: NO signature
change needed at all — `dispatchSubscribeMiddlewareHandlers`/
`dispatchPublishMiddlewareHandlers` keep their EXISTING single-topic-
var-map signatures; only the 2 missing `stats.ReportErrors` calls are
added internally.

**If Phase 0 ships first (or alongside)**: these dispatch functions'
signatures ALSO change to the property-axis's 2-map extension
(`dispatchSubscribeMiddlewareHandlers` gains a `propertyVars
map[string]string` parameter; `dispatchPublishMiddlewareHandlers`
returns 2 SEPARATE maps instead of one merged map) — see
[ReqReply Codec-Declared Middleware](reqreply-codec-declared-middleware.md)'s
own "Phase 0"/"Side track" sections for that combined version. The 2
`stats.ReportErrors` calls this doc adds are IDENTICAL either way — only
the surrounding signature differs depending on which doc's version ships.

## Relationship to `reqreply-codec-declared-middleware.md`

**Not a competing plan — the SAME 2 bugs, extracted for independent
tracking.** `reqreply-codec-declared-middleware.md`'s own "Side track:
fixing 2 pre-existing bugs in events' ALREADY-SHIPPED D-0003 code"
section already has BOTH bugs fully designed, WITH the property-axis
signature extension folded in (since Phase 0 touches these exact files
anyway). Whichever doc's implementation lands FIRST resolves both bugs;
the other doc's own "Side track"/this-doc's fix becomes a no-op
(already fixed). No risk of conflicting fixes — the underlying
technical fix (precedence flag, 2 Observer calls) is IDENTICAL in both
places, just scoped differently (standalone vs. bundled with property
support).

## Files to create

| File | Responsibility |
|---|---|
| `adapters/mqtt5/adapter.go` (edit) | `publish()` gains `isExplicitVars bool` param (Bug 1); subscribe/publish handlers gain `stats.ReportErrors(obs, "middleware:in"/"middleware:fn", err)` calls (Bug 2) |
| `adapters/mqtt5/binding.go` (edit) | `PublishAdapter`'s 2 call sites to `publish()`/`publishHandle()` updated to pass the new `isExplicitVars` flag correctly |
| `adapters/mqtt5/transform_dispatch.go` (edit) | `overrideDerivedVars` call site fixed inside `publish()` per Bug 1's sketch; NO signature change to `dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers` themselves if implemented standalone (Bug 2's fix is purely additive `stats.ReportErrors` calls) |
| `adapters/zeromq/adapter.go` (edit), `adapters/zeromq/binding.go` (edit), `adapters/zeromq/transform_dispatch.go` (edit) | Same 2 fixes, mirrored identically |
| `adapters/mqtt5/adapter_test.go` (edit), `adapters/zeromq/adapter_test.go` (edit) | New tests for both fixes (see "Unit test plan") |

## Unit test plan

| Test | Verifies |
|---|---|
| `TestPublish_ValuePrecedence_ExplicitBeatsMiddlewareBeatsChannelOwn` (mqtt5 and zeromq) | with ALL THREE tiers present on the SAME var name (explicit `PublishOptions.Vars`, a `Middleware`'s `WithPublishTopic`, AND the channel's own `NewTopicParam`-derived value), the final resolved value follows explicit > middleware-derived > channel-own-derived — the CORRECTED order |
| `TestSubscribe_Observer_ReportsMiddlewareInAndFnLocations` (mqtt5 and zeromq) | `dispatchSubscribeMiddlewareHandlers` calls `stats.ReportErrors(obs, "middleware:in", err)` on a `DecodeIn` failure and `stats.ReportErrors(obs, "middleware:fn", err)` on the fn's own business error — both confirmed ABSENT before this fix |
| `TestPublish_Observer_ReportsMiddlewareInAndFnLocations` (mqtt5 and zeromq) | same as above, for `dispatchPublishMiddlewareHandlers` |

## Out of scope

- Anything related to `api/reqreply`'s own property axis, `PropertyParam`,
  or Phase 0 in general — see
  [ReqReply Codec-Declared Middleware](reqreply-codec-declared-middleware.md)
  for that entirely separate, larger design.
- The 3 REST-specific candidates (namespace strictness, codec
  comparison, output-encode Observer gap) — see
  [REST Middleware Conflict-Detection Improvements](rest-middleware-conflict-detection-improvements.md)
  for those.
- `mqtt`(v3) — N/A, `mqtt`(v3) has no codec-declared `Middleware[In,Out]`
  mechanism at all (confirmed: only `mqtt5`/`zeromq` implement
  `dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers`
  today).
