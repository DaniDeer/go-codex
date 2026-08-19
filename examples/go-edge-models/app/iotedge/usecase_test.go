package iotedge

import (
	"errors"
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulepatch"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
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
// ONE baseline-only module ("vulnerability-scanner", never declared in
// sampleManifest's own template) — lets tests exercise both "module
// resolves via the template" and "module resolves via baseline only"
// paths from the SAME fixture.
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
				Modules: iothub.Modules{
					"vulnerability-scanner": iothub.ModuleConfig{
						Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/edge-security-scanner", Tag: "0.0.2"}},
						Type:          "docker",
						Status:        "running",
						RestartPolicy: "always",
						Version:       "auto",
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

const sampleUseCaseName = "usecase1"

func writeSampleManifest(t *testing.T) string {
	t.Helper()
	basePath := t.TempDir()
	if _, err := usecase.NewBaselineFile(basePath).Write(nil, sampleBaselineManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write baseline: %v", err)
	}
	fh := usecase.NewFile(basePath)
	if _, err := fh.Write(map[string]string{"usecase_name": sampleUseCaseName}, sampleManifest(), ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return basePath
}

func TestReadUseCase_ReturnsWrittenManifest(t *testing.T) {
	basePath := writeSampleManifest(t)

	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
}

func TestReadUseCase_PropagatesMissingFileError(t *testing.T) {
	_, err := ReadUseCase("/nonexistent/path", "nonexistent-usecase", ports.FileOptions{})
	if err == nil {
		t.Error("ReadUseCase: want error for nonexistent file, got nil")
	}
}

func TestUpdateUseCaseModuleImage_PatchesOnlyImage(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
	if err := UpdateUseCaseModuleImage(basePath, sampleUseCaseName, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateUseCaseModuleImage: %v", err)
	}

	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after update: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:2.0.0", dashboard.Settings.Image)
	}
	// Other fields must survive the patch untouched.
	if dashboard.Status != "running" {
		t.Errorf("Status = %v, want unchanged \"running\"", dashboard.Status)
	}
	if dashboard.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged \"always\"", dashboard.RestartPolicy)
	}
	if dashboard.Version != "1.0" {
		t.Errorf("Version = %v, want unchanged \"1.0\"", dashboard.Version)
	}
}

func TestUpdateUseCaseModuleImage_RejectsInvalidImage(t *testing.T) {
	basePath := writeSampleManifest(t)

	// modulepatch.NewUpdateModuleImagePatch validates the image BEFORE any
	// disk I/O happens — an empty Name must be rejected here, not silently
	// written to the manifest.
	err := UpdateUseCaseModuleImage(basePath, sampleUseCaseName, "factory-dashboard", docker.Image{}, ports.FileOptions{})
	if err == nil {
		t.Fatal("UpdateUseCaseModuleImage: want error for invalid image, got nil")
	}

	got, readErr := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("ReadUseCase: %v", readErr)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged ghcr.io/org/edge-web:1.0.0 (invalid patch must not touch disk)", dashboard.Settings.Image)
	}
}

