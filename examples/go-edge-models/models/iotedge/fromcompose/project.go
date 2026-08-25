package fromcompose

import (
	"sort"

	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose"
)

// ServicesToModulesCodec is a codex.Codec[iothub.Modules]
// (map[iothub.ModuleName]iothub.ModuleConfig) built via codex.Map,
// pairing serviceNameToModuleNameCodec (the KEY codec) with
// ModuleConfigFromServiceCodec (the VALUE codec, service.go) — the
// SAME "keyCodec + valueCodec -> Codec[map[K]V]" pattern
// iothub.ModulesCodec itself uses one package over. Decode reads an
// ENTIRE Compose services-map wire object (as produced by
// dockercompose.ServicesCodec.Encode) and produces a whole
// iothub.Modules map in ONE call; Encode does the reverse. ConvertProject/
// ConvertDeployment below route the FULL project<->deployment
// transcoding through this ONE value plus dockercompose.ServicesCodec —
// no hand-rolled per-service loop for the actual VALUE transformation
// (only for the separate, name-dependent warning/placeholder-
// personalization pass, which needs each original ServiceName back in
// scope one at a time — see ConvertProject's own comment).
var ServicesToModulesCodec = c.Map[iothub.ModuleName, iothub.ModuleConfig](
	serviceNameToModuleNameCodec, ModuleConfigFromServiceCodec,
)

// ConvertProject converts an entire Compose project into a scaffold
// iothub.LayeredDeployment. The VALUE transformation itself is ONE call:
// re-encode project.Services to wire via dockercompose.ServicesCodec,
// then decode that SAME wire value via ServicesToModulesCodec — "map
// Codec A to Codec B" applied at the whole-collection level, not just
// per-field. ModulesContent.EdgeHub (routes) is ALWAYS LEFT EMPTY — see
// this package's own doc comment for why.
//
// A second, SORTED-NAME pass over project.Services (unavoidable: a
// module's placeholder image needs its OWN sanitized name personalized
// — see personalizePlaceholderImage — and Warnings need each ORIGINAL
// ServiceName, neither of which ServicesToModulesCodec's bulk Decode
// has access to on its own) patches each placeholder image and computes
// every [Warning], in deterministic order (Go map iteration is
// randomized; sorting makes warning ORDER reproducible across runs —
// the resulting Modules map itself has no meaningful order of its own).
//
// KNOWN LIMITATION: if two DIFFERENT service names sanitize to the SAME
// iothub.ModuleName (e.g. "Factory_API" and "factory-api" both become
// "factory-api"), ServicesToModulesCodec's bulk Decode collapses them
// into ONE map entry — which one wins is the SAME "last write during a
// randomized map iteration" non-determinism codex.Map.Decode's own
// implementation has for any key collision; this refactor does not
// attempt to detect or warn about that (real-world Compose service
// names essentially never collide once sanitized).
func ConvertProject(project dockercompose.Project) (iothub.LayeredDeployment, []Warning) {
	raw, err := dockercompose.ServicesCodec.Encode(project.Services)
	if err != nil {
		return iothub.LayeredDeployment{}, []Warning{{
			Kind: WarningPlaceholderImage, Message: "project services could not be encoded: " + err.Error(),
		}}
	}
	modules, err := ServicesToModulesCodec.Decode(raw)
	if err != nil {
		return iothub.LayeredDeployment{}, []Warning{{
			Kind: WarningPlaceholderImage, Message: "internal conversion error: " + err.Error(),
		}}
	}

	names := make([]dockercompose.ServiceName, 0, len(project.Services))
	for name := range project.Services {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	var warnings []Warning
	for _, name := range names {
		svc := project.Services[name]
		moduleName, _ := serviceNameToModuleNameCodec.Decode(string(name))
		mc := modules[moduleName]
		personalizePlaceholderImage(&mc, moduleName)
		modules[moduleName] = mc
		warnings = append(warnings, warningsForService(name, svc, moduleName, mc)...)
	}

	return iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{EdgeAgent: modules},
	}, warnings
}

// ConvertDeployment is the REVERSE of [ConvertProject]: converts an
// entire iothub.LayeredDeployment back into a dockercompose.Project.
// Mirrors ConvertProject's own routing exactly, in the opposite
// direction: encode deployment's modules via ServicesToModulesCodec,
// decode that SAME wire value via dockercompose.ServicesCodec. Routes
// (deployment.ModulesContent.EdgeHub) have NOTHING to reverse — Compose
// has no routing concept a route could map onto — so they are simply
// not represented in the output Project at all (not an error, not a
// Warning; see this package's own doc comment).
func ConvertDeployment(deployment iothub.LayeredDeployment) (dockercompose.Project, []Warning) {
	modules := deployment.ModulesContent.EdgeAgent

	raw, err := ServicesToModulesCodec.Encode(modules)
	if err != nil {
		return dockercompose.Project{}, []Warning{{
			Kind: WarningPlaceholderImage, Message: "modules could not be encoded: " + err.Error(),
		}}
	}
	services, err := dockercompose.ServicesCodec.Decode(raw)
	if err != nil {
		return dockercompose.Project{}, []Warning{{
			Kind: WarningPlaceholderImage, Message: "internal conversion error: " + err.Error(),
		}}
	}

	names := make([]iothub.ModuleName, 0, len(modules))
	for name := range modules {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })

	var warnings []Warning
	for _, moduleName := range names {
		rawName, _ := serviceNameToModuleNameCodec.Encode(moduleName)
		svc := services[dockercompose.ServiceName(rawName.(string))]
		warnings = append(warnings, warningsForModuleConfig(moduleName, modules[moduleName], svc)...)
	}

	return dockercompose.Project{Services: services}, warnings
}
