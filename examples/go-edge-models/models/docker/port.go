package docker

import (
	"fmt"
	"strconv"
	"strings"

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

// ── CLI short-syntax port mapping ────────────────────────────────────────────
//
// `docker run -p 80`/`-p 8080:80`/`-p 8080:80/udp` — Docker's OWN CLI
// flag syntax for exposing/binding one container port at a time (Docker
// Compose's `ports:` service key short-syntax entries reuse this
// IDENTICAL string convention), distinct from the create-options
// document's own ExposedPorts/PortBindings JSON shape above (which
// [ParsePortMapping] produces one piece of).

// PortMappingError is returned by [ParsePortMapping] when raw doesn't
// match a supported "-p" short-syntax form.
type PortMappingError struct {
	Raw    string
	Reason string
}

func (e PortMappingError) Error() string {
	return fmt.Sprintf("invalid port mapping %q: %s", e.Raw, e.Reason)
}

// ParsePortMapping parses ONE `docker run -p` short-syntax entry into its
// container [Port] (always returned) and, if a host port was specified,
// a [PortBindingEntry] (nil when raw declares a container port only, with
// no host-side binding — e.g. bare "80"). Supported forms:
//
//   - "80"          -> Port("80/tcp"), nil
//   - "8080:80"     -> Port("80/tcp"), &PortBindingEntry{HostPort: "8080"}
//   - "8080:80/udp" -> Port("80/udp"), &PortBindingEntry{HostPort: "8080"}
//
// Interface/IP-prefixed forms (e.g. "127.0.0.1:8080:80") are NOT
// supported and return a [PortMappingError] — callers that need
// best-effort, partial-success handling across many entries (e.g. the
// sibling dockercompose package) should collect this error per-entry
// rather than treating it as fatal for an entire document.
func ParsePortMapping(raw string) (Port, *PortBindingEntry, error) {
	if raw == "" {
		return "", nil, PortMappingError{Raw: raw, Reason: "empty string"}
	}

	proto := "tcp"
	rest := raw
	if i := strings.LastIndex(raw, "/"); i != -1 {
		proto = raw[i+1:]
		rest = raw[:i]
		if proto != "tcp" && proto != "udp" {
			return "", nil, PortMappingError{Raw: raw, Reason: fmt.Sprintf("unrecognized protocol %q", proto)}
		}
	}

	parts := strings.Split(rest, ":")
	switch len(parts) {
	case 1:
		containerPort := parts[0]
		if err := validatePortNumber(containerPort); err != nil {
			return "", nil, PortMappingError{Raw: raw, Reason: err.Error()}
		}
		return Port(containerPort + "/" + proto), nil, nil
	case 2:
		hostPort, containerPort := parts[0], parts[1]
		if err := validatePortNumber(hostPort); err != nil {
			return "", nil, PortMappingError{Raw: raw, Reason: "invalid host port: " + err.Error()}
		}
		if err := validatePortNumber(containerPort); err != nil {
			return "", nil, PortMappingError{Raw: raw, Reason: "invalid container port: " + err.Error()}
		}
		return Port(containerPort + "/" + proto), &PortBindingEntry{HostPort: hostPort}, nil
	default:
		// 3+ colon-separated parts — an interface/IP-prefixed form (e.g.
		// "127.0.0.1:8080:80") — not supported in this version.
		return "", nil, PortMappingError{Raw: raw, Reason: "interface/IP-prefixed port mappings are not supported"}
	}
}

// validatePortNumber checks that s is a plain decimal port number in the
// valid 1-65535 range — shared by both the host-port and container-port
// halves of [ParsePortMapping].
func validatePortNumber(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("not a valid port number: %q", s)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("port number out of range (1-65535): %d", n)
	}
	return nil
}

// PortMapping is ONE parsed `docker run -p`/Compose `ports:` short-syntax
// entry: a container [Port] plus an OPTIONAL host-side port (""= no
// host binding, container-only — the same case [ParsePortMapping]
// returns a nil *[PortBindingEntry] for).
type PortMapping struct {
	Port     Port
	HostPort string
}

