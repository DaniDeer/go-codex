package validate_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

func TestNonEmptyString(t *testing.T) {
	c := validate.NonEmptyString
	if !c.Check("hello") {
		t.Error("Check(\"hello\") should pass")
	}
	if c.Check("") {
		t.Error("Check(\"\") should fail")
	}
	if msg := c.Message(""); msg == "" {
		t.Error("Message should not be empty")
	}
}

func TestMinLen(t *testing.T) {
	c := validate.MinLen(3)
	cases := []struct {
		v    string
		pass bool
	}{
		{"abc", true}, {"abcd", true},
		{"ab", false}, {"", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MinLen(3).Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message("ab"); msg == "" {
		t.Error("MinLen.Message should not be empty")
	}
}

func TestMaxLen(t *testing.T) {
	c := validate.MaxLen(5)
	cases := []struct {
		v    string
		pass bool
	}{
		{"abc", true}, {"abcde", true},
		{"abcdef", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MaxLen(5).Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message("toolongvalue"); !strings.Contains(msg, "5") {
		t.Errorf("MaxLen.Message = %q, want max value in message", msg)
	}
}

func TestPattern(t *testing.T) {
	re := regexp.MustCompile(`^\d{4}$`)
	c := validate.Pattern(re)
	cases := []struct {
		v    string
		pass bool
	}{
		{"1234", true},
		{"123", false}, {"12345", false}, {"abcd", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("Pattern(^\\d{4}$).Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message("abc"); msg == "" {
		t.Error("Pattern.Message should not be empty")
	}
}

func TestOneOf(t *testing.T) {
	c := validate.OneOf("red", "green", "blue")
	cases := []struct {
		v    string
		pass bool
	}{
		{"red", true}, {"green", true}, {"blue", true},
		{"yellow", false}, {"", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("OneOf.Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	msg := c.Message("yellow")
	if !strings.Contains(msg, "red") || !strings.Contains(msg, "yellow") {
		t.Errorf("OneOf.Message = %q, want allowed values and rejected value", msg)
	}
}

func TestMQTTTopic(t *testing.T) {
	c := validate.MQTTTopic
	cases := []struct {
		v    string
		pass bool
	}{
		{"sensor/temperature", true},
		{"home/+/temp", true}, // wildcards allowed for subscriptions
		{"home/#", true},      // wildcards allowed for subscriptions
		{"a", true},
		{"/leading/slash", true},
		{"", false},                // empty
		{string([]byte{0}), false}, // null byte
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MQTTTopic.Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message(""); !strings.Contains(msg, "empty") {
		t.Errorf("MQTTTopic.Message(\"\") = %q, want mention of empty", msg)
	}
}

func TestMQTTPublishTopic(t *testing.T) {
	c := validate.MQTTPublishTopic
	cases := []struct {
		v    string
		pass bool
	}{
		{"sensor/temperature", true},
		{"home/living/temp", true},
		{"a", true},
		{"/leading/slash", true},
		{"", false},                // empty
		{string([]byte{0}), false}, // null byte
		{"home/+/temp", false},     // wildcard + not allowed for publish
		{"home/#", false},          // wildcard # not allowed for publish
		{"sensor/+", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("MQTTPublishTopic.Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message("sensor/+"); !strings.Contains(msg, "wildcard") {
		t.Errorf("MQTTPublishTopic.Message(\"sensor/+\") = %q, want mention of wildcard", msg)
	}
}

func TestHTTPPath(t *testing.T) {
	c := validate.HTTPPath
	cases := []struct {
		v    string
		pass bool
	}{
		{"/", true},
		{"/users", true},
		{"/users/{id}", true},
		{"/api/v1/users/{id}/posts", true},
		{"", false},             // empty
		{"users", false},        // no leading slash
		{"/users/my id", false}, // space
		{"/users/\x00", false},  // null byte
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("HTTPPath.Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message("users"); !strings.Contains(msg, "/") {
		t.Errorf("HTTPPath.Message(\"users\") = %q, want mention of leading slash", msg)
	}
}

func TestIntString(t *testing.T) {
	c := validate.IntString
	pass := []string{"0", "1", "-1", "42", "-999", "2147483647"}
	fail := []string{"", "abc", "1.5", " 1", "1 ", "1e2"}
	for _, v := range pass {
		if !c.Check(v) {
			t.Errorf("IntString.Check(%q) = false, want true", v)
		}
	}
	for _, v := range fail {
		if c.Check(v) {
			t.Errorf("IntString.Check(%q) = true, want false", v)
		}
	}
}

func TestPositiveIntString(t *testing.T) {
	c := validate.PositiveIntString
	pass := []string{"1", "42", "2147483647"}
	fail := []string{"0", "-1", "-42", "", "abc", "1.5"}
	for _, v := range pass {
		if !c.Check(v) {
			t.Errorf("PositiveIntString.Check(%q) = false, want true", v)
		}
	}
	for _, v := range fail {
		if c.Check(v) {
			t.Errorf("PositiveIntString.Check(%q) = true, want false", v)
		}
	}
}

func TestNonNegativeIntString(t *testing.T) {
	c := validate.NonNegativeIntString
	pass := []string{"0", "1", "42", "2147483647"}
	fail := []string{"-1", "-42", "", "abc", "1.5"}
	for _, v := range pass {
		if !c.Check(v) {
			t.Errorf("NonNegativeIntString.Check(%q) = false, want true", v)
		}
	}
	for _, v := range fail {
		if c.Check(v) {
			t.Errorf("NonNegativeIntString.Check(%q) = true, want false", v)
		}
	}
}

func TestIntStringInRange(t *testing.T) {
	c := validate.IntStringInRange(1, 100)
	pass := []string{"1", "50", "100"}
	fail := []string{"0", "101", "-1", "", "abc", "1.5"}
	for _, v := range pass {
		if !c.Check(v) {
			t.Errorf("IntStringInRange(1,100).Check(%q) = false, want true", v)
		}
	}
	for _, v := range fail {
		if c.Check(v) {
			t.Errorf("IntStringInRange(1,100).Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("200"); !strings.Contains(msg, "100") {
		t.Errorf("IntStringInRange.Message = %q, want mention of max bound", msg)
	}
}

func TestBearerToken_valid(t *testing.T) {
	c := codex.String().Refine(validate.BearerToken)
	if err := c.Validate("my-bearer-token"); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestBearerToken_emptyString(t *testing.T) {
	c := codex.String().Refine(validate.BearerToken)
	if err := c.Validate(""); err == nil {
		t.Error("want error for empty string, got nil")
	}
}

func TestBearerToken_leadingSpace(t *testing.T) {
	c := codex.String().Refine(validate.BearerToken)
	if err := c.Validate(" token"); err == nil {
		t.Error("want error for leading space, got nil")
	}
}

func TestBearerToken_trailingSpace(t *testing.T) {
	c := codex.String().Refine(validate.BearerToken)
	if err := c.Validate("token "); err == nil {
		t.Error("want error for trailing space, got nil")
	}
}

func TestJWT_valid(t *testing.T) {
	c := codex.String().Refine(validate.JWT)
	if err := c.Validate("header.payload.sig"); err != nil {
		t.Errorf("want nil, got %v", err)
	}
}

func TestJWT_validWithUnpaddedBase64url(t *testing.T) {
	c := codex.String().Refine(validate.JWT)
	// real-world JWT style with _ and - chars
	tok := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1c2VyXzEifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	if err := c.Validate(tok); err != nil {
		t.Errorf("want nil for base64url JWT, got %v", err)
	}
}

func TestJWT_missingSegment(t *testing.T) {
	c := codex.String().Refine(validate.JWT)
	if err := c.Validate("header.payload"); err == nil {
		t.Error("want error for 2-part token, got nil")
	}
}

func TestJWT_empty(t *testing.T) {
	c := codex.String().Refine(validate.JWT)
	if err := c.Validate(""); err == nil {
		t.Error("want error for empty string, got nil")
	}
}

func TestJWT_tooManyParts(t *testing.T) {
	c := codex.String().Refine(validate.JWT)
	if err := c.Validate("a.b.c.d"); err == nil {
		t.Error("want error for 4-part token, got nil")
	}
}

func TestJWT_withSpaces(t *testing.T) {
	c := codex.String().Refine(validate.JWT)
	if err := c.Validate("a.b.c "); err == nil {
		t.Error("want error for token with trailing space, got nil")
	}
}

// ── EnvVarName ────────────────────────────────────────────────────────────────

func TestEnvVarName_Valid(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	cases := []string{"APP_PORT", "LOG_LEVEL", "_INTERNAL", "X1", "A", "_", "MY_APP_123"}
	for _, v := range cases {
		if err := c.Validate(v); err != nil {
			t.Errorf("expected %q to be valid, got: %v", v, err)
		}
	}
}

func TestEnvVarName_Lowercase_Fails(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	err := c.Validate("log_level")
	if err == nil {
		t.Fatal("expected error for lowercase name, got nil")
	}
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestEnvVarName_Dash_Fails(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	if err := c.Validate("APP-PORT"); err == nil {
		t.Error("expected error for name with dash, got nil")
	}
}

func TestEnvVarName_StartsWithDigit_Fails(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	if err := c.Validate("1STVAR"); err == nil {
		t.Error("expected error for name starting with digit, got nil")
	}
}

func TestEnvVarName_Space_Fails(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	if err := c.Validate("APP PORT"); err == nil {
		t.Error("expected error for name with space, got nil")
	}
}

func TestEnvVarName_Empty_Fails(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	if err := c.Validate(""); err == nil {
		t.Error("expected error for empty name, got nil")
	}
}

func TestEnvVarName_Schema_HasPattern(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarName)
	if c.Schema.Pattern == "" {
		t.Error("expected Schema.Pattern to be set")
	}
}

// ── EnvVarPrefix ──────────────────────────────────────────────────────────────

func TestEnvVarPrefix_Match_Passes(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarPrefix("APP_"))
	if err := c.Validate("APP_PORT"); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestEnvVarPrefix_NoMatch_Fails(t *testing.T) {
	c := codex.String().Refine(validate.EnvVarPrefix("APP_"))
	err := c.Validate("DB_HOST")
	if err == nil {
		t.Fatal("expected error for wrong prefix, got nil")
	}
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
}

func TestEnvVarPrefix_Name_NotEmpty(t *testing.T) {
	c := validate.EnvVarPrefix("APP_")
	if c.Name == "" {
		t.Fatal("constraint Name must not be empty")
	}
}

func TestEnvVarName_AndPrefix_Composition(t *testing.T) {
	appVarCodec := codex.String().
		Refine(validate.EnvVarName).
		Refine(validate.EnvVarPrefix("APP_"))

	// Valid: POSIX format + correct prefix
	if err := appVarCodec.Validate("APP_PORT"); err != nil {
		t.Fatalf("expected APP_PORT to be valid: %v", err)
	}

	// Invalid format (lowercase)
	if err := appVarCodec.Validate("app_port"); err == nil {
		t.Error("expected error for lowercase name")
	}

	// Valid format but wrong namespace
	if err := appVarCodec.Validate("DB_HOST"); err == nil {
		t.Error("expected error for wrong prefix")
	}
}
