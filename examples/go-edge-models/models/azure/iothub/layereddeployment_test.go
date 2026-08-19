package iothub_test

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
)

func TestLayeredModulesContentCodec_RoundTrip_ModulesOnly(t *testing.T) {
	mc := iothub.LayeredModulesContent{
		EdgeAgent: iothub.Modules{
			"factory-mqtt-gateway": {
				Settings:      iothub.ModuleSettings{Image: mustImage(t, "ghcr.io/example-org/factory-mqtt-gateway:1.0.0")},
				Type:          "docker",
				Status:        "running",
				RestartPolicy: "always",
				Version:       "1.0.0",
			},
		},
	}
	enc, err := iothub.LayeredModulesContentCodec.Encode(mc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	edgeAgent := encMap[iothub.EdgeAgentKey].(map[string]any)
	if _, ok := edgeAgent["properties.desired.modules.factory-mqtt-gateway"]; !ok {
		t.Errorf("$edgeAgent = %+v, want flat module key present", edgeAgent)
	}
	if _, ok := encMap[iothub.EdgeHubKey]; ok {
		t.Errorf("$edgeHub = %+v, want OMITTED (no routes)", encMap[iothub.EdgeHubKey])
	}

	dec, err := iothub.LayeredModulesContentCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := dec.EdgeAgent["factory-mqtt-gateway"]; !ok {
		t.Errorf("Decode.EdgeAgent = %+v, want factory-mqtt-gateway", dec.EdgeAgent)
	}
	if len(dec.SystemModules) != 0 {
		t.Errorf("Decode.SystemModules = %+v, want empty", dec.SystemModules)
	}
}

func TestLayeredModulesContentCodec_RoundTrip_SystemModulesOverride(t *testing.T) {
	mc := iothub.LayeredModulesContent{
		EdgeAgent: iothub.Modules{
			"factory-mqtt-gateway": {
				Settings:      iothub.ModuleSettings{Image: mustImage(t, "ghcr.io/example-org/factory-mqtt-gateway:1.0.0")},
				Type:          "docker",
				Status:        "running",
				RestartPolicy: "always",
				Version:       "1.0.0",
			},
		},
		SystemModules: map[iothub.SystemModuleName]iothub.SystemModuleConfig{
			"edgeAgent": {
				Settings: iothub.ModuleSettings{Image: mustImage(t, "mcr.microsoft.com/azureiotedge-agent:1.5.99")},
				Type:     "docker",
			},
		},
	}
	enc, err := iothub.LayeredModulesContentCodec.Encode(mc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encMap := enc.(map[string]any)
	edgeAgent := encMap[iothub.EdgeAgentKey].(map[string]any)
	if _, ok := edgeAgent["properties.desired.modules.factory-mqtt-gateway"]; !ok {
		t.Errorf("$edgeAgent = %+v, want flat module key present", edgeAgent)
	}
	if _, ok := edgeAgent["properties.desired.systemModules.edgeAgent"]; !ok {
		t.Errorf("$edgeAgent = %+v, want flat systemModule key present, side by side with module keys", edgeAgent)
	}

	dec, err := iothub.LayeredModulesContentCodec.Decode(encMap)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if _, ok := dec.EdgeAgent["factory-mqtt-gateway"]; !ok {
		t.Errorf("Decode.EdgeAgent = %+v, want factory-mqtt-gateway", dec.EdgeAgent)
	}
	smc, ok := dec.SystemModules["edgeAgent"]
	if !ok {
		t.Fatalf("Decode.SystemModules = %+v, want edgeAgent entry", dec.SystemModules)
	}
	if smc.Settings.Image.String() != "mcr.microsoft.com/azureiotedge-agent:1.5.99" {
		t.Errorf("Decode.SystemModules[edgeAgent].Settings.Image = %v, want mcr.microsoft.com/azureiotedge-agent:1.5.99", smc.Settings.Image)
	}
}

func TestLayeredModulesContentCodec_DecodeError_UnrecognizedEdgeAgentKeyPrefix(t *testing.T) {
	_, err := iothub.LayeredModulesContentCodec.Decode(map[string]any{
		iothub.EdgeAgentKey: map[string]any{
			"schemaVersion": "1.1",
		},
	})
	if err == nil {
		t.Error("Decode: want error for $edgeAgent key matching neither prefix, got nil")
	}
}

func TestLayeredModulesContentCodec_DecodeError_MissingEdgeAgent(t *testing.T) {
	_, err := iothub.LayeredModulesContentCodec.Decode(map[string]any{})
	if err == nil {
		t.Error("Decode: want error for missing $edgeAgent, got nil")
	}
}

func TestLayeredModulesContentCodec_DecodeError_NonObject(t *testing.T) {
	_, err := iothub.LayeredModulesContentCodec.Decode("not an object")
	if err == nil {
		t.Error("Decode: want TypeMismatchError for non-object input, got nil")
	}
}

func TestLayeredModulesContentCodec_Schema(t *testing.T) {
	s := iothub.LayeredModulesContentCodec.Schema
	if s.Type != "object" {
		t.Errorf("Schema.Type = %q, want object", s.Type)
	}
	if len(s.Required) != 1 || s.Required[0] != iothub.EdgeAgentKey {
		t.Errorf("Schema.Required = %+v, want [%q]", s.Required, iothub.EdgeAgentKey)
	}
}
