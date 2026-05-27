package validate

import (
	"fmt"
	"time"

	"github.com/DaniDeer/go-codex/codex"
)

// PositiveDuration is a Constraint that requires time.Duration > 0.
var PositiveDuration = codex.Constraint[time.Duration]{
	Name:    "positive",
	Check:   func(v time.Duration) bool { return v > 0 },
	Message: func(v time.Duration) string { return fmt.Sprintf("expected positive duration, got %s", v) },
}

// NonNegativeDuration is a Constraint that requires time.Duration >= 0.
var NonNegativeDuration = codex.Constraint[time.Duration]{
	Name:    "nonNegative",
	Check:   func(v time.Duration) bool { return v >= 0 },
	Message: func(v time.Duration) string { return fmt.Sprintf("expected non-negative duration, got %s", v) },
}

// MinDuration returns a Constraint that requires time.Duration >= d.
func MinDuration(d time.Duration) codex.Constraint[time.Duration] {
	return codex.Constraint[time.Duration]{
		Name:    fmt.Sprintf("min(%s)", d),
		Check:   func(v time.Duration) bool { return v >= d },
		Message: func(v time.Duration) string { return fmt.Sprintf("expected duration >= %s, got %s", d, v) },
	}
}

// MaxDuration returns a Constraint that requires time.Duration <= d.
func MaxDuration(d time.Duration) codex.Constraint[time.Duration] {
	return codex.Constraint[time.Duration]{
		Name:    fmt.Sprintf("max(%s)", d),
		Check:   func(v time.Duration) bool { return v <= d },
		Message: func(v time.Duration) string { return fmt.Sprintf("expected duration <= %s, got %s", d, v) },
	}
}
