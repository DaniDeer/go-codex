package manifesttemplate

import (
	c "github.com/DaniDeer/go-codex/codex"
)

// ── ModuleName / Modules ───────────────────────────────────────────────────────
//
// See keys.go for ModuleKeyPrefix, the dotted-key extraction machinery
// (moduleKeyConstraint/moduleNameCodec/ModuleNameCodec), and the
// EdgeAgentKey/EdgeHubKey/ModulesContentKey top-level wire-key names this
// file's codecs are built from.

// ModuleName is the module/container name extracted from a dotted module
// key, e.g. "factory-mqtt-gateway" from "properties.desired.modules.factory-mqtt-gateway".
type ModuleName string

// Modules maps each module's extracted name to its full configuration.
type Modules map[ModuleName]ModuleConfig

// ModulesContent models the "modulesContent" wrapper (Azure IoT Edge /
// IoT-Edge deployment manifest naming convention).
type ModulesContent struct {
	EdgeAgent Modules
	// EdgeHub holds every declared $edgeHub route, keyed by route name
	// — see edgehub.go's Route/Routes for the wire shape. Optional: most
	// use cases declare none at the template level (routes are commonly
	// added/overridden entirely by a device config's patch instead — see
	// the sibling deviceconfig package).
	EdgeHub Routes
}

// DeploymentManifest is the top-level deployment manifest document.
type DeploymentManifest struct {
	ModulesContent ModulesContent
}

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
//	      "properties.desired.modules.factory-mqtt-gateway": {...}, ...
//	    }
//	  }
//	}
//
// "$edgeAgent" contains ONLY module entries (every key matches
// ModuleKeyPrefix) — no schemaVersion or other keys mixed in at that level
// — so it maps directly to Modules with no further nesting.

var ModulesContentCodec = c.Struct[ModulesContent](
	c.RequiredField(EdgeAgentKey, ModulesCodec,
		// ModulesCodec's Go type is map[ModuleName]ModuleConfig (from
		// codex.Map), not the named Modules type — same getter/setter
		// type-boundary conversion pattern used for ModuleConfig.Env
		// (Go generic type inference needs exact type identity, not mere
		// assignability, even though Modules IS literally
		// map[ModuleName]ModuleConfig).
		func(mc ModulesContent) map[ModuleName]ModuleConfig { return mc.EdgeAgent },
		func(mc *ModulesContent, val map[ModuleName]ModuleConfig) { mc.EdgeAgent = Modules(val) },
	),
	// $edgeHub is OPTIONAL — absent key decodes to Routes' zero value
	// (nil map), no error, same rule ModuleConfig.Env's OptionalField
	// already follows.
	c.OptionalField(EdgeHubKey, RoutesCodec,
		func(mc ModulesContent) map[RouteName]Route { return mc.EdgeHub },
		func(mc *ModulesContent, val map[RouteName]Route) { mc.EdgeHub = Routes(val) },
	),
)

var DeploymentManifestCodec = c.Struct[DeploymentManifest](
	c.RequiredField(ModulesContentKey, ModulesContentCodec,
		func(dm DeploymentManifest) ModulesContent { return dm.ModulesContent },
		func(dm *DeploymentManifest, val ModulesContent) { dm.ModulesContent = val },
	),
)
