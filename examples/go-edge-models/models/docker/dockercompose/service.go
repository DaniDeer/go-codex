package dockercompose

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

// ── Service ───────────────────────────────────────────────────────────────────

// Service is one Compose service's declared configuration — the subset
// of the real Compose service schema this package models (see this
// package's own doc comment for the full "what IS/is NOT modeled"
// lists).
type Service struct {
	// Image is the service's container image reference, e.g.
	// "ghcr.io/org/repo:1.2.3" — EMPTY when the service declares
	// `build:` instead (see Build/HasImage). Kept as a plain string
	// here (not parsed into docker.Image) since "no image present" is
	// a valid WIRE state this package must represent — parsing/error
	// handling for that case is the caller's job (see the sibling
	// fromcompose package's placeholder-image policy).
	Image string
	// Build holds the service's `build:` block, if declared — either
	// its string short-form (`build: ./dir`) or object long-form
	// (`build: {context: ./dir, dockerfile: Dockerfile, args: [...],
	// target: prod}`), decoded via [BuildCodec] into a real [Build]
	// value (context/dockerfile/args/target), not just a presence
	// flag. Build.IsSet() reports whether a `build:` key was declared
	// at all — see [Build]'s own doc comment for the zero-value
	// convention.
	Build Build
	// Ports holds the service's `ports:` short-syntax entries, PARSED
	// directly into [docker.PortMapping] via [docker.PortMappingCodec]
	// (a Codec[string] wrapping docker.ParsePortMapping/
	// FormatPortMapping) — a malformed entry now fails ServiceCodec's
	// whole Decode, matching every other Service field's already-strict
	// behavior (Healthcheck/Ulimits/mem_limit all hard-fail on a
	// malformed entry today too; Ports was the one historical
	// exception, since removed).
	Ports []docker.PortMapping
	// Volumes holds the service's `volumes:` short-syntax bind mounts —
	// Compose's own "host:container[:mode]" string is IDENTICAL to
	// docker.Bind's own wire format, so this package reuses
	// docker.BindCodec directly (see ServiceCodec below) rather than
	// re-deriving a parser.
	Volumes []docker.Bind
	// Environment holds the service's `environment:` LIST-FORM entries
	// ("KEY=VALUE") — IDENTICAL to docker.Env's own wire format, reused
	// directly. MAP form (`{KEY: VALUE}`) is NOT supported in this
	// version (see this package's own doc comment) — decoding a
	// map-form `environment:` produces a clear type-mismatch error for
	// this field.
	Environment docker.Env
	// Command is the service's `command:` LIST form (string form is not
	// supported — see this package's own doc comment).
	Command []string
	// Entrypoint is the service's `entrypoint:` LIST form (string form
	// is not supported).
	Entrypoint []string
	Hostname   string
	Domainname string
	// Restart holds the RAW `restart:` string (e.g. "no", "always",
	// "on-failure", "on-failure:3", "unless-stopped") — mapping this to
	// an IoT Edge restart-policy enum is the sibling fromcompose
	// package's job, not this package's (the enums don't match 1:1, and
	// this package must stay orchestrator-agnostic).
	Restart string
	// Healthcheck holds the service's `healthcheck:` block, decoded
	// DIRECTLY into a [docker.Healthcheck] via
	// [HealthcheckFromComposeCodec] — Compose's `disable: true`
	// shorthand is already resolved into Docker's own Test:["NONE"]
	// sentinel by the time this field is populated (see
	// HealthcheckFromComposeCodec's doc comment). The ZERO VALUE (no
	// key declared, OR an empty `healthcheck: {}`) means "say nothing
	// about healthchecking."
	Healthcheck docker.Healthcheck
	// MemLimit is `mem_limit`'s parsed byte count — 0 = unset (the SAME
	// sentinel docker.HostConfig.Memory itself already uses; see
	// docker.MemBytesCodec for the "512m"/"1g"-style string parsing).
	MemLimit int64
	// MemReservation is `mem_reservation`'s parsed byte count — 0 =
	// unset (same convention as MemLimit).
	MemReservation int64
	// Ulimits holds the service's `ulimits:` entries, decoded DIRECTLY
	// into []docker.Ulimit via [UlimitsCodec] (codex.EntrySlice over
	// Compose's own map-keyed shorthand — see ComposeUlimit/
	// ComposeUlimitCodec for the per-entry bare-int-or-object wire
	// shape) — this also means Compose ulimit names are now validated
	// against Docker's real `--ulimit` allow-list (see
	// docker.UlimitNameCodec), which the previous bare
	// map[string]ComposeUlimit never checked.
	Ulimits []docker.Ulimit
}

