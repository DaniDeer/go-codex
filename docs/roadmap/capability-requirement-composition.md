# Composable Capability Requirements — declare-first, adapter-satisfies-second

> **Status:** Idea only — no code written. Spun out of a user question
> about [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md)'s
> scope (events-only) while reviewing [`docs/features/capabilities.md`](../features/capabilities.md).
> Planned as 3 sequential implementation phases (`api/events` →
> `api/reqreply` → `api/rest`), each following its own
> design → implement → examples → docs → learnings cycle, followed by a
> Phase 4 review/closeout once all three ship — see "Implementation
> approach" below. **Phase 1's design is FINALIZED and
> READY FOR IMPLEMENTATION** (no code written yet — design and
> implementation are separate steps in this doc's own process).
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

**Status: DESIGN FINALIZED — READY FOR IMPLEMENTATION.** Every open
question below has an exact, pinned-down answer; nothing remains for the
Implement step to decide on the fly.

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
- **Learnings:** recorded here before Phase 2's Design step begins —
  directly resolves the existing "where does reqreply's capability-spec
  type live" open decision with real evidence from Phase 1, instead of
  speculating; also records whether the `LeveledCapability` pattern
  generalizes cleanly to any reqreply capability candidate; **MUST also
  catalog, with exact file/symbol detail (not just "something broke"),
  any `api/reqreply`-related breakage inside `adapters/mqtt5`/
  `adapters/zeromq` caused by Phase 1's renames** — this is the concrete
  input Phase 2 fixes first.

### Phase 2 — `api/reqreply` (apply the (possibly revised) mechanism)

- **Design:** finalize `api/reqreply`'s capability-spec/coverage-check
  API surface in this doc, informed by Phase 1's learnings.
  **FIRST sub-step: reconcile any reqreply breakage Phase 1's Learnings
  cataloged** (see Phase 1 above) — before any of Phase 2's OWN new
  capability work begins. Chosen second because reqreply is structurally
  closest to events (both are dispatch-loop-shaped;
  `middleware.Disposition` is existing precedent for a shared mechanism
  reqreply already consumes without an `api/events` dependency).
  **Also confirm** (per the cross-cutting alignment note above): the new
  reqreply capability mechanism coexists with the ALREADY-SHIPPED
  `reqreply.Middleware[In,Out]` (D-0003) as two independent stage-2
  declarations, per D-0006 §3's resolution — don't assume this holds
  unexamined just because it held for events; and that
  `stats.CapabilityObserver` is wired into reqreply's attach path using
  the SAME type-assertion-guard pattern events already uses, not a new
  interface.
- **Implement:** FIRST fix the cataloged Phase 1 breakage (build/tests
  green again for `api/reqreply` and its adapters), THEN the six
  mandatory requirements for Phase 2's own new capability work.
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
  Design step, not before). **Also confirm** (per the cross-cutting
  alignment note above): the new REST capability mechanism coexists with
  the ALREADY-SHIPPED `rest.Middleware[In,Out]` (D-0003) as two
  independent stage-2 declarations, per D-0006 §3's resolution — REST's
  own `Transform`/`ClientTransform`/`.Use(mw)` dispatch wiring
  (`adapters/nethttp`/`adapters/chi`) differs from events'/reqreply's, so
  this is NOT assumed to hold automatically just because it held for the
  other two; and that `stats.CapabilityObserver` is wired into the new
  ZeroMQ REST adapter's dispatch path using the SAME type-assertion-guard
  pattern events/reqreply use — this phase is where `CapabilityObserver`
  reaches REST for the first time, since Phase 3 introduces REST's
  capability mechanism from scratch.
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
- **Create `docs/roadmap/zeromq-rest-adapter.md`** — a dedicated,
  Explore-mode roadmap doc (mirrors the existing `amqp-adapter.md`/
  `tcp-adapter.md` precedent) capturing the ZeroMQ REQ/REP `api/rest`
  adapter's actual binding-level design (`ports.IOAdapter`
  implementation, error types, `add-a-new-adapter` skill's full
  checklist) with BASIC functionality/capabilities — this doc's own
  Phase 3 subsection only states the adapter's SCOPE (the
  REST-eligible-transport guardrail, MQTT's permanent exclusion), not
  its adapter-level design, which is a separate concern per that skill.
  **Sequencing tension, flagged rather than silently resolved:** per this
  repo's own convention, a dedicated adapter roadmap doc is normally
  written via Explore mode BEFORE that adapter's own Implement step —
  i.e., this naturally belongs at/before Phase 3's own Design step, not
  after Phase 4 (which only starts once Phase 3 has ALREADY shipped).
  Recorded here exactly where requested; confirm/reorder before Phase 3
  begins if the earlier timing was intended instead.
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
| **Phase 2** — Generalizing the declare-then-verify PATTERN to `api/reqreply` (new `reqreply.CapabilityRequirement`-equivalent + coverage check, even with zero concrete reqreply capabilities shipped today) | Any NEW capability VALUE beyond D-0006's existing survey (Message Expiry, Shared Subscriptions, AMQP addressing/dead-lettering remain exactly as speced in D-0006 section 6 — this doc only sketches what a PRESET for them would look like once/if they ship) |
| **Phase 3** — A NEW, synchronous, transport-stateless ZeroMQ REQ/REP adapter for `api/rest`, governed by the REST-eligible-transport guardrail | Ever giving `api/rest` an MQTT (v3/5) adapter — permanently excluded by design, not deferred; that shape belongs to `api/reqreply` |
| Adapter-owned preset/bundle constructors (e.g. `mqtt5.PresetReliableWithHeaders()`) bundling multiple existing `Capability` values + a matching `CapabilityRequirement` set in one call | Merging/consolidating `api/rest` and `api/reqreply` — considered settled as permanently separate APIs (sync/stateless vs. async/broker-mediated), even where both touch ZeroMQ |
| Compile-time vs. runtime enforcement — explicitly documented as staying RUNTIME (like today) for Phases 1-2; becoming genuinely runtime-checked for REST's implicit capabilities once Phase 3 ships a second transport family | Solving compile-time requirement-composition (would need reflection or a closed capability enum; not attempted) |
| Classifying/documenting EXISTING capabilities (`rest.HeaderParam`/`CookieParam`, `mqtt5.UserPropertyParam`) against the three-tier guardrail, AND naming/documenting the **Baseline** tier (Route/Topic) + the dual (declaring-user / adapter-author) framing for ALL THREE tiers — both descriptive-only, no new code, since Baseline is already enforced via `ports.RESTPattern`/`EventPattern`/`ReqReplyPattern` + `SourceAdapter`/`SinkAdapter`/`IOAdapter` | Redesigning `ports.Pattern`/`SourceAdapter`/`SinkAdapter`/`IOAdapter` themselves — they already do their job; this doc only gives their existing role a name in this vocabulary |

