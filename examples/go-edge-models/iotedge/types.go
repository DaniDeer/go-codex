package iotedge

import "github.com/DaniDeer/go-codex/examples/go-edge-models/docker"

// ── Module lifecycle metadata ─────────────────────────────────────────────────

type Type string

type Status string

type RestartPolicy string

type Version string

type StartupOrder int64

// ── ModuleSettings ────────────────────────────────────────────────────────────

// ModuleSettings holds a module's image reference and its Docker
// create-options document.
type ModuleSettings struct {
	Image string
	// CreateOptions is a Docker create-options document. On the wire it is a
	// JSON-escaped STRING (e.g. "{\"ExposedPorts\":{...}}") —
	// CreateOptionsFieldCodec handles the string-escaping transparently, so
	// this field is the fully-typed docker.CreateOptions value, not a raw
	// string.
	CreateOptions docker.CreateOptions
}

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

// ── Environment variables ─────────────────────────────────────────────────────

// EnvVarName is the environment variable's name (map key) — a named type for
// semantic clarity, WITHOUT a format constraint (see EnvVarNameCodec for why).
type EnvVarName string

type EnvVars map[EnvVarName]EnvVar

// EnvVarValue holds EXACTLY ONE of a string, an int64, or a float64 value —
// mirrors the JSON/YAML/TOML wire shape where a module's env var value may
// be a string OR a number, and the number itself may be whole or
// fractional. Pointer fields (nil = unset) are the discriminator: a
// zero-value int64/float64 (0) and an empty string ("") are all legitimate
// values, so nil-vs-non-nil is the only unambiguous signal.
type EnvVarValue struct {
	StringValue *string
	IntValue    *int64
	FloatValue  *float64
}

type EnvVar struct {
	Value EnvVarValue
}

// ── ModuleName / Modules — codex.Map key extraction ───────────────────────────
//
// Wire: {"properties.desired.modules.cv-writer-kvrocks": {...ModuleConfig...}, ...}
// Go:   map[ModuleName]ModuleConfig{"cv-writer-kvrocks": {...}, ...}

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
