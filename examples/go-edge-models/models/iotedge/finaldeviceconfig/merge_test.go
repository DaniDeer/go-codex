package finaldeviceconfig

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	deviceconfig "github.com/DaniDeer/go-codex/examples/go-edge-models/models/iotedge/deviceconfig"
)

// sampleBaseline returns a minimal, valid iothub.BaseDeployment with ZERO
// regular modules/routes of its own — every test in this file asserts
// on modules the sample TEMPLATE (sampleBaseManifest) declares, so the
// baseline layer here only needs to satisfy ManifestCodec's required
// fields (schemaVersion/runtime/systemModules), not contribute any
// modules/routes of its own.
func sampleBaseline() iothub.BaseDeployment {
	return iothub.BaseDeployment{
		ModulesContent: iothub.BaseModulesContent{
			EdgeAgent: iothub.EdgeAgentProperties{
				SchemaVersion: "1.1",
				Runtime: iothub.Runtime{
					Settings: iothub.RuntimeSettings{MinDockerVersion: "v1.25"},
					Type:     "docker",
				},
				SystemModules: iothub.SystemModules{
					EdgeAgent: iothub.SystemModuleConfig{
						Settings: iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.5.31"}},
						Type:     "docker",
					},
					EdgeHub: iothub.SystemModuleConfig{
						Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-hub", Tag: "1.5.31"}},
						Type:          "docker",
						Status:        "running",
						RestartPolicy: "always",
					},
				},
			},
			EdgeHub: iothub.EdgeHubProperties{
				SchemaVersion:                "1.1",
				StoreAndForwardConfiguration: iothub.StoreAndForwardConfiguration{TimeToLiveSecs: 259200},
			},
		},
	}
}

func sampleBaseManifest() iothub.LayeredDeployment {
	return iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			EdgeAgent: iothub.Modules{
				"factory-mqtt-gateway-1": iothub.ModuleConfig{
					Settings: iothub.ModuleSettings{
						Image: docker.Image{Name: "ghcr.io/example-org/factory-gateway", Tag: "0.12.5"},
					},
					Env: iothub.EnvVars{
						"BROKER_URL": {Value: iothub.EnvVarValue{StringValue: strPtr("ToDo: Override value in device layers.")}},
						"TZ":         {Value: iothub.EnvVarValue{StringValue: strPtr("Europe/Berlin")}},
					},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "on-failure",
					Version:       "1.0",
				},
				"factory-cache": iothub.ModuleConfig{
					Settings: iothub.ModuleSettings{
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

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent.Modules["factory-mqtt-gateway-1"]
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
	if got.ModulesContent.EdgeAgent.Modules["factory-cache"].Status != "running" {
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

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	binds := got.ModulesContent.EdgeAgent.Modules["factory-cache"].Settings.CreateOptions.HostConfig.Binds
	if len(binds) != 1 || binds[0].HostPath != "new-volume" || binds[0].ContainerPath != "/data" {
		t.Errorf("CreateOptions.Binds = %+v, want one Bind{HostPath: new-volume, ContainerPath: /data}", binds)
	}
	// Image must survive untouched — only settings.createOptions was patched.
	if got.ModulesContent.EdgeAgent.Modules["factory-cache"].Settings.Image.String() != "apache/kvrocks:2.15.0" {
		t.Errorf("Image = %v, want unchanged apache/kvrocks:2.15.0", got.ModulesContent.EdgeAgent.Modules["factory-cache"].Settings.Image)
	}
}

func TestMerge_WholeModuleOverride_MergesRatherThanReplaces(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1": map[string]any{"status": "stopped"},
		},
	}

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent.Modules["factory-mqtt-gateway-1"]
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

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	extra, ok := got.ModulesContent.EdgeAgent.Modules["factory-edge-agent-extra"]
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

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent.Modules["factory-cache"].Type != "docker" {
		t.Errorf("Type = %v, want docker", got.ModulesContent.EdgeAgent.Modules["factory-cache"].Type)
	}
}

func TestMerge_PatchesRestartPolicy(t *testing.T) {
	base := sampleBaseManifest()
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-cache.restartPolicy": "on-failure",
		},
	}

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	cache := got.ModulesContent.EdgeAgent.Modules["factory-cache"]
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

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent.Modules["factory-cache"].Version != "2.0" {
		t.Errorf("Version = %v, want 2.0", got.ModulesContent.EdgeAgent.Modules["factory-cache"].Version)
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

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	gw := got.ModulesContent.EdgeAgent.Modules["factory-mqtt-gateway-1"]
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
		EdgeHub: map[iothub.RouteName]iothub.Route{
			"factory-mqtt-to-ingest": {
				From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
				To:   iothub.NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
			},
		},
	}

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	route, ok := got.ModulesContent.EdgeHub.Routes["factory-mqtt-to-ingest"]
	if !ok {
		t.Fatal("Merge: expected the new route to be added")
	}
	if route.From != "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry" {
		t.Errorf("route.From = %q, want /messages/modules/factory-mqtt-gateway-1/outputs/telemetry", route.From)
	}
}

