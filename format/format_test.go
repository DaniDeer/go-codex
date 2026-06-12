package format_test

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
)

var testCodec = codex.Struct[struct{ N int }](
	codex.Field[struct{ N int }, int]{
		Name:     "n",
		Codec:    codex.Int().Refine(validate.PositiveInt),
		Get:      func(v struct{ N int }) int { return v.N },
		Set:      func(v *struct{ N int }, x int) { v.N = x },
		Required: true,
	},
)

func TestFormatValidate_PassesValid(t *testing.T) {
	f := format.JSON(testCodec)
	if err := f.Validate(struct{ N int }{N: 1}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestFormatValidate_FailsInvalid(t *testing.T) {
	f := format.JSON(testCodec)
	err := f.Validate(struct{ N int }{N: -1})
	if err == nil {
		t.Fatal("expected constraint error, got nil")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected constraint name in error, got: %v", err)
	}
}

func TestFormatValidate_SameResultAcrossFormats(t *testing.T) {
	// Validate is format-independent — result must be identical for all formats.
	v := struct{ N int }{N: -5}
	errJSON := format.JSON(testCodec).Validate(v)
	errYAML := format.YAML(testCodec).Validate(v)
	errTOML := format.TOML(testCodec).Validate(v)
	errGob := format.Gob(testCodec).Validate(v)

	for label, err := range map[string]error{"JSON": errJSON, "YAML": errYAML, "TOML": errTOML, "Gob": errGob} {
		if err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}

func TestFormatValidate_MarshalValidates(t *testing.T) {
	// Marshal calls Encode, which now validates Refine constraints symmetrically.
	f := format.JSON(testCodec)
	_, err := f.Marshal(struct{ N int }{N: -1})
	if err == nil {
		t.Fatal("Marshal should validate constraints, got no error")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected constraint name in error, got: %v", err)
	}
	// Valid value marshals without error.
	data, err := f.Marshal(struct{ N int }{N: 5})
	if err != nil {
		t.Fatalf("Marshal with valid value should succeed, got: %v", err)
	}
	if string(data) != `{"n":5}` {
		t.Errorf("unexpected marshal output: %s", data)
	}
}

func TestNewTyped_MarshalValidatesAndRenders(t *testing.T) {
	// NewTyped.Marshal runs codec validation before calling the typed marshal fn.
	var rendered string
	f := format.NewTyped(
		testCodec,
		func(v struct{ N int }) ([]byte, error) {
			rendered = fmt.Sprintf("<p>%d</p>", v.N)
			return []byte(rendered), nil
		},
		func([]byte) (struct{ N int }, error) { return struct{ N int }{}, nil },
		"text/html; charset=utf-8",
	)

	if f.ContentType() != "text/html; charset=utf-8" {
		t.Errorf("unexpected content type: %s", f.ContentType())
	}

	// Invalid value: Refine constraint should reject before marshal fn is called.
	_, err := f.Marshal(struct{ N int }{N: -1})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if rendered != "" {
		t.Error("marshal fn should not be called when validation fails")
	}

	// Valid value: marshal fn should be called.
	data, err := f.Marshal(struct{ N int }{N: 7})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != "<p>7</p>" {
		t.Errorf("unexpected output: %s", data)
	}
}

func TestNewTyped_Validate(t *testing.T) {
	f := format.NewTyped(
		testCodec,
		func(v struct{ N int }) ([]byte, error) { return nil, nil },
		func([]byte) (struct{ N int }, error) { return struct{ N int }{}, nil },
		"text/html",
	)
	if err := f.Validate(struct{ N int }{N: 5}); err != nil {
		t.Errorf("expected no error for valid value, got: %v", err)
	}
	if err := f.Validate(struct{ N int }{N: 0}); err == nil {
		t.Error("expected constraint error for zero value, got nil")
	}
}

func TestNewTyped_UnmarshalTyped(t *testing.T) {
	called := false
	f := format.NewTyped(
		testCodec,
		func(v struct{ N int }) ([]byte, error) { return nil, nil },
		func([]byte) (struct{ N int }, error) {
			called = true
			return struct{ N int }{N: 42}, nil
		},
		"text/html",
	)
	v, err := f.Unmarshal([]byte("ignored"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("typed unmarshal fn should have been called")
	}
	if v.N != 42 {
		t.Errorf("expected N=42, got %d", v.N)
	}
}

func TestNewStreamed_IsStreamable(t *testing.T) {
	f := format.NewStreamed(testCodec,
		func(v struct{ N int }, w io.Writer) error { return nil },
		func([]byte) (struct{ N int }, error) { return struct{ N int }{}, nil },
		"text/csv",
	)
	if !f.IsStreamable() {
		t.Error("expected IsStreamable() == true for NewStreamed format")
	}
	if ct := f.ContentType(); ct != "text/csv" {
		t.Errorf("expected ContentType text/csv, got %q", ct)
	}
}

func TestNewStreamed_NonStreamable(t *testing.T) {
	f := format.JSON(testCodec)
	if f.IsStreamable() {
		t.Error("expected IsStreamable() == false for JSON format")
	}
	if err := f.MarshalTo(struct{ N int }{N: 1}, io.Discard); !errors.Is(err, format.ErrNotStreamable) {
		t.Errorf("expected ErrNotStreamable, got %v", err)
	}
}

func TestNewStreamed_MarshalTo_WritesToWriter(t *testing.T) {
	f := format.NewStreamed(testCodec,
		func(v struct{ N int }, w io.Writer) error {
			_, err := fmt.Fprintf(w, "n=%d", v.N)
			return err
		},
		func([]byte) (struct{ N int }, error) { return struct{ N int }{}, nil },
		"text/plain",
	)
	var buf strings.Builder
	if err := f.MarshalTo(struct{ N int }{N: 7}, &buf); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "n=7" {
		t.Errorf("expected n=7, got %q", buf.String())
	}
}

func TestNewStreamed_MarshalTo_ValidatesBeforeWriting(t *testing.T) {
	written := false
	f := format.NewStreamed(testCodec,
		func(v struct{ N int }, w io.Writer) error {
			written = true
			_, err := fmt.Fprintf(w, "n=%d", v.N)
			return err
		},
		func([]byte) (struct{ N int }, error) { return struct{ N int }{}, nil },
		"text/plain",
	)
	// N=0 violates PositiveInt constraint
	err := f.MarshalTo(struct{ N int }{N: 0}, io.Discard)
	if err == nil {
		t.Fatal("expected validation error for N=0")
	}
	if written {
		t.Error("marshal function must not be called when validation fails")
	}
}

func TestNewStreamed_Unmarshal(t *testing.T) {
	called := false
	f := format.NewStreamed(testCodec,
		func(v struct{ N int }, w io.Writer) error { return nil },
		func([]byte) (struct{ N int }, error) {
			called = true
			return struct{ N int }{N: 5}, nil
		},
		"text/plain",
	)
	v, err := f.Unmarshal([]byte("ignored"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("unmarshal fn should have been called")
	}
	if v.N != 5 {
		t.Errorf("expected N=5, got %d", v.N)
	}
}

func TestGob_ContentType(t *testing.T) {
	f := format.Gob(testCodec)
	if ct := f.ContentType(); ct != "application/gob" {
		t.Errorf("expected application/gob, got %q", ct)
	}
}

func TestGob_RoundTrip(t *testing.T) {
	f := format.Gob(testCodec)
	original := struct{ N int }{N: 42}
	data, err := f.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	decoded, err := f.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if decoded.N != original.N {
		t.Errorf("round-trip mismatch: got N=%d, want N=%d", decoded.N, original.N)
	}
}

func TestGob_MarshalValidatesConstraints(t *testing.T) {
	f := format.Gob(testCodec)
	_, err := f.Marshal(struct{ N int }{N: -1})
	if err == nil {
		t.Fatal("expected constraint error, got nil")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected constraint name in error, got: %v", err)
	}
}

func TestGob_UnmarshalValidatesConstraints(t *testing.T) {
	// Encode a value that violates the codec constraint directly via gob
	// (bypassing format.Gob.Marshal), then unmarshal through format.Gob
	// to verify that constraints are enforced on decode.
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(struct{ N int }{N: -3}); err != nil {
		t.Fatalf("raw gob encode error: %v", err)
	}
	f := format.Gob(testCodec)
	_, err := f.Unmarshal(buf.Bytes())
	if err == nil {
		t.Fatal("expected constraint error on unmarshal, got nil")
	}
	if !strings.Contains(err.Error(), "positive") {
		t.Errorf("expected constraint name in error, got: %v", err)
	}
}

func TestGob_DecodeError(t *testing.T) {
	f := format.Gob(testCodec)
	_, err := f.Unmarshal([]byte("not valid gob bytes"))
	if err == nil {
		t.Fatal("expected gob decode error, got nil")
	}
	if !strings.Contains(err.Error(), "gob:") {
		t.Errorf("expected error prefixed with gob:, got: %v", err)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleJSON() {
	type Item struct {
		Name  string
		Price float64
	}

	itemCodec := codex.Struct[Item](
		codex.RequiredField("name", codex.String(),
			func(i Item) string { return i.Name },
			func(i *Item, v string) { i.Name = v },
		),
		codex.RequiredField("price", codex.Float64(),
			func(i Item) float64 { return i.Price },
			func(i *Item, v float64) { i.Price = v },
		),
	)

	j := format.JSON(itemCodec)

	// Marshal a Go value to JSON bytes.
	data, _ := j.Marshal(Item{Name: "Widget", Price: 9.99})
	fmt.Println(string(data))

	// Unmarshal JSON bytes back to the typed value.
	item, _ := j.Unmarshal(data)
	fmt.Printf("%s: %.2f\n", item.Name, item.Price)
	// Output:
	// {"name":"Widget","price":9.99}
	// Widget: 9.99
}
