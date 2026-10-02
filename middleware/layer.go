package middleware

import "github.com/DaniDeer/go-codex/codex"

// Axis names a single merge-field "location" (header, cookie, query,
// property, ...) participating in a multi-axis decode — one declared
// vocabulary surface a [Middleware]'s In/Out value can be decoded from or
// encoded onto. Every REAL call site declares a FIXED, compile-time-known
// number of axes (e.g. REST's request side is always exactly 3: header,
// cookie, query) — Axis is positional (a plain slice), not a named map,
// mirroring that fixed shape exactly; see [DecodeLayer]/[EncodeLayer]'s
// own doc comments for the full rationale.
type Axis[T any] struct {
	// Fields are this axis's merge-field declarations — typically derived
	// from a Merged*Param slice via a package-local xFieldsOf helper (see
	// api/rest/transform.go's headerFieldsOf/cookieFieldsOf/queryFieldsOf
	// for the reference pattern). An empty/nil Fields is a safe no-op —
	// this axis contributes nothing.
	Fields []codex.FieldCodec[T]
	// Vars is this axis's raw, string-keyed wire value map (e.g. the
	// actual incoming HTTP headers, for a header axis) — the SAME shape
	// [codex.DecodeVars]'s own vars parameter takes.
	Vars map[string]string
}

// DecodeLayer decodes a T from zero or more axes, in order, using
// [codex.DecodeVars] internally — the shared, protocol-agnostic merge-
// field dispatch engine extracted from api/rest's buildDecodeIn/
// buildDecodeOut (and mirrored, pre-extraction, by api/events'/
// api/reqreply's own near-identical functions). Fails FAST at the first
// axis whose decode errors — later axes are never attempted — mirroring
// every existing call site's own established behavior exactly (an axis
// decode failure is a caller-visible, immediately-reported error, not
// accumulated alongside other axes' failures).
//
// wrapErr wraps the raw [codex.ValidationErrors] (or other codec error)
// into the caller's OWN structured error type (e.g. rest.MiddlewareInputError,
// events.MiddlewareInputError, reqreply.MiddlewareInputError) — DecodeLayer
// itself is package-agnostic and never constructs one directly, so every
// package's own established error type/message stays completely unchanged
// by this refactor.
//
// An axis with an empty/nil Fields list is skipped entirely (matches every
// existing "if len(fields) > 0" guard this replaces).
func DecodeLayer[T any](axes []Axis[T], wrapErr func(err error) error) (T, error) {
	var out T
	for _, ax := range axes {
		if len(ax.Fields) == 0 {
			continue
		}
		if err := codex.DecodeVars(&out, ax.Vars, ax.Fields...); err != nil {
			return out, wrapErr(err)
		}
	}
	return out, nil
}

// EncodeLayer is [DecodeLayer]'s encode-side mirror — derives one
// map[string]string per axis FROM v, using [codex.EncodeMergeVars]
// internally (NOT [codex.EncodeVars] — merge-field locations are exactly
// where an [codex.OmitEmptyField]/[codex.OmitEmptyFieldFunc]-declared
// field's "omit if empty" behavior must take effect; see
// [codex.EncodeMergeVars]'s own doc comment). Returns a nil map (not an
// empty one) for any axis whose Fields list is empty/nil — matching every
// existing call site's "declares no merge-capable params -> nil map,
// identical to today's behavior" contract.
//
// axisFields is positional, parallel to the returned []map[string]string —
// axisFields[i] produces vars[i]. Fails FAST at the first axis whose
// encode errors, exactly like [DecodeLayer].
//
// wrapErr wraps the raw [codex.ValidationErrors]/[codex.VarEncodeTypeError]
// into the caller's own structured error type (e.g. rest.MiddlewareOutputError) —
// see [DecodeLayer]'s own doc comment for the full rationale.
func EncodeLayer[T any](v T, axisFields [][]codex.FieldCodec[T], wrapErr func(err error) error) ([]map[string]string, error) {
	vars := make([]map[string]string, len(axisFields))
	for i, fields := range axisFields {
		if len(fields) == 0 {
			continue
		}
		enc, err := codex.EncodeMergeVars(v, fields...)
		if err != nil {
			return nil, wrapErr(err)
		}
		vars[i] = enc
	}
	return vars, nil
}
