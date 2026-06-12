package codex_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

type point struct {
	X int
	Y int
}

func pointCodec() codex.Codec[point] {
	return codex.Struct[point](
		codex.Field[point, int]{
			Name:     "x",
			Codec:    codex.Int(),
			Get:      func(p point) int { return p.X },
			Set:      func(p *point, v int) { p.X = v },
			Required: true,
		},
		codex.Field[point, int]{
			Name:     "y",
			Codec:    codex.Int(),
			Get:      func(p point) int { return p.Y },
			Set:      func(p *point, v int) { p.Y = v },
			Required: false,
		},
	)
}

func TestStruct_DecodeRequiredPresent(t *testing.T) {
	c := pointCodec()
	got, err := c.Decode(map[string]any{"x": 3, "y": 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 3 || got.Y != 4 {
		t.Errorf("got %+v, want {X:3 Y:4}", got)
	}
}

func TestStruct_DecodeRequiredMissing(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode(map[string]any{"y": 4})
	if err == nil {
		t.Fatal("expected error for missing required field")
	}
	if !strings.Contains(err.Error(), "x") {
		t.Errorf("error %q does not name the missing field", err.Error())
	}
}

func TestStruct_DecodeOptionalMissing(t *testing.T) {
	c := pointCodec()
	got, err := c.Decode(map[string]any{"x": 5})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.X != 5 || got.Y != 0 {
		t.Errorf("got %+v, want {X:5 Y:0}", got)
	}
}

func TestStruct_DecodeFieldWrongType(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode(map[string]any{"x": "not-a-number", "y": 1})
	if err == nil {
		t.Fatal("expected error for wrong field type")
	}
	if !strings.Contains(err.Error(), "field x") {
		t.Errorf("error %q does not include field path", err.Error())
	}
}

func TestStruct_DecodeNonObject(t *testing.T) {
	c := pointCodec()
	_, err := c.Decode("not-an-object")
	if err == nil {
		t.Fatal("expected error for non-object input")
	}
}

func TestStruct_Encode(t *testing.T) {
	c := pointCodec()
	enc, err := c.Encode(point{X: 1, Y: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("encoded value is not a map: %T", enc)
	}
	if m["x"] != 1 || m["y"] != 2 {
		t.Errorf("encoded map = %v, want {x:1 y:2}", m)
	}
}

func TestStruct_Schema(t *testing.T) {
	c := pointCodec()
	s := c.Schema
	if s.Type != "object" {
		t.Errorf("schema type = %q, want %q", s.Type, "object")
	}
	if _, ok := s.Prop("x"); !ok {
		t.Error("schema missing property 'x'")
	}
	if _, ok := s.Prop("y"); !ok {
		t.Error("schema missing property 'y'")
	}
	// Only 'x' is required (Required: true); 'y' is optional.
	found := false
	for _, r := range s.Required {
		if r == "x" {
			found = true
		}
	}
	if !found {
		t.Errorf("required list %v does not include 'x'", s.Required)
	}
}

func TestStruct_RoundTrip(t *testing.T) {
	c := pointCodec()
	original := point{X: 10, Y: 20}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %+v, want %+v", got, original)
	}
}

func TestRequiredField_SetsRequired(t *testing.T) {
	f := codex.RequiredField("x", codex.Int(),
		func(p point) int { return p.X },
		func(p *point, v int) { p.X = v },
	)
	if !f.Required {
		t.Error("RequiredField: want Required=true")
	}
	if f.Name != "x" {
		t.Errorf("RequiredField: want Name=x, got %q", f.Name)
	}
}

func TestOptionalField_NotRequired(t *testing.T) {
	f := codex.OptionalField("y", codex.Int(),
		func(p point) int { return p.Y },
		func(p *point, v int) { p.Y = v },
	)
	if f.Required {
		t.Error("OptionalField: want Required=false")
	}
}

func TestRequiredField_RoundTrip(t *testing.T) {
	c := codex.Struct[point](
		codex.RequiredField("x", codex.Int(),
			func(p point) int { return p.X },
			func(p *point, v int) { p.X = v },
		),
		codex.OptionalField("y", codex.Int(),
			func(p point) int { return p.Y },
			func(p *point, v int) { p.Y = v },
		),
	)
	original := point{X: 3, Y: 7}
	enc, err := c.Encode(original)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Decode(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != original {
		t.Errorf("round-trip = %+v, want %+v", got, original)
	}
}

func TestStruct_DecodeMultipleErrors(t *testing.T) {
	// codec with 2 required fields
	type pair struct{ A, B int }
	c := codex.Struct[pair](
		codex.RequiredField("a", codex.Int(),
			func(p pair) int { return p.A },
			func(p *pair, v int) { p.A = v },
		),
		codex.RequiredField("b", codex.Int(),
			func(p pair) int { return p.B },
			func(p *pair, v int) { p.B = v },
		),
	)

	// both required fields missing
	_, err := c.Decode(map[string]any{})
	if err == nil {
		t.Fatal("expected error for two missing required fields")
	}

	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	if len(ve) != 2 {
		t.Errorf("expected 2 validation errors, got %d: %v", len(ve), ve)
	}

	msg := err.Error()
	if !strings.Contains(msg, "field a") {
		t.Errorf("error %q does not mention field a", msg)
	}
	if !strings.Contains(msg, "field b") {
		t.Errorf("error %q does not mention field b", msg)
	}
}

// ── DefaultField ──────────────────────────────────────────────────────────────

func TestDefaultField_UsesDefaultWhenAbsent(t *testing.T) {
	type Config struct{ LogLevel string }
	c := codex.Struct[Config](
		codex.DefaultField("log_level", codex.String(), "info",
			func(cfg Config) string { return cfg.LogLevel },
			func(cfg *Config, v string) { cfg.LogLevel = v },
		),
	)

	got, err := c.Decode(map[string]any{})
	if err != nil {
		t.Fatalf("decode empty map: %v", err)
	}
	if got.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", got.LogLevel, "info")
	}
}

func TestDefaultField_PresentValueOverridesDefault(t *testing.T) {
	type Config struct{ LogLevel string }
	c := codex.Struct[Config](
		codex.DefaultField("log_level", codex.String(), "info",
			func(cfg Config) string { return cfg.LogLevel },
			func(cfg *Config, v string) { cfg.LogLevel = v },
		),
	)

	got, err := c.Decode(map[string]any{"log_level": "debug"})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", got.LogLevel, "debug")
	}
}

func TestDefaultField_ZeroValueDefault(t *testing.T) {
	type Config struct{ Timeout int }
	c := codex.Struct[Config](
		codex.DefaultField("timeout", codex.Int(), 0,
			func(cfg Config) int { return cfg.Timeout },
			func(cfg *Config, v int) { cfg.Timeout = v },
		),
	)

	got, err := c.Decode(map[string]any{})
	if err != nil {
		t.Fatalf("decode empty map: %v", err)
	}
	if got.Timeout != 0 {
		t.Errorf("Timeout = %d, want 0", got.Timeout)
	}
}

func TestDefaultField_SchemaContainsDefault(t *testing.T) {
	type Config struct{ LogLevel string }
	c := codex.Struct[Config](
		codex.DefaultField("log_level", codex.String(), "info",
			func(cfg Config) string { return cfg.LogLevel },
			func(cfg *Config, v string) { cfg.LogLevel = v },
		),
	)

	prop, ok := c.Schema.Prop("log_level")
	if !ok {
		t.Fatal("expected log_level in schema properties")
	}
	if prop.Default != "info" {
		t.Errorf("Schema.Default = %v, want %q", prop.Default, "info")
	}
}

func TestDefaultField_RequiredIsFalse(t *testing.T) {
	type Config struct{ X int }
	f := codex.DefaultField("x", codex.Int(), 42,
		func(c Config) int { return c.X },
		func(c *Config, v int) { c.X = v },
	)
	if f.Required {
		t.Errorf("DefaultField.Required should be false")
	}
}

func TestStruct_Encode_CollectsAllFieldErrors(t *testing.T) {
	type User struct {
		Name  string
		Email string
	}
	c := codex.Struct[User](
		codex.RequiredField("name",
			codex.String().Refine(codex.Constraint[string]{
				Name:    "non-empty",
				Check:   func(v string) bool { return v != "" },
				Message: func(v string) string { return "must not be empty" },
			}),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
		codex.RequiredField("email",
			codex.String().Refine(codex.Constraint[string]{
				Name:    "has-at",
				Check:   func(v string) bool { return strings.Contains(v, "@") },
				Message: func(v string) string { return "must contain @" },
			}),
			func(u User) string { return u.Email },
			func(u *User, v string) { u.Email = v },
		),
	)

	// Both fields fail — should collect all errors, not fail-fast.
	_, err := c.Encode(User{Name: "", Email: "not-an-email"})
	if err == nil {
		t.Fatal("expected ValidationErrors, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected ValidationErrors, got %T: %v", err, err)
	}
	if len(ve) != 2 {
		t.Errorf("expected 2 field errors, got %d: %v", len(ve), ve)
	}
}

func TestStruct_Encode_ValidValueSucceeds(t *testing.T) {
	type User struct{ Name string }
	c := codex.Struct[User](
		codex.RequiredField("name",
			codex.String().Refine(codex.Constraint[string]{
				Name:    "non-empty",
				Check:   func(v string) bool { return v != "" },
				Message: func(v string) string { return "must not be empty" },
			}),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
	)
	enc, err := c.Encode(User{Name: "Alice"})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	obj, ok := enc.(map[string]any)
	if !ok || obj["name"] != "Alice" {
		t.Errorf("unexpected encode result: %v", enc)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleRequiredField() {
	type User struct {
		Name  string
		Email string
	}

	// Define the codec once — encode, decode, validate, and schema from one value.
	userCodec := codex.Struct[User](
		codex.RequiredField("name", codex.String(),
			func(u User) string { return u.Name },
			func(u *User, v string) { u.Name = v },
		),
		codex.RequiredField("email", codex.String(),
			func(u User) string { return u.Email },
			func(u *User, v string) { u.Email = v },
		),
	)

	// Decode from intermediate representation (map[string]any).
	user, err := userCodec.Decode(map[string]any{"name": "Alice", "email": "alice@example.com"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%s <%s>\n", user.Name, user.Email)

	// Missing required field returns a structured error.
	_, err = userCodec.Decode(map[string]any{"name": "Bob"})
	fmt.Println(err != nil)
	// Output:
	// Alice <alice@example.com>
	// true
}
