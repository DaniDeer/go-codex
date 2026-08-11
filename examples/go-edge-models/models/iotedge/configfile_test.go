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
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	manifest := sampleManifest()
	raw, err := ConfigFileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fh := NewConfigFile(path)
	got, err := fh.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestNewConfigFile_WriteThenRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	fh := NewConfigFile(path)
	manifest := sampleManifest()
	if err := fh.Write(nil, manifest, ports.FileOptions{}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := fh.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.ModulesContent.EdgeAgent) != 1 {
		t.Errorf("EdgeAgent len = %d, want 1", len(got.ModulesContent.EdgeAgent))
	}
}

func TestNewConfigFile_DifferentPathsAreIndependent(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.json")
	pathB := filepath.Join(dir, "b.json")

	fhA := NewConfigFile(pathA)
	fhB := NewConfigFile(pathB)

	if err := fhA.Write(nil, sampleManifest(), ports.FileOptions{}); err != nil {
		t.Fatalf("Write A: %v", err)
	}
	if _, err := fhB.Read(nil, ports.FileOptions{}); err == nil {
		t.Error("Read B: want error (file b.json was never written), got nil")
	}
}
