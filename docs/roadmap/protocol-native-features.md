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
> mechanism, others remain from earlier rounds where still accurate.
>
> **Relationship to already-SHIPPED designs — stated up front, not buried:**
> whether this doc's mechanism should eventually SUBSUME
> [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md)
> (`middleware.Declaration[In,Out]`, `rest.Middleware[In,Out]`/
> `events.Middleware[In,Out]`, `Transform`/`ClientTransform`/`.Use(mw)`) —
> ALREADY IMPLEMENTED, tested, and documented as current — is an EXPLICITLY
> OPEN QUESTION (§3), not decided either way. Nothing in `api/rest`/
> `api/events`/`middleware` changes as a RESULT of this doc alone; d-0003
> remains the accurate, current description of shipped code until a
> SEPARATE implementation-planning round executes any migration. Breaking
> changes are explicitly accepted as a possibility for that future round
> (see the repo owner's own framing of this rethink: "we can make breaking
> changes if we can achieve these goals more easily").
>
> **Supersedes** the open question in
> [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md)
> (already superseded once, by d-0003) for the specific finding it raised
> (a single shared `middleware.Middleware` struct carrying REST-only
> fields) — whether THIS doc's own mechanism is the long-term resolution,
> or d-0003's `Declaration[In,Out]` split remains it, is the same open
> question as above (§3).
>
> **Answers** [MQTT5 User Property Merge](mqtt5-user-property-merge.md)'s own
> explicitly-flagged "registration surface... NOT resolved" open question — User
> Properties become a concrete, sealed `mqtt5.Capability` instance under this
> design (§5.2).
>
> **Prerequisite still applies**: [ReqReply Workflow Simplification](reqreply-workflow-simplification.md)'s
> `Client`/`Server`/`Attach` rework remains a prerequisite for cleanly exposing
> Response Topic/Correlation Data as a capability (unchanged from this doc's
> original finding — see §5's worked examples for why).
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
   surface today** — this doc's own original finding (kept in §5.2/§5.6), and
   [MQTT5 User Property Merge](mqtt5-user-property-merge.md)'s independent
   confirmation of the SAME gap from the merge-field angle.
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

### 2.3 The addressing-model problem — related, but NOT fully solved by §2.1/§2.2's mechanism

**Correction made this round:** an earlier draft of this section claimed the
`Address` sketch below already achieved the SAME compile-time guarantee as
§2.1/§2.2's sealed `Capability` mechanism. On closer inspection (see §5.3),
that is only PARTLY true: `Address` (below) is an OPEN interface (any
package may implement `AddressID() string`), unlike `Capability`, which is
SEALED per adapter. An address-shape mismatch (e.g. building a channel with
an `amqp.Address` and later attaching it to `mqtt5.Attach`) is therefore
still caught only at RUNTIME, inside the adapter's own logic — not at
compile time the way a `Capability` mismatch now is. This is a genuinely
open gap (§7), not resolved here; the sketch below is kept as the
addressing-model design this doc still recommends, with this caveat stated
plainly rather than glossed over:

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

## 3. Relationship to D-0003 — an honest comparison, REOPENED as an explicitly undecided question

**Status: NOT DECIDED — reopened.** The comparison below was written in an
earlier round, when this doc's own primitive was the now-SUPERSEDED, OPEN
`Feature`/`Provider` sketch (§2's historical note). That round concluded
"Option B" (subsume D-0003). Since then, the primitive itself changed to
the sealed, per-adapter `Capability` mechanism (§2) — and D-0003's
`Middleware[In,Out]` was ALREADY compile-time-safe (generic, no string
IDs) even before this pivot, so the original calculus (weighing an
open/runtime-checked `Feature` against an already-safe `Declaration[In,Out]`)
no longer describes the actual choice on the table. **Whether D-0003 should
fold into the sealed-`Capability` mechanism, stay fully separate, or
something in between is an explicitly OPEN QUESTION — not decided in this
round.** The Option A/B analysis below is KEPT as the historical record of
the PREVIOUS round's reasoning (useful context for a future round revisiting
this question), not as this doc's current conclusion.

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

**This round's status: REOPENED, not decided.** The previous round's
"Decision: Option B" conclusion was reached against the now-superseded
`Feature`/`Provider` primitive and is NOT carried forward as this doc's
current position. Whether D-0003 ever folds into the sealed-`Capability`
mechanism — and if so, whether via the SAME "embed a `Declaration[In,Out]`
inside a capability type" shape Option B sketched, or some other approach —
is left for a future, dedicated round to decide, informed by (but not
bound by) the reasoning above.

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
User Properties, but with no MERGE-CAPABLE sibling (the exact gap
[MQTT5 User Property Merge](mqtt5-user-property-merge.md) targets) and no
declarative "this channel REQUIRES User Property support" statement a caller
can make at the `api/events.Channel` level — today, a caller simply never
attempts to bind an mqtt(v3) client to a channel using `UserPropertyParam`,
by convention, not by any enforced rule.

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

### 5.3 A hypothetical AMQP adapter — the reference case the general mechanism (§2) was built to match

```go
// api/events — Address is an open interface type (§2.3's option (a)),
// NOT a new top-level pattern; TopicAddress covers today's MQTT/ZeroMQ shape
// unchanged.
type Address interface{ AddressID() string }

type TopicAddress struct{ Topic string }
func (TopicAddress) AddressID() string { return "topic" }

// adapters/amqp — a NEW address shape, living entirely in the adapter
// package, never touching api/events' own code.
type Address struct{ Exchange, RoutingKey, Queue string }
func (Address) AddressID() string { return "amqp.address" }

// api/events — NewChannel's signature changes (a genuine breaking change,
// accepted per this doc's own framing) from a bare topic string to an
// Address value; NewChannelFromTopic's existing convenience (today wrapping
// a pre-built Topic struct) becomes NewChannelFromAddress, generalized:
func NewChannel[T any](addr Address, codec codex.Codec[T], opts ...ChannelOpt) Channel[T]

// adapters/amqp — ack-mode is a sealed Capability, supplied at Attach time,
// exactly like mqtt.QoS/mqtt5.QoS in §5.1 — the SAME mechanism §2 formalizes
// generally, applied here to AMQP's own concept.
type Capability interface{ isAMQPCapability() }

type AckMode byte
func (AckMode) isAMQPCapability() {}

func Attach[T any](client *Client, ch events.Channel[T], caps ...Capability) error
```

An AMQP-bound channel declares `events.NewChannel(amqp.Address{Exchange:
"orders", RoutingKey: "created.#", Queue: "worker-1"}, codec, ...)`; an
MQTT-bound channel keeps declaring `events.NewChannel(events.TopicAddress{Topic:
"orders/created"}, codec, ...)` (or a thin `NewChannelFromTopic`-style helper
preserving today's ergonomic bare-string call site for the common case — a
DESIGN DETAIL for the implementation round, not resolved here).

**Error timing — a correction made THIS round, not carried forward
uncritically:** an earlier draft of this worked example claimed the
address-shape mismatch itself (an MQTT-only channel built from an
`amqp.Address`) fails at COMPILE time "via Go's own type system." On closer
inspection, THAT SPECIFIC claim does not hold: `Address` (as sketched above)
is an OPEN interface — any package can implement `AddressID() string` — so
`events.NewChannel(amqp.Address{...}, codec, ...)` compiles successfully
regardless of which adapter the resulting `Channel[T]` is LATER attached to,
because `Channel[T]` does not carry the address's concrete type in its own
Go type. The mismatch (attaching an AMQP-addressed channel to `mqtt5.Attach`)
would only surface INSIDE `mqtt5.Attach`'s own runtime logic (extracting an
MQTT topic from `ch`'s stored address, discovering it is actually an
`amqp.Address`) — i.e. a RUNTIME failure, the SAME category of gap §2
otherwise eliminates for capabilities. **`AckMode` (this example's OWN
sealed capability, supplied at `Attach` time) IS fully compile-time-safe,
exactly like §5.1/§5.2's `QoS`/`UserProperty`** — only the ADDRESS half of
this worked example is not, and this doc does not currently resolve that gap
(flagged as a new open item in §7, not decided here — e.g. parameterizing
`Channel[Addr, T]` by the address type, so `mqtt5.Attach` could require
`Channel[TopicAddress, T]` specifically, is one candidate direction, not
designed further here). **This worked example's CAPABILITY half (`AckMode`)
validates §2's general mechanism end to end; its ADDRESS half surfaces a
genuinely NEW open question** (the address-shape gap above) that this
round's correction discovered, rather than one this example had already
solved — kept in this section because it is still the right worked example
for AMQP, not because every part of it is resolved.

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

**This is exactly why §3 leaves the D-0003 relationship an explicitly OPEN
QUESTION rather than deciding it in this round**: whether/how
`middleware.Middleware`/D-0003's `Declaration[In,Out]` mechanism should ever
interact with §2's sealed `Capability` mechanism turns on resolving this
exact tension for Security specifically — not decided here.

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

**Left genuinely open, not resolved:** this doc does not attempt to force a
fit here. Whether `ports.File`/`Cache`/`SQL`/`Dir` ever need their own
capability-declaration mechanism, and if so whether it resembles §2's
Attach-time model or something shaped differently around ports' own
call-directly (no separate bind step) architecture, is deferred to a future
round (see §7).

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
- **Response Topic + Correlation Data** (MQTT5-only) — confirmed via code this
  is EXACTLY what powers `reqreply` over mqtt5 (`adapters/mqtt5/reqreply.go`
  reads/writes `msg.Properties.ResponseTopic`/`CorrelationData` directly). This
  remains a real, already-shipped instance of the "declared capability, adapter
  either supports it or can't be bound" principle — simply never framed or
  generalized this way before this doc. Still blocked on
  [ReqReply Workflow Simplification](reqreply-workflow-simplification.md)'s
  `Client`/`Server`/`Attach` rework (see the banner above), unchanged from this
  doc's original finding.
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

- **Go generics feasibility**: can a sealed `Capability` implementation carry
  a TYPED `middleware.Declaration[In, Out]` payload (§5.2's `UserProperty[In]`
  sketch) while the SURROUNDING `Attach[T any](..., caps ...Capability)`
  function stays simple and ergonomic across REST's
  `MergedHeaderParam[T]`-style generic constructors elsewhere in this
  codebase? A prototype spike is needed before committing further, not
  assumed here.
- **Spec (OpenAPI/AsyncAPI) rendering plan for adapter-defined capabilities**:
  today's `middleware.Middleware`/`rest.HeaderParam`/etc. render into the spec
  via CORE-PACKAGE code that knows their exact shape. An adapter-defined
  capability (e.g. `amqp.Address`, `mqtt5.QoS`) supplied only at `Attach`
  time has no core-package renderer, and — unlike the earlier `Feature`
  sketch — isn't even part of the channel/route's OWN declared value, making
  spec visibility HARDER to achieve here, not just unresolved: does AsyncAPI
  need a vendor-extension mechanism (`x-mqtt5-qos`) populated by a SEPARATE
  spec-contribution call at `Attach` time, does the adapter supply its own
  renderer callback, or do these capabilities deliberately stay spec-invisible
  (mirroring how `ports.File` has no spec at all)? Not decided — and Shared
  Subscriptions (§6) arguably SHOULD be spec-visible (it changes delivery
  semantics a consumer needs to know about), sharpening why this question
  matters, not just a checkbox to defer.
- **Whether/how D-0003 relates to this mechanism** — see §3, REOPENED this
  round, not decided either way (was previously "Option B: subsume," now
  explicitly undecided given the primitive itself changed).
- **Discovery**: how does a caller learn WHICH capabilities a given adapter
  supports BEFORE attempting to bind, rather than discovering only via a
  compile error when they actually try? A compile error IS strictly earlier
  feedback than a runtime check, but still requires attempting the
  combination in code first — no adapter package today exposes an
  enumerable "list of capabilities I support" a caller could inspect ahead of
  writing code. Worth deciding whether this is a real gap or an acceptable
  tradeoff (IDE autocomplete on the adapter package's own exported symbols
  already gives SOME discoverability, arguably better than either the old
  string-ID approach or a hypothetical runtime introspection API would).
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
- **AMQP's compound ack+persistence configuration has no single-value
  equivalent to MQTT's QoS enum** (NEW this round, surfaced by §5.1's
  two-part-test analysis): AMQP's own version of "delivery guarantee" needs
  AT LEAST two orthogonal sealed capabilities (`amqp.AckMode` for
  auto-vs-manual acknowledgement, `amqp.Persistent` for the delivery-mode
  bit) rather than one `mqtt.QoS`-shaped enum — and even together, they do
  not reach MQTT's native "exactly-once" semantics (AMQP 0-9-1 has no
  built-in equivalent; some broker extensions approximate it, out of scope
  for the base protocol). The exact Go shape of `amqp.AckMode`/
  `amqp.Persistent` — and whether AMQP needs a THIRD capability to fully
  express delivery semantics — is NOT designed here, only the conceptual
  gap is confirmed.
