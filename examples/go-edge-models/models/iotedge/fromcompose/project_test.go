package fromcompose

import (
	"testing"

	iothub "github.com/DaniDeer/go-codex/examples/go-edge-models/models/azure/iothub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/dockercompose"
)

func TestConvertProject_AggregatesModules(t *testing.T) {
	project := dockercompose.Project{
		Services: map[dockercompose.ServiceName]dockercompose.Service{
			"factory-api":   {Image: "ghcr.io/example-org/factory-api:1.0.0"},
			"factory-cache": {Image: "apache/kvrocks:2.15.0"},
		},
	}

	deployment, warnings := ConvertProject(project)

	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none for two already-valid, image-declaring services", warnings)
	}
	modules := deployment.ModulesContent.EdgeAgent
	if len(modules) != 2 {
		t.Fatalf("len(modules) = %d, want 2", len(modules))
	}
	if _, ok := modules["factory-api"]; !ok {
		t.Error(`modules["factory-api"] missing`)
	}
	if _, ok := modules["factory-cache"]; !ok {
		t.Error(`modules["factory-cache"] missing`)
	}
}

func TestConvertProject_RoutesAlwaysEmpty(t *testing.T) {
	project := dockercompose.Project{
		Services: map[dockercompose.ServiceName]dockercompose.Service{
			"svc": {Image: "example/img:1.0"},
		},
	}
	deployment, _ := ConvertProject(project)
	if len(deployment.ModulesContent.EdgeHub) != 0 {
		t.Errorf("EdgeHub routes = %+v, want empty", deployment.ModulesContent.EdgeHub)
	}
}

func TestConvertProject_CollectsWarningsAcrossServices(t *testing.T) {
	project := dockercompose.Project{
		Services: map[dockercompose.ServiceName]dockercompose.Service{
			"Factory_App": {Build: dockercompose.Build{Context: "./app"}},
			"factory-db":  {Image: "example/db:1.0"},
		},
	}
	_, warnings := ConvertProject(project)
	if len(warnings) < 2 {
		t.Fatalf("warnings = %+v, want at least 2 (sanitized name + placeholder image for Factory_App)", warnings)
	}
}

func TestConvertProject_EmptyProject(t *testing.T) {
	deployment, warnings := ConvertProject(dockercompose.Project{})
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if len(deployment.ModulesContent.EdgeAgent) != 0 {
		t.Errorf("modules = %+v, want empty", deployment.ModulesContent.EdgeAgent)
	}
}

// ── ConvertDeployment: the reverse (IoT Edge -> Compose) direction ─────────

func TestConvertDeployment_ReversesEachModule(t *testing.T) {
	deployment := iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			EdgeAgent: iothub.Modules{
				"factory-api": iothub.ModuleConfig{
					Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "ghcr.io/example-org/factory-api", Tag: "1.0.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
				"factory-cache": iothub.ModuleConfig{
					Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "apache/kvrocks", Tag: "2.15.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "never",
					Version:       "1.0",
				},
			},
		},
	}

	project, warnings := ConvertDeployment(deployment)
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none (both restart policies are exact)", warnings)
	}
	if len(project.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(project.Services))
	}
	api, ok := project.Services["factory-api"]
	if !ok {
		t.Fatal(`Services["factory-api"] missing`)
	}
	if api.Image != "ghcr.io/example-org/factory-api:1.0.0" {
		t.Errorf("factory-api.Image = %q", api.Image)
	}
	if api.Restart != "always" {
		t.Errorf("factory-api.Restart = %q", api.Restart)
	}
	cache := project.Services["factory-cache"]
	if cache.Restart != "no" {
		t.Errorf("factory-cache.Restart = %q, want %q", cache.Restart, "no")
	}
}

func TestConvertDeployment_RoutesNotRepresented(t *testing.T) {
	deployment := iothub.LayeredDeployment{
		ModulesContent: iothub.LayeredModulesContent{
			EdgeAgent: iothub.Modules{
				"svc": iothub.ModuleConfig{
					Settings:      iothub.ModuleSettings{Image: docker.Image{Name: "example/img", Tag: "1.0"}},
					Type:          "docker",
					Status:        "running",
					RestartPolicy: "always",
					Version:       "1.0",
				},
			},
			EdgeHub: iothub.Routes{
				"some-route": {From: "/messages/modules/svc/outputs/out", To: iothub.UpstreamTarget},
			},
		},
	}
	// Should not panic or error just because routes are present — they
	// are simply not represented in the resulting Project.
	project, _ := ConvertDeployment(deployment)
	if len(project.Services) != 1 {
		t.Errorf("len(Services) = %d, want 1", len(project.Services))
	}
}

func TestConvertDeployment_EmptyDeployment(t *testing.T) {
	project, warnings := ConvertDeployment(iothub.LayeredDeployment{})
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	if len(project.Services) != 0 {
		t.Errorf("Services = %+v, want empty", project.Services)
	}
}

// ── Full round trip: Compose -> IoT Edge -> Compose ──────────────────────

func TestRoundTrip_ComposeToIoTEdgeToCompose(t *testing.T) {
	original := dockercompose.Project{
		Services: map[dockercompose.ServiceName]dockercompose.Service{
			"factory-api": {
				Image:   "ghcr.io/example-org/factory-api:1.8.16",
				Ports:   []docker.PortMapping{{Port: "80/tcp", HostPort: "8080"}},
				Restart: "always",
				Environment: docker.Env{
					{Name: "TZ", Value: "Europe/Berlin"},
				},
			},
		},
	}

	deployment, warnings := ConvertProject(original)
	if len(warnings) != 0 {
		t.Fatalf("ConvertProject warnings = %+v, want none", warnings)
	}

	roundTripped, warnings2 := ConvertDeployment(deployment)
	if len(warnings2) != 0 {
		t.Fatalf("ConvertDeployment warnings = %+v, want none", warnings2)
	}

	svc, ok := roundTripped.Services["factory-api"]
	if !ok {
		t.Fatal(`Services["factory-api"] missing after round trip`)
	}
	if svc.Image != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("Image = %q, want unchanged", svc.Image)
	}
	wantPort := docker.PortMapping{Port: "80/tcp", HostPort: "8080"}
	if len(svc.Ports) != 1 || svc.Ports[0] != wantPort {
		t.Errorf("Ports = %+v, want [%+v]", svc.Ports, wantPort)
	}
	if svc.Restart != "always" {
		t.Errorf("Restart = %q, want unchanged", svc.Restart)
	}
	if len(svc.Environment) != 1 || svc.Environment[0].Name != "TZ" {
		t.Errorf("Environment = %+v, want unchanged", svc.Environment)
	}
}
