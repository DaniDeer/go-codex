package docker

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

// ── Binds ─────────────────────────────────────────────────────────────────────
//
// Wire: ["/etc/ssl/local/pubkey.pem:/etc/traefik/ssl/pubkey.pem", ...] —
// "host:container[:mode]" strings. Parsed into a structured Bind for
// field-level access/validation.

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

// ── Ulimits ───────────────────────────────────────────────────────────────────
//
// Wire: "Ulimits":[{"Name":"nofile","Soft":1024,"Hard":2048}, ...] — an array
// of resource-limit entries.

// UlimitNameCodec wraps a plain string with ulimitNameConstraint — named and
// exported so a caller assembling their own ulimit-related codec (e.g. a
// validation-only helper, independent of the full Ulimit struct) can reuse
// the exact same name constraint.
var UlimitNameCodec = c.String().Refine(ulimitNameConstraint)

var UlimitCodec = c.Struct[Ulimit](
	c.RequiredField("Name", UlimitNameCodec,
		func(u Ulimit) string { return u.Name },
		func(u *Ulimit, val string) { u.Name = val },
	),
	c.RequiredField("Soft", c.Int64(),
		func(u Ulimit) int64 { return u.Soft },
		func(u *Ulimit, val int64) { u.Soft = val },
	),
	c.RequiredField("Hard", c.Int64(),
		func(u Ulimit) int64 { return u.Hard },
		func(u *Ulimit, val int64) { u.Hard = val },
	),
)

// ── Healthcheck ───────────────────────────────────────────────────────────────
//
// Wire: "Healthcheck":{"Test":["CMD","curl","-f","http://localhost/"],
// "Interval":30000000000,"Timeout":5000000000,"StartPeriod":10000000000,
// "StartInterval":5000000000,"Retries":3}.

// dockerNanosDurationCodec represents Docker's raw-nanosecond-integer wire
// format for Healthcheck's timing fields as an ergonomic time.Duration in Go
// (e.g. 30*time.Second). NOT codex.Duration() — that codec expects a
// duration STRING ("30s") via time.ParseDuration, but Docker's Engine API
// instead uses a bare integer count of nanoseconds (e.g. 30000000000 for
// 30s). Built via MapCodecSafe wrapping codex.Int64(): to (int64→
// time.Duration) is a simple type conversion (always succeeds); from
// (time.Duration→int64) likewise.
var dockerNanosDurationCodec = c.MapCodecSafe(
	c.Int64(),
	func(ns int64) time.Duration { return time.Duration(ns) },
	func(d time.Duration) (int64, error) { return int64(d), nil },
)

var HealthcheckCodec = c.Struct[Healthcheck](
	c.OptionalField("Test", c.SliceOf(c.String()),
		func(h Healthcheck) []string { return h.Test },
		func(h *Healthcheck, val []string) { h.Test = val },
	),
	c.OptionalField("Interval", dockerNanosDurationCodec,
		func(h Healthcheck) time.Duration { return h.Interval },
		func(h *Healthcheck, val time.Duration) { h.Interval = val },
	),
	c.OptionalField("Timeout", dockerNanosDurationCodec,
		func(h Healthcheck) time.Duration { return h.Timeout },
		func(h *Healthcheck, val time.Duration) { h.Timeout = val },
	),
	c.OptionalField("StartPeriod", dockerNanosDurationCodec,
		func(h Healthcheck) time.Duration { return h.StartPeriod },
		func(h *Healthcheck, val time.Duration) { h.StartPeriod = val },
	),
	c.OptionalField("StartInterval", dockerNanosDurationCodec,
		func(h Healthcheck) time.Duration { return h.StartInterval },
		func(h *Healthcheck, val time.Duration) { h.StartInterval = val },
	),
	c.OptionalField("Retries", c.Int(),
		func(h Healthcheck) int { return h.Retries },
		func(h *Healthcheck, val int) { h.Retries = val },
	),
)

// isZeroHealthcheck reports whether h has no meaningful content — used by
// IsZeroCreateOptions below.
func isZeroHealthcheck(h Healthcheck) bool {
	return len(h.Test) == 0 && h.Interval == 0 && h.Timeout == 0 &&
		h.StartPeriod == 0 && h.StartInterval == 0 && h.Retries == 0
}

// ── HostConfig / CreateOptions ────────────────────────────────────────────────

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
		isZeroHealthcheck(co.Healthcheck)
}
