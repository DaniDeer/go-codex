package registry

import (
	"context"
	"errors"
	"net/http"
	"testing"

	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	dockerhubmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/dockerhub"
	ghcrmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/ghcr"
	mcrmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/mcr"
)

func TestNewDispatcher_ResolvesKnownHostsToConcreteClients(t *testing.T) {
	d := NewDispatcher(http.DefaultClient)

	tests := []struct {
		name string
		host string
	}{
		{"DockerHub", dockerhubmodels.RegistryHost},
		{"GHCR", ghcrmodels.RegistryHost},
		{"MCR", mcrmodels.RegistryHost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, ok := d.byHost[tt.host]
			if !ok {
				t.Fatalf("byHost[%q]: want an entry, got none", tt.host)
			}
			if client == nil {
				t.Errorf("byHost[%q]: want non-nil Client", tt.host)
			}
		})
	}
}

func TestDispatcher_Resolve_UnknownHostFallsBackToGeneric(t *testing.T) {
	d := NewDispatcher(http.DefaultClient)

	client, ref, err := d.resolve("example.invalid/org/repo:latest")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ref.Registry != "example.invalid" {
		t.Errorf("resolve: ref.Registry = %q, want %q", ref.Registry, "example.invalid")
	}
	if _, ok := client.(*genericClient); !ok {
		t.Errorf("resolve: want *genericClient fallback, got %T", client)
	}
}

func TestDispatcher_GetTags_PropagatesInvalidImageURL(t *testing.T) {
	d := NewDispatcher(http.DefaultClient)
	_, err := d.GetTags(context.Background(), "INVALID IMAGE REF!!")
	if err == nil {
		t.Fatal("GetTags: want error for invalid imageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("GetTags error = %v, want ImageRefParseError", err)
	}
}

func TestDispatcher_GetImageMetadata_PropagatesInvalidImageURL(t *testing.T) {
	d := NewDispatcher(http.DefaultClient)
	_, err := d.GetImageMetadata(context.Background(), regmodels.GetImageMetadataReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("GetImageMetadata: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("GetImageMetadata error = %v, want ImageRefParseError", err)
	}
}

func TestDispatcher_NewGetTagsToolHandler_PropagatesInvalidImageURL(t *testing.T) {
	d := NewDispatcher(http.DefaultClient)
	handler := d.NewGetTagsToolHandler()
	_, err := handler(context.Background(), regmodels.GetTagsToolReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("handler error = %v, want ImageRefParseError", err)
	}
}

func TestDispatcher_NewGetImageMetadataToolHandler_PropagatesInvalidImageURL(t *testing.T) {
	d := NewDispatcher(http.DefaultClient)
	handler := d.NewGetImageMetadataToolHandler()
	_, err := handler(context.Background(), regmodels.GetImageMetadataReq{ImageURL: "INVALID IMAGE REF!!"})
	if err == nil {
		t.Fatal("handler: want error for invalid ImageURL, got nil")
	}
	var parseErr regmodels.ImageRefParseError
	if !errors.As(err, &parseErr) {
		t.Errorf("handler error = %v, want ImageRefParseError", err)
	}
}
