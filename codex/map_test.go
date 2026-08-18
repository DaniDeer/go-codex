package codex_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

// ── MapCodecSafe ─────────────────────────────────────────────────────────────

type myString string

func TestMapCodecSafe_RoundTrip(t *testing.T) {
	c := codex.MapCodecSafe(
		codex.String(),
		func(s string) myString { return myString(s) },
		func(m myString) (string, error) { return string(m), nil },
	)
	enc, err := c.Encode(myString("hello"))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != myString("hello") {
		t.Errorf("round-trip = %q, want %q", got, "hello")
	}
}

func TestMapCodecSafe_FromError_PropagatedOnEncode(t *testing.T) {
	encodeErr := errors.New("cannot encode empty")
	c := codex.MapCodecSafe(
		codex.String(),
		func(s string) myString { return myString(s) },
		func(m myString) (string, error) {
			if m == "" {
				return "", encodeErr
			}
			return string(m), nil
		},
	)
	_, err := c.Encode(myString(""))
	if err == nil {
		t.Fatal("expected encode error, got nil")
	}
	if !errors.Is(err, encodeErr) {
		t.Errorf("expected wrapped encodeErr, got %v", err)
	}
}

func TestMapCodecSafe_SchemaFromBase(t *testing.T) {
	base := codex.String()
	c := codex.MapCodecSafe(
		base,
		func(s string) myString { return myString(s) },
		func(m myString) (string, error) { return string(m), nil },
	)
	if c.Schema.Type != base.Schema.Type {
		t.Errorf("schema type = %q, want %q", c.Schema.Type, base.Schema.Type)
	}
}

// ── Downcast ──────────────────────────────────────────────────────────────────

type animal interface{ sound() string }
type dog struct{}
type cat struct{}

func (d dog) sound() string { return "woof" }
func (c cat) sound() string { return "meow" }

func TestDowncast_Success(t *testing.T) {
	var a animal = dog{}
	got, err := codex.Downcast[dog](a)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.sound() != "woof" {
		t.Errorf("got %q, want %q", got.sound(), "woof")
	}
}

func TestDowncast_Failure(t *testing.T) {
	var a animal = dog{}
	_, err := codex.Downcast[cat](a)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "codex_test.dog") {
		t.Errorf("error %q does not mention the source type", err.Error())
	}
}

// ── MapCodecValidated ─────────────────────────────────────────────────────────

// positiveString wraps a string guaranteed to be non-empty.
type positiveString string

var positiveStringCodec = codex.MapCodecSafe(
	codex.String().Refine(codex.Constraint[string]{
		Name:    "nonEmpty",
		Check:   func(s string) bool { return s != "" },
		Message: func(s string) string { return "string must not be empty" },
	}),
	func(s string) positiveString { return positiveString(s) },
	func(p positiveString) (string, error) { return string(p), nil },
)

func TestMapCodecValidated_RoundTrip(t *testing.T) {
	c := codex.MapCodecValidated(
		codex.String(),
		positiveStringCodec,
		func(s string) (positiveString, error) {
			if s == "" {
				return "", errors.New("cannot map empty string")
			}
			return positiveString(s), nil
		},
		func(p positiveString) (string, error) { return string(p), nil },
	)

	enc, err := c.Encode(positiveString("hello"))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != positiveString("hello") {
		t.Errorf("round-trip = %q, want %q", got, "hello")
	}
}

func TestMapCodecValidated_ToErrorPropagatedOnDecode(t *testing.T) {
	toErr := errors.New("mapping failed")
	c := codex.MapCodecValidated(
		codex.String(),
		positiveStringCodec,
		func(s string) (positiveString, error) { return "", toErr },
		func(p positiveString) (string, error) { return string(p), nil },
	)
	_, err := c.Decode("anything")
	if !errors.Is(err, toErr) {
		t.Errorf("expected toErr, got %v", err)
	}
}

func TestMapCodecValidated_FromErrorPropagatedOnEncode(t *testing.T) {
	fromErr := errors.New("encode mapping failed")
	c := codex.MapCodecValidated(
		codex.String(),
		positiveStringCodec,
		func(s string) (positiveString, error) { return positiveString(s), nil },
		func(p positiveString) (string, error) { return "", fromErr },
	)
	_, err := c.Encode(positiveString("x"))
	if !errors.Is(err, fromErr) {
		t.Errorf("expected fromErr, got %v", err)
	}
}

