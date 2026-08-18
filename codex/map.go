package codex

import (
	"fmt"
	"strings"
)

// MapCodecSafe creates a new Codec[B] from Codec[A] using two mapping functions.
// from is the encode direction and must always succeed.
// to is the decode direction and may fail.
func MapCodecSafe[A, B any](
	c Codec[A],
	to func(A) B,
	from func(B) (A, error),
) Codec[B] {
	return Codec[B]{
		Encode: func(v B) (any, error) {
			a, err := from(v)
			if err != nil {
				return nil, err
			}
			return c.Encode(a)
		},
		Decode: func(v any) (B, error) {
			a, err := c.Decode(v)
			if err != nil {
				var zero B
				return zero, err
			}
			return to(a), nil
		},
		Schema: c.Schema,
	}
}

// MapCodecValidated creates a Codec[B] from Codec[A] and Codec[B] using two fallible mapping functions.
//
// Both directions may return an error. After mapping to B in the decode direction,
// cb.Validate is called to enforce all Refine constraints defined on cb.
// The resulting codec carries cb's schema.
//
// Use MapCodecValidated when the mapping itself can fail and the target type B has
// its own validation constraints expressed via Refine. For a simpler case where only
// the encode direction can fail and no post-mapping validation is needed, use MapCodecSafe.
func MapCodecValidated[A, B any](
	ca Codec[A],
	cb Codec[B],
	to func(A) (B, error),
	from func(B) (A, error),
) Codec[B] {
	return Codec[B]{
		Schema: cb.Schema,
		Decode: func(v any) (B, error) {
			var zero B
			a, err := ca.Decode(v)
			if err != nil {
				return zero, err
			}
			b, err := to(a)
			if err != nil {
				return zero, err
			}
			if err := cb.Validate(b); err != nil {
				return zero, err
			}
			return b, nil
		},
		Encode: func(v B) (any, error) {
			if err := cb.Validate(v); err != nil {
				return nil, err
			}
			a, err := from(v)
			if err != nil {
				return nil, err
			}
			return ca.Encode(a)
		},
	}
}

// PrefixedKeyCodec builds a Codec[B] for a dotted/namespaced wire key shaped
// "prefix" + name — e.g. "properties.desired.modules.cv-writer" — where B is
// the (possibly named, e.g. `type ModuleName string`) type wrapping the
// extracted name segment. nameConstraint validates ONLY the name segment
// (after prefix is stripped); the full key's own "has prefix, non-empty
// suffix" shape is validated internally and does not need to be expressed by
// the caller.
//
// Decode: full key → strip prefix → validate name segment via
// nameConstraint → B.
// Encode: B → validate name segment via nameConstraint → prepend prefix →
// full key string.
//
// This is a convenience constructor, not new validation behavior — it
// generalizes the two-layer "wire codec validates the full dotted key;
// domain codec validates the extracted name segment" recipe already
// hand-rolled ad hoc across the codebase for this exact shape (e.g.
// examples/flat-key-patch's containerKeyCodec, or
// examples/go-edge-models's manifesttemplate.ModuleNameCodec/RouteNameCodec).
func PrefixedKeyCodec[B ~string](prefix string, nameConstraint Constraint[string]) Codec[B] {
	fullKeyConstraint := Constraint[string]{
		Name: "prefixed-key",
		Check: func(s string) bool {
			return strings.HasPrefix(s, prefix) && len(strings.TrimPrefix(s, prefix)) > 0
		},
		Message: func(s string) string {
			return fmt.Sprintf("key %q must start with %q followed by a non-empty name", s, prefix)
		},
	}
	nameCodec := MapCodecSafe(
		String().Refine(nameConstraint),
		func(s string) B { return B(s) },
		func(b B) (string, error) { return string(b), nil },
	)
	return MapCodecValidated(
		String().Refine(fullKeyConstraint),
		nameCodec,
		func(fullKey string) (B, error) {
			return B(strings.TrimPrefix(fullKey, prefix)), nil
		},
		func(n B) (string, error) {
			return prefix + string(n), nil
		},
	)
}

// Downcast attempts to cast a value of type B to type A.
// Useful for tagged unions where variants share a common interface.
func Downcast[A any, B any](v B) (A, error) {
	a, ok := any(v).(A)
	if !ok {
		var zero A
		return zero, fmt.Errorf("cannot cast %T", v)
	}
	return a, nil
}
