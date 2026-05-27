package validate_test

import (
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
