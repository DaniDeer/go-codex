package iotedge

import (
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func sampleModuleConfig() ModuleConfig {
	return ModuleConfig{
		Settings: ModuleSettings{
			Image: docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"},
			CreateOptions: docker.CreateOptions{
				HostConfig: docker.HostConfig{
					PortBindings: []docker.PortBinding{
						{Port: "8080/tcp", Bindings: []docker.PortBindingEntry{{HostPort: "8080"}}},
					},
					Binds: []docker.Bind{
						{HostPath: "/data", ContainerPath: "/data", Mode: "rw"},
					},
				},
			},
		},
		Status:        "running",
		RestartPolicy: "always",
	}
}

func TestNewModuleSummary_ExtractsExpectedFields(t *testing.T) {
	mc := sampleModuleConfig()
	got := NewModuleSummary(mc)

	want := ModuleSummary{
		Image:         docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"},
		PortBindings:  mc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:         mc.Settings.CreateOptions.HostConfig.Binds,
		Status:        "running",
		RestartPolicy: "always",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewModuleSummary(...) = %+v, want %+v", got, want)
	}
}

func TestNewModuleSummary_UsesPortBindingsNotExposedPorts(t *testing.T) {
	mc := sampleModuleConfig()
	mc.Settings.CreateOptions.ExposedPorts = []docker.Port{"9999/tcp"}
	got := NewModuleSummary(mc)
	for _, pb := range got.PortBindings {
		if string(pb.Port) == "9999/tcp" {
			t.Error("NewModuleSummary should not surface ExposedPorts, only HostConfig.PortBindings")
		}
	}
}

func TestModuleSummaryCodec_EncodeRoundTrip(t *testing.T) {
	summary := NewModuleSummary(sampleModuleConfig())
	raw, err := ModuleSummaryCodec.Encode(summary)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := ModuleSummaryCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(back, summary) {
		t.Errorf("round-trip = %+v, want %+v", back, summary)
	}
}

func TestModuleSummaryCodec_ValidatesImage(t *testing.T) {
	summary := ModuleSummary{Image: docker.Image{}, Status: "running", RestartPolicy: "always"}
	if err := ModuleSummaryCodec.Validate(summary); err == nil {
		t.Error("Validate: want error for empty Image.Name, got nil")
	}
}
