// Package api-events demonstrates the api/events builder: define channels with
// codec-backed payload types, get typed Decode/Encode helpers, and generate a
// full AsyncAPI 3.0 spec — all without importing any messaging library.
//
// AsyncAPI 3.0 operations are app-centric:
//   - Subscribe (action: receive): this app RECEIVES messages on the channel (consumer)
//   - Publish   (action: send):    this app SENDS messages on the channel (producer)
//   - Both:      bidirectional — pass both events.Subscribe and events.Publish to one AddChannel call
//
// Security schemes are declared once per channel via events.WithSecurityScheme
// and referenced via Subscribe.Security / Publish.Security. The spec output
// includes components/securitySchemes and per-operation security requirements.
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
	"github.com/DaniDeer/go-codex/route"
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
	codex.RequiredField("id", codex.String().Refine(validate.UUID).WithDescription("New user's UUID."), func(e UserCreatedEvent) string { return e.ID }, func(e *UserCreatedEvent, v string) { e.ID = v }),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString).WithDescription("Full display name."), func(e UserCreatedEvent) string { return e.Name }, func(e *UserCreatedEvent, v string) { e.Name = v }),
	codex.RequiredField("email", codex.String().Refine(validate.Email).WithDescription("Primary email address."), func(e UserCreatedEvent) string { return e.Email }, func(e *UserCreatedEvent, v string) { e.Email = v }),
)

// DeviceOnline/DeviceOffline share the SAME "devices/{deviceID}/status" topic
// shape (see events.Topic demo in main) despite being different payload types.
type DeviceOnline struct {
	DeviceID string
}
type DeviceOffline struct {
	DeviceID string
	Reason   string
}

var deviceOnlineCodec = codex.Struct[DeviceOnline](
	codex.RequiredField("deviceId", codex.String().Refine(validate.UUID), func(e DeviceOnline) string { return e.DeviceID }, func(e *DeviceOnline, v string) { e.DeviceID = v }),
)
var deviceOfflineCodec = codex.Struct[DeviceOffline](
	codex.RequiredField("deviceId", codex.String().Refine(validate.UUID), func(e DeviceOffline) string { return e.DeviceID }, func(e *DeviceOffline, v string) { e.DeviceID = v }),
	codex.RequiredField("reason", codex.String().Refine(validate.NonEmptyString), func(e DeviceOffline) string { return e.Reason }, func(e *DeviceOffline, v string) { e.Reason = v }),
)

var notificationCommandCodec = codex.Struct[NotificationCommand](
	codex.RequiredField("recipient", codex.String().Refine(validate.Email).WithDescription("Recipient email address."), func(c NotificationCommand) string { return c.Recipient }, func(c *NotificationCommand, v string) { c.Recipient = v }),
	codex.RequiredField("subject", codex.String().Refine(validate.NonEmptyString).WithDescription("Notification subject line."), func(c NotificationCommand) string { return c.Subject }, func(c *NotificationCommand, v string) { c.Subject = v }),
	codex.RequiredField("body", codex.String().Refine(validate.NonEmptyString).WithDescription("Notification body text."), func(c NotificationCommand) string { return c.Body }, func(c *NotificationCommand, v string) { c.Body = v }),
)

