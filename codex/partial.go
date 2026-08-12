package codex

import (
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
)

// This file implements the "patch an existing struct" primitives:
// PartialField and PartialStruct. See docs/concepts/codec.md's
// "PartialField/PartialStruct: patching an existing struct" subsection
// for the full design rationale.
//
// PartialStruct is the "omit unset fields entirely" counterpart to
// Struct: Struct's own Encode unconditionally writes every declared
// field into its output map (RequiredField and OptionalField alike) —
// there is no built-in "leave this key out when it was never set"
// behavior. PartialStruct exists specifically for that case: a "patch"/
// "partial update" struct whose fields are all independently optional
// pointers, where an unset (nil) field must be OMITTED from the encoded
// object entirely (not encoded as null or a zero value), so that a
// caller applying the encoded result as a merge patch (e.g. via
// ports.PatchEncoded) only touches the fields it actually set.
//
// PartialFieldCodec/PartialField are a PARALLEL interface/constructor to
// FieldCodec/RequiredField|OptionalField|DefaultField — not a
// modification of them. Struct's existing "always write every field"
// semantics stay exactly as they are; PartialStruct is a distinct,
// single-purpose entry point for the "some subset of fields" shape, so a
// reader never has to hold two different presence models in their head
// for one function.
//
// Nesting a PartialStruct-built Codec[F] inside another PartialField
// needs NO special mechanism: PartialField accepts any Codec[F], and
// PartialStruct returns a plain Codec[T] — ordinary Codec composability,
// the same way Struct already nests inside Struct today. Presence for a
// nested field is decided EXACTLY like any other field: is the outer
// pointer nil? The caller only allocates the nested pointer when it
// means to include a change inside it.

// PartialFieldCodec is the sealed interface [PartialStruct] composes —
// the "may be entirely absent from the encoded object" counterpart to
// [FieldCodec]. Implemented by the value [PartialField] returns.
type PartialFieldCodec[T any] interface {
	// encode reports whether this field is present on v — i.e. its
	// backing pointer is non-nil. present == false means: omit this key
	// from the encoded object entirely (not null, not a zero value —
	// absent).
	encode(v T) (name string, val any, present bool, err error)
	// decode sets T's field ONLY when name is present in obj — an absent
	// key leaves the corresponding pointer field nil (unset), exactly
	// mirroring how a caller would construct T by hand.
	decode(obj map[string]any, target *T) error
	// schema returns this field's name and schema — used by
	// PartialStruct to build T's overall Schema. Patch fields are NEVER
	// required (nothing to mark required in a "some subset of fields"
	// shape).
	schema() (string, schema.Schema)
}

// partialField is PartialFieldCodec[T]'s sole implementation, returned
// by [PartialField].
type partialField[T, F any] struct {
	name  string
	codec Codec[F]
	get   func(T) *F
	set   func(*T, *F)
}

//lint:ignore U1000 implements PartialFieldCodec interface
func (f partialField[T, F]) encode(v T) (string, any, bool, error) {
	ptr := f.get(v)
	if ptr == nil {
		return f.name, nil, false, nil
	}
	val, err := f.codec.Encode(*ptr)
	if err != nil {
		return f.name, nil, false, err
	}
	return f.name, val, true, nil
}

//lint:ignore U1000 implements PartialFieldCodec interface
func (f partialField[T, F]) decode(obj map[string]any, target *T) error {
	raw, ok := obj[f.name]
	if !ok {
		return nil
	}
	val, err := f.codec.Decode(raw)
	if err != nil {
		return err
	}
	f.set(target, &val)
	return nil
}

//lint:ignore U1000 implements PartialFieldCodec interface
func (f partialField[T, F]) schema() (string, schema.Schema) {
	return f.name, f.codec.Schema
}

// PartialField declares one patchable field of T — T's own field for
// this name MUST be a pointer (*F): nil means "not set, leave untouched"
// when encoding, non-nil means "set to this value". codec is the SAME
// field-level Codec[F] an existing full-struct declaration for this
// concept already uses (e.g. an existing ImageCodec/StatusCodec) —
// reused completely unchanged, "inheriting" that field's own
// constraints/validation with zero new logic.
//
//	type ProfilePatch struct {
//	    Nickname *string
//	    Age      *int
//	}
//	var patchCodec = codex.PartialStruct[ProfilePatch](
//	    codex.PartialField("nickname", codex.String(),
//	        func(p ProfilePatch) *string { return p.Nickname },
//	        func(p *ProfilePatch, v *string) { p.Nickname = v }),
//	    codex.PartialField("age", codex.Int(),
//	        func(p ProfilePatch) *int { return p.Age },
//	        func(p *ProfilePatch, v *int) { p.Age = v }),
//	)
func PartialField[T, F any](
	name string,
	codec Codec[F],
	get func(T) *F,
	set func(*T, *F),
) PartialFieldCodec[T] {
	return partialField[T, F]{name: name, codec: codec, get: get, set: set}
}

// PartialStruct builds a Codec[T] for a "patch"/"partial update" struct
// — every one of T's fields is independently optional (all pointers).
// Unlike [Struct], Encode OMITS the wire key entirely for any field
// whose pointer is nil (never writes a placeholder/null/zero value for
// an unset field); if every field is nil, Encode returns an empty
// map[string]any (not an error — a caller for whom an entirely-empty
// patch is meaningless should reject it themselves, since only the
// caller's domain knows whether that's a mistake). Decode only assigns
// fields actually present in the input, leaving the rest nil. Schema
// marks NO fields Required.
func PartialStruct[T any](fields ...PartialFieldCodec[T]) Codec[T] {
	var props []schema.Property
	for _, f := range fields {
		name, s := f.schema()
		props = append(props, schema.Property{Name: name, Schema: s})
	}

	return Codec[T]{
		Encode: func(v T) (any, error) {
			obj := map[string]any{}
			var errs ValidationErrors
			for _, f := range fields {
				name, val, present, err := f.encode(v)
				if err != nil {
					errs = append(errs, ValidationError{Field: name, Err: err})
					continue
				}
				if present {
					obj[name] = val
				}
			}
			if len(errs) > 0 {
				return obj, errs
			}
			return obj, nil
		},
		Decode: func(v any) (T, error) {
			var result T
			obj, ok := v.(map[string]any)
			if !ok {
				return result, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
			var errs ValidationErrors
			for _, f := range fields {
				name, _ := f.schema()
				if err := f.decode(obj, &result); err != nil {
					errs = append(errs, ValidationError{Field: name, Err: err})
				}
			}
			if len(errs) > 0 {
				return result, errs
			}
			return result, nil
		},
		Schema: schema.Schema{
			Type:       "object",
			Properties: props,
		},
	}
}
