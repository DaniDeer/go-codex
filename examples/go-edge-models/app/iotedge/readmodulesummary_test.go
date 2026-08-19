package iotedge

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/modulesummary"
	"github.com/DaniDeer/go-codex/ports"
)

func TestNewReadModuleSummaryToolHandler_ReturnsSummary(t *testing.T) {
	basePath := writeSampleManifest(t)
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})

	summary, err := handler(context.Background(), modulesummary.ReadReq{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if summary.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", summary.Image)
	}
	if summary.Status == nil || *summary.Status != "running" {
		t.Errorf("Status = %v, want running", summary.Status)
	}
	if summary.RestartPolicy == nil || *summary.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want always", summary.RestartPolicy)
	}
}

func TestNewReadModuleSummaryToolHandler_ModuleNotFound(t *testing.T) {
	basePath := writeSampleManifest(t)
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})

	_, err := handler(context.Background(), modulesummary.ReadReq{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "does-not-exist",
	})
	if err == nil {
		t.Fatal("handler: want error for missing module, got nil")
	}
	var notFoundErr ModuleNotFoundError
	if !errors.As(err, &notFoundErr) {
		t.Errorf("handler error = %v, want ModuleNotFoundError", err)
	}
	if notFoundErr.ModuleName != "does-not-exist" {
		t.Errorf("ModuleNotFoundError.ModuleName = %q, want %q", notFoundErr.ModuleName, "does-not-exist")
	}
}

func TestNewReadModuleSummaryToolHandler_PropagatesReadError(t *testing.T) {
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})
	_, err := handler(context.Background(), modulesummary.ReadReq{
		BasePath:    "/nonexistent",
		UseCaseName: "nonexistent-usecase",
		ModuleName:  "factory-dashboard",
	})
	if err == nil {
		t.Error("handler: want error for nonexistent file, got nil")
	}
}

func TestNewReadModuleSummaryToolHandler_DeviceScoped_ReturnsEffectiveSummary(t *testing.T) {
	basePath := writeSampleManifest(t)
	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "3.0.0"}
	if err := UpdateDeviceModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateDeviceModuleImage: %v", err)
	}
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})

	summary, err := handler(context.Background(), modulesummary.ReadReq{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
		DeviceID:    sampleDeviceID,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if summary.Image.String() != "ghcr.io/org/edge-web:3.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:3.0.0 (device-effective)", summary.Image)
	}
	// Unrelated to the device patch — must survive from the template.
	if summary.Status == nil || *summary.Status != "running" {
		t.Errorf("Status = %v, want unchanged running", summary.Status)
	}
}

func TestNewReadModuleSummaryToolHandler_DeviceScoped_TemplateUnaffected(t *testing.T) {
	basePath := writeSampleManifest(t)
	newImage := docker.Image{Name: "ghcr.io/org/edge-web", Tag: "3.0.0"}
	if err := UpdateDeviceModuleImage(basePath, sampleUseCaseName, sampleDeviceID, "factory-dashboard", newImage, ports.FileOptions{}); err != nil {
		t.Fatalf("UpdateDeviceModuleImage: %v", err)
	}
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})

	// Same request WITHOUT DeviceID must still reflect the TEMPLATE.
	summary, err := handler(context.Background(), modulesummary.ReadReq{
		BasePath:    basePath,
		UseCaseName: sampleUseCaseName,
		ModuleName:  "factory-dashboard",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if summary.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want unchanged ghcr.io/org/edge-web:1.0.0 (template scope)", summary.Image)
	}
}

func TestModuleNotFoundError_LogValue(t *testing.T) {
	err := ModuleNotFoundError{ModuleName: "factory-dashboard"}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
	lv := err.LogValue()
	found := false
	for _, a := range lv.Group() {
		if a.Key == "module_name" && a.Value.String() == "factory-dashboard" {
			found = true
		}
	}
	if !found {
		t.Error("LogValue missing module_name attribute")
	}
}
