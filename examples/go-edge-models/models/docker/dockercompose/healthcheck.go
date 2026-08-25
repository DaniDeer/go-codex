package dockercompose

import (
	"time"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

// ── Healthcheck ───────────────────────────────────────────────────────────────
//
// Wire: `healthcheck: {test: ["CMD","curl","-f","http://localhost/"],
// interval: "30s", timeout: "5s", start_period: "10s", retries: 3}` —
// Compose's OWN duration-STRING convention for the timing fields
// (IDENTICAL to `docker run --health-interval=30s`'s own flag syntax —
// see [docker.CLIDurationCodec]), plus a `disable: true` shorthand
// Compose adds (equivalent to Docker's own `test: ["NONE"]` sentinel,
// but expressed as its own boolean field on the wire) that
// [docker.Healthcheck] itself has no field for.

// ComposeHealthcheck mirrors [docker.Healthcheck]'s field set (same Go
// shape once decoded) plus Disable — Compose's own explicit
// "suppress an image-inherited healthcheck" flag, distinct from simply
// omitting the `healthcheck:` key entirely (which leaves Test/Interval/
// etc. at their zero values, meaning "say nothing about healthchecking,"
// not "explicitly disable it").
type ComposeHealthcheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	StartPeriod time.Duration
	Retries     int
	Disable     bool
}

// ComposeHealthcheckCodec reuses [docker.CLIDurationCodec] directly for
// Interval/Timeout/StartPeriod — the SAME duration-string wire
// convention Compose and `docker run --health-*` both use — rather than
// re-deriving it.
var ComposeHealthcheckCodec = c.Struct[ComposeHealthcheck](
	c.OptionalField("test", c.SliceOf(c.String()),
		func(h ComposeHealthcheck) []string { return h.Test },
		func(h *ComposeHealthcheck, val []string) { h.Test = val },
	),
	c.OptionalField("interval", docker.CLIDurationCodec,
		func(h ComposeHealthcheck) time.Duration { return h.Interval },
		func(h *ComposeHealthcheck, val time.Duration) { h.Interval = val },
	),
	c.OptionalField("timeout", docker.CLIDurationCodec,
		func(h ComposeHealthcheck) time.Duration { return h.Timeout },
		func(h *ComposeHealthcheck, val time.Duration) { h.Timeout = val },
	),
	c.OptionalField("start_period", docker.CLIDurationCodec,
		func(h ComposeHealthcheck) time.Duration { return h.StartPeriod },
		func(h *ComposeHealthcheck, val time.Duration) { h.StartPeriod = val },
	),
	c.OptionalField("retries", c.Int(),
		func(h ComposeHealthcheck) int { return h.Retries },
		func(h *ComposeHealthcheck, val int) { h.Retries = val },
	),
	c.OptionalField("disable", c.Bool(),
		func(h ComposeHealthcheck) bool { return h.Disable },
		func(h *ComposeHealthcheck, val bool) { h.Disable = val },
	),
)

// HealthcheckFromComposeCodec decodes/encodes Compose's `healthcheck:`
// wire shape DIRECTLY as a [docker.Healthcheck] — built via
// codex.MapCodecValidated wrapping [ComposeHealthcheckCodec] (the
// intermediate Compose-wire-shape type, kept because
// docker.Healthcheck itself has no field for Compose's `disable: true`
// shorthand). Disable=true maps to Docker's own documented
// Test:["NONE"] sentinel (see docker.Healthcheck.Test's doc comment) on
// decode, and the reverse on encode — the SAME special-casing that
// previously lived inline in Service.CreateOptionsFor/
// ServiceFromCreateOptions, now expressed once as a codec.
var HealthcheckFromComposeCodec = c.MapCodecValidated(
	ComposeHealthcheckCodec, docker.HealthcheckCodec,
	func(h ComposeHealthcheck) (docker.Healthcheck, error) {
		if h.Disable {
			return docker.Healthcheck{Test: []string{"NONE"}}, nil
		}
		return docker.Healthcheck{
			Test:        h.Test,
			Interval:    h.Interval,
			Timeout:     h.Timeout,
			StartPeriod: h.StartPeriod,
			Retries:     h.Retries,
		}, nil
	},
	func(h docker.Healthcheck) (ComposeHealthcheck, error) {
		if len(h.Test) == 1 && h.Test[0] == "NONE" {
			return ComposeHealthcheck{Disable: true}, nil
		}
		return ComposeHealthcheck{
			Test:        h.Test,
			Interval:    h.Interval,
			Timeout:     h.Timeout,
			StartPeriod: h.StartPeriod,
			Retries:     h.Retries,
		}, nil
	},
)
