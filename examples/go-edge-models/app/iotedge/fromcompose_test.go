package iotedge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/fromcompose"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/usecase"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/ports"
)

const sampleComposeYAML = `
services:
  Factory_API:
    image: ghcr.io/example-org/factory-api:1.8.16
    ports:
      - "8080:80"
    environment:
      - "TZ=Europe/Berlin"
    restart: unless-stopped
  factory-worker:
    build: ./worker
`

func writeSampleComposeFile(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "docker-compose.yaml")
	if err := os.WriteFile(path, []byte(sampleComposeYAML), 0o644); err != nil {
		t.Fatalf("write sample compose file: %v", err)
	}
	return path
}

func TestImportDockerComposeAsUseCase_WritesUseCaseFile(t *testing.T) {
	basePath := usecase.BasePath(t.TempDir())
	composeDir := t.TempDir()
	composePath := writeSampleComposeFile(t, composeDir)

	warnings, err := ImportDockerComposeAsUseCase(basePath, usecase.Name("imported"), composePath, ports.FileOptions{CreateDirs: true})
	if err != nil {
		t.Fatalf("ImportDockerComposeAsUseCase: unexpected error: %v", err)
	}

	// factory-worker (build: only) and Factory_API (sanitized name +
	// restart-policy approximation) should each produce at least one
	// warning.
	if len(warnings) < 3 {
		t.Fatalf("warnings = %+v, want at least 3 (sanitized name, restart approximation, placeholder image)", warnings)
	}
	var sawPlaceholder, sawSanitized bool
	for _, w := range warnings {
		switch w.Kind {
		case fromcompose.WarningPlaceholderImage:
			sawPlaceholder = true
		case fromcompose.WarningSanitizedName:
			sawSanitized = true
		}
	}
	if !sawPlaceholder {
		t.Error("expected a WarningPlaceholderImage for factory-worker (build: only)")
	}
	if !sawSanitized {
		t.Error("expected a WarningSanitizedName for Factory_API")
	}

	deployment, err := usecase.NewFile(basePath).Read(map[string]string{"usecase_name": "imported"}, ports.FileOptions{})
	if err != nil {
		t.Fatalf("read imported use case: %v", err)
	}
	modules := deployment.ModulesContent.EdgeAgent
	if len(modules) != 2 {
		t.Fatalf("len(modules) = %d, want 2", len(modules))
	}
	api, ok := modules["factory-api"]
	if !ok {
		t.Fatal(`modules["factory-api"] missing (name should be sanitized from "Factory_API")`)
	}
	if api.Settings.Image.String() != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("factory-api image = %q", api.Settings.Image.String())
	}
	worker, ok := modules["factory-worker"]
	if !ok {
		t.Fatal(`modules["factory-worker"] missing`)
	}
	if worker.Settings.Image.Name != "replace-me/factory-worker" {
		t.Errorf("factory-worker image name = %q, want placeholder", worker.Settings.Image.Name)
	}
}

func TestImportDockerComposeAsUseCase_PropagatesMissingFileError(t *testing.T) {
	basePathDir := t.TempDir()
	basePath := usecase.BasePath(basePathDir)
	_, err := ImportDockerComposeAsUseCase(basePath, usecase.Name("imported"), filepath.Join(basePathDir, "does-not-exist.yaml"), ports.FileOptions{CreateDirs: true})
	if err == nil {
		t.Error("ImportDockerComposeAsUseCase with a missing compose file: want error, got nil")
	}
}

func TestExportUseCaseAsDockerCompose_RoundTripsViaImport(t *testing.T) {
	basePath := usecase.BasePath(t.TempDir())
	composeDir := t.TempDir()
	composePath := writeSampleComposeFile(t, composeDir)

	if _, err := ImportDockerComposeAsUseCase(basePath, usecase.Name("imported"), composePath, ports.FileOptions{CreateDirs: true}); err != nil {
		t.Fatalf("ImportDockerComposeAsUseCase: %v", err)
	}

	exportPath := filepath.Join(composeDir, "exported-compose.yaml")
	warnings, err := ExportUseCaseAsDockerCompose(basePath, "imported", exportPath, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ExportUseCaseAsDockerCompose: unexpected error: %v", err)
	}
	// unless-stopped has no exact Compose equivalent going FORWARD, but
	// the reverse mapping (always -> "always") is exact, so no
	// restart-policy warning is expected on the way back out.
	for _, w := range warnings {
		if w.Kind == fromcompose.WarningRestartPolicyApproximated {
			t.Errorf("unexpected restart-policy warning on export: %+v", w)
		}
	}

	data, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read exported compose file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("exported compose file is empty")
	}

	project, err := format.YAML(dockercompose.ProjectCodec).Unmarshal(data)
	if err != nil {
		t.Fatalf("exported compose file did not decode as valid Compose YAML: %v", err)
	}
	if len(project.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(project.Services))
	}
	api, ok := project.Services["factory-api"]
	if !ok {
		t.Fatal(`Services["factory-api"] missing from exported compose file`)
	}
	if api.Image != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("factory-api.Image = %q", api.Image)
	}
	worker, ok := project.Services["factory-worker"]
	if !ok {
		t.Fatal(`Services["factory-worker"] missing from exported compose file`)
	}
	if !worker.Build.IsSet() {
		t.Error("factory-worker.Build.IsSet() = false, want true (placeholder image should reverse to a build: block)")
	}
}

func TestExportUseCaseAsDockerCompose_PropagatesMissingUseCaseError(t *testing.T) {
	basePathDir := t.TempDir()
	basePath := usecase.BasePath(basePathDir)
	_, err := ExportUseCaseAsDockerCompose(basePath, "does-not-exist", filepath.Join(basePathDir, "out.yaml"), ports.FileOptions{})
	if err == nil {
		t.Error("ExportUseCaseAsDockerCompose with a missing use case: want error, got nil")
	}
}
