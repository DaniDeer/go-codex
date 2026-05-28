package validate

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
)

var (
	reEmail    = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	reUUID     = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reSlug     = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	reHostname = regexp.MustCompile(`^(?:[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	reSemVer   = regexp.MustCompile(`^v?(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*)` +
		`(?:-(?:(?:0|[1-9]\d*|\d*[a-zA-Z\-][0-9a-zA-Z\-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z\-][0-9a-zA-Z\-]*))*))?` +
		`(?:\+[0-9a-zA-Z\-]+(?:\.[0-9a-zA-Z\-]+)*)?$`)
)

func withFormat(f string) func(schema.Schema) schema.Schema {
	return func(s schema.Schema) schema.Schema {
		s.Format = f
		return s
	}
}

// Email is a Constraint that requires a valid email address.
// Validation uses a standard format check; it does not perform DNS lookup.
var Email = codex.Constraint[string]{
	Name:    "email",
	Check:   func(v string) bool { return reEmail.MatchString(v) },
	Message: func(v string) string { return fmt.Sprintf("invalid email address: %q", v) },
	Schema:  withFormat("email"),
}

// UUID is a Constraint that requires a valid UUID (any version, RFC 4122 format).
var UUID = codex.Constraint[string]{
	Name:    "uuid",
	Check:   func(v string) bool { return reUUID.MatchString(v) },
	Message: func(v string) string { return fmt.Sprintf("invalid UUID: %q", v) },
	Schema:  withFormat("uuid"),
}

// URLWithSchemes returns a Constraint that requires a valid absolute URL whose
// scheme is one of the provided values. The host must be non-empty.
// Schema annotation uses JSON Schema format "uri".
func URLWithSchemes(schemes ...string) codex.Constraint[string] {
	schemeSet := make(map[string]struct{}, len(schemes))
	for _, s := range schemes {
		schemeSet[s] = struct{}{}
	}
	label := strings.Join(schemes, "|")
	return codex.Constraint[string]{
		Name: fmt.Sprintf("url(%s)", label),
		Check: func(v string) bool {
			u, err := url.ParseRequestURI(v)
			if err != nil || u.Host == "" {
				return false
			}
			_, ok := schemeSet[u.Scheme]
			return ok
		},
		Message: func(v string) string {
			return fmt.Sprintf("invalid URL (expected scheme %s): %q", label, v)
		},
		Schema: withFormat("uri"),
	}
}

// URL is a Constraint that requires a valid absolute URL with http or https scheme.
var URL = URLWithSchemes("http", "https")

// URI is a Constraint that requires a valid absolute URI with any scheme.
// Use this for non-HTTP URIs such as grpc://, ws://, or custom schemes.
var URI = codex.Constraint[string]{
	Name: "uri",
	Check: func(v string) bool {
		u, err := url.ParseRequestURI(v)
		return err == nil && u.Scheme != "" && u.Host != ""
	},
	Message: func(v string) string { return fmt.Sprintf("invalid URI: %q", v) },
	Schema:  withFormat("uri"),
}

// IPv4 is a Constraint that requires a valid IPv4 address.
var IPv4 = codex.Constraint[string]{
	Name: "ipv4",
	Check: func(v string) bool {
		ip := net.ParseIP(v)
		return ip != nil && ip.To4() != nil && strings.Contains(v, ".")
	},
	Message: func(v string) string { return fmt.Sprintf("invalid IPv4 address: %q", v) },
	Schema:  withFormat("ipv4"),
}

// IPv6 is a Constraint that requires a valid IPv6 address.
var IPv6 = codex.Constraint[string]{
	Name: "ipv6",
	Check: func(v string) bool {
		ip := net.ParseIP(v)
		return ip != nil && ip.To4() == nil
	},
	Message: func(v string) string { return fmt.Sprintf("invalid IPv6 address: %q", v) },
	Schema:  withFormat("ipv6"),
}

// IP is a Constraint that requires a valid IP address (IPv4 or IPv6).
var IP = codex.Constraint[string]{
	Name:    "ip",
	Check:   func(v string) bool { return net.ParseIP(v) != nil },
	Message: func(v string) string { return fmt.Sprintf("invalid IP address: %q", v) },
	Schema:  withFormat("ip"),
}

// Hostname is a Constraint that requires a valid RFC 1123 hostname.
// Labels must be 1–63 characters; total length must not exceed 253 characters.
var Hostname = codex.Constraint[string]{
	Name: "hostname",
	Check: func(v string) bool {
		return len(v) <= 253 && len(v) > 0 && reHostname.MatchString(v)
	},
	Message: func(v string) string { return fmt.Sprintf("invalid hostname: %q", v) },
	Schema:  withFormat("hostname"),
}

// Date is a Constraint that requires an ISO 8601 date string (YYYY-MM-DD).
var Date = codex.Constraint[string]{
	Name: "date",
	Check: func(v string) bool {
		_, err := time.Parse("2006-01-02", v)
		return err == nil
	},
	Message: func(v string) string { return fmt.Sprintf("invalid date (expected YYYY-MM-DD): %q", v) },
	Schema:  withFormat("date"),
}

// DateTime is a Constraint that requires an RFC 3339 date-time string.
var DateTime = codex.Constraint[string]{
	Name: "date-time",
	Check: func(v string) bool {
		_, err := time.Parse(time.RFC3339, v)
		if err == nil {
			return true
		}
		_, err = time.Parse(time.RFC3339Nano, v)
		return err == nil
	},
	Message: func(v string) string { return fmt.Sprintf("invalid date-time (expected RFC 3339): %q", v) },
	Schema:  withFormat("date-time"),
}

// Time is a Constraint that requires an RFC 3339 full-time string
// (e.g. "10:30:00Z", "10:30:00.5+02:00").
var Time = codex.Constraint[string]{
	Name: "time",
	Check: func(v string) bool {
		dummy := "2000-01-01T" + v
		_, err := time.Parse(time.RFC3339, dummy)
		if err == nil {
			return true
		}
		_, err = time.Parse(time.RFC3339Nano, dummy)
		return err == nil
	},
	Message: func(v string) string {
		return fmt.Sprintf("invalid time (expected HH:MM:SS[.frac]Z or ±offset): %q", v)
	},
	Schema: withFormat("time"),
}

// Slug is a Constraint that requires a URL-friendly slug (lowercase alphanumeric and hyphens).
// Example valid slugs: "hello-world", "my-post-123".
var Slug = codex.Constraint[string]{
	Name:  "slug",
	Check: func(v string) bool { return reSlug.MatchString(v) },
	Message: func(v string) string {
		return fmt.Sprintf("invalid slug (lowercase alphanumeric and hyphens only): %q", v)
	},
	Schema: func(s schema.Schema) schema.Schema {
		s.Pattern = reSlug.String()
		return s
	},
}

// SemVer is a Constraint that requires a semantic version string
// following semver.org spec, with an optional leading "v" prefix.
// Examples: "1.2.3", "v2.0.0-alpha+build.123".
var SemVer = codex.Constraint[string]{
	Name:  "semver",
	Check: func(v string) bool { return reSemVer.MatchString(v) },
	Message: func(v string) string {
		return fmt.Sprintf("invalid semantic version (expected MAJOR.MINOR.PATCH): %q", v)
	},
	Schema: func(s schema.Schema) schema.Schema {
		s.Pattern = reSemVer.String()
		return s
	},
}

// CIDR is a Constraint that requires a valid CIDR notation string
// (e.g. "192.168.0.0/24", "10.0.0.0/8", "::/0").
// Host bits may be set (e.g. "192.168.0.1/24" is accepted).
// No schema annotation — there is no standard JSON Schema format for CIDR.
var CIDR = codex.Constraint[string]{
	Name: "cidr",
	Check: func(v string) bool {
		_, _, err := net.ParseCIDR(v)
		return err == nil
	},
	Message: func(v string) string { return fmt.Sprintf("invalid CIDR notation: %q", v) },
}