func TestUpdateUseCaseModuleImage_PromotesBaselineOnlyModule(t *testing.T) {
	basePath := writeSampleManifest(t)

	// "vulnerability-scanner" is declared ONLY in sampleBaselineManifest,
	// never in sampleManifest's own template — updating its image must
	// AUTO-PROMOTE to a full override in the template, not attempt an
	// incomplete sparse patch onto nothing.
	newImage := docker.Image{Name: "ghcr.io/example-org/edge-security-scanner", Tag: "0.0.3"}
	if err := UpdateUseCaseModuleImage(basePath, sampleUseCaseName, "vulnerability-scanner", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateUseCaseModuleImage: %v", err)
	}

	template, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after update: %v", err)
	}
	scanner, ok := template.ModulesContent.EdgeAgent["vulnerability-scanner"]
	if !ok {
		t.Fatal("ReadUseCase: expected vulnerability-scanner to now be declared in the template")
	}
	if scanner.Settings.Image.String() != "ghcr.io/example-org/edge-security-scanner:0.0.3" {
		t.Errorf("Image = %v, want ghcr.io/example-org/edge-security-scanner:0.0.3", scanner.Settings.Image)
	}
	// The promoted entry must be COMPLETE (every required field set,
	// seeded from baseline) so it decodes cleanly on its own.
	if scanner.Status != "running" || scanner.RestartPolicy != "always" || scanner.Type != "docker" {
		t.Errorf("promoted entry = %+v, want every field seeded from baseline", scanner)
	}

	// Reading it back via the app-level summary helper (baseline-aware)
	// must reflect the NEW image too.
	summary, err := readModuleSummary(basePath, sampleUseCaseName, "", "vulnerability-scanner", ports.FileOptions{})
	if err != nil {
		t.Fatalf("readModuleSummary: %v", err)
	}
	if summary.Image.String() != "ghcr.io/example-org/edge-security-scanner:0.0.3" {
		t.Errorf("Summary.Image = %v, want ghcr.io/example-org/edge-security-scanner:0.0.3", summary.Image)
	}
}

func TestUpdateUseCaseModuleImage_UnknownModule_ReturnsModuleNotFoundError(t *testing.T) {
	basePath := writeSampleManifest(t)
	err := UpdateUseCaseModuleImage(basePath, sampleUseCaseName, "totally-unknown-module", docker.Image{Name: "ghcr.io/org/x", Tag: "1.0.0"}, ports.FileOptions{})
	var notFound ModuleNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("UpdateUseCaseModuleImage error = %v (%T), want ModuleNotFoundError", err, err)
	}
}

func TestUpdateUseCaseSystemModuleImage_PromotesWhenNoTemplateOverrideYet(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.6.0"}
	if err := UpdateUseCaseSystemModuleImage(basePath, sampleUseCaseName, "edgeAgent", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateUseCaseSystemModuleImage: %v", err)
	}

	template, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after update: %v", err)
	}
	smc, ok := template.ModulesContent.SystemModules["edgeAgent"]
	if !ok {
		t.Fatal("ReadUseCase: expected an edgeAgent override to now be declared in the template")
	}
	if smc.Settings.Image.String() != "mcr.microsoft.com/azureiotedge-agent:1.6.0" {
		t.Errorf("Image = %v, want mcr.microsoft.com/azureiotedge-agent:1.6.0", smc.Settings.Image)
	}
	if smc.Type != "docker" {
		t.Errorf("Type = %q, want docker (promoted entry must be complete)", smc.Type)
	}

	summary, err := readModuleSummary(basePath, sampleUseCaseName, "", "edgeAgent", ports.FileOptions{})
	if err != nil {
		t.Fatalf("readModuleSummary: %v", err)
	}
	if summary.Image.String() != "mcr.microsoft.com/azureiotedge-agent:1.6.0" {
		t.Errorf("Summary.Image = %v, want mcr.microsoft.com/azureiotedge-agent:1.6.0", summary.Image)
	}
}

