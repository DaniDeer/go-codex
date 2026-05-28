package validate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

func intptr(v int) *int { return &v }

// NonEmptyString is a Constraint that requires a non-empty string.
var NonEmptyString = codex.Constraint[string]{
	Name:    "non-empty",
	Check:   func(v string) bool { return v != "" },
	Message: func(v string) string { return "expected non-empty string" },
	Schema: func(s schema.Schema) schema.Schema {
		s.MinLength = intptr(1)
		return s
	},
}

// MinLen returns a Constraint that requires a string of at least n characters.
func MinLen(n int) codex.Constraint[string] {
	return codex.Constraint[string]{
		Name:  fmt.Sprintf("minLen(%d)", n),
		Check: func(v string) bool { return len(v) >= n },
		Message: func(v string) string {
			return fmt.Sprintf("expected string of at least %d characters, got %d", n, len(v))
		},
		Schema: func(s schema.Schema) schema.Schema {
			s.MinLength = intptr(n)
			return s
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
		Schema: func(s schema.Schema) schema.Schema {
			s.MaxLength = intptr(n)
			return s
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
		Schema: func(s schema.Schema) schema.Schema {
			s.Pattern = re.String()
			return s
		},
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
		Schema: func(s schema.Schema) schema.Schema {
			enum := make([]any, len(values))
			for i, v := range values {
				enum[i] = v
			}
			s.Enum = enum
			return s
		},
	}
}

// MQTTTopic is a Constraint that validates an MQTT topic string for general use
// (subscribe or publish). It requires the string to be non-empty, contain no
// null bytes (U+0000), and be at most 65535 UTF-8 bytes — as required by the
// MQTT specification (section 4.7).
var MQTTTopic = codex.Constraint[string]{
	Name: "mqtt-topic",
	Check: func(v string) bool {
		return v != "" && !strings.ContainsRune(v, 0) && utf8.RuneCountInString(v) > 0 && len(v) <= 65535
	},
	Message: func(v string) string {
		switch {
		case v == "":
			return "mqtt topic must not be empty"
		case strings.ContainsRune(v, 0):
			return "mqtt topic must not contain null bytes"
		case len(v) > 65535:
			return fmt.Sprintf("mqtt topic exceeds maximum length of 65535 bytes, got %d", len(v))
		default:
			return fmt.Sprintf("invalid mqtt topic: %q", v)
		}
	},
}

// MQTTPublishTopic is a Constraint that validates an MQTT topic string for
// publishing. It applies all rules from [MQTTTopic] and additionally forbids
// wildcard characters ('+' and '#'), which are reserved for subscriptions only.
var MQTTPublishTopic = codex.Constraint[string]{
	Name: "mqtt-publish-topic",
	Check: func(v string) bool {
		return v != "" && !strings.ContainsRune(v, 0) && len(v) <= 65535 &&
			!strings.ContainsAny(v, "+#")
	},
	Message: func(v string) string {
		switch {
		case v == "":
			return "mqtt publish topic must not be empty"
		case strings.ContainsRune(v, 0):
			return "mqtt publish topic must not contain null bytes"
		case len(v) > 65535:
			return fmt.Sprintf("mqtt publish topic exceeds maximum length of 65535 bytes, got %d", len(v))
		case strings.ContainsAny(v, "+#"):
			return fmt.Sprintf("mqtt publish topic must not contain wildcard characters '+' or '#', got %q", v)
		default:
			return fmt.Sprintf("invalid mqtt publish topic: %q", v)
		}
	},
}

// httpPathRe matches a valid HTTP path: starts with '/', followed by any
// sequence of path characters including OpenAPI-style path parameters ({name}).
// Spaces and null bytes are not allowed.
var httpPathRe = regexp.MustCompile(`^/[^\x00 ]*$`)

// HTTPPath is a Constraint that validates an HTTP path string. It requires the
// path to start with '/' and contain no unencoded spaces or null bytes.
// OpenAPI-style path parameters (e.g. /users/{id}) are permitted.
var HTTPPath = codex.Constraint[string]{
	Name:  "http-path",
	Check: func(v string) bool { return httpPathRe.MatchString(v) },
	Message: func(v string) string {
		switch {
		case v == "" || v[0] != '/':
			return fmt.Sprintf("http path must start with '/', got %q", v)
		case strings.ContainsRune(v, 0):
			return fmt.Sprintf("http path must not contain null bytes, got %q", v)
		case strings.ContainsRune(v, ' '):
			return fmt.Sprintf("http path must not contain unencoded spaces, got %q", v)
		default:
			return fmt.Sprintf("invalid http path: %q", v)
		}
	},
}
