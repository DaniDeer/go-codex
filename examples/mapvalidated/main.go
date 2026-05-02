package main

import (
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// Celsius is a validated domain type representing a temperature.
// Valid range: -273.15 (absolute zero) to 1_000_000.
type Celsius float64

// ── Codecs ────────────────────────────────────────────────────────────────────

// celsiusBaseCodec validates the float64 range before we map to Celsius.
var celsiusBaseCodec = codex.MapCodecSafe(
	codex.Float64().
		Refine(validate.MinFloat(-273.15)).
		Refine(validate.MaxFloat(1_000_000)),
	func(f float64) Celsius { return Celsius(f) },
	func(c Celsius) (float64, error) { return float64(c), nil },
)

// celsiusCodec demonstrates MapCodecValidated:
//   - ca: Float64 (wire representation)
//   - cb: celsiusBaseCodec (domain type with range constraints)
//   - to: fallible — rejects NaN and Inf in addition to out-of-range values
//   - from: fallible — always succeeds for valid Celsius values
//
// After mapping to Celsius, cb.Validate enforces the range constraints.
var celsiusCodec = codex.MapCodecValidated(
	codex.Float64(),
	celsiusBaseCodec,
	func(f float64) (Celsius, error) {
		if f != f { // NaN check
			return 0, errors.New("temperature must be a number, got NaN")
		}
		return Celsius(f), nil
	},
	func(c Celsius) (float64, error) { return float64(c), nil },
)

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	// Decode a valid temperature.
	temp, err := celsiusCodec.Decode(float64(36.6))
	if err != nil {
		fmt.Println("decode error:", err)
	} else {
		fmt.Printf("decoded temperature: %.1f°C\n", temp)
	}

	// Encode a valid Celsius value back to a float64.
	encoded, err := celsiusCodec.Encode(Celsius(100))
	if err != nil {
		fmt.Println("encode error:", err)
	} else {
		fmt.Printf("encoded temperature: %v\n", encoded)
	}

	// Decode fails: below absolute zero — caught by cb.Validate (range constraint).
	_, err = celsiusCodec.Decode(float64(-300))
	fmt.Println("below absolute zero:", err)

	// Encode fails: invalid Celsius value violates cb.Validate.
	_, err = celsiusCodec.Encode(Celsius(2_000_000))
	fmt.Println("too hot to encode:  ", err)

	// Decode fails: to function rejects the wire value (NaN).
	nan := func() float64 {
		var zero float64
		return zero / zero
	}()
	_, err = celsiusCodec.Decode(nan)
	fmt.Println("NaN rejected by to: ", err)
}
