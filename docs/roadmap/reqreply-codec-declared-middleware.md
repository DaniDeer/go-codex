# ReqReply Codec-Declared Middleware — bringing `api/reqreply` (AND `api/events`) up to D-0003 parity

> **Status:** Design draft — not yet implemented. Reverted from
> "Design complete" back to draft after Round 8's MAJOR scope
> expansion (see below) — a substantial restructuring that needs its
> own review depth before the "complete" label is honest again.
> Refined across 8 review rounds total. Round 1, all 3
> originally-open design decisions
> RESOLVED (see "Open design decisions"): (1) `ClientTransform`'s shape
> confirmed against `api/rest/transform.go`'s real signatures; (2)
> naming (`reqreply.Middleware[In,Out]` alongside `middleware.
> Middleware`) confirmed safe, zero collision; (3) a NEW protocol-neutral
> **"property" vocabulary axis** (`WithRequestProperty`/
> `WithResponseProperty`) added after confirming MQTT5 User Properties
> and AMQP message headers are the SAME cross-transport concept — a
> genuine companion change for `api/events` too (see "Companion change"
> below). **Round 2 — re-verified against REST's ACTUAL implementation
> code (not just its public signatures) and found/fixed 4 issues**: (a)
> a design ERROR — `DecodeIn`/`EncodeOut`/etc. do NOT combine topic +
> property vars into one map, they take/return SEPARATE maps per axis,
> mirroring REST's real multi-map signatures exactly (see "The
> 'property' vocabulary axis"); (b) dispatch ORDER now stated explicitly
> — `Transform`'s declared middleware runs AFTER the paired security Fn,
> confirmed via REST's real dispatch code; (c) a missing AsyncAPI
> spec-rendering integration, now added (see "AsyncAPI spec rendering");
> (d) merge-field name conflict detection now resolved — follows REST's
> stricter `ConflictingParamContributionError` precedent, a deliberate,
> accepted divergence from Phase 1b's laxer silent-dedupe behavior (see
> "AsyncAPI spec rendering" and "Open design decisions"). **Round 3 —
> deep-compared the new `PropertyParam`/`MergedPropertyParam[T]`/
> `NewPropertyParam[T,V]` sketch against reqreply's OWN real, shipped
> `TopicParam`/`MergedTopicParam[Req]`/`NewTopicParam`
> (`api/reqreply/route.go`), which it claims to mirror exactly, and
> found/fixed 4 parity gaps**: (a) a struct-shape BUG —
> `MergedPropertyParam[T]` now correctly wraps `codex.MergedParam[T]`
> directly (single embed, matching `MergedTopicParam[Req]`'s real
> shape) instead of a disconnected, never-populated separate `Field`
> field; (b) added the missing `WithDescription` method (the only way
> to set one, since `NewPropertyParam` takes no description param,
> mirroring `MergedTopicParam.WithDescription`); (c) added the missing
> `WithCodec` escape-hatch method on plain `PropertyParam` (mirroring
> `TopicParam.WithCodec`); (d) added the missing internal
> `toParam`/`applyRoute` route-builder wiring pair (routes into a NEW
> `rb.propertyParams []PropertyParam` slice, parallel to the existing
> `topicParams` slice). **Round 4 — full end-to-end re-read (including
> Observer integration, previously under-reviewed) found/fixed 2 bugs
> and resolved 1 new open decision**: (a) a self-contradiction leftover
> from BEFORE the Round 2 fix — a paragraph near the
> `RouteHandle`/`MiddlewareHandler` sketch still said `DecodeIn` uses
> "COMBINED" topic+property vars, now corrected to state two SEPARATE
> map parameters; (b) matching ambiguous "COMBINED" wording in the unit
> test plan, reworded for clarity; (c) a genuine gap — the Observer
> integration section never specified which adapter `ErrorKind` wraps
> the new mechanism's own errors; RESOLVED (decision #6): `KindDecode`/
> `KindEncode` for merge-side failures (natural extension of existing
> Phase 1b precedent), and a NEW `KindMiddleware` value added to both
> `mqtt5.ErrorKind` and `zeromq.ErrorKind` for `MiddlewareError` (D2's
> fn-business-error fallback), distinguishing a declared-middleware
> failure from a real handler failure. **Round 5 — resolved the last
> open decision, plus fixed a decision-numbering cross-reference bug**:
> (a) decision #4 (`PropertyParam` location) is now RESOLVED — user
> confirmed DUPLICATION per-package (`reqreply.PropertyParam` + a future
> `events.PropertyParam`), exactly mirroring `TopicParam`'s existing
> precedent, not a new shared cross-package type; (b) fixed 2 spots that
> mislabeled merge-field conflict detection as "decision #4" when it is
> actually decision #5 in the numbered list. **All 6 open design
> decisions are now RESOLVED — zero open points remain.** **Round 6 —
> full end-to-end re-read (readiness check) found/fixed 3 staleness/
> completeness bugs**: (a) THIS banner's own "Refined across 2
> follow-up rounds" sentence was stale (now says 5); (b) the "Observer
> integration" section still claimed decision #6 was "an open
> question... not yet resolved," contradicting this banner and the
> "Open design decisions" section — corrected to state the actual
> resolution; (c) the Files-to-create table never listed
> `adapters/mqtt5/reqreply_transport_test.go`/`adapters/zeromq/
> reqreply_transport_test.go` even though 4 of the 16 planned unit
> tests live there — added. **Round 7 — user challenged the "Design
> draft" label; a deep cross-check against D-0003's OWN full numbered
> decision set (D1-D7, not just the sections earlier rounds already
> touched) found 2 SUBSTANTIVE gaps this round, confirming "draft" was
> still the honest label**: (a) the "Observer integration" section
> cited the WRONG `stats.ReportErrors` location string (reused Phase
> 1b's existing `"topic_var"` tag instead of D5's own dedicated
> `"middleware:in"`/`"middleware:fn"` strings, confirmed shipped in
> `adapters/nethttp/serve.go`/`client.go`) and was missing the
> `"middleware:fn"` call entirely — corrected, now cites D5 explicitly;
> (b) NO stated value-precedence rule existed for when the route's own
> topic/property merge and a `Middleware`'s own merge target the SAME
> var name with agreeing declared attributes but differing runtime
> values — added a new "Value precedence" section mirroring D3's real,
> shipped 2-tier (for reqreply)/3-tier (for REST) rule: middleware-
> derived ALWAYS wins. Both gaps added 2 new unit test rows. Doc was
> THEN relabeled "Design complete — not yet implemented" per user
> instruction. **Round 8 — MAJOR restructuring, at the user's request:
> the property axis is promoted from "reqreply core + events companion
> (deferred, smaller follow-up)" to PHASE 0 — implemented for BOTH
> `api/reqreply` AND `api/events` in the SAME round, specifically to
> enforce consistency from day one.** Investigating what full events
> parity requires surfaced 2 REAL, PRE-EXISTING bugs in events'
> ALREADY-SHIPPED D-0003 mechanism (not introduced by this session) —
> both confirmed via direct code inspection, both approved by the user
> to fix in this SAME round: **(a) a publish-side value-precedence bug
> (D3-equivalent)** — events' `adapters/mqtt5`/`adapters/zeromq` make
> channel-own-derived vars win over middleware-derived vars, backwards
> relative to D3's real, shipped order (confirmed via
> `adapters/nethttp/client.go`'s `overrideDerived` chain); root-caused
> to `PublishAdapter`/`publishHandle`/`publish()`'s parameter shape
> erasing the explicit-vs-channel-own distinction before the
> middleware-override point — NOT a 1-line fix, a real refactor (see
> "Side track" below for the full design); **(b) a missing Observer-
> integration gap (D5-equivalent)** — events' `dispatchSubscribeMiddlewareHandlers`/
> `dispatchPublishMiddlewareHandlers` have ZERO `stats.ReportErrors`
> calls for middleware errors, contradicting D-0003's own "Events
> mirror, confirmed NOT REST-specific" clause for D5. Both bugs get
> full design + unit test coverage this round. Given the substantial
> scope expansion (2 packages instead of 1, plus 2 real bug-fix
> designs), the doc's status REVERTS to "Design draft" until this
> round's expanded design is complete and reviewed at the SAME depth
> Rounds 1-7 gave the reqreply-only design. **Round 9 — reviewed Round
> 8's new content before implementation, found/fixed 2 real issues**:
> (a) Bug 1's originally-sketched fix (2 separate map parameters on
> `publish()`) was MORE INVASIVE than necessary — traced ALL real
> callers in both adapters and confirmed the explicit-vars and
> channel-own-vars cases are ALREADY mutually exclusive at the call
> site, so a single `isExplicitVars bool` flag threaded through the
> EXISTING `vars` parameter is simpler and sufficient — sketch
> corrected; (b) confirmed events needs a BRAND NEW
> `events.ConflictingParamContributionError` type AND a NEW
> `checkEventsParamConflicts` function (events has ZERO pre-existing
> conflict-detection machinery, unlike its other 4 already-shipped
> middleware error types) — the doc previously implied this was
> inherited/existing; now explicit in "AsyncAPI spec rendering,"
> "Structured errors," and the Files-to-create table. **Round 10 —
> final whole-doc review before implementation planning, found THE
> most significant gap of the entire multi-round review**: traced the
> FULL write-side path for a property value, all the way to the
> actual wire, for the FIRST time (all 9 prior rounds verified
> type-level design only). Confirmed via direct code inspection of
> every real `.Publish(...)` call site: **reqreply's server-side reply
> (`serverTransport.Serve`) has ZERO mechanism to write outgoing User
> Properties on either its success-reply OR error-reply path** —
> `ServeOptions`'s only User-Property field is validate-only, checked
> against the INCOMING request; nothing writes to the OUTGOING reply,
> not even Phase 1b's existing `FromResponseUserPropertyParam`
> (spec/validation-only). Without a fix, `WithResponseProperty` would
> silently produce a value with nowhere to go. Confirmed this gap is
> ASYMMETRIC — reqreply's client-request side and events' publish side
> both already have a working write-target (`t.opts.UserProperties`/
> `opts.UserProperties`) the property axis just needs to merge into;
> only the reqreply SERVER-REPLY direction needs genuinely NEW
> capability. Added a new "Write-side wiring" section covering all 3
> cases explicitly, updated 2 Files-to-create rows, added 4 new unit
> tests verifying values actually reach the wire (not just the
> type-level decode/encode). Also fixed a smaller completeness gap:
> events' `dispatchSubscribeMiddlewareHandlers`/
> `dispatchPublishMiddlewareHandlers` signatures need explicit updates
> (new `propertyVars` param / 2-map return) to match the `DecodeIn`/
> `EncodeOut` 2-map extension — previously implied, not stated. **Round
> 11 — found another significant, previously-unquestioned gap:
> `PropertyParam` had NO way to be optional.** Confirmed via
> `codex.NewParam`'s real implementation that it hardcodes
> `RequiredField`, making every property (both reqreply's and events')
> unconditionally required — unlike a topic var (structurally always
> required), a property is conceptually optional metadata. Confirmed
> `codex.OptionalField` already exists as the counterpart, unused by
> `NewParam`. User's decision: add optional-property support NOW —
> `Required bool` added to `PropertyParam`/`MergedPropertyParam[T]`
> (mirroring `HeaderParam.Required`, not touching shared `codex.Param`)
> plus a NEW `NewOptionalPropertyParam[T,V]` constructor, hand-built
> against `codex.OptionalField` (zero `codex` package changes needed),
> applied identically to BOTH `api/reqreply` and `api/events`. This
> also resolved a previously-unexamined ambiguity in the conflict-
> detection section's own `Required: true`/`false` example, which had
> no genuinely analogous case to check before this fix. New Open Design
> Decision #7 added, RESOLVED. 4 new unit tests added. **Round 12 —
> explicit feature-parity investigation against D-0003's own D6
> sub-decisions (D6(a)/D6(b)/D6(c)) and test plan, per user's specific
> request.** Confirmed D-0003's OWN test-plan text ("explicit CallOptions
> wins over ClientTransform-derived values... Repeat for events'
> publish-side precedence: explicit adapter Vars > middleware-derived >
> channel-derived") independently corroborates Round 8's finding that
> events' REAL shipped precedence is backwards relative to the DESIGN
> itself, not just relative to REST's code. Found 3 items: (a) D6(a)
> was functionally already correct but never explicitly labeled — added
> the citation to the existing dedupe test (+ an events-side mirror
> test that didn't exist yet); (b) D6(c) (two middlewares enriching the
> SAME `*Req`/`*T` field, attachment-order/last-applied-wins) had NO
> test at all, and multiple-attachment accumulation was never explicitly
> confirmed as an intended capability — fixed with a new confirming
> note (citing REST's real `append(slices.Clone(r.opts), ...)`) plus 4
> new tests (2 reqreply + 2 events mirrors); (c) confirmed `.Use()`
> needs NO backward-compat widening for reqreply/events (unlike REST's
> real historical migration) — both already built against the shared
> `middleware.RouteMiddleware` interface from day one — documented as a
> genuine parity WIN, not a gap. **Round 13 — reviewed specifically
> through the "consistent, simple, declarative workflow" lens.** Found
> 3 real findings, all sharing one theme: the type-level design already
> supports rich reuse patterns BY CONSTRUCTION, but none were explicitly
> documented, tested, or exemplified — users need to KNOW a pattern is
> INTENDED, not just possible by accident, to use it confidently. Added
> a NEW "Reuse patterns" section stating all 3 explicitly: (a)
> cross-route/channel reuse with DIFFERENT `Req`/`T` types (D-0003's own
> "test that actually proves reuse," now covered); (b) cross-API
> `Declaration[In,Out]` reuse across REST/events/reqreply, mirroring the
> flat mechanism's own Demo 9 pattern, now documented with a worked code
> sketch; (c) cross-role reuse within events (one `Middleware`, both
> Subscribe AND Publish, via independent `receiveFn`/`sendFn` fields).
> Added 4 new unit tests across reqreply and events. **Round 14 —
> applied the `review-go-codex` skill's own Boundary Symmetry Guardrail
> checklist explicitly (single-call convenience wrapper +
> format-agnosticism + nested-struct merge fields).** Investigated
> `CallHandle` wiring — confirmed a NON-issue: `mqtt5.Call`/`CallHandle`
> and `zeromq.Call`/`CallHandle` all construct a `clientTransport` and
> dispatch through the SAME `clientTransport.Call` this doc's Middleware
> mechanism already hooks into, zero separate wiring needed (added
> confirming notes + fixed a `.call`→`.Call` casing slip). Found a REAL
> gap the skill explicitly warns is easy to miss: format-agnosticism
> and nested-struct merge fields were NEVER tested or exemplified for
> the new property/topic axis, despite REST's own real reference tests
> (`TestNestedStructMergeFields_GetSetReachIntoSubstruct`/
> `TestGobBodyFormat_ComposesWithNestedMergeFields`, confirmed to exist)
> setting this exact bar. Added 4 new unit tests (2 reqreply + 2 events
> mirrors) confirming nested sub-struct access and Gob-format
> orthogonality. **Round 15 — traced REST's REAL `checkParamConflicts`/
> `applyParamDeclarations` in full for the FIRST time, found 2
> refinements to conflict detection.** (a) Confirmed REST's real code
> puts header/cookie/query into ONE shared, cross-kind-strict
> namespace — user's decision: reqreply/events do NOT mirror this;
> topic-vars and properties are INDEPENDENT namespaces, never
> cross-checked (stronger justification here than REST's own boundary,
> since topic vars and properties come from genuinely different wire
> locations). (b) Confirmed REST's real `paramContribution` struct has
> NO Codec field at all — our own earlier prose overstated this,
> claiming "different codecs" conflict when REST's real code never
> compares codecs. User's decision: DELIBERATELY go beyond REST's
> precedent — add a Schema-based codec comparison via
> `reflect.DeepEqual`, explicitly flagged as an intentional divergence.
> New Open Design Decision #8 added, RESOLVED. 4 new unit tests added.
> Also spawned a SEPARATE companion roadmap doc evaluating whether
> REST's OWN precedent should eventually adopt either change too (out
> of scope for this doc). **Round 16 — found a real gap: Round 11's new
> `PropertyParam.Required` field was never confirmed to propagate into
> the AsyncAPI spec's `required` array, only checked for runtime
> validation.** Traced Phase 1b's REAL `applyParamDeclarations`
> (`api/reqreply/middleware.go:191`) in full: `Required` genuinely
> determines which property names land in the rendered schema's
> `required` list, confirmed via real code. The "AsyncAPI spec
> rendering" section (written Round 2, BEFORE Round 11 added
> `Required`) was never revisited to state this explicitly for the NEW
> property axis. Fixed in both reqreply's and events' sections, with 3
> new unit tests. Also cross-referenced this doc's own property axis as
> a documented use case in
> [Protocol-Native Feature Declarations](protocol-native-features.md)
> and [MQTT5 User Property Merge](mqtt5-user-property-merge.md) — see
> those docs' own updated banners for the 3-way relationship between
> Phase 1b (validate-only), that doc's planned merge-capable
> `ChannelOpt`, and THIS doc's property axis (API-level, sooner-to-ship,
> non-competing with the broader adapter-level `Capability` mechanism).
> **Round 17 — user asked "anything missing to PLAN the
> implementation?"** Design itself confirmed complete (all 8 numbered
> decisions resolved, zero unresolved markers) but 3 things were
> missing for IMPLEMENTATION PLANNING specifically, now added: (1) a
> NEW "Implementation phasing" section — a 13-phase, dependency-
> respecting build order (was previously just a flat Files-to-create
> list); (2) a NEW demo
> (`examples/reqreply-api/demo_property_axis_middleware.go`) exercising
> the property axis end-to-end, especially "Write-side wiring"'s Case
> 3 fix, as RUNNABLE code, not just unit tests; (3) a NEW "Definition
> of Done" section stating the SAME verification gates
> (`go build`/`go test`/`just check`/`go fmt`/example-run/instructions-
> sync) this session's OWN 16 prior review rounds have used throughout.
> **Round 18 — ⚠️ BREAKING CHANGE, deliberately accepted.** Round 17's
> follow-up question exposed a genuine self-contradiction: this doc
> claimed conflict-detection is BOTH "unified into one pass with Phase
> 1b" AND that "Phase 1b's laxer behavior is NOT retroactively
> changed" — impossible without an unstated origin-tracking rule.
> Presented with the choice, the user chose the SIMPLER option over
> preserving backward compatibility: ONE uniform conflict-detection
> algorithm now applies to ALL contributions (Phase 1b's flat mechanism
> AND the new axis alike) — Phase 1b's OWN silent-first-seen-wins
> dedupe FOR MISMATCHED DECLARATIONS is RETIRED, a narrow but genuine
> breaking change (an existing route with two disagreeing Phase-1b-only
> declarations for the same property name would now fail to
> `Register`). Phase 1b's flat mechanism itself (functions, `.Use()`
> attachment, agreeing-declaration rendering) is UNCHANGED — only this
> ONE specific dedupe behavior changes. Decision #5 and the "Out of
> scope" Phase 1b bullet both updated with the precise scope of the
> break; 1 new regression test added.
> [← Back to Roadmap](index.md)

