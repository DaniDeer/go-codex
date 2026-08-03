package codex

// StringOrInt, StringOrInt32, StringOrInt64, StringOrUint, StringOrUint64,
// StringOrFloat32, and StringOrFloat64 are named convenience constructors for
// a genuinely common wire pattern: a config/env-style value that may be
// EITHER a JSON/YAML/TOML string OR a number (e.g. Docker/IoT-Edge module env
// vars, Kubernetes' apimachinery IntOrString, Terraform/HCL, Helm
// values.yaml). Each is a one-line wrapper over [Either2]:
//
//	func StringOrInt64() Codec[Either[string, int64]] { return Either2(String(), Int64()) }
//
// This is pure sugar — [Either2] already fully implements this today with
// zero new code (`codex.Either2(codex.String(), codex.Int64())`); these
// constructors exist purely for discoverability/naming (a custom Constraint
// cannot express this: [Constraint][T] validates a single fixed type T after
// Decode, and there is no T for which Check could see the "other" type's raw
// value — the type-level choice has to happen in the codec itself, which is
// exactly what Either2/UntaggedUnion provide).
//
// Format-agnostic by construction: encoding/json always decodes a bare JSON
// number into `any` as float64; yaml.v3 decodes an integer as a native Go
// int and a float as float64; BurntSushi/toml decodes an integer as int64
// and a float as float64. Every StringOrXxx constructor's numeric branch
// (Int/Int32/Int64/Uint/Uint64/Float32/Float64) already type-switches over
// ALL of these native representations in its own Decode — so the SAME
// StringOrXxx codec works correctly whether the surrounding document is
// JSON, YAML, or TOML, without any format-specific handling.
//
// Decode tries the string branch first, then the numeric branch (matches
// [Either2]'s documented "try ca first" order): a JSON/YAML/TOML string
// decodes into Either.Left, a number into Either.Right. Encode dispatches on
// whichever field is non-nil. Schema is inherited unchanged from Either2:
// {oneOf: [{type:"string"}, {type:"integer"|"number", ...}]}.
func StringOrInt() Codec[Either[string, int]] { return Either2(String(), Int()) }

// StringOrInt32 is the int32 variant of [StringOrInt] — see its docs for the
// full rationale.
func StringOrInt32() Codec[Either[string, int32]] { return Either2(String(), Int32()) }

// StringOrInt64 is the int64 variant of [StringOrInt] — see its docs for the
// full rationale.
func StringOrInt64() Codec[Either[string, int64]] { return Either2(String(), Int64()) }

// StringOrUint is the uint variant of [StringOrInt] — see its docs for the
// full rationale.
func StringOrUint() Codec[Either[string, uint]] { return Either2(String(), Uint()) }

// StringOrUint64 is the uint64 variant of [StringOrInt] — see its docs for
// the full rationale.
func StringOrUint64() Codec[Either[string, uint64]] { return Either2(String(), Uint64()) }

// StringOrFloat32 is the float32 variant of [StringOrInt] — see its docs for
// the full rationale.
func StringOrFloat32() Codec[Either[string, float32]] { return Either2(String(), Float32()) }

// StringOrFloat64 is the float64 variant of [StringOrInt] — see its docs for
// the full rationale.
func StringOrFloat64() Codec[Either[string, float64]] { return Either2(String(), Float64()) }
