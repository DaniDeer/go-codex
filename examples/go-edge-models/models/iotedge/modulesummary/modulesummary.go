package modulesummary

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/schema"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── ModuleOrSystemModuleNameCodec ──────────────────────────────────────────────
//
// ReadReq.ModuleName/updatemoduleimage.Req.ModuleName keep a SINGLE
// string-ish field (no new parallel field) for a simple one-call UX —
// callers ask for "edgeAgent" exactly the same way they ask for
// "factory-dashboard". Validation is relaxed to accept EITHER a regular
// slug-shaped module name OR one of the two reserved system-module
// names — NOT by loosening iothub.ModuleNameCodec itself
// (that codec's job stays "a real modules-bucket wire key", unchanged);
// this is a tool-input-only concern.

// moduleOrSystemModuleNameConstraint accepts validate.Slug (a regular
// module name) OR exactly "edgeAgent"/"edgeHub" (the two system module
// names — see iothub.SystemModuleNameCodec).
var moduleOrSystemModuleNameConstraint = c.Constraint[string]{
	Name: "moduleOrSystemModuleName",
	Check: func(s string) bool {
		return v.Slug.Check(s) || s == "edgeAgent" || s == "edgeHub"
	},
	Message: func(s string) string {
		return fmt.Sprintf(
			"must be a valid module name (lowercase alphanumeric and hyphens) or one of the system module names (edgeAgent, edgeHub), got %q", s)
	},
}

// ModuleOrSystemModuleNameCodec validates/wraps a bare string as
// iothub.ModuleName, accepting EITHER shape (see
// moduleOrSystemModuleNameConstraint above) — used by
// modulesummary.ReadReq/updatemoduleimage.Req's own moduleName field
// instead of iothub.ModuleNameCodec (which validates a FULL
// dotted WIRE key, not a bare tool-input name).
var ModuleOrSystemModuleNameCodec = c.MapCodecSafe(
	c.String().Refine(moduleOrSystemModuleNameConstraint),
	func(s string) iothub.ModuleName { return iothub.ModuleName(s) },
	func(n iothub.ModuleName) (string, error) { return string(n), nil },
)

// IsSystemModuleName reports whether name matches one of the two
// reserved system module names ("edgeAgent"/"edgeHub") — app/iotedge's
// unified lookup/update helpers branch on this to decide whether to
// resolve/patch via the regular Modules bucket or the SystemModules
// bucket.
func IsSystemModuleName(name iothub.ModuleName) bool {
	return string(name) == "edgeAgent" || string(name) == "edgeHub"
}

// ── Summary ─────────────────────────────────────────────────────────────

// Summary is a REDUCED, read-only view of one module's
// iothub.ModuleConfig OR iothub.SystemModuleConfig —
// image, actual host-mapped ports (PortBindings, not the declared-in-
// image ExposedPorts), bind mounts, status, and restart policy. Intended
// for callers (e.g. an MCP tool's LLM-facing response) that want a quick
// operational summary without the full nested ModuleConfig/
// CreateOptions document. See [NewSummary]/[NewSummaryFromSystemModule].
//
// Status/RestartPolicy are OPTIONAL (nil = not applicable) — a real
// system module's own config (e.g. edgeAgent itself) may genuinely lack
// both (see iothub.SystemModuleConfig's own doc comment);
// SummaryCodec is hand-rolled (not built via c.Struct, for the SAME
// reason iothub.SystemModuleConfigCodec is) so both are
// OMITTED ENTIRELY on Encode when nil, instead of failing their
// OneOf-constrained codec on an empty zero value. A regular module's
// Summary (built via [NewSummary]) always sets both — only
// [NewSummaryFromSystemModule] may leave either nil.
type Summary struct {
	Image         docker.Image
	PortBindings  []docker.PortBinding
	Binds         []docker.Bind
	Status        *iothub.Status
	RestartPolicy *iothub.RestartPolicy
}

