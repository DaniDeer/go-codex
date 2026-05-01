package validate

import (
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
)

// MaxBytes returns a Constraint that requires a byte slice of at most n bytes.
// The check applies to the decoded byte count, not the base64-encoded string length.
func MaxBytes(n int) codex.Constraint[[]byte] {
	return codex.Constraint[[]byte]{
		Name:  fmt.Sprintf("maxBytes(%d)", n),
		Check: func(v []byte) bool { return len(v) <= n },
		Message: func(v []byte) string {
			return fmt.Sprintf("expected at most %d bytes, got %d", n, len(v))
		},
	}
}

// MinBytes returns a Constraint that requires a byte slice of at least n bytes.
// The check applies to the decoded byte count, not the base64-encoded string length.
func MinBytes(n int) codex.Constraint[[]byte] {
	return codex.Constraint[[]byte]{
		Name:  fmt.Sprintf("minBytes(%d)", n),
		Check: func(v []byte) bool { return len(v) >= n },
		Message: func(v []byte) string {
			return fmt.Sprintf("expected at least %d bytes, got %d", n, len(v))
		},
	}
}
