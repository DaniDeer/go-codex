package fromcompose

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose"
)

func TestConvertService_SanitizesName(t *testing.T) {
	moduleName, _, warnings := ConvertService("Factory_API", dockercompose.Service{Image: "ghcr.io/example-org/factory-api:1.0.0"})
	if moduleName != "factory-api" {
		t.Errorf("moduleName = %q, want %q", moduleName, "factory-api")
	}
	if len(warnings) != 1 || warnings[0].Kind != WarningSanitizedName {
		t.Errorf("warnings = %+v, want exactly one WarningSanitizedName", warnings)
	}
}

func TestConvertService_NoWarningWhenNameAlreadyValid(t *testing.T) {
	_, _, warnings := ConvertService("factory-api", dockercompose.Service{Image: "ghcr.io/example-org/factory-api:1.0.0"})
	for _, w := range warnings {
		if w.Kind == WarningSanitizedName {
			t.Errorf("unexpected WarningSanitizedName for an already-valid name: %+v", w)
		}
	}
}

func TestConvertService_PlaceholderImageForBuildOnly(t *testing.T) {
	moduleName, moduleConfig, warnings := ConvertService("factory-app", dockercompose.Service{Build: dockercompose.Build{Context: "./app"}})
	if moduleName != "factory-app" {
		t.Errorf("moduleName = %q", moduleName)
	}
	if moduleConfig.Settings.Image.Name != "replace-me/factory-app" {
		t.Errorf("image name = %q, want placeholder", moduleConfig.Settings.Image.Name)
	}
	found := false
	for _, w := range warnings {
		if w.Kind == WarningPlaceholderImage {
			found = true
		}
	}
	if !found {
		t.Error("expected a WarningPlaceholderImage")
	}
}

func TestConvertService_RestartPolicyMappingTable(t *testing.T) {
	cases := []struct {
		compose         string
		want            string
		wantApproximate bool
	}{
		{"", "never", false},
		{"no", "never", false},
		{"always", "always", false},
		{"on-failure", "on-failure", false},
		{"on-failure:3", "on-failure", false},
		{"unless-stopped", "always", true},
		{"something-unrecognized", "always", true},
	}
	for _, tc := range cases {
		_, moduleConfig, warnings := ConvertService("svc", dockercompose.Service{
			Image:   "example/img:1.0",
			Restart: tc.compose,
		})
		if string(moduleConfig.RestartPolicy) != tc.want {
			t.Errorf("restart(%q) = %q, want %q", tc.compose, moduleConfig.RestartPolicy, tc.want)
		}
		gotApprox := false
		for _, w := range warnings {
			if w.Kind == WarningRestartPolicyApproximated {
				gotApprox = true
			}
		}
		if gotApprox != tc.wantApproximate {
			t.Errorf("restart(%q): approximated = %v, want %v", tc.compose, gotApprox, tc.wantApproximate)
		}
	}
}

func TestConvertService_EnvVarsAsStringVariant(t *testing.T) {
	_, moduleConfig, _ := ConvertService("svc", dockercompose.Service{
		Image: "example/img:1.0",
		Environment: docker.Env{
			{Name: "TZ", Value: "Europe/Berlin"},
		},
	})
	ev, ok := moduleConfig.Env["TZ"]
	if !ok {
		t.Fatal(`Env["TZ"] missing`)
	}
	if ev.Value.StringValue == nil || *ev.Value.StringValue != "Europe/Berlin" {
		t.Errorf("Env[TZ] = %+v, want StringValue=Europe/Berlin", ev.Value)
	}
}

// NOTE: there is no "malformed port produces a Warning" test here
// anymore — ConvertService takes an already-typed dockercompose.Service
// value, and Service.Ports is now []docker.PortMapping (see
// docker.PortMappingCodec), so a malformed port entry can no longer
// reach ConvertService at all: it fails earlier, at
// dockercompose.ServiceCodec.Decode itself (see
// TestServiceCodec_RejectsMalformedPort in the dockercompose package).

func TestConvertService_TypeStatusVersionDefaults(t *testing.T) {
	_, moduleConfig, _ := ConvertService("svc", dockercompose.Service{Image: "example/img:1.0"})
	if moduleConfig.Type != "docker" {
		t.Errorf("Type = %q, want docker", moduleConfig.Type)
	}
	if moduleConfig.Status != "running" {
		t.Errorf("Status = %q, want running", moduleConfig.Status)
	}
	if moduleConfig.Version != "Will be automatically overwritten" {
		t.Errorf("Version = %q", moduleConfig.Version)
	}
}

// ── ModuleConfigFromServiceCodec: the bidirectional codec itself ────────────

func TestModuleConfigFromServiceCodec_DecodesComposeWireDirectly(t *testing.T) {
	// A raw Compose-service wire value (as format.YAML(dockercompose.ServiceCodec)
	// would produce), decoded DIRECTLY into an iothub.ModuleConfig — no
	// dockercompose.Service ever touched by this test's own code. This is
	// the "Codec[A] wraps the SAME wire shape as Codec[B]" promise in
	// action.
	raw := map[string]any{
		"image":   "ghcr.io/example-org/factory-api:1.8.16",
		"restart": "always",
	}
	mc, err := ModuleConfigFromServiceCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if mc.Settings.Image.String() != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("Image = %q", mc.Settings.Image.String())
	}
	if mc.RestartPolicy != "always" {
		t.Errorf("RestartPolicy = %q", mc.RestartPolicy)
	}
	if mc.Type != "docker" || mc.Status != "running" {
		t.Errorf("Type/Status = %q/%q", mc.Type, mc.Status)
	}
}

