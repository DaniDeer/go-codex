package finaldeviceconfig

import (
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
	manifesttemplate "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/manifesttemplate"
)

func sampleBaseManifest() manifesttemplate.DeploymentManifest {
	return manifesttemplate.DeploymentManifest{
		ModulesContent: manifesttemplate.ModulesContent{
			EdgeAgent: manifesttemplate.Modules{
				"factory-mqtt-gateway-1": manifesttemplate.ModuleConfig{
					Settings: manifesttemplate.ModuleSettings{
						Image: docker.Image{Name: "ghcr.io/example-org/factory-gateway", Tag: "0.12.5"},
					},
					Env: manifesttemplate.EnvVars{
						"BROKER_URL": {Value: manifesttemplate.EnvVarValue{StringValue: strPtr("ToDo: Override value in device layers.")}},
						"TZ":         {Value: manifesttemplate.EnvVarValue{StringValue: strPtr("Europe/Berlin")}},
					},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "on-failure",
					Version:       "1.0",
				},
				"factory-cache": manifesttemplate.ModuleConfig{
					Settings: manifesttemplate.ModuleSettings{
						Image: docker.Image{Name: "apache/kvrocks", Tag: "2.15.0"},
					},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
			},
		},
	}
}

func strPtr(s string) *string { return &s }

func TestMerge_DeepEnvVarOverride_LeavesSiblingsUntouched(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.env.BROKER_URL": map[string]any{"value": "mqtts://broker.example.com:8883"},
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent["factory-mqtt-gateway-1"]
	if gw.Env["BROKER_URL"].Value.StringValue == nil || *gw.Env["BROKER_URL"].Value.StringValue != "mqtts://broker.example.com:8883" {
		t.Errorf("BROKER_URL = %+v, want mqtts://broker.example.com:8883", gw.Env["BROKER_URL"])
	}
	// Sibling env var must survive untouched.
	if gw.Env["TZ"].Value.StringValue == nil || *gw.Env["TZ"].Value.StringValue != "Europe/Berlin" {
		t.Errorf("TZ = %+v, want unchanged Europe/Berlin", gw.Env["TZ"])
	}
	// Other module fields on the same module must survive untouched.
	if gw.Status != "running" {
		t.Errorf("Status = %v, want unchanged running", gw.Status)
	}
	if gw.Settings.Image.String() != "ghcr.io/example-org/factory-gateway:0.12.5" {
		t.Errorf("Image = %v, want unchanged", gw.Settings.Image)
	}
	// A different module must be completely untouched.
	if got.ModulesContent.EdgeAgent["factory-cache"].Status != "running" {
		t.Error("factory-cache must be untouched by a patch targeting factory-mqtt-gateway-1")
	}
}

func TestMerge_DeepSettingsFieldOverride(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-cache.settings.createOptions": "{\"HostConfig\": {\"Binds\": [\"new-volume:/data\"]}}",
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	binds := got.ModulesContent.EdgeAgent["factory-cache"].Settings.CreateOptions.HostConfig.Binds
	if len(binds) != 1 || binds[0].HostPath != "new-volume" || binds[0].ContainerPath != "/data" {
		t.Errorf("CreateOptions.Binds = %+v, want one Bind{HostPath: new-volume, ContainerPath: /data}", binds)
	}
	// Image must survive untouched — only settings.createOptions was patched.
	if got.ModulesContent.EdgeAgent["factory-cache"].Settings.Image.String() != "apache/kvrocks:2.15.0" {
		t.Errorf("Image = %v, want unchanged apache/kvrocks:2.15.0", got.ModulesContent.EdgeAgent["factory-cache"].Settings.Image)
	}
}

func TestMerge_WholeModuleOverride_MergesRatherThanReplaces(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1": map[string]any{"status": "stopped"},
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent["factory-mqtt-gateway-1"]
	if gw.Status != "stopped" {
		t.Errorf("Status = %v, want stopped", gw.Status)
	}
	// Deep-merge semantics: other fields on the SAME module survive,
	// unlike a wholesale replace which would wipe everything else.
	if gw.RestartPolicy != "on-failure" {
		t.Errorf("RestartPolicy = %v, want unchanged on-failure", gw.RestartPolicy)
	}
	if gw.Settings.Image.String() != "ghcr.io/example-org/factory-gateway:0.12.5" {
		t.Errorf("Image = %v, want unchanged", gw.Settings.Image)
	}
}

func TestMerge_IntroducesNewModule(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-edge-agent-extra": map[string]any{
				"settings":      map[string]any{"image": "ghcr.io/example-org/extra:1.0.0"},
				"type":          "docker",
				"status":        "running",
				"restartPolicy": "always",
				"version":       "1.0",
			},
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	extra, ok := got.ModulesContent.EdgeAgent["factory-edge-agent-extra"]
	if !ok {
		t.Fatal("Merge: expected a brand-new module to be introduced")
	}
	if extra.Settings.Image.String() != "ghcr.io/example-org/extra:1.0.0" {
		t.Errorf("new module Image = %v, want ghcr.io/example-org/extra:1.0.0", extra.Settings.Image)
	}
}

func TestMerge_PatchesModuleType(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-cache.type": "docker",
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-cache"].Type != "docker" {
		t.Errorf("Type = %v, want docker", got.ModulesContent.EdgeAgent["factory-cache"].Type)
	}
}

