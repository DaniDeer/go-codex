package modulesummary

import (
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
)

func sampleModuleConfig() manifesttemplate.ModuleConfig {
	return manifesttemplate.ModuleConfig{
		Settings: manifesttemplate.ModuleSettings{
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

func TestNewSummary_ExtractsExpectedFields(t *testing.T) {
	mc := sampleModuleConfig()
	got := NewSummary(mc)

	want := Summary{
		Image:         docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"},
		PortBindings:  mc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:         mc.Settings.CreateOptions.HostConfig.Binds,
		Status:        "running",
		RestartPolicy: "always",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewSummary(...) = %+v, want %+v", got, want)
	}
}

func TestNewSummary_UsesPortBindingsNotExposedPorts(t *testing.T) {
	mc := sampleModuleConfig()
	mc.Settings.CreateOptions.ExposedPorts = []docker.Port{"9999/tcp"}
	got := NewSummary(mc)
	for _, pb := range got.PortBindings {
		if string(pb.Port) == "9999/tcp" {
			t.Error("NewSummary should not surface ExposedPorts, only HostConfig.PortBindings")
		}
	}
}

func TestSummaryCodec_EncodeRoundTrip(t *testing.T) {
	summary := NewSummary(sampleModuleConfig())
	raw, err := SummaryCodec.Encode(summary)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := SummaryCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(back, summary) {
		t.Errorf("round-trip = %+v, want %+v", back, summary)
	}
}

func TestSummaryCodec_ValidatesImage(t *testing.T) {
	summary := Summary{Image: docker.Image{}, Status: "running", RestartPolicy: "always"}
	if err := SummaryCodec.Validate(summary); err == nil {
		t.Error("Validate: want error for empty Image.Name, got nil")
	}
}
