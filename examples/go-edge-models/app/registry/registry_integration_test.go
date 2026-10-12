//go:build integration

// This file is gated behind the "integration" build tag — it is NEVER
// compiled or run by a plain `go build ./...` / `go vet ./...` /
// `go test ./...` / `just check`, which must all stay offline-safe and
// deterministic. Run it explicitly, with network access to Docker Hub,
// via:
//
//	go test -tags=integration ./examples/go-edge-models/app/registry/...
//
// Every assertion below was first verified manually against the real
// registry-1.docker.io / auth.docker.io before being committed here — this
// file turns that ad-hoc verification into a repeatable, opt-in test.
//
// THIS FILE IS THE PRIMARY coverage for the full request/response flow
// (auth-challenge handshake, GetTags, GetImageMetadata, manifest-list
// resolution, PlatformNotFoundError) against [NewGenericClient] directly
// — the GENERIC FALLBACK implementation this package's own Dispatcher
// falls back to for any registry host OTHER than the 3 it knows
// concretely (dockerhub/ghcr/mcr). TestIntegration_Dispatcher_
// ResolvesToConcreteClientsAndCallsReal (below) separately proves
// Dispatcher resolves those 3 KNOWN hosts to their own self-contained
// clients and completes a real call against each — this file's OTHER
// tests exercise the fallback branch specifically.
// models/docker/registry's imageref_test.go deliberately does NOT
// re-derive this coverage via local httptest mocks; it stays IO-free,
// testing only pure functions (ParseImageRef, parseChallenge). A real
// registry is a stronger, more realistic signal than a hand-maintained
// mock server ever gave for this logic, and this package no longer has to
// own ~150 lines of mock-server plumbing (auth-realm server, Bearer-token
// verification, manifest-list JSON fixtures, a scheme-rewriting
// http.RoundTripper) to get it.
package registry

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/dockerhub"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/ghcr"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/app/registry/mcr"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	dockerhubmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/dockerhub"
	ghcrmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/ghcr"
	mcrmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/mcr"
)

// requireNetwork skips t if a short-timeout probe against Docker Hub
// fails — so a machine with the "integration" tag set but no internet
// access (or a registry outage) skips cleanly instead of failing the
// whole suite.
func requireNetwork(t *testing.T, client *http.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := NewGenericClient(client, regmodels.DockerHubRegistryHost).GetTags(ctx, "library/alpine"); err != nil {
		t.Skipf("skipping integration test: registry unreachable: %v", err)
	}
}

func integrationClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func TestIntegration_GetTags(t *testing.T) {
	client := integrationClient()
	requireNetwork(t, client)

	tests := []struct {
		image      string
		wantName   string
		minTagsLen int
	}{
		{image: "alpine", wantName: "library/alpine", minTagsLen: 10},
		{image: "nodered/node-red", wantName: "nodered/node-red", minTagsLen: 10},
		// GHCR and MCR — same Client.GetTags call, same registry.TagsList
		// result shape, no registry-specific code involved. Proves
		// docker/registry is registry-agnostic for public images, not
		// just Docker-Hub-shaped.
		{image: "ghcr.io/nginxinc/nginx-unprivileged", wantName: "nginxinc/nginx-unprivileged", minTagsLen: 5},
		{image: "mcr.microsoft.com/dotnet/runtime", wantName: "dotnet/runtime", minTagsLen: 5},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			ref, err := regmodels.ParseImageRef(tt.image)
			if err != nil {
				t.Fatalf("ParseImageRef(%q): %v", tt.image, err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tags, err := NewGenericClient(client, ref.Registry).GetTags(ctx, ref.Repository)
			if err != nil {
				t.Fatalf("Client.GetTags(%q): %v", tt.image, err)
			}
			if tags.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", tags.Name, tt.wantName)
			}
			if len(tags.Tags) < tt.minTagsLen {
				t.Errorf("len(Tags) = %d, want at least %d", len(tags.Tags), tt.minTagsLen)
			}
			found := false
			for _, tag := range tags.Tags {
				if tag == "latest" {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Tags does not contain %q", "latest")
			}
		})
	}
}

