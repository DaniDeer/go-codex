// Package events provides a transport-agnostic event channel builder for go-codex.
//
// Pub/sub has no "server" role: a broker (MQTT/ZeroMQ) is the actual
// intermediary — both a publisher and a subscriber are CLIENTS of a channel.
// Define channels declaratively with codec-backed payload types, then use
// [Channel.WithSubscribe]/[Channel.WithPublish] to build a role-scoped
// [Subscriber]/[Publisher], and [Subscriber.Handle]/[Publisher.Handle] to
// obtain a [ChannelHandle] with typed Decode and Encode helpers. Pass those
// helpers to any message broker (MQTT, AMQP, Kafka, NATS) — this package
// does not import any messaging library.
//
// A [Client] (create one with [NewClient]) accumulates channel registrations
// and produces AsyncAPI specs: [Client.AsyncAPISpec] derives a complete
// AsyncAPI 3.0 document from every channel registered against it via
// [Subscriber.Handle]/[Publisher.Handle] with a non-nil client. Passing nil
// instead builds a spec-free handle — no [Client] required at all.
//
// Typical usage:
//
//	c := events.NewClient(events.WithInfo(events.Info{Title: "User Events", Version: "1.0.0"}))
//	c.AddServer("production", events.Server{
//	    URL:      "mqtt://broker.example.com",
//	    Protocol: "mqtt",
//	})
//
//	// Declare the channel as a value — define once, pass around, register later.
//	var userCreated = events.NewChannel[UserCreated]("user/created", userCreatedCodec,
//	    events.ChannelMeta{Description: "A user was created"},
//	)
//
//	sub := userCreated.WithSubscribe(events.Subscribe{Summary: "Receive user created events", SchemaName: "UserCreatedEvent"})
//	handle, err := sub.Handle(c)
//
//	// In your broker callback (any library):
//	event, err := handle.Decode(msg.Payload())   // JSON → UserCreated, validates
//	payload, err := handle.Encode(event)          // UserCreated → JSON
//
//	// AsyncAPI 3.0 spec:
//	doc, err := c.AsyncAPISpec()
//	yaml, _  := doc.MarshalYAML()
//
// Encoding is JSON only by default. For other formats construct a [format.Format]
// directly and pass it to the adapter's transport constructor (e.g.
// adapters/mqtt.NewSubscribeTransport/NewPublishTransport, consumed via
// [SubscribeHandle]/[PublishHandle]) or attach it declaratively via [Formats]/
// [SubscribeFormats]/[PublishFormats].
//
// # Protocol-native capabilities
//
// A channel's protocol behavior (MQTT QoS, retained messages, ZeroMQ
// high-water-mark, ...) is classified into a three-tier vocabulary — see
// docs/design/d-0006-protocol-native-capabilities.md for the full design:
//
//   - Baseline — the Topic itself (matching the address, encoding/decoding
//     the declared payload codec). Mandatory, never declared explicitly; the
//     precondition every channel already satisfies.
//   - Implicit — a requirement that arises as a side effect of declaring a
//     codec-backed field (e.g. an adapter's own UserPropertyParam-style
//     option). Declaring the field IS the requirement.
//   - Explicit — a standalone requirement, declared via [CapabilityRequirement]
//     (or the sugar helpers [RequireQoS]/[RequireRetained]/[RequireHWM]/
//     [RequireConflate]), independent of any adapter until [Client.Attach] time.
//     [CheckCapabilityCoverage] verifies the supplied adapter Capability
//     values satisfy every declared requirement — including, for
//     requirements with a MinLevel (e.g. RequireQoS), a genuine VALUE
//     check via the optional [LeveledCapability] interface, not just
//     presence-by-name.
//
// Thin-adapter helpers — every adapter maps its own declared Capability
// values to the wire/protocol using these three, instead of hand-rolling
// the same boilerplate per adapter: [VerifyCapabilityCoverage] (guard +
// coverage-check in one call), [ResolveCapabilityValue] (generic
// last-matching-value extraction from a `[]<pkg>.Capability` slice), and
// [RecordCapabilityApplied] (report a successfully-applied capability to
// an optional [stats.CapabilityObserver]).
package events
