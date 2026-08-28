package registry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	c "github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/examples/go-edge-models/internal/registry"
	regmodels "github.com/DaniDeer/go-codex/examples/go-edge-models/models/docker/registry"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// This file holds EVERYTHING related to authenticating against a
// registry — the Bearer-token challenge/token exchange flow (parseChallenge,
// authenticate, newAuthCredentialFunc), the optional Basic-auth escape
// hatch for private repositories (Option/WithCredentials), and the
// format* helpers specific to that flow, PLUS every security scheme/
// requirement value and route (getTokenRoute) this package's auth flow
// needs, PLUS this file's own error types (RegistryAuthChallengeError/
// RegistryAuthError).
//
// EVERYTHING in this file is UNEXPORTED and stays that way deliberately —
// this package's public surface is radically reduced (see doc.go for the
// full 4-item list): the routes (ping.go's PingRoute, gettags.go's
// GetTagsRoute, getimagemetadata.go's GetManifestRoute), the client
// functions built on top of them (gettags.go's GetTags,
// getimagemetadata.go's GetImageMetadata), the domain structs/codecs a
// caller needs to use either (imageref.go/credentials.go plus each
// operation's own file), and the MCP tools wrapping the client functions
// (GetTagsTool/GetImageMetadataTool). A caller never needs
// newAuthCredentialFunc, the format* helpers, or getTokenRoute directly —
// GetTags/GetImageMetadata already wire the whole auth flow internally.
//
// This file deliberately has NO client-wiring logic of its own —
// GetTags/GetImageMetadata just build a newAuthCredentialFunc(...) value
// (same package) and pass it straight through as
// nethttp.CallOptions.CredentialFunc; the Ping + challenge + token-exchange
// dance runs LAZILY, inside that credentialFunc, only when a secured route
// (GetTagsRoute/GetManifestRoute, both declaring RouteMeta.Security) is
// actually called — never up front. newAuthCredentialFunc memoizes its
// result (sync.Once) for its own
// lifetime, so reusing the SAME credentialFunc value across multiple
// secured calls (e.g. GetImageMetadata's two GetManifestRoute calls while
// resolving a manifest list) triggers the dance only ONCE.
//
// EVERY request/response aspect below flows through a route + codec —
// zero manual HTTP request building, zero manual response parsing
// anywhere in this file. The WWW-Authenticate challenge is decoded via
// internal.WWWAuthenticateCodec (parseChallenge is a thin wrapper around
// its Decode); the Bearer/Basic Authorization header values are built via
// internal.BearerTokenCodec/internal.BasicAuthCodec (formatBearerToken/
// formatBasicAuth are thin wrappers around their Encode direction); the
// Docker Distribution auth-scope string is built via
// internal.DockerScopeCodec (formatDockerScope). None of these require a
// caller to import the internal package.
//
// BOTH credential schemes this package uses (Bearer, on
// GetTagsRoute/GetManifestRoute; Basic, on getTokenRoute's token-exchange
// call) flow through the IDENTICAL declarative mechanism — a
// middleware.Middleware whose Security field declares the scheme
// (newAuthMiddleware for Bearer, built from regmodels.BearerAuthScheme/
// BearerAuthSchemeName; getTokenRoute's own basicAuthScheme below for
// Basic) chained onto its route via .Use(...), paired with a
// credentialFunc-shaped Fn passed to nethttp.Call/CallHandle's variadic.
// Neither scheme is ever injected via CallOptions.ExtraHeaders — that
// manual bypass was removed once WithSecurityScheme shipped, so
// nethttp.Call's client-side credential-format check (validating the
// credentialFunc-returned header against the route's declared Codec
// before sending, symmetric with the server-side check) applies to both.
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

