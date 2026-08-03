// Package enum-union-sum shows how go-codex handles the three key type-modeling
// patterns from Go: iota enums, union types, and sum types (discriminated unions).
//
// Each pattern maps to different codec primitives depending on whether the variant
// set is open or closed, and whether variants carry data:
//
//	Iota enum  — closed label set, no payload → MapCodecSafe + validate.OneOf
//	Union type — open set at runtime          → codex.Any()
//	Sum type   — closed set, variants carry data → TaggedUnion / UntaggedUnion / Either2
//
// # Decision guide
//
//	Pattern           | Set    | Variants carry data? | Codec primitive
//	------------------|--------|----------------------|---------------------------
//	Iota enum (str)   | Closed | No (label only)      | MapCodecSafe + OneOf
//	Iota enum (int)   | Closed | No (label only)      | MapCodecSafe + RangeInt
//	Union — open      | Open   | Yes (any type)       | codex.Any()
//	Sum type — tagged | Closed | Yes (different shapes)| TaggedUnion[T]
//	Sum type — binary | Closed | Yes (two branches)   | Either2
//	Bonus: string-or-number (e.g. env var "5" vs 5)    | StringOrInt64 (Either2 convenience)
//
// Run with: go run ./examples/enum-union-sum
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

// ── 1. Iota Enum — string wire encoding ──────────────────────────────────────
//
// Go has no enum keyword. The idiomatic substitute is a named integer type with
// iota constants. To use it with go-codex:
//   - Define the named type and constants as normal Go.
//   - Bridge to a string codec via MapCodecSafe + validate.OneOf.
//   - The schema emits {enum: ["pending", "approved", ...]} — correct in OpenAPI.

type OrderStatus int

const (
	Pending   OrderStatus = iota // 0
	Approved                     // 1
	Shipped                      // 2
	Cancelled                    // 3
)

var orderStatusNames = map[OrderStatus]string{
	Pending:   "pending",
	Approved:  "approved",
	Shipped:   "shipped",
	Cancelled: "cancelled",
}

var orderStatusByName = map[string]OrderStatus{
	"pending":   Pending,
	"approved":  Approved,
	"shipped":   Shipped,
	"cancelled": Cancelled,
}

// OrderStatusCodec bridges OrderStatus ↔ string via MapCodecSafe.
// The wire codec (String + OneOf) validates the string and annotates the schema.
// MapCodecSafe converts between the wire string and the Go named type.
var OrderStatusCodec = codex.MapCodecSafe(
	// Wire codec: validate the string is one of the known labels.
	// Schema: {type: string, enum: ["pending", "approved", "shipped", "cancelled"]}
	codex.String().Refine(validate.OneOf("pending", "approved", "shipped", "cancelled")),
	func(s string) OrderStatus { return orderStatusByName[s] }, // decode: string → OrderStatus
	func(s OrderStatus) (string, error) { // encode: OrderStatus → string
		name, ok := orderStatusNames[s]
		if !ok {
			return "", fmt.Errorf("unknown OrderStatus: %d", s)
		}
		return name, nil
	},
)

// ── 2. Iota Enum — integer wire encoding ─────────────────────────────────────
//
// When the integer representation matters on the wire (e.g. a database column
// or a binary protocol), bridge to Int instead of String.
// Trade-off: the schema loses variant names ({minimum:0, maximum:3} instead of
// {enum: ["pending", ...]}) — use string encoding for REST/event APIs where
// human-readable labels matter.

var OrderStatusIntCodec = codex.MapCodecSafe(
	// Wire codec: integer in valid range.
	// Schema: {type: integer, minimum: 0, maximum: 3}
	codex.Int().Refine(validate.RangeInt(int(Pending), int(Cancelled))),
	func(n int) OrderStatus { return OrderStatus(n) },       // decode: int → OrderStatus
	func(s OrderStatus) (int, error) { return int(s), nil }, // encode: OrderStatus → int
)

// ── 3. Union Type — open runtime union via codex.Any() ───────────────────────
//
// When the set of types is open and caller-controlled — dynamic config values,
// JSON blobs, expression evaluators — use codex.Any().
// There is no schema ({}), no validation. The value passes through unchanged.

var DynamicValueCodec = codex.Any()

