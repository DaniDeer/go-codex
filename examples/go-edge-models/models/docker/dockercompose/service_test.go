package dockercompose

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
	"github.com/DaniDeer/go-codex/format"
)

func TestServiceCodec_DecodesFullService(t *testing.T) {
	yamlDoc := []byte(`
image: ghcr.io/example-org/factory-api:1.8.16
ports:
  - "8080:80"
  - "80"
volumes:
  - "factory-data:/var/lib/data"
environment:
  - "TZ=Europe/Berlin"
  - "DEBUG=false"
command: ["serve"]
hostname: factory-api
restart: unless-stopped
mem_limit: "512m"
ulimits:
  nofile: 1024
`)
	svc, err := format.YAML(ServiceCodec).Unmarshal(yamlDoc)
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if svc.Image != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("Image = %q", svc.Image)
	}
	if len(svc.Ports) != 2 {
		t.Fatalf("len(Ports) = %d, want 2", len(svc.Ports))
	}
	if len(svc.Volumes) != 1 || svc.Volumes[0].HostPath != "factory-data" {
		t.Errorf("Volumes = %+v", svc.Volumes)
	}
	if len(svc.Environment) != 2 {
		t.Errorf("len(Environment) = %d, want 2", len(svc.Environment))
	}
	if svc.Restart != "unless-stopped" {
		t.Errorf("Restart = %q", svc.Restart)
	}
	if svc.MemLimit != 512*1024*1024 {
		t.Errorf("MemLimit = %d, want %d", svc.MemLimit, 512*1024*1024)
	}
	if len(svc.Ulimits) != 1 || svc.Ulimits[0] != (docker.Ulimit{Name: "nofile", Soft: 1024, Hard: 1024}) {
		t.Errorf("Ulimits = %+v", svc.Ulimits)
	}
}

func TestServiceCodec_RejectsMalformedPort(t *testing.T) {
	yamlDoc := []byte("ports:\n  - \"127.0.0.1:8080:80\"\n")
	if _, err := format.YAML(ServiceCodec).Unmarshal(yamlDoc); err == nil {
		t.Error("Unmarshal with an IP-prefixed port entry: want error, got nil")
	}
}

func TestServiceCodec_BuildOnly(t *testing.T) {
	svc, err := format.YAML(ServiceCodec).Unmarshal([]byte("build: ./app\n"))
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if !svc.Build.IsSet() {
		t.Error("Build.IsSet() = false, want true")
	}
	if svc.Build.Context != "./app" {
		t.Errorf("Build.Context = %q, want ./app", svc.Build.Context)
	}
	if svc.HasImage() {
		t.Error("HasImage() = true, want false")
	}
}

func TestServiceCodec_BuildObjectForm(t *testing.T) {
	svc, err := format.YAML(ServiceCodec).Unmarshal([]byte(
		"build:\n  context: .\n  dockerfile: Dockerfile\n  target: prod\n  args:\n    - GIT_COMMIT=abc123\n",
	))
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if !svc.Build.IsSet() {
		t.Error("Build.IsSet() = false, want true")
	}
	if svc.Build.Dockerfile != "Dockerfile" {
		t.Errorf("Build.Dockerfile = %q, want Dockerfile", svc.Build.Dockerfile)
	}
	if svc.Build.Target != "prod" {
		t.Errorf("Build.Target = %q, want prod", svc.Build.Target)
	}
	if len(svc.Build.Args) != 1 || svc.Build.Args[0].Name != "GIT_COMMIT" || svc.Build.Args[0].Value != "abc123" {
		t.Errorf("Build.Args = %+v, want one GIT_COMMIT=abc123 entry", svc.Build.Args)
	}
}

func TestBuildCodec_ShortForm(t *testing.T) {
	b, err := BuildCodec.Decode("./app")
	if err != nil {
		t.Fatalf("Decode(short form) error: %v", err)
	}
	if b.Context != "./app" || b.Dockerfile != "" || b.Target != "" || len(b.Args) != 0 {
		t.Errorf("Decode(short form) = %+v, want Context=./app and all other fields zero", b)
	}
}

func TestBuildCodec_LongForm(t *testing.T) {
	b, err := BuildCodec.Decode(map[string]any{
		"context":    "./app",
		"dockerfile": "Dockerfile.prod",
		"target":     "prod",
	})
	if err != nil {
		t.Fatalf("Decode(long form) error: %v", err)
	}
	if b.Context != "./app" || b.Dockerfile != "Dockerfile.prod" || b.Target != "prod" {
		t.Errorf("Decode(long form) = %+v", b)
	}
}