func TestUpdateUseCaseSystemModuleImage_SparseWhenTemplateOverrideAlreadyExists(t *testing.T) {
	basePath := writeSampleManifest(t)

	// First update promotes a full override.
	firstImage := docker.Image{Name: "mcr.microsoft.com/azureiotedge-hub", Tag: "1.6.0"}
	if err := UpdateUseCaseSystemModuleImage(basePath, sampleUseCaseName, "edgeHub", firstImage, ports.FileOptions{}); err != nil {
		t.Fatalf("first UpdateUseCaseSystemModuleImage: %v", err)
	}
	// Second update should sparse-patch the EXISTING override.
	secondImage := docker.Image{Name: "mcr.microsoft.com/azureiotedge-hub", Tag: "1.6.1"}
	if err := UpdateUseCaseSystemModuleImage(basePath, sampleUseCaseName, "edgeHub", secondImage, ports.FileOptions{}); err != nil {
		t.Fatalf("second UpdateUseCaseSystemModuleImage: %v", err)
	}

	template, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after update: %v", err)
	}
	smc := template.ModulesContent.SystemModules["edgeHub"]
	if smc.Settings.Image.String() != "mcr.microsoft.com/azureiotedge-hub:1.6.1" {
		t.Errorf("Image = %v, want mcr.microsoft.com/azureiotedge-hub:1.6.1", smc.Settings.Image)
	}
	// Fields the second (sparse) update never touched must survive from
	// the first (full) promotion.
	if smc.Status != "running" || smc.RestartPolicy != "always" {
		t.Errorf("Status/RestartPolicy = %q/%q, want unchanged from baseline (running/always)", smc.Status, smc.RestartPolicy)
	}
}

func TestUpdateDeviceSystemModuleImage_FirstOverride_WritesNewDeviceFile(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.6.0"}
	if err := UpdateDeviceSystemModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "edgeAgent", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateDeviceSystemModuleImage: %v", err)
	}

	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	if effective.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Settings.Image.String() != "mcr.microsoft.com/azureiotedge-agent:1.6.0" {
		t.Errorf("EdgeAgent.Settings.Image = %v, want mcr.microsoft.com/azureiotedge-agent:1.6.0",
			effective.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Settings.Image)
	}
	// The device patch should NOT have touched edgeHub.
	if effective.ModulesContent.EdgeAgent.SystemModules.EdgeHub.Status != "running" {
		t.Errorf("EdgeHub.Status = %q, want unchanged \"running\"", effective.ModulesContent.EdgeAgent.SystemModules.EdgeHub.Status)
	}
}

func TestPatchUseCaseModule_PatchesMultipleFieldsLeavesOthersUntouched(t *testing.T) {
	basePath := writeSampleManifest(t)

	status := iothub.Status("stopped")
	if err := PatchUseCaseModule(basePath, sampleUseCaseName, modulepatch.FieldsPatch{
		ModuleName: "factory-dashboard",
		Status:     &status,
	}, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchUseCaseModule: %v", err)
	}

	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase after patch: %v", err)
	}
	dashboard := got.ModulesContent.EdgeAgent["factory-dashboard"]
	if dashboard.Status != "stopped" {
		t.Errorf("Status = %v, want \"stopped\"", dashboard.Status)
	}
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged ghcr.io/org/edge-web:1.0.0", dashboard.Settings.Image)
	}
	if dashboard.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged \"always\"", dashboard.RestartPolicy)
	}
}

func TestPatchUseCaseModule_PropagatesEmptyPatchError(t *testing.T) {
	basePath := writeSampleManifest(t)

	err := PatchUseCaseModule(basePath, sampleUseCaseName, modulepatch.FieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchUseCaseModule: want error for empty patch, got nil")
	}
}

// ── PatchDeviceModule / UpdateDeviceModuleImage ──────────────────────────────

const sampleDeviceID = "sensor-1"

func TestUpdateDeviceModuleImage_FirstOverride_WritesNewDeviceFile(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "9.9.9"}
	if err := UpdateDeviceModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateDeviceModuleImage: %v", err)
	}

	// The TEMPLATE must be completely untouched.
	template, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if template.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("template image must stay unchanged by a device-scoped update")
	}

	// The DEVICE's effective config must reflect the override.
	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	dashboard := effective.ModulesContent.EdgeAgent.Modules["factory-dashboard"]
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:9.9.9" {
		t.Errorf("effective Image = %v, want ghcr.io/org/edge-web:9.9.9", dashboard.Settings.Image)
	}
	// Fields the patch never touched must survive from the template.
	if dashboard.Status != "running" {
		t.Errorf("effective Status = %v, want unchanged running", dashboard.Status)
	}
}

