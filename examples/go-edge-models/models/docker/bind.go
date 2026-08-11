package docker

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Bind mounts ───────────────────────────────────────────────────────────────
//
// Wire: ["/etc/ssl/local/pubkey.pem:/etc/traefik/ssl/pubkey.pem", ...] —
// "host:container[:mode]" strings. Parsed into a structured Bind for
// field-level access/validation.

// Bind is a parsed Docker bind-mount spec: "<hostPath>:<containerPath>[:<mode>]".
type Bind struct {
	HostPath      string
	ContainerPath string
	// Mode is the optional trailing bind option ("ro", "rw", "z", "Z", ...).
	// Empty string means no mode was specified (Docker's default, read-write).
	Mode string
}

// bindPathCodec is the shared, non-empty-string constraint for Bind's
// HostPath/ContainerPath fields — extracted once since both fields use the
// identical constraint.
var bindPathCodec = c.String().Refine(v.NonEmptyString)

var bindStructCodec = c.Struct[Bind](
	c.RequiredField("hostPath", bindPathCodec,
		func(b Bind) string { return b.HostPath },
		func(b *Bind, val string) { b.HostPath = val },
	),
	c.RequiredField("containerPath", bindPathCodec,
		func(b Bind) string { return b.ContainerPath },
		func(b *Bind, val string) { b.ContainerPath = val },
	),
	c.OptionalField("mode", c.String(),
		func(b Bind) string { return b.Mode },
		func(b *Bind, val string) { b.Mode = val },
	),
)

// parseBind splits a "host:container[:mode]" bind spec by ":" segment count.
//
// LIMITATION: this is a simple segment-count split (2 segments = no mode, 3
// segments = with mode) — it does NOT handle host or container paths that
// themselves contain literal ":" characters. This is rare on Linux (Docker
// itself has its own, more elaborate heuristics for disambiguating such
// cases) — a path containing ":" will produce an "invalid bind spec" error
// here rather than a guess.
func parseBind(s string) (Bind, error) {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		return Bind{HostPath: parts[0], ContainerPath: parts[1]}, nil
	case 3:
		return Bind{HostPath: parts[0], ContainerPath: parts[1], Mode: parts[2]}, nil
	default:
		return Bind{}, fmt.Errorf(
			"invalid bind spec %q: expected \"host:container\" or \"host:container:mode\"", s)
	}
}

// formatBind reconstructs the "host:container[:mode]" wire string — omits
// the trailing ":mode" segment entirely when Mode is empty.
func formatBind(b Bind) (string, error) {
	if b.Mode == "" {
		return b.HostPath + ":" + b.ContainerPath, nil
	}
	return b.HostPath + ":" + b.ContainerPath + ":" + b.Mode, nil
}

// BindCodec decodes/encodes the wire string through parseBind/formatBind,
// then validates/re-validates the parsed Bind's own field constraints
// (NonEmptyString on HostPath/ContainerPath) via bindStructCodec.Validate.
var BindCodec = c.MapCodecValidated(
	c.String(), bindStructCodec,
	parseBind, formatBind,
)

// Codec implements [codex.HasCodec][Bind], returning [BindCodec].
func (Bind) Codec() c.Codec[Bind] { return BindCodec }

// NewBind is a named per-field smart constructor: validates hostPath/
// containerPath (both non-empty) via BindCodec.New and returns the
// constructed Bind, or the zero value and the first failing constraint's
// error. mode may be empty (Docker's default, read-write).
func NewBind(hostPath, containerPath, mode string) (Bind, error) {
	return BindCodec.New(Bind{HostPath: hostPath, ContainerPath: containerPath, Mode: mode})
}
