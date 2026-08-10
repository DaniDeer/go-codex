package docker

import (
	"strconv"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Ports ─────────────────────────────────────────────────────────────────────
//
// Wire: {"8080/tcp":{},"8443/tcp":{},...} — keys carry all the information,
// values are always the meaningless empty object {}. ExposedPorts is
// modeled as []Port (not map[Port]struct{}) for idiomatic consumer
// ergonomics — codex.EntrySlice merges each key with its (discarded) value
// into just the Port.

// Port is a Docker port-spec string, "<port>/tcp" or "<port>/udp" (e.g.
// "8080/tcp") — the key shape shared by ExposedPorts and
// HostConfig.PortBindings.
type Port string

// PortBindingEntry is one host-side binding target for a container port.
type PortBindingEntry struct {
	HostPort string
}

// PortBinding pairs a container Port with its host-side binding entries —
// Docker allows binding one container port to multiple host ports/interfaces.
type PortBinding struct {
	Port     Port
	Bindings []PortBindingEntry
}

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

// emptyObjectCodec decodes/encodes exactly {} — codex.Struct with zero
// fields requires the wire value to be an object but declares no fields, so
// it always decodes to struct{}{} and always encodes to {}.
var emptyObjectCodec = c.Struct[struct{}]()

// ExposedPortsCodec merges each "<port>/tcp" key with its discarded {}
// value into a flat []Port via codex.EntrySlice.
var ExposedPortsCodec = c.EntrySlice(
	PortCodec, emptyObjectCodec,
	func(p Port, _ struct{}) Port { return p },
	func(p Port) (Port, struct{}) { return p, struct{}{} },
)

// ── PortBindings ──────────────────────────────────────────────────────────────
//
// Wire: {"8080/tcp":[{"HostPort":"8090"}], ...} — an array of binding-entry
// objects per port key. PortBindingEntry is kept as its own named struct
// (not flattened to []string) so adding e.g. HostIp later is additive, not
// a breaking shape change.

var PortBindingEntryCodec = c.Struct[PortBindingEntry](
	c.RequiredField("HostPort", PortNumberCodec,
		func(e PortBindingEntry) string { return e.HostPort },
		func(e *PortBindingEntry, val string) { e.HostPort = val },
	),
)

// PortBindingCodec merges each "<port>/tcp" key with its binding-entry
// array into a flat []PortBinding via codex.EntrySlice.
var PortBindingCodec = c.EntrySlice(
	PortCodec, c.SliceOf(PortBindingEntryCodec),
	func(p Port, entries []PortBindingEntry) PortBinding {
		return PortBinding{Port: p, Bindings: entries}
	},
	func(pb PortBinding) (Port, []PortBindingEntry) {
		return pb.Port, pb.Bindings
	},
)