func TestMerge_PatchesRestartPolicy(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-cache.restartPolicy": "on-failure",
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	cache := got.ModulesContent.EdgeAgent["factory-cache"]
	if cache.RestartPolicy != "on-failure" {
		t.Errorf("RestartPolicy = %v, want on-failure", cache.RestartPolicy)
	}
	// Sibling field on the same module must survive untouched.
	if cache.Status != "running" {
		t.Errorf("Status = %v, want unchanged running", cache.Status)
	}
}

func TestMerge_PatchesVersion(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-cache.version": "2.0",
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent["factory-cache"].Version != "2.0" {
		t.Errorf("Version = %v, want 2.0", got.ModulesContent.EdgeAgent["factory-cache"].Version)
	}
}

func TestMerge_WholeEnvMapPatch_MergesKeysRatherThanReplacing(t *testing.T) {
	base := sampleBaseManifest()
	// Patching "env" itself (not "env.KEY") with a map STILL deep-merges
	// key-by-key against the base's existing env map — deepMerge treats
	// EVERY map level the same way, all the way down; there is no level
	// at which a map value gets wholesale-replaced by another map. This
	// is the SAME "overwrite/add only, no wholesale replace" behavior
	// documented for the manifest as a whole, just demonstrated one
	// level up from a single env key.
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.env": map[string]any{
				"ONLY_VAR": map[string]any{"value": "solo"},
			},
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent["factory-mqtt-gateway-1"]
	if gw.Env["ONLY_VAR"].Value.StringValue == nil || *gw.Env["ONLY_VAR"].Value.StringValue != "solo" {
		t.Errorf("ONLY_VAR = %+v, want solo", gw.Env["ONLY_VAR"])
	}
	// The base's existing env keys survive — merged in, not replaced.
	if gw.Env["BROKER_URL"].Value.StringValue == nil {
		t.Error("BROKER_URL must survive: patching \"env\" merges keys, it does not replace the whole map")
	}
	if gw.Env["TZ"].Value.StringValue == nil {
		t.Error("TZ must survive: patching \"env\" merges keys, it does not replace the whole map")
	}
}

func TestMerge_AddsNewEdgeHubRoute_WhenTemplateHasNone(t *testing.T) {
	base := sampleBaseManifest() // no $edgeHub declared at all
	patch := deviceconfig.Patch{
		EdgeHub: map[manifesttemplate.RouteName]manifesttemplate.Route{
			"factory-mqtt-to-ingest": {
				From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
				To:   manifesttemplate.NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
			},
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	route, ok := got.ModulesContent.EdgeHub["factory-mqtt-to-ingest"]
	if !ok {
		t.Fatal("Merge: expected the new route to be added")
	}
	if route.From != "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry" {
		t.Errorf("route.From = %q, want /messages/modules/factory-mqtt-gateway-1/outputs/telemetry", route.From)
	}
}

func TestMerge_OverridesExistingEdgeHubRoute(t *testing.T) {
	base := sampleBaseManifest()
	base.ModulesContent.EdgeHub = manifesttemplate.Routes{
		"factory-mqtt-to-ingest": {
			From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
			To:   manifesttemplate.NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
		},
	}
	patch := deviceconfig.Patch{
		EdgeHub: map[manifesttemplate.RouteName]manifesttemplate.Route{
			"factory-mqtt-to-ingest": {From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry", To: manifesttemplate.UpstreamTarget},
		},
	}

	got, err := Merge(base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	route := got.ModulesContent.EdgeHub["factory-mqtt-to-ingest"]
	if route.To.Kind != manifesttemplate.RouteTargetUpstream {
		t.Errorf("route.To.Kind = %v, want RouteTargetUpstream (overridden)", route.To.Kind)
	}
}

func TestMerge_PropagatesDecodeValidationErrors(t *testing.T) {
	base := sampleBaseManifest()
	// An invalid module-name-shaped key (empty suffix after the module
	// name) makes the merged module raw shape invalid — status "bogus"
	// is not a valid manifesttemplate.Status enum value.
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.status": "bogus-status-value",
		},
	}

	_, err := Merge(base, patch)
	if err == nil {
		t.Error("Merge: want error for invalid merged status value, got nil")
	}
}
