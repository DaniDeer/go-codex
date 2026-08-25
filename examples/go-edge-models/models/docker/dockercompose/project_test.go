package dockercompose

import (
	"testing"

	"github.com/DaniDeer/go-codex/format"
)

func TestProjectCodec_DecodesMultipleServices(t *testing.T) {
	yamlDoc := []byte(`
services:
  factory-api:
    image: ghcr.io/example-org/factory-api:1.8.16
    ports:
      - "8080:80"
  factory-cache:
    image: apache/kvrocks:2.15.0
`)
	project, err := format.YAML(ProjectCodec).Unmarshal(yamlDoc)
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if len(project.Services) != 2 {
		t.Fatalf("len(Services) = %d, want 2", len(project.Services))
	}
	api, ok := project.Services["factory-api"]
	if !ok {
		t.Fatal(`Services["factory-api"] missing`)
	}
	if api.Image != "ghcr.io/example-org/factory-api:1.8.16" {
		t.Errorf("factory-api.Image = %q", api.Image)
	}
}

func TestProjectCodec_IgnoresUnmodeledTopLevelKeys(t *testing.T) {
	// version:, networks:, volumes: (top-level) are NOT modeled by this
	// package — codex.Struct's forward-compatible decode should
	// silently ignore them rather than erroring.
	yamlDoc := []byte(`
version: "3.8"
networks:
  default:
    driver: bridge
volumes:
  factory-data: {}
services:
  factory-api:
    image: ghcr.io/example-org/factory-api:1.8.16
`)
	project, err := format.YAML(ProjectCodec).Unmarshal(yamlDoc)
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if len(project.Services) != 1 {
		t.Fatalf("len(Services) = %d, want 1", len(project.Services))
	}
}

func TestProjectCodec_IgnoresUnmodeledServiceKeys(t *testing.T) {
	yamlDoc := []byte(`
services:
  factory-api:
    image: ghcr.io/example-org/factory-api:1.8.16
    depends_on:
      - factory-db
    networks:
      - default
    labels:
      - "com.example.team=platform"
`)
	project, err := format.YAML(ProjectCodec).Unmarshal(yamlDoc)
	if err != nil {
		t.Fatalf("Unmarshal: unexpected error: %v", err)
	}
	if project.Services["factory-api"].Image == "" {
		t.Error("Image should still decode despite unmodeled sibling keys")
	}
}

func TestProjectCodec_MissingServicesIsError(t *testing.T) {
	_, err := format.YAML(ProjectCodec).Unmarshal([]byte("version: \"3.8\"\n"))
	if err == nil {
		t.Error("Unmarshal with no services key: want error, got nil")
	}
}
