package iothub

import (
	"fmt"
	"log/slog"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
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

// LayeredModulesContent models the "modulesContent" wrapper (Azure IoT Edge /
// IoT-Edge deployment manifest naming convention).
type LayeredModulesContent struct {
	EdgeAgent Modules
	// SystemModules OPTIONALLY overrides edgeAgent's/edgeHub's OWN
	// configuration (e.g. a use case needing a different edgeAgent
	// image) — a SEPARATE dotted-key namespace from EdgeAgent's regular
	// modules (see keys.go's SystemModuleKeyPrefix), but the SAME "$edgeAgent"
	// JSON object on the wire — both share one flat key bag, split by
	// prefix on decode and merged back on encode (see LayeredModulesContentCodec).
	// Empty/absent is the common case: most use cases never override a
	// system module.
	SystemModules map[SystemModuleName]SystemModuleConfig
	// EdgeHub holds every declared $edgeHub route, keyed by route name
	// — see edgehub.go's Route/Routes for the wire shape. Optional: most
	// use cases declare none at the template level (routes are commonly
	// added/overridden entirely by a device config's patch instead — see
	// the models/iotedge/deviceconfig package).
	EdgeHub Routes
}

// LayeredDeployment is the top-level deployment manifest document.
type LayeredDeployment struct {
	ModulesContent LayeredModulesContent
}

// ModulesCodec decodes/encodes flat "properties.desired.modules.<name>"-
// shaped keys directly into map[ModuleName]ModuleConfig via codex.Map —
// K=ModuleName (extracted from the dotted key), V=ModuleConfig. Used by
// LayeredModulesContentCodec's own $edgeAgent split (see below) on the SUBSET
// of $edgeAgent's keys matching ModuleKeyPrefix.
var ModulesCodec = c.Map[ModuleName, ModuleConfig](ModuleNameCodec, ModuleConfigCodec)

// LayeredSystemModulesCodec decodes/encodes flat
// "properties.desired.systemModules.<name>"-shaped keys directly into
// map[SystemModuleName]SystemModuleConfig via codex.Map — mirrors
// ModulesCodec exactly, one bucket over. Used by LayeredModulesContentCodec's
// own $edgeAgent split on the SUBSET of $edgeAgent's keys matching
// SystemModuleKeyPrefix.
var LayeredSystemModulesCodec = c.Map[SystemModuleName, SystemModuleConfig](SystemModuleNameCodec, SystemModuleConfigCodec)

// ── LayeredModulesContent / LayeredDeployment ───────────────────────────────────────
//
// Wire:
//
//	{
//	  "modulesContent": {
//	    "$edgeAgent": {
//	      "properties.desired.modules.factory-mqtt-gateway": {...},
//	      "properties.desired.systemModules.edgeAgent.settings.image": "...", ...
//	    },
//	    "$edgeHub": {
//	      "properties.desired.routes.factory-mqtt-to-ingest": "FROM ... INTO ...", ...
//	    }
//	  }
//	}
//
// "$edgeAgent" is a FLAT BAG mixing TWO dotted-key namespaces side by
// side — regular module entries (ModuleKeyPrefix) and system-module
// overrides (SystemModuleKeyPrefix, see keys.go) — so LayeredModulesContentCodec
// is HAND-ROLLED (not built via c.Struct, which can only route ONE JSON
// key to ONE Go field): it splits $edgeAgent's raw object by prefix into
// the two typed sub-maps on Decode, and merges them back into one flat
// object on Encode. $edgeHub stays a separate, ordinary top-level key
// (OPTIONAL — most use cases declare none at the template level).

// edgeAgentPrefixError reports an $edgeAgent key matching NEITHER
// ModuleKeyPrefix NOR SystemModuleKeyPrefix — every $edgeAgent key must
// match one of the two; there is no third bucket. Implements
// slog.LogValuer for structured logging.
type edgeAgentPrefixError struct{ Key string }

func (e edgeAgentPrefixError) Error() string {
	return fmt.Sprintf("iothub: $edgeAgent key %q matches neither %q nor %q",
		e.Key, ModuleKeyPrefix, SystemModuleKeyPrefix)
}

// LogValue implements slog.LogValuer for structured logging.
func (e edgeAgentPrefixError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
	)
}