func TestBuildCodec_ArgsListForm(t *testing.T) {
	b, err := BuildCodec.Decode(map[string]any{
		"context": "./app",
		"args":    []any{"GIT_COMMIT=abc123", "BUILD_ENV=prod"},
	})
	if err != nil {
		t.Fatalf("Decode(args list form) error: %v", err)
	}
	if len(b.Args) != 2 || b.Args[0].Name != "GIT_COMMIT" || b.Args[0].Value != "abc123" ||
		b.Args[1].Name != "BUILD_ENV" || b.Args[1].Value != "prod" {
		t.Errorf("Decode(args list form).Args = %+v", b.Args)
	}
}

func TestBuildCodec_EncodeRoundTripsShortFormToLongForm(t *testing.T) {
	b, err := BuildCodec.Decode("./app")
	if err != nil {
		t.Fatalf("Decode(short form) error: %v", err)
	}
	encoded, err := BuildCodec.Encode(b)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	m, ok := encoded.(map[string]any)
	if !ok {
		t.Fatalf("Encode(short-form-decoded Build) = %T, want map[string]any (long form)", encoded)
	}
	if m["context"] != "./app" {
		t.Errorf("Encode context = %v, want ./app", m["context"])
	}
}

func TestBuildCodec_EncodeOmitsUnsetAsNil(t *testing.T) {
	encoded, err := BuildCodec.Encode(Build{})
	if err != nil {
		t.Fatalf("Encode(Build{}) error: %v", err)
	}
	if encoded != nil {
		t.Errorf("Encode(Build{}) = %v, want nil", encoded)
	}
}

func TestBuildCodec_RejectsBooleanForm(t *testing.T) {
	if _, err := BuildCodec.Decode(true); err == nil {
		t.Error("Decode(true) succeeded, want error (real Compose build: is never a bare bool)")
	}
}

// ── image-or-build-required (cross-field constraint) ────────────────────────

func TestServiceCodec_RejectsNeitherImageNorBuild(t *testing.T) {
	_, err := format.YAML(ServiceCodec).Unmarshal([]byte("hostname: factory-api\n"))
	if err == nil {
		t.Fatal("expected error for service with neither image nor build, got nil")
	}
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
	if ce.Name != "image-or-build-required" {
		t.Errorf("ConstraintError.Name = %q, want image-or-build-required", ce.Name)
	}
}

func TestServiceCodec_AcceptsImageOnly(t *testing.T) {
	_, err := format.YAML(ServiceCodec).Unmarshal([]byte("image: alpine:3.19\n"))
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
}

func TestServiceCodec_AcceptsBuildOnly(t *testing.T) {
	_, err := format.YAML(ServiceCodec).Unmarshal([]byte("build: ./app\n"))
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
}

func TestServiceCodec_AcceptsBothImageAndBuild(t *testing.T) {
	_, err := format.YAML(ServiceCodec).Unmarshal([]byte("image: myapp:latest\nbuild: ./app\n"))
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
}

// ── mem-reservation-not-exceeding-mem-limit (cross-field constraint) ────────

func TestServiceCodec_RejectsMemReservationExceedingMemLimit(t *testing.T) {
	_, err := format.YAML(ServiceCodec).Unmarshal([]byte("image: alpine:3.19\nmem_limit: 256m\nmem_reservation: 512m\n"))
	if err == nil {
		t.Fatal("expected error for mem_reservation exceeding mem_limit, got nil")
	}
	var ce codex.ConstraintError
	if !errors.As(err, &ce) {
		t.Fatalf("expected ConstraintError, got %T: %v", err, err)
	}
	if ce.Name != "mem-reservation-not-exceeding-mem-limit" {
		t.Errorf("ConstraintError.Name = %q, want mem-reservation-not-exceeding-mem-limit", ce.Name)
	}
}

func TestServiceCodec_AcceptsMemReservationWithinMemLimit(t *testing.T) {
	_, err := format.YAML(ServiceCodec).Unmarshal([]byte("image: alpine:3.19\nmem_limit: 512m\nmem_reservation: 256m\n"))
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
}

func TestServiceCodec_MemLimitOrReservationAloneIsFine(t *testing.T) {
	if _, err := format.YAML(ServiceCodec).Unmarshal([]byte("image: alpine:3.19\nmem_limit: 512m\n")); err != nil {
		t.Errorf("mem_limit alone: unexpected error: %v", err)
	}
	if _, err := format.YAML(ServiceCodec).Unmarshal([]byte("image: alpine:3.19\nmem_reservation: 256m\n")); err != nil {
		t.Errorf("mem_reservation alone: unexpected error: %v", err)
	}
}

