package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/ports"
)

func sampleDeviceManifest() DeviceManifest {
	return DeviceManifest{DisplayName: "Sensor 42", Enabled: true}
}

func TestNewDeviceFile_WriteThenRead(t *testing.T) {
	basePath := t.TempDir()
	fh := NewDeviceFile(basePath)
	vars := map[string]string{"usecase_name": "usecase1", "device_id": "sensor-42"}

	if _, err := fh.Write(vars, sampleDeviceManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := fh.Read(vars, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.DisplayName != "Sensor 42" || !got.Enabled {
		t.Errorf("got = %+v, want DisplayName=Sensor 42 Enabled=true", got)
	}
}

func TestNewDeviceFile_DifferentDevicesAreIndependent(t *testing.T) {
	basePath := t.TempDir()
	fh := NewDeviceFile(basePath)

	if _, err := fh.Write(map[string]string{"usecase_name": "usecase1", "device_id": "sensor-a"}, sampleDeviceManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write A: %v", err)
	}
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase1", "device_id": "sensor-b"}, ports.FileOptions{}); err == nil {
		t.Error("Read B: want error (sensor-b was never written), got nil")
	}
}

func TestNewDeviceFile_DifferentUseCasesAreIndependent(t *testing.T) {
	basePath := t.TempDir()
	fh := NewDeviceFile(basePath)

	if _, err := fh.Write(map[string]string{"usecase_name": "usecase-a", "device_id": "sensor-42"}, sampleDeviceManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase-b", "device_id": "sensor-42"}, ports.FileOptions{}); err == nil {
		t.Error("Read under a different use case: want error, got nil")
	}
}

func TestNewDeviceFile_MissingVars_ReturnsMissingFilePathVarError(t *testing.T) {
	basePath := t.TempDir()
	fh := NewDeviceFile(basePath)
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase1"}, ports.FileOptions{}); err == nil {
		t.Error("Read: want MissingFilePathVarError for missing device_id, got nil")
	}
}
