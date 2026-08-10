package docker

import (
	c "github.com/DaniDeer/go-codex/codex"
)

// ── HostConfig / CreateOptions ────────────────────────────────────────────────

// HostConfig models the host-level resource constraints and bindings of a
// Docker create-options document.
type HostConfig struct {
	Binds        []Bind
	PortBindings []PortBinding
	// Memory is the memory limit in bytes (0 = unset/unlimited — the same
	// sentinel Docker itself uses, so there is no ambiguity between "not
	// specified" and "explicitly unlimited").
	Memory int64
	// MemorySwap is the total memory+swap limit in bytes (-1 = unlimited,
	// 0 = unset). Docker requires Memory to also be set when MemorySwap is
	// used — not enforced here (this package models the wire shape, not
	// Docker's own daemon-side cross-field validation).
	MemorySwap int64
	Ulimits    []Ulimit
}

// CreateOptions models the subset of Docker's container create-options
// document commonly used by container-orchestration tooling: the
// container's command/entrypoint/hostname, a healthcheck, exposed ports,
// and host-level bind mounts / port bindings / memory limits / ulimits.
// Field names use PascalCase JSON keys ("Cmd", "Entrypoint", "Hostname",
// "Domainname", "Healthcheck", "ExposedPorts", "HostConfig", "Binds",
// "PortBindings", "HostPort", "Memory", "MemorySwap", "Ulimits") to match
// Docker's own wire contract literally — this is Docker's API convention,
// not a stylistic choice.
type CreateOptions struct {
	// Cmd is the default command (overridable at container start).
	Cmd []string
	// Entrypoint is the fixed executable — Docker always documents Cmd and
	// Entrypoint as a pair: Entrypoint sets what always runs, Cmd supplies
	// default/overridable arguments (or the full command, if Entrypoint is
	// unset).
	Entrypoint   []string
	Hostname     string
	Domainname   string
	ExposedPorts []Port
	HostConfig   HostConfig
	Healthcheck  Healthcheck
	// Env is the container's environment variables — Docker's real
	// create-options document carries these as a flat "Env":
	// ["KEY=VALUE", ...] wire array (see [EnvCodec]).
	Env Env
}

// HostConfigCodec: Binds, PortBindings, Memory, MemorySwap, and Ulimits are
// ALL OptionalField — real create-options documents rarely set all of these
// at once (e.g. a container might set only PortBindings, or only Memory, or
// none of them). Absent -> nil/zero value, the same as "no entries"/"no
// limit" — a semantically harmless equivalence (Docker itself has no way to
// distinguish "explicitly empty/zero" from "not specified" for any of these
// fields).
var HostConfigCodec = c.Struct[HostConfig](
	c.OptionalField("Binds", c.SliceOf(BindCodec),
		func(h HostConfig) []Bind { return h.Binds },
		func(h *HostConfig, val []Bind) { h.Binds = val },
	),
	c.OptionalField("PortBindings", PortBindingCodec,
		func(h HostConfig) []PortBinding { return h.PortBindings },
		func(h *HostConfig, val []PortBinding) { h.PortBindings = val },
	),
	c.OptionalField("Memory", c.Int64(),
		func(h HostConfig) int64 { return h.Memory },
		func(h *HostConfig, val int64) { h.Memory = val },
	),
	c.OptionalField("MemorySwap", c.Int64(),
		func(h HostConfig) int64 { return h.MemorySwap },
		func(h *HostConfig, val int64) { h.MemorySwap = val },
	),
	c.OptionalField("Ulimits", c.SliceOf(UlimitCodec),
		func(h HostConfig) []Ulimit { return h.Ulimits },
		func(h *HostConfig, val []Ulimit) { h.Ulimits = val },
	),
)

// CreateOptionsCodec: every field is OptionalField — real create-options
// documents vary widely in which fields they set (some declare only
// ExposedPorts, others only Cmd, others none of the above). Absent ->
// nil/zero value, same as "nothing declared".
var CreateOptionsCodec = c.Struct[CreateOptions](
	c.OptionalField("Cmd", c.SliceOf(c.String()),
		func(co CreateOptions) []string { return co.Cmd },
		func(co *CreateOptions, val []string) { co.Cmd = val },
	),
	c.OptionalField("Entrypoint", c.SliceOf(c.String()),
		func(co CreateOptions) []string { return co.Entrypoint },
		func(co *CreateOptions, val []string) { co.Entrypoint = val },
	),
	c.OptionalField("Hostname", c.String(),
		func(co CreateOptions) string { return co.Hostname },
		func(co *CreateOptions, val string) { co.Hostname = val },
	),
	c.OptionalField("Domainname", c.String(),
		func(co CreateOptions) string { return co.Domainname },
		func(co *CreateOptions, val string) { co.Domainname = val },
	),
	c.OptionalField("ExposedPorts", ExposedPortsCodec,
		func(co CreateOptions) []Port { return co.ExposedPorts },
		func(co *CreateOptions, val []Port) { co.ExposedPorts = val },
	),
	c.OptionalField("HostConfig", HostConfigCodec,
		func(co CreateOptions) HostConfig { return co.HostConfig },
		func(co *CreateOptions, val HostConfig) { co.HostConfig = val },
	),
	c.OptionalField("Healthcheck", HealthcheckCodec,
		func(co CreateOptions) Healthcheck { return co.Healthcheck },
		func(co *CreateOptions, val Healthcheck) { co.Healthcheck = val },
	),
	c.OptionalField("Env", EnvCodec,
		func(co CreateOptions) Env { return co.Env },
		func(co *CreateOptions, val Env) { co.Env = val },
	),
)

// IsZeroCreateOptions reports whether co has no meaningful content — useful
// for callers that need to distinguish "no create-options document" from
// "an explicit but empty one" (e.g. re-encoding as an empty string instead
// of "{}" when the wire format embeds this document as a JSON-escaped
// string field; see the sibling iotedge package's ModuleSettingsCodec).
func IsZeroCreateOptions(co CreateOptions) bool {
	return len(co.Cmd) == 0 && len(co.Entrypoint) == 0 &&
		co.Hostname == "" && co.Domainname == "" &&
		len(co.ExposedPorts) == 0 &&
		len(co.HostConfig.Binds) == 0 && len(co.HostConfig.PortBindings) == 0 &&
		co.HostConfig.Memory == 0 && co.HostConfig.MemorySwap == 0 &&
		len(co.HostConfig.Ulimits) == 0 &&
		isZeroHealthcheck(co.Healthcheck) &&
		len(co.Env) == 0
}
