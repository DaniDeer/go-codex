package main

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type ModuleConfig struct {
	Settings      ModuleSettings
	Env           EnvVars
	Type          Type
	Status        Status
	RestartPolicy RestartPolicy
	Version       Version
}

type Type string

type Status string

type RestartPolicy string

type Version string

// ── Codecs ────────────────────────────────────────────────────────────────────

var ModuleConfigCodec = c.Struct[ModuleConfig](
	c.RequiredField("settings",
		ModuleSettingsCodec,
		func(mc ModuleConfig) ModuleSettings { return mc.Settings },
		func(mc *ModuleConfig, v ModuleSettings) { mc.Settings = v },
	),
	c.RequiredField("env",
		EnvVarsCodec,
		func(mc ModuleConfig) map[string]EnvVar { return mc.Env },
		func(mc *ModuleConfig, v map[string]EnvVar) { mc.Env = EnvVars(v) },
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

var TypeCodec = c.Struct[Type](
	c.RequiredField("type",
		c.String().Refine(v.OneOf("docker")),
		func(t Type) string { return string(t) },
		func(t *Type, v string) { *t = Type(v) },
	),
)

var StatusCodec = c.Struct[Status](
	c.RequiredField("status",
		c.String().Refine(v.OneOf("running", "stopped")),
		func(s Status) string { return string(s) },
		func(s *Status, v string) { *s = Status(v) },
	),
)

var RestartPolicyCodec = c.Struct[RestartPolicy](
	c.RequiredField("restartPolicy",
		c.String().Refine(v.OneOf("always", "on-failure", "on-unhealthy", "never")),
		func(rp RestartPolicy) string { return string(rp) },
		func(rp *RestartPolicy, v string) { *rp = RestartPolicy(v) },
	),
)

var VersionCodec = c.Struct[Version](
	c.RequiredField("version",
		c.String().Refine(v.NonEmptyString),
		func(v Version) string { return string(v) },
		func(v *Version, val string) { *v = Version(val) },
	),
)
