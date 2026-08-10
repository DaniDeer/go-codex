package registry

// This file is intentionally IO-free — it tests only imageref.go's pure,
// non-auth functions (image-reference string parsing, no network, no
// httptest servers). Auth-flow tests (challenge parsing, Basic-auth
// credential injection) and the end-to-end request/response flow
// (auth-challenge handshake, GetTags, GetImageMetadata, manifest-list
// resolution, PlatformNotFoundError) both live in the sibling app/registry
// package's auth_test.go/registry_integration_test.go instead — this
// models/ package has no I/O of its own to test beyond pure codec/parse
// logic.

import (
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker"
)

func TestParseImageRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    ImageRef
		wantErr bool
	}{
		{
			name:  "bare name defaults to Docker Hub + library prefix + latest",
			input: "alpine",
			want:  ImageRef{Registry: dockerHubRegistryHost, Repository: "library/alpine", Reference: "latest"},
		},
		{
			name:  "bare name with tag",
			input: "alpine:3.19",
			want:  ImageRef{Registry: dockerHubRegistryHost, Repository: "library/alpine", Reference: "3.19"},
		},
		{
			name:  "explicit registry host with port",
			input: "quay.io/prometheus/prometheus:v2.53.0",
			want:  ImageRef{Registry: "quay.io", Repository: "prometheus/prometheus", Reference: "v2.53.0"},
		},
		{
			name:  "digest reference",
			input: "myregistry.example.com/team/app@sha256:" + strings.Repeat("a", 64),
			want: ImageRef{
				Registry: "myregistry.example.com", Repository: "team/app",
				Reference: "sha256:" + strings.Repeat("a", 64),
			},
		},
		{
			name:  "registry host with explicit port, no tag",
			input: "localhost:5000/myimage",
			want:  ImageRef{Registry: "localhost:5000", Repository: "myimage", Reference: "latest"},
		},
		{
			name:    "invalid shape",
			input:   "INVALID IMAGE REF!!",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseImageRef(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseImageRef(%q): want error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseImageRef(%q): unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseImageRef(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

// ── ImageRef <-> docker.Image mapper ───────────────────────────────────────────

func TestImageRef_ToImage(t *testing.T) {
	tests := []struct {
		name string
		ref  ImageRef
		want docker.Image
	}{
		{
			name: "tag reference",
			ref:  ImageRef{Registry: "ghcr.io", Repository: "org/repo", Reference: "1.2.3"},
			want: docker.Image{Name: "ghcr.io/org/repo", Tag: "1.2.3"},
		},
		{
			name: "digest reference",
			ref:  ImageRef{Registry: "quay.io", Repository: "team/app", Reference: "sha256:" + strings.Repeat("a", 64)},
			want: docker.Image{Name: "quay.io/team/app", Digest: docker.Digest("sha256:" + strings.Repeat("a", 64))},
		},
		{
			name: "no registry (already-bare repository)",
			ref:  ImageRef{Repository: "alpine", Reference: "latest"},
			want: docker.Image{Name: "alpine", Tag: "latest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.ToImage(); got != tt.want {
				t.Errorf("ToImage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestImageRefFromImage(t *testing.T) {
	tests := []struct {
		name         string
		img          docker.Image
		registryHost string
		want         ImageRef
	}{
		{
			name:         "tag, bare name gets library/ prefix for Docker Hub",
			img:          docker.Image{Name: "alpine", Tag: "3.19"},
			registryHost: dockerHubRegistryHost,
			want:         ImageRef{Registry: dockerHubRegistryHost, Repository: "library/alpine", Reference: "3.19"},
		},
		{
			name:         "digest, embedded registry prefix stripped in favor of registryHost",
			img:          docker.Image{Name: "ghcr.io/org/repo", Digest: docker.Digest("sha256:" + strings.Repeat("b", 64))},
			registryHost: "ghcr.io",
			want:         ImageRef{Registry: "ghcr.io", Repository: "org/repo", Reference: "sha256:" + strings.Repeat("b", 64)},
		},
		{
			name:         "no tag/digest defaults to latest",
			img:          docker.Image{Name: "org/repo"},
			registryHost: "quay.io",
			want:         ImageRef{Registry: "quay.io", Repository: "org/repo", Reference: "latest"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImageRefFromImage(tt.img, tt.registryHost); got != tt.want {
				t.Errorf("ImageRefFromImage(%+v, %q) = %+v, want %+v", tt.img, tt.registryHost, got, tt.want)
			}
		})
	}
}

func TestImageRef_ToImage_RoundTripsThroughImageRefFromImage(t *testing.T) {
	original := ImageRef{Registry: "ghcr.io", Repository: "org/repo", Reference: "1.2.3"}
	img := original.ToImage()
	back := ImageRefFromImage(img, original.Registry)
	if back != original {
		t.Errorf("round trip: got %+v, want %+v", back, original)
	}
}
