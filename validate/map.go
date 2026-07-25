package validate

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// MinProperties returns a Constraint that requires a map of at least n entries.
// Compose with [codex.Map]/[codex.StringMap]:
//
//	tagsCodec := codex.StringMap(codex.String()).Refine(validate.MinProperties[string, string](1))
func MinProperties[K comparable, V any](n int) codex.Constraint[map[K]V] {
	return codex.Constraint[map[K]V]{
		Name:  fmt.Sprintf("minProperties(%d)", n),
		Check: func(v map[K]V) bool { return len(v) >= n },
		Message: func(v map[K]V) string {
			return fmt.Sprintf("expected at least %d entries, got %d", n, len(v))
		},
		Schema: func(s schema.Schema) schema.Schema {
			s.MinProperties = intptr(n)
			return s
		},
	}
}

// MaxProperties returns a Constraint that requires a map of at most n entries.
func MaxProperties[K comparable, V any](n int) codex.Constraint[map[K]V] {
	return codex.Constraint[map[K]V]{
		Name:  fmt.Sprintf("maxProperties(%d)", n),
		Check: func(v map[K]V) bool { return len(v) <= n },
		Message: func(v map[K]V) string {
			return fmt.Sprintf("expected at most %d entries, got %d", n, len(v))
		},
		Schema: func(s schema.Schema) schema.Schema {
			s.MaxProperties = intptr(n)
			return s
		},
	}
}

// NonEmptyMap returns a Constraint that requires a non-empty map.
// Equivalent to MinProperties[K, V](1), with a schema-appropriate name/message
// — mirrors [NonEmptyString]/[NonEmptySlice] for the map case. Like
// NonEmptySlice, this is a function, not a package-level var: Go has no
// generic package-level vars, so both type parameters must be supplied at
// the call site:
//
//	tagsCodec := codex.StringMap(codex.String()).Refine(validate.NonEmptyMap[string, string]())
func NonEmptyMap[K comparable, V any]() codex.Constraint[map[K]V] {
	return codex.Constraint[map[K]V]{
		Name:    "non-empty",
		Check:   func(v map[K]V) bool { return len(v) > 0 },
		Message: func(v map[K]V) string { return "expected non-empty map" },
		Schema: func(s schema.Schema) schema.Schema {
			s.MinProperties = intptr(1)
			return s
		},
	}
}