// ── Build ─────────────────────────────────────────────────────────────────────

// Build models a Compose service's `build:` block — either the SHORT
// string form (`build: ./app` — the WHOLE string IS Context, every
// other field left at its zero value) or the LONG object form
// (`build: {context: ..., dockerfile: ..., args: [...], target: ...}`).
//
// The ZERO VALUE (Context == "") means "no build: key declared at all"
// — Compose itself always requires a non-empty context whenever
// `build:` IS present (even the long form's own "context:" field is
// required), so Context's emptiness is a safe, unambiguous "absent"
// sentinel — the SAME "zero value means unset" convention
// Service.MemLimit/MemReservation/Healthcheck already use. Use
// [Build.IsSet] to check this rather than comparing against Build{}
// directly (clearer intent, mirrors [Service.HasImage]'s own naming).
type Build struct {
	// Context is the build context path or URL — the WHOLE value for
	// the short string form; the explicit "context:" field for the
	// long object form.
	Context string
	// Dockerfile is the Dockerfile path relative to Context — empty
	// means Compose's own default ("Dockerfile" at the context root).
	Dockerfile string
	// Args holds build-time arguments — reuses [docker.Env]'s SAME
	// "KEY=VALUE" list-form wire convention [Service.Environment]
	// already uses (Compose's `build.args` supports both list and map
	// forms on the real wire; this package models list form only,
	// mirroring Service.Environment's own documented list-only
	// convention).
	Args docker.Env
	// Target selects a specific stage in a multi-stage Dockerfile —
	// empty means Compose's own default (the LAST stage).
	Target string
}

// IsSet reports whether b represents an actually-declared `build:` key
// — see Build's own doc comment for the Context-emptiness sentinel.
func (b Build) IsSet() bool {
	return b.Context != ""
}

// buildObjectCodec decodes/encodes ONLY the LONG object form of
// `build:` — the fields a real Compose `build:` object supports, per
// this package's own "commonly needed subset" scope (see this
// package's own doc comment). [BuildCodec] is the PUBLIC codec that
// additionally accepts the short string form; use that, not this
// unexported helper, for anything touching a real `build:` wire value.
var buildObjectCodec = c.Struct[Build](
	c.RequiredField("context", c.String(),
		func(b Build) string { return b.Context },
		func(b *Build, val string) { b.Context = val },
	),
	c.OptionalField("dockerfile", c.String(),
		func(b Build) string { return b.Dockerfile },
		func(b *Build, val string) { b.Dockerfile = val },
	),
	c.OptionalField("args", docker.EnvCodec,
		func(b Build) docker.Env { return b.Args },
		func(b *Build, val docker.Env) { b.Args = val },
	),
	c.OptionalField("target", c.String(),
		func(b Build) string { return b.Target },
		func(b *Build, val string) { b.Target = val },
	),
)

