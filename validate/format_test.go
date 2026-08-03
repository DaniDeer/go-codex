package validate_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
	"github.com/DaniDeer/go-codex/validate"
)

func TestEmail(t *testing.T) {
	c := validate.Email
	valid := []string{
		"user@example.com",
		"user.name+tag@sub.domain.org",
		"u@x.io",
		"a123@b456.co.uk",
	}
	invalid := []string{
		"",
		"notanemail",
		"@nodomain.com",
		"noatsign",
		"user@",
		"user@domain",
		"user @example.com",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("Email.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("Email.Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("Email.Message should not be empty")
	}
}

func TestUUID(t *testing.T) {
	c := validate.UUID
	valid := []string{
		"550e8400-e29b-41d4-a716-446655440000",
		"6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		"00000000-0000-0000-0000-000000000000",
		"550E8400-E29B-41D4-A716-446655440000", // uppercase
	}
	invalid := []string{
		"",
		"not-a-uuid",
		"550e8400-e29b-41d4-a716",
		"550e8400e29b41d4a716446655440000", // no dashes
		"550e8400-e29b-41d4-a716-44665544000g",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("UUID.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("UUID.Check(%q) = true, want false", v)
		}
	}
}

func TestURL(t *testing.T) {
	c := validate.URL
	valid := []string{
		"http://example.com",
		"https://example.com/path?q=1#frag",
		"http://localhost:8080",
		"https://sub.domain.org/path",
	}
	invalid := []string{
		"",
		"not-a-url",
		"ftp://example.com", // unsupported scheme
		"//example.com",     // no scheme
		"example.com",       // no scheme
		"http://",           // no host
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("URL.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("URL.Check(%q) = true, want false", v)
		}
	}
}

func TestURLWithSchemes(t *testing.T) {
	c := validate.URLWithSchemes("ws", "wss")
	cases := []struct {
		v    string
		pass bool
	}{
		{"ws://example.com/path", true},
		{"wss://example.com/path", true},
		{"http://example.com", false},  // wrong scheme
		{"https://example.com", false}, // wrong scheme
		{"ws://", false},               // no host
		{"", false},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got != tc.pass {
			t.Errorf("URLWithSchemes(ws,wss).Check(%q) = %v, want %v", tc.v, got, tc.pass)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("URLWithSchemes.Message should not be empty")
	}
}

func TestURI(t *testing.T) {
	c := validate.URI
	valid := []string{
		"http://example.com",
		"https://example.com/path",
		"grpc://host:443",
		"ws://host/path",
		"custom://example.com",
	}
	invalid := []string{
		"",
		"not-a-uri",
		"//example.com",  // no scheme
		"http://",        // no host
		"/relative/path", // no scheme
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("URI.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("URI.Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("URI.Message should not be empty")
	}
}

func TestIPv4(t *testing.T) {
	c := validate.IPv4
	valid := []string{
		"192.168.1.1",
		"0.0.0.0",
		"255.255.255.255",
		"127.0.0.1",
	}
	invalid := []string{
		"",
		"256.0.0.1",
		"192.168.1",
		"::1",         // IPv6
		"2001:db8::1", // IPv6
		"notanip",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("IPv4.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("IPv4.Check(%q) = true, want false", v)
		}
	}
}

func TestIPv6(t *testing.T) {
	c := validate.IPv6
	valid := []string{
		"::1",
		"2001:db8::1",
		"2001:0db8:0000:0000:0000:0000:0000:0001",
	}
	invalid := []string{
		"",
		"192.168.1.1", // IPv4
		"notanip",
		":::1",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("IPv6.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("IPv6.Check(%q) = true, want false", v)
		}
	}
}

func TestIP(t *testing.T) {
	c := validate.IP
	valid := []string{
		"192.168.1.1",
		"0.0.0.0",
		"255.255.255.255",
		"::1",
		"2001:db8::1",
		"2001:0db8:0000:0000:0000:0000:0000:0001",
	}
	invalid := []string{
		"",
		"notanip",
		"256.0.0.1",
		":::1",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("IP.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("IP.Check(%q) = true, want false", v)
		}
	}
}

func TestDate(t *testing.T) {
	c := validate.Date
	valid := []string{
		"2024-01-15",
		"2000-12-31",
		"1970-01-01",
	}
	invalid := []string{
		"",
		"2024-1-5",
		"15-01-2024",
		"2024/01/15",
		"not-a-date",
		"2024-13-01", // invalid month
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("Date.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("Date.Check(%q) = true, want false", v)
		}
	}
}

func TestDateTime(t *testing.T) {
	c := validate.DateTime
	valid := []string{
		"2024-01-15T10:30:00Z",
		"2024-01-15T10:30:00+02:00",
		"2000-12-31T23:59:59Z",
	}
	invalid := []string{
		"",
		"2024-01-15",
		"2024-01-15 10:30:00",
		"not-a-datetime",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("DateTime.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("DateTime.Check(%q) = true, want false", v)
		}
	}
}

func TestSlug(t *testing.T) {
	c := validate.Slug
	valid := []string{
		"hello",
		"hello-world",
		"my-post-123",
		"a",
		"abc123",
	}
	invalid := []string{
		"",
		"Hello-World", // uppercase
		"-leading-dash",
		"trailing-dash-",
		"double--dash",
		"has space",
		"has_underscore",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("Slug.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("Slug.Check(%q) = true, want false", v)
		}
	}
}

func TestHostname(t *testing.T) {
	c := validate.Hostname
	valid := []string{
		"example.com",
		"db.internal",
		"api.example.com",
		"localhost",
		"my-host-01",
		"a",
	}
	invalid := []string{
		"",
		"-leading-dash.com",
		"trailing-dash-.com",
		"has space.com",
		"has_underscore.com",
		strings.Repeat("a", 254), // too long
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("Hostname.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("Hostname.Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("Hostname.Message should not be empty")
	}
}

func TestTime(t *testing.T) {
	c := validate.Time
	valid := []string{
		"10:30:00Z",
		"23:59:59Z",
		"00:00:00Z",
		"10:30:00+02:00",
		"10:30:00-05:30",
		"10:30:00.5Z",
		"10:30:00.123456789Z",
	}
	invalid := []string{
		"",
		"10:30:00",   // no timezone
		"10:30",      // no seconds
		"25:00:00Z",  // invalid hour
		"10:60:00Z",  // invalid minute
		"2024-01-15", // date, not time
		"not-a-time",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("Time.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("Time.Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("Time.Message should not be empty")
	}
}

func TestSemVer(t *testing.T) {
	c := validate.SemVer
	valid := []string{
		"1.0.0",
		"0.0.1",
		"v1.2.3",
		"1.2.3-alpha",
		"1.2.3-alpha.1",
		"1.2.3+build.456",
		"v2.0.0-beta+exp.sha.5114f85",
	}
	invalid := []string{
		"",
		"1.0",     // missing patch
		"v1",      // missing minor+patch
		"1.2.3.4", // extra segment
		"not-a-semver",
		"1.2.x", // non-numeric
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("SemVer.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("SemVer.Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("SemVer.Message should not be empty")
	}
}

func TestCIDR(t *testing.T) {
	c := validate.CIDR
	valid := []string{
		"192.168.0.0/24",
		"10.0.0.0/8",
		"0.0.0.0/0",
		"::/0",
		"2001:db8::/32",
	}
	invalid := []string{
		"",
		"192.168.0.0",    // no prefix length
		"256.0.0.0/8",    // invalid IP
		"192.168.0.0/33", // prefix too long
		"not-cidr",
	}
	for _, v := range valid {
		if !c.Check(v) {
			t.Errorf("CIDR.Check(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if c.Check(v) {
			t.Errorf("CIDR.Check(%q) = true, want false", v)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("CIDR.Message should not be empty")
	}
}

func TestFormatConstraints_SchemaAnnotation(t *testing.T) {
	// Each format constraint should annotate Schema.Format correctly.
	cases := []struct {
		name       string
		constraint codex.Constraint[string]
		wantFormat string
	}{
		{"Email", validate.Email, "email"},
		{"UUID", validate.UUID, "uuid"},
		{"URL", validate.URL, "uri"},
		{"URI", validate.URI, "uri"},
		{"URLWithSchemes", validate.URLWithSchemes("ws", "wss"), "uri"},
		{"IPv4", validate.IPv4, "ipv4"},
		{"IPv6", validate.IPv6, "ipv6"},
		{"IP", validate.IP, "ip"},
		{"Hostname", validate.Hostname, "hostname"},
		{"Date", validate.Date, "date"},
		{"DateTime", validate.DateTime, "date-time"},
		{"Time", validate.Time, "time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.constraint.Schema == nil {
				t.Fatal("Schema transformer is nil")
			}
			s := tc.constraint.Schema(schema.Schema{Type: "string"})
			if s.Format != tc.wantFormat {
				t.Errorf("Format = %q, want %q", s.Format, tc.wantFormat)
			}
		})
	}
}

func TestSlug_SchemaAnnotation(t *testing.T) {
	if validate.Slug.Schema == nil {
		t.Fatal("Slug.Schema transformer is nil")
	}
	s := validate.Slug.Schema(schema.Schema{Type: "string"})
	if s.Pattern == "" {
		t.Error("Slug.Schema should set Pattern")
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleEmail() {
	emailCodec := codex.String().Refine(validate.Email)

	_, err := emailCodec.Decode("not-an-email")
	fmt.Println(err != nil) // invalid email

	_, err = emailCodec.Decode("alice@example.com")
	fmt.Println(err) // valid
	// Output:
	// true
	// <nil>
}

func ExampleNonEmptyString() {
	c := codex.String().Refine(validate.NonEmptyString)

	_, err := c.Decode("")
	fmt.Println(err != nil) // empty string rejected

	v, _ := c.Decode("hello")
	fmt.Println(v)
	// Output:
	// true
	// hello
}

func TestContainerImage_valid(t *testing.T) {
	c := validate.ContainerImage
	cases := []struct {
		v   string
		msg string
	}{
		{"alpine:latest", "simple tag"},
		{"ubuntu:22.04", "two-segment tag"},
		{"nginx:1.25-alpine", "tag with suffix"},
		{"docker.io/library/nginx:1.25", "full registry path"},
		{"my.registry.io:5000/project/image:v1", "registry with port"},
		{"alpine", "no tag (implicit latest)"},
		{"gcr.io/project/my-image", "two-level path no tag"},
		{"busybox@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", "digest only"},
		{"alpine:latest@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", "tag + digest"},
		{"registry.io:5000/org/repo:tag@sha256:abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", "full form"},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); !got {
			t.Errorf("ContainerImage.Check(%q) = false, want true — %s", tc.v, tc.msg)
		}
	}
}

func TestContainerImage_invalid(t *testing.T) {
	c := validate.ContainerImage
	cases := []struct {
		v   string
		msg string
	}{
		{"", "empty"},
		{"   ", "whitespace"},
		{"UPPERCASE:latest", "uppercase not allowed in repo"},
		{"image@sha256:xyz", "invalid hex digest"},
		{"-leading-hyphen", "leading hyphen in first segment"},
		{"repo/", "trailing slash"},
		{":tag-only", "leading colon"},
		{"@sha256:abc", "digest without repo"},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got {
			t.Errorf("ContainerImage.Check(%q) = true, want false — %s", tc.v, tc.msg)
		}
	}
	if msg := c.Message("bad"); msg == "" {
		t.Error("ContainerImage.Message should not be empty")
	}
}

func TestPort_valid(t *testing.T) {
	c := validate.Port
	cases := []struct {
		v   string
		msg string
	}{
		{"1", "minimum valid port"},
		{"80", "well-known port"},
		{"8080", "common alt-http port"},
		{"65535", "maximum valid port"},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); !got {
			t.Errorf("Port.Check(%q) = false, want true — %s", tc.v, tc.msg)
		}
	}
}

func TestPort_invalid(t *testing.T) {
	c := validate.Port
	cases := []struct {
		v   string
		msg string
	}{
		{"", "empty"},
		{"0", "zero not a valid port"},
		{"65536", "one above maximum"},
		{"99999999", "way out of range"},
		{"-1", "negative"},
		{"8080/tcp", "not a bare port number"},
		{"abc", "not numeric"},
		{"80 80", "contains whitespace"},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got {
			t.Errorf("Port.Check(%q) = true, want false — %s", tc.v, tc.msg)
		}
	}
	if msg := c.Message("99999"); msg == "" {
		t.Error("Port.Message should not be empty")
	}
}

func TestDockerPort_valid(t *testing.T) {
	c := validate.DockerPort
	cases := []struct {
		v   string
		msg string
	}{
		{"8080/tcp", "common tcp port"},
		{"53/udp", "common udp port"},
		{"1/tcp", "minimum valid port"},
		{"65535/udp", "maximum valid port"},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); !got {
			t.Errorf("DockerPort.Check(%q) = false, want true — %s", tc.v, tc.msg)
		}
	}
}

func TestDockerPort_invalid(t *testing.T) {
	c := validate.DockerPort
	cases := []struct {
		v   string
		msg string
	}{
		{"", "empty"},
		{"8080", "missing protocol"},
		{"8080/sctp", "unsupported protocol"},
		{"0/tcp", "zero not a valid port"},
		{"65536/tcp", "one above maximum"},
		{"8080/TCP", "protocol must be lowercase"},
		{"8080-8090/tcp", "port range not supported"},
	}
	for _, tc := range cases {
		if got := c.Check(tc.v); got {
			t.Errorf("DockerPort.Check(%q) = true, want false — %s", tc.v, tc.msg)
		}
	}
	if msg := c.Message("bad/tcp"); msg == "" {
		t.Error("DockerPort.Message should not be empty")
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

	// Valid format but wrong namespace
	if err := appVarCodec.Validate("DB_HOST"); err == nil {
		t.Fatalf("expected APP_PORT to be valid: %v", err)
	}

	// Valid format but wrong namespace
	if err := appVarCodec.Validate("DB_HOST"); err == nil {
		t.Error("expected error for wrong prefix")
	}
}
