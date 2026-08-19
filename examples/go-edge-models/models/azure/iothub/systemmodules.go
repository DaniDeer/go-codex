package iothub

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// ── SystemModuleConfig ────────────────────────────────────────────────────────
//
// Wire: {"settings": {...}, "type": "docker", "env": {...}, "status"?: "running", "restartPolicy"?: "always"}
//
// A LOOSER-optionality sibling of ModuleConfig — real
// Azure IoT Edge manifests show edgeAgent's OWN systemModules entry with
// ONLY settings/type/env (no status/restartPolicy/version at all), while
// edgeHub's entry additionally carries status/restartPolicy (but still
// never version). Modeling this as ModuleConfig
// (Status/RestartPolicy/Version all REQUIRED) would reject real,
// well-formed data — so this is its own type, reusing ModuleSettings/
// EnvVars/Type (identical shape) but with Status/RestartPolicy OPTIONAL
// and no Version field at all (system modules are never version-tracked
// the way regular containers are).
//
// Used by BOTH this package's own OPTIONAL, per-name
// LayeredDeployment-level override (see keys.go's SystemModuleName/
// SystemModuleKeyPrefix and layereddeployment.go's
// LayeredModulesContent.SystemModules) AND BaseDeployment's mandatory,
// always-both-present SystemModules struct below — one value type,
// reused unchanged by both document shapes.
type SystemModuleConfig struct {
	Settings      ModuleSettings
	Env           EnvVars
	Type          Type
	Status        Status
	RestartPolicy RestartPolicy
}

// SystemModuleConfigCodec is HAND-ROLLED (not built via c.Struct) because
// Status/RestartPolicy must be OMITTED ENTIRELY on Encode when unset
// (e.g. edgeAgent's own entry never has either) — c.Struct's Encode
// always writes every declared field, even OptionalField ones, which
// would try to Encode Status/RestartPolicy's Go zero value ("") through
// their OneOf-constrained codecs and fail. c.PartialField (which DOES
// omit unset fields) belongs to c.PartialStruct's own, entirely-optional
// composition root, incompatible with Settings/Type staying genuinely
// REQUIRED here — so this mixed "some required, some genuinely optional
// enum" shape is hand-rolled instead, mirroring
// RouteCodec/deviceconfig.PatchCodec's own precedent
// for cases c.Struct/c.PartialStruct don't fit.
var SystemModuleConfigCodec = c.Codec[SystemModuleConfig]{
	Encode: func(smc SystemModuleConfig) (any, error) {
		obj := map[string]any{}
		var errs c.ValidationErrors

		settingsRaw, err := ModuleSettingsCodec.Encode(smc.Settings)
		if err != nil {
			errs = append(errs, c.ValidationError{Field: "settings", Err: err})
		} else {
			obj["settings"] = settingsRaw
		}

		typeRaw, err := TypeCodec.Encode(smc.Type)
		if err != nil {
			errs = append(errs, c.ValidationError{Field: "type", Err: err})
		} else {
			obj["type"] = typeRaw
		}

		if len(smc.Env) > 0 {
			envRaw, err := EnvVarsCodec.Encode(smc.Env)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "env", Err: err})
			} else {
				obj["env"] = envRaw
			}
		}

		if smc.Status != "" {
			statusRaw, err := StatusCodec.Encode(smc.Status)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "status", Err: err})
			} else {
				obj["status"] = statusRaw
			}
		}

		if smc.RestartPolicy != "" {
			rpRaw, err := RestartPolicyCodec.Encode(smc.RestartPolicy)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "restartPolicy", Err: err})
			} else {
				obj["restartPolicy"] = rpRaw
			}
		}

		if len(errs) > 0 {
			return obj, errs
		}
		return obj, nil
	},
	Decode: func(raw any) (SystemModuleConfig, error) {
		var smc SystemModuleConfig
		obj, ok := raw.(map[string]any)
		if !ok {
			return smc, c.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", raw)}
		}
		var errs c.ValidationErrors

		if settingsRaw, ok := obj["settings"]; ok {
			val, err := ModuleSettingsCodec.Decode(settingsRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "settings", Err: err})
			} else {
				smc.Settings = val
			}
		} else {
			errs = append(errs, c.ValidationError{Field: "settings", Err: c.ErrMissingField})
		}

		if typeRaw, ok := obj["type"]; ok {
			val, err := TypeCodec.Decode(typeRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "type", Err: err})
			} else {
				smc.Type = val
			}
		} else {
			errs = append(errs, c.ValidationError{Field: "type", Err: c.ErrMissingField})
		}

		if envRaw, ok := obj["env"]; ok {
			val, err := EnvVarsCodec.Decode(envRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "env", Err: err})
			} else {
				smc.Env = EnvVars(val)
			}
		}

		if statusRaw, ok := obj["status"]; ok {
			val, err := StatusCodec.Decode(statusRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "status", Err: err})
			} else {
				smc.Status = val
			}
		}

		if rpRaw, ok := obj["restartPolicy"]; ok {
			val, err := RestartPolicyCodec.Decode(rpRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "restartPolicy", Err: err})
			} else {
				smc.RestartPolicy = val
			}
		}

		if len(errs) > 0 {
			return smc, errs
		}
		return smc, nil
	},
	Schema: schema.Schema{
		Type: "object",
		Properties: []schema.Property{
			{Name: "settings", Schema: ModuleSettingsCodec.Schema},
			{Name: "type", Schema: TypeCodec.Schema},
			{Name: "env", Schema: EnvVarsCodec.Schema},
			{Name: "status", Schema: StatusCodec.Schema},
			{Name: "restartPolicy", Schema: RestartPolicyCodec.Schema},
		},
		Required: []string{"settings", "type"},
	},
}

// ── SystemModules ──────────────────────────────────────────────────────────────
//
// Wire: {"edgeAgent": {...SystemModuleConfig...}, "edgeHub": {...SystemModuleConfig...}}
//
// SystemModules holds a BaseDeployment's own "systemModules" document —
// exactly two entries, ALWAYS both present (unlike a LayeredDeployment's
// own OPTIONAL, per-name override — see keys.go's SystemModuleName/
// SystemModuleKeyPrefix and layereddeployment.go's
// LayeredModulesContent.SystemModules), reusing SystemModuleConfig
// unchanged, above.
type SystemModules struct {
	EdgeAgent SystemModuleConfig
	EdgeHub   SystemModuleConfig
}

var SystemModulesCodec = c.Struct[SystemModules](
	c.RequiredField("edgeAgent", SystemModuleConfigCodec,
		func(sm SystemModules) SystemModuleConfig { return sm.EdgeAgent },
		func(sm *SystemModules, val SystemModuleConfig) { sm.EdgeAgent = val },
	),
	c.RequiredField("edgeHub", SystemModuleConfigCodec,
		func(sm SystemModules) SystemModuleConfig { return sm.EdgeHub },
		func(sm *SystemModules, val SystemModuleConfig) { sm.EdgeHub = val },
	),
)
