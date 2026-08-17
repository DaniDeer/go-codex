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