// BuildCodec decodes/encodes Compose's `build:` key in EITHER of its
// two real wire shapes: a bare STRING (short form — the whole value
// becomes [Build.Context], every other field left zero) or an OBJECT
// (long form — delegates to [buildObjectCodec]). A `nil`/absent wire
// value decodes to the zero Build{} ("no build: key" — see [Build]'s
// own doc comment).
//
// Unlike the package's PRIOR presence-only representation, BuildCodec
// does NOT tolerate a bare boolean (`build: true`/`build: false`) —
// real Compose syntax never uses one; that tolerance was an artifact
// of the old boolean-only [Service.Build] field, not a real Compose
// wire shape, so it is intentionally dropped now that Build carries
// real content.
//
// Encode always produces the LONG object form, even for a Build
// assembled from a short-form decode — Compose accepts the long form
// unconditionally, so this is a safe, information-preserving choice;
// the distinction between "this came from short vs. long form on the
// wire" is not retained (this package makes no attempt to preserve the
// ORIGINAL wire SHAPE, only the wire VALUE — the same trade-off the
// prior buildPresenceCodec already made for the boolean case). A Build
// that [Build.IsSet] reports false (the zero value — no `build:` key
// at all) encodes to `nil`, the standard "this key had nothing to say"
// wire value in this package's convention (mirrors how a decode of
// `nil` produces Build{} in the first place).
var BuildCodec = c.Codec[Build]{
	Decode: func(v any) (Build, error) {
		if v == nil {
			return Build{}, nil
		}
		if s, ok := v.(string); ok {
			return Build{Context: s}, nil
		}
		return buildObjectCodec.Decode(v)
	},
	Encode: func(b Build) (any, error) {
		if !b.IsSet() {
			return nil, nil
		}
		return buildObjectCodec.Encode(b)
	},
	Schema: buildObjectCodec.Schema,
}

// serviceFieldsCodec is the UNCONSTRAINED, field-only assembly every
// Service field is built from — every field OMITTED from Encode
// whenever it's at its zero (absent) value, matching Compose's own
// "everything but image/build is optional" convention AND avoiding the
// noisy `build: null`/`command: []`/`domainname: ""` wire output a plain
// OptionalField would always emit (see codex.OmitEmptyField's own doc
// comment for the "only when zero already means absent by this field's
// own convention" hard rule — every field below already documents that
// exact convention). Deliberately UNEXPORTED and kept SEPARATE from
// the public, cross-field-validated [ServiceCodec] below: it backs
// [CreateOptionsFromServiceCodec], whose OWN documented contract is a
// PARTIAL Service (Image/Build/Restart intentionally left unset — see
// [CreateOptionsFromServiceCodec]'s own doc comment) — a codec that can
// never satisfy "image-or-build-required" by construction (a
// docker.CreateOptions value carries no image/build data at all), so it
// must not be built on the constrained, whole-document codec.
var serviceFieldsCodec = c.Struct[Service](
	c.OmitEmptyField("image", c.String(),
		func(s Service) string { return s.Image },
		func(s *Service, val string) { s.Image = val },
	),
	c.OmitEmptyFieldFunc("build", BuildCodec,
		func(s Service) Build { return s.Build },
		func(s *Service, val Build) { s.Build = val },
		func(v Build) bool { return !v.IsSet() },
	),
	c.OmitEmptyFieldFunc("ports", c.SliceOf(docker.PortMappingCodec),
		func(s Service) []docker.PortMapping { return s.Ports },
		func(s *Service, val []docker.PortMapping) { s.Ports = val },
		func(v []docker.PortMapping) bool { return v == nil },
	),
	c.OmitEmptyFieldFunc("volumes", c.SliceOf(docker.BindCodec),
		func(s Service) []docker.Bind { return s.Volumes },
		func(s *Service, val []docker.Bind) { s.Volumes = val },
		func(v []docker.Bind) bool { return v == nil },
	),
	c.OmitEmptyFieldFunc("environment", docker.EnvCodec,
		func(s Service) docker.Env { return s.Environment },
		func(s *Service, val docker.Env) { s.Environment = val },
		func(v docker.Env) bool { return v == nil },
	),
	c.OmitEmptyFieldFunc("command", c.SliceOf(c.String()),
		func(s Service) []string { return s.Command },
		func(s *Service, val []string) { s.Command = val },
		func(v []string) bool { return v == nil },
	),
	c.OmitEmptyFieldFunc("entrypoint", c.SliceOf(c.String()),
		func(s Service) []string { return s.Entrypoint },
		func(s *Service, val []string) { s.Entrypoint = val },
		func(v []string) bool { return v == nil },
	),
	c.OmitEmptyField("hostname", c.String(),
		func(s Service) string { return s.Hostname },
		func(s *Service, val string) { s.Hostname = val },
	),
	c.OmitEmptyField("domainname", c.String(),
		func(s Service) string { return s.Domainname },
		func(s *Service, val string) { s.Domainname = val },
	),
	c.OmitEmptyField("restart", c.String(),
		func(s Service) string { return s.Restart },
		func(s *Service, val string) { s.Restart = val },
	),
	c.OmitEmptyFieldFunc("healthcheck", HealthcheckFromComposeCodec,
		func(s Service) docker.Healthcheck { return s.Healthcheck },
		func(s *Service, val docker.Healthcheck) { s.Healthcheck = val },
		c.IsZeroValue,
	),
	c.OmitEmptyField("mem_limit", docker.MemBytesCodec,
		func(s Service) int64 { return s.MemLimit },
		func(s *Service, val int64) { s.MemLimit = val },
	),
	c.OmitEmptyField("mem_reservation", docker.MemBytesCodec,
		func(s Service) int64 { return s.MemReservation },
		func(s *Service, val int64) { s.MemReservation = val },
	),
	c.OmitEmptyFieldFunc("ulimits", UlimitsCodec,
		func(s Service) []docker.Ulimit { return s.Ulimits },
		func(s *Service, val []docker.Ulimit) { s.Ulimits = val },
		func(v []docker.Ulimit) bool { return v == nil },
	),
)

