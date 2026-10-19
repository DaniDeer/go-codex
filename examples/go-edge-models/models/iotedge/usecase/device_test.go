package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/file"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
)

func samplePatch() deviceconfig.Patch {
	return deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.status": "stopped",
		},
	}
}

// ── DeviceFile ────────────────────────────────────────────────────────────────

func TestStore_WriteDeviceConfig_ThenReadDeviceFile(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase1", DeviceConfig{DeviceID: "sensor-42", Patch: samplePatch()}, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	got, err := s.ReadDeviceFile(ctx, basePath, "usecase1", "sensor-42")
	if err != nil {
		t.Fatalf("ReadDeviceFile: %v", err)
	}
	if got.EdgeAgent["factory-mqtt-gateway-1.status"] != "stopped" {
		t.Errorf("got = %+v, want EdgeAgent[factory-mqtt-gateway-1.status]=stopped", got)
	}
}

func TestStore_WriteDeviceConfig_DifferentDevicesAreIndependent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase1", DeviceConfig{DeviceID: "sensor-a", Patch: samplePatch()}, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig A: %v", err)
	}
	if _, err := s.ReadDeviceFile(ctx, basePath, "usecase1", "sensor-b"); err == nil {
		t.Error("ReadDeviceFile B: want error (sensor-b was never written), got nil")
	}
}

func TestStore_WriteDeviceConfig_DifferentUseCasesAreIndependent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase-a", DeviceConfig{DeviceID: "sensor-42", Patch: samplePatch()}, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}
	if _, err := s.ReadDeviceFile(ctx, basePath, "usecase-b", "sensor-42"); err == nil {
		t.Error("ReadDeviceFile under a different use case: want error, got nil")
	}
}

// TestDeviceFileResource_MissingDeviceIDVar_ReturnsMissingFilePathVarError
// is a white-box test against the package-level deviceFileResource var
// directly (same rationale as fileResource's own white-box missing-var
// test in usecase_test.go) — Store.ReadDeviceFile's own signature
// always supplies BOTH vars, so a genuinely missing var can only be
// reproduced by bypassing it.
func TestDeviceFileResource_MissingDeviceIDVar_ReturnsMissingFilePathVarError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	handle, err := deviceFileResource.WithRead(file.Read{}).Handle(s.client, file.WithRouter(routerFor(basePath)))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	_, err = s.client.Read(ctx, handle, map[string]string{"usecase_name": "usecase1"})
	var missing file.MissingFilePathVarError
	if !errors.As(err, &missing) {
		t.Errorf("Read: want MissingFilePathVarError for missing device_id, got %v", err)
	}
}

// ── DeviceDir ─────────────────────────────────────────────────────────────────

func TestStore_ListDeviceIDs_DiscoversDevicesForGivenUseCase(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	writeDevice := func(useCaseName Name, deviceID DeviceID) {
		if _, err := s.WriteDeviceConfig(ctx, basePath, useCaseName, DeviceConfig{
			DeviceID: deviceID,
			Patch:    samplePatch(),
		}, file.WriteOptions{CreateDirs: true}); err != nil {
			t.Fatalf("WriteDeviceConfig(%q, %q): %v", useCaseName, deviceID, err)
		}
	}
	writeDevice("usecase1", "sensor-1")
	writeDevice("usecase1", "sensor-2")
	// A different use case's device must NOT be discovered when listing usecase1.
	writeDevice("usecase2", "sensor-99")

	ids, err := s.ListDeviceIDs(ctx, basePath, "usecase1", file.DirOptions{})
	if err != nil {
		t.Fatalf("ListDeviceIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ListDeviceIDs returned %d entries, want 2: %+v", len(ids), ids)
	}
	found := map[string]bool{}
	for _, id := range ids {
		found[string(id)] = true
	}
	if !found["sensor-1"] || !found["sensor-2"] {
		t.Errorf("discovered device_ids = %v, want sensor-1 and sensor-2", found)
	}
}

func TestStore_ListDeviceIDs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase1", DeviceConfig{DeviceID: "sensor-1", Patch: samplePatch()}, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}
	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase1", DeviceConfig{DeviceID: "sensor-2", Patch: samplePatch()}, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	ids, err := s.ListDeviceIDs(ctx, basePath, "usecase1", file.DirOptions{})
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

func TestStore_ListDeviceIDs_NoDevicesForUseCase_ReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())
	ids, err := s.ListDeviceIDs(ctx, basePath, "nonexistent-usecase", file.DirOptions{CreateIfMissing: true})
	if err != nil {
		t.Fatalf("ListDeviceIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ListDeviceIDs = %v, want empty", ids)
	}
}

// ── DeviceConfig ──────────────────────────────────────────────────────────────

func TestStore_WriteDeviceConfig_ReadDeviceConfig_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())
	cfg := DeviceConfig{DeviceID: "sensor-42", Patch: samplePatch()}

	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase1", cfg, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	got, err := s.ReadDeviceConfig(ctx, basePath, "usecase1", "sensor-42")
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

func TestStore_ReadDeviceConfig_PropagatesMissingFileError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())
	_, err := s.ReadDeviceConfig(ctx, basePath, "usecase1", "does-not-exist")
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

func TestStore_ReadEffective_MergesTemplateAndDeviceConfigFromDisk(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())

	writeSampleBaseline(t, s, basePath)
	if _, err := s.WriteUseCaseFile(ctx, basePath, "usecase1", sampleManifest(), file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCaseFile template: %v", err)
	}
	cfg := DeviceConfig{
		DeviceID: "sensor-1",
		Patch:    deviceconfig.Patch{EdgeAgent: map[string]any{"factory-dashboard.status": "stopped"}},
	}
	if _, err := s.WriteDeviceConfig(ctx, basePath, "usecase1", cfg, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteDeviceConfig: %v", err)
	}

	got, err := s.ReadEffective(ctx, basePath, "usecase1", "sensor-1")
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

func TestStore_ReadEffective_PropagatesMissingTemplateError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())
	writeSampleBaseline(t, s, basePath)
	_, err := s.ReadEffective(ctx, basePath, "does-not-exist", "sensor-1")
	if err == nil {
		t.Error("ReadEffective: want error for nonexistent use case, got nil")
	}
}

func TestStore_ReadEffective_PropagatesMissingBaselineError(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())
	if _, err := s.WriteUseCaseFile(ctx, basePath, "usecase1", sampleManifest(), file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCaseFile template: %v", err)
	}
	_, err := s.ReadEffective(ctx, basePath, "usecase1", "sensor-1")
	if err == nil {
		t.Error("ReadEffective: want error for missing baseline.json, got nil")
	}
}

func TestStore_ReadEffective_NoDeviceConfigYet_ReturnsTemplateUnchanged(t *testing.T) {
	// A device that has NEVER had a config file written is NOT an error
	// — its effective config is simply baseline+template merged with an
	// EMPTY device patch, since "no overrides yet" is a valid, expected
	// state (a device connecting for the first time, before any override
	// has ever been applied).
	ctx := context.Background()
	s := newTestStore(t)
	basePath := BasePath(t.TempDir())
	writeSampleBaseline(t, s, basePath)
	if _, err := s.WriteUseCaseFile(ctx, basePath, "usecase1", sampleManifest(), file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCaseFile template: %v", err)
	}
	got, err := s.ReadEffective(ctx, basePath, "usecase1", "never-configured-device")
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	if got.ModulesContent.EdgeAgent.Modules["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged from template", got.ModulesContent.EdgeAgent.Modules["factory-dashboard"].Settings.Image)
	}
}
