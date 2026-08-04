package registry

import (
	"context"
	"errors"
	"net/http"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/docker/registry/internal"
	"github.com/DaniDeer/go-codex/route"
)

// This file holds EVERYTHING related to authenticating against a
// registry — the Bearer-token challenge/token exchange flow (parseChallenge,
// authenticate, bearerCredentialFunc), the optional Basic-auth escape
// hatch for private repositories (Option/WithCredentials), and the
// Format* helpers specific to that flow. This file's error types
// (RegistryAuthChallengeError/RegistryAuthError) live in the sibling
// errors.go alongside client.go's error types — see errors.go's file
// doc comment. client.go deliberately has NO auth logic of its own —
// GetTags/GetImageMetadata just call authenticate() (same package, no
// import needed) and pass the resulting token to bearerCredentialFunc.
//
// EVERY request/response aspect below flows through a route + codec —
// zero manual HTTP request building, zero manual response parsing
// anywhere in this file. The WWW-Authenticate challenge is decoded via
// internal.WWWAuthenticateCodec (parseChallenge is a thin wrapper around
// its Decode); the Bearer/Basic Authorization header values are built via
// internal.BearerTokenCodec/internal.BasicAuthCodec (FormatBearerToken/
// FormatBasicAuth are thin wrappers around their Encode direction); the
// Docker Distribution auth-scope string is built via
// internal.DockerScopeCodec (FormatDockerScope). None of these require a
// caller to import the internal package.
//
// authenticate's Ping/401-detection step is a plain nethttp.CallHandle
// call — reading the WWW-Authenticate challenge header on the 401
// response uses nethttp.UnexpectedStatusError.Header, a declarative
// escape hatch added to adapters/nethttp for exactly this class of
// problem: a response header only present on a non-2xx response, which
// rest.NewRequiredResponseHeaderParam's success-path-only merge cannot
// reach. This file performs no I/O of its own beyond calling
// nethttp.Call/CallHandle — every request/response is route+codec driven.

// ── Format helpers (Challenge / DockerScope / Bearer / Basic) ─────────────────

