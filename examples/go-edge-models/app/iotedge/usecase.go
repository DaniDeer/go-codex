package iotedge

import (
	"errors"
	"os"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
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

// ── PatchDeviceModule / UpdateDeviceModuleImage ──────────────────────────────
//
// The DEVICE-scoped analogues of PatchUseCaseModule/UpdateUseCaseModuleImage:
// instead of patching the use case's shared TEMPLATE, these write into
// ONE device's OWN config file — isolated and reversible, never
// touching the template or any other device.

// PatchDeviceModule applies patch — any subset of one module's fields
// (see modulepatch.FieldsPatch's own doc comment) — to deviceID's OWN
// config file under basePath/useCaseName, leaving the use case template
// and every OTHER device completely untouched. Reuses
// modulepatch.FieldsBodyCodec to encode patch's typed fields into the
// SAME raw shape deviceconfig.Patch.EdgeAgent expects — bridging
// modulepatch's TEMPLATE-level typed validation to the DEVICE level with
// zero duplicated logic.
//
// Two cases, both ending in the SAME deep-merge guarantee (every OTHER
// override already on this device's file survives untouched):
//   - The device already has a config file: deep-merges patch's delta
//     into it via [ports.PatchEncoded] (same mechanism
//     PatchUseCaseModule uses on the template).
//   - This is the device's FIRST-EVER override: [ports.PatchEncoded]
//     requires an EXISTING file to merge into, so this case writes
//     patch's delta directly via [usecase.WriteDeviceConfig] instead —
//     detected via errors.Is(err, os.ErrNotExist) on the read attempt.
//     Any OTHER read error (e.g. an existing-but-malformed device file)
//     propagates as-is, never silently overwritten.
func PatchDeviceModule(basePath, useCaseName, deviceID string, patch modulepatch.FieldsPatch, opts ports.FileOptions) error {
	ucName, err := usecase.NewName(useCaseName)
	if err != nil {
		return err
	}
	devID, err := usecase.NewDeviceID(deviceID)
	if err != nil {
		return err
	}

	rawBody, err := modulepatch.FieldsBodyCodec.Encode(patch)
	if err != nil {
		return err
	}
	delta := deviceconfig.Patch{EdgeAgent: map[string]any{string(patch.ModuleName): rawBody}}

	_, readErr := usecase.ReadDeviceConfig(basePath, ucName, devID, opts)
	switch {
	case readErr == nil:
		return ports.PatchEncoded(usecase.NewDeviceFile(basePath), map[string]string{
			"usecase_name": useCaseName,
			"device_id":    deviceID,
		}, deviceconfig.PatchCodec, delta, opts)
	case errors.Is(readErr, os.ErrNotExist):
		// First-ever override for this device: the "devices/{useCaseName}/"
		// directory may not exist yet either — force CreateDirs regardless
		// of what the caller passed, since a caller reasonably expects a
		// FIRST override to "just work" without knowing in advance whether
		// this device already has a config file.
		firstWriteOpts := opts
		firstWriteOpts.CreateDirs = true
		_, writeErr := usecase.WriteDeviceConfig(basePath, ucName, usecase.DeviceConfig{DeviceID: devID, Patch: delta}, firstWriteOpts)
		return writeErr
	default:
		return readErr
	}
}

// UpdateDeviceModuleImage updates ONE module's image for ONE device only
// — a thin convenience over [PatchDeviceModule] for the single most
// common patch operation, mirroring [UpdateUseCaseModuleImage] exactly
// but scoped to deviceID's own config file. The patch itself is built
// and validated by modulepatch.NewUpdateModuleImage (moduleName's slug
// shape and image's Name/Tag/Digest constraints are both checked there,
// before this ever touches disk).
func UpdateDeviceModuleImage(basePath, useCaseName, deviceID string, moduleName manifesttemplate.ModuleName, image docker.Image, opts ports.FileOptions) error {
	patch, err := modulepatch.NewUpdateModuleImage(moduleName, image)
	if err != nil {
		return err
	}
	return PatchDeviceModule(basePath, useCaseName, deviceID, patch, opts)
}
