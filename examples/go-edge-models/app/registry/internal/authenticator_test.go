package internal

// This file consolidates all authentication-related tests — covering
// authenticator.go (this package's own Authenticator type, the auth-flow
// functions built on it, and its token-exchange route). TestParseChallenge
// covers parseChallenge (pure function, no I/O). The remaining tests are the
// deliberate, narrowly-scoped exception to this package's otherwise
// IO-free unit test design: verifying auth behavior (Basic-auth
// credential injection, Authenticator.CredentialFor memoization/error
// propagation) requires observing HTTP headers/request counts on real
// requests, so a minimal local mock stands in just for these behaviors.
// The end-to-end auth flow against REAL registries (Docker Hub, GHCR,
// MCR) is covered by app/registry's own registry_integration_test.go
// instead.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
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
		t.Error("parseChallenge: want error for non-Bearer challenge")
	}
}

// credRoundTripFunc rewrites "https" to "http" — authenticate/
// Authenticator always dial "https://<registryHost>", but httptest.Server
// serves plain HTTP.
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

// newPingClient builds a *rest.Client attached to registryHost, with
// httpClient as its transport's HTTPClient — the SAME shape every
// self-contained client builds internally. Used directly by
// newTestAuthenticator (below, defined once this file needed both a
// plain *rest.Client AND a full *Authenticator in different tests) to
// build the *Authenticator authenticate's own unit tests now exercise
// via its REAL method receiver, not a hand-rolled *rest.Client passed as
// a loose parameter.
func newPingClient(httpClient *http.Client, registryHost string) *rest.Client {
	c := rest.NewClient()
	_ = c.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{
		HTTPClient: httpClient, BaseURL: RegistryBaseURL(registryHost),
	}))
	return c
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
	a := newTestAuthenticator(client, u.Host)

	creds := &auth.Credentials{Username: "gh-user", Password: "ghp_examplePAT"}
	if _, err := a.authenticate(context.Background(), "org/private-repo", creds, nil); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	raw, err := auth.BasicAuthCodec.Encode(auth.Credentials{Username: creds.Username, Password: creds.Password})
	if err != nil {
		t.Fatalf("BasicAuthCodec.Encode: %v", err)
	}
	want := raw.(string)
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
	a := newTestAuthenticator(client, u.Host)

	if _, err := a.authenticate(context.Background(), "org/public-repo", nil, nil); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent to token endpoint = %q, want empty (anonymous)", gotAuth)
	}
}

// newCountingCredentialCheckRegistry is like newCredentialCheckRegistry but
// also counts how many times each endpoint (registry Ping, auth-realm
// token exchange) is hit — used to verify Authenticator.CredentialFor's
// per-repository token cache actually suppresses repeat network round
// trips.
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

func newTestAuthenticator(httpClient *http.Client, host string) *Authenticator {
	pingClient := newPingClient(httpClient, host)
	return NewAuthenticator(pingClient, httpClient, host)
}

func TestAuthenticator_CredentialFor_MemoizesAcrossMultipleCalls(t *testing.T) {
	registrySrv, _, pingHits, tokenHits := newCountingCredentialCheckRegistry(t)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()
	a := newTestAuthenticator(client, u.Host)

	if _, err := a.CredentialFor(context.Background(), "org/repo", nil); err != nil {
		t.Fatalf("CredentialFor (1st call): %v", err)
	}
	if _, err := a.CredentialFor(context.Background(), "org/repo", nil); err != nil {
		t.Fatalf("CredentialFor (2nd call): %v", err)
	}

	if got := atomic.LoadInt32(pingHits); got != 1 {
		t.Errorf("registry Ping hits = %d, want 1 (memoized)", got)
	}
	if got := atomic.LoadInt32(tokenHits); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (memoized)", got)
	}
}

func TestAuthenticator_CredentialFor_DifferentRepos_EachAuthenticatesIndependently(t *testing.T) {
	registrySrv, _, pingHits, tokenHits := newCountingCredentialCheckRegistry(t)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()
	a := newTestAuthenticator(client, u.Host)

	if _, err := a.CredentialFor(context.Background(), "org/repo-a", nil); err != nil {
		t.Fatalf("CredentialFor(repo-a): %v", err)
	}
	if _, err := a.CredentialFor(context.Background(), "org/repo-b", nil); err != nil {
		t.Fatalf("CredentialFor(repo-b): %v", err)
	}

	if got := atomic.LoadInt32(pingHits); got != 2 {
		t.Errorf("registry Ping hits = %d, want 2 (one per distinct repository)", got)
	}
	if got := atomic.LoadInt32(tokenHits); got != 2 {
		t.Errorf("token endpoint hits = %d, want 2 (one per distinct repository)", got)
	}
}

// TestAuthenticator_CredentialFor_NoAuthNeeded_ReturnsEmptyToken proves
// the anonymous-access case returns an empty token rather than
// hand-building a nil http.Header directly — BearerAuthClientMW's own
// NewOmitEmptyHeaderParam-declared merge field is what actually omits
// the Authorization header downstream from this empty value, not this
// method itself.
func TestAuthenticator_CredentialFor_NoAuthNeeded_ReturnsEmptyToken(t *testing.T) {
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
	a := newTestAuthenticator(client, u.Host)

	token, err := a.CredentialFor(context.Background(), "org/repo", nil)
	if err != nil {
		t.Fatalf("CredentialFor: %v", err)
	}
	if token != "" {
		t.Errorf("token = %q, want empty (no auth required)", token)
	}
}

func TestAuthenticator_CredentialFor_PropagatesAuthError(t *testing.T) {
	registrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(registrySrv.Close)

	u, err := url.Parse(registrySrv.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := httpsToHTTPClient()
	a := newTestAuthenticator(client, u.Host)

	_, err = a.CredentialFor(context.Background(), "org/repo", nil)
	if err == nil {
		t.Fatal("CredentialFor: want error, got nil")
	}
	var authErr RegistryAuthError
	if !errors.As(err, &authErr) {
		t.Errorf("CredentialFor error = %v, want RegistryAuthError", err)
	}

	// Second invocation must return the SAME memoized error, not attempt
	// the request again.
	_, err2 := a.CredentialFor(context.Background(), "org/repo", nil)
	if !errors.As(err2, &authErr) {
		t.Errorf("CredentialFor (2nd call) error = %v, want RegistryAuthError", err2)
	}
}
