package registry

// This file consolidates all authentication-related tests — mirroring
// the imageref.go/auth.go source split. TestParseChallenge covers
// parseChallenge (pure function, no I/O). The remaining tests are the
// deliberate, narrowly-scoped exception to this package's otherwise
// IO-free unit test design: verifying auth behavior (Basic-auth
// credential injection, credentialFunc memoization/error propagation)
// requires observing HTTP headers/request counts on real requests, and
// there is no private registry credential available (or appropriate) in
// this environment to exercise this against a real registry, so a
// minimal local mock stands in just for these behaviors. It
// intentionally does NOT rebuild the full mock-registry apparatus
// removed from registry_test.go (Round 88) — only a bare-minimum Ping
// (401 challenge) + token endpoint, optionally with request counters
// (newCountingCredentialCheckRegistry) to verify
// newAuthCredentialFunc's memoization. The end-to-end auth flow against
// REAL registries (Docker Hub, GHCR, MCR) is covered by
// registry_integration_test.go instead.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/DaniDeer/go-codex/stats"
)

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

// credRoundTripFunc rewrites "https" to "http" — authenticate always
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

// newCredentialCheckRegistry builds a minimal registry (401 + challenge)
// and auth-realm server pair; the auth server records the Authorization
// header it received on each request into *gotAuth.
func newCredentialCheckRegistry(t *testing.T, gotAuth *string) (registrySrv, authSrv *httptest.Server) {
	t.Helper()

	authSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tok"}`))
	}))
	t.Cleanup(authSrv.Close)

	challenge, err := formatChallenge(authSrv.URL+"/token", "test-registry", "")
	if err != nil {
		t.Fatalf("formatChallenge: %v", err)
	}
	registrySrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate", challenge)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(registrySrv.Close)
	return registrySrv, authSrv
}

func TestAuthenticate_WithCredentials_SendsBasicAuthOnTokenExchange(t *testing.T) {
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	creds := &Credentials{Username: "gh-user", Password: "ghp_examplePAT"}
	if _, err := authenticate(context.Background(), client, u.Host, "org/private-repo", creds, nil); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	want, err := formatBasicAuth(creds.Username, creds.Password)
	if err != nil {
		t.Fatalf("formatBasicAuth: %v", err)
	}
	if gotAuth != want {
		t.Errorf("Authorization sent to token endpoint = %q, want %q", gotAuth, want)
	}
}

func TestAuthenticate_NoCredentials_SendsNoAuthorizationOnTokenExchange(t *testing.T) {
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	if _, err := authenticate(context.Background(), client, u.Host, "org/public-repo", nil, nil); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent to token endpoint = %q, want empty (anonymous)", gotAuth)
	}
}

