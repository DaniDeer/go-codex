package iothub_test

import (
	"encoding/json"
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	f "github.com/DaniDeer/go-codex/format"
)

// sanitizedBaselineJSON mirrors the SHAPE of a real Azure IoT Edge base
// deployment manifest (schemaVersion, runtime.registryCredentials,
// systemModules, modules, edgeHub routes/storeAndForwardConfiguration)
// with entirely GENERIC organization/registry/subscription names and no
// real secrets — deliberately NOT the checked-out-of-repo
// examples/go-edge-models/examples/baseline/baseline.json reference file
// (see this package's own README.md/doc.go for why that stays out of
// the repo).
const sanitizedBaselineJSON = `{
	"modulesContent": {
		"$edgeAgent": {
			"properties.desired": {
				"modules": {
					"vulnerability-scanner": {
						"settings": {
							"image": "ghcr.io/example-org/edge-security-scanner:0.0.2",
							"createOptions": ""
						},
						"type": "docker",
						"version": "auto",
						"env": {
							"SEVERITY": {"value": "CRITICAL,HIGH"}
						},
						"status": "running",
						"restartPolicy": "always"
					},
					"baseline-metrics": {
						"settings": {
							"image": "mcr.microsoft.com/azureiotedge-metrics-collector:1.3.0",
							"createOptions": ""
						},
						"type": "docker",
						"version": "auto",
						"env": {
							"UploadTarget": {"value": "AzureMonitor"}
						},
						"status": "running",
						"restartPolicy": "always"
					}
				},
				"runtime": {
					"settings": {
						"minDockerVersion": "v1.25",
						"registryCredentials": {
							"exampleRegistryAlias": {
								"address": "ghcr.io",
								"username": "user",
								"password": "${EXAMPLE_REGISTRY_PAT}"
							}
						}
					},
					"type": "docker"
				},
				"schemaVersion": "1.1",
				"systemModules": {
					"edgeAgent": {
						"settings": {
							"image": "mcr.microsoft.com/azureiotedge-agent:1.5.31",
							"createOptions": ""
						},
						"type": "docker",
						"env": {
							"storageFolder": {"value": "/storage"}
						}
					},
					"edgeHub": {
						"settings": {
							"image": "mcr.microsoft.com/azureiotedge-hub:1.5.31",
							"createOptions": ""
						},
						"type": "docker",
						"env": {
							"storageFolder": {"value": "/storage"}
						},
						"status": "running",
						"restartPolicy": "always"
					}
				}
			}
		},
		"$edgeHub": {
			"properties.desired": {
				"routes": {},
				"schemaVersion": "1.1",
				"storeAndForwardConfiguration": {"timeToLiveSecs": 259200}
			}
		}
	}
}`

func decodeSanitizedBaseline(t *testing.T) iothub.BaseDeployment {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(sanitizedBaselineJSON), &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	m, err := iothub.BaseDeploymentCodec.Decode(raw)
	if err != nil {
		t.Fatalf("ManifestCodec.Decode: %v", err)
	}
	return m
}

func TestBaseDeploymentCodec_Decode_SchemaVersionAndRuntime(t *testing.T) {
	m := decodeSanitizedBaseline(t)
	props := m.ModulesContent.EdgeAgent
	if props.SchemaVersion != "1.1" {
		t.Errorf("SchemaVersion = %q, want 1.1", props.SchemaVersion)
	}
	if props.Runtime.Settings.MinDockerVersion != "v1.25" {
		t.Errorf("Runtime.Settings.MinDockerVersion = %q, want v1.25", props.Runtime.Settings.MinDockerVersion)
	}
	cred, ok := props.Runtime.Settings.RegistryCredentials["exampleRegistryAlias"]
	if !ok {
		t.Fatalf("RegistryCredentials missing \"exampleRegistryAlias\": %+v", props.Runtime.Settings.RegistryCredentials)
	}
	if cred.Address != "ghcr.io" {
		t.Errorf("RegistryCredential.Address = %q, want ghcr.io", cred.Address)
	}
}

func TestBaseDeploymentCodec_Decode_SystemModulesOptionality(t *testing.T) {
	sm := decodeSanitizedBaseline(t).ModulesContent.EdgeAgent.SystemModules
	if sm.EdgeAgent.Status != "" || sm.EdgeAgent.RestartPolicy != "" {
		t.Errorf("edgeAgent Status/RestartPolicy = %q/%q, want both empty (genuinely absent on the wire)",
			sm.EdgeAgent.Status, sm.EdgeAgent.RestartPolicy)
	}
	if sm.EdgeHub.Status != "running" || sm.EdgeHub.RestartPolicy != "always" {
		t.Errorf("edgeHub Status/RestartPolicy = %q/%q, want running/always", sm.EdgeHub.Status, sm.EdgeHub.RestartPolicy)
	}
}

func TestBaseDeploymentCodec_Decode_Modules(t *testing.T) {
	modules := decodeSanitizedBaseline(t).ModulesContent.EdgeAgent.Modules
	if len(modules) != 2 {
		t.Fatalf("len(Modules) = %d, want 2", len(modules))
	}
	if _, ok := modules["vulnerability-scanner"]; !ok {
		t.Error("Modules missing \"vulnerability-scanner\"")
	}
	if _, ok := modules["baseline-metrics"]; !ok {
		t.Error("Modules missing \"baseline-metrics\"")
	}
}

func TestBaseDeploymentCodec_Decode_EdgeHubStoreAndForward(t *testing.T) {
	edgeHub := decodeSanitizedBaseline(t).ModulesContent.EdgeHub
	if edgeHub.SchemaVersion != "1.1" {
		t.Errorf("EdgeHub.SchemaVersion = %q, want 1.1", edgeHub.SchemaVersion)
	}
	if edgeHub.StoreAndForwardConfiguration.TimeToLiveSecs != 259200 {
		t.Errorf("StoreAndForwardConfiguration.TimeToLiveSecs = %d, want 259200", edgeHub.StoreAndForwardConfiguration.TimeToLiveSecs)
	}
	if len(edgeHub.Routes) != 0 {
		t.Errorf("len(Routes) = %d, want 0 (empty routes object)", len(edgeHub.Routes))
	}
}

func TestBaseDeploymentCodec_RoundTrip(t *testing.T) {
	m := decodeSanitizedBaseline(t)
	jsonFmt := f.JSON(iothub.BaseDeploymentCodec)
	enc, err := jsonFmt.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	dec, err := jsonFmt.Unmarshal(enc)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if dec.ModulesContent.EdgeAgent.Runtime.Settings.MinDockerVersion != "v1.25" {
		t.Errorf("round trip lost Runtime.Settings.MinDockerVersion: %+v", dec)
	}
	if len(dec.ModulesContent.EdgeAgent.Modules) != 2 {
		t.Errorf("round trip lost Modules: %+v", dec.ModulesContent.EdgeAgent.Modules)
	}
	if dec.ModulesContent.EdgeAgent.SystemModules.EdgeAgent.Status != "" {
		t.Errorf("round trip introduced a Status for edgeAgent: %+v", dec.ModulesContent.EdgeAgent.SystemModules.EdgeAgent)
	}
}

func TestBaseDeploymentCodec_DecodeError_NonObject(t *testing.T) {
	if _, err := iothub.BaseDeploymentCodec.Decode("not an object"); err == nil {
		t.Error("Decode: want error for non-object input, got nil")
	}
}
