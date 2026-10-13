# Adapter Integration Architecture: a Unified Roadmap

## Status

**Proposal — architecture investigation and audit plan. No code changes yet.**

This doc replaces and merges two earlier drafts:

- `api-adapter-architecture-roadmap.md` — written by an **external agent**
  as an outside-in architectural proposal, without hands-on familiarity
  with this codebase: a broad analysis of the seam between `api/*`
  declarations and `adapters/*` (binding/capability model), proposing a
  5-phase arc through redesign and conformance tests.
- `adapter-dispatch-unification.md` — written by an agent with **hands-on
  experience working on this codebase**: a narrower, evidence-grounded
  audit of dispatch-path duplication *inside* `adapters/mqtt`,
  `adapters/mqtt5`, and `adapters/zeromq`, deliberately scoped to "audit
  only, no redesign yet."

Both documents analyze the same underlying pain point — the adapter layer —
from different altitudes. They are not in conflict; one is the general case,
the other a concrete, cited instance of it. This doc merges them into one
coherent plan, broadens the audit to **all** adapters, and sequences the
work explicitly: audit → design → proof-of-concept → implementation →
conformance tests, with no phase skipped.

### Constraint: backward compatibility is not a goal

go-codex currently has a single consumer — its author. Backward
compatibility is therefore **not a constraint** on this roadmap. Where the
audit and design phases show that the cleanest architecture requires
breaking changes to adapter-facing contracts, or even to some public API,
that is acceptable and expected as part of Phase 4 implementation. This
roadmap should optimize for the clearest, simplest, most maintainable
result rather than hedge against or deprecate around compatibility concerns
that do not currently exist. (This may need revisiting if/when go-codex
gains other consumers, but it does not constrain this round.)

## Motivation

The current design direction in go-codex is promising: users declare
codecs, routes/channels, and middleware; the library derives runtime
behavior and machine-readable specifications from those declarations. A
retrospective review of this project's own design-doc history shows the
`api/*` declaration layer (codecs, builders, opts) has stayed comparatively
clean — evidenced by how quickly
[`d-0008-declarative-router-groups.md`](../design/d-0008-declarative-router-groups.md)
closed by reusing already-validated primitives at every step.

The recurring, expensive failure mode has instead been in the
**adapter-layer dispatch plumbing**, confirmed directly from this project's
own design docs (not re-derived from scratch):

- **`d-0001-rest-middleware-workflow-simplification.md`'s "Lessons
  Learned"** (7 numbered findings): a reflection-based dispatch mechanism
  claimed "equivalent" to the old one silently was NOT (missing Formats
  negotiation, missing `ErrorResponseFor`) — undetected until 128+ old tests
  were migrated wholesale onto the new entry point.
- **`d-0007-declarative-middleware-layering.md`'s "Post-ship regression
  found and fixed"**: events' Subscribe dispatch had **three** separately
  maintained reflection-based implementations — `adapter.go`'s generic
  `makeSubscribeMessageHandler[T]` (exercised only by the low-level escape
  hatch and unit tests), `transport.go`'s reflection-based `Subscribe` (the
  `ports.Pattern` binding path), and `caller.go`'s (mqtt5/mqtt) /
  `serve_subscribers.go`'s (zeromq) reflection-based `ServeSubscribers` —
  the actual, documented, recommended `Client.Attach` workflow every
  example/tutorial tells a user to use. That third path had no
  `MiddlewareHandlers` dispatch of any kind, silently, for the entire
  lifetime of Rollout Phase B — found only when a new example was written
  specifically to exercise that exact entry point. The doc's own words:
  "this is the third time this exact class of gap has surfaced in this
  doc's own history."

This recurring pattern — a capability correctly designed at the `api/*`
layer, but silently missing from one of several independently-maintained
adapter dispatch paths, undetected until a real consumer exercises that
exact entry point — is the concrete, named problem this roadmap
investigates, alongside the broader architectural question of how an
adapter should consume `api/*` declarations through a small, clear,
consistent integration contract in the first place.

## Problem statement

The API layer should make declaring communication contracts easy. Adapters
should make those contracts usable over concrete transports such as HTTP,
MQTT, ZeroMQ, Redis, and WebSocket. These responsibilities are different:

- **API declarations** describe the semantics and requirements of an
  operation or channel.
- **Adapters** translate those semantics into a transport's native
  mechanisms.
- **Capabilities** express whether a transport can satisfy the
  declaration's requirements.
- **Middleware** defines processing around a communication operation; it is
  not itself a transport capability.

Two distinct failure classes can occur when this separation is unclear or
unenforced:

1. **External-seam failure** — an adapter has to reconstruct API-layer
   internals (codec behavior, field discovery, validation, middleware
   ordering) because there is no small, stable, adapter-facing contract to
   consume instead.
2. **Internal-seam failure** — *within one adapter*, multiple independent
   entry points (a compile-time-generic path, a reflection-based path, a
   per-protocol file) each reimplement the same capability's dispatch logic
   separately, so a capability can be correctly wired into one path and
   silently absent from another.

The integration model should make the first failure class structurally
unrepresentable where practical, and should give adapter authors one shared
place to implement a capability's dispatch logic so the second failure
class cannot recur by construction.

## Comparing the two source drafts

This section captures the comparison explicitly, so the reasoning behind
the merge is traceable.

### Overlaps

- Both locate the pain point in the adapter layer, not `api/*` (Doc B says
  so explicitly; Doc A's whole premise assumes `api/*` declarations are
  already in good shape).
- Both insist on auditing the current `main` branch before any redesign
  (Doc A's Phase 1; Doc B's entire scope).
- Both want a capability/adapter matrix as the audit's deliverable —
  different axes, but mergeable into one artifact (see Phase 1 below).
- Both treat `adapters/nethttp`'s single-reflection-path REST dispatch
  (`serve.go`'s `ServeOne`/`Serve` reflecting into `RouteHandle`'s own
  generic methods) as the reference pattern other adapters should converge
  toward.
- Both explicitly refuse to commit to a specific redesign technique
  (reflection vs. codegen, a new `Binding` type vs. reusing existing
  handles) before the audit is done.
- Both want conformance/regression tests that prove per-entry-point
  coverage, not merely per-capability coverage.

### Contradictions / differing designs

- **Scope breadth.** Doc A targets all adapters and all API families
  (routes/channels/reqreply/tools). Doc B deliberately narrows to three
  named adapters (`mqtt`, `mqtt5`, `zeromq`), explicitly deferring
  `websocket`/`redis`/`file`/`sql` until "a driver appears."
- **Problem framing.** Doc A frames the gap as the *external seam*: what
  information crosses the boundary between an `api/*` declaration and an
  adapter (a `Binding` concept, capability requirements, an
  `Attach`/`Bind` lifecycle). Doc B frames the gap as an *internal-to-adapter*
  DRY failure: one adapter, multiple dispatch entry points (a
  compile-time-generic one and one or more reflection-based ones)
  independently reimplementing the same capability's logic.
