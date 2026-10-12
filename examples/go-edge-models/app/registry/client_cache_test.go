package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
	genericmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/generic"
)

// newManifestListRegistry builds a registry+auth-realm server pair
// serving a successful "org/repo" tags/list AND a 2-platform manifest
// list for "org/repo:latest" (resolving to a real single-platform
// manifest for the linux/amd64 entry) — mirrors examples/go-edge-models/
// main.go's own runRegistryDemo fixture, trimmed to just what
// TestGenericClient_GetImageMetadata_SharesCacheAcrossListAndPlatformFetch/
// TestGenericClient_GetTags_CachesTokenAcrossRepeatedCallsToSameRepo need,
// plus request counters on the Ping and token-exchange endpoints to prove
// gc's per-repository token cache is shared across multiple
// GetTagsRoute/GetManifestRoute calls for the SAME repository — unlike
// newCountingCredentialCheckRegistry (auth_test.go), whose registry
// handler always 401s unconditionally (fine for credentialFor-level
// tests, but it would also count every GetTagsRoute/GetManifestRoute
// call's OWN (failing) request against pingHits, making those counts
// unusable for a test that calls GetTags/GetImageMetadata directly).
func newManifestListRegistry(t *testing.T) (registrySrv *httptest.Server, pingHits, tokenHits *int32) {
	t.Helper()

	const fakeToken = "demo-token"
	pingHits = new(int32)
	tokenHits = new(int32)

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(tokenHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"token": fakeToken})
	}))
	t.Cleanup(authSrv.Close)

	challengeRaw, err := auth.ChallengeCodec.Encode(auth.Challenge{
		Realm: authSrv.URL + "/token", Service: "test-registry", Scope: "repository:org/repo:pull",
	})
	if err != nil {
		t.Fatalf("ChallengeCodec.Encode: %v", err)
	}
	challenge := challengeRaw.(string)
	bearerAuth := "Bearer " + fakeToken

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(pingHits, 1)
		if r.Header.Get("Authorization") != bearerAuth {
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v2/org/repo/tags/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "org/repo",
			"tags": []string{"1.0.0", "2.0.0"},
		})
	})
	mux.HandleFunc("/v2/org/repo/manifests/", func(w http.ResponseWriter, r *http.Request) {
		reference := strings.TrimPrefix(r.URL.Path, "/v2/org/repo/manifests/")
		w.Header().Set("Content-Type", "application/json")
		switch reference {
		case "latest":
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("11", 32))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.docker.distribution.manifest.list.v2+json",
				"manifests": []map[string]any{
					{
						"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
						"digest":    "sha256:" + strings.Repeat("f6", 32),
						"size":      1234,
						"platform":  map[string]any{"architecture": "amd64", "os": "linux"},
					},
					{
						"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
						"digest":    "sha256:" + strings.Repeat("07", 32),
						"size":      1235,
						"platform":  map[string]any{"architecture": "arm64", "os": "linux"},
					},
				},
			})
		case "sha256:" + strings.Repeat("f6", 32):
			w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("f6", 32))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schemaVersion": 2,
				"mediaType":     "application/vnd.docker.distribution.manifest.v2+json",
				"config":        map[string]any{"mediaType": "application/vnd.docker.container.image.v1+json", "digest": "sha256:" + strings.Repeat("c3", 32), "size": 5000},
				"layers": []map[string]any{
					{"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip", "digest": "sha256:" + strings.Repeat("d4", 32), "size": 30000000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	})
	registrySrv = httptest.NewServer(mux)
	t.Cleanup(registrySrv.Close)
	return registrySrv, pingHits, tokenHits
}

