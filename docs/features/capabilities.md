# Protocol-Native Capabilities

> See also: [`docs/design/d-0006-protocol-native-capabilities.md`](../design/d-0006-protocol-native-capabilities.md) (full design rationale + survey) · [`adapters/mqtt`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt) · [`adapters/mqtt5`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt5) · [`adapters/zeromq`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq)
>
> `Capability` covers `api/events` (pub/sub), `api/reqreply`
> (request/reply), AND `api/rest` (Phase 3) — each API owns its own
> package-local declaration types (`events.CapabilityRequirement`/
> `reqreply.CapabilityRequirement`/`rest.CapabilityRequirement`, etc. —
> deliberately NOT shared, see the "Coverage checking" section below).
> events/reqreply consume the SAME 4 existing sealed adapter-owned
> `Capability` values (`mqtt`/`mqtt5`'s `QoS`/`Retained`, `zeromq`'s
> `HWM`/`Conflate`); REST has its OWN mechanism with a DIFFERENT tier
> split — see ["REST's capability mechanism"](#rests-capability-mechanism)
> below. Security deliberately uses a different, already-documented
> mechanism instead — see ["Why not Security?"](#why-not-security) below.
>
> **Terminology note:** the declare-time type is `events.CapabilityRequirement`/
> `reqreply.CapabilityRequirement` (renamed from `CapabilitySpec`) and its
> coverage-check error is `events.CapabilityCoverageError`/
> `reqreply.CapabilityCoverageError` (renamed from `MissingCapabilityError`)
> — see [`docs/roadmap/capability-requirement-composition.md`](../roadmap/capability-requirement-composition.md)
> for the full three-tier vocabulary (Baseline/Implicit/Explicit, with
> Explicit further split into 3a/3b) this mechanism is classified under
> (Tier 3a — Explicit, sealed, adapter-owned), and for the planned Phase 3
> rollout to `api/rest`.

## What a `Capability` is

A `Capability` is a **sealed, per-adapter, compile-time-checked** value
that declares a protocol-native, transport-specific behavior — MQTT
quality-of-service, MQTT retained-message flags, ZeroMQ high-water-mark,
and so on. Each adapter defines its OWN `Capability` interface, sealed to
that package (mirroring the technique `ports.Pattern` already uses).

As of [Phase 4 of the capability-requirement-composition
roadmap](../roadmap/capability-requirement-composition.md), `Capability`
is a REAL programming interface, not a zero-cost marker: every value
must implement `Apply(Target) (applied bool, err error)` against its
adapter-specific target object, and `api/events` OWNS the generic
dispatch loop that calls it — `events.ApplyCapabilities[C,T]` — rather
than each adapter hand-rolling its own resolve+assert+call sequence:

```go
// adapters/mqtt5 — QoS/Retained apply to a per-message WireAttributes
// struct (the intermediate BOTH the publish and subscribe native paho
// call sites populate/consume, since Go can't overload Apply by target
// type and mqtt5 QoS/Retained apply to TWO different native types).
type WireAttributes struct {
    QoS      byte
    Retained bool
}

type Capability interface {
    isMQTT5Capability()
    Apply(wire *WireAttributes) (applied bool, err error)
}

type QoS byte
func (q QoS) Apply(wire *WireAttributes) (bool, error) {
    wire.QoS = byte(q)
    return true, nil
}

type Retained bool
func (r Retained) Apply(wire *WireAttributes) (bool, error) {
    wire.Retained = bool(r)
    return true, nil
}
```

```go
// adapters/zeromq — HWM/Conflate apply directly to the adapter's own
// FramedSocket, via the pre-existing HWMSetter/ConflateSetter optional
// extensions (a documented no-op when sock doesn't implement them).
type Capability interface {
    isZeroMQCapability()
    Apply(sock FramedSocket) (applied bool, err error)
}
```

```go
// api/events — the API LAYER calls through the interface; adapter code
// shrinks to ONE call site each, replacing a hand-rolled dispatch loop.
func ApplyCapabilities[C interface{ Apply(T) (bool, error) }, T any](
    caps []C, target T, obs stats.Observer, location string,
)

var wire mqtt5.WireAttributes
events.ApplyCapabilities(opts.Capabilities, &wire, obs, topic) // mqtt5
events.ApplyCapabilities(opts.Capabilities, sock, obs, topic)  // zeromq
```

Because the marker method is unexported, a `zeromq.Capability` value
(e.g. `zeromq.HWM`) **cannot** satisfy `mqtt5.Capability` — attaching a
mismatched capability to the wrong adapter is a **Go compile error**, not
a runtime failure.

Capabilities are supplied at **declare time**, via the adapter's existing
`SubscribeOptions`/`PublishOptions` struct — attached through
`events.Subscriber.WithOptions`/`events.Publisher.WithOptions`:

```go
sub := channel.WithSubscribe(events.Subscribe{}).
    WithOptions(mqtt5.SubscribeOptions{
        Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce},
    })

pub := channel.WithPublish(events.Publish{}).
    WithOptions(mqtt5.PublishOptions[SensorReading]{
        Capabilities: []mqtt5.Capability{mqtt5.Retained(true)},
    })
```

**`Publisher.WithOptions` (Phase 4c):** before Phase 4c, only
`Subscriber` had a `WithOptions` method — `Publisher` had none, so
`events.Client.Publish`'s reflection shim had NO way to reach a declared
Capabilities value at all (it always published at QoS 0/non-retained).
This forced a caller who needed Capabilities to bypass the attached
`events.Client` entirely and construct an adapter's `PublishTransport`
directly via `mqtt5.NewPublishTransport`+`events.PublishHandle` — the
exact shape the "zero backdoor" guardrail forbids, confirmed via a real
example (`examples/events-api/demo_capability_mechanism.go`) that had
done exactly this. `Publisher.WithOptions` now mirrors
`Subscriber.WithOptions` exactly, and BOTH `events.Client.Publish` and
`events.Client.Subscribe`'s reflection shims (`adapters/mqtt5`/`mqtt`/
`zeromq`, all 3) resolve the declared Capabilities value and apply it
via `events.ApplyCapabilities`/`events.ResolveCapabilityValue` before
dispatching — a caller using an already-attached `events.Client` never
needs to touch the adapter's own constructors for this purpose again.

`events.Client.Publish`/`.Subscribe` still don't support PER-CALL format
overrides or declared security/general-purpose middleware wrapping
(Phase 4d's scope, not yet shipped) — a caller needing those still uses
`subscribe`/`publish` (or the adapter's `PublishTransport`/
`SubscribeTransport`) directly. See
[`docs/roadmap/capability-requirement-composition.md`](../roadmap/capability-requirement-composition.md)'s
Phase 4d section.

**Breaking change (Phase 4/4b, `adapters/mqtt5` + core `api/events`):**
`Capabilities` is now the SOLE mechanism for `adapters/mqtt5` — every
parallel, non-`Capability`-shaped path was removed entirely, per this
codebase's explicit "zero backdoor between the api layer and the
adapters" guardrail (see
[`docs/roadmap/capability-requirement-composition.md`](../roadmap/capability-requirement-composition.md)'s
"Architectural guardrail" section):

- The former call-time `qos byte, retained bool` positional parameters
  and the `SubscribeOptions.QoS` plain field on `adapters/mqtt5` were
  REMOVED entirely.
- `events.MQTTQoS`/`events.PublishAttributes`/`Publisher.WithAttributes`
  — a third, transport-agnostic, route/channel-level declaration
  mechanism that entirely bypassed `Capability` — was DELETED from core
  `api/events`, along with its subscribe-side mirror
  (`events.Subscribe.QoS`/`ChannelHandle.SubscribeQoS`). Neither is
  available anymore, on any adapter.
- Every ports-binding convenience (`mqtt5.SubscribeAdapter`,
  `MQTT5DrainPublishOptions`) was migrated to the SAME
  `Capabilities []mqtt5.Capability`-only shape — no raw-value
  constructor parameters or struct fields remain anywhere in this
  package.

`adapters/mqtt` (v3) is now fully migrated too (docs/roadmap/
capability-requirement-composition.md's Phase 5) — its `Capability`
interface requires `Apply(wire *WireAttributes) (bool, error)`, mirroring
`adapters/mqtt5`'s identical shape exactly. Every former raw `qos byte`/
`retained bool` positional parameter and `SubscribeOptions.QoS`/
`MQTTDrainPublishOptions.QoS`/`.Retained` field was REMOVED entirely —
`Capabilities []mqtt.Capability` is the SOLE mechanism, with no
raw-value constructor parameters or struct fields remaining anywhere in
this package either.

### reqreply: the SAME sealed values, a separate `ServeOptions`/`CallOptions.Capabilities` field

`api/reqreply` uses the identical mechanism — `adapters/mqtt5`/
`adapters/zeromq`'s reqreply transports live in the SAME package as their
events-side `Capability` types, so `mqtt5.QoS`/`mqtt5.Retained`/
`zeromq.HWM`/`zeromq.Conflate` apply immediately, once
`ServeOptions`/`CallOptions` gain a `Capabilities` field mirroring
events' `SubscribeOptions`/`PublishOptions.Capabilities`:

```go
route := reqreply.NewRoute[ComputeReq, ComputeResp]("compute/add", reqCodec, respCodec,
    reqreply.RequireQoS(reqreply.AtLeastOnce),
)

// server side — applied to EVERY reply publish (success, error-pattern-
// matched, and dead-letter alike):
server.Attach(mqtt5.NewServerTransport(mqtt5.ServerTransportOptions{
    Client: client, Router: router,
    Serve:  mqtt5.ServeOptions{Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce}},
}))

// client side — applied to the outgoing request publish:
reqreplyClient.Attach(mqtt5.NewClientTransport(mqtt5.ClientTransportOptions{
    Client: client, Router: router,
    Call:   mqtt5.CallOptions{Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce, mqtt5.Retained(true)}},
}))
respAny, err := reqreplyClient.Call(ctx, route, req)
```

Coverage is checked once at `Serve`/`Attach` setup (server side
only, mirroring events' "publish side never auto-checks coverage"
precedent) via `reqreply.VerifyCapabilityCoverage`. zeromq's REQ/REP and
ROUTER/DEALER reqreply transports (4 real dispatch implementations —
`serverTransport`/`routerServerTransport`/`clientTransport`/
`dealerClientTransport`, each independent, none delegating to another)
all wire through the SAME existing `applyCapabilities` helper unchanged
from its events/pub-sub usage.

## Per-adapter capability reference

| Adapter | Capability type | Values | Applied via |
|---|---|---|---|
| `adapters/mqtt` (v3.1.1) | `QoS` | `QoSAtMostOnce` / `QoSAtLeastOnce` / `QoSExactlyOnce` | `SubscribeOptions.Capabilities` / `PublishOptions.Capabilities` |
| `adapters/mqtt` | `Retained` | `bool` | `PublishOptions.Capabilities` |
| `adapters/mqtt5` | `QoS` | `QoSAtMostOnce` / `QoSAtLeastOnce` / `QoSExactlyOnce` (separate sealed type from `mqtt`'s — MQTT v3 and v5 QoS semantics can diverge independently) | `SubscribeOptions.Capabilities` / `PublishOptions.Capabilities` |
| `adapters/mqtt5` | `Retained` | `bool` | `PublishOptions.Capabilities` |
| `adapters/zeromq` | `HWM` | `int` — socket high-water-mark (outstanding-message queue limit) | `SubscribeOptions.Capabilities` / `PublishOptions.Capabilities`, applied via the optional `HWMSetter` extension on `FramedSocket` |
| `adapters/zeromq` | `Conflate` | `bool` — keep only the latest message per topic (`ZMQ_CONFLATE`), mirrors `ports.LatestPort` semantics | applied via the optional `ConflateSetter` extension on `FramedSocket` |

A capability that a socket implementation doesn't support (e.g. a
`FramedSocket` without `HWMSetter`) is a **documented no-op**, not an
error — applying `zeromq.HWM` to a socket type that doesn't implement
`HWMSetter` simply has no effect.

`adapters/mqtt5`/`adapters/zeromq`'s reqreply transports (`api/reqreply`,
NOT `adapters/mqtt` v3 — it has no reqreply transport at all) reuse these
SAME 4 sealed types, applied via `ServeOptions.Capabilities`/
`CallOptions.Capabilities` instead of `SubscribeOptions`/
`PublishOptions` — see "reqreply" below.

### Requirement sugar helpers

Instead of writing `events.CapabilityRequirement{Name: "QoS", ...}` by
hand, use the adapter-agnostic sugar helpers — each produces an ordinary
`CapabilityRequirement`. `api/reqreply` has its OWN identical set
(`reqreply.RequireQoS`/`RequireRetained`/`RequireHWM`/`RequireConflate`,
`reqreply.QoSLevel`) — a deliberately separate, package-local copy (see
below), not shared with `events`:

```go
ch := events.NewChannel[SensorReading]("sensor/reading", sensorCodec,
    events.RequireQoS(events.AtLeastOnce),   // GENUINELY value-checked, see below
    events.RequireRetained(),                // presence-only (boolean toggle)
)

route := reqreply.NewRoute[ComputeReq, ComputeResp]("compute/add", reqCodec, respCodec,
    reqreply.RequireQoS(reqreply.AtLeastOnce), // identical shape, api/reqreply's own type
)
```

`RequireQoS(level)`/`RequireHWM(minimum)` set a `MinLevel` that's
GENUINELY enforced (see "Coverage checking" below); `RequireRetained()`/
`RequireConflate()` are presence-only, correctly — a boolean toggle has
no "insufficient level" to check. There is no `RequireUserProperties()` —
`UserPropertyParam` is a Tier 2 (Implicit), adapter-options-scoped
declaration, not a Tier 3 (Explicit) requirement; see
`docs/roadmap/capability-requirement-composition.md`'s "Design guardrails"
section for the full classification.

### Coverage checking

`events.CapabilityRequirement`/`reqreply.CapabilityRequirement` is a
`ChannelOpt`/`RouteOpt` you declare on a channel/route to assert "this
channel/route requires capability X."
`events.CheckCapabilityCoverage`/`reqreply.CheckCapabilityCoverage` runs
automatically inside each adapter's `ServeSubscribers`/`Serve` dispatch
(reached via `Client.Attach`/`Server.Attach`), comparing declared
requirements against the capabilities actually supplied.

`api/reqreply` deliberately does NOT import `api/events` for this — it
has its OWN, byte-for-byte-identical-in-shape `CapabilityRequirement`/
`CheckCapabilityCoverage`/`CapabilityCoverageError`/
`VerifyCapabilityCoverage`/`LeveledCapability` types, mirroring
`middleware.Disposition`'s own D-0004 placement precedent (cheap to
duplicate a small struct + a handful of functions, rather than introduce
a cross-API import for it). The GENERIC helpers
`events.ResolveCapabilityValue`/`events.RecordCapabilityApplied` ARE
reused as-is by reqreply's adapter-side dispatch code (they're fully
generic, no `api/events`-specific types beyond the trivially-structural
`CapabilityName` interface) — only the declaration-side pieces above are
duplicated.

