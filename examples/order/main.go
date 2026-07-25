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
	ID       string
	Customer Customer
	Shipping Address
	// BillingAddress reuses the SAME Address type/codec as Shipping — the
	// only difference is RequiredField vs OptionalField below. Absent input
	// decodes to a zero-value Address{} (not a pointer/Nullable): Required
	// vs Optional is orthogonal to whether a field's codec is a nested
	// Struct or a scalar.
	BillingAddress Address
	Items          []LineItem
	Tags           map[string]string // e.g. {"channel":"web","priority":"high"}
	Note           *string           // optional free-text note (nil = absent)
	CreatedAt      time.Time         // RFC 3339 timestamp
	DeliveryDate   *time.Time        // optional promised delivery date (date-only)
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var emailPattern = regexp.MustCompile(`^[^@]+@[^@]+\.[^@]+$`)

var addressCodec = codex.Struct[Address](
	codex.RequiredField("street", codex.String().Refine(validate.NonEmptyString), func(a Address) string { return a.Street }, func(a *Address, v string) { a.Street = v }),
	codex.RequiredField("city", codex.String().Refine(validate.NonEmptyString), func(a Address) string { return a.City }, func(a *Address, v string) { a.City = v }),
	codex.RequiredField("country", codex.String().Refine(validate.NonEmptyString), func(a Address) string { return a.Country }, func(a *Address, v string) { a.Country = v }),
)

var customerCodec = codex.Struct[Customer](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString), func(c Customer) string { return c.Name }, func(c *Customer, v string) { c.Name = v }),
	codex.RequiredField("email", codex.String().Refine(validate.Pattern(emailPattern)), func(c Customer) string { return c.Email }, func(c *Customer, v string) { c.Email = v }),
)

var lineItemCodec = codex.Struct[LineItem](
	codex.RequiredField("product", codex.String().Refine(validate.NonEmptyString), func(l LineItem) string { return l.Product }, func(l *LineItem, v string) { l.Product = v }),
	codex.RequiredField("quantity", codex.Int().Refine(validate.PositiveInt), func(l LineItem) int { return l.Quantity }, func(l *LineItem, v int) { l.Quantity = v }),
	codex.RequiredField("price", codex.Float64().Refine(validate.PositiveFloat), func(l LineItem) float64 { return l.Price }, func(l *LineItem, v float64) { l.Price = v }),
)

var orderCodec = codex.Struct[Order](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString), func(o Order) string { return o.ID }, func(o *Order, v string) { o.ID = v }),
	codex.RequiredField("customer", customerCodec, func(o Order) Customer { return o.Customer }, func(o *Order, v Customer) { o.Customer = v }),
	codex.RequiredField("shipping", addressCodec, func(o Order) Address { return o.Shipping }, func(o *Order, v Address) { o.Shipping = v }),
	// Optional nested struct: same addressCodec as "shipping" above, but
	// OptionalField instead of RequiredField — absent input decodes to a
	// zero-value Address{} and the nested codec's own "street"/"city"/
	// "country" Required checks only run when "billingAddress" IS present.
	codex.OptionalField("billingAddress", addressCodec, func(o Order) Address { return o.BillingAddress }, func(o *Order, v Address) { o.BillingAddress = v }),
	codex.RequiredField("items", codex.SliceOf(lineItemCodec), func(o Order) []LineItem { return o.Items }, func(o *Order, v []LineItem) { o.Items = v }),
	// StringMap: arbitrary string key/value labels on the order.
	codex.OptionalField("tags", codex.StringMap(codex.String()), func(o Order) map[string]string { return o.Tags }, func(o *Order, v map[string]string) { o.Tags = v }),
	// Nullable: note is optional; nil means the field is absent (JSON null / omitted).
	codex.OptionalField("note", codex.Nullable(codex.String()), func(o Order) *string { return o.Note }, func(o *Order, v *string) { o.Note = v }),
	// Time: creation timestamp encoded as RFC 3339.
	codex.RequiredField("createdAt", codex.Time(), func(o Order) time.Time { return o.CreatedAt }, func(o *Order, v time.Time) { o.CreatedAt = v }),
	// Nullable + Date: optional promised delivery date encoded as YYYY-MM-DD.
	codex.OptionalField("deliveryDate", codex.Nullable(codex.Date()), func(o Order) *time.Time { return o.DeliveryDate }, func(o *Order, v *time.Time) { o.DeliveryDate = v }),
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
		// billingAddress present — decodes through addressCodec like shipping.
		"billingAddress": map[string]any{
			"street":  "1 Billing Ave",
			"city":    "Invoicetown",
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
	fmt.Printf("bill to:       %s, %s, %s\n", order.BillingAddress.Street, order.BillingAddress.City, order.BillingAddress.Country)
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
	// Also omits "billingAddress" entirely (not nil — the key is just
	// absent) to show the OptionalField-nested-struct zero-value case,
	// distinct from Nullable's explicit-nil case used by note/deliveryDate.
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
		// no "billingAddress" key at all.
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
	fmt.Printf("order2 note:           %v (nil = absent)\n", order2.Note)
	fmt.Printf("order2 deliveryDate:   %v (nil = absent)\n", order2.DeliveryDate)
	fmt.Printf("order2 billingAddress: %+v (zero value = absent, OptionalField on a nested struct)\n", order2.BillingAddress)

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

	// Validation error: "billingAddress" IS present (so its own codec runs)
	// but missing its own required "country" field — proves that declaring
	// the OUTER field Optional never weakens the nested struct's OWN
	// required-field checks; they only apply when the key is present.
	fmt.Println()
	badBillingRaw := map[string]any{
		"id": "ord-004",
		"customer": map[string]any{
			"name":  "Dana",
			"email": "dana@example.com",
		},
		"shipping": map[string]any{
			"street": "1 Ship St", "city": "Shiptown", "country": "Shipland",
		},
		"billingAddress": map[string]any{
			"street": "1 Bill St", "city": "Billtown", // "country" missing
		},
		"items":     []any{map[string]any{"product": "Widget E", "quantity": 1, "price": 5.00}},
		"createdAt": "2024-06-18T08:00:00Z",
	}
	_, err = orderCodec.Decode(badBillingRaw)
	fmt.Println("optional-nested-struct validation error:", err)

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
