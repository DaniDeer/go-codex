package validate

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// reContainerImage matches OCI container image references:
//
//	[registry-host[:port]/]repository[:tag][@digest]
//
// Registry host: hostname optionally with port.
// Repository: one or more slash-separated segments, each 2-255 chars.
// Tag: optional, follows distribution spec (alphanumeric + separators).
// Digest: optional, format algorithm:hex (e.g. sha256:abc...).
var reContainerImage = regexp.MustCompile(`^` +
	// Optional registry host (hostname or hostname:port).
	`(?:` +
	`[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?` +
	`(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?)*` +
	`(?::[0-9]{1,5})?` +
	`/)?` +
	// Repository: one or more segments separated by slash.
	`[a-z0-9]+(?:[._-][a-z0-9]+)*` +
	`(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*` +
	// Optional tag.
	`(?::[\w][\w\.\-]{0,127})?` +
	// Optional digest.
	`(?:@[a-z0-9]+(?:[.+_\-][a-z0-9]+)*:[a-fA-F0-9]{32,})?` +
	`$`)

// ContainerImage is a Constraint that requires a valid OCI container image
// reference (e.g. "alpine:latest", "ubuntu:22.04", "docker.io/library/nginx:1.25",
// "my.registry.io:5000/project/image@sha256:abc...").
//
// The reference is checked against the OCI Distribution Spec format:
//
//	[registry-host[:port]/]repository[:tag][@digest]
//
// No Schema annotation — there is no standard JSON Schema format for container
// image references.
var ContainerImage = codex.Constraint[string]{
	Name:    "container-image",
	Check:   func(v string) bool { return reContainerImage.MatchString(v) },
	Message: func(v string) string { return fmt.Sprintf("invalid container image reference: %q", v) },
}

// reDigest matches a content digest, "algorithm:hex" (e.g.
// "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
// — the SAME digest-segment shape [ContainerImage] itself checks after "@",
// extracted here as its own standalone constraint since a content digest
// also appears on its own in many container/registry APIs (a manifest's
// own content-addressable digest, a resolved image's pinned digest, etc.)
// — not only as an optional suffix of a full image reference.
var reDigest = regexp.MustCompile(`^[a-z0-9]+(?:[.+_\-][a-z0-9]+)*:[a-fA-F0-9]{32,}$`)

// Digest is a Constraint that requires a valid content digest in
// "algorithm:hex" form (e.g. "sha256:abc...", the OCI/Docker Distribution
// Spec convention for content-addressable references to images,
// manifests, and layers).
//
// No Schema annotation — there is no standard JSON Schema format for
// content digests.
var Digest = codex.Constraint[string]{
	Name:  "digest",
	Check: func(v string) bool { return reDigest.MatchString(v) },
	Message: func(v string) string {
		return fmt.Sprintf("invalid content digest: %q (want \"algorithm:hex\", e.g. \"sha256:...\")", v)
	},
}

