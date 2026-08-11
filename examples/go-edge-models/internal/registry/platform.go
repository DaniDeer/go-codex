package internal

import (
	"fmt"
	"regexp"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the "os/arch" platform selector concept — the struct,
// its constraint, and its codec.

// PlatformSelector is a parsed "os/arch" platform selector (e.g.
// "linux/amd64"), decoded/encoded via PlatformSelectorCodec.
type PlatformSelector struct {
	OS           string
	Architecture string
}

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

var platformSelectorStructCodec = c.Struct[PlatformSelector](
	c.RequiredField("os", c.String().Refine(v.NonEmptyString),
		func(p PlatformSelector) string { return p.OS },
		func(p *PlatformSelector, val string) { p.OS = val },
	),
	c.RequiredField("architecture", c.String().Refine(v.NonEmptyString),
		func(p PlatformSelector) string { return p.Architecture },
		func(p *PlatformSelector, val string) { p.Architecture = val },
	),
)

// PlatformSelectorCodec decodes/encodes an "os/arch" platform selector
// string to/from the structured PlatformSelector.
var PlatformSelectorCodec = c.MapCodecValidated(
	c.String().Refine(PlatformConstraint), platformSelectorStructCodec,
	func(s string) (PlatformSelector, error) {
		parts := strings.SplitN(s, "/", 2)
		return PlatformSelector{OS: parts[0], Architecture: parts[1]}, nil
	},
	func(p PlatformSelector) (string, error) {
		return p.OS + "/" + p.Architecture, nil
	},
)
