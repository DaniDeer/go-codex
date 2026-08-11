package iotedge

import (
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regiotedge "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/ports"
)

// ── ReadConfig ────────────────────────────────────────────────────────────────

// ReadConfig reads the deployment manifest at manifestPath — a thin
// wrapper over models/iotedge's declared ConfigFile port.
func ReadConfig(manifestPath string, opts ports.FileOptions) (regiotedge.DeploymentManifest, error) {
	return regiotedge.NewConfigFile(manifestPath).Read(nil, opts)
}

// ── PatchModule / UpdateModuleImage ──────────────────────────────────────────

// PatchModule applies patch — any subset of one module's fields (see
// modulepatch.ModuleFieldsPatch's own doc comment) — to the deployment
// manifest at manifestPath, leaving every other field on that module, and
// every other module, untouched.
func PatchModule(manifestPath string, patch modulepatch.ModuleFieldsPatch, opts ports.FileOptions) error {
	return ports.PatchEncoded(regiotedge.NewConfigFile(manifestPath), nil,
		modulepatch.ModuleFieldsPatchCodec, patch, opts)
}

// UpdateModuleImage updates ONE module's image — a thin convenience over
// [PatchModule] for the single most common patch operation. The patch
// itself is built and validated by modulepatch.NewUpdateModuleImagePatch
// (moduleName's slug shape and image's Name/Tag/Digest constraints are
// both checked there, before this ever touches disk).
func UpdateModuleImage(manifestPath string, moduleName regiotedge.ModuleName, image docker.Image, opts ports.FileOptions) error {
	patch, err := modulepatch.NewUpdateModuleImagePatch(moduleName, image)
	if err != nil {
		return err
	}
	return PatchModule(manifestPath, patch, opts)
}
