package format_test

import (
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

func TestFormatValidate_MarshalDoesNotValidate(t *testing.T) {
	// Marshal is intentionally unconstrained — encode direction is trusted.
	f := format.JSON(testCodec)
	_, err := f.Marshal(struct{ N int }{N: -1})
	if err != nil {
		t.Fatalf("Marshal should not validate constraints, got: %v", err)
	}
}
