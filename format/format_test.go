package format_test

import (
	"fmt"
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
	// Validate is format-independent — result must be identical for all three.
	v := struct{ N int }{N: -5}
	errJSON := format.JSON(testCodec).Validate(v)
	errYAML := format.YAML(testCodec).Validate(v)
	errTOML := format.TOML(testCodec).Validate(v)

	for label, err := range map[string]error{"JSON": errJSON, "YAML": errYAML, "TOML": errTOML} {
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