- **Commitment depth.** Doc A's roadmap already spans investigation through
  redesign and conformance tests (5 phases, ending in acceptance criteria
  for a changed architecture). Doc B hard-stops at "audit only, no code
  changes proposed," explicitly deferring design and implementation to "a
  follow-up round." **Resolved:** this merged roadmap follows Doc A's full
  depth — audit through redesign and conformance tests — rather than Doc
  B's hard stop. That commitment is practical here specifically because
  backward compatibility is not a constraint (see "Constraint: backward
  compatibility is not a goal" above); there is no sole-consumer risk that
  would otherwise argue for stopping at audit-only.
- Doc A never engages with Doc B's concrete regression evidence (the
  3-implementation Subscribe bug). Doc B never engages with Doc A's
  Binding/capability abstraction at all.

### Resolution

Doc B's dispatch-duplication problem is a concrete, narrower instance of
exactly the general failure Doc A's binding/capability seam is meant to
prevent: no single place owns "how a capability is dispatched," so it gets
reimplemented per entry point. The fix is to apply Doc A's Binding concept
at **two** seams instead of one:

1. **External seam** (Doc A's original focus): `api/*` declaration →
   capability-checked → adapter `Attach`/`Bind`.
2. **Internal seam** (Doc B's focus): inside one adapter, every dispatch
   entry point — whether it starts from a compile-time-known generic `T` or
   recovers `T` via reflection against a type-erased handle — must call
   through one shared dispatch-core function per capability, instead of
   reimplementing that capability's logic independently.

Framed this way, the two documents describe the same disease at two
different scales. A single audit phase can produce both matrices at once,
and a single design phase can specify both the external capability contract
and the internal dispatch-core consolidation together, because the second
is a structural consequence of getting the first right: if the API-facing
binding exposes one small, well-typed surface describing "what must be
dispatched," an adapter has no reason to grow three independent
reimplementations of "how" — there is exactly one binding to read from, and
the adapter's job is to make every entry point call through its own single
internal translation of that binding.

## Confirmed asymmetry (carried forward from Doc B)

| Pattern | Dispatch paths confirmed | Converged? |
|---|---|---|
| `api/rest` (`adapters/nethttp`) | ONE — `serve.go`'s `ServeOne`/`Serve` reflects (`reflect.Value.Call`/`MethodByName`) directly into `RouteHandle[Req,Resp]`'s own generic, type-safe methods (`ApplyMergeFields`, `DecodeMerged`, `EncodeResponseMergeFields`, `ErrorStatusFor`, etc.) — confirmed via its own doc comment: "runs the SAME pipeline... invoked via `reflect.Value.Call`." | **YES** — the reference pattern to converge toward |
| `api/events` Subscribe (`adapters/mqtt`/`mqtt5`/`zeromq`) | THREE, confirmed directly from `d-0007`'s own regression writeup | **NO** — confirmed broken, since patched for the specific gap that surfaced it, but the 3-implementation structure itself was never consolidated |
| `api/events` Publish | Not yet audited — `adapters/mqtt5/transport_dispatch.go`'s own doc comment confirms some ad hoc convergence already happened ("the SUBSCRIBE-side equivalents ALREADY EXIST in caller.go... this file reuses them directly rather than duplicating"), but the exact count/location for Publish specifically is unconfirmed | **UNKNOWN** |
| `api/reqreply` Call/Serve (`adapters/mqtt5`/`zeromq`) | Structurally separate files from events' own dispatch (`reqreply_transport.go` vs. `transport.go`) per adapter, confirmed via file listing — whether reqreply's own Call/Serve paths share events' Subscribe shape is unconfirmed | **UNKNOWN** |
| All other adapters (`nethttp`'s non-REST uses, `chi`, `websocket`, `redis`, `file`, `sql`, `mcpgo`, `mcprest`, `openai`, `templ`) | Not yet audited under this lens at all | **UNKNOWN — now in scope** (broadened from Doc B's 3-adapter limit; see Phase 1) |

### Why `d-0008`'s Router is not at risk of this regression class

Confirmed structurally, not just asserted: Router (all three patterns,
fully designed) is purely an `api/*`-layer, spec-assembly-time construct —
its own "Observer integration" section states plainly that "once
`Register()` succeeds, the resulting handle is indistinguishable from one
built without a Router at all" and "every existing Observer call site
downstream (adapter dispatch) is completely unaware a Router was ever
involved." Router never touches dispatch, so it cannot introduce a fourth
independently-maintained dispatch path, or silently miss wiring a
capability into one of the existing ones — it composes `opts`/prefixes
before any existing dispatch path ever runs, using each leaf's own,
completely unchanged `Register`/`Handle` method.

## Preliminary evidence: validating Doc A's model against current code

**Status: targeted, non-exhaustive.** This is not the formal, all-adapter
Phase 1 audit (Round 1 decision 4 still commits to that as a strict
waterfall before Phase 2 design work). This is preparatory grounding of
Doc A's conceptual claims against `api/rest`+`adapters/nethttp`,
`api/events`+`adapters/mqtt`/`mqtt5`/`zeromq`, `api/reqreply`+
`adapters/mqtt5`/`zeromq`, the `d-0006` capability mechanism, and the
`ports.Pattern` seam — done specifically to inform the two decisions Round
1 left deferred (binding type; dispatch-core mechanism) with real evidence
instead of none.

### REST (`api/rest` + `adapters/nethttp`)

- **Binding role — confirmed.** `handlerFunc`/`buildRouteHandler`
  (`adapters/nethttp/adapter.go`, `serve.go:237+`) call `RouteHandle`'s own
  methods directly (`MergeFields`, `ValidateQuery*`, `DecodeMerged`,
  `ErrorStatusFor`, etc.) rather than reconstructing codec/validation/merge
  logic. The reflection-based heterogeneous-route path
  (`buildRouteHandler`) calls the exact same method names via
  `reflect.Value.Call`/`MethodByName`.
- **Lifecycle — confirmed, closer than expected.** Real verbs:
  `NewRoute` (declare) → `Route.Register`/`RegisterHandle` (register/compile)
  → `Server.Attach(ServerTransport)` (literally named "Attach",
  `api/rest/builder.go:3081`) → `Server.Serve` (run). The old
  `nethttp.Serve(mux, builder)` entry point was removed once `Attach`+
  `Serve` shipped (per `d-0002`'s Decision 6).
- **Small interfaces, no universal Adapter — confirmed.** No
  `Adapter{ Attach(any) error }` shape anywhere; only narrow interfaces
  (`ServerTransport`, `ServerAwareTransport`, `ClientTransport`,
  `HeaderCapableTransport`/`CookieCapableTransport`/`QueryCapableTransport`).
  `adapters/nethttp` defines zero interfaces of its own — only concrete
  generic functions/types.
- **Exported-method audit — partially confirmed.** `RouteHandle` has ~30
  exported methods, nearly all genuinely called by `adapters/nethttp` (not
  accidental/unused surface) — but the surface is large, not "small." A
  legitimate critique of granularity, not of duplication.

### Events (`api/events` + `adapters/mqtt`/`mqtt5`/`zeromq`)

- **Binding role — confirmed.** `ChannelHandle[T]` exposes
  `MergeFields`/`PropertyMergeFields`/`DispatchSubscribeMiddleware`/
  `DispatchPublishMiddleware`/`DecodeMerged` plus `Requirements`/
  `Implementations`. `adapters/mqtt5/adapter.go`'s `subscribeWithHandle`
  (the real dispatch path shared by the documented `Client.Attach`
  workflow) calls these directly.
- **Lifecycle — confirmed, including a literal "Attach."**
  `NewChannel` (declare) → `Subscriber.Register`/`Router.Register`
  (register/compile) → `Client.Attach(Transport)` — literally named
  "Attach" (`api/events/builder.go:3040`), with an optional
  `ClientAwareTransport.BindClient` hook that can reject at bind-time → run
  via `Publish`/`Subscribe`/`ServeSubscribers`.
- **Capability check at attach-time — partially confirmed, and Doc A's own
  worked example is wrong.** The generic `CapabilityRequirement`/
  `CheckCapabilityCoverage` mechanism genuinely works for QoS/Retained/
  HWM/Conflate (declare → check → actionable `CapabilityCoverageError`).
  But **MQTT5 User Properties — Doc A's specific worked example — are
  explicitly *not* modeled through this mechanism.** They use a separate,
  pre-existing `UserPropertyParam` mechanism, validated per-message inside
  `caller.go`, not at attach time — and `adapters/mqtt` (v3) doesn't even
  have the `UserPropertyParams` type, so declaring it against v3 would be a
  **Go compile error**, not the clean runtime `CapabilityCoverageError`
  Doc A describes. The adapter's own code comment
  (`adapters/mqtt5/capability.go:100-104`) says folding User Properties
  into the sealed Capability mechanism is "left for a future round." Doc
  A's general model (declare → attach-time check → actionable error) is
  correct for the capability-requirement family that already exists; its
  chosen illustrative example is not currently accurate.
- **Capability checks happen per-call-site, not at one unified boundary —
  new finding.** `CheckCapabilityCoverage` is invoked separately on the
  publish path (`adapters/mqtt5/transport.go:358`) and the subscribe path
  (`adapters/mqtt5/caller.go:122`), not once at a single attach/registration
  gate. A code comment at `transport.go:344-349` confirms this was a
  **previously-missing gap on publish** (coverage existed only on subscribe
  until fixed) — a **third** confirmed instance of this roadmap's core bug
  class (alongside the Subscribe-middleware regression and `d-0005`'s
  ErrorPattern H1/H2 gap).
- **Small interfaces, no universal Adapter — confirmed.**

### Reqreply (`api/reqreply` + `adapters/mqtt5`/`zeromq`)

- **Binding role — partially confirmed.** `RouteHandle[Req,Resp]`
  (`api/reqreply/route.go:1419`) carries codec + `Requirements`, read via
  reflection by adapter transports rather than reconstructed. But
  middleware/`Implementations` live adjacent to the handle (on the
  route/builder), not bundled onto `RouteHandle` itself — a partial, not
  total, match to Doc A's "binding carries everything" framing.
- **Lifecycle — confirmed.** `Route.Register` → `RouteHandle` →
  `Server.Attach`/`Client.Attach` (literal verbs, `api/reqreply/builder.go:462`,
  `client.go:370`) → `Serve`/`Call`, plus escape hatches
  `ServeWithTransport`/`CallWithTransport`.
- **Middleware kept separate from capabilities — confirmed.** Distinct
  calls: `CheckCoverage`/`applyGeneralServerMiddleware`/
  `runServerSecurityMiddleware` (middleware) vs. `VerifyCapabilityCoverage`/
  `ApplyCapabilities` (capabilities) — separate functions, separate call
  sites.
- **Duplication shape — refuted as "same as events," actually worse.**
  reqreply has **six** independent capability-coverage-check call sites
  (mqtt5 Serve + Call, zeromq plain-Serve + ROUTER-Serve + plain-Call +
  DEALER-Call) — wider than events' confirmed 3-implementation Subscribe
  shape, because zeromq alone has two server socket variants and two client
  socket variants. Correlation handling itself genuinely differs by
  protocol (MQTT5 `CorrelationData` vs. ZeroMQ frame/identity), so that
  part is not spurious duplication — but the repeated coverage-check
  plumbing is. Only the middleware-wrapping helper
  (`applyGeneralServerMiddleware`) is shared across zeromq's two server
  variants; nothing is shared across the mqtt5/zeromq boundary.
- **Small interfaces, no universal Adapter — confirmed.**

### Capability mechanism (`d-0006` / `api/events/capability.go`)

- **Boolean vs. leveled capabilities — partially confirmed.** The code
  does distinguish presence-only (`Retained`, `Conflate`) from leveled
  (`QoS`, `HWM` via `LeveledCapability.Level()`/`MinLevel`) capabilities.
  But it supports only a flat `>=` minimum-level comparison
  (`CheckCapabilityCoverage`) — Doc A's richer taxonomy (exact-level,
  weaker-form-satisfiable, transport-guaranteed vs. adapter-approximated,
  observable-to-application vs. implementation-detail-only) is **not
  implemented**. Any future capability-model work has a concrete, narrower
  starting point than Doc A's full taxonomy implied.
- **Capability requirement vs. adapter configuration distinction — held.**
  Spot-checked `adapters/mqtt5/binding.go`/`adapter.go` option structs;
  `Capabilities []Capability` is always a separate field, never merged with
  mechanical config (`UserPropertyParams`, `TopicFilter`). No violation
  found.
- **Observer/Disposition relationship — confirmed, and distinct in time.**
  `stats.CapabilityObserver.RecordCapabilityApplied` fires at
  Apply-time (mechanical wire application), before the coverage check;
  `stats.Disposition`/`DispositionObserver` fire after handler execution —
  a third, unrelated point in the pipeline. The three signals
  (Apply, coverage-check, Disposition) are temporally and conceptually
  distinct today, confirming they are not already conflated (a desirable
  property Phase 2 design should preserve).

### `ports.Pattern` seam (feeds Round 1 decision 3)

- **Already unified at the handle/codec layer — confirmed, stronger than
  expected.** `ports/handle.go`'s `EventPattern` path calls the *exact
  same* `events.NewChannel`/`ChannelHandle` constructors the direct
  `api/events` path uses (its own doc comment: "the SAME calls a
  hand-declared channel makes"). In `adapters/mqtt5`, both the direct
  `api/events` path and the `ports.Pattern`/`Bind` path
  (`SubscribeAdapter`/`PublishAdapter`) consume the identical
  `*events.ChannelHandle[T]` and call the identical shared dispatch helpers
  (`makeSubscribeMessageHandler`, `publishHandle`). Round 1 decision 3's
  "unified Binding/capability type family" is, at this layer, **already
  true in practice** for events/mqtt5 — formalizing it would mostly mean
  naming/documenting an existing convergence, not inventing a new one.
- **Genuine structural difference remains at the outer lifecycle layer.**
  `ports.SourceAdapter`/`SinkAdapter`/`IOAdapter` are long-running,
  `Activate(ctx, ...)`-based, fan-in/fan-out constructs bound via `Bind` —
  with no counterpart in direct `api/events` usage (a single synchronous
  `Subscribe`/`Publish` call, no adapter-interface indirection). `Pattern`
  itself is a closed, sealed spec-declaration concern (9 known variants),
  separate from the adapter-binding-time concern.
  `file`/`redis`/`sql` reinforce this: their bindings implement
  `SourceAdapter`/`SinkAdapter`/`IOAdapter` directly, not `ports.Pattern` —
  `Pattern` is purely a declarative spec layer consumed once at
  handle-construction time, not what adapters bind against.
- **Net assessment:** a unified type family is mechanically plausible at
  the handle/codec/capability layer (already de facto true for at least
  one adapter), but must still reconcile three distinct things — Pattern
  specs, built Handles, and the `SourceAdapter`/`SinkAdapter`/`IOAdapter`
  lifecycle interfaces — not just two. Round 1 decision 3 ("unified")
  stands, informed rather than contradicted by this finding.

### What this evidence resolved (see "Resolved design decisions", Round 2)

The evidence above was enough to resolve both decisions Round 1 had left
open — per pattern family, not with one universal answer. See Round 2
below for the actual resolutions and rationale. Summary of the evidence
that drove them:

- **Binding type:** every pattern family's existing registered handle
  (`RouteHandle`, `ChannelHandle`, reqreply's `RouteHandle`) already
  functions as a working "binding" by Doc A's own definition — adapters
  consistently call into the handle's own methods rather than
  reconstructing logic. The concrete complaints are granularity (REST's
  ~30-method surface) and reqreply's split (middleware lives beside, not
  on, the handle) — both granularity/co-location problems, not
  missing-capability problems, and they differ by family.
- **Dispatch-core mechanism:** the duplication problem is confirmed
  **worse** than previously known: reqreply has 6 independent
  coverage-check call sites (not 3), and a third real regression instance
  was found (capability coverage missing on mqtt5's publish path until
  fixed) beyond the two already cited (events-Subscribe middleware,
  `d-0005`'s ErrorPattern H1/H2). The shapes differ structurally even
  within reqreply across adapters — zeromq's socket-variant duplication
  (ROUTER/DEALER) is a different shape from mqtt5/zeromq's protocol-level
  correlation divergence (MQTT `CorrelationData` vs. ZMQ framing) — so a
  single mechanical answer across pattern families would not fit the
  evidence.
- **New, separate finding:** capability-coverage checks run per-call-site
  today (once on publish, once on subscribe), not at one unified
  attach/registration gate — confirmed independently of the
  how-many-implementations duplication problem above. This is now its own
  Phase 2 goal (Round 2, decision 4).

## Resolved design decisions

Gaps surfaced and resolved so far, in resolution order. Each entry records
the decision and why, so the reasoning stays visible without re-deriving it.
Narrower gaps not yet raised remain open; see "Open design points deferred
to future rounds" below for the ones explicitly parked.

**A critical pass (below, after Round 2) reviewed every entry for pros and
cons rather than just restating them.** Two entries changed status as a
result: Round 1 decision 3 narrowed from a blanket claim to a conditional
one, and Round 2 decisions 1 and 2 are marked **provisional** — evidence-backed
enough to act as working assumptions, but explicitly revisit-if-contradicted
once the full Phase 1 audit (Round 1 decision 4, still a strict waterfall)
completes. This keeps the record honest about the difference between "fully
settled" and "best current evidence, not yet exhaustively checked."

### Round 1 — foundational gaps (shape every later decision)

1. **Adapter-facing binding: reuse existing handles, or a new public
   `Binding` type?** — **Deferred, deliberately left open, no default
   lean.** The new-type idea originates entirely from Doc A, the external,
   outside-in analysis — Doc A's own text already treats it as an
   unvalidated hypothesis ("First determine whether the existing handles
   can serve this role..."). Doc B, written with hands-on codebase
   experience, never engages with this question at all; its evidence (the
   3-implementation Subscribe bug) is orthogonal to whether a new type
   exists. With no hands-on evidence yet pointing either way, this decision
   is deferred to Phase 1 audit findings, and may legitimately differ per
   pattern family (REST vs. events vs. reqreply vs. ports).
2. **How is "one dispatch-core per capability" enforced?** (new shared
   helper package vs. per-adapter-local convention) — **Deferred to Phase 1
   evidence.** Confirmed only that an audit must run first; a mechanism
   choice before seeing the actual per-adapter entry-point shapes
   (compile-time-generic vs. reflected) would be premature.
3. **One Binding/capability model for `api/*` and `ports/*`, or two
   parallel models?** — **Resolved: conditionally unified, narrowed by the
   critical-review pass.** Originally recorded as a blanket "unified."
   The Doc A evidence-grounding pass found this true for exactly one
   adapter (mqtt5: the `ports.Pattern`/`Bind` path and the direct
   `api/events` path already share the identical `ChannelHandle` and
   dispatch helpers). But `file`/`redis`/`sql` — the adapters that are
   *primarily* ports-only — don't implement `ports.Pattern` at all; they
   implement `SourceAdapter`/`SinkAdapter`/`IOAdapter` directly, with
   `Pattern` sitting one layer above, untouched by their bindings. The
   decision is therefore narrowed to: **unified where evidence shows
   convergence** (events-capable adapters, confirmed for mqtt5);
   **explicitly undecided** for pure-ports adapters (`file`/`redis`/`sql`)
   pending the full Phase 1 audit. This does not reopen "Preserve the
   distinction between API contracts and application ports" below: the two
   *integration paths* (`Register`/`Handle` for `api/*`, the `ports.Pattern`
   `Bind` path for `ports/*`) remain explicit in naming and documentation,
   per that section — only the underlying Binding/capability type family is
   shared where unification is confirmed, not the integration path's
   meaning, and not (yet) a claim about every `ports` adapter.
4. **Phase 1 execution sequencing: audit everything first, or iterate per
   pattern family?** — **Resolved: strict waterfall.** The full audit
   (both matrices, across every adapter package, both seams) completes
   before any Phase 2 design work starts on any pattern family. No
   partial/iterative overlap between Phase 1 and Phase 2.

### Round 2 — resolved using the Doc A evidence-grounding pass

These revisit Round 1 decisions 1 and 2 now that the "Preliminary evidence"
section above gives concrete, per-family signal, rather than leaving them
deferred indefinitely.

1. **Decision 1 (binding type), refined — *provisionally* resolved *per
   pattern family*, not universally.** REST's registered handle needs a
   smaller adapter-facing *view* (its ~30-method surface is a genuine
   granularity problem, confirmed by evidence); events' `ChannelHandle` is
   fine as-is (already confirmed working, no changes needed); reqreply
   needs its capability requirements and middleware/`Implementations`
   co-located on or directly alongside the handle (currently split across
   the handle and the adjacent route/builder). No wholesale new `Binding`
   type is adopted for any family — in every case the fix is narrower
   (a view, or co-location), not a replacement type. **Provisional:**
   this was decided from a preliminary, non-exhaustive evidence pass (3 of
   ~13 adapter packages); revisit if the full Phase 1 audit contradicts it.
   **Guardrail (critical-review pass):** per-family does not mean
   undocumented-per-family — Phase 2 must still produce one documented,
   consistent adapter-facing *contract shape* (e.g. "declare + requirements
   + middleware-handle, always three named parts") even though each
   family's concrete type differs, so this doesn't quietly recreate the
   roadmap's core cross-adapter-asymmetry complaint one layer up, at the
   binding level instead of the dispatch level.
2. **Decision 2 (dispatch-core mechanism), refined — *provisionally*
   resolved *per pattern family*, not universally.** Events and reqreply
   get separate consolidation designs in Phase 2/4, not one shared
   cross-pattern-family mechanism: reqreply's duplication is both worse (6
   call sites vs. 3) and differently shaped (zeromq's extra ROUTER/DEALER
   socket variants have no events counterpart), so forcing one scheme
   across both would not fit the evidence. Still open within each family:
   shared-helper-package vs. per-adapter-local convention — that narrower
   choice is deferred to Phase 2 for each family independently.
   **Provisional:** same caveat as decision 1 above — only 2 of ~13 adapter
   packages were sampled; revisit if the full audit contradicts it.
   **Guardrail (critical-review pass):** if the full Phase 1 audit shows
   3+ pattern families each needing a genuinely distinct dispatch-core
   mechanism, that is itself a flagged risk requiring explicit sign-off
   before Phase 4 begins — "per pattern family" is a floor meant to avoid
   forcing one ill-fitting universal mechanism, not an invitation to drift
   into N independently-designed mechanisms with no shared principle at
   all.
3. **Capability-coverage check location — new, explicit Phase 2 goal.**
   Distinct from the dispatch-core duplication problem above (which is
   about HOW MANY implementations exist), this is about WHERE the check
   runs: today it executes per-call-site (separately on publish and on
   subscribe), not once at a single attach/registration gate. Phase 2 must
   design a consolidated capability-check point as its own goal, alongside
   (not folded into) dispatch-core consolidation. **Open question flagged
   by the critical-review pass:** this assumes a single attach-time gate is
   always sufficient, which is not yet verified — some capability-relevant
   values may legitimately be overridable per-call (e.g., a caller setting
   QoS on an individual publish call rather than only at channel-declare
   time), and a single attach-time check could miss such an override
   violating the declared requirement. Phase 2 must explicitly check
   whether per-call capability overrides exist anywhere in scope before
   treating "one gate" as unconditionally sufficient.
4. **Doc A's MQTT3/MQTT5 user-properties worked example — corrected.** It
   does not work as originally described (see "Preliminary evidence" and
   the corrected "Capabilities are requirements" section above). QoS/HWM
   now serve as the primary, accurate illustration of the existing
   mechanism; MQTT user properties are recorded as a named, open gap to
   close in Phase 2/4, after which the original scenario (MQTT 5 accepted,
   MQTT 3 rejected with an actionable error) becomes a valid Phase 3 PoC
   case rather than a presently-false claim.

## Design goals

1. Preserve the simple, declarative user experience of `api/rest`,
   `api/events`, `api/reqreply`, and other API packages.
2. Keep API declarations transport-neutral.
3. Make adapter integration predictable and discoverable for library
   contributors, for both the external seam and the internal
   dispatch-entry-point seam.
4. Detect unsupported requirements when a contract is attached or
   registered, rather than failing unexpectedly during communication.
5. Avoid a universal adapter interface that grows whenever a new API
   concept is added.
6. Avoid forcing unrelated communication patterns into one abstraction.
7. Keep transport-specific configuration in the adapter layer.
8. Preserve type safety and useful compile-time guidance where practical.
9. Keep the adapter-facing contract small and stable enough to evolve
   deliberately.
10. Support adapters that implement only a subset of the API and port
    patterns.
11. Ensure that within one adapter, every dispatch entry point for a given
    capability resolves to exactly one implementation of that capability's
    logic — by construction, not by code review discipline.
12. Check capability coverage at exactly one point in the attach/
    registration lifecycle per binding, not separately at each runtime
    call site (publish, subscribe, call, serve, ...). (Added in Round 2,
    informed by evidence that today's checks run per-call-site — see
    "Resolved design decisions.")

## Proposed conceptual model

```text
Codec
  |
  v
API declaration (Route / Channel / Request-Reply / Tool)
  |                         \
  | semantic contract        \ required capabilities
  |                           \
  +-------------+-------------+
                |
                v
       Adapter-facing binding
                |
                v
       Capability compatibility check
                |
                v
             Adapter
         /      |       \
        v       v        v
   Entry pt A  Entry pt B  Entry pt C   <- e.g. generic `adapter.go`,
        \       |        /                 reflected `transport.go`,
         \      |       /                  protocol file `caller.go`
          v     v      v
       ONE shared dispatch-core
       per capability, inside the
       adapter (internal seam)
                |
                v
       Native transport behavior
```

A binding is the adapter-facing representation of what needs to be executed
or transported. It should expose the operations and metadata the adapter
needs without requiring it to reconstruct API declaration internals.
Capabilities complement the binding; they do not replace it.

- **Binding:** What must the adapter do with this declaration?
- **Capability requirements:** What must be supported for this declaration
  to be valid on the chosen adapter?
- **Adapter translation:** How are the declaration's semantics implemented
  using the native transport, and — critically — through how many
  independently-maintained code paths inside that adapter?

This is a conceptual model. It does not require introducing a new public
`Binding` type if the current registered handles already provide the right
boundary. First determine whether the existing handles can serve this role
with a smaller, more deliberate adapter-facing surface, and whether the
existing per-adapter entry points can be restructured to share one internal
dispatch-core without a new public type at all.

## Capabilities are requirements, not transport options

The existing capability approach (shipped per
[`d-0006-protocol-native-capabilities.md`](../design/d-0006-protocol-native-capabilities.md))
should be retained and evaluated as a central part of the design.

A declaration can require semantic features that not every transport
supports. For example, an event channel may require a minimum QoS level, or
a ZeroMQ high-water-mark guarantee. A binding that requires one of these
capabilities should therefore be rejected for an incompatible adapter
before it is activated — and this already works today:
`RequireQoS`/`RequireHWM` declare the requirement, `CheckCapabilityCoverage`
checks it, and an incompatible adapter produces a structured
`CapabilityCoverageError` (see "Preliminary evidence" above).

```text
Channel declaration
  requires: QoS >= 1
       |
       v
  compatibility check
       |
       +---- adapter without QoS>=1: unsupported -> clear error
       |
       +---- adapter with QoS>=1:    supported   -> attach
```

The declaration should express the semantic requirement, not name a
particular adapter. Prefer a protocol-neutral concept such as a QoS level
over a requirement that directly names a transport, unless the API is
intentionally exposing a protocol-specific extension.

**Known gap, not yet covered by this mechanism: MQTT user properties.**
Doc A's original worked example used "an event channel may require user
properties; MQTT 5 supports them, MQTT 3 does not" as its illustration of
this model. The "Preliminary evidence" audit above found this example does
**not** currently work as described: MQTT user properties are declared and
validated through a separate, pre-existing `UserPropertyParam` mechanism,
not through `CapabilityRequirement`/`CheckCapabilityCoverage` — and
`adapters/mqtt` (v3) doesn't even have the type, so attempting the scenario
today is a **Go compile error**, not the clean runtime
`CapabilityCoverageError` this model promises. This is recorded here as a
concrete, named gap: folding MQTT user properties into the sealed
Capability mechanism (so the scenario works exactly as Doc A originally
described) is in scope for Phase 2/4, not yet done.

### Capability requirements versus adapter configuration

These concepts must remain distinct.

- **Requirement:** "This contract needs user properties."
- **Adapter configuration:** "Configure this MQTT 5 binding with these
  concrete user-property values or mapping rules."

Requirements determine compatibility. Adapter options configure how a
compatible adapter operates. An option must not silently imply that an
unsupported transport can meet a semantic requirement.

### Boolean and parameterized capabilities

Not every capability is a simple yes/no feature. Some have values or
constraints: QoS levels, maximum payload size, delivery guarantees,
ordering, streaming, duplex communication, or transactional behavior.

The model should support these only where there is a concrete use case.
Avoid designing a general constraint solver prematurely. Start with the
capability forms already shipped (QoS/Retained/HWM/Conflate per
`d-0006`), and ensure their semantics stay explicit:

- Is a capability merely present?
- Does the declaration specify a minimum or exact level?
- Can the adapter satisfy a weaker form, or must attachment fail?
- Is the feature guaranteed by the transport, or approximated by the
  adapter?
- Is the feature observable to the application, or merely an
  implementation detail?

A capability should not be advertised as supported if the adapter cannot
uphold the declared semantics.

## Binding and attachment

The adapter needs a clear operation for accepting a declaration or its
registered runtime representation. Names such as `Bind`, `Attach`, or
`Register` are possible; consistency and semantics matter more than the
particular word.

The lifecycle should be explicit:

1. **Declare:** The user creates a route, channel, or other API contract.
2. **Register/compile:** The API layer validates and prepares the runtime
   contract, including codecs, middleware, errors, and requirements.
3. **Attach/bind:** The adapter checks compatibility, resolves its own
   internal dispatch-core for each required capability, and installs or
   activates the contract on its transport.
4. **Run/close:** The adapter owns transport-specific runtime lifecycle
   according to its documented contract.

Do not introduce an additional binding compilation stage if the current
`Register` result already serves as the correct adapter-facing
representation. Prefer reusing and refining existing concepts over adding
another layer of types solely for naming consistency.

### Keep the adapter contract focused

An adapter should receive enough information to execute the declaration,
but should not need to repeat API-layer work such as:

- reconstructing codec behavior;
- rediscovering path, query, header, cookie, or payload fields;
- reimplementing API-level validation and middleware ordering;
- independently deriving error contracts or specification metadata;
- inferring semantic requirements from transport-specific options.

The API layer should own transport-neutral interpretation. The adapter
should own transport mechanics and translation — through exactly one
internal implementation per capability, regardless of how many entry points
that adapter exposes (generic vs. reflected, escape-hatch vs. documented
workflow).

At the same time, avoid turning a handle into a huge public surface merely
because one adapter currently needs access to many internal helpers. Audit
each exported method and classify it as:

- essential public API;
- adapter-facing contract;
- internal implementation detail.

Only make adapter-facing details public when third-party adapters genuinely
need them. Consider a smaller stable view or interface if it can reduce
coupling without creating needless indirection.

## Prefer small semantic interfaces over a universal adapter

Avoid a single interface such as `Adapter{ Attach(any) error }`: it loses
type information and provides little compile-time guidance. Also avoid a
universal interface that requires every adapter to implement every API
family, such as routes, events, request/reply, tools, and streaming.

Prefer interfaces aligned with the communication patterns an adapter
actually supports. An HTTP adapter might bind REST request/response
contracts and streaming responses; an MQTT adapter might bind event and
request/reply contracts. The exact interfaces should follow real use cases
and current package boundaries, not an assumed taxonomy.

An adapter may implement multiple interfaces. Unsupported patterns should
be unrepresentable where practical, or rejected clearly at attachment time.

## Keep middleware separate from capabilities

Middleware describes processing behavior; capabilities describe whether the
adapter can satisfy a requirement.

- Capability: the transport/binding can carry correlation metadata.
  Middleware: generate, validate, or propagate a correlation identifier.
- Capability: a binding supports streaming.
  Middleware: authorize or instrument each message in a stream.

Some middleware may depend on a capability. If so, validate that dependency
explicitly, but do not collapse middleware and capability declarations into
one mechanism. This separation is also what makes dispatch-core
consolidation tractable: a shared dispatch-core function per capability
only has to reason about capability semantics, not about re-deriving
middleware ordering independently at each entry point.

## Preserve the distinction between API contracts and application ports

The API layer and `ports` may both integrate with adapters, but they answer
different questions:

- **`api/*`:** What communication contract does this service expose or
  consume?
- **`ports/*`:** What dependency or interaction does the application need?

They can share lower-level transport machinery, codecs, instrumentation, or
lifecycle utilities. They should not be forced into one public abstraction
merely to make adapter implementations look uniform.

An adapter may support both an API contract and a port pattern, but the two
integration paths should remain explicit in documentation and naming. This
matters doubly for the internal seam: `d-0007`'s confirmed 3-way split was
precisely between the `ports.Pattern` binding path (`transport.go`) and the
documented `Client.Attach` workflow (`caller.go`/`serve_subscribers.go`) —
two legitimately different integration paths that nonetheless must not
silently diverge in capability dispatch.

**Resolved (Round 1, decision 3):** the eventual Binding/capability type
family is unified across both seams — see "Resolved design decisions"
below. That unification is at the type-family level only; it does not
collapse `Register`/`Handle` (`api/*`) and `Bind` (`ports/*`) into one
integration path, and does not change the guidance above.

## Design principles to preserve

- **Declare → register → handle/bind:** Keep the lifecycle consistent and
  explain each stage.
- **One declaration, derived behavior:** Codecs and API declarations remain
  the source of truth for runtime behavior and specifications.
- **Transport-neutral contracts:** API packages must not depend on a
  specific adapter implementation.
- **Capabilities at the integration seam:** Requirements are declared
  semantically and checked against adapter support.
- **Transport-specific options stay with the adapter:** Do not leak MQTT,
  HTTP, or ZeroMQ configuration into generic API declarations unless it is
  intentionally a protocol-specific extension.
- **No false portability:** If two transports cannot provide equivalent
  semantics, the capability check or documentation must make that clear.
- **Small, testable adapters:** Adapter code should focus on mapping
  contracts to transport mechanics.
- **One dispatch-core per capability, per adapter:** Every entry point
  inside an adapter — generic or reflected, escape-hatch or documented
  workflow — calls through the same internal implementation of a given
  capability's dispatch logic. No capability may have more than one
  independently-maintained implementation within a single adapter.

## Phased plan

### Phase 1 — Combined audit (all adapters)

Before changing code, document the actual existing model across **every**
adapter package (`chi`, `file`, `mcpgo`, `mcprest`, `mqtt`, `mqtt5`,
`nethttp`, `openai`, `redis`, `sql`, `templ`, `websocket`, `zeromq`, and
`internal` shared helpers) — not just the three Doc B named.

**Tag each adapter by which seam(s) it serves.** `api/*` communication
patterns (`api/rest`, `api/events`, `api/reqreply`) are the most developed
and most complex part of go-codex today, so most of this roadmap's depth —
examples, regression history, dispatch-entry-point counts — naturally
concentrates there. But `ports/*` application-dependency patterns are part
of the same adapter-layer problem and must not be under-audited just
because their adapters are structurally simpler. Confirmed via their
`binding.go`-named entry points, `adapters/file`, `adapters/redis`, and
`adapters/sql` are primarily `ports`-pattern adapters (binding
`ports.File`/`ports.Cache`/`ports.SQL`-style patterns), not `api/*`
route/channel/tool declarations. Some adapters serve both seams — `mqtt`,
`mqtt5`, and `zeromq` already bind both `api/events` channels and the
`ports.Pattern` path (see `transport.go`, above). Record this seam
classification (`api/*` only / `ports/*` only / both) as an explicit column
in the audit so coverage of `ports` adapters is a deliberate check, not an
afterthought.

Produce one merged audit artifact with two complementary matrices:

1. **External-seam matrix** (Doc A's axis): per API declaration/handle,
   record what is declared, what registration produces, how middleware is
   applied, where capability requirements are stored, how an adapter
   discovers those requirements, how adapter support is represented, when
   incompatibility is detected, and which exported methods exist primarily
   for adapters. Per adapter, record which API families and port patterns
   it supports, which capabilities it advertises, and how it maps them to
   native transport features.
2. **Internal-seam matrix** (Doc B's axis): capability × adapter × dispatch
   entry point → implementation count + file:line, plus a test-coverage
   column recording whether a test exercises that *exact* entry point (not
   just the easiest-to-test path for that capability).

**Deliverable:** a current-state diagram, the capability/adapter
compatibility matrix, and the capability/adapter/entry-point duplication +
coverage matrix. Base this strictly on `main`, not on possibly stale
released documentation.

### Phase 2 — Specify the seam (two sub-tracks, same phase)

Run both sub-tracks together, since they inform each other directly:

- **Capability semantics (external seam):** Write down how requirements
  are declared, advertised, compared, and reported. Ensure capability
  identity and values are semantic rather than accidentally tied to one
  protocol implementation. Do not implement an elaborate generic capability
  framework unless Phase 1's findings demand it.
- **Dispatch-core consolidation (internal seam):** For every
  confirmed-duplicated capability from Phase 1, determine whether the
  reference pattern (REST's single reflection path, calling into the
  handle's own generic methods) is mechanically applicable, or whether a
  pattern's protocol shape (pub/sub fan-out; async request/reply
  correlation) genuinely requires a structurally different dispatch shape
  per entry point. Determine whether a shared, explicit dispatch-core
  abstraction is feasible given each entry point's different starting
  point (fully-generic `T` known at compile time vs. type-erased `any`
  recovered via reflection). **Resolved (Round 2): design this per pattern
  family, not as one universal mechanism** — events and reqreply get
  separate consolidation designs, since the evidence shows their
  duplication differs both in degree (3 vs. 6 confirmed call sites) and in
  shape (reqreply's zeromq ROUTER/DEALER socket variants have no events
  counterpart). Within each family, whether the mechanism is a shared
  helper package or a per-adapter-local convention remains open to this
  phase.

#### New: consolidate capability-coverage checking to one point (Design goal 12)

Distinct from dispatch-core consolidation above (which fixes HOW MANY
implementations exist), this sub-goal fixes WHERE a capability-coverage
check runs. The "Preliminary evidence" audit found checks executing
separately at each runtime call site (once on publish, once on subscribe)
rather than once at a single attach/registration gate — confirmed
independently of the dispatch-core duplication count. Phase 2 must design
a single consolidated check point per binding (likely at `Attach`/
`BindClient`/`ServerAwareTransport.BindServer`-equivalent time, per the
"Binding and attachment" lifecycle below), and Phase 4 must remove the
scattered per-call-site checks once the consolidated gate exists.

**Open question (flagged by critical review, must be answered before this
goal is treated as unconditionally achievable):** does any in-scope
capability allow a legitimate **per-call override** of a capability-relevant
value (e.g. a caller setting QoS on an individual publish call rather than
only at channel-declare time)? If so, a single attach-time gate cannot be
the whole answer — it would miss an override that violates the declared
requirement at the moment it actually matters. Phase 2 must check this
explicitly for each capability in scope, not assume a single gate suffices.

#### Essential: Observer integration and error-pattern mechanisms are in scope

Both sub-tracks above must explicitly account for two existing,
already-shipped cross-cutting mechanisms — they are not new concerns this
roadmap invents, and skipping them in the design would leave the exact gap
this roadmap exists to close:

- **Observer integration** (`stats.Observer` and its siblings —
  `stats.CapabilityObserver`, `stats.Disposition`/`DispositionObserver` per
  `d-0006`, `stats.PipelineObserver` for forge). Every dispatch entry point
  must report through the same observer call sites; a consolidated
  dispatch-core is also the natural place to guarantee that.
- **Error-pattern mechanisms** (`ErrorPattern`, `DeadLetter`, `ErrorChannel`,
  `ObserveErrorResponseFor`/`ErrorPatternObserver` per
  [`d-0005-error-handling.md`](../design/d-0005-error-handling.md)).

`d-0005` itself is a second, independently-confirmed instance of this
roadmap's core bug class: its own "Scope widened" H1/H2 notes document
`ErrorPattern` being correctly wired at one dispatch entry point
(`serve.go`'s `serve`) but silently **not** consulted at another
(`handlerFunc`, backing the port/stream-binding adapters) — found only in a
later review round, the same "wired here, silently missing there" shape
Doc B found for events' Subscribe middleware. Phase 1's audit matrices
should therefore track Observer-call-site coverage and ErrorPattern
consultation as explicit columns per dispatch entry point, not fold them
into a generic "capability implemented: yes/no."

### Phase 3 — Proof of concept

Exercise the proposed model against representative cases drawn from both
source drafts:

1. A simple REST route bound to an HTTP adapter.
2. An event channel without optional capability requirements bound to MQTT
   3 and MQTT 5.
3. An event channel requiring a minimum QoS (or a ZeroMQ HWM) that an
   incompatible adapter cannot satisfy: accepted by a compatible adapter and
   rejected by an incompatible one with an actionable
   `CapabilityCoverageError` — this already works today (see "Preliminary
   evidence"), so this case is a regression test, not new ground. Separately,
   fold MQTT user properties into the sealed Capability mechanism as the
   named gap recorded above, and prove the ORIGINAL Doc A scenario (MQTT 5
   accepted, MQTT 3 rejected with an actionable error, not a compile error)
   once that gap is closed.
4. A request/reply contract bound to two transports that genuinely support
   its required semantics.
5. A middleware chain whose behavior remains consistent across compatible
   adapters.
6. Events' Subscribe path specifically (the confirmed 3-implementation
   case) refactored to one shared dispatch-core, verified against all three
   previously-independent entry points.
7. A `ports`-pattern case — e.g. a `ports.File` or `ports.Cache` bound to
   `adapters/file`/`adapters/redis` — proving the capability/binding model
   and dispatch-core consolidation apply to the `ports/*` seam as well, not
   only `api/*` communication patterns.

These examples should reveal whether the current handle is already a good
binding or whether a separate adapter-facing view is justified, and whether
dispatch-core consolidation is mechanically achievable without forcing
artificial uniformity onto genuinely different entry-point shapes — across
both the `api/*` and `ports/*` seams.

### Phase 4 — Implementation

Based on the audit and PoC:

- standardize the attachment lifecycle and error behavior;
- minimize adapter-specific knowledge of API internals;
- reduce accidental public API surface;
- keep interfaces small and pattern-specific;
- keep protocol options within their adapters;
- reuse shared helpers only where behavior is genuinely common;
- consolidate every confirmed-duplicated capability (per adapter) onto one
  internal dispatch-core, with every entry point calling through it;
- extend the same treatment to events' Publish side and reqreply's
  Call/Serve side once Phase 1 confirms or denies they share Subscribe's
  shape.

### Phase 5 — Conformance tests and documentation

Create reusable adapter conformance tests for common semantics:
encode/decode behavior, middleware execution, error mapping, capability
rejection, lifecycle, and observability where applicable. Add
transport-specific tests for semantics that cannot be shared. A capability
must have tests proving both the supported path and the unsupported path,
**and** a test per confirmed dispatch entry point for any
previously-duplicated capability, so a future regression of this exact
class is caught by CI rather than by a new example exercising an
unguarded path. Sync `docs/roadmap`, `docs/design`, and adapter doc
comments to reflect the converged model.

Who owns these conformance tests, and how an adapter author discovers and
runs them, is itself an open design point — see "API-defined conformance
test suites" below.

## Open design points deferred to future rounds

These are raised now, during this roadmap's drafting, so they are not lost —
but they are explicitly **not decided here**. Each needs its own design
round once Phase 1's audit findings are in hand.

1. **API-defined conformance test suites.** Phase 5 above currently frames
   conformance tests as adapter-authored and somewhat ad hoc. A stronger
   version of that idea: each `api/*` package (`api/rest`, `api/events`,
   `api/reqreply`, `api/mcp`, ...) should define its own conformance test
   suite — an exportable/importable set of tests that any adapter claiming
   to implement that API's contract must run against itself. This
   inverts the ownership: the contract's test suite lives with the
   contract, not with each adapter's own judgment of what to test.

   This is one of several enforcement points this roadmap already relies on
   (capability compatibility checks at attach-time, the Phase 1 audit
   matrices, per-entry-point dispatch tests from Phase 5). Framed
   explicitly, API-defined conformance suites would be the **last**
   enforcement point: the gate that catches a non-conformant adapter when
   its code actually lands on `main`, after every earlier, more static
   check has already had its chance.

   For this to work as intended, the suites must be **clearly visible and
   explorable in the codebase** — a contributor implementing a new adapter
   must be able to find "here is the contract my adapter must pass"
   without spelunking. The exact mechanism is left open: a shared
   `_test`-only helper package per API family, a Go-level conformance
   "suite" type adapters instantiate against their own binding, a
   generated checklist cross-checked by CI, or something else entirely.
   Deciding the mechanism, where it lives, how it is versioned alongside
   the API it tests, and how CI enforces that every in-tree adapter
   actually runs it, is deferred to a future design round once Phase 1's
   audit shows which capabilities and entry points most need this kind of
   enforcement.

## Acceptance criteria

The architecture is ready to stabilize when:

- A contributor can implement a new adapter by following a short,
  documented contract.
- The adapter does not need to reconstruct information already available
  from the API declaration or handle.
- Compatibility is checked before a route/channel is activated wherever
  feasible.
- Unsupported capabilities produce clear, actionable errors.
- API declarations remain independent of concrete adapters.
- Middleware semantics are not duplicated by each adapter.
- The public adapter-facing surface is intentional and reasonably small.
- API bindings and application ports remain conceptually distinct.
- Representative contracts behave consistently across adapters that claim
  compatible capabilities.
- **No capability has more than one independently-maintained dispatch
  implementation within a single adapter** — verified by the Phase 1 audit
  matrix showing a count of 1 per capability/adapter cell, or by an
  explicit, documented exception.
- Every dispatch entry point for a given capability has a test exercising
  that exact entry point.
- Documentation and examples use the same declaration/registration/
  attachment terminology.

## Non-goals

This work should not automatically:

- introduce a universal adapter interface;
- replace the existing capability interfaces;
- require a new `Binding` type before auditing current handles;
- make every API family work with every transport;
- turn capabilities into a generic constraint-solving engine;
- move transport-specific options into `api/*`;
- add more adapters before the current integration pattern is clear;
- force artificial dispatch-shape uniformity onto entry points whose
  underlying protocol semantics (e.g., pub/sub fan-out vs. request/reply
  correlation) are genuinely different;
- benchmark or redesign reflection as a dispatch *technique* — correctness
  and consistency of dispatch are the question, not performance (REST's own
  converged, single-path design also uses reflection, so reflection per se
  is not the issue).

## Recommendation

**Retain the capability approach, make the external adapter integration
seam explicit, and consolidate each adapter's internal dispatch entry
points onto one implementation per capability.** Capabilities and bindings
solve complementary problems at the external seam; dispatch-core
consolidation solves the sibling problem at the internal seam. Treating
them as one investigation — rather than two separately-scoped documents —
is what makes the Phase 1 audit actionable: the same evidence (which
capability, which adapter, which entry point) feeds both the external
capability-compatibility matrix and the internal duplication matrix.

The next step is the Phase 1 audit across all adapters, followed by a
focused proof-of-concept using one REST route, one ordinary event channel,
one channel requiring MQTT 5 user properties, and the confirmed
events-Subscribe 3-implementation case. Use those examples to decide
whether the existing registered handles already provide the right binding
contract, whether a smaller adapter-facing interface is needed, and whether
a shared dispatch-core is mechanically achievable across the
compile-time-generic/reflected boundary.

The desired result is a simple experience for API users and an equally
clear, single-implementation extension point for adapter authors — without
coupling declarations to protocols, accumulating unneeded abstraction
layers, or letting one adapter's multiple entry points drift out of sync
with each other again.