func TestService_HasImage(t *testing.T) {
	if (Service{Image: "x"}).HasImage() != true {
		t.Error("HasImage() = false, want true")
	}
	if (Service{}).HasImage() != false {
		t.Error("HasImage() = true, want false")
	}
}

func TestService_CreateOptionsFor_AssemblesEverything(t *testing.T) {
	svc := Service{
		Ports: []docker.PortMapping{
			{Port: "80/tcp", HostPort: "8080"},
			{Port: "80/tcp", HostPort: ""},
		},
		Volumes:    []docker.Bind{{HostPath: "/data", ContainerPath: "/var/data"}},
		Command:    []string{"serve"},
		Entrypoint: []string{"/bin/sh"},
		Hostname:   "factory-api",
		Domainname: "example.internal",
		MemLimit:   512 * 1024 * 1024,
		Ulimits:    []docker.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}},
		Environment: docker.Env{
			{Name: "TZ", Value: "Europe/Berlin"},
		},
	}

	co := svc.CreateOptionsFor()

	if len(co.ExposedPorts) != 1 {
		t.Errorf("len(ExposedPorts) = %d, want 1", len(co.ExposedPorts))
	}
	if len(co.HostConfig.PortBindings) != 1 {
		t.Errorf("len(PortBindings) = %d, want 1", len(co.HostConfig.PortBindings))
	}
	if len(co.HostConfig.Binds) != 1 || co.HostConfig.Binds[0].HostPath != "/data" {
		t.Errorf("Binds = %+v", co.HostConfig.Binds)
	}
	if co.HostConfig.Memory != 512*1024*1024 {
		t.Errorf("Memory = %d, want %d", co.HostConfig.Memory, 512*1024*1024)
	}
	if len(co.HostConfig.Ulimits) != 1 || co.HostConfig.Ulimits[0].Name != "nofile" {
		t.Errorf("Ulimits = %+v", co.HostConfig.Ulimits)
	}
	if co.Hostname != "factory-api" || co.Domainname != "example.internal" {
		t.Errorf("Hostname/Domainname = %q/%q", co.Hostname, co.Domainname)
	}
	if len(co.Cmd) != 1 || co.Cmd[0] != "serve" {
		t.Errorf("Cmd = %+v", co.Cmd)
	}
	if len(co.Env) != 1 || co.Env[0].Name != "TZ" {
		t.Errorf("Env = %+v", co.Env)
	}
}

func TestService_CreateOptionsFor_HealthcheckDisable(t *testing.T) {
	svc := Service{Healthcheck: docker.Healthcheck{Test: []string{"NONE"}}}
	co := svc.CreateOptionsFor()
	if len(co.Healthcheck.Test) != 1 || co.Healthcheck.Test[0] != "NONE" {
		t.Errorf("Healthcheck.Test = %+v, want [\"NONE\"]", co.Healthcheck.Test)
	}
}

func TestServiceFromCreateOptions_ReconstructsFields(t *testing.T) {
	co := docker.CreateOptions{
		Cmd:          []string{"serve"},
		Entrypoint:   []string{"/bin/sh"},
		Hostname:     "factory-api",
		Domainname:   "example.internal",
		ExposedPorts: []docker.Port{"80/tcp", "9000/udp"},
		HostConfig: docker.HostConfig{
			Binds: []docker.Bind{{HostPath: "/data", ContainerPath: "/var/data"}},
			PortBindings: []docker.PortBinding{
				{Port: "80/tcp", Bindings: []docker.PortBindingEntry{{HostPort: "8080"}}},
			},
			Memory:  512 * 1024 * 1024,
			Ulimits: []docker.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}},
		},
		Env: docker.Env{{Name: "TZ", Value: "Europe/Berlin"}},
	}

	svc := ServiceFromCreateOptions(co)
	if len(svc.Ports) != 2 {
		t.Fatalf("len(Ports) = %d, want 2: %+v", len(svc.Ports), svc.Ports)
	}
	wantPorts := map[docker.PortMapping]bool{
		{Port: "80/tcp", HostPort: "8080"}: true,
		{Port: "9000/udp", HostPort: ""}:   true,
	}
	for _, p := range svc.Ports {
		if !wantPorts[p] {
			t.Errorf("unexpected port entry %+v", p)
		}
	}
	if len(svc.Volumes) != 1 || svc.Volumes[0].HostPath != "/data" {
		t.Errorf("Volumes = %+v", svc.Volumes)
	}
	if svc.MemLimit != 512*1024*1024 {
		t.Errorf("MemLimit = %d", svc.MemLimit)
	}
	if len(svc.Ulimits) != 1 || svc.Ulimits[0] != (docker.Ulimit{Name: "nofile", Soft: 1024, Hard: 2048}) {
		t.Errorf("Ulimits = %+v", svc.Ulimits)
	}
	if svc.Hostname != "factory-api" || svc.Domainname != "example.internal" {
		t.Errorf("Hostname/Domainname = %q/%q", svc.Hostname, svc.Domainname)
	}
	if len(svc.Command) != 1 || svc.Command[0] != "serve" {
		t.Errorf("Command = %+v", svc.Command)
	}
	if len(svc.Environment) != 1 || svc.Environment[0].Name != "TZ" {
		t.Errorf("Environment = %+v", svc.Environment)
	}
}

