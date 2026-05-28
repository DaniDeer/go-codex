package validate_test

import (
	"regexp"
	"strings"
	"testing"

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
