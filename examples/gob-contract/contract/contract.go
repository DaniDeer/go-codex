// Package contract is the shared API contract between producer and consumer services.
//
// Import this package in both services. The Go compiler enforces the contract:
// any field rename, type change, or constraint modification breaks compilation on
// both sides immediately — no stale YAML, no schema drift, no code-generation step.
//
// format.Gob provides the binary wire encoding. Both services use the same codec
// and the same format, so validation rules are enforced on both ends automatically.
//
// This is the "Go library as contract" pattern: instead of OpenAPI/AsyncAPI as the
// cross-service contract (useful for human documentation but powerless at compile
// time), a shared Go module is the authoritative contract for internal Go-to-Go
// communication.
package contract

import (
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// Order is the shared type exchanged between producer and consumer services.
// All fields are exported — required by encoding/gob.
type Order struct {
	ID       string
	Product  string
	Quantity int
	Price    float64
}

// OrderCodec is the single source of truth for Order: encode, decode, and schema.
// Both services use this codec — constraints are enforced on producer (marshal)
// and consumer (unmarshal) without any extra wiring.
var OrderCodec = codex.Struct[Order](
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID).WithTitle("Order ID").WithDescription("Unique order identifier (UUID v4)."),
		func(o Order) string { return o.ID },
		func(o *Order, v string) { o.ID = v },
	),
	codex.RequiredField("product",
		codex.String().Refine(validate.NonEmptyString).WithTitle("Product").WithDescription("Product name or SKU."),
		func(o Order) string { return o.Product },
		func(o *Order, v string) { o.Product = v },
	),
	codex.RequiredField("quantity",
		codex.Int().Refine(validate.PositiveInt).WithTitle("Quantity").WithDescription("Number of units ordered (must be ≥ 1)."),
		func(o Order) int { return o.Quantity },
		func(o *Order, v int) { o.Quantity = v },
	),
	codex.RequiredField("price",
		codex.Float64().Refine(validate.PositiveFloat).WithTitle("Price").WithDescription("Unit price in USD (must be > 0)."),
		func(o Order) float64 { return o.Price },
		func(o *Order, v float64) { o.Price = v },
	),
)

// GobFormat is the binary wire format for Order messages.
// Use this in both producer (Marshal) and consumer (Unmarshal).
//
// Gob encodes typed Go values directly — no map[string]any intermediate.
// Codec constraints run on marshal (producer side) and unmarshal (consumer side).
var GobFormat = format.Gob(OrderCodec)

// OrderChannel is the typed channel definition for the orders topic.
// Each service calls OrderChannel.Register(builder) to get a *ChannelHandle
// and register the channel in its own events.Builder. Both services get the
// same topic template, codec, and format — the contract is enforced by the
// Go compiler and the shared codec constraints.
var OrderChannel = events.NewChannel(
	"orders/{orderId}",
	OrderCodec,
	events.ChannelMeta{
		Title:       "Order Events",
		Description: "Publishes Order messages whenever a new order is placed.",
	},
	events.TopicParam{Name: "orderId", Description: "The UUID of the order being published."},
	events.Publish{
		Summary:    "Send order event",
		SchemaName: "Order",
		Tags:       []string{"orders"},
	},
)
