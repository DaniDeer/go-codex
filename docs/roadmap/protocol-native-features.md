# Feature — sealed, per-adapter capability interfaces for protocol-native declarations

> **Status:** Idea only — no driver yet, no code written. This doc has GROWN
> across several rounds: from its original narrow scope (a pub/sub-only
> `ProtocolFeature` sealed interface) to a cross-boundary design built around
> an OPEN `Feature`/`Provider` primitive (string-based capability matching),
> to its CURRENT mechanism — a SEALED, per-adapter capability interface
> (mirroring `ports.Pattern`'s own already-proven technique) with capabilities
> supplied at `Attach`/bind time, not baked into a channel/route's own
> declared type. **This pivot happened because the open, string-ID-based
> `Feature`/`Provider` primitive could not guarantee Go compile-time errors
> the way today's sealed `RouteOpt`/`ChannelOpt` already do — and compile-time
> safety is a non-negotiable requirement, not a nice-to-have, for this
> redesign.** Read §2 for the current mechanism; §1 still holds as prior-art
> analysis (why today's mechanisms are each partial); some of §5's worked
> examples and §7's open questions have been updated to match the current
> mechanism, others remain from earlier rounds where still accurate. **§8
> adds a DISTINCT, complementary concept — Handler Disposition** —
> resolving how a handler's PER-MESSAGE RUNTIME outcome (e.g. an AMQP
> ack/nack/requeue decision) gets abstracted through the API layer,
> separate from `Capability`'s declare-time configuration.
>
> **Relationship to already-SHIPPED designs — stated up front, not buried:**
> §3 RESOLVES (does not merely propose) the relationship to
> [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)
> (`middleware.Declaration[In,Out]`, `rest.Middleware[In,Out]`/
> `events.Middleware[In,Out]`, `Transform`/`ClientTransform`/`.Use(mw)`) —
> ALREADY IMPLEMENTED, tested, and documented as current: both mechanisms
> occupy the SAME lifecycle stage (declare-time, spec-contributing),
> confirmed via a 4-stage model, WITHOUT merging into one Go type. Nothing
> in `api/rest`/`api/events`/`middleware` changes as a RESULT of this doc
> alone; d-0003 remains the accurate, current description of shipped code
> until a SEPARATE implementation-planning round executes any migration.
> Breaking changes are explicitly accepted as a possibility for that
> future round (see the repo owner's own framing of this rethink: "we can
> make breaking changes if we can achieve these goals more easily").
>
> **Supersedes** the open question in
> [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md)
> (already superseded once, by d-0003) for the specific finding it raised
> (a single shared `middleware.Middleware` struct carrying REST-only
> fields) — §3's confirmed 4-stage model is now the long-term resolution
> (both `Middleware[In,Out]` and `Capability` are stage-2 declarations,
> not a single merged type).
>
> **The formerly-open "registration surface... NOT resolved" question a
> now-retired sibling roadmap doc (`mqtt5-user-property-merge.md`) raised
> is ANSWERED TWO WAYS today**: this doc's own §5.2 sealed `mqtt5.Capability`
> design is one answer (still unimplemented); the OTHER, ALREADY-SHIPPED
> answer is
> [D-0003](../design/d-0003-codec-declared-middlewares.md)'s own Addendum
> (folded in from the now-deleted `reqreply-codec-declared-middleware.md`
> roadmap doc) —
> the "property" vocabulary axis (`WithRequestProperty`/`WithResponseProperty`
> for `api/reqreply`, `WithSubscribeProperty`/`WithPublishProperty` for
> `api/events`), built directly on D-0003's ALREADY-SHIPPED
> `Middleware[In,Out]` mechanism — API-level, not adapter-owned, narrower
> in scope than this doc's `Capability` primitive, and NOT competing with
> it. That retired doc's OWN motivating gap (direct, Middleware-free
> attachment silently failing to merge) turned out to be a genuine BUG in
> already-shipped code (`MergedPropertyParam[T].applyChannel`/`applyRoute`
> not registering the merge field), now fixed — see
> [Feature: Event Channels](../features/events.md#codec-backed-middleware-transformclienttransform)
> for the current, correct behavior (see §5.2.1 for the full relationship:
> Phase 1b's validate-only bridge, this doc's own planned `Capability`,
> and the property axis).
>
> **Response Topic/Correlation Data — DECIDED, closed**:
> [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md)'s
> `Client`/`Server`/`Attach` rework (the prerequisite this doc's original
> finding was waiting on) has SHIPPED, and re-evaluating against the real
> `Attach` shape settled the question: Response Topic/Correlation Data
> stays an IMPLICIT, always-on characteristic of `mqtt5`'s reqreply
> transport, NOT a declared `Capability` (see §5.2's own list and
> d-0004's "Relationship to `protocol-native-features.md`" section for
> the full reasoning) — every mqtt5 reqreply route needs it
> unconditionally, with no opt-out scenario to gate. Shared
> Subscriptions remain this doc's own genuine `Capability` candidate
> from the same MQTT5 feature cluster, undecided and tracked
> independently here.
> [← Back to Roadmap](index.md)

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

### 2.2 Capabilities are supplied at `Attach`/bind time — not baked into a channel's own declared type

```go
// package mqtt5
func Attach[T any](client *Client, ch events.Channel[T], caps ...Capability) error
```

A caller wanting QoS or User Property support supplies it HERE, at the
adapter-specific binding call — NOT at `events.NewChannel(...)` (which stays
exactly as it is today, fully protocol-agnostic, zero adapter import
required). Passing a `zeromq`-defined capability to `mqtt5.Attach` is a
**Go COMPILE ERROR** — the value simply does not satisfy the `caps
...Capability` parameter's type constraint — with NO custom error type
needed at all (the compiler's own diagnostic IS the error), and NO
`Provider`/`Supports`/boolean check anywhere in the design. This is the
concrete mechanism realizing the "tick a box" model from your own framing:
an MQTT v3 client simply has no `mqtt.Capability`-satisfying type for User
Properties to begin with — there is nothing to "not tick," the capability
literally cannot be constructed against that adapter.

The SAME `events.Channel[T]` value stays attachable to MULTIPLE adapters,
each supplying its own capabilities (or none) — `mqtt5.Attach(mqtt5Client,
ch, mqtt5.QoSAtLeastOnce)` and, separately, `zeromq.Attach(zeromqClient,
ch)` both remain valid for the identical declared channel — preserving
"declare once" more cleanly than an earlier (now-superseded) proposal in
this doc that considered adapter-specific DERIVED WRAPPER TYPES requiring a
throwaway value per adapter.

### Why adapter-owned capability declaration doesn't violate the thin-adapter, protocol-agnostic-declaration principle

> **See also — [Thin Adapters Audit](thin-adapters-audit.md)**, the
> MIRROR-IMAGE investigation to this section: instead of asking "does a
> NEW protocol-native capability clear the bar for core-layer, protocol-
> agnostic declaration" (this section's question), that doc asks "does
> EXISTING adapter-owned dispatch logic ALREADY clear that bar, unnoticed,
> and is therefore misplaced today." It reuses the exact two-part test
> below and confirms 3 concrete findings that clear both bars (the same
> reasoning that puts `Security` in core here) plus 1 nuanced overlap case.

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
ONE real method — `Template() string` — not a bare marker:

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

func Attach[T any](client *Client, ch events.Channel[T], caps ...Capability) error

// adapters/mqtt5 — a SEPARATE sealed Capability + QoS type, deliberately
// NOT shared with adapters/mqtt's own (even though the numeric values are
// identical, 0/1/2) — a future divergence between the two protocols' QoS
// semantics would not require touching a shared type at all, and the
// sealing itself already prevents cross-adapter mixups regardless.
type Capability interface{ isMQTT5Capability() }

type QoS byte
func (QoS) isMQTT5Capability() {}
const (QoSAtMostOnce QoS = 0; QoSAtLeastOnce QoS = 1; QoSExactlyOnce QoS = 2)

func Attach[T any](client *Client, ch events.Channel[T], caps ...Capability) error
```

`api/events` itself carries NOTHING mqtt-specific anymore — `mqtt_qos.go` is
DELETED. A caller declares the base channel exactly as today
(`events.NewChannel(topic, codec, opts...)`, zero adapter import), then
supplies QoS AT ATTACH TIME: `mqtt5.Attach(client, ch, mqtt5.QoSAtLeastOnce)`.
Attempting `zeromq.Attach(zeromqClient, ch, mqtt5.QoSAtLeastOnce)` **does
not compile** — `mqtt5.QoS` does not implement `zeromq.Capability` (different
unexported marker method, different package) — the Go compiler rejects it
before the program can even be built, let alone run.

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
[Feature: Event Channels](../features/events.md#codec-backed-middleware-transformclienttransform)) —
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

Supplied at `mqtt5.Attach(client, ch, mqtt5.NewUserProperty[AuthIn](...)
.WithMergeField(...))` — NOT baked into the channel's own declared type.
`adapters/mqtt` (v3) has no `isMQTT5Capability()`-implementing type for User
Properties AT ALL — there is no `mqtt.UserProperty` to construct in the
first place, so the mismatched combination is simply UNWRITABLE, not just
fast-failing. Attempting `mqtt.Attach(v3Client, ch,
mqtt5.NewUserProperty[AuthIn](...))` **does not compile** — the value
doesn't satisfy `mqtt.Capability`.

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
[Feature: ReqReply Codec-Declared Middleware](../features/reqreply-middleware.md)
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

`docs/roadmap/declarative-middleware.md`'s UNSHIPPED `ports.File[T]` sketch
(kept there, not duplicated here) proposes a `RequireScopes[T]` decorator
wrapping `Read`/`Write` — a security-shaped `Fn` extracting grants, merged and
checked ONCE via `middleware.CheckScopes`, attached directly at the
`Read`/`Write` call site.

**§2's sealed-`Capability`-supplied-at-`Attach`-time mechanism presupposes a
separate BINDING step** (`mqtt5.Attach(client, ch, caps...)`) distinct from
the channel/route's own declaration — `ports.File[T]` has NO equivalent
"attach" phase at all: its `Read(ctx, vars, opts)`/`Write(ctx, vars, v,
opts)` methods ARE the only call site, called directly, with no separate
binding step to supply capabilities at. Forcing §2's mechanism onto
`ports.File` would require EITHER inventing a new binding step `ports.File`
doesn't otherwise need, or attaching capabilities via `opts` instead
(`Read(ctx, vars, opts, caps...)`) — structurally different from every
other worked example in this section.

**Left genuinely open here, not resolved — but no longer undriven.** This
doc does not attempt to force a fit for §2's Attach-time `Capability`
mechanism onto `ports.File`/`Cache`/`SQL`/`Dir`, since they structurally
lack the separate bind/`Attach` step that mechanism requires. That
conclusion stands. What HAS changed: whether these ports need SOME
cross-cutting-concern mechanism at all is no longer an open question
without a driver — the driver is the library's UX North Star
(declarative/simple/consistent workflow), and it is already being
pursued, as its own decorator-shaped design, in
[Declarative Middleware](declarative-middleware.md)'s remaining `ports`
scope. That doc, not this one, is where `ports.File`/`Cache`/`SQL`/`Dir`'s
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
  capabilities — ADVANCED (not fully resolved) by §3's 4-stage model.**
  §3's CONFIRMED Candidate 3 (`DeclareCapabilitySpec`, a decoupled,
  adapter-agnostic sibling value declared BEFORE `Attach`) gives spec
  rendering a concrete HOOK POINT it didn't have before — spec generation
  can consume the stage-2 `CapabilitySpec` list without needing to know
  the adapter at all, resolving the "isn't even part of the channel's
  own declared value" objection this bullet originally raised. STILL not
  decided: the EXACT rendering mechanism once a `CapabilitySpec` exists
  (a vendor-extension field, e.g. AsyncAPI's `x-mqtt5-qos`; a generic
  "capabilities" array in the spec; or something else) — that wiring is
  left for the dedicated implementation-planning round, not designed
  here. Shared Subscriptions (§6) remains the sharpest example of why
  spec-visibility matters (delivery-semantics-changing, not just
  metadata).
- **Whether/how D-0003 relates to this mechanism — RESOLVED this round,
  see §3.** No longer "reopened, not decided" — §3 now gives a concrete
  answer (both are stage-2 declarations, sharing a lifecycle stage, not
  merged into one Go type) via the confirmed 4-stage model. Kept as a
  bullet here only as a pointer, not a live open question anymore.
- **[Review-11, Low] Discovery — RESOLVED this round: not a real gap,
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

- **[Review-1, High] Stringly-typed capability matching — RESOLVED this
  round.** The `Feature.FeatureID() string` + runtime `Provider.Supports`
  mechanism this finding was about no longer exists. §2's sealed
  `Capability` interfaces give the SAME compile-time exhaustiveness
  `RouteOpt`/`ChannelOpt` already have — no string IDs, no collision risk
  (Go's own package-scoped unexported-method visibility rules are the
  enforcement, not a naming convention).
- **[Review-2, High] `Provider.Supports` boolean-only — RESOLVED/moot this
  round.** No `Provider` interface exists anymore. "Does this adapter
  support X" is now answered by "does a `Capability`-satisfying value for X
  exist in this adapter's package at all" — a category question the Go
  compiler answers, not a boolean runtime call. (Partial/graduated support
  within ONE capability, e.g. an adapter recognizing SOME but not all
  AckMode values, would still need its OWN validation inside that
  capability's own construction — orthogonal to this finding, not
  reopened.)
- **[Review-3, Medium] Security shows near-zero benefit — RESOLVED this
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
- **[Review-4, Medium] `NewChannel`'s breaking change under-analyzed —
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
  places across the repo. One mitigating, but NOT yet verified, factor:
  the overwhelming majority of the 272 real call sites follow one
  mechanical shape (`events.NewChannel[T](topic, codec, ...)` →
  `events.NewChannelFromTopic(topic, codec, ...)`), suggesting a scripted
  codemod is plausible — this has only been confirmed by visual pattern
  inspection, not by actually running a migration tool, so it stays a
  hypothesis, not a resolved sub-question.
- **[Review-5, Medium] Transport lock-in — RESOLVED/reframed this round.**
  Supplying a capability at `Attach` time (not baked into the channel's own
  declared type) means the lock-in is now EXPLICIT and LOCAL to that one
  `Attach` call site — the underlying `events.Channel[T]` value itself
  remains fully portable and can be attached elsewhere, with different (or
  no) capabilities, without any special handling.
- **[Review-6, Medium] Package placement unreconciled — RESOLVED/moot this
  round.** No new shared package is needed at all — each adapter's
  `Capability` interface lives in its OWN existing package (`mqtt5`, `mqtt`,
  `zeromq`, a hypothetical `amqp`), the same way `ports.Pattern`'s technique
  lives entirely inside package `ports` itself.
- **[Review-7, Medium] `ports.Pattern`'s fate — PARTIALLY resolved this
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
  [Declarative Middleware](declarative-middleware.md)'s remaining
  `ports.File`/`Cache`/`SQL`/`Dir` scope (decorator-shaped cross-cutting
  concerns), NOT here. §5.6's structural observation stands unchanged:
  ports has no separate "Attach" binding step to hang a `Capability` off
  of the way REST/events do, so even with a real driver now identified,
  the RIGHT mechanism for ports is that doc's decorator shape, not an
  attempt to force this doc's `Capability` mechanism onto a boundary
  that structurally can't host it.
- **[Review-8, Low-Medium] `stats.Observer` integration — RESOLVED for
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
- **[Review-9, Low] No "Test plan" section — RESOLVED this round.** A
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
- **[Review-10, Trivial] Field-naming collision — RESOLVED/moot this
  round.** No `UnsupportedFeatureError` type is needed anymore — a
  capability mismatch is the Go compiler's own diagnostic, not a custom
  error type with fields to name.
- **[Review-12, Medium] Should EVERY `Capability` carry its own
  observable declaration, reducing bespoke adapter-side Observer wiring
  — FLAGGED this round, NOT investigated or resolved.** Spun out of a
  separate, broader review of the Observer pattern across the api layer
  (same session, same "thin adapter, thick api layer" principle) —
  confirmed via code that adapters ALREADY hand-roll their own
  capability-specific Observer calls today wherever a protocol feature
  has an observable runtime effect (e.g. `RecordSubscribe`/
  `RecordPublish`'s `success bool` says nothing about WHICH QoS tier
  was actually negotiated, whether a Retained flag was honored, or which
  Shared Subscription group handled a message — each adapter that wants
  this visibility must invent its own ad hoc reporting path, no shared
  mechanism exists). §8's `DispositionObserver` (Review-8, resolved)
  is a NARROWER, adjacent precedent — it solves ONE specific runtime
  outcome (ack/nack/requeue) for ONE specific concept (Handler
  Disposition), not capabilities in general.

  **The open question this bullet exists to scope, not answer:** should
  the sealed `Capability` interface (§2) itself carry an OPTIONAL,
  additive observability hook — e.g. a capability-supplied
  `RecordApplied(obs stats.Observer, ...)`-shaped method, or a NEW
  `stats.CapabilityObserver`-style interface mirroring
  `stats.SecurityObserver`/`DispositionObserver`'s exact type-assertion
  pattern (§8) — so that ANY capability (QoS, Retained, User Properties,
  a future AMQP ack-mode/persistence, Shared Subscriptions) gets a
  UNIFORM, declare-time-defined way to report its own runtime effect,
  instead of each adapter writing bespoke `obs.RecordX(...)` calls
  scattered through its own dispatch code for whatever capability
  happens to be attached. If resolved this way, the GENERIC dispatch
  code in `Attach`/`Serve`/`Subscribe` (adapter-owned) would only need
  to call ONE shared hook per capability, uniformly, regardless of which
  concrete capability type is present — matching the SAME "adapter
  calls one shared thing, doesn't hand-roll per-feature logic" shape
  a sibling Observer-pattern review already established for non-
  capability Observer reporting (`stats.ReportErrors`'s Param-error
  handling gap).

  **Explicitly NOT decided by this bullet:** the exact interface shape;
  whether this generalizes cleanly across QoS/Retained/User-Properties/
  Shared-Subscriptions/a-future-AMQP-adapter's ack-mode (their runtime
  "success" signals may not be uniform enough for one shape — needs a
  worked-example pass mirroring §5's own rigor before committing);
  whether it should live on the `Capability` interface itself (making
  ALL capabilities implement it, even ones with nothing meaningful to
  observe) or as a SEPARATE, optional, type-asserted interface a
  capability MAY additionally implement (mirrors `SecurityObserver`/
  `DispositionObserver`'s own "purely additive, never forced" precedent
  — likely the safer default given §8's own resolved recommendation
  favored optional/additive over baked-in every time it was tested).
  Needs its own dedicated worked-examples pass (mirroring §5) before any
  implementation — not scoped further here.
- **[Cross-doc, Medium] Dead-letter queue dependency on
  `d-0005-error-handling.md` — FLAGGED this round, MUST be
  re-checked before implementation begins.** §6's "AMQP dead-lettering"
  survey entry (added this round) already resolves the DESIGN question —
  dead-lettering is explicitly EXCLUDED from this document's `Capability`
  mechanism, because its declarative surface (`events.DeadLetter(topic,
  ...)`/`reqreply.DeadLetter(topic, ...)`) is meant to live in the CORE
  `api/events`/`api/reqreply` layer, shared uniformly across every
  adapter — NOT as a per-adapter sealed `Capability` type the way QoS/
  User Properties/Retained/AMQP addressing all correctly are. **Before
  implementing THIS document's `Capability` mechanism, re-check
  [`docs/design/d-0005-error-handling.md`](d-0005-error-handling.md)'s
  Topic 4 status**:
  - Do NOT fold dead-lettering into this document's implementation scope
    under any circumstances — it is a confirmed, permanent exclusion
    (§6), not a deferred/open item like the rest of this section.
  - If Topic 4 has NOT shipped yet by the time this document's
    `Capability` mechanism is implemented, there is NO blocking
    dependency — the two can proceed independently, since `DeadLetter`'s
    mqtt/mqtt5/zeromq realization needs no `Capability` plumbing at all
    (plain runtime publish, see Topic 4's "how DLQ works in practice"
    section).
  - HOWEVER, if/when a FUTURE AMQP adapter is ALSO built
    (`docs/roadmap/amqp-adapter.md`, a third, separate roadmap) and
    realizes `DeadLetter` via that document's pre-existing
    `QueueConfig.Args` field (`x-dead-letter-exchange`), double-check at
    that point whether this document's own `Attach`-time `Capability`
    supply mechanism and `amqp-adapter.md`'s queue-declare-time `Args`
    field end up BOTH trying to configure AMQP queue arguments through
    two independent paths — a coordination check, not a design conflict
    known to exist yet, since neither adapter is built.

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

## See also

- [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md) —
  already superseded once by d-0003; this doc's `Feature`/`Provider` model is
  now the intended LONG-TERM resolution instead (§3).
- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) —
  the currently-SHIPPED mechanism this doc's chosen direction (§3, Option B)
  intends to eventually subsume; remains the accurate description of shipped
  code until a separate implementation round executes the migration.
- `mqtt5-user-property-merge.md` (retired) — its own "registration
  surface... NOT resolved" question is answered by §5.2 above; its own
  motivating gap turned out to be a fixable bug in already-shipped code,
  see [Feature: Event Channels](../features/events.md#codec-backed-middleware-transformclienttransform).
- [D-0004 — ReqReply Workflow Simplification](../design/d-0004-reqreply-workflow-simplification.md) — its
  `Client`/`Server`/`Attach` rework was the prerequisite this doc's
  original finding needed to re-evaluate Response Topic/Correlation Data
  against; now shipped, and the re-evaluation DECIDED it stays implicit,
  NOT a declared `Capability`/`Feature` (see §6's own updated entry).
- [Declarative Middleware](declarative-middleware.md) — its own unshipped
  `ports.File[T]` sketch is the basis for §5.6's worked example.
- `docs/concepts/api-contracts.md` — the "one struct, one call" principle every
  worked example in §5 is checked against for non-regression.
