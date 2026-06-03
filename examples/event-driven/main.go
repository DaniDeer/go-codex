// Package event-driven demonstrates generating a full AsyncAPI 3.0 document from
// channel descriptors and Codec-derived schemas using the render/asyncapi/v3 package.
//
// AsyncAPI 3.0 operations are app-centric:
//   - Subscribe (action: receive): this app RECEIVES messages (consumer)
//   - Publish   (action: send):    this app SENDS messages (producer)
//
// In AsyncAPI 3.0 channels and operations are separate top-level maps:
//   - channels: describe topics and their message schemas
//   - operations: link operations to channels via $ref (action: receive/send)
//
// Security schemes are declared once in components/securitySchemes and
// referenced per-operation via Security []route.SecurityRequirement.
//
// Run with: go run ./examples/event-driven
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	v3 "github.com/DaniDeer/go-codex/render/asyncapi/v3"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
)

// UserCreatedEvent is received by this service when a new user registers.
type UserCreatedEvent struct {
	ID    string
	Name  string
	Email string
}

var UserCreatedEventCodec = codex.Struct[UserCreatedEvent](
	codex.RequiredField("id", codex.String().Refine(validate.UUID).WithDescription("New user's UUID."), func(e UserCreatedEvent) string { return e.ID }, func(e *UserCreatedEvent, v string) { e.ID = v }),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString).WithDescription("Full display name."), func(e UserCreatedEvent) string { return e.Name }, func(e *UserCreatedEvent, v string) { e.Name = v }),
	codex.RequiredField("email", codex.String().Refine(validate.Email).WithDescription("Primary email address."), func(e UserCreatedEvent) string { return e.Email }, func(e *UserCreatedEvent, v string) { e.Email = v }),
)

// OrderPlacedEvent is received by this service when a user places an order.
type OrderPlacedEvent struct {
	OrderID string
	UserID  string
	Total   float64
}

var OrderPlacedEventCodec = codex.Struct[OrderPlacedEvent](
	codex.RequiredField("orderId", codex.String().Refine(validate.UUID).WithDescription("Unique order ID."), func(e OrderPlacedEvent) string { return e.OrderID }, func(e *OrderPlacedEvent, v string) { e.OrderID = v }),
	codex.RequiredField("userId", codex.String().Refine(validate.UUID).WithDescription("ID of the user who placed the order."), func(e OrderPlacedEvent) string { return e.UserID }, func(e *OrderPlacedEvent, v string) { e.UserID = v }),
	codex.RequiredField("total", codex.Float64().Refine(validate.PositiveFloat).WithDescription("Order total in USD."), func(e OrderPlacedEvent) float64 { return e.Total }, func(e *OrderPlacedEvent, v float64) { e.Total = v }),
)

// NotificationCommand is sent by this service to trigger a notification.
type NotificationCommand struct {
	Recipient string
	Subject   string
	Body      string
}

var NotificationCommandCodec = codex.Struct[NotificationCommand](
	codex.RequiredField("recipient", codex.String().Refine(validate.Email).WithDescription("Recipient email address."), func(c NotificationCommand) string { return c.Recipient }, func(c *NotificationCommand, v string) { c.Recipient = v }),
	codex.RequiredField("subject", codex.String().Refine(validate.NonEmptyString).WithDescription("Notification subject line."), func(c NotificationCommand) string { return c.Subject }, func(c *NotificationCommand, v string) { c.Subject = v }),
	codex.RequiredField("body", codex.String().Refine(validate.NonEmptyString).WithDescription("Notification body text."), func(c NotificationCommand) string { return c.Body }, func(c *NotificationCommand, v string) { c.Body = v }),
)

// CloudEvent models a CloudEvents 1.0 envelope (https://cloudevents.io/).
//
// Pure and Eq demonstrate two patterns for fixed/constrained fields:
//
//   - specversion uses Pure("1.0"): always encodes as "1.0" and always decodes
//     to "1.0" regardless of the wire value. No validation needed — the value
//     is set automatically.
//
//   - type uses Eq(String(), "com.example.order.placed"): decodes using String()
//     first (type coercion, format validation), then enforces the exact value.
//     Any other event type returns a ConstraintError.
type CloudEvent struct {
	SpecVersion string
	Type        string
	ID          string
}

const cloudEventType = "com.example.order.placed"

var CloudEventCodec = codex.Struct[CloudEvent](
	// Pure("1.0"): always decodes to "1.0" and encodes "1.0" regardless of input.
	codex.RequiredField("specversion", codex.Pure("1.0").WithDescription("CloudEvents specification version. Always 1.0."), func(e CloudEvent) string { return e.SpecVersion }, func(e *CloudEvent, v string) { e.SpecVersion = v }),
	// Eq(String(), cloudEventType): String() handles wire decoding; Eq enforces the exact value.
	codex.RequiredField("type", codex.Eq(codex.String(), cloudEventType).WithDescription("CloudEvent type. Must be " + cloudEventType + "."), func(e CloudEvent) string { return e.Type }, func(e *CloudEvent, v string) { e.Type = v }),
	codex.RequiredField("id", codex.String().Refine(validate.UUID).WithDescription("Unique event identifier (UUID v4)."), func(e CloudEvent) string { return e.ID }, func(e *CloudEvent, v string) { e.ID = v }),
)

