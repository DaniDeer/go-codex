package iotedge

import (
	"testing"

	"github.com/DaniDeer/go-codex/ports"
)

func TestNewDeviceDir_ListDiscoversDevicesForGivenUseCase(t *testing.T) {
	basePath := t.TempDir()

	writeDevice := func(useCaseName, deviceID string) {
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
		found[id] = true
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
