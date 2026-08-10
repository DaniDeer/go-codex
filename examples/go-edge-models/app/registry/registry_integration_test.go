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
// resolution, PlatformNotFoundError) — models/docker/registry's imageref_test.go deliberately does
// NOT re-derive this coverage via local httptest mocks; it stays IO-free,
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

	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
)

// requireNetwork skips t if a short-timeout probe against Docker Hub
// fails — so a machine with the "integration" tag set but no internet
// access (or a registry outage) skips cleanly instead of failing the
// whole suite.
func requireNetwork(t *testing.T, client *http.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := GetTags(ctx, client, "alpine"); err != nil {
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
		// GHCR and MCR — same GetTags call, same registry.TagsList result
		// shape, no registry-specific code involved. Proves docker/registry
		// is registry-agnostic for public images, not just Docker-Hub-shaped.
		{image: "ghcr.io/nginxinc/nginx-unprivileged", wantName: "nginxinc/nginx-unprivileged", minTagsLen: 5},
		{image: "mcr.microsoft.com/dotnet/runtime", wantName: "dotnet/runtime", minTagsLen: 5},
	}

	for _, tt := range tests {
		t.Run(tt.image, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			tags, err := GetTags(ctx, client, tt.image)
			if err != nil {
				t.Fatalf("GetTags(%q): %v", tt.image, err)
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

	// Docker Hub, GHCR, and MCR — same GetImageMetadata call, same
	// registry.ManifestMetadata result shape for every registry.
	images := []string{"alpine", "nodered/node-red", "ghcr.io/nginxinc/nginx-unprivileged", "mcr.microsoft.com/dotnet/runtime"}

	for _, image := range images {
		t.Run(image, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			meta, err := GetImageMetadata(ctx, client, regmodels.GetImageMetadataReq{ImageURL: image + ":latest"})
			if err != nil {
				t.Fatalf("GetImageMetadata(%q:latest): %v", image, err)
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

	amd64, err := GetImageMetadata(ctx, client, regmodels.GetImageMetadataReq{ImageURL: "alpine:latest", Platform: "linux/amd64"})
	if err != nil {
		t.Fatalf("GetImageMetadata(linux/amd64): %v", err)
	}
	arm64, err := GetImageMetadata(ctx, client, regmodels.GetImageMetadataReq{ImageURL: "alpine:latest", Platform: "linux/arm64"})
	if err != nil {
		t.Fatalf("GetImageMetadata(linux/arm64): %v", err)
	}
	if amd64.Image.Digest == arm64.Image.Digest {
		t.Errorf("expected different digests for linux/amd64 vs linux/arm64, both got %q", amd64.Image.Digest)
	}
}

// TestIntegration_PlatformNotFound confirms a platform absent from the
// real manifest list produces a typed, errors.As-navigable error.
func TestIntegration_PlatformNotFound(t *testing.T) {
	client := integrationClient()
	requireNetwork(t, client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := GetImageMetadata(ctx, client, regmodels.GetImageMetadataReq{ImageURL: "alpine:latest", Platform: "plan9/386"})
	var pnfErr PlatformNotFoundError
	if !errors.As(err, &pnfErr) {
		t.Fatalf("GetImageMetadata(plan9/386): want PlatformNotFoundError, got %v", err)
	}
	if pnfErr.Platform != "plan9/386" {
		t.Errorf("PlatformNotFoundError.Platform = %q, want %q", pnfErr.Platform, "plan9/386")
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