// FormatChallenge reconstructs a WWW-Authenticate Bearer challenge header
// value from its realm/service/scope parameters — a thin wrapper around
// internal.ChallengeCodec.Encode. Exported so callers building mock
// registry/auth servers for tests or demos can construct a valid
// challenge header without hand-concatenating
// `Bearer realm="...",service="...",scope="..."` themselves, and without
// needing to know about the internal.Challenge type.
func FormatChallenge(realm, service, scope string) (string, error) {
	raw, err := internal.ChallengeCodec.Encode(internal.Challenge{Realm: realm, Service: service, Scope: scope})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// FormatDockerScope reconstructs a Docker Distribution auth scope string
// from its resourceType/name/actions parameters — a thin wrapper around
// internal.DockerScopeCodec.Encode. Exported so callers building mock
// auth servers, or requesting a scope with custom actions, can construct
// a valid scope string without hand-concatenating
// "type:name:action1,action2" themselves.
func FormatDockerScope(resourceType, name string, actions []string) (string, error) {
	raw, err := internal.DockerScopeCodec.Encode(internal.DockerScope{ResourceType: resourceType, Name: name, Actions: actions})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// FormatBearerToken formats token as an "Authorization: Bearer <token>"
// header value — a thin wrapper around internal.BearerTokenCodec.Encode,
// which never fails for a plain string, so this returns just the
// formatted string (no error) for ergonomic call sites.
func FormatBearerToken(token string) string {
	raw, _ := internal.BearerTokenCodec.Encode(token)
	return raw.(string)
}

// FormatBasicAuth formats username/password as an "Authorization: Basic
// <base64>" header value — a thin wrapper around
// internal.BasicAuthCodec.Encode.
func FormatBasicAuth(username, password string) (string, error) {
	raw, err := internal.BasicAuthCodec.Encode(internal.BasicCredentials{Username: username, Password: password})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// ── Options ───────────────────────────────────────────────────────────────────

// Option configures optional GetTags/GetImageMetadata behavior. The zero
// value (no options passed) preserves the exact anonymous-pull behavior
// these functions have always had — Option is purely additive.
type Option func(*options)

type options struct {
	credentials *Credentials
}

func resolveOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithCredentials supplies Basic-auth credentials for the auth-token
// exchange step — see Credentials' doc comment (types.go) for when this
// is needed (private repositories on registries that require Basic auth
// to mint a Bearer token).
func WithCredentials(creds Credentials) Option {
	return func(o *options) { o.credentials = &creds }
}

// ── Auth challenge parsing ────────────────────────────────────────────────────

// parseChallenge decodes header's "WWW-Authenticate" entry into an
// internal.Challenge (RFC 6750 / Docker Distribution's auth spec: realm/
// service/scope parameters). Thin wrapper around
// internal.WWWAuthenticateCodec.Decode — the header extraction AND the
// parsing both happen inside that single codec Decode call, not a plain
// Header.Get(...) here. Returns RegistryAuthChallengeError wrapping the
// codec's own parse/validation error (missing "Bearer " prefix,
// missing/invalid "realm").
func parseChallenge(header http.Header) (internal.Challenge, error) {
	ch, err := internal.WWWAuthenticateCodec.Decode(header)
	if err != nil {
		return internal.Challenge{}, RegistryAuthChallengeError{Header: header.Get("WWW-Authenticate"), Err: err}
	}
	return ch, nil
}

// ── authenticate ──────────────────────────────────────────────────────────
// authenticate probes registryHost's base endpoint (GET /v2/) and, if it
// requires auth (401 + WWW-Authenticate challenge), fetches a Bearer token
// scoped to "repository:<repository>:pull" from the challenge's realm via
// GetTokenRoute (a normal, fully declarative nethttp.CallHandle call).
// Returns "" (no error) when the registry does not require auth. creds is
// nil for anonymous pulls (the default); when non-nil, its Basic-auth
// value is sent on the token-exchange request only (never on the
// subsequent Bearer-authenticated GetTagsRoute/GetManifestRoute calls) —
// see Credentials' doc comment (types.go) for when this is needed.
func authenticate(ctx context.Context, httpClient *http.Client, registryHost, repository string, creds *Credentials) (string, error) {
	baseURL := registryBaseURL(registryHost)
	pingHandle := PingRoute.ClientHandle()
	_, err := nethttp.CallHandle(ctx, httpClient, baseURL, pingHandle, struct{}{}, nethttp.CallOptions{})
	if err == nil {
		return "", nil // 2xx — registry requires no auth for this request.
	}

	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		return "", RegistryAuthError{Registry: registryHost, Err: err}
	}

	// nethttp.UnexpectedStatusError.Header is the declarative escape hatch
	// for a response header only present on a non-2xx response (the
	// success-path response-header merge, rest.NewRequiredResponseHeaderParam,
	// only applies to 2xx responses). parseChallenge (WWWAuthenticateCodec
	// under the hood) extracts AND decodes the "WWW-Authenticate" entry in
	// one codec Decode call — no manual HTTP request/response handling, no
	// plain Header.Get(...), anywhere in this function.
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
	scope, err := FormatDockerScope("repository", repository, []string{"pull"})
	if err != nil {
		return "", err
	}

	tokenOpts := nethttp.CallOptions{}
	if creds != nil {
		basicAuth, err := FormatBasicAuth(creds.Username, creds.Password)
		if err != nil {
			return "", err
		}
		tokenOpts.ExtraHeaders = http.Header{"Authorization": []string{basicAuth}}
	}

	tokenHandle := GetTokenRoute.ClientHandle()
	tr, err := nethttp.CallHandle(ctx, httpClient, challenge.Realm, tokenHandle,
		GetTokenReq{Service: challenge.Service, Scope: scope}, tokenOpts)
	if err != nil {
		return "", RegistryAuthError{Registry: registryHost, Err: err}
	}
	if tr.Token != "" {
		return tr.Token, nil
	}
	return tr.AccessToken, nil
}

// bearerCredentialFunc returns a nethttp.CallOptions.CredentialFunc that
// supplies the Authorization: Bearer header for token — the declarative
// replacement for setting CallOptions.ExtraHeaders by hand. Invoked
// automatically by nethttp.Call/CallHandle for any route declaring
// RouteMeta.Security (see routes.go's bearerAuthSecurity). Returns an
// empty header (no-op) when token is "" (registry requires no auth).
func bearerCredentialFunc(token string) func(context.Context, []route.SecurityRequirement) (http.Header, error) {
	return func(context.Context, []route.SecurityRequirement) (http.Header, error) {
		if token == "" {
			return nil, nil
		}
		h := make(http.Header, 1)
		h.Set("Authorization", FormatBearerToken(token))
		return h, nil
	}
}