Two kinds of mismatch are caught, both surfaced via a typed
`*events.CapabilityCoverageError`/`*reqreply.CapabilityCoverageError`:

- **Missing** — no supplied capability matches the declared `Name` at
  all.
- **Insufficient** — a matching capability EXISTS, but its value doesn't
  meet the declared `MinLevel` (e.g. `RequireQoS(ExactlyOnce)` declared,
  only `mqtt5.QoSAtMostOnce` supplied). This is a GENUINE value check, via
  the optional `events.LeveledCapability`/`reqreply.LeveledCapability`
  interface (`adapters/mqtt.QoS`/`adapters/mqtt5.QoS`/`adapters/zeromq.HWM`
  all implement `Level() int`) — mirrors the `CapabilityName` optional-
  interface pattern, so neither `api/events` nor `api/reqreply` ever
  imports an adapter package to do this. A requirement with no `MinLevel`
  (e.g. `RequireRetained()`) is presence-only, unaffected by this check.

### Observability

`stats.CapabilityObserver` is an optional, type-asserted `stats.Observer`
extension (mirroring `stats.SecurityObserver`) — implement
`RecordCapabilityApplied(location, capabilityName string)` to get a
callback every time a capability is successfully applied to a
subscribe/publish call. See [`docs/guides/observer.md`](../guides/observer.md).

