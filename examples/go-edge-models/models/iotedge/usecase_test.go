package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/ports"
)

func TestWriteUseCase_ReadUseCase_RoundTrip_WithNestedDevices(t *testing.T) {
	basePath := t.TempDir()

	uc := UseCase{
		Name:               "usecase1",
		DeploymentManifest: sampleManifest(),
		Devices: []DeviceConfig{
			{DeviceID: "sensor-1", DeviceManifest: DeviceManifest{DisplayName: "Sensor One", Enabled: true}},
			{DeviceID: "sensor-2", DeviceManifest: DeviceManifest{DisplayName: "Sensor Two", Enabled: false}},
		},
	}

	if err := WriteUseCase(basePath, uc, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCase: %v", err)
	}

	got, err := ReadUseCase(basePath, "usecase1", ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if got.Name != "usecase1" {
		t.Errorf("Name = %q, want usecase1", got.Name)
	}
	if got.DeploymentManifest.ModulesContent.EdgeAgent["factory-dashboard"].Status != "running" {
		t.Errorf("DeploymentManifest not round-tripped correctly: %+v", got.DeploymentManifest)
	}
	if len(got.Devices) != 2 {
		t.Fatalf("Devices len = %d, want 2: %+v", len(got.Devices), got.Devices)
	}
	byID := map[string]DeviceConfig{}
	for _, d := range got.Devices {
		byID[d.DeviceID] = d
	}
	if byID["sensor-1"].DeviceManifest.DisplayName != "Sensor One" {
		t.Errorf("sensor-1 DisplayName = %q, want Sensor One", byID["sensor-1"].DeviceManifest.DisplayName)
	}
	if !byID["sensor-1"].DeviceManifest.Enabled {
		t.Error("sensor-1 Enabled = false, want true")
	}
	if byID["sensor-2"].DeviceManifest.Enabled {
		t.Error("sensor-2 Enabled = true, want false")
	}
}

func TestReadUseCase_NoDevices_ReturnsEmptyDevicesSlice(t *testing.T) {
	basePath := t.TempDir()
	uc := UseCase{Name: "usecase1", DeploymentManifest: sampleManifest()}
	if err := WriteUseCase(basePath, uc, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCase: %v", err)
	}

	got, err := ReadUseCase(basePath, "usecase1", ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if len(got.Devices) != 0 {
		t.Errorf("Devices = %v, want empty", got.Devices)
	}
}

func TestReadUseCase_PropagatesMissingManifestError(t *testing.T) {
	basePath := t.TempDir()
	_, err := ReadUseCase(basePath, "does-not-exist", ports.FileOptions{})
	if err == nil {
		t.Error("ReadUseCase: want error for nonexistent use case, got nil")
	}
}
