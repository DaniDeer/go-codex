package dockercompose

import (
	"fmt"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

// ── Ulimits ───────────────────────────────────────────────────────────────────
//
// Wire: `ulimits: {nofile: 1024}` (bare int — soft AND hard both set to
// the SAME value) OR `ulimits: {nofile: {soft: 1024, hard: 2048}}` (an
// explicit {soft, hard} object) — Compose's OWN map-keyed shorthand
// (the map KEY is the resource name, e.g. "nofile"; see service.go's
// Service.Ulimits map[string]ComposeUlimit). This shorthand has NO real
// `docker` CLI equivalent (`docker run --ulimit` is always a flat
// "name=soft:hard" string), so it is modeled HERE, not in the parent
// docker package.

// ComposeUlimit is one resource-limit entry's soft/hard values, after
// normalizing BOTH wire forms (bare int or {soft,hard} object) to the
// same shape.
type ComposeUlimit struct {
	Soft int64
	Hard int64
}

var bareUlimitVariantCodec = c.MapCodecSafe(
	c.Int64(),
	func(n int64) ComposeUlimit { return ComposeUlimit{Soft: n, Hard: n} },
	func(u ComposeUlimit) (int64, error) {
		if u.Soft != u.Hard {
			return 0, fmt.Errorf("soft (%d) and hard (%d) differ, cannot use bare-int form", u.Soft, u.Hard)
		}
		return u.Soft, nil
	},
)

var objectUlimitVariantCodec = c.MapCodecSafe(
	c.Struct[ComposeUlimit](
		c.RequiredField("soft", c.Int64(),
			func(u ComposeUlimit) int64 { return u.Soft },
			func(u *ComposeUlimit, val int64) { u.Soft = val },
		),
		c.RequiredField("hard", c.Int64(),
			func(u ComposeUlimit) int64 { return u.Hard },
			func(u *ComposeUlimit, val int64) { u.Hard = val },
		),
	),
	func(u ComposeUlimit) ComposeUlimit { return u },
	func(u ComposeUlimit) (ComposeUlimit, error) { return u, nil },
)

// ComposeUlimitCodec tries the bare-int form FIRST (Soft==Hard is the
// common case — most compose files that bother setting a ulimit at all
// want the SAME value for both), then the {soft,hard} object form —
// mirrors iothub.EnvVarValueCodec's own UntaggedUnion dispatch pattern
// one package over.
var ComposeUlimitCodec = c.UntaggedUnion[ComposeUlimit](
	func(u ComposeUlimit) int {
		if u.Soft == u.Hard {
			return 0
		}
		return 1
	},
	c.UntaggedVariant[ComposeUlimit]{Name: "bare", Codec: bareUlimitVariantCodec},
	c.UntaggedVariant[ComposeUlimit]{Name: "object", Codec: objectUlimitVariantCodec},
)

// UlimitsCodec decodes/encodes Compose's `ulimits: {name: soft|{soft,hard}}`
// map-wire-shape DIRECTLY as a []docker.Ulimit — built via codex.EntrySlice,
// reusing the EXISTING [docker.UlimitNameCodec] as the key codec. This is
// also a genuine validation improvement over a bare map[string]ComposeUlimit:
// Compose ulimit names are now checked against Docker's real `--ulimit`
// allow-list (the SAME list docker.UlimitCodec itself enforces), which a
// bare string-keyed map never did.
var UlimitsCodec = c.EntrySlice(
	docker.UlimitNameCodec, ComposeUlimitCodec,
	func(name string, u ComposeUlimit) docker.Ulimit {
		return docker.Ulimit{Name: name, Soft: u.Soft, Hard: u.Hard}
	},
	func(u docker.Ulimit) (string, ComposeUlimit) {
		return u.Name, ComposeUlimit{Soft: u.Soft, Hard: u.Hard}
	},
)
