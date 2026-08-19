package deviceconfig

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
)

func TestPatchCodec_EncodeDecodeRoundTrip_WholeModuleKey(t *testing.T) {
	p := Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1": map[string]any{"status": "stopped"},
		},
	}
	raw, err := PatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := PatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.EdgeAgent) != 1 {
		t.Fatalf("EdgeAgent = %v, want 1 entry", got.EdgeAgent)
	}
	statusMap, ok := got.EdgeAgent["factory-mqtt-gateway-1"].(map[string]any)
	if !ok || statusMap["status"] != "stopped" {
		t.Errorf("EdgeAgent[factory-mqtt-gateway-1] = %v, want {status: stopped}", got.EdgeAgent["factory-mqtt-gateway-1"])
	}
}

func TestPatchCodec_EncodeDecodeRoundTrip_DottedPathKey(t *testing.T) {
	p := Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.env.BROKER_URL": "mqtts://broker.example.com:8883",
		},
	}
	raw, err := PatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := PatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.EdgeAgent["factory-mqtt-gateway-1.env.BROKER_URL"] != "mqtts://broker.example.com:8883" {
		t.Errorf("EdgeAgent dotted-path key = %v, want mqtts://broker.example.com:8883", got.EdgeAgent["factory-mqtt-gateway-1.env.BROKER_URL"])
	}
}

func TestPatchCodec_EncodeDecodeRoundTrip_EdgeHubRoute(t *testing.T) {
	p := Patch{
		EdgeHub: map[manifesttemplate.RouteName]manifesttemplate.Route{
			"factory-mqtt-to-ingest": {
				From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
				To:   manifesttemplate.NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
			},
		},
	}
	raw, err := PatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := PatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(got.EdgeHub) != 1 || got.EdgeHub["factory-mqtt-to-ingest"] != p.EdgeHub["factory-mqtt-to-ingest"] {
		t.Errorf("EdgeHub = %+v, want %+v", got.EdgeHub, p.EdgeHub)
	}
}

func TestPatchCodec_Encode_EmptyPatch_ReturnsError(t *testing.T) {
	_, err := PatchCodec.Encode(Patch{})
	if err == nil {
		t.Error("Encode: want EmptyPatchError for empty patch, got nil")
	}
	if _, ok := err.(EmptyPatchError); !ok {
		t.Errorf("Encode error = %v (%T), want EmptyPatchError", err, err)
	}
}

func TestPatchCodec_Encode_RejectsInvalidEdgeAgentKey(t *testing.T) {
	_, err := PatchCodec.Encode(Patch{
		EdgeAgent: map[string]any{"Not_A_Valid_Slug!": "value"},
	})
	if err == nil {
		t.Error("Encode: want error for invalid module-name slug key, got nil")
	}
}

func TestPatchCodec_Encode_RejectsEmptyEdgeAgentKey(t *testing.T) {
	_, err := PatchCodec.Encode(Patch{
		EdgeAgent: map[string]any{"": "value"},
	})
	if err == nil {
		t.Error("Encode: want error for empty edge-agent key, got nil")
	}
}

func TestEmptyPatchError_LogValue(t *testing.T) {
	err := EmptyPatchError{}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
	// EmptyPatchError carries no fields — LogValue should still return a
	// (empty) group, not panic.
	_ = err.LogValue()
}

func TestPatchCodec_Decode_RejectsEdgeAgentKeyMissingModulePrefix(t *testing.T) {
	raw := map[string]any{
		"modulesContent": map[string]any{
			"$edgeAgent": map[string]any{
				"not-prefixed-with-properties.desired.modules.": map[string]any{"status": "stopped"},
			},
		},
	}
	_, err := PatchCodec.Decode(raw)
	if err == nil {
		t.Fatal("Decode: want error for a $edgeAgent key missing the module-key prefix, got nil")
	}
	// edgeAgentKeyPrefixError was promoted to codex.DottedKeyError when
	// this bucket was rebuilt on codex.DottedPatchMapCodec.
	var prefixErr codex.DottedKeyError
	if !errors.As(err, &prefixErr) {
		t.Fatalf("Decode error = %v (%T), want codex.DottedKeyError", err, err)
	}
	if prefixErr.Key != "not-prefixed-with-properties.desired.modules." {
		t.Errorf("DottedKeyError.Key = %q, want %q", prefixErr.Key, "not-prefixed-with-properties.desired.modules.")
	}
}

func TestDottedKeyError_LogValue(t *testing.T) {
	err := codex.DottedKeyError{Key: "bad-key", Template: EdgeAgentPatchTemplate, Err: errors.New("boom")}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
	lv := err.LogValue()
	found := false
	for _, a := range lv.Group() {
		if a.Key == "key" && a.Value.String() == "bad-key" {
			found = true
		}
	}
	if !found {
		t.Error("LogValue missing key attribute")
	}
}