## Not yet capability-ified (still call-time options)

These are already exposed as call-time options today but haven't been
migrated onto the sealed `Capability` mechanism — the underlying
behaviour works, just via an older path:

- **`ContentType`** (`mqtt5.PublishOptions`) — sets MQTT5's native
  ContentType property; already wired to format auto-selection on the
  subscribe side. No `mqtt`(v3)/`zeromq` equivalent.
- **MQTT5 User Properties** (`UserPropertyParam`) — a distinct,
  pre-existing declaration mechanism, not folded into `Capability`.

## Surveyed but not implemented

The design doc's feature survey (§6) lists further candidates with no
code yet: MQTT5 Message Expiry Interval, MQTT5 Shared Subscriptions
(`$share/group/topic`), and AMQP addressing/ack-mode/dead-lettering
(blocked on a future AMQP adapter — see
[`docs/roadmap/amqp-adapter.md`](../roadmap/amqp-adapter.md)). None of
these exist in go-codex today; consult the design doc before assuming
otherwise.

## REST's capability mechanism

`api/rest` (Phase 3 of `docs/roadmap/capability-requirement-composition.md`)
gains the SAME conceptual mechanism events/reqreply already ship, but
split across TWO axes REST's own shape demands:

- **Tier 3a (Explicit, Sealed)** — `rest.RequireQoS`/`RequireHWM` +
  `rest.CapabilityRequirement`/`CheckCapabilityCoverage`/
  `VerifyCapabilityCoverage`/`LeveledCapability`. Byte-identical shape
  and mechanism to events'/reqreply's own. **As of this writing, NO
  shipped REST adapter supplies a concrete Capability value** — HTTP
  (`adapters/nethttp`/`adapters/chi`) has no QoS/HWM concept at all, so
  a route declaring `RequireQoS` is CORRECTLY, EAGERLY rejected at
  Serve/Attach time (`*rest.CapabilityCoverageError`) when attached to
  either — exactly the intended "this adapter doesn't support this
  capability" outcome, not a bug. This mirrors this doc's own "declare
  first, adapter satisfies second, adapter may lag behind declaration"
  philosophy: the mechanism is real and generic NOW; a future non-HTTP
  REST-eligible transport (a ZeroMQ REQ/REP adapter — see
  [`docs/roadmap/zeromq-rest-adapter.md`](../roadmap/zeromq-rest-adapter.md),
  tracked as an INDEPENDENT future effort, no longer gated on/gating
  this mechanism) would be the first to satisfy it.