// ServiceCodec is the PUBLIC, whole-document Compose service codec —
// [serviceFieldsCodec] plus cross-field invariants real Compose files
// must satisfy. Use this (not serviceFieldsCodec) for anything parsing
// or producing a genuine Compose service document — [ServicesCodec]/
// [ProjectCodec] are built on it, so a malformed real docker-compose.yml
// (a service declaring neither "image" nor "build", or an inverted
// mem_reservation/mem_limit pair) is now rejected at decode time rather
// than silently accepted.
var ServiceCodec = serviceFieldsCodec.Refine(
	// image-or-build-required: a Compose model invariant in its own
	// right, independent of any downstream consumer (e.g. the sibling
	// fromcompose package) — the Compose spec itself requires at least
	// ONE of "image"/"build" (both together is fine; "build" alone
	// resolves to a placeholder image downstream — see HasImage's own
	// doc comment — but neither is a genuinely malformed service, not a
	// permissive "leave it to the caller" case).
	c.Constraint[Service]{
		Name:  "image-or-build-required",
		Check: func(s Service) bool { return s.Image != "" || s.Build.IsSet() },
		Message: func(Service) string {
			return `service must declare either "image" or "build" (or both)`
		},
	},
	// mem-reservation-not-exceeding-mem-limit: Docker/Compose's own
	// documented rule — mem_reservation (a SOFT limit) must be smaller
	// than mem_limit (the HARD limit) whenever both are set. Either
	// alone (the other left at its zero/"unset" sentinel) is always
	// fine — see MemLimit/MemReservation's own doc comments for the
	// zero-means-unset convention.
	c.Constraint[Service]{
		Name: "mem-reservation-not-exceeding-mem-limit",
		Check: func(s Service) bool {
			return s.MemLimit == 0 || s.MemReservation == 0 || s.MemReservation <= s.MemLimit
		},
		Message: func(s Service) string {
			return fmt.Sprintf("mem_reservation (%d bytes) must not exceed mem_limit (%d bytes)", s.MemReservation, s.MemLimit)
		},
	},
)

// HasImage reports whether svc has a usable image reference — false
// means the service declared `build:` instead. A Service with NEITHER
// image NOR build is no longer representable at all: ServiceCodec's own
// "image-or-build-required" Refine constraint rejects it at decode/
// encode/validate time (a genuine Compose spec violation, not a
// permissive "leave it to the caller" case) — see ServiceCodec's own
// doc comment. The sibling fromcompose package's placeholder-image
// substitution remains for the "build: true, no image" case
// specifically (svc.Build.IsSet(), HasImage() == false), which stays
// valid and unaffected by this constraint.
func (svc Service) HasImage() bool {
	return svc.Image != ""
}