## Motivation

[D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)
gave `api/rest` and `api/events` a SECOND, additive middleware mechanism —
`middleware.Declaration[In,Out]` + a per-pattern `Middleware[In,Out]` type
(`rest.Middleware[In,Out]`/`events.Middleware[In,Out]`) — on top of the
older, still-fully-functional flat `middleware.Middleware` (`.Use()`/
`HandleMW`/`ClientMW`, security-only) mechanism. The new mechanism lets a
middleware value carry its OWN codec-backed Input/Output shape, exactly
like a route/channel declares its own `Req`/`Resp` — enabling declarative
param merge (REST: header/cookie/query, both directions; events: topic
vars) for concerns that are NOT security (enrichment, derived data,
response-attribute policy), attached via `Transform`/`ClientTransform`
(route/channel-BOUND, `fn` gets `req`/`msg` access) or plain `.Use(mw)`
with a bundled `WithReceive`/`WithSend` Fn (route/channel-AGNOSTIC,
reusable verbatim across many routes/channels).

`api/reqreply` never got this second mechanism — not because of a
deliberate exclusion, but PURELY CHRONOLOGICAL: D-0003 was designed and
shipped BEFORE [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md)
(which gave reqreply its `Server`/`Client`/`Attach` rework) and BEFORE
[ReqReply Middleware](reqreply-middleware.md)'s Phase 1/1b (which gave
reqreply its OWN flat `.Use()`/`HandleMW`/`ClientMW` split, mqtt5 AND
zeromq). D-0003 could only build on REST/events' EXISTING baselines at
the time it was written — reqreply had no baseline yet to extend. Today,
reqreply's flat mechanism has reached the SAME maturity point REST's
flat mechanism was at immediately BEFORE D-0003 shipped: a working
security declare/implement split, but no codec-backed, non-security
enrichment/merge mechanism alongside it.

This doc designs that mechanism for `api/reqreply` — `reqreply.
Middleware[In,Out]` + `Transform`/`ClientTransform` — entirely ADDITIVE
to Phase 1/1b's existing flat mechanism (which stays completely
unchanged, exactly as `middleware.Middleware`/`SecurityScheme` remained
unchanged when D-0003 shipped for REST/events).

## Scope decisions (what's in this doc, what's deferred)

| In scope | Out of scope |
|---|---|
| `reqreply.Middleware[In,Out]` embedding `middleware.Declaration[In,Out]` | Retrofitting/changing the existing flat `middleware.Middleware`/`Route.Use`/`HandleMW`/`ClientMW` security mechanism — stays exactly as shipped |
| Topic-var merge vocabulary (`WithRequestTopic`/`WithResponseTopic`, reusing the EXISTING `NewTopicParam`/`MergedTopicParam[Req]` constructor — no new param type) | — |
| **Property merge vocabulary** (`WithRequestProperty`/`WithResponseProperty`, via a NEW `PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` triple — a protocol-neutral "named metadata separate from payload" concept realized differently per adapter: MQTT5 User Properties, future AMQP native message headers) | Building the ADAPTER-side wire mechanism itself (e.g. actually reading/writing MQTT5 User Properties for this NEW axis) — that's `adapters/mqtt5`'s own implementation work, sequenced AFTER this doc's core-type design ships |
| `Transform`/`ClientTransform` — route-BOUND attachment, `fn` gets `req *Req`/`req Req` access, mirroring REST's EXACT request/response-symmetric shape (confirmed against `api/rest/transform.go`'s real signatures — see "API surface") | A `zeromq`-specific extension — this doc's mechanism is fully transport-agnostic (topic vars AND properties are both just named `map[string]string` merges under the hood), so it should work identically for mqtt5 AND zeromq with ZERO core-type adapter-specific work; a route declaring a REQUIRED property on an adapter with no property mechanism (zeromq, mqtt v3) naturally surfaces a validation error there, no special-casing needed |
| Channel-AGNOSTIC attachment via bundled `WithReceive`/`WithSend` + plain `.Use(mw)` | `mqtt`(v3) — N/A, no reqreply support exists there at all (protocol limitation, unchanged) |
| D6(b)/D7-equivalent checks (duplicate middleware name, ambiguous dual-attachment) | A `ports.Middleware[In,Out]` extension — D-0003's own "Feasibility" section already covers that as a SEPARATE future decision, unrelated to reqreply |
| **Phase 0 (Round 8): `api/events`' IDENTICAL property axis, implemented in the SAME round as reqreply's own** — `events.PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]`, `WithSubscribeProperty`/`WithPublishProperty`, `buildDecodeIn`/`buildEncodeOut` extensions, AsyncAPI rendering (see "Phase 0: the property axis ships for `api/reqreply` AND `api/events` TOGETHER" below) | — |
| **Side track (Round 8): fixing 2 pre-existing bugs in events' ALREADY-SHIPPED D-0003 code**, discovered while scoping Phase 0's events parity — a value-precedence bug (D3-equivalent, publish-side) and a missing Observer-integration gap (D5-equivalent, both subscribe AND publish dispatch) — see "Side track" below | Retrofitting/changing anything ELSE in events' shipped D-0003 mechanism beyond these 2 specific, confirmed bugs |

## Toolchain / dependency decisions

None — this is a pure `api/reqreply` + adapter (`adapters/mqtt5`,
`adapters/zeromq`) addition, reusing `middleware.Declaration[In,Out]`
(already shipped, package `middleware`) and `codex.FieldCodec[T]`/
`codex.DecodeVars`/`codex.EncodeVars` (already shipped, package `codex`)
exactly as REST/events already do. No new external dependency.

## API surface

Mirrors `rest.Middleware[In,Out]`/`Transform`/`ClientTransform` in SHAPE
(single `Route` carries BOTH request and reply directions, unlike
events' split Subscriber/Publisher) — **confirmed against
`api/rest/transform.go`'s ACTUAL signatures this round, resolving what
would otherwise have been an open design decision**: REST's `Transform`
fn is `func(ctx, req *Req, in In) (Out, error)` (decodes `In` from
REQUEST-side vars, fn enriches `*Req` AND produces `Out`, which encodes
into RESPONSE-side vars) — REST's OWN `Transform` already spans BOTH the
request and response directions in one call, because a single HTTP
round-trip has both. reqreply's request/reply round-trip is the
STRUCTURALLY IDENTICAL shape (one `Serve` invocation sees both the
decoded request AND produces the encoded reply) — so `reqreply.Transform`
mirrors `rest.Transform` byte-for-byte, no new shape needed. Likewise
`rest.ClientTransform`'s fn is `func(ctx, req Req) (In, error)` — PRODUCES
`In` to encode into the OUTGOING request; `Out` is then decoded
MECHANICALLY (no Fn) from the actual reply's vars, via a `DecodeOut`
closure on `ClientMiddlewareHandler`, exactly mirroring `rest.
ClientMiddlewareHandler.DecodeOut`'s identical "no Fn, no reply-inspection
Fn needed" design. reqreply's VOCABULARY (which var buckets exist to
merge into/from) mirrors events' topic vars for topic-derived data, PLUS
a NEW, protocol-neutral **property** axis (resolved this round — see
below) — NOT REST's richer header/cookie/query surface, since reqreply's
wire boundary is fundamentally "one topic template plus optional
protocol-native metadata," not HTTP's multi-part request shape.

### The "property" vocabulary axis — resolved this round

MQTT5 User Properties and (per [AMQP 0.9.1 Adapter](amqp-adapter.md)'s
own roadmap) AMQP's native message headers are THE SAME cross-transport
CONCEPT: named metadata carried separately from the payload, wire-
realized differently per protocol. This is NOT an MQTT5-specific idea —
confirmed via code that `codex.Param`/`MergedParam[T]`/`NewParam[T,V]`
(`codex/param.go`) are the SHARED primitives `TopicParam`/`HeaderParam`/
`CookieParam`/`QueryParam` ALL already wrap — a NEW `PropertyParam`/
`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` triple can mirror
`TopicParam` exactly (same wrapper shape), MINUS the "must appear in the
topic template" validation topic vars require (a property name is
looked up in an adapter-supplied map, not parsed from the topic string).
`codex.DecodeVars`/`EncodeVars` already operate on ANY generic
`map[string]string` regardless of provenance — no core merge-mechanism
change needed, only a NEW named merge-field slice + constructor,
mirroring topic vars' existing shape.

**Round 3 correction**: an earlier revision of this sketch gave
`MergedPropertyParam[T]` its OWN separate `Field codex.FieldCodec[T]`
field alongside embedding `PropertyParam`. That doesn't actually mirror
`MergedTopicParam[Req]` — confirmed via `api/reqreply/route.go`'s real
definition, `type MergedTopicParam[Req any] struct { codex.MergedParam
[Req] }`, a SINGLE embed. `codex.MergedParam[T]` already carries BOTH
`Param` and `Field FieldCodec[T]` internally (`codex/param.go`), so a
separate `Field` on `MergedPropertyParam[T]` would be disconnected from
`codex.NewParam`'s real merge machinery — dead weight, never populated.
Corrected below to the real one-embed shape, and the sketch now also
includes the methods `TopicParam`/`MergedTopicParam[Req]` actually have
that were missing before: `WithCodec` (plain `PropertyParam`'s
escape-hatch, since `PropertyParam{Name: ..., Description: ...}` struct
literals need a way to attach a codec afterward), `WithDescription`
(`MergedPropertyParam[T]`'s only way to add a description, since
`NewPropertyParam` takes none), and the internal `toParam`/`applyRoute`
route-builder wiring pair mirroring `TopicParam`/`MergedTopicParam[Req]`
exactly (routed into a NEW `rb.propertyParams []PropertyParam` slice on
`routeBuilder`, parallel to its existing `topicParams` slice):

