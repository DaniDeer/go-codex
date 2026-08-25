package dockercompose

import (
	c "github.com/DaniDeer/go-codex/codex"
	f "github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

// ── Project ───────────────────────────────────────────────────────────────────

// ServiceName is a Compose service's own name — the key under the
// top-level `services:` map. Deliberately UNCONSTRAINED (no slug/format
// validation): Compose itself imposes only a loose YAML-key restriction,
// far less strict than an IoT Edge module name's own `validate.Slug`
// requirement (underscores and mixed case are common and valid in real
// compose files) — sanitizing a ServiceName into a valid IoT Edge
// module name is the sibling fromcompose package's job, not this
// package's; a ServiceName round-trips through this package completely
// unchanged.
type ServiceName string

// Project is a Compose file's top-level document — just its named
// services map, since every other top-level key (`networks:`,
// `volumes:`, `configs:`, `secrets:`) is out of scope (see this
// package's own doc comment).
type Project struct {
	Services map[ServiceName]Service
}

// serviceNameCodec wraps a plain string as ServiceName — a trivial,
// always-succeeding conversion (see ServiceName's own doc comment for
// why no constraint is applied here).
var serviceNameCodec = c.MapCodecSafe(
	c.String(),
	func(s string) ServiceName { return ServiceName(s) },
	func(n ServiceName) (string, error) { return string(n), nil },
)

// ServicesCodec decodes/encodes a Project's "services" map — a plain
// codex.Map[ServiceName, Service], matching the same
// map[K]V-over-a-named-object-key pattern used throughout this module
// (e.g. iothub.Modules/iothub.ModulesCodec). Exported (rather than only
// ever appearing inline inside ProjectCodec) so the sibling
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/fromcompose]
// package can re-encode/re-decode a services map on its own — the
// "re-encode via one Map codec, re-decode via another" transcoding
// pattern that package's own ConvertProject/ConvertDeployment rely on.
var ServicesCodec = c.Map[ServiceName, Service](serviceNameCodec, ServiceCodec)

// ProjectCodec decodes/encodes a Project — just its "services" field,
// built from [ServicesCodec].
var ProjectCodec = c.Struct[Project](
	c.RequiredField("services",
		ServicesCodec,
		func(p Project) map[ServiceName]Service { return p.Services },
		func(p *Project, val map[ServiceName]Service) { p.Services = val },
	),
)

// FileFormat is the declared, reusable format/codec pairing for a
// Compose file — no I/O by itself; it exists so [NewFile] and any
// caller building their own ports.File don't have to repeat
// format.YAML(ProjectCodec), mirroring
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase.FileFormat].
var FileFormat = f.YAML(ProjectCodec)

// NewFile declares the file port for a Compose project file at an
// ARBITRARY, caller-supplied path — a thin, pure (no I/O) constructor
// over [ports.NewFile], using [FileFormat]. Unlike
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase.NewFile]
// (basePath + templated "{usecase_name}.json", since that package OWNS
// a known directory layout), a Compose file has no fixed layout this
// package manages — path is the file's ACTUAL location, used AS-IS,
// with no "{var}" placeholders (mirrors
// [github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase.NewBaselineFile]'s
// "single, already-concrete path" shape). Read/Write take a nil vars
// map, e.g. NewFile(composeFilePath).Read(nil, opts).
func NewFile(path string) ports.File[Project] {
	return ports.NewFile[Project](path, FileFormat)
}