// isValidPortNumber reports whether s is a decimal string in the valid
// TCP/UDP port range (1-65535). Shared by [Port] and [DockerPort].
func isValidPortNumber(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

// rePort matches a bare decimal port-number string (no sign, no leading "+").
var rePort = regexp.MustCompile(`^\d{1,5}$`)

// Port is a Constraint that requires a decimal port-number string in the
// valid TCP/UDP port range (1-65535), e.g. "8080", "443", "65535".
//
// Schema sets Pattern to a syntactic hint (`^\d{1,5}$`) — the 1-65535 range
// itself is not expressible in a bare JSON Schema `pattern` regex, so the
// numeric bound is enforced by [Port.Check] only, same rationale as
// [ContainerImage]'s "no standard JSON Schema format" note.
var Port = codex.Constraint[string]{
	Name: "port",
	Check: func(v string) bool {
		return rePort.MatchString(v) && isValidPortNumber(v)
	},
	Message: func(v string) string {
		return fmt.Sprintf("expected port number 1-65535, got %q", v)
	},
	Schema: func(s schema.Schema) schema.Schema {
		s.Pattern = rePort.String()
		return s
	},
}

// reDockerPort matches a Docker port-spec string: a decimal port number
// followed by "/tcp" or "/udp" (e.g. "8080/tcp", "53/udp"). The port number's
// numeric range (1-65535) is checked separately in [DockerPort.Check] — a
// bare regex cannot express that bound.
var reDockerPort = regexp.MustCompile(`^(\d{1,5})/(tcp|udp)$`)

// DockerPort is a Constraint that requires a Docker port-spec string in the
// form "<port>/tcp" or "<port>/udp" (e.g. "8080/tcp", "53/udp"), with the
// port number checked against the valid range (1-65535). This is the key
// shape used by Docker's ExposedPorts and HostConfig.PortBindings maps in
// container create-options documents.
//
// No Schema annotation beyond the syntactic pattern — see [Port]'s docs for
// why the numeric range cannot be expressed in the schema Pattern alone.
var DockerPort = codex.Constraint[string]{
	Name: "docker-port",
	Check: func(v string) bool {
		m := reDockerPort.FindStringSubmatch(v)
		return m != nil && isValidPortNumber(m[1])
	},
	Message: func(v string) string {
		return fmt.Sprintf(`expected port spec "<port>/tcp" or "<port>/udp" (1-65535), got %q`, v)
	},
	Schema: func(s schema.Schema) schema.Schema {
		s.Pattern = reDockerPort.String()
		return s
	},
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
// non-negative integer (>= 0).
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

// reEnvVar matches a valid POSIX environment variable name: starts with an
// uppercase letter or underscore, followed by uppercase letters, digits, or
// underscores. Lowercase letters and hyphens are not allowed.
var reEnvVar = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// EnvVarName is a Constraint that requires a valid POSIX environment variable
// name: the value must start with an uppercase letter or underscore, and contain
// only uppercase letters (A-Z), digits (0-9), and underscores.
//
// Rejects lowercase names ("log_level"), names with hyphens ("APP-PORT"),
// names that start with a digit ("1STVAR"), and names with spaces.
//
// Use this constraint when environment variable names arrive from external input
// (configuration files, CLI flags, user-provided overrides) rather than as
// Go code literals, so that programming errors are caught before the name is
// passed to [config.FromEnvVar] or [os.LookupEnv].
//
// Compose with [EnvVarPrefix] to enforce both format and namespace:
//
//	appVarCodec := codex.String().
//	    Refine(validate.EnvVarName).
//	    Refine(validate.EnvVarPrefix("APP_"))
var EnvVarName = codex.Constraint[string]{
	Name:  "envVarName",
	Check: func(v string) bool { return reEnvVar.MatchString(v) },
	Message: func(v string) string {
		return fmt.Sprintf("invalid env var name %q: must match [A-Z_][A-Z0-9_]*", v)
	},
	Schema: func(s schema.Schema) schema.Schema {
		s.Pattern = reEnvVar.String()
		return s
	},
}

// EnvVarPrefix returns a Constraint that requires the string to begin with the
// given prefix. Use this with [EnvVarName] to enforce both the POSIX format
// and a project-specific namespace (e.g. "APP_"):
//
//	appVarCodec := codex.String().
//	    Refine(validate.EnvVarName).
//	    Refine(validate.EnvVarPrefix("APP_"))
//
// The prefix itself is not validated against [EnvVarName]; ensure it follows
// the same conventions (e.g. "APP_" not "app_").
func EnvVarPrefix(prefix string) codex.Constraint[string] {
	return codex.Constraint[string]{
		Name:  fmt.Sprintf("envVarPrefix(%s)", prefix),
		Check: func(v string) bool { return strings.HasPrefix(v, prefix) },
		Message: func(v string) string {
			return fmt.Sprintf("env var name %q must start with prefix %q", v, prefix)
		},
	}
}

// jwtRe matches a compact JWT: three base64url-encoded segments separated by dots.
// It does not verify signatures or decode payloads.
var jwtRe = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)

// BearerToken is a Constraint that validates a non-empty bearer token string.
// It accepts any non-empty string without leading or trailing whitespace.
// Use with [api/rest.SecurityScheme] or [api/events.SecurityScheme] Codec to
// format-check extracted bearer tokens before calling SecurityFunc.
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
