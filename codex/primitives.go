package codex

import (
	"encoding/base64"
	"fmt"
	"math"

	"github.com/DaniDeer/go-codex/schema"
)

// Int returns a Codec for the int type.
func Int() Codec[int] {
	return Codec[int]{
		Encode: func(v int) (any, error) { return v, nil },
		Decode: func(v any) (int, error) {
			switch n := v.(type) {
			case float64:
				if n != math.Trunc(n) {
					return 0, ConstraintError{
						Name:    "integer",
						Message: fmt.Sprintf("expected integer, got non-integral float %v", n),
					}
				}
				return int(n), nil
			case int:
				return n, nil
			case int64:
				return int(n), nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "integer"},
	}
}

// Int32 returns a Codec for the int32 type.
// The schema uses format "int32" to document the reduced range.
func Int32() Codec[int32] {
	const min, max = math.MinInt32, math.MaxInt32
	return Codec[int32]{
		Encode: func(v int32) (any, error) { return v, nil },
		Decode: func(v any) (int32, error) {
			switch n := v.(type) {
			case float64:
				if n != math.Trunc(n) {
					return 0, ConstraintError{
						Name:    "integer",
						Message: fmt.Sprintf("expected integer, got non-integral float %v", n),
					}
				}
				if n < min || n > max {
					return 0, ConstraintError{
						Name:    "int32",
						Message: fmt.Sprintf("value %v overflows int32", n),
					}
				}
				return int32(n), nil
			case int:
				if n < min || n > max {
					return 0, ConstraintError{
						Name:    "int32",
						Message: fmt.Sprintf("value %v overflows int32", n),
					}
				}
				return int32(n), nil
			case int32:
				return n, nil
			case int64:
				if n < min || n > max {
					return 0, ConstraintError{
						Name:    "int32",
						Message: fmt.Sprintf("value %v overflows int32", n),
					}
				}
				return int32(n), nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "integer", Format: "int32"},
	}
}

// Int64 returns a Codec for the int64 type.
func Int64() Codec[int64] {
	return Codec[int64]{
		Encode: func(v int64) (any, error) { return v, nil },
		Decode: func(v any) (int64, error) {
			switch n := v.(type) {
			case float64:
				if n != math.Trunc(n) {
					return 0, ConstraintError{
						Name:    "integer",
						Message: fmt.Sprintf("expected integer, got non-integral float %v", n),
					}
				}
				return int64(n), nil
			case int:
				return int64(n), nil
			case int64:
				return n, nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "integer"},
	}
}

// Float64 returns a Codec for the float64 type.
func Float64() Codec[float64] {
	return Codec[float64]{
		Encode: func(v float64) (any, error) { return v, nil },
		Decode: func(v any) (float64, error) {
			switch n := v.(type) {
			case float64:
				return n, nil
			case int:
				return float64(n), nil
			case int64:
				return float64(n), nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "number"},
	}
}

// Float32 returns a Codec for the float32 type.
// The schema uses format "float" to document the reduced precision.
func Float32() Codec[float32] {
	return Codec[float32]{
		Encode: func(v float32) (any, error) { return float64(v), nil },
		Decode: func(v any) (float32, error) {
			switch n := v.(type) {
			case float64:
				return float32(n), nil
			case float32:
				return n, nil
			case int:
				return float32(n), nil
			case int64:
				return float32(n), nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "number", Format: "float"},
	}
}

// Uint returns a Codec for the uint type.
// The schema sets minimum: 0 to document the non-negative constraint.
func Uint() Codec[uint] {
	return Codec[uint]{
		Encode: func(v uint) (any, error) { return v, nil },
		Decode: func(v any) (uint, error) {
			switch n := v.(type) {
			case float64:
				if n != math.Trunc(n) {
					return 0, ConstraintError{
						Name:    "integer",
						Message: fmt.Sprintf("expected integer, got non-integral float %v", n),
					}
				}
				if n < 0 {
					return 0, ConstraintError{
						Name:    "uint",
						Message: fmt.Sprintf("expected non-negative integer, got %v", n),
					}
				}
				return uint(n), nil
			case int:
				if n < 0 {
					return 0, ConstraintError{
						Name:    "uint",
						Message: fmt.Sprintf("expected non-negative integer, got %v", n),
					}
				}
				return uint(n), nil
			case uint:
				return n, nil
			case uint64:
				return uint(n), nil
			case int64:
				if n < 0 {
					return 0, ConstraintError{
						Name:    "uint",
						Message: fmt.Sprintf("expected non-negative integer, got %v", n),
					}
				}
				return uint(n), nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "integer", Minimum: float64ptr(0)},
	}
}

// Uint64 returns a Codec for the uint64 type.
// The schema sets minimum: 0 to document the non-negative constraint.
func Uint64() Codec[uint64] {
	return Codec[uint64]{
		Encode: func(v uint64) (any, error) { return v, nil },
		Decode: func(v any) (uint64, error) {
			switch n := v.(type) {
			case float64:
				if n != math.Trunc(n) {
					return 0, ConstraintError{
						Name:    "integer",
						Message: fmt.Sprintf("expected integer, got non-integral float %v", n),
					}
				}
				if n < 0 {
					return 0, ConstraintError{
						Name:    "uint64",
						Message: fmt.Sprintf("expected non-negative integer, got %v", n),
					}
				}
				return uint64(n), nil
			case int:
				if n < 0 {
					return 0, ConstraintError{
						Name:    "uint64",
						Message: fmt.Sprintf("expected non-negative integer, got %v", n),
					}
				}
				return uint64(n), nil
			case uint:
				return uint64(n), nil
			case uint64:
				return n, nil
			case int64:
				if n < 0 {
					return 0, ConstraintError{
						Name:    "uint64",
						Message: fmt.Sprintf("expected non-negative integer, got %v", n),
					}
				}
				return uint64(n), nil
			default:
				return 0, TypeMismatchError{Expected: "number", Got: fmt.Sprintf("%T", v)}
			}
		},
		Schema: schema.Schema{Type: "integer", Minimum: float64ptr(0)},
	}
}

// String returns a Codec for the string type.
func String() Codec[string] {
	return Codec[string]{
		Encode: func(v string) (any, error) { return v, nil },
		Decode: func(v any) (string, error) {
			s, ok := v.(string)
			if !ok {
				return "", TypeMismatchError{Expected: "string", Got: fmt.Sprintf("%T", v)}
			}
			return s, nil
		},
		Schema: schema.Schema{Type: "string"},
	}
}

// Bool returns a Codec for the bool type.
func Bool() Codec[bool] {
	return Codec[bool]{
		Encode: func(v bool) (any, error) { return v, nil },
		Decode: func(v any) (bool, error) {
			b, ok := v.(bool)
			if !ok {
				return false, TypeMismatchError{Expected: "boolean", Got: fmt.Sprintf("%T", v)}
			}
			return b, nil
		},
		Schema: schema.Schema{Type: "boolean"},
	}
}

// Bytes returns a Codec for []byte using base64 standard encoding.
// Encoded values are strings; schema format is "byte".
func Bytes() Codec[[]byte] {
	return Codec[[]byte]{
		Schema: schema.Schema{Type: "string", Format: "byte"},
		Encode: func(v []byte) (any, error) {
			return base64.StdEncoding.EncodeToString(v), nil
		},
		Decode: func(v any) ([]byte, error) {
			s, ok := v.(string)
			if !ok {
				return nil, TypeMismatchError{Expected: "string", Got: fmt.Sprintf("%T", v)}
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, fmt.Errorf("invalid base64: %w", err)
			}
			return b, nil
		},
	}
}

func float64ptr(v float64) *float64 { return &v }

// Any returns a Codec[any] that passes values through without modification.
// Encode and Decode are identity functions; no type checking or coercion is applied.
// The schema is empty ({}) which means "accepts any value" in JSON Schema terms.
//
// Typical uses: extension fields, opaque config blobs, dynamic JSON passed through.
//
// Note: format.FromEnv has no type hints for any-typed fields and will pass the
// raw env var string through as-is.
func Any() Codec[any] {
	return Codec[any]{
		Encode: func(v any) (any, error) { return v, nil },
		Decode: func(v any) (any, error) { return v, nil },
		Schema: schema.Schema{},
	}
}

// Pure returns a Codec[T] that always decodes to value regardless of the wire input,
// and always encodes value regardless of the Go value passed to Encode.
//
// Use for fields that must always carry a fixed value in the encoded form — for example,
// a protocol specversion field ("1.0") or a derived field set automatically.
//
// The schema is {enum: [value]} to communicate the fixed value to documentation tools.
func Pure[T any](value T) Codec[T] {
	return Codec[T]{
		Encode: func(_ T) (any, error) { return value, nil },
		Decode: func(_ any) (T, error) { return value, nil },
		Schema: schema.Schema{Enum: []any{value}},
	}
}

// Empty is a ready-made [Codec] for routes and SSE streams that carry no request body.
// Use it as the reqCodec argument to [api/rest.NewRoute] and [api/rest.NewSSERoute] for
// GET, DELETE, and other body-less routes — no per-file empty struct or codec needed.
//
// handle, err := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
//
//	codex.Empty, userCodec, rest.RouteMeta{OperationID: "getUser"},
//
// ).Register(b)
var Empty Codec[struct{}] = Struct[struct{}]()
