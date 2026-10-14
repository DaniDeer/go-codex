# Adapter Integration Architecture: a Unified Roadmap

## Status

**In progress — audit, design, and proof-of-concept complete; implementation
substantially underway.** Phase 1's full 14-adapter audit is done (both
matrices below). Phase 2's capability-semantics and dispatch-core design is
done for the REST, SSE, and events/reqreply families. Phase 3's proof of
concept is fully done (7/7 cases plus the case-6 follow-up). Phase 4
implementation: the REST family (8/8 steps), the SSE family
(construction+dispatch consolidation + conformance test), and the
events/reqreply family's three workstreams (`adapters/mqtt5`,
`adapters/mqtt` v3, `adapters/zeromq`) are **all fully complete** —
Subscribe dispatch-core consolidation, Publish capability-coverage-
ordering fixes, zeromq's reqreply server/client-side splits, and mqtt5's
own reqreply setup-core extraction (Workstream A's final step) all done;
see "Phase 4 — Implementation" below for the per-workstream writeups.
Phase 5 has two conformance tests seeded
(`adapters/mqtt5/granted_scopes_context_field_test.go`,
`adapters/mqtt5/reqreply_dispatch_core_test.go`); the rest of its
"reusable adapter conformance tests for common semantics" scope, and the
still-open "API-defined conformance test suites" ownership question, remain
for a future round. This status line is updated as major increments land —
see the per-phase sections below for full detail.

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
   (a view, or co-location), not a replacement type. **Confirmed by the
   completed Phase 1 audit** (see "Phase 1 audit results" above): the
   full, independent, all-14-adapter audit found no contradiction — REST's
   granularity problem, events' already-working handle, and reqreply's
   capability/middleware split were each reconfirmed exactly as this
   decision anticipated from the earlier preliminary pass. No longer
   provisional. **Guardrail (critical-review pass):** per-family does not
   mean
   undocumented-per-family — Phase 2 must still produce one documented,
   consistent adapter-facing *contract shape* (e.g. "declare + requirements
   + middleware-handle, always three named parts") even though each
   family's concrete type differs, so this doesn't quietly recreate the
   roadmap's core cross-adapter-asymmetry complaint one layer up, at the
   binding level instead of the dispatch level.
2. **Decision 2 (dispatch-core mechanism), refined — resolved *per pattern
   family*, not universally.** Events/reqreply and REST each get separate
   consolidation designs in Phase 2/4, not one shared cross-pattern-family
   mechanism. Still open within each family: shared-helper-package vs.
   per-adapter-local convention — that narrower choice is deferred to
   Phase 2 for each family independently. **Confirmed by the completed
   Phase 1 audit, with scope expanded (not reversed):**
   - **Events/reqreply family — now explicitly includes `adapters/mqtt`
     (v3), not just mqtt5/zeromq.** The audit confirmed mqtt v3 shares
     mqtt5's exact 3-way Subscribe-dispatch shape (generic escape-hatch,
     reflection-based `ports.Pattern` path, `Client.Attach`-driven
     `ServeSubscribers`) and the same history of newer paths initially
     missing capability/DeadLetter checks — a separate, parallel codebase
     needing its own parallel fix, not something a mqtt5-only
     consolidation would also resolve. Reqreply's duplication was
     corroborated independently at a similar severity (mqtt5: 3 call
     sites; zeromq: 4, across its ROUTER/DEALER socket variants).
   - **REST family — newly confirmed as a second consolidation target,
     now explicitly including both `adapters/nethttp` and `adapters/chi`.**
     `adapters/nethttp` has two independent dispatch implementations
     (reflect-based `serve`/`serveSSE` vs. generic `handlerFunc`), the
     latter confirmed missing a capability-coverage check entirely.
     `adapters/chi` independently duplicates **both** of nethttp's paths
     line-for-line, with no import relationship between the two packages.
   - **Guardrail checked against the completed audit — NOT triggered.**
     The critical-review pass's tripwire ("3+ pattern families each
     needing a genuinely distinct dispatch-core mechanism" → flagged risk
     requiring explicit sign-off before Phase 4) does not fire: exactly
     two families are confirmed (events/reqreply; REST), not three or
     more. No escalation/sign-off is required before Phase 4 on this
     basis. This guardrail remains in force for any further expansion of
     scope discovered later.
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

