package iotedge

import (
	"encoding/json"
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker"
	f "github.com/DaniDeer/go-codex/format"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Module lifecycle metadata ─────────────────────────────────────────────────
//
// TypeCodec, StatusCodec, RestartPolicyCodec, and VersionCodec wrap a plain
// validated STRING as their respective named types — matching the real wire
// shape, where "type"/"status"/"restartPolicy"/"version" are bare JSON
// strings (e.g. "type": "docker"), NOT single-key objects
// ({"type": "docker"}).

var TypeCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("docker")),
	func(s string) Type { return Type(s) },
	func(t Type) (string, error) { return string(t), nil },
)

var StatusCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("running", "stopped")),
	func(s string) Status { return Status(s) },
	func(st Status) (string, error) { return string(st), nil },
)

var RestartPolicyCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("always", "on-failure", "on-unhealthy", "never")),
	func(s string) RestartPolicy { return RestartPolicy(s) },
	func(rp RestartPolicy) (string, error) { return string(rp), nil },
)

var VersionCodec = c.MapCodecSafe(
	c.String().Refine(v.NonEmptyString),
	func(s string) Version { return Version(s) },
	func(ver Version) (string, error) { return string(ver), nil },
)

var StartupOrderCodec = c.MapCodecSafe(
	c.Int64().Refine(v.PositiveInt64),
	func(i int64) StartupOrder { return StartupOrder(i) },
	func(so StartupOrder) (int64, error) { return int64(so), nil },
)

var ModuleConfigCodec = c.Struct[ModuleConfig](
	c.RequiredField("settings",
		ModuleSettingsCodec,
		func(mc ModuleConfig) ModuleSettings { return mc.Settings },
		func(mc *ModuleConfig, v ModuleSettings) { mc.Settings = v },
	),
	// OptionalField: not every module declares an "env" section — absent
	// key decodes to Env's zero value (nil map), no error.
	c.OptionalField("env",
		EnvVarsCodec,
		func(mc ModuleConfig) map[EnvVarName]EnvVar { return mc.Env },
		func(mc *ModuleConfig, v map[EnvVarName]EnvVar) { mc.Env = EnvVars(v) },
	),
	c.RequiredField("type",
		TypeCodec,
		func(mc ModuleConfig) Type { return mc.Type },
		func(mc *ModuleConfig, v Type) { mc.Type = v },
	),
	c.RequiredField("status",
		StatusCodec,
		func(mc ModuleConfig) Status { return mc.Status },
		func(mc *ModuleConfig, v Status) { mc.Status = v },
	),
	c.RequiredField("restartPolicy",
		RestartPolicyCodec,
		func(mc ModuleConfig) RestartPolicy { return mc.RestartPolicy },
		func(mc *ModuleConfig, v RestartPolicy) { mc.RestartPolicy = v },
	),
	c.RequiredField("version",
		VersionCodec,
		func(mc ModuleConfig) Version { return mc.Version },
		func(mc *ModuleConfig, v Version) { mc.Version = v },
	),
)

// ── ModuleSettings ────────────────────────────────────────────────────────────

// ImageCodec validates a module's image reference — named and exported so a
// caller assembling their own image-only codec (e.g. a "patch this
// module's image" wire codec keyed by ModuleName) can reuse the exact same
// validated codec instead of re-deriving the constraint.
var ImageCodec = c.String().Refine(v.NonEmptyString, v.ContainerImage)

// CreateOptionsFieldCodec decodes/encodes createOptions with the SAME
// JSON-in-string behavior as format.EmbeddedJSON(docker.CreateOptionsCodec),
// PLUS one tolerance format.EmbeddedJSON doesn't have: an empty string is
// accepted as equivalent to "{}" (the zero-value docker.CreateOptions{}) —
// some deployment manifests ship createOptions:"" instead of omitting the
// field or writing "{}". json.Unmarshal([]byte(""), ...) is not valid JSON
// and would otherwise fail with format.EmbeddedDecodeError for every such
// module. Symmetric on encode: a zero-value docker.CreateOptions re-encodes
// back to "" (not "{}"), so round-tripping this exact document produces the
// same wire shape it started with, not a cosmetically different (but
// equivalent) "{}" string.
var CreateOptionsFieldCodec = c.MapCodecValidated(
	c.String(), docker.CreateOptionsCodec,
	func(s string) (docker.CreateOptions, error) {
		if s == "" {
			return docker.CreateOptions{}, nil
		}
		var raw any
		if err := json.Unmarshal([]byte(s), &raw); err != nil {
			return docker.CreateOptions{}, f.EmbeddedDecodeError{Format: "json", Err: err}
		}
		return docker.CreateOptionsCodec.Decode(raw)
	},
	func(co docker.CreateOptions) (string, error) {
		if docker.IsZeroCreateOptions(co) {
			return "", nil
		}
		intermediate, err := docker.CreateOptionsCodec.Encode(co)
		if err != nil {
			return "", err
		}
		b, err := json.Marshal(intermediate)
		if err != nil {
			return "", f.EmbeddedEncodeError{Format: "json", Err: err}
		}
		return string(b), nil
	},
)

