package internal

import (
	"fmt"
	"regexp"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
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

// DigestConstraint validates a bare content digest's "algorithm:hex" shape
// — a thin re-export of validate.Digest under this package's own name
// (used for ManifestDescriptor.Digest and ManifestEnvelope.Digest, which
// appear as standalone fields, not only as an optional suffix of a full
// image reference like validate.ContainerImage checks).
var DigestConstraint = v.Digest

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
