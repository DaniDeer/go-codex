package modulesummary

import (
	"reflect"
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func sampleModuleConfig() iothub.ModuleConfig {
	return iothub.ModuleConfig{
		Settings: iothub.ModuleSettings{
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

func statusPtr(s iothub.Status) *iothub.Status                       { return &s }
func restartPolicyPtr(rp iothub.RestartPolicy) *iothub.RestartPolicy { return &rp }

func TestNewSummary_ExtractsExpectedFields(t *testing.T) {
	mc := sampleModuleConfig()
	got := NewSummary(mc)

	want := Summary{
		Image:         docker.Image{Name: "ghcr.io/org/edge-web", Tag: "1.0.0"},
		PortBindings:  mc.Settings.CreateOptions.HostConfig.PortBindings,
		Binds:         mc.Settings.CreateOptions.HostConfig.Binds,
		Status:        statusPtr("running"),
		RestartPolicy: restartPolicyPtr("always"),
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
	summary := Summary{Image: docker.Image{}, Status: statusPtr("running"), RestartPolicy: restartPolicyPtr("always")}
	if err := SummaryCodec.Validate(summary); err == nil {
		t.Error("Validate: want error for empty Image.Name, got nil")
	}
}

func TestNewSummaryFromSystemModule_OmitsAbsentStatusAndRestartPolicy(t *testing.T) {
	smc := iothub.SystemModuleConfig{
		Settings: iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.5.31"}},
		Type:     "docker",
	}
	got := NewSummaryFromSystemModule(smc)
	if got.Status != nil {
		t.Errorf("Status = %v, want nil (edgeAgent never sets it)", got.Status)
	}
	if got.RestartPolicy != nil {
		t.Errorf("RestartPolicy = %v, want nil (edgeAgent never sets it)", got.RestartPolicy)
	}
	if _, err := SummaryCodec.Encode(got); err != nil {
		t.Errorf("Encode: %v, want nil (nil Status/RestartPolicy must encode cleanly)", err)
	}
}

func TestNewSummaryFromSystemModule_SetsStatusAndRestartPolicyWhenPresent(t *testing.T) {
	smc := iothub.SystemModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-hub", Tag: "1.5.31"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
	}
	got := NewSummaryFromSystemModule(smc)
	if got.Status == nil || *got.Status != "running" {
		t.Errorf("Status = %v, want running", got.Status)
	}
	if got.RestartPolicy == nil || *got.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want always", got.RestartPolicy)
	}
}

func TestIsSystemModuleName(t *testing.T) {
	for _, name := range []iothub.ModuleName{"edgeAgent", "edgeHub"} {
		if !IsSystemModuleName(name) {
			t.Errorf("IsSystemModuleName(%q) = false, want true", name)
		}
	}
	if IsSystemModuleName("factory-dashboard") {
		t.Error("IsSystemModuleName(\"factory-dashboard\") = true, want false")
	}
}

func TestSystemModuleConfigFor(t *testing.T) {
	sm := iothub.SystemModules{
		EdgeAgent: iothub.SystemModuleConfig{Type: "docker"},
		EdgeHub:   iothub.SystemModuleConfig{Type: "docker", Status: "running"},
	}
	agent, ok := SystemModuleConfigFor(sm, "edgeAgent")
	if !ok || agent.Status != "" {
		t.Errorf("SystemModuleConfigFor(edgeAgent) = %+v, %v, want zero Status", agent, ok)
	}
	hub, ok := SystemModuleConfigFor(sm, "edgeHub")
	if !ok || hub.Status != "running" {
		t.Errorf("SystemModuleConfigFor(edgeHub) = %+v, %v, want Status=running", hub, ok)
	}
	if _, ok := SystemModuleConfigFor(sm, "edgeFoo"); ok {
		t.Error("SystemModuleConfigFor(edgeFoo) = true, want false (unknown name)")
	}
}
