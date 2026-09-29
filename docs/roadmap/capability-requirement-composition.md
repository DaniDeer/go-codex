# Composable Capability Requirements — declare-first, adapter-satisfies-second

> **Status:** Phases 1-2 SHIPPED; Phase 3's CAPABILITY MECHANISM
> SHIPPED (the sibling `adapters/zeromqrest` adapter build is
> deliberately OUT of this roadmap's own scope — an independent future
> effort, see `docs/roadmap/zeromq-rest-adapter.md`); Phase 4 (the
> Apply-interface, API-layer-owned dispatch redesign — `api/events` +
> `adapters/mqtt5`/`adapters/zeromq`) SHIPPED, including Phase 4b (a
> post-hoc "zero backdoor" guardrail audit that found and closed 3 real
> gaps — see the new "Architectural guardrail" section below and Phase
> 4's own Phase 4b subsection); Phase 4c (closing `Client.Publish`/
> `Subscribe`'s Capabilities gap) SHIPPED; Phase 4d (Attach factory
> redesign — adapters expose `New*Transport` factories, attaching is
> EXCLUSIVELY an api-layer method, all 12 adapter-namespaced `Attach*`
> convenience functions removed) SHIPPED; Phase 4e (closing the
> remaining format/security/middleware "v1 scope" gaps) SHIPPED; Phase 5
> (closing `adapters/mqtt` v3's Phase 4 pub/sub Capability parity gap,
> and applying the same `Apply`/`ApplyCapabilities` shift to
> `api/reqreply`'s mqtt5/zeromq call sites) SHIPPED; Phase 5a (moving
> the type-safe escape hatch itself onto the API layer, formerly
> "Phase 4f") SHIPPED; Phase 6 (`api/rest` Header/Cookie/Query
> real-interface promotion, `HeaderCapableTransport`/
> `CookieCapableTransport`/`QueryCapableTransport` now genuine
> `Extract`-shaped methods on a new `httpCarrier` type) SHIPPED; Phase
> 6a (SSE coverage-check gap closed + `adapters/websocket` brought into
> the same mechanism via a new `wsCarrier` type + cookie support) SHIPPED;
> Phase 7 (ErrorPattern: `ErrorResponseWriter` interface + new
> `rest.PendingCookie`/`DispatchErrorResponse`/
> `CallDispatchErrorResponse`, deleting 2 of 4 remaining
> nethttp/chi ErrorPattern write functions outright — `SetCookie`
> correctly excluded, still public API; Observer: formally CLOSED as
> correctly adapter-owned) SHIPPED; Phase 8 (Review & Closeout)
> pending. See each subsection's own Learnings entry. Spun out of a
> user question about
> [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md)'s
> scope (events-only) while reviewing [`docs/features/capabilities.md`](../features/capabilities.md).
> Planned as 3 sequential implementation phases (`api/events` →
> `api/reqreply` → `api/rest`), each following its own
> design → implement → examples → docs → learnings cycle, followed by a
> Phase 4 review/closeout once all three ship — see "Implementation
> approach" below.
> [← Back to Roadmap](index.md)

## Motivation

**This doc's goal is dual-sided, by design — not declaring-user
convenience alone.** It aims to simplify the API for TWO distinct
audiences at once, via ONE shared vocabulary:

- **The declaring user** (writing `rest.NewRoute`/`events.NewChannel`/
  `reqreply.NewRoute` calls) should be able to compose their requirements
  as a set of small, named capabilities — independent of which adapter
  eventually attaches — with the adapter's `Attach` step acting as the
  SATISFACTION check, exactly like Go's own implicit interface
  satisfaction: declare requirements, then verify an adapter provides ALL
  of them, or reject the attach with a precise reason.
- **The adapter implementer** (writing a new `adapters/<transport>`
  package) should get an EQUALLY simplified, transparent contract:
  exactly what a baseline adapter for a pattern must provide, exactly
  which capabilities they MAY optionally add support for over time, and
  exactly which they never need to (and correctly should not) touch —
  visible at a glance, not discovered by trial and error against a
  monolithic, all-or-nothing interface.

The SAME three-tier classification — **Baseline**, **Implicit**, and
**Explicit** (defined in full below) — is read from both sides at once:
what's mandatory-vs-optional for a declaring user to request is exactly
what's mandatory-vs-optional for an adapter author to implement. Today,
declaring a channel's protocol behavior in `api/events` means importing a
concrete adapter package (`adapters/mqtt5`, `adapters/zeromq`, ...) to
name a capability value (`mqtt5.QoSAtLeastOnce`), even though the USER'S
intent at declare time is protocol-agnostic ("this channel needs
at-least-once delivery," not "this channel needs `mqtt5.QoS`
specifically") — and the mechanism for ADAPTER authors to progressively
add or correctly omit such capabilities, while staying fully transparent
to declaring users, has never been named as a general principle either,
even though two working precedents already exist (see "Design guardrails"
below). Two further gaps, from the same conversation: this declare-then-verify
pattern only exists for `api/events` today (`api/rest`/`api/reqreply` have
nothing equivalent), and there's no "I already know I'm using MQTT5/AMQP,
give me a bundled preset" convenience for users who don't want to compose
requirements one at a time.

## Architectural guardrail: zero backdoors between the api layer and adapters

**Added in Phase 4b**, in response to an explicit user directive, and
elevated to THE central, non-negotiable goal of this entire roadmap AND
of [D-0006](../design/d-0006-protocol-native-capabilities.md) itself —
stated verbatim: *"There should be no backdoor open between the api
layer of go-codex and the adapters. This is the goal of this whole
design d-0006 and this roadmap plan."*

**The rule, stated precisely:** every interaction through which an
adapter fulfills an API/communication-pattern requirement DECLARED on a
route/channel (or its middleware) MUST go through the `Capability` +
`Apply(Target) (bool, error)` interface mechanism, dispatched
EXCLUSIVELY by the API-layer-owned `events.ApplyCapabilities` (and its
future `reqreply`/`rest` mirrors). **Zero raw-value bypass paths. Zero
parallel, non-`Capability`-shaped declaration mechanisms for the same
protocol-native concern** — not even as an "additive, still-supported
legacy path." If a SECOND way to express the same protocol-native
concern exists anywhere between a route/channel declaration and the
wire, that is a backdoor and must be closed, not merely documented as a
fallback.

**Why this needed its own explicit statement, not just "Phase 4
shipped":** a post-hoc audit of the ALREADY-SHIPPED Phase 4 code (see
Phase 4b below) found that shipping the `Capability`/`Apply`/
`ApplyCapabilities` mechanism for the CORE `Subscribe`/`Publish` dispatch
path was necessary but not sufficient — THREE separate backdoors
survived in code Phase 4 had already touched:

1. A ports-binding constructor (`mqtt5.SubscribeAdapter`) took a raw
   `qos byte` parameter and wrote it directly into the native wire
   struct, never touching `Capability`/`Apply` at all.
2. A ports-binding options struct (`MQTT5DrainPublishOptions`) exposed
   raw `QoS byte`/`Retained bool` fields — internally folded into
   `Capability` values before the wire call, but the DECLARATION
   surface itself was still a raw-value shape, not something a user
   configures via the `Capability` interface.
3. A THIRD, transport-agnostic, route/channel-level mechanism
   (`events.MQTTQoS`/`events.PublishAttributes`/
   `Publisher.WithAttributes`) predated this whole redesign and was
   explicitly documented as "additive, still-fully-supported legacy" —
   the clearest violation, since it is declared ON THE CHANNEL itself
   and entirely bypasses `Capability`.

