package docker

import (
	"time"

	c "github.com/DaniDeer/go-codex/codex"
)

// ── Healthcheck ───────────────────────────────────────────────────────────────
//
// Wire: "Healthcheck":{"Test":["CMD","curl","-f","http://localhost/"],
// "Interval":30000000000,"Timeout":5000000000,"StartPeriod":10000000000,
// "StartInterval":5000000000,"Retries":3}.

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

// healthcheckFields declares Healthcheck's full field set, parameterized by
// ONE duration codec — the only thing that actually differs between
// [HealthcheckCodec]'s raw-nanosecond-integer wire form and
// [HealthcheckCLICodec]'s duration-string wire form. Single source of
// truth for the field NAMES and getter/setter closures; only the wire
// encoding of the duration fields themselves varies per caller.
func healthcheckFields(durationCodec c.Codec[time.Duration]) []c.FieldCodec[Healthcheck] {
	return []c.FieldCodec[Healthcheck]{
		c.OptionalField("Test", c.SliceOf(c.String()),
			func(h Healthcheck) []string { return h.Test },
			func(h *Healthcheck, val []string) { h.Test = val },
		),
		c.OptionalField("Interval", durationCodec,
			func(h Healthcheck) time.Duration { return h.Interval },
			func(h *Healthcheck, val time.Duration) { h.Interval = val },
		),
		c.OptionalField("Timeout", durationCodec,
			func(h Healthcheck) time.Duration { return h.Timeout },
			func(h *Healthcheck, val time.Duration) { h.Timeout = val },
		),
		c.OptionalField("StartPeriod", durationCodec,
			func(h Healthcheck) time.Duration { return h.StartPeriod },
			func(h *Healthcheck, val time.Duration) { h.StartPeriod = val },
		),
		c.OptionalField("StartInterval", durationCodec,
			func(h Healthcheck) time.Duration { return h.StartInterval },
			func(h *Healthcheck, val time.Duration) { h.StartInterval = val },
		),
		c.OptionalField("Retries", c.Int(),
			func(h Healthcheck) int { return h.Retries },
			func(h *Healthcheck, val int) { h.Retries = val },
		),
	}
}

var HealthcheckCodec = c.Struct[Healthcheck](healthcheckFields(dockerNanosDurationCodec)...)

// isZeroHealthcheck reports whether h has no meaningful content — used by
// IsZeroCreateOptions (hostconfig.go).
func isZeroHealthcheck(h Healthcheck) bool {
	return len(h.Test) == 0 && h.Interval == 0 && h.Timeout == 0 &&
		h.StartPeriod == 0 && h.StartInterval == 0 && h.Retries == 0
}

// ── CLI-style duration wire form ────────────────────────────────────────────
//
// `docker run --health-interval=30s`/`--health-timeout=5s`/
// `--health-start-period=10s` use ordinary Go-style duration STRINGS
// ("30s", "1m30s") — a genuinely different wire convention from the
// create-options JSON document's raw-nanosecond-integer fields above
// (dockerNanosDurationCodec), even though both describe the SAME
// Healthcheck struct fields. This is the Docker CLI's own flag syntax
// (Docker Compose's `healthcheck.interval`/`timeout`/`start_period`
// service keys reuse the IDENTICAL string-duration convention), so it
// belongs here, not in any Compose-specific model.

// CLIDurationCodec re-exports core codex.Duration() (which already
// implements exactly this "Go duration string" convention) under a
// docker-package name — exported so a caller assembling a CLI-flavored
// Healthcheck codec (e.g. the sibling dockercompose package, which reuses
// this per-field codec directly rather than re-deriving it) doesn't need
// to reach into core codex for it.
var CLIDurationCodec = c.Duration()

// HealthcheckCLICodec decodes/encodes a Healthcheck using
// [CLIDurationCodec] for Interval/Timeout/StartPeriod/StartInterval — the
// SAME Go Healthcheck struct HealthcheckCodec already handles via
// Docker's raw-nanosecond-integer wire form, just with this alternate,
// CLI/Compose-style duration-STRING wire shape instead. Built from the
// SAME [healthcheckFields] declaration as HealthcheckCodec — only the
// duration codec argument differs.
var HealthcheckCLICodec = c.Struct[Healthcheck](healthcheckFields(CLIDurationCodec)...)
