package modulepatch

import (
	"errors"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge"
)

func TestModuleFieldsPatchCodec_EncodeImageOnly(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	p := ModuleFieldsPatch{ModuleName: "temp-sensor", Settings: &ModuleSettingsPatch{Image: &img}}

	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iotedge.ModuleKeyPrefix+"temp-sensor"].(map[string]any)

	settings, ok := module["settings"].(map[string]any)
	if !ok {
		t.Fatal("expected settings key to be present")
	}
	if settings["image"] == nil {
		t.Error("expected settings.image to be present")
	}
	if _, ok := settings["createOptions"]; ok {
		t.Error("settings.createOptions should be ABSENT when CreateOptions is not set")
	}
	if _, ok := module["status"]; ok {
		t.Error("status key should be ABSENT when Status is not set")
	}
	if _, ok := module["env"]; ok {
		t.Error("env key should be ABSENT when Env is not set")
	}
}

func TestModuleFieldsPatchCodec_EncodeStatusOnly(t *testing.T) {
	status := iotedge.Status("running")
	p := ModuleFieldsPatch{ModuleName: "temp-sensor", Status: &status}

	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iotedge.ModuleKeyPrefix+"temp-sensor"].(map[string]any)

	if _, ok := module["settings"]; ok {
		t.Error("settings key should be ABSENT when Settings is nil")
	}
	if module["status"] != "running" {
		t.Errorf("status = %v, want \"running\"", module["status"])
	}
}

func TestModuleFieldsPatchCodec_EncodeMultipleFields(t *testing.T) {
	status := iotedge.Status("running")
	rp := iotedge.RestartPolicy("always")
	p := ModuleFieldsPatch{ModuleName: "temp-sensor", Status: &status, RestartPolicy: &rp}

	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iotedge.ModuleKeyPrefix+"temp-sensor"].(map[string]any)

	if module["status"] != "running" || module["restartPolicy"] != "always" {
		t.Errorf("module = %+v, want status=running, restartPolicy=always", module)
	}
	if len(module) != 2 {
		t.Errorf("module has %d keys, want exactly 2 (status, restartPolicy)", len(module))
	}
}

func TestModuleFieldsPatchCodec_EncodeEnv(t *testing.T) {
	env := iotedge.EnvVars{"FOO": {Value: iotedge.NewEnvVarValueString("bar")}}
	p := ModuleFieldsPatch{ModuleName: "temp-sensor", Env: &env}

	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iotedge.ModuleKeyPrefix+"temp-sensor"].(map[string]any)
	if _, ok := module["env"]; !ok {
		t.Error("expected env key to be present")
	}
}

func TestModuleFieldsPatchCodec_EncodeEmptyPatch_ReturnsError(t *testing.T) {
	p := ModuleFieldsPatch{ModuleName: "temp-sensor"}
	_, err := ModuleFieldsPatchCodec.Encode(p)
	if err == nil {
		t.Fatal("Encode: want error for empty patch, got nil")
	}
	var emptyErr EmptyPatchError
	if !errors.As(err, &emptyErr) {
		t.Errorf("Encode error = %v, want EmptyPatchError", err)
	}
	if emptyErr.ModuleName != "temp-sensor" {
		t.Errorf("EmptyPatchError.ModuleName = %q, want %q", emptyErr.ModuleName, "temp-sensor")
	}
}

func TestModuleFieldsPatchCodec_EncodeEmptySettings_TreatedAsAbsent(t *testing.T) {
	// A non-nil *ModuleSettingsPatch with nothing inside it still encodes
	// to a present (but empty) "settings" object — the outer
	// ModuleFieldsPatch's own EmptyPatchError check only looks at whether
	// ITS OWN fields (settings/env/type/status/restartPolicy/version)
	// produced any keys at all; "settings" itself being non-nil DOES
	// count as a set field, even though it happens to have nothing inside.
	// This documents actual behavior, not a bug: a caller allocating
	// &ModuleSettingsPatch{} with nothing set is a caller mistake, same
	// category as any other no-op patch.
	p := ModuleFieldsPatch{ModuleName: "temp-sensor", Settings: &ModuleSettingsPatch{}}
	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iotedge.ModuleKeyPrefix+"temp-sensor"].(map[string]any)
	settings, ok := module["settings"].(map[string]any)
	if !ok {
		t.Fatal("expected settings key to be present (Settings pointer was non-nil)")
	}
	if len(settings) != 0 {
		t.Errorf("settings = %+v, want empty (nothing set inside)", settings)
	}
}

