// Package event-driven demonstrates generating a full AsyncAPI 2.6 document from
// channel descriptors and Codec-derived schemas using the render/asyncapi package.
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

// UserCreatedEvent is published when a new user registers.
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

// OrderPlacedEvent is published when a user places an order.
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

func main() {
	doc, err := asyncapi.NewDocumentBuilder(asyncapi.Info{
		Title:       "E-Commerce Events",
		Version:     "1.0.0",
		Description: "Domain events for the e-commerce platform.",
	}).
		AddServer("production", asyncapi.Server{
			URL:         "amqp://broker.example.com",
			Protocol:    "amqp",
			Description: "Production message broker",
		}).
		AddChannel("user/created", asyncapi.ChannelItem{
			Description: "Published when a new user registers.",
			Subscribe: &asyncapi.Operation{
				Summary:     "User created",
				Description: "Emitted by the user service after successful registration.",
				Tags:        []string{"user", "registration"},
				Message: asyncapi.Message{
					Name:       "UserCreatedEvent",
					Schema:     UserCreatedEventCodec.Schema,
					SchemaName: "UserCreatedEvent",
				},
			},
		}).
		AddChannel("order/placed", asyncapi.ChannelItem{
			Description: "Published when a user places an order.",
			Subscribe: &asyncapi.Operation{
				Summary:     "Order placed",
				Description: "Emitted by the order service after successful checkout.",
				Tags:        []string{"order"},
				Message: asyncapi.Message{
					Name:       "OrderPlacedEvent",
					Schema:     OrderPlacedEventCodec.Schema,
					SchemaName: "OrderPlacedEvent",
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
