package modulepatch

import (
	"errors"
	"reflect"
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestFieldsPatchCodec_EncodeImageOnly(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	p := FieldsPatch{ModuleName: "temp-sensor", Settings: &SettingsPatch{Image: &img}}

	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iothub.ModuleKeyPrefix+"temp-sensor"].(map[string]any)

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

func TestFieldsPatchCodec_EncodeStatusOnly(t *testing.T) {
	status := iothub.Status("running")
	p := FieldsPatch{ModuleName: "temp-sensor", Status: &status}

	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iothub.ModuleKeyPrefix+"temp-sensor"].(map[string]any)

	if _, ok := module["settings"]; ok {
		t.Error("settings key should be ABSENT when Settings is nil")
	}
	if module["status"] != "running" {
		t.Errorf("status = %v, want \"running\"", module["status"])
	}
}

func TestFieldsPatchCodec_EncodeMultipleFields(t *testing.T) {
	status := iothub.Status("running")
	rp := iothub.RestartPolicy("always")
	p := FieldsPatch{ModuleName: "temp-sensor", Status: &status, RestartPolicy: &rp}

	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iothub.ModuleKeyPrefix+"temp-sensor"].(map[string]any)

	if module["status"] != "running" || module["restartPolicy"] != "always" {
		t.Errorf("module = %+v, want status=running, restartPolicy=always", module)
	}
	if len(module) != 2 {
		t.Errorf("module has %d keys, want exactly 2 (status, restartPolicy)", len(module))
	}
}

func TestFieldsPatchCodec_EncodeEnv(t *testing.T) {
	env := iothub.EnvVars{"FOO": {Value: iothub.NewEnvVarValueString("bar")}}
	p := FieldsPatch{ModuleName: "temp-sensor", Env: &env}

	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iothub.ModuleKeyPrefix+"temp-sensor"].(map[string]any)
	if _, ok := module["env"]; !ok {
		t.Error("expected env key to be present")
	}
}

