# MQTT5 Capability Extensions — Message Expiry Interval & Shared Subscriptions

> **Status:** Both features SHIPPED. Message Expiry Interval: a new
> sealed `mqtt5.MessageExpiry` `Capability`, `WireAttributes.MessageExpiryInterval`
> field, wired into the existing publish path, 4 unit tests per this
> doc's own test plan — see `adapters/mqtt5/capability.go`/`capability_test.go`.
> Shared Subscriptions: resolved its open design decision in favor of
> Option B (a plain `SharedSubscriptionGroup string` field, NOT a
> `Capability` — see that section below for the full investigation that
> closed the decision) — wired into all 4 Subscribe entry points, 5 unit
> tests — see `adapters/mqtt5/binding.go`'s `applySharedSubscriptionGroup`
> and `adapters/mqtt5/shared_subscription_test.go`. This doc's scope is
> now fully closed.
> [← Back to Roadmap](index.md)

## Motivation

`docs/design/d-0006-protocol-native-capabilities.md`'s own feature
survey (§6) identified 2 genuine, never-implemented `Capability`
candidates specific to `adapters/mqtt5` — Message Expiry Interval and
Shared Subscriptions — both confirmed to pass the design's own
two-part test (a compatible shape AND real per-route variability worth
gating), unlike User Properties (fails the cross-adapter support bar)
or Response Topic/Correlation Data (decided NOT a `Capability`
candidate — always-on, no opt-out scenario). `docs/features/
capabilities.md`'s "Surveyed but not implemented" section confirms
neither exists in go-codex today. The ONLY reason these were deferred
through Phases 1-2 of `d-0006-protocol-native-capabilities.md` was
"nobody asked for this specific toggle yet" — not a structural
limitation, since `adapters/mqtt5` (the only adapter either capability
applies to) already exists, unlike AMQP's still-pending candidates.

## Scope decisions

| In scope | Out of scope |
|---|---|
| `mqtt5.MessageExpiry(seconds uint32)` — a new sealed `Capability`, Publish-side only (SHIPPED) | Any `mqtt`(v3)/`zeromq` equivalent (MQTT v3 has no message-expiry concept at all in the spec) |
| MQTT 5 Shared Subscriptions support, Subscribe-side only — SHIPPED as `SubscribeOptions`/`SubscribeAdapterOptions.SharedSubscriptionGroup string`, NOT a `Capability` (Option B — see below for why) | A generalized "Capability that rewrites the topic filter" mechanism — NOT built, since Option A (which would have needed one) was not chosen |
| Extending `adapters/mqtt5.WireAttributes` with `MessageExpiryInterval` | Any change to `mqtt`(v3)'s or `zeromq`'s own `Capability`/`WireAttributes` types — this doc is mqtt5-only; `WireAttributes` is NOT touched for Shared Subscriptions (Option B) |

## Toolchain / dependency decisions

No new dependency — both capabilities are expressible entirely through
`github.com/eclipse/paho.golang`'s existing `paho.PublishProperties`/
topic-string conventions, already vendored.

## API surface

### Message Expiry Interval — SHIPPED, mirrors `Retained` exactly

```go
// adapters/mqtt5/capability.go — new sealed Capability, Publish-side only
// (no Subscribe-side equivalent — mirrors Retained's shape exactly).
type MessageExpiry uint32 // seconds

func (MessageExpiry) isMQTT5Capability() {}
func (MessageExpiry) CapabilityName() string { return "MessageExpiry" }
func (e MessageExpiry) Apply(wire *WireAttributes) (bool, error) {
    v := uint32(e)
    wire.MessageExpiryInterval = &v
    return true, nil
}
```

`WireAttributes` gains one new field:

```go
type WireAttributes struct {
    QoS                   byte
    Retained              bool
    MessageExpiryInterval *uint32 // NEW — nil means "not declared", mirrors paho's own *uint32 shape
}
```

The ONE real call-site change: `adapters/mqtt5/adapter.go`'s publish
path already constructs a `*pahomqtt5.PublishProperties{}` (`props`)
for User Properties, immediately before building the `pahomqtt5.
Publish{..., Properties: props}` value passed to `client.Publish` —
confirmed via reading the current code, this is a genuinely trivial
one-line addition:

```go
props := &pahomqtt5.PublishProperties{}
// ... existing User Properties wiring ...
props.MessageExpiry = wire.MessageExpiryInterval // NEW
```

