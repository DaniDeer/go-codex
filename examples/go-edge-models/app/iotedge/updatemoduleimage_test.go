package iotedge

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage"
	"github.com/DaniDeer/go-codex/ports"
)

func TestNewUpdateModuleImageToolHandler_ReturnsUpdatedSummary(t *testing.T) {
	basePath := writeSampleManifest(t)
	handler := NewUpdateModuleImageToolHandler(ports.FileOptions{})

	summary, err := handler(context.Background(), updatemoduleimage.Req{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
		ImageURL:    "ghcr.io/org/edge-web:2.0.0",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if summary.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:2.0.0", summary.Image)
	}
	// Other fields should still reflect the unchanged manifest state.
	if summary.Status != "running" {
		t.Errorf("Status = %v, want unchanged running", summary.Status)
	}
	if summary.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged always", summary.RestartPolicy)
	}

	// Confirm the change was actually persisted to disk.
	got, err := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Error("handler did not persist the image update to disk")
	}
}

func TestNewUpdateModuleImageToolHandler_RejectsInvalidImageURL(t *testing.T) {
	basePath := writeSampleManifest(t)
	handler := NewUpdateModuleImageToolHandler(ports.FileOptions{})

	_, err := handler(context.Background(), updatemoduleimage.Req{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
		ImageURL:    "not a valid image ref!!",
	})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}

	// The manifest must be untouched by a rejected update.
	got, readErr := ReadUseCase(basePath, sampleUseCaseName, ports.FileOptions{})
	if readErr != nil {
		t.Fatalf("ReadUseCase: %v", readErr)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("handler should not have touched disk for an invalid ImageURL")
	}
}

func TestNewUpdateModuleImageToolHandler_ModuleNotFound(t *testing.T) {
	basePath := writeSampleManifest(t)
	handler := NewUpdateModuleImageToolHandler(ports.FileOptions{})

	_, err := handler(context.Background(), updatemoduleimage.Req{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "does-not-exist",
		ImageURL:    "ghcr.io/org/edge-web:2.0.0",
	})
	if err == nil {
		t.Fatal("handler: want error for missing module, got nil")
	}
	var notFoundErr ModuleNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("handler error = %v, want ModuleNotFoundError", err)
	}
}

func TestNewUpdateModuleImageToolHandler_PropagatesMissingFileError(t *testing.T) {
	handler := NewUpdateModuleImageToolHandler(ports.FileOptions{})
	_, err := handler(context.Background(), updatemoduleimage.Req{
		BasePath:    "/nonexistent",
		UseCaseName: "nonexistent-usecase",
		ModuleName:  "factory-dashboard",
		ImageURL:    "ghcr.io/org/edge-web:2.0.0",
	})
	if err == nil {
		t.Error("handler: want error for nonexistent file, got nil")
	}
}
