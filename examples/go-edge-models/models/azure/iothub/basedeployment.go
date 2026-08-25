package iothub

import (
	c "github.com/DaniDeer/go-codex/codex"
)

// This file assembles this package's own types (schemaversion.go/
// systemmodules.go/runtime.go/storeandforward.go) plus its own reused
// Modules/Routes types (bare-keyed via keys.go's BaseModulesCodec/
// BaseRoutesCodec) into the FULL, nested Azure IoT Edge deployment manifest
// — [BaseDeployment], the priority-0 BASE deployment applied to every
// device matching a target condition, distinct from a [LayeredDeployment]
// (the flat-dotted-key layered-deployment PATCH form — see
// layereddeployment.go) that gets merged on top of it. The
// examples/go-edge-models application layer's own use of these two
// shapes — a GLOBAL baseline file, per-use-case templates, and
// per-device patches, three-way merged — lives in the
// models/iotedge package (usecase.NewBaselineFile/
// finaldeviceconfig.Merge), NOT here (this package models the generic
// Azure spec only, with zero knowledge of that layering).
//
// The real, on-the-wire shape — see keys.go's own doc comment for the
// full example.

// ── EdgeAgentProperties / EdgeHubProperties ───────────────────────────────────

// EdgeAgentProperties is $edgeAgent's "properties.desired" document —
// the schema version, the IoT Edge runtime's own settings, the two
// system modules (edgeAgent/edgeHub themselves), and every common
// module deployed to every device.
type EdgeAgentProperties struct {
	SchemaVersion SchemaVersion
	Runtime       Runtime
	SystemModules SystemModules
	// Modules reuses Modules (map[ModuleName]ModuleConfig)
	// — bare-keyed here (see BaseModulesCodec), unlike LayeredDeployment's
	// own flat-dotted-key "modulesContent"/"$edgeAgent" bag.
	Modules Modules
}

var EdgeAgentPropertiesCodec = c.Struct[EdgeAgentProperties](
	SchemaVersionField(
		func(p EdgeAgentProperties) SchemaVersion { return p.SchemaVersion },
		func(p *EdgeAgentProperties, val SchemaVersion) { p.SchemaVersion = val },
	),
	c.RequiredField("runtime", RuntimeCodec,
		func(p EdgeAgentProperties) Runtime { return p.Runtime },
		func(p *EdgeAgentProperties, val Runtime) { p.Runtime = val },
	),
	c.RequiredField("systemModules", SystemModulesCodec,
		func(p EdgeAgentProperties) SystemModules { return p.SystemModules },
		func(p *EdgeAgentProperties, val SystemModules) { p.SystemModules = val },
	),
	c.RequiredField("modules", BaseModulesCodec,
		// BaseModulesCodec's Go type is map[ModuleName]
		// ModuleConfig (from codex.Map), not the named
		// Modules type — same getter/setter type-boundary conversion
		// pattern BaseModulesContentCodec itself uses.
		func(p EdgeAgentProperties) map[ModuleName]ModuleConfig {
			return p.Modules
		},
		func(p *EdgeAgentProperties, val map[ModuleName]ModuleConfig) {
			p.Modules = Modules(val)
		},
	),
)

// EdgeAgentCodec wraps EdgeAgentPropertiesCodec under PropertiesDesiredKey
// — the "$edgeAgent" value's ACTUAL wire shape: ONE flat key
// ("properties.desired") holding the whole nested document.
var EdgeAgentCodec = c.Struct[EdgeAgentProperties](
	c.RequiredField(PropertiesDesiredKey, EdgeAgentPropertiesCodec,
		func(p EdgeAgentProperties) EdgeAgentProperties { return p },
		func(p *EdgeAgentProperties, val EdgeAgentProperties) { *p = val },
	),
)

// EdgeHubProperties is $edgeHub's "properties.desired" document — the
// schema version, every route, and the store-and-forward retention
// window.
type EdgeHubProperties struct {
	SchemaVersion                SchemaVersion
	Routes                       Routes
	StoreAndForwardConfiguration StoreAndForwardConfiguration
}

var EdgeHubPropertiesCodec = c.Struct[EdgeHubProperties](
	SchemaVersionField(
		func(p EdgeHubProperties) SchemaVersion { return p.SchemaVersion },
		func(p *EdgeHubProperties, val SchemaVersion) { p.SchemaVersion = val },
	),
	c.RequiredField("routes", BaseRoutesCodec,
		func(p EdgeHubProperties) map[RouteName]Route {
			return p.Routes
		},
		func(p *EdgeHubProperties, val map[RouteName]Route) {
			p.Routes = Routes(val)
		},
	),
	c.RequiredField("storeAndForwardConfiguration", StoreAndForwardConfigurationCodec,
		func(p EdgeHubProperties) StoreAndForwardConfiguration { return p.StoreAndForwardConfiguration },
		func(p *EdgeHubProperties, val StoreAndForwardConfiguration) { p.StoreAndForwardConfiguration = val },
	),
)

// EdgeHubCodec wraps EdgeHubPropertiesCodec under PropertiesDesiredKey —
// mirrors EdgeAgentCodec exactly, above.
var EdgeHubCodec = c.Struct[EdgeHubProperties](
	c.RequiredField(PropertiesDesiredKey, EdgeHubPropertiesCodec,
		func(p EdgeHubProperties) EdgeHubProperties { return p },
		func(p *EdgeHubProperties, val EdgeHubProperties) { *p = val },
	),
)

// ── BaseModulesContent / BaseDeployment ──────────────────────────────────────────────────

// BaseModulesContent models the full base deployment's "modulesContent"
// wrapper — mirrors LayeredModulesContent's role exactly, one layer
// richer (EdgeAgentProperties/EdgeHubProperties instead of a flat
// Modules/Routes map).
type BaseModulesContent struct {
	EdgeAgent EdgeAgentProperties
	EdgeHub   EdgeHubProperties
}

var BaseModulesContentCodec = c.Struct[BaseModulesContent](
	c.RequiredField(EdgeAgentKey, EdgeAgentCodec,
		func(mc BaseModulesContent) EdgeAgentProperties { return mc.EdgeAgent },
		func(mc *BaseModulesContent, val EdgeAgentProperties) { mc.EdgeAgent = val },
	),
	c.RequiredField(EdgeHubKey, EdgeHubCodec,
		func(mc BaseModulesContent) EdgeHubProperties { return mc.EdgeHub },
		func(mc *BaseModulesContent, val EdgeHubProperties) { mc.EdgeHub = val },
	),
)

// BaseDeployment is the top-level, FULL/nested Azure IoT Edge deployment
// manifest document — see this file's own doc comment for the complete
// wire shape.
type BaseDeployment struct {
	ModulesContent BaseModulesContent
}

// BaseDeploymentCodec is BaseDeployment's canonical codec.
var BaseDeploymentCodec = c.Struct[BaseDeployment](
	c.RequiredField(ModulesContentKey, BaseModulesContentCodec,
		func(m BaseDeployment) BaseModulesContent { return m.ModulesContent },
		func(m *BaseDeployment, val BaseModulesContent) { m.ModulesContent = val },
	),
)
