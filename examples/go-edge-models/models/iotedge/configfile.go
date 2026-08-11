package iotedge

import (
	f "github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

// ── ConfigFile ────────────────────────────────────────────────────────────────

// ConfigFileFormat is the declared, reusable format/codec pairing for an
// iotedge deployment manifest file — no I/O by itself; it exists so
// NewConfigFile (below) and any caller building their own ports.File
// don't have to repeat format.JSON(DeploymentManifestCodec).
var ConfigFileFormat = f.JSON(DeploymentManifestCodec)

// NewConfigFile declares the file port for the deployment manifest at
// path — a thin, pure (no I/O) constructor over ports.NewFile, using
// ConfigFileFormat. path is a concrete, caller-supplied file location:
// this package has no opinion about where a manifest actually lives, and
// a single "{var}" path template cannot capture an arbitrary multi-segment
// path (template vars never cross a "/" boundary — see
// internal/templatematch), so path is taken as the file port's WHOLE
// template (zero vars) rather than substituted into one.
//
// The returned port is reused for BOTH reading (File.Read) and patching
// (ports.PatchEncoded(file, ...)) — the same file, two different
// operations; see app/iotedge's ReadConfig/PatchModule.
func NewConfigFile(path string) ports.File[DeploymentManifest] {
	return ports.NewFile[DeploymentManifest](path, ConfigFileFormat)
}
