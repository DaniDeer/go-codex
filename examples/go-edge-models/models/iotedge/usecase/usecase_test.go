package usecase

import (
	"os"
	"path/filepath"
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	"github.com/DaniDeer/go-codex/ports"
)

func sampleManifest() iothub.LayeredDeployment {
	return iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			EdgeAgent: iothub.Modules{
				"factory-dashboard": iothub.ModuleConfig{
					Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
			},
		},
	}
}

// sampleBaselineManifest returns a minimal, valid iothub.BaseDeployment with
// ZERO regular modules/routes of its own — mirrors
// finaldeviceconfig's own test helper of the same shape.
func sampleBaselineManifest() iothub.BaseDeployment {
	return iothub.BaseDeployment{
		ModulesContent: iothub.BaseModulesContent{
			EdgeAgent: iothub.EdgeAgentProperties{
				SchemaVersion: "1.1",
				Runtime: iothub.Runtime{
					Settings: iothub.RuntimeSettings{MinDockerVersion: "v1.25"},
					Type:     "docker",
				},
				SystemModules: iothub.SystemModules{
					EdgeAgent: iothub.SystemModuleConfig{
						Settings: iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.5.31"}},
						Type:     "docker",
					},
					EdgeHub: iothub.SystemModuleConfig{
						Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-hub", Tag: "1.5.31"}},
						Type:          "docker",
						Status:        "running",
						RestartPolicy: "always",
					},
				},
			},
			EdgeHub: iothub.EdgeHubProperties{
				SchemaVersion:                "1.1",
				StoreAndForwardConfiguration: iothub.StoreAndForwardConfiguration{TimeToLiveSecs: 259200},
			},
		},
	}
}

// writeSampleBaseline writes sampleBaselineManifest() to
// "{basePath}/baseline/baseline.json" — a test-only convenience most
// ReadEffective-exercising tests need, since the baseline file is now a
// REQUIRED read for every ReadEffective call.
func writeSampleBaseline(t *testing.T, basePath BasePath) {
	t.Helper()
	if _, err := NewBaselineFile(basePath).Write(nil, sampleBaselineManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("writeSampleBaseline: %v", err)
	}
}

// ── NewFile ───────────────────────────────────────────────────────────────────

