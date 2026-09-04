// Package gob-contract demonstrates the "Go library as contract" pattern.
//
// Instead of an OpenAPI or AsyncAPI document as the cross-service contract,
// a shared Go module (the contract/ subpackage in this example) defines:
//
//   - The domain type (Order) with exported fields (required by encoding/gob)
//   - The codec (OrderCodec) — shape, constraints, and schema in one value
//   - The wire format (GobFormat = format.Gob(OrderCodec)) — binary, Go-native
//   - The channel definition (OrderChannel) — topic template and operations
//
// Both services import this package. The Go compiler enforces the contract:
// any field rename, type change, or constraint modification breaks compilation
// on both sides immediately — no stale YAML, no schema drift, no code-generation.
//
// # When to use this pattern
//
// Use the "Go library as contract" pattern when:
//   - All services communicating over this channel are written in Go
//   - You want binary-efficient wire encoding without a schema compiler (protobuf, Avro)
//   - Compile-time contract enforcement matters more than cross-language interoperability
//
// For external-facing APIs (consumed by non-Go clients or documented via tooling),
// use JSON/YAML formats and generate OpenAPI or AsyncAPI specs from the same codec.
//
// # What about OpenAPI/AsyncAPI with Gob?
//
// You can add format.Gob to a route or channel and the spec renderer will emit
// "application/gob" as the content type alongside the JSON Schema body. The schema
// documents the logical data shape — useful for humans — but tooling (Swagger UI,
// API gateways, code generators) cannot interpret or validate binary gob payloads.
// Keep "application/gob" out of external-facing specs; use it only for internal
// Go-to-Go channels where the Go library is the authoritative contract.
//
// Run with: go run ./examples/gob-contract
package main

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/gob-contract/contract"
)

func main() {
	// ── 1. Producer side: encode Order to gob bytes ───────────────────────────
	fmt.Println("=== Producer: marshal Order to gob bytes ===")

	order := contract.Order{
		ID:       "f47ac10b-58cc-4372-a567-0e02b2c3d479",
		Product:  "Wireless Keyboard",
		Quantity: 2,
		Price:    49.99,
	}

	data, err := contract.GobFormat.Marshal(order)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal error:", err)
		os.Exit(1)
	}
	fmt.Printf("encoded %d bytes (binary — not human-readable)\n", len(data))
	fmt.Println()

	// ── 2. Consumer side: decode gob bytes back to Order ─────────────────────
	fmt.Println("=== Consumer: unmarshal gob bytes to Order ===")

	// Both services use the same contract.GobFormat — same codec, same constraints.
	received, err := contract.GobFormat.Unmarshal(data)
	if err != nil {
		fmt.Fprintln(os.Stderr, "unmarshal error:", err)
		os.Exit(1)
	}
	fmt.Printf("received: %+v\n", received)
	fmt.Println()

	// ── 3. Constraint enforcement on the producer side ────────────────────────
	fmt.Println("=== Constraint enforcement (producer rejects invalid Order) ===")

	badOrders := []struct {
		label string
		order contract.Order
	}{
		{"negative price", contract.Order{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "Widget", Quantity: 1, Price: -5.0}},
		{"zero quantity", contract.Order{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "Widget", Quantity: 0, Price: 9.99}},
		{"invalid UUID", contract.Order{ID: "not-a-uuid", Product: "Widget", Quantity: 1, Price: 9.99}},
		{"empty product", contract.Order{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "", Quantity: 1, Price: 9.99}},
	}

	for _, bc := range badOrders {
		_, marshalErr := contract.GobFormat.Marshal(bc.order)
		var ve codex.ValidationErrors
		if errors.As(marshalErr, &ve) {
			fmt.Printf("  %-16s → %v\n", bc.label+":", marshalErr)
		}
	}
	fmt.Println()

	// ── 4. Constraint enforcement on the consumer side ────────────────────────
	fmt.Println("=== Constraint enforcement (consumer validates on unmarshal) ===")

	// Simulate a misbehaving producer: encode an invalid Order directly via
	// encoding/gob (bypassing contract.GobFormat's codec validation).
	// The consumer's Unmarshal still validates via the codec after decoding.
	var buf bytes.Buffer
	if encErr := gob.NewEncoder(&buf).Encode(contract.Order{
		ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Product: "Widget", Quantity: -1, Price: 0.0,
	}); encErr != nil {
		fmt.Fprintln(os.Stderr, "raw gob encode error:", encErr)
		os.Exit(1)
	}
	_, err = contract.GobFormat.Unmarshal(buf.Bytes())
	fmt.Printf("  consumer rejected tampered payload: %v\n", err)
	fmt.Println()

	// ── 5. Channel descriptor and AsyncAPI spec ───────────────────────────────
	fmt.Println("=== AsyncAPI spec (human documentation; Go library is the authoritative contract) ===")
	fmt.Println()

	// Register the channel in a builder to produce an AsyncAPI spec.
	// The spec documents the data shape and topic address for human readers,
	// but the Go library (contract package) remains the enforcement mechanism.
	b := events.NewClient(events.WithInfo(events.Info{
		Title:       "Order Service",
		Version:     "1.0.0",
		Description: "Internal order events — Go-to-Go binary channel (gob).",
	}))
	orderHandle, err := contract.OrderChannel.Handle(b)
	if err != nil {
		fmt.Fprintln(os.Stderr, "register error:", err)
		os.Exit(1)
	}
	fmt.Printf("topic template: %s\n\n", orderHandle.Topic)

	doc, err := b.AsyncAPISpec()
	if err != nil {
		fmt.Fprintln(os.Stderr, "AsyncAPISpec error:", err)
		os.Exit(1)
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintln(os.Stderr, "MarshalYAML error:", err)
		os.Exit(1)
	}
	fmt.Print(string(yamlBytes))
}
