package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wantManifestAcceptHeader is the EXACT Accept header value this module
// has always sent for GetManifestRoute calls — byte-for-byte identical to
// the original hand-built acceptManifestTypes constant that used to live
// in app/registry/internal/manifest.go, now produced instead by
// regmodels.AcceptManifestTypesDeclaration (attached directly to
// GetManifestRoute). This test proves the enum-backed Middleware
// refactor introduced no behavioral change against a real HTTP request.
const wantManifestAcceptHeader = "application/vnd.docker.distribution.manifest.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.index.v1+json"

// anonymousSingleManifestServer serves a fixed single-platform manifest
// response for "org/repo:latest" with NO WWW-Authenticate challenge
// (anonymous pull), capturing the Accept header the manifest request
// carried into gotAccept.
func anonymousSingleManifestServer(t *testing.T, gotAccept *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc("/v2/org/repo/manifests/latest", func(w http.ResponseWriter, r *http.Request) {
		*gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Docker-Content-Digest", "sha256:"+strings.Repeat("ab", 32))
		_, _ = w.Write([]byte(`{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json",` +
			`"config":{"mediaType":"application/vnd.docker.container.image.v1+json","digest":"sha256:` + strings.Repeat("cd", 32) + `","size":10},` +
			`"layers":[{"mediaType":"application/vnd.docker.image.rootfs.diff.tar.gzip","digest":"sha256:` + strings.Repeat("ef", 32) + `","size":100}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestGetImageMetadata_SendsExpectedAcceptHeader confirms GetManifestRoute
// (via regmodels.AcceptManifestTypesDeclaration, attached directly to the
// route) sends the EXACT, byte-for-byte same Accept header value this
// module always has — a real HTTP request assertion, not just a codec
// unit test, proving the Middleware-based refactor changed nothing
// observable over the wire.
func TestGetImageMetadata_SendsExpectedAcceptHeader(t *testing.T) {
	var gotAccept string
	srv := anonymousSingleManifestServer(t, &gotAccept)
	host := strings.TrimPrefix(srv.URL, "http://")
	httpClient := httpsToHTTPClient()

	client := NewGenericClient(httpClient, host)
	if _, err := client.GetImageMetadata(context.Background(), "org/repo", "latest", ""); err != nil {
		t.Fatalf("Client.GetImageMetadata: %v", err)
	}
	if gotAccept != wantManifestAcceptHeader {
		t.Errorf("Accept header = %q, want %q", gotAccept, wantManifestAcceptHeader)
	}
}
