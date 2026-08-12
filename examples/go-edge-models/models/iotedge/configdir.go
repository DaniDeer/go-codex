package iotedge

import (
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/validate"
)

// ── ConfigDir ─────────────────────────────────────────────────────────────────

// useCaseNameCodec validates the {useCase} segment extracted from each
// config file's name — reuses the same NonEmptyString constraint every
// other iotedge string identifier (ModuleName, etc.) already applies.
var useCaseNameCodec = codex.String().Refine(validate.NonEmptyString)

// ConfigDirEntryPattern declares the filename SHAPE for iotedge config
// files inside a config directory: each file is one "use case", and the
// filename (minus ".json") IS that use case's name — e.g. "usecase1.json"
// is the "usecase1" use case's deployment manifest. A file that doesn't
// match this shape at all (a stray ".gitkeep"/README alongside the config
// files) is silently excluded by [ports.Dir.List] — see
// [ports.EntryPattern]'s own doc.
var ConfigDirEntryPattern = ports.EntryPattern{
	Template: "{useCase}.json",
	Params:   []ports.EntryParam{{Name: "useCase", Codec: &useCaseNameCodec}},
}

// NewConfigDir declares the directory-listing port for a directory of
// iotedge config files (deployment manifests), one per use case — mirrors
// [NewConfigFile]'s shape exactly: a thin, pure (no I/O) constructor over
// [ports.NewDir], using [ConfigDirEntryPattern] so [ports.Dir.List]
// discovers which use cases exist and extracts each one's name, the same
// declarative way [NewConfigFile]'s caller validates a manifest's own path
// variables.
//
// The returned port is read-only (listing has no write/patch operation) —
// pair its result with [NewConfigFile] to read/patch a SPECIFIC discovered
// use case's manifest; see app/iotedge's usage.
func NewConfigDir(path string) ports.Dir {
	return ports.NewDir(path, ports.WithEntryPattern(ConfigDirEntryPattern))
}