func main() {
	// Build the event API: register channels with codecs.
	// No messaging library import required.
	b := events.NewBuilder(events.Info{
		Title:       "Notification Service Events",
		Version:     "1.0.0",
		Description: "Channels for the notification service: receives user events, sends notification commands.",
	},
		// WithTopicConstraints is optional. When set, AddChannel returns an
		// InvalidTopicError immediately if the topic violates the constraint.
		// MQTTPublishTopic requires a non-empty topic with no wildcard characters
		// (+ or #), which is correct for producer channels.
		events.WithTopicConstraints(validate.MQTTPublishTopic),
	)
	b.AddServer("production", events.Server{
		URL:         "broker.example.com",
		Protocol:    "amqp",
		Description: "Production message broker",
	})

	// Declare a Bearer JWT security scheme once — referenced by subscribe channels.
	// The Codec field is optional; set it to validate the credential format at the
	// adapter layer (e.g. validate.JWT constraint) before calling SecurityFunc.
	bearerAuth := events.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	}

	// user/created — action: receive — this app RECEIVES events when users register.
	// Security: requires bearerAuth — adapters (e.g. adapters/mqtt) enforce this via
	// SubscribeOptions.SecurityFunc before calling the application handler.
	userCreated, err := events.NewChannel[UserCreatedEvent]("user/created", userCreatedCodec,
		events.ChannelMeta{Description: "User registration events consumed by the notification service."},
		events.Subscribe{
			Summary:    "Receive user created event",
			Tags:       []string{"user", "registration"},
			SchemaName: "UserCreatedEvent",
			// Security: requires bearerAuth on this operation.
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		},
		events.WithSecurityScheme("bearerAuth", bearerAuth),
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	// notification/send — action: send — this app SENDS notification commands.
	// No security on outbound channels: the broker accepts messages from this service.
	notificationSend, err := events.NewChannel[NotificationCommand]("notification/send", notificationCommandCodec,
		events.ChannelMeta{Description: "Notification commands sent by this service to trigger delivery."},
		events.Publish{
			Summary:    "Send notification command",
			Tags:       []string{"notification"},
			SchemaName: "NotificationCommand",
		},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}

	// Bidirectional example (both directions on one channel):
	// events.Subscribe{Summary: "Receive command result", Security: []route.SecurityRequirement{route.Require("bearerAuth")}},
	// events.Publish{Summary: "Send command"},

	// --- events.Topic: reusing a template+params shape (opt-in, NOT the default) ---
	//
	// The plain-string form above (events.NewChannel[T]("user/created", ...))
	// remains the default and primary way to declare a channel — nothing about
	// it changes. events.Topic is a SECOND, additional, opt-in constructor —
	// reach for it only when the SAME topic template + TopicParam declaration
	// would otherwise be copy-pasted across two or more channels of different
	// payload types, giving that shape exactly one source of truth.
	deviceIDCodec := codex.String().Refine(validate.UUID)
	deviceStatusTopic := events.NewTopic("devices/{deviceID}/status",
		events.TopicParam{Name: "deviceID", Codec: &deviceIDCodec},
	)
	deviceOnline, err := events.NewChannelFromTopic(deviceStatusTopic, deviceOnlineCodec,
		events.Subscribe{Summary: "Receive device online event", SchemaName: "DeviceOnline"},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}
	deviceOffline, err := events.NewChannelFromTopic(deviceStatusTopic, deviceOfflineCodec,
		events.Subscribe{Summary: "Receive device offline event", SchemaName: "DeviceOffline"},
	).Register(b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channel registration failed: %v\n", err)
		os.Exit(1)
	}
	// Both channels were built from the SAME Topic value — the {deviceID}
	// variable's name/codec has exactly one source of truth instead of being
	// declared twice. A Topic can also build/validate a topic standalone, with
	// no payload codec involved at all:
	standaloneTopic, err := deviceStatusTopic.BuildTopic(map[string]string{
		"deviceID": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "BuildTopic error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("=== events.Topic: shared template+params shape ===")
	fmt.Printf("deviceOnline topic:  %s\n", deviceOnline.Topic)
	fmt.Printf("deviceOffline topic: %s\n", deviceOffline.Topic)
	fmt.Printf("standalone BuildTopic (no payload codec): %s\n", standaloneTopic)
	fmt.Println()

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

	// --- Generate AsyncAPI 3.0 spec from the same builder ---
	// The spec output includes:
	//   - asyncapi: 3.0.0
	//   - channels: topic descriptors (address, messages)
	//   - operations: linked to channels via $ref (action: receive / action: send)
	//   - components/securitySchemes: declared schemes (bearerAuth)
	fmt.Println("=== AsyncAPI 3.0 spec ===")
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
