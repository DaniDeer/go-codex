package main

import (
	"fmt"
	"strconv"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Domain Subtypes ───────────────────────────────────────────────────────────

// Port is a Docker port-spec string, "<port>/tcp" or "<port>/udp"
// (e.g. "8080/tcp") — the key shape shared by ExposedPorts and
// HostConfig.PortBindings. Validated by validate.DockerPort.
type Port string

// PortCodec validates the "<port>/tcp"|"<port>/udp" shape via
// validate.DockerPort, then wraps the result as the named Port type (a
// trivial, always-succeeding string<->Port conversion) so it can be used
// directly as a Codec[Port] — required by codex.EntrySlice's key type
// parameter below.
var PortCodec = c.MapCodecSafe(
	c.String().Refine(v.DockerPort),
	func(s string) Port { return Port(s) },
	func(p Port) (string, error) { return string(p), nil },
)

// PortNumberCodec accepts a port number as EITHER a JSON string ("8090") or a
// JSON number (8090) — some Docker data sources represent HostPort as a bare
// number rather than a string — and always canonicalizes to a validated
// string on both decode and encode (round-tripping through this codec never
// re-introduces the string-or-number ambiguity on the way back out). Built
// on codex.StringOrInt() + validate.Port's range check (1-65535).
var PortNumberCodec = c.MapCodecSafe(
	c.StringOrInt(),
	func(e c.Either[string, int]) string {
		if e.Left != nil {
			return *e.Left
		}
		return strconv.Itoa(*e.Right)
	},
	func(s string) (c.Either[string, int], error) {
		// Always re-encode as a string — canonical wire form regardless of
		// which representation was originally decoded.
		return c.Either[string, int]{Left: &s}, nil
	},
).Refine(v.Port)

// ── ExposedPorts ──────────────────────────────────────────────────────────────
//
// Wire: {"8080/tcp":{},"8443/tcp":{},...} — keys carry all the information,
// values are always the meaningless empty object {}. Modeled as []Port (not
// map[Port]struct{}) for idiomatic consumer ergonomics — codex.EntrySlice
// merges each key with its (discarded) value into just the Port.

// emptyObjectCodec decodes/encodes exactly {} — codex.Struct with zero
// fields requires the wire value to be an object but declares no fields, so
// it always decodes to struct{}{} and always encodes to {}.
var emptyObjectCodec = c.Struct[struct{}]()

var ExposedPortsCodec = c.EntrySlice(
	PortCodec, emptyObjectCodec,
	func(p Port, _ struct{}) Port { return p },
	func(p Port) (Port, struct{}) { return p, struct{}{} },
)

// ── PortBindings ──────────────────────────────────────────────────────────────
//
// Wire: {"8080/tcp":[{"HostPort":"8090"}], ...} — an array of binding-entry
// objects per port key (Docker allows binding one container port to
// multiple host ports/interfaces). PortBindingEntry is kept as its own named
// struct (not flattened to []string) so adding HostIp later is additive,
// not a breaking shape change.

type PortBindingEntry struct {
	HostPort string
}

var PortBindingEntryCodec = c.Struct[PortBindingEntry](
	c.RequiredField("HostPort", PortNumberCodec,
		func(e PortBindingEntry) string { return e.HostPort },
		func(e *PortBindingEntry, val string) { e.HostPort = val },
	),
)

type PortBinding struct {
	Port     Port
	Bindings []PortBindingEntry
}

var PortBindingCodec = c.EntrySlice(
	PortCodec, c.SliceOf(PortBindingEntryCodec),
	func(p Port, entries []PortBindingEntry) PortBinding {
		return PortBinding{Port: p, Bindings: entries}
	},
	func(pb PortBinding) (Port, []PortBindingEntry) {
		return pb.Port, pb.Bindings
	},
)

// ── Binds ─────────────────────────────────────────────────────────────────────
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

var bindStructCodec = c.Struct[Bind](
	c.RequiredField("hostPath", c.String().Refine(v.NonEmptyString),
		func(b Bind) string { return b.HostPath },
		func(b *Bind, val string) { b.HostPath = val },
	),
	c.RequiredField("containerPath", c.String().Refine(v.NonEmptyString),
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

// ── HostConfig / CreateOptions ────────────────────────────────────────────────

type HostConfig struct {
	Binds        []Bind
	PortBindings []PortBinding
}

var HostConfigCodec = c.Struct[HostConfig](
	c.RequiredField("Binds", c.SliceOf(BindCodec),
		func(h HostConfig) []Bind { return h.Binds },
		func(h *HostConfig, val []Bind) { h.Binds = val },
	),
	c.RequiredField("PortBindings", PortBindingCodec,
		func(h HostConfig) []PortBinding { return h.PortBindings },
		func(h *HostConfig, val []PortBinding) { h.PortBindings = val },
	),
)

// CreateOptions models the subset of Docker's container create-options
// document used by IoT-Edge module deployment manifests: exposed ports and
// host-level bind mounts / port bindings. Field names use PascalCase JSON
// keys ("ExposedPorts", "HostConfig", "Binds", "PortBindings", "HostPort")
// to match Docker's own wire contract literally — this is Docker's API
// convention, not a stylistic choice (unlike this example's OWN top-level
// fields, e.g. ModuleSettings' "image"/"createOptions", which use camelCase).
type CreateOptions struct {
	ExposedPorts []Port
	HostConfig   HostConfig
}

var CreateOptionsCodec = c.Struct[CreateOptions](
	c.RequiredField("ExposedPorts", ExposedPortsCodec,
		func(co CreateOptions) []Port { return co.ExposedPorts },
		func(co *CreateOptions, val []Port) { co.ExposedPorts = val },
	),
	c.RequiredField("HostConfig", HostConfigCodec,
		func(co CreateOptions) HostConfig { return co.HostConfig },
		func(co *CreateOptions, val HostConfig) { co.HostConfig = val },
	),
)
