package internal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry/auth"
	"github.com/DaniDeer/go-codex/stats"
)

// This file holds EVERYTHING about ONE registry host's Bearer-token
// auth-flow MECHANICS: the *Authenticator type itself, its constructor,
// its public CredentialFor entry point, the Ping/challenge/token-exchange
// flow that backs it (authenticate, parseChallenge), the format* helpers
// specific to that flow, the token-exchange request/route
// (getTokenReq/a.tokenRoute), and this package's auth error types
// (RegistryAuthChallengeError/RegistryAuthError). All of it lives in one
// file because authenticate has EXACTLY one caller — CredentialFor,
// itself a method on *Authenticator — splitting the type from the flow
// it exists to drive only obscured that 1:1 relationship. EACH consuming
// package (app/registry, .../dockerhub, .../ghcr, .../mcr) re-exports
// RegistryAuthChallengeError/RegistryAuthError as a public type alias, so
// a caller catching an auth error can `errors.As` on the SAME underlying
// type no matter which concrete Client it called through.
//
// "basicAuth"-scheme bound middleware (basicAuthClientMW) lives in
// middleware.go instead, alongside BearerAuthClientMW — both are HANDLERS
// for a models-declared security scheme (auth.BasicAuthDeclaration/
// auth.BearerAuthDeclaration, models/docker/registry/auth/security.go),
// not auth-flow state, so they belong with the other Client-attached
// middleware, not here.
//
// EVERY request/response aspect below flows through a route + codec —
// zero manual HTTP request building, zero manual response parsing
// anywhere in this file. The WWW-Authenticate challenge is decoded via
// auth.WWWAuthenticateCodec (parseChallenge is a thin wrapper around its
// Decode); the Bearer token header value is built via
// auth.BearerAuthDeclaration's own codec-declared merge field, reusing
// auth.BearerTokenCodec internally; Basic's Authorization header value is
// built via auth.BasicAuthCodec (formatBasicAuth is a thin wrapper around
// its Encode direction); the Docker Distribution auth-scope string is
// built via auth.DockerScopeCodec (formatDockerScope).
//
// authenticate's Ping/401-detection step reuses the owning Authenticator's
// PERSISTENT *rest.Client (a.client) and its own registered pingRoute
// (a.pingRoute, regmodels.PingRoute — see NewAuthenticator below) —
// reading the WWW-Authenticate challenge header on the 401 response uses
// nethttp.UnexpectedStatusError.Header, a declarative escape hatch for a
// response header only present on a non-2xx response, which
// rest.NewRequiredResponseHeaderParam's success-path-only merge cannot
// reach.

// tokenCacheEntry memoizes ONE (repository, credential-identity) pair's
// bearer token (or auth error) for an Authenticator's whole lifetime —
// entry.once.Do ensures authenticate runs AT MOST ONCE per cache key, no
// matter how many concurrent/sequential calls ask for the same
// repository.
type tokenCacheEntry struct {
	once  sync.Once
	token string
	err   error
}

// Authenticator holds ONE registry host's Bearer-token auth-flow state —
// a persistent *rest.Client (reused for the Ping step — see authenticate
// below), its own bound pingRoute (regmodels.PingRoute, built ONCE at
// construction — mirrors how each owning Client registers its own
// tagsRoute/manifestRoute as fields, though PingRoute needs no
// BoundClientMiddleware of its own: it declares no security requirement,
// unlike GetTagsRoute/GetManifestRoute) and tokenRoute (the token-exchange
// endpoint, also built ONCE at construction — see its own doc comment for
// why it is a field but, unlike pingRoute, is never registered on
// a.client), a plain *http.Client (for the token-exchange call against
// the dynamically-discovered realm, which can never join a
// fixed-baseURL persistent client), and a per-repository token cache.
// EACH self-contained registry client (app/registry/dockerhub/.../ghcr/
// .../mcr) and app/registry's own generic-fallback Client owns EXACTLY
// ONE Authenticator, created once at construction — this is the shared
// MECHANICS extracted so "fully self-contained client" doesn't mean
// "triplicated auth-flow implementation."
type Authenticator struct {
	client     *rest.Client
	pingRoute  rest.Route[struct{}, struct{}]
	tokenRoute rest.Route[getTokenReq, auth.TokenResponse]
	httpClient *http.Client
	host       string

	mu    sync.Mutex
	cache map[string]*tokenCacheEntry
}

