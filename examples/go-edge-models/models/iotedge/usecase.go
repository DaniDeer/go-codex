package iotedge

import (
	"github.com/DaniDeer/go-codex/ports"
)

// ── UseCase ───────────────────────────────────────────────────────────────────

// UseCase is the domain-level composition struct for ONE use case: its
// name (from the file's own path, not its body) paired with its PURE
// [DeploymentManifest] wire content, plus every device nested under it.
// [DeploymentManifest] itself stays pure/unchanged — Name and Devices
// live only on UseCase, assembled by [ReadUseCase]/[WriteUseCase].
type UseCase struct {
	Name               string
	DeploymentManifest DeploymentManifest
	Devices            []DeviceConfig
}

// ReadUseCase reads useCaseName's deployment manifest AND every device
// nested under it, assembling one UseCase value in one call.
func ReadUseCase(basePath, useCaseName string, opts ports.FileOptions) (UseCase, error) {
	manifest, err := NewConfigFile(basePath).Read(map[string]string{"usecase_name": useCaseName}, opts)
	if err != nil {
		return UseCase{}, err
	}

	// CreateIfMissing: a use case with no devices yet has no
	// "devices/{useCaseName}" directory at all — treated as zero devices,
	// not a [ports.DirReadError], the same "nothing to discover yet"
	// semantics [ports.Dir.List] already gives an empty (but existing)
	// directory.
	deviceIDs, err := ListDeviceIDs(basePath, useCaseName, ports.DirOptions{
		Observer:        opts.Observer,
		Context:         opts.Context,
		CreateIfMissing: true,
	})
	if err != nil {
		return UseCase{}, err
	}
	devices := make([]DeviceConfig, len(deviceIDs))
	for i, id := range deviceIDs {
		cfg, err := ReadDeviceConfig(basePath, useCaseName, id, opts)
		if err != nil {
			return UseCase{}, err
		}
		devices[i] = cfg
	}

	return UseCase{Name: useCaseName, DeploymentManifest: manifest, Devices: devices}, nil
}

// WriteUseCase writes uc.DeploymentManifest AND every uc.Devices entry —
// the inverse of [ReadUseCase], one call.
func WriteUseCase(basePath string, uc UseCase, opts ports.FileOptions) error {
	if _, err := NewConfigFile(basePath).Write(map[string]string{"usecase_name": uc.Name}, uc.DeploymentManifest, opts); err != nil {
		return err
	}
	for _, device := range uc.Devices {
		if _, err := WriteDeviceConfig(basePath, uc.Name, device, opts); err != nil {
			return err
		}
	}
	return nil
}
