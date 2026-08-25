package codex

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/DaniDeer/go-codex/internal/templatematch"
	"github.com/DaniDeer/go-codex/schema"
)

// This file holds the MQTT-style DOTTED-KEY TEMPLATE primitives —
// go-codex already has an MQTT topic-matching engine
// (internal/templatematch.MatchMQTTWildcard: "{varName}" captures one
// named segment, "+" matches one ANONYMOUS segment, "#" as the last
// segment matches the remaining path WHOLESALE). MatchDottedWildcard
// adapts the SAME algorithm with "." as the level delimiter instead of
// "/", giving dotted wire keys the identical declarative vocabulary MQTT
// topics already have.
//
// Wildcards ("+"/"#") are meaningful ONLY for MATCHING an already-
// existing key against a shape (many different concrete keys, one
// shape) — they have no meaning when BUILDING exactly one new concrete
// key from named values (there is no single value to substitute for
// "any segment"). This splits into two complementary constructors:
//
//   - DottedKeyCodec[K] — template contains ONLY "{varName}"/literal
//     text (no wildcards); builds ONE typed key K via the SAME
//     DecodeVars/EncodeVars/FieldCodec machinery PathParam/TopicParam/
//     FilePathParam already use — composable with Map[K,V]/EntrySlice
//     for a fully-typed dotted-key map.
//   - DottedPatchMapCodec — template MAY contain "+"/"#"; declares a
//     Codec[map[string]any] for a WHOLE BUCKET of matching entries,
//     values left OPAQUE (matching PartialStruct/ApplyPatch's own
//     untyped-leaf precedent for dynamic-shape patches — e.g.
//     examples/go-edge-models/models/iotedge/deviceconfig.PatchCodec's
//     own $edgeAgent bucket).
//
// See docs/concepts/codec.md's "Applying a patch" subsection and
// docs/guides/wire-vocabulary.md's dotted-key decision guide for the
// full design rationale and the relationship to [PrefixedKeyCodec]
// (the simpler "prefix + exactly one segment, no FieldCodec needed"
// shortcut, which stays unchanged and is NOT superseded by this file).

// hasWildcard reports whether template contains an MQTT-style "+" or
// "#" wildcard segment (as opposed to a "{varName}" placeholder or
// literal text).
func hasWildcard(template string) bool {
	for _, seg := range strings.Split(template, ".") {
		if seg == "+" || seg == "#" {
			return true
		}
	}
	return false
}

// literalPrefix returns the literal (non-var, non-wildcard) leading
// portion of template — everything before the FIRST "{varName}"/"+"/"#"
// segment, including a trailing "." separator (empty string if the
// template starts with a var/wildcard segment, or the whole template if
// it contains no var/wildcard segment at all).
func literalPrefix(template string) string {
	segments := strings.Split(template, ".")
	for i, seg := range segments {
		isVar := len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
		if isVar || seg == "+" || seg == "#" {
			if i == 0 {
				return ""
			}
			return strings.Join(segments[:i], ".") + "."
		}
	}
	return template
}

// DottedKeyCodec declares a Codec[K] for a SINGLE dotted wire key
// matching template — an MQTT-style template using "." as the level
// delimiter: literal text must match exactly; "{varName}" captures
// exactly one segment. Captured vars are merged into K via the SAME
// [DecodeVars]/[EncodeVars] + [RequiredField]/[OptionalField]/
// [DefaultField] machinery [PathParam]-equivalents across the codebase
// already use for path/topic/header/query vars — no new declaration
// mechanism.
//
// template must contain ONLY literal text and "{varName}" placeholders —
// PANICS if it contains "+" or "#" (a declaration-time programming
// error: DottedKeyCodec has no decode-only/match-only mode — a wildcard
// pattern is ALWAYS a caller mistake for this specific codec, unlike
// [Template]'s own, more general "construction always succeeds; only
// building fails" convention, which fits [DottedPatchMapCodec]'s
// legitimate whole-bucket, match-only use case instead).
//
// Use with [Map]/[EntrySlice] for a fully-typed dotted-key map — e.g.
// template "properties.desired.modules.{tenant}.{name}" merges into a
// ModuleKey{Tenant, Name} struct, generalizing what
// examples/flat-key-patch's former hand-rolled twoPartKeyCodec did by
// hand. Built on [Template]/[NewTemplate] via [DottedStyle] — the SAME
// shared build/match engine every other path/topic/dotted-key pattern
// in go-codex now uses; DottedKeyCodec is a thin, panic-preserving
// wrapper over it. Decode converts the underlying [Template]'s own
// [TemplateMismatchError] back into [DottedKeyError] at this boundary —
// DottedKeyCodec's established, documented error type — mirroring
// ports/file.go's/ports/dir.go's own boundary-conversion pattern
// (convertFileParamErr/convertDirParamErr) for the identical reason:
// keep the shared [Template] engine's internals invisible to callers
// depending on this codec's own error type.
func DottedKeyCodec[K any](template string, fields ...FieldCodec[K]) Codec[K] {
	if hasWildcard(template) {
		panic(fmt.Sprintf("codex: DottedKeyCodec: template %q must not contain \"+\"/\"#\" wildcards — use DottedPatchMapCodec for the whole-bucket, wildcard-matching case", template))
	}
	inner := NewTemplate(template, DottedStyle, fields...).Codec()
	return Codec[K]{
		Encode: inner.Encode,
		Decode: func(v any) (K, error) {
			k, err := inner.Decode(v)
			if err != nil {
				return k, convertDottedKeyErr(template, err)
			}
			return k, nil
		},
		Schema: inner.Schema,
	}
}