**Round 11 addition — `Required bool` field, resolving a real,
previously-unexamined design gap.** Unlike a topic var (which MUST
appear in the topic template, hence `TopicParam` correctly has no
`Required` field at all — "always required" is structurally
guaranteed), a property (MQTT5 User Property) is conceptually OPTIONAL
metadata — a message may legitimately omit one. Confirmed via
`codex.NewParam`'s real implementation that it HARDCODES
`Field: RequiredField(name, codec, get, set)` — every `MergedParam[T]`
built via `NewParam` (and thus, as originally sketched, every
`MergedPropertyParam[T]` via `NewPropertyParam`) is unconditionally
required, with NO way to declare an optional one. Confirmed
`codex.OptionalField` already exists as `RequiredField`'s counterpart —
the underlying capability is already there, just never exposed through
`NewParam`'s API. Fixed by adding an explicit `Required bool` field
directly to `PropertyParam`/`MergedPropertyParam[T]` (mirroring
`HeaderParam.Required bool` exactly) — NOT to the shared
`codex.Param`/`codex.MergedParam[T]`, keeping `TopicParam`'s own
no-Required-field design untouched — plus a NEW
`NewOptionalPropertyParam[T,V]` constructor, hand-built directly against
`codex.OptionalField` (no `codex` package changes needed at all, since
`MergedParam[T]`'s `Param`/`Field` are already exported):

```go
// PropertyParam describes a named piece of protocol-native metadata
// (MQTT5 User Property, future AMQP message header, ...) — the
// validate-only escape hatch, mirrors [TopicParam] exactly but with NO
// "must appear in the topic template" check (there is no template to
// check against), PLUS a Required field mirroring [HeaderParam.Required]
// (properties, unlike topic vars, are conceptually optional metadata).
// Wraps [codex.Param] directly — same shared primitive
// TopicParam/HeaderParam/CookieParam/QueryParam already use.
type PropertyParam struct {
    codex.Param
    Required bool
}

// WithCodec attaches a codec to p and returns the updated value —
// mirrors [TopicParam.WithCodec] exactly; the only way to add runtime
// validation to a PropertyParam built via a bare struct literal.
func (p PropertyParam) WithCodec(c codex.Codec[string]) PropertyParam { p.Codec = &c; return p }

// applyRoute wires p into rb's property-param list — mirrors
// [TopicParam.applyRoute]'s routeBuilder-option pattern.
func (p PropertyParam) applyRoute(rb *routeBuilder) {
    rb.propertyParams = append(rb.propertyParams, p)
}

// toParam converts p to the shared codex.Param used by the generic
// decode/encode/spec-rendering machinery — mirrors [TopicParam.toParam].
func (p PropertyParam) toParam() codex.Param { return p.Param }

// MergedPropertyParam[T] additionally merges this property's value into
// T — mirrors [MergedTopicParam][T] exactly PLUS the same Required
// field PropertyParam has (set by which constructor built it — see
// NewPropertyParam/NewOptionalPropertyParam below).
type MergedPropertyParam[T any] struct {
    codex.MergedParam[T]
    Required bool
}

// WithDescription sets the PARAMETER-level description and returns the
// updated value — mirrors [MergedTopicParam.WithDescription] exactly;
// the only way to add one, since NewPropertyParam takes no description
// parameter.
func (p MergedPropertyParam[T]) WithDescription(desc string) MergedPropertyParam[T] {
    p.MergedParam = p.MergedParam.WithDescription(desc)
    return p
}

// applyRoute wires p into rb's property-param list — mirrors
// [MergedTopicParam.applyRoute]; carries Required through.
func (p MergedPropertyParam[T]) applyRoute(rb *routeBuilder) {
    rb.propertyParams = append(rb.propertyParams, PropertyParam{Param: p.Param, Required: p.Required})
}

// NewPropertyParam declares a property that is BOTH validated AND
// merge-capable into T, and REQUIRED (errors if absent from the
// adapter-supplied property map) — mirrors [NewTopicParam][T,V]
// exactly, wraps [codex.NewParam][T,V] directly (hardcodes
// RequiredField, same as NewTopicParam/NewParam already do).
func NewPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedPropertyParam[T] {
    return MergedPropertyParam[T]{MergedParam: codex.NewParam(name, codec, get, set), Required: true}
}

// NewOptionalPropertyParam declares a property that is validated AND
// merge-capable into T IF PRESENT — absent from the adapter-supplied
// property map is NOT an error (T's field is simply left at its zero
// value). Hand-built directly against [codex.OptionalField] — NO
// codex package changes needed, since [codex.MergedParam][T]'s Param/
// Field are already exported for exactly this kind of package-local
// construction. This is the property axis's ONE genuine divergence
// from TopicParam (topic vars have no optional variant — a template
// var either appears in the topic string, unconditionally required, or
// doesn't exist at all).
func NewOptionalPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedPropertyParam[T] {
    strCodec := codex.StringValidatorFrom(codec)
    return MergedPropertyParam[T]{
        MergedParam: codex.MergedParam[T]{
            Param: codex.Param{Name: name, Codec: &strCodec},
            Field: codex.OptionalField(name, codec, get, set),
        },
        Required: false,
    }
}
```

`Middleware[In,Out]` gains a SECOND pair of merge-field slices, kept
SEPARATE from `topicMergeFieldsIn`/`topicMergeFieldsOut` (different
validation rules — property names are never checked against a topic
template):

```go
type Middleware[In, Out any] struct {
    middleware.Declaration[In, Out]

    topicMergeFieldsIn     []codex.FieldCodec[In]
    topicMergeFieldsOut    []codex.FieldCodec[Out]
    propertyMergeFieldsIn  []codex.FieldCodec[In]
    propertyMergeFieldsOut []codex.FieldCodec[Out]

    receiveFn func(ctx context.Context, in In) (Out, error)
    sendFn    func(ctx context.Context) (In, error)
}

// WithRequestProperty registers one REQUEST-side property merge field
// into mw's own In vocabulary — mirrors WithRequestTopic exactly, using
// NewPropertyParam instead of NewTopicParam.
func (m Middleware[In, Out]) WithRequestProperty(p MergedPropertyParam[In]) Middleware[In, Out]

// WithResponseProperty is WithRequestProperty's REPLY-side sibling.
func (m Middleware[In, Out]) WithResponseProperty(p MergedPropertyParam[Out]) Middleware[In, Out]
```

**Dispatch-side consequence — CORRECTED this round against REST's
ACTUAL implementation, not just its public signatures**: an earlier
revision of this doc claimed topic vars and property vars get COMBINED
into one map before decoding. That is WRONG — confirmed via
`api/rest/transform.go`'s real `buildDecodeIn`: REST's `MiddlewareHandler.
DecodeIn` takes THREE SEPARATE map parameters
(`func(headerVars, cookieVars, queryVars map[string]string) (any, error)`),
and its body calls `codex.DecodeVars` ONCE PER AXIS, SEQUENTIALLY,
against the SAME target value — never merges the maps themselves. Each
call only touches the fields THAT AXIS declared (via that axis's own
`FieldCodec[In]` slice), so calling `DecodeVars` multiple times against
one `&in` is safe and side-effect-free across axes. reqreply mirrors
this EXACTLY, with its own two axes:

```go
// buildDecodeIn — mirrors rest's identical function, adapted to
// reqreply's two axes (topic, property) instead of REST's three
// (header, cookie, query).
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(topicVars, propertyVars map[string]string) (any, error) {
    return func(topicVars, propertyVars map[string]string) (any, error) {
        var in In
        if len(mw.topicMergeFieldsIn) > 0 {
            if err := codex.DecodeVars(&in, topicVars, mw.topicMergeFieldsIn...); err != nil {
                return nil, MiddlewareInputError{Name: mw.Name, Err: err}
            }
        }
        if len(mw.propertyMergeFieldsIn) > 0 {
            if err := codex.DecodeVars(&in, propertyVars, mw.propertyMergeFieldsIn...); err != nil {
                return nil, MiddlewareInputError{Name: mw.Name, Err: err}
            }
        }
        if err := mw.InCodec.Validate(in); err != nil {
            return nil, MiddlewareInputError{Name: mw.Name, Err: err}
        }
        return in, nil
    }
}
```

`ClientMiddlewareHandler.EncodeIn`/`DecodeOut` mirror this same
per-axis-separate-map shape, confirmed against REST's real signatures:
`EncodeIn func(in any) (topicVars, propertyVars map[string]string, err error)`,
`DecodeOut func(topicVars, propertyVars map[string]string) (any, error)`.

`codex.DecodeVars`/`EncodeVars` themselves need NO change (already
provenance-agnostic about where a `map[string]string` comes from). The
ADAPTER is responsible for supplying the property-value map however it
can: `adapters/mqtt5` builds it from the real message's User Properties
(the SAME extraction `UserPropertyParam`/`FromUserPropertyParam` already
do for the flat mechanism); a future `adapters/amqp` would build it from
the message's native headers property; `adapters/zeromq`/`mqtt`(v3)
simply pass an empty map — a route declaring a REQUIRED property on one
of those adapters then naturally fails with the SAME
`MiddlewareInputError` a missing topic var would produce, no
adapter-specific error type or special-casing needed anywhere in the
core dispatch logic. `topicVars` is unaffected by this correction —
supplied exactly as it already is for the existing `WithRequestTopic`/
`WithResponseTopic` axis.

```go
package reqreply

// Middleware is a codec-backed, reqreply-specific middleware declaration
// — the per-pattern counterpart to [middleware.Declaration], adding
// reqreply's own topic-var AND property merge vocabularies (see "The
// 'property' vocabulary axis" above for the full Middleware[In,Out]
// struct sketch — repeated here is only the method surface). Unlike
// events (whose Subscribe/Publish roles are asymmetric — only one of
// In/Out is ever used per role), reqreply's SINGLE Route sees BOTH
// directions in one round-trip (mirrors REST's Route exactly) — so BOTH
// the In-side AND Out-side merge fields (topic AND property) are used
// together, on the SAME Middleware value, exactly as REST's request/
// response pair already works.

// NewMiddleware builds a Middleware from a middleware.Declaration.
func NewMiddleware[In, Out any](decl middleware.Declaration[In, Out]) Middleware[In, Out]

// WithRequestTopic registers one REQUEST-side topic-var merge field into
// mw's own In vocabulary — reuses the EXISTING NewTopicParam[In]
// constructor directly (no new reqreply-side param type). Only
// meaningful for a topic var the route's OWN template declares but that
// the route's OWN Req does NOT already merge via its own NewTopicParam.
func (m Middleware[In, Out]) WithRequestTopic(p MergedTopicParam[In]) Middleware[In, Out]

// WithResponseTopic is WithRequestTopic's REPLY-side sibling — registers
// one topic-var merge field into mw's own Out vocabulary, encoded into
// the reply's topic vars once Transform's fn (or a bundled WithReceive)
// produces an Out value, OR decoded from the reply's topic vars on the
// client side (mechanical, no Fn — mirrors rest.ClientMiddlewareHandler.
// DecodeOut exactly).
func (m Middleware[In, Out]) WithResponseTopic(p MergedTopicParam[Out]) Middleware[In, Out]

// WithRequestProperty/WithResponseProperty are WithRequestTopic/
// WithResponseTopic's property-axis siblings — see "The 'property'
// vocabulary axis" above for the full rationale and signatures.

// WithReceive/WithSend bundle a route-AGNOSTIC Fn directly onto mw,
// enabling plain .Use(mw) attachment (reusable verbatim across routes) —
// mirrors rest.Middleware's identical SERVER/CLIENT split (WithReceive:
// server-side, produces Out from In; WithSend: client-side, produces In
// to encode into the outgoing request — mirrors [Transform]/
// [ClientTransform]'s own fn shapes with the *Req/Req parameter dropped).
func (m Middleware[In, Out]) WithReceive(fn func(ctx context.Context, in In) (Out, error)) Middleware[In, Out]
func (m Middleware[In, Out]) WithSend(fn func(ctx context.Context) (In, error)) Middleware[In, Out]

func (Middleware[In, Out]) RouteMiddlewareMarker() {}
```

**No `.Use()` backward-compat widening needed — CONFIRMED, not a gap
(Round 12 finding 3).** D-0003's own test plan requires a "backward-
compatibility regression: an EXISTING `.Use(securityScheme)` call site
continues to pass unchanged after `Use`'s parameter type widens to
`middleware.RouteMiddleware`" — a REAL, necessary migration REST had to
make (REST predates D-0003). Confirmed via real code that reqreply's
`Route.Use` (`api/reqreply/middleware.go:46`) and events'
`Subscriber[T].Use`/`Publisher[T].Use` (`api/events/builder.go:1889`)
BOTH already take `...middleware.RouteMiddleware` — the shared marker
interface `middleware.Middleware` (Phase 1/1b's flat type) ALREADY
implements via its own `RouteMiddlewareMarker()` method
(`middleware/middleware.go`). Since reqreply's Phase 1b was built
AFTER D-0003 established this pattern, `Middleware[In,Out]`'s OWN
`RouteMiddlewareMarker()` method (above) is ALL that's needed for
`.Use()` to accept it alongside the existing flat type — no signature
change, no backward-compat regression risk, nothing to test beyond the
ALREADY-covered `TestRoute_Use_BundledWithReceive_AgnosticAttachment`.

```go
// Transform attaches mw's declaration AND its runtime fn to r in ONE
// call — the route-BOUND, SERVER-side attachment point, mirrors
// rest.Transform's EXACT shape. fn receives ctx, the route's OWN
// already-decoded *Req (POINTER — fn may read AND enrich it with derived
// data the wire request never carried), and mw's own decoded+validated
// In (declaratively extracted from REQUEST-side topic vars the route's
// Req does NOT model) — fn PRODUCES mw's own Out value, which mw's OWN
// reply-topic merge fields then encode into the REPLY's topic vars.
// Dispatches AFTER the paired security Fn (if any), both still
// pre-handler — confirmed via adapters/nethttp/serve.go's real dispatch
// order: security runs FIRST (runSecurityMiddlewareReflect), THEN
// declared MiddlewareHandlers run SECOND (runMiddlewareHandlersReflect)
// — mirrors D1's REST/events precedent exactly, same explicit order for
// reqreply's own adapters.
func Transform[Req, Resp, In, Out any](
    r Route[Req, Resp],
    mw Middleware[In, Out],
    fn func(ctx context.Context, req *Req, in In) (Out, error),
) Route[Req, Resp]

// ClientTransform is Transform's route-BOUND, CLIENT-side counterpart —
// mirrors rest.ClientTransform's EXACT shape. fn PRODUCES mw's own In
// value from req (the caller's OWN already-built value, VALUE not
// pointer — the caller already owns and can mutate its own Req directly
// before calling Call at all), encoded into the OUTGOING request's topic
// vars via mw's own request-topic merge fields. AFTER the reply arrives,
// mw's own reply-topic merge fields MECHANICALLY decode Out from the
// reply's actual topic vars — no Fn needed for this half, mirrors
// rest.ClientMiddlewareHandler.DecodeOut exactly.
func ClientTransform[Req, Resp, In, Out any](
    r Route[Req, Resp],
    mw Middleware[In, Out],
    fn func(ctx context.Context, req Req) (In, error),
) Route[Req, Resp]
```

**Multiple attachments per route — CONFIRMED this round (Round 12),
mirroring D-0003's own D6(a)/D6(b)/D6(c) sub-decisions, ALL of which
presuppose this capability.** Confirmed via REAL code
(`api/rest/transform.go`'s `Transform`): `r.opts = append(slices.Clone(
r.opts), middlewareHandlerOpt{...}, middlewareSpecContributionOpt{...})`
— REST's `Transform` APPENDS, never replaces, so chaining MULTIPLE
`Transform`/`ClientTransform` calls onto the SAME route is a real,
INTENDED, supported capability, not an incidental side-effect.
reqreply's own `Transform`/`ClientTransform` mirror this EXACTLY —
`RouteHandle.MiddlewareHandlers`/`ClientMiddlewareHandlers` (both
slices, below) accumulate ACROSS calls, in registration order. This
directly enables D6(a) (two independent `Middleware` values reading
the SAME name — fine, no conflict) and D6(c) (two attached
middlewares' own `fn`s both enriching the SAME `*Req` field —
attachment-order, last-applied-wins, not an error) — see "Unit test
plan" for the new tests confirming this end-to-end.

`RouteHandle` gains two new fields, mirroring `ChannelHandle.
MiddlewareHandlers`/`ClientMiddlewareHandlers` exactly:

```go
type RouteHandle[Req, Resp any] struct {
    // ... existing fields unchanged ...
    MiddlewareHandlers       []MiddlewareHandler
    ClientMiddlewareHandlers []ClientMiddlewareHandler
}
```

`MiddlewareHandler`/`ClientMiddlewareHandler` mirror `rest`'s identical
type-erased runtime dispatch units — `MiddlewareHandler{Name, DecodeIn,
Fn any, Agnostic, dualAttached}` (server-side: `DecodeIn` takes request
topic vars AND adapter-supplied property vars as TWO SEPARATE map
parameters — never combined into one map, per the correction above —
`Fn` produces `Out` for the reply); `ClientMiddlewareHandler{Name, Fn any,
EncodeIn, DecodeOut, Agnostic, dualAttached}` (client-side: `Fn` produces
`In`, `EncodeIn` merges it into the outgoing request's topic AND
property vars (as SEPARATE map return values, mirroring REST's own
`EncodeIn func(in any) (headers, cookies, query map[string]string, err
error)` shape exactly), `DecodeOut` mechanically decodes `Out` from the
reply's topic AND property vars (again as SEPARATE map PARAMETERS, never
one combined map)) — see `api/rest/transform.go` for the exact shape
this doc's implementation ports (closer structural match than events',
since reqreply's `RouteHandle` — like REST's — carries both directions
on one type). See "The 'property' vocabulary axis" above (specifically
its `buildDecodeIn` sketch) for exactly how the adapter supplies the
property-value map, kept SEPARATE from the topic-var map throughout.

## Value precedence: middleware-derived wins (mirrors D-0003's D3)

**Gap found and closed this round (Round 7).** Decision #5's
`ConflictingParamContributionError` only catches a DECLARATION-time
attribute mismatch (kind/required-ness differing for the SAME var
name) — it says nothing about which VALUE wins at RUNTIME when the
route's OWN topic/property merge (from `Req`/`Resp`, via the route's
own `NewTopicParam`) and a `Middleware`'s `WithRequestTopic`/
`WithResponseTopic`/`WithRequestProperty`/`WithResponseProperty` (from
the middleware's OWN `In`/`Out`) both target the SAME var name with
AGREEING declared attributes but potentially DIFFERING runtime values
— they're read from two DIFFERENT typed values, after all. This is
genuinely analogous to D-0003's own D3 ("client-side precedence"),
which resolves the EQUIVALENT question for REST with a concrete,
SHIPPED 3-tier rule — confirmed via `adapters/nethttp/client.go`'s real
`overrideDerived` chain (~lines 974-981): **explicit `CallOptions` >
middleware-derived (`ClientTransform`'s `In`) > route-own-derived**,
applied identically on the SERVER'S response-encode side too (per
`adapters/nethttp/serve.go`'s own "registration-order, last-applied-
wins" comment, ~line 500) — middleware ALWAYS wins over the route's own
value on a name conflict, on BOTH directions.

reqreply mirrors this precedent, but with only TWO tiers instead of
REST's three: reqreply's `ClientCallOptions` (confirmed via
`api/reqreply/client.go`) has NO vars-override field today — no
explicit-override tier exists for reqreply calls, so there is nothing
above middleware-derived to rank against. **Decision: middleware-derived
values ALWAYS override route-own-derived values for the SAME var
name** — on `Transform`'s reply-encode side (a `Middleware`'s
`WithResponseTopic`/`WithResponseProperty` value wins over the route's
own `Resp`-derived value) AND on `ClientTransform`'s request-encode
side (a `Middleware`'s `WithRequestTopic`/`WithRequestProperty` value
wins over the route's own `Req`-derived value) — mirroring REST's own
"registration-order, last-applied-wins" rule exactly, just without the
extra explicit-override tier REST's `CallOptions` provides. Should
reqreply ever gain an explicit per-call vars-override mechanism (not
currently planned, not part of this doc's scope), that new tier would
slot in ABOVE middleware-derived, mirroring REST's `CallOptions`'s role
exactly — noted here for future reference, not a blocker now.

## AsyncAPI spec rendering

**Confirmed real gap in an earlier revision of this doc — now
addressed.** REST's D-0003 mechanism doesn't just drive runtime dispatch
— `Transform`/`ClientTransform` ALSO layer their declared merge-field
params into the OpenAPI spec, via `middlewareSpecContribution`/
`boundSpecContributionOf` (`api/rest/transform.go`), fed into the SAME
`applyParamDeclarations` conflict-detection/layering pass the OLDER flat
mechanism's params already use (D4 — see `docs/design/
d-0003-codec-declared-middlewares.md`). reqreply needs the SAME
integration, for TWO reasons: (1) parity with REST's own precedent, and
(2) parity with reqreply's OWN Phase 1b, which ALREADY renders
header-as-middleware declarations (the flat mechanism's `.Use(mqtt5.
FromUserPropertyParam(...))`) into the request/reply message's AsyncAPI
`headers` schema (`api/reqreply/middleware.go`'s existing
`applyParamDeclarations`/`headerParamProperty`). Leaving THIS NEW
mechanism's property axis un-rendered would be a real, visible
regression relative to what Phase 1b already ships.

Concretely: `Transform`/`ClientTransform` need to feed a
`reqreplyMiddlewareSpecContribution` (mirrors REST's
`middlewareSpecContribution` exactly — `Name`, the declared
`PropertyParam`/`TopicParam` values in spec-only form, `dualAttached`
for D7) into a NEW step inside `Route.Register`, unified with Phase
1b's EXISTING `applyParamDeclarations`/`headerParamProperty` code path
— NOT a separate, second AsyncAPI-rendering mechanism living alongside
it. Concretely, the property axis's contributions should be
DEDUPED/MERGED into the SAME `reqHeaders`/`respHeaders` `schema.Schema`
values Phase 1b's `applyParamDeclarations` already builds and passes to
`Builder.registerRoute` — topic-var contributions render via the
EXISTING topic-param spec path (`buildTopicParameters`,
`Builder.registerRoute`'s own `Parameters` map), unchanged, since
`WithRequestTopic`/`WithResponseTopic` don't introduce any NEW spec
surface beyond what `NewTopicParam` already renders. Only the property
axis needs this new unification work.

**Round 16 addition — `Required` must propagate into the schema's
`required` array too, not just runtime validation.** Traced Phase 1b's
REAL `applyParamDeclarations` (`api/reqreply/middleware.go:191`) in
full: it builds `reqRequired`/`respRequired []string` slices,
appending a param's `Name` to them WHENEVER `p.Required` is true, then
constructs `schema.Schema{Type: "object", Properties: ..., Required:
reqRequired}` — so `Required` genuinely determines WHICH property
names appear in the AsyncAPI `headers` schema's `required` array, not
just whether `MiddlewareInputError` fires at runtime. This unification
step MUST carry the property axis's OWN `PropertyParam.Required`
(Round 11) through the SAME way: a contribution built via
`NewOptionalPropertyParam` (`Required: false`) must NOT appear in
`reqRequired`/`respRequired`, exactly mirroring how an EXISTING
`Required: false` header-as-middleware declaration already behaves
today — a required property (`NewPropertyParam`, `Required: true`)
DOES appear, same as today's required header declarations.

**Conflict detection (decision #5, RESOLVED this round; REVISED Round
18 — see below)**: when TWO different `Middleware[In,Out]` values (or
a `Middleware[In,Out]` contribution AND Phase 1b's flat `.Use(mqtt5.
FromUserPropertyParam(...))` contribution) declare the SAME property
name with DIFFERENT attributes (e.g. one says `Required: true`, the
other `Required: false`, or different codecs), this ERRORS — mirroring
REST's own `checkParamConflicts` behavior. Two contributions agreeing
on the SAME name/attributes still dedupe into ONE spec entry (not an
error) — only a genuine MISMATCH errors.

**Round 18 — REVISED to a DELIBERATE BREAKING CHANGE, resolving a real
self-contradiction an earlier revision of this section had.** That
earlier revision claimed BOTH that this conflict check is "unified with
Phase 1b's EXISTING `applyParamDeclarations`" (implying ONE shared
algorithm) AND that "Phase 1b's existing, laxer flat-mechanism behavior
is NOT retroactively changed to match" (implying TWO different
comparison rules depending on which mechanism a contribution came
from) — these cannot both be true without an UNSTATED origin-tracking
rule the doc never actually specified. Presented with this choice, the
user chose the SIMPLER option, explicitly accepting the breaking-change
cost: **ONE uniform conflict-detection algorithm applies to ALL
contributions — Phase 1b's flat-mechanism params AND the NEW
`Middleware[In,Out]` axis's params alike — with NO origin-based
special-casing.** Two contributions disagreeing on `Required`/codec for
the SAME name ALWAYS error now, regardless of which mechanism declared
them. **Phase 1b's own silent-first-seen-wins dedupe for MISMATCHED
declarations is RETIRED, not preserved** — a deliberate, accepted
breaking change, not an avoided one, per the explicit "breaking changes
are acceptable when they yield a simpler, more maintainable workflow"
principle this session has used elsewhere. One algorithm, one mental
model — genuinely simpler to implement and reason about than tracking
contribution origin through every comparison.

**Scope of the break, precisely — narrow, not broad.** ONLY the
conflict-detection dedupe algorithm for MISMATCHED declarations sharing
a name changes. Phase 1b's flat mechanism ITSELF
(`FromUserPropertyParam`/`FromResponseUserPropertyParam` functions,
`.Use()` attachment, spec rendering for AGREEING declarations) is
COMPLETELY UNCHANGED — still fully functional exactly as shipped (see
"Out of scope" below for the precise carve-out). Only routes that
CURRENTLY have two Phase-1b-only declarations for the SAME property
name with DIFFERING `Required`/codec (previously silently tolerated via
first-seen-wins) would start failing to `Register` at all once this
ships — a narrow, specific, and rare behavioral change (an existing
caller would need to have ALREADY had two disagreeing declarations for
the same name, arguably a latent bug in their own code today), not a
broad one.

**Round 15 — TWO further refinements to conflict detection, both
verified against REST's REAL code (`api/rest/middleware.go`) for the
FIRST time this session, and both RESOLVED as deliberate divergences
from REST's exact behavior:**

1. **Namespace scope — topic-vars and properties are INDEPENDENT
   namespaces, never cross-checked.** Confirmed REST's real
   `applyParamDeclarations` puts header/cookie/query into ONE combined
   `map[string][]paramContribution`, keyed by `Name` ONLY — so a header
   named "X" and a query param named "X" on the SAME route WOULD be
   flagged as conflicting (`kind` differs) by REST's actual shipped
   `checkParamConflicts`. User's decision: reqreply/events do NOT
   mirror this cross-kind strictness for topic-vars vs. properties —
   TWO SEPARATE contribution maps (`topicContributions
   map[string][]paramContribution`, `propertyContributions
   map[string][]paramContribution`), each conflict-checked
   INDEPENDENTLY, NEVER cross-checked against each other. A topic var
   "tenantID" and a property "tenantID" declared on the SAME route are
   explicitly FINE together, no error — a stronger justification than
   REST's own header/cookie/query boundary exists here, since topic
   vars and properties come from genuinely DIFFERENT wire locations
   (the topic template string vs. out-of-band message metadata), not
   just different parts of one HTTP request.
2. **Codec comparison — DELIBERATELY goes beyond REST's real
   precedent.** Confirmed REST's real `paramContribution` struct
   (`source string; kind string; required bool`) has NO `Codec` field
   at all — `checkParamConflicts`'s actual comparison is `kind !=
   first.kind || required != first.required`, meaning REST's shipped
   code NEVER compares codecs, despite this doc's own EARLIER prose
   (above) claiming "different codecs" as an example — that claim
   overstated REST's real behavior. User's decision: reqreply/events'
   NEW conflict detection intentionally goes FURTHER than REST's real
   precedent — since `codex.Codec[T]`'s `Encode`/`Decode` are funcs
   (not meaningfully comparable), the practical check compares each
   contribution's `Codec.Schema` (`schema.Schema`) via
   `reflect.DeepEqual`. Two contributions for the SAME name (within the
   SAME namespace) conflict if: `Required` differs, OR exactly one has
   a nil `Codec` (nil vs non-nil is itself a mismatch — differing
   validation strictness), OR both are non-nil and
   `!reflect.DeepEqual(a.Codec.Schema, b.Codec.Schema)`. This is an
   EXPLICIT, FLAGGED divergence — not a misreading of REST's behavior —
   made because this is brand-new code with no existing callers to
   break, unlike REST's own `checkParamConflicts` (see the new
   companion roadmap doc `rest-middleware-conflict-detection-
   improvements.md` for whether REST's OWN precedent should eventually
   catch up).

**Round 11 update — the `Required: true` vs `Required: false` example
is now GENUINELY meaningful, not a copy-paste artifact.** Before Round
11 added `PropertyParam`/`MergedPropertyParam[T]`'s own `Required bool`
field, NEITHER `TopicParam` NOR `PropertyParam` had any `Required`
field to actually disagree on — this example was carried over from
REST's real `HeaderParam.Required` without a genuinely analogous case
to check. Now that `NewPropertyParam` (required) and
`NewOptionalPropertyParam` (optional) exist as two DIFFERENT ways to
declare the SAME property name, a REAL required-ness mismatch is
possible for properties (still N/A for topic vars, which remain always-
required with no optional variant) — `checkParamConflicts` must compare
`Required` for property contributions specifically.

## Reuse patterns — CONFIRMED intended, stated explicitly (Round 13)

**Added this round specifically per the user's "consistent, simple,
declarative workflow" goal.** The type-level design already supports
several rich reuse patterns BY CONSTRUCTION — this section states them
explicitly as INTENDED capabilities (not accidents of the type system)
so users can rely on them confidently, mirroring D-0003's own emphasis
on proving reuse, not just compiling.

**Cross-route/channel reuse with DIFFERENT `Req`/`T` types**: a bundled
`Middleware` (`WithReceive`/`WithSend` set, no `Transform`/
`ClientTransform` involved) has ZERO reference to any specific `Req`/
`Resp`/`T` — `receiveFn func(ctx, in In) (Out, error)` only mentions
`In`/`Out`. This means the LITERAL SAME `mw` value can be declared
ONCE and attached via plain `.Use(mw)` to MANY routes/channels with
COMPLETELY DIFFERENT `Req`/`Resp`/`T` types — the exact same DRY
promise D-0003 itself singles out as "the test that actually proves
route-agnostic reuse" (see "Unit test plan" for the new tests
confirming this for reqreply AND events).

**Cross-API `Declaration[In,Out]` reuse**: `rest.NewMiddleware`,
`events.NewMiddleware`, and reqreply's own `NewMiddleware` (this doc)
ALL wrap the SAME shared `middleware.Declaration[In,Out]` type — so a
user can build ONE `Declaration` and wrap it separately per API,
reusing the SAME core codec/validation logic across REST, events, AND
reqreply, each with its own vocabulary-axis wrapper — mirroring the
FLAT mechanism's own cross-API OAuth2 sharing pattern (already
demonstrated in `examples/reqreply-api`'s Demo 9), now available for
this SECOND, codec-declared mechanism too:

```go
// ONE shared Declaration, wrapped three times — one core validation
// concern, three API-specific vocabulary attachments.
authDecl := middleware.NewDeclaration(authInCodec, authOutCodec, "auth-check")

restMW := rest.NewMiddleware(authDecl).WithRequestHeader(...)
eventsMW := events.NewMiddleware(authDecl).WithSubscribeTopic(...)
reqreplyMW := reqreply.NewMiddleware(authDecl).WithRequestProperty(...)
```

**Cross-role reuse within events (Subscribe AND Publish)**: `WithReceive`
sets `m.receiveFn`, `WithSend` sets `m.sendFn` — INDEPENDENT,
non-exclusive fields on the SAME `Middleware` value. A single value
carrying BOTH bundled Fns can be attached via `.Use(mw)` to a
`Subscriber[T]` (which extracts only `receiveFn` via its own
`applyAgnosticSubscriber()`) AND, separately, to a `Publisher[T]`
(which extracts only `sendFn` via `applyAgnosticPublisher()`) — ONE
declared concern, reused across BOTH roles of a channel, with zero
interference between the two extraction paths. reqreply has no
equivalent (its SINGLE `Route` sees both directions in one round-trip
already, via `Transform`'s own `fn`, so there's no separate
"role" to reuse across).

## Phase 0: the property axis ships for `api/reqreply` AND `api/events` TOGETHER

**Restructured this round (Round 8) — promoted from "deferred
companion follow-up" to Phase 0, implemented in the SAME round as
reqreply's own property axis, specifically to guarantee consistency
from day one rather than risk two independently-evolved designs.**
`api/events`' own `Middleware[In,Out]` (shipped by D-0003) has the
IDENTICAL gap this doc closes for reqreply: only a topic-var vocabulary
exists (`WithSubscribeTopic`/`WithPublishTopic`) — no declarative
property/header bridge at all, not even a flat-mechanism equivalent of
reqreply's own Phase 1b `FromUserPropertyParam`/
`FromResponseUserPropertyParam` (confirmed via grep: those functions
only exist in `adapters/mqtt5/reqreply_transport.go`, never for events'
`SubscribeMW`/`PublishMW`). Since events has NO existing flat-mechanism
property bridge to unify with (unlike reqreply's Phase 1b), events'
new property axis renders its OWN, standalone AsyncAPI contribution —
simpler than reqreply's case in that one respect.

### `events.PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]`

Confirmed via `api/events/builder.go` that `events.TopicParam`/
`MergedTopicParam[T]` have the IDENTICAL shape to reqreply's own
(`codex.MergedParam[T]` single embed, same `WithCodec`/`WithDescription`
signatures) — so events' `PropertyParam` triple is a DIRECT, mechanical
copy of reqreply's own sketch above (see "The 'property' vocabulary
axis"), with `T` in place of reqreply's `Req`/`Out` naming, no
structural differences:

**Round 11 addition, mirrored from reqreply's own fix**: `Required
bool` added to BOTH `PropertyParam`/`MergedPropertyParam[T]` (NOT to
shared `codex.Param`), plus a NEW `NewOptionalPropertyParam[T,V]`
constructor — see reqreply's own "Round 11 addition" note above for
the full rationale (properties are conceptually optional metadata,
`codex.NewParam` hardcodes `RequiredField`, `codex.OptionalField`
already exists as the counterpart, zero `codex` package changes needed).

```go
// PropertyParam — IDENTICAL shape to reqreply.PropertyParam (see "The
// 'property' vocabulary axis" above, including Round 11's Required
// field addition); wraps codex.Param directly.
type PropertyParam struct {
    codex.Param
    Required bool
}