**This is why "purely additive, nothing deprecated" is the WRONG
default posture for this roadmap**, even though it is normally sound
API-evolution practice elsewhere in this codebase. A capability
mechanism whose entire PURPOSE is "the sole, compile-time-checked way an
adapter fulfills a declared protocol-native requirement" cannot coexist
with an undeprecated parallel path for the identical concern — the
parallel path is not a convenience, it is a hole in the guardrail. Every
phase of this roadmap (past and future) must be re-read against this
rule, and Phase 7 ("Review & Closeout" — D-0006's own rework) must state
this guardrail as D-0006's own explicit design goal, not merely link
back to this doc.

## Design guardrails: one three-tier framework, read from both sides

**Added this round**, in response to a user course-correction: D-0006's
implementation shipped ONE tier of this model cleanly
(`CapabilitySpec`/`CheckCapabilityCoverage`, the "explicit" tier below),
but never named or generalized the fact that TWO further tiers are
equally real, and that EVERY tier means something specific on BOTH sides
of the declare/attach boundary — not just to the declaring user. Every
capability (new or existing) is classified into exactly one of three
tiers, and for each tier this doc states what it means BOTH for the
person declaring an API and for the person implementing an adapter for
it — this symmetry is the actual point, not a side note:

### Tier 1 — Baseline capability (new this round)

- **What it is:** Route (REST: HTTP method + path template) / Topic
  (events, reqreply: topic template) — matching the declared address and
  encoding/decoding the declared `Req`/`Resp`/payload codec. The single
  most basic thing a pattern IS.
- **Declaring-user side:** never requested — it's simply true the moment
  a route/channel/reqreply-route is declared via `rest.NewRoute`/
  `events.NewChannel`/`reqreply.NewRoute`. There is no `RequireRoute()`/
  `RequireTopic()` call because there is nothing to opt into; it's the
  precondition for everything else in this doc to even apply.
- **Adapter-author side:** MANDATORY — implementing this is what makes a
  package a valid REST/events/reqreply adapter at all. Already
  compile-time-enforced today, with ZERO new code needed: `ports.Pattern`'s
  sealed `RESTPattern`/`EventPattern`/`ReqReplyPattern` types plus the
  underlying `ports.SourceAdapter[T]`/`ports.SinkAdapter[T]`/
  `ports.IOAdapter[Req,Resp]` interfaces already gate this — an adapter
  that doesn't implement them simply isn't a REST/events/reqreply adapter,
  full stop, and never compiles as one. This tier's ONLY contribution
  here is NAMING it, so the full taxonomy (baseline → implicit →
  explicit) is complete and adapter authors have a term for "the floor
  every one of my capability decisions sits on top of."

### Tier 2 — Implicit capability

- **What it is:** a requirement that ARISES as a side effect of declaring
  a codec-backed field on the route/channel/reqreply-route. The declaring
  user never writes "require X" — declaring the field IS the requirement.
- **Declaring-user side, two shipped examples that sit at DIFFERENT levels
  today** (an important, honest distinction, not glossed over):
  - **`rest.HeaderParam`/`rest.CookieParam`** — declared at the ROUTE
    level (`rest.NewRoute(..., HeaderParam{...})`), BEFORE any concrete
    adapter is chosen. Genuinely ADAPTER-AGNOSTIC: either
    `adapters/nethttp` or `adapters/chi` can attach, because both
    structurally support HTTP headers/cookies.
  - **`mqtt5.UserPropertyParam`** — declared on `mqtt5.SubscribeOptions`/
    `mqtt5.ServeOptions` directly, which means the caller has ALREADY
    chosen mqtt5 by the time they write it — there is no channel-level,
    pre-adapter-choice place to declare it the way `HeaderParam` allows.
    NOT, today, a true adapter-agnostic implicit capability in
    `HeaderParam`'s sense — captured as an open design decision below.
- **Adapter-author side:** OPTIONAL, symmetric to the declaring side —
  implement the matching option/field (a `HeaderParam`-equivalent
  extraction path, an options-struct field like `UserPropertyParam`), or
  don't. Either way the adapter stays VALID; a declaring user who needs
  the field simply cannot attach that adapter to a route/channel that
  declares it (today this is a trivially-always-true / trivially-never-
  possible structural fact, not a runtime check — REST has one transport
  family so `HeaderParam` is always satisfiable [Phase 3 changes this
  premise — see "Implementation approach" below, where REST gains a
  second transport family and this check becomes genuinely runtime-
  enforced], and mqtt/zeromq simply have no `UserPropertyParam`-shaped
  field to construct in the first place, so the mismatch is a compile
  error by omission). **Worked example — leaving an implicit capability
  out is equally a first-class, permanent, correct outcome, not a gap:**
  a hypothetical ZeroMQ request/response adapter correctly omitting
  header/cookie-equivalent support entirely — it stays a fully valid
  `api/rest` adapter; a declaring user who needs a `HeaderParam` simply
  can never attach it to that route, which is exactly the point.

### Tier 3 — Explicit capability

- **What it is:** a standalone requirement, declared directly, NOT
  derived from any codec field — the user states the protocol behavior
  they want by name.

Tier 3 splits into TWO sub-shapes on the adapter-author side — both are
still Tier 3 from the DECLARING side (standalone, not field-derived);
they differ only in how an adapter author fulfills them. Conflating the
two (found while reviewing how the already-shipped `DeadLetter` pattern
fits this framework) was a real gap in this doc's own vocabulary, now
closed:

#### Tier 3a — Sealed, adapter-owned

- **Declaring-user side, shipped example:** `events.CapabilitySpec`
  (renamed to `events.CapabilityRequirement` by Phase 1 — see
  "Implementation approach" below; `RequireQoS`/`RequireRetained` per
  this doc's own proposal, section above) — declared at the CHANNEL
  level, before any adapter is chosen, verified at `Attach` time via
  `CheckCapabilityCoverage`. Plus this round's new example: a future AMQP
  "message exchange/queue topology" requirement — satisfiable "out of the
  box as a protocol" only by an AMQP-family adapter (no code shipped yet;
  tracked in `docs/roadmap/amqp-adapter.md` and D-0006 section 6's
  survey).
- **Adapter-author side:** OPTIONAL and PROGRESSIVE — an author starts
  with Tier 1 (baseline) satisfied, then adds explicit-capability support
  incrementally over time via the ALREADY-shipped sealed `Capability`
  mechanism (`add-a-new-adapter` skill's **Step 5e**, MANDATORY once an
  adapter adds ANY protocol-specific toggle — see
  `adapters/mqtt5/capability.go`/`adapters/zeromq/capability.go` for the
  reference shape: `HWMSetter`/`ConflateSetter`-style optional
  extensions, "a documented no-op, not an error," when a concrete
  `FramedSocket`/client doesn't implement one). **Leaving a capability
  out is a first-class, permanent, correct outcome — not a partial
  implementation or a gap.** Worked example: an MQTT5 adapter correctly,
  forever, never implementing AMQP's exchange/queue capability, because
  no one would ever want an MQTT5 adapter to fake AMQP topology. This is
  safe specifically BECAUSE `CheckCapabilityCoverage` (explicit) /
  structural type-non-satisfaction (implicit) already make a declared-
  but-unsatisfied requirement transparent and diagnosable at attach
  time — the mismatch is never silently swallowed.
- **Genuine per-adapter mismatch is POSSIBLE and is exactly what this
  shape exists to catch.** A sealed per-adapter `Capability` type means an
  adapter can legitimately lack the capability entirely — that's the
  whole point of `CheckCapabilityCoverage`.

#### Tier 3b — Shared-declaration, universally-realizable

- **Shipped example, traced through actual code:** `events.DeadLetter`/
  `reqreply.DeadLetter` (`api/events/dead_letter.go`/
  `api/reqreply/dead_letter.go`) — declared directly on `NewChannel`/
  `NewRoute` (like 3a), renders a full `asyncapi.ChannelItem` (a
  receive-only operation carrying `DeadLetterEnvelope`'s schema, deduped
  by topic), and is a fully independent type PER PACKAGE (zero shared
  type between `events`/`reqreply` — pre-existing precedent that directly
  validates Phase 2's own placement decision for `CapabilityRequirement`).
- **The critical difference from 3a: NO sealed per-adapter type exists at
  all.** Confirmed by tracing every adapter's dispatch code: `adapters/mqtt`,
  `adapters/mqtt5`, and `adapters/zeromq` ALL call
  `handle.DeadLetterFor(...)` UNCONDITIONALLY, then perform a PLAIN
  `client.Publish(topic, body)` using whatever publish primitive that
  adapter already has for ordinary messages. There is no
  `mqtt5.DeadLetterCapability`-shaped sealed type, no `CheckCapabilityCoverage`
  call, no possibility of "this adapter doesn't implement dead-lettering"
  — because the realization ("publish this envelope to another topic")
  is something ANY pub/sub or reqreply adapter can already do, by
  definition of being a pub/sub or reqreply adapter at all. There is
  nothing to check for absence, so no coverage-check mechanism was ever
  built for it — CORRECTLY, not as a gap.
- **This is D-0006 section 6's OWN already-identified "third category,"**
  now given a name in this doc's vocabulary: declared INTENT is trivially
  uniform across every adapter (one shared Go type suffices), but
  REALIZATION cost genuinely varies per-adapter underneath that SAME
  declared value — AMQP realizes it broker-natively via queue arguments
  (zero runtime cost, no application code); `mqtt`/`mqtt5`/`zeromq`
  realize the IDENTICAL declared value via application-level re-publish
  (real runtime cost) — but this cost difference NEVER surfaces as a
  Go-level compile-time or coverage-check mismatch, unlike 3a.
- **When to reach for 3b instead of 3a:** a capability qualifies for 3b
  ONLY when its declared intent is trivially uniform AND every adapter
  the API spans can ALREADY realize it using a primitive that
  transport/pattern already requires it to have (e.g. "publish somewhere
  else" for any pub/sub-shaped transport). If even ONE plausible future
  adapter genuinely could NOT realize it, use 3a instead — sealing
  incorrectly (3b for something that should be 3a) would silently hide a
  real "this adapter can't do this" case that a coverage check should
  have caught.

### Why this is one framework, not three separate rules

The three tiers, read together, ARE the interface-composition idea in
concrete form: a declaring user's fully composed requirement set —
baseline (always) + whichever implicit fields they declared + whichever
explicit capabilities they requested — is EXACTLY the interface an
attaching adapter must satisfy. An adapter author's implemented
capability set (baseline, always; plus whichever implicit fields and
explicit `Capability` types they chose to support) is EXACTLY what
they're offering to satisfy it with. `Attach` succeeding is nothing more
than "the offered set is a superset of the required set" — mirroring
Go's own implicit interface satisfaction, one layer up, and fully
symmetric: a declaring user sees "what must I ask for / what can I ask
for," an adapter author sees "what must I implement / what can I
implement / what should I never bother implementing," from the exact
same three-tier vocabulary. Tier 3's OWN 3a/3b split (above) doesn't
fragment this — both sub-shapes are still Tier 3 from the declaring
side (standalone, not field-derived); they differ ONLY in which
adapter-author-side enforcement mechanism applies, exactly the same way
Tier 2's `HeaderParam`-vs-`UserPropertyParam` distinction is one tier
read at two different levels, not two tiers.

**The guardrail going forward:** when this doc (or its implementation)
introduces a new capability, or when reviewing an existing one for
classification, always answer, from BOTH sides: (1) which tier is this —
baseline, implicit, or explicit? (2) for an implicit one, is it truly
declared at the route/channel/reqreply-route level BEFORE an adapter is
chosen (like `HeaderParam`), or is it actually scoped to a specific
adapter's own options struct already (like `UserPropertyParam` today)?
(3) for an explicit one on the adapter-author side, is this capability
Step 5e's sealed mechanism, and if the adapter's protocol genuinely
cannot support it, is that recorded as a deliberate, documented omission
(not a TODO)? (4) **for an explicit one, is it Tier 3a (sealed,
per-adapter, a genuine "can't implement it" case is possible — needs
`CheckCapabilityCoverage`) or Tier 3b (one shared Go type, every adapter
the API spans can ALREADY realize it via a primitive it's required to
have anyway — needs no coverage check at all)?** Picking the wrong
sub-shape either forces an artificial sealed-per-adapter type onto
something every adapter can trivially do (3b masquerading as 3a — extra
ceremony for no safety benefit), or forces a single shared declaration
onto something that genuinely needs per-adapter compile-time mismatch
safety (3a masquerading as 3b — silently hiding a real "this adapter
can't do this" case a coverage check should have caught). Conflating
tiers, or describing only the declaring-user half, was the exact gap
this round's course-correction identified in D-0006's original
implementation.

## Relationship to D-0006 — reused mechanism, NOT reopened value-sharing decision

D-0006 (graduated) already ships the requirement-declare / adapter-verify
HALF of this idea for events (names below are the ORIGINAL D-0006 shape —
Phase 1, under "Implementation approach" below, RENAMES
`CapabilitySpec`→`CapabilityRequirement` and
`MissingCapabilityError`→`CapabilityCoverageError`; this section
describes the pre-Phase-1 baseline this whole doc builds from):

- `events.CapabilitySpec{Name, Description}` — a declare-time `ChannelOpt`
  saying "this channel requires capability X," independent of any
  concrete adapter.
- `events.CheckCapabilityCoverage(topic, declared, supplied)` — an
  attach-time check (called automatically by every adapter's
  `ServeSubscribers`) comparing declared specs against the capabilities
  actually supplied, returning a typed `*events.MissingCapabilityError`
  (already `slog.LogValuer`) on drift.
- `stats.CapabilityObserver.RecordCapabilityApplied` — fires per
  successfully-applied capability.

This is genuinely the "declare a requirement, adapter satisfaction is
checked, mismatch is a precise typed error" shape the user described —
just scoped to events only, and runtime-checked (Go generics cannot
express "this adapter type satisfies THIS open-ended per-channel SET of
sealed capability types" as a single compile-time constraint without
either reflection or a closed, enumerable list of capabilities known in
advance — noted as a real limitation, not solved by this doc).

**What this doc does NOT propose:** a shared, cross-adapter capability
VALUE type (e.g. one `events.QoS` enum spanning mqtt/mqtt5/zeromq). D-0006
section 2's two-part test (compatible shape + uniform-enough support)
already surveyed this and rejected it with worked reasoning — ZeroMQ has
no QoS concept and no retained-message concept at all, so a shared value
type would either force ZeroMQ to fake support for something it
structurally lacks, or silently no-op in a way that defeats the whole
point of a REQUIRED capability. Capability VALUES stay adapter-owned and
sealed, unchanged.

**What this doc DOES propose:** (a) a thin, shared REQUIREMENT-NAMING
vocabulary so a caller can express "I need at-least-once delivery" without
importing an adapter package purely to name the requirement (the concrete
VALUE supplied at `Attach` time still comes from the adapter, unchanged);
(b) generalizing the declare-then-verify PATTERN itself (not the specific
`events.CapabilitySpec` type) to `api/reqreply`; (c) adapter-side preset/
bundle constructors for common requirement combinations.

**REST's scope is now resolved, not deferred — see "Implementation
approach" below for the concrete Phase 3 design.** D-0006 section 5.4
concluded REST needs no `Capability` mechanism BECAUSE it was
single-transport at the time; Phase 3 changes that premise deliberately,
via a new, genuinely synchronous, transport-stateless ZeroMQ REQ/REP
adapter (NOT via mqtt5/mqtt, which are permanently excluded from
`api/rest` by the new REST-eligible-transport guardrail Phase 3
introduces). This is no longer an open architectural question this doc
declines to touch — it's Phase 3's committed scope, sequenced last
because it depends on Phases 1-2's learnings and is the biggest rework.

## Implementation approach: phased design, implement, document, and learn

**This doc's shipping process, stated once, applied identically to
Phases 1-3:** each phase runs its own 5-step cycle — nothing starts
Phase N+1's Design step until Phase N's Learnings step has updated this
doc with real findings.

1. **Design** — this roadmap doc is updated with THAT phase's fully
   resolved API surface (every open decision for that phase answered,
   not deferred) BEFORE any code is written. A genuine Refine-mode pass
   per phase, not one upfront design covering all three.
2. **Implement** — code against the resolved design, satisfying the
   `plan-a-new-codex-feature` skill's six mandatory requirements
   (structured errors, observer integration, unit tests, three-surface
   documentation, boundary symmetry where applicable, and — for Phase 3
   specifically — the new-adapter checklist in the `add-a-new-adapter`
   skill).
3. **Examples** — update that pattern's existing runnable example(s) so
   they demonstrate the new/changed mechanism, not just the old one.
4. **Docs** — update that pattern's OWN Zensical feature/guide pages, in
   addition to the always-mandatory `.github/instructions/go-codex.instructions.md`
   sync.
5. **Learnings** — before the NEXT phase's Design step begins, this doc
   is updated with concrete findings from THIS phase that resolve the
   next phase's speculative open decisions with real evidence — not
   assumption.

**Cross-cutting alignment — applies to EVERY phase's Design step, stated
once here rather than repeated three times:**

- **D-0003's declarative middleware (`Middleware[In,Out]`/`Transform`/
  `ClientTransform`/`.Use(mw)`) is ALREADY SHIPPED for all three APIs**
  (`rest.Middleware[In,Out]`, `events.Middleware[In,Out]`,
  `reqreply.Middleware[In,Out]`) — this doc's capability mechanism does
  NOT compete with or fold into it. D-0006 §3 already RESOLVED this exact
  question for events, via a prototyped 4-stage lifecycle model (declare
  → capability-declare → handler-attach → adapter-attach):
  `Middleware[In,Out]` (structured, codec-backed I/O) and `Capability`/
  `CapabilityRequirement` (protocol-native toggles) are BOTH stage-2,
  declare-time, spec-contributing declarations — the SAME kind of thing,
  differing only in payload shape — but they share NOTHING at the Go-type
  level and must NOT be merged into one type. Each phase's Design step
  must EXPLICITLY CONFIRM (not silently assume) this same non-conflict
  holds for that API's own `Middleware[In,Out]`/`Transform`/`.Use(mw)`
  wiring — mirroring D-0006's own "confirmed via a real prototype, not
  merely reasoned about" bar, since reqreply's and REST's dispatch
  specifics differ from events'.
- **`stats.CapabilityObserver.RecordCapabilityApplied` is the SAME
  shared, optional observer extension across all three APIs — never a
  new, per-API observer interface.** Phase 1 already wires it into all 3
  event adapters; Phase 2 wires the SAME interface into reqreply's attach
  path; Phase 3 wires the SAME interface into the new ZeroMQ REST
  adapter. One vocabulary, one extension point, reused three times — not
  reinvented per API.

### Phase 1 — `api/events` (rewrite the shipped D-0006 mechanism)

**Status: SHIPPED.** Design finalized, implemented, tested, documented,
and verified (`go fmt`/`go build`/`go test ./...`/`just check`/all
examples all green). See the Learnings entry at the end of this
subsection for what carries forward to Phase 2.

**Design — RESOLVED, breaking changes deliberately chosen (no
compromise):** the sole user of go-codex has confirmed breaking changes
are fully acceptable and precision/strength take priority over
compatibility with the existing shipped surface — the "Removing an old
API" checklist's caution about real consumers still applies MECHANICALLY
(every reference must be migrated, not silently missed), but migration
itself, not avoidance, is the chosen path.

**D-0003 cross-cutting mirror** (per the "Cross-cutting alignment" note
above, applied here for consistency even though the answer is trivial):
renaming `CapabilitySpec`→`CapabilityRequirement` does not change its
relationship to `events.Middleware[In,Out]` (D-0003) at all — they remain
two independent stage-2 declarations sharing nothing at the Go-type
level, exactly as D-0006 §3 already resolved. No further action needed
for Phase 1 specifically.

**The one real limitation found while designing this phase, now FIXED
(not merely documented):** `CheckCapabilityCoverage` today matches a
declared requirement against a supplied capability by NAME STRING ONLY —
declaring "needs QoS at least `ExactlyOnce`" and an adapter supplying
`QoSAtMostOnce` was never caught as a mismatch; only presence of the name
"QoS" mattered. Phase 1 fixes this via a NEW optional interface,
`LeveledCapability` (mirrors the ALREADY-shipped, proven optional-interface
technique `CapabilityName` uses — `api/events` still never imports an
adapter package to do this):

```go
// api/events/capability.go — rewritten

// CapabilityRequirement declares one adapter-defined protocol-native
// capability requirement (Tier 3 — Explicit) at the channel level,
// independent of any adapter until Attach time. Renamed from
// CapabilitySpec — matches this package's own Capability* naming family
// (CapabilityName/CapabilityNameOf/CapabilityObserver) and this doc's own
// "declare a requirement" vocabulary; "Spec" was ambiguous with AsyncAPI
// spec-rendering.
type CapabilityRequirement struct {
	Name        string
	Description string
	// MinLevel, when non-nil, requires the supplied capability matching
	// Name to ALSO implement LeveledCapability and report Level() >=
	// *MinLevel — a genuine VALUE check. When nil, presence-by-Name is
	// sufficient — correct for boolean/no-natural-ordering capabilities
	// (Retained, Conflate).
	MinLevel *int
}

// applyChannel — UNCHANGED body from today's CapabilitySpec.applyChannel,
// just on the renamed receiver type; no behavior change.
func (r CapabilityRequirement) applyChannel(cb *channelBuilder) {
	cb.requirements = append(cb.requirements, r)
}

// LeveledCapability is an OPTIONAL extension to an adapter's own sealed
// Capability value. Implement Level() to let a CapabilityRequirement with
// a non-nil MinLevel be checked for VALUE, not just presence.
type LeveledCapability interface {
	CapabilityName
	Level() int
}

// CapabilityName/CapabilityNameOf are UNCHANGED — not renamed, not
// touched by Phase 1 at all (everything else in this file IS renamed;
// stated explicitly so nothing is left ambiguous).

// CheckCapabilityCoverage — EXACT algorithm, duplicate-handling rules
// PINNED DOWN (previously unspecified prose, now precise):
//   - supplied is reduced to a by-Name map first; if supplied contains
//     TWO capabilities with the same Name, the LAST one in the slice
//     wins (simple, deterministic, mirrors a map-overwrite — a caller
//     supplying duplicate same-name capabilities is a caller mistake,
//     not something this function needs to reject).
//   - each entry in declared is checked INDEPENDENTLY against that map;
//     declaring the same Name twice is allowed (redundant, harmless) —
//     each occurrence is evaluated on its own.
//   - a Name absent from the map -> Missing.
//   - a Name present, MinLevel nil -> presence sufficient, no further
//     check.
//   - a Name present, MinLevel non-nil, matched value doesn't implement
//     LeveledCapability -> Insufficient{Name, Required: *MinLevel, Supplied: -1}.
//   - a Name present, MinLevel non-nil, matched value implements
//     LeveledCapability but Level() < *MinLevel -> Insufficient{Name,
//     Required: *MinLevel, Supplied: level}.
func CheckCapabilityCoverage(topic string, declared []CapabilityRequirement, supplied []any) error {
	have := make(map[string]any, len(supplied))
	for _, c := range supplied {
		have[CapabilityNameOf(c)] = c // last-wins on duplicate Name
	}
	var missing []string
	var insufficient []LevelMismatch
	for _, d := range declared {
		sc, ok := have[d.Name]
		if !ok {
			missing = append(missing, d.Name)
			continue
		}
		if d.MinLevel == nil {
			continue
		}
		lc, ok := sc.(LeveledCapability)
		if !ok {
			insufficient = append(insufficient, LevelMismatch{Name: d.Name, Required: *d.MinLevel, Supplied: -1})
			continue
		}
		if lc.Level() < *d.MinLevel {
			insufficient = append(insufficient, LevelMismatch{Name: d.Name, Required: *d.MinLevel, Supplied: lc.Level()})
		}
	}
	if len(missing) == 0 && len(insufficient) == 0 {
		return nil
	}
	return &CapabilityCoverageError{Topic: topic, Missing: missing, Insufficient: insufficient}
}

// CapabilityCoverageError replaces MissingCapabilityError — the old name
// no longer described its own shape once Insufficient was added.
type CapabilityCoverageError struct {
	Topic        string
	Missing      []string
	Insufficient []LevelMismatch
}

type LevelMismatch struct {
	Name     string
	Required int
	Supplied int // -1 when the matching capability didn't implement LeveledCapability at all
}

// Error — EXACT text, pinned down for implementation:
//   `api/events: topic %q declares capabilities %v missing, %v insufficient`
// (Missing/Insufficient rendered via their own %v; an empty slice prints
// as "[]", matching MissingCapabilityError's own prior wording style.)
func (e *CapabilityCoverageError) Error() string {
	return fmt.Sprintf("api/events: topic %q declares capabilities %v missing, %v insufficient", e.Topic, e.Missing, e.Insufficient)
}

// LogValue — slog.GroupValue: topic (string), missing (Any, the raw
// []string), insufficient (a slog.Group PER LevelMismatch — name/
// required/supplied keys — not just %v-dumped, so each mismatch is
// independently queryable in structured logs).
func (e *CapabilityCoverageError) LogValue() slog.Value {
	mismatches := make([]any, len(e.Insufficient)) // []any holding slog.Value elements — valid for slog.Any
	for i, m := range e.Insufficient {
		mismatches[i] = slog.GroupValue(
			slog.String("name", m.Name),
			slog.Int("required", m.Required),
			slog.Int("supplied", m.Supplied),
		)
	}
	return slog.GroupValue(
		slog.String("topic", e.Topic),
		slog.Any("missing", e.Missing),
		slog.Any("insufficient", mismatches),
	)
}
```

New file `api/events/capability_require.go` — EXACT bodies, not just
signatures (previously under-specified):

```go
type QoSLevel int

const ( AtMostOnce QoSLevel = iota; AtLeastOnce; ExactlyOnce )

// String — EXACT labels, pinned down: "at-most-once"/"at-least-once"/"exactly-once".
func (l QoSLevel) String() string {
	switch l {
	case AtLeastOnce:
		return "at-least-once"
	case ExactlyOnce:
		return "exactly-once"
	default:
		return "at-most-once"
	}
}

// RequireQoS — now GENUINELY enforced: a supplied mqtt5.QoS/mqtt.QoS
// below level is rejected by CheckCapabilityCoverage (both gain a new
// Level() int method this phase, implementing LeveledCapability).
func RequireQoS(level QoSLevel) ChannelOpt {
	min := int(level)
	return CapabilityRequirement{Name: "QoS", Description: fmt.Sprintf("requires at least %s delivery", level), MinLevel: &min}
}

// RequireRetained — presence-only; Retained is a pure boolean toggle,
// "insufficient level" doesn't apply.
func RequireRetained() ChannelOpt {
	return CapabilityRequirement{Name: "Retained", Description: "requires retained-message support"}
}

// RequireHWM — GENUINELY enforced via zeromq.HWM's new Level() method.
func RequireHWM(minimum int) ChannelOpt {
	min := minimum
	return CapabilityRequirement{Name: "HWM", Description: fmt.Sprintf("requires HWM >= %d", minimum), MinLevel: &min}
}

// RequireConflate — presence-only, same reasoning as RequireRetained.
func RequireConflate() ChannelOpt {
	return CapabilityRequirement{Name: "Conflate", Description: "requires keep-latest-only semantics"}
}
```

**AsyncAPI wire output is BYTE-IDENTICAL post-rename** —
`render/asyncapi/v3.CapabilitySpec` (the "x-capabilities" vendor
extension render type) is completely unchanged; `buildCapabilityRequirements`
still emits the SAME `asyncapi.CapabilitySpec{Name, Description}` shape.
Only the Go-facing DECLARATION type's name changes — no spec/golden-file
impact anywhere.

`RequireUserProperties()` is DELIBERATELY NOT included — `UserPropertyParam`
is Tier 2 (implicit, adapter-options-scoped per this doc's own guardrail
section), not Tier 3; shipping it here would misclassify it. Promoting it
is tracked separately as Open Design Decision #5, not Phase 1 scope.

**Renames (breaking):** `CapabilitySpec`→`CapabilityRequirement`,
`MissingCapabilityError`→`CapabilityCoverageError`,
`ChannelHandle.CapabilitySpecs`→`.Requirements`,
`channelBuilder.capabilitySpecs`→`.requirements`,
`buildCapabilitySpecs`→`buildCapabilityRequirements`.
`render/asyncapi/v3.CapabilitySpec` (the wire/"x-capabilities" rendering
type) is UNCHANGED — a stable output-format concept, distinct from the
Go-facing declaration type being renamed.

**Adapter-side changes:** `adapters/mqtt.QoS`/`adapters/mqtt5.QoS` gain
`func (q QoS) Level() int { return int(q) }`; `adapters/zeromq.HWM` gains
`func (h HWM) Level() int { return int(h) }` — all three now implement
`events.LeveledCapability` alongside their existing `CapabilityName`.
`Retained`/`Conflate` (bool-backed) intentionally get NO `Level()` — they
stay presence-only, correctly. `adapters/mqtt/caller.go`,
`adapters/mqtt5/caller.go`, `adapters/zeromq/serve_subscribers.go` update
their `[]events.CapabilitySpec`-typed fields/vars to
`[]events.CapabilityRequirement` and their `FieldByName("CapabilitySpecs")`
reflection lookups to `FieldByName("Requirements")`.

**Unit test plan** (full precision — restored after an earlier condensing
edit silently dropped several rows; kept at the SAME precision as the
"Exact test-migration mapping" table below, not lossier):

| Test | Verifies |
|---|---|
| `TestQoSLevel_String` | Table test: `AtMostOnce`/`AtLeastOnce`/`ExactlyOnce` → `"at-most-once"`/`"at-least-once"`/`"exactly-once"` |
| `TestRequireQoS_ProducesCapabilityRequirement` | `Name == "QoS"`, `MinLevel != nil`, `*MinLevel == int(level)`, `Description` contains the level's label |
| `TestRequireRetained_ProducesCapabilityRequirement` | `Name == "Retained"`, `MinLevel == nil` |
| `TestRequireHWM_ProducesCapabilityRequirement` | `Name == "HWM"`, `*MinLevel == minimum` |
| `TestRequireConflate_ProducesCapabilityRequirement` | `Name == "Conflate"`, `MinLevel == nil` |
| `TestNewChannel_AcceptsRequireHelpers` | Channel construction succeeds with all 4 `RequireXxx` helpers mixed with other `ChannelOpt`s |
| `TestCheckCapabilityCoverage_RequireQoS_SufficientLevel_Passes` | Declared `AtLeastOnce`, supplied `QoSExactlyOnce` (higher) → nil error |
| `TestCheckCapabilityCoverage_RequireQoS_ExactLevel_Passes` | Declared `AtLeastOnce`, supplied `QoSAtLeastOnce` (EQUAL — the boundary case) → nil error |
| `TestCheckCapabilityCoverage_RequireQoS_InsufficientLevel_ReturnsTypedError` | Declared `ExactlyOnce`, supplied `QoSAtMostOnce` (lower) → `*CapabilityCoverageError{Insufficient: [{Name:"QoS",Required:2,Supplied:0}]}` |
| `TestCheckCapabilityCoverage_RequireHWM_InsufficientLevel_ReturnsTypedError` | Same shape for `zeromq.HWM` |
| `TestCheckCapabilityCoverage_MissingRequirement_ReturnsTypedError` | Declared, nothing supplied at all → `Missing: ["QoS"]`, `Insufficient` empty |
| `TestCheckCapabilityCoverage_RequireRetained_PresenceOnly_Passes` | `RequireRetained()` + supplied `mqtt5.Retained(false)` → nil error (presence, not value, is checked) |
| `TestCheckCapabilityCoverage_NonLeveledSuppliedAgainstLeveledRequirement` | A requirement with `MinLevel` set, supplied value doesn't implement `LeveledCapability` at all → `Insufficient` entry with `Supplied: -1` |
| `TestCapabilityCoverageError_LogValue` | `slog.KindGroup`, keys `topic`/`missing`/`insufficient`, nested group shape for each `LevelMismatch` |
| `TestCapabilityCoverageError_ErrorsAs` | `errors.As` reaches the concrete type through a wrapped error |
| `ExampleRequireQoS` | pkg.go.dev-visible usage |
| `ExampleRequireHWM` | pkg.go.dev-visible usage, including a mismatch showing the new error |

**Exact test-migration mapping** (checked against the ACTUAL existing
test files, not assumed — closes the last ambiguity before Implement):

| File | Existing test | Change |
|---|---|---|
| `api/events/capability_test.go` | `TestCapabilityNameOf` | UNCHANGED — `CapabilityNameOf` itself isn't renamed |
| `api/events/capability_test.go` | `TestCheckCapabilityCoverage_passes` | Keep name; update body to construct `CapabilityRequirement` instead of `CapabilitySpec` |
| `api/events/capability_test.go` | `TestCheckCapabilityCoverage_fails_reportsMissingNames` | Keep name; update assertions to `*CapabilityCoverageError.Missing` (was `*MissingCapabilityError.Names`) |
| `api/events/capability_test.go` | `TestCapabilitySpec_appliesChannelAndRendersSpec` | RENAME to `TestCapabilityRequirement_appliesChannelAndRendersSpec` |
| `adapters/mqtt/capability_test.go`, `adapters/mqtt5/capability_test.go` | `TestQoS_ImplementsCapability` (unchanged) | ADD new `TestQoS_ImplementsLeveledCapability` — asserts `QoSAtLeastOnce.Level() == 1`, etc. |
| `adapters/zeromq/capability_test.go` | `TestHWM_ImplementsCapability` (unchanged) | ADD new `TestHWM_ImplementsLeveledCapability` — asserts `HWM(100).Level() == 100` |

- **Implement:** the six mandatory requirements, against this finalized
  design — including migrating EVERY reference to the renamed symbols
  (not just the example) per the "Removing an old API" checklist's
  sweep step.
  **Collateral impact on `api/reqreply`'s shared adapter code is
  EXPLICITLY ACCEPTABLE for Phase 1 — not a blocker.** `adapters/mqtt5`
  and `adapters/zeromq` are single packages shared by BOTH the pub/sub
  (events) and request/reply (reqreply) sides; Phase 1 touches
  `capability.go`/`caller.go`/`serve_subscribers.go` in each. Checked by
  inspection: `adapters/mqtt5/reqreply.go`/`reqreply_transport.go`'s own
  `QoS byte` field is a PLAIN, separate field, unrelated to the sealed
  `Capability`/`CapabilityRequirement` mechanism being renamed — so
  actual breakage risk looks LOW, but is NOT provably zero until
  Implement is actually attempted (shared package-level identifiers or
  test helpers could still collide). If Phase 1's Implement step DOES
  break any `api/reqreply`-related build/test inside
  `adapters/mqtt5`/`adapters/zeromq`, that breakage is deliberately left
  AS-IS — Phase 1's own scope stays pub/sub-only; fixing it is Phase 2's
  job, not Phase 1's.
- **Examples:** update `examples/events-api/routes/routes.go` (the
  declared `events.CapabilitySpec{Name: "QoS", ...}` becomes
  `events.RequireQoS(events.AtLeastOnce)`) and
  `demo_capability_mechanism.go` (renamed symbols + a new demonstration
  of an INSUFFICIENT-level mismatch, showcasing the fix).
- **Docs:** update `docs/features/capabilities.md` (renamed types, new
  `LeveledCapability`/value-aware section), `docs/features/events.md`,
  `docs/guides/mqtt.md`/`mqtt5.md`/`zeromq.md`, and
  `.github/instructions/go-codex.instructions.md`.
  **Also closes a PRE-EXISTING gap found during Phase 1's review, not
  introduced by it:** `api/events/doc.go` has ZERO mentions of the
  capability mechanism at all today — D-0006 shipped without ever
  updating it. Since Phase 1 is a major rewrite of this exact surface,
  add a new section to `doc.go` covering the three-tier vocabulary
  (Baseline/Implicit/Explicit) at a high level and `CapabilityRequirement`/
  `RequireQoS`/`RequireRetained`/`RequireHWM`/`RequireConflate`/
  `LeveledCapability` by name, rather than carrying the gap forward again.
  **Repo-wide sweep completed this round (the "Removing an old API"
  checklist's mandatory step 4) — found 4 more files carrying the old
  names, not previously listed:**
  - `docs/design/d-0006-protocol-native-capabilities.md` — its graduated
    STATUS BLOCK claims `events.CapabilitySpec` as current. APPEND a
    short amendment blockquote noting the Phase 1 rename + pointing to
    this roadmap doc — do NOT rewrite the historical narrative below it
    (mirrors this repo's existing convention for graduated docs).
  - `docs/design/index.md` — its live D-0006 summary row uses the old
    names; UPDATE to the new ones (this is a current-state index, not
    historical narrative).
  - `.github/skills/review-go-codex/references/checklist.md` — a LIVE
    checklist row (`CapabilitySpec` + coverage); UPDATE to the new names,
    it's actively used for future review rounds.
  - `.github/skills/review-go-codex/SKILL.md` — a live reference table
    row (`api/events/capability.go`); UPDATE to the new names.
  - **Deliberately LEFT UNCHANGED:** `.github/skills/review-go-codex/references/history.md`
    (a historical, append-only round log — its Round P2 entry describes
    what was true AT THAT TIME, consistent with how this repo already
    treats `review-docs`' own history.md).
- **Learnings (recorded, real evidence from Implement — not speculation):**
  - **Zero `api/reqreply` collateral breakage occurred.** `go test
    ./adapters/mqtt5/... ./adapters/zeromq/...` (which includes each
    package's own reqreply-related test files, e.g. `reqreply_test.go`,
    `reqreply_transport_test.go`) passed FULLY on the first run after
    Step 4's renames — confirming the "low risk" assessment from Phase
    1's own Design step was correct. Reason confirmed: reqreply's own
    `QoS byte` field genuinely never touched the sealed
    `Capability`/`CapabilityRequirement` mechanism.
  - **`LeveledCapability` generalized cleanly, zero friction** — adding
    `Level() int` to `mqtt.QoS`/`mqtt5.QoS`/`zeromq.HWM` was a pure,
    additive one-line method each; no adapter-side design tension found.
    This is a positive signal for Phase 2: if reqreply ever gains a
    leveled capability candidate, the same optional-interface technique
    should transplant directly.
  - **Real-adapter types CANNOT be imported into `api/events`'s own
    internal test package** (`package events`) — `adapters/mqtt5`/
    `adapters/zeromq` both import `api/events`, so importing either back
    from an internal test file is a genuine import cycle. Tests requiring
    a `LeveledCapability`-implementing value had to use hand-written fake
    types (`fakeLeveledCapability`, `fakeNamedCapability`) instead of
    real `mqtt5.QoS`/`zeromq.HWM` values. **Carries forward to Phase 2:**
    if `api/reqreply`'s own capability tests live in `package reqreply`
    and reqreply's adapters import `api/reqreply`, the SAME fake-type
    pattern will be needed there too — plan for it in Phase 2's Design
    step rather than rediscovering it during Implement.
  - **This resolves Open Design Decision #1 with real evidence, not
    speculation:** Phase 1 confirms `CapabilityRequirement`'s shape
    (`Name string`, `Description string`, `MinLevel *int`) is simple
    enough to duplicate cheaply into a reqreply-local type with zero
    `api/events` dependency (mirroring `middleware.Disposition`'s
    placement rationale) — no evidence emerged that a SHARED type across
    packages would save meaningful duplication, since the whole struct is
    3 fields. Leading answer for Phase 2's Design step: own
    package-local type, not shared.
  - **Workflow note, not a design finding:** a `go fmt` pass was needed
    at the end of Implement (2 files had formatting drift) — no code
    change, just a reminder to run `gofmt`/`go fmt ./...` before the
    final verification pass, not only after.
  - **Post-ship follow-up, applied the same session:** a code review
    found each adapter's own `resolveCapabilities` (mqtt/mqtt5 were
    BYTE-IDENTICAL bodies; zeromq the same shape) and the repeated
    "guard + `[]any` conversion + coverage-check" / "type-assert observer
    + report" blocks were pure, protocol-agnostic boilerplate — moved to
    `api/events` as 3 new generic helpers
    (`VerifyCapabilityCoverage[C any]`, `ResolveCapabilityValue[Iface,
    V any]`, `RecordCapabilityApplied`), and each adapter's own
    `resolveCapabilities` function (+ its dedicated test) DELETED
    entirely. Zero behavior change (verified: `events-api`'s capability
    demo output is byte-identical pre/post). **Carries forward as a
    reusable pattern for Phase 2/3**: when reqreply/REST need the same
    "resolve a declared capability value from an adapter-owned slice"
    shape, reuse `ResolveCapabilityValue` directly rather than
    re-deriving a per-adapter switch statement.

### Phase 2 — `api/reqreply` (apply the mechanism, reusing all 4 existing capability values)

**Status: SHIPPED.** Design finalized, implemented, tested, documented,
and verified (`go fmt`/`go build ./...`/`go test ./...`/`just check`/all
examples all green). See the Learnings entry at the end of this
subsection for what carries forward to Phase 3.

Chosen second
because reqreply is structurally closest to events (both are
dispatch-loop-shaped; `middleware.Disposition` is existing precedent for
a shared mechanism reqreply already consumes without an `api/events`
dependency). **Phase 1's cataloged-breakage sub-step is MOOT** — Phase 1's
Learnings confirmed ZERO collateral `api/reqreply` breakage occurred, so
there is nothing to reconcile before Phase 2's own work begins.

**Key finding that changes this phase's scope for the better: this ships
REAL capabilities, not an empty mechanism.** The roadmap's earlier framing
("even with zero concrete reqreply capabilities shipped today") is
SUPERSEDED — research found both reqreply transports
(`adapters/mqtt5`/`adapters/zeromq` — confirmed `adapters/mqtt` v3 has NO
reqreply transport at all) live in the SAME PACKAGE as their events-side
sealed Capability types. `mqtt5.QoS`/`mqtt5.Retained`/`zeromq.HWM`/
`zeromq.Conflate` (all 4, already shipped, unchanged) apply to reqreply
requests/replies immediately, once reqreply's `ServeOptions`/`CallOptions`
gain a `Capabilities` field mirroring events'
`SubscribeOptions`/`PublishOptions.Capabilities` — **zero new adapter-side
capability VALUES needed.**

**Design:**

```go
// api/reqreply/capability.go — NEW, own package, ZERO api/events import
// (resolves Open Design Decision #1: the struct is small enough to
// duplicate cheaply — mirrors middleware.Disposition's D-0004 placement
// precedent). Byte-identical SHAPE to events' capability.go — see Phase
// 1's subsection above for the full rationale behind each piece; not
// re-derived here.

type CapabilityRequirement struct {
	Name        string
	Description string
	MinLevel    *int
}

func (r CapabilityRequirement) applyRoute(rb *routeBuilder) {
	rb.requirements = append(rb.requirements, r)
}

type CapabilityName interface{ CapabilityName() string }

func CapabilityNameOf(c any) string { /* identical to events' */ }

type LeveledCapability interface {
	CapabilityName
	Level() int
}

func CheckCapabilityCoverage(topic string, declared []CapabilityRequirement, supplied []any) error {
	/* identical algorithm to events.CheckCapabilityCoverage */
}

type CapabilityCoverageError struct {
	Topic        string
	Missing      []string
	Insufficient []LevelMismatch
}

type LevelMismatch struct {
	Name     string
	Required int
	Supplied int
}

func VerifyCapabilityCoverage[C any](topic string, declared []CapabilityRequirement, supplied []C) error {
	/* identical to events.VerifyCapabilityCoverage — built in from the
	   START this time, per Phase 1's post-ship Learnings, not added
	   later as a follow-up refactor */
}
```

```go
// api/reqreply/capability_require.go — NEW, OWN QoSLevel (duplicated,
// not shared with events.QoSLevel — same "cheap to duplicate, avoid the
// api/events import" reasoning).

type QoSLevel int

const ( AtMostOnce QoSLevel = iota; AtLeastOnce; ExactlyOnce )
func (l QoSLevel) String() string { /* identical labels to events' */ }

func RequireQoS(level QoSLevel) RouteOpt
func RequireRetained() RouteOpt
func RequireHWM(minimum int) RouteOpt
func RequireConflate() RouteOpt
```

**Builder/handle wiring** (exact integration points found by reading the
actual code, not assumed):
- `routeBuilder` (`api/reqreply/route.go`) gains a
  `requirements []CapabilityRequirement` field, alongside its existing
  `middlewareSpecContributions`/`topicParams`/etc. fields — structurally
  identical to how `channelBuilder` already hosts `requirements` beside
  its own middleware contributions, CONFIRMING the D-0003 cross-cutting
  non-conflict by construction, not just by analogy.
- `RouteHandle[Req,Resp]` gains a `Requirements []CapabilityRequirement`
  field, populated at BOTH construction sites: `Route.Register` (route.go
  line ~1013) and `Route.ClientHandle` (line ~1137).
- `Builder.registerRoute` (`api/reqreply/builder.go`, called from
  `Route.Register` at line ~1195) gains a
  `requirements []CapabilityRequirement` parameter — the request-side
  `asyncapi.ChannelItem` it builds ALREADY has a
  `Capabilities []asyncapi.CapabilitySpec` field (the same render-layer
  type events populates); wire it via a reqreply-owned
  `buildCapabilityRequirements` function, byte-identical shape to events'.
  The REPLY channel (receive-only) does NOT get this — mirrors events'
  "coverage is checked once at dispatch time, not per direction"
  reasoning.

**Adapter-side wiring — FULL, PRECISE plumbing (closed a real gap found
during Phase 2's review: earlier drafts of this section vaguely said
"gains `Capabilities`... reuses existing `QoS`/`Retained`" as if mirroring
an existing configurable path — tracing the ACTUAL code found the
server/reply side has NO such path at all today; every touch point below
is a confirmed, counted call site, not an estimate):**

- `adapters/mqtt5.ServeOptions`/`CallOptions` gain
  `Capabilities []mqtt5.Capability`. `adapters/zeromq.ServeOptions`/
  `CallOptions` gain `Capabilities []zeromq.Capability`.

- **`adapters/mqtt5` — Server side (`serverTransport.Serve`,
  `reqreply_transport.go`).** Confirmed: `api/reqreply/reqreply.go`'s
  escape-hatch `Serve[Req,Resp]()` (line 162) is a THIN WRAPPER that
  constructs a `serverTransport` and calls its OWN `Serve` method — there
  is only ONE real dispatch implementation to fix (unlike events, where
  the escape hatch and the Attach-based path are genuinely separate
  implementations) — fixing `serverTransport.Serve` and its 2 helper
  functions fixes BOTH entry points simultaneously, automatically.
  **Two file-level prerequisites closed this round (found by direct
  read, not assumed from package-level import presence):**
  - `adapters/mqtt5/reqreply.go` and `adapters/mqtt5/reqreply_transport.go`
    each need a NEW `"github.com/DaniDeer/go-codex/api/events"` import
    line added — confirmed NEITHER file imports it today (only
    `api/reqreply`). Trivial, zero cycle risk (other files in the same
    package already import it), but must be an explicit file change, not
    assumed "already there" from the package as a whole — Go imports are
    per-file.
  - `requirements` (used below in the coverage check) is NOT yet
    in scope — derive it via reflection, mirroring events' OWN identical
    line exactly (`caller.go:273`/`serve_subscribers.go:138`):
    ```go
    requirements, _ := elem.FieldByName("Requirements").Interface().([]reqreply.CapabilityRequirement)
    ```
    placed right after `path := elem.FieldByName("Topic").String()`,
    alongside where `coverageReqs, _, _ := effectiveSecurity(elem)` is
    already derived for the existing security check.

  Resolve QoS/Retained ONCE near Serve's existing setup code (alongside
  the NEW coverage check):
  ```go
  qos, qosSet := events.ResolveCapabilityValue[Capability, QoS](t.opts.Capabilities)
  retained, retainedSet := events.ResolveCapabilityValue[Capability, Retained](t.opts.Capabilities)
  effectiveQoS := byte(1)       // unchanged existing default
  effectiveRetained := false    // unchanged existing default
  if qosSet {
  	effectiveQoS = byte(qos)
  	events.RecordCapabilityApplied(obs, path, qos)
  }
  if retainedSet {
  	effectiveRetained = bool(retained)
  	events.RecordCapabilityApplied(obs, path, retained)
  }
  if err := reqreply.VerifyCapabilityCoverage(path, requirements, t.opts.Capabilities); err != nil {
  	return err
  }
  ```
  Then THREAD `effectiveQoS`/`effectiveRetained` through EVERY real reply
  publish site (confirmed via grep — not estimated):
  - `publishErrorReply` (`adapters/mqtt5/reqreply.go:280`) — currently
    hardcodes `QoS: 1`, sets no `Retained`. Add `qos byte, retained bool`
    parameters; update its own `Publish{}` literal; update its 2 call
    sites (`reqreply_transport.go:158,564`).
  - `tryDeadLetterReflect` (`reqreply_transport.go:172`) — currently
    hardcodes `QoS: 1`, sets no `Retained`. Add `qos byte, retained bool`
    parameters; update its own `Publish{}` literal; update ALL 9 call
    sites (lines 455/466/497/518/542/589/629/650/666/704).
  - The success-reply inline `Publish{}` literal (`reqreply_transport.go:691`)
    — set `QoS: effectiveQoS, Retained: effectiveRetained` directly
    (already in scope inside `baseHandler`, no threading needed).

- **`adapters/mqtt5` — Client side (`Call`, around
  `reqreply_transport.go:945`).** A real, precise precedent already
  exists here (`qos := t.opts.QoS`, already resolved and used on the
  outgoing request `Publish{}` literal at line ~1106) — EXTEND it rather
  than invent a new pattern:
  ```go
  qos := t.opts.QoS
  if qos == 0 {
  	qos = 1
  }
  retained := false
  if capQoS, ok := events.ResolveCapabilityValue[Capability, QoS](t.opts.Capabilities); ok {
  	qos = byte(capQoS)
  	events.RecordCapabilityApplied(obs, path, capQoS)
  }
  if capRetained, ok := events.ResolveCapabilityValue[Capability, Retained](t.opts.Capabilities); ok {
  	retained = bool(capRetained)
  	events.RecordCapabilityApplied(obs, path, capRetained)
  }
  ```
  Set `Retained: retained` on the outgoing request `Publish{}` literal
  (currently has NO `Retained` field at all — confirmed, a genuine gap
  being closed, not merely wired). **No coverage check on the client/Call
  side** — mirrors events' own "publish side never auto-checks coverage"
  precedent exactly. **`Call` is itself a thin wrapper around a private
  `t.call` helper, and `CallAsync` ALSO delegates to that same `t.call`**
  (confirmed by direct read) — fixing `t.call` once fixes both `Call` and
  `CallAsync` automatically; no separate `CallAsync` work item exists.

- **`adapters/zeromq` — FOUR real dispatch implementations need this,
  not two** (a genuine under-count in earlier drafts of this section,
  closed this round by direct read of `reqreply_transport.go`):
  `serverTransport`/`clientTransport` are NOT the only pair — ZMQ ROUTER/
  DEALER sockets get their OWN, entirely separate `routerServerTransport`/
  `dealerClientTransport` types (used by `AttachRouterServer`/
  `AttachDealerClient`), which do **not** delegate to the REQ/REP pair at
  all. The codebase's own existing comment on `routerServerTransport.Serve`
  already confirms this is the established norm for exactly this kind of
  cross-cutting concern: *"mirrors how Phase 0's capability-parity work
  was ALSO duplicated, not shared, across these same 4 transports."*
  Server AND client sides need ZERO new resolve/apply/record code either
  way — the EXISTING `applyCapabilities(sock, caps, obs, location)`
  function (`adapters/zeromq/capability.go`) already does resolve + apply
  (via `HWMSetter`/`ConflateSetter`) + observer-report internally — call
  it DIRECTLY, at all 4 sites:
  - **`serverTransport.Serve`** (REQ/REP server): right after
    `sock, ok := t.sockets[path]`, add the SAME `requirements` derivation
    line as mqtt5's above, then
    `applyCapabilities(sock, t.opts.Capabilities, obs, path)` +
    `reqreply.VerifyCapabilityCoverage(path, requirements, t.opts.Capabilities)`.
  - **`routerServerTransport.Serve`** (ROUTER server) — identical
    treatment, at its own `sock, ok := t.sockets[path]` site, right
    alongside its own existing `secReqs := effectiveSecurity(elem)` +
    `reqreply.CheckCoverage(...)` pair (same duplication pattern the
    codebase already applies there for security).
  - **`clientTransport.call`** (REQ/DEALER client, private helper behind
    `Call`/`CallAsync` — see mqtt5 note above, same delegation shape
    applies here too): add
    `applyCapabilities(sock, opts.Capabilities, obs, path)` once per
    invocation.
  - **`dealerClientTransport.call`** (DEALER client, private helper behind
    its own `Call`/`CallAsync`): same one-line call.

  HWM/Conflate re-application is idempotent (a minor, accepted
  inefficiency if called on every request, not a correctness issue —
  consistent with how per-call `CallOptions` already override defaults on
  every invocation today). This is because HWM/Conflate are socket-level
  settings (not per-message, unlike mqtt5's QoS/Retained) — applying once
  per Serve/Call invocation, not per received/sent message, mirrors
  events' own zeromq subscribe-dispatch placement exactly.

- **`adapters/mqtt5`/`adapters/zeromq` REUSE `events.ResolveCapabilityValue`/
  `events.RecordCapabilityApplied` AS-IS** (confirmed both adapter
  packages already import both `api/events` AND `api/reqreply` in the
  same package) — this is Phase 1's Learnings applied concretely, not
  just cited.

**D-0003 cross-cutting confirm (per the cross-cutting alignment note
above):** VERIFIED by construction (see `routeBuilder` wiring above), not
merely assumed to hold because it held for events.

**Import-cycle constraint carries forward exactly as flagged in Phase 1's
Learnings:** `adapters/mqtt5`/`adapters/zeromq` import `api/reqreply`, so
`api/reqreply`'s own internal test file (`package reqreply`) cannot
import them — `capability_test.go`/`capability_require_test.go` must use
hand-written fake types (mirrors `fakeLeveledCapability`/
`fakeNamedCapability` from `api/events/capability_require_test.go`
exactly).

**Structured errors:** `reqreply.CapabilityCoverageError`/`LevelMismatch`
— own types (per the placement decision), `Error()`/`LogValue()`, same
shape as events'.

**Observer integration:** reuses `stats.CapabilityObserver` (the SAME
shared extension per the cross-cutting alignment note) via
`events.RecordCapabilityApplied` calls from adapter code — no new
observer interface.

**Unit test plan** (mirrors Phase 1's matrix, reqreply-scoped): the SAME
16-ish rows as Phase 1's finalized table (construction tests for all 4
`RequireXxx` helpers, `QoSLevel.String()`, sufficient/exact/insufficient-
level coverage cases, missing case, presence-only cases, non-leveled-
supplied case, `LogValue`/`errors.As`, 2 `Example` funcs) — in
`api/reqreply/capability_require_test.go`, using fake types per the
import-cycle constraint above. PLUS new adapter-side tests closing the
plumbing gap found this round (not just a generic "wiring" test):

| Test | Verifies |
|---|---|
| `TestServeOptions_CapabilitiesWiredToCoverage` (mqtt5, zeromq) | `Capabilities`/`VerifyCapabilityCoverage` wiring end-to-end |
| `TestServe_QoSAppliedToAllReplyPaths` (mqtt5) | A supplied `mqtt5.QoSExactlyOnce` capability is honored on ALL THREE reply publish paths — success reply, error-pattern-matched reply, AND dead-letter reply — not just one of them |
| `TestServe_RetainedAppliedToReplyPublishes` (mqtt5) | A supplied `mqtt5.Retained(true)` capability sets `Retained: true` on reply publishes (previously never set anywhere) |
| `TestCall_RetainedAppliedToRequestPublish` (mqtt5) | A supplied `mqtt5.Retained(true)` capability sets `Retained: true` on the outgoing request publish (previously never set anywhere) |
| `TestServe_CapabilitiesAppliedViaExistingApplyCapabilities` (zeromq, `serverTransport`) | `ServeOptions.Capabilities` reaches the REQ/REP socket via the EXISTING `applyCapabilities` helper, unchanged behavior, just a new call site |
| `TestRouterServe_CapabilitiesAppliedViaExistingApplyCapabilities` (zeromq, `routerServerTransport`) | Same, for the SEPARATE ROUTER server implementation — not covered by the REQ/REP test above, since neither delegates to the other |
| `TestCall_CapabilitiesAppliedViaExistingApplyCapabilities` (zeromq, `clientTransport`) | Same, for the REQ/DEALER client's private `call` helper — implicitly also verifies `CallAsync` since it delegates to the same helper |
| `TestDealerCall_CapabilitiesAppliedViaExistingApplyCapabilities` (zeromq, `dealerClientTransport`) | Same, for the SEPARATE DEALER client implementation — not covered by the REQ/DEALER test above |

**Files to create/modify:**

| File | Change |
|---|---|
| `api/reqreply/capability.go` | NEW — full shape per above |
| `api/reqreply/capability_require.go` | NEW — `QoSLevel`, 4 `RequireXxx` helpers |
| `api/reqreply/capability_test.go`, `capability_require_test.go` | NEW — full test matrix, fake types |
| `api/reqreply/route.go` | `routeBuilder.requirements`, `RouteHandle.Requirements` (2 construction sites) |
| `api/reqreply/builder.go` | `registerRoute` gains `requirements` param + `buildCapabilityRequirements` |
| `adapters/mqtt5/reqreply.go` | `ServeOptions`/`CallOptions.Capabilities []mqtt5.Capability`; `publishErrorReply` gains `qos byte, retained bool` params (1 signature + 1 `Publish{}` literal) — **no new import needed here** (corrected during Implement — see Learnings: `Capabilities []Capability` is a package-local type, `byte`/`bool` params need no `api/events`; ONLY `reqreply_transport.go` ends up calling `events.*`) |
| `adapters/mqtt5/reqreply_transport.go` | NEW `api/events` import; `serverTransport.Serve`: NEW `requirements` reflection-derivation line + resolve QoS/Retained once + `reqreply.VerifyCapabilityCoverage` call; `tryDeadLetterReflect` gains `qos byte, retained bool` params (1 signature + 1 literal); update ALL 9 `tryDeadLetterReflect` call sites + 2 `publishErrorReply` call sites + 1 success-reply inline literal (12 call-site edits total, all pre-counted via grep); client-side private `call` helper (covers `Call`+`CallAsync` for free): extend the existing `qos := t.opts.QoS` resolution + add `Retained` threading (previously unset) |
| `adapters/zeromq/reqreply_transport.go` | FOUR separate dispatch implementations, each gets a `requirements` derivation line (server variants only) + one-line `applyCapabilities(sock, ...)` call: `serverTransport.Serve` (+ `reqreply.VerifyCapabilityCoverage`), `routerServerTransport.Serve` (+ same coverage check, alongside its existing `secReqs`/`CheckCoverage` pair), `clientTransport.call` (covers `Call`+`CallAsync` for free), `dealerClientTransport.call` (covers its own `Call`+`CallAsync` for free) — zero new resolve/apply/record code, reuses the existing `applyCapabilities` function as-is at all 4 sites |
| `examples/reqreply-api` | New capability demo, mirrors `demo_capability_mechanism.go` |
| `docs/features/capabilities.md` | Extended — reqreply now included, no longer pub/sub-only |
| reqreply feature/guide pages, `.github/instructions/go-codex.instructions.md` | Updated |

- **Implement:** the six mandatory requirements against this finalized
  design.
- **Examples:** new capability demo in `examples/reqreply-api`.
- **Docs:** `docs/features/capabilities.md` (no longer describes itself
  as pub/sub-only), reqreply feature/guide pages,
  `.github/instructions/go-codex.instructions.md`.
- **Learnings (recorded, real evidence from Implement — not
  speculation):**
  - **The Design step's own file-level import claim was WRONG in one
    place, corrected during Implement, not caught by review.** The
    finalized Design said `adapters/mqtt5/reqreply.go` needs a NEW
    `api/events` import "confirmed both adapter packages already import
    both api/events AND api/reqreply in the same package" — true at the
    PACKAGE level, false at the FILE level (Go imports are per-file):
    `reqreply.go`'s own changes (`Capabilities []Capability` field,
    `publishErrorReply`'s new `qos byte, retained bool` params) never
    reference `events.*` at all — only `reqreply_transport.go` (which
    calls `events.ResolveCapabilityValue`/`RecordCapabilityApplied`)
    needed the new import. `go build` caught it immediately
    ("imported and not used"), so this was a zero-cost correction, but a
    reminder: a package-level import-cycle analysis in Design does NOT
    guarantee a specific FILE needs the import — verify per-file during
    Implement, don't assume.
  - **The `requirements` reflection-derivation line was correctly
    anticipated in Design (from the gap-fix review round) and needed
    ZERO changes during Implement** — `elem.FieldByName("Requirements").
    Interface().([]reqreply.CapabilityRequirement)` worked exactly as
    designed, both in mqtt5's and zeromq's `Serve` methods, mirroring
    events' identical pattern byte-for-byte. This is a genuine positive
    signal: whenever `LeveledCapability`/`CapabilityRequirement`
    threading design work is done at the SAME precision level Phase 1's
    post-ship review demanded, Implement introduces zero surprises.
  - **The 4-transport zeromq duplication (server: REQ/REP +
    ROUTER, client: REQ + DEALER) was likewise anticipated precisely and
    needed zero changes during Implement** — each of the 4 real dispatch
    implementations got its own one-line `applyCapabilities` call (plus
    the `requirements` line on the 2 server variants), exactly as
    planned; no 5th/6th call site was found (the earlier Design-time
    finding — that `Call`/`CallAsync` on each client type both delegate
    to a single private `call` helper — held up perfectly, verified by
    the passing round-trip and per-transport unit tests).
  - **Zero new reqreply-side test-fixture friction beyond what Phase 1
    already flagged.** The anticipated import-cycle constraint (real
    `mqtt5.QoS`/`zeromq.HWM` values cannot be imported into
    `api/reqreply`'s own internal `package reqreply` test file) held
    exactly as predicted — `fakeLeveledCapability`/`fakeNamedCapability`
    were duplicated verbatim from `api/events`'s own test file with zero
    adaptation beyond the package name, confirming Phase 1's Learnings
    entry flagging this in advance was the correct call.
  - **The AsyncAPI rendering reuse (`asyncapi.ChannelItem.Capabilities`/
    `asyncapi.CapabilitySpec`, a render-layer type SHARED across events
    and reqreply, unlike the Go-facing declaration types) required zero
    new render-layer code** — only a new `buildCapabilityRequirements`
    function in `api/reqreply/builder.go` (byte-identical shape to
    events' own function of the same name) populating the ALREADY-
    EXISTING shared field. Confirms the render layer was correctly
    designed as protocol/API-agnostic from D-0006 onward.
  - **All examples continued to pass without modification to any
    OTHER example** — only `examples/reqreply-api` itself was touched
    (1 new route, 1 new `Build()` wiring line, 1 new demo file, 1 new
    `main.go` call), confirming Phase 2's additive-only claim held in
    practice, not just in the design's own "Scope decisions" table.
  - **Carries forward to Phase 3:** REST's request/reply shape is
    closest to reqreply's own (synchronous, one reply per request) of
    the three APIs — expect the SAME "requirements reflection line +
    per-dispatch-implementation applyCapabilities-equivalent call"
    pattern to transplant directly once Phase 3's own adapter (a NEW
    ZeroMQ REQ/REP-based `api/rest` adapter, not an existing one) exists;
    verify per-file imports explicitly during THAT Implement step too,
    given this phase's one real (if trivial) miss.

### Phase 3 — `api/rest` (a new, synchronous, transport-stateless adapter)

**Status: CAPABILITY MECHANISM SHIPPED.** Per an explicit scope
narrowing (this doc's implementation execution deliberately excludes
the new `adapters/zeromqrest` adapter build — see the scope-split note
below, now further refined): Design finalized, Implemented, tested,
documented, and verified (`go fmt`/`go build ./...`/`go test ./...`/
`just check`/all examples all green) for the CAPABILITY MECHANISM half
only. The new ZeroMQ REQ/REP adapter itself is NOT part of this
roadmap's own phase count or Implement scope — it remains entirely
tracked, independently, in
[`docs/roadmap/zeromq-rest-adapter.md`](zeromq-rest-adapter.md), free to
be picked up in a wholly separate future session whenever real demand
appears, with ZERO information lost (that doc's full design — wire
framing, dispatch requirements, all 6 Open Design Decisions — stands on
its own). See the Learnings entry at the end of this subsection for
what carries forward to that future session and to Phase 4.

**Scope split (resolved earlier, refined further at Implement time):**
this phase originally spanned TWO concerns designed in TWO separate
docs, per this repo's own convention — a new adapter gets its own
dedicated Explore-mode roadmap doc, written BEFORE its Implement step.
At Implement time, this was narrowed FURTHER: only the capability
mechanism's OWN Implement step happens as part of this roadmap; the
adapter's Implement step is deliberately deferred to its own,
completely independent future session — not merely designed separately
but EXECUTED separately too:

- **Adapter plumbing** (wire framing, socket lifecycle, package naming,
  `ports.IOAdapter` binding) — designed in
  [`docs/roadmap/zeromq-rest-adapter.md`](zeromq-rest-adapter.md), a
  SEPARATE, dedicated doc (mirrors `docs/roadmap/amqp-adapter.md`'s own
  precedent). Do not re-derive that doc's proposals here.
- **The capability mechanism itself** — designed HERE, since it's the
  subject of this whole roadmap doc, governed by the new
  **REST-eligible-transport guardrail**: a transport is only ever
  REST-eligible if it is BOTH (1) synchronous (request immediately
  expects its matching response, no broker-mediated delivery gap) AND
  (2) transport-stateless (no persistent, stateful broker connection the
  way MQTT's CONNECT/long-lived-session model requires). HTTP and the new
  ZeroMQ REQ/REP adapter both qualify; **MQTT (v3 or 5) is PERMANENTLY
  EXCLUDED from ever getting an `api/rest` adapter** — that shape
  (transport-stateful, broker-mediated, inherently async-flavored even
  when used for correlation) belongs to `api/reqreply`, by settled
  design, not an open question. `api/rest` and `api/reqreply` stay
  PERMANENTLY SEPARATE APIs, distinguished by communication semantics
  (synchronous/stateless vs. asynchronous/broker-mediated) — even though
  both will touch ZeroMQ, they do so via genuinely DIFFERENT socket
  patterns (REQ/REP for REST vs. reqreply's existing async-correlation
  pattern), which is expected and is NOT a reason to merge the two APIs.

- **Design:** finalize, in THIS doc:
  - **`rest.PathParam` is Tier 1 (Baseline), NOT part of this
    discussion** — a gap in this doc's own taxonomy found and closed
    this round: Path resolution is mandatory, part of route matching
    itself (same reasoning as `Topic`/`Route` for events/reqreply), so
    it was never meant to be Tier 2 alongside Header/Cookie/Query —
    stated explicitly here so a future reader doesn't wonder why it's
    absent from the Tier 2 discussion below. (Whether `zeromqrest` can
    even REALIZE a templated `Path` server-side is a separate, adapter-
    plumbing concern — see the sibling adapter doc's "Path template
    variables" section: a real, inherited limitation from
    `adapters/zeromq`'s own literal-socket-map precedent, resolved
    there as an accepted Phase 3 scope limitation, not a capability-tier
    question.)
  - `rest.HeaderParam`/`CookieParam`/`QueryParam` become GENUINELY
    runtime-checked Tier 2 (Implicit) capabilities once REST has a
    second transport family (today's "trivially always satisfied
    because REST has one transport" argument disappears — confirmed by
    tracing `adapters/nethttp`: it unconditionally calls
    `handle.ValidateHeaders`/`ValidateCookies`/`ValidateQuery` every
    request, since `*http.Request` structurally always carries all
    three).

    **The concrete mechanism (resolved this round, not left as a vague
    "runtime-checked" placeholder):** three tiny, zero-cost OPTIONAL
    marker interfaces in `api/rest`, mirroring `adapters/zeromq`'s
    `HWMSetter`/`ConflateSetter` and `LeveledCapability`'s EXACT
    optional-interface pattern — the only difference being WHAT gets
    type-asserted: Tier 3 asserts on a supplied capability VALUE, Tier 2
    asserts on the ADAPTER'S OWN TRANSPORT TYPE (there is no per-declare
    "supplied capabilities slice" for headers/cookies/query the way
    there is for QoS):
    ```go
    // api/rest — implemented by an adapter's own transport type
    // (*nethttp.transport, *zeromqrest.transport, ...) when it can
    // extract that param kind from its wire format. One no-op method
    // each — pure compile-time marker, zero runtime cost.
    type HeaderCapableTransport interface{ SupportsHeaderParams() }
    type CookieCapableTransport interface{ SupportsCookieParams() }
    type QueryCapableTransport  interface{ SupportsQueryParams() }
    ```
    `adapters/nethttp`/`adapters/chi` implement all three trivially
    (HTTP's baseline reality made explicit in code, not just doc
    comments). `adapters/zeromqrest` implements `HeaderCapableTransport`/
    `QueryCapableTransport` (both fold into ONE params frame — no
    wire-level reason to distinguish once you're not literally HTTP) but
    DELIBERATELY OMITS `CookieCapableTransport` (a correct, permanent,
    compiler-visible adapter-side omission, exactly like AMQP's
    exchange/queue capability being MQTT5-unsatisfiable — see the
    sibling adapter doc's Open Design Decision #1, now resolved to
    reference this exact marker).

    At `Serve`/`AttachServer`/`Call`/`AttachClient` setup — ONCE, not
    per-request, the SAME timing `VerifyCapabilityCoverage` already
    uses — the adapter checks: does the route declare a requirement for
    this param kind, AND does my own transport type implement the
    matching marker? A NEW typed error,
    `rest.UnsupportedParamKindError{Kind string, Adapter string}`,
    is returned IMMEDIATELY at attach time on a mismatch — not a
    confusing per-request `MissingRequiredParam` failure discovered only
    when a caller happens to hit that specific route.

    **A real, previously-missed BLOCKER, now closed:** there was no way
    for an adapter to even ASK "does this route declare any Header/
    Cookie/Query params at all" — every exported field on `rest.
    RouteHandle` was enumerated (`Descriptor`, `RequestFormats`/
    `Formats`, `SecuritySchemes`, `GlobalSecurity`, `Middlewares`,
    `Implementations`/`ClientImplementations`, `MiddlewareHandlers`/
    `ClientMiddlewareHandlers`) — NONE expose the declared `headerParams`/
    `cookieParams`/`queryParams` (all unexported). The EXISTING
    `HeaderMergeFields()`/`CookieMergeFields()`/`QueryMergeFields()`
    methods are INSUFFICIENT — they only cover the merge-field-style
    subset, silently missing a plain `rest.HeaderParam{Name: "X"}` opt
    declaration that needs no merge field at all. **Fix:** add 3 new
    exported methods, mirroring the EXISTING `PathParamNames() []string`
    method's exact shape and doc-comment convention
    ("Adapters use this to build the map required by..."):
    ```go
    func (h *RouteHandle[Req, Resp]) HeaderParamNames() []string
    func (h *RouteHandle[Req, Resp]) CookieParamNames() []string
    func (h *RouteHandle[Req, Resp]) QueryParamNames() []string
    ```
    Each iterates the FULL declared set (`h.headerParams`/
    `h.cookieParams`/`h.queryParams` directly — NOT the merge-field-only
    subset) — called via reflection
    (`rv.MethodByName("CookieParamNames").Call(nil)`) by the coverage
    check above, exactly like every adapter already reflects on
    `MergeFields()`/`PathParamNames()`-style methods elsewhere in this
    codebase — no new reflection pattern, just a missing accessor.

    **Coverage-check SCOPE must include declared `SecurityScheme`s, not
    just plain param declarations** (a real gap found and closed this
    round, not originally obvious): `route.APIKeyScheme(name, in
    string)`'s `in` field is literally `"header"`/`"query"`/`"cookie"` —
    a route declaring `route.APIKeyScheme("key", "cookie")` with NO
    separate `CookieParam` would otherwise silently bypass a check that
    only scans `RequestCookieParams`/`RequestHeaderParams`/
    `QueryParams`. The scanning helper that feeds
    `UnsupportedParamKindError`'s check must inspect BOTH plain param
    declarations AND every declared `SecurityScheme`'s `In` field.
    Bearer/OAuth2 schemes conventionally rely on the Header capability
    (`Authorization` header, pure HTTP convention) — `route.
    SecurityScheme` has NO explicit `In` field for those two types, so
    this dependency is documented as an ACCEPTED, UNENFORCED assumption,
    not structurally checkable the way APIKey's explicit `In` is.
    Credential EXTRACTION itself (reading the actual header/cookie/query
    value) stays entirely the DECLARING USER'S OWN job inside their
    `ServerImplementation.Fn` — go-codex never extracts credentials
    itself, confirmed by tracing `examples/rest-api/handlers/security.go`
    — so Security's OWN mechanism needs ZERO new go-codex-side plumbing
    beyond this coverage-check SCOPE widening.

    **`ErrorPattern` needs NO separate capability entry at all**
    (confirmed by tracing `rest.ErrorPatternResponse{Status int, Body
    []byte, Value any, Action}` and `adapters/nethttp`'s
    `writeErrorPatternResponse`): the type is purely Status+Body,
    already trivially portable to ANY REST-eligible transport (the
    sibling adapter doc's `[status, code, body]` frame already covers it
    exactly) — and when a matched pattern's `Value` ALSO merges response
    headers/cookies, `writeErrorPatternResponse` reuses the IDENTICAL
    `ValidateResponseHeaders`/`ValidateResponseCookies` path a plain
    success response uses. ErrorPattern rides entirely on the
    Header/Cookie mechanism above; it does not need its own marker
    interface or coverage check.
  - `api/rest` gains its OWN `rest.CapabilityRequirement`/
    `CheckCapabilityCoverage`/`CapabilityCoverageError`/
    `VerifyCapabilityCoverage`/`LeveledCapability`/`RequireQoS`/
    `RequireHWM` (own package-local mirror, SAME reasoning as reqreply's
    Phase 2 — cheap to duplicate a small shape, avoid a cross-API
    import). No `RequireRetained` — REST has no retained-message
    concept; this ISN'T a gap, it's correctly absent.

    **No `RequireConflate` either — resolved with protocol-level
    reasoning, not deferred as "zero Tier 3a for now."** Tracing
    `adapters/zeromq/capability.go`'s own doc comments: `HWM`
    (`ZMQ_SNDHWM`/`ZMQ_RCVHWM`) is documented as applying "depending on
    socket type" — a genuine per-socket-type ZeroMQ option that bounds
    internal queue depth regardless of pattern, so it transfers cleanly
    to a synchronous REQ/REP socket (guards against unbounded memory
    growth if a peer stalls) — KEEP `RequireHWM`/`mqtt5`-style `HWM`
    support for `zeromqrest`. `Conflate` (`ZMQ_CONFLATE`, "keep only the
    LATEST message **per topic**, discarding older, still-unread ones")
    has NO protocol-level meaning for REQ/REP: the pattern has at most
    ONE outstanding request/reply in flight at a time by definition —
    there is no backlog of unread messages for "keep only latest" to
    ever apply to. This is a genuine, permanent, protocol-driven
    omission (mirrors `CookieCapableTransport`'s omission exactly), not
    a "surveyed but not implemented" placeholder awaiting future demand
    — `zeromqrest` should never grow a `Conflate` capability, unlike
    MQTT5's Message Expiry/Shared Subscriptions (see Phase 4's new
    `mqtt5-capability-extensions.md` task below), which ARE genuinely
    just waiting for someone to ask.

    **Tier 2's `UnsupportedParamKindError` is a plain Go error, never an
    observer event** — mirrors `VerifyCapabilityCoverage`'s own
    established precedent exactly (confirmed by tracing every one of
    its 5 existing call sites across `adapters/mqtt5`/`adapters/zeromq`:
    the error is always `return`ed directly, never wrapped in a
    `stats.Report*`/`RecordCapabilityApplied` call). Both are
    attach-time/Serve-setup-time structural failures, discovered BEFORE
    any request stream exists to report an observer event into — the
    caller of `AttachServer`/`Serve` receives the error synchronously.
    Only TWO things ever reach an Observer in this whole mechanism:
    successful Tier 3a capability APPLICATION
    (`RecordCapabilityApplied`, once, at setup) and per-REQUEST
    rejections happening inside the live dispatch loop
    (`RecordSecurityRejection` being the existing example) — coverage/
    structural mismatches of any tier are never one of those two.
  - Whether REST's OpenAPI-only spec rendering stays OpenAPI-only for
    the new transport, or needs a parallel AsyncAPI-style rendering path
    for capability vendor extensions — OpenAPI 3.1 has no native
    "x-capabilities"-equivalent convention the way AsyncAPI's own
    vendor-extension mechanism does; resolve via an
    `x-codex-capabilities` OpenAPI vendor extension (mirrors AsyncAPI's
    `x-capabilities` naming, adapted to OpenAPI's own extension
    convention of an `x-` prefix on ANY object, not just channels), added
    to the route's own `Operation` object.
  - **REST's relationship to declarative middleware is NOT one uniform
    thing — traced precisely this round, replacing an earlier vague
    "not assumed to hold automatically" hedge with two DIFFERENT,
    verified findings:**
    - **Tier 3a (`RequireQoS`/`RequireHWM` → the new `routeBuilder.
      requirements` field) is INDEPENDENT from declarative middleware —
      same pattern Phases 1-2 already confirmed.** `requirements` is a
      field no middleware contribution (`middlewareSpecContributions`,
      `middlewares`) ever touches — two genuinely separate
      declarations sharing nothing at the Go-type level, exactly as
      D-0006 §3 resolved for events/reqreply.
    - **Tier 2 (`HeaderParamNames`/`CookieParamNames`/`QueryParamNames`)
      is GENUINELY ENTANGLED with declarative middleware — NOT
      independent, and this is CORRECT, verified by tracing
      `api/rest/middleware.go`'s `applyParamDeclarations`, not assumed:**
      it merges BOTH legacy `middleware.Middleware.RequestHeaderParams`/
      `RequestCookieParams`/`RequestQueryParams` AND D-0003's codec-
      backed `Middleware[In,Out]` contributions (attached via
      `Transform`/`ClientTransform`) DIRECTLY into `rb.headerParams`/
      `cookieParams`/`queryParams` — via `toHeaderParam(s).applyRoute(rb)`
      and its cookie/query siblings — appending to the EXACT SAME list
      a plain `rest.HeaderParam{}` route opt would. This merge
      (`Route.registerHandle` → `applyMiddlewareDeclarations` →
      `applyParamDeclarations`) runs BEFORE `RouteHandle` is
      constructed, so by the time the 3 new accessor methods read
      `h.headerParams`/etc., they ALREADY reflect middleware-declared
      requirements too. **Net effect, confirmed not assumed:** a route
      declaring a header requirement ONLY via `Transform`/`.Use(mw)`
      (never a plain `HeaderParam{}` opt) is STILL correctly covered by
      the Tier 2 coverage-check — nothing extra needs to be built for
      this, but it must be stated as a verified fact (mirroring Phase
      2's "VERIFIED by construction" bar), not left as an unstated
      side effect a future reader might assume was accidental. The SAME
      reasoning extends to the `SecurityScheme.In` coverage-scope
      finding from earlier this round: a scheme attached via
      `.Use(SomeMiddleware.Security)` lands in the SAME `SecuritySchemes`
      map (populated by `applySecurityDeclarations`) the coverage-check
      already reads — also automatically correct, also previously
      unstated.
    - **Legacy `middleware.Middleware` vs. codec-backed
      `Middleware[In,Out]`'s own future is tracked SEPARATELY** — see
      the new `docs/roadmap/middleware-consolidation.md`, spun out this
      round after finding `HandleMW`/`ClientMW` are hard-coded to the
      LEGACY concrete type (not the shared `RouteMiddleware` interface),
      meaning Security enforcement and codec-backed param merging are
      two genuinely different mechanisms today, not a redundant
      duplication — Phase 3 does NOT block on that doc's outcome either
      way, since both mechanisms already coexist correctly as traced
      above.
    - `stats.CapabilityObserver` is wired into the new ZeroMQ REST
      adapter's dispatch path (designed in the sibling adapter doc)
      using the SAME type-assertion-guard pattern events/reqreply use —
      this phase is where `CapabilityObserver` reaches REST for the
      first time, since Phase 3 introduces REST's capability mechanism
      from scratch.

**Unit test plan** (mirrors Phase 1/2's matrix format): construction
tests for `RequireQoS`/`RequireHWM` (no `RequireRetained`/
`RequireConflate` — correctly absent), sufficient/exact/insufficient-
level coverage cases, missing case, presence-only cases,
non-leveled-supplied case, `LogValue`/`errors.As`, 2 `Example` funcs —
in `api/rest/capability_require_test.go`, using fake types per the SAME
import-cycle constraint Phase 1/2 already hit (`adapters/nethttp`
imports `api/rest`, so `api/rest`'s own `package rest` test file cannot
import it). PLUS 3 NEW tests closing this round's accessor gap:
`TestHeaderParamNames_ReturnsPlainAndMergeFieldDeclarations`/
`TestCookieParamNames_...`/`TestQueryParamNames_...` (each verifying
BOTH a plain-opt AND a merge-field-style declaration are returned, not
just one) — plus `TestCoverageScan_IncludesAPIKeySchemeIn` (the
APIKey-cookie gap case found earlier this round: a route declaring only
`route.APIKeyScheme("key", "cookie")`, no separate `CookieParam`, still
triggers the Cookie requirement).

**Files to create/modify:**

| File | Change |
|---|---|
| `api/rest/capability.go` | NEW — `CapabilityRequirement`, `CheckCapabilityCoverage`, `CapabilityCoverageError`/`LevelMismatch`, `VerifyCapabilityCoverage`, `LeveledCapability`, `HeaderCapableTransport`/`CookieCapableTransport`/`QueryCapableTransport`, `UnsupportedParamKindError` |
| `api/rest/capability_require.go` | NEW — `RequireQoS`/`RequireHWM` sugar (no `RequireRetained`/`RequireConflate`) |
| `api/rest/builder.go` | `routeBuilder.requirements` field; `RouteHandle.Requirements` field (populated at both Register-equivalent construction sites); NEW `HeaderParamNames`/`CookieParamNames`/`QueryParamNames` methods (mirrors `PathParamNames()` exactly, full declared set not just merge-field subset); a coverage-scan helper inspecting BOTH the 3 new accessors AND every declared `SecurityScheme`'s `In` field |
| `api/rest/capability_test.go`, `capability_require_test.go` | NEW — full test matrix per above, fake types |
| `render/openapi/openapi.go` | NEW `x-codex-capabilities` vendor extension rendering on the route's `Operation` object |
| `route/route.go` | NEW `route.CapabilitySpec{Name, Description}` render-layer mirror + `Route.Capabilities []CapabilitySpec` field (added during Implement — the Design step's own sketch didn't anticipate `route.Route`, the shared HTTP-shaped descriptor type, needing this itself) |
| `adapters/nethttp/capability.go`, `adapters/chi/capability.go` | NEW — trivial `transportCapabilities` marker implementing all 3 Tier 2 interfaces (added during Implement, per the narrowed scope below — proves the mechanism over a REAL adapter without needing `adapters/zeromqrest` to exist) |
| `adapters/zeromqrest/*` | **DEFERRED, NOT part of this Implement step** — an independent future session, per the sibling adapter doc's own design (unchanged, untouched) |

- **Implement (NARROWED SCOPE — capability mechanism only, executed):**
  the six mandatory requirements for the `api/rest`/`route`/
  `render/openapi` changes above, PLUS wiring `adapters/nethttp`/
  `adapters/chi`'s 3 trivial marker implementations into their EXISTING
  `Serve`/`AttachServer` dispatch (verified: full pre-existing test
  suites pass UNCHANGED). The `adapters/zeromqrest` adapter's own
  Implement step (full `add-a-new-adapter` skill checklist) does NOT
  happen here — deferred whole, to its own future session.
- **Examples:** `examples/rest-api` gained
  `demo_capability_mechanism.go` — demonstrates Tier 2 (HeaderParam/
  QueryParam, always passes) and Tier 3a (RequireQoS, correctly
  rejected — no adapter supplies a QoS value yet) against the REAL
  nethttp adapter.
- **Docs:** updated `docs/features/rest-api.md`, `docs/features/capabilities.md`
  (new "REST's capability mechanism" section, "Why not REST/Security?"
  retitled to "Why not Security?"), `.github/instructions/go-codex.instructions.md`.
- **Learnings (recorded, real evidence from Implement — not
  speculation):**
  - **A genuine client/server ASYMMETRY was found and correctly worked
    around, not silently papered over.** `Route.ClientHandle()` does
    NOT run `applyParamDeclarations`'s middleware-merge step (only
    `applyMiddlewareSecurityForClient` — Security only) — confirmed by
    a FAILING test (`TestHeaderParamNames_IncludesMiddlewareDeclaredHeader`
    initially returned an empty slice via `ClientHandle()`). Root cause:
    `ClientHandle()` is DELIBERATELY infallible (its own doc comment:
    "conflict detection and drift-closing coverage checking do NOT run
    here"), and the merge step can fail (`checkParamConflicts`), so it's
    correctly excluded from the infallible path. Fixed by testing
    through `RegisterHandle` (server-side) instead — the CORRECT handle
    source for `HeaderParamNames()`'s actual consumers (an adapter's
    `Serve`/`AttachServer`, always server-side). Carries forward: any
    FUTURE `adapters/zeromqrest` Implement session must consume
    `HeaderParamNames()`/etc. from the SAME server-side construction
    path, not assume `ClientHandle()`-sourced handles carry the full
    merged set.
  - **`route.Route` (the shared, transport-agnostic descriptor type)
    needed a NEW field the Design step's file sketch never
    anticipated** — `Capabilities []route.CapabilitySpec` — since
    OpenAPI rendering operates on `route.Route`, not `rest.RouteHandle`
    directly. A small, zero-cost addition (mirrors `SecurityRequirement`'s
    existing role on the same struct), but a reminder that a Design
    step's own "Files to create" sketch can still miss a render-layer
    plumbing detail until Implement traces the ACTUAL rendering call
    path.
  - **Wiring nethttp/chi's markers surfaced a genuinely useful NEW
    shared helper not in the original Design sketch**:
    `rest.CheckParamKindCoverage(adapter, requiredKinds, transport)` —
    added during Implement (mirrors `VerifyCapabilityCoverage`'s own
    "thin-adapter convenience wrapper, don't hand-roll per-adapter"
    philosophy) once it became clear EVERY adapter wiring this in would
    otherwise duplicate the identical 3-kind type-assertion sequence.
  - **Zero behavior change to nethttp/chi, PROVEN not assumed**: both
    packages' FULL pre-existing test suites (8.2s/0.4s respectively)
    pass byte-for-byte unmodified after wiring both new coverage checks
    into their dispatch — confirming the "declare first, adapter
    satisfies second" mechanism genuinely coexists with zero regression
    risk for a real, heavily-tested adapter, not just in theory.
  - **Carries forward to `adapters/zeromqrest`'s own FUTURE Implement
    session** (whenever it happens, entirely separately): that session
    inherits a FULLY SHIPPED, tested `api/rest` capability mechanism —
    it need only implement `HeaderCapableTransport`/
    `QueryCapableTransport` (deliberately NOT `CookieCapableTransport`)
    and a real `HWM` `Capability` value (deliberately NOT `Conflate`),
    then wire `rest.CheckParamKindCoverage`/`VerifyCapabilityCoverage`
    into its own dispatch exactly like nethttp/chi just did — zero
    `api/rest`-side changes anticipated.

### Phase 4 — `api/events`: promote capability APPLICATION from adapter-owned loops to an API-layer-owned dispatcher

**Status: SHIPPED (mqtt5 + zeromq events-side), INCLUDING Phase 4b's
zero-backdoor guardrail fixes** (see Phase 4b subsection below).
`adapters/mqtt` (v3)'s full migration to this same shape (not merely its
`PublishAttributes` follow-on, already done in Phase 4b) is deferred to
Phase 5 (see Learnings below).

**Motivation — a deeper gap found reviewing Phases 1-3's own shipped
mechanism against the corrected mental model:** the goal is not merely
"an adapter implements SOME interface" — it's that the **`api/*` layer
itself calls through that interface**, driven by what the user declared
on the route/channel, with the adapter contributing ONLY the interface
implementation + its own protocol-specific target object. Re-auditing
the ALREADY-SHIPPED mechanism against this bar found it still falls
short in two places:

- **`adapters/zeromq.HWMSetter`/`ConflateSetter` ARE real interfaces**
  (`SetHWM(n int) error` genuinely configures the socket) — **but the
  LOOP that resolves a supplied capability, type-asserts the Setter,
  and calls it is itself defined IN THE ADAPTER**
  (`adapters/zeromq/capability.go`'s `applyCapabilities`), not in
  `api/events`. The API layer defines `HWMSetter` but never CALLS it —
  the adapter still does that itself.
- **`adapters/mqtt5.QoS`/`Retained` have NO interface at all** — the
  supplied value is extracted via `events.ResolveCapabilityValue` and
  used DIRECTLY as a raw byte/bool in a `Publish{}` literal, inline, at
  every publish call site (`adapter.go`/`caller.go`, several sites
  each). Not pluggable, not an interface the adapter "implements
  against" in any meaningful sense.

**Design — the `Apply(Target) (applied bool, err error)` shape:**

Give every capability value a method against its OWN adapter-specific
target type, and move the CALLING LOOP into `api/events` as a single,
fully generic function — Go's type inference resolves the target type
`T` per call site, so ONE function serves every adapter family with
zero per-family special-casing:

```go
// api/events — the API LAYER now owns this generic dispatch loop.
// Replaces EVERY adapter's own hand-rolled resolve+assert+call+record
// sequence (adapters/zeromq's own (deleted) applyCapabities function;
// adapters/mqtt5's inline manual field-assignment blocks).
func ApplyCapabilities[C interface{ Apply(T) (bool, error) }, T any](
    caps []C, target T, obs stats.Observer, location string,
) {
    for _, c := range caps {
        applied, err := c.Apply(target)
        if applied && err == nil {
            if nc, ok := any(c).(CapabilityName); ok {
                RecordCapabilityApplied(obs, location, nc)
            }
        }
    }
}
```

```go
// adapters/zeromq/capability.go — Capability itself now REQUIRES
// Apply (merged into the sealed marker interface, not a separate
// optional Setter split) — genuinely "an API the adapter implements
// against."
type Capability interface {
    isZeroMQCapability()
    Apply(sock FramedSocket) (applied bool, err error)
}

func (h HWM) Apply(sock FramedSocket) (bool, error) {
    setter, ok := sock.(HWMSetter)
    if !ok {
        return false, nil // documented no-op, UNCHANGED semantics
    }
    return true, setter.SetHWM(int(h))
}
```

**`adapters/mqtt5` needed one more piece than zeromq: `WireAttributes`.**
`mqtt5.QoS`/`Retained` apply to TWO different native paho target types —
`*pahomqtt5.Publish` (publish path) AND `pahomqtt5.SubscribeOptions`
(subscribe path) — and Go doesn't support method overloading by
parameter type, so ONE `Apply` method can't target both directly. The
shipped design introduces an adapter-owned intermediate struct BOTH
call sites populate/consume:

```go
// adapters/mqtt5/capability.go — shipped shape.
type WireAttributes struct {
    QoS      byte
    Retained bool
}

type Capability interface {
    isMQTT5Capability()
    Apply(wire *WireAttributes) (bool, error)
}

func (q QoS) Apply(wire *WireAttributes) (bool, error) {
    wire.QoS = byte(q)
    return true, nil
}
func (r Retained) Apply(wire *WireAttributes) (bool, error) {
    wire.Retained = bool(r)
    return true, nil
}
```

Adapter code SHRINKS to one call site each, replacing every existing
resolve+assign block:

```go
// adapters/zeromq/serve_subscribers.go — was a local applyCapabilities(...) call
events.ApplyCapabilities(r.capabilities, c.sock, obs, r.topic)

// adapters/mqtt5/adapter.go / caller.go — was manual "if qosSet {...}" blocks
var wire WireAttributes
events.ApplyCapabilities(opts.Capabilities, &wire, obs, path)
// wire.QoS / wire.Retained then flow into the native Publish{}/SubscribeOptions{} literal.
```

**Semantics preserved exactly, verified not assumed:** `(false, nil)`
means "target doesn't support this capability" — the SAME documented
no-op-not-an-error convention every capability already had; a genuine
`Apply` error is silently swallowed (no `RecordCapabilityApplied`),
matching today's `if err == nil { RecordCapabilityApplied(...) }`
pattern exactly — no behavior change, only WHERE the loop lives.

**Radical breaking change, per explicit user direction ("we are doing
breaking changes; every interaction between the api layer and the
adapter layer is via the interface approach"):** the former
`qos byte, retained bool` positional call-time parameters and the
`SubscribeOptions.QoS` plain field were REMOVED ENTIRELY from
`adapters/mqtt5` — `Capabilities` is now the SOLE mechanism, closing the
old precedence ambiguity (`if qos == 0 && qosSet { qos = capQoS }`)
outright rather than reconciling it. `adapters/zeromq` never had this
dual-path (`Capabilities` was always its only mechanism), so its own
fix was purely the calling-loop relocation, with ZERO call-site changes
needed anywhere (including its `reqreply_transport.go` sites) — `zeromq`
package's `applyCapabilities` kept its EXACT prior signature
(`FramedSocket, []Capability, stats.Observer, string`) but its body is
now a 1-line delegation to `events.ApplyCapabilities`.

- **Implemented:** the six mandatory requirements, for `adapters/mqtt5`
  and `adapters/zeromq` (events-side call sites: `adapter.go`,
  `caller.go`, `handletransport.go`, `binding.go`). `api/events`'s new
  `ApplyCapabilities[C,T]` generic function. `adapters/zeromq`'s
  `applyCapabilities` kept as a thin same-signature wrapper (not
  deleted — see Learnings). `adapters/mqtt5`'s every manual QoS/Retained
  inline assignment (subscribe-dispatch AND publish-dispatch call
  sites) replaced with `events.ApplyCapabilities` calls against
  `WireAttributes`.
- **Deferred to Phase 5 (a new addition, not originally planned):**
  mirroring this SAME treatment in `adapters/mqtt` (v3) — same file
  names, same pattern. Kept out of this round to preserve
  phase-by-phase discipline; `adapters/mqtt` (v3) is UNCHANGED and
  still fully on its pre-Phase-4 legacy positional-param path, which
  still compiles and passes today (this package was never touched this
  round).
- **Examples:** `examples/events-api`'s mqtt5-backed demos (
  `demo_capability_mechanism.go`, `demo_connect_level_security.go`,
  `demo_error_pattern.go`, `demo_property_merge_direct_attachment.go`,
  `demo_security_subscribemw.go`, `demo_user_property_middleware.go`)
  updated to construct `Capabilities: []mqtt5.Capability{...}` instead
  of the removed positional qos/retained constructor args — this is a
  REAL breaking change surfaced in example code, not merely internal;
  every declaring user of `NewPublishTransport`/`NewSubscribeTransport`
  must migrate the same way.
- **Docs:** update `docs/features/capabilities.md`'s mechanism
  description and `.github/instructions/go-codex.instructions.md`.
- **Learnings:** recorded here before Phase 5 begins.

**Learnings:**

1. **The `WireAttributes` intermediate wasn't optional design polish —
   it's the ONLY way a single `Apply` method can serve two native
   target types.** Any future capability that applies to multiple wire
   objects (e.g. a hypothetical MQTT5 "message expiry" affecting both
   `Publish` and a retained-message read path) should reach for this
   same adapter-owned intermediate-struct pattern rather than trying to
   force method overloading Go doesn't support.
2. **`adapters/zeromq`'s fix was cheaper than `adapters/mqtt5`'s by
   construction, not luck.** zeromq's `HWM`/`Conflate` never had a
   legacy positional dual-path — `Capabilities` was ALWAYS the sole
   mechanism there. Because `applyCapabilities` kept its exact prior
   signature, EVERY existing call site (events- AND reqreply-side)
   needed zero changes — this is a real, generalizable lesson: keeping
   an unchanged function signature while swapping its INTERNAL
   implementation to delegate to a new API-layer mechanism is strictly
   preferable to a call-site-breaking rewrite, whenever the old
   signature was already capability-only.
3. **The scope of "radical breaking changes" turned out larger than
   the initial design sketch implied** — it wasn't just
   `adapter.go`/`caller.go`'s dispatch blocks, but also
   `handletransport.go`'s public `NewPublishTransport`/
   `NewSubscribeTransport` constructor signatures AND `binding.go`'s
   `ports.SinkAdapter`-facing `MQTT5DrainPublishOptions.QoS`/`Retained`
   fields. **Update, Phase 4b:** the initial pass folded these into a
   per-item `Capabilities` slice internally while KEEPING the struct's
   raw fields, reasoning `MQTT5DrainPublishOptions` and
   `events.PublishAttributes`/`ResolvePublishAttributes` were "a
   SEPARATE, pre-existing declarative mechanism, out of scope." A
   follow-up guardrail audit (see Phase 4b below) found this reasoning
   was WRONG — that separateness is exactly what makes it a backdoor,
   not a reason to leave it alone. Both were removed entirely in Phase
   4b. Enumerating a legacy mechanism's FULL call-site list via grep
   BEFORE starting the rewrite (as this session did) is what kept this
   discoverable rather than a series of surprise compile failures.
4. **Real compile-time proof the design works:** `adapters/mqtt5`'s
   full pre-existing test suite (52+ files) needed mechanical
   call-site updates (removing positional args, folding
   qos/retained into `Capabilities`) but ZERO test assertions changed
   meaning — confirming the redesign is a pure mechanism relocation,
   not a behavior change, exactly as designed.

#### Phase 4b — closing the guardrail gaps found in a post-hoc audit

**Status: SHIPPED.** After Phase 4's initial implementation, the user
asked for an explicit review against the "no backdoor between the api
layer and the adapters" guardrail (see "Architectural guardrail" above)
— this subsection records that audit's findings and the fixes shipped
in response, all within the SAME `adapters/mqtt5`/`api/events` scope
Phase 4 already owned (no new packages touched).

**Findings:**

1. **`mqtt5.SubscribeAdapter`'s raw `qos byte` parameter** — wrote
   directly into `pahomqtt5.Subscribe{QoS: a.qos}`, never touching
   `Capability`/`Apply` at all. **Fixed:** removed the parameter; added
   `Capabilities []Capability` to `SubscribeAdapterOptions`; wired
   through `events.ApplyCapabilities` into a `WireAttributes`, exactly
   mirroring the core `subscribeWithHandle` path.
2. **`MQTT5DrainPublishOptions.QoS byte`/`.Retained bool`** — a raw-value
   declaration surface, even though the wire application already routed
   through `Apply`. **Fixed:** removed both fields; added
   `Capabilities []Capability`; the adapter now passes it straight
   through with no reconstruction step.
3. **`events.MQTTQoS`/`events.PublishAttributes`/
   `Publisher.WithAttributes`** (core `api/events`) — a third,
   transport-agnostic, route/channel-level declaration mechanism
   entirely bypassing `Capability`, previously documented as "additive,
   still-fully-supported legacy." The clearest backdoor found.
   **Fixed:** deleted `api/events/mqtt_qos.go` entirely; removed
   `Publisher.WithAttributes`, `ChannelHandle.publishAttrsFn`/
   `ResolvePublishAttributes`. Since `events.Subscribe.QoS`'s type WAS
   `MQTTQoS`, the SAME symmetric backdoor on the subscribe side was
   found and closed too: removed `Subscribe.QoS`/
   `ChannelHandle.SubscribeQoS` entirely — `Capabilities` is now the
   ONLY way to express subscribe-side QoS, matching the publish side.
   This is a BREAKING change reaching into core `api/events`, affecting
   BOTH `adapters/mqtt` (v3) and `adapters/mqtt5`.
4. **`adapters/mqtt` (v3) — narrow follow-on, not a full migration.**
   Since `PublishAttributes` was deleted from `api/events`, `adapters/mqtt`'s
   own `PublishAdapter`/`ServeSubscribers` had to drop their usage of it
   too (their `ResolvePublishAttributes` fallback block and the
   `SubscribeQoS`-via-reflection fallback) — but their OWN raw
   `qos byte, retained bool` positional params and `SubscribeOptions.QoS`
   field were DELIBERATELY LEFT AS-IS, since a full migration to the
   `Capability`/`Apply`-only shape `adapters/mqtt5` now has is Phase 5's
   job, not Phase 4b's. Phase 5's own scope statement is updated
   accordingly (see below).
5. **`adapters/zeromq`'s `SubscribeAdapter`/`PublishAdapter` ports
   bindings** — not a backdoor (nothing bypassed `Capability`), but a
   coverage gap: neither exposed a `Capabilities` field at all, making
   `HWM`/`Conflate` unreachable through these two adapters. **Fixed:**
   added `Capabilities []Capability` to both
   `SubscribeAdapterOptions`/`DrainPublishOptions`, purely additive.
6. **Stale doc comments** in `adapters/mqtt5/adapter.go`'s
   `SubscribeOptions.Capabilities`/`PublishOptions.Capabilities` still
   described the OLD dual-path/fallback precedence Phase 4's actual code
   change had already removed. **Fixed:** corrected to describe the
   shipped sole-mechanism behavior.

**Learnings:**

1. **"Purely additive, nothing deprecated" is the wrong default posture
   for a capability mechanism whose whole point is being the SOLE path.**
   Phase 4's own initial pass treated `MQTT5DrainPublishOptions`/
   `PublishAttributes` as "a separate, pre-existing mechanism, out of
   scope" — reasonable-sounding, but wrong: for a "no backdoor" design,
   separateness IS the violation, not an excuse to leave it alone. This
   is now stated as this roadmap's own explicit guardrail (see above) so
   future phases don't repeat the mistake.
2. **A "kept, not deprecated" comment on a parallel mechanism is a code
   smell worth treating as a standing audit item, not reassurance.**
   `mqtt_qos.go`'s own doc comment said exactly this about
   `PublishAttributes` — its presence should have been a prompt to ask
   "does this bypass the interface?" at Phase 4 design time, not
   discovered only in a follow-up audit.
3. **Deleting a shared type (`MQTTQoS`) forces auditing EVERY field of
   that type across the codebase, not just the one call site that
   prompted the deletion.** `Subscribe.QoS`'s type was `MQTTQoS` —
   deleting `PublishAttributes` (the originally-named target) could not
   be done without ALSO resolving `Subscribe.QoS`, which turned out to
   be the exact same backdoor shape on the subscribe side. Grepping for
   every USE of a type being deleted (not just its originally-flagged
   use) is what surfaced this before it caused a silent compile break.
4. **A backdoor's "internal reconstruction" doesn't excuse its public
   shape.** `MQTT5DrainPublishOptions.QoS`/`.Retained` already flowed
   into `Capability.Apply` internally before Phase 4b — the WIRE
   application was already correct. It was still a backdoor, because the
   guardrail is about where the USER interacts with the mechanism, not
   only where the adapter ends up calling it.

#### Phase 4c — closing the `Client.Publish`/`Client.Subscribe` Capabilities gap

**Status: SHIPPED.** Found while reviewing `examples/events-api/
demo_capability_mechanism.go` against the "zero backdoor" guardrail: the
demo builds a real `*events.Client`, attaches `mqtt5.Attach` (used for
the subscribe side), then for PUBLISH drops into
`mqtt5.NewPublishTransport`+`events.PublishHandle` directly — bypassing
the attached Client entirely — to express `Capabilities: []Capability{
Retained(true)}`.

**Root cause, confirmed via code trace, not assumed:** every adapter's
`events.Transport` implementation (`adapters/mqtt5`/`mqtt`/`zeromq`'s
`transport.go`) is explicitly documented **"v1 scope"**: `Client.Publish`
hardcodes `QoS: defaultQoS` (always 0) and has NO path to read
`Capabilities` at all — there is no `Publisher[T].WithOptions` method to
even DECLARE Capabilities on a Publisher (unlike `Subscriber[T].WithOptions`,
which already exists and IS read by `ServeSubscribers`, just not by the
single-channel `Client.Subscribe`). Each adapter's own doc comment says
the same thing near-verbatim: *"a caller needing [QoS/security/format
overrides] should use [subscribe]/[Publish] directly."* This is a
pre-existing scope decision from `docs/design/d-0002-pubsub-workflow-simplification.md`'s
Decision 5 — not something Phase 4/4b introduced — but it is EXACTLY
the shape of backdoor the Phase 4b guardrail forbids: a user needing
Capabilities is systematically forced off the api-layer-owned
`Client.Publish`/`Subscribe` surface.

**Cross-API comparison, confirming this is events-specific, not
universal:**
- `api/rest`'s `adapters/nethttp/clienttransport.go` states, verbatim:
  *"Call and Consume are FULL-FEATURED... there is NO remaining 'v1
  scope' asterisk."* Achieved by resolving header/cookie/query/security
  FROM THE DECLARED ROUTE/MIDDLEWARE (the reflection shim reads them off
  the `RouteHandle`), not via a bigger per-call options struct.
- `api/reqreply`'s `mqtt5.AttachClient`/`AttachServer` accept an
  ATTACH-TIME `CallOptions`/`ServeOptions` value (including
  `Capabilities`) applied UNIFORMLY to every route through that
  Client/Server — coarser-grained than REST, but not a backdoor.
- `api/events`'s `mqtt5.Attach` takes NO opts param at all — zero ways
  to express Capabilities without touching `adapters/mqtt5` directly.
  The actual gap this phase closes.

**Design — Option A, per-channel declared (matches REST's actual
mechanism: declared where the requirement lives, and reuses
`Subscriber.WithOptions`'s ALREADY-PROVEN shape verbatim for the publish
side, rather than an attach-time-global setting):**

```go
// api/events/builder.go — NEW, mirrors Subscriber[T].WithOptions exactly.
func (p Publisher[T]) WithOptions(opts any) Publisher[T] {
    p.handlerOpts = opts
    return p
}
```

`Publisher.Handle` copies `handlerOpts` onto the built
`ChannelHandle.HandlerOpts` field (today only populated on the subscribe
side) — a `mqtt5.PublishOptions[T]{Capabilities: []mqtt5.Capability{...}}`
value declared this way is now reachable by `Client.Publish`'s
reflection shim via `elem.FieldByName("HandlerOpts")`, exactly the same
technique `ServeSubscribers`/`Client.Subscribe`'s OWN HandlerOpts
resolution already uses elsewhere in this codebase.

Each adapter's `transport.go` `Publish`/`Subscribe` methods then:
1. Recover `HandlerOpts` via reflection (already have `elem`/`handleVal`
   in scope).
2. Type-assert to the adapter's own `PublishOptions[T]`/`SubscribeOptions`
   shape (or extract just `.Capabilities` — Design detail to finalize:
   whether the WHOLE Options struct is read, matching per-channel
   declared `Capabilities`, `UserPropertyParams`, etc., or ONLY
   `Capabilities` for this narrower phase — leaning toward reading the
   Capabilities field ONLY this round, to keep Phase 4c scoped; anything
   else declared via `WithOptions` for OTHER purposes stays honored only
   by `ServeSubscribers`/the direct escape-hatch calls, unchanged).
3. Build a `WireAttributes` (mqtt5) and call `events.ApplyCapabilities`
   — IDENTICAL to the core `publish`/`subscribeWithHandle` path — before
   the native publish/subscribe call, replacing the hardcoded
   `defaultQoS`.
4. `adapters/zeromq`: confirm `HWM`/`Conflate` are meaningful on BOTH
   Publish and Subscribe sides or subscribe-only before wiring (avoid
   assuming symmetry with mqtt5 uncritically).

**Fix `demo_capability_mechanism.go`:** replace the manual
`NewPublishTransport`+`events.PublishHandle` call with
`evClient.Publish(ctx, routes.CapabilityPub, msg)`, with
`routes.CapabilityPub` declaring `Capabilities` via the new
`WithOptions`.

**Explicitly out of scope for Phase 4c** (see Phase 4d immediately
below): format overrides, security/credential ClientMW enforcement, and
general-purpose middleware wrapping remain unaddressed by
`Client.Publish`/`Subscribe` after this phase — Capabilities is the ONLY
gap closed here.

**Implemented, exactly as designed:**
- `Publisher[T].WithOptions(opts any) Publisher[T]` added to
  `api/events/builder.go`, mirroring `Subscriber[T].WithOptions` field-
  for-field; `Publisher.Handle` now threads `p.opts` into
  `buildChannelHandle` instead of a hardcoded `nil` — `ChannelHandle.HandlerOpts`
  is now populated on BOTH sides, closing the asymmetry.
- `adapters/mqtt5/transport.go`: both `Publish` and `Subscribe` resolve
  `HandlerOpts.Capabilities` via a new `resolveHandlerOptsCapabilities`
  reflection helper, build a `WireAttributes`, and call
  `events.ApplyCapabilities` — IDENTICAL mechanism to the core
  `publish`/`subscribeWithHandle` path Phase 4 already built. The
  `Capabilities`-only field extraction (not the whole Options struct) was
  chosen, exactly as the Design anticipated — format/security/general
  middleware stay out of scope for THIS phase (Phase 4d's job).
- `adapters/mqtt/transport.go`: same fix, but resolved via
  `events.ResolveCapabilityValue` (this package's still-legacy
  mechanism, unaffected by mqtt5's Apply-interface migration) — matching
  `adapters/mqtt`'s OWN current shape exactly, not silently upgrading it
  to mqtt5's Apply shape (that remains Phase 5's job).
- `adapters/zeromq/transport.go`: same fix, `events.ApplyCapabilities`
  applied directly against the socket for BOTH Publish and Subscribe
  (HWM/Conflate ARE meaningful on both sides, confirmed by the existing
  `HWMSetter`/`ConflateSetter` extensions accepting any `FramedSocket`).
- All 3 adapters' `Attach`/`transport` doc comments updated to say
  "v1 scope, NARROWED by Phase 4c" instead of blanket "v1 scope" —
  explicitly naming Capabilities as closed and format/security/
  middleware as still open (Phase 4d), rather than leaving a stale,
  now-partially-wrong blanket claim in place.
- `demo_capability_mechanism.go` fixed to call `evClient.Publish`
  directly — zero adapter-package touch needed for this demo's own
  purpose anymore.
- 6 new tests added (2 per adapter — Publish and Subscribe honoring a
  declared Capabilities value), all passing; full existing test suites
  (`mqtt5`, `mqtt`, `zeromq`) pass UNCHANGED — confirming, as Phase 4/4b
  did before, that this is a pure mechanism-reach fix, not a behavior
  change to any EXISTING call path.

**Learnings:**
1. **A "the demo used the escape hatch" observation was a genuine,
   reproducible product gap, not an example-quality nit.** Tracing WHY
   the demo used the escape hatch (not just fixing the demo's code)
   surfaced a real, pre-existing "v1 scope" limitation dating back to
   `docs/design/d-0002-pubsub-workflow-simplification.md`'s Decision 5 —
   confirming the general principle that an example reaching for a
   lower-level API is itself a signal worth investigating, not just
   patching over.
2. **Cross-API comparison (REST vs. reqreply vs. events) was the fastest
   way to find the RIGHT fix shape.** REST's `Client.Call` was already
   "full-featured, no v1-scope asterisk"; reqreply's `AttachClient`/
   `AttachServer` had a coarser, attach-time-uniform partial fix; events
   had neither. Comparing all three directly (rather than designing
   events' fix in isolation) is what surfaced Option A (per-channel
   declared, matching REST's actual mechanism) as preferable to Option B
   (attach-time uniform, matching reqreply's partial fix) BEFORE writing
   any code.
3. **Symmetric field addition (`Publisher.WithOptions` mirroring
   `Subscriber.WithOptions`) cost nothing extra** — `buildChannelHandle`
   already accepted `opts any` generically and unconditionally populated
   `HandlerOpts` regardless of role; the ONLY asymmetry was
   `Publisher.Handle` passing a hardcoded `nil` instead of a real field.
   A one-line fix once the missing field/method was noticed — worth
   flagging as a lesson: an asymmetric fallback (`nil`) two structurally
   IDENTICAL types share is a natural place to look for exactly this
   kind of quietly-missing capability.
4. **Not every `evtClient := events.NewClient(...)` without a visible
   `.Attach()` call in the SAME file is a violation.** Re-auditing
   `demo_error_pattern.go` confirmed its several un-attached Clients are
   legitimate spec-registration-only containers (`pub.Handle(evtClient)`)
   feeding a SEPARATE `ports.SinkPort`/`PublishAdapter` dispatch path —
   a DIFFERENT, equally sanctioned mechanism, not a bypass of anything.
   Distinguishing "built a Client, used a lower-level path anyway" from
   "built a Client, registered its spec, dispatched through ports
   instead" mattered for not over-flagging false positives.

#### Phase 4d — Attach factory redesign: adapters expose `New*Transport` factories; attaching is EXCLUSIVELY an api-layer method

**Status: SHIPPED.** Found while reviewing `demo_capability_mechanism.go`:
the demo called `mqtt5adapter.Attach(evClient, broker, router)` — an
ADAPTER-namespaced free function that internally builds an unexported
`*transport` value and calls `evClient.Attach(...)` itself, hiding the
object entirely. User's explicit directive: "In the adapter layer we
can have a New factory to retrieving an attachable Client/Server with
the respective configuration provided by the user. This gets attached
to the api layer via an attach method owned by the api layer" — this is
arguably the MOST foundational expression of the "zero backdoor between
the api layer and the adapters" guardrail (Phase 4b): even the ATTACH
step itself must go through `api/events`/`api/rest`/`api/reqreply`, not
an adapter-owned convenience wrapper.

**Verified before implementing, not assumed:** does `Attach` itself
composite the spec by registering routes/channels? NO — `Route.Register`/
`Subscriber.Register` (operating on the SAME `Client`/`Server` object
`Attach` later binds a transport to) is what accumulates the spec +
dispatch registry; `Attach` is a strictly SEPARATE, later step that only
binds the wire transport. Confirmed `reqreply`'s `Builder` is literally
`type Builder = Server` (a type alias, not a separate type) — the same
one-object, two-separate-steps pattern holds across all 3 APIs. All 5
api-layer `Attach` methods (`events.Client.Attach`, `rest.Client.Attach`,
`rest.Server.Attach`, `reqreply.Client.Attach`, `reqreply.Server.Attach`)
ALREADY EXISTED — zero core `api/*` changes were needed; this phase's
entire job was removing the adapter-side convenience wrappers that hid
them.

**Design — one uniform shape, all 12 Attach-family functions inventoried
via grep and replaced identically:**

```go
// BEFORE — adapter-namespaced, hides the transport object, does the
// attach itself:
func Attach(client *events.Client, mqttClient MQTTClient, router MQTTRouter) error {
    return client.Attach(&transport{caller: newCaller(mqttClient, router, client)})
}

// AFTER — adapter provides ONLY a configured transport value via a
// factory taking a SINGLE Options struct (confirmed with user: no
// positional params, even for currently-required config — a
// deliberate, strict, declarative shape, trading compile-time-enforced
// required-arg positions for a uniform New(opts) pattern); attaching is
// EXCLUSIVELY the caller's own client.Attach(...) call:
type TransportOptions struct {
    Client MQTTClient
    Router MQTTRouter
}

func NewTransport(opts TransportOptions) events.Transport {
    return &transport{caller: newCaller(opts.Client, opts.Router, nil)}
}

// caller:
transport := mqtt5.NewTransport(mqtt5.TransportOptions{Client: broker, Router: router})
if err := evClient.Attach(transport); err != nil { ... }
```

The Options struct name is the factory name + `Options` (mirrors this
codebase's existing `PublishOptions`/`SubscribeOptions`/`ServeOptions`/
`CallOptions` convention exactly); pre-existing `ServeOptions`/
`CallOptions` (reqreply) are NESTED as a field inside the new structs
(`Serve ServeOptions`/`Call CallOptions`), not flattened — keeping their
own established field names/doc comments intact.

**Full inventory (12 functions removed, 12 factories added, all
verified mechanically trivial — each old `Attach*` function was
confirmed to be EXACTLY `return client.Attach(&transport{...})`, a
one-line wrap, so this is pure code motion + a config-shape change, not
new runtime behavior):**

| Package | Removed | Added |
|---|---|---|
| `adapters/mqtt5` (events) | `Attach(client, mqttClient, router)` | `NewTransport(TransportOptions{Client, Router})` |
| `adapters/mqtt` (events) | `Attach(eventsClient, mqttClient)` | `NewTransport(TransportOptions{Client})` |
| `adapters/zeromq` (events) | `Attach(client, sock)` | `NewTransport(TransportOptions{Socket})` |
| `adapters/nethttp` (rest client) | `Attach(client, httpClient, baseURL)` | `NewClientTransport(ClientTransportOptions{HTTPClient, BaseURL})` |
| `adapters/nethttp` (rest server) | `AttachMux(builder, mux, addr)` | `NewServerTransport(ServerTransportOptions{Mux, Addr})` |
| `adapters/chi` (rest server) | `AttachRouter(builder, r, addr)` | `NewServerTransport(ServerTransportOptions{Router, Addr})` |
| `adapters/mqtt5` (reqreply server) | `AttachServer(server, client, router, opts...)` | `NewServerTransport(ServerTransportOptions{Client, Router, Serve})` |
| `adapters/mqtt5` (reqreply client) | `AttachClient(client, mqttClient, router, opts...)` | `NewClientTransport(ClientTransportOptions{Client, Router, Call})` |
| `adapters/zeromq` (reqreply server, REQ/REP) | `AttachServer(server, sockets, opts...)` | `NewServerTransport(ServerTransportOptions{Sockets, Serve})` |
| `adapters/zeromq` (reqreply client, REQ/REP) | `AttachClient(client, sockets, opts...)` | `NewClientTransport(ClientTransportOptions{Sockets, Call})` |
| `adapters/zeromq` (reqreply server, ROUTER/DEALER) | `AttachRouterServer(server, sockets, opts...)` | `NewRouterServerTransport(RouterServerTransportOptions{Sockets, Serve})` |
| `adapters/zeromq` (reqreply client, ROUTER/DEALER) | `AttachDealerClient(client, sockets, opts...)` | `NewDealerClientTransport(DealerClientTransportOptions{Sockets, Call})` |

**Blast radius, sized explicitly before starting (confirmed acceptable
— breaking change, no deprecate-and-keep):** every example across
`examples/events-api`/`examples/reqreply-api`/`examples/rest-api` and
any other example touching these 5 adapter packages; every adapter's
own test suite; the 3 `NoXTransportAttachedError` message strings
(previously suggesting `nethttp.Attach(client, httpClient, baseURL)` as
the fix); `docs/features/`/`docs/guides/`/
`.github/instructions/go-codex.instructions.md`/the
`add-a-new-adapter` skill's own reference pattern (so future adapters
follow the NEW convention from day one).

**Also folded in, same sweep:** `examples/events-api`'s remaining
`NewSubscribeTransport`/`NewPublishTransport` DIRECT usages (the LOWER,
adapter-facing escape-hatch tier, distinct from but adjacent to the
Attach-factory work) — converted to the `Client`+`Attach`+
`Publish`/`Subscribe`/`ServeSubscribers` pattern in every demo EXCEPT
`demo_escape_hatch_workflow.go`, whose entire documented purpose is
demonstrating that escape hatch remains available for cases the
`Client` surface doesn't (yet) cover.

- **Learnings (recorded, real evidence from Implement — not
  speculation):**
  - **A real deadlock, not a hypothetical one.** `Client.Attach`/
    `Server.Attach` called the new `BindClient`/`BindServer` hook WHILE
    STILL HOLDING their own write lock. `zeromq`'s reqreply server-side
    `BindServer` needs `server.RegisteredTopics()`, which takes an
    `RLock` on the SAME mutex — `sync.RWMutex` is not reentrant, so this
    deadlocked immediately in a real test run, not a contrived one. Fixed
    by releasing the lock BEFORE calling the Bind hook in all 3 `Attach`
    implementations. General lesson: never call an optional
    caller-supplied hook while holding a mutex the hook might re-enter
    via another method on the same receiver.
  - **Converting the escape-hatch demos surfaced a genuine, previously
    masked test-infrastructure bug**, not a mechanism gap:
    `MockRouter.WaitHandler` (events-api's test broker) matched topics by
    an EXACT string compare against the caller's literal
    `"sensors/{sensorID}/..."` form, but registered map keys are always
    the wildcard-derived form (`"sensors/+/..."`) — so it always
    silently consumed its full ~1s worst-case poll. This was invisible
    under `NewSubscribeTransport` (non-blocking registration, survives a
    short ctx) but became a real, intermittent timeout under
    `Client.Subscribe` (blocking, unregisters on ctx cancel) once a
    short-lived handler ctx raced against the ~1-2s worst-case poll.
    Fixed by checking both the literal and the wildcard-normalized form.
    Confirms this roadmap's own "verify equivalence by migration, not by
    review" lesson (Phase 1) once again — converting REAL call sites
    found a bug a design review never would have.
  - **The conversion also surfaced a second real, if narrower, gap:**
    `Client.Subscribe`'s reflection shim never consulted
    `events.DeadLetter` (only `ErrorChannel`) — fixed as a direct
    side-effect of the WaitHandler debugging session, mirroring
    `adapter.go`'s existing `tryDeadLetter` convention.
  - **Two escape-hatch usages in `demo_error_pattern.go` were
    deliberately NOT converted**, because they depend on features
    `Client.Subscribe` genuinely lacks today (`SubscribeOptions.OnError`
    callback dispatch; `SubscribeMW`/security enforcement) — both are
    explicitly Phase 4e's scope, not silently dropped. Tracked via
    inline comments at the call sites AND 2 dedicated SQL todos
    (`p4e-onerror-dispatch`, `p4e-subscribemw-security-real-case`) so
    they are not lost across the phase renumbering.
  - **Full verification, repeated (not one-shot):** `gofmt -l .` clean;
    `go build ./...`/`go vet ./...` clean; `go test ./...` run 4 times
    (once full, 3x targeted at `./api/...`/`./adapters/...`) all green —
    one apparent one-off `FAIL` on a single full run did not reproduce
    across 3 immediate repeats of the same targeted packages, consistent
    with test-timing flakiness rather than a real regression; all
    `examples/*` run to exit 0; `just check` (gosec + staticcheck) clean,
    zero new suppressions.

#### Phase 4e — closing the REMAINING `Client.Publish`/`Subscribe` "v1 scope" gaps

**Status: SHIPPED — all 4 stages (A/B/C/D) complete for all 3 adapters
(`mqtt5`, `mqtt` v3, `zeromq`).** Renumbered from the original "Phase 4d" — the
Attach-factory redesign above was inserted BEFORE this phase since it
reshapes the SAME `transport.go`/`reqreply_transport.go` files this
phase touches; doing the factory redesign first avoids reworking those
files twice. Immediately follows the NEW Phase 4d, BEFORE Phase 5 —
finishing the FULL guardrail closure on `api/events`'s
`Client.Publish`/`Subscribe` shim (not just its Capabilities slice)
avoids doing overlapping shim-editing work in two separate passes once
Phase 5 touches the same files for reqreply parity.

**Concrete, confirmed-real gaps found DURING Phase 4d's own examples
sweep (recorded here explicitly so they are NOT forgotten — user's
direction: "You can move it to 4e. The important thing is that we do
not forget it!"):**

- `examples/events-api/demo_error_pattern.go`'s
  `demoErrorChannelActionsSubscribeSide` (the `run` closure, ~line 388)
  observes `SubscribeOptions.OnError` firing on a handler business
  error — `Client.Subscribe`'s reflection shim (`adapters/mqtt5/transport.go`)
  never calls `opts.OnError` at all. Kept on the `NewSubscribeTransport`
  escape hatch, with an explicit comment naming this phase, pending
  Item 1 (format overrides)/a new "OnError dispatch" sub-item below.
- `examples/events-api/demo_error_pattern.go`'s
  `demoErrorChannelMiddlewareCombo` (the `securedSub`/`dataTransport`
  half, ~line 549) proves a security-rejecting `SubscribeMW` blocks the
  handler — `Client.Subscribe` doesn't run SubscribeMW/security
  dispatch at all today, so this is a DIRECT, real-world instance of
  Item 2's "silent security bypass" finding below, not merely a
  hypothetical. Kept on the escape hatch with an explicit comment.
- Once Phase 4e ships OnError dispatch + SubscribeMW/security
  enforcement in `Client.Subscribe`, BOTH of these demo functions should
  be converted to the `Client.Attach`+`Client.Subscribe` pattern (same
  sweep discipline as the rest of Phase 4d), closing the LAST 2
  `NewSubscribeTransport` escape-hatch usages in `examples/events-api`
  outside `demo_escape_hatch_workflow.go` (which stays exempt by
  design).

**Scope, corrected by a code-reading review pass (done before
implementation started) — the gap is BIGGER than originally listed.**
Reading `subscribeHandler[T]`/`publish[T]`'s real per-message pipeline
(`adapters/mqtt5/adapter.go`, the reference implementation this shim
was always meant to match) end-to-end shows the reflection shim
(`transport.go`'s `Client.Publish`/`Subscribe`) currently skips EVERY
pipeline step between decode and calling `fn` except `ApplyCapabilities`
(Phase 4c) — not just the 3 originally-named items. Two ADDITIONAL,
previously-undocumented gaps found this pass, folded into scope below
rather than deferred to yet another phase:

- **Property-merge** (`ChannelHandle.PropertyMergeFields`/
  `MergePropertyVars`) — a channel declaring a `MergedPropertyParam`
  directly on `NewChannel` never gets those fields populated from
  incoming User Properties through `Client.Subscribe`.
- **Codec-backed Middleware/Transform dispatch**
  (`ChannelHandle.MiddlewareHandlers`/`ClientMiddlewareHandlers`, the
  `.Use()`-attached mechanism from
  docs/design/d-0003-codec-declared-middlewares.md) — entirely
  unexercised by the shim today.
- Also folded in: **User-Property-param validation**
  (`SubscribeOptions.UserPropertyParams`, the adapter-local mirror of
  REST's header-param validation) — likewise unexercised.

Per each adapter's own "v1 scope" doc comment (still true after Phase
4c) and the REST reference shape, the full item list:

1. **Format overrides** — REST's `ClientCallOptions{RequestFormats,
   ResponseFormats any}`/`ClientConsumeOptions{Formats any}` is the
   reference: a per-call, adapter-agnostic options struct the
   reflection shim resolves generically. `events.Client.Publish`/
   `.Subscribe` take NO per-call options param today
   (`Publish(ctx, pub any, msg any)`/`Subscribe(ctx, sub any, fn any)`)
   — needs an analogous `events.ClientPublishOptions`/
   `ClientSubscribeOptions` as a variadic trailing param — PER-CALL,
   unlike Phase 4c's Capabilities (per-channel-declared), since format
   overrides are legitimately a call-time concern.
2. **Security/credential ClientMW enforcement — the MOST SEVERE gap.**
   REST's `Client.Call` resolves declared security/credential ClientMW
   automatically from the RouteHandle. `events.Client.Publish`/
   `.Subscribe`'s shim currently skips this ENTIRELY — confirmed via
   its own doc comment: *"a channel declaring Security plus a
   correctly-paired credential SubscribeMW/PublishMW gets ZERO runtime
   enforcement through Client.Attach — no credential is fetched or
   injected, silently, with no error."* This is a SILENT SECURITY
   BYPASS, not just a missing convenience. Resolved via reflecting
   `ChannelHandle.Implementations`/`ClientImplementations`, mirroring
   `subscribeWithHandle`'s/`publish`'s own already-working security
   dispatch.
3. **General-purpose middleware wrapping** — declared `SubscribeMW`/
   `PublishMW` (logging/observability/rate-limiting, non-security) also
   currently skipped — same fix shape as #2, lower severity.
4. **Property-merge** and **User-Property-param validation** (see
   above) — wired alongside #2/#3, same reflection technique.
5. **Codec-backed Middleware/Transform dispatch** (see above).
6. Mirror across all 3 adapters (`mqtt5`, `mqtt` v3, `zeromq`).
7. **Definition of done:** drop the "v1 scope" doc-comment framing
   entirely from all 3 adapters' `transport.go`, matching
   `adapters/nethttp/clienttransport.go`'s already-achieved "no
   remaining v1 scope asterisk" wording.

**Design decisions, resolved via this review — not left open:**

- **No core `api/events` changes needed for security/general-middleware
  dispatch.** `middleware.ServerImplementation`/`ClientImplementation`
  are ALREADY non-generic (`Fn any`, `Name string`, `Satisfies []string`)
  plain fields on `ChannelHandle` (`Implementations`/
  `ClientImplementations`), reachable via the SAME
  `elem.FieldByName(...)` reflection technique
  `adapters/mqtt5/reqreply_transport.go`'s `Call` and
  `adapters/nethttp/clienttransport.go`'s `Call` ALREADY use for their
  own client-side security dispatch — proven, shipped precedent, not a
  new technique. `impl.Fn`'s boxed value is ALREADY a concretely-T Go
  closure (built when `.SubscribeMW(fn)`/`.PublishMW(fn)` was called at
  declare time) — `reflect.ValueOf(impl.Fn).Call(...)` invokes it
  directly; a general-purpose wrapping Fn's decorator shape
  (`func(next func(ctx,T) error) func(ctx,T) error`) is composed via
  `reflect.MakeFunc`, mirroring `reqreply_transport.go`'s `innerCall`/
  `wantGeneralFnType` technique and
  `adapters/nethttp/clienttransport.go`'s `networkStep` exactly — an
  ALREADY-established codebase idiom for this exact problem.
- **Two SMALL, additive core `api/events` changes ARE needed** — thin
  monomorphized wrapper methods on `ChannelHandle[T]`, mirroring the
  ALREADY-EXISTING convention `EncodeVars`/`DecodeMergedWithFormats`/
  `MergePropertyVars`/`ErrorResponseFor`/`DeadLetterFor` establish (a
  core generic helper gets a thin per-T method wrapper specifically so
  reflection can call it):
  `ChannelHandle[T].DispatchSubscribeMiddleware(ctx, msg *T, topicVars, propertyVars map[string]string) error`
  and
  `ChannelHandle[T].DispatchPublishMiddleware(ctx, msg T) (topicVars, propertyVars map[string]string, err error)`
  — each a ONE-LINE body calling the existing free generic function
  (`DispatchSubscribeMiddlewareHandlers`/`DispatchPublishMiddlewareHandlers`,
  `api/events/transform_dispatch.go`) with `h.MiddlewareHandlers`/
  `h.ClientMiddlewareHandlers`. No new behavior, no new type — purely a
  reflection-callability adapter, same bar as every other handle method.
- **Per-call validation, not Attach-time eager validation — matches
  existing REST/reqreply precedent, not a new choice.** Confirmed via
  reading `adapters/nethttp/clienttransport.go`'s `Call`: it validates
  `ClientImplementations` shapes at the START of EACH `Call`, not once
  at `Attach` time. `Client.Publish`/`Subscribe`'s shim does the SAME —
  validate shapes once per `Publish` call / once at the start of each
  `Subscribe` call (before its blocking per-message loop starts,
  mirroring `subscribeWithHandle`'s own "validated EAGERLY here, before
  the broker subscription is made" timing) — a malformed Fn fails
  loudly and immediately via the existing
  `middleware.MiddlewareShapeError`, never silently. This RESOLVES the
  original "fail-open vs fail-closed" question: there is no
  silent-bypass window left once this ships — the question was really
  "what happens during the gap," and closing the gap answers it.
- **Format overrides struct shape** — mirrors `rest.ClientConsumeOptions`
  (single `Formats any` field, not REST `Call`'s Request/Response
  split, since Publish/Subscribe are each single-direction):
  `type ClientPublishOptions struct { Formats any }` /
  `type ClientSubscribeOptions struct { Formats any }` — added as a
  trailing variadic param:
  `Publish(ctx, pub, msg any, opts ...ClientPublishOptions) error` /
  `Subscribe(ctx, sub any, fn any, opts ...ClientSubscribeOptions) error`
  — non-breaking for every existing call site (0 args = current
  behavior).
- **Error-dispatch ordering, resolved by mirroring `subscribeHandler[T]`'s
  own real order exactly** (property-merge → user-property-param
  validation → security/codec-credential → security/Implementations →
  codec-Middleware/Transform → handler call), with ErrorChannel/
  DeadLetter/`OnError` consulted at EVERY one of those failure points,
  exactly as `adapter.go`'s existing per-step `tryPublishErrorChannel`/
  `tryDeadLetter`/`opts.OnError` triplet already does — no reordering
  question remains once the shim runs the SAME steps in the SAME order
  as the reference implementation it was always meant to match.
- **Format overrides are bundled into `adapters/mqtt5`'s Stage A/B
  work** (user-confirmed), not a fully separate later stage — they
  share the eager-shape-validation scaffolding Stage A/B already builds,
  and security (the most severe gap) is addressed first regardless.
  Only `adapters/mqtt` (v3) and `adapters/zeromq` need a dedicated
  later stage purely for format-override wiring (the core
  `ClientPublishOptions`/`ClientSubscribeOptions` structs land once,
  during mqtt5's work).

**Sequencing: Phase 4c → Phase 4d → Phase 4e → THEN Phase 5**
(closing `adapters/mqtt` v3's Phase 4 pub/sub Capability parity gap,
and the `api/reqreply` Apply shift for mqtt5/zeromq) → Phase 5a (moving
the type-safe escape hatch onto the API layer, formerly "Phase 4f").
Phase 6 (`api/rest`)/Phase 7 (Review & Closeout / D-0006 rework) remain
after that, unchanged in relative order.

**Stages A/B implementation plan (`adapters/mqtt5`, the reference
adapter) — user-confirmed before implementing:**

- **Format overrides bundled into Stage A/B** (not a separate later
  stage) — shares the eager-shape-validation scaffolding Stage A/B
  already builds; security (the most severe gap) addressed first
  regardless.
- Stage A = full `Client.Subscribe` pipeline parity (property-merge,
  User-Property-param validation, codec-based + declarative
  SubscribeMW security, codec-Middleware/Transform dispatch,
  general-purpose wrapping, format overrides). Stage B = the SAME for
  `Client.Publish`. Stage C narrows to ONLY wiring the (once-added)
  core `ClientPublishOptions`/`ClientSubscribeOptions` structs' Formats
  resolution into `adapters/mqtt` (v3) and `adapters/zeromq`. Stage D =
  mirroring Stage A/B's full security/middleware pipeline to those same
  2 adapters.

- **Learnings (recorded, real evidence from Implement — not
  speculation):**
  - **A previously-existing, ALREADY-SHIPPED reflection-only dispatch
    mechanism was found and reused, not duplicated.**
    `adapters/mqtt5/caller.go` (built for `(*caller).ServeSubscribers`'s
    own registry-walk dispatch) ALREADY contained
    `subscribeSecurityFnType`/`generalWrapFnType`/
    `validateSubscribeImplementationShapesReflect`/
    `runSubscribeSecurityImplsReflect`/`wrapHandlerGeneralReflect`/
    `runErasedBuiltinSecurityCheck` — the EXACT subscribe-side
    reflection helpers Stage A needed. Confirmed via a first-attempt
    duplicate (`transport_dispatch.go`) that collided by function name
    at compile time — the collision itself is direct evidence the two
    independently-designed mechanisms converged on identical shapes,
    validating the design review's "already-established, proven
    precedent" claim. Deleted the duplicate subscribe-side helpers and
    reused `caller.go`'s directly; only wrote NEW publish-side
    siblings (`runPublishSecurityImplsReflect`,
    `validateClientImplementationShapesReflect`,
    `wrapClientGeneralDecoratorReflect`,
    `buildPublishSecurityFnType`) plus small shared HandlerOpts-
    extraction/security-requirement-resolution/format-override helpers
    in a NEW `transport_dispatch.go`.
  - **`caller.go`'s existing mechanism was confirmed to be a
    deliberate, documented SUBSET** ("a known simplification of this
    reflect dispatch path" — its own doc comment) — no MergeFields/
    property-merge/ErrorChannel/DeadLetter/codec-Middleware/format
    overrides. Stage A's `Client.Subscribe` needed ALL of those (its
    whole point), so `caller.go`'s pieces were reused for the security/
    general-MW SUBSET only; property-merge, User-Property-param
    validation, ErrorChannel/DeadLetter dispatch, and codec-Middleware
    dispatch were newly wired directly in `transport.go`, matching
    `subscribeHandler[T]`'s real order exactly (property-merge →
    user-property-param validation → security/codec-credential →
    security/Implementations → codec-Middleware/Transform → handler
    call), each with the SAME ErrorChannel→DeadLetter→OnError fallback
    triplet — factored into one `dispatchFailure` closure to avoid
    repeating it at all 6 call sites.
  - **2 new core `api/events` additions, exactly as scoped**:
    `ChannelHandle[T].DispatchSubscribeMiddleware`/
    `DispatchPublishMiddleware` (thin one-line wrapper methods) and
    `ClientPublishOptions`/`ClientSubscribeOptions` + the variadic
    `Transport.Publish`/`Subscribe`/`Client.Publish`/`Subscribe`
    signature change — confirmed non-breaking for every existing call
    site (0 variadic args = current behavior); the ONE test-only
    breakage was `api/events/builder_test.go`'s `mockTransport`, fixed
    by adding the 2 new variadic params to its method signatures.
  - **Full verification, repeated:** `gofmt -l .` clean; `go build
    ./...`/`go vet ./...` clean; `go test ./...` full run green (56
    packages); all `examples/*/` exit 0; `just check` (gosec +
    staticcheck) clean, 0 issues, 492 files, 0 new suppressions. 15 new
    tests added (`adapters/mqtt5/client_full_pipeline_test.go`),
    covering property-merge, User-Property-param validation
    (success/reject), codec-based security credential (reject),
    SubscribeMW/PublishMW security (success), general-purpose wrapping
    (order verified both sides), codec-Middleware dispatch (order
    verified both sides), format overrides (both sides), and eager
    shape-validation errors (both sides) — all passing on first real
    run against the NEW `Client.Attach`+`Client.Subscribe`/`Publish`
    surface (never the lower escape hatch).
  - `adapters/mqtt` (v3) and `adapters/zeromq`'s `Transport.Publish`/
    `Subscribe` were given the SAME variadic-param signature (interface
    conformance only) with an explicit code comment marking Stage D as
    the deferred, NOT-forgotten follow-up — mirrors this roadmap's own
    established "close the interface now, mirror the full
    implementation in a later, explicitly-tracked stage" discipline
    from Phase 4d's Attach-factory rollout.

**Stage D (`adapters/mqtt` v3, `adapters/zeromq`) + Stage E
(definition-of-done) — Learnings:**

- **Both adapters ALREADY had reusable, pre-existing reflection
  dispatch helpers for the subscribe side, confirming the design
  review's precedent claim a SECOND time (Stage A's mqtt5 reuse was the
  first).** `adapters/mqtt/caller.go` (built for its own
  `ServeSubscribers`) already had `runSubscribeSecurityImplsReflect`/
  `validateSubscribeImplementationShapesReflect` in the EXACT shape
  needed; `adapters/zeromq/serve_subscribers.go` already had
  `validateSubscribeImplementationShapesReflect`. Both reused directly
  — only publish-side siblings (`runPublishSecurityImplsReflect`,
  `validateClientImplementationShapesReflect`,
  `wrapClientGeneralDecoratorReflect`) were newly written per adapter,
  in a new `transport_dispatch.go` file each, mirroring mqtt5's own
  file structure.
- **The two adapters' security Fn SHAPES genuinely differ, confirmed
  by reading each adapter's OWN generic reference implementation before
  writing any reflection code** — not assumed identical across
  adapters: `mqtt` (v3)'s subscribe-side security shape is
  MAP-based (`func(context.Context, pahomqtt.Message, *T)
  (map[string][]string, error)`, mirroring mqtt5's grant-merge design,
  via `middleware.CheckScopes`), while `zeromq`'s is a plain
  `func(context.Context, *T, []route.SecurityRequirement) error` (no
  grants map — zeromq has no built-in credential-extraction mechanism
  of its own). A first-draft mqtt-v3 test using the WRONG (zeromq-style)
  shape silently no-opped (shape mismatch → eager
  `MiddlewareShapeError` → the test's own `_ = c.Subscribe(...)`
  discarded it) — caught by the test's own assertion failing with a
  zero-value error, not a panic; fixed by reading `adapter.go`'s actual
  `impl.Fn.(func(...))` type assertion for each adapter before writing
  the corresponding test, not by assumption.
- **`mqtt` (v3)'s Capabilities resolution was deliberately left
  UNTOUCHED** (still `events.ResolveCapabilityValue`, the pre-Phase-4
  mechanism) — migrating it to the Apply-interface shape `mqtt5`/
  `zeromq` already use is explicitly Phase 5's job, not this one's;
  Stage D's security/middleware/format-override pipeline work was
  wired ALONGSIDE the existing Capabilities resolution, not through it.
- **A REAL bug found via example conversion, not via unit tests** (the
  SAME lesson Phase 1 first taught this roadmap, and Phase 4d's own
  WaitHandler bug repeated): converting
  `examples/events-api/demo_error_pattern.go`'s LAST 2
  `NewSubscribeTransport` escape-hatch usages onto
  `Client.Attach`+`Client.Subscribe` (closing the final escape-hatch
  usages outside `demo_escape_hatch_workflow.go`) surfaced that all 3
  adapters' shared `dispatchFailure` closure called a declared
  `OnError` callback UNCONDITIONALLY after every branch — including
  when a declared `events.ErrorChannel` had ALREADY matched and
  published an `ErrorRespond` response, which `tryPublishErrorChannel`'s
  own contract says must return immediately, `handled=true`, WITHOUT
  ever calling `OnError`. None of the 27 new unit tests (mqtt5: 16
  including the regression test; zeromq: 12; mqtt v3: 12) caught this,
  because none happened to combine a matched `ErrorRespond` declaration
  with a non-nil `OnError` callback in the SAME test — exactly the
  combination the demo's `onErrorCalled` assertion exercises. Fixed in
  all 3 adapters' `dispatchFailure` closures (added explicit `return`
  after the `ErrorRespond`-publish branch AND after a successful
  DeadLetter publish); a regression test
  (`TestClientSubscribe_ErrorChannel_MatchedRespond_SkipsOnError`) was
  added to `adapters/mqtt5/client_full_pipeline_test.go` to lock in the
  fix (verified 3x non-flaky). **Lesson reconfirmed**: converting a
  REAL, pre-existing caller onto a new mechanism is the actual
  verification step for "this dispatcher is equivalent to the reference
  implementation" — a passing, hand-written unit test suite is not
  equivalent to exercising it through genuine, independently-written
  call sites.
- **Definition of done, verified via grep, not assumed:** all 3
  adapters' `transport.go` doc comments now say "FULL-FEATURED... no
  remaining 'v1 scope' asterisk," matching
  `adapters/nethttp/clienttransport.go`'s own wording exactly. The 3
  guide docs (`docs/guides/mqtt5.md`/`mqtt.md`/`zeromq.md`) and
  `.github/instructions/go-codex.instructions.md` were swept for the
  same stale "v1 scope limits... use `New*Transport` directly instead"
  phrasing.
- **Full verification, repeated across the WHOLE Stage D+E change,
  not just the new files:** `gofmt -l .` clean; `go build ./...`/
  `go vet ./...` clean; `go test ./...` full run green (all packages,
  post-bugfix); all `examples/*/` exit 0 (including the converted
  `demo_error_pattern.go`, manually re-inspected for the corrected
  `onErrorCalled=false` output on the `ErrorRespond` case); `just check`
  (gosec + staticcheck) clean, 0 issues, 494 files, 0 new suppressions.

**Addendum — a real, previously-untracked gap found and fixed during a
LATER `examples/events-api` escape-hatch sweep (not part of the
original Stage A-E work above):** reviewing every remaining
`NewPublishTransport`/`NewSubscribeTransport` usage in
`examples/events-api` for staleness (per an explicit user request)
surfaced that `Client.Subscribe`'s dispatch handler in BOTH
`adapters/mqtt5/transport.go` and `adapters/mqtt/transport.go` never
called `context.WithValue(ctx, contextKey{}, msg)`/
`context.WithValue(msgCtx, userPropsKey{}, msg.Properties.User)` the
way `adapter.go`'s `makeSubscribeMessageHandler`/`subscribeHandler[T]`
already does — so `MessageFromContext`/`UserPropertiesFromContext`
(mqtt5) and `MessageFromContext` (mqtt v3) silently returned
`(nil, false)` through `Client.Subscribe`, even though Stage A's own
"full pipeline parity" claim implied otherwise. This is a genuine gap
Stage A missed (it added property-MERGE support but never added raw
ctx-based message/property access). **Fixed**: injected the SAME
`context.WithValue` calls at the SAME pre-decode placement in both
adapters' `Client.Subscribe` handlers, threading the resulting
`msgCtx` through security/middleware/handler dispatch (mqtt v3's
handler additionally had to move its once-precomputed `ctxVal` INSIDE
the per-message closure, since it must now vary per message). 2 new
regression tests added
(`TestClientSubscribe_MessageAndUserPropertiesFromContext_Retrievable`
in `adapters/mqtt5`, `TestClientSubscribe_MessageFromContext_Retrievable`
in `adapters/mqtt`). This closes the LAST remaining reason (beyond
Category 3's structural one, see below) any `examples/events-api` demo
needed the escape hatch — `demo_user_property_middleware.go` converted
onto `Client.Attach`+`Client.Subscribe`/`Publish` as a direct result.

**Also found during the SAME sweep: 2 stale-but-harmless comments,
fixed.** `demo_security_subscribemw.go`'s mqtt5 leg and
`demo_property_merge_direct_attachment.go`/`demo_wildcard_subscription.go`
had never been converted despite Stage A-E already closing the
capability gaps their (now-stale) comments cited — converted onto
`Client.Attach`+`Client.Subscribe`/`Publish`, no new mechanism needed.

**Definition-of-done re-confirmed, not just assumed:** after this
addendum, exactly ONE real `NewSubscribeTransport` call site remains in
`examples/events-api` — `demo_escape_hatch_workflow.go`, the
DESIGNATED exempt demo — confirmed via `grep -rn
"NewPublishTransport\|NewSubscribeTransport" examples/events-api/*.go`
(every other match is comment-only, and each comment was individually
verified accurate, not just left unchecked).

### Phase 5 — closing `adapters/mqtt` (v3)'s Phase 4 pub/sub Capability parity gap, and applying the same `Apply`/`ApplyCapabilities` shift to `api/reqreply`'s mqtt5/zeromq call sites

**Status: SHIPPED.** Two independent workstreams, both confirmed to
match this section's original scope exactly, with zero surprises found
during implementation:

- **`adapters/mqtt` (v3) — the large piece.** `Capability` now REQUIRES
  `Apply(wire *WireAttributes) (bool, error)` (a real interface, not a
  marker), mirroring `adapters/mqtt5`'s identical shape exactly. Every
  legacy positional/field-based backdoor was removed entirely:
  `publish[T]`/`publishHandle[T]`'s `qos byte, retained bool` params
  (`adapter.go`), `subscribe[T]`/`subscribeHandle[T]`/
  `serveOneSubscriber[T]`'s `qos byte` param (`caller.go`), the
  reflection-based `Client.Publish`/`Client.Subscribe` dispatch's inline
  `events.ResolveCapabilityValue` resolution (`transport.go`),
  `NewPublishTransport`/`NewSubscribeTransport`'s `qos byte, retained
  bool`/`qos byte` params (`handletransport.go`), and
  `SubscribeAdapter`'s `qos byte` param +
  `MQTTDrainPublishOptions.QoS`/`.Retained` fields (`binding.go`) — all
  replaced by `Capabilities []Capability`-only construction, applied via
  `events.ApplyCapabilities` against a `WireAttributes` value. A
  post-implementation grep sweep (mirroring Phase 4b's own audit
  discipline) confirmed zero remaining legacy call sites.
- **`api/reqreply`'s Apply shift — the small piece.**
  `adapters/mqtt5/reqreply_transport.go`'s `Serve`/`Call` capability
  resolution (previously manual `events.ResolveCapabilityValue[Capability,
  QoS/Retained]` calls) now goes through the SAME `WireAttributes`+
  `events.ApplyCapabilities` pattern Publish/Subscribe already use — a
  pure relocation, zero behavior change (confirmed via the full
  pre-existing reqreply test suite passing unchanged). `adapters/zeromq`
  needed ZERO changes — confirmed via re-reading all 5 of its reqreply
  `applyCapabilities` call sites, already delegating to
  `events.ApplyCapabilities` since Phase 4.

**Definition-of-done met:** `adapters/mqtt` (v3) now meets the FULL
zero-backdoor guardrail (see "Architectural guardrail" above) — the
SAME bar `adapters/mqtt5` already met.

**Real call sites migrated (breaking change, confirmed bounded via
repo-wide grep before starting, exactly as anticipated):**
`examples/events-api/mqttbroker/broker.go`'s `NewPublishTransport` call
and `examples/events-api/demo_escape_hatch_workflow.go`'s
`NewSubscribeTransport` call (both switched to `Capabilities`-based
construction); `examples/sensor-service/main.go`'s `SubscribeAdapter`
call (dropped the now-removed positional `qos` argument — it was
already `0`, the same value `WireAttributes{}`'s zero value produces,
so no `Capabilities` value was needed to preserve behavior).

**Learnings:**

1. **Applying Phase 4b's OWN corrected lesson directly, from the start,
   avoided repeating its mistake.** Phase 4b had to walk back an
   earlier decision to "fold Capabilities in internally while keeping
   the raw QoS/Retained fields, since they're a separate mechanism" —
   this round went straight to the Capabilities-only end state for
   `MQTTDrainPublishOptions`/`SubscribeAdapterOptions` in one pass, with
   no intermediate "keep both" step to later walk back.
2. **The mechanical test-migration surface was genuinely large (20+
   call sites across 5 test files) but ENTIRELY mechanical** — every
   call site's literal `1`/`false` qos/retained argument was simply
   removed (Go generic type inference then resolves cleanly against
   the new, shorter signature); zero test assertions changed meaning,
   confirming — exactly as Phase 4's own Learnings #4 predicted for
   this exact follow-on — that this is a pure mechanism relocation, not
   a behavior change. 2 tests (`TestServeSubscribers_CapabilitiesOverridesQoS`,
   `TestPublish_CapabilitiesRetainedFallback`) tested OLD dual-path
   semantics that no longer exist (Capabilities "overriding" a
   now-deleted QoS field/call-time fallback) — renamed and rewritten to
   assert the NEW single-mechanism semantics, mirroring
   `adapters/mqtt5`'s own equivalent test names/shapes exactly
   (`TestServeSubscribers_CapabilitiesSetsQoS`/
   `TestPublish_CapabilitiesSetsRetained`).
3. **The reqreply Apply-shift's `WireAttributes` seeding required care
   to preserve 2 DIFFERENT pre-existing default/precedence behaviors,
   not a single uniform default** — `Serve`'s reply-publish path
   defaulted to a HARDCODED `QoS 1` (unrelated to any `CallOptions`),
   while `Call`'s request-publish path defaulted to `CallOptions.QoS`
   (itself defaulting to 1 only when unset) — confirmed via
   `events.ApplyCapabilities`'s own semantics (only calls `Apply` for
   capabilities PRESENT in the slice, leaving a pre-seeded
   `WireAttributes` value untouched otherwise) that pre-seeding
   `wire := WireAttributes{QoS: <the correct prior default for THIS
   call site>}` before calling `ApplyCapabilities` exactly reproduces
   each site's own prior fallback behavior — a generalizable technique
   for any future relocation onto `ApplyCapabilities` where the
   pre-migration code had a non-zero default.

#### Phase 5a — moving the type-safe escape hatch itself onto the API layer (formerly "Phase 4f")

**Status: Implemented.** `api/reqreply.ServeWithTransport`/
`CallWithTransport` and `api/rest.CallWithTransport` have shipped;
`adapters/mqtt5`/`adapters/zeromq`'s `Serve`/`Call`/`ServeRouter`/
`CallDealer`/`CallHandle` and `adapters/nethttp.CallWithHandle` are
fully deleted. See "Implementation learnings" below for what changed
relative to the design sketch this section originally shipped with —
`ClientCallOptions` grew substantially beyond the two-field sketch, and
`ServeOne` was NOT relocated (a design decision reversed during
implementation, not an oversight).

**Original design pass (kept for history):**
Originally raised while reviewing `demo_escape_hatch_workflow.go`'s own
justification (above): its current, still-true reason to exist
(compile-time type safety, no `*events.Client` ceremony, mqtt v3's
non-blocking registration semantics) mirrors `adapters/nethttp`'s OWN
`CallWithHandle`/`ServeOne` existing alongside `api/rest`'s
`Client.Call`/`Attach` for the identical reason — a consistent
pattern, but NOT one the user accepts as permanent. **Directive:
investigate moving this WHOLE tier's "drive" verb onto the API layer,
keeping ONLY a `New*Transport[T]`-shaped factory adapter-owned**
(mirrors `api/events`'s OWN, ALREADY-correct split — see below).

**A follow-up review pass (separate session) corrected a significant
mischaracterization in the ORIGINAL scope statement above: it wrongly
implied reqreply's gap was "the same shape as REST's, just less
investigated." It is NOT. The three APIs' actual difficulty differs
enormously — captured below, resolved per-API:**

- **`api/events` — CONFIRMED already at the target shape, zero work
  needed.** `events.SubscribeHandle`/`PublishHandle` (the "attach and
  drive" verbs) ALREADY live in `api/events` itself. Reading
  `adapters/mqtt5.NewSubscribeTransport[T]`/`NewPublishTransport[T]`'s
  actual bodies confirms they delegate to `subscribeWithHandle[T]`/
  `publishHandle[T]` — FULLY GENERIC, ZERO-REFLECTION, `T` flowing as
  a real compile-time type parameter throughout. Only the factory
  (needs the adapter's own concrete connection type — `MQTTClient`+
  `Router` for mqtt5, `pahomqtt.Client` for mqtt v3, `FramedSocket` for
  zeromq — as a constructor parameter, which `api/events` cannot
  itself supply) stays adapter-owned. This is the reference shape
  Phase 5a brings REST and reqreply to.

- **`api/reqreply` — CONFIRMED a near-trivial, LOW-RISK pure
  relocation, not a redesign.** Reading `mqtt5.Serve[Req,Resp]`/
  `Call[Req,Resp]`'s actual bodies (and `zeromq`'s identical
  counterparts) shows they build `&serverTransport{...}`/
  `&clientTransport{...}` — the EXACT SAME types
  `NewServerTransport(opts)`/`NewClientTransport(opts)` (Phase 4d's
  already-shipped factories) build — and delegate immediately:
  `return t.Serve(ctx, handle, fn)` / `t.Call(ctx, handle, req)`. Their
  own doc comments confirm it: *"Zero duplicate logic — full
  capability parity with AttachClient is therefore automatic."*
  **This also means reqreply's escape hatch was NEVER actually
  reflection-free** — `clientTransport.call`'s internals already
  reflect over the `*RouteHandle[Req,Resp]`'s own monomorphized
  closures (e.g. `elem.FieldByName("EncodeRequest")`) — the same
  established, safe "reflect against already-concrete closures on a
  type-erased handle" idiom used pervasively elsewhere in this
  codebase (not raw/unsafe reflection). Design (resolved):
  ```go
  // api/reqreply — NEW, thin api-layer verbs (pure relocation — the
  // function body IS the adapter's existing Serve/Call body, unchanged)
  func ServeWithTransport[Req, Resp any](ctx context.Context, transport ServerTransport, handle *RouteHandle[Req, Resp], fn func(context.Context, Req) (Resp, error)) error {
      return transport.Serve(ctx, handle, fn)
  }
  func CallWithTransport[Req, Resp any](ctx context.Context, transport ClientTransport, handle *RouteHandle[Req, Resp], req Req, opts ...ClientCallOptions) (Resp, error) {
      var zero Resp
      respAny, err := transport.Call(ctx, handle, req, opts...)
      if err != nil { return zero, err }
      resp, ok := respAny.(Resp)
      if !ok { return zero, TransportTypeMismatchError{...} }
      return resp, nil
  }
  // adapters/mqtt5 + adapters/zeromq — DELETE Serve[Req,Resp]/Call[Req,Resp]
  // entirely; NewServerTransport/NewClientTransport (Phase 4d, already
  // shipped) are the ONLY adapter-owned piece left — matches events' shape.
  ```

- **`api/rest` — CONFIRMED genuinely harder; this was the one real
  open design decision, now resolved by explicit user choice.**
  Reading `CallWithHandle`'s actual body shows it does NOT delegate to
  `rest.ClientTransport` at all — it takes a raw `*http.Client,
  baseURL string` and calls `callWithVars(...)`, nethttp's OWN
  separate, hand-written implementation (param derivation, security,
  network call, decode — all duplicated logic, never shared with
  `rest.ClientTransport.Call`'s reflection-based pipeline). This is a
  material difference from reqreply, not just "less investigated."
  **Decision (user-confirmed): Option A — delegate through the
  EXISTING `rest.ClientTransport` interface**, mirroring how
  reqreply's escape hatch already works, rather than introducing a
  brand-new `rest.CallTransport[Req,Resp]` generic interface (Option
  B, rejected — would have preserved strict zero-reflection but at the
  cost of a second interface every REST adapter must implement/
  maintain, unlike events/reqreply where ONE interface already covers
  both call shapes). Design (resolved):
  ```go
  // api/rest — NEW, thin api-layer verb, delegates through the EXISTING
  // type-erased rest.ClientTransport interface (same shape reqreply uses)
  func CallWithTransport[Req, Resp any](ctx context.Context, transport ClientTransport, handle *RouteHandle[Req, Resp], req Req, opts ...ClientCallOptions) (Resp, error)
  // adapters/nethttp — DELETE CallWithHandle[Req,Resp]; its logic is
  // ALREADY duplicated in rest.ClientTransport's own Call path via
  // NewClientTransport(opts) — no new adapter-side type needed.
  ```
  **This original sketch UNDERSTATED the real scope — see "Implementation
  learnings" below: `ClientCallOptions` had to grow six new fields, and
  `clientTransport.Call` itself (the pre-existing `Client.Attach` path,
  not just the new verb) had four real, previously-undiscovered gaps
  that had to be fixed for the deletion to be truly lossless.**

- **`ServeOne` — RESOLVED: structurally REST-only, cannot be
  generalized. Final decision (reverses the "adopted" recommendation
  below): NOT relocated, stays in `adapters/nethttp` unchanged.**
  `rest.ServerTransport.Serve(ctx) error` (Phase 4d, already shipped)
  already blocks and owns its own `*http.Server`, walking every
  registered route — the direct, already-existing
  cross-API-consistent equivalent of `reqreply.ServerTransport.Serve`/
  `events.Transport.Subscribe`. `ServeOne` is a DIFFERENT, additional
  use case — build a bare `http.Handler` for exactly one route, to
  mount onto a caller-owned, external `*http.ServeMux`/app router — a
  use case with NO structural equivalent in MQTT/ZeroMQ/reqreply
  (there is no "caller-owned external broker to embed a handler into"
  concept for those protocols). Its own return type, `http.Handler`,
  is inherently, unavoidably HTTP-specific — `api/rest` cannot express
  it at all by design (no `net/http` import). A closer look during
  implementation walked back the original "still relocate it"
  recommendation directly below: `ServeOne` was never actually a
  backdoor (it doesn't bypass `rest.ServerTransport`/duplicate logic
  the way `CallWithHandle` risked doing) — it is already exactly where
  it should structurally live. ~~Recommendation (adopted): still
  relocate it to `api/rest` as a REST-only verb, for consistency with
  `CallWithHandle`'s move — it is pure builder-pattern sugar with no
  adapter-specific state (`rest.NewServer`+`Register`+internal `serve`
  are all already callable from `api/rest`+the adapter's own
  `Register` alone).~~

- **`chi` — CONFIRMED out of scope, not symmetric with `nethttp`.**
  `chi` has NO `CallWithHandle` equivalent at all (server-only router
  adapter, no HTTP client concept). `chi.serveOne[Req,Resp]` exists but
  is UNEXPORTED — used only by `chi`'s own test suite, never a real
  caller; its own doc comment states it became *"an internal helper
  now that [AttachRouter]..."* (demoted during Phase 4d, not an
  oversight). **Phase 5a's REST scope is entirely about
  `adapters/nethttp.CallWithHandle`/`ServeOne` — `chi` has nothing to
  migrate.**

- **Real, broad existing usage confirmed — a genuine migration, not a
  toy relocation.** Non-test usage of `nethttp.CallWithHandle`/
  `ServeOne` spans 9+ example directories AND, notably,
  `adapters/mcprest/bridge.go` — a DIFFERENT adapter package depending
  directly on `nethttp.CallWithHandle` to bridge MCP tool calls through
  an outbound REST call. Phase 5a's Implement step must explicitly
  migrate `mcprest` too, not just examples.

- **Sequencing (resolved): Phase 5a runs immediately AFTER Phase 5**
  (this section), which is why it is positioned right here. Phase 5a's
  reqreply relocation touches the SAME `adapters/mqtt5`/`adapters/
  zeromq` reqreply files Phase 5's `Apply`/`ApplyCapabilities` shift
  also touches — running Phase 5 first avoids reworking those files
  twice (the same reasoning that already ordered Phase 4d before 4e in
  this doc). Phase 6 (REST Header/Cookie/Query real-interface work)
  touches different files (`capability.go`/carrier extraction, not
  `client.go`/`serve.go`'s Call/Serve dispatch) — independent of Phase
  5a, can run in parallel or either order.

- **Remaining open items for Phase 5a's Implement step** (naming
  bikeshed only, non-blocking): exact final function names
  (`ServeWithTransport`/`CallWithTransport` above are working names,
  not final); whether `ServeOne`'s relocated name changes to match
  `rest`'s existing verb-naming convention.

**Implementation learnings (this section written after Phase 5a
shipped — reconciles the design sketch above with what was actually
built):**

- **`reqreply` was exactly as easy as designed — a provably lossless,
  mechanical relocation.** `NewServerTransport`/`NewClientTransport`
  (both adapters) already nested the full rich options type the
  deleted escape hatches took directly; zero fields were lost. The one
  real (non-test) call site (`examples/reqreply-api`) and ~80 test
  call sites across both adapters were migrated using a **reusable
  test-shim pattern**: define a test-only function with the OLD
  signature that internally builds the new transport and delegates,
  then do a precise, method-call-excluding regex rename
  (`(?<![\w.])FuncName\(`) across every test file — avoids rewriting
  every call site's argument list by hand.
- **`api/rest.ClientCallOptions` had to grow FAR beyond the two-field
  sketch above.** Investigating `nethttp.CallWithHandle`'s actual
  richness (not just its signature) surfaced that its own
  `nethttp.CallOptions` carried six fields `ClientCallOptions` lacked:
  explicit per-call `QueryParams`/`CookieParams`/`HeaderParams`
  override maps, `ExtraHeaders` (arbitrary undeclared headers),
  `OnCredentialRejected` (401 notification hook), and a per-call
  `Observer` override. All six were added to `ClientCallOptions`.
  `ExtraHeaders` could NOT be typed `http.Header` (`api/rest` may not
  import `net/http`, per `.github/instructions/go-codex.instructions.md`)
  — it is `map[string][]string` instead, which is `http.Header`'s
  exact underlying layout; `nethttp` converts it back via a direct
  type conversion at the request-build boundary. `Observer
  stats.Observer` introduced no NEW import-rule violation — `api/rest`
  already imports `stats` elsewhere (`observability.go`,
  `transform_dispatch.go`).
- **The biggest, previously-unknown finding: `clientTransport.Call`
  itself — i.e. the EXISTING, "blessed" `Client.Attach`+`Client.Call`
  workflow, not just the new escape-hatch replacement — had FOUR real,
  pre-existing gaps relative to `callWithVars`/`CallWithHandle`.**
  These were never exercised by `Client.Call`'s own test suite because
  nothing had ever compared the two paths field-by-field before. Only
  discovered by literally porting a representative sample of
  `CallWithHandle`'s existing tests onto `CallWithTransport` and
  running them (per this skill's "Removing an old API" checklist —
  reflection-dispatcher equivalence is a hypothesis until verified by
  migration, not by code review). All four were fixed directly in
  `clientTransport.Call`, benefiting `Client.Attach` too, not just the
  new verb:
  1. Query/Cookie/Header codec validation (`ValidateQuery`/
     `ValidateCookies`/`ValidateHeaders`) was never called.
  2. Implementation-shape eager validation was missing (new
     `validateClientImplementationShapesReflect` helper added).
  3. Response header/cookie merge-field decoding was missing — no
     reflection-callable way existed to merge into an already-decoded
     value, so a new exported method, `RouteHandle.
     ApplyResponseMergeFields(resp *Resp, headers, cookies
     map[string]string) error`, was added to `api/rest` (mirrors the
     existing request-side `ApplyMergeFields`).
  4. `stats.WithDiagnostics`/`DiagnosticsFromContext` draining was
     missing.
  - **One gap was left as an accepted, documented limitation, not
    fixed:** `rest.ClientTransform` dispatch. Confirmed via repo-wide
    grep that ZERO real (non-test) `CallWithHandle` callers ever used
    it, and that `Client.Attach`+`Client.Consume` has the IDENTICAL,
    symmetric gap already (unaffected by this phase) — closing it was
    out of scope for a "lossless replacement of `CallWithHandle`"
    goal, since `CallWithHandle` itself never supported it either.
- **`clientTransport.Call`'s reflection dispatch also had to accept a
  bare `*rest.RouteHandle[Req,Resp]`, not just `rest.Route[Req,Resp]`**
  — confirmed via reading its actual type check
  (`!strings.HasPrefix(rv.Type().Name(), "Route[")`, hard-rejecting a
  bare handle). A new `recoverClientRouteHandleValue` helper (mirrors
  reqreply's `recoverRouteHandleValue`) fixed this — required for
  `mcprest`/`binding.go`'s bare-handle callers to work at all.
- **Blast radius was confirmed narrow, not "every REST adapter":**
  `chi` has zero `rest.ClientTransport` implementation at all
  (server-only router) — `nethttp` was the only adapter needing the
  reflection-dispatch work.
- **Real callers migrated, beyond the 9+ examples originally
  estimated:** `adapters/mcprest/bridge.go`'s OWN public API
  (`ToolHandler`/`MappedToolHandler`) took a `nethttp.CallOptions`
  parameter — migrated to `rest.ClientCallOptions`, decoupling
  `mcprest` from a specific adapter's option type as a side benefit.
  `adapters/nethttp/binding.go`'s `ports.IOAdapter` REST-call binding
  (`nethttpCallAdapter`) also called `CallWithHandle` internally —
  `CallStreamOptions`/`DrainCallOptions`'s public `CallOpts` field type
  changed from `nethttp.CallOptions` to `rest.ClientCallOptions`
  accordingly (a real, but 1:1 field-mapping-preserving, breaking
  change for that binding's own callers).

### Phase 6 — `api/rest`: the SAME shift for Header/Cookie/Query

**Status: SHIPPED.** Promotes `HeaderCapableTransport`/
`CookieCapableTransport`/`QueryCapableTransport` from Pattern C's
no-op markers (`SupportsHeaderParams()` etc. — presence-only, used
SOLELY for the Attach-time `CheckParamKindCoverage` check, never
called at request time) to REAL, callable `Extract`-shaped methods —
directly answering the earlier open question ("capabilities are just
zero-cost markers... I want it a programming interface an adapter can
be implemented against").

**Review pass (separate session, before implementation started) found
the inventory below materially incomplete — corrected here, both
open decisions RESOLVED:**

- **Corrected inventory: the real count is 36 individual
  `queryValues(r)`/`cookieValues(r)`/`headerValues(r)` call sites, not
  "~16" as originally estimated** (counted via direct grep across all 6
  files, not re-derived from the block-count reasoning below).
  Breakdown: `nethttp/adapter.go`=12, `nethttp/serve.go`=3,
  `nethttp/serve_sse.go`=3, `chi/adapter.go`=12, `chi/serve.go`=3,
  `chi/serve_sse.go`=3. The root cause of the undercount:
  `adapter.go`'s 2 non-reflection dispatch blocks (one plain, one SSE,
  in EACH adapter) each call `queryValues(r)`/`cookieValues(r)`/
  `headerValues(r)` TWICE per request — once for
  `ValidateQuery`/`ValidateCookies`/`ValidateHeaders`, then AGAIN later
  to build the merge-field vars map consumed by `codex.DecodeVars`
  (plain) / `handle.MergeEvent` (SSE, captured once per connection via
  closure, reused per-event-send). `serve.go`/`serve_sse.go`'s
  reflection-based dispatch already extracts once into
  `queryVars`/`headerVars`/`cookieVars` locals and reuses them for both
  validation and merge — no double extraction there. The "8 files
  touched" count is unaffected and accurate (`adapter.go`/`serve.go`/
  `serve_sse.go`/`capability.go` × 2 adapters).
- **Decision A — RESOLVED: fix the double extraction, not preserve
  it.** Since Phase 6 touches every one of these call sites anyway
  (renaming the extraction call), it will ALSO construct exactly ONE
  carrier value per request-dispatch invocation and reuse its
  extracted maps for BOTH the validation call and the merge-field
  vars — eliminating the redundant re-parsing (repeated
  `r.URL.Query()`/header-map-copy/cookie-jar-walk work) in
  `adapter.go`'s 2 blocks × 2 adapters as a bonus fix bundled into the
  migration, not a separate phase (the fix is mechanically identical
  work to the rename itself — capture the extracted map into a local
  once, consumed by both call sites, instead of calling `Extract*()`
  twice).
- **Decision B — RESOLVED: widen scope, but as a SEPARATE new phase, not
  folded into this one.** `adapters/websocket/binding.go` was found to
  have its own, previously undocumented, near-identical duplicate of
  this exact mechanism: private `queryValues`/`headerValues` functions
  (not `nethttp`'s — a THIRD, package-local copy), calling
  `route.ValidateQuery`/`ValidateHeaders` against an inline interface
  literal satisfied by `ports.Socket.Route`'s concrete type,
  `*rest.RouteHandle[struct{}, struct{}]` — the SAME Tier-2 REST
  validation this whole roadmap is built around, just never wired into
  `HeaderCapableTransport`/`CookieCapableTransport`/
  `QueryCapableTransport`/`CheckParamKindCoverage` at all, and with NO
  cookie support (upgrade requests can carry cookies too). This is
  material enough (a genuinely separate package, its own carrier type,
  a real API signature change on 3 exported constructors) to warrant
  its own phase rather than scope-creeping Phase 6 — see the new
  **Phase 6a** below, which also bundles in a related, also
  previously-undocumented gap: `serve_sse.go` (both `nethttp` AND
  `chi`) never calls `CheckParamKindCoverage` for SSE routes at all
  (currently harmless, since `nethttp`/`chi` always satisfy every kind
  — but a real gap for any future SSE-capable adapter that doesn't).
- **Confirmed out of scope, explicitly recorded so it is not later
  mistaken for an oversight**: `credentialExtractorFor(r)`
  (`adapters/nethttp`/`adapters/chi`'s security-credential extraction)
  uses raw single-key `r.Header.Get`/`r.URL.Query().Get`/`r.Cookie`
  lookups — structurally different from bulk-map extraction (it never
  builds a full map), so it does not naturally map onto
  `Extract*() map[string]string` and stays untouched.
  `pathValues`/`responseHeaderValues`/`responseCookieValues` remain
  out of scope too, as already stated below (path vars have no
  capability-coverage question; response-header/cookie validation
  reads a server's OWN already-built outgoing values, no incoming
  carrier involved).
- **Scope is SERVER-side extraction ONLY — client-side is confirmed
  OUT of scope, not silently forgotten.** `adapters/nethttp/client.go`'s
  `ValidateQuery(opts.QueryParams)`/`ValidateCookies(opts.CookieParams)`/
  `ValidateHeaders(opts.HeaderParams)` and `clienttransport.go`'s
  `EncodeQueryVars`/`EncodeHeaderVars`/`EncodeCookieVars` calls both
  operate on a caller-SUPPLIED map or an ENCODED-FROM-Req value — there
  is no incoming carrier to extract FROM on the client side (the client
  BUILDS the outgoing request, it doesn't read one), so `Extract`-shaped
  methods have no client-side counterpart to unify.
- **Response-header validation (`ValidateResponseHeaders`) is ALSO
  confirmed OUT of scope** — a server validates its OWN
  already-built response headers before writing them; there is no
  "does this transport support extracting response headers" question
  (a server can always read the response headers IT is about to write),
  so this axis has no coverage/capability question to answer at all.
- **REJECTED: a single generic `rest.ApplyRequestParams`-style
  dispatcher swallowing extract+validate+error-response in one call**
  (the ORIGINAL scope statement's proposed shape, before this
  inventory). Reading all 4 call-site shapes closely shows each one's
  error-response ceremony is genuinely, irreducibly different per call
  site — different HTTP status semantics are NOT the variable (all 4
  use `http.StatusBadRequest`), but the SURROUNDING dispatch is: two
  call sites consult a declared `ErrorPattern` via
  `tryRespondErrorPatternGeneric`/`tryRespondErrorPattern` (one
  reflection-based, one generic-based) before falling back to
  `errFn`/`opts.ErrorHandler`, while the plain-non-reflection SSE block
  skips `ErrorPattern` entirely; `rest.ReportQueryErrors`/
  `ReportCookieErrors`/`ReportHeaderErrors` differ per param KIND (not
  per call site) and must stay separately callable so a caller's
  `stats.Observer` can distinguish which kind failed. Forcing ALL of
  this through one generic callback-laden function would trade a small
  amount of extraction duplication for a much LARGER, uglier
  configuration-object surface — a worse trade than what Phase 4's
  `ApplyCapabilities` made (there, the "surrounding ceremony" was
  uniform: `WireAttributes`/`FramedSocket` mutation plus one
  `RecordCapabilityApplied` call, identical at every call site — NOT
  true here). **Resolved, narrower scope: unify ONLY the raw
  EXTRACTION step** (turning `queryValues(r)`/`cookieValues(r)`/
  `headerValues(r)` into real interface method calls on a per-request
  carrier), leaving each call site's own validate+error-response
  ceremony exactly as-is, calling `handle.ValidateQuery`/
  `ValidateCookies`/`ValidateHeaders` with the carrier's extracted map
  the SAME way it calls them with `queryValues(r)`'s map today — a
  drop-in, byte-identical-output replacement, not a dispatch redesign.
- **Concrete new interface shape** (in `api/rest/capability.go`,
  replacing the existing no-op declarations — a breaking rename,
  consistent with this whole roadmap's "breaking changes are
  acceptable" stance):
  ```go
  type HeaderCapableTransport interface{ ExtractHeaders() map[string]string }
  type CookieCapableTransport interface{ ExtractCookies() map[string]string }
  type QueryCapableTransport interface {
      ExtractQuery() map[string]string        // first-value-wins, mirrors ValidateQuery
      ExtractQueryMulti() map[string][]string  // mirrors ValidateQueryMulti
  }
  ```
  `QueryCapableTransport` gains TWO methods (not one) because
  `opts.MultiValueQueryParams` already toggles between
  `ValidateQuery(queryValues(r))` (first-value-wins) and
  `ValidateQueryMulti(r.URL.Query())` (full multi-value) at every
  existing call site — both forms must remain reachable through the
  carrier, matching the existing toggle exactly, not silently
  collapsing to one shape.
- **`httpCarrier{r *http.Request}`** — a NEW, per-adapter (one in
  `adapters/nethttp`, one in `adapters/chi`, byte-identical shape,
  mirrors `transportCapabilities{}`'s existing per-adapter duplication
  precedent) per-REQUEST value (unlike the OLD `transportCapabilities{}`
  singleton, which held no data — the whole point of a "real" interface
  is that it wraps genuine per-request state) implementing all 3
  interfaces by moving `queryValues`/`cookieValues`/`headerValues`'s
  EXISTING logic verbatim into `ExtractQuery`/`ExtractCookies`/
  `ExtractHeaders` methods, plus a trivial `ExtractQueryMulti() map[string][]string { return c.r.URL.Query() }`. The 3 old private
  helper functions are DELETED (their logic didn't change, only its
  home — method on a carrier type instead of a free function).
- **`CheckParamKindCoverage`'s Attach-time coverage check is
  UNCHANGED and stays SAFE** — confirmed via re-reading its
  implementation: it performs ONLY a `transport.(HeaderCapableTransport)`
  TYPE ASSERTION, never invokes the method — so the existing
  package-level zero-value `httpTransport = transportCapabilities{}`
  singleton pattern is replaced by a zero-value `httpTransport =
  httpCarrier{}` (nil `*http.Request` field) used SOLELY for that one
  Attach-time type-assertion call; it is NEVER used for real
  extraction (each per-request dispatch constructs its OWN
  `httpCarrier{r}` from the real, live `*http.Request`). No nil-pointer
  risk: the zero-value carrier's methods are never called, only
  type-asserted against.
- **Every one of the 36 call sites' extraction line changes from**
  `queryValues(r)`/`cookieValues(r)`/`headerValues(r)`/`r.URL.Query()`
  **to** `httpCarrier{r}.ExtractQuery()`/`.ExtractCookies()`/
  `.ExtractHeaders()`/`.ExtractQueryMulti()` **— a mechanical rename for
  the 12 `serve.go`/`serve_sse.go` call sites (3 each × 4 files,
  behavior-identical, no logic change), and a rename PLUS the
  Decision-A de-duplication fix for the 24 `adapter.go` call sites (12
  each × 2 adapters, each of the 2 blocks per adapter collapsing from 2
  extractions down to 1 reused carrier — see Decision A above).**
  `pathValues`/`responseHeaderValues` are NOT touched (confirmed out of
  scope above).

**Implementation risk, stated explicitly before starting:** this phase
touches 8 files of ALREADY-SHIPPED, heavily-tested, core REST
request-dispatch code (`adapters/nethttp`/`adapters/chi`'s
`adapter.go`/`serve.go`/`serve_sse.go`) across 36 individual call
sites (corrected count — see the review pass above) — the largest
surface area any single phase in this roadmap has touched in
already-working dispatch code. Per the `plan-a-new-codex-
feature` skill's "Removing an old API" checklist (enumerate every
responsibility, verify a representative sample by migration not
review, sweep for doc references), this warrants explicit user
confirmation before implementation begins, not autonomous continuation
— unlike Phase 4e's mechanical, additive, strictly-non-breaking
signature changes, this phase's `queryValues`/`cookieValues`/
`headerValues` DELETION is a genuine "old API removal" against
production dispatch code with real regression risk if any one of the
36 call sites is migrated incorrectly. Mitigating factor found during
the review pass: 17 existing test files across both adapters already
exercise Query/Cookie/HeaderParam behavior via real HTTP
request/response assertions (not by calling the private functions
directly — none do) — a reasonable regression safety net for what is,
underneath, a pure internal refactor.

**Learnings (implementation matched the design exactly, zero
surprises):** the mitigating factor above held — the full pre-existing
`adapters/nethttp`/`adapters/chi`/`api/rest` test suites passed
unchanged after the migration, confirming the 36-call-site rename +
Decision-A de-duplication was genuinely behavior-identical. The
`net/http.Query()`-called-twice-per-request inefficiency Decision A
targeted is now gone from both adapters' non-reflection dispatch
blocks. `CheckParamKindCoverage`'s Attach-time type-assertion against
the zero-value `httpCarrier{}` needed zero changes, exactly as
predicted. Full verification (`gofmt`/`build`/`vet`/`test`/`just
check`/all examples) passed clean on the first attempt.

**Status: SHIPPED.**

### Phase 6a — closing the SSE `CheckParamKindCoverage` gap, and bringing `adapters/websocket` into the same real-interface mechanism

**Status: SHIPPED.** Split out of Phase
6's Decision B (above) per explicit user direction, rather than
scope-creeping Phase 6 itself — this phase is INDEPENDENT of Phase 6's
own implementation (it can run before, after, or interleaved, since it
touches entirely different files: `serve_sse.go`'s registration path
and all of `adapters/websocket`), though it reuses Phase 6's
`HeaderCapableTransport`/`CookieCapableTransport`/`QueryCapableTransport`
interface shapes once those are real (so in PRACTICE it should follow
Phase 6, not precede it — Phase 6 has since SHIPPED, so this
precondition is now satisfied).

**A follow-up review pass (separate session, after Phase 6 shipped)
found 2 real gaps in the design below and resolved both — see "Gap A"
and "Gap B" inline where each finding's fix is described, plus the
corrected "Implementation risk" paragraph at the end of this section.**

**Two independent findings, bundled into one phase because both are
"coverage/capability-check completeness" gaps found during the same
review pass:**

- **Finding 1 — `serve_sse.go` never calls `CheckParamKindCoverage` at
  all, in EITHER adapter.** `serve.go`'s reflection-dispatch route
  builder calls `rest.CheckParamKindCoverage("nethttp"/"chi",
  requiredKinds, httpTransport)` once per route at build time (verified
  — this is the ONLY call site of `CheckParamKindCoverage` in either
  adapter's non-test code). `serve_sse.go`'s equivalent SSE route
  builder has no such call — an SSE route declaring Header/Cookie/Query
  params gets ZERO coverage verification. Currently harmless (`nethttp`/
  `chi` both trivially satisfy every kind via `httpCarrier{}`, so the
  check can never fail for them today), but a real, silent gap for any
  future SSE-capable adapter that doesn't support one of these kinds —
  it would proceed without error instead of failing fast at
  registration time the way a REST route on the same adapter would.
  **Fix**: add the identical `CheckParamKindCoverage` call to
  `serve_sse.go`'s route-registration path, in both adapters, deriving
  `requiredKinds` from the SSE handle's own
  `HeaderParamNames`/`CookieParamNames`/`QueryParamNames`/
  `SecuritySchemes`.
  **Review-pass correction (Gap A — the ORIGINAL text above wrongly
  claimed these 3 accessors were "confirmed present on
  `*rest.SSERouteHandle` too"; verified FALSE via direct code reading):
  only `PathParamNames()` currently exists on `*rest.SSERouteHandle` —
  `HeaderParamNames`/`CookieParamNames`/`QueryParamNames` exist ONLY on
  `*rest.RouteHandle` today.** `SSERouteHandle` already has the
  BACKING unexported fields (`headerParams`/`cookieParams`/
  `queryParams`) — it just never got the 3 public accessor methods.
  **New prerequisite step, folded into this phase**: add
  `HeaderParamNames`/`CookieParamNames`/`QueryParamNames` methods to
  `*rest.SSERouteHandle` in `api/rest/builder.go`, mirroring
  `RouteHandle`'s identical methods' body exactly (same
  field-to-name-slice shape) — a small, low-risk, purely additive
  `api/rest` change Finding 1's `serve_sse.go` fix now explicitly
  depends on.
- **Finding 2 — `adapters/websocket/binding.go` independently
  reimplements this whole mechanism, undocumented anywhere in this
  roadmap, with 2 real gaps of its own.** Structure (confirmed via
  reading the code): `upgradeAndValidate` (ONE shared function) is
  called from all 3 exported constructors
  (`IngestSocketAdapter`/`BroadcastSocketAdapter`/`DuplexSocketAdapter`
  — `wsIngestAdapter`/`wsBroadcastAdapter`/`wsDuplexAdapter`'s
  `Activate` methods). It takes an inline interface literal
  (`{ValidatePathParams(...); ValidateQuery(...); ValidateHeaders(...)}`)
  satisfied by `ports.Socket.Route`'s concrete type,
  `*rest.RouteHandle[struct{}, struct{}]` — i.e. this IS the same
  Tier-2 REST validation mechanism, just reached via a hand-rolled
  `Socket` port type rather than `api/rest`'s own `Builder`/`Attach`
  flow. Its own private `queryValues`/`headerValues` (package-local,
  NOT reusing `nethttp`'s — Go disallows cross-package unexported
  reuse) extract ONCE and correctly reuse the same maps for both
  validation and the `vars` merge (no Decision-A-style double
  extraction here — this package never had that bug). Two real gaps:
  1. **No cookie support at all** — `upgradeAndValidate` extracts/
     validates query and headers but never cookies, even though a
     WebSocket upgrade request (an ordinary HTTP GET) can carry
     cookies exactly like any REST request. `*rest.RouteHandle`
     already has `ValidateCookies` — this is a real, addressable gap,
     not a protocol limitation.
  2. **Never wired into `HeaderCapableTransport`/`QueryCapableTransport`/
     `CheckParamKindCoverage` at all** — `adapters/websocket` does not
     implement any of the 3 capable-transport interfaces, so a
     websocket route declaring Header/Query/Cookie params gets NO
     Attach-time (construction-time, for this adapter)
     coverage-check protection — the same class of silent gap as
     Finding 1, just never given the mechanism to begin with rather
     than having it and skipping one call site.
  **Fix**:
  - New `adapters/websocket/capability.go`: a `wsCarrier{r *http.Request}`
    type implementing all 3 real interfaces (Phase 6's shape),
    mirroring `httpCarrier`'s structure — package-local, per this
    roadmap's established per-adapter-duplication precedent (same
    reasoning as `Capability`/`WireAttributes` being separate types per
    MQTT adapter).
  - Add `ValidateCookies(map[string]string) error` to
    `upgradeAndValidate`'s inline route-interface parameter; extract
    via `wsCarrier{r}.ExtractCookies()`; validate; merge into `vars`
    alongside query/header (identical pattern to the existing two).
  - Wire `rest.CheckParamKindCoverage("websocket", requiredKinds,
    wsCarrier{})` into all 3 constructors
    (`IngestSocketAdapter`/`BroadcastSocketAdapter`/`DuplexSocketAdapter`)
    at construction time — mirroring nethttp/chi's Attach-time
    placement, since these constructors ARE this adapter's
    "Attach"-equivalent moment. `requiredKinds` derives from
    `handle.Route.HeaderParamNames()`/`CookieParamNames()`/
    `QueryParamNames()`/`SecuritySchemes` (all already present on
    `*rest.RouteHandle`, zero `api/rest` changes needed for this part).
  - **Confirmed, deliberate breaking change**: all 3 constructors gain
    an `error` return (they currently return only the adapter value) —
    every real caller needs migrating to handle it. Consistent with
    this roadmap's established "breaking changes are acceptable, call
    them out explicitly" stance.
  - Migrate `adapters/websocket`'s own private `queryValues`/
    `headerValues` onto `wsCarrier`, delete the free functions.

**Review-pass correction (Gap B — the ORIGINAL text above materially
understated the caller-migration surface as "expected small: 2
examples + this package's own test suite"; a direct repo-wide grep
before starting found the real count, mirroring the SAME kind of
undercount Phase 6's own review pass found for ITS call-site
inventory):**

- **Real total: 30 call sites**, not "2 examples + this package's own
  test suite" — `adapters/websocket/binding_test.go` alone has **21**
  (including one godoc `ExampleDuplexSocketAdapter` function, whose
  inline `port.Bind(ctx, adapterws.DuplexSocketAdapter(...))` call
  needs restructuring into an idiomatic 2-statement form, not just a
  mechanical `_ = err`, since it is user-facing documentation);
  `adapters/websocket/client_test.go` has 1; `adapters/chi/socket_test.go`
  has 1; `examples/websocket-duplex/main.go` and
  `examples/websocket-client/main.go` have 1 each.
- **Previously unmentioned cascade, found only by grepping for the
  constructor names repo-wide rather than assuming the blast radius
  stopped at `adapters/websocket`**: `adapters/chi/socket.go`'s OWN 3
  wrapper constructors (`chi.IngestSocketAdapter`/
  `chi.BroadcastSocketAdapter`/`chi.DuplexSocketAdapter` — the chi
  variants documented as mirroring `adapters/websocket`'s exactly)
  directly embed `websocket.XSocketAdapter(...)`'s return value as a
  struct-literal field (e.g. `SourceAdapter:
  websocket.IngestSocketAdapter(...)`) — a 2-value return cannot be
  used as a single struct-literal field initializer, so `chi`'s own 3
  wrapper constructors MUST ALSO gain the identical `(adapter, error)`
  signature change, cascading one package further than the original
  scope described. `chi`'s wrappers have ZERO external callers today
  (confirmed via repo-wide grep) beyond their own doc comments and
  `socket_test.go`'s 1 call site (already counted above), so this
  cascade's OWN migration cost is small — but the signature change
  itself must still happen, and was completely unmentioned before this
  review pass.
- **Open architectural alternative surfaced and put to the user, then
  resolved**: instead of changing all 3 constructors' signatures, run
  `CheckParamKindCoverage` INSIDE `Activate` and report a failure via
  the adapter's already-existing async `errs chan<- error` parameter
  (`ports.SourceAdapter.Activate(ctx, dst, errs)` already has this
  channel — zero constructor signature change, zero caller migration
  anywhere would have been needed). **Rejected in favor of keeping the
  constructor-returns-error design** (user decision) — the
  `Activate`-based alternative's fail-fast guarantee is materially
  WEAKER than nethttp/chi's (asynchronous, discovered only once
  `Activate` actually runs, and only if the caller is draining the
  `errs` channel — vs. nethttp/chi's guaranteed-synchronous,
  before-anything-starts failure). Preserving PARITY with nethttp/chi's
  existing guarantee was judged more valuable than avoiding the larger
  (but still entirely mechanical, no-new-decisions-per-site) migration.

**Implementation risk**: Finding 1 is a 2-line addition per adapter (no
behavior change for existing routes), now ALSO requiring the new Gap A
`SSERouteHandle` accessor-method prerequisite (small, additive,
low-risk). Finding 2 touches one already-small file (`binding.go`,
~690 lines) plus one new file, with the corrected, now-precise real
risk being the constructor signature change's caller-migration
surface: 30 call sites in `adapters/websocket`/its examples/tests, PLUS
`adapters/chi/socket.go`'s own 3 wrapper constructors needing the
identical signature change cascaded through. Still smaller than Phase
6's 36-call-site surface, and every one of these ~34 sites is
mechanical (thread through the already-typed `error`, zero per-site
decisions) — but the corrected count is meaningfully larger than
originally estimated, so treat this phase with the SAME migration-care
discipline Phase 6 required, not as the "smaller, more contained"
phase the pre-review-pass text characterized it as.

**Learnings (implementation matched the corrected design exactly, one
small positive discovery beyond it):** the corrected 30+/~34-call-site
estimate held — every call site was a mechanical wrap, zero behavior
changes needed at any of them (every existing route/handle already
satisfies the coverage check, so `codex.Must` never panics in any
pre-existing test/example). One thing NOT anticipated in the design:
rather than inventing a new test-only "unwrap or fail" helper for the
~30 call-site migration, the existing library-wide `codex.Must[T
any](v T, err error) T` (already imported/used in most of the touched
files for unrelated `PluginSocketPattern`/etc. calls) was reused
directly — simpler than a bespoke helper, and consistent with how the
2 real examples already handled other fallible constructors. A new
`checkSocketParamKindCoverage` helper in `adapters/websocket/
capability.go` was added (not originally spelled out in the design) to
avoid tripling the same 4-line coverage-check block across all 3
constructors — a small, uncontroversial addition. Full verification
(`gofmt`/`build`/`vet`/`test`/`just check`/all examples, including both
`websocket-duplex`/`websocket-client` explicitly) passed clean on the
first attempt.

### Phase 7 — Observer + ErrorPattern as interface-level cross-cutting concerns

**Status: SHIPPED (ErrorPattern half); Observer half formally CLOSED,
no code needed.** Raised while
reviewing this doc's own Phase 8 (below) closing todo: "check in every
api and its adapter the observer pattern integration and the error
pattern integration, with the goal to have a thin adapter and the
observation/error handling inside the API layer, with interfaces
adapters implement against." Phases 4d/4e/6/6a already promoted
`ClientTransport`/`ServerTransport`/`Transport` (and
`HeaderCapableTransport`/`CookieCapableTransport`/
`QueryCapableTransport`) from ad-hoc adapter wiring to REAL, declared
interfaces every adapter implements and attaches via `Client.Attach`/
`Server.Attach`. This phase asks whether that same treatment should
extend to Observer instrumentation and ErrorPattern dispatch — both
were investigated in DEPTH across two separate review passes; ONE
(ErrorPattern) reached a committed, interface-based design matching
the stated goal exactly; the OTHER (Observer) was found to be a
genuine structural constraint, not a gap, and is formally CLOSED below
rather than left open.

**Current-state findings (confirmed via direct code investigation across two review passes, not assumed):**

- **Observer outcome-recording is 100% adapter-owned — deliberately,
  by existing design.** Counting every `obs.Record*`/`Observer.Record*`
  call site repo-wide (excluding tests): `adapters/mqtt5`=94,
  `adapters/zeromq`=93, `adapters/nethttp`=70, `adapters/mqtt` (v3)=46,
  `adapters/websocket`=26, `adapters/mcpgo`=15, `adapters/openai`=12,
  `adapters/sql`=8, `adapters/chi`=4, vs only `api/reqreply`=4 and
  `api/events`=4 — and those last 8 are `RecordValidationError` calls
  inside their shared `Observability[Req,Resp]`/`Observability[T]`
  decorators (diagnostics-draining only), NOT outcome recording
  (`RecordRequest`/`RecordPublish`/`RecordSubscribe`). Reading
  `api/reqreply/observability.go`'s own doc comment confirms this is
  intentional: *"Deliberately does NOT itself call
  `stats.Observer.RecordRequest` or start a `stats.TraceObserver`
  span — every reqreply adapter transport ... ALREADY calls
  `RecordRequest` ... unconditionally on every code path, so doing so
  again here would double-count/duplicate those events."* The
  underlying reason this is correct, not merely convenient: only the
  adapter's transport dispatch code has access to the REAL outcome
  data (an HTTP status code, a QoS-acked publish, a ZeroMQ reply
  frame) and the REAL duration measured around the actual network
  round-trip — a generic, api-layer-owned wrapper sitting "above" the
  adapter can at best observe `error != nil`, strictly less granular
  than what adapters record today.
- **ErrorPattern MATCHING is already api-layer-owned; only wire-WRITING
  is adapter-owned — and even that is smaller than first estimated.**
  `RouteHandle.ObserveErrorResponseFor` (in `api/rest`) already
  performs the `errors.As` match against declared `ErrorPattern` rules
  AND reports the outcome to `stats.ErrorPatternObserver` —
  centralized, confirmed via `api/rest/transform_dispatch.go`'s
  `CallObserveErrorResponseFor` (the reflection-based caller uses this
  SAME centralized function too, not a separate copy). This means
  every adapter-side function touching ErrorPattern dispatch has
  ALREADY delegated the hard part (matching) to `api/*` — what
  remains, everywhere, is JUST the final protocol-specific step:
  encoding the matched response onto the wire (HTTP
  status+body+headers+cookies for REST; a declared topic publish for
  pub/sub; a broadcast for duplex/broadcast sockets).
- **Corrected duplication inventory (a SECOND review pass found 2 MORE
  byte-identical/near-identical functions the first pass missed):**
  Between `adapters/nethttp` and `adapters/chi`, **5 functions** are
  duplicated, not 3:
  1. `tryRespondErrorPatternGeneric[Req, Resp any](...)` — byte-identical.
  2. `writeErrorPatternResponse[Req, Resp any](...)` — byte-identical.
  3. `tryRespondErrorPattern(...)` (reflection-dispatch variant,
     `serve.go`) — byte-identical.
  4. **`writeErrorPatternResponseReflect(...)`** (the reflection
     variant's OWN response-writer, also in `serve.go`) —
     byte-identical, confirmed via direct diff — MISSED by the first
     review pass entirely.
  5. **`SetCookie(w http.ResponseWriter, name, value string, opts
     CookieOptions) error`** — identical signature, near-identical body
     (differs only in comment wording, confirmed via diff) — also
     MISSED by the first pass.
  Both packages are net/http-family (chi is a router ON TOP of
  `net/http`, using the identical `*http.Request`/`http.ResponseWriter`
  types) and both define a structurally identical `PendingCookie{Name,
  Value, Opts CookieOptions}` type (fields confirmed identical via
  diff — only doc-comment wording differs). This mirrors the existing
  precedent already established for `adapters/internal/httpsecurity`
  (net/http-family-only sharing). `adapters/mqtt5`/`adapters/zeromq`
  have NO equivalent finding — their ErrorPattern wire-realization
  genuinely differs by protocol shape (declared topic publish vs.
  broadcast), so there is no byte-identical duplication to centralize
  there, and this phase's ErrorPattern work stays REST-scoped
  (`nethttp`/`chi` only), matching Phase 6's own scope precedent.

**Committed design — ErrorPattern: a real `ErrorResponseWriter` interface, not just shared code**

Because ALL 5 duplicated functions' remaining logic (after the
already-centralized match step) is JUST the header/cookie/status/body
WRITE onto `http.ResponseWriter`, this is small enough to express as
ONE new interface method — achieving the user's stated goal directly
(a real interface adapters implement against, full responsibility
inside `api/rest`, adapters reduced to a thin write-only
implementation) rather than merely relocating duplicate code into a
shared internal package:

```go
// api/rest — sketch; exact field names/types finalized at implementation time.
type CookiePayload struct {
    Name, Value string
    Opts        CookieOptions // generalizes nethttp/chi's already-identical CookieOptions
}

// ErrorResponseWriter is an OPTIONAL, type-asserted interface (mirrors
// Phase 6's HeaderCapableTransport precedent — NOT a required method on
// ServerTransport, so this is additive, not a breaking interface
// change) an adapter's response-writer implements to realize a matched
// ErrorPattern onto the wire.
type ErrorResponseWriter interface {
    WriteErrorResponse(headers map[string][]string, cookies []CookiePayload, status int, body []byte) error
}

// api/rest — NEW orchestrating function owning the FULL
// match→validate→encode→write loop end to end (both the generic and
// reflection-dispatch callers use this ONE function going forward —
// no more separate Generic/Reflect pairs).
func DispatchErrorPattern(ctx context.Context, obs stats.Observer, handle any, w ErrorResponseWriter, err error) (handled bool, updatedErr error)
```

`adapters/nethttp`/`adapters/chi` each implement ONE ~10-line
`WriteErrorResponse` method (the actual `w.Header().Add`/`SetCookie`/
`w.WriteHeader`/`w.Write` calls) — genuinely thin, not just
deduplicated. **All 5 duplicated functions get DELETED outright** in
both adapters (not relocated to a shared internal package) once
`DispatchErrorPattern` replaces every one of their call sites — this
is the key difference from a narrower "just share the code" approach:
the adapter's OWN exported/internal surface shrinks, rather than
staying the same size but pointing at shared code.

**Formally CLOSED — Observer (no further exploration planned):** a
generic, api-layer-owned decorator wrapping `Call`/`Serve` to call
`obs.RecordRequest` centrally was explored and found to have a real,
unavoidable structural cost: `RecordRequest`'s existing,
adapter-recorded status/outcome granularity (an actual HTTP status
code; a real QoS ack; a ZeroMQ reply-presence signal) is NOT
recoverable from a generic wrapper that only sees `error`/`nil` around
an opaque `Call`/`Serve` invocation, unless `ClientTransport`/
`ServerTransport`'s signatures themselves changed to return a
structured outcome value — a materially invasive change across EVERY
adapter, with real risk of losing today's per-protocol granularity
(HTTP status codes vs. MQTT QoS/ack vs. ZeroMQ reply presence are not
obviously unifiable into one small struct without lossy compromise).
**Closing condition, explicitly checked before closing (not assumed):
is the Observer API already clear enough, per-adapter and per-API, that
adapter-ownership here isn't itself a documentation gap masquerading
as an architecture gap?** Verified YES — `docs/features/observer.md`
already has a "Per-layer behavior" table stating plainly that
`RecordRequest`/`RecordSubscribe`/`RecordPublish` are adapter-owned,
PLUS three dedicated, detailed sections (`api/reqreply.Observability`,
`api/events.Observability`, `api/rest`'s Diagnostics-ferry mechanism),
each explaining ITS OWN rationale (double-counting avoidance, ctx
injection shape, per-adapter exceptions like mqtt5's server side
having no ctx to inject into), each cross-referencing a runnable
example. This is not a documentation gap — the API is already clear.
**Conclusion: Observer's current adapter-ownership is CORRECT, not a
gap, and this question is closed — no code change, no further
exploration planned for this roadmap.**

**Learnings (ErrorPattern implementation — 3 real corrections found
during detailed signature design, all resolved before writing code,
none discovered mid-implementation):**

1. **`SetCookie` was wrongly included in the "delete all 5" plan.**
   It's a general-purpose, exported, public API function with ~15 real
   call sites beyond ErrorPattern (success-path cookie writing in both
   adapters) — it cannot be deleted. Only 2 of the 4 remaining
   ErrorPattern-specific functions (`tryRespondErrorPatternGeneric`/
   `tryRespondErrorPattern`) needed FULL deletion of their write logic;
   the other 2 became thin `WriteErrorResponse` interface-method
   implementations that still call the UNCHANGED, still-public
   `SetCookie` internally.
2. **Real call-site count for the 2 fully-internally-replaced
   orchestrator functions was 66** (`tryRespondErrorPatternGeneric`:
   14+14; `tryRespondErrorPattern`: 19+19) — every one of them stayed
   completely UNCHANGED at the call site (only the 2 functions'
   INTERNAL bodies were replaced with thin delegating shims preserving
   the exact bool-return/`*err`-mutation contract), so this large
   count carried ZERO actual migration risk — a good example of
   "large count, low per-site complexity."
3. **The "full migration" of the shared `PendingCookie` staging type
   was precisely scoped and much narrower than initially feared.**
   `respHeaders` needed ZERO type migration (`http.Header` already IS
   `map[string][]string`, Go's assignability rules allow passing it
   directly to a `map[string][]string` parameter with no conversion
   boilerplate at all). `PendingCookie` itself became a straightforward
   TYPE ALIAS (`type PendingCookie = rest.PendingCookie` in both
   adapters) — the ~80 `ctx.Value(...)`/pass-through touch points
   originally worried about were unaffected (they never touch the
   internal field), leaving only 14 CONSTRUCTION sites (`Opts:
   cookieOptionsFrom(...)` → `Attrs: ...` directly) and 16
   CONSUMPTION sites (`pc.Opts` → `cookieOptionsFrom(pc.Attrs)`,
   moving the adapter-specific conversion from staging-time to
   write-time) across both adapters — squarely in line with prior
   phases' migration scale (30 real touch points, not 80+).
   `CookieAttributes` (already existing in `api/rest`) turned out to
   be an exact structural match for what the new `PendingCookie.Attrs`
   field needed — no new attribute type was required at all.

Full verification (`gofmt`/`build`/`vet`/`test`/`just check`/all
examples, including explicit confirmation that `examples/rest-api`'s
own `ErrorPattern`/response-violation demos still produce byte-identical
output) passed clean on the first attempt after these 3 corrections
were folded into the design before writing any code.

### Phase 8 — Review & Closeout (not a feature phase)

Once Phase 3 ships, this roadmap doc's implementation is considered
COMPLETE — Phase 8 is the closing review pass, not further feature work:

- Run the `review-go-codex` skill, split per-API (not one combined pass):
  - **1.1 — `api/events` — DONE (Round 144).** 2 trivial findings: a
    stale `CapabilitySpec`→`CapabilityRequirement` rename cross-reference
    in `render/asyncapi/v3/document.go`'s godoc, and a missing direct
    unit test for `events.ApplyCapabilities` (added). No `bug`/`small`
    findings — naming parity, param types, `ErrorChannel`, the
    `Capability`/`LeveledCapability` mechanism, and observer wiring were
    all already covered by prior rounds.
  - **1.2 — `api/reqreply` — DONE (Round 145).** 1 `bug` finding: the
    async `RouteHandle.NewFutureAny`'s type-mismatch path (the
    `FutureFactory` mechanism behind `Client.CallAsync`) used a bare
    `fmt.Errorf`, not `errors.As`-navigable, while its synchronous twin
    (`CallWithTransport`) correctly returned the typed
    `TransportTypeMismatchError` for the identical failure — fixed +
    regression test added. No other findings — the rest was already
    covered by prior rounds.
  - 1.3 — `api/rest` — **DONE (Round 146).** 2 `small`/`trivial`
    findings: `adapters/chi.OptionsShapeError.Error()` said "nethttp"
    instead of "chi" (copy-paste artifact from mirroring the nethttp
    adapter's identical error type) — fixed; and 6 stale
    `[HandlerIngest]`/`[SSEFromStream]` godoc bracket-links across
    `adapters/nethttp` and `adapters/chi`'s `stream.go`/
    `stream_errors.go` (both symbols were renamed/unexported during the
    earlier stream-bridge cleanup — `HandlerIngest`→`IngestAdapter`,
    `SSEFromStream`→unexported `sseFromStream` reachable only via
    `SSEFromHub`) — repointed to the live symbols. No `bug` findings —
    REST's naming parity, param types, format API parity, error
    sentinels, observer wiring, boundary symmetry, and error-path
    ergonomics were all already covered by prior rounds.
- Run the `review-docs` skill, split per-API (mirrors the 1.1/1.2/1.3
  `review-go-codex` split, per explicit user preference):
  - **2.1 — `api/rest` — DONE (Round DR9).** Found the D1 root cause was
    much larger than a typical round: an earlier Phase 4d rename
    (`nethttp.AttachMux`/`chi.AttachRouter`/client-side `nethttp.Attach`
    → `NewServerTransport`/`NewClientTransport` + `.Attach(...)`) was
    applied correctly in code everywhere but never fully swept from
    docs — including `api/rest/builder.go`'s OWN exported godoc. Fixed
    across `docs/concepts/*.md` (REST-scoped portions), `docs/guides/
    {http-server,http-client,openapi}.md`, `docs/features/{http-client,
    security,rest-api,sse-streaming}.md`, `examples/rest-api/*` stale
    comments, `api/rest/{builder,middleware,builder_test}.go` godoc,
    `adapters/nethttp/{doc,stream,client_test}.go`, `adapters/chi/
    {doc,adapter_test}.go`. Also fixed a fictional roadmap-labeled
    `ErrorResponse[...]` block in `http-server.md` (real mechanism:
    `rest.ErrorPattern`) and false "v1-scoped" claims about
    `rest.Client.Call` (it is full-featured). See
    `.github/skills/review-docs/references/history.md`'s Round DR9 for
    the full finding list.
  - 2.2 — `api/events` — not yet run.
  - 2.3 — `api/reqreply` — not yet run.
  - 2.4 — shared/cross-cutting surfaces (README, project-structure.md,
    zensical.toml nav, go-codex.instructions.md, docs/index.md,
    get-started.md, reference/index.md) — not yet run; runs LAST (after
    2.1-2.3) to verify their nav/cross-link changes are consistent.
- **Joint declarative-workflow walkthrough** — once Phase 3 ships,
  design review alone won't catch every rough edge; only walking the
  real, end-to-end user journey does. Together (user + agent), declare
  one `api/rest`, one `api/events`, and one `api/reqreply` API from
  scratch, step by step: struct codec definition → route/channel
  declaration → capability requirement declaration → adapter
  attachment → running it. This is a real "first-time user" simulation,
  not a test-writing exercise. Document every friction point, awkward
  step, or improvement opportunity found along the way — either as a
  Learnings entry in this doc or as a new follow-on roadmap doc if the
  fix is substantial enough to warrant one.
- **Add three per-API guided tutorial skills**, split rather than
  combined (per explicit preference), so each covers one API's full
  declarative workflow end to end:
  - `.github/skills/tutorial-api-rest/SKILL.md`
  - `.github/skills/tutorial-api-events/SKILL.md`
  - `.github/skills/tutorial-api-reqreply/SKILL.md`

  Each skill's scope mirrors the joint walkthrough above: guide a user,
  live, from struct codec definition through route/channel declaration,
  capability requirement declaration, and adapter attachment. Author
  these during Phase 4, informed by whatever the joint walkthrough
  surfaces — the tutorials should teach the polished workflow, not the
  as-yet-unrefined one. Follow
  `.github/instructions/agent-skills.instructions.md` for skill
  authoring conventions.
- **`docs/roadmap/zeromq-rest-adapter.md` — SPUN OUT ALREADY, not a
  Phase 4 task.** An earlier draft of this bullet flagged a sequencing
  tension (a dedicated adapter roadmap doc normally belongs BEFORE its
  adapter's Implement step, not after Phase 4) and left it open pending
  confirmation. RESOLVED: the doc was written during Phase 3's own
  Design step (the earlier timing), not deferred here — this bullet is
  kept only as a historical pointer, no further action needed.
- **`docs/roadmap/mqtt5-capability-extensions.md` — DONE, created.** A
  dedicated, Explore-mode roadmap doc designing the 2
  surveyed-but-deferred Tier 3a candidates from `docs/features/
  capabilities.md`'s "Surveyed but not implemented" section, now that
  `adapters/mqtt5` already exists (no adapter-doesn't-exist-yet blocker,
  unlike AMQP's still-pending candidates) — the ONLY reason these were
  deferred through Phases 1-2 was "nobody asked for this specific toggle
  yet," not a structural limitation:
  - **MQTT5 Message Expiry Interval** — a new sealed `mqtt5.Capability`
    (`mqtt5.MessageExpiry(seconds uint32)`), applied via
    `PublishOptions.Capabilities` — mirrors `Retained`'s shape exactly
    (a Publish-side-only attribute, no Subscribe-side equivalent).
    Ready to implement, no open questions — a one-line `adapter.go`
    change wires `wire.MessageExpiryInterval` into the already-existing
    `pahomqtt5.PublishProperties` construction.
  - **MQTT5 Shared Subscriptions** (`$share/group/topic`) — a new sealed
    `mqtt5.Capability` (`mqtt5.SharedSubscription(group string)`),
    applied via `SubscribeOptions.Capabilities` — confirmed via
    `paho.golang`'s `SubscribeOptions` struct that `$share/` is a
    topic-FILTER-STRING prefix, not a `WireAttributes` field, so it
    does NOT fit `Capability.Apply(*WireAttributes)`'s existing shape.
    Left as a genuine OPEN design decision in the new doc (widen
    `WireAttributes` with a mutable `Topic` field vs. a separate
    `TopicParam`-adjacent declaration that bypasses
    `ApplyCapabilities`) — not silently assumed to be a trivial
    string-prefix operation, not pre-decided here.
- **`docs/design/d-0006-protocol-native-capabilities.md` reworked —
  DONE.** Its status block now states the "zero backdoor between the
  api layer and the adapters" rule as its OWN first-class, explicit
  design goal (not merely a cross-reference to this roadmap doc),
  records Phase 4b's audit (3 real backdoors found in already-shipped
  code) as the motivating case study, and trims the accumulated
  "Amendment"/"Relationship to..."/"Supersedes" addenda layering into a
  condensed "Other resolved follow-ons" list — the body (§0-§9) is kept
  as original design-round history per repo convention.
- **Widened follow-on, spun into its OWN roadmap doc**:
  [`docs/roadmap/design-doc-compaction.md`](design-doc-compaction.md) —
  the api/adapter layer is now considered architecturally+feature
  complete for the workflow model this whole roadmap built (declare
  pattern → declare capability requirement/value → attach adapter, with
  `Capability` as the adapter's programming contract). That doc plans:
  further D-0006 body trimming if any is found necessary, giving the
  workflow model a durable narrative home in `docs/concepts/
  declaring-apis-and-ports.md`, and relocating THIS phase's own
  interface-audit bullet (below) into a new `docs/concepts/
  ports-and-adapters.md` section so it survives this roadmap doc's own
  eventual delete/promote fate (see the next bullet).
- Decide this roadmap doc's fate per the `plan-a-new-codex-feature`
  skill's delete/keep/promote-to-`docs/design/` policy. **Anticipated
  outcome, flagged now but confirmed only once Phase 3 actually
  ships:** promotion to `docs/design/d-NNNN-...md`, NOT deletion — this
  doc establishes ONE pattern followed by THREE api packages
  (`api/events`, `api/reqreply`, `api/rest`) and fundamentally changes
  how all three declare protocol-native behavior, matching the skill's
  promotion bar exactly (not a routine, single-feature roadmap doc).
- **Audit which adapter-side functions implement a real, declared
  interface vs. which are ad-hoc functions with no interface contract
  at all — raised during Phase 5a's review, COMPLETED below.**
  **Slated for relocation** (not yet moved) — per
  `design-doc-compaction.md` above, this table is durable reference
  material that should live in `docs/concepts/ports-and-adapters.md`'s
  planned "Interface inventory" section so it survives this roadmap
  doc's own eventual delete/promote fate; kept inline here until that
  move executes. Phase
  5a's own investigation surfaced this as a recurring, easy-to-miss
  distinction: `adapters/mqtt5.NewServerTransport(opts)
  reqreply.ServerTransport`/`adapters/nethttp.NewClientTransport(opts)
  rest.ClientTransport` satisfy REAL, declared, api-layer-owned
  interfaces (Phase 4d's shipped shape) — but plenty of other
  adapter-exported functions (e.g. the pre-Phase-5a
  `Serve[Req,Resp]`/`Call[Req,Resp]`, `CallWithHandle[Req,Resp]`,
  `ServeOne[Req,Resp]`) were exported, documented, real API surface
  that DON'T implement any declared interface at all. This closes the
  exact kind of ambiguity Phase 5a's review had to rediscover via
  first-principles code reading rather than consulting an existing,
  trustworthy inventory.

  **(A) Available interfaces adapters implement against** (confirmed
  via direct grep across every adapter, not assumed):

  | Package | Interface | Implemented by |
  |---|---|---|
  | `api/rest` | `ServerTransport`/`ServerAwareTransport` | `nethttp.serverTransport`, `chi.serverTransport` |
  | `api/rest` | `ClientTransport` | `nethttp.clientTransport` only (chi is server-only) |
  | `api/rest` | `HeaderCapableTransport`/`CookieCapableTransport`/`QueryCapableTransport` (Phase 6) | `nethttp.httpCarrier`, `chi.httpCarrier` |
  | `api/rest` | `ErrorResponseWriter` (Phase 7) | `nethttp`/`chi`'s `*statusResponseWriter` |
  | `api/rest` | `ErrorPatternValuer` | `nethttp.ErrorPatternResponse` |
  | `api/events` | `Transport`/`ClientAwareTransport` | `mqtt5`, `mqtt` (v3), `zeromq` |
  | `api/events` | `PublishTransport[T]`/`SubscribeTransport[T]` (Phase 5a shape) | `mqtt5`, `mqtt`, `zeromq` |
  | `api/reqreply` | `ServerTransport`/`ServerAwareTransport`/`ClientTransport` | `mqtt5`, `zeromq` |
  | `api/reqreply` | `ErrorPatternValuer` | `mqtt5.ErrorPatternResponse`, `zeromq.ErrorPatternResponse` |
  | all 3 | `CapabilityName`/`LeveledCapability` | `mqtt5.QoS`/`Retained`, `zeromq.HWM`/`Conflate`, `mqtt.QoS`/`Retained` |

  `Topical` (`api/reqreply`) and `RouteOpt`/`ChannelOpt` are NOT
  adapter-facing at all — `Topical` is implemented by `*RouteHandle`
  itself (api-layer-internal), and `RouteOpt`/`ChannelOpt` are
  implemented by USER-declared param types, not adapters — both listed
  here only to confirm they were checked, not omitted by oversight.

  **(B) Missing interfaces / gaps + potential closes:**
  1. REST supplies no native Tier-3a `Capability` VALUE of its own
     (`RequireQoS`/`RequireHWM` are REST-side requirement
     declarations, but no adapter can satisfy them — HTTP genuinely
     has no QoS/HWM concept). Confirmed CORRECT-AS-IS (Phase 3's own
     design: "adapter doesn't support this capability" is the intended
     outcome) — NOT a real gap, listed for completeness.
  2. `events.Address`/the full `Channel[Addr,T]` retrofit remains
     unshipped — a REAL gap, but deliberately DEFERRED (per D-0006's
     graduation note) until a real Address-needing adapter (AMQP)
     exists; not actionable today.
  3. reqreply's server-side ErrorPattern WRITE step (after the
     already-centralized `ObserveErrorResponseFor` match) has no
     `ErrorResponseWriter`-equivalent interface — mqtt5/zeromq realize
     it via inline/free-function reply-publish logic. Phase 7
     evaluated exactly this and found the 2 adapters' protocol shapes
     (declared-topic reply vs. broadcast) too divergent to unify
     profitably for just 2 implementers — a narrower, PER-PATTERN
     interface (e.g. `ReplyPublisher interface { PublishReply(topic
     string, body []byte) error }`) could be designed in a future
     session if this becomes a maintenance burden, but is not an
     obvious net win today.

  **(C) Interactions deliberately NOT interface-implemented, with reasons:**
  1. **Observer outcome-recording** (`RecordRequest`/`RecordPublish`/
     `RecordSubscribe`) — formally closed in Phase 7's review: only
     the adapter's own transport dispatch has the REAL per-protocol
     status/duration data; documented adapter-owned-by-design in
     `docs/features/observer.md`.
  2. **Capability VALUES themselves** (`mqtt5.QoS`, `zeromq.HWM`,
     etc.) — D-0006's original, never-reopened rejection of a shared
     value type: only the `CapabilityName`/`LeveledCapability`/`Apply`
     INTERFACES are shared, never the concrete sealed values.
  3. **Adapter `Options` structs** (`IngestSocketAdapterOptions`,
     `PublishOptions`, `ServerTransportOptions`, etc.) — deliberately
     concrete, never interfaces: these are CONSTRUCTOR-TIME
     configuration, not a dispatch contract, and forcing a shared
     field set across adapters with zero configuration overlap would
     defeat the purpose of adapter-specific tuning.
  4. **reqreply/events' server-side ErrorPattern wire-write** — see
     (B3) above.
  5. **A small set of genuinely free-standing exported helper
     functions per adapter** (confirmed via a full exported-function
     sweep across all 6 adapters, finding no additional gaps beyond
     B1-B3): `Connect`/`NewSecuredClient` (connection establishment),
     `MessageFromContext`/`RequestFromContext`/
     `ResponseCookiesFromContext`/`ResponseHeadersFromContext` (ctx
     accessors), `FromUserPropertyParam`/
     `FromResponseUserPropertyParam` (codec-declaration sugar),
     `NewCachingCredentialFunc` (credential-caching utility),
     `NewHub`/`NewDialer`/`NewUpgrader` (websocket connection-management
     constructors) — none of these are escape-hatch-style dispatch
     functions competing with an interface; each is connection setup,
     ctx plumbing, or declaration sugar, genuinely outside any
     interface's scope, matching the "intentionally adapter-specific
     sugar" category this audit's own action item anticipated.
- **Observer + ErrorPattern cross-cutting concerns** — see Phase 7
  above (folded in inline, no longer a separate spun-out doc).

## Scope decisions

| In scope (this doc) | Out of scope / deferred |
|---|---|
| **Phase 1** — Renamed/redesigned requirement type for events (`CapabilityRequirement`, was `CapabilitySpec`) + value-aware `LeveledCapability` + sugar helpers (`events.RequireQoS(events.AtLeastOnce)`, `RequireRetained`, `RequireHWM`, `RequireConflate`), rewriting D-0006's shipped mechanism into the three-tier vocabulary — BREAKING, deliberately (see Open Design Decision #7) | Any new shared capability VALUE type spanning adapters (rejected by D-0006, not reopened here) |
| **Phase 2** — Generalizing the declare-then-verify PATTERN to `api/reqreply` (own `reqreply.CapabilityRequirement` + coverage check) AND reusing all 4 ALREADY-SHIPPED capability values (`mqtt5.QoS`/`Retained`, `zeromq.HWM`/`Conflate` — zero new adapter types, per Phase 2's Design finding) | Any NEW capability VALUE beyond D-0006's existing survey (Message Expiry, Shared Subscriptions, AMQP addressing/dead-lettering remain exactly as speced in D-0006 section 6 — this doc only sketches what a PRESET for them would look like once/if they ship) |
| **Phase 3** — A NEW, synchronous, transport-stateless ZeroMQ REQ/REP adapter for `api/rest`, governed by the REST-eligible-transport guardrail | Ever giving `api/rest` an MQTT (v3/5) adapter — permanently excluded by design, not deferred; that shape belongs to `api/reqreply` |
| Adapter-owned preset/bundle constructors (e.g. `mqtt5.PresetReliableWithHeaders()`) bundling multiple existing `Capability` values + a matching `CapabilityRequirement` set in one call | Merging/consolidating `api/rest` and `api/reqreply` — considered settled as permanently separate APIs (sync/stateless vs. async/broker-mediated), even where both touch ZeroMQ |
| Compile-time vs. runtime enforcement — explicitly documented as staying RUNTIME (like today) for Phases 1-2; becoming genuinely runtime-checked for REST's implicit capabilities once Phase 3 ships a second transport family | Solving compile-time requirement-composition (would need reflection or a closed capability enum; not attempted) |
| Classifying/documenting EXISTING capabilities (`rest.HeaderParam`/`CookieParam`, `mqtt5.UserPropertyParam`) against the three-tier guardrail, AND naming/documenting the **Baseline** tier (Route/Topic) + the dual (declaring-user / adapter-author) framing for ALL THREE tiers — both descriptive-only, no new code, since Baseline is already enforced via `ports.RESTPattern`/`EventPattern`/`ReqReplyPattern` + `SourceAdapter`/`SinkAdapter`/`IOAdapter` | Redesigning `ports.Pattern`/`SourceAdapter`/`SinkAdapter`/`IOAdapter` themselves — they already do their job; this doc only gives their existing role a name in this vocabulary |

## Sketched API surface

**Phase 1's `api/events` and Phase 2's `api/reqreply` surfaces are now
FINALIZED — see their own subsections under "Implementation approach"
above for the authoritative, resolved signatures.** The sketch below is
kept for Phase 3 only, still speculative:

```go
// adapters/mqtt5 — preset/bundle constructors. Each preset is sugar: it
// returns exactly what a caller would otherwise assemble by hand from
// existing SubscribeOptions/PublishOptions/Capabilities/
// CapabilityRequirement values — no new underlying mechanism.
func PresetReliable() SubscribeOptions
func PresetReliableWithHeaders() SubscribeOptions

// SPECULATIVE — future explicit capability example, illustrating the
// "Design guardrails" classification above; NOT designed in depth here.
// Tracked against docs/roadmap/amqp-adapter.md, which owns the actual
// AMQP adapter design — this sketch exists ONLY to show that a
// channel-level, adapter-agnostic "message exchange/queue topology"
// requirement fits the SAME explicit-capability shape as RequireQoS/
// RequireRetained above, once a real AMQP adapter exists to satisfy it.
//
//	func RequireExchange(kind ExchangeKind) ChannelOpt
```

## Structured errors

- **Events (Phase 1, FINALIZED):** `*events.CapabilityCoverageError`
  (renamed/redesigned from `MissingCapabilityError`, breaking change,
  deliberate — see the Phase 1 subsection above) — `Error()`/`LogValue()`
  covering BOTH `Missing` and `Insufficient` (`[]LevelMismatch`) failure
  kinds. No `Unwrap()` (no wrapped inner error, matches the old type's
  shape).
- **ReqReply (Phase 2, FINALIZED):** own `reqreply.CapabilityCoverageError`/
  `LevelMismatch` — a `reqreply`-local type (no `api/events` dependency —
  mirrors why `middleware.Disposition` was placed outside `api/events`
  specifically so `api/reqreply` could reuse it without importing
  `api/events`), same shape/`Error()`/`LogValue()` convention as events'.

## Observer integration

- **Events (Phase 1, FINALIZED):** unchanged mechanism —
  `stats.CapabilityObserver.RecordCapabilityApplied` still fires for
  every adapter-supplied capability; value-aware checking happens
  entirely inside `CheckCapabilityCoverage`, upstream of where the
  observer fires. No new observer method needed.
- **ReqReply (Phase 2, FINALIZED):** reuses the SAME shared
  `stats.CapabilityObserver` extension — `adapters/mqtt5`/
  `adapters/zeromq`'s reqreply dispatch code calls
  `events.RecordCapabilityApplied` directly (already fully generic, no
  reqreply-specific observer code needed) for every capability actually
  applied to a served/called route.

## Unit test plan

**Phase 1's and Phase 2's full test matrices are FINALIZED — see their
own subsections under "Implementation approach" above.** Phase 3 sketch
below, still open:

| Test | Verifies |
|---|---|
| `mqtt5.PresetReliable()`/`PresetReliableWithHeaders()` return the same `SubscribeOptions` a caller would hand-assemble | Preset correctness, no hidden extra behavior |
| `nil` Observer / plain Observer (no `CapabilityObserver`) → no panic on the new REST/ZeroMQ adapter's attach path | Observer guard correctness |

## Files to create

**Phase 1's and Phase 2's full file lists are FINALIZED — see their own
subsections under "Implementation approach" above.** Phase 3 sketch,
still open:

| File | Responsibility |
|---|---|
| `adapters/mqtt5/preset.go` | `PresetReliable`, `PresetReliableWithHeaders` (and equivalents for `adapters/mqtt`/`adapters/zeromq` if the pattern proves useful there) |
| `docs/features/capabilities.md` | Updated in Phase 1/2 already; extended again once Phase 3 ships |

## Out of scope (this doc, until resolved elsewhere)

- Any new capability VALUE (Message Expiry, Shared Subscriptions, AMQP
  addressing/dead-lettering) — tracked entirely in D-0006 section 6 and
  `docs/roadmap/amqp-adapter.md`; this doc only sketches what a preset
  bundling them would look like once/if they ship.
- **(UPDATED — no longer a blanket exclusion)** `api/rest` becoming
  arbitrarily multi-transport-pluggable in general is out of scope — this
  doc commits ONLY to the one specific, synchronous+transport-stateless
  ZeroMQ REQ/REP adapter (Phase 3), not an open-ended pluggable-transport
  architecture. Permanently, definitionally out of scope regardless of
  future rounds: giving `api/rest` an MQTT (v3/5) adapter — excluded by
  the REST-eligible-transport guardrail, not deferred.

## Open design decisions (to resolve before/during implementation)

1. **(RESOLVED by Phase 2's Design step) Where does reqreply's capability
   type live?** Own package-local type in `api/reqreply`
   (`CapabilityRequirement`/`CheckCapabilityCoverage`/etc., full
   independent duplication, zero `api/events` dependency) — mirrors
   `middleware.Disposition`'s placement rationale. Decided WITH a
   concrete driver, not speculatively: Phase 2 found both reqreply
   transports (`mqtt5`, `zeromq`) can reuse their EXISTING events-side
   capability VALUES (`QoS`/`Retained`/`HWM`/`Conflate`) immediately, so
   there was never a need for a shared type across packages — only the
   generic, adapter-facing HELPERS (`events.ResolveCapabilityValue`/
   `events.RecordCapabilityApplied`) are reused as-is (from adapters,
   which already import both packages), while the DECLARATION-side type
   stays independently duplicated per package.
2. **(RESOLVED by Phase 1's Design step) Package placement for events'
   `RequireXxx` helpers** — `api/events` directly, alongside
   `CapabilityRequirement` (its own new home, renamed from
   `CapabilitySpec`) — no new shared package. Whether `api/reqreply`
   reuses `QoSLevel`'s exact naming (vs. its own type) remains open,
   folded into Open Design Decision #1 above, to be resolved during
   Phase 2's Design step.
3. **Preset scope** — which combinations deserve a named preset
   constructor vs. just documentation showing how to compose the
   existing primitives by hand? Needs at least one real user request
   before committing more than `PresetReliable`/`PresetReliableWithHeaders`.
4. **(RESOLVED — no longer open) The REST/multi-transport question now
   has a concrete driver: Phase 3's new ZeroMQ REQ/REP adapter.** What
   remains genuinely open, to be resolved during Phase 3's Design step,
   not before: whether REST's OpenAPI-only spec rendering stays
   OpenAPI-only for the new transport or needs its own rendering path
   (mirrors D-0006's own precedent of a fresh naming/rendering survey
   before a real Address-needing adapter existed).
5. **Should `mqtt5.UserPropertyParam`-style adapter-options-scoped fields
   be promoted to genuine channel-level implicit capabilities?** Per the
   "Design guardrails" section above, `UserPropertyParam` today is
   declared on `mqtt5`'s own options struct — the caller has already
   committed to mqtt5 before writing it, unlike `HeaderParam`'s genuine
   pre-adapter-choice, route-level declaration. Promoting it to a
   channel-level `ChannelOpt` (declarable before choosing an adapter,
   gating whichever adapter attaches, checked via
   `CheckCapabilityCoverage` like the explicit case) is a real,
   non-trivial change — and only worth doing once a concrete second
   adapter exists that would need to REJECT attaching to a channel that
   already declared a user-property-style requirement it can't satisfy
   (today, mqtt v3/zeromq simply have no such options field to construct
   in the first place, so the mismatch is already a compile error by
   omission — it's not obvious runtime coverage-checking adds anything
   yet). Left open, hypothetical, not committed.
6. **Should `add-a-new-adapter` skill's Step 5e be updated to explicitly
   narrate this doc's "baseline, then progressive optional capabilities;
   leaving one out is fine" framing?** Step 5e already MANDATES the
   sealed `Capability` mechanism for adapter authors — it's the correct
   enforcement point, unchanged. What it doesn't yet say in words is the
   permission-granting half of this doc's guardrail: that omitting a
   capability your protocol doesn't support is a correct, permanent
   outcome, not a partial implementation to apologize for. A
   documentation-sequencing question, not a design question — lean
   toward updating Step 5e's wording once this doc's classification
   stabilizes through an implementation pass, not now.
7. **(RESOLVED — breaking, deliberately) Additive vs. breaking Phase 1
   rewrite.** The sole user of go-codex confirmed breaking changes are
   fully acceptable and explicitly prioritized precision/strength over
   compatibility with the already-shipped surface. Decided: **breaking**
   — `CapabilitySpec`→`CapabilityRequirement`,
   `MissingCapabilityError`→`CapabilityCoverageError` (now also carrying
   `Insufficient []LevelMismatch`, fixing the name-only-matching
   limitation via a new `LeveledCapability` optional interface), plus the
   matching field/reflection renames across all 3 event adapters. The
   `plan-a-new-codex-feature` skill's "Removing an old API" checklist
   still applies MECHANICALLY during Implement (every real consumer,
   including `examples/events-api`, must be migrated, and docs swept for
   dangling references) — migration, not avoidance, is simply the chosen
   path rather than a reason to stay additive. See the Phase 1 subsection
   under "Implementation approach" above for the full resolved design.

## See also

- [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md) — the shipped mechanism this doc extends, and the two-part test that scopes what stays adapter-owned
- [D-0006 §3 — "Relationship to D-0003"](../design/d-0006-protocol-native-capabilities.md#3-relationship-to-d-0003--resolved-via-a-4-stage-lifecycle-model-confirmed-via-a-fifth-throwaway-go-prototype) — the prototyped 4-stage lifecycle model (declare → capability-declare → handler-attach → adapter-attach) this doc's "cross-cutting alignment" note (under "Implementation approach" above) re-applies to Phases 2 and 3, not just events
- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) — `Middleware[In,Out]`/`Transform`/`ClientTransform`/`.Use(mw)`, already shipped for all 3 APIs this doc's phases touch
- [`docs/features/capabilities.md`](../features/capabilities.md) — the current, shipped-only reference page
- [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md) — precedent for placing a shared mechanism (`middleware.Disposition`) outside `api/events` specifically so `api/reqreply` can reuse it dependency-free
- `.github/skills/add-a-new-adapter/SKILL.md`'s Step 5e — the existing MANDATORY sealed-`Capability` requirement for adapter authors, which this doc's Tier 3 (explicit) adapter-author framing builds directly on top of
