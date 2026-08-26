package codex_test

import (
	"fmt"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestMaybe_ZeroValueIsNothing(t *testing.T) {
	var m codex.Maybe[string]
	if m.IsSet() {
		t.Error("zero-valued Maybe[string] should report IsSet() == false")
	}
	if m.Get() != "" {
		t.Errorf("Get() = %q, want zero value", m.Get())
	}
}

func TestMaybe_JustConstructsSet(t *testing.T) {
	m := codex.Just("bob")
	if !m.IsSet() {
		t.Error("Just(v) should report IsSet() == true")
	}
	if m.Get() != "bob" {
		t.Errorf("Get() = %q, want bob", m.Get())
	}
}

func TestMaybe_NothingConstructsUnset(t *testing.T) {
	m := codex.Nothing[string]()
	if m.IsSet() {
		t.Error("Nothing[string]() should report IsSet() == false")
	}
	if m != (codex.Maybe[string]{}) {
		t.Error("Nothing[string]() should equal the zero value Maybe[string]{}")
	}
}

func TestMaybe_SetMarksPresent(t *testing.T) {
	var m codex.Maybe[int]
	m.Set(42)
	if !m.IsSet() {
		t.Error("Set(v) should mark IsSet() == true")
	}
	if m.Get() != 42 {
		t.Errorf("Get() = %d, want 42", m.Get())
	}
}

func TestMaybe_SetRepeatable(t *testing.T) {
	var m codex.Maybe[int]
	m.Set(1)
	m.Set(2)
	m.Set(3)
	if !m.IsSet() {
		t.Error("IsSet() should be true after repeated Set calls")
	}
	if m.Get() != 3 {
		t.Errorf("Get() = %d, want 3 (most recent Set)", m.Get())
	}
}

func TestMaybe_TryGet_UnsetReturnsFalse(t *testing.T) {
	var m codex.Maybe[string]
	v, ok := m.TryGet()
	if ok {
		t.Error("TryGet() ok = true, want false (never set)")
	}
	if v != "" {
		t.Errorf("TryGet() value = %q, want zero value", v)
	}
}

func TestMaybe_TryGet_SetReturnsTrue(t *testing.T) {
	m := codex.Just("hi")
	v, ok := m.TryGet()
	if !ok {
		t.Error("TryGet() ok = false, want true")
	}
	if v != "hi" {
		t.Errorf("TryGet() value = %q, want hi", v)
	}
}

func TestMaybeMap_AppliesFnWhenSet(t *testing.T) {
	m := codex.Just(3)
	r := codex.MaybeMap(m, func(v int) string { return fmt.Sprintf("n=%d", v) })
	if !r.IsSet() {
		t.Error("MaybeMap(Just(v), fn) should be set")
	}
	if r.Get() != "n=3" {
		t.Errorf("Get() = %q, want n=3", r.Get())
	}
}

func TestMaybeMap_SkipsFnWhenNothing(t *testing.T) {
	called := false
	m := codex.Nothing[int]()
	r := codex.MaybeMap(m, func(v int) string {
		called = true
		return "unreachable"
	})
	if r.IsSet() {
		t.Error("MaybeMap(Nothing, fn) should stay unset")
	}
	if called {
		t.Error("fn should never be called for Nothing")
	}
}

func TestMaybeFlatMap_AppliesFnWhenSet(t *testing.T) {
	m := codex.Just(3)
	r := codex.MaybeFlatMap(m, func(v int) codex.Maybe[string] {
		return codex.Just(fmt.Sprintf("n=%d", v))
	})
	if !r.IsSet() {
		t.Error("MaybeFlatMap(Just(v), fn) should be set")
	}
	if r.Get() != "n=3" {
		t.Errorf("Get() = %q, want n=3", r.Get())
	}
}

func TestMaybeFlatMap_SkipsFnWhenNothing(t *testing.T) {
	called := false
	m := codex.Nothing[int]()
	r := codex.MaybeFlatMap(m, func(v int) codex.Maybe[string] {
		called = true
		return codex.Just("unreachable")
	})
	if r.IsSet() {
		t.Error("MaybeFlatMap(Nothing, fn) should stay unset")
	}
	if called {
		t.Error("fn should never be called for Nothing")
	}
}

func TestMaybeFlatMap_FnCanProduceNothing(t *testing.T) {
	// The key differentiator vs. MaybeMap: fn itself can turn a Just into
	// a Nothing (e.g. a possibly-failing parse), which MaybeMap's plain
	// func(T) R signature cannot express.
	m := codex.Just("not-a-number")
	r := codex.MaybeFlatMap(m, func(s string) codex.Maybe[int] {
		return codex.Nothing[int]() // simulate a failed parse
	})
	if r.IsSet() {
		t.Error("MaybeFlatMap should propagate a Nothing produced by fn")
	}
}

func TestOrElse_ReturnsValueWhenSet(t *testing.T) {
	m := codex.Just("value")
	if got := m.OrElse("fallback"); got != "value" {
		t.Errorf("OrElse = %q, want value", got)
	}
}

func TestOrElse_ReturnsFallbackWhenNothing(t *testing.T) {
	m := codex.Nothing[string]()
	if got := m.OrElse("fallback"); got != "fallback" {
		t.Errorf("OrElse = %q, want fallback", got)
	}
}

func TestFilter_KeepsWhenSetAndPredicateTrue(t *testing.T) {
	m := codex.Just(10)
	r := m.Filter(func(v int) bool { return v > 5 })
	if !r.IsSet() || r.Get() != 10 {
		t.Errorf("Filter result = %+v, want Just(10)", r)
	}
}

func TestFilter_ReturnsNothingWhenPredicateFalse(t *testing.T) {
	m := codex.Just(3)
	r := m.Filter(func(v int) bool { return v > 5 })
	if r.IsSet() {
		t.Error("Filter should return Nothing when predicate is false")
	}
}

func TestFilter_ReturnsNothingWhenAlreadyNothing(t *testing.T) {
	called := false
	m := codex.Nothing[int]()
	r := m.Filter(func(v int) bool {
		called = true
		return true
	})
	if r.IsSet() {
		t.Error("Filter on Nothing should stay Nothing")
	}
	if called {
		t.Error("predicate should never be called for Nothing")
	}
}

// ── MaybeField / Struct integration ─────────────────────────────────────────

type maybeDoc struct {
	Name string
	Nick codex.Maybe[string]
}

func maybeDocCodec() codex.Codec[maybeDoc] {
	return codex.Struct[maybeDoc](
		codex.RequiredField("name", codex.String(),
			func(d maybeDoc) string { return d.Name },
			func(d *maybeDoc, v string) { d.Name = v }),
		codex.MaybeField("nick", codex.String(),
			func(d maybeDoc) codex.Maybe[string] { return d.Nick },
			func(d *maybeDoc, v codex.Maybe[string]) { d.Nick = v }),
	)
}

func TestMaybeField_EncodeOmitsWhenNeverSet(t *testing.T) {
	raw, err := maybeDocCodec().Encode(maybeDoc{Name: "x"})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if _, present := raw.(map[string]any)["nick"]; present {
		t.Error(`"nick" should be absent (never Set)`)
	}
}

func TestMaybeField_EncodeIncludesWhenSetToZeroValue(t *testing.T) {
	// The key differentiator vs. OmitEmptyField: explicitly Set to the
	// zero value ("") still encodes the key, unlike a heuristic check.
	doc := maybeDoc{Name: "x"}
	doc.Nick.Set("")
	raw, err := maybeDocCodec().Encode(doc)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	nick, present := obj["nick"]
	if !present {
		t.Fatal(`"nick" should be PRESENT (explicitly Set to "", even though that's the zero value)`)
	}
	if nick != "" {
		t.Errorf(`obj["nick"] = %v, want ""`, nick)
	}
}

func TestMaybeField_DecodePresentKeySetsMaybe(t *testing.T) {
	d, err := maybeDocCodec().Decode(map[string]any{"name": "x", "nick": "bob"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if !d.Nick.IsSet() || d.Nick.Get() != "bob" {
		t.Errorf("Nick = %+v, want Just(bob)", d.Nick)
	}
}

func TestMaybeField_DecodeAbsentKeyLeavesNothing(t *testing.T) {
	d, err := maybeDocCodec().Decode(map[string]any{"name": "x"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if d.Nick.IsSet() {
		t.Error("Nick should be Nothing (unset) when the key is absent")
	}
}

func TestStruct_MixesMaybeAndOtherFieldKinds(t *testing.T) {
	type mixed struct {
		Req    string
		Opt    string
		Def    string
		OmitEm string
		Mby    codex.Maybe[string]
	}
	mixedCodec := codex.Struct[mixed](
		codex.RequiredField("req", codex.String(),
			func(m mixed) string { return m.Req },
			func(m *mixed, v string) { m.Req = v }),
		codex.OptionalField("opt", codex.String(),
			func(m mixed) string { return m.Opt },
			func(m *mixed, v string) { m.Opt = v }),
		codex.DefaultField("def", codex.String(), "default",
			func(m mixed) string { return m.Def },
			func(m *mixed, v string) { m.Def = v }),
		codex.OmitEmptyField("omit_em", codex.String(),
			func(m mixed) string { return m.OmitEm },
			func(m *mixed, v string) { m.OmitEm = v }),
		codex.MaybeField("mby", codex.String(),
			func(m mixed) codex.Maybe[string] { return m.Mby },
			func(m *mixed, v codex.Maybe[string]) { m.Mby = v }),
	)

	v := mixed{Req: "r"}
	v.Mby.Set("")
	raw, err := mixedCodec.Encode(v)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if obj["req"] != "r" {
		t.Errorf("req = %v, want r", obj["req"])
	}
	if _, ok := obj["opt"]; !ok {
		t.Error("opt should be present (OptionalField always encodes)")
	}
	if _, ok := obj["def"]; !ok {
		t.Error("def should be present (DefaultField always encodes)")
	}
	if _, ok := obj["omit_em"]; ok {
		t.Error("omit_em should be absent (OmitEmptyField, zero value)")
	}
	if _, ok := obj["mby"]; !ok {
		t.Error("mby should be present (MaybeField, explicitly Set even though to zero value)")
	}
}

func TestMaybeField_EncodeVarsIgnoresSparseRule(t *testing.T) {
	// EncodeVars calls a field's plain encode() directly, NOT Struct's
	// sparse-aware Encode loop -- confirms the omit rule is exclusive to
	// Struct, per docs/concepts/codec.md's "Interaction with
	// Template/DottedKeyCodec/DecodeVars/EncodeVars" paragraph in the
	// Maybe[T] subsection (mirrors OmitEmptyField's identical caveat).
	field := codex.MaybeField("nick", codex.String(),
		func(d maybeDoc) codex.Maybe[string] { return d.Nick },
		func(d *maybeDoc, v codex.Maybe[string]) { d.Nick = v })

	vars, err := codex.EncodeVars(maybeDoc{}, field)
	if err != nil {
		t.Fatalf("EncodeVars: unexpected error: %v", err)
	}
	if _, ok := vars["nick"]; !ok {
		t.Error(`vars["nick"] absent -- EncodeVars must still encode a Nothing Maybe as V's zero value (sparse rule is Struct-loop-exclusive)`)
	}
}

// ── MaybeCodec ───────────────────────────────────────────────────────────

func TestMaybeCodec_DecodeEncodeRoundTrip(t *testing.T) {
	c := codex.MaybeCodec(codex.String())

	just, err := c.Decode("hi")
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if !just.IsSet() || just.Get() != "hi" {
		t.Errorf("Decode(%q) = %+v, want Just(hi)", "hi", just)
	}

	raw, err := c.Encode(just)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if raw != "hi" {
		t.Errorf("Encode(Just(hi)) = %v, want hi", raw)
	}

	// Encode of Nothing renders inner's zero value -- NOT omitted, unlike
	// MaybeField's sparse behavior (MaybeCodec alone has no omission).
	nothingRaw, err := c.Encode(codex.Nothing[string]())
	if err != nil {
		t.Fatalf("Encode(Nothing): unexpected error: %v", err)
	}
	if nothingRaw != "" {
		t.Errorf("Encode(Nothing) = %v, want empty string (inner's zero value)", nothingRaw)
	}
}

func TestMaybeCodec_ComposesWithSliceOf(t *testing.T) {
	c := codex.SliceOf(codex.MaybeCodec(codex.Int()))
	decoded, err := c.Decode([]any{1, 2, 3})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if len(decoded) != 3 || !decoded[0].IsSet() || decoded[0].Get() != 1 {
		t.Errorf("Decode = %+v, want three Just values", decoded)
	}
}

func TestMaybeCodec_ComposesWithRequiredField_NoOmission(t *testing.T) {
	type doc struct {
		Note codex.Maybe[string]
	}
	docCodec := codex.Struct[doc](
		codex.RequiredField("note", codex.MaybeCodec(codex.String()),
			func(d doc) codex.Maybe[string] { return d.Note },
			func(d *doc, v codex.Maybe[string]) { d.Note = v }),
	)

	// Absent key -> RequiredField fails to decode (Maybe's Nothing does
	// NOT make the FIELD itself optional -- that's MaybeField's job).
	if _, err := docCodec.Decode(map[string]any{}); err == nil {
		t.Error("Decode with absent key should fail (RequiredField, not MaybeField)")
	}

	d, err := docCodec.Decode(map[string]any{"note": "hi"})
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if !d.Note.IsSet() || d.Note.Get() != "hi" {
		t.Errorf("Note = %+v, want Just(hi)", d.Note)
	}

	// Encode ALWAYS shows the key, even for a Nothing -- the key
	// differentiator vs. MaybeField's omission behavior.
	raw, err := docCodec.Encode(doc{})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	if _, present := raw.(map[string]any)["note"]; !present {
		t.Error(`"note" should be PRESENT even for Nothing -- RequiredField+MaybeCodec never omits`)
	}
}

func TestMaybeField_EquivalentToOmitEmptyFieldFuncPlusMaybeCodec(t *testing.T) {
	type doc struct {
		Nick codex.Maybe[string]
	}
	get := func(d doc) codex.Maybe[string] { return d.Nick }
	set := func(d *doc, v codex.Maybe[string]) { d.Nick = v }

	viaMaybeField := codex.Struct[doc](
		codex.MaybeField("nick", codex.String(), get, set),
	)
	viaComposition := codex.Struct[doc](
		codex.OmitEmptyFieldFunc("nick", codex.MaybeCodec(codex.String()), get, set,
			func(m codex.Maybe[string]) bool { return !m.IsSet() }),
	)

	for _, tc := range []struct {
		name string
		d    doc
	}{
		{"never set", doc{}},
		{"set to zero value", func() doc { var d doc; d.Nick.Set(""); return d }()},
		{"set to a value", func() doc { var d doc; d.Nick.Set("bob"); return d }()},
	} {
		raw1, err1 := viaMaybeField.Encode(tc.d)
		raw2, err2 := viaComposition.Encode(tc.d)
		if (err1 == nil) != (err2 == nil) {
			t.Errorf("%s: error mismatch: MaybeField=%v, composition=%v", tc.name, err1, err2)
		}
		obj1, obj2 := raw1.(map[string]any), raw2.(map[string]any)
		_, present1 := obj1["nick"]
		_, present2 := obj2["nick"]
		if present1 != present2 {
			t.Errorf("%s: presence mismatch: MaybeField=%v, composition=%v", tc.name, present1, present2)
		}
		if present1 && obj1["nick"] != obj2["nick"] {
			t.Errorf("%s: value mismatch: MaybeField=%v, composition=%v", tc.name, obj1["nick"], obj2["nick"])
		}
	}
}

// ── Examples ─────────────────────────────────────────────────────────────

func ExampleMaybe() {
	var nick codex.Maybe[string]
	fmt.Println(nick.IsSet())
	nick.Set("bob")
	fmt.Println(nick.IsSet(), nick.Get())
	// Output:
	// false
	// true bob
}

func ExampleMaybeField() {
	type Profile struct {
		Name string
		Nick codex.Maybe[string]
	}
	profileCodec := codex.Struct[Profile](
		codex.RequiredField("name", codex.String(),
			func(p Profile) string { return p.Name },
			func(p *Profile, v string) { p.Name = v }),
		codex.MaybeField("nick", codex.String(),
			func(p Profile) codex.Maybe[string] { return p.Nick },
			func(p *Profile, v codex.Maybe[string]) { p.Nick = v }),
	)

	raw, _ := profileCodec.Encode(Profile{Name: "alice"})
	obj := raw.(map[string]any)
	_, hasNick := obj["nick"]
	fmt.Println(hasNick)
	// Output: false
}
