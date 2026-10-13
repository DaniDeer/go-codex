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
| `api/rest` (`adapters/nethttp`), standard HTTP/SSE surface | ONE — `serve.go`'s `ServeOne`/`Serve` reflects (`reflect.Value.Call`/`MethodByName`) directly into `RouteHandle[Req,Resp]`'s own generic, type-safe methods (`ApplyMergeFields`, `DecodeMerged`, `EncodeResponseMergeFields`, `ErrorStatusFor`, etc.) — confirmed via its own doc comment: "runs the SAME pipeline... invoked via `reflect.Value.Call`." **Superseded by the Phase 1 audit**: this holds only for this surface — see the "Correction to Confirmed asymmetry table" under "Phase 1 audit results" below for the full, corrected picture once `ports`-binding adapters and `adapters/chi` are included. | **YES, for this surface only** — see correction below |
| `api/events` Subscribe (`adapters/mqtt`/`mqtt5`/`zeromq`) | THREE, confirmed directly from `d-0007`'s own regression writeup | **NO** — confirmed broken, since patched for the specific gap that surfaced it, but the 3-implementation structure itself was never consolidated |
| `api/events` Publish | Dispatch-*implementation*-count still not directly audited — `adapters/mqtt5/transport_dispatch.go`'s own doc comment confirms some ad hoc convergence already happened ("the SUBSCRIBE-side equivalents ALREADY EXIST in caller.go... this file reuses them directly rather than duplicating"), but the exact count/location for Publish specifically remains unconfirmed on that axis. A *related but distinct* axis WAS confirmed by the later "Preliminary evidence" pass: capability-coverage checking on the publish path was missing until fixed (a location bug, not a count), per `adapters/mqtt5/transport.go:344-349`. | **PARTIALLY KNOWN** — implementation count still open; a capability-check-location gap on publish is confirmed |
| `api/reqreply` Call/Serve (`adapters/mqtt5`/`zeromq`) | **CONFIRMED by the later "Preliminary evidence" pass** (see below) — structurally separate files from events' own dispatch (`reqreply_transport.go` vs. `transport.go`) per adapter, and NOT the same shape as events' Subscribe: 6 independent capability-coverage-check call sites (mqtt5 Serve+Call, zeromq plain-Serve+ROUTER-Serve+plain-Call+DEALER-Call), wider than events' 3. | **NO — confirmed, and worse than events' Subscribe shape** (6 vs. 3 call sites) |
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

### Doc B's own claims — independently re-verified

Doc B based its claims on citing two existing design docs (`d-0001`,
`d-0007`) rather than fresh investigation, so they're re-confirmed here
directly against current code rather than taken on faith a second time.

- **The 3-way Subscribe dispatch split — confirmed, still unconsolidated
  on `main`.** All three independent implementations exist exactly as Doc B
  describes, in both `adapters/mqtt5` and `adapters/zeromq`:
  (a) `adapters/mqtt5/adapter.go:273`'s generic `makeSubscribeMessageHandler[T]`
  (escape hatch only); (b) `adapters/mqtt5/transport.go:462`'s reflection-based
  `Subscribe` (the `ports.Pattern` path), which independently re-implements
  decode/property-extraction/security/credential-check logic with comments
  explicitly flagging each step as a duplicate ("mirrors",
  "IDENTICAL", "SAME") rather than delegating; (c)
  `adapters/mqtt5/caller.go:122`'s `ServeSubscribers`, dispatch built by
  `caller.go:426`'s `makeErasedSubscribeMessageHandler`, explicitly
  documented as "the `ServeSubscribers` dispatch *equivalent* of
  `makeSubscribeMessageHandler`" — parallel, not shared. None of the three
  delegates to another. zeromq mirrors this shape at `handletransport.go:69`,
  `transport.go:360`/`591`, and `serve_subscribers.go:491`.
- **The specific missing-middleware regression — confirmed, fixed, but with
  a real residual test-coverage gap.** Documented almost verbatim at
  `docs/design/d-0007-declarative-middleware-layering.md:3305-3361`; the fix
  is live (`adapters/mqtt5/caller.go:418-425`). **New finding, not
  previously flagged**: the *exact* entry-point combination this regression
  was about — `Client.Attach` + `ServeSubscribers` + genuine `SubscribeMW`/
  `GrantedScopes`/`ContextField` enrichment — is covered only by a runnable
  example (`examples/events-api/demo_granted_scopes_context_field.go`), not
  by an automated `go test` assertion. Existing unit tests exercise
  `ServeSubscribers` for shape-errors and `DeadLetter` separately
  (`adapters/mqtt5/caller_test.go:326,553`), but not this specific
  middleware-enrichment path. This is a concrete, low-effort action item
  Phase 5 should pick up directly: converting the existing example into (or
  adding alongside it) a real regression-guard test is exactly the kind of
  "test per confirmed dispatch entry point" Phase 5 already commits to.
- **The `d-0008` Router-immunity argument — confirmed, in both doc text and
  code.** `d-0008`'s own text matches almost verbatim
  (`docs/design/d-0008-declarative-router-groups.md:475-477`). Code confirms
  it structurally: `api/events/router.go:210-212`'s `Router.Register` is a
  one-line delegate to the shared generic router, touching no
  adapter-dispatch code; there's even a dedicated regression test,
  `TestRouter_Register_IndistinguishableFromDirectRegister`
  (`api/events/router_test.go:348`, mirrored for REST at
  `api/rest/router_test.go:350`).
- **Doc B's scoping to mqtt/mqtt5/zeromq only — confirmed as correctly
  scoped, not merely unaudited elsewhere.** Spot-checked `adapters/websocket`
  (chosen for having its own non-trivial dispatch logic): it has no
  Subscribe/`SubscribeMW`/`MiddlewareHandlers` concept at all — a single
  dispatch path via REST-style socket binding (`IngestSocketAdapter`/
  `BroadcastSocketAdapter`/`DuplexSocketAdapter` over `ports.SocketPattern`).
  This is a genuine architectural difference, not "the same bug, not yet
  found" — the 3-way-duplication shape categorically does not apply here.
  This doesn't clear `websocket` of all Phase 1 audit scope (it still needs
  auditing for ITS OWN capability/dispatch shape), but it does confirm Doc
  B's original narrowing to mqtt/mqtt5/zeromq wasn't an arbitrary or lazy
  scope cut.

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

### Round 3 — narrower gaps

