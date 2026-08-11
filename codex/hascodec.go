package codex

import "github.com/DaniDeer/go-codex/schema"

// HasCodec is implemented by a type that declares its own canonical
// Codec[T] — the "this type knows how to validate/encode/decode itself"
// convention, as close as Go gets to Pydantic's model-owns-its-validation
// idea without inheritance (Go has none): a type implements Codec() in
// one line, and the generic functions below ([Validate], [New],
// [EncodeSelf], [DecodeAs], [SchemaOf]) then work on ANY type
// implementing it, with zero per-type boilerplate beyond that one method.
//
// Prefer defining Codec() as a package-level function
// (func Codec() codex.Codec[MyType]) when the type is a plain value type
// with no per-instance state — the common case. Use a method receiver
// only when the codec genuinely depends on instance state.
//
// IMPORTANT for [DecodeAs] and [SchemaOf]: neither has a T value to call
// .Codec() on yet, so both call it on T's ZERO VALUE (var zero T;
// zero.Codec()). This is correct and side-effect-free for the documented
// common case (a stateless Codec()) — but means a HasCodec
// implementation whose Codec() genuinely depends on instance state must
// NOT be used with DecodeAs/SchemaOf (its zero value would return the
// WRONG codec). [Validate], [New], and [EncodeSelf] have no such
// restriction — they always call Codec() on the actual value passed in.
//
// Adoption is entirely OPT-IN, exactly like [Codec.New] itself — no
// existing type or package is required to implement HasCodec, and
// go-codex never assumes a type does.
type HasCodec[T any] interface {
	Codec() Codec[T]
}

// Validate checks v against its own declared Codec — the HasCodec-generic
// form of v.Codec().Validate(v), callable without repeating the codec
// name at the call site.
func Validate[T HasCodec[T]](v T) error {
	return v.Codec().Validate(v)
}

// New validates v against its own declared Codec and returns it on
// success (or the zero value and the first failing constraint's error) —
// the HasCodec-generic form of [Codec.New].
func New[T HasCodec[T]](v T) (T, error) {
	return v.Codec().New(v)
}

// EncodeSelf encodes v via its own declared Codec. Named EncodeSelf
// (not Encode) to read naturally at the call site — "v encodes itself" —
// and to avoid ever shadowing a Codec[T] value literally named Encode in
// scope.
func EncodeSelf[T HasCodec[T]](v T) (any, error) {
	return v.Codec().Encode(v)
}

// DecodeAs decodes raw into a T via T's own declared Codec — T is an
// explicit type parameter since there is no T value yet to call .Codec()
// on (see HasCodec's own doc comment for the zero-value-call contract
// this relies on):
//
//	img, err := codex.DecodeAs[Image](raw)
func DecodeAs[T HasCodec[T]](raw any) (T, error) {
	var zero T
	return zero.Codec().Decode(raw)
}

// SchemaOf returns T's declared Codec's Schema — useful for spec
// generation/documentation without constructing a T first (see
// HasCodec's own doc comment for the zero-value-call contract this
// relies on, the same one [DecodeAs] uses):
//
//	s := codex.SchemaOf[Image]()
func SchemaOf[T HasCodec[T]]() schema.Schema {
	var zero T
	return zero.Codec().Schema
}