var reSHA256Digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func TestIntegration_GetImageMetadata(t *testing.T) {
	client := integrationClient()
	requireNetwork(t, client)

	// Docker Hub, GHCR, and MCR — same Client.GetImageMetadata call, same
	// registry.ManifestMetadata result shape for every registry.
	images := []string{"alpine", "nodered/node-red", "ghcr.io/nginxinc/nginx-unprivileged", "mcr.microsoft.com/dotnet/runtime"}

	for _, image := range images {
		t.Run(image, func(t *testing.T) {
			ref, err := regmodels.ParseImageRef(image + ":latest")
			if err != nil {
				t.Fatalf("ParseImageRef(%q): %v", image, err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			meta, err := NewGenericClient(client, ref.Registry).GetImageMetadata(ctx, ref.Repository, ref.Reference, "")
			if err != nil {
				t.Fatalf("Client.GetImageMetadata(%q:latest): %v", image, err)
			}
			if meta.SchemaVersion != 2 {
				t.Errorf("SchemaVersion = %d, want 2", meta.SchemaVersion)
			}
			if !reSHA256Digest.MatchString(string(meta.Image.Digest)) {
				t.Errorf("Digest = %q, want a valid sha256:<64-hex> digest", meta.Image.Digest)
			}
			if meta.TotalSizeBytes <= 0 {
				t.Errorf("TotalSizeBytes = %d, want > 0", meta.TotalSizeBytes)
			}
			if meta.MediaType == "" {
				t.Error("MediaType is empty")
			}
		})
	}
}

// TestIntegration_PlatformOverride confirms per-platform resolution
// actually selects a DIFFERENT manifest-list entry against real
// multi-arch data — alpine:latest is a real manifest list with distinct
// linux/amd64 and linux/arm64 entries.
func TestIntegration_PlatformOverride(t *testing.T) {
	client := integrationClient()
	requireNetwork(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := NewGenericClient(client, regmodels.DockerHubRegistryHost)
	amd64, err := c.GetImageMetadata(ctx, "library/alpine", "latest", "linux/amd64")
	if err != nil {
		t.Fatalf("GetImageMetadata(linux/amd64): %v", err)
	}
	arm64, err := c.GetImageMetadata(ctx, "library/alpine", "latest", "linux/arm64")
	if err != nil {
		t.Fatalf("GetImageMetadata(linux/arm64): %v", err)
	}
	if amd64.Image.Digest == arm64.Image.Digest {
		t.Errorf("expected different digests for linux/amd64 vs linux/arm64, both got %q", amd64.Image.Digest)
	}
}

// TestIntegration_PlatformNotFound confirms a platform absent from the
// real manifest list produces a typed, errors.As-navigable error.
// "windows/amd64" is a real, enum-supported platform
// (util.SupportedPlatforms) but genuinely absent from alpine's real
// manifest list — unlike an entirely invented "os/arch" string, it
// exercises the "not found in THIS image's manifest list" branch
// specifically, not DefaultPlatformCodec's own enum rejection.
func TestIntegration_PlatformNotFound(t *testing.T) {
	client := integrationClient()
	requireNetwork(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := NewGenericClient(client, regmodels.DockerHubRegistryHost).GetImageMetadata(ctx, "library/alpine", "latest", "windows/amd64")
	var pnfErr PlatformNotFoundError
	if !errors.As(err, &pnfErr) {
		t.Fatalf("GetImageMetadata(windows/amd64): want PlatformNotFoundError, got %v", err)
	}
	if pnfErr.Platform != "windows/amd64" {
		t.Errorf("PlatformNotFoundError.Platform = %q, want %q", pnfErr.Platform, "windows/amd64")
	}
}

// TestIntegration_Dispatcher_ResolvesToConcreteClientsAndCallsReal proves
// Dispatcher (dispatcher.go) picks the matching CONCRETE per-registry
// client (not just the generic fallback NewGenericClient's own tests
// above exercise) for each of the 3 known hosts, and that each completes
// a real GetTags call against it — the per-registry-dispatch coverage
// this package's own Dispatcher relies on, mirroring what a standalone
// dispatch package's own integration test used to cover before
// Dispatcher was folded into this package directly.
func TestIntegration_Dispatcher_ResolvesToConcreteClientsAndCallsReal(t *testing.T) {
	client := integrationClient()
	requireNetwork(t, client)

	d := NewDispatcher(client)

	cases := []struct {
		name     string
		imageURL string
		wantHost string
	}{
		{"DockerHub", "alpine", dockerhubmodels.RegistryHost},
		{"GHCR", "ghcr.io/nginxinc/nginx-unprivileged", ghcrmodels.RegistryHost},
		{"MCR", "mcr.microsoft.com/dotnet/runtime", mcrmodels.RegistryHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, ref, err := d.resolve(tc.imageURL)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if ref.Registry != tc.wantHost {
				t.Fatalf("resolve: want host %q, got %q", tc.wantHost, ref.Registry)
			}
			switch tc.name {
			case "DockerHub":
				if _, ok := resolved.(*dockerhub.Client); !ok {
					t.Errorf("resolve: want *dockerhub.Client, got %T", resolved)
				}
			case "GHCR":
				if _, ok := resolved.(*ghcr.Client); !ok {
					t.Errorf("resolve: want *ghcr.Client, got %T", resolved)
				}
			case "MCR":
				if _, ok := resolved.(*mcr.Client); !ok {
					t.Errorf("resolve: want *mcr.Client, got %T", resolved)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			tags, err := d.GetTags(ctx, tc.imageURL)
			if err != nil {
				t.Fatalf("GetTags(%s): %v", tc.imageURL, err)
			}
			if len(tags.Tags) == 0 {
				t.Errorf("GetTags(%s): want at least one tag, got none", tc.imageURL)
			}
		})
	}
}

// TestIntegration_ParseImageRef_RealConventions is a small sanity check —
// not exhaustive (see the offline TestParseImageRef for full coverage) —
// confirming ParseImageRef's Docker Hub conventions match the exact image
// strings exercised against the real registry above.
func TestIntegration_ParseImageRef_RealConventions(t *testing.T) {
	tests := []struct {
		input string
		want  regmodels.ImageRef
	}{
		{input: "alpine", want: regmodels.ImageRef{Registry: "registry-1.docker.io", Repository: "library/alpine", Reference: "latest"}},
		{input: "nodered/node-red:1.3.7", want: regmodels.ImageRef{Registry: "registry-1.docker.io", Repository: "nodered/node-red", Reference: "1.3.7"}},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := regmodels.ParseImageRef(tt.input)
			if err != nil {
				t.Fatalf("regmodels.ParseImageRef(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("regmodels.ParseImageRef(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}
