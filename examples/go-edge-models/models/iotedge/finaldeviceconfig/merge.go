package finaldeviceconfig

import (
	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Merge ──────────────────────────────────────────────────────────────────────
//
// Merge layers a use case's iothub.LayeredDeployment and a
// device's own deviceconfig.Patch onto the GLOBAL iothub.BaseDeployment,
// producing the FINAL, deployable-to-IoT-Hub config for one device:
// "baseline + template + device config, layered on top" — the priority-
// 0 BASE deployment every device shares, with the use case's own
// modules/routes/system-module overrides layered in, and that ONE
// device's own patch layered on top of THAT.
//
// Three buckets, each merged the same two-step way (Go-level map UNION
// for baseline+template, since both sides are ALREADY the same typed Go
// value — template wins on name collision — then
// codex.ApplyDottedPatch for the device patch, which needs the raw,
// opaque JSON shape since a patch entry may reach ARBITRARILY deep):
//
//   - Modules: iothub.BaseModulesContent.EdgeAgent.Modules ∪
//     template.ModulesContent.EdgeAgent, then patch.EdgeAgent.
//   - SystemModules: baseline's OWN (always-both-present) EdgeAgent/
//     EdgeHub SystemModuleConfig, overridden WHOLESALE by any
//     template.ModulesContent.SystemModules entry, then patch.SystemModules.
//   - Routes: iothub.BaseModulesContent.EdgeHub.Routes ∪
//     template.ModulesContent.EdgeHub, then patch.EdgeHub (routes are
//     atomic — a whole-route add/override, no dotted-path reach needed).
//
// schemaVersion/runtime/storeAndForwardConfiguration are BASELINE-ONLY —
// neither the use case template nor a device patch can touch them; they
// pass through Merge unchanged.
//
// Merge is OVERWRITE/ADD ONLY: a patch value always either creates a new
// key or replaces an existing one; there is no way to DELETE a field a
// lower layer already set (no RFC 7396 null-means-remove semantics) —
// codex.ApplyDottedPatch's own documented behavior. The result is
// re-encoded/re-decoded through iothub.BaseDeploymentCodec, so any merge
// that produces an invalid manifest fails HERE, not silently.
//
// This is a DERIVED operation — not part of either wire format — so it
// lives in its own package, mirroring modulepatch's own "derived, not
// wire" positioning one level up: Merge depends on azure/iothub AND
// deviceconfig, a dependency shape neither of those wire packages may
// take on (they must stay independently reusable, with zero knowledge
// of one another).

// bareSystemModuleNameCodec validates/wraps a bare string as
// iothub.SystemModuleName — restricted to the two real system
// module names via v.OneOf, same constraint
// iothub.SystemModuleNameCodec applies, just without the
// dotted-key template machinery (mirrors iothub.go's own bare
// module/route name codecs — this package's merge glue is the ONE OTHER
// place that needs a bare-keyed system-module map, to round-trip
// through codex.ApplyDottedPatch).
var bareSystemModuleNameCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("edgeAgent", "edgeHub")),
	func(s string) iothub.SystemModuleName { return iothub.SystemModuleName(s) },
	func(n iothub.SystemModuleName) (string, error) { return string(n), nil },
)

// bareSystemModulesCodec decodes/encodes a bare-keyed
// map[iothub.SystemModuleName]iothub.SystemModuleConfig
// — mirrors iothub.BaseModulesCodec exactly, one bucket over.
var bareSystemModulesCodec = c.Map[iothub.SystemModuleName, iothub.SystemModuleConfig](
	bareSystemModuleNameCodec, iothub.SystemModuleConfigCodec,
)

// Merge applies template and patch on top of base, returning the fully
// layered iothub.BaseDeployment.
func Merge(base iothub.BaseDeployment, template iothub.LayeredDeployment, patch deviceconfig.Patch) (iothub.BaseDeployment, error) {
	agentProps := base.ModulesContent.EdgeAgent
	hubProps := base.ModulesContent.EdgeHub

	// ── Modules: union, then dotted patch ──────────────────────────────
	mergedModules := make(iothub.Modules, len(agentProps.Modules)+len(template.ModulesContent.EdgeAgent))
	for name, mc := range agentProps.Modules {
		mergedModules[name] = mc
	}
	for name, mc := range template.ModulesContent.EdgeAgent {
		mergedModules[name] = mc
	}
	rawModules, err := iothub.BaseModulesCodec.Encode(mergedModules)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	patchedRawModules := c.ApplyDottedPatch(rawModules.(map[string]any), patch.EdgeAgent)
	patchedModules, err := iothub.BaseModulesCodec.Decode(patchedRawModules)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}

	// ── SystemModules: baseline's fixed pair, template WHOLESALE
	// override, then dotted patch ──────────────────────────────────────
	mergedSystemModules := map[iothub.SystemModuleName]iothub.SystemModuleConfig{
		"edgeAgent": agentProps.SystemModules.EdgeAgent,
		"edgeHub":   agentProps.SystemModules.EdgeHub,
	}
	for name, smc := range template.ModulesContent.SystemModules {
		mergedSystemModules[name] = smc
	}
	rawSystemModules, err := bareSystemModulesCodec.Encode(mergedSystemModules)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	patchedRawSystemModules := c.ApplyDottedPatch(rawSystemModules.(map[string]any), patch.SystemModules)
	patchedSystemModules, err := bareSystemModulesCodec.Decode(patchedRawSystemModules)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}

	// ── Routes: union, then whole-route add/override (atomic — no
	// dotted-path reach needed) ────────────────────────────────────────
	mergedRoutes := make(iothub.Routes, len(hubProps.Routes)+len(template.ModulesContent.EdgeHub)+len(patch.EdgeHub))
	for name, route := range hubProps.Routes {
		mergedRoutes[name] = route
	}
	for name, route := range template.ModulesContent.EdgeHub {
		mergedRoutes[name] = route
	}
	for name, route := range patch.EdgeHub {
		mergedRoutes[name] = route
	}

	result := iothub.BaseDeployment{
		ModulesContent: iothub.BaseModulesContent{
			EdgeAgent: iothub.EdgeAgentProperties{
				SchemaVersion: agentProps.SchemaVersion,
				Runtime:       agentProps.Runtime,
				SystemModules: iothub.SystemModules{
					EdgeAgent: patchedSystemModules["edgeAgent"],
					EdgeHub:   patchedSystemModules["edgeHub"],
				},
				Modules: patchedModules,
			},
			EdgeHub: iothub.EdgeHubProperties{
				SchemaVersion:                hubProps.SchemaVersion,
				Routes:                       mergedRoutes,
				StoreAndForwardConfiguration: hubProps.StoreAndForwardConfiguration,
			},
		},
	}

	// Re-validate via round trip through ManifestCodec — any merge that
	// produced an invalid manifest fails HERE, not silently.
	raw, err := iothub.BaseDeploymentCodec.Encode(result)
	if err != nil {
		return iothub.BaseDeployment{}, err
	}
	return iothub.BaseDeploymentCodec.Decode(raw)
}
