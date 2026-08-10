package iotedge

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── ModuleName / Modules — codex.Map key extraction ───────────────────────────
//
// Wire: {"properties.desired.modules.cv-writer-kvrocks": {...ModuleConfig...}, ...}
// Go:   map[ModuleName]ModuleConfig{"cv-writer-kvrocks": {...}, ...}
//
// Same two-layer key-validation pattern as examples/flat-key-patch's
// containerKeyCodec (wire-level full-key constraint + domain-level name
// constraint via MapCodecValidated) — but the target here is a NAMED
// ModuleName type (not bare string), so it composes with codex.Map
// (→ map[ModuleName]ModuleConfig) instead of flat-key-patch's
// codex.EntrySlice (→ a merged []Container slice). Use Map when you want
// the result as a Go map keyed by the extracted value; use EntrySlice when
// you want the extracted key merged into a flat slice of combined structs.

// ModuleName is the module/container name extracted from a dotted module
// key, e.g. "cv-writer-kvrocks" from "properties.desired.modules.cv-writer-kvrocks".
type ModuleName string

// Modules maps each module's extracted name to its full configuration.
type Modules map[ModuleName]ModuleConfig

// ModulesContent models the "modulesContent" wrapper (Azure IoT Edge /
// IoT-Edge deployment manifest naming convention).
type ModulesContent struct {
	EdgeAgent Modules
}

// DeploymentManifest is the top-level deployment manifest document.
type DeploymentManifest struct {
	ModulesContent ModulesContent
}

// ModuleKeyPrefix is the fixed namespace for all module keys, e.g.
// "properties.desired.modules.cv-writer-kvrocks". Exported so a caller
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
