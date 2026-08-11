package docker

import (
	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Ulimits ───────────────────────────────────────────────────────────────────
//
// Wire: "Ulimits":[{"Name":"nofile","Soft":1024,"Hard":2048}, ...] — an array
// of resource-limit entries.

// Ulimit is a single Docker resource-limit entry (soft/hard limits for one
// named Linux resource, e.g. the number of open file descriptors).
type Ulimit struct {
	Name string
	Soft int64
	Hard int64
}

// ulimitNameConstraint validates a Ulimit's Name against Docker's full
// documented `--ulimit` name list (the standard Linux RLIMIT_* resource
// names, in Docker's lowercase convention) — Docker itself rejects any
// other name at the daemon level, so this mirrors real, enforced behavior
// rather than being an arbitrarily narrow choice.
var ulimitNameConstraint = v.OneOf(
	"as", "core", "cpu", "data", "fsize", "locks", "memlock", "msgqueue",
	"nice", "nofile", "nproc", "rss", "rtprio", "rttime", "sigpending", "stack",
)

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

// Codec implements [codex.HasCodec][Ulimit], returning [UlimitCodec].
func (Ulimit) Codec() c.Codec[Ulimit] { return UlimitCodec }

// NewUlimit is a named per-field smart constructor: validates name against
// Docker's real `--ulimit` name allow-list via UlimitCodec.New and returns
// the constructed Ulimit, or the zero value and the first failing
// constraint's error.
func NewUlimit(name string, soft, hard int64) (Ulimit, error) {
	return UlimitCodec.New(Ulimit{Name: name, Soft: soft, Hard: hard})
}
