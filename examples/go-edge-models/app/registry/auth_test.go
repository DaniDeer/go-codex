package registry

// This file covers app/registry's OWN auth-related surface — the
// generic Option/WithCredentials/WithCredentialsByRegistry/WithObserver
// resolution for NewGenericClient. The underlying auth-FLOW mechanics
// (challenge parsing, token exchange, per-repository caching) now live in
// the Go-internal app/registry/internal package and are tested there
// directly (white-box) — see that package's own auth_test.go. The
// end-to-end auth flow against REAL registries (Docker Hub, GHCR, MCR) is
// covered by registry_integration_test.go instead.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
	genericmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/generic"
	"github.com/DaniDeer/go-codex/stats"
)

// credRoundTripFunc rewrites "https" to "http" — NewGenericClient always
// dials "https://<registryHost>", but httptest.Server serves plain HTTP.
type credRoundTripFunc func(*http.Request) (*http.Response, error)

func (f credRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func httpsToHTTPClient() *http.Client {
	return &http.Client{
		Transport: credRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			req = req.Clone(req.Context())
			if req.URL.Scheme == "https" {
				req.URL.Scheme = "http"
			}
			return http.DefaultTransport.RoundTrip(req)
		}),
	}
}

// newCredentialCheckRegistry builds a minimal registry (401 + challenge
// on the Ping path, a successful tags/list on every OTHER path) and
// auth-realm server pair; the auth server records the Authorization
// header it received on each request into *gotAuth. This test only
// cares about what Authorization reaches the TOKEN endpoint, so the
// tags/list response always succeeds unconditionally once a (any) Bearer
// token is minted — it does not re-validate the token itself.
func newCredentialCheckRegistry(t *testing.T, gotAuth *string) (registrySrv, authSrv *httptest.Server) {
	t.Helper()

	authSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tok"}`))
	}))
	t.Cleanup(authSrv.Close)

	raw, err := auth.ChallengeCodec.Encode(auth.Challenge{Realm: authSrv.URL + "/token", Service: "test-registry"})
	if err != nil {
		t.Fatalf("ChallengeCodec.Encode: %v", err)
	}
	challenge := raw.(string)

	registrySrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.Header().Set("WWW-Authenticate", challenge)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"org/repo","tags":["1.0.0"]}`))
	}))
	t.Cleanup(registrySrv.Close)
	return registrySrv, authSrv
}

func basicAuthHeader(t *testing.T, username, password string) string {
	t.Helper()
	raw, err := auth.BasicAuthCodec.Encode(auth.Credentials{Username: username, Password: password})
	if err != nil {
		t.Fatalf("BasicAuthCodec.Encode: %v", err)
	}
	return raw.(string)
}

func hostOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return srv.Listener.Addr().String()
}

// ── RegistryCredentials / WithCredentialsByRegistry ───────────────────────────

func TestWithCredentialsByRegistry_PicksCorrectEntryPerRegistry(t *testing.T) {
	var gotAuthA, gotAuthB string
	registrySrvA, _ := newCredentialCheckRegistry(t, &gotAuthA)
	registrySrvB, _ := newCredentialCheckRegistry(t, &gotAuthB)

	hostA := hostOf(t, registrySrvA)
	hostB := hostOf(t, registrySrvB)
	client := httpsToHTTPClient()

	credsA := genericmodels.Credentials{Username: "user-a", Password: "pass-a"}
	credsB := genericmodels.Credentials{Username: "user-b", Password: "pass-b"}
	byRegistry := genericmodels.RegistryCredentials{hostA: credsA, hostB: credsB}

	gcA := NewGenericClient(client, hostA, WithCredentialsByRegistry(byRegistry))
	if _, err := gcA.GetTags(context.Background(), "org/repo-a"); err != nil {
		t.Fatalf("GetTags(A): %v", err)
	}
	if want := basicAuthHeader(t, credsA.Username, credsA.Password); gotAuthA != want {
		t.Errorf("Authorization sent for registry A = %q, want %q", gotAuthA, want)
	}

	gcB := NewGenericClient(client, hostB, WithCredentialsByRegistry(byRegistry))
	if _, err := gcB.GetTags(context.Background(), "org/repo-b"); err != nil {
		t.Fatalf("GetTags(B): %v", err)
	}
	if want := basicAuthHeader(t, credsB.Username, credsB.Password); gotAuthB != want {
		t.Errorf("Authorization sent for registry B = %q, want %q", gotAuthB, want)
	}
}

func TestWithCredentialsByRegistry_NoMatchingEntry_FallsBackToAnonymous(t *testing.T) {
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	host := hostOf(t, registrySrv)
	client := httpsToHTTPClient()

	// The map has an entry, but not for THIS registry's host.
	byRegistry := genericmodels.RegistryCredentials{"some-other-host.example.com": {Username: "u", Password: "p"}}

	gc := NewGenericClient(client, host, WithCredentialsByRegistry(byRegistry))
	if _, err := gc.GetTags(context.Background(), "org/repo"); err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent to token endpoint = %q, want empty (anonymous fallback)", gotAuth)
	}
}

func TestWithCredentials_WinsOverWithCredentialsByRegistry(t *testing.T) {
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	host := hostOf(t, registrySrv)
	client := httpsToHTTPClient()

	single := genericmodels.Credentials{Username: "single-user", Password: "single-pass"}
	byRegistry := genericmodels.RegistryCredentials{host: {Username: "map-user", Password: "map-pass"}}

	gc := NewGenericClient(client, host,
		WithCredentialsByRegistry(byRegistry), WithCredentials(single))
	if _, err := gc.GetTags(context.Background(), "org/repo"); err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if want := basicAuthHeader(t, single.Username, single.Password); gotAuth != want {
		t.Errorf("Authorization sent to token endpoint = %q, want %q (WithCredentials should win)", gotAuth, want)
	}
}

// ── WithObserver ──────────────────────────────────────────────────────────────
//
// Pure resolveOptions-level assertion — no HTTP involved (mirrors this
// package's stated IO-free unit-test policy). End-to-end observer firing
// through GetTags/GetImageMetadata's real rest.Client.Call invocations is
// demonstrated by examples/go-edge-models/main.go's runRegistryDemo, not
// re-derived here as a mock-server test.
func TestWithObserver_SetsObserverOnOptions(t *testing.T) {
	obs := stats.NoopObserver{}
	o := resolveOptions([]Option{WithObserver(obs)})
	if o.observer != obs {
		t.Errorf("resolveOptions(...).observer = %#v, want %#v", o.observer, obs)
	}
}

func TestWithObserver_AbsentLeavesObserverNil(t *testing.T) {
	o := resolveOptions([]Option{WithCredentials(genericmodels.Credentials{Username: "u", Password: "p"})})
	if o.observer != nil {
		t.Errorf("resolveOptions(...).observer = %#v, want nil", o.observer)
	}
}
