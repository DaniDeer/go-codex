package validate

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// PositiveUint is a Constraint that requires uint > 0.
var PositiveUint = codex.Constraint[uint]{
	Name:    "positive",
	Check:   func(v uint) bool { return v > 0 },
	Message: func(v uint) string { return fmt.Sprintf("expected positive integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Minimum = float64ptr(0)
		s.ExclusiveMinimum = true
		return s
	},
}

// MinUint returns a Constraint that requires uint >= n.
func MinUint(n uint) codex.Constraint[uint] {
	return codex.Constraint[uint]{
		Name:    fmt.Sprintf("min(%d)", n),
		Check:   func(v uint) bool { return v >= n },
		Message: func(v uint) string { return fmt.Sprintf("expected integer >= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(n))
			return s
		},
	}
}

// MaxUint returns a Constraint that requires uint <= n.
func MaxUint(n uint) codex.Constraint[uint] {
	return codex.Constraint[uint]{
		Name:    fmt.Sprintf("max(%d)", n),
		Check:   func(v uint) bool { return v <= n },
		Message: func(v uint) string { return fmt.Sprintf("expected integer <= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Maximum = float64ptr(float64(n))
			return s
		},
	}
}

// RangeUint returns a Constraint that requires min <= uint <= max.
func RangeUint(min, max uint) codex.Constraint[uint] {
	return codex.Constraint[uint]{
		Name:    fmt.Sprintf("range(%d,%d)", min, max),
		Check:   func(v uint) bool { return v >= min && v <= max },
		Message: func(v uint) string { return fmt.Sprintf("expected integer in [%d, %d], got %d", min, max, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(min))
			s.Maximum = float64ptr(float64(max))
			return s
		},
	}
}

// PositiveUint64 is a Constraint that requires uint64 > 0.
var PositiveUint64 = codex.Constraint[uint64]{
	Name:    "positive",
	Check:   func(v uint64) bool { return v > 0 },
	Message: func(v uint64) string { return fmt.Sprintf("expected positive integer, got %d", v) },
	Schema: func(s schema.Schema) schema.Schema {
		s.Minimum = float64ptr(0)
		s.ExclusiveMinimum = true
		return s
	},
}

// MinUint64 returns a Constraint that requires uint64 >= n.
func MinUint64(n uint64) codex.Constraint[uint64] {
	return codex.Constraint[uint64]{
		Name:    fmt.Sprintf("min(%d)", n),
		Check:   func(v uint64) bool { return v >= n },
		Message: func(v uint64) string { return fmt.Sprintf("expected integer >= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(n))
			return s
		},
	}
}

// MaxUint64 returns a Constraint that requires uint64 <= n.
func MaxUint64(n uint64) codex.Constraint[uint64] {
	return codex.Constraint[uint64]{
		Name:    fmt.Sprintf("max(%d)", n),
		Check:   func(v uint64) bool { return v <= n },
		Message: func(v uint64) string { return fmt.Sprintf("expected integer <= %d, got %d", n, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Maximum = float64ptr(float64(n))
			return s
		},
	}
}

// RangeUint64 returns a Constraint that requires min <= uint64 <= max.
func RangeUint64(min, max uint64) codex.Constraint[uint64] {
	return codex.Constraint[uint64]{
		Name:    fmt.Sprintf("range(%d,%d)", min, max),
		Check:   func(v uint64) bool { return v >= min && v <= max },
		Message: func(v uint64) string { return fmt.Sprintf("expected integer in [%d, %d], got %d", min, max, v) },
		Schema: func(s schema.Schema) schema.Schema {
			s.Minimum = float64ptr(float64(min))
			s.Maximum = float64ptr(float64(max))
			return s
		},
	}
}
