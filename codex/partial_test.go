package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// profilePatch is a small test-local type for exercising PartialField/
// PartialStruct — mirrors examples/construction's Profile shape but with
// every field optional (a pointer), the "patch" convention.
type profilePatch struct {
	Handle *string
	Score  *int
}

var profilePatchCodec = codex.PartialStruct[profilePatch](
	codex.PartialField("handle",
		codex.String().Refine(validate.MinLen(3)),
		func(p profilePatch) *string { return p.Handle },
		func(p *profilePatch, v *string) { p.Handle = v },
	),
	codex.PartialField("score",
		codex.Int().Refine(validate.RangeInt(0, 100)),
		func(p profilePatch) *int { return p.Score },
		func(p *profilePatch, v *int) { p.Score = v },
	),
)

func TestPartialField_Encode_OmitsNilPointer(t *testing.T) {
	raw, err := profilePatchCodec.Encode(profilePatch{})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if _, ok := obj["handle"]; ok {
		t.Error("handle should be absent from the encoded map (nil pointer)")
	}
	if _, ok := obj["score"]; ok {
		t.Error("score should be absent from the encoded map (nil pointer)")
	}
}

func TestPartialField_Encode_IncludesSetPointer(t *testing.T) {
	handle := "alice"
	raw, err := profilePatchCodec.Encode(profilePatch{Handle: &handle})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if obj["handle"] != "alice" {
		t.Errorf("handle = %v, want %q", obj["handle"], "alice")
	}
	if _, ok := obj["score"]; ok {
		t.Error("score should still be absent (was never set)")
	}
}

func TestPartialField_Encode_PropagatesFieldCodecError(t *testing.T) {
	badHandle := "ab" // fails MinLen(3)
	_, err := profilePatchCodec.Encode(profilePatch{Handle: &badHandle})
	if err == nil {
		t.Fatal("Encode: want error for handle failing MinLen(3), got nil")
	}
	var verrs codex.ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("Encode error = %T, want codex.ValidationErrors", err)
	}
}

func TestPartialStruct_Encode_MultipleFieldsSparse(t *testing.T) {
	handle := "alice"
	score := 42
	raw, err := profilePatchCodec.Encode(profilePatch{Handle: &handle, Score: &score})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	if len(obj) != 2 {
		t.Errorf("encoded map has %d keys, want 2: %+v", len(obj), obj)
	}
}

func TestPartialStruct_Encode_AllFieldsNil_ReturnsEmptyMap(t *testing.T) {
	raw, err := profilePatchCodec.Encode(profilePatch{})
	if err != nil {
		t.Fatalf("Encode: want nil error for all-nil patch, got %v", err)
	}
	obj := raw.(map[string]any)
	if len(obj) != 0 {
		t.Errorf("encoded map = %+v, want empty", obj)
	}
}

func TestPartialStruct_Decode_OnlySetsPresentFields(t *testing.T) {
	got, err := profilePatchCodec.Decode(map[string]any{"handle": "bob"})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Handle == nil || *got.Handle != "bob" {
		t.Errorf("Handle = %v, want \"bob\"", got.Handle)
	}
	if got.Score != nil {
		t.Errorf("Score = %v, want nil (key absent)", got.Score)
	}
}

func TestPartialStruct_Decode_RoundTrip(t *testing.T) {
	handle := "carol"
	score := 7
	original := profilePatch{Handle: &handle, Score: &score}

	raw, err := profilePatchCodec.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := profilePatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.Handle == nil || *back.Handle != handle {
		t.Errorf("Handle round-trip = %v, want %q", back.Handle, handle)
	}
	if back.Score == nil || *back.Score != score {
		t.Errorf("Score round-trip = %v, want %d", back.Score, score)
	}
}