func TestFieldsPatchCodec_EncodeEmptyPatch_ReturnsError(t *testing.T) {
	p := FieldsPatch{ModuleName: "temp-sensor"}
	_, err := FieldsPatchCodec.Encode(p)
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

func TestFieldsPatchCodec_EncodeEmptySettings_TreatedAsAbsent(t *testing.T) {
	// A non-nil *SettingsPatch with nothing inside it still encodes
	// to a present (but empty) "settings" object — the outer
	// FieldsPatch's own EmptyPatchError check only looks at whether
	// ITS OWN fields (settings/env/type/status/restartPolicy/version)
	// produced any keys at all; "settings" itself being non-nil DOES
	// count as a set field, even though it happens to have nothing inside.
	// This documents actual behavior, not a bug: a caller allocating
	// &SettingsPatch{} with nothing set is a caller mistake, same
	// category as any other no-op patch.
	p := FieldsPatch{ModuleName: "temp-sensor", Settings: &SettingsPatch{}}
	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj := raw.(map[string]any)
	modulesContent := obj["modulesContent"].(map[string]any)
	edgeAgent := modulesContent["$edgeAgent"].(map[string]any)
	module := edgeAgent[iothub.ModuleKeyPrefix+"temp-sensor"].(map[string]any)
	settings, ok := module["settings"].(map[string]any)
	if !ok {
		t.Fatal("expected settings key to be present (Settings pointer was non-nil)")
	}
	if len(settings) != 0 {
		t.Errorf("settings = %+v, want empty (nothing set inside)", settings)
	}
}

func TestFieldsPatchCodec_EncodeRejectsInvalidImage(t *testing.T) {
	img := docker.Image{} // empty Name — invalid
	p := FieldsPatch{ModuleName: "temp-sensor", Settings: &SettingsPatch{Image: &img}}
	if _, err := FieldsPatchCodec.Encode(p); err == nil {
		t.Error("Encode: want error for invalid Image, got nil")
	}
}

func TestFieldsPatchCodec_DecodeRoundTrip(t *testing.T) {
	img := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	status := iothub.Status("running")
	rp := iothub.RestartPolicy("always")
	p := FieldsPatch{
		ModuleName:    "temp-sensor",
		Settings:      &SettingsPatch{Image: &img},
		Status:        &status,
		RestartPolicy: &rp,
	}

	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := FieldsPatchCodec.Decode(raw)
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

func TestFieldsPatchCodec_DecodeCreateOptionsAndEnv(t *testing.T) {
	co := docker.CreateOptions{Hostname: "sensor-host"}
	env := iothub.EnvVars{"FOO": {Value: iothub.NewEnvVarValueString("bar")}}
	p := FieldsPatch{
		ModuleName: "temp-sensor",
		Settings:   &SettingsPatch{CreateOptions: &co},
		Env:        &env,
	}

	raw, err := FieldsPatchCodec.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := FieldsPatchCodec.Decode(raw)
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

// TestSettingsPatchCodec_CreateOptions_EncodesAsJSONStringNotRawObject is
// a regression test: SettingsPatchCodec's "createOptions" field must use
// iothub.CreateOptionsFieldCodec (JSON-escaped STRING wire
// shape), not docker.CreateOptionsCodec directly (raw object) — a
// mismatch that previously round-tripped fine WITHIN this package's own
// codec (self-consistent) but broke the moment the resulting patch was
// merged into a real iothub.DeploymentManifest and decoded via
// iothub.DeploymentManifestCodec (which expects "createOptions"
// as a string, per its own wire format).
func TestSettingsPatchCodec_CreateOptions_EncodesAsJSONStringNotRawObject(t *testing.T) {
	co := docker.CreateOptions{Hostname: "sensor-host"}
	sp := SettingsPatch{CreateOptions: &co}

	raw, err := SettingsPatchCodec.Encode(sp)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("Encode result = %T, want map[string]any", raw)
	}
	if _, ok := obj["createOptions"].(string); !ok {
		t.Errorf("createOptions = %T (%v), want a JSON-escaped STRING", obj["createOptions"], obj["createOptions"])
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

func TestNewUpdateModuleImage_ReturnsValueOnSuccess(t *testing.T) {
	image := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	got, err := NewUpdateModuleImage("temp-sensor", image)
	if err != nil {
		t.Fatalf("NewUpdateModuleImage: unexpected error: %v", err)
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

func TestNewUpdateModuleImage_RejectsInvalidModuleName(t *testing.T) {
	image := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	if _, err := NewUpdateModuleImage("Not A Valid Slug", image); err == nil {
		t.Error("NewUpdateModuleImage: want error for invalid module name, got nil")
	}
}

func TestNewUpdateModuleImage_RejectsInvalidImage(t *testing.T) {
	if _, err := NewUpdateModuleImage("temp-sensor", docker.Image{}); err == nil {
		t.Error("NewUpdateModuleImage: want error for empty image Name, got nil")
	}
}

func TestNewUpdateModuleImageFromBase_SeedsEveryFieldFromBase(t *testing.T) {
	base := iothub.ModuleConfig{
		Settings: iothub.ModuleSettings{
			Image:         docker.Image{Name: "ghcr.io/org/repo", Tag: "1.0.0"},
			CreateOptions: docker.CreateOptions{Hostname: "sensor-host"},
		},
		Env:           iothub.EnvVars{"FOO": {Value: iothub.NewEnvVarValueString("bar")}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "1.0.0",
	}
	newImage := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}

	got, err := NewUpdateModuleImageFromBase("temp-sensor", base, newImage)
	if err != nil {
		t.Fatalf("NewUpdateModuleImageFromBase: %v", err)
	}
	if got.ModuleName != "temp-sensor" {
		t.Errorf("ModuleName = %q, want temp-sensor", got.ModuleName)
	}
	if got.Settings == nil || got.Settings.Image == nil || *got.Settings.Image != newImage {
		t.Errorf("Settings.Image = %v, want %v (new image)", got.Settings, newImage)
	}
	if got.Settings.CreateOptions == nil || got.Settings.CreateOptions.Hostname != "sensor-host" {
		t.Errorf("Settings.CreateOptions = %v, want Hostname=sensor-host (seeded from base)", got.Settings.CreateOptions)
	}
	if got.Type == nil || *got.Type != "docker" {
		t.Errorf("Type = %v, want docker (seeded from base)", got.Type)
	}
	if got.Status == nil || *got.Status != "running" {
		t.Errorf("Status = %v, want running (seeded from base)", got.Status)
	}
	if got.RestartPolicy == nil || *got.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %v, want always (seeded from base)", got.RestartPolicy)
	}
	if got.Version == nil || *got.Version != "1.0.0" {
		t.Errorf("Version = %v, want 1.0.0 (seeded from base)", got.Version)
	}
	if got.Env == nil || (*got.Env)["FOO"].Value.StringValue == nil || *(*got.Env)["FOO"].Value.StringValue != "bar" {
		t.Errorf("Env = %v, want FOO=bar (seeded from base)", got.Env)
	}

	// The resulting patch must encode to a COMPLETE, standalone-decodable
	// ModuleConfig (every required field present).
	raw, err := FieldsBodyCodec.Encode(got)
	if err != nil {
		t.Fatalf("FieldsBodyCodec.Encode: %v", err)
	}
	if _, err := iothub.ModuleConfigCodec.Decode(raw); err != nil {
		t.Errorf("iothub.ModuleConfigCodec.Decode(promoted patch): %v, want a fully valid ModuleConfig", err)
	}
}

func TestNewUpdateModuleImageFromBase_RejectsInvalidImage(t *testing.T) {
	base := iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/org/repo", Tag: "1.0.0"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "1.0.0",
	}
	if _, err := NewUpdateModuleImageFromBase("temp-sensor", base, docker.Image{}); err == nil {
		t.Error("NewUpdateModuleImageFromBase: want error for empty image Name, got nil")
	}
}

// ── FieldsBodyCodec / NonEmptyFieldsPatch (device-level bridge) ─────────────

func TestFieldsBodyCodec_EncodeProducesRawBodyWithoutModuleNameWrapping(t *testing.T) {
	image := docker.Image{Name: "ghcr.io/org/repo", Tag: "2.0.0"}
	patch := FieldsPatch{ModuleName: "temp-sensor", Settings: &SettingsPatch{Image: &image}}

	raw, err := FieldsBodyCodec.Encode(patch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	body, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("Encode = %T, want map[string]any", raw)
	}
	// No "modulesContent"/"$edgeAgent"/module-name-key wrapping — just
	// the bare patchable-fields object, ready to be assigned directly to
	// e.g. deviceconfig.Patch.EdgeAgent[string(patch.ModuleName)].
	if _, hasModulesContent := body["modulesContent"]; hasModulesContent {
		t.Error("FieldsBodyCodec.Encode should NOT include the outer modulesContent wrapping")
	}
	settings, ok := body["settings"].(map[string]any)
	if !ok || settings["image"] != "ghcr.io/org/repo:2.0.0" {
		t.Errorf("body[settings] = %v, want image=ghcr.io/org/repo:2.0.0", body["settings"])
	}
}

func TestFieldsBodyCodec_DecodeRoundTrip(t *testing.T) {
	status := iothub.Status("stopped")
	patch := FieldsPatch{ModuleName: "temp-sensor", Status: &status}

	raw, err := FieldsBodyCodec.Encode(patch)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := FieldsBodyCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// ModuleName is NOT part of the body — Decode never populates it.
	if got.ModuleName != "" {
		t.Errorf("ModuleName = %q, want empty (not part of the body)", got.ModuleName)
	}
	if got.Status == nil || *got.Status != "stopped" {
		t.Errorf("Status = %v, want stopped", got.Status)
	}
}

func TestNonEmptyFieldsPatch_RejectsAllNilFields(t *testing.T) {
	if NonEmptyFieldsPatch.Check(FieldsPatch{ModuleName: "temp-sensor"}) {
		t.Error("NonEmptyFieldsPatch.Check: want false for a patch with every field nil")
	}
}

func TestNonEmptyFieldsPatch_AcceptsOneFieldSet(t *testing.T) {
	status := iothub.Status("stopped")
	if !NonEmptyFieldsPatch.Check(FieldsPatch{ModuleName: "temp-sensor", Status: &status}) {
		t.Error("NonEmptyFieldsPatch.Check: want true when Status is set")
	}
}

func TestNonEmptyFieldsPatch_NotAppliedToFieldsPatchCodec(t *testing.T) {
	// FieldsPatchCodec.Encode must keep returning its own richer
	// EmptyPatchError{ModuleName} for an empty patch, NOT a generic
	// ConstraintError from NonEmptyFieldsPatch — confirms
	// NonEmptyFieldsPatch is NOT wired via .Refine onto FieldsBodyCodec.
	_, err := FieldsPatchCodec.Encode(FieldsPatch{ModuleName: "temp-sensor"})
	var emptyErr EmptyPatchError
	if !errors.As(err, &emptyErr) {
		t.Errorf("Encode error = %v (%T), want EmptyPatchError", err, err)
	}
}
