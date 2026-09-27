# Composable Capability Requirements — declare-first, adapter-satisfies-second

> **Status:** Idea only — no code written. Spun out of a user question
> about [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md)'s
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
  family so `HeaderParam` is always satisfiable, and mqtt/zeromq simply
  have no `UserPropertyParam`-shaped field to construct in the first
  place, so the mismatch is a compile error by omission).

### Tier 3 — Explicit capability

- **What it is:** a standalone requirement, declared directly, NOT
  derived from any codec field — the user states the protocol behavior
  they want by name.
- **Declaring-user side, shipped example:** `events.CapabilitySpec`
  (`RequireQoS`/`RequireRetained` per this doc's own proposal, section
  above) — declared at the CHANNEL level, before any adapter is chosen,
  verified at `Attach` time via `CheckCapabilityCoverage`. Plus this
  round's new example: a future AMQP "message exchange/queue topology"
  requirement — satisfiable "out of the box as a protocol" only by an
  AMQP-family adapter (no code shipped yet; tracked in
  `docs/roadmap/amqp-adapter.md` and D-0006 section 6's survey).
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
  implementation or a gap.** Worked examples from this round: a
  hypothetical ZeroMQ request/response adapter correctly omitting
  header/cookie-equivalent support; an MQTT5 adapter correctly, forever,
  never implementing AMQP's exchange/queue capability, because no one
  would ever want an MQTT5 adapter to fake AMQP topology. This is safe
  specifically BECAUSE `CheckCapabilityCoverage` (explicit) / structural
  type-non-satisfaction (implicit) already make a declared-but-
  unsatisfied requirement transparent and diagnosable at attach time —
  the mismatch is never silently swallowed.

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
same three-tier vocabulary.

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
(not a TODO)? Conflating tiers, or describing only the declaring-user
half, was the exact gap this round's course-correction identified in
D-0006's original implementation.

## Relationship to D-0006 — reused mechanism, NOT reopened value-sharing decision

D-0006 (graduated) already ships the requirement-declare / adapter-verify
HALF of this idea for events:

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

### Phase 1 — `api/events` (rewrite the shipped D-0006 mechanism)

- **Design:** finalize this phase's API surface in this doc, resolving
  the open "additive vs. breaking rewrite" question first (a real
  consumer already exists: `examples/events-api/demo_capability_mechanism.go`
  uses today's shipped `Capability`/`CapabilitySpec` surface directly —
  if the resolved answer is "breaking," the skill's "Removing an old API"
  checklist applies BEFORE any deletion).
- **Implement:** the six mandatory requirements, against the finalized
  design.
- **Examples:** update `examples/events-api`'s capability-related demo(s)
  (`demo_capability_mechanism.go` and any other file touching
  `Capability`/`CapabilitySpec`) to the rewritten mechanism.
- **Docs:** update `docs/features/capabilities.md`, `docs/features/events.md`,
  `docs/guides/mqtt.md`/`mqtt5.md`/`zeromq.md`, and
  `.github/instructions/go-codex.instructions.md`.
- **Learnings:** recorded here before Phase 2's Design step begins —
  directly resolves the existing "where does reqreply's capability-spec
  type live" open decision with real evidence from Phase 1, instead of
  speculating.

### Phase 2 — `api/reqreply` (apply the (possibly revised) mechanism)

- **Design:** finalize `api/reqreply`'s capability-spec/coverage-check
  API surface in this doc, informed by Phase 1's learnings. Chosen
  second because reqreply is structurally closest to events (both are
  dispatch-loop-shaped; `middleware.Disposition` is existing precedent
  for a shared mechanism reqreply already consumes without an
  `api/events` dependency).
- **Implement:** the six mandatory requirements.
- **Examples:** update reqreply's own example(s) demonstrating the new
  capability mechanism.
- **Docs:** update reqreply's feature/guide pages and
  `.github/instructions/go-codex.instructions.md`.
- **Learnings:** recorded here before Phase 3's Design step begins.

### Phase 3 — `api/rest` (a new, synchronous, transport-stateless adapter)

- **Design:** finalize a NEW ZeroMQ REQ/REP-based adapter for `api/rest`
  in this doc, informed by Phases 1-2's learnings, governed by the new
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
  Also resolve during Design: `rest.HeaderParam`/`CookieParam` become
  GENUINELY runtime-checked Tier 2 (implicit) capabilities once REST has
  a second transport family (today's "trivially always satisfied because
  REST has one transport" argument disappears); and whether REST's
  OpenAPI-only spec rendering stays OpenAPI-only for the new transport or
  needs its own rendering path (flagged now, resolved during Phase 3's
  Design step, not before).
- **Implement:** the six mandatory requirements PLUS the
  `add-a-new-adapter` skill's full new-adapter checklist (this is a brand
  new transport package, not an extension of an existing one).
- **Examples:** update/add `api/rest` example(s) demonstrating the
  ZeroMQ REQ/REP adapter alongside the existing HTTP ones.
- **Docs:** update `docs/features/rest-api.md`, `docs/features/capabilities.md`,
  relevant guides, and `.github/instructions/go-codex.instructions.md`.
- **Learnings:** recorded here before Phase 4 begins.

### Phase 4 — Review & Closeout (not a feature phase)

Once Phase 3 ships, this roadmap doc's implementation is considered
COMPLETE — Phase 4 is the closing review pass, not further feature work:

- Run the `review-go-codex` skill across `api/events`/`api/reqreply`/
  `api/rest` for cross-layer consistency now that all three implement the
  same three-tier model (naming parity, error shapes, observer wiring,
  param types).
- Run the `review-docs` skill for a final three-surface documentation
  sync pass across every touched package.
- Decide this roadmap doc's fate per the `plan-a-new-codex-feature`
  skill's delete/keep/promote-to-`docs/design/` policy. **Anticipated
  outcome, flagged now but confirmed only once Phase 3 actually
  ships:** promotion to `docs/design/d-NNNN-...md`, NOT deletion — this
  doc establishes ONE pattern followed by THREE api packages
  (`api/events`, `api/reqreply`, `api/rest`) and fundamentally changes
  how all three declare protocol-native behavior, matching the skill's
  promotion bar exactly (not a routine, single-feature roadmap doc).

## Scope decisions

| In scope (this doc) | Out of scope / deferred |
|---|---|
| **Phase 1** — Shared requirement-naming helpers for events (thin `ChannelOpt` wrappers around `CapabilitySpec`, e.g. `events.RequireQoS(events.AtLeastOnce)`), rewriting D-0006's shipped mechanism into the three-tier vocabulary | Any new shared capability VALUE type spanning adapters (rejected by D-0006, not reopened here) |
| **Phase 2** — Generalizing the declare-then-verify PATTERN to `api/reqreply` (new `reqreply.CapabilitySpec`-equivalent + coverage check, even with zero concrete reqreply capabilities shipped today) | Any NEW capability VALUE beyond D-0006's existing survey (Message Expiry, Shared Subscriptions, AMQP addressing/dead-lettering remain exactly as speced in D-0006 section 6 — this doc only sketches what a PRESET for them would look like once/if they ship) |
| **Phase 3** — A NEW, synchronous, transport-stateless ZeroMQ REQ/REP adapter for `api/rest`, governed by the REST-eligible-transport guardrail | Ever giving `api/rest` an MQTT (v3/5) adapter — permanently excluded by design, not deferred; that shape belongs to `api/reqreply` |
| Adapter-owned preset/bundle constructors (e.g. `mqtt5.PresetReliableWithHeaders()`) bundling multiple existing `Capability` values + a matching `CapabilitySpec` set in one call | Merging/consolidating `api/rest` and `api/reqreply` — considered settled as permanently separate APIs (sync/stateless vs. async/broker-mediated), even where both touch ZeroMQ |
| Compile-time vs. runtime enforcement — explicitly documented as staying RUNTIME (like today) for Phases 1-2; becoming genuinely runtime-checked for REST's implicit capabilities once Phase 3 ships a second transport family | Solving compile-time requirement-composition (would need reflection or a closed capability enum; not attempted) |
| Classifying/documenting EXISTING capabilities (`rest.HeaderParam`/`CookieParam`, `mqtt5.UserPropertyParam`) against the three-tier guardrail — a documentation/audit deliverable, using the guardrail section above | — |
| Naming/documenting the **Baseline** tier (Route/Topic) and the dual (declaring-user / adapter-author) framing for ALL THREE tiers — descriptive only, no new code, since Baseline is already enforced via `ports.RESTPattern`/`EventPattern`/`ReqReplyPattern` + `SourceAdapter`/`SinkAdapter`/`IOAdapter` | Redesigning `ports.Pattern`/`SourceAdapter`/`SinkAdapter`/`IOAdapter` themselves — they already do their job; this doc only gives their existing role a name in this vocabulary |

## Sketched API surface

```go
// api/events — thin, adapter-agnostic requirement-naming helpers, each a
// ChannelOpt wrapping the existing CapabilitySpec. The concrete Capability
// VALUE supplied at Attach time is still adapter-owned (e.g.
// mqtt5.QoSAtLeastOnce) — these helpers only affect the DECLARED
// requirement's name/description, sparing the caller from writing
// `events.CapabilitySpec{Name: "QoS", ...}` by hand and from having to
// agree byte-for-byte with whatever name a given adapter's
// CapabilityName() reports.
func RequireQoS(level QoSLevel) ChannelOpt
func RequireRetained() ChannelOpt
func RequireUserProperties() ChannelOpt

// QoSLevel is a DECLARATION-ONLY concept (drives CapabilitySpec.Name/
// Description) — NOT a shared runtime value. It never crosses into an
// adapter's own sealed Capability type; CheckCapabilityCoverage still
// matches by name only, exactly as today.
type QoSLevel int

const (
	AtMostOnce QoSLevel = iota
	AtLeastOnce
	ExactlyOnce
)

// api/reqreply — the SAME declare-then-verify pattern events already has,
// ported to reqreply's own Route builder. Mirrors CapabilitySpec/
// CheckCapabilityCoverage's shape exactly (naming TBD — see "Open design
// decisions").
type CapabilitySpec struct {
	Name        string
	Description string
}

func CheckCapabilityCoverage(topic string, declared []CapabilitySpec, supplied []any) error

// adapters/mqtt5 — preset/bundle constructors. Each preset is sugar: it
// returns exactly what a caller would otherwise assemble by hand from
// existing SubscribeOptions/PublishOptions/Capabilities/CapabilitySpec
// values — no new underlying mechanism.
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

- Events: reuses `*events.MissingCapabilityError` (already
  `slog.LogValuer`) unchanged — the new `RequireQoS`/`RequireRetained`/
  `RequireUserProperties` helpers produce ordinary `CapabilitySpec`
  values, so no new error type is needed on the events side.
- ReqReply: needs its OWN typed error (open design decision below) —
  either a `reqreply.MissingCapabilityError` (own type, own package, no
  `api/events` dependency — mirrors why `middleware.Disposition` was
  placed outside `api/events` specifically so `api/reqreply` could reuse
  it without importing `api/events`) or literal reuse of
  `events.MissingCapabilityError` if a cross-package error type turns out
  to be acceptable. Whichever is chosen, the error MUST implement
  `Error()`/`LogValue()` per this repo's structured-error convention
  (see `codex/errors.go`).

## Observer integration

- Events: unchanged — `stats.CapabilityObserver.RecordCapabilityApplied`
  already fires for every adapter-supplied capability; the new
  `RequireXxx` helpers don't add or remove observer calls, only sugar the
  declaration side.
- ReqReply: needs the type-assertion guard extended to reqreply's own
  attach path (`if capObs, ok := obs.(stats.CapabilityObserver); ok { ... }`),
  wired into whichever reqreply adapters eventually supply capabilities.
  No concrete reqreply capability exists yet (mirrors Disposition's own
  "prove the plumbing before a real capability arrives" precedent from
  D-0006 section 8).

## Unit test plan (sketch — refine once open decisions are resolved)

| Test | Verifies |
|---|---|
| `RequireQoS`/`RequireRetained`/`RequireUserProperties` produce the expected `CapabilitySpec{Name, Description}` | Helper correctness |
| `NewChannel` accepts `RequireXxx(...)` alongside existing `ChannelOpt`s | No regression to channel declaration |
| `CheckCapabilityCoverage` still matches a `RequireQoS`-declared spec against an adapter-supplied `mqtt5.QoSAtLeastOnce` value | Helpers are drop-in compatible with the existing coverage check |
| Missing capability via a `RequireXxx`-declared spec still returns `*MissingCapabilityError` with the right `Names` | No behavior change from the sugar layer |
| reqreply `CapabilitySpec`/`CheckCapabilityCoverage` — happy path (all declared specs matched) | New reqreply mechanism, happy path |
| reqreply `CheckCapabilityCoverage` — missing capability returns typed error with `errors.As` reachable inner error and correct `LogValue()` group | New reqreply mechanism, error path |
| `mqtt5.PresetReliable()`/`PresetReliableWithHeaders()` return the same `SubscribeOptions` a caller would hand-assemble | Preset correctness, no hidden extra behavior |
| `nil` Observer / plain Observer (no `CapabilityObserver`) → no panic on reqreply's new attach path | Observer guard correctness |

## Files to create

| File | Responsibility |
|---|---|
| `api/events/capability_require.go` | `RequireQoS`/`RequireRetained`/`RequireUserProperties` + `QoSLevel` |
| `api/reqreply/capability.go` | `CapabilitySpec`, `CheckCapabilityCoverage`, capability-related error type |
| `adapters/mqtt5/preset.go` | `PresetReliable`, `PresetReliableWithHeaders` (and equivalents for `adapters/mqtt`/`adapters/zeromq` if the pattern proves useful there) |
| `docs/features/capabilities.md` | Update once shipped — document the requirement-naming helpers and presets alongside the existing per-adapter table |

## Out of scope (this doc, until resolved elsewhere)

- Any new capability VALUE (Message Expiry, Shared Subscriptions, AMQP
  addressing/dead-lettering) — tracked entirely in D-0006 section 6 and
  `docs/roadmap/amqp-adapter.md`; this doc only sketches what a preset
  bundling them would look like once/if they ship.
- `api/rest` transport pluggability (see "Open design decisions").

## Open design decisions (to resolve before/during implementation)

1. **Where does reqreply's capability-spec type live?** Own package-local
   type in `api/reqreply` (no `api/events` dependency, mirrors
   `middleware.Disposition`'s placement rationale), or does it make more
   sense for both to share one type in a neutral location? Needs a
   concrete reqreply capability candidate (e.g. a future AMQP ack-mode) to
   decide with real evidence, not speculatively.
2. **Exact requirement-naming vocabulary and package placement for
   events' `RequireXxx` helpers** — `api/events` directly (simplest,
   mirrors `CapabilitySpec`'s own location) vs. a new shared package. No
   strong driver for a new package identified yet; default to `api/events`
   unless a concrete cross-cutting need (e.g. reqreply reusing the exact
   same `QoSLevel` naming) emerges.
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
7. **Additive vs. breaking Phase 1 rewrite — MUST be resolved before
   Phase 1's Design step is considered complete.** D-0006's `Capability`/
   `CapabilitySpec`/`CheckCapabilityCoverage`/`MissingCapabilityError`
   surface is ALREADY shipped with a real consumer
   (`examples/events-api/demo_capability_mechanism.go`). "Rewrite" could
   mean: (a) **additive** — existing exported symbols keep working
   unchanged, internals are reorganized, and the new `RequireQoS`/
   `RequireRetained`/Baseline-naming vocabulary is layered on top; or (b)
   **breaking** — the shipped surface itself is renamed/reshaped to
   match the three-tier vocabulary more directly. If (b), the
   `plan-a-new-codex-feature` skill's "Removing an old API" checklist
   (enumerate every responsibility of the old symbols, migrate every real
   consumer including the example, sweep docs for dangling references)
   applies BEFORE any deletion — not assumed free. Not decided here;
   decided during Phase 1's own Design step.

## See also

- [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md) — the shipped mechanism this doc extends, and the two-part test that scopes what stays adapter-owned
- [`docs/features/capabilities.md`](../features/capabilities.md) — the current, shipped-only reference page
- [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md) — precedent for placing a shared mechanism (`middleware.Disposition`) outside `api/events` specifically so `api/reqreply` can reuse it dependency-free
- `.github/skills/add-a-new-adapter/SKILL.md`'s Step 5e — the existing MANDATORY sealed-`Capability` requirement for adapter authors, which this doc's Tier 3 (explicit) adapter-author framing builds directly on top of