func TestServiceFromCreateOptions_HealthcheckDisableReversed(t *testing.T) {
	co := docker.CreateOptions{Healthcheck: docker.Healthcheck{Test: []string{"NONE"}}}
	svc := ServiceFromCreateOptions(co)
	if len(svc.Healthcheck.Test) != 1 || svc.Healthcheck.Test[0] != "NONE" {
		t.Errorf("Healthcheck.Test = %+v, want [\"NONE\"]", svc.Healthcheck.Test)
	}
}

func TestServiceFromCreateOptions_RoundTripsWithCreateOptionsFor(t *testing.T) {
	original := Service{
		Ports: []docker.PortMapping{
			{Port: "80/tcp", HostPort: "8080"},
			{Port: "9000/udp", HostPort: ""},
		},
		Volumes:    []docker.Bind{{HostPath: "/data", ContainerPath: "/var/data"}},
		Command:    []string{"serve"},
		Entrypoint: []string{"/bin/sh"},
		Hostname:   "factory-api",
		Domainname: "example.internal",
		MemLimit:   512 * 1024 * 1024,
		Ulimits:    []docker.Ulimit{{Name: "nofile", Soft: 1024, Hard: 2048}},
		Environment: docker.Env{
			{Name: "TZ", Value: "Europe/Berlin"},
		},
	}

	co := original.CreateOptionsFor()
	roundTripped := ServiceFromCreateOptions(co)

	if len(roundTripped.Ports) != len(original.Ports) {
		t.Errorf("Ports = %+v, want same length as %+v", roundTripped.Ports, original.Ports)
	}
	if roundTripped.Hostname != original.Hostname || roundTripped.Domainname != original.Domainname {
		t.Errorf("Hostname/Domainname mismatch: got %q/%q, want %q/%q",
			roundTripped.Hostname, roundTripped.Domainname, original.Hostname, original.Domainname)
	}
	if roundTripped.MemLimit != original.MemLimit {
		t.Errorf("MemLimit = %d, want %d", roundTripped.MemLimit, original.MemLimit)
	}
	if len(roundTripped.Environment) != len(original.Environment) {
		t.Errorf("Environment = %+v, want same length as %+v", roundTripped.Environment, original.Environment)
	}
}

func TestCreateOptionsFromServiceCodec_RoundTrip(t *testing.T) {
	raw := map[string]any{
		"image": "ghcr.io/example-org/factory-api:1.8.16",
		"ports": []any{"8080:80"},
	}
	co, err := CreateOptionsFromServiceCodec.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: unexpected error: %v", err)
	}
	if len(co.ExposedPorts) != 1 || co.ExposedPorts[0] != "80/tcp" {
		t.Errorf("ExposedPorts = %+v", co.ExposedPorts)
	}
	if len(co.HostConfig.PortBindings) != 1 || co.HostConfig.PortBindings[0].Bindings[0].HostPort != "8080" {
		t.Errorf("PortBindings = %+v", co.HostConfig.PortBindings)
	}

	encoded, err := CreateOptionsFromServiceCodec.Encode(co)
	if err != nil {
		t.Fatalf("Encode: unexpected error: %v", err)
	}
	obj, ok := encoded.(map[string]any)
	if !ok {
		t.Fatalf("Encode result type = %T, want map[string]any", encoded)
	}
	ports, ok := obj["ports"].([]any)
	if !ok || len(ports) != 1 || ports[0] != "8080:80" {
		t.Errorf("encoded ports = %+v", obj["ports"])
	}
}
