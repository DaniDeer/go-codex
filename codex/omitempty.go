package codex

import (
	"reflect"

	"github.com/DaniDeer/go-codex/schema"
)

// This file implements the "omit a zero-valued field from Encode" primitives:
// OmitEmptyField, OmitEmptyFieldFunc, OmitDefaultField, and the IsZeroValue
// helper. See docs/concepts/codec.md's "OmitEmptyField/OmitEmptyFieldFunc/
// OmitDefaultField: omitting a zero-valued field on encode" subsection for
// the full design rationale, including why OptionalField/DefaultField are
// NOT modified directly and the reflection trade-off discussion.
//
// sparseFieldCodec is a PARALLEL, OPTIONAL companion capability a
// FieldCodec[T] may additionally implement — checked via type-assertion
// exclusively inside Struct's Encode loop (see object.go). A FieldCodec[T]
// that doesn't implement it (every existing RequiredField/OptionalField/
// DefaultField, backed by Field[T,F]) keeps Struct's current "always write
// every field" behavior completely unchanged. This mirrors PartialField's
// own reasoning for staying a parallel mechanism rather than a breaking
// change to FieldCodec itself — see partial.go's own doc comment.

// sparseFieldCodec is the sealed interface a [FieldCodec] may additionally
// implement to participate in [Struct]'s "omit if empty" Encode behavior.
// Implemented by the value [OmitEmptyField]/[OmitEmptyFieldFunc]/
// [OmitDefaultField] return.
type sparseFieldCodec[T any] interface {
	// encodeSparse reports whether this field's key should be OMITTED from
	// the encoded object entirely (present == false) rather than written
	// with its current value.
	encodeSparse(v T) (name string, val any, present bool, err error)
}

// sparseField is sparseFieldCodec[T]'s sole implementation, returned by
// [OmitEmptyField]/[OmitEmptyFieldFunc]/[OmitDefaultField] AND (in
// maybe.go) [MaybeField] — one struct backs all four constructors,
// mirroring how Field[T,F] backs RequiredField/OptionalField/DefaultField.
// MaybeField's isEmpty is simply Maybe[V].IsSet() negated — a DEFINITIVE
// presence check rather than a zero-value heuristic, see maybe.go's own
// doc comment.
type sparseField[T, F any] struct {
	name    string
	codec   Codec[F]
	get     func(T) F
	set     func(*T, F)
	isEmpty func(F) bool
	// def holds the field's default value, applied on decode when the key
	// is absent. nil means no default (OmitEmptyField/OmitEmptyFieldFunc);
	// non-nil means OmitDefaultField's declared default — same "pointer
	// distinguishes no-default from zero-value-default" convention as
	// Field.Default.
	def *F
}

// encode implements FieldCodec[T]. It ALWAYS encodes the current value,
// exactly like OptionalField -- it deliberately does NOT consult isEmpty.
// This method is only reached when a sparseField is used OUTSIDE Struct's
// own Encode loop (Template/DottedKeyCodec/DecodeVars/EncodeVars all call a
// field's plain encode() directly) -- see docs/concepts/codec.md's matching
// subsection, "Interaction with Template/DottedKeyCodec/DecodeVars/
// EncodeVars", for why this must never silently drop a path/topic var.
//
//lint:ignore U1000 implements FieldCodec interface
func (f sparseField[T, F]) encode(v T) (string, any, error) {
	val := f.get(v)
	enc, err := f.codec.Encode(val)
	return f.name, enc, err
}

// encodeSparse implements sparseFieldCodec[T] -- the ONLY method that
// consults isEmpty, and the ONLY path Struct's Encode loop uses for a
// sparseField.
//
//lint:ignore U1000 implements sparseFieldCodec interface
func (f sparseField[T, F]) encodeSparse(v T) (string, any, bool, error) {
	val := f.get(v)
	if f.isEmpty(val) {
		return f.name, nil, false, nil
	}
	enc, err := f.codec.Encode(val)
	if err != nil {
		return f.name, nil, false, err
	}
	return f.name, enc, true, nil
}

// decode implements FieldCodec[T]. Absent key -> declared default applied
// (if any, i.e. OmitDefaultField), else Go zero value remains -- exactly
// mirroring Field.decode's own OptionalField/DefaultField behavior.
//
//lint:ignore U1000 implements FieldCodec interface
func (f sparseField[T, F]) decode(obj map[string]any, target *T) error {
	raw, ok := obj[f.name]
	if !ok {
		if f.def != nil {
			f.set(target, *f.def)
		}
		return nil
	}

	val, err := f.codec.Decode(raw)
	if err != nil {
		return err
	}

	f.set(target, val)
	return nil
}