func TestPartialStruct_Decode_NonObject_ReturnsTypeMismatchError(t *testing.T) {
	_, err := profilePatchCodec.Decode("not-an-object")
	if err == nil {
		t.Fatal("Decode: want error for non-object input, got nil")
	}
	var tme codex.TypeMismatchError
	if !errors.As(err, &tme) {
		t.Fatalf("Decode error = %T, want codex.TypeMismatchError", err)
	}
	if tme.Expected != "object" {
		t.Errorf("TypeMismatchError.Expected = %q, want %q", tme.Expected, "object")
	}
}

func TestPartialStruct_Decode_FieldCodecError(t *testing.T) {
	// "ab" fails handle's MinLen(3) constraint.
	_, err := profilePatchCodec.Decode(map[string]any{"handle": "ab"})
	if err == nil {
		t.Fatal("Decode: want error for handle failing MinLen(3), got nil")
	}
	var verrs codex.ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("Decode error = %T, want codex.ValidationErrors", err)
	}
}

func TestPartialStruct_Decode_MultipleErrors(t *testing.T) {
	// Both handle (MinLen(3)) and score (RangeInt(0,100)) fail.
	_, err := profilePatchCodec.Decode(map[string]any{"handle": "ab", "score": 999})
	if err == nil {
		t.Fatal("Decode: want error for both fields failing, got nil")
	}
	var verrs codex.ValidationErrors
	if !errors.As(err, &verrs) {
		t.Fatalf("Decode error = %T, want codex.ValidationErrors", err)
	}
	if len(verrs) != 2 {
		t.Errorf("Decode ValidationErrors has %d entries, want 2: %+v", len(verrs), verrs)
	}
}

func TestPartialStruct_Schema_NoFieldsRequired(t *testing.T) {
	s := profilePatchCodec.Schema
	if len(s.Required) != 0 {
		t.Errorf("Schema.Required = %v, want empty", s.Required)
	}
	if len(s.Properties) != 2 {
		t.Errorf("Schema.Properties has %d entries, want 2", len(s.Properties))
	}
}

// ── Nesting: proves PartialField needs zero special-casing for a nested
// PartialStruct-built Codec[F] ─────────────────────────────────────────

type addressPatch struct {
	City *string
}

var addressPatchCodec = codex.PartialStruct[addressPatch](
	codex.PartialField("city",
		codex.String().Refine(validate.NonEmptyString),
		func(a addressPatch) *string { return a.City },
		func(a *addressPatch, v *string) { a.City = v },
	),
)

type personPatch struct {
	Name    *string
	Address *addressPatch
}

var personPatchCodec = codex.PartialStruct[personPatch](
	codex.PartialField("name",
		codex.String().Refine(validate.NonEmptyString),
		func(p personPatch) *string { return p.Name },
		func(p *personPatch, v *string) { p.Name = v },
	),
	codex.PartialField("address", addressPatchCodec,
		func(p personPatch) *addressPatch { return p.Address },
		func(p *personPatch, v *addressPatch) { p.Address = v },
	),
)

func TestPartialField_NestedPartialStruct_ComposesUnchanged(t *testing.T) {
	// nil outer pointer -> omitted entirely.
	raw, err := personPatchCodec.Encode(personPatch{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	if _, ok := obj["address"]; ok {
		t.Error("address should be absent when the outer pointer is nil")
	}

	// non-nil outer pointer -> encodes via the nested PartialStruct's own
	// result, present under the outer key.
	city := "Berlin"
	raw2, err := personPatchCodec.Encode(personPatch{Address: &addressPatch{City: &city}})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj2 := raw2.(map[string]any)
	addr, ok := obj2["address"].(map[string]any)
	if !ok {
		t.Fatalf("address = %v, want a nested object", obj2["address"])
	}
	if addr["city"] != "Berlin" {
		t.Errorf("address.city = %v, want %q", addr["city"], "Berlin")
	}

	// Decode round-trips the nested structure too.
	back, err := personPatchCodec.Decode(raw2)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.Address == nil || back.Address.City == nil || *back.Address.City != "Berlin" {
		t.Errorf("Decode round-trip Address = %+v, want City=Berlin", back.Address)
	}
}
