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
// IsZeroCreateOptions (hostconfig.go).
func isZeroHealthcheck(h Healthcheck) bool {
	return len(h.Test) == 0 && h.Interval == 0 && h.Timeout == 0 &&
		h.StartPeriod == 0 && h.StartInterval == 0 && h.Retries == 0
}