// schema implements FieldCodec[T]. Never Required (mirrors
// OptionalField/DefaultField); Default is set in the schema when declared.
//
//lint:ignore U1000 implements FieldCodec interface
func (f sparseField[T, F]) schema() (string, schema.Schema, bool) {
	s := f.codec.Schema
	if f.def != nil {
		s.Default = any(*f.def)
	}
	return f.name, s, false
}

// OmitEmptyField declares a field that decodes EXACTLY like OptionalField
// (absent key -> Go zero value, never Required) but is OMITTED from the
// encoded object -- not written as null/""/[]/0 -- whenever its current
// value equals F's Go zero value.
//
// CRITICAL: comparing to the zero value cannot distinguish "never set" from
// "deliberately set to the zero-equivalent value". Only use this for a
// field whose OWN documented convention already treats the zero value as a
// first-class "absent" sentinel (mirrors dockercompose's own
// Build.Context==""/MemLimit==0/Healthcheck==Healthcheck{} convention).
// Never use it for a field where the zero value is itself meaningful data
// (e.g. a Priority int where 0 is a real, distinct priority level) -- use
// [PartialField]/[PartialStruct] instead when that distinction matters. See
// docs/concepts/codec.md's matching subsection for the full rationale.
func OmitEmptyField[T any, F comparable](
	name string, codec Codec[F], get func(T) F, set func(*T, F),
) FieldCodec[T] {
	var zero F
	return sparseField[T, F]{
		name:  name,
		codec: codec,
		get:   get,
		set:   set,
		isEmpty: func(v F) bool {
			return v == zero
		},
	}
}

// OmitEmptyFieldFunc is [OmitEmptyField] with an explicit isEmpty predicate
// instead of a zero-value comparison -- required whenever F is not
// comparable (slices, maps) or "empty" means something other than Go's zero
// value (e.g. a domain-specific IsSet()-style check). See [IsZeroValue] for
// a ready-made reflection-based predicate covering the general nil/zero
// case for non-comparable types.
func OmitEmptyFieldFunc[T, F any](
	name string, codec Codec[F], get func(T) F, set func(*T, F),
	isEmpty func(F) bool,
) FieldCodec[T] {
	return sparseField[T, F]{
		name:    name,
		codec:   codec,
		get:     get,
		set:     set,
		isEmpty: isEmpty,
	}
}

// OmitDefaultField decodes EXACTLY like DefaultField (absent key -> the
// declared default is applied) but is OMITTED from the encoded object
// whenever the field's CURRENT value equals that same declared default --
// giving a "minimal diff" round trip (only fields that differ from their
// default appear on the wire) without changing DefaultField's own,
// currently-relied-upon "always show the resolved value" contract.
//
// Same ambiguity caveat as OmitEmptyField applies: this cannot distinguish
// "never touched" from "explicitly reset to the default" -- use only when
// that distinction doesn't matter for this field. See
// docs/concepts/codec.md's matching subsection ("Why not just change
// OptionalField/DefaultField directly?") for the full rationale.
func OmitDefaultField[T any, F comparable](
	name string, codec Codec[F], defaultVal F, get func(T) F, set func(*T, F),
) FieldCodec[T] {
	return sparseField[T, F]{
		name:  name,
		codec: codec,
		get:   get,
		set:   set,
		isEmpty: func(v F) bool {
			return v == defaultVal
		},
		def: &defaultVal,
	}
}

// IsZeroValue reports whether v is F's Go zero value, via reflection --
// unlike a plain "==" comparison, this works for ANY type including
// slices/maps/funcs/pointers that Go's comparable constraint excludes, and
// (for slices/maps/pointers specifically) checks NIL-ness, not
// length/emptiness -- so an explicitly-decoded empty slice/map ([]T{},
// map[K]V{}) is correctly treated as NOT zero, distinct from a
// never-populated nil one.
//
// This is go-codex's ONLY use of reflection, and it is entirely opt-in:
// pass it explicitly to [OmitEmptyFieldFunc]'s isEmpty parameter when you
// want generic nil/zero detection without writing your own predicate --
//
//	codex.OmitEmptyFieldFunc("command", codex.SliceOf(codex.String()),
//	    get, set, codex.IsZeroValue)
//
// Domain types with their OWN asymmetric "empty" rule (e.g. a Build.IsSet()
// that checks only ONE field, not the whole struct) must still write their
// own predicate -- IsZeroValue only knows Go's structural zero value, not
// business meaning. See docs/concepts/codec.md's matching subsection for
// the full rationale (including the nil-vs-empty-slice correctness case).
func IsZeroValue[F any](v F) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return true
	}
	return rv.IsZero()
}
