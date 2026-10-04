# D-0006 — Protocol-Native Capabilities

> **Status: GRADUATED — fully implemented and shipped, now covering ALL
> THREE api packages.** This document was originally
> `docs/roadmap/protocol-native-features.md`; it graduated here once its
> Capability mechanism, spec-rendering, Observer integration, and Handler
> Disposition all shipped (mirroring
> [D-0001](d-0001-rest-middleware-workflow-simplification.md)'s own graduation
> precedent — scoped at the time to `api/events` only). It was then
> REWORKED and EXTENDED, end to end, by
> `docs/design/d-0006-protocol-native-capabilities.md` — which has now
> ITSELF graduated by merging wholesale into §9 below, per the SAME
> convention this document used for its own graduation. This status
> block is rewritten to describe the COMPLETE, current three-tier model
> across `api/events`, `api/reqreply`, AND `api/rest` — not just the
> original events-only slice. Everything below this status block (§0-§8
> original design-round text, §9 the merged roadmap's full phase
> history) is historical reasoning, kept per this repo's convention;
> read this block first for the current, authoritative shipped shape.
>
> ## Shipped shape — the complete three-tier model
>
> **Tier 1 (Explicit) — `api/events` AND `api/reqreply`.** A SEALED,
> per-adapter `Capability` interface (mirrors `ports.Pattern`'s
> technique) — `adapters/mqtt.Capability`, `adapters/mqtt5.Capability`,
> `adapters/zeromq.Capability`, each with its own concrete types
> (`QoS`/`Retained` for mqtt/mqtt5, `HWM`/`Conflate` for zeromq) —
> supplied at DECLARE time via a `Capabilities []<pkg>.Capability` field
> on each adapter's `SubscribeOptions`/`PublishOptions`/`ServeOptions`/
> `CallOptions` struct, attached via `events.Subscriber.WithOptions`/
> `events.Publisher.WithOptions` (events) or each adapter's
> `ServerTransportOptions.Serve`/`ClientTransportOptions.Call` field
> (reqreply) — never a new `Attach`-time parameter, in either api
> package. `events.CapabilityRequirement`/`reqreply.CapabilityRequirement`
> (a `ChannelOpt`/`RouteOpt`, `events`'s renamed from `CapabilitySpec`;
> `reqreply` ships its OWN byte-for-byte-identical-shaped type, no
> cross-api import) declares a requirement for AsyncAPI `x-capabilities`
> rendering; `events.CheckCapabilityCoverage`/`reqreply.
> CheckCapabilityCoverage` (VALUE-aware via the optional
> `LeveledCapability` interface, not just name-matching) runs
> automatically at Serve/Attach time, returning a typed
> `*CapabilityCoverageError` on a mismatch. `stats.CapabilityObserver`/
> `stats.DispositionObserver` — both optional, type-asserted, mirroring
> `SecurityObserver` — are shipped for both api packages.
>
> **Tier 2 (Implicit) — `api/rest`, NEW ground this document never
> originally covered.** `rest.HeaderParam`/`CookieParam`/`QueryParam`
> become GENUINELY runtime-checked capabilities once REST has more than
> one transport family: three REAL, callable interfaces —
> `rest.HeaderCapableTransport` (`ExtractHeaders() map[string]string`),
> `rest.CookieCapableTransport` (`ExtractCookies() map[string]string`),
> `rest.QueryCapableTransport` (`ExtractQuery()`/`ExtractQueryMulti()`) —
> implemented by each adapter's own per-request carrier type
> (`adapters/nethttp`/`chi`'s `httpCarrier`, `adapters/websocket`'s
> `wsCarrier`), constructed fresh per request and used for BOTH the
> Attach-time coverage check (`rest.CheckParamKindCoverage`, scanning
> `RouteHandle.HeaderParamNames()`/`CookieParamNames()`/
> `QueryParamNames()` ALONGSIDE every declared `SecurityScheme`'s `In`
> field via `rest.RequiredParamKinds`) AND the actual per-request
> extraction at dispatch time — promoted from zero-cost, no-op PRESENCE
> markers to real, callable methods doing the actual work.
> `rest.UnsupportedParamKindError` fires at attach time for a param kind
> a transport genuinely can't carry (e.g. a future ZeroMQ REQ/REP
> adapter deliberately omitting `CookieCapableTransport`).
>
> **Tier 3a (adapter capability VALUES)** — the concrete sealed types
> implementing Tier 1's interfaces: `mqtt5.QoS`/`Retained`,
> `zeromq.HWM`/`Conflate`, `mqtt.QoS`/`Retained` (v3, brought to full
> parity with mqtt5/zeromq) — never a shared cross-adapter value type,
> by design (see "Design goal" below).
>
> **Attach-factory redesign (foundational, all 3 api packages).** Every
> adapter exposes `New<Kind>Transport(opts <Kind>TransportOptions)
> <Interface>` factories — `mqtt5.NewTransport`/`NewServerTransport`/
> `NewClientTransport`, `nethttp.NewServerTransport`/
> `NewClientTransport`, etc. — constructing a fully-configured,
> attachable value; attaching it is EXCLUSIVELY `events.Client.Attach`/
> `rest.Client.Attach`/`rest.Server.Attach`/`reqreply.Client.Attach`/
> `reqreply.Server.Attach`'s job. All 12 adapter-namespaced
> `Attach`/`AttachMux`/`AttachRouter`/`AttachServer`/`AttachClient`/
> `AttachRouterServer`/`AttachDealerClient` convenience functions across
> every adapter were REMOVED entirely (breaking) — there is no
> adapter-namespaced Attach function anywhere in this codebase anymore.
>
> **ErrorPattern + Observer as interface-level concerns.** REST's
> `ErrorResponseWriter` interface (+ `rest.PendingCookie`/
> `DispatchErrorResponse`/`CallDispatchErrorResponse`) moved the
> declared-error-response WRITE step onto an api-layer-owned interface,
> deleting 2 of 4 remaining adapter-owned ErrorPattern write functions
> outright. Observer outcome-recording
> (`RecordRequest`/`RecordPublish`/`RecordSubscribe`) was formally
> evaluated and CLOSED as correctly adapter-owned — only the adapter's
> own transport dispatch has the real per-protocol status/duration data;
> see `docs/features/observer.md`.
>
> Handler Disposition (`middleware.Disposition`/`EnsureDispositionBox`/
> `SetDisposition`/`DispositionFromContext`/`ResolveDisposition`) is
> shipped in `middleware` (not `api/events`, so `api/reqreply` reuses it
> with no `api/events` dependency), wired into all 3 event adapters and
> both reqreply adapters (`mqtt5`, `zeromq`) — each currently resolves to
> a no-op-equivalent default (no adapter has a real ack/nack protocol
> yet), proving the plumbing end-to-end for a future ack-capable adapter
> (AMQP) to consume without further core changes.
>
> See §9 below for the full phase-by-phase implementation history (the
> merged roadmap doc) and `docs/concepts/ports-and-adapters.md`'s
> "Interface inventory" section for the durable, reference-style version
> of which adapter-side functions implement a real, declared interface
> vs. which are deliberately ad-hoc.
>
> ## Design goal: zero backdoor between the api layer and the adapters
>
> `Capability`/`Apply`/`events.ApplyCapabilities` is the SOLE mechanism
> for every protocol-native concern this document covers — there is no
> parallel declaration path, and none is ever acceptable going forward.
> This was NOT true from the start: this mechanism originally shipped
> ADDITIVE, alongside a pre-existing `QoS byte`/`Retained bool`/
> `api/events/mqtt_qos.go` legacy field path, on the theory that the two
> could coexist. §9 below's Phase 4b (merged from the former roadmap
> doc) audited every real adapter for exactly this coexistence and
> found **3 genuine backdoors already shipped** — code paths that bypassed
> `Capability`/`Apply` entirely and set protocol-native wire state
> directly — proving "additive" quietly reintroduces the same
> uncontrolled-adapter-surface problem §0 below identifies as the core
> motivation for this whole document. That Phase 4b deleted
> `api/events/mqtt_qos.go` entirely and removed every legacy
> positional/field path from `adapters/mqtt5`, closing all 3. This rule
> is now this document's own first-class design goal, not merely a
> cross-reference: **any future capability, on any adapter, MUST go
> through `Capability`/`Apply` — a "kept for compatibility" parallel path
> is the exact failure mode already caught and fixed once.**
> `adapters/mqtt` v3's own un-migrated legacy path and `adapters/zeromq`
> (which never had a legacy dual-path) are unaffected by this history —
> the backdoor was specific to `adapters/mqtt5`/core `api/events`.
>
> ## Other resolved follow-ons (condensed)
>
> - **`Address`/`Channel[Addr,T]` retrofit — deferred, not abandoned.**
>   `events.Address`/`events.TopicAddress` shipped as standalone, additive
>   types (§2.3/§5.3); the full `Channel[Addr Address, T any]` generic
>   retrofit collides with the already-shipped
>   `NewChannelFromTopic[T any](topic Topic, ...)` symbol and has zero
>   real consumer until a future AMQP adapter needs a non-topic-string
>   address — a fresh naming survey is required before attempting it.
> - **Relationship to [D-0003](../design/d-0003-codec-declared-middlewares.md)
>   — resolved.** `Middleware[In,Out]` and `Capability` occupy the same
>   lifecycle stage (declare-time, spec-contributing) per a confirmed
>   4-stage model, without merging into one Go type.
> - **MQTT5 User Property merge gap — resolved as a bug fix, not a new
>   mechanism.** The property vocabulary axis
>   (`WithRequestProperty`/`WithSubscribeProperty`/etc., built on
>   D-0003's `Middleware[In,Out]`) already covers this; the actual
>   blocker was a merge-field registration bug in
>   `MergedPropertyParam[T].applyChannel`/`applyRoute`, now fixed — see
>   [Feature: Event Channels](../features/events.md#codec-backed-middleware-subscribemwpublishmw).
> - **Response Topic/Correlation Data — decided, closed.** Stays an
>   IMPLICIT, always-on characteristic of `mqtt5`'s reqreply transport
>   (every route needs it unconditionally, no opt-out scenario to gate),
>   NOT a declared `Capability` — see
>   [D-0004](../design/d-0004-reqreply-workflow-simplification.md)'s own
>   "Relationship to `protocol-native-features.md`" section.
> - **MQTT5 Message Expiry Interval / Shared Subscriptions — spun out.**
>   Both genuine, never-implemented `Capability` candidates from this
>   document's own survey (§6) are now designed in their own dedicated
>   doc, [`docs/roadmap/mqtt5-capability-extensions.md`](../roadmap/mqtt5-capability-extensions.md) —
>   Message Expiry is ready to implement; Shared Subscriptions carries a
>   genuine open design decision (topic-filter-string composition, not a
>   `WireAttributes` field) not resolved here.
>
> [← Back to Design Documents](index.md)

## 0. Motivation — are we actually hitting unsolvable constraints, or just discomfort?

This doc's original scope (kept below in §1 as prior art, not deleted) started from
a narrow, concrete pain point: MQTT5's User Properties, Shared Subscriptions, and
Message Expiry have no clean declaration surface, since — unlike REST, which is
always HTTP — pub/sub spans THREE incompatible transports within ONE `api/events`
pattern. Revisiting this with a wider lens surfaces THREE escalating pieces of
evidence that the constraint is structural, not cosmetic:

1. **`api/events/mqtt_qos.go` is transport-specific code living in a
   transport-agnostic package, confirmed via code.** `MQTTQoS`/
   `PublishAttributes{QoS, Retained}` are MQTT-family concepts (shared by `mqtt` v3
   and `mqtt5`, both confirmed via `adapters/mqtt`/`adapters/mqtt5` consuming them
   as their publish/subscribe FALLBACK default) with **zero** ZeroMQ equivalent —
   confirmed via `grep`, `adapters/zeromq` never references `MQTTQoS`/
   `PublishAttributes` at all. This already violates `api/events`'s own
   transport-agnostic charter for the THREE adapters it ships TODAY; it gets
   categorically worse the moment a 4th event transport with a genuinely different
   reliability model (AMQP's exchange-type/ack modes, Kafka's partition/offset
   semantics) needs to plug in — there is no natural home for THOSE concepts inside
   `api/events` either, and inventing a new "protocol-specific file living in the
   agnostic package" each time does not scale.
2. **MQTT5-only capabilities (User Properties, Message Expiry, Shared
   Subscriptions, Response Topic/Correlation Data) have no clean declaration
   surface today** — this doc's own original finding (kept in §5.2/§5.6); a
   now-retired sibling roadmap doc (`mqtt5-user-property-merge.md`)
   independently confirmed the SAME gap from the merge-field angle, though
   its own motivating case turned out to be a fixable BUG in already-shipped
   code rather than a missing feature (see §5.2 below).
3. **The deepest issue, not previously named in any doc: `events.NewChannel(topic
   string, codec, opts...)` (confirmed via `api/events/builder.go`) hardcodes a
   FLAT TOPIC STRING as pub/sub's universal addressing primitive.** This fits
   MQTT/ZeroMQ (both genuinely topic-shaped protocols) but does NOT fit AMQP
   (exchange + routing key + queue — a fundamentally different ADDRESSING MODEL,
   not merely a missing per-message property) or Kafka (topic + partition key).
   Routing AMQP through today's `Topic string` constructor would require encoding
   exchange/routing-key/queue into one opaque string — precisely the kind of leaky
   workaround this whole rethink exists to eliminate, not a solution to it.

**Conclusion: these are real, structural limits — not a matter of adding one more
escape hatch.** The fix explored below is NOT "bolt a fourth
`ProtocolFeature`-shaped mechanism onto the existing `RouteOpt`/`ChannelOpt`/
`Pattern`/`Declaration[In,Out]` set" — it is "identify the ONE unifying primitive
underneath routes/channels/files/params/security/QoS/addressing, and rebuild the
declaration surface around it, deliberately allowing breaking changes to
already-shipped mechanisms where the new model is genuinely cleaner."

## 1. Prior art already IN this codebase — the seed of the answer

Four mechanisms already do a NARROWER version of "declare something, then have
something else validate/fulfill it or reject" — confirmed via code, not
aspirational:

- **`ports.Pattern`** (`ports/pattern.go`) — a SEALED interface
  (`interface{ isPortPattern() }`, unexported marker method, satisfiable only from
  inside package `ports`) with named implementations (`RESTPattern`/
  `EventPattern`/`ReqReplyPattern`/`MCPPattern`/`FilePattern`/`SQLPattern`/
  `CachePattern`/`SocketPattern`/`LLMPattern`), each carrying pattern-specific
  fields, resolved by a port's own `Register`/build call. This is close to a
  "communication pattern selector" — but it selects the WHOLE pattern in one
  value, and — critically — it is CLOSED: no package outside `ports` can ever
  define a new `Pattern` implementation, by design.
- **`rest.RouteOpt`/`events.ChannelOpt`** (`interface{ applyRoute(*routeBuilder) }`/
  `interface{ applyChannel(*channelBuilder) }`, confirmed via
  `api/rest/builder.go:476`/`api/events/builder.go:505`) — every declarable
  building block (`PathParam`, `QueryParam`, `HeaderParam`, `CookieParam`,
  `RouteMeta`, `ErrorPattern`, format declarations, `TopicParam`,
  `Subscribe`/`Publish`) is ALREADY a value implementing a common, per-package
  sealed interface, accumulated into a builder via a variadic parameter. **This
  is already most of the "tick a box" model this rethink is chasing** — the
  missing piece is that it is sealed to `api/rest`/`api/events` THEMSELVES: an
  adapter package (`adapters/mqtt5`, a hypothetical `adapters/amqp`) cannot define
  a new `RouteOpt`/`ChannelOpt`, and there is no mechanism for an adapter to
  declare "I do/don't support opt X" — every declared opt is UNCONDITIONALLY
  assumed compatible with whichever adapter eventually binds, which works only
  because REST has exactly one transport (HTTP) and `ChannelOpt` was designed
  assuming MQTT/ZeroMQ's shared topic model.
- **`middleware.RouteMiddleware`** (`middleware/middleware.go:70`,
  `interface{ RouteMiddlewareMarker() }`) — unlike `ports.Pattern`, this marker
  method is EXPORTED, deliberately, specifically so a type declared OUTSIDE
  package `middleware` (`rest.Middleware[In,Out]`, `events.Middleware[In,Out]`,
  per d-0003) can still satisfy it — an OPEN, cross-package-extensible marker.
  **An earlier round of this doc considered this the closer prior art and
  reused its technique directly** (an open `Feature` interface matched by a
  string ID) — that approach was SUPERSEDED (§2's historical note) precisely
  because openness-via-exported-marker cannot reject a MISMATCHED capability
  at compile time: Go's structural typing means ANY value satisfying an open,
  exported marker compiles wherever that marker is accepted, regardless of
  which package defined it. `ports.Pattern`'s CLOSED technique (below) is what
  §2.1 actually reuses instead.
- **This doc's own original `ProtocolFeature` sketch** (kept in §5's worked
  examples below) — the existing attempt to patch the SECOND gap (adapter-declared
  capability support) for pub/sub specifically, via a parallel sealed interface +
  `.WithFeature()` + adapter-side type-switch + a dedicated
  `UnsupportedProtocolFeatureError`. Confirmed workable in isolation, but
  deliberately scoped narrow, and left its own generalization explicitly
  unresolved.

**The rethink's job, stated precisely: unify `RouteOpt`/`ChannelOpt`/
`ProtocolFeature`/`Pattern` into ONE mechanism achieving what NONE of them
achieve alone — adapter-extensibility (any adapter package can define new
capabilities, unlike `RouteOpt`/`ChannelOpt`/`Pattern`'s current per-core-package
sealing) WITHOUT sacrificing compile-time safety (unlike an open,
cross-package-satisfiable marker interface, which cannot reject a mismatch
until runtime).** §2 resolves this by applying `ports.Pattern`'s OWN sealed
technique PER ADAPTER instead of per-core-package — the seed of the answer
really was already in this codebase, just not yet applied at the right
granularity.

## 2. The candidate unifying primitive: sealed, per-adapter capability interfaces

**Historical note:** an earlier round of this doc explored an OPEN
`Feature`/`Provider` primitive — a single, cross-package `Feature` interface
matched by a string ID, checked at bind time via
`Provider.Supports(id) bool`. That approach is REJECTED: it can only reject a
mismatch at RUNTIME (a string comparison), never at compile time — a step
back from the compile-time exhaustiveness `RouteOpt`/`ChannelOpt`'s sealed,
per-package interfaces already give today. Compile-time safety is a
non-negotiable requirement for this redesign, not a trade-off to accept.
The mechanism below replaces that approach entirely.

### 2.1 Core shape — sealed per-adapter capability, mirroring `ports.Pattern`'s own proven technique

```go
// package mqtt5 (an EXISTING adapter package — no new shared package
// needed at all). Capability is SEALED to this package, via an unexported
// marker method — the EXACT SAME technique ports.Pattern already uses
// (ports/pattern.go: `type Pattern interface{ isPortPattern() }`), applied
// here PER ADAPTER instead of per-port-kind.
type Capability interface{ isMQTT5Capability() }

// Concrete capabilities are plain, adapter-owned types — no shared
// vocabulary, no string ID, no core-package change ever required to add
// a new one.
type QoS byte

func (QoS) isMQTT5Capability() {}

const (
    QoSAtMostOnce  QoS = 0
    QoSAtLeastOnce QoS = 1
    QoSExactlyOnce QoS = 2
)

// UserProperty[In] carries an embedded middleware.Declaration[In, struct{}]
// for its merge-capable half (ALREADY-SHIPPED codec-backed vocabulary,
// reused unchanged — see §5.2) — a capability MAY optionally carry
// structured, codec-backed data; a pure protocol toggle (like QoS above)
// needs none.
type UserProperty[In any] struct {
    middleware.Declaration[In, struct{}]
    mergeFields []codex.FieldCodec[In]
}

func (UserProperty[In]) isMQTT5Capability() {}
```

Because `isMQTT5Capability()` is UNEXPORTED and declared inside package
`mqtt5`, Go's own visibility rules make this SEALED exactly the way
`ports.Pattern` already is: a type declared in ANY OTHER package — including
another adapter's own capability type, e.g. `zeromq.Conflate` — structurally
CANNOT satisfy `mqtt5.Capability`, regardless of shape, because it can never
implement an unexported method belonging to a different package. This is a
closed, Go-compiler-enforced guarantee, not a convention callers must
remember to respect.

### 2.2 Capabilities are supplied at declare time via `SubscribeOptions`/`PublishOptions` — not baked into a channel's own declared type

**REVISED — see §7's Review-13 for the full resolution history.** An
earlier round of this section sketched capabilities as a NEW `Attach`
function parameter (`func Attach[T any](client *Client, ch
events.Channel[T], caps ...Capability) error`); tracing the ACTUAL shipped
`Subscriber[T]`/`Publisher[T]`/`ChannelHandle[T]` machinery found this
ALREADY has an answer, via the existing `WithOptions`/`HandlerOpts`
mechanism:

```go
// package mqtt5
type SubscribeOptions struct {
    // ... existing fields (TopicFilter, QoS, OnError, Observer, UserPropertyParams) ...

    // Capabilities supplies sealed, compile-time-checked protocol-native
    // declarations (QoS, Retained, UserProperty, ...) for this channel —
    // the RECOMMENDED path going forward, alongside the pre-existing QoS/
    // Retained fields (kept, not deprecated — an escape hatch for the
    // common single-value case).
    Capabilities []Capability
}
```

A caller wanting QoS or User Property support supplies it HERE, attached
via the ALREADY-SHIPPED `Subscriber[T].WithOptions(mqtt5.SubscribeOptions{
Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce}})` — NOT at
`events.NewChannel(...)` (which stays exactly as it is today, fully
protocol-agnostic, zero adapter import required). A `zeromq`-defined
capability inside `mqtt5.SubscribeOptions.Capabilities` is a **Go COMPILE
ERROR** — the value simply does not satisfy the field's
`[]mqtt5.Capability` element type — with NO custom error type needed at
all (the compiler's own diagnostic IS the error), and NO
`Provider`/`Supports`/boolean check anywhere in the design. This is the
concrete mechanism realizing the "tick a box" model from your own framing:
an MQTT v3 client simply has no `mqtt.Capability`-satisfying type for User
Properties to begin with — there is nothing to "not tick," the capability
literally cannot be constructed against that adapter.

The SAME `events.Channel[T]` value stays attachable to MULTIPLE adapters,
each supplying its own capabilities (or none) via its OWN
`Subscriber[T]`/`Publisher[T]` built from the SAME underlying channel —
preserving "declare once" more cleanly than an earlier (now-superseded)
proposal in this doc that considered adapter-specific DERIVED WRAPPER
TYPES requiring a throwaway value per adapter.

### Why adapter-owned capability declaration doesn't violate the thin-adapter, protocol-agnostic-declaration principle

> This section's two-part test was later reapplied in the MIRROR-IMAGE
> direction by a since-completed "Thin Adapters Audit" — instead of
> asking "does a NEW protocol-native capability clear the bar for
> core-layer, protocol-agnostic declaration" (this section's question),
> that audit asked "does EXISTING adapter-owned dispatch logic ALREADY
> clear that bar, unnoticed, and is therefore misplaced today." It
> reused the exact two-part test below, confirmed 4 concrete findings
> (moving duplicated middleware/security dispatch logic from
> `adapters/{mqtt,mqtt5,zeromq,nethttp,chi}` into `api/events`/
> `api/reqreply`/`api/rest`/a new `adapters/internal/httpsecurity`), and
> has since SHIPPED — see `docs/concepts/ports-and-adapters.md`'s
> "Guardrail: adapters as pure protocol shims" section for the resulting
> permanent rule.

This codebase's guiding architecture is: routes/channels/ports are declared
ONCE, protocol-agnostically (typically in a shared `domain`/`contract`
package with zero adapter imports); adapters stay THIN, wired in LATER
(typically in `main.go`), providing only the concrete IO binding. Does
requiring `import "adapters/mqtt5"` to supply `mqtt5.QoS`/
`mqtt5.UserProperty(...)` violate this?

**Correction made this round: an earlier draft of this section overstated
its case, claiming "every capability surveyed has NO protocol-agnostic
meaning."** That is not quite right — QoS/delivery-guarantee level and
MQTT5 User Properties/AMQP message headers DO have recognizable
cross-protocol semantic categories (they are standard vocabulary in
distributed messaging, not MQTT-specific jargon invented for this doc). The
PRECISE reason these capabilities still stay adapter-owned/sealed is a
**two-part test**, not "no shared meaning at all":

1. **Compatible mechanical shape** — is the DECLARATION/enforcement shape
   uniform enough across every adapter that supports the capability at all
   (not a lossy mapping, not a compound-vs-single-field mismatch)?
2. **Uniform-enough support** — does close to every relevant adapter
   support SOME form of the capability, so "attach without it" isn't the
   common case a shared type would need to gracefully degrade for?

**A capability needs to clear BOTH bars to deserve a shared, protocol-
agnostic, core-layer declaration** (the way `Security` does) — failing
EITHER bar alone is sufficient to disqualify it, and they fail
INDEPENDENTLY for different capabilities (worked through in detail in
§5.1/§5.2/§5.6's own worked examples, not repeated here):

- **QoS/delivery-guarantee** fails bar 1: AMQP's version is a COMPOUND of
  delivery-mode + consumer ack-mode (no native "exactly-once" at all,
  unlike MQTT's single enum value) — see §5.1.
- **MQTT5 User Properties/AMQP message headers** actually PASSES bar 1
  (both are essentially string-keyed maps) but fails bar 2: MQTT v3 and
  ZeroMQ have no equivalent mechanism at all — see §5.2. This is the case
  that ISOLATES bar 2 as an independent disqualifier, distinct from QoS's
  bar-1 failure.
- **Retained message** passes bar 1 fully (IDENTICAL mechanism between MQTT
  v3/v5) but fails bar 2 even more narrowly (MQTT-family only) — see §5.1's
  closing note. Confirms the "keep separate sealed types even for identical
  mechanisms" choice holds regardless of HOW WELL bar 1 passes, once bar 2
  fails.
- **AMQP Exchange/RoutingKey/Queue addressing, ack-mode specifics** fail
  BOTH bars trivially — no shared category exists at all.

Requiring the adapter import to supply any of these is the SAME honest
coupling `rest.HeaderParam` already has with `api/rest` today — HTTP-shaped
params have no meaning outside HTTP, and nobody considers that a
thin-adapter violation; the same logic extends to a capability that DOES
have cross-protocol meaning but fails the two-part test's shape or support
bar. **`Security` is the one surveyed capability that clears BOTH bars** —
uniform shape (scheme + scopes + credential, same declaration everywhere)
AND uniform-enough support (every adapter can enforce or at least document
a security requirement) — which is precisely WHY it correctly stays
declared in the protocol-agnostic `middleware`/`api/events` core today,
unaffected by anything in this section (whether/how it folds into this
mechanism is §3's explicitly open question, not decided here). If a
genuinely cross-protocol capability that clears BOTH bars is ever
identified beyond Security, it would follow the SAME core-layer treatment
— not designed further here.

### 2.3 The addressing-model problem — RESOLVED this round via a second throwaway Go prototype

**History:** an earlier draft of this section claimed the `Address` sketch
already achieved the SAME compile-time guarantee as §2.1/§2.2's sealed
`Capability` mechanism; a later round corrected this (the sketch used a
plain OPEN interface, `AddressID() string`, which does NOT reject a
mismatch at compile time). That correction is now SUPERSEDED by an actual
resolution, proven via a real, throwaway Go prototype (same discipline as
§7's "Go generics feasibility" spike — compiled and run, not just reasoned
about; deleted after the finding below was extracted):

- **(a) `events.Address` becomes its own open interface type** —
  `TopicAddress{Topic string}` (what MQTT/ZeroMQ implicitly use today) and a NEW
  `amqp.Address{Exchange, RoutingKey, Queue string}` both implement it;
  `NewChannel` accepts an `Address` instead of a bare `string`. AMQP stays inside
  `api/events`, sharing Security/params/capability-declaration machinery; it
  gains only its own address shape.
- **(b) AMQP becomes its own top-level communication pattern** (e.g. `api/queue`),
  mirroring how `api/reqreply` is ALREADY separate from `api/events` today
  despite conceptual overlap (confirmed: `api/reqreply` does not embed or extend
  `api/events.Channel` — it is its own package with its own `Route`/builder). This
  trades a small amount of duplicated builder/Security/capability plumbing for
  a completely clean addressing model per pattern, with no interface abstraction
  needed at all for the address shape itself.

**Decision (§5.3 walks the reasoning in full): (a), NOT (b), for AMQP
specifically.** AMQP is still fundamentally "publish a message to a named
destination, subscribe to receive messages routed to you" — the SAME
request/response-free, fire-and-forget SHAPE `api/events.Channel[T]` already
models (unlike `api/reqreply`, whose correlation/reply-matching workflow is
genuinely a DIFFERENT shape, justifying its existing separateness). Only the
ADDRESS differs, not the surrounding contract (Security, capabilities, codec,
merge fields, spec generation). Reserve (b) — a wholly separate top-level
pattern — for a FUTURE transport that is not just differently-addressed but
differently-SHAPED (e.g. a genuinely stream/partition-consumer model like
Kafka's own consumer-group semantics, which do not cleanly reduce to "subscribe
to one destination, get individually-addressed messages").

**The compile-time-safety resolution for (a), confirmed via the prototype
(full reasoning and evidence in §5.3):** `events.Channel[T]` gains a SECOND
type parameter, `Addr`, constrained to an `Address` interface that requires
ONE real method — `Template() string` — not a bare marker. **Implementation
status (see §7's "Blocking discovery... RESOLVED this round" note):** the
`Address`/`TopicAddress` TYPES below have SHIPPED, as standalone, additive
types — the retrofit of `Channel[T]` itself into `Channel[Addr, T]` (and the
~300-call-site migration that would require) is DEFERRED until a real
Address-needing adapter (AMQP) exists to consume it. The pseudocode below
therefore still describes the FULL, not-yet-built target shape:

```go
// package events
type Address interface{ Template() string }
type TopicAddress struct{ Topic string }
func (a TopicAddress) Template() string { return a.Topic }

type Channel[Addr Address, T any] struct{ /* ... */ }
func NewChannel[Addr Address, T any](addr Addr, codec codex.Codec[T], opts ...ChannelOpt) Channel[Addr, T]
```

Compile-time safety comes from ordinary Go type EQUALITY, not from sealing
`Address` per adapter — `mqtt5.Attach[T any](client *Client, ch
events.Channel[events.TopicAddress, T], caps ...Capability) error` simply
requires the LITERAL concrete type `events.TopicAddress` in its own
signature; `events.Channel[amqp.Address, T]` is a DIFFERENT Go type,
rejected at compile time with zero marker-method boilerplate. This is
SIMPLER than either candidate (a)/(b1) originally sketched in §7 (sealing
`Address` per adapter was UNNECESSARY — requiring the exact concrete type
in `Attach`'s own signature already achieves the identical guarantee).

## 3. Relationship to D-0003 — RESOLVED via a 4-stage lifecycle model, confirmed via a fifth throwaway Go prototype

**Status: RESOLVED**, via a fifth throwaway Go prototype and a proposed
4-stage lifecycle model — see "This round's status" at the end of this
section for the confirmed answer. This section's history, kept for
context: the comparison below was originally written when this doc's own
primitive was the now-SUPERSEDED, OPEN `Feature`/`Provider` sketch (§2's
historical note); that round concluded "Option B" (subsume D-0003). A
LATER round reopened the question (the primitive had changed to the
sealed, per-adapter `Capability` mechanism, and D-0003's
`Middleware[In,Out]` was ALREADY compile-time-safe even before that
pivot, so the original 2-option calculus no longer described the actual
choice). **THIS round resolves it** — not by choosing A or B, but by
recognizing both options were framed around a too-coarse, 2-stage mental
model. The Option A/B analysis below is KEPT as historical record of the
reasoning that led here, not as this doc's current conclusion.

D-0003's `middleware.Declaration[In,Out]` + `rest.Middleware[In,Out]`/
`events.Middleware[In,Out]` (codec-backed, structured Input/Output;
`Transform`/`ClientTransform`/`.Use(mw)` attachment) is ALREADY SHIPPED, reviewed
(`/review-go-codex` round, zero findings), and documented as the current design.
Two paths were weighed (in the earlier, now-superseded round):

**Option A — keep `Feature`/`Provider` and `Declaration[In,Out]` as two
COMPLEMENTARY, independent axes** (this doc's ORIGINAL framing, before this
round): opaque protocol capability flags (`Feature`) vs. structured codec-backed
Input/Output data (`Declaration[In,Out]`) are genuinely different concerns — a
Shared Subscription toggle has no natural "In/Out" shape; a header value
absolutely does. Under this option, NOTHING about d-0003 changes; `Feature`
becomes a NEW, additional mechanism sitting alongside it, used only for the
protocol-toggle cases `Declaration[In,Out]` was never meant to cover.

- *Pro:* zero risk to already-shipped, tested code; the two mechanisms are each
  simpler in isolation, since neither has to accommodate the other's shape.
- *Con:* a caller still has to learn TWO vocabularies (`.Use(Middleware[In,Out])`
  AND `.WithFeature(protocolFeature)`) for what is, from the "declare a
  capability, adapter fulfills or rejects" mental model, the SAME kind of thing.
  This directly contradicts the "clear, simple, declarative workflow" goal this
  rethink is chasing — a REST route's header param, a QoS declaration, and a
  Shared Subscription toggle would remain conceptually unified in the user's head
  but syntactically split in the API, which is exactly the kind of surface-level
  inconsistency this rethink exists to remove.

**Option B — `Feature` SUBSUMES `Declaration[In,Out]`** (the chosen direction):
every `Middleware[In,Out]`-equivalent value becomes ONE MORE `Feature`
implementation — `FeatureID()` returning e.g. `"rest.middleware:<name>"` — whose
struct EMBEDS a `Declaration[In,Out]` for its codec-backed half, exactly the way
`rest.Middleware[In,Out]` already embeds `middleware.Declaration[In,Out]` today
(confirmed via `api/rest/middleware_declaration.go`). Concretely:

```go
// api/rest — Middleware[In,Out] gains ONE new method, otherwise unchanged
// in shape from its current, shipped form:
func (Middleware[In, Out]) FeatureID() string { return "rest.middleware:" + m.Name }
```

- *Pro:* ONE vocabulary, ONE mental model, end to end — a header param, a QoS
  enum, a Shared Subscription toggle, and a codec-backed enrichment middleware
  are ALL `Feature` values, checked against `Provider.Supports` the SAME way,
  erroring with the SAME `UnsupportedFeatureError` shape. This is the option that
  actually delivers on "the user ticks boxes; the API doesn't force two parallel
  systems for what feels like one concept to the user." (Note: §5.5/§5.6 show
  this benefit is weakest for security-shaped concerns specifically — see
  §7's "Review-3" bullet, not resolved further here.)
- *Con, stated explicitly, not minimized:* this REOPENS already-shipped, reviewed,
  tested code — `api/rest/middleware_declaration.go`, `api/rest/transform.go`,
  `api/events/middleware_declaration.go`, `api/events/transform.go`, and every
  adapter's dispatch wiring for both (`adapters/nethttp/serve.go`,
  `adapters/chi/serve.go`, `adapters/{mqtt,mqtt5,zeromq}/transform_dispatch.go`) —
  all independently reviewed clean in the most recent `/review-go-codex` round.
  The TWO attachment styles d-0003 shipped (`Transform`/`ClientTransform`
  route-bound vs. `.Use(mw)` route-agnostic) need an EQUIVALENT under `Feature` —
  likely unchanged in SHAPE (the enrichment-`fn`-needs-`req`-access vs.
  `fn`-is-`Req`-free distinction is orthogonal to whether the carrying type is
  called `Middleware[In,Out]` or `Feature`), just re-homed. This is real
  migration cost, not a free refactor.

**This round's status: RESOLVED, via a fifth throwaway Go prototype and a
proposed 4-stage lifecycle model** (compiled and run, not merely reasoned
about — deleted after this finding was extracted). Neither Option A nor
Option B above is the final answer — both were framed around a
2-stage mental model (declare, then attach) that turned out to be too
coarse. The resolution comes from splitting the lifecycle into FOUR
stages: (1) **declare** — route/channel/port, fully adapter-agnostic; (2)
**capability-declare** — capabilities added, still adapter-agnostic, able
to contribute to spec; (3) **handler-attach** — business logic attached;
(4) **adapter-attach** — the concrete adapter supplied, checked against
stage 2.

**The reframed answer to "does D-0003 fold into `Capability`":** D-0003's
`Middleware[In,Out]` ALREADY occupies stage 2 — declared before
handler/adapter, contributing to spec. Under the 4-stage model, both
`Middleware[In,Out]` (structured, codec-backed I/O data) and `Capability`
(protocol-native toggles) are **stage-2, spec-contributing declarations**
— the SAME kind of thing, differing only in PAYLOAD shape, not in WHEN or
HOW they attach. **They do NOT need to merge into one Go type** — each
keeps its own runtime-enforcement mechanism (`Middleware[In,Out]`:
reflection-based `Transform`/`.Use(mw)` dispatch, unchanged; `Capability`:
sealed, compile-time-checked `Attach`-time supply, unchanged) — but they
are now understood as two INSTANCES of one shared lifecycle STAGE, not
two competing mechanisms one must subsume the other.

**Three candidate Go mechanisms were spiked to make stage 2 → stage 4
concrete, with a clear winner:**

1. **Threaded third type parameter** (`Channel[Addr, T, Caps]`, `Caps`
   carried through `.Use`/`.WithHandler` to `Attach`) — CONFIRMED
   compile-time-safe through all 4 stages, even after an intervening
   handler-attach step (a real cross-adapter mismatch was rejected with
   an actual compiler error). **But CONFIRMED to fail this doc's own
   ergonomics bar**: Go infers `Caps` as the CONCRETE capability type
   supplied (e.g. `mqtt5.QoS`), not the sealed INTERFACE `Attach`
   requires (`mqtt5.Capability`) — so explicit type-parameter brackets
   are REQUIRED at every `.Use(...)` call, even for a single capability,
   confirmed via a failed build attempt before the fix. Heterogeneous
   capability mixes (e.g. `QoS` + `UserProperty` together) need the SAME
   explicit pin. Rejected as the primary mechanism — real ergonomic
   regression from the zero-bracket bar the last four spikes established.
2. **Type-erased storage** (`[]any`, runtime-reasserted at `Attach`) —
   CONFIRMED to sacrifice compile-time safety entirely: a genuine
   cross-adapter mismatch (a `zeromq`-only capability attached via
   `mqtt5.Attach`) **compiled successfully**, caught only via a runtime
   type-switch. Rejected outright — this is exactly the category of
   regression this doc's "we do not compromise on compile-time safety"
   bar exists to prevent.
3. **CONFIRMED WINNER — a decoupled, spec-only sibling value + a
   `CheckCoverage`-style drift-check:** stage 2 (`DeclareCapabilitySpec`)
   returns a SEPARATE, plain value — NOT threaded through the channel's
   own Go type at all. `Attach` (stage 4) is **completely UNCHANGED**
   from the already-proven, zero-bracket mechanism (§2) — capabilities
   still supplied directly, still sealed, still compile-time-checked on
   the axis that matters most (adapter/protocol mismatch). The ONLY new
   piece: an OPTIONAL drift-check —
   `CheckCapabilityCoverage(specs, supplied)` — comparing the stage-2
   spec-declarations against what was ACTUALLY supplied at stage 4 by
   name, mirroring `rest.CheckCoverage`'s existing pattern for Security.
   CONFIRMED via the prototype: a happy-path case (spec matches supplied
   capability) passes; a genuine DRIFT case (a spec declared for
   `mqtt5.user-property` with no matching capability actually supplied)
   is correctly caught with a typed `MissingCapabilityError`. Zero
   brackets needed anywhere in this design — `Channel[Addr,T]` stays
   EXACTLY as simple as today's already-proven shape.

**The honest trade-off, stated plainly:** Candidate 3 achieves DECOUPLING
(stage 2 and stage 4 are independent calls, and stage 2 alone is enough
to drive spec-rendering — resolving §7's "spec rendering plan" gap) but
NOT full type-level LINKING between them — a caller could still forget
the drift-check itself (it is an opt-in call, not automatic), unlike
`Attach`'s own protocol-mismatch check, which the compiler enforces
unconditionally. This is a DIFFERENT, weaker guarantee than what
`Capability`/`Attach` alone already provides for protocol-mismatch — but
it is the SAME class of guarantee `rest.CheckCoverage` already accepts
for Security (a coverage check the caller must actually invoke, not a
compiler-enforced one), so it is a precedented trade-off, not a novel
compromise.

**Resolved recommendation for a future implementation round:** adopt
Candidate 3's shape — `Middleware[In,Out]` and a NEW, decoupled
`CapabilitySpec`-style declaration BOTH live at stage 2, sharing NOTHING
at the Go-type level (no forced common interface), but conceptually
unified as "declare-time, spec-contributing" in this doc's/`docs/concepts/
declaring-apis-and-ports.md`'s own vocabulary; `Capability`'s existing
`Attach`-time mechanism (§2) is UNCHANGED and remains the sole
compile-time enforcement point; a `CheckCapabilityCoverage`-style
drift-check is ADDED as an opt-in safety net, not a compiler guarantee.
Exact package placement/naming for `CapabilitySpec` and precise
wiring into `rest`/`events`' existing spec-generation code is NOT
designed further here — flagged for the dedicated implementation-planning
round §7 already calls for.

## 4. What this is expected to simplify — validated against worked examples (§5), not merely asserted

| Today | Under sealed, per-adapter `Capability` interfaces |
|---|---|
| `RouteOpt`/`ChannelOpt`/`ports.Pattern` (each sealed to ITS OWN package) vs. no mechanism at all for adapter-owned protocol-native capabilities | Every adapter gets the SAME `ports.Pattern`-style sealed-interface technique, applied to ITS OWN capabilities — no new shared package, no core-package change ever needed to add one |
| `api/events/mqtt_qos.go` (MQTT-specific, lives in the transport-agnostic package) | `mqtt.QoS`/`mqtt5.QoS` (adapter-OWNED, sealed `Capability` types) — `api/events` itself has ZERO MQTT-specific code |
| A capability mismatch (e.g. attaching mqtt5-only User Properties to an mqtt v3 client) is either silently ignored (today, by convention) or, in the previous round's `Feature`/`Provider` sketch, a RUNTIME string-comparison failure | A capability mismatch is a **Go COMPILE ERROR** — the mismatched value simply does not satisfy the accepting `Attach` function's `caps ...Capability` parameter type; no custom error type needed at all |
| Adding AMQP requires either warping `Topic string` into an opaque encoding, or accepting an awkward, incomplete fit | AMQP gets its own `Address` shape (§2.3, already compile-time-checked) and its OWN sealed capabilities (`Exchange`, `RoutingKey`, ack-mode) — zero changes to MQTT/ZeroMQ code |
| A new adapter capability (e.g. a 4th MQTT5-only property) requires waiting for a NEW roadmap-doc-sanctioned mechanism extension each time | A new adapter defines a new sealed capability type in its OWN existing package — no core-package change, no shared vocabulary to extend |
| Declaring a channel/route stays adapter-agnostic today (a real strength) | UNCHANGED — capabilities are supplied at `Attach`/bind time, not baked into the channel/route's own declared type; `events.NewChannel(...)` needs zero adapter imports for the common case, exactly as today |

## 5. Worked examples

### 5.1 MQTT QoS — shared by two-of-three adapters (the simplest case)

Today (`api/events/mqtt_qos.go`, confirmed via code): `events.MQTTQoS`/
`Subscribe.QoS`/`events.PublishAttributes{QoS, Retained}` live in `api/events`
itself — MQTT-specific concepts inside the transport-agnostic package,
consumed by `adapters/mqtt`/`adapters/mqtt5` as a fallback default, silently
IGNORED by `adapters/zeromq` (no code ever reads them there — not an error, just
dead weight for that adapter).

Under the sealed `Capability` mechanism (§2):

```go
// adapters/mqtt — sealed to this package (mirrors ports.Pattern exactly).
type Capability interface{ isMQTTCapability() }

type QoS byte
func (QoS) isMQTTCapability() {}
const (QoSAtMostOnce QoS = 0; QoSAtLeastOnce QoS = 1; QoSExactlyOnce QoS = 2)

// Capabilities supplied via the EXISTING SubscribeOptions/PublishOptions +
// WithOptions/HandlerOpts mechanism (§2.2/§7 Review-13) — no new Attach
// parameter needed.
type SubscribeOptions struct {
    // ... existing fields ...
    Capabilities []Capability
}

// adapters/mqtt5 — a SEPARATE sealed Capability + QoS type, deliberately
// NOT shared with adapters/mqtt's own (even though the numeric values are
// identical, 0/1/2) — a future divergence between the two protocols' QoS
// semantics would not require touching a shared type at all, and the
// sealing itself already prevents cross-adapter mixups regardless.
type Capability interface{ isMQTT5Capability() }

type QoS byte
func (QoS) isMQTT5Capability() {}
const (QoSAtMostOnce QoS = 0; QoSAtLeastOnce QoS = 1; QoSExactlyOnce QoS = 2)

type SubscribeOptions struct {
    // ... existing fields ...
    Capabilities []Capability
}
```

`api/events` itself carries NOTHING NEW mqtt-specific — `mqtt_qos.go`'s
`MQTTQoS`/`PublishAttributes` stay as an ADDITIVE legacy path (§7 Review-13's
resolution: no breaking change to existing fields with no correctness
problem), with `Capabilities` as the new, RECOMMENDED, sealed path. A
caller declares the base channel exactly as today (`events.NewChannel(topic,
codec, opts...)`, zero adapter import), then supplies QoS AT DECLARE TIME,
via the existing `WithOptions` mechanism:
`sub.WithOptions(mqtt5.SubscribeOptions{Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce}})`.
Attempting the equivalent with `zeromq.SubscribeOptions{Capabilities:
[]zeromq.Capability{mqtt5.QoSAtLeastOnce}}` **does not compile** —
`mqtt5.QoS` does not implement `zeromq.Capability` (different unexported
marker method, different package) — the Go compiler rejects it before the
program can even be built, let alone run.

**Error timing:** compile time — strictly stronger than an eager runtime
check, and a strict improvement over today's silent no-op for ZeroMQ (which
simply never reads `events.MQTTQoS` at all).

**Why QoS stays adapter-owned despite a shared semantic category — the
two-part test (§2), applied:** "at-most/at-least/exactly-once" IS
recognized cross-protocol vocabulary, so this is NOT a case of "no shared
meaning at all." It fails the two-part test anyway, on BOTH bars
independently:

- **Bar 1 (shape) — FAILS.** AMQP has NO single field matching MQTT's QoS
  enum — its own version would be `amqp.AckMode` (auto-ack vs.
  manual-ack-with-requeue) COMBINED with a SEPARATE `amqp.Persistent`
  delivery-mode bit — two orthogonal settings, not one. There is no native
  "exactly-once" in AMQP 0-9-1 at all (some brokers approximate it via
  extensions, not the base protocol). A shared `events.QoS` enum could not
  represent AMQP's compound configuration without either losing
  expressiveness or forcing AMQP to bolt on ADDITIONAL adapter-specific
  capabilities anyway — at which point the shared type adds nothing.
- **Bar 2 (support) — ALSO FAILS.** ZeroMQ has no ack/retry mechanism at
  the protocol level whatsoever — it cannot support ANY level above
  fire-and-forget. A shared, non-sealed `events.QoS` type would need a
  RUNTIME check to reject ZeroMQ (back to the rejected string-ID approach);
  sealing it to `api/events` itself would mean NO adapter — including MQTT,
  which DOES support it — could ever implement it.

Failing bar 1 ALONE would already disqualify QoS from a shared core type;
failing bar 2 as well only reinforces the same conclusion via an
independent path. Either failure alone is sufficient — see §5.2 for a case
that passes bar 1 but still fails on bar 2 alone.

**Retained message** (MQTT v3 + MQTT5 only, absent from AMQP/ZeroMQ) is a
narrower, CLEANER case worth contrasting explicitly: unlike QoS, Retained
passes bar 1 COMPLETELY — the mechanism is IDENTICAL between the two MQTT
versions (a broker-side flag: cache the last message on a topic, deliver it
to new subscribers). It still fails bar 2 (not supported by AMQP/ZeroMQ at
all), so the SAME per-adapter sealed treatment applies:

```go
// adapters/mqtt and adapters/mqtt5 — TWO separate sealed capability
// types, even though the mechanism is IDENTICAL between them (unlike
// QoS, where mqtt/mqtt5 at least COULD in principle diverge later) —
// sealing per adapter is about compile-time rejection of NON-supporting
// adapters (AMQP, ZeroMQ), not about whether the shape happens to match
// across the adapters that DO support it.
type Retained bool
func (Retained) isMQTTCapability() {}  // adapters/mqtt
func (Retained) isMQTT5Capability() {} // adapters/mqtt5 (separate type)
```

This confirms §5.1's own `QoS` choice (two separate sealed types, not one
shared enum, even where mqtt/mqtt5 are numerically/semantically identical)
was already the right call BEFORE this round's two-part test was
formalized — Retained simply makes the reasoning starker, since there is no
shape argument to make here at all, only the support one.

### 5.2 MQTT5 User Properties — the motivating "adapter simply doesn't tick the box" case

Today (`adapters/mqtt5/adapter.go:71`, confirmed via code): `UserPropertyParam`
is a VALIDATE-ONLY escape hatch, living entirely inside `adapters/mqtt5` (never
`api/events`) — already correctly scoped to the ONE adapter that understands
User Properties. A MERGE-CAPABLE sibling for the adapter-agnostic case DOES
now exist — `events.NewPropertyParam[T,V]`/`reqreply.NewPropertyParam[T,V]`,
attached directly to `NewChannel`/`NewRoute` (see
[Feature: Event Channels](../features/events.md#codec-backed-middleware-subscribemwpublishmw)) —
but there is still no declarative "this channel REQUIRES User Property
support" statement a caller can make at the `api/events.Channel` level for
an mqtt(v3)-specific capability; today, a caller simply never attempts to
bind an mqtt(v3) client to a channel using `UserPropertyParam`, by
convention, not by any enforced rule.

Under the sealed `Capability` mechanism (§2):

```go
// adapters/mqtt5 — a sealed Capability carrying an embedded
// middleware.Declaration for its merge-capable half (ALREADY-SHIPPED
// codec-backed vocabulary, reused unchanged), resolving MQTT5 User
// Property Merge's own "registration surface... NOT resolved" question
// directly.
type UserProperty[In any] struct {
    middleware.Declaration[In, struct{}] // no Out — properties are request-side only, mirrors events.Middleware's subscribe-only asymmetry (d-0003)
    mergeFields []codex.FieldCodec[In]
}

func (UserProperty[In]) isMQTT5Capability() {}
```

Supplied at declare time via `sub.WithOptions(mqtt5.SubscribeOptions{
Capabilities: []mqtt5.Capability{mqtt5.NewUserProperty[AuthIn](...)
.WithMergeField(...)}})` — NOT baked into the channel's own declared type
(§7 Review-13's resolution — no separate `Attach`-time parameter needed).
`adapters/mqtt` (v3) has no `isMQTT5Capability()`-implementing type for User
Properties AT ALL — there is no `mqtt.UserProperty` to construct in the
first place, so the mismatched combination is simply UNWRITABLE, not just
fast-failing. Attempting `mqtt.SubscribeOptions{Capabilities:
[]mqtt.Capability{mqtt5.NewUserProperty[AuthIn](...)}}` **does not
compile** — the value doesn't satisfy `mqtt.Capability`.

**Error timing:** compile time — this is the MOST DIRECT realization of
your own original framing: "the MQTT v3 adapter just not ticks the
box... so there is simply no work around needed in any adapter or api
module" — stronger even than that framing suggested, since there's nothing
to "not tick": the capability literally cannot be constructed against
that adapter.

**Why User Properties stays adapter-owned despite PASSING the shape bar —
the two-part test (§2), applied:** "arbitrary key-value message metadata"
is a genuinely shared semantic category, and — unlike QoS (§5.1) — the
MECHANISM is actually compatible where the capability exists at all: AMQP
0-9-1's `Basic.Properties.headers` is a generic field-table (a key-value
map, richer-typed but structurally the same idea as MQTT5's string-only
User Properties — a strict superset, not an incompatible shape). This
capability therefore **PASSES bar 1**, unlike QoS — but:

- **Bar 2 (support) — FAILS.** MQTT v3's packet format has NO custom-header
  mechanism at all (confirmed: `adapters/mqtt5/adapter.go`'s own
  `UserPropertyParam` has no `adapters/mqtt` equivalent, by protocol
  limitation, not an oversight). ZeroMQ has no message-metadata concept
  either — a ZeroMQ message is just raw frames, no properties table of any
  kind. So even with a COMPATIBLE shape, only 2 of the (at least) 4
  adapters this doc considers (`mqtt5`, `amqp`) would ever implement it —
  "attach without this capability" is the COMMON case, not the exception, a
  shared core type would need to gracefully degrade for.

**This is the case that ISOLATES bar 2 as an INDEPENDENT disqualifier from
bar 1** — User Properties/AMQP headers is the cleanest illustration that
even a capability with a GENUINELY compatible mechanical shape still
correctly stays adapter-owned/sealed once support is uneven enough. A
future `amqp.Header[In]` capability (mirroring `mqtt5.UserProperty[In]`'s
own embedded-`Declaration[In,Out]` shape, per §2.1) would be entirely
plausible under this mechanism — sealed to `adapters/amqp` specifically,
not shared with `mqtt5.UserProperty`, for the SAME bar-2 reason, even
though their underlying shapes are compatible enough that a shared type
was tempting to consider.

#### 5.2.1 A THIRD, ALREADY-SHIPPED answer to this SAME use case — the "property" axis in D-0003's own Addendum

**Documented here as a related use case, NOT a competing design** —
added after the (now-deleted) `reqreply-codec-declared-middleware.md`
roadmap doc's own 19 review rounds confirmed a genuinely
distinct path to the SAME underlying problem this section analyzes
(declaring MQTT5 User Properties / AMQP headers), and now SHIPPED (see
[D-0003](../design/d-0003-codec-declared-middlewares.md)'s own Addendum). That doc adds a NEW vocabulary axis
DIRECTLY on `middleware.Middleware[In,Out]` (already SHIPPED via
D-0003, not a hypothetical) — `WithRequestProperty`/`WithResponseProperty`
for `api/reqreply`, `WithSubscribeProperty`/`WithPublishProperty` for
`api/events` — via a NEW `PropertyParam`/`MergedPropertyParam[T]`/
`NewPropertyParam[T,V]`/`NewOptionalPropertyParam[T,V]` triple, confirmed
to mirror `TopicParam`'s existing wrapper pattern exactly (see
D-0003's own Addendum, and
[Feature: Codec-Declared Middleware](../features/codec-declared-middleware.md)
for the user-facing docs).

**Where this sits relative to THIS doc's `Capability` mechanism —
distinct, not overlapping, by design:**

| | `protocol-native-features.md`'s `Capability` (this doc) | D-0003's Addendum's property axis |
|---|---|---|
| Ownership | ADAPTER-owned (`mqtt5.Capability`, sealed) | API-LEVEL (`api/reqreply`/`api/events`, not adapter-owned) |
| Supplied at | `Attach`/bind time | Declare time, on a `Middleware[In,Out]` value, via `.Use()`/`Transform`/`ClientTransform` |
| Scope | GENERAL primitive — ANY protocol-native declaration (QoS, User Properties, Shared Subscriptions, Message Expiry, ...) | SCOPED specifically to "named metadata separate from payload" (the User-Property/AMQP-header use case only) |
| Status | Idea only — no driver yet, no code written | ✅ SHIPPED — 19 review rounds, 13 implementation phases, verified end-to-end |
| Dependency | Needs this doc's OWN redesign implemented first | Reuses D-0003's ALREADY-SHIPPED `Middleware[In,Out]`/`Transform`/`ClientTransform` machinery directly — no new core mechanism needed |

**Not mutually exclusive** with Phase 1b's `FromUserPropertyParam`
(validate+spec only, unchanged) — this is a THIRD
point on the SAME spectrum: sooner-to-ship, narrower in scope than
`Capability`, but NOT redundant with it. A future `mqtt5.Capability`
could still additionally expose adapter-level concerns (Shared
Subscriptions, Message Expiry, ContentType) the property axis was never
scoped to touch — that doc's `UserProperty[In]` sketch (above, §5.2)
remains a valid, DIFFERENT-ownership-model answer to the SAME narrow
User-Property-merge slice, for whenever `Capability` itself gets
implemented.

### 5.3 A hypothetical AMQP adapter — NOW fully compile-time-safe, address AND capability, confirmed via a real prototype

**History:** this worked example went through THREE states across successive
rounds — (1) an initial claim that address-shape mismatches were already
compile-time-safe (incorrect); (2) a correction acknowledging they were NOT
(the `Address` interface was OPEN, no type-level guarantee); (3) THIS
round's actual resolution, proven via a real, throwaway Go prototype
(compiled and run — the SAME discipline as §7's "Go generics feasibility"
spike), not merely reasoned about. The design below is the CONFIRMED,
tested shape:

```go
// api/events — Address requires ONE real method, Template() — not a bare
// marker like an earlier draft's AddressID() string. Template() names
// whichever field hosts {placeholder} vars, letting the EXISTING shared
// TopicParam/BuildTopic mechanism generalize across address shapes without
// per-Addr-type special-casing (confirmed via the prototype — see below).
type Address interface{ Template() string }

type TopicAddress struct{ Topic string }
func (a TopicAddress) Template() string { return a.Topic }

// adapters/amqp — a NEW, COMPOUND address shape, living entirely in the
// adapter package. Only RoutingKey hosts {placeholder} vars in practice;
// Exchange/Queue are typically fixed and stay OUTSIDE the shared
// var-substitution mechanism entirely — confirmed to work cleanly, not
// merely assumed.
type Address struct{ Exchange, RoutingKey, Queue string }
func (a Address) Template() string { return a.RoutingKey }

// api/events — Channel gains a SECOND type parameter, Addr, constrained to
// Address. NewChannel infers BOTH Addr (from addr's own concrete type) AND
// T (from codec) — CONFIRMED zero explicit type-parameter brackets needed
// at the call site, matching the ergonomics bar the generics spike
// established.
type Channel[Addr Address, T any] struct{ /* ... */ }
func NewChannel[Addr Address, T any](addr Addr, codec codex.Codec[T], opts ...ChannelOpt) Channel[Addr, T]

// NewChannelFromTopic preserves today's ergonomic bare-string call site —
// CONFIRMED working, not just gestured at.
func NewChannelFromTopic[T any](topic string, codec codex.Codec[T], opts ...ChannelOpt) Channel[TopicAddress, T]

// adapters/amqp — capabilities stay sealed, supplied at Attach time,
// exactly like mqtt.QoS/mqtt5.QoS in §5.1 — unaffected by the
// address-parameterization change; both mechanisms compose cleanly.
// UPDATE (§7's "AMQP compound ack+persistence" resolution, confirmed via
// its own prototype): AMQP's delivery-guarantee capabilities split by
// ROLE (unlike MQTT's symmetric QoS), so Attach itself splits too —
// PublishAttach/SubscribeAttach, each accepting only its own role-scoped
// sealed interface. A role-inappropriate capability (e.g. AckMode passed
// to PublishAttach) is a COMPILE ERROR, not a runtime check.
type PublishCapability interface{ isAMQPPublishCapability() }
type SubscribeCapability interface{ isAMQPSubscribeCapability() }

type Persistent bool
func (Persistent) isAMQPPublishCapability() {}

type PublisherConfirms bool
func (PublisherConfirms) isAMQPPublishCapability() {}

// NOTE: declaring AckMode alone does not make manual-ack actually usable —
// see §8 "Handler Disposition" for the separate, dispatch-time mechanism
// a handler needs to signal ack/nack/requeue outcomes; AckMode and
// Disposition are two DISTINCT, complementary mechanisms, not one.
type AckMode byte
func (AckMode) isAMQPSubscribeCapability() {}

// PublishAttach/SubscribeAttach both require the LITERAL concrete type
// amqp.Address in their own signature — not a generic Addr — the SAME
// mechanism §2.3 already established for adapter-correctness, now
// combined with role-scoped capability interfaces for role-correctness.
func PublishAttach[T any](client *Client, ch events.Channel[Address, T], caps ...PublishCapability) error
func SubscribeAttach[T any](client *Client, ch events.Channel[Address, T], caps ...SubscribeCapability) error
```

An AMQP-bound channel declares `events.NewChannel(amqp.Address{Exchange:
"orders", RoutingKey: "created.{region}", Queue: "worker-1"}, codec, ...)`;
an MQTT-bound channel keeps declaring
`events.NewChannel(events.TopicAddress{Topic: "orders/created"}, codec,
...)`, or the CONFIRMED `NewChannelFromTopic("orders/created", codec, ...)`
convenience for the common bare-string case.

**Error timing — CONFIRMED via the prototype, both directions, with actual
compiler output captured:**

```
# amqp.Address passed to an mqtt5-shaped Attach (requires events.TopicAddress):
./main.go:21:27: in call to mqtt5.Attach, type events.Channel[amqp.Address, OrderEvent]
    of amqpCh does not match events.Channel[events.TopicAddress, T] (cannot infer T)

# events.TopicAddress passed to an amqp-shaped Attach (requires amqp.Address):
./main.go:19:26: in call to amqp.Attach, type events.Channel[events.TopicAddress, SensorReading]
    of mqttCh does not match events.Channel[amqp.Address, T] (cannot infer T)
```

Both mismatches are rejected AT COMPILE TIME, symmetrically — the SAME bar
`Capability` mismatches already met (§5.1/§5.2), now also met for
addressing. **This worked example's capability half (`AckMode`) and address
half are BOTH now fully compile-time-safe, confirmed via a real prototype,
not asserted** — the gap this doc carried since an earlier round is closed.

**The topic-var complication, confirmed resolved, not merely assumed away:**
the prototype ran `ch.BuildAddr(vars)` against BOTH a `TopicAddress`-based
channel AND an `amqp.Address`-based channel through the SAME shared
mechanism (operating on `ch.Addr.Template()`), producing correct output for
both: the MQTT case resolved `"sensors/{sensorID}/data"` →
`"sensors/abc-123/data"`; the AMQP case resolved ONLY the `RoutingKey`
segment (`"created.{region}"` → `"created.eu-west"`) while `Exchange`
(`"orders"`) and `Queue` (`"worker-1"`) passed through untouched, exactly as
intended — confirming `TopicParam`'s existing merge-field mechanism
generalizes across address shapes via ONE new interface method
(`Template()`), with no adapter needing its own bespoke var-extraction
logic.

### 5.4 REST header/cookie/query param — confirming REST needs none of this

Today's `rest.HeaderParam`/`MergedHeaderParam[T]`/`NewRequiredHeaderParam[T,V]`
(confirmed via `api/rest/builder.go`) already work exactly as intended — REST
has exactly one transport, so `rest.RouteOpt`'s EXISTING per-package sealing
(`interface{ applyRoute(*routeBuilder) }`, confirmed at
`api/rest/builder.go:476`) ALREADY gives REST the SAME compile-time
exhaustiveness §2's sealed `Capability` mechanism gives multi-adapter
boundaries — there is no adapter-mismatch scenario for REST to guard against
in the first place (only `adapters/nethttp`/`chi` exist, both HTTP). **This
worked example's conclusion, unchanged from the earlier rounds: REST needs NO
new mechanism at all** — its own sealed `RouteOpt` was ALREADY at the "ideal"
compile-time-safety bar §2's `Capability` mechanism now brings to multi-adapter
boundaries (events, and any future REST-shaped-but-multi-transport pattern).

**Error timing:** unchanged from today — REST's spec-layering conflict
detection already runs at `Register`/`ValidateRoute` time via
`applyParamDeclarations`/`checkParamConflicts` (confirmed via
`api/rest/middleware.go`), and REST's single-transport nature means there is
no adapter-capability question left to answer.

### 5.5 Security — the one surveyed case that IS genuinely cross-protocol, and why that matters here

Today's `middleware.SecurityScheme`/`HandleMW`/`ClientMW` (confirmed via
`middleware/middleware.go`) already has its OWN paired declare/implement split
— `Middleware` (declare-time, spec-contributing, protocol-agnostic — no
adapter import needed) separate from `ServerImplementation`/
`ClientImplementation` (register-time, runtime-only, matched by `Satisfies`).

**Security does NOT fit the sealed-`Capability`-at-Attach-time mechanism §2
describes for QoS/User Properties/AMQP addressing — and that's expected, not
a gap.** §2's two-part test (compatible shape + uniform-enough support) is
precisely why: Security is the ONE surveyed capability that CLEARS BOTH
BARS — uniform shape (scheme + scopes + credential, the SAME declaration
everywhere) AND uniform-enough support (every adapter can enforce or at
least document a security requirement). QoS/User Properties/AMQP addressing
each fail at least one bar (§5.1/§5.2's worked analysis), which is WHY they
stay adapter-owned while Security correctly stays declared in the
protocol-agnostic `middleware`/`api/events` core today, with ZERO adapter
import required at declare time. Folding Security into a sealed,
per-adapter `Capability` mechanism would require EITHER (a) each adapter
re-declaring its own copy of the "security scheme" concept (duplicating
what `middleware.SecurityScheme` already expresses once, protocol-
agnostically), or (b) keeping Security's declaration in the core layer while
somehow ALSO satisfying a sealed, adapter-owned interface — a combination
this doc does not attempt to design here.

**§3's confirmed 4-stage model resolves this tension, rather than leaving
it open**: `middleware.Middleware`/D-0003's `Declaration[In,Out]`
mechanism does NOT need to interact with §2's sealed `Capability`
mechanism at the Go-type level at all — Security stays a stage-2,
spec-contributing declaration (exactly what it already is today,
unchanged), while `Capability` occupies the SAME lifecycle stage for
protocol-native toggles, via its own separate, sealed, `Attach`-time
mechanism. See §3 for the full resolution.

**Error timing:** unchanged — `rest.CheckCoverage`'s existing
`Register`/adapter-time check remains the authoritative mechanism for "is
this declared scheme actually implemented," independent of anything in §2.

### 5.6 `ports.File` read/write scope-check — a genuinely open case, not forced to fit

`docs/roadmap/mcp-ports-declarative-middleware.md`'s (formerly
`declarative-middleware.md`'s) UNSHIPPED `ports.File[T]` sketch
(kept there, not duplicated here) proposes a `RequireScopes[T]` decorator
wrapping `Read`/`Write` — a security-shaped `Fn` extracting grants, merged and
checked ONCE via `middleware.CheckScopes`, attached directly at the
`Read`/`Write` call site.

**§2's sealed-`Capability`-supplied-at-declare-time mechanism (§7 Review-13's
resolution) presupposes an existing per-call `Options` struct** (e.g.
`mqtt5.SubscribeOptions.Capabilities`) distinct from the channel/route's own
declaration — `ports.File[T]` has NO equivalent options-carrying phase at
all: its `Read(ctx, vars, opts)`/`Write(ctx, vars, v, opts)` methods ARE the
only call site, and while `opts` exists, it is a `ports`-level, adapter-
agnostic struct — there is no adapter-owned `SubscribeOptions`-equivalent to
add a `Capabilities` field to. Forcing §2's mechanism onto `ports.File`
would require EITHER inventing an adapter-specific options type `ports.File`
doesn't otherwise need, or extending the generic `opts` with a type-erased
capabilities slot — structurally different from every other worked example
in this section.

**Left genuinely open here, not resolved — but no longer undriven.** This
doc does not attempt to force a fit for §2's Attach-time `Capability`
mechanism onto `ports.File`/`Cache`/`SQL`/`Dir`, since they structurally
lack the separate bind/`Attach` step that mechanism requires. That
conclusion stands. What HAS changed: whether these ports need SOME
cross-cutting-concern mechanism at all is no longer an open question
without a driver — the driver is the library's UX North Star
(declarative/simple/consistent workflow), and it is already being
pursued, as its own design, in
[MCP and Ports Declarative Middleware](../roadmap/mcp-ports-declarative-middleware.md)'s
`ports` scope. That doc, not this one, is where `ports.File`/`Cache`/`SQL`/`Dir`'s
cross-cutting-concern story gets resolved (see §7's Review-7 bullet for
the cross-reference).

## 6. Concrete feature survey (kept from this doc's original scope — still accurate, now framed as sealed-`Capability` candidates)

**Every capability below independently CONFIRMS §2's two-part test**
(compatible shape + uniform-enough support) as the reason it stays
adapter-owned — NOT "none has a protocol-agnostic meaning" (an earlier,
overstated version of this section's own claim, corrected in §2). Several
DO have a recognizable cross-protocol semantic category (QoS,
User-Properties/headers, Retained) but still fail the two-part test on one
or both bars, per §5.1/§5.2's worked analysis; others (AMQP's own
addressing/ack specifics) have no shared category at all, the simpler case.
Declaring any of them via the owning adapter's own package is the honest,
correct coupling either way, not a design compromise.

**Already exposed as call-time options today (not yet declarative or gating
anything — candidates to migrate onto a sealed `Capability` type once this
mechanism exists, not necessarily required to):**

- **`ContentType`** (`mqtt5.PublishOptions`) — sets MQTT5's native ContentType
  property; ALREADY wired to format auto-selection (`makeSubscribeMessageHandler`
  matches incoming `ContentType` against `format.Format.ContentType()`). No
  `mqtt`(v3)/`zeromq` equivalent (confirmed: v3 "carries no content-type" per an
  existing code comment).
- **`Retained`** (`mqtt5`/`mqtt` v3 `PublishOptions`, confirmed via both
  adapters' `Publish` functions taking a `retained bool` param) — passes the
  two-part test's SHAPE bar completely (identical mechanism across mqtt/mqtt5,
  see §5.1's worked contrast) but fails the SUPPORT bar (MQTT-family only;
  ZeroMQ has NO retained-message concept at all, no broker to retain anything
  in) — kept as two separate sealed types per §5.1, not one shared type.
- **MQTT5 User Properties** (`adapters/mqtt5/adapter.go`'s `UserPropertyParam`)
  — passes the two-part test's SHAPE bar (a key-value map, compatible with
  AMQP's own `Basic.Properties.headers` field-table) but fails the SUPPORT bar
  (MQTT v3 and ZeroMQ have no equivalent at all) — see §5.2's worked analysis,
  the case that isolates support-failure as an independent disqualifier from
  QoS's shape-failure.

**NOT exposed anywhere today — genuine candidates for a sealed `Capability` type:**

- **Message Expiry Interval** (MQTT5-only) — a publish-time TTL on a message; no
  `mqtt`(v3)/`zeromq` equivalent.
- ~~**Response Topic + Correlation Data** (MQTT5-only)~~ **DECIDED —
  NOT a `Capability` candidate, closed.** Confirmed via code this is
  EXACTLY what powers `reqreply` over mqtt5 (`adapters/mqtt5/reqreply.go`
  reads/writes `msg.Properties.ResponseTopic`/`CorrelationData`
  directly) — was blocked on
  [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md)'s
  `Client`/`Server`/`Attach` rework, now SHIPPED, so this was
  re-evaluated against a real `Attach` shape and DECIDED (in d-0004
  itself, see its own "Relationship to `protocol-native-features.md`"
  section): it stays an IMPLICIT, always-on characteristic of `mqtt5`'s
  reqreply transport, not a declared `Capability` — EVERY mqtt5 reqreply
  route needs it unconditionally, with no opt-out scenario to gate,
  which fails the actual test that motivates `Capability` in the first
  place (compile-time-safe OPT-IN gating for something not every binding
  needs). Removed from this "genuine candidates" list accordingly.
- **Shared Subscriptions** (`$share/group/topic`, MQTT5-only) —
  competing-consumers load-balancing: multiple subscriber instances register
  the SAME shared-group topic, and the broker delivers each message to exactly
  ONE group member. Genuinely DELIVERY-SEMANTICS-CHANGING, not just metadata —
  a strong candidate for a sealed `Capability` specifically (as opposed to a plain
  call-time option) since getting it wrong changes correctness, not just
  observability. No `mqtt`(v3)/`zeromq` equivalent in the SPEC itself.
- **ZeroMQ HWM (High Water Mark) / Conflate** — backpressure/mailbox behavior
  (Conflate = "drop older undelivered messages, keep only the latest," mirrors
  `ports.LatestPort`'s existing semantics elsewhere in this codebase) —
  zeromq-only, no MQTT-family equivalent at all.
- **AMQP Exchange/RoutingKey/Queue, ack-mode** (§5.3) — the newest addition to
  this survey, and the one that drove §2.3's `Address` resolution.

**A THIRD category the two-part test doesn't yet articulate — AMQP
dead-lettering (`x-dead-letter-exchange`/`x-dead-letter-routing-key` queue
arguments):** spun out of
[Unified Error Handling — REST, Events, ReqReply](d-0005-error-handling.md)'s
Topic 4 (dead-letter queue design). Every OTHER entry in this survey falls
into one of two buckets: passes BOTH bars → one shared, protocol-agnostic
declaration (`Security`, the only one); fails EITHER bar → no shared
declaration at all, fully adapter-owned types with zero cross-adapter Go
API (QoS, User Properties, Retained, AMQP addressing). **Dead-lettering
does not fit either bucket cleanly**:

- Its REALIZATION fails both bars badly — AMQP dead-letters via a
  broker-native QUEUE ARGUMENT (`x-dead-letter-exchange`), configured
  ONCE at bind/queue-declare time, with the BROKER doing all routing
  automatically (zero runtime application code); MQTT (v3/v5) has NO
  native dead-letter concept in the protocol at all, and ZeroMQ has NO
  broker whatsoever (brokerless by design) — so mqtt/mqtt5/zeromq MUST
  realize it via the APPLICATION explicitly re-publishing the failed
  message to a designated topic at RUNTIME. By the two-part test's own
  logic, this predicts "no shared declaration" (same conclusion as QoS).
- But its DECLARED INTENT is trivially uniform across every adapter:
  "if this fails, send it here" is structurally identical to
  `events.ErrorChannel`'s existing shape (a topic + a codec) — nothing
  about the DECLARATION itself is protocol-specific, even though the
  ENFORCEMENT beneath it diverges completely.

**Conclusion**: dead-lettering gets ONE shared, cross-protocol Go API
(`events.DeadLetter(topic, opts...)`/`reqreply.DeadLetter(topic, opts...)`
— see the error-handling doc's Topic 4) despite FAILING the two-part
test's realization bars, because the test's bars are about
DECLARATION/ENFORCEMENT shape uniformity, and declaration-shape
uniformity alone is sufficient here — enforcement is free to vary
per-adapter underneath the SAME declared value. A hypothetical future
AMQP adapter would realize `DeadLetter(topic)` by setting
`x-dead-letter-exchange`/`x-dead-letter-routing-key` as queue arguments
at `Subscribe`/bind time (zero per-message runtime cost — the broker
does the work); `mqtt`/`mqtt5`/`zeromq` realize the IDENTICAL declared
value via their own dispatch code explicitly publishing on an unmatched
failure (runtime cost, since no broker feature exists to delegate to).
Both realizations satisfy the SAME declared contract — this is the first
survey entry where "shared declaration, per-adapter-realized" is the
right answer, rather than either "shared everything" or "shared
nothing."

**Considered, but likely NOT worth exposing as a `Capability`:**

- **Topic Alias / Subscription Identifiers** (MQTT5-only) — pure
  WIRE-BANDWIDTH/multiplexing OPTIMIZATIONS, invisible at the application level.
  Mirrors why go-codex doesn't expose raw MQTT QoS-transport internals either —
  these belong entirely inside the adapter's own wire-encoding, never surfaced
  as a channel-level declaration.

## 7. Deferred to a future implementation-planning round — mixed resolved/open state

This doc commits to the DIRECTION for capability declaration (sealed,
per-adapter `Capability` interfaces, supplied at `Attach`/bind time — §2) and
for AMQP addressing (§2.3's option (a)) — the following remain explicitly
open, to be resolved by a SEPARATE, dedicated implementation-planning round
before any code is written:

- **Go generics feasibility — RESOLVED this round via a real, throwaway Go
  prototype** (a standalone module outside this repo, compiled and run, not
  merely reasoned about on paper; deleted after the finding below was
  extracted). All four things that needed concrete proof, not assumption,
  were tested:
  1. **Sealing + generic payload coexist in one slice** — a non-generic
     capability (`QoS`) and a generic one (`UserProperty[In]`, carrying an
     embedded `middleware.Declaration[In, struct{}]`) both satisfied the
     SAME sealed `Capability` interface and were passed together in one
     `caps ...Capability` call, with `UserProperty` instantiated at TWO
     different concrete `In` types in the same call. Compiled and ran
     successfully.
  2. **Cross-package sealing still rejects mismatches with a generic
     capability in the mix** — passing an `mqtt5.QoS` value to a
     `zeromq`-style `Attach` function (a SEPARATE sealed `Capability`
     interface) produced an actual compiler rejection: `mqtt5.QoS does not
     implement zeromq.Capability (missing method isZeroMQCapability)` —
     captured verbatim, not assumed.
  3. **`Attach` CAN dispatch on a generic capability without knowing its
     `In` at `Attach`'s own type-parameter list** — the real subtlety this
     item existed to test. Resolved via a SECOND, non-generic interface
     (`mergeCapability{ Capability; mergeInto(vars map[string]string)
     (string, error) }`) that `UserProperty[In]` ALSO implements — its
     method body closes over its own already-bound `In` internally, so the
     INTERFACE signature itself never mentions `In`. `Attach[T
     any](...)` type-switches on this non-generic interface and successfully
     invoked `mergeInto` against BOTH differently-instantiated
     `UserProperty[In]` values, decoding each one's own vars correctly,
     confirming Go's rule that a generic type's method set is fully concrete
     per instantiation — this is not a special case requiring new language
     features, just correct application of existing generics rules.
  4. **Ergonomics — matches existing go-codex call-site style exactly, with
     ONE real correction found along the way.** An initial constructor
     shape (`NewUserProperty[In](name, inCodec Codec[In])`) FAILED to
     compile when used without explicit type brackets — Go inferred `In`
     from `inCodec` (a value that must already exist, chicken-and-egg for a
     not-yet-built struct type) rather than from field getters/setters,
     exposing a genuine design mistake, not a generics limitation. Corrected
     to mirror `codex.RequiredField[T,F]`/`rest.NewRequiredHeaderParam[T,V]`'s
     OWN proven pattern exactly — infer `In` from the FIRST field's
     `get func(In) string`/`set func(*In, string) error` closures, fixed via
     the constructor's return type, with later chained fields constrained to
     the SAME already-fixed `In` automatically. Retested: compiles and reads
     with **zero explicit type-parameter brackets**, directly comparable to
     real call sites in `examples/rest-api/routes/routes.go`
     (`rest.NewRequiredHeaderParam("X-Request-Id", codex.String(), func(r
     ProfileReq) string {...}, func(r *ProfileReq, v string) {...})`).
  **Conclusion: feasible, with no fundamental blocker — but the constructor
  shape matters.** A future implementation round should design
  capability constructors the SAME way `codex.RequiredField`/
  `rest.NewRequiredHeaderParam` already do (infer the struct type from
  field getters/setters, never from a pre-built struct-level codec argument)
  — this is now a CONFIRMED constraint, not merely a stylistic preference.
- **Spec (OpenAPI/AsyncAPI) rendering plan for adapter-defined
  capabilities — RESOLVED this round.** A new `events.CapabilitySpec{Name,
  Description string}` value implements `ChannelOpt`, declared inline in
  `NewChannel(...)`/`WithSubscribe`/`WithPublish`'s variadic opts (mirrors
  `TopicParam`'s own declaration-site placement) — this IS §3's Candidate 3
  (`DeclareCapabilitySpec`) made concrete. Rendering choice: a GENERIC
  `x-capabilities: [{name, description}]` AsyncAPI vendor-extension array at
  the channel/operation level — chosen over a per-capability vendor field
  (e.g. `x-mqtt5-qos`) so the spec renderer never needs a change when a new
  capability type is added; every declared `CapabilitySpec` renders
  uniformly regardless of which adapter eventually supplies it. A
  companion `CheckCapabilityCoverage(declared []CapabilitySpec, supplied
  []Capability) error` helper (opt-in, mirrors `rest.CheckCoverage`'s own
  not-compiler-enforced precedent, returning a `MissingCapabilityError` on
  drift) is called AUTOMATICALLY by each adapter's own bulk
  `ServeSubscribers`/`Attach`-equivalent dispatch at startup — the caller
  never has to remember to invoke it by hand. Shared Subscriptions (§6)
  remains the sharpest example of why spec-visibility matters
  (delivery-semantics-changing, not just metadata).
- **Whether/how D-0003 relates to this mechanism — RESOLVED this round,
  see §3.** No longer "reopened, not decided" — §3 now gives a concrete
  answer (both are stage-2 declarations, sharing a lifecycle stage, not
  merged into one Go type) via the confirmed 4-stage model. Kept as a
  bullet here only as a pointer, not a live open question anymore.
- **Discovery — RESOLVED this round: not a real gap,
  confirmed via existing-precedent evidence, not a spike.** The original
  worry: no adapter package exposes an enumerable "list of capabilities I
  support" a caller could inspect ahead of writing code — a mismatch is
  only caught at compile time, when the caller actually attempts the
  combination.

  **Confirmed via code search: this codebase has ZERO existing precedent
  for a runtime "list what's supported" API on ANY sealed/closed
  mechanism** — `Capability` would not be introducing a novel gap, it
  would be the FIRST place asked to solve a problem nothing else here
  solves either:
  - `ports.Pattern` (confirmed `ports/doc.go:65`) — its own concrete
    patterns (`RESTPattern`, `EventPattern`, `ReqReplyPattern`,
    `MCPPattern`) are discoverable ONLY via godoc cross-links and the
    package's own exported symbols, never a runtime `ListPatterns()`-style
    call.
  - `rest.SecurityScheme` (confirmed `api/rest/builder.go:2310`) — a
    caller learns what a route requires by reading the exported struct
    literal at the call site, not by querying an adapter for "which
    schemes do you support."
  - No `api/mcp`/`adapters/mcpgo` capability-listing precedent either —
    the MCP protocol's own `ListTools` enumerates already-REGISTERED tool
    instances (a runtime inventory of what a server exposes), which is a
    different question from "which capability TYPES could this adapter
    package ever accept," and has no bearing here.

  **Resolved position:** IDE autocomplete + godoc on each adapter
  package's own exported `Capability`-implementing types IS this
  codebase's established discovery mechanism for every sealed interface,
  not a weaker substitute invented for this design. A compile error on
  mismatch remains strictly earlier feedback than a runtime check would
  give, and no existing mechanism in this codebase pays for anything
  more — so `Capability` introduces no regression by not inventing a
  runtime introspection API either. Not designed further: an actual
  `ListCapabilities()`-style API remains available as a FUTURE addition
  if a concrete, driven need for it ever appears (mirroring this doc's
  own "don't force an answer without a driver" discipline — see Review-7),
  but nothing in the current survey drives it.
- **Capabilities that clear the two-part test's shape bar but not the
  support bar (or vice versa)** (REVISED this round — corrects an earlier,
  overstated version of this bullet that claimed "no capability surveyed has
  any protocol-agnostic meaning at all"): §2's two-part test (compatible
  shape + uniform-enough support) is the CURRENT reasoning, and several
  surveyed capabilities DO have a real cross-protocol semantic category
  (QoS, User-Properties/headers, Retained — §5.1/§5.2) while still failing
  the test on at least one bar. What remains genuinely undecided: whether a
  capability that clears BOTH bars, beyond Security, will ever be
  identified, and if so whether it should extend the core `middleware`/
  `api/events` layer (Security's own treatment) or need some THIRD
  mechanism this doc hasn't designed. Not designed here, flagged as an edge
  case only.
- **AMQP's compound ack+persistence configuration — RESOLVED this round via
  a third real, throwaway Go prototype** (same discipline as the two prior
  spikes — compiled and run, not just reasoned about; deleted after the
  finding was extracted). Confirmed: AMQP's delivery-guarantee story needs
  THREE separate capabilities, not two — `amqp.Persistent` (per-message
  delivery-mode bit), `amqp.PublisherConfirms` (channel-level broker-ack-of-
  receipt), and `amqp.AckMode` (per-consumer auto/manual acknowledgement) —
  settling the doc's own previously-open "does AMQP need a THIRD
  capability" question: YES, `PublisherConfirms` is a genuinely separate
  AMQP concept from `Persistent` (a message can be persistent without
  publisher confirms and vice versa), and does NOT fold into `AckMode`
  either. Even all three together do not reach MQTT's native
  "exactly-once" semantics (AMQP 0-9-1 has no built-in equivalent; some
  broker extensions approximate it, out of scope for the base protocol).

  **The deeper design question this spike surfaced and resolved (not
  previously identified in this bullet): unlike MQTT's `QoS` — meaningful
  symmetrically on BOTH publish and subscribe — AMQP's three capabilities
  split cleanly by ROLE**: `Persistent`/`PublisherConfirms` are
  PUBLISH-only, `AckMode` is SUBSCRIBE-only. Two candidate mechanisms were
  tested head-to-head: (A) one role-agnostic `Attach` accepting all three
  together, sorting role-appropriateness out via a RUNTIME check inside
  `Attach`'s own dispatch — confirmed working, but a role mismatch (e.g.
  `AckMode` supplied while publishing) is caught only at runtime, the SAME
  category of gap this doc's compile-time-safety bar exists to close. (B)
  role-SPLIT `PublishAttach`/`SubscribeAttach`, each accepting only its OWN
  role-scoped sealed interface (`PublishCapability`/`SubscribeCapability`)
  — CONFIRMED via the prototype that a role-inappropriate capability is
  REJECTED AT COMPILE TIME, both directions, with actual compiler output
  captured:
  ```
  # AckMode (subscribe-only) passed to PublishAttach:
  cannot use amqp.AckModeManual ... as amqp.PublishCapability value in
  argument to amqp.PublishAttach: amqp.AckMode does not implement
  amqp.PublishCapability (missing method isAMQPPublishCapability)

  # Persistent (publish-only) passed to SubscribeAttach:
  cannot use amqp.Persistent(true) ... as amqp.SubscribeCapability value in
  argument to amqp.SubscribeAttach: amqp.Persistent does not implement
  amqp.SubscribeCapability (missing method isAMQPSubscribeCapability)
  ```
  **Candidate (B) is the confirmed recommendation** — it extends this
  doc's compile-time-safety bar from adapter-correctness (§2, §2.3) to
  ROLE-correctness too, at the cost of two `Attach`-shaped functions
  instead of one per adapter (a natural fit, since `events.Channel` itself
  already splits `WithSubscribe`/`WithPublish` by role — role-split
  `Attach` functions mirror an ALREADY-ESTABLISHED asymmetry, not a new
  one). `Persistent`/`PublisherConfirms` implementing ONLY
  `PublishCapability` (never `SubscribeCapability`) and `AckMode`
  implementing ONLY `SubscribeCapability` is the confirmed, tested shape —
  not designed further here (e.g. exact constructor ergonomics for these
  three types were not spiked, only the role-split mechanism itself).
- **The `Address` mismatch is NOT compile-time-safe — RESOLVED this round
  via a second real, throwaway Go prototype** (same discipline as the "Go
  generics feasibility" spike above — compiled and run, not just reasoned
  about; deleted after the finding was extracted). `events.Channel[T]`
  gains a SECOND type parameter, `Addr`, constrained to an `Address`
  interface requiring one real method (`Template() string`, naming
  whichever field hosts `{placeholder}` vars) — NOT a bare marker.
  Adapter `Attach` functions require the LITERAL concrete address type in
  their own signature (`events.Channel[events.TopicAddress, T]` for
  `mqtt5`/`zeromq`, `events.Channel[amqp.Address, T]` for `amqp`) — ordinary
  Go type equality then rejects any mismatch at COMPILE time, confirmed
  bidirectionally with actual compiler output captured (see §5.3). This
  turned out SIMPLER than either candidate this bullet originally posed:
  sealing `Address` per adapter was UNNECESSARY — requiring the exact
  concrete type in `Attach`'s own signature already achieves the identical
  guarantee, with zero marker-method boilerplate. The prototype ALSO
  confirmed (not merely assumed) that the EXISTING `TopicParam`/`BuildTopic`
  var-substitution mechanism generalizes across address shapes via
  `Address.Template()` — AMQP's compound `{Exchange, RoutingKey, Queue}`
  resolves ONLY its `RoutingKey` segment through the shared mechanism,
  leaving `Exchange`/`Queue` untouched, exactly as needed. This closes the
  doc's own "we do not compromise on compile-time safety" bar for
  addressing, matching what was already true for capabilities.

**The following gaps were surfaced by a dedicated critical review of this doc
(a `/review` pass) two rounds ago. Several are now RESOLVED by this round's
mechanism pivot (marked below); the rest remain open, per the same
one-at-a-time future-round policy as before:**

- **Stringly-typed capability matching — RESOLVED this
  round.** The `Feature.FeatureID() string` + runtime `Provider.Supports`
  mechanism this finding was about no longer exists. §2's sealed
  `Capability` interfaces give the SAME compile-time exhaustiveness
  `RouteOpt`/`ChannelOpt` already have — no string IDs, no collision risk
  (Go's own package-scoped unexported-method visibility rules are the
  enforcement, not a naming convention).
- **`Provider.Supports` boolean-only — RESOLVED/moot this
  round.** No `Provider` interface exists anymore. "Does this adapter
  support X" is now answered by "does a `Capability`-satisfying value for X
  exist in this adapter's package at all" — a category question the Go
  compiler answers, not a boolean runtime call. (Partial/graduated support
  within ONE capability, e.g. an adapter recognizing SOME but not all
  AckMode values, would still need its OWN validation inside that
  capability's own construction — orthogonal to this finding, not
  reopened.)
- **Security shows near-zero benefit — RESOLVED this
  round via §3's 4-stage model.** §5.5 explains WHY Security doesn't fit
  the sealed-per-adapter mechanism (it's genuinely cross-protocol, unlike
  QoS/User Properties/AMQP addressing) — and §3's confirmed resolution
  now explains WHERE Security actually fits instead: it stays a stage-2,
  spec-contributing declaration in the protocol-agnostic core (exactly
  like `Middleware[In,Out]`/D-0003 today), never needing to become a
  sealed, adapter-specific `Capability` at all. The "near-zero benefit"
  observation was correct; it is no longer an unexplained tension, since
  §3 now gives Security's OWN stage a name and a confirmed neighbor
  (`Middleware[In,Out]`), not just a footnote.
- **`NewChannel`'s breaking change under-analyzed —
  RESOLVED: migration blast radius counted, confirmed large.** The
  address-parameterization prototype (§2.3/§5.3) already CONFIRMED
  `NewChannelFromTopic`'s ergonomics (zero explicit type-parameter brackets
  at the bare-string call site); this round enumerated every real call
  site, via `grep`, not estimate:

  | Category | Count |
  | --- | --- |
  | `examples/*` (real call sites, comment-only mentions excluded) | 21 |
  | `api/events/*_test.go` (the package's OWN tests) | 141 |
  | `adapters/*/*_test.go` (mqtt/mqtt5/zeromq/redis/sql/file conformance tests) | 110 |
  | **Subtotal — real, compiled Go call sites** | **272** |
  | Godoc comment examples (`adapters/mqtt/doc.go`, `topicvars.go`) | 2 |
  | Documentation site snippets (`docs/**/*.md`) | 26 |
  | **Grand total** | **300** |

  This is a genuinely large blast radius, not a small one — any adoption
  of `Channel[Addr, T]`'s two-type-parameter signature touches roughly 300
  places across the repo.

  **Blocking discovery this round — the hypothesized migration name
  COLLIDES with an already-shipped, unrelated symbol.** The mitigating
  hypothesis above (`events.NewChannel[T](topic, codec, ...)` →
  `events.NewChannelFromTopic(topic, codec, ...)`) assumed
  `NewChannelFromTopic` was free to repurpose. It is NOT:
  `NewChannelFromTopic[T any](topic Topic, codec codex.Codec[T], opts
  ...ChannelOpt) Channel[T]` already exists, shipped, documented, and
  actively used — it takes a pre-built [`Topic`] VALUE (topic template +
  bundled `TopicParam`s), a COMPLETELY DIFFERENT shape from the
  string-topic constructor this section's plan needed that name for.
  Reusing the name would either silently change its meaning (a real
  breaking change disguised as a rename) or require a signature-based
  overload Go generics do not support.

  **RESOLVED this round — Address parameterization ships ADDITIVELY,
  NOT as a retrofit of `Channel[T]`/`NewChannel[T]`.** Given (a) this
  naming collision proves the originally-planned migration was never as
  mechanical as hoped, (b) Phase 4 (the only concrete CONSUMER of a
  non-topic-string `Address` — a real AMQP adapter) is explicitly out of
  scope for this implementation round, and (c) a ~300-call-site breaking
  change with ZERO real consumer today is not yet justified — this round
  ships `events.Address` (the `Template() string` interface) and
  `events.TopicAddress{Topic string}` (today's only implementation) as
  standalone, ADDITIVE types with NO changes to `Channel[T]`, `NewChannel`,
  or any of the 272 real call sites. `Channel[Addr Address, T any]`'s full
  generic retrofit (and the resulting mass migration) is DEFERRED until
  `docs/roadmap/amqp-adapter.md`'s own future round actually needs a
  non-topic-string address to exist for something to consume — at which
  point a fresh naming survey (avoiding today's `NewChannelFromTopic`
  collision) is required BEFORE any call-site migration begins.
- **Transport lock-in — RESOLVED/reframed this round.**
  Supplying a capability at `Attach` time (not baked into the channel's own
  declared type) means the lock-in is now EXPLICIT and LOCAL to that one
  `Attach` call site — the underlying `events.Channel[T]` value itself
  remains fully portable and can be attached elsewhere, with different (or
  no) capabilities, without any special handling.
- **Package placement unreconciled — RESOLVED/moot this
  round.** No new shared package is needed at all — each adapter's
  `Capability` interface lives in its OWN existing package (`mqtt5`, `mqtt`,
  `zeromq`, a hypothetical `amqp`), the same way `ports.Pattern`'s technique
  lives entirely inside package `ports` itself.
- **`ports.Pattern`'s fate — PARTIALLY resolved this
  round.** `ports.Pattern` stays a separate, deliberately-closed mechanism,
  entirely UNAFFECTED by this pivot — it selects a port's fundamental shape;
  §2's sealed `Capability` mechanism is orthogonal, declaring capabilities
  WITHIN whichever pattern was already chosen. Whether `ports.File`/
  `Cache`/`SQL`/`Dir` ever need their OWN capability-EQUIVALENT mechanism
  is no longer "no driver at all" — **a driver now exists, but it is a
  DIFFERENT driver pointing at a DIFFERENT doc, not this one's `Capability`
  mechanism.** The driver is the library's own UX North Star (declarative/
  simple/consistent workflow for the user), not adapter/protocol
  capability mismatch — and it is already being pursued in
  [MCP and Ports Declarative Middleware](../roadmap/mcp-ports-declarative-middleware.md)'s
  `ports.File`/`Cache`/`SQL`/`Dir` scope (cross-cutting
  concerns), NOT here. §5.6's structural observation stands unchanged:
  ports has no separate "Attach" binding step to hang a `Capability` off
  of the way REST/events do, so even with a real driver now identified,
  the RIGHT mechanism for ports is that doc's decorator shape, not an
  attempt to force this doc's `Capability` mechanism onto a boundary
  that structurally can't host it.
- **`stats.Observer` integration — RESOLVED for
  Handler Disposition (§8) via a sixth throwaway Go prototype** (compiled
  and run, not merely reasoned about — deleted after this finding was
  extracted). Capability MISMATCHES remain correctly out of scope for
  Observer (a compile error never reaches a running process, confirmed
  unchanged from the prior round's note) — but Handler Disposition's
  runtime OUTCOME is a genuine, confirmed gap: `stats.Observer`'s core
  `RecordSubscribe(topic, success bool, duration)` (confirmed
  `stats/observer.go:52-61`) cannot distinguish `DispositionAck` from
  `DispositionNackDiscard` from `DispositionNackRequeue` — just a
  boolean.

  **Confirmed mechanism: a NEW, OPTIONAL `DispositionObserver` interface,
  mirroring `stats.SecurityObserver`'s EXACT existing pattern** (confirmed
  `stats/observer.go:96-101` — type-asserted by the adapter, purely
  additive, zero change required to any existing `Observer`
  implementation):

  ```go
  // package stats
  type DispositionObserver interface {
      RecordDisposition(topic string, disposition Disposition)
  }
  ```

  Called by the adapter's own dispatch loop AFTER `ResolveDisposition`
  resolves the final outcome — the SAME ordering precedent
  `RecordSecurityRejection`'s own call sites already establish (confirmed
  `adapters/mqtt5/adapter.go:353-387`: resolve first, then record).

  **The sharper, CONFIRMED finding — why this is necessary, not just
  nice-to-have:** the prototype tested the critical case directly — a
  handler returns `nil` (NO processing error) but explicitly signals
  `DispositionNackDiscard` (e.g. "structurally valid, but business rules
  reject this message, don't retry"). `RecordSubscribe`'s existing
  `success bool`, left UNCHANGED (tied ONLY to the handler's own returned
  error, per its current documented contract), correctly reports
  `success=true` — but this means **without `RecordDisposition`, this
  discarded message is OBSERVABLY INDISTINGUISHABLE from one that
  succeeded normally** — a real blind spot for any dashboard/alerting
  built only on `RecordSubscribe`.

  **A second candidate — deriving `RecordSubscribe`'s `success` FROM the
  disposition instead — was tested and REJECTED as a confirmed, silent
  breaking change**: the prototype confirmed that for the SAME nil-error-
  but-discarded case, this candidate reports `success=false`, contradicting
  `RecordSubscribe`'s existing, shipped documentation ("success is false
  when decode or the application handler failed" — confirmed
  `stats/observer.go:56`) — any EXISTING `Observer` implementation relying
  on that documented meaning would silently start seeing different
  values with no code change on its own part.

  **Resolved recommendation:** `RecordSubscribe` stays completely
  UNCHANGED; `DispositionObserver.RecordDisposition` is ADDED as a
  purely additive, optional extension — confirmed via the prototype to
  compile, fire correctly (in the right order, with correct values) for
  an observer implementing both, and to be silently skipped (no panic, no
  behavior change) for one implementing only the base `Observer`.
- **No "Test plan" section — RESOLVED this round.** A
  "## Test plan (once implementation begins)" section is now added
  (mirroring [D-0003](../design/d-0003-codec-declared-middlewares.md)'s own
  section), covering: sealed `Capability` compile-time positive/negative
  pairs (including the AMQP role-split rejection), `Address`
  parameterization/`NewChannelFromTopic` ergonomics, Handler Disposition's
  ctx-sink round trip and default-fallback cases, `DispositionObserver`'s
  additive behavior, and §3's 4-stage lifecycle spec-contribution/drift-
  check cases — explicitly deferring shared `Middleware[In,Out]`
  construction/dispatch test cases to D-0003's own Test plan, not
  duplicating them.
- **Field-naming collision — RESOLVED/moot this
  round.** No `UnsupportedFeatureError` type is needed anymore — a
  capability mismatch is the Go compiler's own diagnostic, not a custom
  error type with fields to name.
- **Should EVERY `Capability` carry its own
  observable declaration, reducing bespoke adapter-side Observer wiring
  — RESOLVED this round.** Spun out of a separate, broader review of the
  Observer pattern across the api layer (same session, same "thin
  adapter, thick api layer" principle) — confirmed via code that adapters
  ALREADY hand-roll their own capability-specific Observer calls today
  wherever a protocol feature has an observable runtime effect (e.g.
  `RecordSubscribe`/`RecordPublish`'s `success bool` says nothing about
  WHICH QoS tier was actually negotiated, whether a Retained flag was
  honored, or which Shared Subscription group handled a message — each
  adapter that wants this visibility must invent its own ad hoc reporting
  path, no shared mechanism exists). §8's `DispositionObserver`
  (Review-8, resolved) is a NARROWER, adjacent precedent — it solves ONE
  specific runtime outcome (ack/nack/requeue) for ONE specific concept
  (Handler Disposition), not capabilities in general.

  **Resolution: ONE new, optional, type-asserted `stats.CapabilityObserver`
  interface** — `RecordCapabilityApplied(location, capability string)` —
  mirroring `SecurityObserver`/`DispositionObserver`'s exact "purely
  additive, never forced" type-assertion pattern (not baked into the
  `Capability` interface itself; `Capability` stays a bare marker-method
  seal with zero observability surface of its own). Generic across EVERY
  capability type (QoS, Retained, User Properties, HWM, Conflate, a
  future AMQP ack-mode/persistence) — an adapter's dispatch code calls
  it ONCE per capability actually exercised, with `capability` being that
  capability's own self-reported or `%T`-derived name, and `location`
  identifying the channel/topic. This gives the GENERIC dispatch code in
  each adapter's `ServeSubscribers`/publish path a SINGLE shared hook to
  call, uniformly, regardless of which concrete capability is present —
  matching the same "adapter calls one shared thing, doesn't hand-roll
  per-feature logic" shape established elsewhere for Observer reporting.
  Purely additive: zero change to any existing `Observer` implementation,
  since the interface is optional and type-asserted exactly like
  `SecurityObserver`.
- **[Cross-doc, Medium] Dead-letter queue dependency on
  `d-0005-error-handling.md` — RESOLVED this round: Topic 4 has SHIPPED.**
  §6's "AMQP dead-lettering" survey entry (added this round) already
  resolves the DESIGN question — dead-lettering is explicitly EXCLUDED
  from this document's `Capability` mechanism, because its declarative
  surface (`events.DeadLetter(topic, ...)`/`reqreply.DeadLetter(topic,
  ...)`) is meant to live in the CORE `api/events`/`api/reqreply` layer,
  shared uniformly across every adapter — NOT as a per-adapter sealed
  `Capability` type the way QoS/User Properties/Retained/AMQP addressing
  all correctly are. **Confirmed via code**:
  [`docs/design/d-0005-error-handling.md`](../design/d-0005-error-handling.md)'s
  Topic 4 has SHIPPED (`api/events/dead_letter.go`/`api/reqreply/dead_letter.go`
  both exist, `DeadLetter`/`AddGlobalDeadLetter` implemented and tested) —
  the "if Topic 4 has NOT shipped yet" branch below no longer applies:
  - Do NOT fold dead-lettering into this document's implementation scope
    under any circumstances — it is a confirmed, permanent exclusion
    (§6), not a deferred/open item like the rest of this section.
  - Confirmed NO blocking dependency existed either way — `DeadLetter`'s
    mqtt/mqtt5/zeromq realization needs no `Capability` plumbing at all
    (plain runtime publish, see Topic 4's "how DLQ works in practice"
    section) — this held true even before Topic 4 shipped.
  - HOWEVER, if/when a FUTURE AMQP adapter is ALSO built
    (`docs/roadmap/amqp-adapter.md`, a third, separate roadmap) and
    realizes `DeadLetter` via that document's pre-existing
    `QueueConfig.Args` field (`x-dead-letter-exchange`), double-check at
    that point whether this document's own `Attach`-time `Capability`
    supply mechanism and `amqp-adapter.md`'s queue-declare-time `Args`
    field end up BOTH trying to configure AMQP queue arguments through
    two independent paths — a coordination check, not a design conflict
    known to exist yet, since neither adapter is built.
- **`Attach` signature reconciliation — RESOLVED this
  round: Option 3 was already-shipped, existing code, just not yet
  recognized as the answer.** Originally found while cross-checking this
  document against a since-completed, unrelated "Thin Adapters Audit"
  round (independent of anything THAT audit changed — it never touched
  `Attach` at all). §2.2's ORIGINAL pseudocode (now retired — see below)
  sketched:

  ```go
  func Attach[T any](client *Client, ch events.Channel[T], caps ...Capability) error
  ```

  a PER-CHANNEL function taking a specific `ch events.Channel[T]` plus
  `caps ...Capability`. Confirmed via `grep -n "^func Attach"
  adapters/*/*.go` that NONE of the 3 actually shipped signatures match
  this shape — all 3 are BULK, per-`*events.Client` functions wiring
  every subscriber/publisher on the Client at once, with NO `ch`/`caps`
  parameter at all (`adapters/mqtt5/transport.go:77`,
  `adapters/mqtt/transport.go:72`, `adapters/zeromq/transport.go:66`).

  **The resolution — tracing the ACTUAL `Subscriber[T]`/`Publisher[T]`/
  `ChannelHandle[T]` machinery (`api/events/builder.go`), not just the
  `Attach` function in isolation, surfaces that this question is ALREADY
  ANSWERED by existing shipped code:**
  - `Subscriber[T].WithOptions(opts any)`/`Publisher[T].WithOptions(opts
    any)` already exist TODAY, copying onto `ChannelHandle.HandlerOpts
    any` — a general, ALREADY-SHIPPED, per-channel, adapter-owned,
    DECLARE-TIME (i.e. BEFORE the bulk `Attach`/`ServeSubscribers` call)
    configuration slot.
  - `adapters/mqtt5.SubscribeOptions.QoS byte`'s own EXISTING doc comment
    says this VERBATIM, confirmed via code: "when `Subscriber.WithOptions`
    attaches a `SubscribeOptions` value as a channel's declare-time
    `ChannelHandle.HandlerOpts`... there is no other way for
    `ServeSubscribers` to learn a per-channel QoS." **This is Option 3,
    literally already built** — `adapters/mqtt`, `adapters/mqtt5`, AND
    `adapters/zeromq` each already have their OWN `SubscribeOptions`/
    `PublishOptions` struct wired through this identical
    `WithOptions`→`HandlerOpts` pipeline; `mqtt5.UserPropertyParam` is the
    closest existing precedent for a LIST of adapter-owned declarations
    living inside such a struct.
  - **Confirmed resolution**: Capability supply is a DECLARE-TIME concern
    via a NEW `Capabilities []<pkg>.Capability` field added to each
    adapter's EXISTING `SubscribeOptions`/`PublishOptions` struct (mirrors
    `UserPropertyParams []UserPropertyParam`'s own shape exactly) — NOT a
    new `Attach(client, ch, caps...)` parameter as originally sketched
    above. `caps ...Capability` variadic parameters are retired from
    §2.2/§5.1/§5.2/§5.3's pseudocode; every one of those snippets should
    read `SubscribeOptions{..., Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce}}`
    (attached via `Subscriber.WithOptions(opts)`) instead, when this
    document's own implementation round updates them.
  - **Additive, not breaking, for existing `QoS byte`/`Retained bool`
    fields**: kept as-is (no correctness problem to fix), with the new
    `Capabilities` slice as the RECOMMENDED, sealed-and-compile-time-
    checked path going forward — mirrors how `rest.PathParam` struct
    literals stay valid alongside `NewPathParam[T]`'s merge-capable
    convenience (an escape hatch, not a deprecation).
  - No new mechanism needed for compile-time adapter-rejection either —
    a concretely-typed `Capabilities []mqtt5.Capability` field on
    `mqtt5.SubscribeOptions` already rejects a `zeromq.Capability` value
    at compile time, the identical guarantee §2.2's original `Attach`
    parameter would have given, via ordinary Go field-type-checking
    instead of a new function parameter.

## 8. Handler Disposition — a DISTINCT concept from `Capability`, RESOLVED via a fourth throwaway Go prototype

**The gap, confirmed via code, not assumed:** a subscribe handler's
returned error (`func(ctx, T) error`, confirmed
`adapters/mqtt5/adapter.go:239`) is used TODAY only for observability
(`stats.ReportErrors`) and `ErrorPattern`/`ErrorChannel` response-publishing
(confirmed `adapters/mqtt5/adapter.go:434-450`) — it is **never translated
into any protocol-level acknowledgement action, anywhere in this
codebase** (confirmed via repo-wide search: zero hits for
requeue/nack/disposition outside unrelated comments, across `adapters/`,
`api/`, `ports/`, `stream/`). This means `amqp.AckMode` (§5.3/§7's own
recently-resolved capability) is currently **UNUSABLE in any principled
way** — declaring manual-ack mode gives a handler NO abstracted way to
signal "requeue this" vs. "discard this" vs. "acknowledge this," forcing a
caller to bypass `api/events` and reach into `adapters/amqp`'s raw
connection object directly — precisely the adapter-leak this entire
roadmap doc exists to prevent.

**Why this is a DISTINCT concept from `Capability` (§2), not a variant of
it — stated explicitly so future readers don't conflate the two:**
`Capability` declares STATIC, `Attach`-time CONFIGURATION (WHAT protocol
features a channel/subscription uses — QoS level, ack MODE, User
Properties). Handler Disposition is about a handler's PER-MESSAGE RUNTIME
OUTCOME — a completely different axis (declare-time vs. dispatch-time),
the SAME distinction this doc's own review history already insisted on
keeping separate for `Security` vs. protocol-native capabilities (§5.5).
Folding disposition into `Capability` would repeat that exact mistake.

**Scope, per explicit confirmation: NOT AMQP-only.** `api/reqreply`'s own
adapters (`adapters/mqtt5`, `adapters/zeromq`) have the SAME underlying
need — a request/reply handler's outcome may need to map onto whatever
acknowledgement concept ITS underlying transport has (or none, for
transports without one). The mechanism below was tested for reuse across
BOTH `api/events` and a `reqreply`-shaped consumer, not just pub/sub.

### The confirmed mechanism — a ctx-mutable-sink, mirroring an ALREADY-PROVEN pattern in this codebase

Three candidates were weighed; the prototype confirms the third:

- **(A) Error-type matching** (mirrors `ErrorPattern`'s technique): couples
  disposition to the handler's returned error TYPE — rejected as the
  primary mechanism, since a handler may want DIFFERENT dispositions for
  the SAME error type depending on context (e.g. retry budget already
  exhausted vs. not).
- **(B) Explicit second return value** (`func(ctx, T) (Disposition,
  error)`): rejected — a genuine breaking change to every handler
  signature across the codebase, and awkward for handlers that don't care
  about disposition at all.
- **(C) CONFIRMED — ctx-mutable-sink signal**, mirroring
  `nethttp.WithResponseHeaders`/`ResponseHeadersFromContext`'s EXACT
  pre-allocation pattern (confirmed at `adapters/nethttp/adapter.go:57-68`):
  handler signature stays **completely unchanged**. A handler that wants a
  non-default outcome calls `events.SetDisposition(ctx, ...)` inside its
  own body; the adapter pre-allocates the box BEFORE calling the handler
  and reads the result back AFTER:

```go
// package events (or a shared package — see "Package placement" below)
type Disposition int

const (
    DispositionDefault Disposition = iota // no explicit signal — adapter falls back to its own default
    DispositionAck
    DispositionNackRequeue
    DispositionNackDiscard
)

func EnsureDispositionBox(ctx context.Context) context.Context
func SetDisposition(ctx context.Context, d Disposition)
func DispositionFromContext(ctx context.Context) Disposition

// ResolveDisposition is the shared helper any adapter calls to get the
// FINAL disposition — folding in the "nil error -> Ack, non-nil error ->
// NackRequeue" default when the handler never explicitly signaled.
func ResolveDisposition(ctx context.Context, handlerErr error) Disposition
```

**Confirmed via the prototype (compiled and run, not merely reasoned
about — deleted after this finding was extracted), all of the following:**

1. **The ctx-signal mechanics work exactly like `WithResponseHeaders`** —
   pre-allocate, mutate inside the handler, read back after.
2. **A simulated AMQP dispatch correctly resolves all four cases**:
   explicit `NackRequeue` (overriding a returned error), explicit
   `NackDiscard`, default `Ack` (nil error, no signal), and default
   `NackRequeue` (non-nil error, no signal) — each mapped to the
   corresponding simulated `channel.Ack`/`channel.Nack(requeue=...)` call.
3. **An adapter with NO acknowledgement concept (MQTT5) safely ignores the
   mechanism entirely** — it never calls `EnsureDispositionBox`, so even a
   handler that calls `SetDisposition` anyway (e.g. shared business logic
   reused across adapters) hits `SetDisposition`'s own no-op-when-absent
   safety — CONFIRMED zero leakage of the concept into adapters that don't
   need it, not merely assumed.
4. **Package placement — CONFIRMED genuinely shared, not `api/events`-
   specific**: a `reqreply`-shaped stub package (deliberately NOT embedding
   or extending the `events`-shaped stub, mirroring `api/reqreply`'s real
   independence from `api/events`) called
   `events.EnsureDispositionBox`/`events.ResolveDisposition` DIRECTLY and
   got IDENTICAL, correct behavior — confirming the mechanism does NOT
   need to be duplicated per-pattern. **Recommendation:** `Disposition`/
   `EnsureDispositionBox`/`SetDisposition`/`DispositionFromContext`/
   `ResolveDisposition` should live in a shared package (`middleware` is
   the natural existing candidate — it already hosts other cross-cutting,
   ctx-carried mechanisms like `ContextField[V]` — or a new small sibling
   package), NOT inside `api/events` itself, so `api/reqreply` can import
   it without an `api/events` dependency it otherwise wouldn't need.
5. **Ergonomics — confirmed comparable to real, shipped call sites**:
   `events.SetDisposition(ctx, events.DispositionNackRequeue)` reads
   directly analogous to `chiadapter.WithResponseHeaders(ctx, h)`
   (confirmed real call site, `examples/rest-api/demo_violations.go:63`) —
   same shape (ctx + one value), same simplicity.

**Relationship to `AckMode` (§5.3, §7):** declaring `amqp.AckMode` (a
`Capability`) without ALSO having Handler Disposition available is an
INCOMPLETE story — the capability declares that manual acknowledgement is
IN USE, but gives a handler no way to control the outcome. Both mechanisms
are needed together for AMQP's manual-ack mode to be genuinely usable
through this codebase's declarative layer; they remain two SEPARATE
mechanisms (declare-time vs. dispatch-time), attached/signaled at
different points, not merged into one.

**Not designed further here (flagged for a future round, consistent with
this doc's own discipline):** the EXACT default-fallback policy (is
"nil→Ack, error→NackRequeue" the right universal default, or should it be
itself configurable per-channel?); whether `DispositionNackRequeue` needs a
richer shape (e.g. a requeue delay, which real AMQP brokers support via
dead-lettering/TTL patterns, not the base `Nack` call); and the exact final
package name/location (this section recommends `middleware` or a new
sibling package, but does not commit to one).

**Scope explicitly limited to `events`/`reqreply` — `ports` NOT covered,
NOT tested, flagged as an open question, not an oversight:** every pattern
this section confirms (`api/events`, `api/reqreply`) shares a MESSAGE-
DISPATCH shape — an adapter calls a handler, then resolves an outcome for
that ONE message/call. `ports.File`/`Cache`/`SQL` have NO analogous
dispatch loop — `Read`/`Write`/`Get`/`Set` are direct, synchronous method
calls, with no separate "dispatch, then resolve a disposition" step to
attach a signal to. The one plausible ports-side analogue — SQL
transaction commit/rollback, where a handler's outcome could decide
whether a `ports.SQL` write commits or rolls back, structurally similar to
ack/nack — is **genuinely NEW territory, not an existing gap**: confirmed
via code that `ports`/`adapters/sql` have ZERO transaction/commit/rollback
concept today. Whether `Disposition` (or some ports-specific analogue)
extends there, and what shape it would take given the missing dispatch
loop, is NOT designed here — left for a dedicated future round if a
concrete driver appears.

## 9. The capability-requirement-composition rework — full phase history (merged)

**This section is MERGED IN WHOLESALE from the former
`docs/design/d-0006-protocol-native-capabilities.md`, per this repo's
graduation convention** (the same move this very document made when IT
graduated from `docs/roadmap/protocol-native-features.md` — see this
doc's own status block above). That roadmap doc REWORKED §0-§8 above's
originally events/reqreply-only Capability mechanism into the complete,
three-tier vocabulary (Baseline/Implicit/Explicit) summarized in the
"Shipped shape" section at the top of this document, extended it to
`api/reqreply`, and added an entirely NEW tier this document never
covered originally: `api/rest`'s Header/Cookie/Query real-interface
promotion (Phase 6/6a). Its own Phase 8 review-and-closeout pass
confirmed the whole model — across all three api packages — is now
architecturally and feature complete. The phase-by-phase content below
is preserved VERBATIM (heading levels demoted to nest here, phase labels
and numbering UNCHANGED) specifically so the ~95 existing inline
`"docs/design/d-0006-protocol-native-capabilities.md's Phase N"`-style
comments scattered across `api/rest`/`api/events`/`api/reqreply` and
every adapter package — now repointed at this file — remain accurate:
"Phase 4e" (for example) still means exactly what it always meant, just
as a subsection here instead of a standalone roadmap file.

### Motivation
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

### Architectural guardrail: zero backdoors between the api layer and adapters
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

### Design guardrails: one three-tier framework, read from both sides
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

#### Tier 1 — Baseline capability (new this round)
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

#### Tier 2 — Implicit capability
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

#### Tier 3 — Explicit capability
- **What it is:** a standalone requirement, declared directly, NOT
  derived from any codec field — the user states the protocol behavior
  they want by name.

Tier 3 splits into TWO sub-shapes on the adapter-author side — both are
still Tier 3 from the DECLARING side (standalone, not field-derived);
they differ only in how an adapter author fulfills them. Conflating the
two (found while reviewing how the already-shipped `DeadLetter` pattern
fits this framework) was a real gap in this doc's own vocabulary, now
closed:

##### Tier 3a — Sealed, adapter-owned
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

##### Tier 3b — Shared-declaration, universally-realizable
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

#### Why this is one framework, not three separate rules
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

### Relationship to D-0006 — reused mechanism, NOT reopened value-sharing decision
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

### Implementation approach: phased design, implement, document, and learn
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

#### Phase 1 — `api/events` (rewrite the shipped D-0006 mechanism)
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

#### Phase 2 — `api/reqreply` (apply the mechanism, reusing all 4 existing capability values)
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

#### Phase 3 — `api/rest` (a new, synchronous, transport-stateless adapter)
**Status: CAPABILITY MECHANISM SHIPPED.** Per an explicit scope
narrowing (this doc's implementation execution deliberately excludes
the new `adapters/zeromqrest` adapter build — see the scope-split note
below, now further refined): Design finalized, Implemented, tested,
documented, and verified (`go fmt`/`go build ./...`/`go test ./...`/
`just check`/all examples all green) for the CAPABILITY MECHANISM half
only. The new ZeroMQ REQ/REP adapter itself is NOT part of this
roadmap's own phase count or Implement scope — it remains entirely
tracked, independently, in
[`docs/roadmap/zeromq-rest-adapter.md`](../roadmap/zeromq-rest-adapter.md), free to
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
  [`docs/roadmap/zeromq-rest-adapter.md`](../roadmap/zeromq-rest-adapter.md), a
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
      `docs/design/d-0003-codec-declared-middlewares.md`'s Addendum 3
      (originally a standalone roadmap doc spun out this round after
      finding `HandleMW`/`ClientMW` are hard-coded to the LEGACY concrete
      type, not the shared `RouteMiddleware` interface; later merged into
      D-0003 once resolved), meaning Security enforcement and codec-backed
      param merging are two genuinely different mechanisms today, not a
      redundant duplication — Phase 3 does NOT block on that doc's
      outcome either
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

#### Phase 4 — `api/events`: promote capability APPLICATION from adapter-owned loops to an API-layer-owned dispatcher
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

##### Phase 4b — closing the guardrail gaps found in a post-hoc audit
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

##### Phase 4c — closing the `Client.Publish`/`Client.Subscribe` Capabilities gap
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

##### Phase 4d — Attach factory redesign: adapters expose `New*Transport` factories; attaching is EXCLUSIVELY an api-layer method
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

##### Phase 4e — closing the REMAINING `Client.Publish`/`Subscribe` "v1 scope" gaps
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

#### Phase 5 — closing `adapters/mqtt` (v3)'s Phase 4 pub/sub Capability parity gap, and applying the same `Apply`/`ApplyCapabilities` shift to `api/reqreply`'s mqtt5/zeromq call sites
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

##### Phase 5a — moving the type-safe escape hatch itself onto the API layer (formerly "Phase 4f")
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

#### Phase 6 — `api/rest`: the SAME shift for Header/Cookie/Query
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

#### Phase 6a — closing the SSE `CheckParamKindCoverage` gap, and bringing `adapters/websocket` into the same real-interface mechanism
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

#### Phase 7 — Observer + ErrorPattern as interface-level cross-cutting concerns
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

#### Phase 8 — Review & Closeout (not a feature phase)
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
  - 2.2 — `api/events` — **DONE (Round DR10).** Found an EVEN LARGER
    problem than 2.1: on top of the same stale-Attach-naming pattern
    (`mqtt.Attach`/`mqtt5.Attach`/`zeromq.Attach` → `NewTransport(...)` +
    `client.Attach(...)`), Phase 5's "zero backdoor" redesign REMOVED
    positional `qos`/`retained` call-time parameters from
    `NewSubscribeTransport`/`NewPublishTransport` entirely (replaced by a
    `Capabilities []Capability` field), leaving many docs with literally
    non-compiling example code — including `docs/features/events.md`'s
    very first "Declaring channels" example calling `.Register(client)`
    on a `Channel[T]` value, which has never had that method (only
    `Subscriber[T]`/`Publisher[T]`, reached via `.WithSubscribe(...)`/
    `.WithPublish(...)`, have `Register`/`Handle`). Also found a false
    "v1-scoped, does NOT enforce SubscribeMW" claim about
    `mqtt5.Attach + Client.Subscribe` (verified full-featured via
    `adapters/mqtt5/transport.go`'s own doc comment) and stale
    `SecurityFunc`/`CredentialFunc` field references (removed, replaced
    by security-shaped `SubscribeMW`/`PublishMW`). Fixed across
    `api/events/{doc.go,builder.go}`, every `adapters/{mqtt,mqtt5,
    zeromq}` doc.go/godoc, `docs/features/{events,asyncapi,ports,
    error-handling,observer,security,redis}.md`, `docs/guides/{mqtt,
    mqtt5,observer,stream,error-handling}.md`, `docs/concepts/{codec-as-
    contract,observable-layers,ports-and-adapters,pipelines}.md`,
    `docs/what-is-go-codex.md`, and `examples/gob-contract/main.go`'s
    stale comment. See `.github/skills/review-docs/references/
    history.md`'s Round DR10 for the full finding list.
  - 2.3 — `api/reqreply` — **DONE (Round DR11).** Found the SAME
    stale-Attach-naming root cause as 2.1/2.2 (`AttachServer`/
    `AttachClient`/`AttachRouterServer`/`AttachDealerClient`/standalone
    `Serve`/`Call`/`ServeRouter`/`CallDealer` all removed, replaced by
    `NewServerTransport`/`NewClientTransport`/`NewRouterServerTransport`/
    `NewDealerClientTransport` + `Server.Attach`/`Client.Attach`) —
    plus an entire dead "escape hatch" guide section in each of
    `docs/guides/{mqtt5,zeromq}.md` teaching calls to functions that no
    longer exist anywhere in the codebase. Also found and fixed a false
    "v1 scope, NOT honored" doc claim in
    `adapters/zeromq/reqreply_transport.go` (verified the code actually
    DOES honor `RequestFormats`/`Formats`/`ErrorPattern` — the doc was
    simply never updated after that work shipped), plus a self-caught bug
    where an initial fix attempt invented a nonexistent
    `reqreply.ClientCallOptions.Vars` field (real mechanism: per-call
    template vars are derived from the request struct via
    `reqreply.NewTopicParam` merge fields). See
    `.github/skills/review-docs/references/history.md`'s Round DR11 for
    the full finding list.
  - 2.4 — shared/cross-cutting surfaces (README, project-structure.md,
    zensical.toml nav, go-codex.instructions.md, docs/index.md,
    get-started.md, reference/index.md) — **DONE (Round DR12).** Found:
    a real compile-error bug in README's own Layer-2 code sample
    (`handle, _ := createUser.Register(builder)` — `Register` returns
    only `error`, not `(handle, error)`; fixed to `RegisterHandle`);
    `zensical.toml` nav pointing at 4 nonexistent files (1 renamed —
    `features/reqreply-middleware.md` → `features/codec-declared-
    middleware.md`, an orphaned file with zero nav entry — and 3 fully
    dead roadmap entries removed) plus 1 orphaned real file
    (`roadmap/idea-codec-defined-hateoas.md`) with no nav entry or
    roadmap/index.md row, both added; `docs/reference/index.md`
    repeating the SAME stale `api/reqreply`/`adapters/mqtt5`/
    `adapters/zeromq` Register/Serve/Call/ServeRouter/CallDealer bugs
    2.2/2.3 already fixed elsewhere; `middleware` and 5 real adapters
    (`mcprest`/`openai`/`file`/`redis`/`websocket`) missing from
    README's and reference/index.md's directory/import tables despite
    already being documented in go-codex.instructions.md's canonical
    Package Structure table; `middleware` package missing `doc.go`
    entirely (created one) and `adapters/file`'s package doc living in
    `binding.go` instead of `doc.go` (moved, mechanical). Also applied
    targeted, high-confidence fixes to
    `.github/instructions/go-codex.instructions.md`'s `api/reqreply` row
    (same stale-Attach-naming class as 2.2/2.3) WITHOUT attempting a
    full historical-narrative rewrite — that remains explicitly scoped
    to the separate `design-doc-compaction.md` roadmap. See
    `.github/skills/review-docs/references/history.md`'s Round DR12 for
    the full finding list. **Phase 8 item 2 (review-docs, all 4
    sub-items) is now fully complete.**
- **Joint declarative-workflow walkthrough + 3 per-API tutorial
  skills — SPUN OUT into their own roadmap doc,
  [`docs/roadmap/declarative-workflow-tutorials.md`](../roadmap/declarative-workflow-tutorials.md),
  Design draft.** Mirrors how `design-doc-compaction.md` and
  `mqtt5-capability-extensions.md` were already spun out above — both
  remaining Phase 8 items (the human-in-the-loop walkthrough declaring
  one `api/rest`/`api/events`/`api/reqreply` API end-to-end, and the 3
  `tutorial-api-{rest,events,reqreply}` skills it feeds) are now planned
  in full detail there, not inline here. See that doc for the exact
  walkthrough script, the shared tutorial-skill shape, and its own Open
  design decisions.
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
  [`docs/roadmap/design-doc-compaction.md`](../roadmap/design-doc-compaction.md) —
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

### Scope decisions
| In scope (this doc) | Out of scope / deferred |
|---|---|
| **Phase 1** — Renamed/redesigned requirement type for events (`CapabilityRequirement`, was `CapabilitySpec`) + value-aware `LeveledCapability` + sugar helpers (`events.RequireQoS(events.AtLeastOnce)`, `RequireRetained`, `RequireHWM`, `RequireConflate`), rewriting D-0006's shipped mechanism into the three-tier vocabulary — BREAKING, deliberately (see Open Design Decision #7) | Any new shared capability VALUE type spanning adapters (rejected by D-0006, not reopened here) |
| **Phase 2** — Generalizing the declare-then-verify PATTERN to `api/reqreply` (own `reqreply.CapabilityRequirement` + coverage check) AND reusing all 4 ALREADY-SHIPPED capability values (`mqtt5.QoS`/`Retained`, `zeromq.HWM`/`Conflate` — zero new adapter types, per Phase 2's Design finding) | Any NEW capability VALUE beyond D-0006's existing survey (Message Expiry, Shared Subscriptions, AMQP addressing/dead-lettering remain exactly as speced in D-0006 section 6 — this doc only sketches what a PRESET for them would look like once/if they ship) |
| **Phase 3** — A NEW, synchronous, transport-stateless ZeroMQ REQ/REP adapter for `api/rest`, governed by the REST-eligible-transport guardrail | Ever giving `api/rest` an MQTT (v3/5) adapter — permanently excluded by design, not deferred; that shape belongs to `api/reqreply` |
| Adapter-owned preset/bundle constructors (e.g. `mqtt5.PresetReliableWithHeaders()`) bundling multiple existing `Capability` values + a matching `CapabilityRequirement` set in one call | Merging/consolidating `api/rest` and `api/reqreply` — considered settled as permanently separate APIs (sync/stateless vs. async/broker-mediated), even where both touch ZeroMQ |
| Compile-time vs. runtime enforcement — explicitly documented as staying RUNTIME (like today) for Phases 1-2; becoming genuinely runtime-checked for REST's implicit capabilities once Phase 3 ships a second transport family | Solving compile-time requirement-composition (would need reflection or a closed capability enum; not attempted) |
| Classifying/documenting EXISTING capabilities (`rest.HeaderParam`/`CookieParam`, `mqtt5.UserPropertyParam`) against the three-tier guardrail, AND naming/documenting the **Baseline** tier (Route/Topic) + the dual (declaring-user / adapter-author) framing for ALL THREE tiers — both descriptive-only, no new code, since Baseline is already enforced via `ports.RESTPattern`/`EventPattern`/`ReqReplyPattern` + `SourceAdapter`/`SinkAdapter`/`IOAdapter` | Redesigning `ports.Pattern`/`SourceAdapter`/`SinkAdapter`/`IOAdapter` themselves — they already do their job; this doc only gives their existing role a name in this vocabulary |

### Sketched API surface
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

### Structured errors
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

### Observer integration
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

### Unit test plan
**Phase 1's and Phase 2's full test matrices are FINALIZED — see their
own subsections under "Implementation approach" above.** Phase 3 sketch
below, still open:

| Test | Verifies |
|---|---|
| `mqtt5.PresetReliable()`/`PresetReliableWithHeaders()` return the same `SubscribeOptions` a caller would hand-assemble | Preset correctness, no hidden extra behavior |
| `nil` Observer / plain Observer (no `CapabilityObserver`) → no panic on the new REST/ZeroMQ adapter's attach path | Observer guard correctness |

### Files to create
**Phase 1's and Phase 2's full file lists are FINALIZED — see their own
subsections under "Implementation approach" above.** Phase 3 sketch,
still open:

| File | Responsibility |
|---|---|
| `adapters/mqtt5/preset.go` | `PresetReliable`, `PresetReliableWithHeaders` (and equivalents for `adapters/mqtt`/`adapters/zeromq` if the pattern proves useful there) |
| `docs/features/capabilities.md` | Updated in Phase 1/2 already; extended again once Phase 3 ships |

### Out of scope (this doc, until resolved elsewhere)
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

### Open design decisions (to resolve before/during implementation)
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

### See also
- [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md) — the shipped mechanism this doc extends, and the two-part test that scopes what stays adapter-owned
- [D-0006 §3 — "Relationship to D-0003"](../design/d-0006-protocol-native-capabilities.md#3-relationship-to-d-0003--resolved-via-a-4-stage-lifecycle-model-confirmed-via-a-fifth-throwaway-go-prototype) — the prototyped 4-stage lifecycle model (declare → capability-declare → handler-attach → adapter-attach) this doc's "cross-cutting alignment" note (under "Implementation approach" above) re-applies to Phases 2 and 3, not just events
- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) — `Middleware[In,Out]`/`Transform`/`ClientTransform`/`.Use(mw)`, already shipped for all 3 APIs this doc's phases touch
- [`docs/features/capabilities.md`](../features/capabilities.md) — the current, shipped-only reference page
- [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md) — precedent for placing a shared mechanism (`middleware.Disposition`) outside `api/events` specifically so `api/reqreply` can reuse it dependency-free
- `.github/skills/add-a-new-adapter/SKILL.md`'s Step 5e — the existing MANDATORY sealed-`Capability` requirement for adapter authors, which this doc's Tier 3 (explicit) adapter-author framing builds directly on top of

## Test plan (once implementation begins)

- Sealed per-adapter `Capability` interface — the compile-time contract is
  the primary thing under test:
  - Positive: a capability declared by adapter `mqtt5` compiles and is
    accepted by `mqtt5.Attach`/`mqtt5.PublishAttach`/`SubscribeAttach`.
  - Negative: attaching an `mqtt5`-only capability (e.g. a User Properties
    capability) to an `mqtt` (v3) client — captured as a DOCUMENTED
    compiler-error string (a `// want` comment or build-failure fixture),
    not a runtime `error` value, since the whole point is the mismatch
    never reaches a running process. Mirrors how `ports.Pattern` mismatches
    are already tested today.
  - Same positive/negative pair repeated for the AMQP role split:
    `Persistent`/`PublisherConfirms` accepted by `PublishAttach`, rejected
    (compile error) if passed to `SubscribeAttach`; `AckMode` accepted by
    `SubscribeAttach`, rejected if passed to `PublishAttach`.
- `Address` parameterization (`Channel[Addr,T]`, §2.3/§5.3):
  - `NewChannelFromTopic(topic, codec, ...)` — confirms zero explicit
    type-parameter brackets at the bare-string call site (the ergonomics
    finding the addressing spike already confirmed).
  - A hypothetical AMQP-shaped `Address` (exchange/routing-key/queue)
    compiling against the SAME `Channel[Addr,T]` shape and dispatching
    correctly through `PublishAttach`/`SubscribeAttach`.
- Handler Disposition (§8) — the ctx-mutable-sink round trip:
  - A handler that calls `SetDisposition(ctx, DispositionNackDiscard)`
    (or `Ack`/`NackRequeue`) — confirm the adapter's own
    `ResolveDisposition` call, made AFTER the handler returns, reads back
    the SAME value the handler set.
  - Default-when-unset case: a handler that returns `nil` and never calls
    `SetDisposition` — confirm `ResolveDisposition` falls back to the
    documented default (`Ack`), and a handler that returns a non-nil error
    without calling `SetDisposition` falls back to `NackRequeue`.
  - Confirm the SAME `Disposition` mechanism works unchanged across BOTH
    `events` and `reqreply` (the reuse claim §8 makes, not just an
    events-only test).
- `DispositionObserver` (Review-8) — purely additive behavior:
  - An `Observer` implementation that does NOT implement
    `DispositionObserver` — confirm no panic, no behavior change, and
    `RecordSubscribe`'s existing `success bool` is computed exactly as
    before (tied only to the handler's own returned error).
  - An `Observer` implementation that DOES implement
    `DispositionObserver` — confirm `RecordDisposition(topic,
    disposition)` fires AFTER `ResolveDisposition` resolves the final
    outcome, with the correct resolved value, and that `RecordSubscribe`'s
    `success bool` is UNCHANGED alongside it (the two calls answer
    different questions, confirmed not to conflict).
- §3's 4-stage lifecycle model (declare → capability-declare →
  handler-attach → adapter-attach):
  - A `Middleware[In,Out]` and a `Capability` both attached to the same
    channel/route, both contributing to the SAME generated spec (AsyncAPI/
    OpenAPI) entry, confirmed additive — neither overwrites the other's
    spec contribution, and neither is silently dropped.
  - A `CapabilitySpec`+drift-check test: confirm a capability's declared
    spec contribution is checked against what the adapter actually attaches
    at bind time, and a deliberately mismatched fixture is caught as a
    drift failure, not silently accepted.
- Shared ground with [D-0003](../design/d-0003-codec-declared-middlewares.md):
  this doc does not duplicate D-0003's own "### Test plan" bullets for
  `Middleware[In,Out]`/`Transform`/`ClientTransform` construction and
  dispatch — those stay owned by D-0003's test plan; this section only
  covers test cases specific to `Capability`/`Address`/`Disposition`, the
  concepts this doc actually introduces.

## Final implementation phase — sync `go-codex.instructions.md` and `review-go-codex` (once shipped)

**Not part of implementation itself — the LAST step, only once `Capability`/
`Address`/`Disposition` are actually built, tested (per the Test plan
above), and merged.** Two interim documents already give adapter authors
guidance in the meantime and are NOT touched by this phase:
`docs/concepts/ports-and-adapters.md`'s "Guardrail: adapters as pure
protocol shims" section (states the target design ahead of its own
implementation, as a guardrail against inventing a competing ad-hoc
mechanism) and `add-a-new-adapter/SKILL.md`'s Step 5e (tells a
new-adapter author today not to bolt a protocol-specific field onto an
`api/*` declaration type). Both are deliberately framed as "not yet
shipped" — appropriate for PREP guidance, but NOT a substitute for
updating the project's actual sources of truth once the mechanism is
real. That update is this phase:

1. **`.github/instructions/go-codex.instructions.md`** — add a Design
   Philosophy bullet describing the SHIPPED sealed `Capability` mechanism
   in PRESENT tense (no "forward-looking"/"not yet shipped" hedging by
   this point), alongside the existing "Adapters are thin"/"a user works
   ENTIRELY in the `api/*`/`ports` abstraction" bullet pair — mirrors how
   every other graduated design doc (D-0001 through D-0005) updated this
   file at its own point of shipping.
2. **`review-go-codex/references/checklist.md`** — add a new numbered
   section (e.g. "§15. Adapter Thinness & Capability Guardrail") auditing
   REAL, post-ship conformance: confirm `api/events/mqtt_qos.go` was
   ACTUALLY DELETED (not merely documented as pending removal), and flag
   any `api/*` package that still carries protocol-specific vocabulary
   after this point as a genuine finding — plus a matching Gotchas-list
   pointer bullet in `review-go-codex/SKILL.md` (mirroring the existing
   "Design guardrail: adapters implement wire protocols only..." bullet's
   format, which points at checklist.md §13 rather than restating it
   inline).
3. **`add-a-new-adapter/SKILL.md`'s Step 5e** — reword from
   "forward-looking... preparing for a future, not-yet-shipped design" to
   a MANDATORY, present-tense requirement; drop the "until it ships"
   interim-guidance framing entirely.
4. **`docs/concepts/ports-and-adapters.md`**'s "Guardrail: adapters as
   pure protocol shims" section — drop the "not yet implemented"/"once
   `Capability` ships" hedging from its "Already shipped vs. genuinely
   new" framing once the mechanism it describes is real.
5. **`review-go-codex/references/history.md`** — append a new Round entry
   recording this sync itself (mirrors how every other graduated design
   doc's own instructions-file/skill sync is recorded there today).

This document's own implementation is NOT complete until this phase
runs — a `Capability`/`Address`/`Disposition` merge that skips it leaves
the project's sources of truth silently out of date, the exact failure
mode `docs/design/d-0001-rest-middleware-workflow-simplification.md`'s
own "Lessons Learned" section warns is invisible to
`go build`/`go vet`/`go test`/staticcheck.

## See also

- `docs/roadmap/common-middleware-architecture.md` (now deleted, its
  finding fully absorbed into D-0003) —
  already superseded once by d-0003, and this doc's own EARLIER "Option B:
  subsume D-0003" framing (superseded in turn — see §3) never actually
  reopened it a second time; §3's confirmed 4-stage lifecycle model is the
  CURRENT resolution instead — `Capability` and `Middleware[In,Out]` share a
  lifecycle STAGE, not a Go type.
- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) —
  the currently-SHIPPED mechanism this doc's `Capability` mechanism (§2)
  coexists ALONGSIDE, per §3's confirmed 4-stage model — NOT a mechanism it
  subsumes or migrates (that was an earlier, now-superseded framing); both
  remain the accurate, current description of their respective shipped/
  planned code.
- `mqtt5-user-property-merge.md` (retired) — its own "registration
  surface... NOT resolved" question is answered by §5.2 above; its own
  motivating gap turned out to be a fixable bug in already-shipped code,
  see [Feature: Event Channels](../features/events.md#codec-backed-middleware-subscribemwpublishmw).
- [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md) — its
  `Client`/`Server`/`Attach` rework was the prerequisite this doc's
  original finding needed to re-evaluate Response Topic/Correlation Data
  against; now shipped, and the re-evaluation DECIDED it stays implicit,
  NOT a declared `Capability`/`Feature` (see §6's own updated entry).
- [MCP and Ports Declarative Middleware](../roadmap/mcp-ports-declarative-middleware.md)
  (formerly `declarative-middleware.md`) — its own unshipped
  `ports.File[T]` sketch is the basis for §5.6's worked example.
- `docs/concepts/api-contracts.md` — the "one struct, one call" principle every
  worked example in §5 is checked against for non-regression.
