package codex

import (
	"errors"
	"fmt"
	"sort"

	"github.com/DaniDeer/go-codex/schema"
)

// FieldCodec is the sealed interface implemented by [Field] (via
// [RequiredField]/[OptionalField]/[DefaultField]) that [Struct] composes to
// build a full object codec. Its methods are unexported — only this
// package can produce values satisfying it — but the interface NAME is
// exported so other packages can name it in their own signatures (e.g. to
// hold a slice of heterogeneous per-field declarations, as [DecodeVars] and
// [EncodeVars] do, or as api/rest's merge-capable Param constructors do to
// bridge a declared Field into a route's automatic request merge).
type FieldCodec[T any] interface {
	encode(T) (string, any, error)
	decode(map[string]any, *T) error
	schema() (string, schema.Schema, bool)
}

// Field describes a single struct field and its codec.
type Field[T any, F any] struct {
	Name     string
	Codec    Codec[F]
	Get      func(T) F
	Set      func(*T, F)
	Required bool
	// Default holds the field's default value. A non-nil pointer means the field
	// has a declared default; nil means no default. A pointer is used to
	// distinguish "no default" from a zero-value default.
	Default *F
}

//lint:ignore U1000 implements FieldCodec interface
func (f Field[T, F]) encode(v T) (string, any, error) {
	val := f.Get(v)
	enc, err := f.Codec.Encode(val)
	return f.Name, enc, err
}

//lint:ignore U1000 implements FieldCodec interface
func (f Field[T, F]) decode(obj map[string]any, target *T) error {
	raw, ok := obj[f.Name]
	if !ok {
		if f.Default != nil {
			f.Set(target, *f.Default)
			return nil
		}
		if f.Required {
			return ErrMissingField
		}
		return nil
	}

	val, err := f.Codec.Decode(raw)
	if err != nil {
		return err
	}

	f.Set(target, val)
	return nil
}

//lint:ignore U1000 implements FieldCodec interface
func (f Field[T, F]) schema() (string, schema.Schema, bool) {
	s := f.Codec.Schema
	if f.Default != nil {
		s.Default = any(*f.Default)
	}
	return f.Name, s, f.Required
}

// RequiredField is a shorthand for [Field] with Required set to true.
// The intent is explicit at the call site — no boolean flag needed.
func RequiredField[T, F any](name string, codec Codec[F], get func(T) F, set func(*T, F)) Field[T, F] {
	return Field[T, F]{Name: name, Codec: codec, Get: get, Set: set, Required: true}
}

// OptionalField is a shorthand for [Field] with Required set to false.
// The intent is explicit at the call site — no boolean flag needed.
func OptionalField[T, F any](name string, codec Codec[F], get func(T) F, set func(*T, F)) Field[T, F] {
	return Field[T, F]{Name: name, Codec: codec, Get: get, Set: set}
}

// DefaultField is a shorthand for [Field] with Required set to false and a
// documented default value. When the field is absent during decode, defaultVal
// is used automatically. The default appears in generated schemas as "default".
func DefaultField[T, F any](name string, codec Codec[F], defaultVal F, get func(T) F, set func(*T, F)) Field[T, F] {
	return Field[T, F]{Name: name, Codec: codec, Get: get, Set: set, Default: &defaultVal}
}

// IdentityField is [RequiredField] with T=F=V — declares a field where V
// ITSELF is the whole value (not a wrapper struct), via the identity
// get/set. Intended for a single-var [Template][V] (or [DottedKeyCodec][V])
// whose vars type V is a bare scalar, e.g. Template[Name]/Template[string]
// — the identity get/set (`func(v V) V { return v }` / `func(v *V, val V)
// { *v = val }`) would otherwise be repeated by hand at every such call
// site.
//
// NOT the same thing as a plain FIELD ACCESSOR — e.g.
// `codex.RequiredField("image", imageCodec, func(ms ModuleSettings)
// docker.Image { return ms.Image }, func(ms *ModuleSettings, v docker.Image)
// { ms.Image = v })` has T=ModuleSettings (a multi-field struct) and
// F=docker.Image (just ONE of its fields) — get/set genuinely EXTRACT/
// ASSIGN a field there, so T≠F, and [IdentityField] does not apply
// (there is no shortcut for that shape; [RequiredField]/[OptionalField]/
// [DefaultField]'s get/set closures are already the simplest expression
// of "field X of struct T" Go's type system allows). [IdentityField]
// applies ONLY when T=F=V — the container has NO other fields at all, so
// get/set do nothing but hand the value back unchanged.
//
//	var itemURITemplate = codex.NewTemplate("items://{id}", codex.PathStyle,
//	    codex.IdentityField("id", codex.String().Refine(validate.NonEmptyString)),
//	)
func IdentityField[V any](name string, codec Codec[V]) Field[V, V] {
	return RequiredField(name, codec,
		func(v V) V { return v },
		func(v *V, val V) { *v = val },
	)
}

