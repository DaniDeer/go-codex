package modulepatch

import (
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestModuleSettingsPatchCodec_EncodeOmitsUnsetFields(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "1.0.0"}
	raw, err := ModuleSettingsPatchCodec.Encode(ModuleSettingsPatch{Image: &img})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	if _, ok := obj["image"]; !ok {
		t.Error("expected image key to be present")
	}
	if _, ok := obj["createOptions"]; ok {
		t.Error("createOptions key should be ABSENT (never set)")
	}
}

func TestModuleSettingsPatchCodec_EncodeAllUnset_ReturnsEmptyMap(t *testing.T) {
	raw, err := ModuleSettingsPatchCodec.Encode(ModuleSettingsPatch{})
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	if len(obj) != 0 {
		t.Errorf("encoded map = %+v, want empty", obj)
	}
}

func TestModuleSettingsPatchCodec_DecodeRoundTrip(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "1.0.0"}
	co := docker.CreateOptions{Hostname: "sensor-host"}
	original := ModuleSettingsPatch{Image: &img, CreateOptions: &co}

	raw, err := ModuleSettingsPatchCodec.Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := ModuleSettingsPatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.Image == nil || *back.Image != img {
		t.Errorf("Image = %v, want %v", back.Image, img)
	}
	if back.CreateOptions == nil || back.CreateOptions.Hostname != co.Hostname {
		t.Errorf("CreateOptions = %+v, want %+v", back.CreateOptions, co)
	}
}
