package docker

import "time"

// ── Ports ─────────────────────────────────────────────────────────────────────

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

// ── Bind mounts ───────────────────────────────────────────────────────────────

// Bind is a parsed Docker bind-mount spec: "<hostPath>:<containerPath>[:<mode>]".
type Bind struct {
	HostPath      string
	ContainerPath string
	// Mode is the optional trailing bind option ("ro", "rw", "z", "Z", ...).
	// Empty string means no mode was specified (Docker's default, read-write).
	Mode string
}

// ── Ulimits ───────────────────────────────────────────────────────────────────

// Ulimit is a single Docker resource-limit entry (soft/hard limits for one
// named Linux resource, e.g. the number of open file descriptors).
type Ulimit struct {
	Name string
	Soft int64
	Hard int64
}

// ── Healthcheck ───────────────────────────────────────────────────────────────

// Healthcheck configures a container's HEALTHCHECK behavior. It is a
// TOP-LEVEL createOptions field (a sibling of HostConfig, not nested in it).
type Healthcheck struct {
	// Test is the health-check command, e.g. ["CMD","curl","-f",...] or
	// ["CMD-SHELL","curl -f ... || exit 1"]. The special single-element form
	// ["NONE"] explicitly disables an image-inherited healthcheck.
	Test          []string
	Interval      time.Duration
	Timeout       time.Duration
	StartPeriod   time.Duration
	StartInterval time.Duration // Docker 25+
	Retries       int
}

// ── HostConfig / CreateOptions ────────────────────────────────────────────────

// HostConfig models the host-level resource constraints and bindings of a
// Docker create-options document.
type HostConfig struct {
	Binds        []Bind
	PortBindings []PortBinding
	// Memory is the memory limit in bytes (0 = unset/unlimited — the same
	// sentinel Docker itself uses, so there is no ambiguity between "not
	// specified" and "explicitly unlimited").
	Memory int64
	// MemorySwap is the total memory+swap limit in bytes (-1 = unlimited,
	// 0 = unset). Docker requires Memory to also be set when MemorySwap is
	// used — not enforced here (this package models the wire shape, not
	// Docker's own daemon-side cross-field validation).
	MemorySwap int64
	Ulimits    []Ulimit
}

// CreateOptions models the subset of Docker's container create-options
// document commonly used by container-orchestration tooling: the
// container's command/entrypoint/hostname, a healthcheck, exposed ports,
// and host-level bind mounts / port bindings / memory limits / ulimits.
// Field names use PascalCase JSON keys ("Cmd", "Entrypoint", "Hostname",
// "Domainname", "Healthcheck", "ExposedPorts", "HostConfig", "Binds",
// "PortBindings", "HostPort", "Memory", "MemorySwap", "Ulimits") to match
// Docker's own wire contract literally — this is Docker's API convention,
// not a stylistic choice.
type CreateOptions struct {
	// Cmd is the default command (overridable at container start).
	Cmd []string
	// Entrypoint is the fixed executable — Docker always documents Cmd and
	// Entrypoint as a pair: Entrypoint sets what always runs, Cmd supplies
	// default/overridable arguments (or the full command, if Entrypoint is
	// unset).
	Entrypoint   []string
	Hostname     string
	Domainname   string
	ExposedPorts []Port
	HostConfig   HostConfig
	Healthcheck  Healthcheck
}