func (p PropertyParam) WithCodec(c codex.Codec[string]) PropertyParam { p.Codec = &c; return p }

func (p PropertyParam) applyChannel(cb *channelBuilder) {
    cb.propertyParams = append(cb.propertyParams, p)
}

func (p PropertyParam) toParam() codex.Param { return p.Param }

// MergedPropertyParam[T] — IDENTICAL shape to reqreply.MergedPropertyParam[T];
// a SINGLE embed of codex.MergedParam[T], plus Required.
type MergedPropertyParam[T any] struct {
    codex.MergedParam[T]
    Required bool
}

func (p MergedPropertyParam[T]) WithDescription(desc string) MergedPropertyParam[T] {
    p.MergedParam = p.MergedParam.WithDescription(desc)
    return p
}

func (p MergedPropertyParam[T]) applyChannel(cb *channelBuilder) {
    cb.propertyParams = append(cb.propertyParams, PropertyParam{Param: p.Param, Required: p.Required})
}

// NewPropertyParam — IDENTICAL shape to reqreply.NewPropertyParam[T,V]; REQUIRED.
func NewPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedPropertyParam[T] {
    return MergedPropertyParam[T]{MergedParam: codex.NewParam(name, codec, get, set), Required: true}
}

// NewOptionalPropertyParam — IDENTICAL shape to
// reqreply.NewOptionalPropertyParam[T,V]; hand-built against
// codex.OptionalField, absent-from-map is NOT an error.
func NewOptionalPropertyParam[T, V any](
    name string,
    codec codex.Codec[V],
    get func(T) V,
    set func(*T, V),
) MergedPropertyParam[T] {
    strCodec := codex.StringValidatorFrom(codec)
    return MergedPropertyParam[T]{
        MergedParam: codex.MergedParam[T]{
            Param: codex.Param{Name: name, Codec: &strCodec},
            Field: codex.OptionalField(name, codec, get, set),
        },
        Required: false,
    }
}
```

### `events.Middleware[In,Out]` gains property merge fields — mirrors its OWN existing asymmetric topic-var split

Confirmed via `api/events/middleware_declaration.go`'s real struct:
events' `Middleware[In,Out]` ALREADY has `topicMergeFieldsIn
[]codex.FieldCodec[In]`/`topicMergeFieldsOut []codex.FieldCodec[Out]` as
TWO SEPARATE fields, used ASYMMETRICALLY per role — Subscribe uses ONLY
`In`/`topicMergeFieldsIn` (Out is unused); Publish uses ONLY
`Out`/`topicMergeFieldsOut` (In is unused) — unlike reqreply/REST, where
a SINGLE `Middleware` value uses BOTH `In` AND `Out` together on one
Route. The property axis mirrors this SAME asymmetric pattern exactly,
adding `propertyMergeFieldsIn`/`propertyMergeFieldsOut` alongside the
existing topic fields:

```go
type Middleware[In, Out any] struct {
    middleware.Declaration[In, Out]

    topicMergeFieldsIn     []codex.FieldCodec[In]
    topicMergeFieldsOut    []codex.FieldCodec[Out]
    propertyMergeFieldsIn  []codex.FieldCodec[In]
    propertyMergeFieldsOut []codex.FieldCodec[Out]

    receiveFn func(ctx context.Context, in In) error
    sendFn    func(ctx context.Context) (Out, error)
}

