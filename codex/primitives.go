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
					return 0, fmt.Errorf("expected integer, got non-integral float %v", n)
				}
				return int(n), nil
			case int:
				return n, nil
			case int64:
				return int(n), nil
			default:
				return 0, fmt.Errorf("expected number, got %T", v)
			}
		},
		Schema: schema.Schema{Type: "integer"},
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
					return 0, fmt.Errorf("expected integer, got non-integral float %v", n)
				}
				return int64(n), nil
			case int:
				return int64(n), nil
			case int64:
				return n, nil
			default:
				return 0, fmt.Errorf("expected number, got %T", v)
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
				return 0, fmt.Errorf("expected number, got %T", v)
			}
		},
		Schema: schema.Schema{Type: "number"},
	}
}

// String returns a Codec for the string type.
func String() Codec[string] {
	return Codec[string]{
		Encode: func(v string) (any, error) { return v, nil },
		Decode: func(v any) (string, error) {
			s, ok := v.(string)
			if !ok {
				return "", fmt.Errorf("expected string, got %T", v)
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
				return false, fmt.Errorf("expected bool, got %T", v)
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
				return nil, fmt.Errorf("expected string, got %T", v)
			}
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return nil, fmt.Errorf("invalid base64: %w", err)
			}
			return b, nil
		},
	}
}
