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
	reEmail = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
	reUUID  = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	reSlug  = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
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

// URL is a Constraint that requires a valid absolute URL with http or https scheme.
var URL = codex.Constraint[string]{
	Name: "url",
	Check: func(v string) bool {
		u, err := url.ParseRequestURI(v)
		if err != nil {
			return false
		}
		return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	},
	Message: func(v string) string { return fmt.Sprintf("invalid URL: %q", v) },
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
		return err == nil
	},
	Message: func(v string) string { return fmt.Sprintf("invalid date-time (expected RFC 3339): %q", v) },
	Schema:  withFormat("date-time"),
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