// WithSubscribeProperty registers one property merge field into mw's In
// vocabulary — mirrors WithSubscribeTopic exactly, using NewPropertyParam
// instead of NewTopicParam. Meaningful ONLY for Subscribe attachment
// (mirrors topicMergeFieldsIn's own Subscribe-only use).
func (m Middleware[In, Out]) WithSubscribeProperty(p MergedPropertyParam[In]) Middleware[In, Out]

// WithPublishProperty registers one property merge field into mw's Out
// vocabulary — mirrors WithPublishTopic exactly. Meaningful ONLY for
// Publish attachment (mirrors topicMergeFieldsOut's own Publish-only use).
func (m Middleware[In, Out]) WithPublishProperty(p MergedPropertyParam[Out]) Middleware[In, Out]
```

### `events.transform.go`'s `buildDecodeIn`/`buildEncodeOut` gain a property-vars map parameter

Confirmed via `api/events/transform.go`'s real signatures:
`buildDecodeIn[In, Out any](mw) func(topicVars map[string]string) (any,
error)` and `buildEncodeOut[In, Out any](mw) func(outAny any)
(map[string]string, error)` — SINGLE-map shape today (topic vars only,
events has only one wire location — the topic — per role today). Both
gain a SEPARATE `propertyVars` parameter/return value, mirroring
reqreply's/REST's own "never combine, always separate maps per axis"
pattern (Round 2's correction, applied consistently here too):

```go
// buildDecodeIn — extended with a SEPARATE propertyVars map parameter,
// mirroring reqreply's/REST's multi-map-never-combined pattern.
func buildDecodeIn[In, Out any](mw Middleware[In, Out]) func(topicVars, propertyVars map[string]string) (any, error) {
    return func(topicVars, propertyVars map[string]string) (any, error) {
        var in In
        if len(mw.topicMergeFieldsIn) > 0 {
            if err := codex.DecodeVars(&in, topicVars, mw.topicMergeFieldsIn...); err != nil {
                return nil, MiddlewareInputError{Name: mw.Name, Err: err}
            }
        }
        if len(mw.propertyMergeFieldsIn) > 0 {
            if err := codex.DecodeVars(&in, propertyVars, mw.propertyMergeFieldsIn...); err != nil {
                return nil, MiddlewareInputError{Name: mw.Name, Err: err}
            }
        }
        if err := mw.InCodec.Validate(in); err != nil {
            return nil, MiddlewareInputError{Name: mw.Name, Err: err}
        }
        return in, nil
    }
}

// buildEncodeOut — extended with a SEPARATE propertyVars return value.
func buildEncodeOut[In, Out any](mw Middleware[In, Out]) func(outAny any) (topicVars, propertyVars map[string]string, err error) {
    // ... mirrors buildDecodeIn's structure, encode-side.
}
```

`MiddlewareHandler.DecodeIn`/`ClientMiddlewareHandler.EncodeOut` (both in
`api/events/transform.go`) update to the new two-map signatures
accordingly — adapters supply the property-value map exactly as
reqreply's do (real message's User Properties for mqtt5, empty map for
adapters with no property mechanism).

### AsyncAPI spec rendering — simpler than reqreply's case

Since events has NO existing flat-mechanism property bridge (confirmed
above), the property axis's contributions render as a NEW, standalone
AsyncAPI `Message.Headers`-equivalent schema entry — no unification
step needed with a pre-existing mechanism (unlike reqreply, which must
unify with Phase 1b's existing `applyParamDeclarations`).

**Round 16 addition — `Required` must propagate into this standalone
schema's `required` array too, mirroring reqreply's own identical Round
16 fix immediately above.** Since events builds this schema from
SCRATCH (no pre-existing mechanism to inherit the behavior from), this
needs EXPLICIT confirmation, not an assumption: a contribution built
via `events.NewOptionalPropertyParam` (`Required: false`) must NOT
appear in the rendered schema's `required` array; one built via
`events.NewPropertyParam` (`Required: true`) DOES.

**Conflict detection — CORRECTED this round (Round 9): this is 100%
NEW code for events, not something inherited from an existing
mechanism.** An earlier revision of this section said conflict
detection "applies identically" as if events already had SOME
conflict-detection machinery to extend — confirmed via grep this is
WRONG: `api/events/middleware_declaration.go` already ships
`MiddlewareInputError`/`MiddlewareError`/`DuplicateMiddlewareNameError`/
`AmbiguousMiddlewareAttachmentError`, but has ZERO conflict-detection
machinery today (no `checkParamConflicts`-equivalent function, no
`ConflictingParamContributionError` type at all). Phase 0 must
introduce BOTH, brand new, for events — a package-local
`events.ConflictingParamContributionError` type (mirroring reqreply's
own NEW type field-for-field, per this codebase's established
per-package error-type precedent) AND a NEW `checkEventsParamConflicts`
function (mirroring REST's real `checkParamConflicts`), applying
decision #5's resolved REST-style behavior: two `Middleware[In,Out]`
contributions declaring the SAME property/topic name with DIFFERENT
attributes error; agreeing contributions dedupe into one spec entry.
**Round 11 update**: `Required` is now a genuinely comparable attribute
for property contributions specifically, mirroring reqreply's own
identical Round 11 update immediately above — `events.NewPropertyParam`
(required) vs `events.NewOptionalPropertyParam` (optional) declaring
the SAME property name is a real mismatch `checkEventsParamConflicts`
must catch.

**Round 15 update — SAME two refinements as reqreply's own Round 15
update immediately above, mirrored field-for-field**: (1) topic-vars
and properties are INDEPENDENT namespaces for events too — TWO
SEPARATE contribution maps (`topicContributions`/`propertyContributions`),
never cross-checked; a topic var and a property sharing the SAME name
on one channel is explicitly FINE; (2) `checkEventsParamConflicts`
deliberately goes BEYOND REST's real precedent (which has no Codec
field at all) — comparing each contribution's `Codec.Schema` via
`reflect.DeepEqual`, nil-vs-non-nil treated as a mismatch, alongside
the existing `Required` check.

## Side track: fixing 2 pre-existing bugs in events' ALREADY-SHIPPED D-0003 code

**Found this round (Round 8) while scoping Phase 0's events parity
work — both are REAL, PRE-EXISTING bugs in code that shipped under
D-0003 already, NOT introduced by this session's reqreply design work.
Confirmed via direct code inspection, not speculation. Both approved
by the user to fix in the SAME Phase 0 round, since Phase 0 already
touches these exact code paths to add property-var support.**

**Round 19 — also tracked INDEPENDENTLY, in case Phase 0 is delayed.**
Both bugs are now ALSO documented in their OWN standalone roadmap doc,
[Events Middleware Precedence/Observer Bugs](events-middleware-precedence-observer-bugs.md)
— not a competing plan, the SAME underlying fix, just independently
actionable without waiting on Phase 0's larger 2-package scope.
Whichever doc's implementation lands first resolves both bugs.

### Bug 1 — publish-side value precedence is backwards (D3-equivalent)

D-0003's D3 decision states the precedence order should be: **explicit
override > middleware-derived > channel/route-own-derived** (middleware
wins over the channel's own value on a name collision) — confirmed as
REST's real, shipped order via `adapters/nethttp/client.go`'s
`overrideDerived` chain (`query = overrideDerived(query, mwQuery)` then
`opts.QueryParams = overrideDerived(query, explicitQuery)` — middleware
beats route-own, explicit beats middleware).

Events' publish-side code does the OPPOSITE — confirmed IDENTICAL in
BOTH `adapters/mqtt5/adapter.go` and `adapters/zeromq/adapter.go`:

```go
// Current (WRONG): channel-own vars win over middleware-derived vars.
vars = overrideDerivedVars(mwVars, vars)
```

`overrideDerivedVars(derived, explicit)` returns `explicit`-wins-on-
collision (confirmed via its own doc comment/implementation in
`adapters/mqtt5/transform_dispatch.go`) — so passing `vars` (channel-
own) as the `explicit` argument makes channel-own beat middleware,
backwards relative to D3.

**Why this is NOT a 1-line fix**: traced the call chain via
`adapters/mqtt5/binding.go`'s `PublishAdapter` — when `a.opts.Vars ==
nil`, `publishHandle` derives `vars` from the channel's own
`NewTopicParam` merge fields (pure channel-own); when `a.opts.Vars !=
nil`, the caller's EXPLICIT static override is passed directly as
`vars`. By the time `vars` reaches the `overrideDerivedVars` call
INSIDE `publish()`, the distinction between "this is channel-own" and
"this is a genuine explicit override" has ALREADY been erased — both
arrive as a plain `map[string]string`. A naive argument-order reversal
(`overrideDerivedVars(vars, mwVars)`, making `mwVars` always win) would
ALSO make middleware incorrectly beat a genuine EXPLICIT override,
violating the "explicit > middleware" half of D3's rule.

**Real fix — REFINED this round (Round 9) to a simpler, more minimal
design than an earlier revision of this sketch proposed.** Traced ALL
real callers of `publish()`/`publishHandle()` in `adapters/mqtt5/
binding.go` (and confirmed IDENTICAL in `adapters/zeromq/binding.go`):
`PublishAdapter` branches `if a.opts.Vars == nil { publishHandle(...) }
else { publish(..., a.opts.Vars, ...) }` — the two cases are ALREADY
MUTUALLY EXCLUSIVE at the call site (explicit `Vars` COMPLETELY
REPLACES channel-own derivation via `publishHandle`'s own
`codex.EncodeVars(msg, handle.MergeFields()...)` call — never both at
once). Since a caller of `publish()` is NEVER in both states
simultaneously, a full parameter-shape restructuring (2 separate maps,
as an earlier revision proposed) is MORE invasive than necessary — a
SINGLE `isExplicitVars bool` flag threaded through the SAME existing
`vars map[string]string` parameter is sufficient and more minimal:

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

### Bug 2 — missing Observer integration on both subscribe and publish middleware dispatch (D5-equivalent)

D-0003's D5 decision ("Observer/stats integration") explicitly states
"Events mirror, confirmed NOT REST-specific" — `events.Transform`/
`events.ClientTransform` should get the SAME `stats.ReportErrors(obs,
"middleware:in"/"middleware:fn", err)` calls REST's real
`runMiddlewareHandlersReflect`/client dispatch already has (confirmed
Round 7 for reqreply's own design). But events' actual dispatch
functions — `dispatchSubscribeMiddlewareHandlers` (subscribe-side) and
`dispatchPublishMiddlewareHandlers` (publish-side), both in
`adapters/mqtt5/transform_dispatch.go` — have ZERO `stats.ReportErrors`
calls for either failure class today, confirmed via direct code
inspection. This was simply never implemented, despite D5 already
declaring it should exist.

**Fix**: add `stats.ReportErrors(obs, "middleware:in", err)` on a
`DecodeIn` failure and `stats.ReportErrors(obs, "middleware:fn", err)`
on the fn's own business error, at BOTH dispatch call sites
(`adapters/mqtt5/adapter.go`'s subscribe/publish handlers, which call
`dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers`)
— and confirm/mirror the identical fix in `adapters/zeromq`'s
equivalent dispatch functions. Uses the SAME `obs`/`ObserverFromContext`
nil-guard pattern already established elsewhere in these adapters.

**Signature note (confirmed this round, Round 10)**: both dispatch
functions' OWN signatures also need updating to match the
`MiddlewareHandler.DecodeIn`/`ClientMiddlewareHandler.EncodeOut`
2-map extension (see "Phase 0" above) — `dispatchSubscribeMiddlewareHandlers`
gains a `propertyVars map[string]string` parameter (currently calls
`h.DecodeIn(topicVars)`, single-arg; becomes `h.DecodeIn(topicVars,
propertyVars)`); `dispatchPublishMiddlewareHandlers` must return 2
SEPARATE maps instead of merging everything into one (currently: `for
k, v := range mwVars { vars[k] = v }`, a single merged map — this
CANNOT stay a single map once property vars exist, since topic vars
and property vars have entirely different downstream consumers — see
"Write-side wiring" immediately below).

## Write-side wiring: getting property values onto the actual wire

**MAJOR finding this round (Round 10) — traced for the FIRST time
across all 9 prior rounds.** Every prior round verified this doc's
TYPE-LEVEL design (structs, method signatures, `DecodeIn`/`EncodeOut`
shapes) but never traced a property value all the way to the actual
wire. Doing so this round surfaced a genuine, asymmetric capability
gap — confirmed via direct code inspection of all real
`.Publish(...)`/`userProps` call sites in both adapters, not
speculation.

### Case 1 — reqreply client-request (`WithRequestProperty`): FINE, existing write-target

`adapters/mqtt5/reqreply_transport.go`'s client-side request-publish
(confirmed real code, ~line 787) already builds `userProps :=
append(pahomqtt5.UserProperties(nil), t.opts.UserProperties...)`, then
appends security-credential-derived properties, before setting them on
the outgoing request message. The property axis's own
`propertyVars map[string]string` (from `WithRequestProperty`'s
`EncodeIn`) needs to merge into this SAME `userProps` slice — convert
each `propertyVars` entry into a `UserProperty{Key: k, Value: v}` and
append alongside the existing credential-derived ones. A small,
well-defined addition to EXISTING, working machinery.

### Case 2 — events publish (`WithPublishProperty`): FINE in principle, but needs the dispatch-signature split from above

`adapters/mqtt5/adapter.go`'s publish path (confirmed real code, ~line
803) already builds `userProps := append(pahomqtt5.UserProperties(nil),
opts.UserProperties...)` the SAME way. Once
`dispatchPublishMiddlewareHandlers` returns 2 separate maps (topic vars,
property vars — per the signature note above), the property-vars map
merges into this SAME `userProps` slice, exactly mirroring Case 1 —
NOT into `vars`/`BuildTopic` (that path stays topic-vars-only). Without
the signature split, property vars would either leak into the topic
template (wrong) or get silently dropped (wrong) — confirming why the
split is not optional.

### Case 3 — reqreply server-reply (`WithResponseProperty`): BROKEN — no write-target exists, needs BRAND NEW capability

**This is the significant gap.** Traced ALL 3 real `.Publish(...)`
call sites in `adapters/mqtt5/reqreply_transport.go`:
- The error-reply path (`ErrorPattern`-matched, ~line 122): `props :=
  &pahomqtt5.PublishProperties{ContentType: ..., CorrelationData:
  ...}` — `.User` is NEVER set.
- The success-reply path (~line 461, the NORMAL case): `replyProps :=
  &pahomqtt5.PublishProperties{}`, then only `CorrelationData` is
  conditionally set — `.User` is NEVER set here either.

Confirmed via `ServeOptions` (`adapters/mqtt5/reqreply.go`): its ONLY
User-Property field is `UserPropertyParams` — explicitly
VALIDATE-ONLY, checked "against the INCOMING request's... User
Properties" — nothing for OUTGOING replies. **There is no existing
mechanism anywhere in shipped code — not even Phase 1b's
`FromResponseUserPropertyParam`, which only populates spec/validation
metadata the CLIENT reads, never causing the SERVER to write
anything — for a server to write ANY outgoing User Property onto its
own reply.** Without a fix, a route declaring `WithResponseProperty`
would correctly produce an `Out` value and a `propertyVars` map, with
NOWHERE to write it — the reply publishes exactly as today, silently
dropping the declared value.

**Fix**: add BRAND NEW capability to `serverTransport.Serve`'s BOTH
reply-publish call sites (success AND error-reply) — build a
`replyProps.User` slice from the property axis's `propertyVars` map
(converting each entry to `pahomqtt5.UserProperty{Key: k, Value: v}`),
set alongside the existing `CorrelationData`/`ContentType` fields. This
is NEW code, not an extension of existing machinery (unlike Cases 1/2)
— `ServeOptions` currently has no static escape-hatch equivalent to
`t.opts.UserProperties`/`opts.UserProperties` for the reply direction,
and none is needed: the property axis's `WithResponseProperty` IS the
mechanism, this fix just completes its wire path.

### `adapters/zeromq`: N/A for all 3 cases

Zeromq has no property mechanism at all — already correctly documented
elsewhere in this doc ("supplies an empty property-value map"; "a
route declaring a REQUIRED property... fails naturally"). No write-side
wiring needed there.

## Structured errors (all implement `slog.LogValuer`)

Reuses the SAME error TYPES rest/events already ship for this mechanism
(package-local copies, per this codebase's own established "each API
layer keeps its own copy" precedent — NOT shared cross-package types):

```go
// MiddlewareInputError — mw's In value failed to decode/validate from
// request-side topic vars.
type MiddlewareInputError struct {
    Name string
    Err  error
}