// newCountingCredentialCheckRegistry is like newCredentialCheckRegistry but
// also counts how many times each endpoint (registry Ping, auth-realm
// token exchange) is hit — used to verify newAuthCredentialFunc's
// memoization actually suppresses repeat network round trips.
func newCountingCredentialCheckRegistry(t *testing.T) (registrySrv, authSrv *httptest.Server, pingHits, tokenHits *int32) {
	t.Helper()

	pingHits = new(int32)
	tokenHits = new(int32)

	authSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(tokenHits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"tok"}`))
	}))
	t.Cleanup(authSrv.Close)

	challenge, err := formatChallenge(authSrv.URL+"/token", "test-registry", "")
	if err != nil {
		t.Fatalf("formatChallenge: %v", err)
	}
	registrySrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(pingHits, 1)
		w.Header().Set("WWW-Authenticate", challenge)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(registrySrv.Close)
	return registrySrv, authSrv, pingHits, tokenHits
}

func TestNewAuthCredentialFunc_MemoizesAcrossMultipleCalls(t *testing.T) {
	registrySrv, _, pingHits, tokenHits := newCountingCredentialCheckRegistry(t)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	credFn := newAuthCredentialFunc(client, u.Host, "org/repo")

	if _, err := credFn(context.Background(), nil); err != nil {
		t.Fatalf("credFn (1st call): %v", err)
	}
	if _, err := credFn(context.Background(), nil); err != nil {
		t.Fatalf("credFn (2nd call): %v", err)
	}

	if got := atomic.LoadInt32(pingHits); got != 1 {
		t.Errorf("registry Ping hits = %d, want 1 (memoized)", got)
	}
	if got := atomic.LoadInt32(tokenHits); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (memoized)", got)
	}
}

func TestNewAuthCredentialFunc_NoAuthNeeded_ReturnsNilHeader(t *testing.T) {
	registrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(registrySrv.Close)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	credFn := newAuthCredentialFunc(client, u.Host, "org/repo")
	header, err := credFn(context.Background(), nil)
	if err != nil {
		t.Fatalf("credFn: %v", err)
	}
	if header != nil {
		t.Errorf("header = %v, want nil (no auth required)", header)
	}
}

func TestNewAuthCredentialFunc_PropagatesAuthError(t *testing.T) {
	registrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(registrySrv.Close)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	credFn := newAuthCredentialFunc(client, u.Host, "org/repo")
	_, err = credFn(context.Background(), nil)
	if err == nil {
		t.Fatal("credFn: want error, got nil")
	}
	var authErr RegistryAuthError
	if !errors.As(err, &authErr) {
		t.Errorf("credFn error = %v, want RegistryAuthError", err)
	}

	// Second invocation must return the SAME memoized error, not attempt
	// the request again.
	_, err2 := credFn(context.Background(), nil)
	if !errors.As(err2, &authErr) {
		t.Errorf("credFn (2nd call) error = %v, want RegistryAuthError", err2)
	}
}

// ── RegistryCredentials / WithCredentialsByRegistry ───────────────────────────

func TestWithCredentialsByRegistry_PicksCorrectEntryPerRegistry(t *testing.T) {
	var gotAuthA, gotAuthB string
	registrySrvA, _ := newCredentialCheckRegistry(t, &gotAuthA)
	registrySrvB, _ := newCredentialCheckRegistry(t, &gotAuthB)

	uA, err := url.Parse(registrySrvA.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	uB, err := url.Parse(registrySrvB.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	credsA := Credentials{Username: "user-a", Password: "pass-a"}
	credsB := Credentials{Username: "user-b", Password: "pass-b"}
	byRegistry := RegistryCredentials{uA.Host: credsA, uB.Host: credsB}

	credFnA := newAuthCredentialFunc(client, uA.Host, "org/repo-a", WithCredentialsByRegistry(byRegistry))
	if _, err := credFnA(context.Background(), nil); err != nil {
		t.Fatalf("credFnA: %v", err)
	}
	wantA, err := formatBasicAuth(credsA.Username, credsA.Password)
	if err != nil {
		t.Fatalf("formatBasicAuth: %v", err)
	}
	if gotAuthA != wantA {
		t.Errorf("Authorization sent for registry A = %q, want %q", gotAuthA, wantA)
	}

	credFnB := newAuthCredentialFunc(client, uB.Host, "org/repo-b", WithCredentialsByRegistry(byRegistry))
	if _, err := credFnB(context.Background(), nil); err != nil {
		t.Fatalf("credFnB: %v", err)
	}
	wantB, err := formatBasicAuth(credsB.Username, credsB.Password)
	if err != nil {
		t.Fatalf("formatBasicAuth: %v", err)
	}
	if gotAuthB != wantB {
		t.Errorf("Authorization sent for registry B = %q, want %q", gotAuthB, wantB)
	}
}

func TestWithCredentialsByRegistry_NoMatchingEntry_FallsBackToAnonymous(t *testing.T) {
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	// The map has an entry, but not for THIS registry's host.
	byRegistry := RegistryCredentials{"some-other-host.example.com": {Username: "u", Password: "p"}}

	credFn := newAuthCredentialFunc(client, u.Host, "org/repo", WithCredentialsByRegistry(byRegistry))
	if _, err := credFn(context.Background(), nil); err != nil {
		t.Fatalf("credFn: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent to token endpoint = %q, want empty (anonymous fallback)", gotAuth)
	}
}

func TestWithCredentials_WinsOverWithCredentialsByRegistry(t *testing.T) {
	var gotAuth string
	registrySrv, _ := newCredentialCheckRegistry(t, &gotAuth)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()

	single := Credentials{Username: "single-user", Password: "single-pass"}
	byRegistry := RegistryCredentials{u.Host: {Username: "map-user", Password: "map-pass"}}

	credFn := newAuthCredentialFunc(client, u.Host, "org/repo",
		WithCredentialsByRegistry(byRegistry), WithCredentials(single))
	if _, err := credFn(context.Background(), nil); err != nil {
		t.Fatalf("credFn: %v", err)
	}
	want, err := formatBasicAuth(single.Username, single.Password)
	if err != nil {
		t.Fatalf("formatBasicAuth: %v", err)
	}
	if gotAuth != want {
		t.Errorf("Authorization sent to token endpoint = %q, want %q (WithCredentials should win)", gotAuth, want)
	}
}

func TestRegistryCredentialsCodec_RejectsUnknownRegistryHost(t *testing.T) {
	err := RegistryCredentialsCodec.Validate(RegistryCredentials{
		"quay.io": {Username: "u", Password: "p"},
	})
	if err == nil {
		t.Fatal("RegistryCredentialsCodec.Validate: want error for unknown registry host, got nil")
	}
}

func TestRegistryCredentialsCodec_RoundTrip(t *testing.T) {
	creds := RegistryCredentials{
		dockerHubRegistryHost: {Username: "docker-user", Password: "docker-pass"},
		ghcrRegistryHost:      {Username: "", Password: "ghp_examplePAT"},
		mcrRegistryHost:       {Username: "mcr-user", Password: "mcr-pass"},
	}

	encoded, err := RegistryCredentialsCodec.Encode(creds)
	if err != nil {
		t.Fatalf("RegistryCredentialsCodec.Encode: %v", err)
	}
	decoded, err := RegistryCredentialsCodec.Decode(encoded)
	if err != nil {
		t.Fatalf("RegistryCredentialsCodec.Decode: %v", err)
	}
	if len(decoded) != len(creds) {
		t.Fatalf("decoded = %+v, want %+v", decoded, creds)
	}
	for host, want := range creds {
		if got := decoded[host]; got != want {
			t.Errorf("decoded[%q] = %+v, want %+v", host, got, want)
		}
	}
}

func TestCredentialsCodec_RejectsEmptyPasswordButAllowsEmptyUsername(t *testing.T) {
	// GHCR (and similar) authenticate correctly with an empty/arbitrary
	// username and the PAT carried entirely in Password — Username must
	// stay unconstrained while Password remains required.
	if err := CredentialsCodec.Validate(Credentials{Username: "", Password: "ghp_examplePAT"}); err != nil {
		t.Errorf("CredentialsCodec.Validate with empty Username: want nil error, got %v", err)
	}
	if err := CredentialsCodec.Validate(Credentials{Username: "user", Password: ""}); err == nil {
		t.Error("CredentialsCodec.Validate with empty Password: want error, got nil")
	}
}

// ── WithObserver ──────────────────────────────────────────────────────────────
//
// Pure resolveOptions-level assertion — no HTTP involved (mirrors this
// package's stated IO-free unit-test policy). End-to-end observer firing
// through GetTags/GetImageMetadata's real nethttp.CallHandle invocations
// is demonstrated by examples/go-edge-models/main.go's runRegistryDemo,
// not re-derived here as a mock-server test.
func TestWithObserver_SetsObserverOnOptions(t *testing.T) {
	obs := stats.NoopObserver{}
	o := resolveOptions([]Option{WithObserver(obs)})
	if o.observer != obs {
		t.Errorf("resolveOptions(...).observer = %#v, want %#v", o.observer, obs)
	}
}

func TestWithObserver_AbsentLeavesObserverNil(t *testing.T) {
	o := resolveOptions([]Option{WithCredentials(Credentials{Username: "u", Password: "p"})})
	if o.observer != nil {
		t.Errorf("resolveOptions(...).observer = %#v, want nil", o.observer)
	}
}