// ── 4. Sum Type — sealed interface with TaggedUnion ──────────────────────────
//
// A sum type is a closed set of variants where each variant carries different
// data. Go's approximation: a sealed interface (unexported method). go-codex
// supports this directly via TaggedUnion — no external discriminator package needed.
//
// PaymentStatus is the sealed interface. Only this package can implement it.

type PaymentStatus interface {
	paymentStatusTag() // unexported → external packages cannot add variants
}

type PendingPayment struct{}
type CompletedPayment struct{ TxID string }
type FailedPayment struct{ Reason string }

func (PendingPayment) paymentStatusTag()   {}
func (CompletedPayment) paymentStatusTag() {}
func (FailedPayment) paymentStatusTag()    {}

var pendingCodec = codex.MapCodecSafe(
	codex.Struct[PendingPayment](),
	func(PendingPayment) PaymentStatus { return PendingPayment{} },
	func(p PaymentStatus) (PendingPayment, error) { return codex.Downcast[PendingPayment](p) },
)

var completedCodec = codex.MapCodecSafe(
	codex.Struct[CompletedPayment](
		codex.RequiredField("tx_id", codex.String().Refine(validate.NonEmptyString),
			func(c CompletedPayment) string { return c.TxID },
			func(c *CompletedPayment, v string) { c.TxID = v },
		),
	),
	func(c CompletedPayment) PaymentStatus { return c },
	func(p PaymentStatus) (CompletedPayment, error) { return codex.Downcast[CompletedPayment](p) },
)

var failedCodec = codex.MapCodecSafe(
	codex.Struct[FailedPayment](
		codex.RequiredField("reason", codex.String().Refine(validate.NonEmptyString),
			func(f FailedPayment) string { return f.Reason },
			func(f *FailedPayment, v string) { f.Reason = v },
		),
	),
	func(f FailedPayment) PaymentStatus { return f },
	func(p PaymentStatus) (FailedPayment, error) { return codex.Downcast[FailedPayment](p) },
)

// PaymentStatusCodec discriminates on the "status" field.
// Schema: {oneOf: [...], discriminator: {propertyName: "status", mapping: {...}}}
// Wire:   {"status":"completed","tx_id":"abc123"}
var PaymentStatusCodec = codex.TaggedUnion[PaymentStatus](
	"status",
	map[string]codex.Codec[PaymentStatus]{
		"pending":   pendingCodec,
		"completed": completedCodec,
		"failed":    failedCodec,
	},
	func(p PaymentStatus) (string, error) {
		switch p.(type) {
		case PendingPayment:
			return "pending", nil
		case CompletedPayment:
			return "completed", nil
		case FailedPayment:
			return "failed", nil
		default:
			return "", fmt.Errorf("unknown PaymentStatus: %T", p)
		}
	},
)

// ── 5. Sum Type — binary Either2 ─────────────────────────────────────────────
//
// Either2 is the simplest sum type: exactly two branches.
// Use it when a field can be one of two distinct types.
// Left branch is tried first; right branch is the fallback.
// Schema: {oneOf: [schemaA, schemaB]}

type ProductRef struct{ SKU string }

var productRefCodec = codex.Struct[ProductRef](
	codex.RequiredField("sku", codex.String().Refine(validate.NonEmptyString),
		func(r ProductRef) string { return r.SKU },
		func(r *ProductRef, v string) { r.SKU = v },
	),
)

// SkuOrInline: a product field that is either a SKU string reference
// OR an inline ProductRef object. The Left branch (string) is tried first.
// Schema: {oneOf: [{type:string}, {type:object,properties:{sku:...}}]}
var SkuOrInlineCodec = codex.Either2(
	codex.String().Refine(validate.NonEmptyString), // Left: plain SKU string
	productRefCodec, // Right: inline ProductRef object
)