func TestNewFile_ReadRoundTrip(t *testing.T) {
	basePath := BasePath(t.TempDir())
	usecasesDir := filepath.Join(string(basePath), "usecases")
	if err := os.MkdirAll(usecasesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	manifest := sampleManifest()
	raw, err := FileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(usecasesDir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fh := NewFile(basePath)
	got, err := fh.Read(map[string]string{"usecase_name": "usecase1"}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestNewFile_WriteThenRead(t *testing.T) {
	basePath := BasePath(t.TempDir())

	fh := NewFile(basePath)
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

func TestNewBaselineFile_WriteThenRead(t *testing.T) {
	basePath := BasePath(t.TempDir())

	fh := NewBaselineFile(basePath)
	bl := sampleBaselineManifest()
	if _, err := fh.Write(nil, bl, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := fh.Read(nil, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ModulesContent.EdgeAgent.SchemaVersion != "1.1" {
		t.Errorf("SchemaVersion = %q, want 1.1", got.ModulesContent.EdgeAgent.SchemaVersion)
	}
	if got.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion != "v1.25" {
		t.Errorf("MinDockerVersion = %q, want v1.25", got.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion)
	}
}

func TestNewBaselineFile_PathHasNoTemplateVariables(t *testing.T) {
	basePath := BasePath(t.TempDir())
	wantPath := filepath.Join(string(basePath), "baseline", "baseline.json")

	fh := NewBaselineFile(basePath)
	if _, err := fh.Write(nil, sampleBaselineManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected file at fixed path %q (no template variables): %v", wantPath, err)
	}
}

func TestNewFile_DifferentUseCasesAreIndependent(t *testing.T) {
	basePath := BasePath(t.TempDir())

	fh := NewFile(basePath)

	if _, err := fh.Write(map[string]string{"usecase_name": "usecase-a"}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write A: %v", err)
	}
	if _, err := fh.Read(map[string]string{"usecase_name": "usecase-b"}, ports.FileOptions{}); err == nil {
		t.Error("Read B: want error (usecase-b was never written), got nil")
	}
}

func TestNewFile_MissingUseCaseNameVar_ReturnsMissingFilePathVarError(t *testing.T) {
	basePath := BasePath(t.TempDir())
	fh := NewFile(basePath)
	if _, err := fh.Read(nil, ports.FileOptions{}); err == nil {
		t.Error("Read: want MissingFilePathVarError for missing usecase_name, got nil")
	}
}

// ── NewDir / ListNames ────────────────────────────────────────────────────────

func TestNewDir_ListDiscoversUseCases(t *testing.T) {
	dir := t.TempDir()

	manifest := sampleManifest()
	raw, err := FileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile usecase1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "usecase2.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile usecase2: %v", err)
	}
	// Stray non-conforming file — must be silently excluded.
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile .gitkeep: %v", err)
	}

	useCaseDir := NewDir(dir)
	entries, err := useCaseDir.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List returned %d entries, want 2 (stray .gitkeep excluded): %+v", len(entries), entries)
	}

	useCases := map[string]bool{}
	for _, e := range entries {
		useCases[e.Vars["useCase"]] = true
		if e.Kind != ports.EntryFile {
			t.Errorf("entry %q Kind = %v, want EntryFile", e.Name, e.Kind)
		}
	}
	if !useCases["usecase1"] || !useCases["usecase2"] {
		t.Errorf("discovered use cases = %v, want usecase1 and usecase2", useCases)
	}
}

func TestNewDir_ListThenReadDiscoveredManifest(t *testing.T) {
	basePath := BasePath(t.TempDir())
	usecasesDir := filepath.Join(string(basePath), "usecases")
	if err := os.MkdirAll(usecasesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	manifest := sampleManifest()
	raw, err := FileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(usecasesDir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	useCaseDir := NewDir(usecasesDir)
	entries, err := useCaseDir.List(nil, ports.DirOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}

	// Discovery (List) feeds the discovered use case name directly into
	// NewFile(basePath)'s own vars — the same declarative flow
	// app/iotedge would use to read whichever use case a caller picked.
	fh := NewFile(basePath)
	got, err := fh.Read(map[string]string{"usecase_name": entries[0].Vars["useCase"]}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read discovered manifest: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Status != "running" {
		t.Errorf("Status = %q, want running", got.ModulesContent.EdgeAgent["factory-dashboard"].Status)
	}
}

func TestListNames(t *testing.T) {
	basePath := BasePath(t.TempDir())
	usecasesDir := filepath.Join(string(basePath), "usecases")
	if err := os.MkdirAll(usecasesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	manifest := sampleManifest()
	raw, err := FileFormat.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(usecasesDir, "usecase1.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile usecase1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(usecasesDir, "usecase2.json"), raw, 0o600); err != nil {
		t.Fatalf("WriteFile usecase2: %v", err)
	}

	names, err := ListNames(basePath, ports.DirOptions{})
	if err != nil {
		t.Fatalf("ListNames: %v", err)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[string(n)] = true
	}
	if !found["usecase1"] || !found["usecase2"] {
		t.Errorf("ListNames = %v, want usecase1 and usecase2", names)
	}
}

// ── UseCase / Read / Write ─────────────────────────────────────────────────────

func TestWrite_Read_RoundTrip_WithNestedDevices(t *testing.T) {
	basePath := BasePath(t.TempDir())

	uc := UseCase{
		Name:               "usecase1",
		DeploymentManifest: sampleManifest(),
		Devices: []DeviceConfig{
			{DeviceID: "sensor-1", Patch: deviceconfig.Patch{EdgeAgent: map[string]any{"factory-dashboard.status": "stopped"}}},
			{DeviceID: "sensor-2", Patch: deviceconfig.Patch{EdgeAgent: map[string]any{"factory-dashboard.restartPolicy": "never"}}},
		},
	}

	if err := Write(basePath, uc, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(basePath, "usecase1", ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
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
		byID[string(d.DeviceID)] = d
	}
	if byID["sensor-1"].Patch.EdgeAgent["factory-dashboard.status"] != "stopped" {
		t.Errorf("sensor-1 Patch = %+v, want EdgeAgent[factory-dashboard.status]=stopped", byID["sensor-1"].Patch)
	}
	if byID["sensor-2"].Patch.EdgeAgent["factory-dashboard.restartPolicy"] != "never" {
		t.Errorf("sensor-2 Patch = %+v, want EdgeAgent[factory-dashboard.restartPolicy]=never", byID["sensor-2"].Patch)
	}
}

func TestRead_NoDevices_ReturnsEmptyDevicesSlice(t *testing.T) {
	basePath := BasePath(t.TempDir())
	uc := UseCase{Name: "usecase1", DeploymentManifest: sampleManifest()}
	if err := Write(basePath, uc, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := Read(basePath, "usecase1", ports.FileOptions{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Devices) != 0 {
		t.Errorf("Devices = %v, want empty", got.Devices)
	}
}

func TestRead_PropagatesMissingManifestError(t *testing.T) {
	basePath := BasePath(t.TempDir())
	_, err := Read(basePath, "does-not-exist", ports.FileOptions{})
	if err == nil {
		t.Error("Read: want error for nonexistent use case, got nil")
	}
}