func TestMapCodecValidated_ValidateBFailsOnDecode(t *testing.T) {
	// positiveStringCodec rejects empty strings via Refine; the mapped value
	// violates the constraint so cb.Validate must return an error.
	c := codex.MapCodecValidated(
		codex.String(),
		positiveStringCodec,
		func(s string) (positiveString, error) {
			// map any input to empty to trigger cb.Validate failure
			return positiveString(""), nil
		},
		func(p positiveString) (string, error) { return string(p), nil },
	)
	_, err := c.Decode("anything")
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestMapCodecValidated_ValidateBFailsOnEncode(t *testing.T) {
	c := codex.MapCodecValidated(
		codex.String(),
		positiveStringCodec,
		func(s string) (positiveString, error) { return positiveString(s), nil },
		func(p positiveString) (string, error) { return string(p), nil },
	)
	_, err := c.Encode(positiveString(""))
	if err == nil {
		t.Fatal("expected validation error on encode, got nil")
	}
}

func TestMapCodecValidated_SchemaFromCb(t *testing.T) {
	c := codex.MapCodecValidated(
		codex.String(),
		positiveStringCodec,
		func(s string) (positiveString, error) { return positiveString(s), nil },
		func(p positiveString) (string, error) { return string(p), nil },
	)
	if c.Schema.Type != positiveStringCodec.Schema.Type {
		t.Errorf("schema type = %q, want %q", c.Schema.Type, positiveStringCodec.Schema.Type)
	}
}

func TestMapCodecValidated_BaseDecodeErrorPropagated(t *testing.T) {
	c := codex.MapCodecValidated(
		codex.Int(),
		codex.MapCodecSafe(
			codex.Int(),
			func(n int) int { return n },
			func(n int) (int, error) { return n, nil },
		),
		func(n int) (int, error) { return n, nil },
		func(n int) (int, error) { return n, nil },
	)
	_, err := c.Decode("not-an-int")
	if err == nil {
		t.Fatal("expected base decode error, got nil")
	}
	if !strings.Contains(err.Error(), "expected") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ── PrefixedKeyCodec ─────────────────────────────────────────────────────────

// moduleName is a named string type standing in for a real caller's
// domain-specific identifier (e.g. manifesttemplate.ModuleName).
type moduleName string

var moduleNameConstraint = codex.Constraint[string]{
	Name:    "module-name",
	Check:   func(s string) bool { return s != "" && !strings.Contains(s, " ") },
	Message: func(s string) string { return fmt.Sprintf("module name %q must be non-empty and contain no spaces", s) },
}

var testModuleKeyCodec = codex.PrefixedKeyCodec[moduleName]("properties.desired.modules.", moduleNameConstraint)

func TestPrefixedKeyCodec_RoundTrip(t *testing.T) {
	enc, err := testModuleKeyCodec.Encode(moduleName("cv-writer"))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc != "properties.desired.modules.cv-writer" {
		t.Errorf("Encode = %v, want properties.desired.modules.cv-writer", enc)
	}
	got, err := testModuleKeyCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != moduleName("cv-writer") {
		t.Errorf("round-trip = %q, want cv-writer", got)
	}
}

func TestPrefixedKeyCodec_DecodeError_MissingPrefix(t *testing.T) {
	_, err := testModuleKeyCodec.Decode("wrong.prefix.cv-writer")
	if err == nil {
		t.Fatal("expected error for missing prefix, got nil")
	}
}

func TestPrefixedKeyCodec_DecodeError_EmptySuffix(t *testing.T) {
	_, err := testModuleKeyCodec.Decode("properties.desired.modules.")
	if err == nil {
		t.Fatal("expected error for empty name suffix, got nil")
	}
}

func TestPrefixedKeyCodec_EncodeError_NameConstraintFails(t *testing.T) {
	_, err := testModuleKeyCodec.Encode(moduleName("has space"))
	if err == nil {
		t.Fatal("expected error for name constraint failure, got nil")
	}
}

func TestPrefixedKeyCodec_SchemaCarriesStringType(t *testing.T) {
	if testModuleKeyCodec.Schema.Type != "string" {
		t.Errorf("Schema.Type = %q, want string", testModuleKeyCodec.Schema.Type)
	}
}

func ExamplePrefixedKeyCodec() {
	type routeName string

	nameConstraint := codex.Constraint[string]{
		Name:    "route-name",
		Check:   func(s string) bool { return s != "" },
		Message: func(s string) string { return "route name must not be empty" },
	}
	routeKeyCodec := codex.PrefixedKeyCodec[routeName]("properties.desired.routes.", nameConstraint)

	name, err := routeKeyCodec.Decode("properties.desired.routes.factory-mqtt-to-ingest")
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(name)
	// Output: factory-mqtt-to-ingest
}
