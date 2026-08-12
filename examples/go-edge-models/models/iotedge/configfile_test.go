package iotedge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/ports"
)

func sampleManifest() DeploymentManifest {
	return DeploymentManifest{
		ModulesContent: ModulesContent{
			EdgeAgent: Modules{
				"factory-dashboard": ModuleConfig{
					Settings:      ModuleSettings{Image: docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
			},
		},
	}
}

func TestNewConfigFile_ReadRoundTrip(t *testing.T) {
	basePath := t.TempDir()
	usecasesDir := filepath.Join(basePath, "usecases")
	if err := os.MkdirAll(usecasesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	manifest := sampleManifest()
	raw, err := ConfigFileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(usecasesDir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fh := NewConfigFile(basePath)
	got, err := fh.Read(map[string]string{"usecase_name": "usecase1"}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestNewConfigFile_WriteThenRead(t *testing.T) {
	basePath := t.TempDir()

	fh := NewConfigFile(basePath)
	manifest := sampleManifest()
	vars := map[string]string{"usecase_name": "usecase1"}
	if _, err := fh.Write(vars, manifest, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := fh.Read(vars, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.ModulesContent.EdgeAgent) != 1 {
		t.Errorf("EdgeAgent len = %d, want 1", len(got.ModulesContent.EdgeAgent))
	}
}

func TestNewConfigFile_DifferentUseCasesAreIndependent(t *testing.T) {
	basePath := t.TempDir()

	fh := NewConfigFile(basePath)

	if _, err := fh.Write(map[string]string{"usecase_name": "usecase-a"}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write A: %v", err)
	}
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase-b"}, ports.FileOptions{}); err == nil {
		t.Error("Read B: want error (usecase-b was never written), got nil")
	}
}

func TestNewConfigFile_MissingUseCaseNameVar_ReturnsMissingFilePathVarError(t *testing.T) {
	basePath := t.TempDir()
	fh := NewConfigFile(basePath)
	if _, err := fh.Read(nil, ports.FileOptions{}); err == nil {
		t.Error("Read: want MissingFilePathVarError for missing usecase_name, got nil")
	}
}
