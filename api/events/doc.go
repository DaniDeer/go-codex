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
// directly and pass it to the adapter (e.g. [adapters/mqtt.SubscribeHandler]).
package events