func TestModuleConfigFromServiceCodec_EncodesBackToComposeWire(t *testing.T) {
	mc := iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/factory-api", Tag: "1.8.16"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "1.0",
	}
	raw, err := ModuleConfigFromServiceCodec.Encode(mc)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("Encode result type = %T, want map[string]any", raw)
	}
	if obj["image"] != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("image = %v", obj["image"])
	}
	if obj["restart"] != "always" {
		t.Errorf("restart = %v", obj["restart"])
	}
}

func TestModuleConfigFromServiceCodec_ValidatesAutomatically(t *testing.T) {
	// A ModuleConfig with an invalid enum value must fail Validate — this
	// is iothub.ModuleConfigCodec's OWN Refine constraint, enforced by
	// MapCodecValidated with ZERO extra code in this package.
	mc := iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "example/img", Tag: "1.0"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "1.0",
	}
	if err := ModuleConfigFromServiceCodec.Validate(mc); err != nil {
		t.Errorf("Validate(valid mc) = %v, want nil", err)
	}
}

// ── ConvertModuleConfig: the reverse (IoT Edge -> Compose) direction ────────

func TestConvertModuleConfig_ReversesImageAndRestart(t *testing.T) {
	mc := iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/factory-api", Tag: "1.8.16"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "1.0",
	}
	name, svc, warnings := ConvertModuleConfig("factory-api", mc)
	if name != "factory-api" {
		t.Errorf("name = %q, want %q", name, "factory-api")
	}
	if svc.Image != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("Image = %q", svc.Image)
	}
	if svc.Restart != "always" {
		t.Errorf("Restart = %q", svc.Restart)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none (always is an exact reverse mapping)", warnings)
	}
}

func TestConvertModuleConfig_PlaceholderImageReversesToBuild(t *testing.T) {
	mc := iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "replace-me/factory-worker", Tag: "latest"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "never",
		Version:       "1.0",
	}
	_, svc, _ := ConvertModuleConfig("factory-worker", mc)
	if !svc.Build.IsSet() {
		t.Error("Build.IsSet() = false, want true (placeholder image should reverse to a build: block)")
	}
	if svc.Image != "" {
		t.Errorf("Image = %q, want empty", svc.Image)
	}
}

func TestConvertModuleConfig_OnUnhealthyIsApproximated(t *testing.T) {
	mc := iothub.ModuleConfig{
		Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "example/img", Tag: "1.0"}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "on-unhealthy",
		Version:       "1.0",
	}
	_, svc, warnings := ConvertModuleConfig("svc", mc)
	if svc.Restart != "always" {
		t.Errorf("Restart = %q, want always (closest analog)", svc.Restart)
	}
	found := false
	for _, w := range warnings {
		if w.Kind == WarningRestartPolicyApproximated {
			found = true
		}
	}
	if !found {
		t.Error("expected a WarningRestartPolicyApproximated for on-unhealthy")
	}
}

func TestConvertModuleConfig_ExactRestartPoliciesNoWarning(t *testing.T) {
	cases := []iothub.RestartPolicy{"never", "always", "on-failure"}
	for _, policy := range cases {
		mc := iothub.ModuleConfig{
			Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "example/img", Tag: "1.0"}},
			Type:          "docker",
			Status:        "running",
			RestartPolicy: policy,
			Version:       "1.0",
		}
		_, _, warnings := ConvertModuleConfig("svc", mc)
		for _, w := range warnings {
			if w.Kind == WarningRestartPolicyApproximated {
				t.Errorf("policy %q: unexpected WarningRestartPolicyApproximated", policy)
			}
		}
	}
}

func TestConvertModuleConfig_ReconstructsCreateOptionsFields(t *testing.T) {
	mc := iothub.ModuleConfig{
		Settings: iothub.ModuleSettings{
			Image: docker.Image{Name: "example/img", Tag: "1.0"},
			CreateOptions: docker.CreateOptions{
				ExposedPorts: []docker.Port{"80/tcp"},
				HostConfig: docker.HostConfig{
					PortBindings: []docker.PortBinding{
						{Port: "80/tcp", Bindings: []docker.PortBindingEntry{{HostPort: "8080"}}},
					},
					Binds: []docker.Bind{{HostPath: "/data", ContainerPath: "/var/data"}},
				},
			},
		},
		Env:           iothub.EnvVars{"TZ": {Value: iothub.EnvVarValue{StringValue: strPtr("Europe/Berlin")}}},
		Type:          "docker",
		Status:        "running",
		RestartPolicy: "always",
		Version:       "1.0",
	}
	_, svc, _ := ConvertModuleConfig("svc", mc)
	wantPort := docker.PortMapping{Port: "80/tcp", HostPort: "8080"}
	if len(svc.Ports) != 1 || svc.Ports[0] != wantPort {
		t.Errorf("Ports = %+v, want [%+v]", svc.Ports, wantPort)
	}
	if len(svc.Volumes) != 1 || svc.Volumes[0].HostPath != "/data" {
		t.Errorf("Volumes = %+v", svc.Volumes)
	}
	if len(svc.Environment) != 1 || svc.Environment[0].Name != "TZ" {
		t.Errorf("Environment = %+v", svc.Environment)
	}
}

func strPtr(s string) *string { return &s }
