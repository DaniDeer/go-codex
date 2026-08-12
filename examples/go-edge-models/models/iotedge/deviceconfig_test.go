package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/ports"
)

func TestWriteDeviceConfig_ReadDeviceConfig_RoundTrip(t *testing.T) {
	basePath := t.TempDir()
	cfg := DeviceConfig{DeviceID: "sensor-42", DeviceManifest: sampleDeviceManifest()}

	if _, err := WriteDeviceConfig(basePath, "usecase1", cfg, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	got, err := ReadDeviceConfig(basePath, "usecase1", "sensor-42", ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadDeviceConfig: %v", err)
	}
	if got.DeviceID != "sensor-42" {
		t.Errorf("DeviceID = %q, want sensor-42", got.DeviceID)
	}
	if got.DeviceManifest.DisplayName != "Sensor 42" || !got.DeviceManifest.Enabled {
		t.Errorf("DeviceManifest = %+v, want DisplayName=Sensor 42 Enabled=true", got.DeviceManifest)
	}
}

func TestReadDeviceConfig_PropagatesMissingFileError(t *testing.T) {
	basePath := t.TempDir()
	_, err := ReadDeviceConfig(basePath, "usecase1", "does-not-exist", ports.FileOptions{})
	if err == nil {
		t.Error("ReadDeviceConfig: want error for nonexistent device, got nil")
	}
}
