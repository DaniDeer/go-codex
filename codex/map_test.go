package codex_test

import (
	"errors"
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