- **The `Address` mismatch is NOT compile-time-safe** (NEW this round,
  discovered while correcting §2.3/§5.3): unlike a sealed `Capability`
  mismatch, an address-SHAPE mismatch (building a channel with an
  `amqp.Address`, attaching it to `mqtt5.Attach`) is caught only at RUNTIME
  today, because `Address` (§2.3) is deliberately an OPEN interface, not a
  sealed one. Whether `Address` should ALSO become sealed per adapter
  (losing the "any adapter can define a new address shape independently"
  openness, mirroring `Capability`'s own trade-off), or whether
  `events.Channel[T]` needs an additional type parameter carrying the
  address's concrete type (so `Attach` functions can require a SPECIFIC
  address type at their own signature), is NOT decided — flagged as a real
  gap this doc's own "we do not compromise on compile-time safety" bar has
  NOT yet met for addressing specifically, even though it has for
  capabilities.

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
- **[Review-3, Medium] Security shows near-zero benefit — still open,
  reframed.** §5.5 now explains WHY: Security is the one surveyed concept
  that IS genuinely cross-protocol, which is exactly why it does NOT fit the
  sealed-per-adapter mechanism the same way QoS/User Properties/AMQP
  addressing do. This is no longer treated as an unexplained tension — it
  directly motivates §3's reopened D-0003 question — but that question
  itself remains undecided.
