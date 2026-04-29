package codex

import (
	"fmt"

	"github.com/DaniDeer/go-codex/schema"
)

type fieldCodec[T any] interface {
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
}

//lint:ignore U1000 implements fieldCodec interface
func (f Field[T, F]) encode(v T) (string, any, error) {
	val := f.Get(v)
	enc, err := f.Codec.Encode(val)
	return f.Name, enc, err
}

//lint:ignore U1000 implements fieldCodec interface
func (f Field[T, F]) decode(obj map[string]any, target *T) error {
	raw, ok := obj[f.Name]
	if !ok {
		if f.Required {
			return fmt.Errorf("missing required field: %s", f.Name)
		}
		return nil
	}

	val, err := f.Codec.Decode(raw)
	if err != nil {
		return fmt.Errorf("field %s: %w", f.Name, err)
	}

	f.Set(target, val)
	return nil
}

//lint:ignore U1000 implements fieldCodec interface
func (f Field[T, F]) schema() (string, schema.Schema, bool) {
	return f.Name, f.Codec.Schema, f.Required
}

// Struct builds a Codec[T] by composing field codecs. Schema is built eagerly.
func Struct[T any](fields ...fieldCodec[T]) Codec[T] {
	props := map[string]schema.Schema{}
	req := []string{}
	for _, f := range fields {
		name, s, r := f.schema()
		props[name] = s
		if r {
			req = append(req, name)
		}
	}

	return Codec[T]{
		Encode: func(v T) (any, error) {
			obj := map[string]any{}
			for _, f := range fields {
				name, val, err := f.encode(v)
				if err != nil {
					return nil, err
				}
				obj[name] = val
			}
			return obj, nil
		},
		Decode: func(v any) (T, error) {
			var result T
			obj, ok := v.(map[string]any)
			if !ok {
				return result, fmt.Errorf("expected object, got %T", v)
			}
			for _, f := range fields {
				if err := f.decode(obj, &result); err != nil {
					return result, err
				}
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
