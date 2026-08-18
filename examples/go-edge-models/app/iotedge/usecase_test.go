package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	"github.com/DaniDeer/go-codex/ports"
)

func sampleManifest() manifesttemplate.DeploymentManifest {
	return manifesttemplate.DeploymentManifest{
		ModulesContent: manifesttemplate.ModulesContent{
			EdgeAgent: manifesttemplate.Modules{
				"factory-dashboard": manifesttemplate.ModuleConfig{
					Settings:      manifesttemplate.ModuleSettings{Image: docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
			},
		},
	}
}

const sampleUseCaseName = "usecase1"

func writeSampleManifest(t *testing.T) string {
	t.Helper()
	basePath := t.TempDir()
	fh := usecase.NewFile(basePath)
	if _, err := fh.Write(map[string]string{"usecase_name": sampleUseCaseName}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return basePath
}

func TestReadUseCase_ReturnsWrittenManifest(t *testing.T) {
	basePath := writeSampleManifest(t)

	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestReadUseCase_PropagatesMissingFileError(t *testing.T) {
	_, err := ReadUseCase("/nonexistent/path", "nonexistent-usecase", ports.FileOptions{})
	if err == nil {
		t.Error("ReadUseCase: want error for nonexistent file, got nil")
	}
}

func TestUpdateUseCaseModuleImage_PatchesOnlyImage(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
	if err := UpdateUseCaseModuleImage(basePath, sampleUseCaseName, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateUseCaseModuleImage: %v", err)
	}

	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after update: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:2.0.0", dashboard.Settings.Image)
	}
	// Other fields must survive the patch untouched.
	if dashboard.Status != "running" {
		t.Errorf("Status = %v, want unchanged \"running\"", dashboard.Status)
	}
	if dashboard.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged \"always\"", dashboard.RestartPolicy)
	}
	if dashboard.Version != "1.0" {
		t.Errorf("Version = %v, want unchanged \"1.0\"", dashboard.Version)
	}
}

func TestUpdateUseCaseModuleImage_RejectsInvalidImage(t *testing.T) {
	basePath := writeSampleManifest(t)

	// modulepatch.NewUpdateModuleImagePatch validates the image BEFORE any
	// disk I/O happens — an empty Name must be rejected here, not silently
	// written to the manifest.
	err := UpdateUseCaseModuleImage(basePath, sampleUseCaseName, "factory-dashboard", docker.Image{}, ports.FileOptions{})
	if err == nil {
		t.Fatal("UpdateUseCaseModuleImage: want error for invalid image, got nil")
	}

	got, readErr := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("ReadUseCase: %v", readErr)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged ghcr.io/org/edge-web:1.0.0 (invalid patch must not touch disk)", dashboard.Settings.Image)
	}
}

func TestPatchUseCaseModule_PatchesMultipleFieldsLeavesOthersUntouched(t *testing.T) {
	basePath := writeSampleManifest(t)

	status := manifesttemplate.Status("stopped")
	if err := PatchUseCaseModule(basePath, sampleUseCaseName, modulepatch.FieldsPatch{
		ModuleName: "factory-dashboard",
		Status:     &status,
	}, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchUseCaseModule: %v", err)
	}

	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after patch: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Status != "stopped" {
		t.Errorf("Status = %v, want \"stopped\"", dashboard.Status)
	}
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
	if dashboard.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged \"always\"", dashboard.RestartPolicy)
	}
}

func TestPatchUseCaseModule_PropagatesEmptyPatchError(t *testing.T) {
	basePath := writeSampleManifest(t)

	err := PatchUseCaseModule(basePath, sampleUseCaseName, modulepatch.FieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchUseCaseModule: want error for empty patch, got nil")
	}
}

// ── PatchDeviceModule / UpdateDeviceModuleImage ──────────────────────────────

const sampleDeviceID = "sensor-1"