func TestPatchDeviceModule_SecondOverride_MergesOntoExistingDeviceFile(t *testing.T) {
	basePath := writeSampleManifest(t)

	// First override: image.
	firstImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "2.0.0"}
	if err := UpdateDeviceModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "factory-dashboard", firstImage, ports.FileOptions{}); err != nil {
		t.Fatalf("first UpdateDeviceModuleImage: %v", err)
	}

	// Second override: status, on a DIFFERENT field of the SAME module.
	status := iothub.Status("stopped")
	if err := PatchDeviceModule(basePath, sampleUseCaseName, sampleDeviceID, modulepatch.FieldsPatch{
		ModuleName: "factory-dashboard",
		Status:     &status,
	}, ports.FileOptions{}); err != nil {
		t.Fatalf("second PatchDeviceModule: %v", err)
	}

	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	dashboard := effective.ModulesContent.EdgeAgent.Modules["factory-dashboard"]
	// BOTH overrides must be present — the second patch must not have
	// clobbered the first.
	if dashboard.Settings.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:2.0.0 (from the FIRST override)", dashboard.Settings.Image)
	}
	if dashboard.Status != "stopped" {
		t.Errorf("Status = %v, want stopped (from the SECOND override)", dashboard.Status)
	}
}

func TestUpdateDeviceModuleImage_IntroducesNewModuleAtDeviceLevel(t *testing.T) {
	basePath := writeSampleManifest(t)

	newImage := docker.Image{Name: "ghcr.io/org/extra-sensor-agent", Tag: "1.0.0"}
	patch, err := modulepatch.NewUpdateModuleImage("extra-sensor-agent", newImage)
	if err != nil {
		t.Fatalf("NewUpdateModuleImage: %v", err)
	}
	// A brand-new module needs its OTHER required fields set too — Image
	// alone isn't a complete ModuleConfig.
	moduleType := iothub.Type("docker")
	moduleStatus := iothub.Status("running")
	restartPolicy := iothub.RestartPolicy("always")
	version := iothub.Version("1.0")
	patch.Type = &moduleType
	patch.Status = &moduleStatus
	patch.RestartPolicy = &restartPolicy
	patch.Version = &version

	if err := PatchDeviceModule(basePath, sampleUseCaseName, sampleDeviceID, patch, ports.FileOptions{}); err != nil {
		t.Fatalf("PatchDeviceModule: %v", err)
	}

	effective, err := usecase.ReadEffective(basePath, sampleUseCaseName, sampleDeviceID, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadEffective: %v", err)
	}
	extra, ok := effective.ModulesContent.EdgeAgent.Modules["extra-sensor-agent"]
	if !ok {
		t.Fatal("ReadEffective: expected a brand-new module introduced at the device level")
	}
	if extra.Settings.Image.String() != "ghcr.io/org/extra-sensor-agent:1.0.0" {
		t.Errorf("new module Image = %v, want ghcr.io/org/extra-sensor-agent:1.0.0", extra.Settings.Image)
	}
	// The ORIGINAL template module must be completely untouched.
	if effective.ModulesContent.EdgeAgent.Modules["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("factory-dashboard must be untouched by a patch introducing a different module")
	}
}

func TestPatchDeviceModule_RejectsInvalidUseCaseName(t *testing.T) {
	basePath := writeSampleManifest(t)
	err := PatchDeviceModule(basePath, "", sampleDeviceID, modulepatch.FieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchDeviceModule: want error for empty useCaseName, got nil")
	}
}

func TestPatchDeviceModule_RejectsInvalidDeviceID(t *testing.T) {
	basePath := writeSampleManifest(t)
	err := PatchDeviceModule(basePath, sampleUseCaseName, "", modulepatch.FieldsPatch{ModuleName: "factory-dashboard"}, ports.FileOptions{})
	if err == nil {
		t.Error("PatchDeviceModule: want error for empty deviceID, got nil")
	}
}
