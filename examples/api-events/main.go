// Package api-events demonstrates the api/events builder: define channels with
// codec-backed payload types, get typed Decode/Encode helpers, and generate a
// full AsyncAPI 2.6 spec — all without importing any messaging library.
//
// AsyncAPI operations are app-centric:
//   - Subscribe: this app RECEIVES messages on the channel (consumer)
//   - Publish:   this app SENDS messages on the channel (producer)
//   - Both:      bidirectional — set both Subscribe and Publish on one ChannelConfig
//
// The same ChannelHandle.Decode and ChannelHandle.Encode helpers work unchanged
// with MQTT (Paho), AMQP, Kafka, NATS, or any other message broker.
//
// Run with: go run ./examples/api-events
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- Domain event types ---

// UserCreatedEvent is received by this service when a new user registers.
type UserCreatedEvent struct {
	ID    string
	Name  string
	Email string
}

// NotificationCommand is sent by this service to trigger a notification.
type NotificationCommand struct {
	Recipient string
	Subject   string
	Body      string
}

// --- Codecs: single source of truth for encode, decode, validation, schema ---

var userCreatedCodec = codex.Struct[UserCreatedEvent](
	codex.Field[UserCreatedEvent, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.UUID).WithDescription("New user's UUID."),
		Get:      func(e UserCreatedEvent) string { return e.ID },
		Set:      func(e *UserCreatedEvent, v string) { e.ID = v },
		Required: true,
	},
	codex.Field[UserCreatedEvent, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Full display name."),
		Get:      func(e UserCreatedEvent) string { return e.Name },
		Set:      func(e *UserCreatedEvent, v string) { e.Name = v },
		Required: true,
	},
	codex.Field[UserCreatedEvent, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Email).WithDescription("Primary email address."),
		Get:      func(e UserCreatedEvent) string { return e.Email },
		Set:      func(e *UserCreatedEvent, v string) { e.Email = v },
		Required: true,
	},
)

var notificationCommandCodec = codex.Struct[NotificationCommand](
	codex.Field[NotificationCommand, string]{
		Name:     "recipient",
		Codec:    codex.String().Refine(validate.Email).WithDescription("Recipient email address."),
		Get:      func(c NotificationCommand) string { return c.Recipient },
		Set:      func(c *NotificationCommand, v string) { c.Recipient = v },
		Required: true,
	},
	codex.Field[NotificationCommand, string]{
		Name:     "subject",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Notification subject line."),
		Get:      func(c NotificationCommand) string { return c.Subject },
		Set:      func(c *NotificationCommand, v string) { c.Subject = v },
		Required: true,
	},
	codex.Field[NotificationCommand, string]{
		Name:     "body",
		Codec:    codex.String().Refine(validate.NonEmptyString).WithDescription("Notification body text."),
		Get:      func(c NotificationCommand) string { return c.Body },
		Set:      func(c *NotificationCommand, v string) { c.Body = v },
		Required: true,
	},
)

func main() {
	// Build the event API: register channels with codecs.
	// No messaging library import required.
	b := events.NewBuilder(events.Info{
		Title:       "Notification Service Events",
		Version:     "1.0.0",
		Description: "Channels for the notification service: receives user events, sends notification commands.",
	})
	b.AddServer("production", events.Server{
		URL:         "amqp://broker.example.com",
		Protocol:    "amqp",
		Description: "Production message broker",
	})

	// user/created — Subscribe: this app RECEIVES events when users register.
	// AsyncAPI "subscribe" means the broker delivers messages to this application.
	userCreated := events.AddChannel[UserCreatedEvent](b, "user/created", userCreatedCodec,
		events.ChannelConfig{
			Description: "User registration events consumed by the notification service.",
			Subscribe: &events.OperationConfig{
				Summary:    "Receive user created event",
				Tags:       []string{"user", "registration"},
				SchemaName: "UserCreatedEvent",
			},
		})

	// notification/send — Publish: this app SENDS notification commands.
	// AsyncAPI "publish" means this application produces messages to the broker.
	notificationSend := events.AddChannel[NotificationCommand](b, "notification/send", notificationCommandCodec,
		events.ChannelConfig{
			Description: "Notification commands sent by this service to trigger delivery.",
			Publish: &events.OperationConfig{
				Summary:    "Send notification command",
				Tags:       []string{"notification"},
				SchemaName: "NotificationCommand",
			},
		})

	// Bidirectional example (Both directions on one channel):
	// ChannelConfig{
	//     Subscribe: &events.OperationConfig{Summary: "Receive command result"},
	//     Publish:   &events.OperationConfig{Summary: "Send command"},
	// }

	// --- Demonstrate codec-backed Decode/Encode ---
	// These helpers work with any broker library; pass them to your callbacks.

	fmt.Println("=== Decode + Encode demo (transport-agnostic) ===")
	fmt.Println()

	// Subscribe path: decode an incoming payload (broker → app).
	payload := []byte(`{"id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","name":"Alice","email":"alice@example.com"}`)
	event, err := userCreated.Decode(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Decode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Received event: %+v\n", event)

	// Invalid payload → validation error from codec.
	_, err = userCreated.Decode([]byte(`{"id":"not-a-uuid","name":"","email":"bad"}`))
	fmt.Printf("Validation error: %v\n", err)
	fmt.Println()

	// Publish path: encode an outgoing command (app → broker).
	cmd := NotificationCommand{
		Recipient: event.Email,
		Subject:   "Welcome to the platform!",
		Body:      "Hi " + event.Name + ", your account is ready.",
	}
	encoded, err := notificationSend.Encode(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Encode error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Publishing to %s: %s\n", notificationSend.Topic, encoded)
	fmt.Println()

	// Channel handles expose the topic for broker registration.
	fmt.Printf("userCreated topic:    %s\n", userCreated.Topic)
	fmt.Printf("notificationSend topic: %s\n", notificationSend.Topic)
	fmt.Println()

	// --- Generate AsyncAPI 2.6 spec from the same builder ---
	fmt.Println("=== AsyncAPI 2.6 spec ===")
	fmt.Println()

	doc, err := b.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "AsyncAPISpec error: %v\n", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "MarshalYAML error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))
}
