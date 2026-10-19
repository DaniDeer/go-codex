package usecase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	adapterfile "github.com/DaniDeer/go-codex/adapters/file"
	"github.com/DaniDeer/go-codex/api/file"
	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
)

// newTestStore builds a *Store backed by the real filesystem
// (adapters/file.NewBackend) — the Stage D2 test-setup replacement for
// the former bare ports.File/ports.Dir constructors, which needed no
// such setup step at all. Every test below constructs its own Store
// (cheap — a *file.Client holding one attached Backend), mirroring
// NewRegistryClient's own per-test-fixture construction convention in
// app/registry's test suite.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(adapterfile.NewBackend())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// newTestScope builds a *Store (as [newTestStore]) and binds it to a
// fresh t.TempDir() basePath via [Store.For] — the Store/Scope-split
// test-setup convenience every test below uses; returns the basePath
// too, since several tests also need the raw string value for direct
// os.MkdirAll/filepath.Join fixture setup that bypasses the Scope.
func newTestScope(t *testing.T) (*Scope, BasePath) {
	t.Helper()
	store := newTestStore(t)
	basePath := BasePath(t.TempDir())
	scope, err := store.For(basePath)
	if err != nil {
		t.Fatalf("Store.For: %v", err)
	}
	return scope, basePath
}

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
// "{basePath}/baseline/baseline.json" via s — a test-only convenience
// most ReadEffective-exercising tests need, since the baseline file is
// now a REQUIRED read for every ReadEffective call.
func writeSampleBaseline(t *testing.T, s *Scope) {
	t.Helper()
	if _, err := s.WriteBaseline(context.Background(), sampleBaselineManifest(), file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("writeSampleBaseline: %v", err)
	}
}

// ── Store.ReadUseCaseFile / Store.WriteUseCaseFile ─────────────────────────────

func TestStore_ReadUseCaseFile_ReadRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, basePath := newTestScope(t)
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

	got, err := s.ReadUseCaseFile(ctx, "usecase1")
	if err != nil {
		t.Fatalf("ReadUseCaseFile: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestStore_WriteUseCaseFile_ThenRead(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)

	manifest := sampleManifest()
	if _, err := s.WriteUseCaseFile(ctx, "usecase1", manifest, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCaseFile: %v", err)
	}

	got, err := s.ReadUseCaseFile(ctx, "usecase1")
	if err != nil {
		t.Fatalf("ReadUseCaseFile: %v", err)
	}
	if len(got.ModulesContent.EdgeAgent) != 1 {
		t.Errorf("EdgeAgent len = %d, want 1", len(got.ModulesContent.EdgeAgent))
	}
}

func TestStore_WriteBaseline_ThenRead(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)

	bl := sampleBaselineManifest()
	if _, err := s.WriteBaseline(ctx, bl, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}

	got, err := s.ReadBaseline(ctx)
	if err != nil {
		t.Fatalf("ReadBaseline: %v", err)
	}
	if got.ModulesContent.EdgeAgent.SchemaVersion != "1.1" {
		t.Errorf("SchemaVersion = %q, want 1.1", got.ModulesContent.EdgeAgent.SchemaVersion)
	}
	if got.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion != "v1.25" {
		t.Errorf("MinDockerVersion = %q, want v1.25", got.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion)
	}
}

func TestStore_WriteBaseline_PathHasNoTemplateVariables(t *testing.T) {
	ctx := context.Background()
	s, basePath := newTestScope(t)
	wantPath := filepath.Join(string(basePath), "baseline", "baseline.json")

	if _, err := s.WriteBaseline(ctx, sampleBaselineManifest(), file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteBaseline: %v", err)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("expected file at fixed path %q (no template variables): %v", wantPath, err)
	}
}

func TestStore_WriteUseCaseFile_DifferentUseCasesAreIndependent(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)

	if _, err := s.WriteUseCaseFile(ctx, "usecase-a", sampleManifest(), file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("WriteUseCaseFile A: %v", err)
	}
	if _, err := s.ReadUseCaseFile(ctx, "usecase-b"); err == nil {
		t.Error("ReadUseCaseFile B: want error (usecase-b was never written), got nil")
	}
}

