package main

import (
	"encoding/json"
	"fmt"
	"regexp"

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
	ID       string
	Customer Customer
	Shipping Address
	Items    []LineItem
	Note     string // optional
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
	codex.Field[Order, string]{
		Name:     "note",
		Codec:    codex.String(),
		Get:      func(o Order) string { return o.Note },
		Set:      func(o *Order, v string) { o.Note = v },
		Required: false,
	},
)

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
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
		"note": "Please leave at door",
	}

	order, err := orderCodec.Decode(raw)
	if err != nil {
		fmt.Println("decode error:", err)
		return
	}
	fmt.Printf("order id:     %s\n", order.ID)
	fmt.Printf("customer:     %s <%s>\n", order.Customer.Name, order.Customer.Email)
	fmt.Printf("ship to:      %s, %s, %s\n", order.Shipping.Street, order.Shipping.City, order.Shipping.Country)
	for i, item := range order.Items {
		fmt.Printf("item %d:       %s × %d @ $%.2f\n", i+1, item.Product, item.Quantity, item.Price)
	}
	fmt.Printf("note:         %s\n", order.Note)

	// Validation error: negative quantity.
	fmt.Println()
	badRaw := map[string]any{
		"id": "ord-002",
		"customer": map[string]any{
			"name":  "Bob",
			"email": "bob@example.com",
		},
		"shipping": map[string]any{
			"street": "1 Bad St", "city": "Errortown", "country": "Nowhere",
		},
		"items": []any{
			map[string]any{"product": "Widget C", "quantity": -1, "price": 5.00},
		},
	}
	_, err = orderCodec.Decode(badRaw)
	fmt.Println("validation error:", err)

	// Validation error: invalid email.
	badEmail := map[string]any{
		"id":       "ord-003",
		"customer": map[string]any{"name": "Carol", "email": "not-an-email"},
		"shipping": map[string]any{"street": "1 St", "city": "City", "country": "Land"},
		"items":    []any{map[string]any{"product": "X", "quantity": 1, "price": 1.0}},
	}
	_, err = orderCodec.Decode(badEmail)
	fmt.Println("email error:     ", err)

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
	order2, err := orderCodec.Decode(roundTrip)
	if err != nil {
		fmt.Println("round-trip error:", err)
		return
	}
	fmt.Printf("round-trip ok:   id=%s items=%d\n", order2.ID, len(order2.Items))

	// Schema — shows the full nested structure.
	fmt.Println()
	schemaJSON, _ := json.MarshalIndent(orderCodec.Schema, "", "  ")
	fmt.Printf("schema:\n%s\n", schemaJSON)
}
