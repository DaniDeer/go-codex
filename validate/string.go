package validate

import (
	"fmt"
	"regexp"
	"strconv"
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

// IntString is a Constraint that requires the string to be a valid signed
// integer (as accepted by [strconv.Atoi]).
//
// Intended for use in [api/rest.RouteConfig.PathParamCodecs] and
// [api/events.ChannelConfig.TopicParamCodecs] where path and topic variables
// are always strings but may represent integers:
//
//	PathParamCodecs: map[string]codex.Codec[string]{
//	    "page": codex.String().Refine(validate.IntString),
//	}
var IntString = codex.Constraint[string]{
	Name:    "int-string",
	Check:   func(v string) bool { _, err := strconv.Atoi(v); return err == nil },
	Message: func(v string) string { return fmt.Sprintf("expected a valid integer string, got %q", v) },
}

// PositiveIntString is a Constraint that requires the string to represent a
// positive integer (> 0).
var PositiveIntString = codex.Constraint[string]{
	Name: "positive-int-string",
	Check: func(v string) bool {
		n, err := strconv.Atoi(v)
		return err == nil && n > 0
	},
	Message: func(v string) string {
		return fmt.Sprintf("expected a positive integer string (> 0), got %q", v)
	},
}

// NonNegativeIntString is a Constraint that requires the string to represent a
// non-negative integer (≥ 0).
var NonNegativeIntString = codex.Constraint[string]{
	Name: "non-negative-int-string",
	Check: func(v string) bool {
		n, err := strconv.Atoi(v)
		return err == nil && n >= 0
	},
	Message: func(v string) string {
		return fmt.Sprintf("expected a non-negative integer string (>= 0), got %q", v)
	},
}

// jwtRe matches a compact JWT: three base64url-encoded segments separated by dots.
// It does not verify signatures or decode payloads.
var jwtRe = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)

// BearerToken is a Constraint that validates a non-empty Bearer token string.
// It accepts any non-empty string without leading or trailing whitespace.
// Use with [api/rest.SecurityScheme] or [api/events.SecurityScheme] Codec to
// format-check extracted Bearer tokens before calling SecurityFunc.
var BearerToken = codex.Constraint[string]{
	Name:  "bearer-token",
	Check: func(v string) bool { return v != "" && v == strings.TrimSpace(v) },
	Message: func(_ string) string {
		return "bearer token must be non-empty and contain no leading or trailing whitespace"
	},
}

// JWT is a Constraint that validates a compact JWT serialization:
// three base64url-encoded segments separated by dots (header.payload.signature).
// It does not verify signatures or decode claims.
// Use with [api/rest.SecurityScheme] or [api/events.SecurityScheme] Codec to
// format-check extracted JWTs before calling SecurityFunc.
var JWT = codex.Constraint[string]{
	Name:    "jwt",
	Check:   func(v string) bool { return jwtRe.MatchString(v) },
	Message: func(_ string) string { return "value must be a compact JWT (header.payload.signature in base64url)" },
}

// IntStringInRange returns a Constraint that requires the string to represent
// an integer within [min, max] (inclusive on both ends).
func IntStringInRange(min, max int) codex.Constraint[string] {
	return codex.Constraint[string]{
		Name: fmt.Sprintf("int-string-range(%d,%d)", min, max),
		Check: func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= min && n <= max
		},
		Message: func(v string) string {
			return fmt.Sprintf("expected integer string in [%d, %d], got %q", min, max, v)
		},
	}
}
