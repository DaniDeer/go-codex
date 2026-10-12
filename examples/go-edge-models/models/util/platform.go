package util

import (
	"fmt"
	"strings"

	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/schema"
	v "github.com/DaniDeer/go-codex/validate"
)

// This file holds the "os/arch" platform selector concept — the struct,
// its enum constraint, its codec, and DefaultPlatformCodec — the
// reusable "empty string means the default platform, else a valid
// selector" codec every self-contained registry client's
// GetImageMetadata method uses instead of hand-rolling the same
// empty-check before PlatformSelectorCodec.Decode.

// PlatformSelector is a parsed "os/arch" platform selector (e.g.
// "linux/amd64"), decoded/encoded via PlatformSelectorCodec.
type PlatformSelector struct {
	OS           string
	Architecture string
}

// SupportedPlatforms is the closed list of "os/arch" platform selector
// pairs PlatformConstraint accepts — a real, finite ENUM (not an open
// regex matching any syntactically-shaped "os/arch" string), reflecting
// the platforms this module's registry clients are actually meant to be
// used against.
var SupportedPlatforms = []string{
	"linux/amd64", "linux/arm64", "linux/arm/v7", "linux/386",
	"linux/ppc64le", "linux/s390x", "windows/amd64",
}

// PlatformConstraint validates a platform selector string against
// SupportedPlatforms — an enum (via validate.OneOf), not a pattern
// match; renders as "enum: [...]" in schema output.
var PlatformConstraint = v.OneOf(SupportedPlatforms...)

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

// DefaultPlatform is the "os/arch" selector string used when a caller
// leaves GetImageMetadata's platform argument empty. Must be a member of
// SupportedPlatforms — enforced by mustDecodePlatform below, which
// panics at package init if it ever isn't.
const DefaultPlatform = "linux/amd64"

// DefaultPlatformSelector is DefaultPlatform, pre-parsed via
// PlatformSelectorCodec at package init — guarantees the two can never
// drift out of sync (a hand-duplicated PlatformSelector{OS:"linux",...}
// literal could silently diverge from the string constant above).
var DefaultPlatformSelector = mustDecodePlatform(DefaultPlatform)

// mustDecodePlatform decodes s via PlatformSelectorCodec, PANICKING if
// it fails — only ever called with the compile-time-authored
// DefaultPlatform constant above, so an invalid value is a programming
// error that should fail loudly at package init, not be silently
// propagated.
func mustDecodePlatform(s string) PlatformSelector {
	p, err := PlatformSelectorCodec.Decode(s)
	if err != nil {
		panic(fmt.Sprintf("util: invalid DefaultPlatform %q: %v", s, err))
	}
	return p
}

// emptyPlatformCodec is DefaultPlatformCodec's Left branch — it succeeds
// ONLY for the exact empty string ("" means "caller wants the default
// platform"), encoding struct{}{} back to "".
var emptyPlatformCodec = c.Codec[struct{}]{
	Encode: func(struct{}) (any, error) { return "", nil },
	Decode: func(v any) (struct{}, error) {
		s, ok := v.(string)
		if !ok || s != "" {
			return struct{}{}, fmt.Errorf("expected empty string, got %v", v)
		}
		return struct{}{}, nil
	},
	Schema: schema.Schema{Type: "string", Enum: []any{""}},
}

// DefaultPlatformCodec decodes either the empty string (Left — "use the
// default platform") or a valid, enum-constrained "os/arch" platform
// selector string (Right, via PlatformSelectorCodec) — a real oneOf
// schema, built via codex.Either2. Every self-contained registry
// client's GetImageMetadata method uses this instead of hand-rolling an
// empty-string check before PlatformSelectorCodec.Decode:
//
//	e, err := util.DefaultPlatformCodec.Decode(platform)
//	selector := util.DefaultPlatformSelector
//	if e.IsRight() {
//	    selector = *e.Right
//	}
var DefaultPlatformCodec = c.Either2(emptyPlatformCodec, PlatformSelectorCodec)