func TestUpdateDeviceModuleImage_FirstOverride_WritesNewDeviceFile(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "9.9.9"}
	if err := UpdateDeviceModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateDeviceModuleImage: %v", err)
	}

	// The TEMPLATE must be completely untouched.
	template, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if template.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("template image must stay unchanged by a device-scoped update")
	}

	// The DEVICE's effective config must reflect the override.
	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	dashboard := effective.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:9.9.9" {
		t.Errorf("effective Image = %v, want ghcr.io/org/edge-web:9.9.9", dashboard.Settings.Image)
	}
	// Fields the patch never touched must survive from the template.
	if dashboard.Status != "running" {
		t.Errorf("effective Status = %v, want unchanged running", dashboard.Status)
	}
}

func TestPatchDeviceModule_SecondOverride_MergesOntoExistingDeviceFile(t *testing.T) {
	basePath := writeSampleManifest(t)

	// First override: image.
	firstImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
	if err := UpdateDeviceModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "factory-dashboard", firstImage, ports.FileOptions{}); err != nil {
		t.Fatalf("first UpdateDeviceModuleImage: %v", err)
	}

	// Second override: status, on a DIFFERENT field of the SAME module.
	status := manifesttemplate.Status("stopped")
	if err := PatchDeviceModule(basePath, sampleUseCaseName, sampleDeviceID, modulepatch.FieldsPatch{
		ModuleName: "factory-dashboard",
		Status:     &status,
	}, ports.FileOptions{}); err != nil {
		t.Fatalf("second PatchDeviceModule: %v", err)
	}

	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	dashboard := effective.ModulesContent.EdgeAgent["factory-dashboard"]
	// BOTH overrides must be present — the second patch must not have
	// clobbered the first.
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:2.0.0 (from the FIRST override)", dashboard.Settings.Image)
	}
	if dashboard.Status != "stopped" {
		t.Errorf("Status = %v, want stopped (from the SECOND override)", dashboard.Status)
	}
}

func TestUpdateDeviceModuleImage_IntroducesNewModuleAtDeviceLevel(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/extra-sensor-agent", Tag: "1.0.0"}
	patch, err := modulepatch.NewUpdateModuleImage("extra-sensor-agent", newImage)
	if err != nil {
		t.Fatalf("NewUpdateModuleImage: %v", err)
	}
	// A brand-new module needs its OTHER required fields set too — Image
	// alone isn't a complete ModuleConfig.
	moduleType := manifesttemplate.Type("docker")
	moduleStatus := manifesttemplate.Status("running")
	restartPolicy := manifesttemplate.RestartPolicy("always")
	version := manifesttemplate.Version("1.0")
	patch.Type = &moduleType
	patch.Status = &moduleStatus
	patch.RestartPolicy = &restartPolicy
	patch.Version = &version

	if err := PatchDeviceModule(basePath, sampleUseCaseName, sampleDeviceID, patch, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchDeviceModule: %v", err)
	}

	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	extra, ok := effective.ModulesContent.EdgeAgent["extra-sensor-agent"]
	if !ok {
		t.Fatal("ReadEffective: expected a brand-new module introduced at the device level")
	}
	if extra.Settings.Image.String() != "ghcr.io/org/extra-sensor-agent:1.0.0" {
		t.Errorf("new module Image = %v, want ghcr.io/org/extra-sensor-agent:1.0.0", extra.Settings.Image)
	}
	// The ORIGINAL template module must be completely untouched.
	if effective.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("factory-dashboard must be untouched by a patch introducing a different module")
	}
}

func TestPatchDeviceModule_RejectsInvalidUseCaseName(t *testing.T) {
	basePath := writeSampleManifest(t)
	err := PatchDeviceModule(basePath, "", sampleDeviceID, modulepatch.FieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchDeviceModule: want error for empty useCaseName, got nil")
	}
}

func TestPatchDeviceModule_RejectsInvalidDeviceID(t *testing.T) {
	basePath := writeSampleManifest(t)
	err := PatchDeviceModule(basePath, sampleUseCaseName, "", modulepatch.FieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchDeviceModule: want error for empty deviceID, got nil")
	}
}
