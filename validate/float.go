package validate

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// PositiveFloat is a Constraint that requires float64 > 0.
var PositiveFloat = codex.Constraint[float64]{
	Name:    "positive",
	Check:   func(v float64) bool { return v > 0 },
	Message: func(v float64) string { return fmt.Sprintf("expected positive number, got %g", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Minimum = float64ptr(0)
		s.ExclusiveMinimum = true
		return s
	},
}

// NegativeFloat is a Constraint that requires float64 < 0.
var NegativeFloat = codex.Constraint[float64]{
	Name:    "negative",
	Check:   func(v float64) bool { return v < 0 },
	Message: func(v float64) string { return fmt.Sprintf("expected negative number, got %g", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Maximum = float64ptr(0)
		s.ExclusiveMaximum = true
		return s
	},
}

// NonZeroFloat is a Constraint that requires float64 != 0.
var NonZeroFloat = codex.Constraint[float64]{
	Name:    "nonzero",
	Check:   func(v float64) bool { return v != 0 },
	Message: func(v float64) string { return "expected non-zero number, got 0" },
}

// MinFloat returns a Constraint that requires float64 >= n.
func MinFloat(n float64) codex.Constraint[float64] {
	return codex.Constraint[float64]{
		Name:    fmt.Sprintf("min(%g)", n),
		Check:   func(v float64) bool { return v >= n },
		Message: func(v float64) string { return fmt.Sprintf("expected number >= %g, got %g", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(n)
			return s
		},
	}
}

// MaxFloat returns a Constraint that requires float64 <= n.
func MaxFloat(n float64) codex.Constraint[float64] {
	return codex.Constraint[float64]{
		Name:    fmt.Sprintf("max(%g)", n),
		Check:   func(v float64) bool { return v <= n },
		Message: func(v float64) string { return fmt.Sprintf("expected number <= %g, got %g", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Maximum = float64ptr(n)
			return s
		},
	}
}

// RangeFloat returns a Constraint that requires min <= float64 <= max.
func RangeFloat(min, max float64) codex.Constraint[float64] {
	return codex.Constraint[float64]{
		Name:    fmt.Sprintf("range(%g,%g)", min, max),
		Check:   func(v float64) bool { return v >= min && v <= max },
		Message: func(v float64) string { return fmt.Sprintf("expected number in [%g, %g], got %g", min, max, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(min)
			s.Maximum = float64ptr(max)
			return s
		},
	}
}
