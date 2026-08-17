package usecase

import (
	"testing"

	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	"github.com/DaniDeer/go-codex/ports"
)

func sampleDeviceManifest() deviceconfig.Manifest {
	return deviceconfig.Manifest{DisplayName: "Sensor 42", Enabled: true}
}

// ── DeviceFile ────────────────────────────────────────────────────────────────

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

// ── DeviceDir ─────────────────────────────────────────────────────────────────

func TestNewDeviceDir_ListDiscoversDevicesForGivenUseCase(t *testing.T) {
	basePath := t.TempDir()

	writeDevice := func(useCaseName Name, deviceID DeviceID) {
		if _, err := WriteDeviceConfig(basePath, useCaseName, DeviceConfig{
			DeviceID:       deviceID,
			DeviceManifest: sampleDeviceManifest(),
		}, ports.FileOptions{CreateDirs: true}); err != nil {
			t.Fatalf("WriteDeviceConfig(%q, %q): %v", useCaseName, deviceID, err)
		}
	}
	writeDevice("usecase1", "sensor-1")
	writeDevice("usecase1", "sensor-2")
	// A different use case's device must NOT be discovered when listing usecase1.
	writeDevice("usecase2", "sensor-99")

	entries, err := NewDeviceDir(basePath).List(map[string]string{"usecase_name": "usecase1"}, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2: %+v", len(entries), entries)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.Vars["device_id"]] = true
	}
	if !ids["sensor-1"] || !ids["sensor-2"] {
		t.Errorf("discovered device_ids = %v, want sensor-1 and sensor-2", ids)
	}
}

func TestListDeviceIDs(t *testing.T) {
	basePath := t.TempDir()

	if _, err := WriteDeviceConfig(basePath, "usecase1", DeviceConfig{DeviceID: "sensor-1", DeviceManifest: sampleDeviceManifest()}, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}
	if _, err := WriteDeviceConfig(basePath, "usecase1", DeviceConfig{DeviceID: "sensor-2", DeviceManifest: sampleDeviceManifest()}, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	ids, err := ListDeviceIDs(basePath, "usecase1", ports.DirOptions{})
	if err != nil {
		t.Fatalf("ListDeviceIDs: %v", err)
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[string(id)] = true
	}
	if !found["sensor-1"] || !found["sensor-2"] {
		t.Errorf("ListDeviceIDs = %v, want sensor-1 and sensor-2", ids)
	}
}

func TestListDeviceIDs_NoDevicesForUseCase_ReturnsEmpty(t *testing.T) {
	basePath := t.TempDir()
	ids, err := ListDeviceIDs(basePath, "nonexistent-usecase", ports.DirOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("ListDeviceIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ListDeviceIDs = %v, want empty", ids)
	}
}

// ── DeviceConfig ──────────────────────────────────────────────────────────────

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
