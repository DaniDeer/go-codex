package validate_test

import (
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── MaxBytes ─────────────────────────────────────────────────────────────────

func TestMaxBytes_Check(t *testing.T) {
	c := validate.MaxBytes(4)
	cases := []struct {
		name  string
		input []byte
		pass  bool
	}{
		{"exact limit", []byte{1, 2, 3, 4}, true},
		{"under limit", []byte{1, 2}, true},
		{"empty", []byte{}, true},
		{"over limit", []byte{1, 2, 3, 4, 5}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Check(tc.input); got != tc.pass {
				t.Errorf("MaxBytes(4).Check(%v) = %v, want %v", tc.input, got, tc.pass)
			}
		})
	}
}

func TestMaxBytes_Message(t *testing.T) {
	c := validate.MaxBytes(4)
	msg := c.Message([]byte{1, 2, 3, 4, 5})
	if msg == "" {
		t.Fatal("Message should not be empty")
	}
	// Should mention the limit and the actual count.
	if msg != "expected at most 4 bytes, got 5" {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestMaxBytes_NoSchemaAnnotation(t *testing.T) {
	c := validate.MaxBytes(100)
	if c.Schema != nil {
		t.Error("MaxBytes must not carry a Schema fn (no JSON Schema equivalent)")
	}
}

func TestMaxBytes_RefineIntegration(t *testing.T) {
	codec := codex.Bytes().Refine(validate.MaxBytes(3))

	// Decode base64("ab") = 2 bytes — OK.
	if _, err := codec.Decode("YWI="); err != nil {
		t.Fatalf("expected ok for 2-byte payload: %v", err)
	}

	// Decode base64("abcd") = 4 bytes — exceeds limit.
	if _, err := codec.Decode("YWJjZA=="); err == nil {
		t.Fatal("expected error for 4-byte payload with MaxBytes(3)")
	}
}

// ── MinBytes ─────────────────────────────────────────────────────────────────

func TestMinBytes_Check(t *testing.T) {
	c := validate.MinBytes(3)
	cases := []struct {
		name  string
		input []byte
		pass  bool
	}{
		{"exact limit", []byte{1, 2, 3}, true},
		{"over limit", []byte{1, 2, 3, 4}, true},
		{"under limit", []byte{1, 2}, false},
		{"empty", []byte{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.Check(tc.input); got != tc.pass {
				t.Errorf("MinBytes(3).Check(%v) = %v, want %v", tc.input, got, tc.pass)
			}
		})
	}
}

func TestMinBytes_Message(t *testing.T) {
	c := validate.MinBytes(5)
	msg := c.Message([]byte{1, 2})
	if msg != "expected at least 5 bytes, got 2" {
		t.Errorf("unexpected message: %q", msg)
	}
}

func TestMinBytes_NoSchemaAnnotation(t *testing.T) {
	c := validate.MinBytes(1)
	if c.Schema != nil {
		t.Error("MinBytes must not carry a Schema fn")
	}
}

func TestMinBytes_RefineIntegration(t *testing.T) {
	codec := codex.Bytes().Refine(validate.MinBytes(2))

	// Decode base64("ab") = 2 bytes — OK.
	if _, err := codec.Decode("YWI="); err != nil {
		t.Fatalf("expected ok for 2-byte payload: %v", err)
	}

	// Decode base64("a") = 1 byte — below minimum.
	if _, err := codec.Decode("YQ=="); err == nil {
		t.Fatal("expected error for 1-byte payload with MinBytes(2)")
	}
}