// SummaryCodec is Summary's canonical codec — used as ReadTool's output
// codec (readmodulesummary.go). There is no [codex.HasCodec]/New smart
// constructor for Summary: like registry.TagsList, it is always
// machine-constructed from a ModuleConfig via [NewSummary], never
// hand-assembled by a caller, so a smart constructor would add
// ceremony with no constraint payoff.
var SummaryCodec = c.Codec[Summary]{
	Encode: func(s Summary) (any, error) {
		obj := map[string]any{}
		var errs c.ValidationErrors

		imageRaw, err := iothub.ImageCodec.Encode(s.Image)
		if err != nil {
			errs = append(errs, c.ValidationError{Field: "image", Err: err})
		} else {
			obj["image"] = imageRaw
		}

		if len(s.PortBindings) > 0 {
			raw, err := docker.PortBindingCodec.Encode(s.PortBindings)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "portBindings", Err: err})
			} else {
				obj["portBindings"] = raw
			}
		}

		if len(s.Binds) > 0 {
			raw, err := c.SliceOf(docker.BindCodec).Encode(s.Binds)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "binds", Err: err})
			} else {
				obj["binds"] = raw
			}
		}

		if s.Status != nil {
			raw, err := iothub.StatusCodec.Encode(*s.Status)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "status", Err: err})
			} else {
				obj["status"] = raw
			}
		}

		if s.RestartPolicy != nil {
			raw, err := iothub.RestartPolicyCodec.Encode(*s.RestartPolicy)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "restartPolicy", Err: err})
			} else {
				obj["restartPolicy"] = raw
			}
		}

		if len(errs) > 0 {
			return obj, errs
		}
		return obj, nil
	},
	Decode: func(raw any) (Summary, error) {
		var s Summary
		obj, ok := raw.(map[string]any)
		if !ok {
			return s, c.TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", raw)}
		}
		var errs c.ValidationErrors

		if imageRaw, ok := obj["image"]; ok {
			val, err := iothub.ImageCodec.Decode(imageRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "image", Err: err})
			} else {
				s.Image = val
			}
		} else {
			errs = append(errs, c.ValidationError{Field: "image", Err: c.ErrMissingField})
		}

		if portBindingsRaw, ok := obj["portBindings"]; ok {
			val, err := docker.PortBindingCodec.Decode(portBindingsRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "portBindings", Err: err})
			} else {
				s.PortBindings = val
			}
		}

		if bindsRaw, ok := obj["binds"]; ok {
			val, err := c.SliceOf(docker.BindCodec).Decode(bindsRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "binds", Err: err})
			} else {
				s.Binds = val
			}
		}

		if statusRaw, ok := obj["status"]; ok {
			val, err := iothub.StatusCodec.Decode(statusRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "status", Err: err})
			} else {
				s.Status = &val
			}
		}

		if rpRaw, ok := obj["restartPolicy"]; ok {
			val, err := iothub.RestartPolicyCodec.Decode(rpRaw)
			if err != nil {
				errs = append(errs, c.ValidationError{Field: "restartPolicy", Err: err})
			} else {
				s.RestartPolicy = &val
			}
		}

		if len(errs) > 0 {
			return s, errs
		}
		return s, nil
	},
	Schema: schema.Schema{
		Type: "object",
		Properties: []schema.Property{
			{Name: "image", Schema: iothub.ImageCodec.Schema},
			{Name: "portBindings", Schema: docker.PortBindingCodec.Schema},
			{Name: "binds", Schema: c.SliceOf(docker.BindCodec).Schema},
			{Name: "status", Schema: iothub.StatusCodec.Schema},
			{Name: "restartPolicy", Schema: iothub.RestartPolicyCodec.Schema},
		},
		Required: []string{"image"},
	},
}

// NewSummary extracts a Summary from mc — a pure mapping, no I/O, no
// validation beyond what mc itself already satisfies (mc is assumed to
// already be a valid, decoded ModuleConfig). A regular module's
// Status/RestartPolicy are always set.
func NewSummary(mc iothub.ModuleConfig) Summary {
	status := mc.Status
	restartPolicy := mc.RestartPolicy
	return Summary{
		Image:         mc.Settings.Image,
		PortBindings:  mc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:         mc.Settings.CreateOptions.HostConfig.Binds,
		Status:        &status,
		RestartPolicy: &restartPolicy,
	}
}

// NewSummaryFromSystemModule extracts a Summary from smc — mirrors
// [NewSummary] exactly, for the system-module case (edgeAgent/edgeHub
// themselves, see the
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub]
// package). Status/RestartPolicy stay nil when smc itself leaves them
// unset (e.g. edgeAgent's own config never sets either) — Summary's own
// doc comment explains why this is safe to encode.
func NewSummaryFromSystemModule(smc iothub.SystemModuleConfig) Summary {
	s := Summary{
		Image:        smc.Settings.Image,
		PortBindings: smc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:        smc.Settings.CreateOptions.HostConfig.Binds,
	}
	if smc.Status != "" {
		status := smc.Status
		s.Status = &status
	}
	if smc.RestartPolicy != "" {
		restartPolicy := smc.RestartPolicy
		s.RestartPolicy = &restartPolicy
	}
	return s
}

// SystemModuleConfigFor looks up name ("edgeAgent"/"edgeHub") in sm,
// returning the matching iothub.SystemModuleConfig — a small
// helper so app/iotedge's unified module/system-module lookup doesn't
// need to know iothub.SystemModules' own two-field shape.
func SystemModuleConfigFor(sm iothub.SystemModules, name iothub.SystemModuleName) (iothub.SystemModuleConfig, bool) {
	switch name {
	case "edgeAgent":
		return sm.EdgeAgent, true
	case "edgeHub":
		return sm.EdgeHub, true
	default:
		return iothub.SystemModuleConfig{}, false
	}
}
