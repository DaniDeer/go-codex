package iothub

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Module lifecycle metadata ─────────────────────────────────────────────────
//
// TypeCodec, StatusCodec, RestartPolicyCodec, and VersionCodec wrap a plain
// validated STRING as their respective named types — matching the real wire
// shape, where "type"/"status"/"restartPolicy"/"version" are bare JSON
// strings (e.g. "type": "docker"), NOT single-key objects
// ({"type": "docker"}).

type Type string

type Status string

type RestartPolicy string

type Version string

type StartupOrder int64

// TypeCodec uses c.Eq rather than v.OneOf, unlike Status/RestartPolicy
// below — "docker" is the ONLY valid value (not one of several), so this
// is a fixed-value ("constant") field: c.Eq(base, value) expresses
// "only this exact value is valid" directly, rather than a degenerate
// single-element enum. See docs/guides/wire-vocabulary.md's "Fixed-value
// fields" section for the general recipe.
var TypeCodec = c.MapCodecSafe(
	c.Eq(c.String(), "docker"),
	func(s string) Type { return Type(s) },
	func(t Type) (string, error) { return string(t), nil },
)

var StatusCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("running", "stopped")),
	func(s string) Status { return Status(s) },
	func(st Status) (string, error) { return string(st), nil },
)

var RestartPolicyCodec = c.MapCodecSafe(
	c.String().Refine(v.OneOf("always", "on-failure", "on-unhealthy", "never")),
	func(s string) RestartPolicy { return RestartPolicy(s) },
	func(rp RestartPolicy) (string, error) { return string(rp), nil },
)

var VersionCodec = c.MapCodecSafe(
	c.String().Refine(v.NonEmptyString),
	func(s string) Version { return Version(s) },
	func(ver Version) (string, error) { return string(ver), nil },
)

var StartupOrderCodec = c.MapCodecSafe(
	c.Int64().Refine(v.PositiveInt64),
	func(i int64) StartupOrder { return StartupOrder(i) },
	func(so StartupOrder) (int64, error) { return int64(so), nil },
)
