package modulesummary

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
)

// ── Summary ─────────────────────────────────────────────────────────────

// Summary is a REDUCED, read-only view of one module's
// manifesttemplate.ModuleConfig — image, actual host-mapped ports
// (PortBindings, not the declared-in-image ExposedPorts), bind mounts,
// status, and restart policy. Intended for callers (e.g. an MCP tool's
// LLM-facing response) that want a quick operational summary without
// the full nested ModuleConfig/CreateOptions document. See [NewSummary].
type Summary struct {
	Image         docker.Image
	PortBindings  []docker.PortBinding
	Binds         []docker.Bind
	Status        manifesttemplate.Status
	RestartPolicy manifesttemplate.RestartPolicy
}

// SummaryCodec is Summary's canonical codec — used as ReadTool's output
// codec (readmodulesummary.go). There is no [codex.HasCodec]/New smart
// constructor for Summary: like registry.TagsList, it is always
// machine-constructed from a ModuleConfig via [NewSummary], never
// hand-assembled by a caller, so a smart constructor would add
// ceremony with no constraint payoff.
var SummaryCodec = c.Struct[Summary](
	c.RequiredField("image",
		manifesttemplate.ImageCodec,
		func(s Summary) docker.Image { return s.Image },
		func(s *Summary, v docker.Image) { s.Image = v },
	),
	c.OptionalField("portBindings",
		docker.PortBindingCodec,
		func(s Summary) []docker.PortBinding { return s.PortBindings },
		func(s *Summary, v []docker.PortBinding) { s.PortBindings = v },
	),
	c.OptionalField("binds",
		c.SliceOf(docker.BindCodec),
		func(s Summary) []docker.Bind { return s.Binds },
		func(s *Summary, v []docker.Bind) { s.Binds = v },
	),
	c.RequiredField("status",
		manifesttemplate.StatusCodec,
		func(s Summary) manifesttemplate.Status { return s.Status },
		func(s *Summary, v manifesttemplate.Status) { s.Status = v },
	),
	c.RequiredField("restartPolicy",
		manifesttemplate.RestartPolicyCodec,
		func(s Summary) manifesttemplate.RestartPolicy { return s.RestartPolicy },
		func(s *Summary, v manifesttemplate.RestartPolicy) { s.RestartPolicy = v },
	),
)

// NewSummary extracts a Summary from mc — a pure mapping, no I/O, no
// validation beyond what mc itself already satisfies (mc is assumed to
// already be a valid, decoded ModuleConfig).
func NewSummary(mc manifesttemplate.ModuleConfig) Summary {
	return Summary{
		Image:         mc.Settings.Image,
		PortBindings:  mc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:         mc.Settings.CreateOptions.HostConfig.Binds,
		Status:        mc.Status,
		RestartPolicy: mc.RestartPolicy,
	}
}
