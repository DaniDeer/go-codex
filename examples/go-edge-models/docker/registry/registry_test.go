package registry

// This file is intentionally IO-free — it tests only pure functions
// (string parsing/formatting, no network, no httptest servers). The
// end-to-end request/response flow (auth-challenge handshake, GetTags,
// GetImageMetadata, manifest-list resolution, PlatformNotFoundError) is
// covered by registry_integration_test.go instead, which exercises the
// SAME code paths against the REAL Docker Hub registry — a stronger,
// more realistic signal than a local httptest mock ever gave, without
// this package having to own and maintain ~150 lines of mock-server
// plumbing (auth-realm server, Bearer-token verification, manifest-list
// JSON fixtures, a scheme-rewriting http.RoundTripper, ...). See
// registry_integration_test.go's file doc comment for how to run it
// (requires the "integration" build tag + network access; NOT part of the
// default `go test ./...` / `just check` path).

import (
	"strings"
	"testing"
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
			input: "ghcr.io/bosch-cc-mfd/edge-gateway:0.12.5.0",
			want:  ImageRef{Registry: "ghcr.io", Repository: "bosch-cc-mfd/edge-gateway", Reference: "0.12.5.0"},
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

func TestParseChallenge(t *testing.T) {
	header := make(map[string][]string)
	header["Www-Authenticate"] = []string{
		`Bearer realm="https://auth.example.com/token",service="registry.example.com",scope="repository:my/repo:pull"`,
	}

	ch, err := parseChallenge(header)
	if err != nil {
		t.Fatalf("parseChallenge: %v", err)
	}
	if ch.Realm != "https://auth.example.com/token" || ch.Service != "registry.example.com" || ch.Scope != "repository:my/repo:pull" {
		t.Errorf("parseChallenge = %+v, unexpected", ch)
	}

	badHeader := make(map[string][]string)
	badHeader["Www-Authenticate"] = []string{`Basic realm="x"`}
	if _, err := parseChallenge(badHeader); err == nil {
		t.Error("parseChallenge: want error for non-Bearer scheme")
	}
}