// NewAuthenticator returns an Authenticator for host, dispatching its
// Ping step through client (the SAME persistent *rest.Client the owning
// Client's GetTagsRoute/GetManifestRoute calls use) and
// regmodels.PingRoute (registered once, here, as a's own pingRoute
// field) and its token-exchange step through httpClient (a fresh
// transport per discovery — see authenticate below for why the dynamic
// realm rules out reusing client).
//
// Explicitly calls client.RegisterRoute(regmodels.PingRoute) — not
// strictly required (Client.Call auto-registers a route the first time
// it's called), but mirrors events.Client's own explicit registration
// workflow for consistency, and keeps PingRoute's registration co-located
// with where pingRoute itself is set, rather than repeated once per
// owning Client (see docs/features/http-client.md's "Auto-registration
// vs. explicit Client.RegisterRoute" for the full rationale). A failure
// here would only ever signal a genuine programmer error (PingRoute is a
// hardcoded, correctly-typed value) — fatal, matching this package's
// existing Attach-error convention (see each owning Client's own
// constructor).
//
// tokenRoute is ALSO built once, here, as a's own field — mirroring
// pingRoute — but deliberately gets NO RegisterRoute call: tokenRoute's
// own call (see authenticate below) never goes through client at all; it
// is dispatched via rest.CallWithTransport against a transport built
// fresh per call, because the auth realm host is only known dynamically
// (parsed from each WWW-Authenticate challenge) and can differ per call.
// docs/features/rest-redirects.md documents CallWithTransport used
// directly (no *rest.Client) as deliberately "registry-free" — registering
// tokenRoute on client would be meaningless (client never dispatches it)
// and would only pollute client's redirect-target registry with a route
// it never actually calls.
func NewAuthenticator(client *rest.Client, httpClient *http.Client, host string) *Authenticator {
	if err := client.RegisterRoute(regmodels.PingRoute); err != nil {
		panic("app/registry/internal: NewAuthenticator: RegisterRoute(PingRoute): " + err.Error())
	}
	return &Authenticator{
		client:     client,
		pingRoute:  regmodels.PingRoute,
		tokenRoute: newTokenRoute(),
		httpClient: httpClient,
		host:       host,
		cache:      make(map[string]*tokenCacheEntry),
	}
}

// CredentialFor returns the Bearer token for repository, given creds (nil
// for anonymous access), authenticating (Ping + challenge + token
// exchange, via authenticate below) AT MOST ONCE per (repository,
// credential-identity) pair for a's entire lifetime — repeated calls for
// the SAME repository (e.g. a manifest-list resolution's two
// GetManifestRoute calls) hit the cache instead of re-running the whole
// dance.
func (a *Authenticator) CredentialFor(ctx context.Context, repository string, creds *auth.Credentials) (string, error) {
	obs := stats.ObserverFromContext(ctx)

	key := repository + "|" + credentialFingerprint(creds)
	a.mu.Lock()
	entry, ok := a.cache[key]
	if !ok {
		entry = &tokenCacheEntry{}
		a.cache[key] = entry
	}
	a.mu.Unlock()

	entry.once.Do(func() {
		entry.token, entry.err = a.authenticate(ctx, repository, creds, obs)
	})
	return entry.token, entry.err
}

// credentialFingerprint returns a stable, opaque string identifying
// creds' identity (or "anonymous" for nil) — the credential half of
// CredentialFor's cache key. Not a security boundary (this is purely an
// in-memory map key, never persisted or transmitted), so a plain SHA-256
// hex digest is more than sufficient.
func credentialFingerprint(creds *auth.Credentials) string {
	if creds == nil {
		return "anonymous"
	}
	sum := sha256.Sum256([]byte(creds.Username + "\x00" + creds.Password))
	return hex.EncodeToString(sum[:])
}

// ── Format helpers (Challenge / DockerScope / Basic auth) ───────────────────