func TestModuleFieldsPatchCodec_EncodeRejectsInvalidImage(t *testing.T) {
	img := docker.Image{} // empty Name — invalid
	p := ModuleFieldsPatch{ModuleName: "temp-sensor", Settings: &ModuleSettingsPatch{Image: &img}}
	if _, err := ModuleFieldsPatchCodec.Encode(p); err == nil {
		t.Error("Encode: want error for invalid Image, got nil")
	}
}

func TestModuleFieldsPatchCodec_DecodeRoundTrip(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	status := iotedge.Status("running")
	rp := iotedge.RestartPolicy("always")
	p := ModuleFieldsPatch{
		ModuleName:    "temp-sensor",
		Settings:      &ModuleSettingsPatch{Image: &img},
		Status:        &status,
		RestartPolicy: &rp,
	}

	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := ModuleFieldsPatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if back.ModuleName != p.ModuleName {
		t.Errorf("ModuleName = %q, want %q", back.ModuleName, p.ModuleName)
	}
	if back.Settings == nil || back.Settings.Image == nil || *back.Settings.Image != img {
		t.Errorf("Settings.Image = %v, want %v", back.Settings, img)
	}
	if back.Settings.CreateOptions != nil {
		t.Error("Settings.CreateOptions should remain nil (was never set)")
	}
	if back.Status == nil || *back.Status != status {
		t.Errorf("Status = %v, want %v", back.Status, status)
	}
	if back.RestartPolicy == nil || *back.RestartPolicy != rp {
		t.Errorf("RestartPolicy = %v, want %v", back.RestartPolicy, rp)
	}
	if back.Env != nil {
		t.Error("Env should remain nil (was never set)")
	}
	if back.Type != nil {
		t.Error("Type should remain nil (was never set)")
	}
	if back.Version != nil {
		t.Error("Version should remain nil (was never set)")
	}
}

func TestModuleFieldsPatchCodec_DecodeCreateOptionsAndEnv(t *testing.T) {
	co := docker.CreateOptions{Hostname: "sensor-host"}
	env := iotedge.EnvVars{"FOO": {Value: iotedge.NewEnvVarValueString("bar")}}
	p := ModuleFieldsPatch{
		ModuleName: "temp-sensor",
		Settings:   &ModuleSettingsPatch{CreateOptions: &co},
		Env:        &env,
	}

	raw, err := ModuleFieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := ModuleFieldsPatchCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if back.Settings == nil || back.Settings.CreateOptions == nil || back.Settings.CreateOptions.Hostname != "sensor-host" {
		t.Errorf("Settings.CreateOptions = %+v, want Hostname=sensor-host", back.Settings)
	}
	if back.Env == nil || !reflect.DeepEqual(*back.Env, env) {
		t.Errorf("Env = %+v, want %+v", back.Env, env)
	}
}

func TestEmptyPatchError_LogValue(t *testing.T) {
	err := EmptyPatchError{ModuleName: "temp-sensor"}
	if err.Error() == "" {
		t.Error("Error() should not be empty")
	}
	lv := err.LogValue()
	found := false
	for _, a := range lv.Group() {
		if a.Key == "module_name" {
			found = true
			if a.Value.String() != "temp-sensor" {
				t.Errorf("module_name = %q, want %q", a.Value.String(), "temp-sensor")
			}
		}
	}
	if !found {
		t.Error("LogValue missing module_name attribute")
	}
}

func TestNewUpdateModuleImagePatch_ReturnsValueOnSuccess(t *testing.T) {
	image := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	got, err := NewUpdateModuleImagePatch("temp-sensor", image)
	if err != nil {
		t.Fatalf("NewUpdateModuleImagePatch: unexpected error: %v", err)
	}
	if got.ModuleName != "temp-sensor" {
		t.Errorf("ModuleName = %q, want %q", got.ModuleName, "temp-sensor")
	}
	if got.Settings == nil || got.Settings.Image == nil || *got.Settings.Image != image {
		t.Errorf("Settings.Image = %v, want %v", got.Settings, image)
	}
	if got.Settings.CreateOptions != nil {
		t.Error("Settings.CreateOptions should not be set")
	}
	if got.Status != nil || got.RestartPolicy != nil || got.Env != nil || got.Type != nil || got.Version != nil {
		t.Error("only Settings.Image should be set on the returned patch")
	}
}

func TestNewUpdateModuleImagePatch_RejectsInvalidModuleName(t *testing.T) {
	image := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	if _, err := NewUpdateModuleImagePatch("Not A Valid Slug", image); err == nil {
		t.Error("NewUpdateModuleImagePatch: want error for invalid module name, got nil")
	}
}

func TestNewUpdateModuleImagePatch_RejectsInvalidImage(t *testing.T) {
	if _, err := NewUpdateModuleImagePatch("temp-sensor", docker.Image{}); err == nil {
		t.Error("NewUpdateModuleImagePatch: want error for empty image Name, got nil")
	}
}