func TestMerge_OverridesExistingEdgeHubRoute(t *testing.T) {
	base := sampleBaseManifest()
	base.ModulesContent.EdgeHub = iothub.Routes{
		"factory-mqtt-to-ingest": {
			From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry",
			To:   iothub.NewBrokeredEndpoint("/modules/factory-ingest-agent/inputs/ingest"),
		},
	}
	patch := deviceconfig.Patch{
		EdgeHub: map[iothub.RouteName]iothub.Route{
			"factory-mqtt-to-ingest": {From: "/messages/modules/factory-mqtt-gateway-1/outputs/telemetry", To: iothub.UpstreamTarget},
		},
	}

	bl := sampleBaseline()
	got, err := Merge(bl, base, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	route := got.ModulesContent.EdgeHub.Routes["factory-mqtt-to-ingest"]
	if route.To.Kind != iothub.RouteTargetUpstream {
		t.Errorf("route.To.Kind = %v, want RouteTargetUpstream (overridden)", route.To.Kind)
	}
}

func TestMerge_BaselineModule_SurvivesUntouched(t *testing.T) {
	bl := sampleBaseline()
	bl.ModulesContent.EdgeAgent.Modules = iothub.Modules{
		"vulnerability-scanner": {
			Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/edge-security-scanner", Tag: "0.0.2"}},
			Type:          "docker",
			Status:        "running",
			RestartPolicy: "always",
			Version:       "auto",
		},
	}
	tmpl := sampleBaseManifest() // declares only factory-mqtt-gateway-1/factory-cache, no vulnerability-scanner
	got, err := Merge(bl, tmpl, deviceconfig.Patch{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if _, ok := got.ModulesContent.EdgeAgent.Modules["vulnerability-scanner"]; !ok {
		t.Error("Merge: baseline-only module must survive into the final manifest")
	}
	if _, ok := got.ModulesContent.EdgeAgent.Modules["factory-mqtt-gateway-1"]; !ok {
		t.Error("Merge: template module must ALSO survive alongside the baseline module")
	}
}

func TestMerge_TemplateOverridesBaselineModule_ByName(t *testing.T) {
	bl := sampleBaseline()
	bl.ModulesContent.EdgeAgent.Modules = iothub.Modules{
		"shared-module": {
			Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/shared", Tag: "0.0.1"}},
			Type:          "docker",
			Status:        "running",
			RestartPolicy: "always",
			Version:       "0.0.1",
		},
	}
	tmpl := iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			EdgeAgent: iothub.Modules{
				"shared-module": {
					Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/shared", Tag: "1.0.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0.0",
				},
			},
		},
	}
	got, err := Merge(bl, tmpl, deviceconfig.Patch{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent.Modules["shared-module"].Settings.Image.String() != "ghcr.io/example-org/shared:1.0.0" {
		t.Errorf("shared-module Image = %v, want the TEMPLATE's override (1.0.0), not baseline's (0.0.1)",
			got.ModulesContent.EdgeAgent.Modules["shared-module"].Settings.Image)
	}
}

func TestMerge_SystemModule_DevicePatch_ReachesBaselineDefault(t *testing.T) {
	bl := sampleBaseline()
	tmpl := iothub.LayeredDeployment{}
	patch := deviceconfig.Patch{
		SystemModules: map[string]any{
			"edgeAgent.settings.image": "mcr.microsoft.com/azureiotedge-agent:1.5.99",
		},
	}
	got, err := Merge(bl, tmpl, patch)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Settings.Image.String() != "mcr.microsoft.com/azureiotedge-agent:1.5.99" {
		t.Errorf("SystemModules.EdgeAgent.Settings.Image = %v, want patched 1.5.99",
			got.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Settings.Image)
	}
	// edgeHub (untouched by the patch) must survive from baseline unchanged.
	if got.ModulesContent.EdgeAgent.SystemModules.EdgeHub.Status != "running" {
		t.Errorf("SystemModules.EdgeHub.Status = %q, want unchanged \"running\" from baseline",
			got.ModulesContent.EdgeAgent.SystemModules.EdgeHub.Status)
	}
}

func TestMerge_SystemModule_TemplateWholesaleOverride(t *testing.T) {
	bl := sampleBaseline()
	tmpl := iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			SystemModules: map[iothub.SystemModuleName]iothub.SystemModuleConfig{
				"edgeAgent": {
					Settings: iothub.ModuleSettings{Image: docker.Image{Name: "mcr.microsoft.com/azureiotedge-agent", Tag: "1.6.0"}},
					Type:     "docker",
				},
			},
		},
	}
	got, err := Merge(bl, tmpl, deviceconfig.Patch{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Settings.Image.String() != "mcr.microsoft.com/azureiotedge-agent:1.6.0" {
		t.Errorf("SystemModules.EdgeAgent.Settings.Image = %v, want template's override 1.6.0",
			got.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Settings.Image)
	}
}

func TestMerge_BaselineOnlyFields_PassThroughUnchanged(t *testing.T) {
	bl := sampleBaseline()
	got, err := Merge(bl, iothub.LayeredDeployment{}, deviceconfig.Patch{})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.ModulesContent.EdgeAgent.SchemaVersion != "1.1" {
		t.Errorf("SchemaVersion = %q, want 1.1 (baseline-only, unchanged)", got.ModulesContent.EdgeAgent.SchemaVersion)
	}
	if got.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion != "v1.25" {
		t.Errorf("Runtime.Settings.MinDockerVersion = %q, want v1.25 (baseline-only, unchanged)",
			got.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion)
	}
	if got.ModulesContent.EdgeHub.StoreAndForwardConfiguration.TimeToLiveSecs != 259200 {
		t.Errorf("StoreAndForwardConfiguration.TimeToLiveSecs = %d, want 259200 (baseline-only, unchanged)",
			got.ModulesContent.EdgeHub.StoreAndForwardConfiguration.TimeToLiveSecs)
	}
}

func TestMerge_PropagatesDecodeValidationErrors(t *testing.T) {
	base := sampleBaseManifest()
	// An invalid module-name-shaped key (empty suffix after the module
	// name) makes the merged module raw shape invalid — status "bogus"
	// is not a valid iothub.Status enum value.
	patch := deviceconfig.Patch{
		EdgeAgent: map[string]any{
			"factory-mqtt-gateway-1.status": "bogus-status-value",
		},
	}

	bl := sampleBaseline()
	_, err := Merge(bl, base, patch)
	if err == nil {
		t.Error("Merge: want error for invalid merged status value, got nil")
	}
}
