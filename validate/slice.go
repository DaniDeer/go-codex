package validate

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

// MinItems returns a Constraint that requires a slice of at least n elements.
// Compose with [codex.SliceOf]:
//
//	itemsCodec := codex.SliceOf(lineItemCodec).Refine(validate.MinItems[LineItem](1))
func MinItems[T any](n int) codex.Constraint[[]T] {
	return codex.Constraint[[]T]{
		Name:  fmt.Sprintf("minItems(%d)", n),
		Check: func(v []T) bool { return len(v) >= n },
		Message: func(v []T) string {
			return fmt.Sprintf("expected at least %d item(s), got %d", n, len(v))
		},
		Schema: func(s schema.Schema) schema.Schema {
			s.MinItems = intptr(n)
			return s
		},
	}
}

// MaxItems returns a Constraint that requires a slice of at most n elements.
func MaxItems[T any](n int) codex.Constraint[[]T] {
	return codex.Constraint[[]T]{
		Name:  fmt.Sprintf("maxItems(%d)", n),
		Check: func(v []T) bool { return len(v) <= n },
		Message: func(v []T) string {
			return fmt.Sprintf("expected at most %d item(s), got %d", n, len(v))
		},
		Schema: func(s schema.Schema) schema.Schema {
			s.MaxItems = intptr(n)
			return s
		},
	}
}

// NonEmptySlice returns a Constraint that requires a non-empty slice.
// Equivalent to MinItems[T](1), with a schema-appropriate name/message —
// mirrors [NonEmptyString] for the array case. Unlike NonEmptyString this
// is a function, not a package-level var: Go has no generic package-level
// vars, so the type parameter must be supplied at the call site:
//
//	itemsCodec := codex.SliceOf(lineItemCodec).Refine(validate.NonEmptySlice[LineItem]())
func NonEmptySlice[T any]() codex.Constraint[[]T] {
	return codex.Constraint[[]T]{
		Name:    "non-empty",
		Check:   func(v []T) bool { return len(v) > 0 },
		Message: func(v []T) string { return "expected non-empty array" },
		Schema: func(s schema.Schema) schema.Schema {
			s.MinItems = intptr(1)
			return s
		},
	}
}

// UniqueItems returns a Constraint that requires every element of a slice to
// be distinct, checked via Go equality (==) — T must be [comparable]. This
// excludes element types containing slices, maps, or funcs; for those,
// write a custom [codex.Constraint] using reflect.DeepEqual or a
// domain-specific key extractor instead.
//
// Uses a map[T]struct{} for O(n) duplicate detection.
func UniqueItems[T comparable]() codex.Constraint[[]T] {
	return codex.Constraint[[]T]{
		Name: "uniqueItems",
		Check: func(v []T) bool {
			seen := make(map[T]struct{}, len(v))
			for _, item := range v {
				if _, ok := seen[item]; ok {
					return false
				}
				seen[item] = struct{}{}
			}
			return true
		},
		Message: func(v []T) string { return "expected all items to be unique" },
		Schema: func(s schema.Schema) schema.Schema {
			s.UniqueItems = true
			return s
		},
	}
}
