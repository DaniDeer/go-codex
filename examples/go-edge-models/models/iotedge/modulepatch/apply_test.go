package modulepatch

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func sampleModuleConfig() iothub.ModuleConfig {
	return iothub.ModuleConfig{
		Settings: iothub.ModuleSettings{
			Image: docker.Image{Name: "ghcr.io/example-org/factory-gateway", Tag: "0.12.5"},
		},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "on-failure",
		Version:       "1.0",
	}
}

func TestApplyToModule_RoundTrip(t *testing.T) {
	base := sampleModuleConfig()
	stopped := iothub.Status("stopped")
	patch := FieldsPatch{ModuleName: "factory-gateway", Status: &stopped}

	got, err := ApplyToModule(base, patch)
	if err != nil {
		t.Fatalf("ApplyToModule: %v", err)
	}
	if got.Status != "stopped" {
		t.Errorf("Status = %v, want stopped", got.Status)
	}
}

func TestApplyToModule_OneFieldOverwritten_SiblingsSurvive(t *testing.T) {
	base := sampleModuleConfig()
	newVersion := iothub.Version("2.0")
	patch := FieldsPatch{ModuleName: "factory-gateway", Version: &newVersion}

	got, err := ApplyToModule(base, patch)
	if err != nil {
		t.Fatalf("ApplyToModule: %v", err)
	}
	if got.Version != "2.0" {
		t.Errorf("Version = %v, want 2.0", got.Version)
	}
	// Siblings survive untouched.
	if got.Status != "running" {
		t.Errorf("Status = %v, want unchanged running", got.Status)
	}
	if got.RestartPolicy != "on-failure" {
		t.Errorf("RestartPolicy = %v, want unchanged on-failure", got.RestartPolicy)
	}
	if got.Settings.Image.String() != "ghcr.io/example-org/factory-gateway:0.12.5" {
		t.Errorf("Image = %v, want unchanged", got.Settings.Image)
	}
}

func TestApplyToModule_PropagatesDecodeValidationErrors(t *testing.T) {
	base := sampleModuleConfig()
	// "bogus" is not a valid Type enum value ("docker" is the only one) —
	// merging it in must surface a decode validation error from
	// ModuleConfigCodec, not silently succeed.
	bogus := iothub.Type("bogus")
	patch := FieldsPatch{ModuleName: "factory-gateway", Type: &bogus}

	_, err := ApplyToModule(base, patch)
	if err == nil {
		t.Error("ApplyToModule: want error for invalid merged type value, got nil")
	}
}