// convertDottedKeyErr converts a [TemplateMismatchError] from the shared
// [Template] engine into [DottedKeyError] — DottedKeyCodec's own,
// established error type (see [DottedKeyCodec]'s doc comment). Errors
// other than a structural mismatch (e.g. a field's own codec validation
// failure, surfaced as [ValidationErrors] via [DecodeVars]) pass through
// unchanged — DottedKeyError has always meant "the key doesn't match the
// template's SHAPE," not "a captured segment's value failed a
// constraint."
func convertDottedKeyErr(template string, err error) error {
	var tm TemplateMismatchError
	if errors.As(err, &tm) {
		return DottedKeyError{Key: tm.Concrete, Template: tm.Template, Err: errors.New("key does not match template")}
	}
	return err
}

// KeyVarConstraint pairs a named template variable with a [Constraint]
// validating its captured segment value — used by [DottedPatchMapCodec].
// Anonymous "+" segments have no name to attach a constraint to.
type KeyVarConstraint struct {
	Name       string
	Constraint Constraint[string]
}

// DottedKeyError reports a wire-level dotted key that doesn't match its
// expected template (structural mismatch), or whose named segment fails
// a registered [KeyVarConstraint] — returned by [DottedKeyCodec] and
// [DottedPatchMapCodec]. Implements slog.LogValuer for structured
// logging.
type DottedKeyError struct {
	Key      string
	Template string
	Err      error
}

func (e DottedKeyError) Error() string {
	return fmt.Sprintf("dotted key %q (template %q): %s", e.Key, e.Template, e.Err)
}

func (e DottedKeyError) Unwrap() error { return e.Err }

// LogValue implements slog.LogValuer for structured logging.
func (e DottedKeyError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("key", e.Key),
		slog.String("template", e.Template),
		slog.Any("err", e.Err),
	)
}

// DottedPatchMapCodec declares a Codec[map[string]any] for a WIRE-level
// BUCKET of dotted-key patch entries whose keys must match template — an
// MQTT-style template ("." as the level delimiter): literal text must
// match exactly; "{varName}" captures and (if a [KeyVarConstraint] is
// registered for that name via varConstraints) validates one segment;
// "+" matches one ANONYMOUS segment; "#" as the LAST template segment
// matches the remaining path WHOLESALE (zero or more further segments,
// left OPAQUE — matching [PartialStruct]/[ApplyPatch]'s own untyped-leaf
// precedent for dynamic-shape patches).
//
// Decode strips the template's own LITERAL PREFIX (everything before the
// first {var}/+/# token) from each matching wire key, producing the
// remaining dotted path as the Go-level map key (values passed through
// unvalidated/opaque) — e.g. template
// "properties.desired.modules.{moduleName}.#" strips
// "properties.desired.modules." from each matching key, ready for
// [ApplyDottedPatch]/[ApplyDottedPatchTo]. Encode re-adds that same
// literal prefix to each key, then re-validates the reconstructed key
// against template (catching caller mistakes before they reach the
// wire).
func DottedPatchMapCodec(template string, varConstraints ...KeyVarConstraint) Codec[map[string]any] {
	prefix := literalPrefix(template)
	constraintFor := make(map[string]Constraint[string], len(varConstraints))
	for _, vc := range varConstraints {
		constraintFor[vc.Name] = vc.Constraint
	}
	validateVars := func(key string, vars map[string]string) error {
		for name, val := range vars {
			if name == "#" {
				continue // opaque tail — never constrained
			}
			if con, ok := constraintFor[name]; ok && !con.Check(val) {
				return DottedKeyError{Key: key, Template: template, Err: ConstraintError{
					Name:    con.Name,
					Message: con.Message(val),
				}}
			}
		}
		return nil
	}
	wrapMismatch := func(key string) func(template, concrete string) error {
		return func(template, concrete string) error {
			return DottedKeyError{Key: key, Template: template, Err: fmt.Errorf("key does not match template")}
		}
	}

	return Codec[map[string]any]{
		Encode: func(m map[string]any) (any, error) {
			out := make(map[string]any, len(m))
			for k, v := range m {
				full := prefix + k
				vars, err := templatematch.MatchDottedWildcard(template, full, wrapMismatch(full))
				if err != nil {
					return nil, err
				}
				if err := validateVars(full, vars); err != nil {
					return nil, err
				}
				out[full] = v
			}
			return out, nil
		},
		Decode: func(v any) (map[string]any, error) {
			raw, ok := v.(map[string]any)
			if !ok {
				return nil, TypeMismatchError{Expected: "object", Got: fmt.Sprintf("%T", v)}
			}
			out := make(map[string]any, len(raw))
			for k, val := range raw {
				vars, err := templatematch.MatchDottedWildcard(template, k, wrapMismatch(k))
				if err != nil {
					return nil, err
				}
				if err := validateVars(k, vars); err != nil {
					return nil, err
				}
				out[strings.TrimPrefix(k, prefix)] = val
			}
			return out, nil
		},
		Schema: schema.Schema{Type: "object"},
	}
}
