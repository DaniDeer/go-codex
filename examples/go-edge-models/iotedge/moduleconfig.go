package iotedge

import (
	c "github.com/DaniDeer/go-codex/codex"
)

// ── ModuleConfig ──────────────────────────────────────────────────────────────

// ModuleConfig is a single module's full desired-state configuration.
type ModuleConfig struct {
	Settings      ModuleSettings
	Env           EnvVars
	Type          Type
	Status        Status
	RestartPolicy RestartPolicy
	Version       Version
}

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
