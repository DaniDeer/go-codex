package iotedge

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

// ── ModuleSummary ─────────────────────────────────────────────────────────────

// ModuleSummary is a REDUCED, read-only view of one module's
// ModuleConfig — image, actual host-mapped ports (PortBindings, not the
// declared-in-image ExposedPorts), bind mounts, status, and restart
// policy. Intended for callers (e.g. an MCP tool's LLM-facing response)
// that want a quick operational summary without the full nested
// ModuleConfig/CreateOptions document. See [NewModuleSummary].
type ModuleSummary struct {
	Image         docker.Image
	PortBindings  []docker.PortBinding
	Binds         []docker.Bind
	Status        Status
	RestartPolicy RestartPolicy
}

// ModuleSummaryCodec is ModuleSummary's canonical codec — used as
// ReadModuleSummaryTool's output codec (readmodulesummary.go). There is
// no [codex.HasCodec]/New smart constructor for ModuleSummary: like
// registry.TagsList, it is always machine-constructed from a
// ModuleConfig via [NewModuleSummary], never hand-assembled by a caller,
// so a smart constructor would add ceremony with no constraint payoff.
var ModuleSummaryCodec = c.Struct[ModuleSummary](
	c.RequiredField("image",
		ImageCodec,
		func(s ModuleSummary) docker.Image { return s.Image },
		func(s *ModuleSummary, v docker.Image) { s.Image = v },
	),
	c.OptionalField("portBindings",
		docker.PortBindingCodec,
		func(s ModuleSummary) []docker.PortBinding { return s.PortBindings },
		func(s *ModuleSummary, v []docker.PortBinding) { s.PortBindings = v },
	),
	c.OptionalField("binds",
		c.SliceOf(docker.BindCodec),
		func(s ModuleSummary) []docker.Bind { return s.Binds },
		func(s *ModuleSummary, v []docker.Bind) { s.Binds = v },
	),
	c.RequiredField("status",
		StatusCodec,
		func(s ModuleSummary) Status { return s.Status },
		func(s *ModuleSummary, v Status) { s.Status = v },
	),
	c.RequiredField("restartPolicy",
		RestartPolicyCodec,
		func(s ModuleSummary) RestartPolicy { return s.RestartPolicy },
		func(s *ModuleSummary, v RestartPolicy) { s.RestartPolicy = v },
	),
)

// NewModuleSummary extracts a ModuleSummary from mc — a pure mapping, no
// I/O, no validation beyond what mc itself already satisfies (mc is
// assumed to already be a valid, decoded ModuleConfig).
func NewModuleSummary(mc ModuleConfig) ModuleSummary {
	return ModuleSummary{
		Image:         mc.Settings.Image,
		PortBindings:  mc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:         mc.Settings.CreateOptions.HostConfig.Binds,
		Status:        mc.Status,
		RestartPolicy: mc.RestartPolicy,
	}
}
