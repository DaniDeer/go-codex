package main

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
)

// ── Domain Subtypes ───────────────────────────────────────────────────────────

type EnvVars map[string]EnvVar

// EnvVarValue holds EXACTLY ONE of a string, an int64, or a float64 value —
// mirrors the JSON/YAML/TOML wire shape where a module's env var value may
// be a string OR a number, and the number itself may be whole or
// fractional. Pointer fields (nil = unset) are the discriminator: a
// zero-value int64/float64 (0) and an empty string ("") are all legitimate
// values, so nil-vs-non-nil is the only unambiguous signal.
//
// codex.Either2/StringOrInt64 are exactly 2-branch (Either[A,B] has exactly
// Left/Right) and cannot express a 3-way choice. codex.UntaggedUnion is
// already the general N-way structural union primitive — its
// `variants ...UntaggedVariant[T]` is variadic — so it is the right tool
// here.
//
// JSON int-vs-float note: encoding/json decodes EVERY JSON number into
// float64 — "5" and "5.0" are indistinguishable once parsed into `any`.
// codex.Int64()'s Decode already rejects non-integral floats
// (ConstraintError), so trying the int64 branch BEFORE the float64 branch
// below means: a whole JSON number becomes IntValue, a fractional one
// becomes FloatValue — deterministic, and no new heuristic code needed
// beyond the existing Int64()/Float64() codecs' own behavior. YAML/TOML
// don't need this dispatch trick at all — they preserve int-vs-float from
// the original wire syntax directly (yaml.v3 decodes an integer as a native
// Go int, BurntSushi/toml as a native int64; both decode a float as
// float64).
type EnvVarValue struct {
	StringValue *string
	IntValue    *int64
	FloatValue  *float64
}

// ── Codecs ────────────────────────────────────────────────────────────────────

// stringVariantCodec / intVariantCodec / floatVariantCodec are
// EnvVarValueCodec's three UntaggedUnion branches, each wrapping the
// underlying primitive codec via MapCodecSafe: `to` (A→B) is infallible
// (always succeeds — the direction MapCodecSafe requires to be
// error-free); `from` (B→A) fails when a DIFFERENT field is set, which is
// what drives UntaggedUnion's per-branch Encode dispatch via which().
var stringVariantCodec = c.MapCodecSafe(
	c.String(),
	func(s string) EnvVarValue { return EnvVarValue{StringValue: &s} },
	func(ev EnvVarValue) (string, error) {
		if ev.StringValue == nil {
			return "", fmt.Errorf("not a string EnvVarValue")
		}
		return *ev.StringValue, nil
	},
)

var intVariantCodec = c.MapCodecSafe(
	c.Int64(),
	func(i int64) EnvVarValue { return EnvVarValue{IntValue: &i} },
	func(ev EnvVarValue) (int64, error) {
		if ev.IntValue == nil {
			return 0, fmt.Errorf("not an int EnvVarValue")
		}
		return *ev.IntValue, nil
	},
)

var floatVariantCodec = c.MapCodecSafe(
	c.Float64(),
	func(f float64) EnvVarValue { return EnvVarValue{FloatValue: &f} },
	func(ev EnvVarValue) (float64, error) {
		if ev.FloatValue == nil {
			return 0, fmt.Errorf("not a float EnvVarValue")
		}
		return *ev.FloatValue, nil
	},
)

// EnvVarValueCodec tries string, then int64, then float64 (in that order —
// see the int-vs-float dispatch note on EnvVarValue above). Anything else
// (bool, object, array, null) fails all three branches, returning
// codex.EitherError listing every underlying failure.
//
// Schema: {oneOf: [stringVariantCodec.Schema, intVariantCodec.Schema,
// floatVariantCodec.Schema]} — inherited unchanged from
// codex.String()/Int64()/Float64() via MapCodecSafe.
var EnvVarValueCodec = c.UntaggedUnion[EnvVarValue](
	func(ev EnvVarValue) int {
		switch {
		case ev.StringValue != nil:
			return 0
		case ev.IntValue != nil:
			return 1
		default:
			return 2
		}
	},
	c.UntaggedVariant[EnvVarValue]{Name: "string", Codec: stringVariantCodec},
	c.UntaggedVariant[EnvVarValue]{Name: "int", Codec: intVariantCodec},
	c.UntaggedVariant[EnvVarValue]{Name: "float", Codec: floatVariantCodec},
)

type EnvVar struct {
	Value EnvVarValue
}

var EnvVarCodec = c.Struct[EnvVar](
	c.RequiredField("value", EnvVarValueCodec,
		func(e EnvVar) EnvVarValue { return e.Value },
		func(e *EnvVar, v EnvVarValue) { e.Value = v },
	),
)

var EnvVarsCodec = c.StringMap[EnvVar](EnvVarCodec)
