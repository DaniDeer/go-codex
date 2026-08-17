package iotedge

import (
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	"github.com/DaniDeer/go-codex/ports"
)

// ── ReadUseCase ───────────────────────────────────────────────────────────────

// ReadUseCase reads useCaseName's deployment manifest under basePath — a
// thin wrapper over models/iotedge/usecase's declared file port. Named
// distinctly from usecase.Read (which additionally reads every nested
// device into one composed usecase.UseCase value) — this function
// returns just the deployment manifest.
func ReadUseCase(basePath, useCaseName string, opts ports.FileOptions) (manifesttemplate.DeploymentManifest, error) {
	return usecase.NewFile(basePath).Read(map[string]string{"usecase_name": useCaseName}, opts)
}

// ── PatchUseCaseModule / UpdateUseCaseModuleImage ────────────────────────────

// PatchUseCaseModule applies patch — any subset of one module's fields
// (see modulepatch.FieldsPatch's own doc comment) — to useCaseName's
// deployment manifest under basePath, leaving every other field on that
// module, and every other module, untouched.
func PatchUseCaseModule(basePath, useCaseName string, patch modulepatch.FieldsPatch, opts ports.FileOptions) error {
	return ports.PatchEncoded(usecase.NewFile(basePath), map[string]string{"usecase_name": useCaseName},
		modulepatch.FieldsPatchCodec, patch, opts)
}

// UpdateUseCaseModuleImage updates ONE module's image — a thin
// convenience over [PatchUseCaseModule] for the single most common patch
// operation. The patch itself is built and validated by
// modulepatch.NewUpdateModuleImage (moduleName's slug shape and image's
// Name/Tag/Digest constraints are both checked there, before this ever
// touches disk).
func UpdateUseCaseModuleImage(basePath, useCaseName string, moduleName manifesttemplate.ModuleName, image docker.Image, opts ports.FileOptions) error {
	patch, err := modulepatch.NewUpdateModuleImage(moduleName, image)
	if err != nil {
		return err
	}
	return PatchUseCaseModule(basePath, useCaseName, patch, opts)
}
