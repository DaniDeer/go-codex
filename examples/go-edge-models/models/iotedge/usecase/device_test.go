package usecase

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	"github.com/DaniDeer/go-codex/ports"
)

func samplePatch() deviceconfig.Patch {
	return deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.status": "stopped",
		},
	}
}

// ── DeviceFile ────────────────────────────────────────────────────────────────

func TestNewDeviceFile_WriteThenRead(t *testing.T) {
	basePath := BasePath(t.TempDir())
	fh := NewDeviceFile(basePath)
	vars := map[string]string{"usecase_name": "usecase1", "device_id": "sensor-42"}

	if _, err := fh.Write(vars, samplePatch(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := fh.Read(vars, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.EdgeAgent["factory-mqtt-gateway-1.status"] != "stopped" {
		t.Errorf("got = %+v, want EdgeAgent[factory-mqtt-gateway-1.status]=stopped", got)
	}
}

func TestNewDeviceFile_DifferentDevicesAreIndependent(t *testing.T) {
	basePath := BasePath(t.TempDir())
	fh := NewDeviceFile(basePath)

	if _, err := fh.Write(map[string]string{"usecase_name": "usecase1", "device_id": "sensor-a"}, samplePatch(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write A: %v", err)
	}
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase1", "device_id": "sensor-b"}, ports.FileOptions{}); err == nil {
		t.Error("Read B: want error (sensor-b was never written), got nil")
	}
}

func TestNewDeviceFile_DifferentUseCasesAreIndependent(t *testing.T) {
	basePath := BasePath(t.TempDir())
	fh := NewDeviceFile(basePath)

	if _, err := fh.Write(map[string]string{"usecase_name": "usecase-a", "device_id": "sensor-42"}, samplePatch(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase-b", "device_id": "sensor-42"}, ports.FileOptions{}); err == nil {
		t.Error("Read under a different use case: want error, got nil")
	}
}

func TestNewDeviceFile_MissingVars_ReturnsMissingFilePathVarError(t *testing.T) {
	basePath := BasePath(t.TempDir())
	fh := NewDeviceFile(basePath)
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase1"}, ports.FileOptions{}); err == nil {
		t.Error("Read: want MissingFilePathVarError for missing device_id, got nil")
	}
}

// ── DeviceDir ─────────────────────────────────────────────────────────────────

func TestNewDeviceDir_ListDiscoversDevicesForGivenUseCase(t *testing.T) {
	basePath := BasePath(t.TempDir())

	writeDevice := func(useCaseName Name, deviceID DeviceID) {
		if _, err := WriteDeviceConfig(basePath, useCaseName, DeviceConfig{
			DeviceID: deviceID,
			Patch:    samplePatch(),
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
	basePath := BasePath(t.TempDir())

	if _, err := WriteDeviceConfig(basePath, "usecase1", DeviceConfig{DeviceID: "sensor-1", Patch: samplePatch()}, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}
	if _, err := WriteDeviceConfig(basePath, "usecase1", DeviceConfig{DeviceID: "sensor-2", Patch: samplePatch()}, ports.FileOptions{CreateDirs: true}); err != nil {
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
	basePath := BasePath(t.TempDir())
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
	basePath := BasePath(t.TempDir())
	cfg := DeviceConfig{DeviceID: "sensor-42", Patch: samplePatch()}

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
	if got.Patch.EdgeAgent["factory-mqtt-gateway-1.status"] != "stopped" {
		t.Errorf("Patch = %+v, want EdgeAgent[factory-mqtt-gateway-1.status]=stopped", got.Patch)
	}
}

func TestReadDeviceConfig_PropagatesMissingFileError(t *testing.T) {
	basePath := BasePath(t.TempDir())
	_, err := ReadDeviceConfig(basePath, "usecase1", "does-not-exist", ports.FileOptions{})
	if err == nil {
		t.Error("ReadDeviceConfig: want error for nonexistent device, got nil")
	}
}

func TestDeviceConfig_Merge_LayersPatchOntoTemplate(t *testing.T) {
	template := iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			EdgeAgent: iothub.Modules{
				"factory-mqtt-gateway-1": iothub.ModuleConfig{
					Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/factory-gateway", Tag: "0.12.5"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "on-failure",
					Version:       "1.0",
				},
			},
		},
	}
	cfg := DeviceConfig{DeviceID: "sensor-1", Patch: samplePatch()}

	got, err := cfg.Merge(sampleBaselineManifest(), template)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent.Modules["factory-mqtt-gateway-1"]
	if gw.Status != "stopped" {
		t.Errorf("Status = %v, want stopped (patched)", gw.Status)
	}
	// Fields the patch never touched must survive.
	if gw.RestartPolicy != "on-failure" {
		t.Errorf("RestartPolicy = %v, want unchanged on-failure", gw.RestartPolicy)
	}
}

func TestReadEffective_MergesTemplateAndDeviceConfigFromDisk(t *testing.T) {
	basePath := BasePath(t.TempDir())

	writeSampleBaseline(t, basePath)
	if _, err := NewFile(basePath).Write(map[string]string{"usecase_name": "usecase1"}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write template: %v", err)
	}
	cfg := DeviceConfig{
		DeviceID: "sensor-1",
		Patch:    deviceconfig.Patch{EdgeAgent: map[string]any{"factory-dashboard.status": "stopped"}},
	}
	if _, err := WriteDeviceConfig(basePath, "usecase1", cfg, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	got, err := ReadEffective(basePath, "usecase1", "sensor-1", ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent.Modules["factory-dashboard"]
	if dashboard.Status != "stopped" {
		t.Errorf("Status = %v, want stopped (device-patched)", dashboard.Status)
	}
	// Fields the device's patch never touched must survive from the template.
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged from template", dashboard.Settings.Image)
	}
	if dashboard.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged always", dashboard.RestartPolicy)
	}
	// The baseline's own system modules must survive into the effective manifest.
	if got.ModulesContent.EdgeAgent.SchemaVersion != "1.1" {
		t.Errorf("SchemaVersion = %q, want 1.1 (from baseline)", got.ModulesContent.EdgeAgent.SchemaVersion)
	}
}

func TestReadEffective_PropagatesMissingTemplateError(t *testing.T) {
	basePath := BasePath(t.TempDir())
	writeSampleBaseline(t, basePath)
	_, err := ReadEffective(basePath, "does-not-exist", "sensor-1", ports.FileOptions{})
	if err == nil {
		t.Error("ReadEffective: want error for nonexistent use case, got nil")
	}
}

func TestReadEffective_PropagatesMissingBaselineError(t *testing.T) {
	basePath := BasePath(t.TempDir())
	if _, err := NewFile(basePath).Write(map[string]string{"usecase_name": "usecase1"}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write template: %v", err)
	}
	_, err := ReadEffective(basePath, "usecase1", "sensor-1", ports.FileOptions{})
	if err == nil {
		t.Error("ReadEffective: want error for missing baseline.json, got nil")
	}
}

func TestReadEffective_NoDeviceConfigYet_ReturnsTemplateUnchanged(t *testing.T) {
	// A device that has NEVER had a config file written is NOT an error
	// — its effective config is simply baseline+template merged with an
	// EMPTY device patch, since "no overrides yet" is a valid, expected
	// state (a device connecting for the first time, before any override
	// has ever been applied).
	basePath := BasePath(t.TempDir())
	writeSampleBaseline(t, basePath)
	if _, err := NewFile(basePath).Write(map[string]string{"usecase_name": "usecase1"}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write template: %v", err)
	}
	got, err := ReadEffective(basePath, "usecase1", "never-configured-device", ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	if got.ModulesContent.EdgeAgent.Modules["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged from template", got.ModulesContent.EdgeAgent.Modules["factory-dashboard"].Settings.Image)
	}
}
