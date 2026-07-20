package codex

import (
	"fmt"

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