// Struct builds a Codec[T] by composing field codecs. Schema is built eagerly.
func Struct[T any](fields ...FieldCodec[T]) Codec[T] {
	var props []schema.Property
	var req []string
	for _, f := range fields {
		name, s, r := f.schema()
		props = append(props, schema.Property{Name: name, Schema: s})
		if r {
			req = append(req, name)
		}
	}

	return Codec[T]{
		Encode: func(v T) (any, error) {
			obj := map[string]any{}
			var errs ValidationErrors
			for _, f := range fields {
				// sparseFieldCodec is an OPTIONAL companion capability
				// (OmitEmptyField/OmitEmptyFieldFunc/OmitDefaultField, see
				// omitempty.go) that lets a field OMIT its key entirely
				// instead of always writing it. Every other field type
				// (RequiredField/OptionalField/DefaultField) doesn't
				// implement it, so this check is a no-op for them --
				// completely backward compatible.
				if sf, ok := f.(sparseFieldCodec[T]); ok {
					name, val, present, err := sf.encodeSparse(v)
					if err != nil {
						errs = append(errs, ValidationError{Field: name, Err: err})
					} else if present {
						obj[name] = val
					}
					continue
				}
				name, val, err := f.encode(v)
				if err != nil {
					errs = append(errs, ValidationError{Field: name, Err: err})
				} else {
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
				name, _, _ := f.schema()
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
			Required:   req,
		},
	}
}

// StrictStruct is [Struct], but Decode additionally rejects any input key not
// declared by fields — the JSON Schema "additionalProperties: false"
// semantics. Encode is unchanged: "unknown field" only has meaning on the
// decode (external input) direction. Use StrictStruct when unrecognized keys
// should be treated as errors (e.g. catching a typo'd field name) instead of
// silently ignored, which is [Struct]'s default (forward-compatible) behavior.
//
//	strictOrderCodec := codex.StrictStruct[Order](
//	    codex.RequiredField("id", codex.String(), ...),
//	    // ...
//	)
//	_, err := strictOrderCodec.Decode(map[string]any{"id": "x", "totall": 9.99})
//	// err: field "totall": unknown field (ErrUnknownField) — likely a typo for "total"
//
// Strictness is NOT viral/recursive: a nested [Struct] field inside a
// StrictStruct-declared outer struct stays non-strict unless that nested
// codec is ALSO declared via StrictStruct — opt in at each nesting level
// independently, exactly like Required/Optional/Default are declared
// independently at each level.
//
// Unknown-key errors are collected alongside normal per-field errors
// (missing required fields, constraint failures) in one pass — a request
// with both a missing required field AND a typo'd key reports both, not
// just one.
func StrictStruct[T any](fields ...FieldCodec[T]) Codec[T] {
	c := Struct[T](fields...)

	known := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		name, _, _ := f.schema()
		known[name] = struct{}{}
	}

	falseVal := false
	c.Schema.AdditionalProperties = &falseVal

	innerDecode := c.Decode
	c.Decode = func(v any) (T, error) {
		var zero T
		obj, ok := v.(map[string]any)
		if !ok {
			return zero, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
		}

		result, err := innerDecode(v)
		var errs ValidationErrors
		if err != nil {
			if !errors.As(err, &errs) {
				return zero, err
			}
		}

		var unknown []string
		for k := range obj {
			if _, ok := known[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		sort.Strings(unknown) // deterministic error order across runs
		for _, k := range unknown {
			errs = append(errs, ValidationError{Field: k, Err: ErrUnknownField})
		}

		if len(errs) > 0 {
			return result, errs
		}
		return result, nil
	}

	return c
}