No `Subscribe` path change needed — `pahomqtt5.SubscribeOptions` has no
`MessageExpiry`-equivalent field (a subscriber cannot request expiry;
only a publisher sets it), consistent with `Retained`'s own
publish-only precedent.

### Shared Subscriptions — SHIPPED (Option B)

MQTT5 Shared Subscriptions are encoded ENTIRELY as a `$share/group/`
prefix on the topic FILTER STRING itself — confirmed via reading
`github.com/eclipse/paho.golang`'s `SubscribeOptions` struct, which has
no shared-subscription-specific field at all (only `RetainHandling`/
`NoLocal`/`RetainAsPublished`). This is structurally DIFFERENT from
`QoS`/`Retained`/`MessageExpiry`, all of which apply to a WIRE
ATTRIBUTE struct field — `WireAttributes` never carries the topic
filter itself (it's passed separately as `filter string` to
`client.Subscribe`), so `Capability.Apply(wire *WireAttributes)
(bool, error)`'s EXISTING signature has no natural place to rewrite a
topic string.

**Two candidate directions were weighed — Option A was investigated
concretely (not just reasoned about) and found more invasive than this
doc's own original sketch suggested, closing the decision in favor of
Option B:**

- **Option A — widen `WireAttributes` to also carry a mutable topic.**
  Investigated against the ACTUAL current code (not hypothetically):
  all 4 of mqtt5's Subscribe entry points (`ServeSubscribers`/
  `serveOneEntry`, `subscribeWithHandle`, the ports.Pattern-binding
  `transport.Subscribe`, and the ports `SubscribeAdapter` binding)
  register the router handler (`router.RegisterHandler(filter,
  handler)`) using the SAME `filter` string later handed to the broker
  `client.Subscribe` call, computed BEFORE any capability application
  runs — in `subscribeWithHandle` specifically, router registration
  happens even before `ApplyCapabilities` is called at all. Option A
  would require reordering router-registration-after-capability-
  application in all 4 places — real behavior-change risk (a filter
  mismatch between router registration and the broker subscribe would
  silently break message delivery), not a trivial field-threading
  exercise as the original sketch implied.
- **Option B — a plain, non-`Capability` declaration field (CHOSEN).**
  A `SharedSubscriptionGroup string` field added directly to
  `SubscribeOptions`/`SubscribeAdapterOptions` (and extracted via the
  SAME reflection mechanism `transport.Subscribe` already uses for
  `Capabilities`/`UserPropertyParams`), applied via a small shared
  helper (`applySharedSubscriptionGroup`) at each call site's EXISTING
  filter-computation point — no reordering needed anywhere. Accepted
  tradeoff: Shared Subscriptions does NOT go through
  `events.ApplyCapabilities`/`RequireX` coverage-checking — judged
  acceptable because, unlike QoS/Retained/MessageExpiry's genuine
  protocol-level semantics, Shared Subscriptions has no real
  cross-adapter support-variability question for that mechanism to
  gate: it's a pure client-chosen topic-string shape, not a
  broker/protocol capability an adapter might lack.

See `adapters/mqtt5/binding.go`'s `applySharedSubscriptionGroup` doc
comment for the shipped implementation's own design-history note.

## Structured errors

Neither capability needs a NEW error type — `MessageExpiry`'s `Apply`
can never fail (a pure field assignment, mirroring `QoS`/`Retained`'s
existing `(true, nil)`-always contract). Shared Subscriptions'
eventual error surface depends on which option is chosen above (Option
A: none, same reasoning; Option B: would reuse whatever `TopicParam`
declaration-time validation already exists).

## Observer integration