// formatChallenge reconstructs a WWW-Authenticate challenge header value
// from its realm/service/scope parameters — a thin wrapper around
// auth.ChallengeCodec.Encode. Unexported: this package's own
// authenticate() is the only caller that ever needs to construct this
// header shape (a real registry SERVER, not this client, is what emits
// it).
func formatChallenge(realm, service, scope string) (string, error) {
	raw, err := auth.ChallengeCodec.Encode(auth.Challenge{Realm: realm, Service: service, Scope: scope})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// formatDockerScope reconstructs a Docker Distribution auth scope string
// from its resourceType/name/actions parameters — a thin wrapper around
// auth.DockerScopeCodec.Encode. Unexported: only authenticate()
// (building the "repository:<repository>:pull" scope for a token
// request) needs this.
func formatDockerScope(resourceType, name string, actions []string) (string, error) {
	raw, err := auth.DockerScopeCodec.Encode(auth.DockerScope{ResourceType: resourceType, Name: name, Actions: actions})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// formatBasicAuth formats username/password as an "Authorization: Basic
// <base64>" header value — a thin wrapper around auth.BasicAuthCodec.Encode.
func formatBasicAuth(username, password string) (string, error) {
	raw, err := auth.BasicAuthCodec.Encode(auth.Credentials{Username: username, Password: password})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// ── Auth challenge parsing ────────────────────────────────────────────────────

// parseChallenge decodes header's "WWW-Authenticate" entry into an
// auth.Challenge (RFC 6750 / Docker Distribution's auth spec: realm/
// service/scope parameters). Thin wrapper around
// auth.WWWAuthenticateCodec.Decode. Returns RegistryAuthChallengeError
// wrapping the codec's own parse/validation error (missing "Bearer "
// prefix, missing/invalid "realm").
func parseChallenge(header http.Header) (auth.Challenge, error) {
	ch, err := auth.WWWAuthenticateCodec.Decode(header)
	if err != nil {
		return auth.Challenge{}, RegistryAuthChallengeError{Header: header.Get("WWW-Authenticate"), Err: err}
	}
	return ch, nil
}

// ── authenticate ──────────────────────────────────────────────────────────
// authenticate probes a's host's base endpoint (GET /v2/) via a.client
// and a.pingRoute (the owning Authenticator's own PERSISTENT *rest.Client
// and registered PingRoute — reusing the SAME transport
// GetTagsRoute/GetManifestRoute calls use) and, if it requires auth (401
// + WWW-Authenticate challenge), fetches a bearer token scoped to
// "repository:<repository>:pull" from the challenge's realm via
// a.tokenRoute. a.tokenRoute's own token-exchange call stays a one-off
// rest.CallWithTransport against a transport built fresh per call
// (a.httpClient) — the auth realm varies per challenge, so it can never
// join a fixed-baseURL persistent client the way
// PingRoute/GetTagsRoute/GetManifestRoute do. Returns "" (no error) when
// the registry does not require auth. creds is nil for anonymous pulls;
// when non-nil, its Basic-auth value is sent on the token-exchange
// request ONLY (never on the subsequent Bearer-authenticated
// GetTagsRoute/GetManifestRoute calls). A method on *Authenticator (its
// ONLY caller is CredentialFor, also on *Authenticator) rather than a
// free function — a.client/a.pingRoute/a.tokenRoute/a.httpClient/a.host
// replace what used to be 4 separate loose parameters.
func (a *Authenticator) authenticate(ctx context.Context, repository string, creds *auth.Credentials, obs stats.Observer) (string, error) {
	_, err := a.client.Call(ctx, a.pingRoute, struct{}{}, rest.ClientCallOptions{Observer: obs})
	if err == nil {
		return "", nil // 2xx — registry requires no auth for this request.
	}

	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		return "", RegistryAuthError{Registry: a.host, Err: err}
	}

	challenge, err := parseChallenge(statusErr.Header)
	if err != nil {
		return "", err
	}
	// The challenge comes from the REPOSITORY-AGNOSTIC base "/v2/" ping —
	// it cannot legitimately carry a scope naming OUR repository (some
	// registries omit Scope entirely here, e.g. Docker Hub; others, e.g.
	// GHCR, include a non-empty but placeholder/example value such as
	// "repository:user/image:pull" that must NOT be reused verbatim).
	// The correct scope for a pull is always self-built from the
	// repository we are actually calling for — challenge.Scope is
	// deliberately ignored here, regardless of whether it is empty.
	scope, err := formatDockerScope("repository", repository, []string{"pull"})
	if err != nil {
		return "", err
	}

	tokenOpts := rest.ClientCallOptions{Observer: obs}
	tokenRoute := a.tokenRoute.ClientBoundMW(basicAuthClientMW(creds))

	tokenHandle := tokenRoute.ClientHandle()
	tokenTransport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: a.httpClient, BaseURL: challenge.Realm})
	tr, err := rest.CallWithTransport(ctx, tokenTransport, tokenHandle,
		getTokenReq{Service: challenge.Service, Scope: scope}, tokenOpts)
	if err != nil {
		return "", RegistryAuthError{Registry: a.host, Err: err}
	}

	if tr.Token != "" {
		return tr.Token, nil
	}
	return tr.AccessToken, nil
}