**Gap closed in Phase 3: MQTT user properties.** Doc A's original worked
example used "an event channel may require user properties; MQTT 5
supports them, MQTT 3 does not" as its illustration of this model. The
"Preliminary evidence" audit above found this example did **not** work as
described at the time: MQTT user properties were declared and validated
only through a separate, pre-existing `UserPropertyParam` mechanism, not
through `CapabilityRequirement`/`CheckCapabilityCoverage` — and
`adapters/mqtt` (v3) didn't have the type, so attempting the scenario was a
**Go compile error**, not the clean runtime `CapabilityCoverageError` this
model promises. **This is now fixed and proven** (Phase 3, case 3):
`events.RequireUserProperties()` + `adapters/mqtt5.UserProperties` close
the gap, with MQTT 5 accepting and MQTT 3 rejecting via an actionable
`CapabilityCoverageError` — see "Phase 3 results" below. `UserPropertyParam`
itself is intentionally unchanged (its per-message value-validation
concern is separate, per Design goal 12's split); only the declare-time
requirement-coverage half was folded in.

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
| mqtt5 | `api/events` (Subscribe+Publish) | pub/sub, topic-based | `Capability` (QoS, Retained, **UserProperties, added Phase 3**) — capability.go:35-95; User Properties' per-message value validation remains via the separate, pre-existing `UserPropertyParam` mechanism (capability.go:100-102), only declare-time requirement-coverage was folded in | sealed, `isMQTT5Capability()` marker | Guaranteed-at-setup via `events.VerifyCapabilityCoverage` | Yes — `SubscribeError`/dead-letter/error-channel | 5 explicit call sites, all 3 Subscribe + both Publish entry points: adapter.go:791 (subscribeWithHandle), adapter.go:970 (publish), caller.go:157 (ServeSubscribers), transport.go:521 (Subscribe reflection), transport.go:360 (Publish reflection) — **corrected during Phase 3: all entry points already have this check**, see "Reconciliation" note below | `*events.ChannelHandle[T]` | `events.Transport.Publish/Subscribe/ServeSubscribers`, Attach/Bind | `events.ApplyCapabilities` + `wrapSubscribeGeneral`/`wrapPublishGeneral`/reflection mirrors | `obs.RecordSubscribe`/`RecordPublish`, dozens of sites across adapter.go/transport.go/caller.go | `tryDeadLetter`/`tryPublishErrorChannel`, 14+ call sites in adapter.go |
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
| Capability coverage checking — events | mqtt5 | adapter.go:791,970; caller.go:157; `transport.go:521` (Subscribe), `transport.go:360` (Publish) | **5** confirmed explicit `VerifyCapabilityCoverage` call sites across all 3 Subscribe + 2 Publish entry points | Shared function (`events.VerifyCapabilityCoverage`), called from all 5 sites — **corrected during Phase 3 (see below): every entry point already has this check, confirmed by re-reading the code directly** | adapter_test.go, caller_test.go, transport_test.go, capability_test.go | **Correction (Phase 3):** the Phase 1 audit's claim that `transport.go`'s reflection Subscribe/Publish paths were "missing an independent coverage pre-check" is **factually wrong** as of current `main` — both already call `events.VerifyCapabilityCoverage` (transport.go:521, :360), each with an explicit code comment dating the fix to before this audit ran ("Mirrors Transport.Publish's identical fix"). Treat this as a corrected finding, not an open gap — see the "Correction" note after this matrix. |
| Capability coverage checking — reqreply | mqtt5 | reqreply_transport.go:440 (CheckCoverage) + :462,1192 (VerifyCapabilityCoverage) | 3 call sites | Shared function, called at 3 distinct sites, resolved once per Serve setup (not per-message) | capability_reqreply_test.go | Documented "per-message vs per-setup" distinction |
| ErrorPattern/DeadLetter | mqtt5 | events: `tryDeadLetter`/`tryPublishErrorChannel` (~14 sites); reqreply: `tryDeadLetterReflect` + `ErrorPatternResponse` matching | 1 shared helper per side, many invocations | Shared helper, duplicated call-site pattern (not logic duplication) | dead_letter_test.go, dead_letter_reqreply_test.go, error_pattern tests | "Category A fix," "now ErrorPattern-eligible" comments indicate past gaps on certain failure paths |
| Observer reporting | mqtt5 | `obs.RecordSubscribe`/`RecordPublish` (dozens of sites, 3 impl families for Subscribe + 2 for Publish); `obs.RecordRequest` (40+ sites, reqreply) | Many duplicated call-site families | Duplicated call-site patterns across the 3 Subscribe/2 Publish implementations | error_pattern_observer_test.go, disposition_test.go | Repeated optional-interface-check pattern (SecurityObserver/TraceObserver/DispositionObserver) duplicated identically 3× |

**Biggest finding (reconfirmed fresh)**: events Subscribe has 3 and Publish
has 2 fully independent, hand-maintained dispatch implementations, with an
extensive documented history of each capability/error/observer concern
independently regressing on at least one of the parallel paths before
being fixed. **Correction, found during Phase 3 implementation work**: an
earlier pass of this audit claimed `transport.go`'s reflection
Subscribe/Publish paths were missing an independent
`VerifyCapabilityCoverage` pre-check. Re-reading the code directly while
implementing Phase 3's case 6 found this claim is **wrong** — both calls
already exist (`transport.go:521`, `:360`), each with its own code comment
dated before this audit ran, documenting that exact gap as already fixed
independently on this path. The remaining, accurate finding is narrower but
still real: the requirement-coverage check is correct at all 3 (now
confirmed) entry points, but it is **duplicated 3 separate times**
(2 for Subscribe, 1 shared pattern for Publish's 2 paths) rather than
implemented once — a DRY/future-regression-prevention concern, not a
currently-active bug. See "Phase 3 results" below for the dispatch-core
consolidation that addresses this.

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

**Status: COMPLETE.** Both sub-tracks below are resolved: capability
semantics closed with no new mechanism needed; dispatch-core consolidation
produced concrete, code-grounded designs for both confirmed families (REST:
`adapters/nethttp`+`adapters/chi`; events/reqreply:
`adapters/mqtt`+`adapters/mqtt5`+`adapters/zeromq`) plus a refined,
mechanically-compatible resolution of Design goal 12's capability-coverage
split. See the two "... dispatch-core consolidation design" subsections
below for the concrete, implementer-ready designs. Phase 3 (proof of
concept) and Phase 4 (implementation) may now begin.

Run both sub-tracks together, since they inform each other directly:

- **Capability semantics (external seam):** Write down how requirements
  are declared, advertised, compared, and reported. Ensure capability
  identity and values are semantic rather than accidentally tied to one
  protocol implementation. Do not implement an elaborate generic capability
  framework unless Phase 1's findings demand it. **Resolved: no new
  mechanism needed.** The completed Phase 1 audit found exactly two
  capability forms in use anywhere across all 14 adapters — presence-only
  (`Retained`, `Conflate`) and minimum-level via `>=` (`QoS`, `HWM`) — and
  no adapter anywhere needing the richer taxonomy Round 3 specified
  conceptually (exact-level, weaker-form-satisfiable,
  guaranteed-vs-approximated, observable-vs-detail). Per Round 3's own
  YAGNI framing ("concrete types... Phase 2's job once a real capability
  needing one of these axes is confirmed"), that confirmation never
  arrived — the existing `CapabilityRequirement`/`CheckCapabilityCoverage`
  mechanism is sufficient as-is. This sub-track closes with no new design
  output; revisit only if a future adapter concretely needs one of the
  unimplemented axes.
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

#### Resolved: capability-coverage checking splits into two mechanisms (Design goal 12, refined)

Distinct from dispatch-core consolidation above (which fixes HOW MANY
implementations exist), this sub-goal originally proposed fixing WHERE a
capability-coverage check runs, as a single unified gate. **The open
question below is now answered, and the goal splits into two distinct
mechanisms rather than one gate:**

**Confirmed finding**: a legitimate per-call capability override exists.
`adapters/mqtt5`'s `PublishOptions[T].Capabilities` (adapter.go:152) lets a
caller set QoS (and other capabilities) on an individual publish call, not
only once at channel-declare time — exercised throughout
`adapter_test.go` (e.g. `PublishOptions[sensorReading]{Capabilities:
[]Capability{QoS(1)}}`). A single attach-time gate cannot be the whole
answer for this.

**Resolution — two distinct mechanisms, not one**:

1. **Requirement coverage (declare-time, static).** "Can this adapter
   satisfy the channel's declared `CapabilityRequirement`s at all" is a
   property of the channel declaration and the bound adapter, fixed at
   bind time — this genuinely moves to ONE check at `Attach`/`BindClient`/
   `ServerAwareTransport.BindServer`-equivalent time, exactly as originally
   proposed. Phase 4 removes the scattered per-call-site requirement
   checks once this consolidated gate exists.
2. **Capability application (per-call, dynamic).** Applying specific
   capability values to a specific outgoing message (e.g. the QoS a
   particular publish call actually uses) is inherently per-call and
   cannot be collapsed into a single attach-time gate — this is not a
   location bug to fix, it is the correct, necessary shape. What Phase 4
   *can* still fix is implementing it through exactly ONE shared,
   consolidated code path per adapter (not re-duplicated per dispatch
   entry point) — this folds into the **dispatch-core consolidation**
   goal above, not a separate location-consolidation goal.

Phase 4 scope is therefore: consolidate requirement-coverage checking to
one Attach-time gate (new, standalone work), and ensure capability
*application* goes through the one dispatch-core per adapter that
dispatch-core consolidation already produces (no separate mechanism
needed for it).

#### REST family dispatch-core consolidation design (nethttp + chi)

Concrete design for the REST family's dispatch-core consolidation (Round 2
decision 2's "shared-helper-package vs. per-adapter-local" choice, now
resolved for this family). Grounded directly in code, including an
existing in-repo precedent (`adapters/internal/httpsecurity`/`scopesmerge`)
for exactly this kind of cross-package family-scoped helper.

**Within `adapters/nethttp`**: `serve.go`'s reflection-based
`buildRouteHandler` and `adapter.go`'s generic `handlerFunc[Req,Resp]`
cannot become one Go function — they sit on opposite sides of Go's
generics/reflection boundary (`serve` walks a heterogeneous route slice
with erased `Req`/`Resp`; `handlerFunc` has them as concrete compile-time
parameters, and Go has no reflective generic instantiation). But full
function-level unification isn't needed to close the confirmed gap: three
of the four missing checks (`rest.CheckCoverage`, `VerifyCapabilityCoverage`,
`CheckParamKindCoverage`) already take plain typed slices/maps with no
`Req`/`Resp` in their signatures, and the fourth (shape validation) already
has a working precedent for crossing this exact boundary —
`httpsecurity.CollectGrantsReflect` is already called from both the fully
erased `serve.go` path and `handlerFunc`'s fully generic path, paying a
small reflect cost from the generic side to reach a shared reflect-based
core. **Design: extract a single unexported `validateRouteConstruction`
core** (new function) that both `buildRouteHandler` and `handlerFunc` call
once, each building the same reflect-friendly inputs from their own
starting point. This closes the gap by construction — a future check added
to one path is automatically present in the other.

**Between `adapters/nethttp` and `adapters/chi`**: a shared cross-package
dispatch-core package is feasible and not hypothetical — `adapters/chi`
already imports `adapters/internal/httpsecurity`, whose own doc comment
scopes it explicitly to "the net/http-family adapter tree (adapters/nethttp,
adapters/chi, and any future adapter built on net/http)." The precondition
this relies on — both adapters operating on `*http.Request`/
`http.ResponseWriter` — is independently confirmed: `httpCarrier` and
`applyGeneralMiddleware` are byte-for-byte identical in both packages
today. The one genuine per-package difference is path-param extraction
(`r.PathValue(name)` vs. `chi.URLParam(r, name)`), which has the exact same
signature shape in both and is a clean dependency-injection seam, not a
structural blocker. Chi's router-level registration and middleware chaining
stay entirely outside the shared core, one layer up — exactly how
`applyGeneralMiddleware` already works without any chi-awareness today.

**Resolved design: a new `adapters/internal/restdispatch` package**,
mirroring the `httpsecurity`/`scopesmerge` convention, holding:
`httpCarrier` (promoted, not copied, from the now-duplicate nethttp/chi
definitions), `ValidateRouteConstruction` (the four-check construction-time
core from point 1), and `DispatchRoute` (the full per-request dispatch
body — decode/validate/security/middleware/handle/encode — parameterized
by an injected `pathValues func(*http.Request, []string) map[string]string`
to absorb the one real per-router difference). All four existing entry
points (`nethttp.buildRouteHandler`, `nethttp.handlerFunc`,
`chi.buildRouteHandler`, `chi.handlerFunc`) become thin wrappers around it.

**Recommended Phase 4 implementation order** (each step independently
verifiable before the next):

1. Add `adapters/internal/restdispatch` with `httpCarrier` + 
   `ValidateRouteConstruction`, assembled from nethttp's/chi's existing
   (confirmed identical) implementations.
2. Repoint `nethttp.buildRouteHandler` to call it; verify nethttp's tests
   unchanged (pure extraction, zero behavior change).
3. Repoint `chi.buildRouteHandler` the same way; delete chi's now-dead copy.
4. Extract the per-request dispatch closure into `restdispatch.DispatchRoute`
   (parameterized by `pathValues`); repoint nethttp; verify nethttp's full
   suite before touching chi.
5. Repoint `chi.buildRouteHandler` to the same `DispatchRoute`; delete
   chi's duplicate closure; verify chi's suite.
6. Wire `handlerFunc` in both packages through `ValidateRouteConstruction`
   at construction time, closing the confirmed capability-coverage gap.
7. (Stretch goal, separate step) extend `handlerFunc`'s per-request body
   through `DispatchRoute` too, once step 4 proves the seam works.
8. Add a conformance test exercising `ValidateRouteConstruction`/
   `DispatchRoute` against both a nethttp-shaped and chi-shaped `pathValues`
   with the same fixtures, catching future per-router divergence at the
   shared-package level.

**Confirmed behavioral-compatibility risk, not an invisible side effect**:
step 6 gives `handlerFunc` a new failure mode. Its public signature (used by
`IngestAdapter`/`HandlerLatest`/`PipelineHandler`/etc.) has no error-return
path to extend without a wider breaking change — since backward
compatibility is explicitly a non-goal of this roadmap (see "Constraint:
backward compatibility is not a goal" above), the design accepts a
**panic** (a new, named `restdispatch.ConstructionError` type, not a bare
`panic(err)`) on failed construction-time validation, mirroring
`buildRouteHandler`'s existing "fail loudly, never silently" principle via
the only mechanism available at that call shape. This must be documented
explicitly in each affected function's doc comment and the changelog when
implemented — a route that previously silently served with an unenforced
coverage gap will instead panic at construction/`Activate` time. No
non-panicking opt-out is included speculatively; add one only if a concrete
need appears later. **Also flagged, not yet audited** (at Phase 2
design time): `serve_sse.go` in both packages almost certainly shares this
exact duplication shape and should be treated as a near-certain follow-on
consolidation target, not an afterthought, when Phase 4 is actually
scoped. **Update — confirmed and resolved in Phase 4**: the audit found
this flag correct (`buildSSERouteHandler` was essentially identical across
both packages, construction block and per-event streaming body alike) and
the SSE family's own construction+dispatch consolidation (`restdispatch.
DispatchSSERoute`) plus its own conformance test are now fully done — see
"Phase 4 — Implementation", SSE family section.

#### Events/reqreply family dispatch-core consolidation design (mqtt + mqtt5 + zeromq)

Concrete design for the events/reqreply family's dispatch-core
consolidation, the audit's #1 priority target. Grounded directly in code:
`adapters/mqtt/{adapter,caller,transport,binding}.go`,
`adapters/mqtt5/{adapter,caller,transport,transport_dispatch,
reqreply_transport}.go`, `adapters/zeromq/{adapter,transport,
serve_subscribers,reqreply_transport,handletransport}.go`,
`api/events/capability.go`, `api/reqreply/capability.go`, and
`adapters/internal/scopesmerge`.

##### 1. Within one adapter: Subscribe's 3-way duplication collapses to 2, not 1

Every one of the three adapters has exactly the same two-sided boundary,
not three independent sides:

- **Side A — compile-time generic.** The escape-hatch entry point
  (mqtt5's `makeSubscribeMessageHandler[T any]`, mqtt's equivalent, and
  zeromq's `subscribeWithHandle[T any]`) is built with `T` known at
  compile time. Its handler closure has shape `func(*pahomqtt5.Publish)`
  (or zeromq's socket-frame equivalent) and calls `handle.Decode`/
  `handle.DecodeMergedWithFormats` as **direct, generic method calls** —
  zero `reflect.Value` anywhere in the hot path.
- **Side B — type-erased, `reflect.Value`-driven.** The other two entry
  points — `transport.go`'s `(*transport).Subscribe` (recovers a
  `reflect.Value handleVal`/`elem` once via `recoverHandle`, then calls
  `decodeMergedMethod.CallSlice(...)` per message) and
  `caller.go`'s `ServeSubscribers`→`makeErasedSubscribeMessageHandler`
  (recovers an `erasedSubscriberHandle` struct of pre-extracted
  `reflect.Value` fields via `extractErasedSubscriberHandle`, then calls
  `info.decodeFn.Call(...)` per message) — **already start from the exact
  same type-erasure boundary**: a `reflect.Value`-shaped view over a
  type-erased `*events.ChannelHandle[T]`/`*reqreply.RouteHandle[Req,Resp]`.
  Reading `makeErasedSubscribeMessageHandler` (caller.go:426) next to
  `transport.Subscribe`'s inline handler (transport.go:586) confirms this
  directly: both build the identical `dispatchFailure` closure
  (ErrorChannel→DeadLetter→OnError fallback, same precedence), both run
  decode→user-property-validation→security→codec-Middleware→handler in
  the same order, against fields recovered by reflection from the same
  handle shape. The only structural difference is *when/how* the
  `reflect.Value` fields are obtained — `recoverHandle`+`elem.FieldByName`
  fresh per `Subscribe` call vs. a pre-extracted `erasedSubscriberHandle`
  struct built once per `ServeSubscribers` loop entry — not a difference
  in what the dispatch body does with them.

**Concrete design**: within each adapter, extract ONE unexported
reflect-based dispatch core — e.g. `subscribeDispatchCore` — shaped as:

```
subscribeDispatchCore(
    ctx, client, obs,
    fields erasedChannelFields,   // the reflect.Value/concrete-field bundle
                                    // BOTH transport.Subscribe and
                                    // ServeSubscribers already compute
                                    // (decodeMethod, errorResponseForMethod,
                                    //  deadLetterForMethod, Implementations,
                                    //  MiddlewareHandlers, Requirements, ...)
    fn reflect.Value,              // the type-erased handler, already
                                    // shape-validated + wrapped by
                                    // wrapHandlerGeneralReflect
    opts SubscribeOptions,
) func(*wireMessage)               // returns the per-message handler closure
```

`transport.Subscribe` becomes: recover `fields` via `recoverHandle`,
validate/wrap `fn` (unchanged, already shared logic), call
`subscribeDispatchCore(...)`, pass the returned closure to the broker
client directly. `ServeSubscribers` becomes: for each registered entry,
build the SAME `erasedChannelFields` shape via
`extractErasedSubscriberHandle` (already does this — just needs its
output struct's shape aligned with what `subscribeDispatchCore` wants),
call the same core, register the closure per filter.

**Correction found during Phase 3 implementation (see the roadmap's
"Phase 3 results" below): this is only true for the `dispatchFailure`
triplet, not the full per-message dispatch body.** Implementing this
design for real found `transport.Subscribe` and `ServeSubscribers`'
per-message bodies have genuinely diverged, not merely duplicated —
topic-template-var matching and `PropertyMergeField` merge-into-value
support exist only in `transport.Subscribe`; `stats.Disposition` tracking
exists only in `ServeSubscribers`' path. Only the `dispatchFailure`
triplet was confirmed byte-for-byte identical and was safely extracted
into a shared `subscribeDispatchFailure` function. The rest of this
design's "one shared core" remains the right target, but requires an
explicit decision on reconciling those three divergences first — it is
not a pure mechanical unification as originally scoped here.

The generic Side-A path (`makeSubscribeMessageHandler`) **cannot** be
folded into this same Go function — Go has no reflective generic
instantiation, so a function taking `reflect.Value` cannot also be called
with a concrete `T` without boxing it through `any` first, which is
exactly the cost the generic path exists to avoid paying on the hot
decode path. **Resolved count: 3 → 2 implementations per adapter** (one
generic, one shared reflect-core), not 3 → 1. This matches the REST
family's nethttp precedent exactly: `buildRouteHandler` (reflect) and
`handlerFunc` (generic) stayed two functions there too, unified only via
a shared construction-time validation core — the same shape of partial
convergence applies here.

**Does the same answer apply to Publish's 2-way duplication?** Yes,
identically — mqtt5's `publish[T]`/`publishHandle[T]` (adapter.go:943,
1218, generic) vs. `(*transport).Publish` (transport.go:174, reflection)
is the SAME two-sided boundary with only one entry point on the reflect
side (no `ServePublishers`-equivalent registry-walk entry point exists for
Publish in any of the three adapters — Publish is always caller-driven,
never server-driven). So Publish's reflect-side duplication is already
effectively 1 implementation per adapter structurally; the fix there is
narrower — just ensure the reflect `Publish` path's requirement-coverage
and `ApplyCapabilities`/Observer/ErrorPattern call sequence is a single
function called from the one reflect entry point, not inlined ad hoc
(zeromq's audit already found this path's coverage check missing — see
point 4 below).

##### 2. Across mqtt vs. mqtt5: do NOT share a cross-package dispatch-core

**Definitive answer: no.** This is the opposite conclusion from the REST
family's nethttp/chi case, and for a reason specific to the evidence, not
a generic "different libraries, so no" hand-wave:

- nethttp/chi's shared-core precondition was that **both packages operate
  on the exact same wire type** (`*http.Request`/`http.ResponseWriter`),
  confirmed by `httpCarrier` and `applyGeneralMiddleware` being
  byte-for-byte identical today. The ONE real difference (path-param
  extraction) was a clean single-function injection seam
  (`func(*http.Request, []string) map[string]string`).
- mqtt vs. mqtt5 fail this precondition at the root: mqtt's reflect/generic
  dispatch bodies are built against `paho.mqtt.golang`'s `MQTT.Client`/
  `MQTT.Message` types (no `Properties`/User Properties concept at all —
  confirmed via grep, `mqtt.Capability` has no `UserPropertyParam`
  counterpart), while mqtt5's are built against `paho.golang/paho`'s
  `*paho.Publish`/`Properties` (MQTT5-only fields: `ContentType`,
  `UserProperties`, `CorrelationData`). This is not one cosmetic
  difference to inject around — mqtt5's dispatch core touches
  `msg.Properties.ContentType`/`msg.Properties.User` at MULTIPLE points
  (content-type format auto-selection, property-merge-field decode, User
  Property param validation) that have **no equivalent field to inject at
  all** in mqtt v3's wire message, not merely a different accessor for the
  same concept. A shared core would need either (a) a lowest-common-
  denominator interface mqtt v3 could never fully implement (dead code on
  the v3 side, or runtime panics/no-ops standing in for features that
  structurally cannot exist there), or (b) enough injected hook functions
  that the "shared core" becomes a thin shell calling back into
  per-adapter logic anyway — defeating the consolidation's own purpose of
  having ONE place a fix lands automatically for both.
- The REST family's injection seam was ONE function
  (`pathValues func(*http.Request, []string) map[string]string`) covering
  ONE difference. The mqtt/mqtt5 equivalent would need injection seams for
  message-property extraction, content-type resolution, user-property
  decode, AND the underlying client/publish call signatures themselves
  (`MQTT.Client.Publish` vs. `paho.golang`'s async `Publish`) — this is
  not "a few more parameters," it is most of the dispatch body becoming
  injected callbacks, which is indistinguishable in practice from keeping
  two separate implementations but adding an extra layer of indirection
  with no payoff.

**Recommendation: keep mqtt and mqtt5 as separate codebases, each with its
own internal `subscribeDispatchCore`/reflect-core from point 1, following
the SAME shape/convention but NOT the same package or shared Go function.**
This is consistent with the existing, deliberate `scopesmerge` precedent:
`adapters/internal/scopesmerge` is explicitly scoped ONLY to the narrow
concern that genuinely IS protocol-agnostic (grants-merging over
`map[string][]string`/`[]any` — no MQTT/MQTT5/ZeroMQ-specific type
anywhere in its signature), while `httpsecurity` (REST's equivalent) was
deliberately NOT reused for events/reqreply specifically because of this
same kind of boundary mismatch. The right level of sharing across mqtt/
mqtt5/zeromq is: shared API-layer helpers that are ALREADY
protocol-agnostic by construction (`events.VerifyCapabilityCoverage`,
`events.ApplyCapabilities`, `scopesmerge.*`, `reqreply.CheckCoverage`) —
already true today — plus a **shared naming/shape CONVENTION** for each
adapter's own internal dispatch-core (same function name pattern, same
field-bundle shape, same call order), enforced by doc-comment cross-
references (as the code already does extensively, e.g. "mirrors
adapters/mqtt5's identical...") and by Phase 5 conformance tests run
per-adapter against the same fixtures — NOT by a shared Go package. If a
genuinely protocol-agnostic piece of logic is found during Phase 4
implementation (e.g. the `dispatchFailure` fallback-precedence shape is
identical prose across all three adapters even though its field types
differ), extract ONLY that narrow piece into `adapters/internal`,
following `scopesmerge`'s precedent of a tightly-scoped single-concern
package — do not pre-design a wider shared package speculatively.

##### 3. zeromq reqreply's 4-socket-variant duplication: separate the setup core from the recv-loop

Reading `serverTransport.Serve` (reqreply_transport.go:508) against
`routerServerTransport.Serve` (reqreply_transport.go:1555) confirms a
clean internal seam already exists, just not factored out:

- **Setup phase (CAN consolidate, no protocol dependency)**: recovering
  the route handle via reflection, validating the handler's `Fn` shape
  against `wantFnType`, `validateServerImplementationShapes`,
  `reqreply.CheckCoverage` (security/middleware-declaration coverage),
  `reqreply.VerifyCapabilityCoverage` (capability-requirement coverage),
  `applyCapabilities` (socket-level HWM/Conflate), and
  `applyGeneralServerMiddleware` (already shared as a free function,
  confirmed used by BOTH plain and ROUTER Serve at lines 615 and 1635) —
  every one of these runs ONCE per `Serve` call, before the recv loop
  starts, and is **already textually identical** between plain and
  ROUTER-Serve (both call the same functions in the same order on the
  same field names). Zero genuine protocol divergence exists in this
  phase — HWM/Conflate/coverage/middleware-wrapping do not know or care
  whether the underlying socket is REQ/REP or ROUTER.
- **Recv-loop phase (MUST stay separate)**: how a request frame is
  received, how the reply is correlated back to the right peer, and how
  the reply is sent — REQ/REP has no addressing concept (the socket
  itself enforces strict one-reply-per-request ordering); ROUTER
  prepends/strips an identity frame per message and must echo it back on
  reply, with no ordering guarantee the socket enforces for you. This is
  the genuinely protocol-necessitated part and correctly stays unmerged.
  The client side mirrors this: `clientTransport.call` (plain REQ) has no
  correlation data to manage beyond strict request/reply ordering;
  `dealerClientTransport.call` (DEALER) manages its own identity/sequence
  correlation since DEALER sockets are inherently async multiplexed.

**Concrete design**: extract an unexported
`reqreplyServerSetupCore(routeAny, fnAny any) (*serverDispatchFields, error)`
function — covering recovery, shape validation, `CheckCoverage`,
`VerifyCapabilityCoverage`, `applyCapabilities`, and
`applyGeneralServerMiddleware` — called identically by both
`serverTransport.Serve` and `routerServerTransport.Serve` at the top of
each. Each `Serve` then runs its own socket-specific recv loop using
`serverDispatchFields.dispatchFn` (the already-middleware-wrapped
handler) and `serverDispatchFields.{decodeWithFormatsMethod,
encodeWithFormatsMethod, observeErrorResponseForMethod,
deadLetterForMethod}` for the per-message decode/encode/error path — this
part (decode received bytes → call `dispatchFn` → encode/send reply) is
ALSO textually near-identical between the two Serve variants (same
decode/encode/ErrorPattern/DeadLetter sequence), so a second, smaller
shared helper — `reqreplyServerDispatchOne(fields, reqBytes) (respBytes
[]byte, err error)` — can cover "decode, call handler, encode" while
leaving ONLY the socket recv/identity-framing/send mechanics (genuinely
~20-30 lines per variant) unconsolidated. The same split applies
client-side: a shared `reqreplyClientCallSetup`/`reqreplyClientEncodeDecode`
helper covering route recovery, shape validation, `VerifyCapabilityCoverage`,
format resolution, and request-encode/response-decode, with ONLY the
socket send/correlate/receive mechanics (empty-delimiter+identity framing
for plain, DEALER's async correlation bookkeeping for DEALER) left
per-variant. This does not reduce "4 implementations" to "1" — it reduces
each 4-way duplication to "1 shared setup/encode-decode core + 4 genuinely
distinct, now much smaller, socket-mechanics bodies," which is the
honest, protocol-correct target (not a universal merge).

##### 4. Capability-coverage checking: one gate per logical dispatch setup, not one global location

**Confirm and refine, not simply confirm**: Design goal 12's "requirement-
coverage becomes one Attach-time check" is correct in spirit but needs
one precision fix once the actual `events.Client.Attach`/`Register`
lifecycle is read closely. `events.Client.Attach(t)` calls
`t.BindClient(c)` immediately — but `Subscriber.Register(client)` (the
call that populates `client.SubscriberEntries()`, which `ServeSubscribers`
walks) has **no ordering constraint relative to `Attach`** — an
application may register channels before OR after attaching a transport
(confirmed: `Register` only touches `client`'s internal registry, never
touches `client.transport`). This means a literal single check inside
`BindClient` itself would silently skip any `Subscriber` registered
AFTER `Attach` — this would be a NEW regression, not a fix, if
implemented naively.

**Resolved design**: "one Attach-time check" means **exactly one
syntactic call site per logical dispatch-setup moment, never per-message,
and never duplicated across the (now 2, per point 1) entry points that
share that moment** — not literally one global call inside `BindClient`.
Concretely, per adapter:

- **Generic/reflect call-time Subscribe/Publish** (`subscribeWithHandle`/
  `transport.Subscribe`/`transport.Publish`): the check runs once, at the
  top of the (now-shared, per point 1) dispatch-core function, before any
  message is received/sent — this already matches `subscribeWithHandle`'s
  existing timing exactly, and is what `transport.Subscribe`'s
  already-shipped fix for mqtt5 does today (transport.go:514-519) for
  Subscribe; the same must be added for `transport.Publish` wherever it
  is currently missing (zeromq's audit confirms it is).
- **`ServeSubscribers`' registry walk**: the check runs once PER ENTRY,
  in the `ServeSubscribers` loop, before that entry's goroutine starts
  (mqtt5's `caller.go:157` already does exactly this — `opts.Capabilities`
  checked once per entry inside the `for _, entry := range entries` loop,
  not inside `serveOneEntry`/`makeErasedSubscribeMessageHandler`'s
  per-message body). This is the correct existing shape; Phase 4's job is
  making sure mqtt v3 and zeromq's `ServeSubscribers` match it exactly
  (confirm no adapter has regressed this into the per-message body).
- **reqreply Serve**: the check runs once inside the (now-shared, per
  point 3) `reqreplyServerSetupCore`, before the recv loop starts — this
  already matches mqtt5's existing `reqreply_transport.go:462` timing and
  zeromq's existing `serverTransport.Serve` timing; the fix is making
  ROUTER-Serve call the SAME shared core function rather than its own
  copy, not changing WHEN the check runs.

**Mechanically compatible with points 1-3's dispatch-core design? Yes, by
construction**: each dispatch-core function's signature already separates
"setup, run once" (requirement-coverage check belongs here) from "per-
message/per-call body" (capability APPLICATION belongs here, per Design
goal 12's already-resolved split) — point 1's `subscribeDispatchCore` and
point 3's `reqreplyServerSetupCore` are EXACTLY the functions where the
once-per-setup requirement-coverage check now has a single home, and the
returned per-message closure is EXACTLY where capability application
(QoS applied via `events.ApplyCapabilities`, the per-call
`PublishOptions[T].Capabilities` override) continues to run per-message
as Design goal 12 already requires. The dispatch-core's job after Phase 4
becomes precisely "run the message through a handler the setup-time check
already validated is coverage-safe" — no per-message re-check of
requirement coverage is added or needed anywhere in this design.

##### 5. Recommended Phase 4 implementation order, per adapter (3 separate workstreams)

These are **three independent workstreams** (per point 2's answer — no
shared package links them), each independently schedulable and
independently verifiable; the SAME step shape is recommended for all
three because the duplication shape is structurally identical, not
because the code is shared.

**Workstream A — `adapters/mqtt5`** (do this one first — it has the
clearest, most thoroughly-documented existing gap and the smallest
reqreply side, making it the best place to prove the pattern before
repeating it):

1. Define `erasedChannelFields` (new unexported struct) capturing exactly
   the field set `transport.Subscribe`'s `recoverHandle`+`elem.FieldByName`
   calls and `caller.go`'s `extractErasedSubscriberHandle` already both
   produce (decode method, error/dead-letter methods, Implementations,
   MiddlewareHandlers, Requirements, security schemes, descriptor) — a
   pure struct-shape unification, zero behavior change yet.
2. Extract `subscribeDispatchCore(ctx, client, obs, fields, fn, opts)
   func(*paho.Publish)` from the currently-duplicated body shared between
   `transport.Subscribe`'s inline handler and
   `makeErasedSubscribeMessageHandler` — verify byte-identical behavior
   via mqtt5's existing `transport_test.go`+`caller_test.go` suites
   (should pass unchanged; this step is pure extraction).
3. Repoint `transport.Subscribe` to build `erasedChannelFields` via
   `recoverHandle`, call `subscribeDispatchCore`, verify tests.
4. Repoint `ServeSubscribers`/`extractErasedSubscriberHandle` to build the
   same `erasedChannelFields` shape, call the same core, verify tests;
   delete the now-dead `makeErasedSubscribeMessageHandler`.
5. Add the requirement-coverage check to `subscribeDispatchCore`'s setup
   section (closing the confirmed Subscribe-reflection-path gap by
   construction, not as a separate patch) — add a regression test
   exercising `transport.Subscribe` with an unsatisfiable
   `CapabilityRequirement`, mirroring the existing per-path capability
   tests.
6. Repeat steps 1-5 for Publish (`transport.Publish` vs.
   `publish[T]`/`publishHandle[T]`) — smaller, since there is no
   `ServePublishers`-equivalent third entry point; just unify the
   reflect-side body into its own dispatch-core function and confirm the
   requirement-coverage check is present (audit found this may already be
   missing — treat as a confirmed fix, not speculative).
7. Extract `reqreplyServerSetupCore`/`reqreplyServerDispatchOne` from
   `reqreply_transport.go`'s `serverTransport.Serve` (there is only ONE
   server-socket variant in mqtt5, so this step is pure refactor-for-
   future-reuse-by-convention, not yet a merge of two things — do it here
   first since mqtt5's reqreply side is simplest, establishing the target
   shape zeromq's workstream C will follow).
8. Add conformance tests (Phase 5 scope, but seed them here) exercising
   `subscribeDispatchCore`/`reqreplyServerSetupCore` directly, independent
   of which entry point calls them, so future regressions are caught at
   the core, not only through one entry point's own tests.

**Workstream B — `adapters/mqtt` (v3)** (second — apply the PROVEN shape
from Workstream A, adapted to v3's own client library, with no shared
code between the two):

1-6. Identical step shape to Workstream A steps 1-6, applied to mqtt v3's
own `adapter.go`/`caller.go`/`transport.go` (its own `subscribeHandle`/
`subscribeHandler`, `transport.go`'s reflection Subscribe, and
`ServeSubscribers`). Since mqtt v3 already has 6 confirmed capability-
coverage call sites (vs. mqtt5's 3), expect MORE sites to collapse into
the one dispatch-core's setup section here, not fewer — this is a bigger
cleanup than mqtt5's despite following the same recipe. No reqreply
Serve/Call exists in mqtt v3 (events-only), so skip the equivalent of
step 7.
7. Add conformance tests mirroring Workstream A's step 8, run against
   mqtt v3's own dispatch-core (NOT the same test code as mqtt5's — a
   new, adapter-local conformance test file, following the same fixture
   shape by convention).

**Workstream C — `adapters/zeromq`** (third — the largest workstream,
since it must cover BOTH events (3-way Subscribe, 2-way Publish, same
shape as A/B) AND reqreply's 4-socket-variant split from point 3):

1-6. Identical step shape to Workstream A steps 1-6, applied to zeromq's
`subscribeWithHandle`/`transport.Subscribe`/`ServeSubscribers`
(`serve_subscribers.go`'s `buildSubscriberRoute`/`dispatchToRoute`/
`processMessage` play the role mqtt5's `extractErasedSubscriberHandle`/
`makeErasedSubscribeMessageHandler` do). **Correction (Phase 3):** an
earlier pass of this audit claimed zeromq's reflection Subscribe/Publish
paths were missing their coverage check; re-verified directly against
current code during Phase 3 implementation work, this is **wrong** —
`transport.go:421` (Subscribe) and `:281` (Publish) both already call
`events.VerifyCapabilityCoverage`, per a commit (`47f4e198`) that predates
this roadmap's own Phase 1 audit. Reframe this workstream's motivation
accordingly: it closes a DRY/future-regression-prevention concern (one
shared implementation instead of 3 separately-correct ones), not an
active gap.
7. Extract `reqreplyServerSetupCore`/`reqreplyServerDispatchOne` per point
   3's design, shared by `serverTransport.Serve` AND
   `routerServerTransport.Serve` — verify both `reqreply_transport_test.go`
   and `forward_reqreply_test.go` (ROUTER-specific) pass unchanged after
   repointing each `Serve` to call the shared core, before touching the
   recv-loop bodies.
8. Extract the client-side equivalent
   (`reqreplyClientCallSetup`/`reqreplyClientEncodeDecode`) shared by
   `clientTransport.call` AND `dealerClientTransport.call`, leaving only
   each socket's send/correlate/receive mechanics distinct — verify
   `callraw_test.go`/`capability_reqreply_test.go` for both variants.
9. Add conformance tests mirroring Workstream A's step 8/B's step 7, for
   BOTH the events dispatch-core and the reqreply setup/dispatch-one
   cores, covering all 4 reqreply socket-variant entry points against the
   same shared-core fixtures.

##### 6. Risks and behavioral-compatibility concerns

- **(a) Attach-time consolidation changing per-call re-check behavior**:
  LOW risk, if point 4's refined design (checks live in each
  dispatch-core's setup section, not literally inside `BindClient`) is
  followed — this preserves every adapter's EXISTING timing (per-entry
  for `ServeSubscribers`, per-Serve-call for reqreply, per-call for the
  generic/reflect Subscribe/Publish paths) exactly. **Corrected (Phase
  3): there is no missing check left to add** — mqtt5's and zeromq's
  reflection Subscribe/Publish paths already have their coverage check
  (see the corrections above); the only behavior change from this
  consolidation is collapsing duplicate, individually-correct
  implementations into one, not adding a previously-absent check. Several
  of mqtt v3's 6 call sites still genuinely consolidate into fewer (that
  finding was not re-examined and stands as audited).
  **Confirmed NOT at risk**: mqtt5's `PublishOptions[T].Capabilities`
  per-call override (adapter.go:152) is capability APPLICATION, not
  requirement COVERAGE — Design goal 12 already separates these, and this
  design's dispatch-cores apply capabilities per-message inside the
  returned closure exactly as today, never gating that on a one-time
  setup check. **Genuine new risk to flag explicitly**: if a future
  `Subscriber`/`Publisher`/`Route` is registered or re-subscribed to an
  ALREADY-RUNNING `ServeSubscribers` loop (not currently supported by any
  of the three adapters — `ServeSubscribers` walks a snapshot of entries
  once at call time, per `errgroup.WithContext` usage, with no live
  re-walk), the consolidated per-entry check continues to only run at
  that snapshot time — this is UNCHANGED behavior, not a regression, but
  should be called out in each dispatch-core's doc comment so a future
  contributor doesn't assume live re-registration is coverage-checked.
- **(b) Subscribe entry points already found to behave subtly
  differently**: **Corrected during Phase 3 — the two cases below were
  originally reported as currently-active divergences; re-verification
  against the actual code found both had already been fixed before this
  audit ran.** Recorded here for traceability, not as open risks:
  1. **zeromq's historical coverage-check gap** — `transport.go`'s
     reflection Subscribe/Publish paths DID at one point lack the check
     the generic/`ServeSubscribers` paths had; commit `47f4e198` (dated
     before this roadmap's Phase 1 audit) already fixed both. All 3 entry
     points behave identically today. Consolidation (steps 1-5) still
     reduces the risk of this exact class of regression recurring, but
     there is no divergent behavior to canonicalize — all 3 already
     agree.
  2. **mqtt5's reflection `transport.Subscribe` path** — same situation,
     same commit, same correction. No open divergence.
  3. **Property/merge-field support differs by necessity, not bug**:
     zeromq's reqreply side has NO property-merge-field/topic-template
     concept (confirmed via code comment: "zeromq's REQ/REP wire format
     carries no topic frame at all"), while mqtt5's does. The shared
     `reqreplyServerSetupCore`/dispatch-one design (point 3) must NOT
     attempt to unify this away — zeromq's core correctly passes an
     always-empty property-value map (already how it works today) rather
     than gaining a new feature it has no wire representation for;
     consolidation must preserve this intentional asymmetry, not sand it
     flat.

**Summary and biggest risk**: Consolidate events Subscribe/Publish within
each of mqtt, mqtt5, and zeromq onto one shared, adapter-local,
`reflect`-based dispatch-core per capability (3→2 implementations: one
generic, one reflect-core), keep mqtt and mqtt5 as permanently separate,
non-shared codebases despite their structural mirroring (their underlying
client libraries diverge too deeply at the wire-property level for a
shared package to pay off), split zeromq's reqreply 4-socket-variant
duplication into a shared setup/encode-decode core plus genuinely distinct,
now much smaller, per-socket recv/correlate/send bodies, and move
requirement-coverage checking to exactly one call site per dispatch-core's
setup section (not literally into `BindClient`, since `Subscriber.Register`
has no enforced ordering relative to `Attach`) while leaving per-call
capability application untouched. **Updated during Phase 3**: the
original "biggest risk" here (silently canonicalizing the wrong behavior
where entry points already diverge, citing zeromq's and mqtt5's coverage
checks as currently-missing) no longer applies — re-verification against
the actual code found both had already been fixed (commit `47f4e198`,
predating this roadmap's Phase 1 audit) before Phase 4 work was even
scoped. The consolidation's value is now accurately framed as
DRY/future-regression-prevention (collapsing N individually-correct
implementations into one, so this exact class of gap — proven to have
happened at least twice already — cannot recur silently), not closing a
presently-active bug. The general principle stands for any future finding
of this kind: re-verify an audit's "confirmed gap" claims directly against
current code before treating them as license to change behavior, since an
audit can itself become stale between when it runs and when its findings
are acted on.

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

**Status: 7 of 7 cases done (1, 2, 3, 4, 5, 6, 7); case 6's deferred
behavior decisions subsequently resolved and implemented in a follow-up
round.** See "Phase 3 results" below for outcomes, including one
significant finding that revises Phase 2's design (case 6), one
correction to Phase 1's own findings (surfaced while implementing case
6), and case 6's own follow-up round (scope correction from 2 to 3
dispatch paths, both behavior decisions resolved, behavioral parity
implemented and verified).

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

#### Phase 3 results

All 7 cases now done — cases 1, 2, 3, 6, 7 in the initial round; cases 4
(cross-transport reqreply) and 5 (middleware chain consistency across
adapters) in a follow-up round, once no existing test in the codebase was
confirmed to already cover either.

- **Case 1 (REST route) — confirmed, no new code.** `go test ./api/rest/...
  ./adapters/nethttp/...` passes against `main` unchanged, confirming the
  roadmap's own note that this case already works today.
- **Case 2 (basic event channel, MQTT3+MQTT5) — confirmed, no new code.**
  `go test ./api/events/... ./adapters/mqtt/... ./adapters/mqtt5/...`
  passes unchanged.
- **Case 3, QoS/HWM half — confirmed, no new code.** Covered by the same
  test run above (existing `capability_test.go` suites exercise this
  already).
- **Case 3, MQTT User Properties half — new ground, IMPLEMENTED and
  proven.** Closed the named gap from Round 2 decision 4: added
  `events.RequireUserProperties()` (`api/events/capability_require.go`,
  mirroring `RequireRetained`/`RequireConflate`'s presence-only shape) and
  a new sealed `adapters/mqtt5.UserProperties` capability (`capability.go`,
  mirroring `Retained`'s shape; `Apply` is a no-op returning
  `(true, nil)` — it exists purely for declare-time requirement-coverage
  matching via `CapabilityName()`, deliberately NOT replacing
  `UserPropertyParam`'s separate per-message value-validation concern, per
  Design goal 12's capability-application/requirement-coverage split).
  `adapters/mqtt` (v3) gets no equivalent type — it has no user-properties
  concept at all. Three new tests prove the ORIGINAL Doc A scenario end to
  end:
  - `TestPublish_RequireUserProperties_SuppliedCapability_Passes`
    (`adapters/mqtt5/adapter_test.go`) — MQTT 5 **accepted**.
  - `TestPublish_RequireUserProperties_NoCapabilitySupplied_FailsCoverage`
    (`adapters/mqtt5/adapter_test.go`) — regression guard, mirrors the
    existing QoS/Retained pattern.
  - `TestPublish_RequireUserProperties_FailsCoverage`
    (`adapters/mqtt/adapter_test.go`) — MQTT 3 **rejected** with an
    actionable `*events.CapabilityCoverageError{Missing: ["UserProperties"]}`
    — not a Go compile error, closing the gap exactly as described.
  All three pass; full `api/events`/`adapters/mqtt`/`adapters/mqtt5`/
  `adapters/zeromq` suites re-verified with no regressions.
- **Case 7 (ports pattern) — confirmed, no new code, a deliberate negative
  result.** `go test ./ports/... ./adapters/file/... ./adapters/redis/...`
  passes unchanged. This is the expected, correct outcome per Phase 1's
  finding that `file`/`redis` have no duplication problem and no
  capability mechanism at all: the binding/dispatch-core model does not
  force changes onto adapters that don't need them. Proving that
  explicitly (rather than assuming it) was the point of including this
  case.
- **Case 5 (middleware chain consistency across REST adapters) — new
  ground, IMPLEMENTED.** Confirmed first that no existing test actually
  binds one identical route+middleware declaration to both
  `adapters/nethttp` and `adapters/chi` and asserts matching behavior —
  prior cross-references between the two packages' test suites are
  incidental doc comments only (e.g. "mirrors adapters/nethttp's
  identically-named test"), never a shared assertion. Added
  `TestMiddlewareChain_ConsistentAcrossNethttpAndChi`
  (`adapters/chi/cross_adapter_consistency_test.go`, `package chi` — not
  an external `_test` package — specifically so it can reach chi's own
  unexported `serveOne` directly while importing `adapters/nethttp`
  normally, since `nethttp.ServeOne` is exported but `chi.serveOne` is
  not, an asymmetry Phase 1 already confirmed in the public surface
  between the two packages). The test declares **one** `rest.Route` with
  **one** general-purpose `.HandleMW(nil, apiKeyFn)` middleware (mirroring
  the existing `TestHandler_RequireAPIKey_RunsWithoutRouteSecurity`
  pattern both packages already carry independently), serves it via both
  `nethttp.ServeOne` and chi's `serveOne` wrapped in `httptest.NewServer`,
  and fires three cases (missing/wrong/valid API key) at both, asserting
  identical accept/reject status codes, identical success/failure
  response shape, and that the Fn runs exactly once per request on each
  adapter (6 calls total). **Result: passes — no observable behavioral
  drift found yet** between chi's confirmed line-for-line duplication of
  nethttp's dispatch logic and nethttp itself, for this middleware shape.
  This is a reassuring negative result, not evidence the duplication risk
  is unfounded: it proves today's two implementations haven't yet
  diverged, which is exactly the kind of regression this roadmap's
  dispatch-core consolidation (Phase 4, REST family) is meant to make
  structurally impossible to reintroduce, rather than merely
  coincidentally absent.
- **Case 4 (reqreply contract on two transports) — new ground,
  IMPLEMENTED at reduced rigor (explicitly stated, not hidden).** No live
  MQTT5/ZeroMQ broker is available in this sandboxed environment, so a
  single shared, literally-cross-package live test (the ideal proof) isn't
  achievable here. Instead, added one mirrored test per adapter, each
  named identically (`TestCrossTransportContract_ComputeRoundTrip`) and
  explicitly cross-referencing the other in its doc comment, both
  declaring the exact same logical contract — the same `computeReq`/
  `computeResp` types, the same `"/compute"` `reqreply.RouteHandle`, the
  same `X+Y=Sum` handler semantics already shared verbatim by both
  packages' pre-existing test fixtures (`newRouteHandle()`,
  `computeReqCodec`/`computeRespCodec`) — against each package's own
  pre-existing mock harness:
  - `adapters/mqtt5/cross_transport_contract_test.go` — a genuine,
    fully-wired, in-process Serve+Call round trip via mqtt5's own
    `mockClient`/`mockRouter` harness (the same harness
    `TestCall_ValidRoundTrip` already uses) — the higher-rigor half, since
    mqtt5's mock harness already supports true end-to-end dispatch.
  - `adapters/zeromq/cross_transport_contract_test.go` — zeromq's own
    `mockSocket` harness is one-way/scripted (canned frames in, recorded
    frames out), matching how zeromq's own pre-existing suite already
    tests Serve and Call independently, never end-to-end in one test. The
    new test therefore proves both halves of the same contract
    separately: Serve's encode path (the handler computes the correct Sum
    and it's reflected verbatim, via `format.JSON(computeRespCodec)`, in
    the reply frame) and Call's decode path (a reply frame carrying the
    same Sum decodes into the correct `computeResp`) — together
    equivalent evidence to mqtt5's one wired round trip, at a rigor level
    consistent with this package's own existing test conventions rather
    than a bespoke, one-off harness invented only for this test.
  Both tests pass; this rigor asymmetry (mqtt5 genuinely end-to-end,
  zeromq split encode+decode) is recorded here plainly rather than
  presented as equivalent proof — a known, accepted limitation of this
  sandboxed environment, not a design choice.
- **Case 6 (events Subscribe dispatch-core) — behavior decisions
  RESOLVED, behavioral parity IMPLEMENTED across all three dispatch
  paths; literal single-function unification deliberately deferred.**
  This case went through two rounds: the initial PoC round (below)
  revealed Phase 2's Workstream A design, as written, was not a safe
  mechanical refactor; a follow-up round (once the user confirmed Phase 3
  was otherwise complete) made the three deferred behavior decisions and
  implemented them.

  **Initial finding (first round)**: reading `transport.go`'s
  `(*transport).Subscribe` and `caller.go`'s
  `makeErasedSubscribeMessageHandler` side by side to implement the full
  `subscribeDispatchCore` merge found they have genuinely diverged beyond
  duplication, not merely duplicated — topic-var/property-merge support,
  Disposition tracking, and a per-message re-wrap inefficiency differed
  between the two. The one piece confirmed byte-for-byte identical — the
  `dispatchFailure` (ErrorChannel→DeadLetter→OnError) fallback triplet —
  was extracted into a shared `subscribeDispatchFailure` function
  (`adapters/mqtt5/transport_dispatch.go`); the rest was deferred pending
  an explicit decision.

  **Scope correction (second round, before any decision was made)**:
  re-reading both paths closely enough to actually implement a merge
  found mqtt5 has **three** independent Subscribe dispatch
  implementations, not two — a third, generic
  `subscribeWithHandle[T]`/`makeSubscribeMessageHandler[T]`
  (`adapters/mqtt5/adapter.go`, backing the `subscribe[T]` escape hatch)
  had been missed by the first round's investigation. Its topic-var
  mechanism also turned out to be genuinely different from
  `transport.Subscribe`'s (`handle.MergeFields()`/`TopicVarsFromMessage`/
  `codex.DecodeVars`, vs. `transport.go`'s `matchTopicTemplate`/
  `DecodeMergedWithFormats`) — two mechanisms accomplishing the same
  thing, not one missing it. The actual per-path feature matrix, once
  fully re-verified:

  | Path | Topic-var support | Disposition tracking | Wrap timing |
  |---|---|---|---|
  | `subscribeWithHandle`/`makeSubscribeMessageHandler[T]` (generic, `adapter.go`) | yes (`MergeFields`/`TopicVarsFromMessage`/`DecodeVars` mechanism) | **missing** | once (correct) |
  | `transport.Subscribe` (reflect, `ports.Pattern` binding) | yes (`matchTopicTemplate`/`DecodeMergedWithFormats` mechanism) | **missing** | once (correct) |
  | `ServeSubscribers`/`makeErasedSubscribeMessageHandler` (reflect, `Client.Attach`) | **missing entirely** | yes | **per-message (bug)** |

  **Decisions made** (via `ask_user`, both chosen "add parity"): (1)
  topic-var/property-merge support on the `ServeSubscribers` path should
  be added to reach parity with the other two; (2) Disposition tracking
  on `transport.Subscribe` (and, once the 3-way scope correction above
  was found, on the generic path too) should be added to reach parity
  with `ServeSubscribers`. The per-message re-wrap divergence needed no
  decision — a confirmed pure performance fix, applied regardless.

  **What was implemented**: rather than forcing all three call sites
  through one literal shared function (assessed as high-risk given the
  three paths' genuinely different typing — one generic, two
  reflect-based — and their security/error-handling-critical content),
  each path was brought to full behavioral parity directly, in place:
  - `transport.go`'s `Subscribe` gained `stats.EnsureDispositionBox`/
    `ResolveDisposition`/`DispositionObserver` wiring, mirroring
    `ServeSubscribers`' existing plumbing exactly.
  - `adapter.go`'s generic `makeSubscribeMessageHandler[T]` gained the
    identical Disposition wiring.
  - `caller.go`'s `erasedSubscriberHandle` was extended with
    `decodeMergedMethod`/`mergePropertyVarsMethod`/
    `propertyMergeFieldsLen` (the same reflect-bound methods
    `transport.go` already used), and
    `makeErasedSubscribeMessageHandler` was rewritten to derive topic
    vars via `matchTopicTemplate` and merge them via
    `DecodeMergedWithFormats`/`MergePropertyVars` — exactly mirroring
    `transport.go`'s mechanism (the two reflect-based paths now share the
    SAME topic-var mechanism; the generic path keeps its own, distinct,
    pre-existing one — reconciling the two different reflect-path
    mechanisms into one was in scope and done; reconciling the generic
    path's separate mechanism into the SAME one was not part of the
    decided scope and was not attempted). The per-message
    `wrapHandlerGeneralReflect` call was hoisted out of the hot path to
    run once, matching `transport.go`.
  - A newly-found, explicitly out-of-scope asymmetry was flagged, not
    fixed: `SubscribeOptions` (used by `ServeSubscribers`) has no
    `Formats` field at all, unlike the other two paths' call-time format
    override mechanisms — left exactly as-is (no regression, no silently
    added feature), noted below as a new open item.

  **Verification**: 3 new regression tests added
  (`adapters/mqtt5/subscribe_dispatch_parity_test.go`) proving each
  closed gap — topic-var merge now works via `ServeSubscribers`;
  Disposition tracking now works via `transport.Subscribe`; Disposition
  tracking now works via the generic `subscribeWithHandle` escape hatch.
  Full `adapters/mqtt5` suite (pre-existing + new) passes unchanged;
  full-repo `go build ./...`/`go test ./...` (zero failures), `go vet`,
  `gofmt` all clean.

  **What remains open, by design**: a literal single shared
  `subscribeDispatchCore` function used by all three call sites (true
  code-level deduplication, not just behavioral parity) is a separate,
  still-open Phase 4 opportunity — this round closed the BEHAVIOR gaps
  Phase 3 found, deliberately without forcing the three implementations'
  genuinely different typing (one generic, two reflect) into one body in
  the same pass, consistent with this roadmap's "no false portability"
  principle. The newly-found `SubscribeOptions.Formats` asymmetry (noted
  above) is also left open. Both are candidates for Phase 4's
  events/reqreply family work, not resolved here.

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

#### Phase 4 progress: REST family (8 of 8 steps — COMPLETE)

**Status: ALL 8 steps of the REST family's plan (above) DONE.** Began
with the REST family per user choice — fully specified by Phase 2,
untouched by any Phase 3 finding, lowest-risk starting point. Completed
across several rounds; see each step's own writeup below for what was
found and decided along the way.

- **Step 1 — new `adapters/internal/restdispatch` package**: added,
  mirroring the `httpsecurity`/`scopesmerge` convention. Holds
  `HTTPCarrier`/`HTTPTransport` (promoted from `adapters/nethttp`'s and
  `adapters/chi`'s previously separate, confirmed-byte-for-byte-identical
  definitions) and `ValidateRouteConstruction` (the four-check
  construction-time core — shape validation, security coverage,
  capability coverage, param-kind coverage — assembled verbatim from the
  confirmed-identical block previously duplicated in both packages' own
  `buildRouteHandler`, generalizing only the one genuine per-caller
  parameter, the adapter-name string used in error messages/
  `CheckParamKindCoverage`'s label).
- **Step 2 — `adapters/nethttp` repointed**: `capability.go`'s
  `httpCarrier`/`httpTransport` are now type aliases to
  `restdispatch.HTTPCarrier`/`restdispatch.HTTPTransport` (so every
  existing call site, including the deliberately-untouched
  `serve_sse.go`, keeps compiling unchanged); `serve.go`'s
  `buildRouteHandler` now calls `restdispatch.ValidateRouteConstruction("nethttp", handle)`
  in place of its own inline recovery+checks. Verified: nethttp's full
  test suite passes unchanged (pure extraction, zero behavior change).
- **Step 3 — `adapters/chi` repointed** the identical way; its duplicate
  construction block deleted. Verified: chi's full test suite passes
  unchanged, including the Phase 3 case 5 cross-adapter consistency test.
- **One side effect found and fixed during verification, not a design
  change**: `go vet` flags unkeyed struct literals for a type now defined
  in a *different* package (it didn't flag this when `httpCarrier` was
  package-local) — the ~14 existing `httpCarrier{r}` construction call
  sites across both packages (untouched by this consolidation otherwise)
  were updated to the keyed form `httpCarrier{R: r}` to keep `go vet`
  clean. Zero behavior change — purely a literal-syntax fix forced by the
  type's new cross-package location.
- **Verification (steps 1-3)**: full-repo `go build ./...`, `go test ./...`
  (zero failures), `go vet ./...` (clean, including the unkeyed-literal
  fix above), `gofmt` all clean.

**Steps 4-5 — per-request `DispatchRoute` body extraction, both packages
repointed.** Investigated thoroughly before coding, same rigor that
caught case 6's scope correction: programmatically diffed (package-name-
normalized) every helper the per-request dispatch body calls. Found:

- The full per-request closure body itself, plus 11 supporting helpers
  (`defaultErrorHandler`, `tryRespondErrorPattern`, `callErr`,
  `callErrStatusFor`, `isSecuritySatisfyingHandler`, `satisfiesPerHandler`,
  `credentialExtractorFor`, `negotiateRequestFormatReflect`,
  `negotiateFormatReflect`, `formatContentTypesReflect`,
  `responseHeaderValues`, `responseCookieValues`, `primaryStatusFor`,
  `asRedirectError`), confirmed byte-for-byte identical between
  `nethttp`/`chi` (only cosmetic comment differences). `PendingCookie` was
  already a shared `rest.PendingCookie` type alias in both packages.
- The one genuine per-package difference, confirmed exactly as Phase 2
  predicted: `pathValues` (`r.PathValue` vs. `gochi.URLParam`) — injected
  as a `DispatchRoute` parameter.
- A second, real difference the doc's simplified description didn't
  fully anticipate, found during this investigation: `contextKey`/
  `responseHeadersKey`/`responseCookiesKey` are PACKAGE-LOCAL sentinel
  types backing each package's own public `RequestFromContext`/
  `ResponseHeadersFromContext`/`WithResponseCookies` accessors — these
  cannot be promoted into `restdispatch` without breaking those public
  accessors (a correctness risk, not a style one). Confirmed the
  dispatch body itself never reads these keys back via `ctx.Value` (only
  writes them, for user-supplied handler code) — so `DispatchRoute`
  accepts an ALREADY-ctx-wrapped `context.Context` plus explicit
  `respHeaders`/`pendingCookies` parameters, never touching the
  per-package sentinel types itself.
- A third, similar finding: `statusResponseWriter` (and its
  `WriteErrorResponse` method, which calls each package's own exported
  `SetCookie`/`CookieOptions`-translation helper directly) also could not
  be promoted — its fields are unexported, so a type alias would make
  cross-package struct-literal construction at `handlerFunc`'s/
  `serve_sse.go`'s many existing call sites fail to compile (unexported
  fields from another package are invisible to literal construction).
  Resolved by having `DispatchRoute` accept a small `restdispatch.ResponseWriter`
  interface (`http.ResponseWriter` + `rest.ErrorResponseWriter`) instead
  of a concrete shared type — `statusResponseWriter` itself stays
  package-local and completely unchanged in both packages. The two
  direct cookie-write call sites inside `DispatchRoute`'s own body
  (outside `WriteErrorResponse`) use an injected `writeCookie` closure
  built by each package's thin wrapper over its own `SetCookie`/
  `cookieOptionsFrom`.
- **A beneficial side effect of this investigation**: since all 11
  helpers above are ALSO used by `handlerFunc`/`serve_sse.go` (not just
  `buildRouteHandler`), promoting them with the same type-alias/func-var-
  alias pattern already established for `httpCarrier` means
  `handlerFunc`/`serve_sse.go` now ALSO call the shared implementations —
  closing part of their own duplication too, as a side effect, without
  touching their own dispatch LOGIC or structure at all (steps 6-8,
  covering `handlerFunc`'s actual construction-time wiring and the
  `serve_sse.go` dispatch-body audit, remain separately scoped and not
  started).

Implemented: added `restdispatch.DispatchRoute` (the full per-request
pipeline) plus the 11 helpers above (with `callErr`/`callErrStatusFor`/
`satisfiesPerHandler`/`isSecuritySatisfyingHandler`/
`credentialExtractorFor`/`responseHeaderValues`/`responseCookieValues`/
`primaryStatusFor`/`asRedirectError`/`negotiateFormatReflect` exported for
cross-package aliasing; `negotiateRequestFormatReflect`/
`formatContentTypesReflect`/the reflect-based `tryRespondErrorPattern`
kept unexported, `buildRouteHandler`-exclusive). Repointed both
`adapters/nethttp`'s and `adapters/chi`'s `buildRouteHandler` to the thin
wrapper shape: resolve `Options`, build a `writeCookie` closure, then call
`restdispatch.DispatchRoute` from inside the existing ctx-wrapping
`http.HandlerFunc`. Verified nethttp's full suite, then chi's full suite
(including the Phase 3 case 5 cross-adapter test), both passing
unchanged.

- **Verification (steps 4-5)**: full-repo `go build ./...`, `go test ./...`
  (zero failures), `go vet ./...` (clean), `gofmt` all clean.

**Step 6 — `handlerFunc`'s construction-time gate.** User initially chose
to scope this round as steps 6+7 together; designing step 7 (routing
`handlerFunc`'s full per-request body through `DispatchRoute`) surfaced
real complexity beyond the "stretch goal" framing, so the round was
pulled back to step 6 alone (step 7 deferred — see below).

- **Finding that shaped the design**: `handlerFunc`'s `fn` and `impls`
  are NOT read from `handle.HandlerFn`/`handle.Implementations` —
  confirmed via exhaustive grep that every real call site
  (`IngestAdapter`/`HandlerLatest`/`PipelineHandler`/etc. in
  `binding.go`/`stream.go`) never calls `handle.WithHandler(fn)`, so
  `handle.HandlerFn` is unset; `fn`/`impls` arrive as `handlerFunc`'s own
  parameters instead. Calling the existing `ValidateRouteConstruction`
  as-is would have hit the "has no handler" error branch and panicked on
  literally every route — a guaranteed regression, not an edge case.
  Everything else `ConstructedRoute` carries (`MiddlewareHandlers`,
  `Requirements`, etc.) IS read directly from `handle` in both call
  shapes — only `fn`/`impls` diverge.
- **Resolved design**: added
  `restdispatch.ValidateRouteConstructionForHandler(adapterName, handle,
  fn, impls)` — shares the existing four-check core (refactored into a
  private `validateRouteConstruction` both entry points call) but
  resolves `HandlerFn`/`Impls` from the explicitly-supplied `fn`/`impls`
  parameters instead of the handle's own fields. The existing
  `ValidateRouteConstruction(adapterName, handle)` is unchanged for
  `buildRouteHandler`.
- **Added `restdispatch.ConstructionError`** — a named type (`Adapter`,
  `Route`, `Err` fields, with `Error()`/`Unwrap()`) used ONLY at the
  panic call site in each package's `handlerFunc` — not baked into
  `validateRouteConstruction`'s own returned errors, which stay the raw
  underlying error (preserving `buildRouteHandler`'s existing
  error-return contract/tests exactly, since `ValidateRouteConstruction`
  itself is unchanged for that path).
- **Both packages' `handlerFunc`** now call
  `ValidateRouteConstructionForHandler` at the top; on error,
  `panic(restdispatch.ConstructionError{...})` — the per-request body
  below is otherwise **completely untouched** this round, confirming
  this was purely an additive gate, not a dispatch rewrite.
- **Verification**: 2 new regression tests per package (4 total) —
  `TestHandlerFunc_MissingSecurityCoverage_Panics` (a route declaring a
  security scheme via `.Use()` but never attaching an implementation now
  panics with `ConstructionError` wrapping the same
  `rest.MissingSecurityMiddlewareError` `buildRouteHandler` would have
  returned as a plain error) and `TestHandlerFunc_ValidConstruction_DoesNotPanic`
  (a companion guard: the gate is additive, a valid route must not
  panic). Both packages' full suites re-verified passing unchanged
  BEFORE these new tests were added, confirming no existing test
  silently depended on the previously-missing gate's absence. Full-repo
  `go build ./...`, `go test ./...` (zero failures), `go vet ./...`
  (clean), `gofmt` all clean.
**Step 7 — `handlerFunc`'s full per-request body through
`DispatchRoute`.** Deferred twice earlier given the `fn`/`impls`-override
complexity found while scoping step 6; revisited once step 6 had already
solved that exact problem.

- **Investigation**: read `handlerFunc`'s ENTIRE per-request body
  (nethttp) end to end and compared every mechanism against
  `DispatchRoute`'s own reflect-based calls. Found the SAME pattern that
  recurred throughout this whole Phase 4/SSE-family effort: every
  apparent "different mechanism" turned out to be the SAME canonical
  `RouteHandle` method, just reached two different ways (generic direct
  call vs. reflection) — confirmed for: the merge mechanism
  (`MergeFields`+`DecodeVars` vs. `DecodeMerged`/`ApplyMergeFields` —
  `DecodeMerged` literally calls `ApplyMergeFields` internally, per
  step 6's own earlier finding); response merge-field encoding
  (`ResponseHeaderMergeFields`/`ResponseCookieMergeFields` vs.
  `EncodeResponseMergeFields` — the latter's own source is exactly the
  combined form of the former two calls); format negotiation
  (`negotiateFormat`/`negotiateRequestFormat` vs.
  `NegotiateFormatReflect`/`negotiateRequestFormatReflect` — same
  underlying `format.Format[T]` methods); `primaryStatus` (literally
  calls `primaryStatusFor` already); `tryRespondErrorPatternGeneric` vs.
  `tryRespondErrorPattern` (both call the same
  `RouteHandle.DispatchErrorResponse`/`rest.CallDispatchErrorResponse`
  machinery, confirmed matching call-site count and order at every
  validation/security/middleware/handler-error point). **No remaining
  unexplained divergence found** — unlike the mqtt5 case-6 precedent
  (where real behavioral differences WERE found), this generic/reflect
  duality is confirmed to be a pure dispatch-mechanism difference, not a
  behavior difference, consistently across the whole body.
- **Implemented**: rewrote both packages' `handlerFunc` to the thin-
  wrapper shape — `ValidateRouteConstructionForHandler` (unchanged from
  step 6) → build a `writeCookie` closure → wrap `ctx` with the
  package's own context keys (unchanged) → call
  `restdispatch.DispatchRoute` → `applyGeneralMiddleware(inner,
  cr.Impls)`. Deleted the now-fully-superseded generic per-request body
  from both packages, including `tryRespondErrorPatternGeneric` and
  `negotiateRequestFormat` (confirmed, via grep, exclusively used by
  `handlerFunc` — safe to delete) and the generic `primaryStatus`
  (ditto). `negotiateFormat` was KEPT (confirmed still used by
  `sseHandlerFunc`, a separate, untouched function) — same "check every
  helper for other callers before deleting" discipline used throughout
  Phase 4.
- **A real, unrelated build hazard hit during verification, not a
  design issue**: the sandboxed environment's root filesystem ran out of
  disk space mid-build (`go-build` cache had grown to ~9 GB) — resolved
  with `go clean -cache`, after which the full-repo build/test completed
  normally. Noted here only because it could otherwise look like a
  code-change failure if not explained.
- **Verification**: both packages' full suites pass unchanged; full-repo
  `go build ./...`, `go test ./...` (zero failures), `go vet ./...`
  (clean), `gofmt` all clean (one file needed a trailing `gofmt -w` after
  the large body deletion — purely whitespace, fixed immediately).

**Step 8 — cross-adapter conformance test, DONE. The REST family's
8-step plan is now fully complete.**

- **Design**: `adapters/internal/restdispatch` cannot import
  `adapters/chi` (that would invert the dependency direction this whole
  consolidation establishes), so the test can't literally call
  `gochi.URLParam`. Instead, it builds its own
  `restdispatch.ResponseWriter` implementation (a minimal, test-local
  `testStatusResponseWriter`) and TWO genuinely different `pathValues`
  implementations: `pathValuesStdlib` (the real
  `(*http.Request).PathValue` mechanism, identical to
  `adapters/nethttp`'s own) and `pathValuesManualTemplate` (a standalone
  path-template matcher, representationally equivalent to what any OTHER
  router — chi included — does internally, without introducing a
  dependency on one).
- **Test**
  (`adapters/internal/restdispatch/dispatch_conformance_test.go`,
  `TestDispatchRoute_ConsistentAcrossPathValueMechanisms`): builds ONE
  `*rest.RouteHandle[Req, Resp]` fixture with a path param, calls
  `ValidateRouteConstruction` ONCE, then `DispatchRoute` TWICE — once
  per `pathValues` mechanism — served via two separate
  `httptest.NewServer`s. Fires identical requests (valid and invalid
  UUID path param) at both and asserts byte-identical status codes and
  response bodies. This directly exercises the ONE genuine per-adapter
  seam Phase 2's design identified — everything else in the shared core
  is proven identical by construction (one function, one call site);
  only `pathValues` is adapter-supplied, so this is the one place future
  per-router divergence could hide.
- **Verification**: new test passes; `adapters/nethttp`'s and
  `adapters/chi`'s full suites re-verified unchanged; full-repo
  `go build ./...`, `go test ./...` (zero failures), `go vet ./...`
  (clean), `gofmt` all clean.

**REST family status: 8 of 8 steps DONE.**

**Post-completion hygiene note:** this session's own verification loop
(`go build`/`go test`/`go vet`/`gofmt -l` on touched files) does not catch
unused package-level declarations — only `staticcheck` (via `just check`)
does. Running `just check` after the REST and SSE families were both
marked complete found 10 genuinely dead declarations (5 symbols ×
`adapters/nethttp`/`adapters/chi`: `primaryStatusFor`, `httpTransport`,
`callErr`, `callErrStatusFor`, `validateImplementationShapesReflect`) —
package-local aliases/copies kept during earlier steps because they were
still called at the time, orphaned once later steps (handlerFunc's full
`DispatchRoute` wiring, the SSE consolidation) replaced their last
remaining call sites too. All 10 were removed; `just check`
(staticcheck + gosec + `gofmt -l`) and the full build/test/vet/fmt loop
were re-run clean afterward. No behavior change — pure dead-code removal.
Lesson for future rounds: run `just check`, not just `go vet`, after any
consolidation round that replaces a package-local helper's last caller.

#### Phase 4 progress: SSE family (the `serve_sse.go` follow-on audit, now closed)

**Status: DONE.** The `serve_sse.go` follow-on audit (flagged throughout
Phase 4 but never investigated) was finally carried out, confirmed the
suspected duplication was real, and the consolidation was implemented in
the same round, following the REST family's already-proven pattern.

- **Audit finding**: diffed (package-name/comment-normalized) every
  function in both packages' `serve_sse.go` — `writeSSEData` confirmed
  byte-for-byte identical; `buildSSERouteHandler`'s ENTIRE body
  (construction-time checks AND the per-event streaming dispatch)
  confirmed essentially identical too, the exact same shape of gap the
  REST family's `buildRouteHandler` already had.
- **Key simplification found while designing the fix**: `rest.SSERouteHandle[Req, Event]`'s
  field names/types (`Descriptor`, `SecuritySchemes`, `GlobalSecurity`,
  `Implementations`, `MiddlewareHandlers`, `HandlerOpts`, `HandlerFn`,
  `Requirements`) match `rest.RouteHandle[Req, Resp]`'s exactly — so
  **no new construction-time entry point was needed at all**: the
  existing `restdispatch.ValidateRouteConstruction` is reused directly
  for SSE routes too. Only `ConstructedRoute.RespType`'s derivation
  means something different for SSE's `func(ctx, Req, send func(Event)
  error) error` handler shape (captures the bare `error` type instead of
  a real Resp type) — harmless, since the new SSE dispatch function
  never reads `cr.RespType`.
- **One small additive capability needed**: the per-event body reads
  back the response's already-committed status code
  (`sw.code == http.StatusOK`, deciding whether a late handler error can
  still produce a normal error response). Added a `StatusCode() int`
  method to both packages' `statusResponseWriter` and to
  `restdispatch.ResponseWriter`'s interface.
- **Implemented**: promoted `writeSSEData` into `restdispatch` (aliased
  in both packages); added `restdispatch.DispatchSSERoute` — the full
  per-event streaming pipeline, reusing every helper already shared from
  the REST family's steps 1-6 (`CallErr`, `CredentialExtractorFor`,
  `IsSecuritySatisfyingHandler`/`SatisfiesPerHandler`,
  `NegotiateFormatReflect`, `ResponseHeaderValues`/`ResponseCookieValues`,
  `AsRedirectError`, `HTTPCarrier`) — confirming Phase 4's investment in
  the REST family's shared helpers pays off again here, not just once.
  Confirmed (and preserved) that SSE has NO `rest.ErrorPattern` dispatch
  at all, unlike `DispatchRoute` — a pre-existing, separate gap this
  consolidation does not silently add. Rewrote both packages'
  `buildSSERouteHandler` to the thin-wrapper shape: construction via the
  existing `ValidateRouteConstruction`, then `DispatchSSERoute`.
- **Verification**: both packages' full suites pass unchanged; full-repo
  `go build ./...`, `go test ./...` (zero failures), `go vet ./...`
  (clean), `gofmt` all clean.
- **Follow-on, now DONE**: added
  `TestDispatchSSERoute_ConsistentAcrossPathValueMechanisms`
  (`adapters/internal/restdispatch/dispatch_sse_conformance_test.go`),
  mirroring the REST family's step 8 conformance test exactly — one
  `*rest.SSERouteHandle[Req, Event]` fixture (same `{id}` path-param
  shape), `ValidateRouteConstruction` called once (confirmed unchanged
  for SSE handles), `DispatchSSERoute` called twice (once per
  `pathValues` mechanism — the same stdlib-`PathValue`/manual-template
  stand-ins the REST conformance test already defined, reused directly
  since both tests live in the same package), served via two
  `httptest.NewServer`s. Parses each response's SSE `data:` lines and
  asserts byte-identical framed event output and status code for both a
  valid and invalid path-param value. Verified: passes; full-repo
  `go build ./...`, `go test ./...` (zero failures), `go vet ./...`
  (clean), `gofmt` all clean. **Both the REST and SSE families' own
  conformance tests are now complete.**

#### Phase 4 progress: events/reqreply family, Workstream A (`adapters/mqtt5`) — Subscribe literal dispatch-core merge

**Status: PARTIALLY DONE** (Subscribe side only; Publish and reqreply
remain per Workstream A's own steps 6-8, not started). The literal merge
Phase 3 case 6 deferred as "too risky" — because at the time
`transport.Subscribe` and `caller.go`'s `ServeSubscribers` dispatch had
genuinely diverged (topic-var/property-merge only on one path,
Disposition tracking only on the other) — is now safe: case 6's own
behavioral-parity fix closed exactly those two divergences, and a fresh
side-by-side re-reading of both bodies post-fix found them **byte-for-byte
identical**, field for field, not merely behaviorally equivalent. This
is the literal merge the design's point 1 originally called for (3 → 2
implementations per adapter: one shared reflect-core, plus the
compile-time-generic `subscribeWithHandle[T]` escape hatch, which cannot
share a function with the reflect paths — Go has no reflective generic
instantiation).

- **Struct alignment**: added `buildErasedSubscriberHandleFields(handleVal, elem
  reflect.Value) erasedSubscriberHandle` (`caller.go`) — the single place
  every field both `transport.Subscribe` and `ServeSubscribers`' dispatch
  need is now extracted, replacing `transport.Subscribe`'s own scattered
  ad hoc `elem.FieldByName(...)` calls. `extractErasedSubscriberHandle`
  (ServeSubscribers' entry point, which also needs the channel's own
  `Handler`/`HandlerOpts` fields) now wraps this shared constructor rather
  than duplicating its field-extraction logic.
- **Shared dispatch core**: extracted `subscribeDispatchCore(ctx, client,
  obs, info, wrappedFn, formatsOverride, userPropertyParams, onErrorFn)
  pahomqtt5.MessageHandler` (`transport_dispatch.go`) — the full
  per-message pipeline (decode-with-vars-and-formats → property-var
  extraction + merge → user-property validation → built-in security →
  Implementations security → middleware dispatch → unified `CheckScopes`
  → disposition/span → wrapped-handler call → `subscribeDispatchFailure`
  on any step's error), assembled verbatim from the two confirmed-
  identical bodies — ONE canonical version now, not two.
- **`transport.Subscribe` repointed**: keeps its own unique setup
  (`recoverHandle`, implementation shape validation, the Subscribe-side
  capability coverage check, `wrapHandlerGeneralReflect`, filter
  derivation, broker subscribe/unsubscribe bracket) exactly as before;
  builds its `erasedSubscriberHandle` via the shared constructor; calls
  `subscribeDispatchCore` for the handler closure.
- **`caller.serveOneEntry` repointed**: wraps the handler once (mirroring
  `transport.Subscribe`'s identical timing), calls the SAME
  `subscribeDispatchCore`; the now fully-superseded
  `makeErasedSubscribeMessageHandler` function was deleted.
- **New regression tests**
  (`adapters/mqtt5/subscribe_dispatch_core_test.go`):
  `TestSubscribeDispatchCore_DirectCall` (topic-var merge, proven by
  calling `subscribeDispatchCore` directly, independent of either entry
  point) and `TestSubscribeDispatchCore_DecodeFailure_InvokesOnError`
  (decode failure reaches `onErrorFn`, handler never invoked) — seeding
  Phase 5's own "a test per confirmed dispatch entry point" principle at
  the shared core itself, not just through one entry point's own tests.
- **Verification**: `adapters/mqtt5`'s full suite passes unchanged after
  each repointing step (including Phase 3 case 6's own 3 parity tests);
  full-repo `go build ./...`, `go test ./...` (zero failures),
  `go vet ./...` (clean), `gofmt -l` (clean), and `just check`
  (staticcheck + gosec + fmt) all clean — confirmed no new dead code was
  left behind by `makeErasedSubscribeMessageHandler`'s deletion, per the
  lesson from this session's earlier REST/SSE dead-code cleanup round.

**Step 6 — Publish confirmation found a real ordering divergence, not
just a confirmation.** Per this design's own point 1, Publish's
reflect-side duplication was expected to already be "effectively 1
implementation per adapter structurally" (no `ServePublishers`-equivalent
third entry point) — true: both `transport.Publish`'s coverage check and
`events.ApplyCapabilities` call were already present and already one
function, not duplicated. But re-reading `transport.Publish` (reflect)
side by side with `publish[T]` (generic) found their CALL SEQUENCE had
silently diverged:

- **Generic `publish[T]`'s order**: coverage check
  (`VerifyCapabilityCoverage`) → `ApplyCapabilities` → implementation-
  shape validation → property-var merge → middleware dispatch →
  `BuildTopic` → security credential dispatch/validate → transmit. The
  coverage check runs FIRST, before any middleware/security side effects.
- **Reflect `transport.Publish`'s order (before this fix)**:
  implementation-shape validation → property-var merge → middleware
  dispatch → `BuildTopic` → security credential dispatch/validate →
  coverage check → `ApplyCapabilities` → transmit. The coverage check ran
  LAST, AFTER middleware dispatch and security credential validation had
  already executed.
- **Consequence**: a caller relying on "a capability-coverage failure
  means nothing else ran" (true and reasonable on the generic path) was
  silently wrong on the reflect path — a `PublishBoundMW` Fn's side
  effects (and any security credential check) would run BEFORE the
  declared-but-unmet requirement was ever detected.
- **Second, smaller divergence found alongside it**: the generic path's
  coverage check and `ApplyCapabilities` call both use `handle.Topic`
  (the raw, undecorated channel topic); the reflect path used
  `finalTopic` (the resolved, post-topic-vars topic) for the same calls
  instead — only observable when a channel's topic vars actually resolve
  to a different string, but a real inconsistency in the observability
  labels passed to `RecordCapabilityApplied`/span naming.

This is the same class of finding Subscribe needed case 6's
reconciliation for — silent cross-path sequencing drift, not
duplication. **Fix**: reordered `transport.Publish` to match `publish[T]`'s
exact position (coverage check + `ApplyCapabilities`, using the raw
`topic`, moved to run immediately after implementation-shape validation,
before property merge/middleware/security run at all) — a mechanical,
behavior-aligning reorder, not a new mechanism. Added 2 new regression
tests (`publish_dispatch_order_test.go`) exercising `Client.Publish`
(the reflect path, through `Client.Attach`): one proving a
`PublishBoundMW` Fn does NOT run when coverage fails (verified this test
actually fails against the pre-fix ordering, confirming it's meaningful,
not a false positive), one confirming the fix is not a regression
(middleware still runs and the message still publishes when a matching
Capability is supplied). Verified: `adapters/mqtt5`'s full suite
unchanged plus the 2 new tests; whole-repo `go build`/`go test` (zero
failures)/`go vet`/`gofmt -l` clean; `just check` (staticcheck + gosec +
fmt) clean.

- **Remaining in Workstream A** (at this point): the reqreply Serve-side
  setup-core extraction (step 7) and conformance tests seeding Phase 5
  for this family specifically (step 8). Workstream C (`adapters/zeromq`)
  is untouched.

#### Phase 4 progress: events/reqreply family, Workstream A (`adapters/mqtt5`), steps 7-8 — reqreply setup-core extraction + conformance tests — WORKSTREAM A FULLY COMPLETE (8/8)

**Status: DONE.** Read `(*serverTransport).Serve` (the ONLY server-socket
variant mqtt5 has — confirmed, no second variant exists) end to end
alongside `adapters/zeromq`'s already-built `reqreply_dispatch_core.go`
(Workstream C's reference shape) before implementing. Confirmed the
roadmap's own step-7 framing: since mqtt5 has only one variant, this is
a pure refactor-for-future-reuse-by-convention, not a merge of two
existing duplicates — no `reqreplyReplySink`-style abstraction was
needed (that exists in zeromq specifically to parameterize over its
REQ/REP-vs-ROUTER reply-framing difference, which mqtt5 has no second
variant of). Scope stayed a straightforward 2-function split, mirroring
the *shape* zeromq proved (setup-core + dispatch-one, standalone
functions) without inventing an unneeded abstraction.

Found the natural split point directly: `Serve` divided cleanly into a
one-time setup block (route-handle recovery, req/resp-type + fn-shape
validation, decode/encode/dead-letter method resolution,
`hasMergeFields`/`hasPropertyMergeFields` flags, Implementations/
MiddlewareHandlers extraction + `CheckCoverage`, capability requirements
+ `ApplyCapabilities`/`VerifyCapabilityCoverage` → `effectiveQoS`/
`effectiveRetained`, `requestHeaderParams`) and a per-message closure
(`baseHandler`: User-Property validation ×2, topic-var extraction/
validation, decode, property-var merge, security-credential validation,
paired security middleware, codec-backed middleware dispatch, unified
`CheckScopes`, handler call, encode, reply/forward/error/dead-letter
publishing) followed by a short, non-reusable bracket
(`RegisterHandler`+`Subscribe`+`return nil`) that stayed in `Serve`
as-is.

**Implemented**: new file `adapters/mqtt5/reqreply_dispatch_core.go`
with `reqreplyServerSetup` (struct bundling every field the per-message
body needs), `reqreplyServerSetupCore` (the former inline setup block,
assembled verbatim), and `reqreplyServerDispatchOne` (the former
`baseHandler` body, assembled verbatim — same byte-identical-extraction
discipline used throughout this session's other dispatch-core work).
`(*serverTransport).Serve` now calls `reqreplyServerSetupCore` once,
then registers a thin closure delegating to `reqreplyServerDispatchOne`
via `applyGeneralServerMiddleware` exactly as before.

**Step 8 (conformance tests seeded here)**: added
`adapters/mqtt5/reqreply_dispatch_core_test.go` — two tests calling
`reqreplyServerSetupCore`/`reqreplyServerDispatchOne` directly against a
hand-built route handle and a crafted `*paho.Publish`, independent of
`Serve`/the mock client-router harness: a happy-path request/response
round trip, and a decode-failure case proving the handler is never
invoked and `onError` receives `KindDecode`. (A test-authoring pitfall
caught during this round, unrelated to the extraction itself: the
shared `computeReq`/`computeResp` fixture's codec declares lowercase
JSON field names `x`/`y`/`sum` — not the Go struct field names `X`/`Y`/
`Sum` — an easy, unrelated mistake to make when hand-crafting a raw JSON
payload for a direct dispatch-core test; both tests were fixed to use
the correct casing and now pass for the right reason.)

Verified: `adapters/mqtt5`'s full suite unchanged plus the 2 new tests;
whole-repo `go build`/`go test` (zero failures)/`go vet`/`gofmt -l`
clean; `just check` (staticcheck + gosec + fmt) clean — confirmed no
orphaned helper left behind by the extraction (the two now-unused
imports in `reqreply_transport.go`, `errors` and
`adapters/internal/scopesmerge`, were removed; both are still used, now
solely from the new file).

**Workstream A is now fully complete — all 8 steps done** (Subscribe
dispatch-core merge, Publish ordering fix, reqreply setup-core
extraction, reqreply conformance tests). Workstream C
(`adapters/zeromq`) remains fully complete from earlier in Phase 4 too —
see its own progress subsections above. The events/reqreply family's
Phase 4 dispatch-core consolidation scope is therefore complete across
all three adapters (mqtt5, mqtt v3, zeromq).

#### Phase 4 progress: events/reqreply family, Workstream B (`adapters/mqtt` v3) — Subscribe dispatch-core merge

**Status: DONE** (Subscribe side; Publish and reqreply are out of scope
for v3 — mqtt v3 is events-only, confirmed by Phase 1's own audit).
Applied the proven Workstream A recipe to `adapters/mqtt` (v3), condensed
into a single round since mqtt5's experience showed the parity-fix and
literal-merge steps compose safely together.

**Investigation found mqtt v3 was in the exact pre-case-6 state mqtt5 was
in** — a confirmed 3-way Subscribe dispatch split, shaped as a mirror
image of mqtt5's original divergence (2 of 3 paths agreed on one axis,
and the third diverged on BOTH axes, rather than mqtt5's "each path had
exactly one gap"):

| Path | Topic-var support | Disposition tracking |
|---|---|---|
| `subscribeHandler[T]` (generic, `adapter.go`) | yes | **no** |
| `(*transport).Subscribe` (reflect, `transport.go`) | yes | **no** |
| `subscribeEntryReflect` (reflect, `caller.go`, backs `ServeSubscribers`) | **no** | yes |

A THIRD divergence was found while writing the shared dispatch core (not
caught by the initial topic-var/Disposition investigation alone): the two
reflect paths' middleware-dispatch-error handling disagreed on BOTH
error-kind classification and detail. `transport.Subscribe` distinguished
`IsFnError` (→ `KindHandler`, wrapped as `events.MiddlewareError`, with
`stats.ReportErrors`) from the encode/"in" case (→ `KindDecode`) but never
checked whether the failing Fn was a Security-carrying handler.
`subscribeEntryReflect` had the OPPOSITE gap: it checked
`events.IsSecuritySatisfyingHandler` (correctly escalating to
`KindSecurity` + `RecordSecurityRejection`) but otherwise always fell
back to `KindDecode` regardless of `IsFnError`, and never called
`stats.ReportErrors` at all. No per-message-re-wrap inefficiency was
found this time (both reflect paths already hoist their wrap composition
once, outside the per-message closure, unlike mqtt5's pre-fix state).

**Fix — full "add parity" superset, reapplying mqtt5's case 6 precedent**:
new `adapters/mqtt/subscribe_dispatch_core.go` defines
`erasedSubscriberHandle` (leaner than mqtt5's — v3 has no User-Property/
property-merge mechanism at all) + `buildErasedSubscriberHandleFields`
(the shared constructor) + `subscribeDispatchCore` (the full per-message
pipeline, assembled as the superset of all three found divergences:
topic-var merge via `matchTopicTemplate`/`DecodeMergedWithFormats`,
Disposition tracking via `EnsureDispositionBox`/`ResolveDisposition`/
`RecordDisposition`, and the complete middleware-error-kind logic
combining both paths' partial fixes). `transport.Subscribe` and
`subscribeEntryReflect` both repointed to call it. The generic
`subscribeHandler[T]`'s own Disposition gap was fixed separately and
directly in `adapter.go` (not part of the dispatch-core merge, since it's
a distinct, compile-time-typed function that cannot share code with the
reflect paths — the same "3 → 2, not 3 → 1" principle Workstream A
established).

Added 4 new regression tests: `TestServeSubscribers_TopicVarMerge_NowSupported`,
`TestAttach_ClientSubscribe_Disposition_NowSupported`,
`TestSubscribeHandler_Disposition_NowSupported` (closing each confirmed
gap, mirroring mqtt5's identically-named tests — `subscribe_dispatch_parity_test.go`)
and `TestSubscribeDispatchCore_DirectCall`/
`TestSubscribeDispatchCore_DecodeFailure_InvokesOnError` (direct core
tests, mirroring mqtt5's `subscribe_dispatch_core_test.go`).

Verified incrementally after each repointing step (transport.go, then
caller.go, then the generic-path Disposition fix); `adapters/mqtt`'s full
suite (157 tests) passes unchanged plus all 4 new tests; whole-repo
`go build`/`go test` (zero failures)/`go vet`/`gofmt -l` clean; `just
check` (staticcheck + gosec + fmt) clean.

**Remaining in Workstream B**: none — mqtt v3 has no reqreply surface
(events-only adapter), so Workstream B is now fully complete.

#### Phase 4 progress: events/reqreply family, Workstream C (`adapters/zeromq`), events side — parity fix (not a literal merge)

**Status: DONE for Subscribe's parity gaps; literal merge NOT performed
(documented, deliberate choice, see rationale below); Publish and
reqreply sides of Workstream C remain entirely unstarted.**

**Investigated** `transport.go`'s `(*transport).Subscribe` (the
ports.Pattern-binding path — a BLOCKING RECEIVE LOOP, architecturally
different from mqtt5/mqtt v3's callback-registration model, since ZeroMQ
SUB sockets require a dedicated polling loop) side by side with
`serve_subscribers.go`'s `(*subscriberRoute).processMessage` (the
ServeSubscribers/Client.Attach path, which already dispatches via a
per-route, per-message method — closer in shape to mqtt5/v3's dispatch
cores than `transport.Subscribe`'s inline loop body). Found
`processMessage` is already the superset — it has topic-var merge,
Disposition tracking, AND `ValidateTopic`/`ValidateTopicVars` calls
`transport.Subscribe` lacked:

| Feature | `transport.Subscribe` (before fix) | `processMessage` |
|---|---|---|
| Topic-var merge | yes | yes |
| Disposition tracking | **no** | yes |
| `ValidateTopic`/`ValidateTopicVars` | **no** | yes |
| Formats-override support | yes (`ClientSubscribeOptions.Formats`) | no (`SubscribeOptions[T]` has no Formats field — same deliberately-unfixed asymmetry mqtt5/mqtt v3 flagged) |

`ValidateTopic`/`ValidateTopicVars` is a genuinely new finding beyond the
topic-var/Disposition shape prior Workstreams found: a channel declared
with `WithTopicCodec`/`WithTopicConstraints` (client-level) or a
per-variable `TopicParam` codec constraint was silently unenforced
against RECEIVED concrete topics on `transport.Subscribe`'s path — decode-
time merge-field validation only covers topic params that are ALSO merge
fields, not every declared constraint.

**Fix — targeted parity, not a literal merge.** Unlike Workstreams A/B
(where the two reflect paths shared the same reflect.Value-driven
per-message-closure shape and could be merged into one function), this
round deliberately applies ONLY the "add parity" fixes directly to
`transport.Subscribe`'s existing body — adding `ValidateTopic`/
`ValidateTopicVars` calls (mirroring `processMessage`'s placement, before
decode) and per-message `stats.EnsureDispositionBox`/`ResolveDisposition`/
`RecordDisposition` (mirroring `processMessage`'s placement, around the
handler call) — rather than attempting to force `transport.Subscribe`'s
loop body through `(*subscriberRoute).processMessage` directly. Rationale:
`transport.Subscribe`'s own setup (implementation-shape validation,
general-purpose wrap composition, requirement-coverage check) uses a
DIFFERENT general-wrap mechanism and a DIFFERENT `SubscribeOptions`
source than `buildSubscriberRoute`'s (`ClientSubscribeOptions` call-time
param vs. `SubscribeOptions[T]`'s declared `HandlerOpts`) — reconciling
these into one shared constructor is a larger, higher-risk undertaking
than the targeted fix, and mirrors mqtt5's OWN original case 6 caveat
("behavioral parity... judged too risky given generic-vs-reflect typing
differences... not a literal single merged function") — the same
precedented, lower-risk choice, made explicit here rather than silently
deferred.

One subtlety handled carefully: `EnsureDispositionBox` must be called
PER MESSAGE, not once before the loop (unlike the loop's other
pre-computed, reused-across-messages values) — a shared box across every
message would let one message's `SetDisposition` call leak into
another's.

Added 2 new regression tests
(`adapters/zeromq/subscribe_dispatch_parity_test.go`):
`TestAttach_ClientSubscribe_ValidateTopic_NowSupported` (a topic
constraint that passes the declared template but rejects a specific
concrete delivered topic, isolating receive-time validation from
declare-time validation) and
`TestAttach_ClientSubscribe_Disposition_NowSupported` (mirrors mqtt5's/
mqtt v3's identically-named tests). Both verified to actually FAIL
against the pre-fix code before restoring the fix, confirming they are
meaningful regression guards, not false positives.

Verified: `adapters/zeromq`'s full suite unchanged plus the 2 new tests;
whole-repo `go build`/`go test` (zero failures)/`go vet`/`gofmt -l`
clean; `just check` (staticcheck + gosec + fmt) clean.

#### Phase 4 progress: events/reqreply family, Workstream C (`adapters/zeromq`), Publish ordering fix

**Status: DONE.** Audited `transport.go`'s `(*transport).Publish` (the
reflect path, backing `Client.Attach`'s `Client.Publish`) against the
generic `publish[T]` the same way mqtt5's Workstream A step 6 audited its
own two Publish paths — and found the **identical ordering bug**:
`transport.Publish` checked capability-requirement coverage LAST
(immediately before transmit, AFTER middleware dispatch and security
implementations dispatch had already executed), while `publish[T]`
checks coverage FIRST, before any of those run. Also carried the same
observability-label inconsistency (`finalTopic`, post-`BuildTopic`,
instead of the raw `topic` `publish[T]` uses).

**Fix**: identical to mqtt5's — moved the coverage check +
`events.ApplyCapabilities` to run immediately after implementation-shape
validation, using the raw topic, deleting the old late-position block.
Added 2 regression tests
(`adapters/zeromq/publish_dispatch_order_test.go`,
`TestClientPublish_RequireHWM_NoCapability_FailsBeforeMiddlewareRuns`/
`..._SuppliedCapability_RunsMiddlewareAndPublishes`), mirroring mqtt5's
own Publish-ordering tests exactly (declares `events.RequireHWM`, a
`PublishBoundMW` with an observable side effect) — verified the first
test actually fails against the pre-fix ordering before restoring the
fix.

Verified: `adapters/zeromq`'s full suite unchanged plus the 2 new tests;
whole-repo `go build`/`go test` (zero failures)/`go vet`/`gofmt -l`
clean; `just check` clean.

**This closes the Publish side of Workstream C entirely — and also
corrected a mistaken claim this doc made about Workstream B.** While
writing this round's cross-adapter comparison, re-checked `adapters/mqtt`
(v3) too and found it had the IDENTICAL, still-unfixed ordering bug —
Workstream B's own write-up above did not audit this (it only touched
mqtt v3's Subscribe side, not Publish) and should not have been read as
implying Publish was already fine there. Fixed `adapters/mqtt`'s
`transport.Publish` the same way in this same round, with the same 2
regression tests (`adapters/mqtt/publish_dispatch_order_test.go`),
verified against mqtt v3's full suite (unchanged) and the same
fails-before-fix check. The ordering bug is now confirmed and fixed
identically across all 3 event adapters (mqtt5, mqtt v3, zeromq) that
have a reflect Publish path alongside a generic one.

**Remaining in Workstream C**: the full reqreply 4-socket-variant
setup/dispatch-one split (point 3 of this family's design — the largest
remaining piece of the entire events/reqreply family, covering
`serverTransport.Serve`/`routerServerTransport.Serve` and their
client-side counterparts).

#### Phase 4 progress: events/reqreply family, Workstream C (`adapters/zeromq`), reqreply server-side setup/dispatch-one split

**Status: DONE for the SERVER side** (`serverTransport.Serve`/
`routerServerTransport.Serve`); the client side
(`clientTransport.call`/`dealerClientTransport.call`) remains unstarted.

Investigated `serverTransport.Serve` (plain REQ/REP) side by side with
`routerServerTransport.Serve` (ROUTER) — exactly as point 3 of this
family's design predicted: the SETUP phase (route-handle recovery,
handler-shape validation, `reqreply.CheckCoverage`, capability
application + `reqreply.VerifyCapabilityCoverage`, general-purpose
middleware wrapping) is confirmed **byte-for-byte identical** between the
two (the code's own doc comments already said so: "same mechanism...
duplicated for the ROUTER variant"). The per-message DISPATCH body
(decode → property-merge → paired-security → codec-middleware →
unified-scopes-check → handler → encode) is ALSO confirmed near-identical
— the only genuine differences are reply framing (REQ/REP: `[status,
payload]`, 2 frames, no addressing; ROUTER: `[identity, delimiter,
status, payload]`, 4 frames, echoing the sender's identity) and
concurrency (REQ/REP processes sequentially; ROUTER spawns one goroutine
per request, since ROUTER allows concurrent in-flight requests from
multiple peers while REQ/REP enforces strict one-reply-per-request
ordering).

**Design**: new `adapters/zeromq/reqreply_dispatch_core.go` —
`reqreplyServerSetupCore(routeAny, fnAny, sock, obs, caps)
(*reqreplyServerSetup, error)` (the shared setup phase, assembled
verbatim from the confirmed-identical block) and
`reqreplyServerDispatchOne(ctx, setup, sink, sockets, obs, payload,
onError)` (the shared per-message pipeline), parameterized by a small
`reqreplyReplySink` struct (`label` for the `stats.Observer.RecordRequest`
protocol tag, plus `sendSuccess`/`sendError`/`sendForward` closures) that
captures the ONE genuine per-variant difference — reply framing/identity
— leaving the dispatch body itself completely socket-agnostic. ROUTER's
goroutine-per-request concurrency and REQ/REP's sequential loop both stay
in their own `Serve` function (the protocol-necessitated recv-loop
mechanics point 3 of the design said must stay unmerged); only the
per-message dispatch is unified.

Repointed both `serverTransport.Serve` and `routerServerTransport.Serve`
to thin wrappers: call `reqreplyServerSetupCore` once, build their own
`reqreplyReplySink` (REQ/REP: built once per `Serve` call; ROUTER: built
per request, inside the goroutine, since it needs that specific request's
identity frame), then call `reqreplyServerDispatchOne` from their own
recv loop/goroutine. Verified incrementally — `serverTransport.Serve`
repointed and tested first, then `routerServerTransport.Serve` — both
confirmed unchanged against the full `adapters/zeromq` suite (including
every reqreply-specific test: dead-letter, error-pattern, forward,
capability, disposition, security-middleware tests for both variants).

Added 2 new regression tests
(`adapters/zeromq/reqreply_dispatch_core_test.go`):
`TestReqreplyServerDispatchOne_DirectCall` (decode → handler → encode
round-trip, called directly against the shared core, independent of
either `Serve` variant) and
`TestReqreplyServerDispatchOne_DecodeFailure_InvokesOnError` (decode
failure reaches `onError`, handler never invoked) — seeding Phase 5's
"a test per confirmed dispatch entry point" principle at the shared core
itself, mirroring mqtt5's/mqtt v3's identical `subscribe_dispatch_core_test.go`
pattern for this family's reqreply side.

Verified: `adapters/zeromq`'s full suite unchanged plus the 2 new tests;
whole-repo `go build`/`go test` (zero failures)/`go vet`/`gofmt -l`
clean; `just check` (staticcheck + gosec + fmt) clean — confirmed no
dead code left behind by the two large extractions (the old inline
bodies were fully deleted, not left as unreferenced duplicates).

#### Phase 4 progress: events/reqreply family, Workstream C (`adapters/zeromq`), reqreply client-side setup/dispatch-one split

**Status: DONE.** This closes the client side — and with it, the LAST
piece of the entire events/reqreply family's dispatch-core consolidation
scope (per point 3 of this family's design).

Investigated `clientTransport.call` (plain REQ) side by side with
`dealerClientTransport.call` (DEALER) — found them to be an even closer
match than the server side's two `Serve` variants: contrary to this
family's own design text (which anticipated DEALER's "inherently async
multiplexed" nature would force a correlation-bookkeeping divergence),
this actual implementation's `dealerClientTransport.call` is **fully
synchronous**, just like `clientTransport.call` — it sends one request
then blocks in a recv-poll loop for exactly one reply, with no async
correlation logic anywhere. The ENTIRE setup phase (route recovery,
capability application + requirement coverage, request type-check, the
observability-only `BuildTopic` path override, per-call format-override
resolution, client implementation-shape validation) and the ENTIRE
dispatch body (credential Fn → codec-middleware-in → encode → send →
recv-poll → forward/error/ordinary reply classification → codec-
middleware-out → decode), including the `reflect.MakeFunc`-based
general-purpose `ClientMW` decorator wrapping, are confirmed
**near-identical** — the only genuine differences are wire framing
(REQ: send `[payload]`, recv `[status, payload]`; DEALER: send
`[emptyDelimiter, payload]`, recv `[delimiter, status, payload]`, needing
the empty delimiter DEALER requires to mimic REQ's hidden envelope) and
the observability label ("ZMQ-REQ"/"ZMQ-DEALER").

**Design**: new `adapters/zeromq/reqreply_client_dispatch_core.go` —
split into THREE pieces (not two, unlike the server side), to preserve
exact `TraceObserver` span timing:
1. `reqreplyClientRecoverAndResolvePath` — the setup steps that MUST run
   **before** the span starts (matching both pre-extraction bodies'
   exact span placement: immediately after the observability-only
   `BuildTopic` override resolves, before format-override resolution) —
   route recovery, capability application + coverage, type-check,
   `BuildTopic` override.
2. `reqreplyClientResolveDispatchSetup` — the setup steps that run
   **after** the span starts — format-override resolution,
   implementation-shape validation — returning the full
   `reqreplyClientSetup`.
3. `reqreplyClientCallOne` — the shared dispatch body (the
   `reflect.MakeFunc`-based `innerCall`/`wrappedCall` logic, assembled
   verbatim from the two confirmed near-identical bodies), parameterized
   by a `reqreplyClientSink` struct (`label` + `sendRequest` +
   `recvReply`) capturing the one genuine per-variant difference. A
   shared `reqreplyClientRecvOne` helper covers the recv-poll mechanics
   common to both (`SetRecvTimeout`, `ctx.Done()` check, `ErrTimeout`
   retry) — each sink's `recvReply` wraps it, validates its own expected
   frame count, and strips its own envelope prefix (DEALER's leading
   delimiter), so the rest of the dispatch body operates on uniform
   "logical frames" regardless of variant.

This 3-way split (vs. the server side's 2-way setup/dispatch split) was
necessary specifically to avoid a subtle, easy-to-miss behavior change:
collapsing setup into one function would have meant format-override
failures no longer being wrapped by the `TraceObserver` span the way
they were before this extraction — traced here precisely, not silently
accepted, consistent with this whole effort's "no silent behavior
changes" discipline.

Repointed both `clientTransport.call` and `dealerClientTransport.call`
to thin wrappers. `CallRaw` (a separate, pre-existing Phase 0 primitive
on both transports, with its own already-established mirroring
discipline) is explicitly OUT of scope for this round — left untouched,
a candidate for a future round if ever needed.

Added 1 new regression test
(`adapters/zeromq/reqreply_client_dispatch_core_test.go`,
`TestReqreplyClientCallOne_DirectCall`) exercising the full
`reqreplyClientRecoverAndResolvePath`/`reqreplyClientResolveDispatchSetup`/
`reqreplyClientCallOne` pipeline directly against a scripted mock-socket
reply, independent of either `call` variant.

Verified: `adapters/zeromq`'s full suite unchanged (including every
Call-specific test: forward, error-pattern, capability, security-
middleware, trace-span tests for both REQ and DEALER variants) plus the
1 new test; whole-repo `go build`/`go test` (zero failures)/`go
vet`/`gofmt -l` clean; `just check` (staticcheck + gosec + fmt) clean —
confirmed no dead code left behind by this ~700-line extraction.

**This completes Workstream C, and with it, the entire events/reqreply
family's Phase 4 dispatch-core consolidation scope** (Workstreams A, B,
and C's Subscribe/Publish/reqreply splits across mqtt5, mqtt v3, and
zeromq) — except for mqtt5's own reqreply setup-core extraction (step 7
of Workstream A), which remains a standalone, lower-priority item since
mqtt5 has only ONE reqreply server-socket variant (no current
duplication to resolve there, unlike zeromq's confirmed 4-way split),
and conformance-test seeding for Phase 5 across the family.

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

**Done** — `adapters/mqtt5/granted_scopes_context_field_test.go` adds this
as an automated regression test, mirroring the example's scenario (local
fixtures only, no new `examples/` dependency): a correctly-scoped API key
is accepted and the authenticated key is readable via the `ContextField`
with zero manual re-decoding; a known key whose granted scopes don't
satisfy the declared requirement is rejected with `KindSecurity`, and the
handler never runs. Both paths are now proven against the shared
`subscribeDispatchCore` dispatch path (the same one closed by this
session's Workstream A Subscribe merge), not just the standalone example.

A debugging note worth recording for future test authors in this package:
this package's `mockClient`/`mockRouter` pair is **not a loopback
broker** — `evtClient.Publish(...)` only records the outgoing message on
the mock client (`client.lastPublished()`), it does **not** automatically
deliver it back to registered subscribers. Every existing test in this
package that exercises a Publish→Subscribe round trip explicitly bridges
the two via `router.dispatch(topic, client.lastPublished())` (or an
inline-constructed `*pahomqtt5.Publish`) — a new test that publishes via
the generic `events.Client` API and omits this manual dispatch call will
silently never invoke any handler or `OnError` callback, and a declared-
but-never-assigned result variable (e.g. `var rejected SubscribeError`)
will read back as its **zero value**, not as "no error occurred" — for
`SubscribeError`, that zero value happens to be `{Kind: KindDecode, Err:
nil}` (since `KindDecode` is error-kind iota 0), which looks confusingly
like a real decode-failure rejection rather than "nothing ran." This
exact confusion cost significant debugging time during this round and is
worth flagging explicitly so it isn't rediscovered.

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
is what made the Phase 1 audit actionable: the same evidence (which
capability, which adapter, which entry point) fed both the external
capability-compatibility matrix and the internal duplication matrix.

**Status of this recommendation, updated as the roadmap executed**: the
Phase 1 audit (all adapters), Phase 2 design (REST, SSE, events/reqreply
families), and Phase 3 proof-of-concept (all 7 cases + the case-6
follow-up) described above are now complete — see "Phase 1 audit results",
"Phase 2 — Specify the seam", and "Phase 3 — Proof of concept" for the full
findings. Those findings confirmed this recommendation held: the existing
registered handles work as the binding contract for events/reqreply as-is,
REST needed (and received) a smaller shared dispatch-core view
(`adapters/internal/restdispatch`), and a shared dispatch-core proved
mechanically achievable across the reflected entry points within each
adapter (though not across the compile-time-generic/reflected boundary,
confirmed not worth forcing — see Phase 4's per-workstream "3 → 2, not
3 → 1" notes). Phase 4 implementation is now substantially complete for
REST, SSE, and events/reqreply (see "Phase 4 — Implementation" for current
status); Phase 5's conformance-test and documentation-sync work is only
just beginning.

The desired result — a simple experience for API users and an equally
clear, single-implementation extension point for adapter authors, without
coupling declarations to protocols, accumulating unneeded abstraction
layers, or letting one adapter's multiple entry points drift out of sync
with each other again — has been substantially achieved for the REST, SSE,
and events/reqreply families; remaining work is tracked per-phase above.