// groupPortMappings groups already-validated []docker.PortMapping (see
// [docker.PortMappingCodec]) into docker.CreateOptions' own
// ExposedPorts/PortBindings shape — a container port with NO host
// binding contributes only to ExposedPorts; one WITH a host binding
// contributes to both, and multiple entries for the SAME container port
// (Docker allows binding one container port to multiple host
// ports/interfaces) are grouped under one docker.PortBinding. Pure,
// infallible grouping — every docker.PortMapping in the input is
// already known-valid by construction (decoded via ServiceCodec).
func groupPortMappings(mappings []docker.PortMapping) ([]docker.Port, []docker.PortBinding) {
	var exposedPorts []docker.Port
	seen := make(map[docker.Port]bool, len(mappings))
	bindingsByPort := make(map[docker.Port][]docker.PortBindingEntry, len(mappings))
	for _, pm := range mappings {
		if !seen[pm.Port] {
			seen[pm.Port] = true
			exposedPorts = append(exposedPorts, pm.Port)
		}
		if pm.HostPort != "" {
			bindingsByPort[pm.Port] = append(bindingsByPort[pm.Port], docker.PortBindingEntry{HostPort: pm.HostPort})
		}
	}
	var portBindings []docker.PortBinding
	for _, port := range exposedPorts {
		if entries := bindingsByPort[port]; len(entries) > 0 {
			portBindings = append(portBindings, docker.PortBinding{Port: port, Bindings: entries})
		}
	}
	return exposedPorts, portBindings
}

// ungroupPortMappings is the reverse of [groupPortMappings]: expands
// docker.CreateOptions' ExposedPorts/PortBindings shape back into flat
// []docker.PortMapping — a port with MULTIPLE host bindings produces
// one PortMapping PER BINDING (Compose's own short syntax only
// expresses one binding per line, but Docker itself allows more).
func ungroupPortMappings(exposedPorts []docker.Port, portBindings []docker.PortBinding) []docker.PortMapping {
	bindingsByPort := make(map[docker.Port][]docker.PortBindingEntry, len(portBindings))
	for _, pb := range portBindings {
		bindingsByPort[pb.Port] = pb.Bindings
	}
	var mappings []docker.PortMapping
	for _, port := range exposedPorts {
		entries := bindingsByPort[port]
		if len(entries) == 0 {
			mappings = append(mappings, docker.PortMapping{Port: port})
			continue
		}
		for _, entry := range entries {
			mappings = append(mappings, docker.PortMapping{Port: port, HostPort: entry.HostPort})
		}
	}
	return mappings
}

// serviceToCreateOptions is [CreateOptionsFromServiceCodec]'s "to"
// (Service -> docker.CreateOptions) direction — now a PURE field-copy
// plus port-grouping, since Ports/Healthcheck/Ulimits are already
// codec-native Go values by the time ServiceCodec decodes a Service
// (no parsing, formatting, or warning-collection left to do here).
//
// NOTE: MemReservation ("mem_reservation" — a SOFT memory limit) has NO
// corresponding field in docker.HostConfig (which only models Memory —
// a hard limit — and MemorySwap — the total memory+swap limit, a
// DIFFERENT concept entirely). Mapping MemReservation onto MemorySwap
// would be semantically WRONG, so it is deliberately dropped here
// rather than mis-mapped — svc.MemReservation itself stays available on
// the Service value for any caller that needs it directly, even though
// CreateOptions doesn't surface it.
func serviceToCreateOptions(svc Service) (docker.CreateOptions, error) {
	exposedPorts, portBindings := groupPortMappings(svc.Ports)
	return docker.CreateOptions{
		Cmd:          svc.Command,
		Entrypoint:   svc.Entrypoint,
		Hostname:     svc.Hostname,
		Domainname:   svc.Domainname,
		ExposedPorts: exposedPorts,
		HostConfig: docker.HostConfig{
			Binds:        svc.Volumes,
			PortBindings: portBindings,
			Memory:       svc.MemLimit,
			Ulimits:      svc.Ulimits,
		},
		Healthcheck: svc.Healthcheck,
		Env:         svc.Environment,
	}, nil
}

