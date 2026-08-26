package codex_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

func TestStringCodec_DecodeEncodeRoundTrip(t *testing.T) {
	c := codex.StringCodec(
		func(s string) (int, error) { return len(s), nil },
		func(v int) (string, error) { return "n=" + string(rune('0'+v)), nil },
		schema.Schema{Type: "integer"},
	)
	got, err := c.Decode("abc")
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got != 3 {
		t.Fatalf("Decode() = %d, want 3", got)
	}
	enc, err := c.Encode(3)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if enc != "n=3" {
		t.Fatalf("Encode() = %v, want n=3", enc)
	}
}

func TestStringCodec_DecodeWrongTypeReturnsTypeMismatchError(t *testing.T) {
	c := codex.IntString()
	_, err := c.Decode(42) // not a string
	var tme codex.TypeMismatchError
	if !errors.As(err, &tme) {
		t.Fatalf("Decode(42) error = %v, want TypeMismatchError", err)
	}
	if tme.Expected != "string" {
		t.Errorf("TypeMismatchError.Expected = %q, want string", tme.Expected)
	}
}

func TestStringCodec_DecodeParseErrorPropagates(t *testing.T) {
	c := codex.IntString()
	_, err := c.Decode("not-a-number")
	if err == nil {
		t.Fatal("Decode(\"not-a-number\") expected error, got nil")
	}
}

func TestIntString_RoundTrip(t *testing.T) {
	c := codex.IntString()
	got, err := c.Decode("42")
	if err != nil || got != 42 {
		t.Fatalf("Decode(\"42\") = (%d, %v), want (42, nil)", got, err)
	}
	enc, err := c.Encode(42)
	if err != nil || enc != "42" {
		t.Fatalf("Encode(42) = (%v, %v), want (\"42\", nil)", enc, err)
	}
}

func TestInt64String_RoundTrip(t *testing.T) {
	c := codex.Int64String()
	got, err := c.Decode("9223372036854775807")
	if err != nil || got != 9223372036854775807 {
		t.Fatalf("Decode() = (%d, %v)", got, err)
	}
	enc, err := c.Encode(int64(-5))
	if err != nil || enc != "-5" {
		t.Fatalf("Encode(-5) = (%v, %v)", enc, err)
	}
}

func TestUintString_RoundTrip(t *testing.T) {
	c := codex.UintString()
	got, err := c.Decode("18446744073709551615")
	if err != nil || got != 18446744073709551615 {
		t.Fatalf("Decode() = (%d, %v)", got, err)
	}
	enc, err := c.Encode(uint64(7))
	if err != nil || enc != "7" {
		t.Fatalf("Encode(7) = (%v, %v)", enc, err)
	}
}

func TestBoolString_RoundTrip(t *testing.T) {
	c := codex.BoolString()
	got, err := c.Decode("true")
	if err != nil || got != true {
		t.Fatalf("Decode(\"true\") = (%v, %v)", got, err)
	}
	enc, err := c.Encode(false)
	if err != nil || enc != "false" {
		t.Fatalf("Encode(false) = (%v, %v)", enc, err)
	}
}

// textID is a small local type implementing encoding.TextMarshaler/
// TextUnmarshaler on its pointer receiver, standing in for a real
// third-party type like uuid.UUID.
type textID struct{ v string }

func (t textID) MarshalText() ([]byte, error) { return []byte("id-" + t.v), nil }

func (t *textID) UnmarshalText(b []byte) error {
	s := string(b)
	if len(s) < 3 || s[:3] != "id-" {
		return errors.New("textID: missing id- prefix")
	}
	t.v = s[3:]
	return nil
}

func TestTextCodec_DecodeEncodeRoundTrip(t *testing.T) {
	c := codex.TextCodec[textID]()
	got, err := c.Decode("id-42")
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.v != "42" {
		t.Fatalf("Decode() = %+v, want v=42", got)
	}
	enc, err := c.Encode(textID{v: "42"})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if enc != "id-42" {
		t.Fatalf("Encode() = %v, want id-42", enc)
	}
}

func TestTextCodec_DecodeErrorPropagates(t *testing.T) {
	c := codex.TextCodec[textID]()
	_, err := c.Decode("no-prefix")
	if err == nil {
		t.Fatal("Decode(\"no-prefix\") expected error, got nil")
	}
}

// failingTextID's MarshalText always fails — used to prove TextCodec's
// Encode PROPAGATES a MarshalText error instead of silently falling back
// to a best-effort string (the G1 fix: StringCodec's format callback now
// returns an error, so TextCodec no longer has to swallow this).
type failingTextID struct{}

func (failingTextID) MarshalText() ([]byte, error) {
	return nil, errors.New("failingTextID: MarshalText always fails")
}

func (*failingTextID) UnmarshalText(_ []byte) error { return nil }

func TestTextCodec_EncodeMarshalTextErrorPropagates(t *testing.T) {
	c := codex.TextCodec[failingTextID]()
	_, err := c.Encode(failingTextID{})
	if err == nil {
		t.Fatal("Encode() expected the MarshalText error to propagate, got nil")
	}
}