// TestFileResource_MissingUseCaseNameVar_ReturnsMissingFilePathVarError
// is a white-box test against the package-level fileResource var
// directly (bypassing Store.ReadUseCaseFile's own signature, which
// always supplies a usecase_name — even an empty Name still produces a
// PRESENT, merely invalid, vars entry) — proves the underlying
// MissingFilePathVarError still fires when vars genuinely lacks the
// key, the same regression this test proved before Stage D2.
func TestFileResource_MissingUseCaseNameVar_ReturnsMissingFilePathVarError(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)

	handle, err := fileResource.WithRead(file.Read{}).Handle(s.client, file.WithRouter(s.router))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	_, err = s.client.Read(ctx, handle, nil)
	var missing file.MissingFilePathVarError
	if !errors.As(err, &missing) {
		t.Errorf("Read: want MissingFilePathVarError for missing usecase_name, got %v", err)
	}
}

// ── Store.ListUseCaseEntries / Store.ListNames ─────────────────────────────────

func TestStore_ListUseCaseEntries_DiscoversUseCases(t *testing.T) {
	ctx := context.Background()
	s, basePath := newTestScope(t)
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
	// Stray non-conforming file — must be silently excluded.
	if err := os.WriteFile(filepath.Join(usecasesDir, ".gitkeep"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile .gitkeep: %v", err)
	}

	entries, err := s.ListUseCaseEntries(ctx, file.DirOptions{})
	if err != nil {
		t.Fatalf("ListUseCaseEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ListUseCaseEntries returned %d entries, want 2 (stray .gitkeep excluded): %+v", len(entries), entries)
	}

	useCases := map[string]bool{}
	for _, e := range entries {
		useCases[e.Vars["useCase"]] = true
		if e.Kind != file.EntryFile {
			t.Errorf("entry %q Kind = %v, want EntryFile", e.Name, e.Kind)
		}
	}
	if !useCases["usecase1"] || !useCases["usecase2"] {
		t.Errorf("discovered use cases = %v, want usecase1 and usecase2", useCases)
	}
}

func TestStore_ListUseCaseEntries_ThenReadDiscoveredManifest(t *testing.T) {
	ctx := context.Background()
	s, basePath := newTestScope(t)
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

	entries, err := s.ListUseCaseEntries(ctx, file.DirOptions{})
	if err != nil {
		t.Fatalf("ListUseCaseEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListUseCaseEntries returned %d entries, want 1", len(entries))
	}

	// Discovery (ListUseCaseEntries) feeds the discovered use case name
	// directly into ReadUseCaseFile's own vars — the same declarative
	// flow app/iotedge would use to read whichever use case a caller
	// picked.
	got, err := s.ReadUseCaseFile(ctx, Name(entries[0].Vars["useCase"]))
	if err != nil {
		t.Fatalf("ReadUseCaseFile discovered manifest: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Status != "running" {
		t.Errorf("Status = %q, want running", got.ModulesContent.EdgeAgent["factory-dashboard"].Status)
	}
}

func TestStore_ListNames(t *testing.T) {
	ctx := context.Background()
	s, basePath := newTestScope(t)
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

	names, err := s.ListNames(ctx, file.DirOptions{})
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

// ── UseCase / Store.Read / Store.Write ─────────────────────────────────────────

func TestStore_Write_Read_RoundTrip_WithNestedDevices(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)

	uc := UseCase{
		Name:               "usecase1",
		DeploymentManifest: sampleManifest(),
		Devices: []DeviceConfig{
			{DeviceID: "sensor-1", Patch: deviceconfig.Patch{EdgeAgent: map[string]any{"factory-dashboard.status": "stopped"}}},
			{DeviceID: "sensor-2", Patch: deviceconfig.Patch{EdgeAgent: map[string]any{"factory-dashboard.restartPolicy": "never"}}},
		},
	}

	if err := s.Write(ctx, uc, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := s.Read(ctx, "usecase1")
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

func TestStore_Read_NoDevices_ReturnsEmptyDevicesSlice(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)
	uc := UseCase{Name: "usecase1", DeploymentManifest: sampleManifest()}
	if err := s.Write(ctx, uc, file.WriteOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := s.Read(ctx, "usecase1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Devices) != 0 {
		t.Errorf("Devices = %v, want empty", got.Devices)
	}
}

func TestStore_Read_PropagatesMissingManifestError(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestScope(t)
	_, err := s.Read(ctx, "does-not-exist")
	if err == nil {
		t.Error("Read: want error for nonexistent use case, got nil")
	}
}
