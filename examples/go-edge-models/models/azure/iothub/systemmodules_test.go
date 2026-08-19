package iothub_test

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestSystemModuleConfigCodec_RoundTrip_WithStatusAndRestartPolicy(t *testing.T) {
	smc := iothub.SystemModuleConfig{
		Settings:      iothub.ModuleSettings{Image: mustImage(t, "mcr.microsoft.com/azureiotedge-hub:1.5.31")},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
	}
	enc, err := iothub.SystemModuleConfigCodec.Encode(smc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	if _, ok := encMap["status"]; !ok {
		t.Errorf("Encode result = %+v, want \"status\" present", encMap)
	}
	if _, ok := encMap["restartPolicy"]; !ok {
		t.Errorf("Encode result = %+v, want \"restartPolicy\" present", encMap)
	}

	dec, err := iothub.SystemModuleConfigCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Status != "running" || dec.RestartPolicy != "always" {
		t.Errorf("round trip = %+v, want %+v", dec, smc)
	}
}

// TestSystemModuleConfigCodec_OmitsAbsentStatusAndRestartPolicy is the
// edgeAgent case: real Azure manifests show edgeAgent's OWN
// systemModules entry with NO "status"/"restartPolicy" key at all — this
// must Encode WITHOUT error (not try to encode "" through
// StatusCodec/RestartPolicyCodec's OneOf constraint) and OMIT both keys
// entirely.
func TestSystemModuleConfigCodec_OmitsAbsentStatusAndRestartPolicy(t *testing.T) {
	smc := iothub.SystemModuleConfig{
		Settings: iothub.ModuleSettings{Image: mustImage(t, "mcr.microsoft.com/azureiotedge-agent:1.5.31")},
		Type:     "docker",
	}
	enc, err := iothub.SystemModuleConfigCodec.Encode(smc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	if _, ok := encMap["status"]; ok {
		t.Errorf("Encode result = %+v, want \"status\" OMITTED (not present)", encMap)
	}
	if _, ok := encMap["restartPolicy"]; ok {
		t.Errorf("Encode result = %+v, want \"restartPolicy\" OMITTED (not present)", encMap)
	}

	dec, err := iothub.SystemModuleConfigCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.Status != "" || dec.RestartPolicy != "" {
		t.Errorf("round trip = %+v, want empty Status/RestartPolicy", dec)
	}
}

func TestSystemModuleConfigCodec_DecodeError_MissingRequiredField(t *testing.T) {
	_, err := iothub.SystemModuleConfigCodec.Decode(map[string]any{
		"type": "docker",
	})
	if err == nil {
		t.Error("Decode: want error for missing \"settings\", got nil")
	}
}

func TestSystemModuleConfigCodec_DecodeError_NonObject(t *testing.T) {
	_, err := iothub.SystemModuleConfigCodec.Decode("not an object")
	if err == nil {
		t.Error("Decode: want TypeMismatchError for non-object input, got nil")
	}
}

func TestSystemModuleNameCodec_RoundTrip(t *testing.T) {
	got, err := iothub.SystemModuleNameCodec.Decode("properties.desired.systemModules.edgeAgent")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != "edgeAgent" {
		t.Errorf("Decode = %q, want edgeAgent", got)
	}
	enc, err := iothub.SystemModuleNameCodec.Encode(got)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if enc != "properties.desired.systemModules.edgeAgent" {
		t.Errorf("Encode = %v, want full dotted key", enc)
	}
}

func TestSystemModuleNameCodec_RejectsUnknownName(t *testing.T) {
	if _, err := iothub.SystemModuleNameCodec.Encode("edgeFoo"); err == nil {
		t.Error("Encode(\"edgeFoo\"): want error, got nil")
	}
}

func TestSystemModulesCodec_RoundTrip(t *testing.T) {
	sm := iothub.SystemModules{
		EdgeAgent: iothub.SystemModuleConfig{
			Settings: iothub.ModuleSettings{Image: mustImage(t, "mcr.microsoft.com/azureiotedge-agent:1.5.31")},
			Type:     "docker",
		},
		EdgeHub: iothub.SystemModuleConfig{
			Settings:      iothub.ModuleSettings{Image: mustImage(t, "mcr.microsoft.com/azureiotedge-hub:1.5.31")},
			Type:          "docker",
			Status:        "running",
			RestartPolicy: "always",
		},
	}
	enc, err := iothub.SystemModulesCodec.Encode(sm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	dec, err := iothub.SystemModulesCodec.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if dec.EdgeAgent.Status != "" {
		t.Errorf("EdgeAgent.Status = %q, want empty", dec.EdgeAgent.Status)
	}
	if dec.EdgeHub.Status != "running" {
		t.Errorf("EdgeHub.Status = %q, want running", dec.EdgeHub.Status)
	}
}

func TestModulesCodec_BareKeyRoundTrip(t *testing.T) {
	m := map[iothub.ModuleName]iothub.ModuleConfig{
		"vulnerability-scanner": {
			Settings:      iothub.ModuleSettings{Image: mustImage(t, "ghcr.io/example-org/edge-security-scanner:0.0.2")},
			Type:          "docker",
			Status:        "running",
			RestartPolicy: "always",
			Version:       "auto",
		},
	}
	enc, err := iothub.BaseModulesCodec.Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap, ok := enc.(map[string]any)
	if !ok {
		t.Fatalf("Encode result = %T, want map[string]any", enc)
	}
	if _, ok := encMap["vulnerability-scanner"]; !ok {
		t.Errorf("Encode result = %+v, want bare (non-prefixed) key present", encMap)
	}

	dec, err := iothub.BaseModulesCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := dec["vulnerability-scanner"]; !ok {
		t.Errorf("Decode result = %+v, want bare key round-tripped", dec)
	}
}

func TestModulesCodec_RejectsInvalidName(t *testing.T) {
	_, err := iothub.BaseModulesCodec.Decode(map[string]any{
		"Not_A_Slug": map[string]any{},
	})
	if err == nil {
		t.Error("Decode: want error for non-slug bare key, got nil")
	}
}

func TestRoutesCodec_BareKeyRoundTrip(t *testing.T) {
	m := map[iothub.RouteName]iothub.Route{
		"telemetry-route": {From: "/messages/modules/vulnerability-scanner/outputs/telemetry", To: iothub.UpstreamTarget},
	}
	enc, err := iothub.BaseRoutesCodec.Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	if _, ok := encMap["telemetry-route"]; !ok {
		t.Errorf("Encode result = %+v, want bare key present", encMap)
	}
	dec, err := iothub.BaseRoutesCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := dec["telemetry-route"]; !ok {
		t.Errorf("Decode result = %+v, want bare key round-tripped", dec)
	}
}

func mustImage(t *testing.T, s string) docker.Image {
	t.Helper()
	img, err := iothub.ImageCodec.Decode(s)
	if err != nil {
		t.Fatalf("ImageCodec.Decode(%q): %v", s, err)
	}
	return img
}