// ---- getTokenRoute (the token-exchange endpoint) ----

// getTokenReq is the token-exchange route's request — Service and Scope
// merge automatically into the service/scope query parameters via
// rest.CallWithTransport.
type getTokenReq struct {
	Service string
	Scope   string
}

// newTokenRoute constructs the registry auth-token endpoint, assigned
// ONCE to Authenticator's own tokenRoute field by NewAuthenticator (see
// its own doc comment for why this field, unlike pingRoute, is never
// registered via Client.RegisterRoute). Its path is deliberately EMPTY:
// the auth realm is an arbitrary full URL that may be on a COMPLETELY
// DIFFERENT HOST than the registry itself (e.g. Docker Hub's registry is
// registry-1.docker.io but its auth realm is auth.docker.io/token) —
// authenticate (above) passes the realm URL (parsed from the
// WWW-Authenticate challenge header) as the baseURL for this route's
// rest.CallWithTransport, so the route's own path template must
// contribute nothing beyond that.
func newTokenRoute() rest.Route[getTokenReq, auth.TokenResponse] {
	return rest.NewRoute[getTokenReq, auth.TokenResponse](
		"GET", "",
		c.Struct[getTokenReq](), auth.TokenResponseCodec,
		rest.RouteMeta{
			OperationID:    "getToken",
			Summary:        "Fetch an access token from the registry's auth realm",
			RespSchemaName: "TokenResponse",
		},
		rest.NewOptionalQueryParam("service",
			c.String(),
			func(r getTokenReq) string { return r.Service },
			func(r *getTokenReq, v string) { r.Service = v },
		),
		rest.NewOptionalQueryParam("scope",
			c.String(),
			func(r getTokenReq) string { return r.Scope },
			func(r *getTokenReq, v string) { r.Scope = v },
		),
	)
}

// ── Auth errors ────────────────────────────────────────────────────────────────────

// RegistryAuthChallengeError is returned when a registry's 401 response
// carries a malformed or missing WWW-Authenticate header.
type RegistryAuthChallengeError struct {
	Header string
	Err    error
}

func (e RegistryAuthChallengeError) Error() string {
	return fmt.Sprintf("parse WWW-Authenticate challenge %q: %s", e.Header, e.Err)
}
func (e RegistryAuthChallengeError) Unwrap() error { return e.Err }
func (e RegistryAuthChallengeError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("header", e.Header), slog.Any("cause", e.Err))
}

// RegistryAuthError is returned when the auth realm's token endpoint call
// fails, or the ping request itself fails for a reason other than a clean
// 401 challenge.
type RegistryAuthError struct {
	Registry string
	Err      error
}

func (e RegistryAuthError) Error() string {
	return fmt.Sprintf("authenticate with registry %q: %s", e.Registry, e.Err)
}
func (e RegistryAuthError) Unwrap() error { return e.Err }
func (e RegistryAuthError) LogValue() slog.Value {
	return slog.GroupValue(slog.String("registry", e.Registry), slog.Any("cause", e.Err))
}
