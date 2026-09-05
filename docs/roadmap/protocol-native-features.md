# Protocol-Native Feature Declarations — `api/events`, `api/rest`, adapters

> **Status:** Idea only — no driver yet. Spun out of
> [Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
> review of the middleware concept for spec-adding custom middleware
> (REST's `FromHeaderParam`-style bridges). That review surfaced a
> genuine question: how should PROTOCOL-SPECIFIC capabilities (MQTT5's
> User Properties, Shared Subscriptions, Message Expiry; ZeroMQ's
> Conflate/HWM) be declared on a channel, given that — unlike REST,
> which is always HTTP — pub/sub spans THREE incompatible transports
> within ONE pattern? A caller declaring "this channel uses Shared
> Subscriptions" only makes sense if the adapter it's eventually bound
> to actually understands that concept; an adapter that doesn't
> (`mqtt` v3, `zeromq`) has nothing to do with the declaration at all.
> This doc proposes a general mechanism — `ProtocolFeature` — for
> exactly this "declare a capability, let the binding adapter validate/
> fulfill it or reject" pattern, GENERALIZED (per a follow-up
> discussion) beyond pub/sub to REST's header/cookie/query params too.
> [← Back to Roadmap](index.md)
>
> **Thin-adapter review (confirmed, no rework needed to this doc's own
> mechanism):** a dedicated review against the codebase's guiding
> principle — adapters stay THIN (pure IO, attach-only, adapter-specific
> config/options); ALL workflow (middleware/handler attachment, calling
> client/server functions) lives in the `api/*` declaration layer, never
> called directly from an adapter package — confirmed `ProtocolFeature`
> as sketched below ALREADY respects it: `.WithFeature()` is declared on
> the api-layer role-scoped builder (`Subscriber[T]`/`Publisher[T]`);
> evaluation happens inside the CONCRETE adapter's own thin `Subscribe`/
> `Publish` dispatch (exactly where protocol-native wire behavior
> belongs — e.g. rewriting a topic to `$share/group/topic`). REST
> (`rest.Client`/`Server` + `nethttp`/`chi.Attach*`) and pub/sub
> (`events.Client` + `mqtt5`/`mqtt`/`zeromq.Attach`, plus Decision 7's
> `NewPublishTransport`/`SubscribeTransport` + `events.PublishHandle`/
> `SubscribeHandle`) were independently confirmed to already fully
> follow this same split. **The one confirmed violation is
> `api/reqreply`**, which has NO `Client`/`Server`/`Attach` at all —
> its entire workflow (topic subscribe, correlation matching, reply
> management, dispatch loop) lives directly inside
> `adapters/mqtt5.Serve`/`.Call` and `adapters/zeromq.Serve`/`.Call`,
> called directly by users. This matters HERE because Response Topic +
> Correlation Data — this doc's own confirmed, already-shipped example
> of a protocol-native capability — is hardwired inside that same
> violating code (`adapters/mqtt5/reqreply.go`). Fixing `api/reqreply`'s
> architecture is a PREREQUISITE for cleanly exposing Response Topic/
> Correlation Data (and Shared Subscriptions, for req-reply's own
> reply-topic fan-out) as real `ProtocolFeature` declarations — see
> [ReqReply Workflow Simplification](reqreply-workflow-simplification.md),
> which designs that fix. No changes are needed to THIS doc's own
> `ProtocolFeature` mechanism sketch as a result of this review — the
> mechanism was already correct; only ITS BEST EXAMPLE currently lives
> in code that needs the separate rework linked above before the
> example can be cleanly declarative.

## The core idea — mirrors `ports.Pattern`'s sealed-interface technique

```go
// api/events (pub/sub-scoped version) — sealed marker interface,
// mirrors ports.Pattern's isPortPattern() technique exactly: any
// package can implement it via the unexported marker method, but only
// packages this codebase controls actually do, keeping the type
// closed against arbitrary external implementations.
type ProtocolFeature interface{ isProtocolFeature() }
```

Concrete features live in the ADAPTER package that defines them — same
import direction as today (adapters import `api/events`/`api/rest`,
never the reverse):

```go
// adapters/mqtt5
type SharedSubscription struct{ Group string }
func (SharedSubscription) isProtocolFeature() {}

type MessageExpiry struct{ Duration time.Duration }
func (MessageExpiry) isProtocolFeature() {}

// adapters/zeromq
type ConflateMode struct{}
func (ConflateMode) isProtocolFeature() {}
```

**Declared** via a new `.WithFeature(f events.ProtocolFeature) Subscriber[T]`/
`Publisher[T]` method (accumulates a `[]ProtocolFeature` on the
role-scoped builder — same accumulation pattern
[Pub/Sub Workflow Simplification](../design/d-0002-pubsub-workflow-simplification.md)'s
`.Use()` already established).

**Evaluated** at the CONCRETE adapter's OWN `Subscribe`/`Publish`/
`ServeSubscribers`/`Register` dispatch — deliberately NOT at
`.Handle(client)`, which is adapter-AGNOSTIC by design and cannot know
what a specific transport supports. Each adapter type-switches over the
declared features it receives; an unrecognized feature type fails
EAGERLY via a new `events.UnsupportedProtocolFeatureError{Topic,
Feature}` (mirrors `UnsupportedMiddlewareParamsError`'s naming/eager-
validation convention exactly — see the pub/sub doc's own escape-hatch-
closure history for the established pattern this follows). A
RECOGNIZED feature is applied natively — e.g. `mqtt5.Subscribe`
recognizing a `SharedSubscription{Group: "workers"}` rewrites the
subscribed topic to `$share/workers/{topic}` before calling the
underlying MQTT SUBSCRIBE.

## Concrete feature survey (confirmed via code where already partially exposed)

**Already exposed as call-time options today (not yet declarative or
gating anything — candidates to MIGRATE onto `ProtocolFeature` once it
exists, not necessarily required to):**

- **`ContentType`** (`mqtt5.PublishOptions`) — sets MQTT5's native
  ContentType property; ALREADY wired to format auto-selection
  (`makeSubscribeMessageHandler` matches incoming `ContentType` against
  `format.Format.ContentType()`). No `mqtt`(v3)/`zeromq` equivalent
  (confirmed: v3 "carries no content-type" per an existing code
  comment).
- **`Retained`** (`mqtt5`/`mqtt` v3 `PublishOptions`, confirmed via both
  adapters' `Publish` functions taking a `retained bool` param) —
  MQTT-family only; ZeroMQ has NO retained-message concept at all (no
  broker to retain anything in).

**NOT exposed anywhere today — genuine candidates for `ProtocolFeature`:**

- **Message Expiry Interval** (MQTT5-only) — a publish-time TTL on a
  message; no `mqtt`(v3)/`zeromq` equivalent.
- **Response Topic + Correlation Data** (MQTT5-only) — confirmed via
  code this is EXACTLY what powers `reqreply` over mqtt5
  (`adapters/mqtt5/reqreply.go` reads/writes
  `msg.Properties.ResponseTopic`/`CorrelationData` directly). This is
  not a NEW finding so much as a confirmation that `reqreply`'s entire
  viability over MQTT5 — and its NON-viability over `mqtt`(v3), which
  has neither property — is ITSELF a real, already-shipped instance of
  this exact "declared capability, adapter either supports it or
  can't be bound" principle, simply never framed or generalized this
  way before.
- **Shared Subscriptions** (`$share/group/topic`, MQTT5-only) —
  competing-consumers load-balancing: multiple subscriber instances
  register the SAME shared-group topic, and the broker delivers each
  message to exactly ONE group member (not all). This is genuinely
  DELIVERY-SEMANTICS-CHANGING, not just metadata — a strong candidate
  for `ProtocolFeature` specifically (as opposed to a plain call-time
  option) since getting it wrong changes correctness, not just
  observability. No `mqtt`(v3)/`zeromq` equivalent in the SPEC itself
  (some v3 brokers offer a non-standard vendor extension, but it is not
  part of the MQTT 3.1.1 specification).
- **ZeroMQ HWM (High Water Mark) / Conflate** — backpressure/mailbox
  behavior (Conflate = "drop older undelivered messages, keep only the
  latest," mirrors `ports.LatestPort`'s existing semantics elsewhere in
  this codebase) — zeromq-only, no MQTT-family equivalent at all.

**Considered, but likely NOT worth exposing as a `ProtocolFeature`:**

- **Topic Alias / Subscription Identifiers** (MQTT5-only) — pure
  WIRE-BANDWIDTH/multiplexing OPTIMIZATIONS, invisible at the
  application level (Topic Alias just replaces a long topic string with
  a numeric ID on the wire; Subscription Identifiers just tag which
  subscription a delivered message matched). Mirrors why go-codex
  doesn't expose raw MQTT QoS-transport internals either — these
  belong entirely inside the adapter's own wire-encoding, never
  surfaced as a channel-level declaration.

## GENERALIZATION — applying the SAME mechanism to REST's header/cookie/query params (a bigger idea, not resolved here)

A follow-up discussion surfaced a genuinely larger architectural
insight: REST's EXISTING `HeaderParam`/`CookieParam`/`QueryParam`
declarations (and their `FromHeaderParam`-style middleware bridges)
are CONCEPTUALLY THE SAME KIND OF THING as a pub/sub `ProtocolFeature`
— a declared CAPABILITY REQUIREMENT that only a compatible adapter can
fulfill. REST already enforces something similar structurally (only
`adapters/nethttp`/`chi` can ever bind a `HeaderParam`-declared route,
since no other transport exists for REST) — but the PRINCIPLE
generalizes: "declare a capability on a route/channel, then have the
BINDING adapter validate/fulfill it or reject" is the SAME mechanism
REST's params, pub/sub's Security, and this doc's `ProtocolFeature`s
all separately instantiate today, just inconsistently named and
structured across the three.

**This generalized framing could actually SUPERSEDE or SUBSUME
[Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md)'s
struct-splitting approach** — instead of splitting `middleware.Middleware`
into `rest.Middleware`/`events.Middleware`/`reqreply.Middleware` STRUCTS
(fixed fields, closed set), EVERYTHING (Security, header/cookie/query
params, mqtt5 User Properties, Shared Subscriptions, etc.) could be
modeled as a `Feature` value in a single `[]Feature` slice on ONE
shared declaration surface, with EACH adapter/binding validating which
Features it recognizes — a more extensible, open-ended mechanism than
fixed struct fields, and one where adding a NEW capability (e.g. a
future transport's own native feature) never requires touching the
shared type at all.

**Relationship to other spun-out docs (not resolved here, flagged for
a future investigation):**

- [Common-Base + Per-Pattern-Derived Middleware Types](common-middleware-architecture.md) —
  may be SUPERSEDED by the generalized `Feature`-slice idea above,
  rather than pursued as originally scoped (struct-splitting). Not
  decided — both remain open until a dedicated comparison session.
- REST's client-side general-purpose `ClientMW` hook (resolved via a
  dedicated Fn-shape mechanism mirroring pub/sub's `PublishMW` — see
  [d-0001's Addendum 3](../design/d-0001-rest-middleware-workflow-simplification.md#addendum-3-client-side-general-purpose-clientmw-hook-closes-the-last-known-restevents-middleware-asymmetry))
  — a `Feature`-based `ClientMW` COULD have closed that gap uniformly
  too (the SAME declare-and-validate mechanism working for client-side
  capabilities), but the shipped fix used a dedicated Fn shape instead;
  left here as a historical alternative-design note, not re-opened.
- [MQTT5 User Property Merge](mqtt5-user-property-merge.md) — User
  Properties become a CONCRETE `ProtocolFeature`/`Feature` instance
  under this design, resolving that doc's own explicitly-flagged
  "registration surface... NOT resolved" open question (it was
  drafted as a plain `ChannelOpt`/`MergedUserPropertyParam[T]`; this
  doc's mechanism offers an alternative, possibly more consistent,
  registration path worth comparing against before implementation).
- [ReqReply Workflow Simplification](reqreply-workflow-simplification.md) —
  a PREREQUISITE, not just a related doc: `api/reqreply` currently has
  no `Client`/`Server`/`Attach` architecture (confirmed thin-adapter
  violation — see the status banner above), so Response Topic +
  Correlation Data (this doc's own confirmed, already-shipped
  `ProtocolFeature`-shaped capability) has nowhere clean to be declared
  today; it is hardwired inside `adapters/mqtt5/reqreply.go`'s
  `Serve`/`Call`. Once that doc's `Client`/`Server`+`Attach` rework
  lands, Response Topic/Correlation Data (and req-reply's own use of
  Shared Subscriptions for reply fan-out) become this doc's first real,
  cleanly-declarative `ProtocolFeature` instances.

## Open questions (not answered — for a future design session)

- Does `ProtocolFeature` (pub/sub-scoped) and a hypothetical
  REST-generalized `Feature` actually need to be the SAME Go type/
  interface, or can they stay independently-scoped sealed interfaces
  that just SHARE a common design pattern? (The pub/sub-scoped version
  above is concrete and ready to prototype; the REST generalization is
  explicitly NOT concrete yet.)
- Exact validation-error type/timing for REST's hypothetical
  `Feature`-based params: today's `checkParamConflicts`/
  `applyParamDeclarations` do MERGE+CONFLICT detection at `Register`
  time (spec-relevant); would `Feature`-based params need the SAME
  two-phase declare-then-validate split, or could adapter binding fully
  replace that mechanism?
- Should `ProtocolFeature`s be allowed to CONTRIBUTE to the AsyncAPI
  spec (e.g. rendering "x-shared-subscription-group" as a vendor
  extension) the way `Security` does, or should they remain
  purely-runtime, invisible in the rendered spec? Not decided — Shared
  Subscriptions arguably SHOULD be visible in the spec (it changes
  delivery semantics a consumer needs to know about); Message Expiry
  is more ambiguous.
- How does a caller discover WHICH `ProtocolFeature`s a given adapter
  supports, before attempting to bind (rather than discovering via a
  runtime `UnsupportedProtocolFeatureError`)? REST's param declarations
  have no equivalent "does this adapter support X" discovery mechanism
  either — worth checking if this is a real gap or an acceptable
  "fails fast, not before" tradeoff consistent with the rest of this
  codebase's eager-validation philosophy.

No implementation, no locked API — this doc exists to hold the
confirmed mechanism sketch, the concrete feature survey, and the
generalized architectural question, not to answer the open ones.
