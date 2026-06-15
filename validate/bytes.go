package validate

import (
	"bytes"
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

// HasPrefix returns a Constraint that requires the byte slice to begin with the
// given prefix.
//
// For well-known file formats, prefer the predefined constants in this package:
// [PNG], [JPEG], [GIF], [WebP], [PDF], [ZIP]. They produce human-readable
// constraint names ("png", "jpeg", …) instead of hex strings, and their
// check logic is tested and documented.
//
// Use HasPrefix for custom or proprietary binary formats not covered by the
// built-in constants — for example, an internal binary protocol or a
// vendor-specific file type.
//
// An empty prefix always passes. The produced error is a [codex.ConstraintError]
// navigable via [errors.As].
func HasPrefix(prefix []byte) codex.Constraint[[]byte] {
	return codex.Constraint[[]byte]{
		Name: fmt.Sprintf("hasPrefix(%x)", prefix),
		Check: func(v []byte) bool {
			return len(v) >= len(prefix) && bytes.Equal(v[:len(prefix)], prefix)
		},
		Message: func(v []byte) string {
			got := v
			if len(got) > len(prefix) {
				got = got[:len(prefix)]
			}
			return fmt.Sprintf("expected byte prefix %x, got %x", prefix, got)
		},
	}
}
