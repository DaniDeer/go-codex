package codex

import (
	"encoding"
	"fmt"
	"strconv"

	"github.com/DaniDeer/go-codex/schema"
)

// StringCodec builds a Codec[V] whose wire representation is a Go string —
// the general-purpose primitive for merging a NON-string typed value (an
// int, a UUID, any custom type) into a path/topic/query/header/cookie/key
// variable. parse converts the raw wire string into V (decode direction);
// format converts V back into the wire string (encode direction) — format
// itself returns an error so a failing conversion (e.g. a TextMarshaler
// that rejects an invalid invariant) propagates through Encode instead of
// being silently swallowed; return a nil error for formats that cannot
// fail. sch describes V's shape for spec/schema rendering.
//
// StringCodec is the answer to "can a path param be an int/UUID instead of
// a string": [codex.DecodeVars]/[codex.EncodeVars] box every path/topic/
// query/header/cookie/key variable as a plain Go string, so ANY Codec[V]
// that decodes-from and encodes-to a string composes with
// [RequiredField]/[OptionalField] (and therefore [NewParam] and every
// per-boundary constructor built on it) — StringCodec is simply the
// easiest way to build one:
//
//	var idCodec = codex.StringCodec(
//	    func(s string) (int, error) { return strconv.Atoi(s) },
//	    func(v int) (string, error) { return strconv.Itoa(v), nil },
//	    schema.Schema{Type: "integer"},
//	)
//	rest.NewPathParam("id", idCodec,
//	    func(r GetUserReq) int { return r.ID },
//	    func(r *GetUserReq, v int) { r.ID = v },
//	)
//
// Decode returns [TypeMismatchError] if the value handed to it is not a Go
// string (should not happen when called via [DecodeVars]/[ValidateParams],
// which always box vars as string; guards against direct misuse).
func StringCodec[V any](parse func(string) (V, error), format func(V) (string, error), sch schema.Schema) Codec[V] {
	return Codec[V]{
		Encode: func(v V) (any, error) { return format(v) },
		Decode: func(v any) (V, error) {
			var zero V
			s, ok := v.(string)
			if !ok {
				return zero, TypeMismatchError{Expected: "string", Got: fmt.Sprintf("%T", v)}
			}
			return parse(s)
		},
		Schema: sch,
	}
}

// textValue is the constraint TextCodec requires: *V must implement both
// encoding.TextMarshaler and encoding.TextUnmarshaler (the standard way a
// Go type declares "I have a canonical string form"). Satisfied by types
// like uuid.UUID (github.com/google/uuid) with zero go-codex-specific code.
type textValue[V any] interface {
	*V
	encoding.TextMarshaler
	encoding.TextUnmarshaler
}

// TextCodec builds a Codec[V] for any type V whose pointer already
// implements encoding.TextMarshaler/encoding.TextUnmarshaler — no explicit
// parse/format functions needed, unlike [StringCodec]. This is the
// zero-boilerplate path for types like uuid.UUID that already have a
// canonical text form:
//
//	var idCodec = codex.TextCodec[uuid.UUID]()
//	rest.NewPathParam("id", idCodec,
//	    func(r GetUserReq) uuid.UUID { return r.ID },
//	    func(r *GetUserReq, v uuid.UUID) { r.ID = v },
//	)
//
// Schema defaults to {Type: "string"} — chain .Refine on the returned
// Codec for additional constraints (format, pattern, etc.) if needed.
func TextCodec[V any, PV textValue[V]]() Codec[V] {
	return StringCodec(
		func(s string) (V, error) {
			var v V
			if err := PV(&v).UnmarshalText([]byte(s)); err != nil {
				return v, err
			}
			return v, nil
		},
		func(v V) (string, error) {
			b, err := PV(&v).MarshalText()
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
		schema.Schema{Type: "string"},
	)
}

// IntString is a [StringCodec] for int, using strconv.Atoi/strconv.Itoa —
// the common case of an int-typed path/topic/query/header/cookie/key
// variable.
func IntString() Codec[int] {
	return StringCodec(
		strconv.Atoi,
		func(v int) (string, error) { return strconv.Itoa(v), nil },
		schema.Schema{Type: "integer"},
	)
}

// Int64String is a [StringCodec] for int64, using strconv.ParseInt/FormatInt
// (base 10).
func Int64String() Codec[int64] {
	return StringCodec(
		func(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) },
		func(v int64) (string, error) { return strconv.FormatInt(v, 10), nil },
		schema.Schema{Type: "integer"},
	)
}

// UintString is a [StringCodec] for uint64, using strconv.ParseUint/FormatUint
// (base 10).
func UintString() Codec[uint64] {
	return StringCodec(
		func(s string) (uint64, error) { return strconv.ParseUint(s, 10, 64) },
		func(v uint64) (string, error) { return strconv.FormatUint(v, 10), nil },
		schema.Schema{Type: "integer"},
	)
}

// BoolString is a [StringCodec] for bool, using strconv.ParseBool/FormatBool.
func BoolString() Codec[bool] {
	return StringCodec(
		strconv.ParseBool,
		func(v bool) (string, error) { return strconv.FormatBool(v), nil },
		schema.Schema{Type: "boolean"},
	)
}
