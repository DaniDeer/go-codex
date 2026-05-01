package codex_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
)

func TestNullable_EncodeNil(t *testing.T) {
	c := codex.Nullable(codex.String())
	enc, err := c.Encode(nil)
	if err != nil {
		t.Fatalf("Encode nil: %v", err)
	}
	if enc != nil {
		t.Errorf("want nil, got %v", enc)
	}
}

func TestNullable_EncodeNonNil(t *testing.T) {
	c := codex.Nullable(codex.String())
	s := "hello"
	enc, err := c.Encode(&s)
	if err != nil {
		t.Fatalf("Encode non-nil: %v", err)
	}
	if enc != "hello" {
		t.Errorf("want %q, got %v", "hello", enc)
	}
}

func TestNullable_DecodeNil(t *testing.T) {
	c := codex.Nullable(codex.String())
	got, err := c.Decode(nil)
	if err != nil {
		t.Fatalf("Decode nil: %v", err)
	}
	if got != nil {
		t.Errorf("want nil pointer, got %v", got)
	}
}

func TestNullable_DecodeNonNil(t *testing.T) {
	c := codex.Nullable(codex.String())
	got, err := c.Decode("world")
	if err != nil {
		t.Fatalf("Decode non-nil: %v", err)
	}
	if got == nil {
		t.Fatal("want non-nil pointer")
	}
	if *got != "world" {
		t.Errorf("want %q, got %q", "world", *got)
	}
}

func TestNullable_DecodeError(t *testing.T) {
	c := codex.Nullable(codex.Int())
	if _, err := c.Decode("not-a-number"); err == nil {
		t.Fatal("expected error for wrong inner type")
	}
}

func TestNullable_SchemaInheritsInner(t *testing.T) {
	inner := codex.Int()
	c := codex.Nullable(inner)
	if !c.Schema.Nullable {
		t.Error("want Nullable=true")
	}
	if c.Schema.Type != "integer" {
		t.Errorf("want type=integer, got %q", c.Schema.Type)
	}
}

func TestNullable_DoesNotMutateInner(t *testing.T) {
	inner := codex.String()
	_ = codex.Nullable(inner)
	if inner.Schema.Nullable {
		t.Error("Nullable should not mutate inner codec schema")
	}
}
