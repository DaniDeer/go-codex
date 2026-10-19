package iotedge

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/file"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/updatemoduleimage"
)

func TestNewUpdateModuleImageToolHandler_ReturnsUpdatedSummary(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	scope, basePath := writeSampleManifest(t, s)
	handler := NewUpdateModuleImageToolHandler(s, file.WriteOptions{})

	summary, err := handler(ctx, updatemoduleimage.Req{
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
	if summary.Status == nil || *summary.Status != "running" {
		t.Errorf("Status = %v, want unchanged running", summary.Status)
	}
	if summary.RestartPolicy == nil || *summary.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want unchanged always", summary.RestartPolicy)
	}

	// Confirm the change was actually persisted to disk.
	got, err := ReadUseCase(ctx, scope, sampleUseCaseName)
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:2.0.0" {
		t.Error("handler did not persist the image update to disk")
	}
}

func TestNewUpdateModuleImageToolHandler_RejectsInvalidImageURL(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	scope, basePath := writeSampleManifest(t, s)
	handler := NewUpdateModuleImageToolHandler(s, file.WriteOptions{})

	_, err := handler(ctx, updatemoduleimage.Req{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
		ImageURL:    "not a valid image ref!!",
	})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}

	// The manifest must be untouched by a rejected update.
	got, readErr := ReadUseCase(ctx, scope, sampleUseCaseName)
	if readErr != nil {
		t.Fatalf("ReadUseCase: %v", readErr)
	}
	if got.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("handler should not have touched disk for an invalid ImageURL")
	}
}

func TestNewUpdateModuleImageToolHandler_ModuleNotFound(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	_, basePath := writeSampleManifest(t, s)
	handler := NewUpdateModuleImageToolHandler(s, file.WriteOptions{})

	_, err := handler(ctx, updatemoduleimage.Req{
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
	ctx := context.Background()
	s := newTestStore(t)
	handler := NewUpdateModuleImageToolHandler(s, file.WriteOptions{})
	_, err := handler(ctx, updatemoduleimage.Req{
		BasePath:    "/nonexistent",
		UseCaseName: "nonexistent-usecase",
		ModuleName:  "factory-dashboard",
		ImageURL:    "ghcr.io/org/edge-web:2.0.0",
	})
	if err == nil {
		t.Error("handler: want error for nonexistent file, got nil")
	}
}

func TestNewUpdateModuleImageToolHandler_DeviceScoped_WritesOnlyDeviceFile(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	scope, basePath := writeSampleManifest(t, s)
	handler := NewUpdateModuleImageToolHandler(s, file.WriteOptions{})

	summary, err := handler(ctx, updatemoduleimage.Req{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
		ImageURL:    "ghcr.io/org/edge-web:5.0.0",
		DeviceID:    sampleDeviceID,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if summary.Image.String() != "ghcr.io/org/edge-web:5.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:5.0.0", summary.Image)
	}

	// The TEMPLATE must be completely untouched.
	template, err := ReadUseCase(ctx, scope, sampleUseCaseName)
	if err != nil {
		t.Fatalf("ReadUseCase: %v", err)
	}
	if template.ModulesContent.EdgeAgent["factory-dashboard"].Settings.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Error("template image must stay unchanged by a device-scoped update")
	}
}
