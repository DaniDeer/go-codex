package validate

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

func float64ptr(v float64) *float64 { return &v }

// PositiveInt is a Constraint that requires int > 0.
var PositiveInt = codex.Constraint[int]{
	Name:    "positive",
	Check:   func(v int) bool { return v > 0 },
	Message: func(v int) string { return fmt.Sprintf("expected positive integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Minimum = float64ptr(0)
		s.ExclusiveMinimum = true
		return s
	},
}

// NegativeInt is a Constraint that requires int < 0.
var NegativeInt = codex.Constraint[int]{
	Name:    "negative",
	Check:   func(v int) bool { return v < 0 },
	Message: func(v int) string { return fmt.Sprintf("expected negative integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Maximum = float64ptr(0)
		s.ExclusiveMaximum = true
		return s
	},
}

// MinInt returns a Constraint that requires int >= n.
func MinInt(n int) codex.Constraint[int] {
	return codex.Constraint[int]{
		Name:    fmt.Sprintf("min(%d)", n),
		Check:   func(v int) bool { return v >= n },
		Message: func(v int) string { return fmt.Sprintf("expected integer >= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(n))
			return s
		},
	}
}

// MaxInt returns a Constraint that requires int <= n.
func MaxInt(n int) codex.Constraint[int] {
	return codex.Constraint[int]{
		Name:    fmt.Sprintf("max(%d)", n),
		Check:   func(v int) bool { return v <= n },
		Message: func(v int) string { return fmt.Sprintf("expected integer <= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Maximum = float64ptr(float64(n))
			return s
		},
	}
}

// RangeInt returns a Constraint that requires min <= int <= max.
func RangeInt(min, max int) codex.Constraint[int] {
	return codex.Constraint[int]{
		Name:    fmt.Sprintf("range(%d,%d)", min, max),
		Check:   func(v int) bool { return v >= min && v <= max },
		Message: func(v int) string { return fmt.Sprintf("expected integer in [%d, %d], got %d", min, max, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(min))
			s.Maximum = float64ptr(float64(max))
			return s
		},
	}
}

// NonZeroInt is a Constraint that requires int != 0.
var NonZeroInt = codex.Constraint[int]{
	Name:    "nonzero",
	Check:   func(v int) bool { return v != 0 },
	Message: func(_ int) string { return "expected non-zero integer, got 0" },
}

// PositiveInt32 is a Constraint that requires int32 > 0.
var PositiveInt32 = codex.Constraint[int32]{
	Name:    "positive",
	Check:   func(v int32) bool { return v > 0 },
	Message: func(v int32) string { return fmt.Sprintf("expected positive integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Minimum = float64ptr(0)
		s.ExclusiveMinimum = true
		return s
	},
}

// NegativeInt32 is a Constraint that requires int32 < 0.
var NegativeInt32 = codex.Constraint[int32]{
	Name:    "negative",
	Check:   func(v int32) bool { return v < 0 },
	Message: func(v int32) string { return fmt.Sprintf("expected negative integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Maximum = float64ptr(0)
		s.ExclusiveMaximum = true
		return s
	},
}

// MinInt32 returns a Constraint that requires int32 >= n.
func MinInt32(n int32) codex.Constraint[int32] {
	return codex.Constraint[int32]{
		Name:    fmt.Sprintf("min(%d)", n),
		Check:   func(v int32) bool { return v >= n },
		Message: func(v int32) string { return fmt.Sprintf("expected integer >= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(n))
			return s
		},
	}
}

// MaxInt32 returns a Constraint that requires int32 <= n.
func MaxInt32(n int32) codex.Constraint[int32] {
	return codex.Constraint[int32]{
		Name:    fmt.Sprintf("max(%d)", n),
		Check:   func(v int32) bool { return v <= n },
		Message: func(v int32) string { return fmt.Sprintf("expected integer <= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Maximum = float64ptr(float64(n))
			return s
		},
	}
}

// RangeInt32 returns a Constraint that requires min <= int32 <= max.
func RangeInt32(min, max int32) codex.Constraint[int32] {
	return codex.Constraint[int32]{
		Name:    fmt.Sprintf("range(%d,%d)", min, max),
		Check:   func(v int32) bool { return v >= min && v <= max },
		Message: func(v int32) string { return fmt.Sprintf("expected integer in [%d, %d], got %d", min, max, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(min))
			s.Maximum = float64ptr(float64(max))
			return s
		},
	}
}

// PositiveInt64 is a Constraint that requires int64 > 0.
var PositiveInt64 = codex.Constraint[int64]{
	Name:    "positive",
	Check:   func(v int64) bool { return v > 0 },
	Message: func(v int64) string { return fmt.Sprintf("expected positive integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Minimum = float64ptr(0)
		s.ExclusiveMinimum = true
		return s
	},
}

// NegativeInt64 is a Constraint that requires int64 < 0.
var NegativeInt64 = codex.Constraint[int64]{
	Name:    "negative",
	Check:   func(v int64) bool { return v < 0 },
	Message: func(v int64) string { return fmt.Sprintf("expected negative integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Maximum = float64ptr(0)
		s.ExclusiveMaximum = true
		return s
	},
}

// MinInt64 returns a Constraint that requires int64 >= n.
func MinInt64(n int64) codex.Constraint[int64] {
	return codex.Constraint[int64]{
		Name:    fmt.Sprintf("min(%d)", n),
		Check:   func(v int64) bool { return v >= n },
		Message: func(v int64) string { return fmt.Sprintf("expected integer >= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(n))
			return s
		},
	}
}

// MaxInt64 returns a Constraint that requires int64 <= n.
func MaxInt64(n int64) codex.Constraint[int64] {
	return codex.Constraint[int64]{
		Name:    fmt.Sprintf("max(%d)", n),
		Check:   func(v int64) bool { return v <= n },
		Message: func(v int64) string { return fmt.Sprintf("expected integer <= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Maximum = float64ptr(float64(n))
			return s
		},
	}
}

// RangeInt64 returns a Constraint that requires min <= int64 <= max.
func RangeInt64(min, max int64) codex.Constraint[int64] {
	return codex.Constraint[int64]{
		Name:    fmt.Sprintf("range(%d,%d)", min, max),
		Check:   func(v int64) bool { return v >= min && v <= max },
		Message: func(v int64) string { return fmt.Sprintf("expected integer in [%d, %d], got %d", min, max, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(min))
			s.Maximum = float64ptr(float64(max))
			return s
		},
	}
}