// formatChallenge reconstructs a WWW-Authenticate Bearer challenge
// header value from its realm/service/scope parameters — a thin wrapper
// around internal.ChallengeCodec.Encode. Unexported: this package's own
// authenticate() is the only caller that ever needs to construct this
// header shape (a real registry SERVER, not this client, is what emits
// it) — no external caller needs this.
func formatChallenge(realm, service, scope string) (string, error) {
	raw, err := internal.ChallengeCodec.Encode(internal.Challenge{Realm: realm, Service: service, Scope: scope})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// formatDockerScope reconstructs a Docker Distribution auth scope string
// from its resourceType/name/actions parameters — a thin wrapper around
// internal.DockerScopeCodec.Encode. Unexported: only authenticate()
// (building the "repository:<repository>:pull" scope for a token
// request) needs this.
func formatDockerScope(resourceType, name string, actions []string) (string, error) {
	raw, err := internal.DockerScopeCodec.Encode(internal.DockerScope{ResourceType: resourceType, Name: name, Actions: actions})
	if err != nil {
		return "", err
	}
	return raw.(string), nil
}

// formatBearerToken formats token as an "Authorization: Bearer <token>"
// header value — a thin wrapper around internal.BearerTokenCodec.Encode,
// which never fails for a plain string, so this returns just the
// formatted string (no error) for ergonomic call sites.
func formatBearerToken(token string) string {
	raw, _ := internal.BearerTokenCodec.Encode(token)
	return raw.(string)
}

// formatBasicAuth formats username/password as an "Authorization: Basic
// <base64>" header value — a thin wrapper around
// internal.BasicAuthCodec.Encode.
func formatBasicAuth(username, password string) (string, error) {
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
	credentials           *regmodels.Credentials
	credentialsByRegistry regmodels.RegistryCredentials
	observer              stats.Observer
}

func resolveOptions(opts []Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithCredentials supplies Basic-auth credentials for the auth-token
// exchange step — see Credentials' doc comment (credentials.go) for when this
// is needed (private repositories on registries that require Basic auth
// to mint a Bearer token).
func WithCredentials(creds regmodels.Credentials) Option {
	return func(o *options) { o.credentials = &creds }
}

// WithCredentialsByRegistry supplies a full registry-host → Credentials
// map — GetTags/GetImageMetadata pick the right entry automatically based
// on the image URL's resolved registry host, so the SAME options value
// can be reused unchanged across calls to different registries. See
// RegistryCredentials' doc comment (credentials.go) for the exact set of
// supported registry-host keys and the WithCredentials escape hatch for
// others. If BOTH WithCredentials and WithCredentialsByRegistry are
// supplied to the same call, WithCredentials wins (it is the more
// specific, single-registry override).
func WithCredentialsByRegistry(creds regmodels.RegistryCredentials) Option {
	return func(o *options) { o.credentialsByRegistry = creds }
}

// WithObserver is usually NOT needed: GetTags/GetImageMetadata's internal
// nethttp.CallHandle invocations already fall back to
// stats.ObserverFromContext(ctx) whenever no explicit Observer is set —
// the SAME context-based default every nethttp.Call caller gets. Just
// attach an observer to ctx once, before calling GetTags/GetImageMetadata:
//
//	ctx = stats.WithObserver(ctx, obs)
//	tags, err := GetTags(ctx, httpClient, imageURL) // already observed
//
// WithObserver exists as an EXPLICIT, per-call override on top of that —
// for a caller who wants a DIFFERENT Observer for one specific
// GetTags/GetImageMetadata call without touching a shared ctx (mirrors
// nethttp.CallOptions.Observer's own "explicit always wins over context"
// precedence). It applies to EVERY nethttp.CallHandle invocation this
// package makes on behalf of one call — the auth-realm Ping + token
// exchange (authenticate, when the registry requires auth) AND the actual
// GetTagsRoute/GetManifestRoute calls, giving RecordRequest metrics
// (method, path, status, duration) for all of them. A nil/absent Observer
// here preserves the ctx-based default described above unchanged.
func WithObserver(obs stats.Observer) Option {
	return func(o *options) { o.observer = obs }
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
// getTokenRoute (a normal, fully declarative nethttp.CallHandle call).
// Returns "" (no error) when the registry does not require auth. creds is
// nil for anonymous pulls (the default); when non-nil, its Basic-auth
// value is sent on the token-exchange request ONLY (never on the
// subsequent Bearer-authenticated GetTagsRoute/GetManifestRoute calls) —
// see Credentials' doc comment (credentials.go) for when this is needed. Both
// the Basic-auth credential here and the Bearer token GetTags/
// GetImageMetadata use afterward flow through the SAME credentialFunc-shaped Fn +
// Security-declaring middleware.Middleware mechanism (this file's own
// basicAuthScheme below / newAuthMiddleware's regmodels.BearerAuthScheme
// above) — no CallOptions.ExtraHeaders injection anywhere in this
// package.
func authenticate(ctx context.Context, httpClient *http.Client, registryHost, repository string, creds *regmodels.Credentials, obs stats.Observer) (string, error) {
	baseURL := registryBaseURL(registryHost)
	pingHandle := regmodels.PingRoute.ClientHandle()
	_, err := nethttp.CallHandle(ctx, httpClient, baseURL, pingHandle, struct{}{}, nethttp.CallOptions{Observer: obs})
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
	scope, err := formatDockerScope("repository", repository, []string{"pull"})
	if err != nil {
		return "", err
	}

	// Basic-auth credentials (when supplied) flow through a
	// credential-providing middleware.Middleware, the SAME declarative
	// mechanism newAuthCredentialFunc's Bearer credential uses below on
	// GetTagsRoute/GetManifestRoute — not a manual CallOptions.ExtraHeaders
	// injection. getTokenRoute declares Security unconditionally (this
	// file's own basicAuthSecurity), so this middleware's Fn is invoked
	// automatically by nethttp.CallHandle whenever creds is non-nil; when
	// creds is nil (anonymous exchange), no middleware is attached at all —
	// an absent credential-providing middleware on a secured route is
	// never an error, so the request goes out exactly as it always has: no
	// Authorization header at all.
	tokenOpts := nethttp.CallOptions{Observer: obs}
	var tokenMws []middleware.ClientMiddleware
	if creds != nil {
		tokenMws = append(tokenMws, middleware.ClientMiddleware{
			Fn: func(context.Context, []route.SecurityRequirement) (http.Header, error) {
				basicAuth, err := formatBasicAuth(creds.Username, creds.Password)
				if err != nil {
					return nil, err
				}
				h := make(http.Header, 1)
				h.Set("Authorization", basicAuth)
				return h, nil
			},
		})
	}

	tokenHandle := getTokenRoute.ClientHandle()
	tr, err := nethttp.CallHandle(ctx, httpClient, challenge.Realm, tokenHandle,
		getTokenReq{Service: challenge.Service, Scope: scope}, tokenOpts, tokenMws...)
	if err != nil {
		return "", RegistryAuthError{Registry: registryHost, Err: err}
	}

	if tr.Token != "" {
		return tr.Token, nil
	}
	return tr.AccessToken, nil
}

// credentialFunc names nethttp.CallOptions.CredentialFunc's function type
// for readability at call sites that need to store or pass one around
// (newAuthCredentialFunc's return type, embedded in newAuthMiddleware's
// Fn field above) instead of repeating the full inline function type.
type credentialFunc = func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)

// newAuthCredentialFunc returns a credentialFunc that authenticates
// against registryHost for repository LAZILY — on first invocation by
// nethttp.Call/CallHandle, which only happens for a route whose
// ClientHandle carries a matching Security requirement (see
// newAuthMiddleware above, which wraps this as its Fn alongside the
// "bearerAuth" Security declaration) — and MEMOIZES the result (via
// sync.Once) for the lifetime of the returned closure. Reusing the SAME
// credentialFunc value across multiple
// CallHandle invocations against secured routes therefore performs the
// Ping + WWW-Authenticate-challenge + token-exchange dance only ONCE, no
// matter how many secured calls are made with it (see
// getimagemetadata.go's GetImageMetadata, which reuses one credentialFunc across two
// GetManifestRoute calls while resolving a manifest list).
//
// Returns a nil header (no-op — the request goes out unauthenticated)
// when the registry turns out to require no auth at all (authenticate
// returns "" for a 2xx Ping). Returns any authentication error
// (RegistryAuthChallengeError/RegistryAuthError) unchanged once observed --
// matching authenticate's own error behavior, just deferred to first use.
//
// Unexported: newAuthMiddleware (above) is the only caller — GetTags/
// GetImageMetadata (gettags.go/getimagemetadata.go) go through THAT,
// never this directly. This package's public surface is deliberately
// just routes + client functions + domain structs/codecs — a caller
// never needs to build their own credentialFunc directly.
func newAuthCredentialFunc(httpClient *http.Client, registryHost, repository string, opts ...Option) credentialFunc {
	o := resolveOptions(opts)
	// A single WithCredentials value is the more specific override and
	// wins over WithCredentialsByRegistry when both are supplied. When
	// only the map is supplied, look up the entry for THIS call's
	// resolved registryHost — registryHost is already a parameter here
	// (passed by GetTags/GetImageMetadata from ParseImageRef's resolved
	// ref.Registry), so there is no ordering issue.
	creds := o.credentials
	if creds == nil {
		if cr, ok := o.credentialsByRegistry[registryHost]; ok {
			creds = &cr
		}
	}
	var (
		once    sync.Once
		token   string
		authErr error
	)
	return func(ctx context.Context, _ []route.SecurityRequirement) (http.Header, error) {
		once.Do(func() {
			token, authErr = authenticate(ctx, httpClient, registryHost, repository, creds, o.observer)
		})
		if authErr != nil {
			return nil, authErr
		}
		if token == "" {
			return nil, nil // registry requires no auth for this request.
		}
		h := make(http.Header, 1)
		h.Set("Authorization", formatBearerToken(token))
		return h, nil
	}
}

// newAuthMiddleware is the CLIENT-side fulfillment of GetTagsRoute/
// GetManifestRoute's "bearerAuth" requirement — those routes declare the
// requirement THEMSELVES (regmodels.BearerAuthDeclaration, attached via
// .Use(...) — see their own doc comments); this middleware only supplies
// the credential, attached via .UseClient(...) in gettags.go's GetTags
// and getimagemetadata.go's GetImageMetadata:
//
//	authMw := newAuthMiddleware(httpClient, ref.Registry, ref.Repository, opts...)
//	handle := regmodels.GetTagsRoute.UseClient(authMw).ClientHandle()
//
// A [middleware.ClientMiddleware] structurally cannot carry a Security
// declaration — this function's return type reflects that: it wraps
// ONLY newAuthCredentialFunc's lazy, memoized (via sync.Once)
// Ping/challenge/token-exchange dance, nothing else.
func newAuthMiddleware(httpClient *http.Client, registryHost, repository string, opts ...Option) middleware.ClientMiddleware {
	return middleware.ClientMiddleware{
		Name: "registry-auth",
		Fn:   newAuthCredentialFunc(httpClient, registryHost, repository, opts...),
	}
}

// ---- basicAuth scheme declaration, and getTokenRoute ----
//
// regmodels.BearerAuthScheme/BearerAuthSchemeName (GetTagsRoute/
// GetManifestRoute's shared scheme metadata, consumed above by
// newAuthMiddleware) live in the sibling models/docker/registry package's
// security.go instead — they are part of those routes' DECLARED CONTRACT,
// not this file's auth-flow implementation. basicAuthSecurity/
// basicAuthScheme below stay HERE because getTokenRoute (also declared
// here) has no legitimate standalone caller outside this file's own
// authenticate() function — auth-flow plumbing, not part of the
// externally-facing contract.

// basicAuthSecurity declares that getTokenRoute accepts Basic-auth
// credentials — set as RouteMeta.Security below so
// [nethttp.CallOptions.CredentialFunc] is invoked automatically by
// [nethttp.Call]/[nethttp.CallHandle] whenever auth.go's authenticate()
// supplies one (private-repo Credentials). Declaring this UNCONDITIONALLY
// is safe even for anonymous (no-Credentials) token exchanges: a nil/no-op
// credentialFunc on a secured route is never an error (see auth.go) — the
// request simply goes out without a Basic-auth header in that case,
// exactly as it always has.
var basicAuthSecurity = []route.SecurityRequirement{{"basicAuth": nil}}

// basicAuthScheme declares the "basicAuth" scheme's spec metadata and a
// non-empty-string format Codec, attached to getTokenRoute below via
// rest.WithSecurityScheme — getTokenRoute has a single caller (this
// file's own authenticate) so a plain manual declaration is appropriate
// here, unlike GetTagsRoute/GetManifestRoute's middleware-based
// declaration above (see newAuthMiddleware) — giving auth.go's Basic-auth
// token-exchange credential the
// SAME client-side format-validation safety net Bearer credentials already
// get, instead of the manual CallOptions.ExtraHeaders injection this
// package used before.
var basicAuthScheme = rest.SecurityScheme{
	SecurityScheme: route.BasicScheme(),
}.WithCodec(c.String().Refine(validate.NonEmptyString))

// getTokenReq is getTokenRoute's request — Service and Scope merge
// automatically into the service/scope query parameters via
// nethttp.CallHandle.
type getTokenReq struct {
	Service string
	Scope   string
}

// getTokenRoute is the registry auth-token endpoint. Its path is
// deliberately EMPTY: the auth realm is an arbitrary full URL that may be
// on a COMPLETELY DIFFERENT HOST than the registry itself (e.g. Docker
// Hub's registry is registry-1.docker.io but its auth realm is
// auth.docker.io/token) — authenticate() (this file) passes the realm URL (parsed from the
// WWW-Authenticate challenge header) as the baseURL for this route's
// nethttp.Call, so the route's own path template must contribute nothing
// beyond that. Req is getTokenReq, whose Service/Scope fields merge into
// the service/scope query params automatically via nethttp.CallHandle —
// both OptionalField since real registries vary in which of the two they
// actually populate in a challenge.
var getTokenRoute = rest.NewRoute[getTokenReq, internal.TokenResponse](
	"GET", "",
	c.Struct[getTokenReq](), internal.TokenResponseCodec,
	rest.RouteMeta{
		OperationID:    "getToken",
		Summary:        "Fetch a Bearer token from the registry's auth realm",
		RespSchemaName: "TokenResponse",
		Security:       basicAuthSecurity,
	},
	rest.WithSecurityScheme("basicAuth", basicAuthScheme),
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
