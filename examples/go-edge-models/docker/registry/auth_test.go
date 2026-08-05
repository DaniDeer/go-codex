package registry

// This file consolidates all authentication-related tests — mirroring
// the client.go/auth.go source split. TestParseChallenge covers
// parseChallenge (pure function, no I/O). The remaining tests are the
// deliberate, narrowly-scoped exception to this package's otherwise
// IO-free unit test design: verifying auth behavior (Basic-auth
// credential injection, CredentialFunc memoization/error propagation)
// requires observing HTTP headers/request counts on real requests, and
// there is no private registry credential available (or appropriate) in
// this environment to exercise this against a real registry, so a
// minimal local mock stands in just for these behaviors. It
// intentionally does NOT rebuild the full mock-registry apparatus
// removed from registry_test.go (Round 88) — only a bare-minimum Ping
// (401 challenge) + token endpoint, optionally with request counters
// (newCountingCredentialCheckRegistry) to verify
// NewAuthCredentialFunc's memoization. The end-to-end auth flow against
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

	challenge, err := FormatChallenge(authSrv.URL+"/token", "test-registry", "")
	if err != nil {
		t.Fatalf("FormatChallenge: %v", err)
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
	if _, err := authenticate(context.Background(), client, u.Host, "org/private-repo", creds); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	want, err := FormatBasicAuth(creds.Username, creds.Password)
	if err != nil {
		t.Fatalf("FormatBasicAuth: %v", err)
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

	if _, err := authenticate(context.Background(), client, u.Host, "org/public-repo", nil); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent to token endpoint = %q, want empty (anonymous)", gotAuth)
	}
}

// newCountingCredentialCheckRegistry is like newCredentialCheckRegistry but
// also counts how many times each endpoint (registry Ping, auth-realm
// token exchange) is hit — used to verify NewAuthCredentialFunc's
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

	challenge, err := FormatChallenge(authSrv.URL+"/token", "test-registry", "")
	if err != nil {
		t.Fatalf("FormatChallenge: %v", err)
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

	credFn := NewAuthCredentialFunc(client, u.Host, "org/repo")

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

	credFn := NewAuthCredentialFunc(client, u.Host, "org/repo")
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

	credFn := NewAuthCredentialFunc(client, u.Host, "org/repo")
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
