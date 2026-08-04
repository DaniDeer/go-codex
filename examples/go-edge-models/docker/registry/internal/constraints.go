package internal

import (
	"fmt"
	"regexp"

	c "github.com/DaniDeer/go-codex/codex"
)

// rePlatform matches a "os/arch" platform selector, e.g. "linux/amd64",
// "linux/arm64" — the shape a platform selector string and
// PlatformDescriptor's OS/Architecture pair are compared against.
var rePlatform = regexp.MustCompile(`^[a-z0-9]+/[a-z0-9]+$`)

// PlatformConstraint validates a platform selector's "os/arch" shape.
var PlatformConstraint = c.Constraint[string]{
	Name:  "platform",
	Check: func(v string) bool { return rePlatform.MatchString(v) },
	Message: func(v string) string {
		return fmt.Sprintf("platform %q must be \"os/arch\" (e.g. \"linux/amd64\")", v)
	},
}

// reDigest matches a content digest, "algorithm:hex" (e.g.
// "sha256:e3b0c4429...") — the same digest-segment shape validate.ContainerImage
// itself checks after "@", extracted here as its own standalone constraint
// since digests also appear on their own (ManifestDescriptor.Digest,
// ManifestEnvelope.Digest) — not only as an optional suffix of a full image
// reference.
var reDigest = regexp.MustCompile(`^[a-z0-9]+(?:[.+_\-][a-z0-9]+)*:[a-fA-F0-9]{32,}$`)

// DigestConstraint validates a bare content digest's "algorithm:hex" shape.
var DigestConstraint = c.Constraint[string]{
	Name:  "digest",
	Check: func(v string) bool { return reDigest.MatchString(v) },
	Message: func(v string) string {
		return fmt.Sprintf("digest %q must be \"algorithm:hex\" (e.g. \"sha256:...\")", v)
	},
}

// ActionsConstraint requires DockerScope.Actions to contain at least one
// action — an empty actions list would encode to a scope string with a
// trailing empty segment ("repository:name:"), which no real registry
// treats as meaningful.
var ActionsConstraint = c.Constraint[[]string]{
	Name:  "actions",
	Check: func(v []string) bool { return len(v) > 0 },
	Message: func(v []string) string {
		return "actions must contain at least one action (e.g. \"pull\")"
	},
}