- **Tier 2 (Implicit) — NEW, not needed by events/reqreply**:
  `rest.HeaderParam`/`CookieParam`/`QueryParam` become GENUINELY
  runtime-checked capabilities once REST has more than one transport
  family. Three OPTIONAL marker interfaces —
  `rest.HeaderCapableTransport`/`CookieCapableTransport`/
  `QueryCapableTransport` (one no-op method each) — are implemented by
  an adapter's own transport type; `RouteHandle.HeaderParamNames()`/
  `CookieParamNames()`/`QueryParamNames()` (mirrors `PathParamNames()`)
  return the FULL declared set (plain-opt AND declarative-middleware-
  merged) for an adapter's `Serve` dispatch to scan, ALONGSIDE
  every declared `SecurityScheme`'s `In` field
  (`rest.RequiredParamKinds`) — closing a gap where a Cookie-based API
  key with no separate `CookieParam` would otherwise bypass the check.
  `adapters/nethttp`/`adapters/chi` implement all 3 markers trivially
  (HTTP always supports headers/cookies/query) — every EXISTING route
  using these params continues to work with ZERO behavior change,
  proven by the full existing test suite passing unmodified.
  `rest.UnsupportedParamKindError` fires at attach time for a param kind
  an adapter's transport genuinely can't carry — e.g. the future ZeroMQ
  REQ/REP adapter deliberately omitting `CookieCapableTransport`.
  Renders into the OpenAPI spec's `x-codex-capabilities` vendor
  extension (mirrors AsyncAPI's own `x-capabilities`).

See [`docs/features/rest-api.md`](rest-api.md) for REST's own feature
page.

## Why not Security?

D-0006's own two-part test — does a capability have a **compatible
shape** AND **uniform-enough support** across every adapter that could
carry it — decides whether something becomes a sealed, adapter-owned
`Capability`, or a single shared, protocol-agnostic mechanism instead:

- **Security** is the one surveyed case that CLEARS both bars — the same
  scheme+scopes+credential shape, and every adapter can enforce or
  document it — so it stays a single, protocol-agnostic
  `middleware.SecurityScheme` declaration, unchanged by this mechanism.
  See [`docs/features/security.md`](security.md). (Whether this legacy,
  non-generic declaration mechanism should eventually fold into the
  newer codec-backed `Middleware[In,Out]` family is a SEPARATE,
  unresolved evaluation — see
  [`docs/roadmap/middleware-consolidation.md`](../roadmap/middleware-consolidation.md).)
- **`api/reqreply`** ALSO shares **Handler Disposition**
  (`middleware.Disposition`/`SetDisposition`/`ResolveDisposition`) with
  `api/events` — Disposition lives in `middleware`, not `api/events`,
  specifically so `api/reqreply` can reuse it with no `api/events`
  dependency. Unlike Disposition, `reqreply.CapabilityRequirement` and
  friends are NOT literally shared with `events` — each API has its own
  package-local copy of the SAME shape (see "Coverage checking" above).
- **`ports.File`/`Cache`/`SQL`/`Dir`** structurally lack the
  options-at-a-bind-step shape `Capability` requires; their own
  cross-cutting-concern story is tracked separately in
  [`docs/roadmap/declarative-middleware.md`](../roadmap/declarative-middleware.md).

If you're looking for a single "what protocol knobs exist per API" answer:
`Capability` (this page) covers `api/events`, `api/reqreply`, and
`api/rest`; Security uses the mechanism linked above instead.
