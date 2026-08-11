package iotedge

import (
	"context"
	"errors"
	"testing"

	regiotedge "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
	"github.com/DaniDeer/go-codex/ports"
)

func TestNewReadModuleSummaryToolHandler_ReturnsSummary(t *testing.T) {
	path := writeSampleManifest(t)
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})

	summary, err := handler(context.Background(), regiotedge.ReadModuleSummaryReq{
		ManifestPath: path,
		ModuleName:   "factory-dashboard",
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if summary.Image.String() != "ghcr.io/org/edge-web:1.0.0" {
		t.Errorf("Image = %v, want ghcr.io/org/edge-web:1.0.0", summary.Image)
	}
	if summary.Status != "running" {
		t.Errorf("Status = %v, want running", summary.Status)
	}
	if summary.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want always", summary.RestartPolicy)
	}
}

func TestNewReadModuleSummaryToolHandler_ModuleNotFound(t *testing.T) {
	path := writeSampleManifest(t)
	handler := NewReadModuleSummaryToolHandler(ports.FileOptions{})

	_, err := handler(context.Background(), regiotedge.ReadModuleSummaryReq{
		ManifestPath: path,
		ModuleName:   "does-not-exist",
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
	_, err := handler(context.Background(), regiotedge.ReadModuleSummaryReq{
		ManifestPath: "/nonexistent/manifest.json",
		ModuleName:   "factory-dashboard",
	})
	if err == nil {
		t.Error("handler: want error for nonexistent file, got nil")
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
