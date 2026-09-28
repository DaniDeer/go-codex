# Composable Capability Requirements — declare-first, adapter-satisfies-second

> **Status:** Phases 1-2 SHIPPED (see each subsection's own Learnings
> entry); Phases 3-4 not yet started. Spun out of a user
> question about
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

**Scope split (resolved this round):** this phase spans TWO concerns
that get designed in TWO separate docs, per this repo's own convention —
a new adapter gets its own dedicated Explore-mode roadmap doc, written
BEFORE its Implement step:

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
| `adapters/zeromqrest/*` | Per the sibling adapter doc's own design — consumes `HeaderParamNames`/etc. via reflection, exactly as described in that doc's "Security/Middleware dispatch" section |

- **Implement:** the six mandatory requirements PLUS the
  `add-a-new-adapter` skill's full new-adapter checklist (this is a brand
  new transport package, not an extension of an existing one) — the
  ADAPTER half of Implement follows
  [`zeromq-rest-adapter.md`](zeromq-rest-adapter.md)'s own design; the
  CAPABILITY half follows this doc's design above.
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
- **Create `docs/roadmap/mqtt5-capability-extensions.md`** — a
  dedicated, Explore-mode roadmap doc designing+shipping the 2
  surveyed-but-deferred Tier 3a candidates from `docs/features/
  capabilities.md`'s "Surveyed but not implemented" section, now that
  `adapters/mqtt5` already exists (no adapter-doesn't-exist-yet blocker,
  unlike AMQP's still-pending candidates) — the ONLY reason these were
  deferred through Phases 1-2 was "nobody asked for this specific toggle
  yet," not a structural limitation:
  - **MQTT5 Message Expiry Interval** — a new sealed `mqtt5.Capability`
    (e.g. `mqtt5.MessageExpiry(seconds int)`), applied via
    `PublishOptions.Capabilities` — mirrors `Retained`'s shape exactly
    (a Publish-side-only attribute, no Subscribe-side equivalent).
  - **MQTT5 Shared Subscriptions** (`$share/group/topic`) — a new sealed
    `mqtt5.Capability` (e.g. `mqtt5.SharedSubscription(group string)`),
    applied via `SubscribeOptions.Capabilities` — needs its OWN design
    decision on how the `$share/` prefix composes with the channel's
    already-declared topic template (does the capability wrap/rewrite
    the subscribe filter at Attach time, or does it require a NEW
    `TopicParam`-adjacent declaration?) — not silently assumed to be a
    trivial string-prefix operation.
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
