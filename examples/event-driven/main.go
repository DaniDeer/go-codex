// Package event-driven demonstrates generating a full AsyncAPI 2.6 document from
// channel descriptors and Codec-derived schemas using the render/asyncapi package.
//
// AsyncAPI operations are app-centric:
//   - Subscribe: this app RECEIVES messages (consumer)
//   - Publish:   this app SENDS messages (producer)
//
// Run with: go run ./examples/event-driven
package main

import (
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/asyncapi"
	"github.com/DaniDeer/go-codex/validate"
)

// UserCreatedEvent is received by this service when a new user registers.
type UserCreatedEvent struct {
	ID    string
	Name  string
	Email string
}

var UserCreatedEventCodec = codex.Struct[UserCreatedEvent](
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

// OrderPlacedEvent is received by this service when a user places an order.
type OrderPlacedEvent struct {
	OrderID string
	UserID  string
	Total   float64
}

var OrderPlacedEventCodec = codex.Struct[OrderPlacedEvent](
	codex.Field[OrderPlacedEvent, string]{
		Name:     "orderId",
		Codec:    codex.String().Refine(validate.UUID).WithDescription("Unique order ID."),
		Get:      func(e OrderPlacedEvent) string { return e.OrderID },
		Set:      func(e *OrderPlacedEvent, v string) { e.OrderID = v },
		Required: true,
	},
	codex.Field[OrderPlacedEvent, string]{
		Name:     "userId",
		Codec:    codex.String().Refine(validate.UUID).WithDescription("ID of the user who placed the order."),
		Get:      func(e OrderPlacedEvent) string { return e.UserID },
		Set:      func(e *OrderPlacedEvent, v string) { e.UserID = v },
		Required: true,
	},
	codex.Field[OrderPlacedEvent, float64]{
		Name:     "total",
		Codec:    codex.Float64().Refine(validate.PositiveFloat).WithDescription("Order total in USD."),
		Get:      func(e OrderPlacedEvent) float64 { return e.Total },
		Set:      func(e *OrderPlacedEvent, v float64) { e.Total = v },
		Required: true,
	},
)

// NotificationCommand is sent by this service to trigger a notification.
type NotificationCommand struct {
	Recipient string
	Subject   string
	Body      string
}

var NotificationCommandCodec = codex.Struct[NotificationCommand](
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
	doc, err := asyncapi.NewDocumentBuilder(asyncapi.Info{
		Title:       "Notification Service Events",
		Version:     "1.0.0",
		Description: "Channels for the notification service.",
	}).
		AddServer("production", asyncapi.Server{
			URL:         "amqp://broker.example.com",
			Protocol:    "amqp",
			Description: "Production message broker",
		}).
		// Subscribe: this app RECEIVES user created events from the broker.
		AddChannel("user/created", asyncapi.ChannelItem{
			Description: "User registration events consumed by the notification service.",
			Subscribe: &asyncapi.Operation{
				Summary:     "Receive user created event",
				Description: "Triggered after the user service completes registration.",
				Tags:        []string{"user", "registration"},
				Message: asyncapi.Message{
					Name:       "UserCreatedEvent",
					Schema:     UserCreatedEventCodec.Schema,
					SchemaName: "UserCreatedEvent",
				},
			},
		}).
		// Subscribe: this app RECEIVES order placed events from the broker.
		AddChannel("order/placed", asyncapi.ChannelItem{
			Description: "Order events consumed by the notification service.",
			Subscribe: &asyncapi.Operation{
				Summary:     "Receive order placed event",
				Description: "Triggered after the order service completes checkout.",
				Tags:        []string{"order"},
				Message: asyncapi.Message{
					Name:       "OrderPlacedEvent",
					Schema:     OrderPlacedEventCodec.Schema,
					SchemaName: "OrderPlacedEvent",
				},
			},
		}).
		// Publish: this app SENDS notification commands to the broker.
		AddChannel("notification/send", asyncapi.ChannelItem{
			Description: "Notification commands produced by this service.",
			Publish: &asyncapi.Operation{
				Summary:     "Send notification command",
				Description: "Dispatched to the notification delivery worker.",
				Tags:        []string{"notification"},
				Message: asyncapi.Message{
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

	fmt.Println("# Full AsyncAPI 2.6 document (YAML)")
	fmt.Println()
	fmt.Print(string(yamlBytes))
}
