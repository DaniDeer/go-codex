# Protocol-Native Capabilities

> See also: [`docs/design/d-0006-protocol-native-capabilities.md`](../design/d-0006-protocol-native-capabilities.md) (full design rationale + survey) · [`adapters/mqtt`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt) · [`adapters/mqtt5`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt5) · [`adapters/zeromq`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/zeromq)
>
> `Capability` is a **pub/sub-only** mechanism, scoped to
> `adapters/mqtt`/`adapters/mqtt5`/`adapters/zeromq` (consumed via
> `api/events`). REST, Security, and reqreply deliberately use different,
> already-documented mechanisms instead — see
> ["Why not REST/Security/reqreply?"](#why-not-restsecurityreqreply) below.

## What a `Capability` is

A `Capability` is a **sealed, per-adapter, compile-time-checked** value
that declares a protocol-native, transport-specific behavior — MQTT
quality-of-service, MQTT retained-message flags, ZeroMQ high-water-mark,
and so on. Each adapter defines its OWN `Capability` interface, sealed to
that package (mirroring the technique `ports.Pattern` already uses):

```go
// adapters/mqtt5
type Capability interface{ isMQTT5Capability() }

type QoS byte
func (QoS) isMQTT5Capability() {}

type Retained bool
func (Retained) isMQTT5Capability() {}
```

Because the marker method is unexported, a `zeromq.Capability` value
(e.g. `zeromq.HWM`) **cannot** satisfy `mqtt5.Capability` — attaching a
mismatched capability to the wrong adapter is a **Go compile error**, not
a runtime failure.

Capabilities are supplied at **declare time**, via the adapter's existing
`SubscribeOptions`/`PublishOptions` struct — attached through
`events.Subscriber.WithOptions`/`events.Publisher.WithOptions`:

```go
sub := events.NewSubscriber(channel, transport).
    WithOptions(mqtt5.SubscribeOptions{
        Capabilities: []mqtt5.Capability{mqtt5.QoSAtLeastOnce},
    })

pub := events.NewPublisher(channel, transport).
    WithOptions(mqtt5.PublishOptions{
        Capabilities: []mqtt5.Capability{mqtt5.Retained(true)},
    })
```

This is purely additive alongside the pre-existing `QoS byte`/`Retained
bool` call-time fields — those remain a documented, supported "escape
hatch" for the common single-value case. `Capabilities` is the
RECOMMENDED, sealed path going forward.

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

### Coverage checking

`events.CapabilitySpec` is a `ChannelOpt` you declare on a channel to
assert "this channel requires capability X." `events.CheckCapabilityCoverage`
runs automatically inside each adapter's `ServeSubscribers`, comparing
declared specs against the capabilities actually supplied — a genuine
mismatch (a spec declared with no matching capability ever supplied)
surfaces as a typed `MissingCapabilityError`.

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

## Why not REST/Security/reqreply?

D-0006's own two-part test — does a capability have a **compatible
shape** AND **uniform-enough support** across every adapter that could
carry it — decides whether something becomes a sealed, adapter-owned
`Capability`, or a single shared, protocol-agnostic mechanism instead:

- **`api/rest`** needs no `Capability` mechanism at all. REST has exactly
  one transport family (HTTP, via `adapters/nethttp`/`adapters/chi`), so
  its existing sealed `RouteOpt` already gives the same compile-time
  exhaustiveness — there's no cross-adapter mismatch to guard against.
  See [`docs/features/rest-api.md`](rest-api.md).
- **Security** is the one surveyed case that CLEARS both bars — the same
  scheme+scopes+credential shape, and every adapter can enforce or
  document it — so it stays a single, protocol-agnostic
  `middleware.SecurityScheme` declaration, unchanged by this mechanism.
  See [`docs/features/security.md`](security.md).
- **`api/reqreply`** doesn't get `Capability` either, but shares **Handler
  Disposition** (`middleware.Disposition`/`SetDisposition`/
  `ResolveDisposition`) with `api/events` — Disposition lives in
  `middleware`, not `api/events`, specifically so `api/reqreply` can reuse
  it with no `api/events` dependency.
- **`ports.File`/`Cache`/`SQL`/`Dir`** structurally lack the
  options-at-a-bind-step shape `Capability` requires; their own
  cross-cutting-concern story is tracked separately in
  [`docs/roadmap/declarative-middleware.md`](../roadmap/declarative-middleware.md).

If you're looking for a single "what protocol knobs exist per API" answer:
`Capability` (this page) is pub/sub-only; everything else uses the
mechanism linked above for its API.
