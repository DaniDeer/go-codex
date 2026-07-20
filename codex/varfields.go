package codex

import (
	"fmt"
	"log/slog"
)

// VarEncodeTypeError is returned by [EncodeVars] when a field's Codec.Encode
// does not produce a string value. Attaching an unsuitable codec (e.g.
// [Int] directly, instead of a string-wire-wrapped codec built via
// [MapCodecSafe]) to a var field is a caller programming error, not a
// runtime data error — vars maps are always string-keyed/string-valued
// (path segments, topic segments, header/query/cookie values, and file
// path segments are all strings at the wire level).
type VarEncodeTypeError struct {
	Field string
	Got   string // fmt.Sprintf("%T", val)
}

func (e VarEncodeTypeError) Error() string {
	return fmt.Sprintf("codex: var field %q: Codec.Encode must produce a string, got %s", e.Field, e.Got)
}

// LogValue implements [slog.LogValuer] for structured logging.
func (e VarEncodeTypeError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("field", e.Field),
		slog.String("got", e.Got),
	)
}

// DecodeVars decodes each named field in fields from vars into target,
// mutating only those fields — any other fields already set on *target are
// left untouched. This is a PARTIAL merge, unlike [Struct]'s Decode, which
// builds an entirely new T from one JSON object.
//
// fields are declared with the SAME [RequiredField]/[OptionalField]/
// [DefaultField] constructors already used for [Struct] — no new
// declaration API. A field's Codec must accept a string value on Decode
// (e.g. [String]()... or [MapCodecSafe]([String]()..., ...) for a typed
// field like int or time.Time) since vars is always string-keyed/
// string-valued (path segments, topic segments, header/query/cookie
// values, and file path segments are all strings at the wire level).
//
// [RequiredField] vars that are absent from vars return [ValidationErrors]
// containing [ErrMissingField]; [OptionalField]/[DefaultField] vars that
// are absent are skipped/defaulted exactly as in [Struct]. Codec validation
// failures are collected the same way — DecodeVars never stops at the
// first error; every field is attempted, and every failure is reported.
//
//	var req GetUserReq
//	err := codex.DecodeVars(&req, map[string]string{"id": r.PathValue("id")},
//	    codex.RequiredField("id", codex.String().Refine(validate.UUID),
//	        func(r GetUserReq) string { return r.ID },
//	        func(r *GetUserReq, v string) { r.ID = v }))
func DecodeVars[T any](target *T, vars map[string]string, fields ...FieldCodec[T]) error {
	obj := make(map[string]any, len(vars))
	for k, v := range vars {
		obj[k] = v
	}

	var errs ValidationErrors
	for _, f := range fields {
		name, _, _ := f.schema()
		if err := f.decode(obj, target); err != nil {
			errs = append(errs, ValidationError{Field: name, Err: err})
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// EncodeVars extracts each named field in fields from v using its Get
// function and Codec, producing a map[string]string. This replaces
// hand-written varsFor func(T) map[string]string closures used by every
// adapter's SinkAdapter/IOAdapter/SourceAdapter constructor (adapters/file,
// adapters/redis, adapters/mqtt, adapters/mqtt5, adapters/zeromq) — call it
// FROM inside the closure the adapter expects:
//
//	varsFor := func(r SensorReading) map[string]string {
//	    return codex.Must(codex.EncodeVars(r, sensorIDField))
//	}
//
// Returns [VarEncodeTypeError] if any field's Codec.Encode does not
// produce a string — a caller programming error (an unsuitable codec was
// attached to a var field), not a runtime data error.
func EncodeVars[T any](v T, fields ...FieldCodec[T]) (map[string]string, error) {
	out := make(map[string]string, len(fields))
	var errs ValidationErrors
	for _, f := range fields {
		name, val, err := f.encode(v)
		if err != nil {
			errs = append(errs, ValidationError{Field: name, Err: err})
			continue
		}
		s, ok := val.(string)
		if !ok {
			return nil, VarEncodeTypeError{Field: name, Got: fmt.Sprintf("%T", val)}
		}
		out[name] = s
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}
