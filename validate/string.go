package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/DaniDeer/go-codex/codex"
)

// NonEmptyString is a Constraint that requires a non-empty string.
var NonEmptyString = codex.Constraint[string]{
	Name:    "non-empty",
	Check:   func(v string) bool { return v != "" },
	Message: func(v string) string { return "expected non-empty string" },
}

// MinLen returns a Constraint that requires a string of at least n characters.
func MinLen(n int) codex.Constraint[string] {
	return codex.Constraint[string]{
		Name:  fmt.Sprintf("minLen(%d)", n),
		Check: func(v string) bool { return len(v) >= n },
		Message: func(v string) string {
			return fmt.Sprintf("expected string of at least %d characters, got %d", n, len(v))
		},
	}
}

// MaxLen returns a Constraint that requires a string of at most n characters.
func MaxLen(n int) codex.Constraint[string] {
	return codex.Constraint[string]{
		Name:  fmt.Sprintf("maxLen(%d)", n),
		Check: func(v string) bool { return len(v) <= n },
		Message: func(v string) string {
			return fmt.Sprintf("expected string of at most %d characters, got %d", n, len(v))
		},
	}
}

// Pattern returns a Constraint that requires the string to match the given regular expression.
// The caller is responsible for compiling the regexp (use regexp.MustCompile for literals).
func Pattern(re *regexp.Regexp) codex.Constraint[string] {
	return codex.Constraint[string]{
		Name:    fmt.Sprintf("pattern(%s)", re.String()),
		Check:   func(v string) bool { return re.MatchString(v) },
		Message: func(v string) string { return fmt.Sprintf("expected string matching %q, got %q", re.String(), v) },
	}
}

// OneOf returns a Constraint that requires the string to be one of the given values.
func OneOf(values ...string) codex.Constraint[string] {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return codex.Constraint[string]{
		Name:  fmt.Sprintf("oneOf(%s)", strings.Join(values, "|")),
		Check: func(v string) bool { _, ok := set[v]; return ok },
		Message: func(v string) string {
			return fmt.Sprintf("expected one of [%s], got %q", strings.Join(values, ", "), v)
		},
	}
}