1. **Conformance-test-suite mechanism — resolved: a shared, importable
   `_test`-only helper package per API family** (e.g. `api/rest/resttest`),
   mirroring Go stdlib conventions (`net/http/httptest`, `testing/fstest`,
   `testing/iotest`). Rejected: a testify-style `Suite` type (adds an
   object-model layer go-codex doesn't use elsewhere, against "keep the
   adapter-facing contract small"); a CI-generated checklist as the
   *primary* mechanism (not self-enforcing at `go test` time — exactly the
   silent-rot failure mode this roadmap exists to close; it may still
   survive as a secondary, non-enforcing human-readable summary). The
   helper package exposes a function-shaped entry point (e.g.
   `resttest.RunConformance(t, binding)`) rather than a type hierarchy, and
   its exact signature can evolve alongside each pattern family's
   still-provisional binding shape (Round 2 decision 1) without blocking
   Phase 2. This item is removed from "Open design points deferred to
   future rounds" below, which it previously occupied.
2. **Capability-value taxonomy — resolved: expand now, conceptually (not
   as concrete Go types yet).** Despite the "no concrete driver yet"
   analysis favoring YAGNI, the explicit decision is to specify all four of
   Doc A's originally-open axes now at the conceptual/semantic level — see
   the rewritten "Boolean and parameterized capabilities" section below for
   the actual specification. Concrete Go types implementing this are
   Phase 2's job; this round only fixes the semantics each axis must
   support.
3. **Adapter-interface granularity — resolved: codify the existing
   pattern as the explicit rule**, rather than leaving it case-by-case.
   One small interface per (pattern family × lifecycle role) — typically
   1–2 methods — plus separate, optional `...Aware`/`...Capable` extension
   interfaces for opt-in features, exactly matching what the Doc A
   evidence pass already found in code (`ServerTransport`/`ClientTransport`,
   `PublishTransport[T]`/`SubscribeTransport[T]`, `HeaderCapableTransport`,
   etc.). Phase 2/4 should treat deviation from this shape as something to
   justify explicitly, not a free choice per family.
4. **Phase 1 audit matrix template — resolved: specify the concrete column
   schema now**, rather than deferring template design to Phase 1 itself —
   see the "Audit matrix template" subsection added to Phase 1 below.

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

- **Requirement:** "This contract needs QoS level 1 or higher."
- **Adapter configuration:** "Configure this MQTT binding's default QoS, or
  its retry/reconnect policy."

Requirements determine compatibility. Adapter options configure how a
compatible adapter operates. An option must not silently imply that an
unsupported transport can meet a semantic requirement. (The "Preliminary
evidence" audit spot-checked this for `adapters/mqtt5`'s option structs and
found no violation — `Capabilities` is always a separate field, never
merged with mechanical config like `UserPropertyParams`/`TopicFilter`.)

### Boolean and parameterized capabilities

Not every capability is a simple yes/no feature. Some have values or
constraints: QoS levels, maximum payload size, delivery guarantees,
ordering, streaming, duplex communication, or transactional behavior.

Start with the capability forms already shipped (QoS/Retained/HWM/Conflate
per `d-0006`) — presence-only (`Retained`, `Conflate`) and
minimum-level (`QoS`, `HWM`, via `>=`) — confirmed by the "Preliminary
evidence" audit to be the only two forms that exist in code today. Avoid
designing a general constraint-solving engine (still a non-goal, see
below); what follows is a conceptual specification of four additional
semantic axes (**Round 3, decision 2**), not yet a concrete Go type design
— that remains Phase 2's job once a real capability needing one of these
axes is confirmed:

1. **Presence vs. minimum-level vs. exact-level.** A requirement declares
   one of three modes: *presence* (capability must merely exist, no value
   comparison); *minimum level* (adapter's level must be `>=` the declared
   value — the only leveled form that exists today); or *exact level* (the
   adapter's level must equal the declared value exactly — satisfied by
   neither a higher nor a lower level). Exact-level matters when a
   capability's semantics genuinely change at different levels rather than
   monotonically improving (e.g. a level that trades a guarantee for a
   different, not strictly "better," behavior).
2. **Weaker-form-satisfiable.** A requirement may explicitly permit an
   adapter to satisfy it via a documented, non-native/approximated
   mechanism instead of failing coverage outright — but only if that
   degradation is surfaced, never silent (see axis 3). Without an explicit
   opt-in, the default remains today's behavior: an adapter that cannot
   natively meet the requirement fails coverage.
3. **Transport-guaranteed vs. adapter-approximated.** Every capability
   implementation declares whether its semantics are protocol-native
   (guaranteed by the wire protocol/broker itself) or adapter-approximated
   (emulated client-side, e.g. a retry loop simulating a guarantee the
   protocol doesn't actually provide). This flag is required capability
   metadata, not an afterthought — it should flow into the coverage-check
   result and into `stats.CapabilityObserver.RecordCapabilityApplied`'s
   observed event (both already confirmed to exist), so a guaranteed vs.
   approximated capability is distinguishable after the fact, not just at
   declare time.
4. **Observable-to-application vs. implementation-detail-only.** A
   capability declares whether its satisfaction mode (including whether it
   was approximated per axis 3) is something application/handler code can
   itself query at runtime, or whether it is purely internal and invisible
   to the handler. This determines whether app code can adapt behavior
   (e.g. log a warning when running in approximated mode) or is fully
   insulated from the detail.

A capability should not be advertised as supported if the adapter cannot
uphold the declared semantics — this remains true under every axis above;
axis 2 (weaker-form-satisfiable) is the only sanctioned, explicit exception,
and only when axis 3 makes the approximation visible rather than silent.

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

**Explicit granularity rule (Round 3, decision 3):** one small interface
per (pattern family × lifecycle role) — typically 1–2 methods — plus
separate, optional `...Aware`/`...Capable` extension interfaces for opt-in
features. This codifies what the "Preliminary evidence" audit already
found in code: `ServerTransport`/`ClientTransport` (reqreply),
`Transport`/`ClientAwareTransport` (events), `PublishTransport[T]`/
`SubscribeTransport[T]` (events, generic per-pattern), and capability-only
interfaces like `HeaderCapableTransport`/`CookieCapableTransport`/
`QueryCapableTransport` (REST) all already follow this shape. Treat any
future interface design that deviates from it (e.g. a single interface
spanning multiple lifecycle roles, or one with 3+ required methods) as
something to justify explicitly, not a free per-family choice.

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

**Resolved (Round 1, decision 3, narrowed by the critical-review pass):**
the Binding/capability type family is unified **where evidence shows
convergence** (confirmed for mqtt5's events-capable path); it is
**explicitly undecided** for pure-ports adapters (`file`/`redis`/`sql`)
pending the full Phase 1 audit — see "Resolved design decisions" below for
the full reasoning. Where unification applies, it is at the type-family
level only; it does not collapse `Register`/`Handle` (`api/*`) and `Bind`
(`ports/*`) into one integration path, and does not change the guidance
above.

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
released documentation. **Status: COMPLETE — see "Phase 1 audit results
(all adapters)" below the matrix template for the populated matrices,
findings, and reconciliation with the earlier "Preliminary evidence."**

#### Audit matrix template (Round 3, decision 4)

Specified now so Phase 1 can start directly from it rather than re-deriving
a format first. One row per (adapter × capability or pattern-family), as
appropriate to each matrix.

**External-seam matrix** — columns:

| Column | Meaning |
|---|---|
| Adapter | Package name (e.g. `adapters/mqtt5`) |
| Seam(s) served | `api/*` only / `ports/*` only / both (per the seam-tagging rule above) |
| Pattern family | REST / events / reqreply / tools / ports-File / ports-Cache / ports-SQL / ... |
| Declared capabilities | Which `CapabilityRequirement`-style capabilities this adapter can advertise |
| Capability form | Presence / minimum-level / exact-level / weaker-form-satisfiable (per the taxonomy above) |
| Guaranteed or approximated | Per capability, whether native or adapter-emulated |
| Observable to application | Per capability, yes/no (per the taxonomy above) |
| Where coverage is checked | File:line of the compatibility-check call site(s) — ideally one, per Design goal 12 |
| Binding/handle type used | e.g. `RouteHandle[Req,Resp]`, `ChannelHandle[T]` |
| Adapter-facing exported methods used | Which handle methods this adapter actually calls (feeds the essential/adapter-facing/internal-detail classification) |
| Middleware application point | File:line where declared middleware is dispatched |
| Observer call sites | File:line(s) reporting through `stats.Observer`/siblings |
| ErrorPattern/DeadLetter consultation | File:line(s), or "not consulted" |

**Internal-seam matrix** — columns:

| Column | Meaning |
|---|---|
| Capability (or middleware concern) | e.g. "Subscribe dispatch," "capability coverage check," "ErrorPattern consultation" |
| Adapter | Package name |
| Entry point | Name/file:line of each independently-reachable dispatch path (e.g. `adapter.go`'s generic handler, `transport.go`'s reflection path, `caller.go`'s `ServeSubscribers`) |
| Implementation count | Total number of independently-maintained implementations found for this capability × adapter |
| Shared vs. duplicated | Which entry points already delegate to a shared helper vs. independently reimplement |
| Test coverage per entry point | Yes/no per entry point — does an automated test exercise this *exact* path, not just the easiest one for the capability |
| Known regression history | Cross-reference to `d-0001`/`d-0005`/`d-0007`/this doc's "Preliminary evidence," if this exact cell already has a documented incident |

### Phase 1 audit results (all adapters)

**Status: COMPLETE.** Full, fresh, independent audit across all 14 targets
(`chi`, `file`, `mcpgo`, `mcprest`, `mqtt`, `mqtt5`, `nethttp`, `openai`,
`redis`, `sql`, `templ`, `websocket`, `zeromq`, `internal`), dispatched as 14
parallel investigations per the template above. This fresh pass
deliberately ignored the earlier "Preliminary evidence" section's findings
(a narrower, Doc A/B-focused pass) to maximize rigor; see "Reconciliation
with Preliminary evidence" at the end of this section for where the two
agree, extend, or correct each other.

#### External-seam matrix results

##### `adapters/chi`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| adapters/chi | REST route dispatch (Ingest/Pipeline/Latest/Tool ports) + declarative attach via `rest.Server` | Request/Response REST | Header/Cookie/Query transport-capability extraction (`rest.HeaderCapableTransport`/`CookieCapableTransport`/`QueryCapableTransport`) | Compile-time type-assertion marker (`httpTransport` zero-value carrier) + per-request real `httpCarrier{r}` | Guaranteed at compile time via `var _ rest.XxxCapableTransport = httpTransport` (capability.go:77-80) | Not directly observable to application code; internal Attach-time assertion | capability.go:63-72, 77-80 | `*rest.RouteHandle[Req,Resp]`, `*rest.SSERouteHandle[Req,Event]` | `handle.Descriptor.Method/Path`, `handle.Implementations`, `handle.MiddlewareHandlers` (reflection in serve.go) | `applyGeneralMiddleware` (adapter.go:239) + per-request `MiddlewareHandlers` dispatch in `handlerFunc`/`buildRouteHandler` | `stats.ObserverFromContext`/`ReportErrors`/`RecordDiagnostic`/SecurityObserver across adapter.go, serve.go, serve_sse.go (~20 sites) | `tryRespondErrorPatternGeneric` (adapter.go:1109, ~15 sites) + `tryRespondErrorPattern` (serve.go:841, ~17 sites); no DeadLetter |

##### `adapters/file`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `ScanAdapter`/`WatchAdapter` (binding.go:31,122) | `ports.SourcePort` | ports source | N/A — no capability mechanism | N/A | N/A | via `errs` chan | N/A | `ports.SourceAdapter[T]` | `AdapterName`, `Activate` | none (no middleware layer in ports) | Scan: forwarded to `gstream.FromCodec` options; Watch: `Observer` option accepted but **dead — never read** (binding.go:111) | N/A |
| `ReadEachAdapter`/`ReadAdapter` (binding.go:187,300) | `ports.IOPort` | ports IO | N/A | N/A | N/A | via returned error stream | N/A | `ports.IOAdapter[In,Resp]` | `AdapterName`, `Transform`; `ports.File.ReadMerged`, `ports.ValidateParams` | none | observer forwarded into `fileOpts.Observer`, not called directly | N/A |
| `DrainWriteAdapter` (binding.go:343) | `ports.SinkPort` | ports sink | N/A | N/A | N/A | via `OnError` callback | N/A | `ports.SinkAdapter[T]` | `AdapterName`, `Activate` | none | **only** direct `stats.ReportErrors(obs,"file",err)` call in the package (binding.go:368) | N/A |
| `DrainWriteFileAdapter`/`DrainPatchAdapter`/`DrainPatchEncodedAdapter` (binding.go:433,536,615) | `ports.SinkPort` | ports sink | N/A | N/A | N/A | via `OnError` callback | N/A | `ports.SinkAdapter[T/map/P]` | `AdapterName`, `Activate`; `ports.File.{MergeFields,Write,Patch}`, `ports.PatchEncoded`, `ports.ValidateParams` | none | observer forwarded into `fileOpts.Observer`, not called directly | N/A |

No capability-requirement/coverage-check mechanism exists anywhere in this
package — confirmed across all 8 exported constructors and their `*Options`
structs.

##### `adapters/mcpgo`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| ToolHandler/RegisterTool | `api/mcp` Tool registration | Request/response RPC | N/A — no capability mechanism; only codec validation | N/A | N/A | via `stats.Observer.RecordRequest`/`ReportErrors` | No pre-flight gate; per-call only | `*apimcp.ToolHandle[In,Out]` (adapter.go:70) | `handle.Decode`/`Encode`/`ErrorResponseFor` | Inline in `server.ToolHandlerFunc` closure (adapter.go:81-139) | adapter.go:93,100,128,137 | `handle.ErrorResponseFor` at adapter.go:112; no DeadLetter anywhere in `api/mcp` |
| ResourceHandler/ResourceHandlerWithVars | `api/mcp` Resource registration | Request/response (URI-templated) | N/A | N/A | N/A | via RecordRequest | No gate | `*apimcp.ResourceHandle[V,T]` (adapter.go:168,262) | `handle.Encode`, `handle.ExtractURIVars` | Inline, 2 near-duplicate closures (adapter.go:179-206, 271-301) | adapter.go:194-298 (multiple sites) | None — no `ErrorResponseFor` equivalent for resources |
| PromptHandler | `api/mcp` Prompt registration | Request/response | N/A — only `PromptArg.Required` | N/A | N/A | via RecordRequest/ReportErrors | No gate | `*apimcp.PromptHandle` (adapter.go:351) | `handle.ValidateArgs` | Inline closure (adapter.go:369-400) | adapter.go:386-397 | None |
| ToolLatestHandler/ToolPipelineHandler (+ port adapters) | Same Tool seam, reactive variants | Request/response wrapping cache/pipeline | N/A | N/A | N/A | delegates to ToolHandler's RecordRequest | No gate | `*apimcp.ToolHandle[In,Out]` | Delegates entirely to `ToolHandler` (stream.go:76,155) | Reuses ToolHandler's one closure | Inherited | Inherited (`ErrorResponseFor`) |

##### `adapters/mcprest`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `adapters/mcprest` | Bridges an MCP tool call to an outbound REST client call | REST (client) exposed as MCP tool | N/A — inherits whatever `rest`/`nethttp` checks internally; adds none itself | N/A | N/A | N/A | Not checked in mcprest — delegated entirely to `adapters/nethttp`'s client transport setup | `*rest.RouteHandle[Req,Resp]` via `rest.Route.ClientHandle()` (bridge.go:44) | `rest.CallWithTransport` (bridge.go:63), `nethttp.NewClientTransport` (bridge.go:46) | None in mcprest — any `ClientMW` attached by the caller before reaching mcprest | None in mcprest — entirely inside `adapters/nethttp/clienttransport.go` | `errors.go:117-205` `DefaultErrorPatterns()` maps nethttp/rest client errors + mcprest's own `ToolRequestMapError`/`ToolResponseMapError` into `apimcp.ErrorPattern`; no DeadLetter |

##### `adapters/mqtt` (v3)

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| adapters/mqtt (v3) | `api/events` pub/sub | pub/sub | `QoS` (0/1/2), `Retained` — sealed `mqtt.Capability`; **no `UserPropertyParam`** (confirmed absent via grep — MQTT5-only feature) | sealed interface, `Apply(wire *WireAttributes)` | Approximated per-call, not compiler-enforced | Yes — missing capability surfaces as an error to caller/errs channel | **Scattered — 6 distinct call sites**: adapter.go:653 (publish), caller.go:139 (subscribeHandle), caller.go:463 (ServeSubscribers reflection), binding.go:123 (SubscribeAdapter), transport.go:308 (reflection Publish), transport.go:564 (reflection Subscribe) | `events.ChannelHandle[T]` (generic + reflection-recovered) | `SubscribeAdapter`/`PublishAdapter`, `subscribe`/`subscribeHandle`/`publish`/`publishHandle` | `wrapPublishGeneral`/`wrapSubscribeGeneral` + reflection mirrors `wrapClientGeneralDecoratorReflect`/`wrapServerGeneralDecoratorReflect` | `stats.ObserverFromContext` + `Record*` across adapter.go, caller.go:112,454, binding.go, transport.go/transport_dispatch.go, plus `Observability[T]` wrapper (caller.go:759) | `tryPublishErrorChannel`/`tryDeadLetter` (adapter.go:216,238), consulted at every failure point; reflection mirrors in transport.go:429,442; ServeSubscribers' reflection dispatch now wired too (per in-code comment acknowledging a prior gap) |

##### `adapters/mqtt5`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| mqtt5 | `api/events` (Subscribe+Publish) | pub/sub, topic-based | `Capability` (QoS, Retained) — capability.go:35-95; **User Properties via separate `UserPropertyParam`, NOT part of Capability** (capability.go:100-102) | sealed, `isMQTT5Capability()` marker | Guaranteed-at-setup via `events.VerifyCapabilityCoverage` | Yes — `SubscribeError`/dead-letter/error-channel | 3 explicit call sites: adapter.go:791 (subscribeWithHandle), adapter.go:970 (publish), caller.go:157 (ServeSubscribers) — transport.go's reflection Subscribe/Publish rely only on `ApplyCapabilities`, **no independent coverage pre-check found** | `*events.ChannelHandle[T]` | `events.Transport.Publish/Subscribe/ServeSubscribers`, Attach/Bind | `events.ApplyCapabilities` + `wrapSubscribeGeneral`/`wrapPublishGeneral`/reflection mirrors | `obs.RecordSubscribe`/`RecordPublish`, dozens of sites across adapter.go/transport.go/caller.go | `tryDeadLetter`/`tryPublishErrorChannel`, 14+ call sites in adapter.go |
| mqtt5 | `api/reqreply` (Call+Serve) | request/reply, correlation-id + reply-topic | `Capability` (QoS, Retained), same sealed interface reused | same sealed interface | Guaranteed via `reqreply.CheckCoverage` (Serve setup, reqreply_transport.go:440) + `VerifyCapabilityCoverage` (Serve:462, Call:1192) | Yes — `ErrorPatternResponse`, `CallError`, `ServeError` | reqreply_transport.go:440,462,1192 (3 call sites, checked once per Serve setup not per-message) | reflection-recovered route handle | `reqreply.ServerTransport.Serve`, `ClientTransport.Call`/`CallRaw` | `events.ApplyCapabilities` reused for wire attrs; `applyGeneralServerMiddleware`/`runServerSecurityMiddleware`/`mergeCredentialUserProperties` | `obs.RecordRequest("MQTT5-REP/REQ",...)`, 40+ sites | `tryDeadLetterReflect` (reqreply_transport.go:196) + `ErrorPatternResponse` matching |
| mqtt5 | `ports.Pattern` binding | stream adapters (Sub/Pub/Call/Serve) | Same `Capability` type | Same, delegated | Delegates to underlying generic/transport functions, no separate check | Yes, same plumbing | Delegated — binding.go itself has no independent check | `*events.ChannelHandle[T]`/route handles wrapped in `ports.Sink/Source/IO/ToolAdapter` | `SubscribeAdapter`/`PublishAdapter`/`CallAdapter`/`ServeAdapter` | Delegated | Delegated | Delegated (e.g. `handleUpstreamError` reuses `tryPublishErrorChannel`) |

##### `adapters/nethttp`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| adapters/nethttp | `api/rest` standard HTTP route dispatch | REST request/response, ONE reflection-based implementation (reference pattern) | `rest.CapabilityRequirement` (`RequireQoS`/`RequireHWM`) | `rest.CapabilityName`, minimum-level | Approximated — always fails closed since HTTP has no native QoS/HWM | Yes — eager failure at `serve()`/`ServeOne` time, not request time | `buildRouteHandler`, serve.go:~290-320 (security + capability + param-kind coverage), via `VerifyCapabilityCoverage` | `*rest.RouteHandle[Req,Resp]` via reflection | `Decode`, `Validate*`, `MergeFields`, `*ParamNames` (reflect-called) | `applyGeneralMiddleware` wraps `http.Handler` outermost-in | `stats.ObserverFromContext`, `SecurityObserver`, `ReportErrors` (serve.go:326,488,619,633) | `tryRespondErrorPattern`, 21 call sites (serve.go:848) |
| adapters/nethttp | `api/rest` SSE dispatch — ONE implementation, separate file (`serve_sse.go`), duplicates REST's format-negotiation/decode/validate logic rather than reusing it | SSE streaming | Same `CapabilityRequirement` mechanism reused | Same as REST row | Approximated, same as REST | Yes — `serveSSE`/`buildSSERouteHandler` eager check (serve_sse.go:130,141) | serve_sse.go:130 (security CheckCoverage), :141 (capability coverage) | `*rest.SSERouteHandle[Req,Event]` via reflection | Format negotiation + decode/validate reflect calls mirroring serve.go | Same `applyGeneralMiddleware`-style wrapping | serve_sse.go:221,274,285,424 | **None** — code comment explicitly states "no declared ErrorPattern concept exists for SSE" (serve_sse.go:241) |
| adapters/nethttp | **`ports`-binding adapters** (IngestAdapter/LatestAdapter/HandlerLatest/PipelineHandler/CallAdapter/etc.) via shared `handlerFunc[Req,Resp]` (adapter.go:167) — **a SECOND, independently-written implementation, NOT a thin delegate of `buildRouteHandler`** | `ports.Source/Sink/Transform`+stream-binding over REST+SSE, generic (non-reflection) dispatch | No `CapabilityRequirement` applied in this path | N/A | N/A (gap, not approximation) | **Not checked** — zero capability-coverage calls found in `adapter.go`'s `handlerFunc` | **No `VerifyCapabilityCoverage`/`CheckCoverage` call anywhere in this path — a confirmed, previously-unflagged gap** | `*rest.RouteHandle[Req,Resp]` directly (generics, not reflection) | Same method names, called directly (typed, not reflected) | Same `impls` plumbing passed into `handlerFunc(..., impls...)` | adapter.go:196,348,503,517,749,794,805,857,889 | `tryRespondErrorPatternGeneric`, ~20 call sites (adapter.go:1043) — added later per `d-0005`'s H1 finding to close an original zero-coverage gap |

##### `adapters/openai`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `adapters/openai` (CallAdapter) | `api/llm` LLM-completion seam | LLM Pattern only — no streaming/function-calling/tool pattern exists | **None — no `Capabilities`/feature-flag concept exists anywhere in `api/llm`** | N/A — entirely config-driven (`CallAdapterOptions`) | N/A | Only indirectly via errors/Observer | Nowhere — no coverage-check mechanism exists | `*llm.CallHandle[Req,Resp]` | `EncodeRequest`, `ResponseSchema`, `DecodeResponse`, `SystemPrompt`, `Name` — all 5 exported fields/funcs used | None — no middleware layer; cross-cutting concerns inlined in `complete()` | `obs.RecordRequest`, 9 sites (every exit path); `stats.ReportErrors` at client.go:199 | None — no `ErrorPattern`/`DeadLetter` type exists in this repo at all for LLM |

##### `adapters/redis`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| Read-through cache (GetAdapter/Get/GetMerged/Seed) | `ports.IOPort` | `ports.CachePattern` | Get/lookup, decode+codec-validate, optional miss-as-error | Declarative `CachePattern{Key,TTL}` | Approximated — no requirement enforcement, purely opt-in | Yes — `CacheObserver.RecordCacheHit/Miss` + `ReportErrors` | Not checked anywhere — no capability-requirement mechanism exists | `ports.Cache[T]` | `GetAdapter`, `Get`, `GetMerged`, `Seed` | None | binding.go:70,176,187,191 | None — no ErrorPattern/DeadLetter type in this package |
| Write-through cache (SetAdapter/Set/SetHandle/DrainSetAdapter) | `ports.IOPort`/`ports.SinkPort` | `ports.CachePattern` | Set/write, encode+codec-validate, pass-through-on-failure, TTL override | Shared `writeThrough` helper | Approximated — behavioral guarantee, not statically checked | Yes — `RecordCacheWrite` + `ReportErrors` | None | `ports.Cache[T]` | `SetAdapter`, `Set`, `SetHandle`, `DrainSetAdapter` | None | binding.go:279,374,384,483 | None |
| **Pub/Sub (`SubscribeAdapter`/`PublishAdapter`)** | — | **NOT SHIPPED** — confirmed via file listing (no `pubsub.go`) and `docs/roadmap/redis-pubsub.md`'s own status: "Design draft — awaiting refinement / use case" | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A | N/A |

##### `adapters/sql`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `QueryAdapter[T]`/`DrainInsertAdapter[T]`/`QueryEachAdapter[In,T]` | `ports.Source/Sink/IOPort` | `ports.SQLPattern` — **confirmed strictly metadata-only**: a `{Table,Op}` struct propagated via context, no handle, no spec document | Table/Op descriptive metadata only, not a capability-requirement type | N/A | Approximated — metadata is descriptive only, never enforced | Via `errs` chan / `OnError` callback / returned stream | **Not checked** — `ports.SQLPattern` has no coverage/requirement mechanism at all | None (plain context value, not a handle) | `AdapterName`, `Activate`/`Transform`; shared `Validate`/`resolveTableOp` helpers | None — validation inlined per-call | binding.go:88-90,169-171,242-244 + `Validate`'s own `SQLObserver` calls | Not consulted — no ErrorPattern/DeadLetter anywhere in `ports`/`sql` |
| `Migrator` (Up/Down/Status) | Standalone helper, not a `ports` adapter | N/A | Observer-only; no `SQLPattern` involvement | N/A | N/A | Return error (`MigrationError`) | N/A | N/A | `NewMigrator`, `Up`, `Down`, `Status` | None | migrate.go:75-77,86-88,102-104,117-119 | Not consulted |

##### `adapters/templ`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| adapters/templ (`Format`/`StreamingFormat`) | REST response rendering (HTML) | Format/encoding only — no resilience/retry seam | None — no declared capability set at all | N/A | N/A | No — no Observer plumbed through this package at all | Not checked in this package; delegated to caller-supplied `codex.Codec[Props]` | `format.Format[Props]` value | `Format[Props]()`, `StreamingFormat[Props]()` | None — content negotiation/Observer handled entirely by the consuming `rest.Route`/adapter | **None found anywhere in package** | None — only a local `DecodeNotSupportedError` sentinel for the intentionally-unsupported decode path |

##### `adapters/websocket`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `IngestSocketAdapter[T]`/`BroadcastSocketAdapter[T]`/`DuplexSocketAdapter[In,Out]` | Server upgrade → `ports.Source/Sink/DuplexAdapter` | `ports.SocketPattern` | Header/Cookie/Query param-kind coverage (shared `checkSocketParamKindCoverage`, explicitly commented "to avoid tripling the same 4-line check") | Real-interface capability check, not a declared-list struct | Guaranteed-at-construction (checked once, error from constructor) | Yes | capability.go:76-82, called once per adapter at construction (binding.go:147,267,~473) | `ports.Socket[In,Out]` | `handle.Route.Validate*`, `MergeInbound`/`MergeOutbound`, `InFormat.Unmarshal`/`OutFormat.Marshal`, `hub.*` | **None — zero `MiddlewareHandlers`/`DispatchSubscribeMiddleware`/`SubscribeMW` usage anywhere, confirmed fresh via repo-wide grep** | `obs.RecordRequest/RecordSubscribe/RecordPublish`, `ReportErrors` — ~30 sites across binding.go | `ErrorFrames` consulted in Broadcast's and Duplex's own `handleUpstreamError` closures (near-identical, explicitly commented as mirroring each other); Ingest has none |
| `DialSourceAdapter[T]`/`DialSinkAdapter[T]`/`DialDuplexAdapter[In,Out]` | Client dial variants | Same pattern, client-side | **None — no HTTP-request param surface, no coverage check at all for client dialers** | N/A | N/A | N/A | Not checked | `ports.Socket[In,Out]` (reused) | `handle.Route.BuildPath`, `InFormat.Unmarshal`/`OutFormat.Marshal` | None | client.go, ~11 sites | None — `DialAdapterOptions` has no `ErrorFrames` field at all |

No `RegisterSocket`/AsyncAPI channel-registration mechanism exists anywhere
in this package or repo-wide (confirmed via repo-wide grep).

##### `adapters/zeromq`

| Adapter | Seam(s) served | Pattern family | Declared capabilities | Capability form | Guaranteed or approximated | Observable to application | Where coverage is checked | Binding/handle type used | Adapter-facing exported methods used | Middleware application point | Observer call sites | ErrorPattern/DeadLetter consultation |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| adapters/zeromq | `api/events` Subscribe | pub/sub | `Capability` (HWM, Conflate) | sealed, `Apply(sock)(bool,error)`, `events.ApplyCapabilities` | Approximated — `Apply` no-ops when socket doesn't implement setter interface | Yes, via Observer + returned applied flag | adapter.go `subscribeWithHandle` (generic) AND transport.go `Subscribe` (reflection) — **each does its own separate coverage check** | `*events.ChannelHandle[T]` | `Subscribe`, `WithOptions`, `SubscribeMW` | `wrapSubscribeGeneral`/`wrapServerGeneralDecoratorReflect` | adapter.go:706,719; serve_subscribers.go:324,335 | `tryDeadLetter[T]` (adapter.go:337) + reflection equivalent |
| adapters/zeromq | `api/events` Publish | pub/sub | same `Capability` set | same | same | Yes | adapter.go `publish[T]`/`publishHandle[T]` (870,1033); transport.go:156 `(*transport).Publish` (reflection) — separate coverage check each | `*events.ChannelHandle[T]` | `Publish`, `WithOptions`, `PublishMW` | `wrapPublishGeneral[T]`/`wrapClientGeneralDecoratorReflect` | `publish_adapter_observer_test.go`; transport.go:326 `publishDeadLetterReflect` | `tryPublishErrorChannel[T]` (adapter.go:317); `publishDeadLetterReflect` (transport.go:326) |
| adapters/zeromq | `api/reqreply` Call+Serve — plain REQ/REP | req/reply | `Capability` (HWM, Conflate) applied per Serve/Call via `ServeTransportOptions.Capabilities`/`ClientCallOptions.Capabilities` | Sealed interface + `reqreply.VerifyCapabilityCoverage` (reqreply_transport.go:607,1017) | Guaranteed-checked (hard error if required capability absent) | Yes | plain-Serve: reqreply_transport.go:594,607; plain-Call: reqreply_transport.go:1017 | `reqreply.RouteHandle[Req,Resp]` | `Serve`, `Call`, `CallRaw` | `applyGeneralServerMiddleware` (reqreply_transport.go:384) — **shared** between plain+ROUTER Serve (called at 615 and 1635) | `sendHandlerErrorReplyReflect`/`sendErrorReply` (adapter.go:1077) | `tryDeadLetterReflect` (reqreply_transport.go:141), 8+ call sites |
| adapters/zeromq | `api/reqreply` Call+Serve — ROUTER/DEALER | req/reply | same `Capability` set | same, `reqreply.VerifyCapabilityCoverage` (1631, 1998) | Guaranteed-checked | Yes | ROUTER-Serve: reqreply_transport.go:1621,1631; DEALER-Call: reqreply_transport.go:1998 | `reqreply.RouteHandle[Req,Resp]` | `Serve` (ROUTER), `Call`/`CallRaw` (DEALER) | `applyGeneralServerMiddleware` reused at line 1635 (same function as plain) | `sendRouterHandlerErrorReplyReflect`/`sendRouterForwardReplyReflect` (166,201) | `tryDeadLetterReflect`, identity-frame-based correlation (frames[0], line 1674) |
| adapters/zeromq | `ports.Pattern` binding | n/a | — | — | — | not separately checked | binding.go wraps events/reqreply constructors (`PublishOptions`/`SubscribeOptions`→binding.go:60-212) — no independent `ports.Pattern` dispatch loop; delegates to the rows above | `PublisherBinding`/`SubscriberBinding` structs | `Publish`, `Subscribe` wrappers | delegates to adapter.go/handletransport.go paths | delegates | delegates |

##### `adapters/internal`

Only two sub-packages exist — no general "internal" grab-bag:

- **`httpsecurity`** (net/http-family only — nethttp, chi): merges legacy
  Fn-based + new `MiddlewareHandler`-based `GrantedScopes` before one
  `middleware.CheckScopes` call.
- **`scopesmerge`** (events/reqreply-family only — mqtt, mqtt5, zeromq):
  the transport-agnostic equivalent, plus `HasSatisfyingHandler` to gate
  whether `CheckScopes` should run at all.

No external-seam row applies — `adapters/internal` binds to no `api/*`
pattern directly; it's pure shared utility consumed by the other adapters.

#### Internal-seam matrix results

##### `adapters/chi`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Route dispatch (declarative Attach workflow) | chi | `serve`→`buildRouteHandler` (reflection), serve.go:161,239 | 1 (reflect-based) | **Duplicated vs. `adapters/nethttp`'s identical `serve`/`buildRouteHandler`** — comments explicitly say "mirrors adapters/nethttp...identical...check"; chi has "no import relationship with nethttp...intentionally duplicated" | serve_test.go, adapter_test.go, error_pattern tests | n/a within this cell |
| Route dispatch (direct generic handler, non-Attach) | chi | `handlerFunc[Req,Resp]`, adapter.go:256 | 1 generic func, 2 call sites (Ingest, Latest) | Parallels the reflect path; also duplicated vs. nethttp's identical `handlerFunc` | binding_test.go, adapter_test.go | — |
| SSE dispatch (Attach + direct) | chi | `serveSSE`/`buildSSERouteHandler` (serve_sse.go:50,95); `sseHandlerFunc` (adapter.go:761) | 2 | Both duplicated vs. nethttp's identical SSE paths | serve_test.go, gap1_ports_middleware_dispatch_test.go | **Gap-1 review fix** (binding.go:163-165): generic SSE path previously passed NO `Implementations`, so `MiddlewareHandlers` never dispatched — a real historical regression, fixed, cross-referenced to nethttp's identical bug |
| Error handling / ErrorPattern consultation | chi | `tryRespondErrorPattern` (serve.go:841) + `tryRespondErrorPatternGeneric` (adapter.go:1109) | 2 independent implementations in-package | Duplicated in-package AND duplicated across packages vs. nethttp's identical functions | error_pattern_category_a_test.go, port_adapter_error_pattern_test.go | Comments flag deliberate parity fixes to keep both in sync with nethttp — drift/regression risk between the duplicated implementations |
| Observer reporting | chi | ~20 call sites across adapter.go/serve.go/serve_sse.go/stream.go | ~20, no single central dispatcher | Duplicated per-path (reflect route, generic route, SSE reflect, SSE generic, stream hub) | Covered incidentally | — |

**Chi's single biggest finding**: it is architecturally parallel to, but
**not a thin delegate of**, `adapters/nethttp` — it independently
duplicates nethttp's entire dispatch, error-pattern, and Observer logic
**line-for-line** (explicitly documented in ~15 comments), making chi/nethttp
together a confirmed cross-adapter duplication pair the per-adapter
template doesn't fully capture on its own.

##### `adapters/file`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Scan/decode, Watch/poll, Read, 4× Write/drain variants | file | One independent `Activate`/`Transform` per adapter function (binding.go, 8 functions) | 8 entry points, but each serves a **genuinely different operation** (not 8 competing implementations of one concern) | Not duplicated by design — `ReadAdapter` is an honest thin wrapper of `ReadEachAdapter`; the 4 Drain* sinks serve 4 different write semantics | Dedicated tests per function | None found |
| Param validation ("vars") | file | 4 call sites, all calling the same `ports.ValidateParams` helper | 4 call sites, 1 shared implementation | Shared logic, duplicated call-site boilerplate (minor, not a bug) | Per-adapter `_ParamValidationError` tests | None |
| Observer reporting | file | 1 direct `stats.ReportErrors` call (`DrainWriteAdapter` only); all others forward-only; `WatchAdapter`'s `Observer` option is dead code | Asymmetric — 1 direct call + N forwarding sites + 1 unused option | **Fresh finding, not previously flagged**: inconsistent Observer usage across sibling sink adapters | No dedicated observer-forwarding test for the affected adapters | None found — new finding |

**No duplication problem in this package by design.** The one soft spot is
the asymmetric/dead Observer wiring (fresh finding).

##### `adapters/mcpgo`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Tool-call dispatch (core + latest + pipeline + port-adapter variants) | mcpgo | `ToolHandler` closure (adapter.go:81), all variants delegate/wrap it (stream.go:76,155; binding.go:33,92) | 1 canonical implementation, N thin wrappers | **Shared** — no independent reflection-based dispatcher exists; unlike events/reqreply, there is no escape-hatch generic path that bypasses this | adapter_test.go, stream_test.go | None found |
| Resource dispatch (literal + vars-aware) | mcpgo | `ResourceHandler` (adapter.go:179), `ResourceHandlerWithVars` (adapter.go:271) | 2 near-identical hand-copied closures | **Duplicated** — not built on a shared helper | adapter_test.go | None confirmed, but a maintenance-risk pattern |
| Observer reporting | mcpgo | Inline per-call in 4 closures (tool/resource/resource-vars/prompt) | 4 independent inline implementations | Duplicated — "resolve observer, start span, RecordRequest" boilerplate repeated near-identically instead of factored out | adapter_test.go, stream_test.go | None found |
| ErrorPattern | mcpgo | `handle.ErrorResponseFor` (adapter.go:112), tool-only | 1 call site | Shared (only tools have it; resources/prompts lack an equivalent — asymmetric, not duplicated) | error_pattern_test.go | None found |
| DeadLetter | mcpgo | N/A | 0 — no DeadLetter type exists anywhere in `api/mcp` | N/A | N/A | N/A |

**Biggest finding**: unlike events/reqreply, mcpgo's tool-call dispatch is
genuinely single-path — no escape-hatch/reflection duplication exists for
it. The duplication that does exist (resource closures, Observer
boilerplate) is smaller-scale and in-package only.

##### `adapters/mcprest`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| REST client dispatch (transport build + HTTP call) | mcprest via nethttp | `MappedToolHandler` → `nethttp.NewClientTransport` → `rest.CallWithTransport` (bridge.go:46,63) | 1 (reused, not reimplemented) | **Shared, not duplicated** — zero reimplementation of HTTP transport/dispatch logic | bridge_test.go (+ nethttp's own client transport tests) | None found specific to mcprest |
| Error-pattern mapping | mcprest | `DefaultErrorPatterns()` (errors.go:117) | 1 implementation, but duplicative *in spirit* — re-wraps the same nethttp/rest client error types nethttp's own error-dispatch machinery already classifies | Shared error *types*, duplicated *mapping tables* (mcprest keeps its own payload/pattern list) | errors_test.go | None found |
| Capability-requirement checking | — | — | 0 — mcprest checks nothing itself | Relies entirely on whatever `rest.RouteHandle`/`nethttp` transport already enforced at construction time | N/A | N/A |

**Genuinely thin bridge** — the one place it adds its own logic
(error-pattern mapping) is a deliberate, narrow translation layer, not
transport duplication.

##### `adapters/mqtt` (v3)

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Subscribe dispatch | mqtt | (1) `subscribeHandle`/`subscribeHandler` escape hatch; (2) `transport.go`'s reflection `Client.Subscribe` shim; (3) `(*caller).ServeSubscribers` + reflection body | **3** independent implementations — **confirms mqtt v3 shares mqtt5's exact 3-way shape, a cross-check not previously done explicitly for v3** | Duplicated logically (coverage check + ApplyCapabilities + Fn-shape validation re-implemented 3×), though all funnel to the same low-level decode helper | adapter_test.go, binding_test.go, caller_test.go, transform_dispatch_test.go | In-code comments flag several of these as "PREVIOUSLY-MISSING"/belatedly added — a recurring regression pattern of new dispatch paths omitting the check |
| Publish dispatch | mqtt | (1) `publish[T]`/`publishHandle[T]` shared primitive; (2) `transport.go`'s reflection `Client.Publish` shim | **2** independent implementations | Shared primitive reused by binding/transport wrappers, but coverage-check logic still duplicated between primitive and reflection path | adapter_test.go, publish_adapter_observer_test.go, publish_error_channel_exclusion_test.go | Dedicated regression-test filenames indicate past ErrorChannel/DeadLetter exclusion bugs on this path |
| Capability coverage check | mqtt | 6 call sites (listed in external-seam row above) | 6 | Scattered, not centralized — each dispatch path re-implements the `events.VerifyCapabilityCoverage` call | capability_test.go + per-path test files | Multiple comments explicitly document several of these 6 sites as "PREVIOUSLY-MISSING"/belatedly added |
| ErrorPattern/DeadLetter consultation | mqtt | `tryPublishErrorChannel`/`tryDeadLetter` (shared helpers) + reflection mirrors in transport.go and ServeSubscribers | 4 independently-reachable consultation implementations | Centralized in 2 shared helpers for non-reflection paths; reflection paths re-implement equivalent logic via `reflect.Value` rather than calling the helpers directly | error_channel/dead_letter test files | Explicit comments confirm `ServeSubscribers` was missing this entirely in a prior revision — "same regression class as mqtt5" |

**Biggest finding**: `adapters/mqtt` (v3) is a fully independent codebase
from `adapters/mqtt5` (not a shared package, deliberately — its own doc
comment says so), yet is a near line-for-line structural mirror, including
**the same 3-way Subscribe duplication and the same historical regression
pattern of newer paths initially missing coverage/DeadLetter checks**
before being belatedly patched. This means any future consolidation fix
designed only against mqtt5 will need a **separate, parallel fix for mqtt
v3** — they do not share code that a single change could fix once.

##### `adapters/mqtt5`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| events Subscribe dispatch | mqtt5 | (1) `makeSubscribeMessageHandler[T]` (adapter.go:273, generic escape-hatch); (2) `(*transport).Subscribe` (transport.go:462, reflection); (3) `(*caller).ServeSubscribers`→`makeErasedSubscribeMessageHandler` (caller.go:122,426) | **3** independent implementations | Duplicated logic across all 3, confirmed by `transport_dispatch.go`'s own doc comment (explicitly a "reflection-only mirror," structurally separate) | adapter_test.go, transport_test.go, caller_test.go | `transport_dispatch.go` comments document a historical bug: "ServeSubscribers dispatch previously consulted NEITHER mechanism" — fixed later |
| events Publish dispatch | mqtt5 | (1) `publish[T]`/`publishHandle[T]` (adapter.go:943,1218); (2) `(*transport).Publish` (transport.go:174, reflection mirror) | **2** independent implementations | Partially shared small helpers, but each has its own full dispatch body — acknowledged duplication, not full sharing | adapter_test.go, transport_test.go, client_full_pipeline_test.go | A fixed RequireQoS/coverage-check gap historically present in only one of the two paths |
| reqreply Call dispatch | mqtt5 | `(*clientTransport).Call`/`call` (reqreply_transport.go:975,989); `CallRaw` (1497, separate bypass) | 1 primary + 1 raw-bypass variant | Single typed implementation; `CallRaw` is an intentional separate escape hatch | reqreply_test.go, callraw_test.go | Past forward/error-pattern-precedence bugs referenced in test filenames |
| reqreply Serve dispatch | mqtt5 | `(*serverTransport).Serve` (reqreply_transport.go:347) | 1 implementation | Single, not duplicated | reqreply_test.go, grantedscopes_reqreply_test.go | Documented fixed asymmetries ("closing an asymmetry with...") |
| Capability coverage checking — events | mqtt5 | adapter.go:791,970; caller.go:157 — **transport.go's reflection Subscribe/Publish show NO independent `VerifyCapabilityCoverage` call, only `ApplyCapabilities`** | 3 confirmed explicit sites; **potential gap flagged on the reflection path** | Shared function (`events.VerifyCapabilityCoverage`), called from 3 distinct sites — reflection path's coverage may be weaker | adapter_test.go, caller_test.go, capability_test.go | capability_test.go notes — reflection path gap is a fresh observation from this audit, not previously documented |
| Capability coverage checking — reqreply | mqtt5 | reqreply_transport.go:440 (CheckCoverage) + :462,1192 (VerifyCapabilityCoverage) | 3 call sites | Shared function, called at 3 distinct sites, resolved once per Serve setup (not per-message) | capability_reqreply_test.go | Documented "per-message vs per-setup" distinction |
| ErrorPattern/DeadLetter | mqtt5 | events: `tryDeadLetter`/`tryPublishErrorChannel` (~14 sites); reqreply: `tryDeadLetterReflect` + `ErrorPatternResponse` matching | 1 shared helper per side, many invocations | Shared helper, duplicated call-site pattern (not logic duplication) | dead_letter_test.go, dead_letter_reqreply_test.go, error_pattern tests | "Category A fix," "now ErrorPattern-eligible" comments indicate past gaps on certain failure paths |
| Observer reporting | mqtt5 | `obs.RecordSubscribe`/`RecordPublish` (dozens of sites, 3 impl families for Subscribe + 2 for Publish); `obs.RecordRequest` (40+ sites, reqreply) | Many duplicated call-site families | Duplicated call-site patterns across the 3 Subscribe/2 Publish implementations | error_pattern_observer_test.go, disposition_test.go | Repeated optional-interface-check pattern (SecurityObserver/TraceObserver/DispositionObserver) duplicated identically 3× |

**Biggest finding (reconfirmed fresh, with one new addition)**: events
Subscribe has 3 and Publish has 2 fully independent, hand-maintained
dispatch implementations, with an extensive documented history of each
capability/error/observer concern independently regressing on at least one
of the parallel paths before being fixed. **New in this fresh pass**:
`transport.go`'s reflection Subscribe/Publish paths show only
`ApplyCapabilities` calls, not an independent `VerifyCapabilityCoverage`
pre-check — a potential coverage gap on the reflection path that the
original narrower evidence pass did not surface as explicitly.

##### `adapters/nethttp`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Route dispatch (decode/validate/encode/error pipeline) | nethttp | `serve()`/`ServeOne`→`buildRouteHandler` (serve.go:159,218,237) | **ONE** reflection-based implementation — confirmed still the sole REST route-dispatch path; "reference pattern" claim holds **for this entry point** | N/A (reference) | serve_test.go, adapter_test.go | n/a — reference implementation |
| Route dispatch (ports-binding path) | nethttp | `handlerFunc[Req,Resp]` (adapter.go:167), called from binding.go/stream.go | **A genuinely SECOND, independent implementation — NOT a thin delegate of `buildRouteHandler`** | **Duplicated**, not shared — full generics-typed re-implementation of body decode, format negotiation, validation, merge-fields, middleware Fn application, encode, and error handling | binding_test.go, port_adapter_error_pattern_test.go, gap1_ports_middleware_dispatch_test.go | `d-0005`:25-37 explicitly documents this as a SEPARATE dispatch function, originally out of scope for ErrorPattern/Observer fixes, later patched (H1) to add parity — **duplication itself was never eliminated, only the gap it caused** |
| SSE dispatch | nethttp | `serveSSE`/`buildSSERouteHandler` (serve_sse.go:53,98) | ONE implementation, but conceptually parallel to (not reused from) `serve.go`'s pipeline | Duplicated vs. serve.go (format negotiation/decode/validate reflect helpers largely re-implemented) | transform_sse_dispatch_test.go | Comment flags no ErrorPattern concept for SSE — intentional scope gap, not a regression |
| ErrorPattern consultation | nethttp | `tryRespondErrorPattern` (serve.go:848) vs. `tryRespondErrorPatternGeneric` (adapter.go:1043) | TWO separate functions, one per dispatch path | Duplicated by necessity since the two dispatch paths are themselves duplicated | error_pattern_category_a_test.go, port_adapter_error_pattern_test.go | `d-0005` documents `handlerFunc` originally had ZERO ErrorPattern coverage until a later pass (H1) added the generic counterpart |
| Observer reporting | nethttp | ~6 sites in serve.go vs. ~9 in adapter.go | Independent call-site sets | Duplicated call sites, same `stats` package reused (shared library, duplicated wiring) | redirect_observer_test.go, gap1_ports_middleware_dispatch_test.go | `d-0005` separately notes REST's middleware-fn/handler-error branches historically had "ZERO observer calls" before fixes — parallel gap addressed via the H1 finding |
| **Capability coverage check** | nethttp | `rest.VerifyCapabilityCoverage` present in BOTH `serve.go` and `serve_sse.go` — **absent in `handlerFunc`/binding.go entirely** | Present in 2 of 3 dispatch paths | **Not shared with `handlerFunc` — a confirmed, previously-under-examined gap**: ports-binding routes declaring capability requirements silently skip the eager-failure guarantee REST/SSE routes get | capability_test.go exercises only the reflect-based paths | **No regression history found — this appears to have simply never been extended to the ports-binding path; a genuinely new finding from this fresh audit, not previously flagged in design docs** |

**Biggest finding — corrects the doc's own earlier "Confirmed asymmetry"
table**: the common assumption that `adapters/nethttp`'s REST dispatch is
*the single converged reference pattern* holds only for the standard
REST/SSE surface (`serve.go`/`serve_sse.go`). Once the `ports`-binding
adapters are included, nethttp **also has two parallel, independently
evolving dispatch implementations** (reflect-based vs. generic-based),
sharing only library primitives, not code — plus a confirmed capability-
coverage gap on the generic side that has never been closed. See
"Reconciliation with Preliminary evidence" below.

##### `adapters/openai`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Completion call dispatch | openai | `complete[Req,Resp]` (client.go:88) — sole implementation | 1 | Not duplicated — single generic function, no streaming variant, no second call path | client_test.go (11 dedicated cases) | One commit fixed a missing-Name/Observer-placement bug; no architectural duplication |
| Streaming dispatch | openai | None — `Transform` fans discrete request items through the same single `complete()` call | 0 (token-by-token streaming doesn't exist for this adapter) | N/A | N/A | N/A |
| Error handling | openai | 6 exported error types, each constructed at a single site in `complete()` | 6 distinct types, each with exactly 1 construction path | Not duplicated | Each has a dedicated test + LogValue test | Same commit fixed a Name-field bug across these constructions |
| Observer reporting | openai | 9 `RecordRequest` sites + 1 `ReportErrors` site, all inlined in `complete()` | 9+1 call sites, all within one function | Duplicated in the sense that every exit branch repeats the call inline rather than via `defer`/helper | Verified via `recordingObserver` test double | Same commit explicitly fixed "observer location" |

**Clean baseline — no internal-seam fragmentation problem to report.** The
one known regression was a correctness bug (wrong Name/Observer placement),
not architectural duplication.

##### `adapters/redis`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Get dispatch | redis | `GetAdapter.Transform`→`GetMerged`→`Get`; `Seed` (warm-restart wrapper) | 4 entry points, 1 underlying implementation | Shared — all funnel through `Get` | Extensive per-entry-point tests | A dedicated parity test (`TestGetSetAdapter_DelegatesToGetSet_SameBehavior`) suggests prior drift risk between adapter/plain-function behavior was a known concern |
| Set dispatch | redis | `SetAdapter`, `DrainSetAdapter`, `Set`, `SetHandle` — all call shared `writeThrough`+`keyVarsFor` | 1 shared implementation, 4 entry points | **Shared** — explicitly documented "Shared by Set, SetAdapter, and DrainSetAdapter" | Extensive per-entry-point tests | Same parity test covers this path too |
| Subscribe dispatch (pub/sub) | redis | **None — not implemented** | 0 | N/A | 0 tests | `docs/roadmap/redis-pubsub.md` confirms it hasn't shipped — design draft only |
| Key-codec handling | redis | `cache.BuildKey` called independently at 2 sites (Get, writeThrough) rather than one shared key-resolution helper | 2 independent call sites to a shared external function | Partially duplicated — minor | TestCache_BuildKey + missing-var tests | No explicit regression; low-risk since behavior is identical |
| Error wrapping | redis | `CacheError{}` constructed independently ~6+ times, no single factory function | Duplicated struct-literal construction | Low-risk (trivial struct), but genuine duplication | LogValue/chain tests | None noted |
| Observer reporting | redis | Observer *resolution* duplicated at 5 independent sites; Observer *recording* centralized in `Get`/`writeThrough` | 5 resolution sites + 2 recording call sites | Mixed — resolution duplicated, recording shared | Nil-safety deliberately tested | No incident history; nil-safety was a deliberate design concern |

**Biggest finding**: the two core helpers (`Get`, `writeThrough`) genuinely
prevent duplication for the operations that exist — the real gap is the
**entirely unshipped pub/sub side**, confirmed still just a design draft.

##### `adapters/sql`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Row/param validation | sql | `Validate()` called from 3 sites (Query, DrainInsert, QueryEach), defined once | 1 shared implementation, 3 call sites | **Shared** — single function reused | binding_test.go + validate_test.go (9 funcs) | A commit added test-quality-gap coverage to validate_test.go |
| Table/Op default resolution | sql | `resolveTableOp`, called at 3 sites | 1 shared implementation | Shared | Default-resolution tests for Drain/QueryEach; **QueryAdapter's own path has no dedicated test — a coverage gap** | None found |
| Query dispatch (poll-based) | sql | `QueryAdapter.Activate` (single ticker loop) | 1 | N/A (only one query-dispatch adapter) | 3 dedicated tests | None found |
| DrainInsert dispatch | sql | `DrainInsertAdapter.Activate`, delegates looping to shared `gstream.Drain` | 1 (adapter-local closure) + shared loop mechanics | Shared loop mechanics; validation/insert closure is adapter-local | 3 dedicated tests | None found |
| QueryEach dispatch | sql | `QueryEachAdapter.Transform`, bespoke goroutine fan-in — **does NOT reuse `gstream.Drain`** | 1 implementation, hand-rolled, structurally distinct from DrainInsert | **Duplicated control-flow pattern** — separate fan-in mechanics from the shared helper DrainInsert uses | 3 dedicated tests | None found |
| Migration handling | sql | `Up`/`Down`/`Status`, shared `newProvider()` + 3 distinct methods with near-identical obs/err patterns | 1 shared constructor + 3 near-duplicate method bodies | Shared construction; dispatch logic duplicated per-method, not factored into one helper | 7 test funcs | A commit added 34 lines of test-quality-gap coverage — regression-driven hardening |
| Error wrapping | sql | 4 distinct error types, each independently implementing `Error()`/`Unwrap()`/`LogValue()` | 4 types, structurally identical boilerplate, no shared base | Duplicated boilerplate (same shape, no shared helper) | LogValue tests for 2 of 4 types found — **gap for RowValidationError/MigrationError** | None found |
| Observer reporting | sql | 6+ independent `ObserverFromContext` fallback blocks; actual recording via shared `stats` functions | 6+ resolution sites | Mixed — resolution duplicated, recording shared | No isolated Observer-call-site unit tests in the sql package itself | None found |

**Biggest finding (reconfirms prior roadmap note)**: `ports.SQLPattern` is
confirmed strictly metadata-only — no handle, no capability-requirement
mechanism, no ErrorPattern/DeadLetter anywhere. The adapters' only real
shared logic is `Validate`/`resolveTableOp`; the three dispatch loops
themselves (ticker-poll, shared-`gstream.Drain`, hand-rolled fan-in) are
structurally distinct by necessity, not duplicated unnecessarily.

##### `adapters/templ`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Buffered HTML rendering | templ | `Format[Props]` marshal closure (adapter.go:47-53) | 1 | Shares the underlying `templ.Component.Render` call with the streaming variant, but buffering logic is unique | adapter_test.go | None found |
| Streaming HTML rendering | templ | `StreamingFormat[Props]` marshal closure (adapter.go:80-82) | 1 | Structurally distinct closure from the buffered variant — no shared internal helper between the two | adapter_test.go | None found |
| Decode/unmarshal (explicitly unsupported) | templ | Both `Format` and `StreamingFormat` independently return `DecodeNotSupportedError` | 2, duplicated, identical logic | **Duplicated** — not factored into a shared function (minor) | adapter_test.go | None found |
| Props validation | templ | Entirely delegated to caller-supplied `codex.Codec[Props]` | 0 (pass-through) | Shared with every other format adapter via the common `format` package | N/A | None found |

**Thin, single-purpose adapter — no dispatch logic, no capability
mechanism, no Observer calls of its own.** The only duplication is the
trivially-small `DecodeNotSupportedError` stub written out twice.

##### `adapters/websocket`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| Upgrade/validation | websocket | `upgradeAndValidate` (binding.go:33-102), 3 call sites (Ingest/Broadcast/Duplex) | 1 shared implementation | **Shared** | binding_test.go (20 funcs) | None noted |
| Capability/param-kind coverage check | websocket | `checkSocketParamKindCoverage` (capability.go:76-82), 3 call sites | 1 shared implementation, explicitly commented "to avoid tripling the same 4-line check" | **Shared** | capability_test.go | None |
| Inbound decode loop | websocket | `readLoop` (binding.go:103-131), 4 call sites across server+client | 1 shared generic implementation | **Shared** — genuinely one core, **contrast with mqtt5/zeromq's 3-way duplication** | binding_test.go + client_test.go | None |
| Outbound/broadcast send | websocket | Broadcast's `gstream.Drain`-based loop vs. Duplex's own inline select-loop | 2 independent implementations | **Partially duplicated** — both reimplement per-session hub.send + MergeOutbound + Marshal + RecordPublish bookkeeping | binding_test.go | Structurally the one place genuine duplication exists here (smaller-scale than mqtt5/zeromq) |
| Client-side managed-connection write | websocket | `managedConn.write`, used by DialSink + DialDuplex | 1 shared implementation | **Shared** | client_test.go | None |
| Reconnect/dial-loop handling | websocket | `dialLoop`, used directly and via `managedConn.readEach` | 1 shared implementation | **Shared** | client_test.go | None |
| Error handling — upstream pipeline errors → ErrorFrame | websocket | Broadcast's and Duplex's own `handleUpstreamError` closures | 2 independent implementations | **Duplicated** — explicitly acknowledged in comments as mirroring each other rather than sharing one function | binding_test.go | Explicit comment flags the duplication as intentional mirroring, not accidental |
| Declarative middleware | websocket | N/A | **0** — confirmed fresh via repo-wide grep, zero `MiddlewareHandlers`/`DispatchSubscribeMiddleware`/`SubscribeMW` usage | N/A | N/A | Reconfirms the roadmap's earlier finding that this package has no declarative middleware concept at all |
| chi-variant delegation | websocket | — | **0** — package depends only on a narrow `Mux` interface, not on `adapters/chi` at all | N/A — no chi coupling found | N/A | No duplication axis here, contrary to what might be assumed from nethttp/chi's pattern |

**Biggest finding**: websocket's core dispatch (`upgradeAndValidate`,
`readLoop`, `dialLoop`, `managedConn`) is **genuinely shared** across
server and client variants — materially better than mqtt5/zeromq's
situation. The one real duplication pocket is the
`handleUpstreamError`/`ErrorFrame`-matching closure between Broadcast and
Duplex.

##### `adapters/zeromq`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage per entry point | Known regression history |
|---|---|---|---|---|---|---|
| events Subscribe dispatch | zeromq | (1) `subscribeWithHandle[T]` (adapter.go:348, generic); (2) `(*transport).Subscribe` (transport.go:360, reflection); (3) `(*caller).ServeSubscribers`→`dispatchToRoute`/`processMessage` (serve_subscribers.go:302,363,491) | **3** | Duplicated (3 independent recv/decode/dispatch loops); `serveOneSubscriber` is a thin wrapper of (3), not a 4th implementation | transport_test.go, caller_test.go, stream_test.go | **Code comments explicitly document a historical bug**: Subscribe's coverage check was missing on the `transport.go`/`ports.Pattern` reflection path even though the generic and `ServeSubscribers` paths had it — confirmed shipped regression |
| events Publish dispatch | zeromq | (1) `publish[T]`/`publishHandle[T]` (adapter.go:870,1033); (2) `(*transport).Publish` (transport.go:156) | **2** | Duplicated (fewer than Subscribe — no publish-side server-loop equivalent) | publish_adapter_observer_test.go | Same coverage-check gap noted for Publish ("mirrors Transport.Publish's identical fix") |
| reqreply plain-Serve dispatch | zeromq | `(*serverTransport).Serve` (reqreply_transport.go:508) | 1 | N/A alone, but sibling-duplicated vs. ROUTER below | reqreply_transport_test.go, dead_letter_reqreply_test.go | — |
| reqreply ROUTER-Serve dispatch | zeromq | `(*routerServerTransport).Serve` (reqreply_transport.go:1555) | 1 | Duplicated relative to plain-Serve (separate recv loop keyed on identity frame), but shares `applyGeneralServerMiddleware` with plain-Serve | forward_reqreply_test.go | Comment notes it replaces "now-deleted ServeRouter" — history of partial consolidation |
| reqreply plain-Call / DEALER-Call dispatch | zeromq | `(*clientTransport).call`/`CallRaw`; `(*dealerClientTransport).call`/`CallRaw` | 2+2 (typed + raw, per variant) | Duplicated — necessitated by envelope framing differences (empty-delimiter+identity vs. socket-implicit routing) | callraw_test.go, capability_reqreply_test.go, dead_letter_reqreply_test.go | — |
| Capability coverage checking | zeromq | 4 reqreply call sites (plain-Serve, ROUTER-Serve, plain-Call, DEALER-Call) + 2 events call sites (Subscribe, Publish, via `ApplyCapabilities`) | 4 + 2 | Duplicated call sites, but all route through shared `reqreply.CheckCoverage`/`VerifyCapabilityCoverage`/`events.ApplyCapabilities` — logic shared, call sites duplicated | capability_reqreply_test.go (4 tests, one per variant), capability_test.go | Explicit "confirmed-missing gap" fixed in Subscribe/Publish |
| Observer reporting | zeromq | pub/sub: adapter.go + serve_subscribers.go; reqreply: adapter.go + reqreply_transport.go | ≥6 distinct call-site families | Duplicated per socket-variant, no shared single Observer-emit helper across pub/sub vs. reqreply | publish_adapter_observer_test.go, error_channel_category_a_test.go | — |

**Biggest finding**: zeromq has 3 independent events-Subscribe and 2
events-Publish dispatch loops (same shape as mqtt5), PLUS the reqreply side
doubles again across plain vs. ROUTER/DEALER socket variants — a confirmed,
shipped coverage-check regression on the reflection path is directly
documented in code comments, the clearest "textbook N-implementations
regression" evidence found in this entire audit.

##### `adapters/internal`

| Capability (or middleware concern) | Adapter | Entry point | Implementation count | Shared vs. duplicated | Test coverage | Known regression history |
|---|---|---|---|---|---|---|
| Legacy Fn grants collection + CheckScopes | nethttp, chi | `httpsecurity.RunSecurityMiddlewareReflect`/`CollectGrantsReflect` | 1 (shared) | Shared | httpsecurity_test.go | None noted |
| MiddlewareHandler GrantedScopes merge (REST) | nethttp, chi | `httpsecurity.MergeMiddlewareHandlerGrants` | 1 (shared) | Shared | httpsecurity_test.go | None noted |
| MiddlewareHandler GrantedScopes merge (events/reqreply) | mqtt, mqtt5, zeromq | `scopesmerge.MergeHandlerGrants` | 1 (shared across these 3) | Shared across the 3, but **duplicate-by-design relative to `httpsecurity`'s near-identical function** — the two packages were deliberately kept separate rather than unified | scopesmerge_test.go, zeromq/grantedscopes_reqreply_test.go | **Comment explicitly documents a found regression**: an unconditional `CheckScopes` call broke zeromq's grant-less legacy security — fixed by introducing `HasSatisfyingHandler` |
| CheckScopes invocation gating | mqtt, mqtt5, zeromq | `scopesmerge.HasSatisfyingHandler` | 1 (shared) | Shared | scopesmerge_test.go | Same regression as above |

**Biggest finding — reinforces the roadmap's own problem statement rather
than contradicting it**: `adapters/internal` is NOT a cross-adapter
dispatch-core. It contains exactly one narrow, deliberately-scoped utility
concern (grants merging before `CheckScopes`), and even that one concern is
implemented **twice** (`httpsecurity` vs. `scopesmerge`) by explicit
design choice rather than unified further — confirming no shared
dispatch-core exists anywhere in the codebase today for capability
checking, middleware application, or Observer/error-pattern reporting.

#### Reconciliation with "Preliminary evidence"

This fresh, independent pass agrees with the earlier Doc A/Doc B grounding
on every claim it re-examined, and extends it in several places. Two
corrections are important enough to flag explicitly rather than leave two
silently-differing accounts in this doc:

1. **Correction: REST (`adapters/nethttp`) is not a single converged
   dispatch path once the `ports`-binding adapters are included.** The
   "Confirmed asymmetry" table above (and several other passages)
   describe REST's dispatch as "ONE — the reference pattern" based on
   `serve.go`'s reflection path alone. This fresh audit confirms that claim
   is only true for the standard REST/SSE HTTP surface; `handlerFunc`
   (`adapter.go:167`, backing `IngestAdapter`/`LatestAdapter`/
   `PipelineHandler`/etc.) is a second, independently-maintained, fully
   generic re-implementation of the same pipeline, and — a genuinely new
   finding — it has **no capability-coverage check at all**, unlike
   `serve.go`/`serve_sse.go`. The "Confirmed asymmetry" table's REST row is
   updated below to reflect this.
2. **Extension, not correction: `adapters/mqtt` (v3) shares mqtt5's exact
   3-way Subscribe-dispatch shape.** Prior evidence focused on mqtt5;
   Doc B's own scope statement named `mqtt`/`mqtt5`/`zeromq` together but
   the detailed 3-way citation was always mqtt5-specific. This audit
   confirms, independently, that mqtt v3 (a fully separate codebase, not a
   shared package) has the identical structural shape and the identical
   history of newer paths initially missing capability/DeadLetter checks —
   meaning a consolidation fix designed only against mqtt5 will need a
   **separate, parallel fix for mqtt v3**.
3. **Corroboration: the reqreply 6-call-site duplication finding holds.**
   The earlier "Preliminary evidence" pass found 6 independent
   capability-coverage-check call sites across mqtt5+zeromq's reqreply
   Serve/Call sides. This fresh, adapter-by-adapter audit independently
   reaches the same conclusion via a different counting path (mqtt5: 3
   reqreply call sites; zeromq: 4 reqreply call sites across its 4 socket
   variants) — consistent in substance, though the per-adapter breakdown
   differs slightly by counting granularity (CheckCoverage vs.
   VerifyCapabilityCoverage counted separately in this pass).
4. **New, not previously surfaced: `adapters/chi` independently duplicates
   `adapters/nethttp`'s entire dispatch/error-pattern/Observer logic
   line-for-line**, by explicit design (no import relationship between the
   two packages). This is a cross-adapter duplication axis the per-adapter
   matrix template captures only partially (each package's own row looks
   "internally" fine; the duplication is *between* chi and nethttp, not
   within either one alone). Phase 2 should treat the REST family as
   **three** dispatch surfaces needing consolidation design (nethttp's
   reflect path, nethttp's generic path, and chi's independent mirror of
   both), not the one or two previously assumed.
5. **New: `adapters/internal` is confirmed NOT a shared dispatch-core.**
   This was an open question in the roadmap's framing ("no real shared
   dispatch-core exists yet anywhere in the codebase" was an assumption,
   not a confirmed fact) — this audit confirms it directly: the one
   existing shared-utility package (`adapters/internal`) addresses a single
   narrow concern (grants merging) and is itself split into two
   deliberately-unmerged implementations.
6. **New: several adapters have no duplication problem at all** — `file`,
   `sql` (dispatch-loop level), `openai`, `templ`, `mcpgo`'s tool-call path,
   and (mostly) `websocket`'s core dispatch (`readLoop`/`upgradeAndValidate`/
   `dialLoop`/`managedConn` are genuinely shared). This matters for Phase 2
   prioritization: the events/reqreply family (mqtt/mqtt5/zeromq) plus the
   REST family (nethttp/chi) concentrate essentially all of the confirmed
   duplication found across all 14 targets; the `ports`-only adapters
   (file, redis, sql) and the simpler single-purpose adapters (openai,
   templ, mcprest) do not need the same Phase 4 consolidation effort.

##### Correction to "Confirmed asymmetry" table

The `api/rest` row in the "Confirmed asymmetry" table (near the top of this
doc) should be read as superseded by this fresher finding:

| Pattern | Dispatch paths confirmed | Converged? |
|---|---|---|
| `api/rest` (`adapters/nethttp`), standard HTTP/SSE surface only | ONE — `serve.go`'s `ServeOne`/`Serve` + `serve_sse.go`'s `serveSSE`, both reflection-based | **YES**, for this surface only |
| `api/rest` via `ports`-binding adapters (`IngestAdapter`/`LatestAdapter`/`PipelineHandler`/etc.) | A SECOND, independent, generic (non-reflection) implementation (`handlerFunc`, `adapter.go:167`) — confirmed not a thin delegate | **NO** — two parallel dispatch implementations, confirmed missing a capability-coverage check the reflect-based surface has |
| `adapters/chi` (both surfaces) | Independently duplicates `adapters/nethttp`'s entire dispatch, error-pattern, and Observer logic — no import relationship, no shared code | **NO** — a third and fourth dispatch implementation, line-for-line mirrors of the two above |

#### Overall synthesis — where Phase 2/4 effort should concentrate

Ranked by confirmed duplication severity and regression history found in
this audit:

1. **Highest priority: events/reqreply across `mqtt`/`mqtt5`/`zeromq`.**
   Every one of the three adapters shows 2-4 independent dispatch
   implementations per capability, a long, explicitly-documented history of
   each one independently regressing (missing coverage checks, missing
   DeadLetter consultation, missing middleware dispatch), and zeromq's
   reqreply side doubles the count again via ROUTER/DEALER socket variants.
   This is exactly where Round 2's "per pattern family" dispatch-core
   decision should be spent first.
2. **Second priority: the REST family across `nethttp`/`chi`.** Less
   severe than events/reqreply (no multi-adapter protocol-version split
   like mqtt/mqtt5), but genuinely two-to-four parallel implementations
   once `ports`-binding and `chi` are counted, plus one confirmed,
   previously-unflagged capability-coverage gap (`handlerFunc`).
3. **Lower priority, narrower fixes only: `websocket`'s
   `handleUpstreamError` duplication**, `chi`'s own Gap-1 regression (now
   fixed), `file`'s asymmetric Observer wiring, and `mcpgo`'s duplicate
   resource-handler closures — each is a small, self-contained fix, not a
   consolidation redesign.
4. **No action needed**: `file`, `sql` (dispatch level), `openai`,
   `templ`, `mcprest`, and `adapters/internal` show no duplication problem
   warranting Phase 4 attention; `redis`'s only gap (unshipped pub/sub) is
   out of scope until that feature actually ships.

**Deliverable status: COMPLETE.** The current-state diagram, external-seam
compatibility matrix, and internal-seam duplication/coverage matrix called
for at the start of this Phase 1 section are the two matrix result sets
above, covering all 14 adapter packages. Phase 2 design work may now begin.

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
- apply this directly to reqreply's Call/Serve side — already confirmed by
  the "Preliminary evidence" pass to have its own duplication shape (6
  independent capability-coverage-check call sites, worse than and
  differently shaped from events' Subscribe), not waiting on further
  confirmation;
- extend the same treatment to events' Publish side once Phase 1 confirms
  its dispatch-implementation count (still open — see "Confirmed
  asymmetry").

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

**Immediate, low-effort action item found by the Doc B grounding pass**: the
`Client.Attach` + `ServeSubscribers` + `SubscribeMW`/`GrantedScopes`/
`ContextField` combination — the exact entry point `d-0007`'s regression was
about — is currently exercised only by a runnable example
(`examples/events-api/demo_granted_scopes_context_field.go`), not an
automated test. This doesn't need to wait for the full Phase 1 audit or
Phase 4 redesign; it can be closed directly as a conformance-test seed, and
doing so early is a concrete demonstration of this phase's own principle.

## Open design points deferred to future rounds

These are raised now, during this roadmap's drafting, so they are not lost —
but they are explicitly **not decided here**. Each needs its own design
round once Phase 1's audit findings are in hand.

*(The API-defined conformance test suite mechanism that originally lived
here was resolved in Round 3 — see "Resolved design decisions" below. This
section is currently empty; new narrower gaps will be added here as they
surface in future rounds.)*

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
one channel requiring a minimum QoS or ZeroMQ HWM (the corrected,
already-working illustration — see "Capabilities are requirements" and
Phase 3), and the confirmed events-Subscribe 3-implementation case. Use
those examples to decide
whether the existing registered handles already provide the right binding
contract, whether a smaller adapter-facing interface is needed, and whether
a shared dispatch-core is mechanically achievable across the
compile-time-generic/reflected boundary.

The desired result is a simple experience for API users and an equally
clear, single-implementation extension point for adapter authors — without
coupling declarations to protocols, accumulating unneeded abstraction
layers, or letting one adapter's multiple entry points drift out of sync
with each other again.