Both capabilities flow through the EXISTING `stats.CapabilityObserver.
RecordCapabilityApplied` call `events.ApplyCapabilities` already makes
for every capability, generically — no new observer hook needed for
Message Expiry Interval. Shared Subscriptions' observer story again
depends on the chosen option (Option A: same generic path; Option B:
none, since it would bypass `ApplyCapabilities` entirely — itself a
data point against Option B's consistency).

## Unit test plan

| Test | Verifies |
|---|---|
| `TestMessageExpiry_Apply_SetsWireField` | `Apply` sets `wire.MessageExpiryInterval` correctly, returns `(true, nil)` |
| `TestMessageExpiry_CapabilityName` | Returns `"MessageExpiry"` |
| `TestPublish_MessageExpiry_SetsPahoProperty` | End-to-end: a channel declaring `PublishOptions{Capabilities: []Capability{MessageExpiry(60)}}` produces a `pahomqtt5.Publish` with `Properties.MessageExpiry` set to `60` |
| `TestPublish_NoMessageExpiry_LeavesPropertyNil` | Zero-value/undeclared case — `Properties.MessageExpiry` stays `nil`, no behavior change for existing routes (mirrors every prior phase's "zero behavior change" regression bar) |

Shared Subscriptions shipped with its own test table, covering all 4
Subscribe entry points plus the zero-value regression bar every prior
phase uses:

| Test | Verifies |
|---|---|
| `TestApplySharedSubscriptionGroup_EmptyGroup_LeavesFilterUnchanged` | Zero-value/undeclared case — filter unchanged, no behavior change for existing routes |
| `TestApplySharedSubscriptionGroup_NonEmptyGroup_PrefixesFilter` | The `$share/<group>/` prefix is applied correctly (direct helper test) |
| `TestSubscribeAdapter_SharedSubscriptionGroup_PrefixesFilter` | End-to-end via the ports `SubscribeAdapter` binding — both the router-registered filter AND the broker subscription's Topic carry the prefixed filter |
| `TestServeSubscribers_SharedSubscriptionGroup_PrefixesFilter` | End-to-end via the `ServeSubscribers`/`Client.Attach` path |
| `TestTransportSubscribe_SharedSubscriptionGroup_PrefixesFilter` | End-to-end via the ports.Pattern-binding `transport.Subscribe` path, extracted via `subscribeHandlerOptsFields`'s reflection |

## Files changed

| File | Responsibility |
|---|---|
| `adapters/mqtt5/capability.go` | Added `MessageExpiry` sealed `Capability` + `WireAttributes.MessageExpiryInterval` field (existing file, additive change) |
| `adapters/mqtt5/adapter.go` | One-line addition wiring `props.MessageExpiry = wire.MessageExpiryInterval` into the existing publish path; `SubscribeOptions.SharedSubscriptionGroup` field + wiring into `subscribeWithHandle`'s filter computation |
| `adapters/mqtt5/binding.go` | New `applySharedSubscriptionGroup` helper; `SubscribeAdapterOptions.SharedSubscriptionGroup` field + wiring into `SubscribeAdapter`'s filter computation |
| `adapters/mqtt5/caller.go` | `serveOneEntry`'s filter computation wired to apply `SharedSubscriptionGroup` |
| `adapters/mqtt5/transport.go` / `transport_dispatch.go` | `subscribeHandlerOptsFields` extended to extract `SharedSubscriptionGroup` via reflection; `transport.Subscribe`'s filter computation wired accordingly |
| `adapters/mqtt5/capability_test.go` | `MessageExpiry` tests per the table above |
| `adapters/mqtt5/shared_subscription_test.go` | New file — Shared Subscriptions tests per the table above |

## Out of scope (this doc)

- `mqtt`(v3)/`zeromq`'s own capability sets — untouched, this doc is
  mqtt5-only, matching the design doc's own per-adapter-sealed-type
  precedent (a future MQTT v3/ZeroMQ divergence would not require
  touching a shared type).
- AMQP's own addressing/ack-mode/dead-lettering candidates — blocked
  on a future AMQP adapter existing at all.
- Any generalized "Capability that mutates the topic/address" mechanism
  — Option A was investigated and NOT chosen (see above), so no such
  mechanism was built; `WireAttributes` remains unchanged except for
  `MessageExpiryInterval`.
- `transport.Subscribe`'s own pre-existing `TopicFilter`-override gap
  (found while implementing this, NOT fixed): unlike
  `SubscribeOptions.TopicFilter`/`SubscribeAdapterOptions.TopicFilter`,
  this ports.Pattern-binding path has no way to override the derived
  wildcard filter at all today — a separate, unrelated gap, flagged for
  a future round if ever needed.

## Open design decisions

None remaining — both Message Expiry Interval and Shared Subscriptions
have shipped. This doc's scope is fully closed.

## See also

- [Composable Capability Requirements](../design/d-0006-protocol-native-capabilities.md) — the shipped `Capability`/`ApplyCapabilities` mechanism this doc's `MessageExpiry`/`SharedSubscription` values plug into.
- [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md) — §6's original feature survey identifying both candidates.
- [`docs/features/capabilities.md`](../features/capabilities.md) — "Surveyed but not implemented" section, the other cross-reference for these 2 candidates.
