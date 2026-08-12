package iotedge

import (
	"path/filepath"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	regiotedge "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/ports"
)

func sampleManifest() regiotedge.DeploymentManifest {
	return regiotedge.DeploymentManifest{
		ModulesContent: regiotedge.ModulesContent{
			EdgeAgent: regiotedge.Modules{
				"factory-dashboard": regiotedge.ModuleConfig{
					Settings:      regiotedge.ModuleSettings{Image: docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
			},
		},
	}
}

func writeSampleManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	fh := regiotedge.NewConfigFile(path)
	if _, err := fh.Write(nil, sampleManifest(), ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return path
}

func TestReadConfig_ReturnsWrittenManifest(t *testing.T) {
	path := writeSampleManifest(t)

	got, err := ReadConfig(path, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadConfig: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestReadConfig_PropagatesMissingFileError(t *testing.T) {
	_, err := ReadConfig("/nonexistent/path/manifest.json", ports.FileOptions{})
	if err == nil {
		t.Error("ReadConfig: want error for nonexistent file, got nil")
	}
}

func TestUpdateModuleImage_PatchesOnlyImage(t *testing.T) {
	path := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
	if err := UpdateModuleImage(path, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateModuleImage: %v", err)
	}

	got, err := ReadConfig(path, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadConfig after update: %v", err)
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

func TestUpdateModuleImage_RejectsInvalidImage(t *testing.T) {
	path := writeSampleManifest(t)

	// modulepatch.NewUpdateModuleImagePatch validates the image BEFORE any
	// disk I/O happens — an empty Name must be rejected here, not silently
	// written to the manifest.
	err := UpdateModuleImage(path, "factory-dashboard", docker.Image{}, ports.FileOptions{})
	if err == nil {
		t.Fatal("UpdateModuleImage: want error for invalid image, got nil")
	}

	got, readErr := ReadConfig(path, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("ReadConfig: %v", readErr)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged ghcr.io/org/edge-web:1.0.0 (invalid patch must not touch disk)", dashboard.Settings.Image)
	}
}

func TestPatchModule_PatchesMultipleFieldsLeavesOthersUntouched(t *testing.T) {
	path := writeSampleManifest(t)

	status := regiotedge.Status("stopped")
	if err := PatchModule(path, modulepatch.ModuleFieldsPatch{
		ModuleName: "factory-dashboard",
		Status:     &status,
	}, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchModule: %v", err)
	}

	got, err := ReadConfig(path, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadConfig after patch: %v", err)
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

func TestPatchModule_PropagatesEmptyPatchError(t *testing.T) {
	path := writeSampleManifest(t)

	err := PatchModule(path, modulepatch.ModuleFieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchModule: want error for empty patch, got nil")
	}
}