// ── 5b. Bonus — StringOrInt64, the "string or number" convenience ───────────
//
// A very common special case of Either2: a config/env-style value that may be
// EITHER a string OR a number on the wire (Docker/IoT-Edge module env vars
// "5" vs 5, Kubernetes' apimachinery IntOrString, Terraform/HCL, Helm
// values.yaml). codex.StringOrInt64() (and its siblings StringOrInt/
// StringOrInt32/StringOrUint/StringOrUint64/StringOrFloat32/StringOrFloat64,
// one per numeric primitive) is a one-line named convenience over exactly
// this pattern: Either2(String(), Int64()). It works uniformly across JSON,
// YAML, and TOML — every numeric primitive's Decode already type-switches
// over each format library's native number representation (encoding/json's
// float64, yaml.v3's int/float64, BurntSushi/toml's int64/float64).
var CountOrLabelCodec = codex.StringOrInt64()

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	j := format.JSON(OrderStatusCodec)

	// ── Section 1: Iota enum (string) ─────────────────────────────────────────
	fmt.Println("=== 1. Iota Enum — string wire encoding ===")

	// Encode: OrderStatus → JSON string
	data, _ := j.Marshal(Approved)
	fmt.Println("encode Approved:", string(data)) // "approved"

	// Decode: JSON string → OrderStatus
	status, _ := j.Unmarshal([]byte(`"shipped"`))
	fmt.Printf("decode \"shipped\": %v (int=%d)\n", orderStatusNames[status], int(status))

	// Validation: unknown label rejected with structured error
	_, err := j.Unmarshal([]byte(`"refunded"`))
	fmt.Println("unknown label error:", err)

	// Schema: {type:string, enum:[...]}
	schemaJSON, _ := json.MarshalIndent(OrderStatusCodec.Schema, "", "  ")
	fmt.Println("schema:", string(schemaJSON))
	fmt.Println()

	// ── Section 2: Iota enum (integer) ────────────────────────────────────────
	fmt.Println("=== 2. Iota Enum — integer wire encoding ===")

	jInt := format.JSON(OrderStatusIntCodec)
	data, _ = jInt.Marshal(Shipped)
	fmt.Println("encode Shipped:", string(data)) // 2

	statusInt, _ := jInt.Unmarshal([]byte(`1`))
	fmt.Printf("decode 1: %v\n", orderStatusNames[statusInt]) // "approved"

	_, err = jInt.Unmarshal([]byte(`99`))
	fmt.Println("out-of-range error:", err)

	intSchemaJSON, _ := json.MarshalIndent(OrderStatusIntCodec.Schema, "", "  ")
	fmt.Println("schema:", string(intSchemaJSON))
	fmt.Println()

	// ── Section 3: Open union via codex.Any() ─────────────────────────────────
	fmt.Println("=== 3. Union Type — open runtime union (codex.Any) ===")

	// Any passes values through unchanged — string, int, bool, map, all accepted.
	for _, v := range []any{"hello", 42, true, map[string]any{"x": 1}} {
		encoded, _ := DynamicValueCodec.Encode(v)
		decoded, _ := DynamicValueCodec.Decode(encoded)
		fmt.Printf("any roundtrip: %T(%v)\n", decoded, decoded)
	}

	anySchemaJSON, _ := json.MarshalIndent(DynamicValueCodec.Schema, "", "  ")
	fmt.Println("schema:", string(anySchemaJSON)) // {} — no type constraint
	fmt.Println()

	// ── Section 4: Sum type — TaggedUnion ─────────────────────────────────────
	fmt.Println("=== 4. Sum Type — TaggedUnion (sealed interface) ===")

	jStatus := format.JSON(PaymentStatusCodec)

	// Decode each variant — the "status" discriminator routes to the right codec.
	pending, _ := jStatus.Unmarshal([]byte(`{"status":"pending"}`))
	fmt.Printf("decode pending: %T\n", pending)

	completed, _ := jStatus.Unmarshal([]byte(`{"status":"completed","tx_id":"tx-abc123"}`))
	fmt.Printf("decode completed: %T, TxID=%s\n", completed, completed.(CompletedPayment).TxID)

	failed, _ := jStatus.Unmarshal([]byte(`{"status":"failed","reason":"insufficient funds"}`))
	fmt.Printf("decode failed: %T, Reason=%s\n", failed, failed.(FailedPayment).Reason)

	// Encode — discriminator field written automatically.
	outData, _ := jStatus.Marshal(CompletedPayment{TxID: "tx-xyz999"})
	fmt.Println("encode completed:", string(outData))

	// Validation: unknown discriminator value rejected.
	_, err = jStatus.Unmarshal([]byte(`{"status":"refunded"}`))
	var variantErr codex.UnknownVariantError
	if errors.As(err, &variantErr) {
		fmt.Printf("unknown variant error: tag=%q variant=%q\n", variantErr.Tag, variantErr.Variant)
	}

	// Validation: variant field constraint violated.
	_, err = jStatus.Unmarshal([]byte(`{"status":"completed","tx_id":""}`))
	fmt.Println("field constraint error:", err)

	// Schema: {oneOf:[...], discriminator:{propertyName:"status", mapping:{...}}}
	statusSchemaJSON, _ := json.MarshalIndent(PaymentStatusCodec.Schema, "", "  ")
	fmt.Println("schema:", string(statusSchemaJSON))
	fmt.Println()

	// ── Section 5: Sum type — Either2 ─────────────────────────────────────────
	fmt.Println("=== 5. Sum Type — Either2 (binary sum type) ===")

	jEither := format.JSON(SkuOrInlineCodec)

	// Left branch: plain SKU string
	ref1, _ := jEither.Unmarshal([]byte(`"WIDGET-001"`))
	fmt.Printf("left branch: Left=%q Right=%v\n", *ref1.Left, ref1.Right)

	// Right branch: inline ProductRef object (string branch fails first)
	ref2, _ := jEither.Unmarshal([]byte(`{"sku":"GADGET-007"}`))
	fmt.Printf("right branch: Left=%v Right=%+v\n", ref2.Left, *ref2.Right)

	// Encode left
	leftData, _ := jEither.Marshal(codex.Either[string, ProductRef]{Left: ptr("SKU-123")})
	fmt.Println("encode left:", string(leftData))

	// Encode right
	rightData, _ := jEither.Marshal(codex.Either[string, ProductRef]{Right: &ProductRef{SKU: "SKU-456"}})
	fmt.Println("encode right:", string(rightData))

	// Both fail: EitherError lists both branch errors.
	_, err = jEither.Unmarshal([]byte(`42`))
	var eitherErr codex.EitherError
	if errors.As(err, &eitherErr) {
		fmt.Printf("both fail: %d branch errors\n", len(eitherErr.Errors))
	}

	eitherSchemaJSON, _ := json.MarshalIndent(SkuOrInlineCodec.Schema, "", "  ")
	fmt.Println("schema:", string(eitherSchemaJSON))
	fmt.Println()

	// ── Section 5b: Bonus — StringOrInt64 ─────────────────────────────────────
	fmt.Println("=== 5b. Bonus — StringOrInt64 (string-or-number convenience) ===")

	jCountOrLabel := format.JSON(CountOrLabelCodec)

	// A JSON string decodes into the Left (string) branch.
	label, _ := jCountOrLabel.Unmarshal([]byte(`"unlimited"`))
	fmt.Printf("decode string: Left=%q Right=%v\n", *label.Left, label.Right)

	// A JSON number decodes into the Right (int64) branch.
	count, _ := jCountOrLabel.Unmarshal([]byte(`5`))
	fmt.Printf("decode number: Left=%v Right=%d\n", count.Left, *count.Right)

	countOrLabelSchemaJSON, _ := json.MarshalIndent(CountOrLabelCodec.Schema, "", "  ")
	fmt.Println("schema:", string(countOrLabelSchemaJSON))
	fmt.Println()

	// ── Decision guide ────────────────────────────────────────────────────────
	fmt.Println("=== Decision guide ===")
	fmt.Println()
	fmt.Println("Pattern            | Set    | Data? | Codec primitive")
	fmt.Println("-------------------|--------|-------|--------------------------------")
	fmt.Println("Iota enum (string) | Closed | No    | MapCodecSafe + validate.OneOf")
	fmt.Println("Iota enum (int)    | Closed | No    | MapCodecSafe + validate.RangeInt")
	fmt.Println("Open union         | Open   | Any   | codex.Any()")
	fmt.Println("Sum type (tagged)  | Closed | Yes   | codex.TaggedUnion[T]")
	fmt.Println("Sum type (untagged)| Closed | Yes   | codex.UntaggedUnion[T]")
	fmt.Println("Sum type (binary)  | Closed | Yes   | codex.Either2(ca, cb)")
	fmt.Println()
	fmt.Println("Rule of thumb:")
	fmt.Println("  - All variants are just labels, no payload → iota enum")
	fmt.Println("  - You don't control all types at compile time → codex.Any()")
	fmt.Println("  - Variants carry different data, wire has a discriminator field → TaggedUnion")
	fmt.Println("  - Variants carry different data, decode by shape alone → UntaggedUnion")
	fmt.Println("  - Exactly two types, one tried before the other → Either2")
	fmt.Println()
	fmt.Println("done.")

	_ = os.Stdout // suppress unused import in some editors
}

func ptr[T any](v T) *T { return &v }
