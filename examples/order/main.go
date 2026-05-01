package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Types ─────────────────────────────────────────────────────────────────────

type Address struct {
	Street  string
	City    string
	Country string
}

type Customer struct {
	Name  string
	Email string
}

type LineItem struct {
	Product  string
	Quantity int
	Price    float64
}

type Order struct {
	ID           string
	Customer     Customer
	Shipping     Address
	Items        []LineItem
	Tags         map[string]string // e.g. {"channel":"web","priority":"high"}
	Note         *string           // optional free-text note (nil = absent)
	CreatedAt    time.Time         // RFC 3339 timestamp
	DeliveryDate *time.Time        // optional promised delivery date (date-only)
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var emailPattern = regexp.MustCompile(`^[^@]+@[^@]+\.[^@]+$`)

var addressCodec = codex.Struct[Address](
	codex.Field[Address, string]{
		Name:     "street",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(a Address) string { return a.Street },
		Set:      func(a *Address, v string) { a.Street = v },
		Required: true,
	},
	codex.Field[Address, string]{
		Name:     "city",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(a Address) string { return a.City },
		Set:      func(a *Address, v string) { a.City = v },
		Required: true,
	},
	codex.Field[Address, string]{
		Name:     "country",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(a Address) string { return a.Country },
		Set:      func(a *Address, v string) { a.Country = v },
		Required: true,
	},
)

var customerCodec = codex.Struct[Customer](
	codex.Field[Customer, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(c Customer) string { return c.Name },
		Set:      func(c *Customer, v string) { c.Name = v },
		Required: true,
	},
	codex.Field[Customer, string]{
		Name:     "email",
		Codec:    codex.String().Refine(validate.Pattern(emailPattern)),
		Get:      func(c Customer) string { return c.Email },
		Set:      func(c *Customer, v string) { c.Email = v },
		Required: true,
	},
)

var lineItemCodec = codex.Struct[LineItem](
	codex.Field[LineItem, string]{
		Name:     "product",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(l LineItem) string { return l.Product },
		Set:      func(l *LineItem, v string) { l.Product = v },
		Required: true,
	},
	codex.Field[LineItem, int]{
		Name:     "quantity",
		Codec:    codex.Int().Refine(validate.PositiveInt),
		Get:      func(l LineItem) int { return l.Quantity },
		Set:      func(l *LineItem, v int) { l.Quantity = v },
		Required: true,
	},
	codex.Field[LineItem, float64]{
		Name:     "price",
		Codec:    codex.Float64().Refine(validate.PositiveFloat),
		Get:      func(l LineItem) float64 { return l.Price },
		Set:      func(l *LineItem, v float64) { l.Price = v },
		Required: true,
	},
)

var orderCodec = codex.Struct[Order](
	codex.Field[Order, string]{
		Name:     "id",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(o Order) string { return o.ID },
		Set:      func(o *Order, v string) { o.ID = v },
		Required: true,
	},
	codex.Field[Order, Customer]{
		Name:     "customer",
		Codec:    customerCodec,
		Get:      func(o Order) Customer { return o.Customer },
		Set:      func(o *Order, v Customer) { o.Customer = v },
		Required: true,
	},
	codex.Field[Order, Address]{
		Name:     "shipping",
		Codec:    addressCodec,
		Get:      func(o Order) Address { return o.Shipping },
		Set:      func(o *Order, v Address) { o.Shipping = v },
		Required: true,
	},
	codex.Field[Order, []LineItem]{
		Name:     "items",
		Codec:    codex.SliceOf(lineItemCodec),
		Get:      func(o Order) []LineItem { return o.Items },
		Set:      func(o *Order, v []LineItem) { o.Items = v },
		Required: true,
	},
	// StringMap: arbitrary string key/value labels on the order.
	codex.Field[Order, map[string]string]{
		Name:     "tags",
		Codec:    codex.StringMap(codex.String()),
		Get:      func(o Order) map[string]string { return o.Tags },
		Set:      func(o *Order, v map[string]string) { o.Tags = v },
		Required: false,
	},
	// Nullable: note is optional; nil means the field is absent (JSON null / omitted).
	codex.Field[Order, *string]{
		Name:     "note",
		Codec:    codex.Nullable(codex.String()),
		Get:      func(o Order) *string { return o.Note },
		Set:      func(o *Order, v *string) { o.Note = v },
		Required: false,
	},
	// Time: creation timestamp encoded as RFC 3339.
	codex.Field[Order, time.Time]{
		Name:     "createdAt",
		Codec:    codex.Time(),
		Get:      func(o Order) time.Time { return o.CreatedAt },
		Set:      func(o *Order, v time.Time) { o.CreatedAt = v },
		Required: true,
	},
	// Nullable + Date: optional promised delivery date encoded as YYYY-MM-DD.
	codex.Field[Order, *time.Time]{
		Name:     "deliveryDate",
		Codec:    codex.Nullable(codex.Date()),
		Get:      func(o Order) *time.Time { return o.DeliveryDate },
		Set:      func(o *Order, v *time.Time) { o.DeliveryDate = v },
		Required: false,
	},
)

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	note := "Please leave at door"
	delivery := "2024-07-01"
	// Raw input as it would arrive after json.Unmarshal into map[string]any.
	raw := map[string]any{
		"id": "ord-001",
		"customer": map[string]any{
			"name":  "Alice",
			"email": "alice@example.com",
		},
		"shipping": map[string]any{
			"street":  "123 Main St",
			"city":    "Exampleville",
			"country": "Exampleland",
		},
		"items": []any{
			map[string]any{"product": "Widget A", "quantity": 2, "price": 9.99},
			map[string]any{"product": "Widget B", "quantity": 1, "price": 24.50},
		},
		"tags":         map[string]any{"channel": "web", "priority": "high"},
		"note":         note,
		"createdAt":    "2024-06-15T09:00:00Z",
		"deliveryDate": delivery,
	}

	order, err := orderCodec.Decode(raw)
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("order id:      %s\n", order.ID)
	fmt.Printf("customer:      %s <%s>\n", order.Customer.Name, order.Customer.Email)
	fmt.Printf("ship to:       %s, %s, %s\n", order.Shipping.Street, order.Shipping.City, order.Shipping.Country)
	for i, item := range order.Items {
		fmt.Printf("item %d:        %s × %d @ $%.2f\n", i+1, item.Product, item.Quantity, item.Price)
	}
	fmt.Printf("tags:          %v\n", order.Tags)
	if order.Note != nil {
		fmt.Printf("note:          %s\n", *order.Note)
	}
	fmt.Printf("createdAt:     %s\n", order.CreatedAt.Format(time.RFC3339))
	if order.DeliveryDate != nil {
		fmt.Printf("deliveryDate:  %s\n", order.DeliveryDate.Format("2006-01-02"))
	}

	// Nullable: order with no note and no delivery date.
	fmt.Println()
	rawNoNote := map[string]any{
		"id": "ord-002",
		"customer": map[string]any{
			"name":  "Bob",
			"email": "bob@example.com",
		},
		"shipping": map[string]any{
			"street": "1 Main St", "city": "Testtown", "country": "Testland",
		},
		"items":        []any{map[string]any{"product": "Widget C", "quantity": 3, "price": 5.00}},
		"createdAt":    "2024-06-16T10:30:00Z",
		"note":         nil,
		"deliveryDate": nil,
	}
	order2, err := orderCodec.Decode(rawNoNote)
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("order2 note:         %v (nil = absent)\n", order2.Note)
	fmt.Printf("order2 deliveryDate: %v (nil = absent)\n", order2.DeliveryDate)

	// Validation error: negative quantity.
	fmt.Println()
	badRaw := map[string]any{
		"id": "ord-003",
		"customer": map[string]any{
			"name":  "Carol",
			"email": "carol@example.com",
		},
		"shipping": map[string]any{
			"street": "1 Bad St", "city": "Errortown", "country": "Nowhere",
		},
		"items":     []any{map[string]any{"product": "Widget D", "quantity": -1, "price": 5.00}},
		"createdAt": "2024-06-17T08:00:00Z",
	}
	_, err = orderCodec.Decode(badRaw)
	fmt.Println("validation error:", err)

	// Encode back to map.
	fmt.Println()
	encoded, err := orderCodec.Encode(order)
	if err != nil {
		fmt.Println("encode error:", err)
		return
	}
	encodedJSON, _ := json.MarshalIndent(encoded, "", "  ")
	fmt.Printf("encoded:\n%s\n", encodedJSON)

	// Full round-trip: Go value → JSON bytes → map[string]any → decode.
	fmt.Println()
	jsonBytes, _ := json.Marshal(encoded)
	var roundTrip map[string]any
	_ = json.Unmarshal(jsonBytes, &roundTrip)
	order3, err := orderCodec.Decode(roundTrip)
	if err != nil {
		fmt.Println("round-trip error:", err)
		return
	}
	fmt.Printf("round-trip ok:   id=%s items=%d tags=%v\n", order3.ID, len(order3.Items), order3.Tags)

	// Schema — shows the full nested structure including new field types.
	fmt.Println()
	schemaJSON, _ := json.MarshalIndent(orderCodec.Schema, "", "  ")
	fmt.Printf("schema:\n%s\n", schemaJSON)
}
