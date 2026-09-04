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
	"github.com/DaniDeer/go-codex/api/rest"
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
// Each service calls OrderChannel.WithPublish(...)/.WithSubscribe(...)
// .Handle(builder) to get a *ChannelHandle and register the channel in
// its own events.Client. Both services get the same topic template,
// codec, and format — the contract is enforced by the Go compiler and
// the shared codec constraints.
//
// The topic param is MERGE-capable (events.NewTopicParam, not a plain
// events.TopicParam) — orderId is derived directly from Order.ID, so a
// caller never builds the topic vars map by hand ("one struct, one
// call"). GobFormat is declared inline via events.Formats — a
// first-class part of the channel's own declaration — so Client.Attach's
// Publish/Subscribe resolve it automatically (see the fix documented in
// docs/design/d-0002-pubsub-workflow-simplification.md's Decision 9); no
// per-call format override or post-hoc handle.WithFormats call is
// needed.
var OrderChannel = events.NewChannel(
	"orders/{orderId}",
	OrderCodec,
	events.ChannelMeta{
		Title:       "Order Events",
		Description: "Publishes Order messages whenever a new order is placed.",
	},
	events.NewTopicParam("orderId", codex.String().Refine(validate.UUID),
		func(o Order) string { return o.ID },
		func(o *Order, v string) { o.ID = v },
	).WithDescription("The UUID of the order being published."),
	events.Formats(GobFormat),
)

// OrderSubscriber forks OrderChannel's shared declaration into the
// subscribe role — see [events.Channel.WithSubscribe]. Needed alongside
// OrderChannel's own WithPublish fork so the pub/sub demo in main.go can
// exercise BOTH client.Publish and client.Subscribe against the SAME
// channel declaration.
var OrderSubscriber = OrderChannel.WithSubscribe(events.Subscribe{
	Summary:    "Receive order event",
	SchemaName: "Order",
	Tags:       []string{"orders"},
})

// OrderPublisher forks OrderChannel's shared declaration into the publish
// role, mirroring OrderSubscriber above.
var OrderPublisher = OrderChannel.WithPublish(events.Publish{
	Summary:    "Send order event",
	SchemaName: "Order",
	Tags:       []string{"orders"},
})

// OrderRoute is REST's mirror-image declaration of the SAME "Go library
// as contract" pattern: an echo-style route (receives an Order, returns
// it unchanged) with GobFormat declared for BOTH directions
// (rest.RequestFormats for the request body, rest.Formats for the
// response body) — proving the identical Decision 9 fix on the REST
// side (docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 2):
// Client.Call now resolves a route's declared Gob format automatically,
// for both EncodeRequestWithFormats and DecodeResponseWithFormats.
var OrderRoute = rest.NewRoute[Order, Order]("POST", "/orders",
	OrderCodec, OrderCodec,
	rest.RouteMeta{
		OperationID: "createOrder",
		Summary:     "Echo an Order — Gob request AND response body",
	},
	rest.RequestFormats[Order](GobFormat),
	rest.Formats[Order](GobFormat),
)
