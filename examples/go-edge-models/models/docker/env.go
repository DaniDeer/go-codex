package docker

import (
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
)

// ── Env ───────────────────────────────────────────────────────────────────────
//
// Wire: "Env":["KEY=VALUE", ...] — Docker's real create-options document's
// flat environment-variable array.

// EnvVar is one parsed "KEY=VALUE" container environment variable — see
// [EnvCodec] for the wire []string <-> []EnvVar codec. Name is
// deliberately UNCONSTRAINED (no format validation): real-world container
// env var names commonly violate the POSIX convention (e.g.
// "https_proxy", mixed case) and Docker itself places no restriction on
// them, so validate.EnvVarName is intentionally NOT applied here — the
// SAME reasoning manifesttemplate.EnvVarNameCodec's own doc comment documents for
// IoT Edge module env vars.
type EnvVar struct {
	Name  string
	Value string
}

// Env is a list of parsed container environment variables — the shared
// domain type produced by decoding a "KEY=VALUE" wire array (see
// [EnvCodec]) and the return type of [iotedge.FlattenEnvVars]'s
// one-direction iotedge -> docker mapper (see that function's own doc
// comment for why there is no reverse mapper).
type Env []EnvVar

// parseEnvVar splits one "KEY=VALUE" wire entry on the FIRST "=" — a value
// may itself legitimately contain "=" (e.g. "KEY=a=b"), so splitting on the
// first occurrence only (not every occurrence) is the correct behavior.
// An entry with no "=" at all (e.g. bare "KEY", meaning "inherit from the
// host" in Docker's own CLI) becomes EnvVar{Name: "KEY", Value: ""} —
// never an error; Env.Name is deliberately unconstrained (see EnvVar's own
// doc comment), so this can never fail.
func parseEnvVar(s string) EnvVar {
	if i := strings.Index(s, "="); i != -1 {
		return EnvVar{Name: s[:i], Value: s[i+1:]}
	}
	return EnvVar{Name: s}
}

// formatEnvVar reconstructs the "KEY=VALUE" wire string from v.
func formatEnvVar(v EnvVar) string {
	return v.Name + "=" + v.Value
}

// EnvCodec decodes/encodes the wire []string <-> []EnvVar, applying
// parseEnvVar/formatEnvVar per entry. Both directions are infallible
// (EnvVar.Name has no format constraint — see its own doc comment), so
// this is a plain MapCodecSafe over a slice, the SAME pattern
// docker/registry's own tagsFieldCodec uses.
var EnvCodec = c.MapCodecSafe(
	c.SliceOf(c.String()),
	func(ss []string) Env {
		out := make(Env, len(ss))
		for i, s := range ss {
			out[i] = parseEnvVar(s)
		}
		return out
	},
	func(vs Env) ([]string, error) {
		out := make([]string, len(vs))
		for i, v := range vs {
			out[i] = formatEnvVar(v)
		}
		return out, nil
	},
)
