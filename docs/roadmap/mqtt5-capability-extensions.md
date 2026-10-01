# MQTT5 Capability Extensions — Message Expiry Interval & Shared Subscriptions

> **Status:** Design draft — Message Expiry Interval ready to implement;
> Shared Subscriptions has one open design decision, not pre-decided.
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
| `mqtt5.MessageExpiry(seconds uint32)` — a new sealed `Capability`, Publish-side only | Any `mqtt`(v3)/`zeromq` equivalent (MQTT v3 has no message-expiry concept at all in the spec) |
| `mqtt5.SharedSubscription(group string)` — a new sealed `Capability`, Subscribe-side only, PENDING the open design decision below | A generalized "Capability that rewrites the topic filter" mechanism for OTHER future capabilities — scoped narrowly to what Shared Subscriptions itself needs |
| Extending `adapters/mqtt5.WireAttributes` with the new field(s) these 2 capabilities apply to | Any change to `mqtt`(v3)'s or `zeromq`'s own `Capability`/`WireAttributes` types — this doc is mqtt5-only |

## Toolchain / dependency decisions

No new dependency — both capabilities are expressible entirely through
`github.com/eclipse/paho.golang`'s existing `paho.PublishProperties`/
topic-string conventions, already vendored.

## API surface

### Message Expiry Interval — ready to implement, mirrors `Retained` exactly

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

### Shared Subscriptions — the open design decision (NOT pre-decided)

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

**Two candidate directions, neither pre-decided:**

- **Option A — widen `WireAttributes` to also carry a mutable topic.**
  Add `Topic string` (pre-populated with the undecorated filter before
  `ApplyCapabilities` runs) to `WireAttributes`; `SharedSubscription`'s
  `Apply` rewrites `wire.Topic = "$share/" + group + "/" + wire.Topic`.
  The subscribe call site reads `wire.Topic` instead of the original
  `filter` parameter after capabilities apply. Tradeoff: every OTHER
  capability's `Apply` implementation gains access to (and could
  theoretically mutate) the topic too — a broader blast-radius change
  to `WireAttributes`'s own contract than a single-purpose field would
  need.
- **Option B — a `TopicParam`-adjacent declaration, not a `Capability`
  at all.** Since the `$share/` prefix is really a TOPIC-SHAPE
  decision (not a per-message wire attribute), it could instead be
  expressed as a channel-declaration-time option (e.g. `mqtt5.
  SharedSubscriptionGroup(group string)` passed alongside the
  channel's topic template, composed at Attach time before the filter
  is ever handed to `Capability.Apply`). This keeps `WireAttributes`
  untouched but means Shared Subscriptions would NOT go through the
  `events.ApplyCapabilities`/`RequireX` coverage-checking mechanism
  this whole roadmap built — a real inconsistency with how every other
  capability declares/is-required, worth weighing against the
  simplicity of not touching `WireAttributes`.

**Not silently assumed to be a trivial string-prefix operation** — per
this doc's own directive: whichever option is chosen, it changes
either `WireAttributes`'s contract (Option A) or introduces a SECOND,
parallel declaration mechanism alongside `Capability` for one specific
case (Option B) — both are real design costs, not a free win.

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

Shared Subscriptions' test plan is deferred until the open design
decision above is resolved — writing tests against an undecided API
shape would need rework either way.

## Files to create

| File | Responsibility |
|---|---|
| `adapters/mqtt5/capability.go` | Add `MessageExpiry` sealed `Capability` + `WireAttributes.MessageExpiryInterval` field (existing file, additive change) |
| `adapters/mqtt5/adapter.go` | One-line addition wiring `props.MessageExpiry = wire.MessageExpiryInterval` into the existing publish path (existing file) |
| `adapters/mqtt5/capability_test.go` | New tests per the table above (existing file) |

Shared Subscriptions' file list depends on the chosen option — not
committed here.

## Out of scope (this doc)

- `mqtt`(v3)/`zeromq`'s own capability sets — untouched, this doc is
  mqtt5-only, matching the design doc's own per-adapter-sealed-type
  precedent (a future MQTT v3/ZeroMQ divergence would not require
  touching a shared type).
- AMQP's own addressing/ack-mode/dead-lettering candidates — blocked
  on a future AMQP adapter existing at all.
- Any generalized "Capability that mutates the topic/address" mechanism
  beyond what Shared Subscriptions itself needs (see Option A/B above)
  — if Option A is chosen, `WireAttributes.Topic` should be scoped
  narrowly, not marketed as a new general-purpose extension point.

## Open design decisions (to resolve before/during implementation)

- **Shared Subscriptions: Option A (widen `WireAttributes`) vs. Option
  B (a separate, non-`Capability` declaration mechanism)** — the
  central open question this doc raises, not pre-decided.
- Message Expiry Interval has NO open design decisions — ready to
  implement as sketched above whenever prioritized.

## See also

- [Composable Capability Requirements](../design/d-0006-protocol-native-capabilities.md) — the shipped `Capability`/`ApplyCapabilities` mechanism this doc's `MessageExpiry`/`SharedSubscription` values plug into.
- [D-0006 — Protocol-Native Capabilities](../design/d-0006-protocol-native-capabilities.md) — §6's original feature survey identifying both candidates.
- [`docs/features/capabilities.md`](../features/capabilities.md) — "Surveyed but not implemented" section, the other cross-reference for these 2 candidates.