func TestGenericClient_GetImageMetadata_SharesCacheAcrossListAndPlatformFetch(t *testing.T) {
	registrySrv, pingHits, tokenHits := newManifestListRegistry(t)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	httpClient := httpsToHTTPClient()

	client := NewGenericClient(httpClient, u.Host)
	meta, err := client.GetImageMetadata(context.Background(), "org/repo", "latest", "linux/amd64")
	if err != nil {
		t.Fatalf("Client.GetImageMetadata: %v", err)
	}
	if meta.Image.Digest == "" {
		t.Error("GetImageMetadata: want a resolved Digest, got empty")
	}

	// Two GetManifestRoute calls happen above (list resolution + the
	// platform-specific fetch) — the SAME repository ("org/repo") for
	// both, so gc's per-repository token cache must serve both from a
	// SINGLE Ping + token-exchange round trip, not two.
	if got := atomic.LoadInt32(pingHits); got != 1 {
		t.Errorf("registry Ping hits = %d, want 1 (shared across both GetManifestRoute calls)", got)
	}
	if got := atomic.LoadInt32(tokenHits); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (shared across both GetManifestRoute calls)", got)
	}
}

func TestGenericClient_GetTags_CachesTokenAcrossRepeatedCallsToSameRepo(t *testing.T) {
	registrySrv, pingHits, tokenHits := newManifestListRegistry(t)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	httpClient := httpsToHTTPClient()

	client := NewGenericClient(httpClient, u.Host)
	if _, err := client.GetTags(context.Background(), "org/repo"); err != nil {
		t.Fatalf("Client.GetTags (1st call): %v", err)
	}
	if _, err := client.GetTags(context.Background(), "org/repo"); err != nil {
		t.Fatalf("Client.GetTags (2nd call): %v", err)
	}

	if got := atomic.LoadInt32(pingHits); got != 1 {
		t.Errorf("registry Ping hits = %d, want 1 (cached across repeated calls to the same repo)", got)
	}
	if got := atomic.LoadInt32(tokenHits); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (cached across repeated calls to the same repo)", got)
	}
}

func TestNewGenericClient_SeparateInstances_EachHonorsItsOwnCredentials(t *testing.T) {
	// Documents/confirms that credentials ARE construction-time-only for a
	// SINGLE held genericClient (see genericClient's own doc comment), but
	// that constraint never blocks a caller from using a DIFFERENT
	// credential against the same host: a SECOND, separately-constructed
	// NewGenericClient with its OWN WithCredentials is fully independent
	// of the first — e.g. Dispatcher's own per-call resolution path
	// (dispatcher.go) relies on exactly this (a caller who needs a
	// different identity constructs a separate Client).
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	httpClient := httpsToHTTPClient()

	credsA := genericmodels.Credentials{Username: "user-a", Password: "pass-a"}
	credsB := genericmodels.Credentials{Username: "user-b", Password: "pass-b"}

	clientA := NewGenericClient(httpClient, u.Host, WithCredentials(credsA))
	_, _ = clientA.GetTags(context.Background(), "org/repo")
	wantARaw, err := auth.BasicAuthCodec.Encode(auth.Credentials(credsA))
	if err != nil {
		t.Fatalf("BasicAuthCodec.Encode: %v", err)
	}
	wantA := wantARaw.(string)
	if gotAuth != wantA {
		t.Errorf("clientA call Authorization = %q, want %q", gotAuth, wantA)
	}

	// gotAuth is overwritten by the SAME server var on this 2nd call —
	// a SEPARATELY constructed Client with a DIFFERENT WithCredentials
	// must still use ITS OWN credential, independent of clientA.
	clientB := NewGenericClient(httpClient, u.Host, WithCredentials(credsB))
	_, _ = clientB.GetTags(context.Background(), "org/repo")
	wantBRaw, err := auth.BasicAuthCodec.Encode(auth.Credentials(credsB))
	if err != nil {
		t.Fatalf("BasicAuthCodec.Encode: %v", err)
	}
	wantB := wantBRaw.(string)
	if gotAuth != wantB {
		t.Errorf("clientB call (different WithCredentials) Authorization = %q, want %q — a separate Client must honor its own credentials", gotAuth, wantB)
	}
}