func main() {
	// ── Pure + Eq demo ────────────────────────────────────────────────────────
	fmt.Println("=== Pure + Eq: CloudEvent codec ===")

	// Valid CloudEvent.
	event, err := CloudEventCodec.Decode(map[string]any{
		"specversion": "1.0",
		"type":        cloudEventType,
		"id":          "550e8400-e29b-41d4-a716-446655440000",
	})
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("decoded: specversion=%q type=%q id=%s\n", event.SpecVersion, event.Type, event.ID)
	}

	// Pure: specversion in wire data is ignored — always decodes to "1.0".
	event, err = CloudEventCodec.Decode(map[string]any{
		"specversion": "ignored",
		"type":        cloudEventType,
		"id":          "550e8400-e29b-41d4-a716-446655440000",
	})
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("pure: specversion wire=ignored → decoded=%q\n", event.SpecVersion)
	}

	// Eq: wrong event type returns a ConstraintError.
	_, err = CloudEventCodec.Decode(map[string]any{
		"specversion": "1.0",
		"type":        "com.example.user.created", // wrong type
		"id":          "550e8400-e29b-41d4-a716-446655440000",
	})
	fmt.Println("wrong type:", err)

	// Encode: Pure always writes "1.0" regardless of the CloudEvent.SpecVersion value.
	enc, err := CloudEventCodec.Encode(CloudEvent{
		SpecVersion: "anything",
		Type:        cloudEventType,
		ID:          "550e8400-e29b-41d4-a716-446655440000",
	})
	if err != nil {
		fmt.Println("encode error:", err)
	} else {
		encMap := enc.(map[string]any)
		fmt.Printf("encoded: specversion=%q (Pure ignores CloudEvent.SpecVersion)\n", encMap["specversion"])
	}

	fmt.Println()

	// ── AsyncAPI 3.0 document ─────────────────────────────────────────────────
	// Security schemes are declared once in components/securitySchemes and
	// referenced per-operation. Operations link to channels via $ref.
	doc, err := v3.NewDocumentBuilder(v3.Info{
		Title:       "Notification Service Events",
		Version:     "1.0.0",
		Description: "Channels for the notification service.",
	}).
		AddServer("production", v3.Server{
			URL:         "broker.example.com",
			Protocol:    "amqp",
			Description: "Production message broker",
			// Server-level security: all channels on this server require bearerAuth
			// unless a per-operation security field overrides it.
			Security: []route.SecurityRequirement{route.Require("bearerAuth")},
		}).
		// bearerAuth scheme: JWT Bearer token, format-validated.
		AddSecurityScheme("bearerAuth", route.BearerScheme("JWT")).
		// action: receive — this app RECEIVES user created events.
		// In AsyncAPI 3.0 the channel key is a logical identifier; Address is the topic.
		AddChannel("userCreated", v3.ChannelItem{
			Address:     "user/created",
			Description: "User registration events consumed by the notification service.",
			Subscribe: &v3.Operation{
				Summary:     "Receive user created event",
				Description: "Triggered after the user service completes registration.",
				Tags:        []string{"user", "registration"},
				// Per-operation security: same as server default here, but explicit.
				Security: []route.SecurityRequirement{route.Require("bearerAuth")},
				Message: v3.Message{
					Name:       "UserCreatedEvent",
					Schema:     UserCreatedEventCodec.Schema,
					SchemaName: "UserCreatedEvent",
				},
			},
		}).
		// action: receive — this app RECEIVES order placed events.
		AddChannel("orderPlaced", v3.ChannelItem{
			Address:     "order/placed",
			Description: "Order events consumed by the notification service.",
			Subscribe: &v3.Operation{
				Summary:     "Receive order placed event",
				Description: "Triggered after the order service completes checkout.",
				Tags:        []string{"order"},
				Security:    []route.SecurityRequirement{route.Require("bearerAuth")},
				Message: v3.Message{
					Name:       "OrderPlacedEvent",
					Schema:     OrderPlacedEventCodec.Schema,
					SchemaName: "OrderPlacedEvent",
				},
			},
		}).
		// action: send — this app SENDS notification commands to the broker.
		// Empty Security slice marks this outbound channel as unsecured (no auth
		// required to publish — the broker accepts commands from this service).
		AddChannel("notificationSend", v3.ChannelItem{
			Address:     "notification/send",
			Description: "Notification commands produced by this service.",
			Publish: &v3.Operation{
				Summary:     "Send notification command",
				Description: "Dispatched to the notification delivery worker.",
				Tags:        []string{"notification"},
				Security:    []route.SecurityRequirement{}, // explicitly unsecured
				Message: v3.Message{
					Name:       "NotificationCommand",
					Schema:     NotificationCommandCodec.Schema,
					SchemaName: "NotificationCommand",
				},
			},
		}).
		Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build error: %v\n", err)
		os.Exit(1)
	}

	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "render error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("# Full AsyncAPI 3.0 document (YAML)")
	fmt.Println("# channels + operations are separate top-level keys.")
	fmt.Println("# action: receive (subscribe) / action: send (publish).")
	fmt.Println()
	fmt.Print(string(yamlBytes))
}