// portMappingStructCodec validates PortMapping's OWN Go-level shape — the
// "port" field reuses [PortCodec] (defense-in-depth re-validation of the
// SAME "<n>/tcp"|"<n>/udp" shape [ParsePortMapping]/[FormatPortMapping]
// already enforce); "hostPort" is a plain optional string (empty = no
// host binding). Required as [PortMappingCodec]'s "cb" side so
// codex.MapCodecValidated has something to call Validate against — not
// itself used for wire IO (the wire shape is a plain string, handled by
// [PortMappingCodec]'s "ca" side).
var portMappingStructCodec = c.Struct[PortMapping](
	c.RequiredField("port", PortCodec,
		func(m PortMapping) Port { return m.Port },
		func(m *PortMapping, val Port) { m.Port = val },
	),
	c.OptionalField("hostPort", c.String(),
		func(m PortMapping) string { return m.HostPort },
		func(m *PortMapping, val string) { m.HostPort = val },
	),
)

// PortMappingCodec decodes/encodes a `docker run -p`/Compose `ports:`
// short-syntax STRING directly as a [PortMapping] value — built via
// codex.MapCodecValidated wrapping the EXISTING, already-tested
// [ParsePortMapping]/[FormatPortMapping] functions (single source of
// truth for the actual parse/format logic; this codec just gives it a
// Codec[PortMapping] shape so callers like dockercompose.Service can
// use it directly as a field codec, e.g. via
// codex.SliceOf(docker.PortMappingCodec), instead of hand-rolling their
// own parse/format loop over a raw []string).
var PortMappingCodec = c.MapCodecValidated(
	c.String(), portMappingStructCodec,
	func(raw string) (PortMapping, error) {
		port, binding, err := ParsePortMapping(raw)
		if err != nil {
			return PortMapping{}, err
		}
		hostPort := ""
		if binding != nil {
			hostPort = binding.HostPort
		}
		return PortMapping{Port: port, HostPort: hostPort}, nil
	},
	func(m PortMapping) (string, error) {
		return FormatPortMapping(m.Port, m.HostPort)
	},
)

// FormatPortMapping is the reverse of [ParsePortMapping]: reconstructs a
// `docker run -p`/Compose `ports:` short-syntax string from a container
// [Port] and an OPTIONAL host port (empty string = container port only,
// no host-side binding — the same "no [PortBindingEntry]" case
// [ParsePortMapping] returns nil for). Supported round trips:
//
//   - Port("80/tcp"), ""      -> "80"
//   - Port("80/tcp"), "8080"  -> "8080:80"
//   - Port("80/udp"), "8080"  -> "8080:80/udp"
//
// The "/tcp" protocol suffix is OMITTED on output (the canonical short
// form — both Docker and Compose already treat tcp as the implicit
// default), so `FormatPortMapping(port, hostPort)` does not byte-for-byte
// round-trip an ORIGINAL raw string that explicitly spelled out
// "/tcp" — it reconstructs the same PORT MAPPING, not the same string.
// Returns a [PortMappingError] if port is not a valid "<n>/tcp"|"<n>/udp"
// string (e.g. constructed by hand rather than via [ParsePortMapping]).
func FormatPortMapping(port Port, hostPort string) (string, error) {
	s := string(port)
	i := strings.LastIndex(s, "/")
	if i == -1 {
		return "", PortMappingError{Raw: s, Reason: "missing protocol suffix"}
	}
	containerPort, proto := s[:i], s[i+1:]
	if err := validatePortNumber(containerPort); err != nil {
		return "", PortMappingError{Raw: s, Reason: "invalid container port: " + err.Error()}
	}
	if proto != "tcp" && proto != "udp" {
		return "", PortMappingError{Raw: s, Reason: fmt.Sprintf("unrecognized protocol %q", proto)}
	}

	suffix := ""
	if proto == "udp" {
		suffix = "/udp"
	}

	if hostPort == "" {
		return containerPort + suffix, nil
	}
	if err := validatePortNumber(hostPort); err != nil {
		return "", PortMappingError{Raw: s, Reason: "invalid host port: " + err.Error()}
	}
	return hostPort + ":" + containerPort + suffix, nil
}