// MiddlewareError — a Transform/ClientTransform (or bundled WithReceive/
// WithSend) fn returned its own business error not matched by any
// declared ErrorPattern — D2's fallback, mirrors rest.MiddlewareError/
// events.MiddlewareError exactly.
type MiddlewareError struct {
    Name string
    Err  error
}

// DuplicateMiddlewareNameError — two Middleware values with the SAME
// Declaration.Name attached to one route (D6(b)).
type DuplicateMiddlewareNameError struct {
    Route string
    Name  string
}

// AmbiguousMiddlewareAttachmentError — a SINGLE Middleware value carries
// a bundled WithReceive/WithSend Fn AND is ALSO passed to Transform/
// ClientTransform on the SAME route (D7).
type AmbiguousMiddlewareAttachmentError struct {
    Name string
}

// ConflictingParamContributionError — two DIFFERENT Middleware
// contributions (or a Middleware contribution vs. Phase 1b's flat
// mechanism) declare the SAME property/topic name with DIFFERENT
// attributes (kind or required-ness) — mirrors rest.
// ConflictingParamContributionError exactly (confirmed via
// api/rest/middleware.go's real checkParamConflicts). RESOLVED decision
// (see "AsyncAPI spec rendering" above): the NEW mechanism follows
// REST's stricter precedent, NOT Phase 1b's laxer silent-dedupe one.
type ConflictingParamContributionError struct {
    Route        string
    ParamName    string
    FirstSource  string
    SecondSource string
}
```

Each implements `Error() string`/`Unwrap() error` (where applicable)/
`LogValue() slog.Value`. `MiddlewareInputError`/`MiddlewareError`/
`DuplicateMiddlewareNameError`/`AmbiguousMiddlewareAttachmentError` are
direct ports of `rest`'s/`events`' ALREADY-SHIPPED identical error
types, field-for-field (`events.MiddlewareInputError` etc. already
exist today). **`ConflictingParamContributionError` is different —
CLARIFIED this round (Round 9)**: `rest.ConflictingParamContributionError`
already ships, but `events.ConflictingParamContributionError` does NOT
exist yet — confirmed via grep that events has ZERO conflict-detection
machinery today. Phase 0 must create this type NEW for events too
(mirroring `rest.ConflictingParamContributionError` field-for-field,
same as reqreply's own new type does) — see "AsyncAPI spec rendering"
under "Phase 0" above for the full design.

## Observer integration

**Corrected this round (Round 7) against D-0003's own D5 decision —
`docs/design/d-0003-codec-declared-middlewares.md`'s "Observer/stats
integration" — which an earlier revision of this section never cited
and got wrong as a result.** D5 establishes a CONCRETE, shipped
precedent, confirmed via real code, not just its stated intent:
`adapters/nethttp/serve.go`'s `runMiddlewareHandlersReflect` calls
`stats.ReportErrors(obs, "middleware:in", err)` on a `DecodeIn` failure
and `stats.ReportErrors(obs, "middleware:fn", fnErr)` on the `fn`'s own
returned business error (lines ~849/~860) — the SAME two calls also
appear client-side in `adapters/nethttp/client.go` (~line 971) for
`ClientTransform`'s dispatch. These are DEDICATED location strings for
THIS mechanism's own two failure classes, distinct from Phase 1b's
EXISTING `"topic_var"` string (used for the ROUTE's own topic-var decode
failures, an unrelated, older concern) — conflating the two would lose
exactly the distinction D5 was designed to create.

reqreply mirrors D5 exactly, with the SAME two calls, made from the
adapter (`mqtt5`/`zeromq` transport code) alongside the `ServeError`/
`CallError{Kind: ...}` wrapping (decision #6, resolved below):
`stats.ReportErrors(obs, "middleware:in", err)` for a
`MiddlewareInputError` (request-side merge decode failure — wrapped as
`ServeError{Kind: KindDecode}`/`CallError{Kind: KindDecode}`), and
`stats.ReportErrors(obs, "middleware:fn", err)` for a `MiddlewareError`
(the `Transform`/`ClientTransform`-attached `fn`'s own business error,
D2's fallback — wrapped as `ServeError{Kind: KindMiddleware}`/
`CallError{Kind: KindMiddleware}`). No NEW observer interface needed —
both calls use the EXISTING `stats.Observer`/`stats.ReportErrors`
machinery, resolved via the SAME `obs := opts.Observer; if obs == nil {
obs = stats.ObserverFromContext(ctx) }` nil-guard pattern every other
adapter call site already uses. An output-merge (encode-side) failure
is NOT separately reported via `stats.ReportErrors` — confirmed this
mirrors REST's OWN real behavior exactly (`adapters/nethttp/serve.go`'s
middleware `EncodeOut` composition loop has no `ReportErrors` call
either, only the `ServeError`/`CallError{Kind: KindEncode}` wrapping) —
not an oversight to fix, a faithfully-mirrored existing asymmetry.

## Unit test plan

Mirrors `api/rest`'s own `Transform`/`ClientTransform` test structure
(the closest structural match — single Route, both directions):

| Test | Verifies |
|---|---|
| `TestMiddleware_WithRequestTopic_MergesIn` | `Transform`'s `fn` receives correctly-decoded `In` from request topic vars |
| `TestMiddleware_WithResponseTopic_EncodesOutIntoReply` (server) | `Transform`'s returned `Out` correctly encodes into the reply's topic vars |
| `TestMiddleware_WithResponseTopic_DecodesOutFromReply` (client) | `ClientTransform`'s `DecodeOut` mechanically decodes `Out` from the actual reply's topic vars, no Fn involved |
| `TestTransform_EnrichesReqPointer` | `Transform`'s `fn` can both READ and WRITE `*Req`, visible to the route's own handler afterward |
| `TestRoute_MultipleTransformAttachments_DispatchInRegistrationOrder` (Round 12) | TWO SEPARATE `Transform` calls chained onto the SAME route both dispatch, in registration order — confirms `MiddlewareHandlers` genuinely ACCUMULATES across calls (mirrors REST's real `append(slices.Clone(r.opts), ...)` pattern), not just a theoretical slice-type possibility |
| `TestTransform_D6c_TwoMiddlewaresEnrichSameReqField_LastAppliedWins` (Round 12) | D6(c): two `Transform`-attached middlewares' own `fn`s BOTH write to the SAME `*Req` field — confirms attachment-order, last-applied-wins, NOT flagged as a conflict/error, mirroring D-0003's own explicit resolution |
| `TestClientTransform_ProducesInFromReq` | `ClientTransform`'s `fn` receives the caller's own `Req` value (read-only) and produces `In` |
| `TestRoute_Use_BundledWithReceive_AgnosticAttachment` | a `Middleware` with `WithReceive` set, attached via plain `.Use()`, dispatches without needing `Transform` |
| `TestRoute_Use_SameMiddlewareValue_ReusedAcrossDifferentReqTypes` (Round 13) | mirrors D-0003's OWN explicitly-called-out "test that actually proves reuse" — the LITERAL SAME bundled `Middleware` value, attached via plain `.Use()` to TWO+ routes with DIFFERENT `Req`/`Resp` types, dispatches correctly and independently on EACH — not just that the mechanism compiles |
| `TestNewPropertyParam_NestedSubStructField_GetSetReachesIntoSubstruct` (Round 14) | mirrors REST's own `TestNestedStructMergeFields_GetSetReachIntoSubstruct` reference pattern — a `WithRequestProperty`/`WithRequestTopic` merge field whose `get`/`set` reach into a NESTED sub-struct field (e.g. `func(r ComputeReq) string { return r.Meta.TenantID }`), confirming the merge mechanism works identically to a flat top-level field |
| `TestMiddleware_PropertyMergeComposesWithGobRequestFormat` (Round 14) | mirrors REST's own `TestGobBodyFormat_ComposesWithNestedMergeFields` reference pattern — a route's `WithRequestFormats`/`WithFormats` set to a NON-JSON format (Gob), with a `WithRequestProperty`/`WithResponseProperty` merge field ALSO declared on the SAME route, confirming payload-format and var-merge remain fully orthogonal (neither affects the other) |
| `TestRoute_Register_DuplicateMiddlewareNameError` | D6(b): two `Middleware` values sharing a `Declaration.Name` on one route |
| `TestRoute_Register_AmbiguousMiddlewareAttachmentError` | D7: one value both bundled AND passed to `Transform` |
| `TestRoute_Register_ConflictingParamContributionError` | two `Middleware` values (or a `Middleware` + Phase 1b's flat mechanism) declare the SAME property/topic name with DIFFERENT attributes — fails with `ConflictingParamContributionError` (decision #5's resolved REST-style behavior) |
| `TestRoute_Register_TwoPhase1bOnlyContributions_MismatchNowErrors` (Round 18) | **BREAKING-CHANGE regression test**: TWO Phase-1b-ONLY declarations (`.Use(mqtt5.FromUserPropertyParam(...))`, no NEW `Middleware[In,Out]` involved at all) for the SAME property name with DIFFERING `Required`/codec now FAIL with `ConflictingParamContributionError` — confirms the uniform algorithm applies even with ZERO new-axis contributions present, replacing Phase 1b's old silent first-seen-wins dedupe for this specific mismatched case |
| `TestRoute_Register_AgreeingParamContributions_DedupeWithoutError` | D6(a) (Round 12 label added): two `Middleware` values declaring the SAME name with the SAME attributes dedupe into ONE spec entry, no error — mirrors D-0003's own explicit "two independent In types reading the same name is VALID, no new check needed" callout |
| `TestTransform_MiddlewareError_FallsBackWhenNoErrorPatternMatch` | D2: `fn`'s business error wraps as `MiddlewareError` when no `ErrorPattern` matches |
| `TestAttachServer_Transform_RunsAfterPairedSecurity` (mqtt5) | D1: `Transform`'s declared middleware runs AFTER the paired security Fn (both pre-handler) — confirms reqreply's dispatch order matches REST's real, established order (security first, then declared middleware), not just "the same point" |
| `TestAttachServer_Transform_RunsAfterPairedSecurity` (zeromq) | same, zeromq — confirms the mechanism is genuinely transport-agnostic with zero adapter-specific work |
| `TestMiddleware_WithRequestProperty_MergesIn` | `Transform`'s `fn` receives correctly-decoded `In`, decoded from its OWN separate property-value map, alongside (never combined with) any topic vars decoded on the same route |
| `TestMiddleware_WithRequestProperty_RequiredButAdapterSuppliesNoPropertyMap` | a route declaring a REQUIRED property on an adapter with no property mechanism (simulated empty map) fails with the SAME `MiddlewareInputError` a missing topic var would — confirms no special-casing needed |
| `TestMiddleware_WithOptionalRequestProperty_PresentMergesCorrectly` (Round 11) | `NewOptionalPropertyParam`'s declared field merges correctly into `In` when the property IS present in the adapter-supplied map — behaves identically to a required property when present |
| `TestMiddleware_WithOptionalRequestProperty_AbsentLeavesZeroValueNoError` (Round 11) | `NewOptionalPropertyParam`'s declared field is absent from the adapter-supplied map — `Transform`'s `fn` still runs, `In`'s field is left at its zero value, NO `MiddlewareInputError` — confirms `codex.OptionalField`'s existing no-op-when-absent behavior is correctly reachable through the new constructor |
| `TestRoute_Register_RequiredVsOptionalPropertyMismatchError` (Round 11) | two `Middleware` contributions declare the SAME property name, one via `NewPropertyParam` (required) and one via `NewOptionalPropertyParam` (optional) — fails with `ConflictingParamContributionError`, confirming `Required` is now a genuinely checked attribute |
| `TestRoute_Register_TopicAndPropertySameName_NoConflict` (Round 15) | a topic var named "tenantID" (via `WithRequestTopic`) AND a property ALSO named "tenantID" (via `WithRequestProperty`) declared on the SAME route — NO error, confirming topic-vars and properties are INDEPENDENT conflict-detection namespaces |
| `TestRoute_Register_ConflictingPropertyCodecSchemaError` (Round 15) | two `Middleware` contributions declare the SAME property name with matching `Required` but DIFFERENT codec schemas — fails with `ConflictingParamContributionError`, confirming the NEW Schema-based comparison (deliberately stricter than REST's real precedent, which has no Codec field at all) |
| `TestRoute_Register_OptionalProperty_NotInSchemaRequiredList` (Round 16) | a property declared via `NewOptionalPropertyParam` does NOT appear in the rendered AsyncAPI schema's `required` array, confirming `Required` propagates into the spec, not just runtime validation |
| `TestRoute_Register_RequiredProperty_InSchemaRequiredList` (Round 16) | a property declared via `NewPropertyParam` DOES appear in the rendered AsyncAPI schema's `required` array, mirroring Phase 1b's existing `applyParamDeclarations` behavior for header-as-middleware declarations |
| `TestAttachClient_WithRequestProperty_WritesOutgoingUserProperty` (mqtt5) | **Write-side wiring Case 1**: a `Middleware`'s `WithRequestProperty`-declared value actually appears in the outgoing REQUEST's real MQTT5 User Properties, merged alongside any security-credential-derived properties |
| `TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty` (mqtt5) | **Write-side wiring Case 3 — the MAJOR finding this round**: a `Middleware`'s `WithResponseProperty`-declared value actually appears in the outgoing REPLY's real MQTT5 User Properties — verifies the BRAND NEW capability added to `serverTransport.Serve`'s success-reply publish path, since no such capability existed before this round's fix. A regression test that would have caught this gap by simply asserting on the reply's actual wire properties. |
| `TestAttachServer_WithResponseProperty_ErrorReplyAlsoWritesUserProperty` (mqtt5) | same as above, but for the ERROR-reply publish path (`ErrorPattern`-matched) — confirms the fix covers BOTH reply paths, not just the success path |
| `TestAttachServer_MiddlewareError_WrapsAsKindMiddleware` (mqtt5 and zeromq) | decision #6: a `Transform`-attached `fn`'s own business error (falling back to `MiddlewareError` per D2) surfaces through `ServeError{Kind: KindMiddleware}` (a NEW `ErrorKind` value in each adapter), NOT `KindHandler` — distinguishing a declared-middleware failure from a real handler failure |
| `TestAttachServer_Observer_ReportsMiddlewareInAndFnLocations` (mqtt5 and zeromq) | D5 (mirrored from REST): a `DecodeIn` failure calls `stats.ReportErrors(obs, "middleware:in", err)`, and the `fn`'s own business error calls `stats.ReportErrors(obs, "middleware:fn", err)` — NOT the flat mechanism's existing `"topic_var"` string, confirming the two mechanisms stay distinguishable in observer output |
| `TestClientTransform_ValueConflict_MiddlewareDerivedWins` | mirrors D3: when the route's OWN topic/property merge (from `Req`) and a `Middleware`'s `WithRequestTopic`/`WithRequestProperty` (from `In`) both target the SAME var name with agreeing declared attributes but DIFFERING runtime values, the middleware-derived value wins in the outgoing request — mirrors REST's real `overrideDerived` precedence exactly |
| `TestTransform_ValueConflict_MiddlewareDerivedWins` (server, reply-encode side) | same precedence rule applied to `Transform`'s reply-side encode: a `Middleware`'s `WithResponseTopic`/`WithResponseProperty` value overrides the route's own `Resp`-derived value for the SAME var name, mirroring REST's server-side "registration-order, last-applied-wins" behavior |

### `api/events` — Phase 0 property axis tests (mirror the reqreply rows above, adapted for events' asymmetric Subscribe/Publish role split — no single combined Transform)

| Test | Verifies |
|---|---|
| `TestMiddleware_WithSubscribeProperty_MergesIn` | `Transform`'s (subscribe-side) `fn` receives correctly-decoded `In`, decoded from its OWN separate property-value map, alongside any topic vars decoded on the same channel |
| `TestMiddleware_WithPublishProperty_EncodesOutIntoMessage` | `ClientTransform`'s (publish-side) returned `Out` correctly encodes into the outgoing message's property vars, as a SEPARATE map from topic vars |
| `TestMiddleware_WithSubscribeProperty_RequiredButAdapterSuppliesNoPropertyMap` | a channel declaring a REQUIRED property on an adapter with no property mechanism (zeromq) fails with the SAME `MiddlewareInputError` a missing topic var would |
| `TestMiddleware_WithOptionalSubscribeProperty_AbsentLeavesZeroValueNoError` (Round 11) | mirrors reqreply's own Round 11 test — `events.NewOptionalPropertyParam`'s declared field is absent from the adapter-supplied map, `Transform`'s `fn` still runs, no `MiddlewareInputError` |
| `TestChannel_Register_RequiredVsOptionalPropertyMismatchError` (Round 11) | mirrors reqreply's own Round 11 test — two `Middleware` contributions declare the SAME property name, one required, one optional — fails with `events.ConflictingParamContributionError` |
| `TestChannel_Register_TopicAndPropertySameName_NoConflict` (Round 15) | mirrors reqreply's own Round 15 test — a topic var and a property sharing the SAME name on one channel — NO error, confirming independent namespaces for events too |
| `TestChannel_Register_OptionalProperty_NotInSchemaRequiredList` (Round 16) | mirrors reqreply's own Round 16 test — an optional property does NOT appear in events' standalone rendered schema's `required` array |
| `TestChannel_Register_ConflictingParamContributionError` (events) | two `Middleware` values declare the SAME property/topic name with DIFFERENT attributes — fails with `ConflictingParamContributionError`, mirroring reqreply's decision #5 |
| `TestChannel_Register_AgreeingParamContributions_DedupeWithoutError` (events, Round 12) | D6(a) mirror: two `Middleware` values declaring the SAME name with the SAME attributes dedupe into ONE spec entry, no error |
| `TestSubscriber_MultipleTransformAttachments_DispatchInRegistrationOrder` (Round 12) | mirrors reqreply's own Round 12 test — TWO SEPARATE `Transform` calls chained onto the SAME `Subscriber[T]` both dispatch, in registration order |
| `TestSubscriber_Use_SameMiddlewareValue_ReusedAcrossDifferentTTypes` (Round 13) | mirrors reqreply's own Round 13 test — the LITERAL SAME bundled `Middleware` value, attached via plain `.Use()` to TWO+ `Subscriber[T]` channels with DIFFERENT `T` types, dispatches correctly and independently on EACH |
| `TestNewPropertyParam_NestedSubStructField_GetSetReachesIntoSubstruct` (events, Round 14) | mirrors reqreply's own Round 14 test — a `WithSubscribeProperty`/`WithSubscribeTopic` merge field whose `get`/`set` reach into a NESTED sub-struct field |
| `TestMiddleware_PropertyMergeComposesWithGobRequestFormat` (events, Round 14) | mirrors reqreply's own Round 14 test — a channel's `SubscribeFormats`/`PublishFormats` set to a NON-JSON format (Gob), with a property merge field ALSO declared, confirming orthogonality |
| `TestMiddleware_Use_SameValue_ReusedAcrossSubscriberAndPublisher` (Round 13) | a SINGLE `Middleware` value carrying BOTH a bundled `WithReceive` Fn AND a bundled `WithSend` Fn, attached via `.Use()` to a `Subscriber[T]` AND, separately, to a `Publisher[T]` — confirms `applyAgnosticSubscriber()`/`applyAgnosticPublisher()` correctly extract only the relevant half on each side, with zero interference |
| `TestTransform_D6c_TwoMiddlewaresEnrichSameMsgField_LastAppliedWins` (events, Round 12) | D6(c) mirror: two `Transform`-attached (subscribe-side) middlewares' own `fn`s BOTH write to the SAME `*T` field — confirms attachment-order, last-applied-wins, NOT an error |
| `TestSubscribe_MiddlewareDispatch_RunsAfterPairedSecurity` (mqtt5) | D1: subscribe-side declared middleware runs AFTER the paired security Fn, mirroring reqreply's/REST's established order |
| `TestPublish_MiddlewareDispatch_ValuePrecedence_ExplicitBeatsMiddlewareBeatsChannelOwn` (mqtt5 and zeromq) | **Bug 1 fix verification**: with ALL THREE tiers present on the SAME var name (explicit `PublishOptions.Vars`, a `Middleware`'s `WithPublishTopic`/`WithPublishProperty`, AND the channel's own `NewTopicParam`-derived value), the final resolved value follows explicit > middleware-derived > channel-own-derived — the CORRECTED order, replacing the confirmed-backwards behavior found this round |
| `TestSubscribe_Observer_ReportsMiddlewareInAndFnLocations` (mqtt5 and zeromq) | **Bug 2 fix verification**: `dispatchSubscribeMiddlewareHandlers` calls `stats.ReportErrors(obs, "middleware:in", err)` on a `DecodeIn` failure and `stats.ReportErrors(obs, "middleware:fn", err)` on the fn's own business error — both calls confirmed ABSENT before this round's fix |
| `TestPublish_Observer_ReportsMiddlewareInAndFnLocations` (mqtt5 and zeromq) | same as above, for `dispatchPublishMiddlewareHandlers` |
| `TestPublish_WithPublishProperty_WritesOutgoingUserProperty_SeparateFromTopicVars` (mqtt5) | **Write-side wiring Case 2**: a `Middleware`'s `WithPublishProperty`-declared value appears in the outgoing message's real MQTT5 User Properties, merged into `opts.UserProperties`'s mechanism — confirmed SEPARATE from any `WithPublishTopic`-declared value on the SAME message (no cross-contamination between the two var kinds after `dispatchPublishMiddlewareHandlers`'s 2-map split) |

## Files to create

| File | Responsibility |
|---|---|
| `api/reqreply/middleware_declaration.go` (NEW) | `Middleware[In,Out]`, `NewMiddleware`, `WithRequestTopic`/`WithResponseTopic`/`WithRequestProperty`/`WithResponseProperty`/`WithReceive`/`WithSend`, `RouteMiddlewareMarker`, error types — direct port of `api/rest/middleware_declaration.go`'s structure (closer shape match than events') |
| `api/reqreply/property_param.go` (NEW) | `PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` — thin wrapper over `codex.Param`/`MergedParam`/`NewParam`, mirrors `TopicParam`'s exact wrapper pattern minus template-presence validation; PLUS (Round 11) a `Required bool` field on both types and a NEW `NewOptionalPropertyParam[T,V]` constructor built directly against `codex.OptionalField` — zero `codex` package changes needed |
| `api/reqreply/transform.go` (NEW) | `Transform`/`ClientTransform`, `MiddlewareHandler`/`ClientMiddlewareHandler`, `buildDecodeIn`/`buildEncodeOut`/`buildEncodeIn`/`buildDecodeOut`, `buildMiddlewareHandler`/`buildClientMiddlewareHandler` — direct port of `api/rest/transform.go`'s structure; `DecodeIn`/`EncodeOut`/`EncodeIn`/`DecodeOut` take/return topic vars and property vars as SEPARATE map parameters (never combined), mirroring REST's real multi-map signatures exactly; `stats.ReportErrors(obs, "middleware:in"/"middleware:fn", err)` calls (D5, see "Observer integration"); value-precedence merge (middleware-derived overrides route-own-derived, see "Value precedence" above) via the SAME `overrideDerived`-style helper REST's adapters use |
| `api/reqreply/route.go` (edit) | `RouteHandle.MiddlewareHandlers`/`ClientMiddlewareHandlers` fields; `Route.Register`/`ClientHandle` call the D6(b)/D7 check (mirrors `checkMiddlewareNameUniquenessAndAttachment`) AND the NEW `checkParamConflicts`-equivalent (`ConflictingParamContributionError`, unified with Phase 1b's existing `applyParamDeclarations`) |
| `api/reqreply/builder.go` (edit) | `registerRoute`/`AsyncAPISpec`-adjacent code unifies `Transform`/`ClientTransform`'s property-axis spec contributions into the SAME `reqHeaders`/`respHeaders` `schema.Schema` values Phase 1b's `applyParamDeclarations` already builds — see "AsyncAPI spec rendering" above |
| `adapters/mqtt5/reqreply_transport.go` (edit), `adapters/mqtt5/errors.go` (edit), `adapters/mqtt5/reqreply_transport_test.go` (edit) | `serverTransport.Serve`/`clientTransport.Call` consult `MiddlewareHandlers`/`ClientMiddlewareHandlers`, dispatching AFTER the paired security Fn (both pre-handler/pre-encode, confirmed order via REST's real dispatch code); supplies the property-value map from the real message's User Properties (reuses the SAME extraction `UserPropertyParam` already does); client-request side (`WithRequestProperty`) merges its `propertyVars` into the EXISTING `userProps`/`t.opts.UserProperties` mechanism (Case 1, see "Write-side wiring"); **MAJOR — server-reply side (`WithResponseProperty`) needs BRAND NEW capability**: BOTH the success-reply AND error-reply `.Publish(...)` call sites gain a `replyProps.User` build step from `propertyVars`, since NO existing mechanism writes outgoing reply User Properties today (Case 3, see "Write-side wiring" — this is the most significant gap found across all review rounds); `errors.go` gains a NEW `KindMiddleware` `ErrorKind` value (decision #6) with a matching `String()` case; `_test.go` gains `TestAttachServer_Transform_RunsAfterPairedSecurity`, `TestAttachServer_MiddlewareError_WrapsAsKindMiddleware`, and NEW `TestAttachServer_WithResponseProperty_WritesOutgoingUserProperty` (verifies the reply's ACTUAL wire User Properties carry the declared value — see "Unit test plan"). **Round 14 confirmed (non-gap)**: `mqtt5.Call`/`CallHandle` (the latter a deprecated alias for the former) construct a `clientTransport` and dispatch through this SAME `clientTransport.Call` — Middleware dispatch is picked up automatically, zero separate wiring needed for either single-call convenience entry point |
| `adapters/zeromq/reqreply_transport.go` (edit), `adapters/zeromq/errors.go` (edit), `adapters/zeromq/reqreply_transport_test.go` (edit) | Same for topic-var dispatch, all 4 transports — confirms transport-agnostic parity; supplies an empty property-value map (zeromq has no property mechanism) — a route declaring a REQUIRED property fails naturally, no special-casing; `errors.go` gains the SAME NEW `KindMiddleware` value, mirroring mqtt5's; `_test.go` gains the SAME two test names, mirroring mqtt5's. **Round 14 confirmed (non-gap)**: `zeromq.Call`/`CallHandle` (`adapters/zeromq/adapter.go`) mirror mqtt5's exact pattern — construct a `clientTransport` and dispatch through the SAME `clientTransport.Call`, automatically picking up Middleware dispatch |
| `api/reqreply/middleware_declaration_test.go`, `transform_test.go`, `property_param_test.go` (NEW) | See "Unit test plan" |
| `docs/features/security.md` or a NEW `docs/features/reqreply-middleware.md` | Document the new mechanism alongside the existing security one |
| `.github/instructions/go-codex.instructions.md` | `api/reqreply` AND `api/events` rows updated |
| `api/events/property_param.go` (NEW) | `events.PropertyParam`/`MergedPropertyParam[T]`/`NewPropertyParam[T,V]` — DIRECT copy of reqreply's own triple, see "Phase 0" above; PLUS (Round 11) the SAME `Required bool` field + `NewOptionalPropertyParam[T,V]` constructor, mirrored field-for-field |
| `api/events/middleware_declaration.go` (edit) | `Middleware[In,Out]` gains `propertyMergeFieldsIn`/`propertyMergeFieldsOut` fields (alongside existing `topicMergeFieldsIn`/`Out`); `WithSubscribeProperty`/`WithPublishProperty` methods added; NEW `events.ConflictingParamContributionError` type AND a NEW `checkEventsParamConflicts` function added — CONFIRMED via Round 9 that events has ZERO pre-existing conflict-detection machinery, this is 100% new code, not an extension |
| `api/events/transform.go` (edit) | `buildDecodeIn`/`buildEncodeOut` gain a SEPARATE `propertyVars` map parameter/return value; `MiddlewareHandler.DecodeIn`/`ClientMiddlewareHandler.EncodeOut` signatures updated to match; `stats.ReportErrors(obs, "middleware:in"/"middleware:fn", err)` calls added (D5 fix, see "Side track" Bug 2) |
| `api/events/builder.go` (edit) | New standalone AsyncAPI spec contribution for the property axis (no unification needed — events has no pre-existing flat mechanism, see "AsyncAPI spec rendering" under "Phase 0") |
| `adapters/mqtt5/adapter.go` (edit), `adapters/mqtt5/transform_dispatch.go` (edit), `adapters/mqtt5/binding.go` (edit) | Property-value map supplied from real MQTT5 User Properties for subscribe/publish dispatch; **Bug 1 fix**: `PublishAdapter`/`publishHandle`/`publish()` restructured to preserve explicit-vs-channel-own distinction through the middleware-override point (see "Side track" Bug 1 code sketch); **Bug 2 fix**: `dispatchSubscribeMiddlewareHandlers`/`dispatchPublishMiddlewareHandlers` gain the missing `stats.ReportErrors` calls; `dispatchSubscribeMiddlewareHandlers` signature gains a `propertyVars` parameter, `dispatchPublishMiddlewareHandlers` returns 2 SEPARATE maps instead of one merged map (see "Write-side wiring" Case 2) — publish-side property vars merge into the EXISTING `userProps`/`opts.UserProperties` mechanism, kept SEPARATE from topic vars feeding `BuildTopic` |
| `adapters/zeromq/adapter.go` (edit), `adapters/zeromq/binding.go` (edit) | Same 2 bug fixes mirrored for zeromq (empty property-value map, zeromq has no property mechanism — a route declaring a REQUIRED property fails naturally) |
| `api/events/middleware_declaration_test.go`, `transform_test.go`, `property_param_test.go` (NEW) | See "Unit test plan" — events rows |
| `adapters/mqtt5/adapter_test.go` (edit), `adapters/zeromq/adapter_test.go` (edit) | New tests for both side-track bug fixes (precedence, Observer location strings) |
| `docs/features/security.md` or a NEW `docs/features/reqreply-middleware.md` | Document the new mechanism alongside the existing security one, covering BOTH `api/reqreply` and `api/events` |
| `examples/reqreply-api/demo_property_axis_middleware.go` (NEW, Round 17) | A new demo (mirrors `demo_cross_api_oauth2_sharing.go`'s "Demo 9" one-file-per-demo pattern — this would be the next available number) demonstrating the property axis END-TO-END, over mqtt5: a route declares `WithRequestProperty`(a tenant-ID property, merged into `In`) via `Transform`, and `WithResponseProperty` echoing a derived value back — the CLIENT then prints the actual outgoing/incoming MQTT5 User Properties, visibly proving "Write-side wiring"'s design (especially Case 3's brand-new server-reply capability) works as RUNNABLE, OBSERVABLE code, not just unit tests |

## Implementation phasing (suggested build order, Round 17)

**Added because "anything missing to PLAN the implementation" surfaced
that the Files-to-create table above is a flat list, not a build
sequence — for a doc this large (2 packages, 8 resolved design
decisions, write-side wiring, 2 side-track bug fixes), a
dependency-respecting order matters.** Each phase below only depends on
types/functions the PRIOR phase already introduced — not an arbitrary
ordering:

1. **reqreply core types** — `api/reqreply/property_param.go` (NEW).
   No adapter dependency; pure `codex`/`middleware` package types. Can
   be implemented and unit-tested in complete isolation first.
2. **reqreply dispatch** — `api/reqreply/middleware_declaration.go`,
   `transform.go` (NEW), `route.go` (edit — `RouteHandle.MiddlewareHandlers`/
   `ClientMiddlewareHandlers` fields + D6(b)/D7 checks + the NEW
   `checkParamConflicts`-equivalent). Depends on phase 1's
   `PropertyParam`/`MergedPropertyParam[T]`.
3. **reqreply AsyncAPI unification** — `api/reqreply/builder.go` (edit),
   unifying phase 2's spec contributions into Phase 1b's EXISTING
   `applyParamDeclarations`. Depends on phase 2's dispatch types
   existing to contribute FROM.
4. **reqreply adapter wiring — mqtt5** — `adapters/mqtt5/reqreply_transport.go`
   (edit), `errors.go` (edit, NEW `KindMiddleware`). Depends on phase
   2's `MiddlewareHandler`/`ClientMiddlewareHandler` types to consult.
   **Includes Case 3's write-side fix** (BOTH reply-publish call sites
   gain `replyProps.User` — the single most significant piece of new
   capability this whole doc adds, per "Write-side wiring").
5. **reqreply adapter wiring — zeromq** — `adapters/zeromq/reqreply_transport.go`
   (edit), `errors.go` (edit). Mirrors phase 4 minus property support
   (zeromq supplies an empty property map by design).
6. **reqreply tests** — `middleware_declaration_test.go`,
   `transform_test.go`, `property_param_test.go` (NEW), plus the
   `_test.go` additions to BOTH adapters (mqtt5/zeromq). Only
   meaningful once phases 1-5 exist to test against.
7. **events core types** — `api/events/property_param.go` (NEW).
   Mirrors phase 1 field-for-field (Phase 0 parity) — independent of
   reqreply's own phases 2-6, could be done IN PARALLEL with them if
   preferred, but sequenced after for a single reviewable narrative
   here.
8. **events dispatch + AsyncAPI** — `api/events/middleware_declaration.go`
   (edit), `transform.go` (edit), `builder.go` (edit — standalone spec
   contribution, no unification needed). Depends on phase 7.
9. **events adapter wiring + BOTH side-track bug fixes** —
   `adapters/mqtt5/adapter.go` (edit), `transform_dispatch.go` (edit),
   `binding.go` (edit). Depends on phase 8's `MiddlewareHandler`/
   `ClientMiddlewareHandler` 2-map signatures. **Bug 1** (precedence)
   and **Bug 2** (missing Observer calls) are BOTH scoped to this SAME
   file set — natural to fix alongside the property-var wiring that
   already touches these exact dispatch functions, not a separate pass.
10. **events adapter wiring — zeromq** — `adapters/zeromq/adapter.go`
    (edit), `binding.go` (edit). Mirrors phase 9's 2 bug fixes.
11. **events tests** — `middleware_declaration_test.go`,
    `transform_test.go`, `property_param_test.go` (NEW), plus
    `adapter_test.go` (edit) for BOTH adapters covering the 2 bug
    fixes. Only meaningful once phases 7-10 exist.
12. **Docs + example** — `.github/instructions/go-codex.instructions.md`
    (both `api/reqreply`/`api/events` rows), a NEW `docs/features/*.md`
    page, and the NEW `examples/reqreply-api/demo_property_axis_middleware.go`
    (see Files-to-create row above) — the demo specifically EXERCISES
    phase 4's Case 3 write-side fix end-to-end, so it's a genuine
    integration check, not just documentation.
13. **Final verification** — see "Definition of Done" below.

## Definition of Done (Round 17)

**Added for the same reason as "Implementation phasing" above — this
session's OWN doc-editing rounds have consistently used a verification
gate after every change; the actual CODE implementation deserves the
SAME discipline, stated explicitly rather than assumed:**

- [ ] `go fmt ./...` — no diff remains.
- [ ] `go build ./...` — compiles cleanly, zero errors.
- [ ] `go test ./...` — ALL packages pass, including every NEW
      `_test.go` file from BOTH `api/reqreply` and `api/events`, plus
      the adapter-level tests in `adapters/mqtt5`/`adapters/zeromq`
      (including the 2 side-track bug-fix tests and the "Write-side
      wiring" regression tests — see "Unit test plan").
- [ ] `just check` (staticcheck + gosec) — no NEW warnings introduced;
      no `//nolint`/`//gosec` suppressions added to silence new
      findings.
- [ ] The NEW `examples/reqreply-api/demo_property_axis_middleware.go`
      demo runs (`go run ./examples/reqreply-api`) and exits 0,
      visibly printing the property axis's actual wire-level User
      Properties (confirms "Write-side wiring" end-to-end, not just
      unit-tested).
- [ ] Every OTHER existing example still builds/runs cleanly (no
      stale-pattern regressions introduced by this doc's changes) —
      `for d in examples/*/; do go run ./$d; done` per this codebase's
      own established review practice.
- [ ] `.github/instructions/go-codex.instructions.md` updated for BOTH
      the `api/reqreply` row (new mechanism) and the `api/events` row
      (Phase 0 parity) — the single source of design truth stays in
      sync, per this codebase's own maintenance discipline.
- [ ] This roadmap doc's OWN status banner updated to reflect SHIPPED
      once merged — mirroring how other roadmap docs in this codebase
      (e.g. `zeromq-security.md`, `refreshing-cacheable.md`) get
      explicitly marked SHIPPED/superseded after their design is
      actually implemented, rather than left silently stale as
      "Design draft" forever.

## Out of scope (deferred)

- Retiring/changing Phase 1b's flat `FromUserPropertyParam`/
  `FromResponseUserPropertyParam` mechanism — stays exactly as shipped;
  this doc's NEW property axis is fully additive, a SECOND way to
  declare a property-backed concern (codec-backed, `Declaration[In,Out]`-
  typed) alongside the flat mechanism's existing, simpler one (raw
  string params on `middleware.Middleware`) — mirrors how D-0003 itself
  never touched REST's/events' own flat mechanisms either. **Precise
  carve-out (Round 18)**: this "stays exactly as shipped" claim covers
  the flat mechanism's FUNCTIONS/attachment/rendering-for-agreeing-
  declarations — it does NOT cover conflict-detection's dedupe
  algorithm for MISMATCHED declarations, which decision #5's Round 18
  revision DELIBERATELY changes (a narrow, accepted BREAKING CHANGE,
  not an oversight) — see decision #5 and "AsyncAPI spec rendering"
  for the exact scope of what changes vs. what doesn't.
- `ports.ReqReplyPattern`/`ports.EventPattern` changes — likely none
  needed, mirroring `reqreply-middleware.md`'s own identical conclusion
  for Phase 1 (`PluginReqReplyPattern`/`PluginEventPattern` already
  delegate to `Route.Register`/`Channel.Register`, so new
  `RouteHandle`/`ChannelHandle` fields populate for free).
- Extending to `mqtt`(v3) — N/A, no reqreply support exists there;
  events' `mqtt`(v3) support, if any, is unaffected since this doc's
  property axis targets `mqtt5`/`zeromq` only (same adapters reqreply
  targets).
- Retrofitting/fixing anything ELSE in events' shipped D-0003
  mechanism beyond the 2 specific, confirmed bugs in "Side track"
  above — e.g. NOT auditing REST's own D-0003 implementation for
  similar latent bugs (out of scope for this doc; a separate concern
  if ever needed).

## Open design decisions (to resolve before implementation)

1. ~~`ClientTransform`'s `Out` shape — request-enrichment only, or reply-
   inspection too?~~ **RESOLVED this round** — confirmed against
   `api/rest/transform.go`'s ACTUAL signatures: `rest.ClientTransform`'s
   fn produces `In` (encoded into the OUTGOING request), and `Out` is
   decoded MECHANICALLY (no Fn) from the actual response, via
   `ClientMiddlewareHandler.DecodeOut`. reqreply mirrors this exactly —
   no new shape needed, no genuinely new problem to solve; REST already
   solved "a boundary with both a request AND a response/reply direction"
   and reqreply's shape is structurally identical.
2. ~~Should `Transform`/`ClientTransform` be able to run a general
   codec-backed enrichment concern using MQTT5 User Properties, not just
   topic vars?~~ **RESOLVED this round, scope EXPANDED** — MQTT5 User
   Properties and (future) AMQP message headers are the SAME
   cross-transport concept (named metadata separate from payload,
   wire-realized differently per protocol), NOT MQTT5-specific.
   Decision: added as a SECOND vocabulary axis directly on core
   `reqreply.Middleware[In,Out]` (`WithRequestProperty`/
   `WithResponseProperty`, protocol-neutral naming — "Property," not
   "Header," avoiding HTTP-specific casing/multi-value implications
   that don't apply to MQTT5/AMQP) — see "The 'property' vocabulary
   axis" above for the full design. Confirmed this is ALSO a genuine gap
   in `api/events`' own `Middleware[In,Out]` — **UPDATED Round 8**:
   promoted from a deferred follow-up to Phase 0, implemented in the
   SAME round as reqreply's own axis (see "Phase 0: the property axis
   ships for `api/reqreply` AND `api/events` TOGETHER" above).
3. ~~**Naming**: is `reqreply.Middleware[In,Out]` the right name, given
   `middleware.Middleware` (the FLAT type) already exists and is
   imported by the SAME package?~~ **RESOLVED this round** — confirmed
   via grep: `api/reqreply` has ZERO existing unqualified `Middleware`
   identifier today, so `reqreply.Middleware[In,Out]` (package-qualified
   from outside, unqualified `Middleware[In,Out]` inside the package
   itself) coexists safely with the imported `middleware.Middleware`
   package-qualified reference — the EXACT SAME pattern `api/rest`/
   `api/events` already use with zero reported friction. No rename
   needed.
4. ~~Where should `PropertyParam`/`MergedPropertyParam[T]`/
   `NewPropertyParam[T,V]` live?~~ **RESOLVED this round (Round 5)** —
   user's decision: DUPLICATE per-package (`reqreply.PropertyParam` and
   a future `events.PropertyParam`), each an independently-defined
   wrapper type over the SAME shared `codex.Param`/`MergedParam[T]`/
   `NewParam[T,V]` primitives — exactly mirroring how `TopicParam`
   already works today (`events.TopicParam`/`reqreply.TopicParam` are
   two separate types, never one cross-package type). This matches this
   codebase's own established precedent (`codex/param.go`'s own doc
   comment: "each API layer keeps its own thin wrapper over the SAME
   shared `codex` primitive, never one cross-package type") rather than
   introducing a new, first-ever shared cross-package `Param`-family
   type. **UPDATED Round 8**: the events companion is no longer a
   future follow-up — see "Phase 0: the property axis ships for
   `api/reqreply` AND `api/events` TOGETHER" above for the now-concrete
   `events.PropertyParam` sketch, confirmed duplicating this exact
   design.
5. ~~Merge-field name conflict detection — REST-style ERROR on
   mismatch, or Phase 1b-style silent DEDUPE?~~ **RESOLVED this round
   (Round 2); REVISED Round 18 into a deliberate BREAKING CHANGE.**
   User's decision: follow REST's stricter
   `ConflictingParamContributionError` behavior (errors when two
   contributions disagree on kind/required-ness for the SAME name;
   agreeing contributions still dedupe into one spec entry, no error).
   **Round 18 correction**: an earlier revision of this decision
   claimed BOTH "unified with Phase 1b's existing pass" AND "Phase 1b's
   laxer behavior is NOT retroactively changed" — self-contradictory
   without an unstated origin-tracking rule. Presented with this
   choice, the user chose the SIMPLER option: ONE uniform algorithm
   applies to ALL contributions (Phase 1b's flat mechanism AND the new
   axis alike), no origin-based special-casing — Phase 1b's OWN
   silent-first-seen-wins dedupe for MISMATCHED declarations is RETIRED,
   a deliberate, ACCEPTED breaking change (not avoided), per the
   explicit "breaking changes are fine when they yield simpler,
   maintainable workflows" principle. Scope of the break is narrow:
   ONLY mismatched-declaration dedupe changes — Phase 1b's flat
   mechanism ITSELF (functions, `.Use()` attachment, agreeing-
   declaration rendering) stays completely unchanged (see "Out of
   scope" below for the precise carve-out). See "AsyncAPI spec
   rendering" above for the full mechanism and the new
   `ConflictingParamContributionError` type.
6. ~~NEW this round (Round 4) — which adapter `ErrorKind` wraps the NEW
   mechanism's own errors?~~ **RESOLVED this round.** Neither
   `mqtt5.ErrorKind` nor `zeromq.ErrorKind` has a value earmarked for a
   declared-middleware failure today. Existing Phase 1b precedent
   already uses `KindDecode` for topic-var decode failures (confirmed in
   `adapters/mqtt5/reqreply_transport.go`), which naturally extends to
   this mechanism's own merge-side errors: `KindDecode` for a
   `MiddlewareInputError` (request-side merge failure, whether topic or
   property), `KindEncode` for an output-merge failure (reply-side).
   `MiddlewareError` (D2's fn-business-error fallback) needed a decision
   — user's choice: add a NEW `KindMiddleware` value to BOTH
   `mqtt5.ErrorKind` and `zeromq.ErrorKind` (option (b), over reusing
   `KindHandler`), giving an `OnError`/Observer consumer a clear signal
   that the failure happened in DECLARED MIDDLEWARE, before the real
   handler ran, distinguishable from a genuine handler failure. This
   adds one new enum value to each adapter's existing `ErrorKind` type
   (alongside `KindDecode`/`KindHandler`/`KindEncode`/`KindTimeout`/
   `KindSecurity`), with a `String()` case added to match.
7. ~~NEW this round (Round 11) — should `PropertyParam`/
   `MergedPropertyParam[T]` support OPTIONAL (non-required)
   properties?~~ **RESOLVED this round.** Confirmed `codex.NewParam`
   hardcodes `RequiredField`, making every property declared via the
   originally-sketched `NewPropertyParam` unconditionally required, with
   no way to express "optional" — unlike a topic var (structurally
   always required, correctly has no `Required` field), a property is
   conceptually optional metadata a message may legitimately omit.
   Confirmed `codex.OptionalField` already exists as `RequiredField`'s
   counterpart, unused by `NewParam`. User's decision: ADD optional-
   property support NOW, as part of Phase 0 — a `Required bool` field
   on `PropertyParam`/`MergedPropertyParam[T]` (mirroring
   `HeaderParam.Required`, NOT added to shared `codex.Param`) plus a NEW
   `NewOptionalPropertyParam[T,V]` constructor, hand-built against
   `codex.OptionalField` (zero `codex` package changes needed). Applied
   identically to BOTH `api/reqreply` and `api/events` (Phase 0 parity).
   This ALSO resolves a previously-unexamined ambiguity: the conflict-
   detection section's own `Required: true`/`Required: false` example
   had no genuinely analogous case to check before this fix (neither
   `TopicParam` nor `PropertyParam` had a `Required` field) — now it
   does, for properties specifically.
8. ~~NEW this round (Round 15) — TWO sub-questions found while tracing
   REST's REAL `checkParamConflicts`/`applyParamDeclarations` in full
   for the first time: (a) should topic-vars and properties share ONE
   conflict-detection namespace (mirroring REST's real header/cookie/
   query cross-kind strictness) or be INDEPENDENT namespaces? (b)
   should conflict detection compare codecs, given REST's REAL
   `paramContribution` struct has NO Codec field at all (contradicting
   this doc's own earlier prose, which overstated REST's real
   behavior)?~~ **RESOLVED this round.** (a) User's decision:
   INDEPENDENT namespaces — topic-vars and properties never conflict
   with each other even sharing the same name, justified by genuinely
   different wire locations (stronger justification than REST's own
   header/cookie/query boundary, which are all "just parts of one HTTP
   request"). (b) User's decision: DELIBERATELY go beyond REST's real
   precedent — add a Schema-based codec comparison (via
   `reflect.DeepEqual` on `Codec.Schema`, nil-vs-non-nil treated as a
   mismatch), explicitly flagged as an intentional divergence, not a
   misreading of REST's behavior. See "AsyncAPI spec rendering" above
   (both reqreply's and events' sections) for the full design. Also
   spawned a SEPARATE companion roadmap doc,
   `rest-middleware-conflict-detection-improvements.md`, evaluating
   whether REST's OWN real precedent should eventually adopt either
   change too (a distinct, out-of-scope-for-THIS-doc question, since it
   would be a BEHAVIORAL CHANGE to already-shipped REST code with
   existing callers, unlike this doc's brand-new mechanism).
