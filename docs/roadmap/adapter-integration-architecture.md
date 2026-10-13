# Adapter Integration Architecture: a Unified Roadmap

## Status

**Proposal — architecture investigation and audit plan. No code changes yet.**

This doc replaces and merges two earlier drafts:

- `api-adapter-architecture-roadmap.md` — an external, broad analysis of the
  seam between `api/*` declarations and `adapters/*` (binding/capability
  model), proposing a 5-phase arc through redesign and conformance tests.
- `adapter-dispatch-unification.md` — a narrower, evidence-grounded audit of
  dispatch-path duplication *inside* `adapters/mqtt`, `adapters/mqtt5`, and
  `adapters/zeromq`, deliberately scoped to "audit only, no redesign yet."

Both documents analyze the same underlying pain point — the adapter layer —
from different altitudes. They are not in conflict; one is the general case,
the other a concrete, cited instance of it. This doc merges them into one
coherent plan, broadens the audit to **all** adapters, and sequences the
work explicitly: audit → design → proof-of-concept → implementation →
conformance tests, with no phase skipped.

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
  follow-up round."
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
supports. For example, an event channel may require user properties. MQTT 5
supports MQTT user properties, while MQTT 3 does not provide the same
native feature. A binding that requires this capability should therefore be
rejected for an incompatible adapter before it is activated.

```text
Channel declaration
  requires: UserProperties
       |
       v
  compatibility check
       |
       +---- MQTT 3: unsupported -> clear error
       |
       +---- MQTT 5: supported   -> attach
```

The declaration should express the semantic requirement, not name a
particular adapter. Prefer a protocol-neutral concept such as event user
properties over a requirement that directly names MQTT 5, unless the API is
intentionally exposing a protocol-specific extension.

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
  recovered via reflection).

### Phase 3 — Proof of concept

Exercise the proposed model against representative cases drawn from both
source drafts:

1. A simple REST route bound to an HTTP adapter.
2. An event channel without optional transport features bound to MQTT 3 and
   MQTT 5.
3. An event channel requiring user properties: accepted by MQTT 5 and
   rejected by MQTT 3 with an actionable error.
4. A request/reply contract bound to two transports that genuinely support
   its required semantics.
5. A middleware chain whose behavior remains consistent across compatible
   adapters.
6. Events' Subscribe path specifically (the confirmed 3-implementation
   case) refactored to one shared dispatch-core, verified against all three
   previously-independent entry points.

These examples should reveal whether the current handle is already a good
binding or whether a separate adapter-facing view is justified, and whether
dispatch-core consolidation is mechanically achievable without forcing
artificial uniformity onto genuinely different entry-point shapes.

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