var LayeredModulesContentCodec = c.Codec[LayeredModulesContent]{
	Encode: func(mc LayeredModulesContent) (any, error) {
		obj := map[string]any{}
		var errs c.ValidationErrors

		edgeAgent := map[string]any{}
		modulesRaw, err := ModulesCodec.Encode(map[ModuleName]ModuleConfig(mc.EdgeAgent))
		if err != nil {
			errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: err})
		} else {
			for k, v := range modulesRaw.(map[string]any) {
				edgeAgent[k] = v
			}
		}
		if len(mc.SystemModules) > 0 {
			systemModulesRaw, err := LayeredSystemModulesCodec.Encode(mc.SystemModules)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: err})
			} else {
				for k, v := range systemModulesRaw.(map[string]any) {
					edgeAgent[k] = v
				}
			}
		}
		obj[EdgeAgentKey] = edgeAgent

		// $edgeHub is OPTIONAL — omitted entirely when there are no
		// routes, same rule ModuleConfig.Env's OptionalField already
		// follows for c.Struct-built codecs.
		if len(mc.EdgeHub) > 0 {
			edgeHubRaw, err := RoutesCodec.Encode(map[RouteName]Route(mc.EdgeHub))
			if err != nil {
				errs = append(errs, c.ValidationError{Field: EdgeHubKey, Err: err})
			} else {
				obj[EdgeHubKey] = edgeHubRaw
			}
		}

		if len(errs) > 0 {
			return obj, errs
		}
		return obj, nil
	},
	Decode: func(raw any) (LayeredModulesContent, error) {
		var mc LayeredModulesContent
		obj, ok := raw.(map[string]any)
		if !ok {
			return mc, c.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", raw)}
		}
		var errs c.ValidationErrors

		edgeAgentRaw, ok := obj[EdgeAgentKey]
		if !ok {
			errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: c.ErrMissingField})
		} else {
			edgeAgentObj, ok := edgeAgentRaw.(map[string]any)
			if !ok {
				errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: c.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", edgeAgentRaw)}})
			} else {
				modules := map[string]any{}
				systemModules := map[string]any{}
				for k, v := range edgeAgentObj {
					switch {
					case strings.HasPrefix(k, ModuleKeyPrefix):
						modules[k] = v
					case strings.HasPrefix(k, SystemModuleKeyPrefix):
						systemModules[k] = v
					default:
						errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: edgeAgentPrefixError{Key: k}})
					}
				}
				modulesDec, err := ModulesCodec.Decode(modules)
				if err != nil {
					errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: err})
				} else {
					mc.EdgeAgent = Modules(modulesDec)
				}
				if len(systemModules) > 0 {
					systemModulesDec, err := LayeredSystemModulesCodec.Decode(systemModules)
					if err != nil {
						errs = append(errs, c.ValidationError{Field: EdgeAgentKey, Err: err})
					} else {
						mc.SystemModules = systemModulesDec
					}
				}
			}
		}

		// $edgeHub is OPTIONAL — absent key decodes to Routes' zero
		// value (nil map), no error.
		if edgeHubRaw, ok := obj[EdgeHubKey]; ok {
			edgeHubDec, err := RoutesCodec.Decode(edgeHubRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: EdgeHubKey, Err: err})
			} else {
				mc.EdgeHub = Routes(edgeHubDec)
			}
		}

		if len(errs) > 0 {
			return mc, errs
		}
		return mc, nil
	},
	Schema: schema.Schema{
		Type: "object",
		Properties: []schema.Property{
			{Name: EdgeAgentKey, Schema: schema.Schema{Type: "object"}},
			{Name: EdgeHubKey, Schema: schema.Schema{Type: "object"}},
		},
		Required: []string{EdgeAgentKey},
	},
}

var LayeredDeploymentCodec = c.Struct[LayeredDeployment](
	c.RequiredField(ModulesContentKey, LayeredModulesContentCodec,
		func(dm LayeredDeployment) LayeredModulesContent { return dm.ModulesContent },
		func(dm *LayeredDeployment, val LayeredModulesContent) { dm.ModulesContent = val },
	),
)