## Sketched API surface

**Phase 1's `api/events` surface is now FINALIZED — see the "Phase 1"
subsection under "Implementation approach" above for the authoritative,
resolved signatures** (`CapabilityRequirement`, `LeveledCapability`,
`CapabilityCoverageError`/`LevelMismatch`, `RequireQoS`/`RequireRetained`/
`RequireHWM`/`RequireConflate`). The sketch below is kept for Phases 2-3,
still speculative:

```go
// api/reqreply — the SAME declare-then-verify pattern events now has
// (post-Phase-1), ported to reqreply's own Route builder. Naming/shape
// TBD during Phase 2's own Design step, informed by Phase 1's learnings
// (see "Open design decisions").
type CapabilityRequirement struct {
	Name        string
	Description string
	MinLevel    *int
}

func CheckCapabilityCoverage(topic string, declared []CapabilityRequirement, supplied []any) error

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
- **ReqReply (Phase 2, still open):** needs its OWN typed error — either
  a `reqreply`-local type (no `api/events` dependency — mirrors why
  `middleware.Disposition` was placed outside `api/events` specifically
  so `api/reqreply` could reuse it without importing `api/events`) or
  literal reuse of `events.CapabilityCoverageError` if a cross-package
  error type turns out to be acceptable once Phase 1 ships. Whichever is
  chosen, the error MUST implement `Error()`/`LogValue()` per this repo's
  structured-error convention (see `codex/errors.go`). Decided during
  Phase 2's own Design step, informed by Phase 1's Learnings.

## Observer integration

- **Events (Phase 1, FINALIZED):** unchanged mechanism —
  `stats.CapabilityObserver.RecordCapabilityApplied` still fires for
  every adapter-supplied capability; value-aware checking happens
  entirely inside `CheckCapabilityCoverage`, upstream of where the
  observer fires. No new observer method needed.
- **ReqReply (Phase 2, still open):** needs the type-assertion guard
  extended to reqreply's own attach path
  (`if capObs, ok := obs.(stats.CapabilityObserver); ok { ... }`), wired
  into whichever reqreply adapters eventually supply capabilities. No
  concrete reqreply capability exists yet (mirrors Disposition's own
  "prove the plumbing before a real capability arrives" precedent from
  D-0006 section 8).

## Unit test plan

**Phase 1's full test matrix is FINALIZED — see its own subsection under
"Implementation approach" above.** Phase 2/3 sketch below, still open:

| Test | Verifies |
|---|---|
| reqreply `CapabilityRequirement`/`CheckCapabilityCoverage` — happy path (all declared requirements matched) | New reqreply mechanism, happy path |
| reqreply `CheckCapabilityCoverage` — missing/insufficient requirement returns typed error with `errors.As` reachable inner error and correct `LogValue()` group | New reqreply mechanism, error path |
| `mqtt5.PresetReliable()`/`PresetReliableWithHeaders()` return the same `SubscribeOptions` a caller would hand-assemble | Preset correctness, no hidden extra behavior |
| `nil` Observer / plain Observer (no `CapabilityObserver`) → no panic on reqreply's new attach path | Observer guard correctness |

## Files to create

**Phase 1's full file list is FINALIZED — see its own subsection under
"Implementation approach" above.** Phase 2/3 sketch, still open:

| File | Responsibility |
|---|---|
| `api/reqreply/capability.go` | `CapabilityRequirement`, `CheckCapabilityCoverage`, capability-related error type (naming TBD during Phase 2's Design step) |
| `adapters/mqtt5/preset.go` | `PresetReliable`, `PresetReliableWithHeaders` (and equivalents for `adapters/mqtt`/`adapters/zeromq` if the pattern proves useful there) |
| `docs/features/capabilities.md` | Updated in Phase 1 already; extended again once Phase 2/3 ship |

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

1. **Where does reqreply's capability-spec type live?** Own package-local
   type in `api/reqreply` (no `api/events` dependency, mirrors
   `middleware.Disposition`'s placement rationale), or does it make more
   sense for both to share one type in a neutral location? Needs a
   concrete reqreply capability candidate (e.g. a future AMQP ack-mode) to
   decide with real evidence, not speculatively.
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
