package manifesttemplate

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file is the SINGLE SOURCE OF TRUTH for the deployment manifest's
// WIRE-KEY vocabulary: the top-level wrapper key names
// ([ModulesContentKey]/[EdgeAgentKey]/[EdgeHubKey]), the dotted-key
// PREFIXES module/route entries live under ([ModuleKeyPrefix]/
// [RouteKeyPrefix]), and the two-layer codecs that validate a FULL
// dotted key and extract/wrap its name segment ([ModuleNameCodec]/
// [RouteNameCodec]). Every OTHER package that hand-rolls a codec
// touching these same wire buckets — models/iotedge/modulepatch,
// models/iotedge/deviceconfig, models/iotedge/finaldeviceconfig — reuses
// these exact constants/codecs instead of re-hardcoding the same
// literal strings, so a caller answering "what does this wire key
// actually look like" or "how do I validate one" reads ONE file rather
// than grepping four packages.
//
// The real, on-the-wire shape this file's constants describe:
//
//	{
//	  "modulesContent": {
//	    "$edgeAgent": {"properties.desired.modules.<name>": {...ModuleConfig...}, ...},
//	    "$edgeHub":   {"properties.desired.routes.<name>":  "FROM ... INTO ...", ...}
//	  }
//	}
//
// NOT included here: ordinary codex.Struct/PartialField FIELD NAMES
// ("settings"/"env"/"type"/"status"/"restartPolicy"/"version"/"image"/
// "createOptions") — these follow the STANDARD codex.Struct/PartialField
// idiom used identically throughout the whole go-codex ecosystem (every
// full/partial type pair repeats field names this way); consolidating
// those into constants would be a much larger, unrelated refactor
// touching a pattern that has never needed it elsewhere. This file
// scopes strictly to the DOTTED-KEY EXTRACTION vocabulary — the part
// that had actually drifted into ad-hoc, un-sourced duplication.

// ── Top-level wire-key names ──────────────────────────────────────────────────

// ModulesContentKey is the deployment manifest's top-level wrapper key,
// e.g. {"modulesContent": {...}}.
const ModulesContentKey = "modulesContent"

// EdgeAgentKey is the flat object holding every module entry, keyed by
// ModuleKeyPrefix-prefixed dotted keys, e.g. {"$edgeAgent": {...}}.
const EdgeAgentKey = "$edgeAgent"

// EdgeHubKey is the flat object holding every route entry, keyed by
// RouteKeyPrefix-prefixed dotted keys, e.g. {"$edgeHub": {...}}.
const EdgeHubKey = "$edgeHub"

// ── ModuleName dotted-key extraction ──────────────────────────────────────────
//
// Wire: {"properties.desired.modules.factory-mqtt-gateway": {...ModuleConfig...}, ...}
// Go:   map[ModuleName]ModuleConfig{"factory-mqtt-gateway": {...}, ...}
//
// Same two-layer key-validation pattern as examples/flat-key-patch's
// containerKeyCodec (wire-level full-key constraint + domain-level name
// constraint via MapCodecValidated) — but the target here is a NAMED
// ModuleName type (not bare string), so it composes with codex.Map
// (→ map[ModuleName]ModuleConfig) instead of flat-key-patch's
// codex.EntrySlice (→ a merged []Container slice). Use Map when you want
// the result as a Go map keyed by the extracted value; use EntrySlice when
// you want the extracted key merged into a flat slice of combined structs.

// ModuleKeyPrefix is the fixed namespace for all module keys, e.g.
// "properties.desired.modules.factory-mqtt-gateway". Exported so a caller
// composing their own dotted-key codec (e.g. a patch targeting one module
// by name) can reuse the exact same prefix instead of duplicating it.
const ModuleKeyPrefix = "properties.desired.modules."

// moduleKeyConstraint validates the FULL wire key: must start with
// ModuleKeyPrefix and have a non-empty module-name segment after it.
var moduleKeyConstraint = c.Constraint[string]{
	Name: "module-key",
	Check: func(s string) bool {
		return strings.HasPrefix(s, ModuleKeyPrefix) && len(strings.TrimPrefix(s, ModuleKeyPrefix)) > 0
	},
	Message: func(s string) string {
		return fmt.Sprintf("key %q must start with %q followed by a module name", s, ModuleKeyPrefix)
	},
}

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

// ── RouteName dotted-key extraction ────────────────────────────────────────────
//
// Wire: {"properties.desired.routes.factory-mqtt-to-ingest": "FROM ... INTO ...", ...}
// Go:   map[RouteName]Route{"factory-mqtt-to-ingest": {...}, ...}
//
// Mirrors ModuleName's own dotted-key extraction exactly, above — same
// two-layer wire-key + name-constraint validation via MapCodecValidated,
// just under a different prefix and value shape.

// RouteKeyPrefix is the fixed namespace for all route keys, e.g.
// "properties.desired.routes.factory-mqtt-to-ingest". Exported so a
// caller composing their own dotted-key codec (e.g. a device-config
// patch targeting one route by name) can reuse the exact same prefix
// instead of duplicating it.
const RouteKeyPrefix = "properties.desired.routes."

// routeKeyConstraint validates the FULL wire key: must start with
// RouteKeyPrefix and have a non-empty route-name segment after it.
var routeKeyConstraint = c.Constraint[string]{
	Name: "route-key",
	Check: func(s string) bool {
		return strings.HasPrefix(s, RouteKeyPrefix) && len(strings.TrimPrefix(s, RouteKeyPrefix)) > 0
	},
	Message: func(s string) string {
		return fmt.Sprintf("key %q must start with %q followed by a route name", s, RouteKeyPrefix)
	},
}

// routeNameCodec validates the extracted name segment — reusing
// validate.Slug (lowercase alphanumeric + hyphens), the same constraint
// moduleNameCodec applies — and wraps the validated string as the named
// RouteName type.
var routeNameCodec = c.MapCodecSafe(
	c.String().Refine(v.Slug),
	func(s string) RouteName { return RouteName(s) },
	func(n RouteName) (string, error) { return string(n), nil },
)

// RouteNameCodec: two-layer validation via MapCodecValidated — the wire
// codec validates the FULL dotted key; routeNameCodec validates+wraps the
// extracted name segment. Mirrors ModuleNameCodec exactly.
var RouteNameCodec = c.MapCodecValidated(
	c.String().Refine(routeKeyConstraint),
	routeNameCodec,
	func(fullKey string) (RouteName, error) {
		return RouteName(strings.TrimPrefix(fullKey, RouteKeyPrefix)), nil
	},
	func(n RouteName) (string, error) {
		return RouteKeyPrefix + string(n), nil
	},
)
