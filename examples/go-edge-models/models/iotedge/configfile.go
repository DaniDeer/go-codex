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

// NewConfigFile declares the templated file port for a USE CASE's
// deployment manifest under basePath — "{basePath}/usecases/{usecase_name}.json"
// — a thin, pure (no I/O) constructor over ports.NewFile, using
// ConfigFileFormat. usecase_name is validated against useCaseNameCodec
// (the SAME codec [ConfigDirEntryPattern] uses to validate a discovered
// use case's name) via a PLAIN (non-merge) [ports.FilePathParam] —
// [DeploymentManifest] stays pure wire/file content; usecase_name is
// never merged into it. Read/Write take usecase_name via their own vars
// map, e.g. NewConfigFile(basePath).Read(map[string]string{"usecase_name": name}, opts).
//
// The returned port is reused for reading (File.Read), writing
// (File.Write), and patching (ports.PatchEncoded(file, ...)) — see
// [ReadUseCase]/[WriteUseCase] for the higher-level convenience that
// combines this with device discovery, and app/iotedge's
// ReadConfig/PatchModule for direct usage.
func NewConfigFile(basePath string) ports.File[DeploymentManifest] {
	return ports.NewFile[DeploymentManifest](basePath+"/usecases/{usecase_name}.json", ConfigFileFormat,
		ports.FilePathParam{Name: "usecase_name", Codec: &useCaseNameCodec},
	)
}