var ModuleSettingsCodec = c.Struct[ModuleSettings](
	c.RequiredField("image",
		ImageCodec,
		func(ms ModuleSettings) string { return ms.Image },
		func(ms *ModuleSettings, v string) { ms.Image = v },
	),
	c.OptionalField("createOptions",
		CreateOptionsFieldCodec,
		func(ms ModuleSettings) docker.CreateOptions { return ms.CreateOptions },
		func(ms *ModuleSettings, val docker.CreateOptions) { ms.CreateOptions = val },
	),
)

// ── Environment variables ─────────────────────────────────────────────────────

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

var EnvVarCodec = c.Struct[EnvVar](
	c.RequiredField("value", EnvVarValueCodec,
		func(e EnvVar) EnvVarValue { return e.Value },
		func(e *EnvVar, v EnvVarValue) { e.Value = v },
	),
)

// EnvVarsCodec uses codex.Map (not StringMap) since the key is the named
// EnvVarName type, not a bare string.
var EnvVarsCodec = c.Map[EnvVarName, EnvVar](EnvVarNameCodec, EnvVarCodec)

// ── ModuleName / Modules — codex.Map key extraction ───────────────────────────
//
// Same two-layer key-validation pattern as examples/flat-key-patch's
// containerKeyCodec (wire-level full-key constraint + domain-level name
// constraint via MapCodecValidated) — but the target here is a NAMED
// ModuleName type (not bare string), so it composes with codex.Map
// (→ map[ModuleName]ModuleConfig) instead of flat-key-patch's
// codex.EntrySlice (→ a merged []Container slice). Use Map when you want
// the result as a Go map keyed by the extracted value; use EntrySlice when
// you want the extracted key merged into a flat slice of combined structs.

// moduleNameCodec validates the extracted name segment — reusing
// validate.Slug (lowercase alphanumeric + hyphens) rather than a one-off
// constraint — and wraps the validated string as the named ModuleName type.
var moduleNameCodec = c.MapCodecSafe(
	c.String().Refine(v.Slug),
	func(s string) ModuleName { return ModuleName(s) },
	func(n ModuleName) (string, error) { return string(n), nil },
)

// ModuleNameCodec: two-layer validation via MapCodecValidated — the wire
// codec validates the FULL dotted key; moduleNameCodec validates+wraps the
// extracted name segment.
//
// Decode: full key → strip prefix → moduleNameCodec.Validate (via Slug) → ModuleName.
// Encode: ModuleName → prepend prefix → validate full key → full key string.
var ModuleNameCodec = c.MapCodecValidated(
	c.String().Refine(moduleKeyConstraint),
	moduleNameCodec,
	func(fullKey string) (ModuleName, error) {
		return ModuleName(strings.TrimPrefix(fullKey, ModuleKeyPrefix)), nil
	},
	func(n ModuleName) (string, error) {
		return ModuleKeyPrefix + string(n), nil
	},
)

// ModulesCodec decodes/encodes the flat "$edgeAgent" object directly into
// map[ModuleName]ModuleConfig via codex.Map — K=ModuleName (extracted from
// the dotted key), V=ModuleConfig.
var ModulesCodec = c.Map[ModuleName, ModuleConfig](ModuleNameCodec, ModuleConfigCodec)

// ── ModulesContent / DeploymentManifest ───────────────────────────────────────
//
// Wire:
//
//	{
//	  "modulesContent": {
//	    "$edgeAgent": {
//	      "properties.desired.modules.cv-writer-kvrocks": {...}, ...
//	    }
//	  }
//	}
//
// "$edgeAgent" contains ONLY module entries (every key matches
// ModuleKeyPrefix) — no schemaVersion or other keys mixed in at that level
// — so it maps directly to Modules with no further nesting.

var ModulesContentCodec = c.Struct[ModulesContent](
	c.RequiredField("$edgeAgent", ModulesCodec,
		// ModulesCodec's Go type is map[ModuleName]ModuleConfig (from
		// codex.Map), not the named Modules type — same getter/setter
		// type-boundary conversion pattern used for ModuleConfig.Env
		// (Go generic type inference needs exact type identity, not mere
		// assignability, even though Modules IS literally
		// map[ModuleName]ModuleConfig).
		func(mc ModulesContent) map[ModuleName]ModuleConfig { return mc.EdgeAgent },
		func(mc *ModulesContent, val map[ModuleName]ModuleConfig) { mc.EdgeAgent = Modules(val) },
	),
)

var DeploymentManifestCodec = c.Struct[DeploymentManifest](
	c.RequiredField("modulesContent", ModulesContentCodec,
		func(dm DeploymentManifest) ModulesContent { return dm.ModulesContent },
		func(dm *DeploymentManifest, val ModulesContent) { dm.ModulesContent = val },
	),
)
