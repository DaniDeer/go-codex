package manifesttemplate

import (
	"fmt"
	"sort"
	"strconv"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

// ── Environment variables ─────────────────────────────────────────────────────

// EnvVarName is the environment variable's name (map key) — a named type for
// semantic clarity, WITHOUT a format constraint (see EnvVarNameCodec for why).
type EnvVarName string

type EnvVars map[EnvVarName]EnvVar

// EnvVarValue holds EXACTLY ONE of a string, an int64, or a float64 value —
// mirrors the JSON/YAML/TOML wire shape where a module's env var value may
// be a string OR a number, and the number itself may be whole or
// fractional. Pointer fields (nil = unset) are the discriminator: a
// zero-value int64/float64 (0) and an empty string ("") are all legitimate
// values, so nil-vs-non-nil is the only unambiguous signal.
type EnvVarValue struct {
	StringValue *string
	IntValue    *int64
	FloatValue  *float64
}

type EnvVar struct {
	Value EnvVarValue
}

// EnvVarNameCodec wraps a bare string as the named EnvVarName type with NO
// format constraint. validate.EnvVarName (POSIX "[A-Z_][A-Z0-9_]*") looks
// like the obvious fit, but real-world env var names commonly do NOT follow
// that convention (lowercase names like "https_proxy"/"no_proxy" and mixed
// case names are common in practice) — Docker itself places no format
// restriction on environment variable names. Applying validate.EnvVarName
// here would reject legitimate real-world data, so this codec stays a
// plain, unconstrained wrapper; add validate.EnvVarName back via
// .Refine(...) if your own deployment's env vars are known to always
// follow that convention.
var EnvVarNameCodec = c.MapCodecSafe(
	c.String(),
	func(s string) EnvVarName { return EnvVarName(s) },
	func(n EnvVarName) (string, error) { return string(n), nil },
)

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

// EnvVarValueCodec tries string, then int64, then float64, in that order —
// deliberate: encoding/json decodes EVERY JSON number into float64 ("5" and
// "5.0" are indistinguishable once parsed into `any`), and codex.Int64()'s
// Decode already rejects non-integral floats (ConstraintError), so trying
// int64 BEFORE float64 means a whole JSON number becomes IntValue and a
// fractional one becomes FloatValue — deterministic, no extra heuristic
// code needed beyond the existing Int64()/Float64() codecs' own behavior.
// (YAML/TOML don't need this dispatch trick — they preserve int-vs-float
// from the original wire syntax directly.)
//
// codex.Either2/StringOrInt64 are exactly 2-branch and cannot express a
// 3-way choice; codex.UntaggedUnion is the general N-way structural union
// primitive (its `variants ...UntaggedVariant[T]` is variadic), so it is
// the right tool for a string-or-int-or-float value.
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

// Codec implements [codex.HasCodec][EnvVarValue], returning
// [EnvVarValueCodec].
func (EnvVarValue) Codec() c.Codec[EnvVarValue] { return EnvVarValueCodec }

// NewEnvVarValueString constructs an EnvVarValue holding a string. This is
// the named-constructor form of EnvVarValue{StringValue: &s} — it avoids
// the caller having to take the address of a local variable by hand, and
// is INFALLIBLE: no branch of EnvVarValueCodec's union has a Refine
// constraint today, so there is nothing that could reject s.
func NewEnvVarValueString(s string) EnvVarValue { return EnvVarValue{StringValue: &s} }

// NewEnvVarValueInt constructs an EnvVarValue holding an int64. See
// [NewEnvVarValueString] for the general rationale (named constructor,
// infallible).
func NewEnvVarValueInt(i int64) EnvVarValue { return EnvVarValue{IntValue: &i} }

// NewEnvVarValueFloat constructs an EnvVarValue holding a float64. See
// [NewEnvVarValueString] for the general rationale (named constructor,
// infallible).
func NewEnvVarValueFloat(f float64) EnvVarValue { return EnvVarValue{FloatValue: &f} }

var EnvVarCodec = c.Struct[EnvVar](
	c.RequiredField("value", EnvVarValueCodec,
		func(e EnvVar) EnvVarValue { return e.Value },
		func(e *EnvVar, v EnvVarValue) { e.Value = v },
	),
)

// EnvVarsCodec uses codex.Map (not StringMap) since the key is the named
// EnvVarName type, not a bare string.
var EnvVarsCodec = c.Map[EnvVarName, EnvVar](EnvVarNameCodec, EnvVarCodec)

// FlattenEnvVars converts an IoT Edge module's typed string/int64/float64
// EnvVars map into Docker's flat "KEY=VALUE" docker.Env form — the
// practical use case: preparing to actually `docker create`/`run` a
// container using env vars sourced from an IoT Edge manifest. Numeric
// values are formatted via strconv (int64 as a plain decimal, float64 via
// strconv.FormatFloat's 'g' verb, Go's own shortest-round-trip
// representation). Entries are sorted by name for deterministic output —
// EnvVars is a Go map, which has no stable iteration order on its own.
//
// This mapper is intentionally ONE DIRECTION ONLY (iotedge -> docker).
// There is no reverse mapper (docker.Env -> EnvVars): going backward would
// require GUESSING whether a flat "KEY=VALUE" string's value was
// originally a string, an int64, or a float64 — that guess is lossy and
// unreliable, so it is out of scope here.
func FlattenEnvVars(vars EnvVars) docker.Env {
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, string(name))
	}
	sort.Strings(names)

	out := make(docker.Env, 0, len(vars))
	for _, name := range names {
		v := vars[EnvVarName(name)].Value
		out = append(out, docker.EnvVar{Name: name, Value: formatEnvVarValue(v)})
	}
	return out
}

// formatEnvVarValue formats one EnvVarValue as a plain string — exactly
// one of StringValue/IntValue/FloatValue is non-nil (see EnvVarValue's own
// doc comment); an entirely-empty EnvVarValue (a decode/construction bug,
// not a valid wire value) formats as "".
func formatEnvVarValue(v EnvVarValue) string {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return strconv.FormatInt(*v.IntValue, 10)
	case v.FloatValue != nil:
		return strconv.FormatFloat(*v.FloatValue, 'g', -1, 64)
	default:
		return ""
	}
}
