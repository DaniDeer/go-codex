package main

import (
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	v "github.com/DaniDeer/go-codex/validate"
)

// ── Domain types ──────────────────────────────────────────────────────────────

type ModuleSettings struct {
	Image string
	// CreateOptions is a Docker create-options document. On the wire it is a
	// JSON-escaped STRING (e.g. "{\"ExposedPorts\":{...}}") — format.EmbeddedJSON
	// below handles the string-escaping transparently, so this field is the
	// fully-typed CreateOptions value, not a raw string.
	CreateOptions CreateOptions
}

// ── Codecs ────────────────────────────────────────────────────────────────────

var ModuleSettingsCodec = c.Struct[ModuleSettings](
	c.RequiredField("image",
		c.String().Refine(v.NonEmptyString, v.ContainerImage),
		func(ms ModuleSettings) string { return ms.Image },
		func(ms *ModuleSettings, v string) { ms.Image = v },
	),
	c.RequiredField("createOptions",
		format.EmbeddedJSON(CreateOptionsCodec),
		func(ms ModuleSettings) CreateOptions { return ms.CreateOptions },
		func(ms *ModuleSettings, val CreateOptions) { ms.CreateOptions = val },
	),
)