// createOptionsToService is [CreateOptionsFromServiceCodec]'s "from"
// (docker.CreateOptions -> Service) direction — the returned Service has
// Image/Build/Restart left at their zero values (CreateOptions carries
// neither) — a caller assembling a full Service populates those
// separately.
func createOptionsToService(co docker.CreateOptions) (Service, error) {
	return Service{
		Ports:       ungroupPortMappings(co.ExposedPorts, co.HostConfig.PortBindings),
		Volumes:     co.HostConfig.Binds,
		Environment: co.Env,
		Command:     co.Cmd,
		Entrypoint:  co.Entrypoint,
		Hostname:    co.Hostname,
		Domainname:  co.Domainname,
		Healthcheck: co.Healthcheck,
		MemLimit:    co.HostConfig.Memory,
		// MemReservation stays zero — see serviceToCreateOptions' own
		// doc comment for why docker.HostConfig has no field to
		// reconstruct it from; this is a PERMANENT, documented one-way
		// loss.
		Ulimits: co.HostConfig.Ulimits,
	}, nil
}

// CreateOptionsFromServiceCodec is a Codec[docker.CreateOptions] backed
// by [serviceFieldsCodec]'s OWN Compose Service wire shape (the
// UNCONSTRAINED variant, deliberately NOT [ServiceCodec] — see
// serviceFieldsCodec's own doc comment for why: this bridge's Service
// side is always PARTIAL, missing Image/Build/Restart by design, so it
// can never satisfy ServiceCodec's "image-or-build-required" invariant)
// — built via codex.MapCodecValidated, this is the direct
// "Docker <-> docker-compose" mapping: encode a docker.CreateOptions
// value and get back a Compose service-shaped wire document (minus
// Image/Build/Restart, which CreateOptions has no equivalent for);
// decode a Compose service-shaped wire document and get back a
// docker.CreateOptions value directly, with zero intermediate Service
// value ever exposed to the caller. [Service.CreateOptionsFor]/
// [ServiceFromCreateOptions] are thin, ergonomic Service-typed wrappers
// around the SAME to/from functions.
var CreateOptionsFromServiceCodec = c.MapCodecValidated(
	serviceFieldsCodec, docker.CreateOptionsCodec,
	serviceToCreateOptions, createOptionsToService,
)

// CreateOptionsFor assembles a fully-formed docker.CreateOptions purely
// from svc's own fields — PURE "Compose -> Docker" mapping requiring
// ZERO orchestrator (IoT Edge, Kubernetes, ...) vocabulary whatsoever.
// A thin, infallible wrapper around [CreateOptionsFromServiceCodec]'s
// own "to" direction (svc is already fully validated by the time a
// caller holds a Service value, so this cannot fail).
func (svc Service) CreateOptionsFor() docker.CreateOptions {
	co, _ := serviceToCreateOptions(svc)
	return co
}

// ServiceFromCreateOptions is the REVERSE of [Service.CreateOptionsFor]:
// reconstructs a Service's `ports`/`volumes`/`environment`/`command`/
// `entrypoint`/`hostname`/`domainname`/`healthcheck`/`mem_limit` fields
// PURELY from a docker.CreateOptions value — no orchestrator (IoT Edge,
// Kubernetes, ...) vocabulary whatsoever. A thin, infallible wrapper
// around [CreateOptionsFromServiceCodec]'s own "from" direction. The
// returned Service has Image/Build/Restart left at their zero values
// (CreateOptions carries neither) — a caller assembling a full Service
// populates those separately.
func ServiceFromCreateOptions(co docker.CreateOptions) Service {
	svc, _ := createOptionsToService(co)
	return svc
}