- **[Review-4, Medium] `NewChannel`'s breaking change under-analyzed — still
  open, unaffected by this round's pivot.** §2.3/§5.3's `Address`
  breaking-change migration-cost gap is orthogonal to the capability-matching
  mechanism change — unchanged from before.
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
  WITHIN whichever pattern was already chosen. Still open: whether
  `ports.File`/`Cache`/`SQL`/`Dir` ever need their OWN capability mechanism
  at all — linked to §5.6's genuinely-open case (ports has no separate
  "Attach" binding step to hang capabilities off of the way REST/events do).
- **[Review-8, Low-Medium] No `stats.Observer` integration — still open,
  unaffected by this round's pivot.** If anything, LESS urgent now: a
  capability mismatch is a COMPILE error (never reaches a running process at
  all), so there is no runtime rejection EVENT left for an Observer to
  record for THIS specific failure mode — but whether other aspects of
  capability USE (e.g. a supplied `mqtt5.UserProperty`'s own merge-decode
  failure) should report through Observer remains open and undecided.
- **[Review-9, Low] No "Test plan" section — still open, unaffected by this
  round's pivot.** Unlike [D-0003](../design/d-0003-codec-declared-middlewares.md)'s
  own "## Test plan" section, this doc still has none.
- **[Review-10, Trivial] Field-naming collision — RESOLVED/moot this
  round.** No `UnsupportedFeatureError` type is needed anymore — a
  capability mismatch is the Go compiler's own diagnostic, not a custom
  error type with fields to name.

## See also

- [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md) —
  already superseded once by d-0003; this doc's `Feature`/`Provider` model is
  now the intended LONG-TERM resolution instead (§3).
- [D-0003 — Codec-Declared Middlewares](../design/d-0003-codec-declared-middlewares.md) —
  the currently-SHIPPED mechanism this doc's chosen direction (§3, Option B)
  intends to eventually subsume; remains the accurate description of shipped
  code until a separate implementation round executes the migration.
- [MQTT5 User Property Merge](mqtt5-user-property-merge.md) — its own
  "registration surface... NOT resolved" question is answered by §5.2 above.
- [ReqReply Workflow Simplification](reqreply-workflow-simplification.md) — a
  prerequisite for Response Topic/Correlation Data becoming a real `Feature`
  (§6), unchanged from this doc's original finding.
- [Declarative Middleware](declarative-middleware.md) — its own unshipped
  `ports.File[T]` sketch is the basis for §5.6's worked example.
- `docs/concepts/api-contracts.md` — the "one struct, one call" principle every
  worked example in §5 is checked against for non-regression.
