// Package events provides a transport-agnostic event channel builder for go-codex.
//
// Define channels declaratively with codec-backed payload types; register them
// with a [Builder] to obtain a [ChannelHandle] with typed Decode and Encode helpers.
// Pass those helpers to any message broker (MQTT, AMQP, Kafka, NATS) — this package
// does not import any messaging library.
//
// Spec generation is also available: [Builder.AsyncAPISpec] derives a complete
// AsyncAPI 3.0 document from the registered channels.
//
// Typical usage:
//
//	b := events.NewBuilder(events.Info{Title: "User Events", Version: "1.0.0"})
//	b.AddServer("production", events.Server{
//	    URL:      "mqtt://broker.example.com",
//	    Protocol: "mqtt",
//	})
//
//	// Declare the channel as a value — define once, pass around, register later.
//	var userCreated = events.NewChannel[UserCreated]("user/created", userCreatedCodec,
//	    events.ChannelMeta{Description: "A user was created"},
//	    events.Subscribe{Summary: "Receive user created events", SchemaName: "UserCreatedEvent"},
//	)
//
//	handle, err := userCreated.Register(b)
//
//	// In your broker callback (any library):
//	event, err := handle.Decode(msg.Payload())   // JSON → UserCreated, validates
//	payload, err := handle.Encode(event)          // UserCreated → JSON
//
//	// AsyncAPI 3.0 spec:
//	doc, err := b.AsyncAPISpec()
//	yaml, _  := doc.MarshalYAML()
//
// Encoding is JSON only by default. For other formats construct a [format.Format]
// directly and pass it to the adapter (e.g. [adapters/mqtt.SubscribeHandler]).
package events
