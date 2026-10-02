# Security & Authentication

> See also: [`route` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/route)
>
> Runnable demos: [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) (bearer JWT + scopes, both chi and net/http servers) · [`examples/events-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/events-api) (`demo_security_subscribemw.go`: message-level security across mqtt v3/mqtt5/zeromq; `demo_connect_level_security.go`: connect-level `SecuredClient`)
>
> `api/mcp` has NO security methods by deliberate, permanent design — MCP
> security is handled by the host application (its own OAuth flow, or
> transport-level auth on the SSE/stdio transport itself), not by go-codex.
> This is the one place go-codex's security model is intentionally
> asymmetric with REST/events/reqreply.

go-codex documents security requirements in the spec and provides declarative hooks for runtime enforcement — **the library does not import any crypto or JWT library**. For REST, a security scheme is declared ONCE, via `middleware.SecurityScheme(schemeName, scheme, scopes, codec)`/`rest.FromSecurityScheme(schemeName, rest.SecurityScheme, scopes)` (bridging an existing `rest.SecurityScheme` value), attached to the route via `Route.Use(mw)` — there is no builder-level scheme registry, and `rest.WithSecurityScheme` was REMOVED (there is no metadata-only registration anymore; every declared scheme is a real requirement) — and the SAME declaration is consumed identically by both the server (`Route.Register`/`RegisterHandle`) and the client (`Route.ClientHandle`), so one route definition gets IDENTICAL credential-format enforcement on both ends. Runtime credential validation itself is attached PER-ROUTE, paired against the declared `middleware.Middleware`: server-side via `Route.HandleMW(mw, fn)`, client-side via `Route.ClientMW(mw, fn)` (see "HTTP client — credential-providing ClientMW" below).

## Connection-level vs message-level security

REST is stateless per-request — every `rest.CallWithTransport`/incoming request IS a
fresh connection-equivalent, so there's no "connection level" distinct
from "message level" at all.

MQTT (both 3.1.1 and 5.0) is different: connections are long-lived and
stateful. The caller calls `Connect()` ONCE, entirely outside go-codex
(`MQTTClient`/`pahomqtt.Client` deliberately expose no connection-lifecycle
methods — go-codex never manages `Connect()`/`Disconnect()`), then reuses
that SAME client across many `Subscribe`/`Publish`/`Serve`/`Call`
invocations for the program's lifetime. This means TWO independent
security layers exist:

1. **Connection-level** — broker ACLs (Mosquitto `acl_file`, EMQX rules,
   etc.) tied to CONNECT-time username/password/TLS client cert,
   configured ENTIRELY in the caller's own connect code, before any
   `events.Channel`/`reqreply.Route` is touched. This already works today
   with ZERO go-codex involvement, and is sufficient for "one connection =
   one identity" topologies (e.g. one IoT device, one dedicated broker
   connection, one CONNECT-time credential).

   go-codex adds an OPTIONAL, codec-validated FORMAT check on top via
   `mqtt5.SecuredClient`/`mqtt.SecuredClient` (protocol-symmetric across
   MQTT 3.1.1 + MQTT5, unlike the message-level model below, since CONNECT
   username/password exists in both protocol versions). Validation happens
   ONCE, synchronously, at construction — never per message:

   ```go
   var connectBearerAuth = mqtt5.ConnectSecurityScheme{SecurityScheme: route.BasicScheme()}.
       WithCodec(codex.String().Refine(validate.MinLen(8)))

   client := paho.NewClient(...)
   if _, err := client.Connect(ctx, &paho.Connect{
       Username: "svc-account", Password: []byte(token),
   }); err != nil { /* handle */ }

   secured, err := mqtt5.NewSecuredClient(client, connectBearerAuth, "svc-account", token)
   if err != nil { /* malformed credential — client is never used */ }

   // secured is a drop-in replacement — every NewSubscribeTransport/
   // NewPublishTransport/Serve/Call call site below is UNCHANGED from how
   // it would look with the raw client.
   transport := mqtt5.NewSubscribeTransport[T](secured, router, opts)
   err = events.SubscribeHandle(ctx, sub, transport, fn)
   ```

   `NewSecuredClient` combines `username`+`password` into a single
   `"username:password"` string before validating (the MQTT CONNECT packet
   carries them as two separate wire fields — this combined form is a
   VALIDATION-TIME representation only, never transmitted; mirrors
   `examples/go-edge-models/app/registry`'s `internal.BasicAuthCodec`
   convention for HTTP Basic auth). On failure, returns
   `ConnectSecurityCredentialError` and the underlying client is never
   touched. `*SecuredClient` satisfies `MQTTClient`/`pahomqtt.Client`
   transparently via Go struct embedding — no other code changes needed.
2. **Message-level** (`WithSecurityScheme` + a security-shaped
   `SubscribeMW`/`PublishMW`-paired implementation, shipped for MQTT v3,
   MQTT5, AND ZeroMQ — see below) — opt-in, per-message codec-validated
   credentials, needed when ONE connection carries traffic for MULTIPLE
   logical identities (e.g. a gateway relaying many devices' messages
   over one shared broker connection) and broker ACLs alone can't
   distinguish between them.

Both layers are independent and composable — use connection-level ACLs for
coarse-grained, per-connection authorization, and a message-level
`SubscribeMW`/`PublishMW`-paired implementation for fine-grained,
per-message claims within a single shared connection. Neither requires
the other.

### TLS / transport encryption is always the caller's own concern

TLS (transport encryption) is a DIFFERENT axis from everything above —
it protects the wire, not the message/identity — and go-codex
deliberately has NO opinion on it anywhere: it is never part of an
OpenAPI/AsyncAPI spec (neither format has a "requires TLS" field; that's
implied by the `servers` entry's own scheme, e.g. `https://`), and never
touches go-codex's security-scheme model. Confirmed per transport:

- **REST**: client-side, the caller supplies its own `*http.Client` to
  `nethttp.Call`/`rest.CallWithTransport` — TLS is entirely
  `http.Transport.TLSClientConfig`'s concern (client certs, custom
  `RootCAs`, etc.), go-codex never constructs an `http.Client` itself.
  Server-side, `b.Attach(nethttp.NewServerTransport(...))`/`b.Attach(chiadapter.NewServerTransport(...))` just wire a `mux`/`router`;
  the caller builds their own `*http.Server` (with or without
  `TLSConfig`) around it — go-codex's own `Server.Serve` explicitly
  documents this ("a caller needing full control over TLS/timeouts/etc.
  builds their own `*http.Server`").
- **mqtt5/mqtt**: the optional `Connect()` convenience wrapper
  (`ConnectOptions.TLS *tls.Config`) is a PURE pass-through to Go's own
  `tls.Dialer`/`pahomqtt.ClientOptions.SetTLSConfig` — not part of any
  spec/security-scheme mechanism, purely a connection-dial convenience.
  Callers needing more control bypass it entirely and construct their
  own already-connected client, same as `SecuredClient` above.
- **zeromq**: no TLS/CURVE surface exists at all — `adapters/zeromq` has
  ZERO dependency on any concrete ZeroMQ library (not even in `go.mod`);
  it is a pure `FramedSocket` interface the caller implements from
  whatever binding they choose (e.g. `github.com/pebbe/zmq4`). CURVE
  (libzmq's own transport-encryption/auth mechanism) would be configured
  entirely on the caller's own concrete socket, before wrapping it as a
  `FramedSocket` — a deliberate, permanent, researched decision (CURVE/ZAP
  is the caller's own concern; see
  [D-0004](../design/d-0004-reqreply-workflow-simplification.md)'s
  Addendum for the full rationale), not something go-codex exposes today.

## Security schemes (REST)

```go
import (
    "github.com/DaniDeer/go-codex/api/rest"
    "github.com/DaniDeer/go-codex/middleware"
    "github.com/DaniDeer/go-codex/route"
    "github.com/DaniDeer/go-codex/validate"
)

// Declare each scheme ONCE as a shared value — Go's ordinary "declare once,
// reference everywhere" idiom, no builder-level registry needed.
var bearerAuthScheme = rest.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken)) // format check before HandleMW/before send

var bearerAuth = rest.FromSecurityScheme("bearerAuth", bearerAuthScheme, []string{"write:users"})

var apiKeyAuthScheme = rest.SecurityScheme{
    SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
}
var apiKeyAuth = rest.FromSecurityScheme("apiKey", apiKeyAuthScheme, nil)

b := rest.NewServer(rest.Info{Title: "User API", Version: "1.0.0"})

// Attach the scheme directly to every route that needs it, via Use.
createUser := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec,
    rest.RouteMeta{OperationID: "createUser"},
).Use(bearerAuth)
err := createUser.Register(b) // error only — see "Runtime enforcement" below for HandleMW
```

`Server.OpenAPISpec()` aggregates `components.securitySchemes` automatically from every registered route's own `.Use()`-attached declarations — no separate builder-level step needed. When two routes declare the same scheme name with different values, the last-registered route wins (no error); define the scheme once as a shared value (as above) to avoid relying on this.

Built-in scheme constructors:

```go
route.BearerScheme("JWT")                          // Authorization: Bearer <token>
route.BasicScheme()                                 // Authorization: Basic <base64>
route.APIKeyScheme("X-API-Key", "header")           // header-based API key
route.APIKeyScheme("api_key", "query")              // query param API key
route.OAuth2Scheme(route.OAuthFlows{...})           // OAuth 2.0
route.OpenIDConnectScheme("https://.../.well-known")// OIDC discovery
```

## Global and per-route security

`Server.AddGlobalSecurity`/`RouteMeta.Security` answer "which routes require auth" — a SEPARATE, unchanged concern from a `.Use()`-attached scheme declaration ("what does a named scheme look like"). `RouteMeta.Security`'s `nil` (inherit global) and `[]route.SecurityRequirement{}` (explicit opt-out) states are UNCHANGED by the security-declaration redesign — only the THIRD state (a non-empty, MANUALLY-set `Security` paired with metadata-only registration) was removed, since `middleware.SecurityScheme`/`rest.FromSecurityScheme` now populate `RouteMeta.Security` automatically as part of `.Use()`:

```go
// Global security — applies to all operations by default.
b.AddGlobalSecurity(route.Require("bearerAuth"))

// Per-route: bearerAuth is the SAME shared Middleware value from the
// section above — .Use() populates RouteMeta.Security automatically.
createUser := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec,
    rest.RouteMeta{OperationID: "createUser"},
).Use(bearerAuth)
err := createUser.Register(b)

// Explicitly public — empty slice overrides global security (unaffected
// by the security-declaration redesign; no .Use() call needed at all).
publicRoute := rest.NewRoute[struct{}, Info]("GET", "/health",
    codex.Empty, infoCodec,
    rest.RouteMeta{
        Security: []route.SecurityRequirement{}, // no auth required
    },
)
err = publicRoute.Register(b)
```

## Security requirement shapes — single, OR, AND, opt-out, inherit

`route.SecurityRequirement` is `map[string][]string` (scheme name → required
OAuth2 scopes) — a single map can hold MULTIPLE scheme names as keys, and
`RouteMeta.Security` is a SLICE of these maps. This gives two independent
axes of combination:

- **Within one map**: every key (scheme name) must be satisfied together — **AND**.
- **Across the slice**: any one map fully satisfied is enough — **OR**.

`route.Require(name, scopes...)` builds a SINGLE-KEY map. Each
`middleware.SecurityScheme`/`rest.FromSecurityScheme` value, attached via
`.Use()`, contributes exactly ONE scheme-name key, merged AND-wise into
`RouteMeta.Security`'s FIRST map entry — calling `.Use()` twice with two
DIFFERENT scheme declarations naturally produces the **AND** case (shape
2 below) with zero manual `RouteMeta.Security` needed at all.

**1. Single scheme required** (the baseline shown above):

```go
route := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec, rest.RouteMeta{OperationID: "createUser"},
).Use(bearerAuth) // bearerAuth = rest.FromSecurityScheme("bearerAuth", ..., []string{"write:users"})
```

**2. AND — both required together** (two `.Use()` calls, each declaring a
DIFFERENT scheme, merge into ONE map — no manual `RouteMeta.Security`
needed):

```go
route := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec, rest.RouteMeta{OperationID: "createUser"},
).Use(bearerAuth).Use(apiKeyAuth)
// Descriptor.Security == [{"bearerAuth": [...], "apiKey": []}] — AND
```

**3. Explicit opt-out** (empty slice, NOT nil — overrides global security
for just this route; no `.Use()` call needed):

```go
rest.RouteMeta{Security: []route.SecurityRequirement{}},
```

**4. Inherit global** (omit `Security` entirely — the default):

```go
rest.RouteMeta{OperationID: "health"}, // no Security field at all
```

**5. Same scheme, different scopes per route** — each route builds its
OWN `middleware.Middleware` value from the SAME shared `rest.SecurityScheme`
(the scheme's SPEC metadata is reusable; the SCOPES are per-declaration):

```go
// route A — needs the "profile" scope:
routeA := rest.NewRoute[...](...).Use(rest.FromSecurityScheme("bearerAuth", bearerAuthScheme, []string{"profile"}))

// route B — needs the "admin" scope:
routeB := rest.NewRoute[...](...).Use(rest.FromSecurityScheme("bearerAuth", bearerAuthScheme, []string{"admin"}))
```

> **Known limitation — OR across TWO scheme-declaring `.Use()` calls is
> NOT directly expressible.** `applySecurityDeclarations` ALWAYS merges
> every `.Use()`-attached scheme into `RouteMeta.Security`'s FIRST map
> entry (AND-only) — it has no mechanism to route a scheme declaration
> into a SEPARATE, alternative map entry. A manually-set
> `RouteMeta.Security` with multiple OR'd map entries is NOT corrupted by
> `.Use()` for schemes it does NOT also declare, but attaching `.Use()`
> for a scheme ALSO present in that manual structure still merges an
> extra key into entry `[0]`, producing an unintended shape. If your API
> genuinely needs "scheme A alone OR scheme B alone" (not "A syntax
> AND B"), build the requirement/spec manually via `RouteMeta.Security`
> and register each scheme's OpenAPI metadata without a scope-bearing
> `.Use()` call at all — this is a real, open gap in the current
> declaration surface, not a documented feature; track it before relying
> on it in production.

## Runtime enforcement (nethttp / chi adapters)

```go
route := createUser.WithHandler(handler).WithOptions(nethttp.Options{
    // SecurityFunc is called after Codec format validation passes.
    // Receives the *http.Request and the route's declared security requirements.
    SecurityFunc: func(ctx context.Context, r *http.Request, reqs []route.SecurityRequirement) error {
        token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        return jwtlib.VerifyScopes(token, reqs)
    },
})
route.Register(b)
b.Attach(nethttp.NewServerTransport(nethttp.ServerTransportOptions{Mux: mux, Addr: addr}))
go func() { _ = b.Serve(ctx) }()
```

The adapter enforcement sequence:
1. Extracts the raw credential per scheme type (`Authorization: Bearer <token>`, `X-API-Key: <key>`, etc.)
2. Validates it via `SecurityScheme.Codec` → returns `rest.SecurityCredentialError` + 401 on failure
3. Calls `SecurityFunc` → returns `rest.SecurityError` + 401 on rejection

Routes with `nil Security` (default) trigger enforcement when global security is set.

`rest.CallWithTransport` (client-side) runs the SAME sequence, symmetrically, on the OUTGOING request before it is sent — see "HTTP client — credential-providing ClientMW" below. **`rest.Client.Call` (bound via `Client.Attach(nethttp.NewClientTransport(...))`) runs the SAME enforcement too** — it is full-featured: security/credential `ClientMW`, path/query/header/cookie params, per-call format override, and error-pattern decoding are all supported (see `adapters/nethttp/clienttransport.go`'s own doc comment for confirmation there is no remaining "v1 scope" limitation).

### Codec-backed Security — `HandleMW`/`ClientMW`'s bound path + `GrantedScopes`

A codec-backed `rest.Middleware[In, Out]` (built via `rest.SecurityMiddleware[In, Out]`,
generic over In/Out since docs/roadmap/declarative-middleware-layering.md's
Rollout Phase A) can ALSO carry a Security declaration and be attached via
`Route.HandleMW`/`Route.ClientMW` — the SAME two methods used for the
legacy shape above. `HandleMW`/`ClientMW` detect which shape `fn` is by
its REFLECTED signature (not by `mw`'s type), so attaching either shape
uses the identical method call:

```go
type APIKeyIn struct{ Key string }
type APIKeyOut struct{ GrantedScopes map[string][]string }

apiKeyMw := rest.SecurityMiddleware[APIKeyIn, APIKeyOut]("apiKeyAuth",
    rest.SecurityScheme{SecurityScheme: route.APIKeyScheme("X-Api-Key", "header")}, nil,
).WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
    func(in APIKeyIn) string { return in.Key },
    func(in *APIKeyIn, v string) { in.Key = v },
))

route := createUser.Use(apiKeyMw).HandleMW(apiKeyMw,
    func(ctx context.Context, req *CreateUserReq, in APIKeyIn) (APIKeyOut, error) {
        if !validKey(in.Key) {
            return APIKeyOut{}, errors.New("invalid API key")
        }
        return APIKeyOut{GrantedScopes: map[string][]string{"apiKeyAuth": nil}}, nil
    })
```

`fn` gets `*Req` access (read/enrich, exactly like the generic middleware
mechanism — see [Feature: Codec-Declared Middleware](codec-declared-middleware.md)),
and `In`'s own header/cookie/query merge fields decode declaratively from
the request — no manual `r.Header.Get(...)` anywhere. **`Out` carries the
RESOLVED "conventional field" convention: a field named `GrantedScopes
map[string][]string`**, read by the adapter via reflection and fed into
the SAME `middleware.CheckScopes` call the legacy `SecurityFunc`/credential
path already used — a route requiring specific scopes (not just scheme
presence) checks them identically regardless of which attachment style
supplied the grant. Returning an error from `fn` keeps Security's own
distinct fallback (401, `rest.SecurityError`), not the generic 400
ordinary middleware errors fall back to.

On the CLIENT side, the identical shape detection applies to `ClientMW`:
a credential-supplying `fn` shaped `func(ctx context.Context, req Req) (In, error)`
(Req BY VALUE, matching the route's own Req type) is recognized as the
bound path and dispatched through the SAME merge-field mechanism, instead
of hand-building an `http.Header` value. **`rest.NewOmitEmptyHeaderParam`
is the key building block for an anonymous-access credential Fn** — a
field declared with it OMITS its header entirely when the current value
is the zero value (e.g. an empty token), rather than sending an empty
header value; see `examples/go-edge-models/app/registry/auth.go`'s
`newAuthCredentialFunc` and `examples/go-edge-models/models/docker/registry/security.go`'s
`BearerAuthDeclaration` for a complete, runnable, real-world example of
this exact pattern (a registry client that may or may not need Bearer
auth, decided per-call by whether an actual token was obtained).

**Coverage note — `ports`-facing binding adapters (`IngestAdapter`,
`LatestAdapter`, `SSEAdapter`, and `stream.go`'s `HandlerLatest`/
`PipelineHandler`, both `nethttp` and `chi`) dispatch `HandleMW`-attached
middleware/Security identically to `Server.Attach`.** A route's
`MiddlewareHandlers` (Security-shaped or ordinary) run through the exact
same unified sequence regardless of whether the route is wired via
`b.Attach(...)` or bound directly to a `ports.SourceAdapter`/`SinkAdapter`/
`LatestAdapter` — "declare once, works everywhere" holds across both
consumption paths.

## Credential format validation

Use `validate` constraints to validate raw credential strings before `SecurityFunc` runs:

```go
// Bearer token: non-empty, no leading/trailing whitespace
codex.String().Refine(validate.BearerToken)

// UUID v4 API key
codex.String().Refine(validate.UUID)

// Non-empty API key
codex.String().Refine(validate.NonEmptyString)
```

## HTTP client — credential-providing `ClientMW`

For `rest.CallWithTransport` and `rest.Client.Call` alike (both are
full-featured — see the "Runtime enforcement" section above), provide
credentials via a credential-providing
implementation attached with [`Route.ClientMW`](../features/http-client.md),
PAIRED against the SAME `middleware.Middleware` value the route's security
requirement was declared with (via `.Use(mw)`). The Fn matches
[`CredentialFunc`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp#CredentialFunc)'s
shape (`func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error)`).
There is no per-call override anymore — a route's `ClientMW`-declared
credential fulfillment applies to EVERY call made through that route
value; a caller needing a genuinely different credential for one call
builds a DIFFERENT `Route` value via a fresh `.ClientMW(...)`:

```go
securedMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)

securedRoute := rest.NewRoute[GetDataReq, Data]("GET", "/data",
    reqCodec, respCodec, rest.RouteMeta{OperationID: "getSecuredData"},
).Use(securedMw).ClientMW(&securedMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
    h := make(http.Header)
    h.Set("Authorization", "Bearer "+getToken(ctx))
    return h, nil
})

handle := securedRoute.ClientHandle()
transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: serverURL})
data, err := rest.CallWithTransport(ctx, transport, handle, GetDataReq{}, rest.ClientCallOptions{})
```

The credential-providing `Fn` is GATED by `Satisfies` (derived from
`mw.Security.SchemeName`) vs. the route's declared requirements — it only
runs when the route actually declares that scheme. For static
credentials, use `ClientCallOptions.ExtraHeaders` instead.

**Symmetric credential-format validation.** If `securedRoute`'s declared
scheme carries a non-nil `Codec` — populated identically on BOTH
`Route.Register`/`RegisterHandle` and `Route.ClientHandle` — `Call`
validates the credential-providing `Fn`'s returned header against that
SAME `Codec` before sending, reusing the identical extraction/validation
logic the server `Handler` uses on an incoming request. A malformed
credential returns `rest.SecurityCredentialError` locally, before any
network call, and fires `stats.SecurityObserver.RecordSecurityRejection`
— catching a credential bug immediately instead of after a round trip and
a generic 401.

This does NOT require a credential-providing `ClientMW` to be attached at
all on a secured route, and the check only fires when one actually returns
something: no `ClientMW` attached, or one that deliberately returns
`(nil, nil)` to mean "this call needs no credential" (e.g. an auth flow
that first probes whether the specific server instance requires auth at
all — see `examples/go-edge-models/app/registry`'s
`newAuthCredentialFunc`), remains a deliberate non-error. The request is
simply sent without the credential, and it's up to the server to accept or
reject it — symmetric with server-side `SecurityFunc`.

### Caching a credential-providing Fn

Re-authenticating on every call is wasteful when the underlying `inner`
credential fetch is itself an HTTP round trip (e.g. an OAuth2 token
endpoint, or `examples/go-edge-models/app/registry`'s registry token
exchange). `nethttp.NewCachingCredentialFunc` wraps any
[`CredentialFunc`](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp#CredentialFunc)-shaped
function with TTL-based caching:

```go
credFn, invalidate := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{
    TTL: time.Hour,
})
securedRoute := contract.GetSecuredData(securedMw).ClientMW(&securedMw, credFn)
```

- `inner` is invoked at most once per TTL window; concurrent callers during
  a cache miss share the SAME in-flight call (hand-rolled single-flight —
  no thundering herd on the auth server, no external dependency).
- The returned `invalidate func()` immediately expires the cached
  credential. `NewCachingCredentialFunc` does NOT know when a credential is
  rejected — the server only reveals that via a 401 response, which is
  observed by `Call`, not by the credential Fn (which runs before
  the network call). Wire `invalidate` to `ClientCallOptions.OnCredentialRejected`
  and retry once, explicitly:

```go
callOpts := rest.ClientCallOptions{
    OnCredentialRejected: invalidate, // purges the cache; does NOT retry
}
handle := securedRoute.ClientHandle()
transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: serverURL})
resp, err := rest.CallWithTransport(ctx, transport, handle, req, callOpts)

var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
    resp, err = rest.CallWithTransport(ctx, transport, handle, req, callOpts) // fresh credential now
}
```

`OnCredentialRejected` is purely a notification hook — `CallWithTransport`
never retries automatically, keeping control flow explicit and in the
caller's hands.

`CachingCredentialFuncOptions.Observer`, when set, receives
`stats.CredentialCacheObserver` hit/refresh events
(`RecordCredentialCacheHit`/`RecordCredentialCacheRefresh`), each including a
`location` string derived from the security scheme names of the request's
`[]route.SecurityRequirement`.

One `NewCachingCredentialFunc` instance is one cache entry — construct a
separate instance per credential scope (e.g. per host/registry) when
different routes need independently-cached credentials.

### Live-reloadable credentials — `codex.Mutable[T]`/`codex.Cacheable[T]`

`SecurityFunc` (server) and a credential-providing `ClientMW`-attached `Fn`
(client) are plain closures — nothing above requires them to close over a
static value. A rotating signing key (server-side `SecurityFunc`) or a
TTL-bearing credential cache (client-side `Fn`) can capture a
`*codex.Mutable[T]`/`*codex.Cacheable[T]` and call `.Get()` INSIDE the
closure body, exactly like any other variable — no dedicated wrapper API
exists or is planned for this; a plain static value,
`NewCachingCredentialFunc`'s own hand-rolled cache, and these two
containers are all equally valid choices for the SAME closure field. See
`docs/concepts/codec.md`'s "Composing with adapters" subsection (under
`Getter`/`Setter`) for the full pattern, including the one real gotcha
(`.Get()` must never be hoisted out of the closure), and
`examples/mutable-security-keys` for a runnable end-to-end demo wiring
both containers into real `nethttp` server/client hooks.

## Security for event channels (AsyncAPI)

Just like REST, an events security scheme is declared ONCE and attached to
a channel's subscribe/publish role via `.Use()` — mirroring
`middleware.SecurityScheme`/`rest.FromSecurityScheme`/`Route.Use` exactly:

```go
import (
    "github.com/DaniDeer/go-codex/api/events"
    "github.com/DaniDeer/go-codex/route"
    "github.com/DaniDeer/go-codex/validate"
)

// Declare each scheme ONCE as a shared value.
var bearerAuthScheme = events.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken))

var bearerAuth = events.FromSecurityScheme("bearerAuth", bearerAuthScheme, nil)

eventsClient := events.NewClient(events.WithInfo(events.Info{Title: "User Events", Version: "1.0.0"}))
eventsClient.AddServer("production", events.Server{
    URL:      "broker.example.com",
    Protocol: "mqtt5",
})

// Attach the scheme directly to the subscribe role that needs it, via Use —
// CheckCoverage (run unconditionally by Subscriber.Handle) rejects a
// declared Security requirement with no attached SubscribeMW implementation
// satisfying it.
userCreatedSub := events.NewChannel[UserCreated]("user/created", codec,
    events.ChannelMeta{Description: "A user was created"},
).WithSubscribe(events.Subscribe{
    Summary:  "Receive user created events",
    Security: []route.SecurityRequirement{route.Require("bearerAuth")},
}).Use(bearerAuth)
```

`Client.AsyncAPISpec()` aggregates `components.securitySchemes` automatically
from every registered channel's own `.Use()`-attached declarations — no
separate builder-level step needed, same last-registered-wins-on-collision
policy as REST.

`events.WithSecurityScheme` is a DEPRECATED, older declaration mechanism
(kept only for backward-compat regression coverage) — new code should use
`FromSecurityScheme` + `.Use()` as shown above.

MQTT5 adapter — the server side runs a BUILT-IN codec-based credential
check (extracting the "Authorization" MQTT5 User Property for `http`/`oauth2`/
`openIdConnect` schemes, or the User Property named `scheme.Name` for
`apiKey` schemes) BEFORE the optional custom `SubscribeMW`-attached
security Fn — same two-step order as the nethttp/chi request pipeline:

```go
// client.Attach(mqtt5.NewTransport(...)) + events.Client.Subscribe is
// FULL-FEATURED (docs/design/d-0006-protocol-native-capabilities.md's
// Phase 4e closed the former "v1 scope" gap for this package — see
// adapters/mqtt5/transport.go's NewTransport doc comment) — it enforces
// SubscribeMW of both recognized shapes identically to the
// events.SubscribeHandle + mqtt5.NewSubscribeTransport handle-based path
// below, so either call surface works here.
transport := mqtt5.NewSubscribeTransport[UserCreated](client, router, mqtt5.SubscribeOptions{})
err := events.SubscribeHandle(ctx, userCreatedSub.SubscribeMW(&bearerAuth,
    func(ctx context.Context, msg *paho.Publish, value *UserCreated) (map[string][]string, error) {
        // Runs AFTER the built-in Codec check passes — add extra business
        // logic here (e.g. a database revocation check) if needed. Write
        // access to value lets a credential be embedded as an ordinary
        // payload field too, if needed.
        if err := checkNotRevoked(msg); err != nil {
            return nil, err
        }
        return map[string][]string{"bearerAuth": nil}, nil
    }), transport, handler)
```

The client (publish) side is symmetric via `PublishMW` — supplies the
credential as MQTT5 User Properties, and the SAME built-in codec check runs
BEFORE the message is actually published, mirroring
`rest.CallWithTransport`'s `CredentialFunc` handling exactly.

MQTT 3.1.1 (`adapters/mqtt`) has no per-message metadata channel, so
User-Property-style codec extraction only applies to MQTT5 — but message-level
security is NOT absent for MQTT 3.1.1. The OLD imperative
`SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc` escape
hatch was **removed entirely** (BREAKING — see
[D-0002](../design/d-0002-pubsub-workflow-simplification.md)'s own
"Addendum: Observability Core Consolidation, `SecurityFunc` Retirement,
and `examples/events-api`"): message-level security now lives ONLY in a
security-shaped
`SubscribeMW`/`PublishMW`-paired implementation Fn, the SAME declarative
mechanism mqtt5 uses above:

```go
// mqtt v3's paired Fn shape: func(ctx, pahomqtt.Message, *T) (map[string][]string, error)
// — receives the raw pahomqtt.Message directly, since MQTT 3.1.1 carries no
// User Properties to extract a credential from automatically. The
// credential itself is typically captured in a CLOSURE at CONNECT time
// (recommended for Paho) or read from an in-payload field on *T (write
// access lets it be embedded there too, if needed).
mqttSecurityImpl := func(credential string) func(context.Context, pahomqtt.Message, *SensorReading) (map[string][]string, error) {
    return func(_ context.Context, _ pahomqtt.Message, _ *SensorReading) (map[string][]string, error) {
        if !validAPIKeys[credential] {
            return nil, fmt.Errorf("unknown API key %q", credential)
        }
        return map[string][]string{"apiKeyAuth": {}}, nil
    }
}
sub := sensorDataSub.Use(apiKeyAuthMW).SubscribeMW(&apiKeyAuthMW, mqttSecurityImpl("sensor-key-abc123"))
```

Use connection-level `mqtt.SecuredClient` for connection-level (not
message-level) enforcement. See
[`examples/events-api/handlers/security.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/events-api/handlers/security.go)'s
`MQTTSecurityImpl` for the full runnable pattern (mirrors Pattern 1 —
closure — from the now-retired `adapters-mqtt-security` example).

**ZeroMQ pub/sub (`adapters/zeromq`) has a message-level security
mechanism too** — the OLD `SubscribeOptions.SecurityFunc`/
`PublishOptions.CredentialFunc` fields were likewise **removed entirely**
(same Phase 2 as above). The paired Fn shape is
`func(ctx, *T, []route.SecurityRequirement) error` (plain error return, no
scope-grant map, unlike mqtt/mqtt5's shape) — ZeroMQ's `[topic, payload]`
frames carry nothing beyond what's already decoded into `T`, so a
production route needs an in-payload credential field (e.g. a `Token
string` field) for the implementation to read/write, since there's no
raw-message parameter to extract one from. See
[`examples/events-api/handlers/security.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/events-api/handlers/security.go)'s
`ZeromqSecurityImpl` for the shape demonstrated without a real credential
field, and
[`examples/reqreply-api/routes/routes.go`](https://github.com/DaniDeer/go-codex/blob/main/examples/reqreply-api/routes/routes.go)'s
`OAuthComputeReq` for the in-payload-field pattern applied for real (reqreply,
not pub/sub, but the SAME shape). There is still no connection-level
`SecuredClient` equivalent for ZeroMQ (its base REQ/REP/PUB/SUB patterns
have no CONNECT-time credential handshake to validate) — a permanent,
deliberate, researched exclusion (CURVE/ZAP is the caller's own concern),
plus a CLOSED optional out-of-band frame-based mechanism question (no
driver ever surfaced); see
[D-0004](../design/d-0004-reqreply-workflow-simplification.md)'s Addendum
for the full rationale (the original tracking doc,
`zeromq-security.md`, has since shipped and been deleted per its own
graduation policy).

### Codec-backed Security — `SubscribeMW`/`PublishMW`'s bound path + `GrantedScopes`

Mirrors `api/rest`'s identical Rollout Phase A mechanism (see "Codec-backed
Security" under "Runtime enforcement" above), folded into events as Rollout
Phase B: a codec-backed `events.Middleware[In, Out]` (built via
`events.SecurityMiddleware[In, Out]`, generalized over In/Out) can ALSO
carry a real credential payload and be attached via `Subscriber.SubscribeMW`/
`Publisher.PublishMW` — the SAME two methods used for the legacy shape above.
`SubscribeMW`/`PublishMW` detect which shape `fn` is by its REFLECTED
signature (never `mw`'s type), so attaching either shape uses the identical
method call:

```go
type BearerIn struct{ Token string }
type BearerOut struct{ GrantedScopes map[string][]string }

bearerMw := events.SecurityMiddleware[BearerIn, BearerOut]("bearerAuth",
    bearerAuthScheme, nil,
).WithSubscribeProperty(events.NewPropertyParam("Authorization", codex.String(),
    func(in BearerIn) string { return in.Token },
    func(in *BearerIn, v string) { in.Token = v },
))

userCreatedSub = userCreatedSub.SubscribeMW(bearerMw,
    func(ctx context.Context, msg *UserCreated, in BearerIn) error {
        if !validToken(in.Token) {
            return errors.New("invalid bearer token")
        }
        return nil
    })
```

`fn` gets `*T` access (read/enrich, exactly like the generic middleware
mechanism), and `In`'s own topic/property merge fields decode declaratively
from the incoming message — no manual extraction anywhere. `Out` carries the
SAME conventional `GrantedScopes map[string][]string` field REST uses,
read by the adapter via reflection and fed into the SAME `middleware.CheckScopes`
call the legacy Fn path already used.

On the publish side, `PublishMW`'s bound shape is `func(ctx, msg T) (Out, error)`
(T BY VALUE) — recognized identically, dispatched through the SAME
merge-field mechanism instead of hand-building MQTT5 User Properties.

### Connection-level auth spec registration — `Client.AddConnectSecurityScheme`

A connection-level scheme (the broker's OWN CONNECT-time credential —
see "Connection-level vs message-level security" above) referenced ONLY
via a `Server.Security` list, never by any individual channel's own
Subscribe/Publish requirement, previously had no way to be spec-registered
at all (`components/securitySchemes` was aggregated ONLY from
per-channel declarations). `Client.AddConnectSecurityScheme` closes this:

```go
eventsClient.AddConnectSecurityScheme("brokerAuth", route.SecurityScheme{
    Type: route.SecuritySchemeHTTP, Scheme: "basic",
})
eventsClient.AddServer("mqtt5", events.Server{
    URL: "mqtts://broker:8883", Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("brokerAuth")},
})

// ... later, at ATTACH time — real credentials handed to the adapter,
// a real broker-rejection error surfaces BEFORE Client.Attach is reached.
client, router, err := mqtt5.Connect(ctx, "broker:8883", mqtt5.ConnectOptions{
    ClientID: "svc-1", Username: username, Password: password,
    Observer: obs, // optional — reports a broker-rejected CONNECT via RecordSecurityRejection
})
if err != nil {
    var connErr mqtt5.ConnectError
    if errors.As(err, &connErr) {
        // connErr.ReasonCode/ReasonString are populated when the broker
        // actually sent a CONNACK (e.g. 0x86 "Bad username or password") —
        // zero-value for a dial-stage failure.
    }
    return err
}
```

Reuse the SAME scheme NAME at both call sites — the primary (and today
only) safety mechanism linking the declared spec entry to the real
credentials supplied at attach time; see
[D-0006](../design/d-0006-protocol-native-capabilities.md) for the full
declare/attach-time-supply lifecycle this capability follows.

## Security for request-reply routes (reqreply)

`api/reqreply` now mirrors REST/events' exact declare-once,
enforce-symmetrically model on BOTH transports it supports, via the SAME
`.Use()`/`HandleMW`/`ClientMW` declare/implement split — shipped for
mqtt5 first, then zeromq (see
[D-0004](../design/d-0004-reqreply-workflow-simplification.md)'s own
Addendum for the full record; both source roadmap docs,
`reqreply-middleware.md` and `zeromq-security.md`, have since shipped and
been deleted) — REPLACES the older `reqreply.WithSecurityScheme`-only
mechanism, kept only as a deprecated-but-functional alias. The paired
implementation Fn's SHAPE differs per adapter (mqtt5: scope-grant
`(map[string][]string, error)`, reading the raw `*pahomqtt5.Publish`;
zeromq: plain `error`, reading/writing the decoded `*Req` directly — no
raw-message equivalent exists for zeromq) — each adapter mirrors its OWN
established precedent, not a shared shape. See "Sharing a security
scheme declaration across REST/events/reqreply" below for how the
SAME scheme DECLARATION (not the Fn) can still be reused across all
three APIs regardless of this per-adapter Fn-shape difference.

```go
var bearerAuthCodec = codex.String().Refine(validate.BearerToken)
var bearerAuth = middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, &bearerAuthCodec)

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add", computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{OperationID: "computeAdd"},
)
```

Server side — `.Use(bearerAuth)` declares the requirement; `HandleMW`
attaches the PAIRED implementation Fn (`func(ctx, msg *paho.Publish, reqs)
(map[string][]string, error)` — the SAME scope-grant shape REST's
`HandleMW` uses), consulted by mqtt5's server-transport dispatch (built via `mqtt5.NewServerTransport`). A route
declaring a scheme with no attached implementation fails loudly at
Serve time with `reqreply.
MissingSecurityMiddlewareError` (`reqreply.CheckCoverage`, mirrors
`rest.CheckCoverage` exactly) — never a silent no-op:

```go
securedRoute := ComputeRoute.Use(bearerAuth).
    HandleMW(&bearerAuth, func(ctx context.Context, msg *paho.Publish, reqs []route.SecurityRequirement) (map[string][]string, error) {
        return map[string][]string{"bearerAuth": nil}, checkNotRevoked(msg, reqs)
    })
handle, err := securedRoute.Register(server)
```

Client side — `ClientMW` attaches the PAIRED credential-supplying Fn
(`func(ctx, reqs) ([]mqtt5.UserProperty, error)` — replaces the OLD
`CallOptions.CredentialFunc`, removed entirely as a breaking change),
consulted by mqtt5's client-transport dispatch (built via `mqtt5.NewClientTransport`), validated client-side
before the request is ever published:

```go
callRoute := ComputeRoute.Use(bearerAuth).
    ClientMW(&bearerAuth, func(ctx context.Context, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error) {
        token, err := fetchToken(ctx)
        if err != nil {
            return nil, err
        }
        return []mqtt5.UserProperty{{Key: "Authorization", Value: "Bearer " + token}}, nil
    })
resp, err := client.Call(ctx, callRoute, req)
```

A `HandleMW`/`ClientMW` implementation naming a scheme never `.Use()`'d on
the same route fails at `Route.Register`/`ClientHandle` time with
`reqreply.UnknownMiddlewareImplementationError` — the reverse-direction
sibling of `MissingSecurityMiddlewareError` above.

`Server.AddGlobalSecurity(reqs...)` and per-route `RouteMeta.Security`
(nil=inherit global, empty=no auth) work identically to REST/events.
`reqreply.SecurityCredentialError`/`reqreply.SecurityError` are the
request-reply analogues of REST's error types — same fields, same
`errors.As`/`slog.LogValuer` shape.

**User Property param-as-middleware**: `reqreply.Middleware[In,Out]`'s
`WithRequestPropertySpec`/`WithResponsePropertySpec` declare a
presence-only (non-merged) User Property param — a header-like param
declaration with no corresponding `In`/`Out` struct field to decode
into. No `HandleMW`/`ClientMW` pairing needed (unlike security schemes,
header params aren't gated behind `CheckCoverage`) — declaring
`.Use(...)` is enough for mqtt5's server-transport dispatch to validate
the real MQTT5 User Property automatically, and for the property to
render into the request/reply message's AsyncAPI `headers` schema:

```go
var apiKeyParam = reqreply.PropertyParam{Param: codex.Param{Name: "X-API-Key"}, Required: true}

route := ComputeRoute.Use(
    reqreply.NewMiddleware[struct{}, struct{}](middleware.Declaration[struct{}, struct{}]{
        Name: "declare-api-key-property",
    }).WithRequestPropertySpec(apiKeyParam),
)
```

[Feature: Codec-Declared Middleware](codec-declared-middleware.md)'s
`reqreply.Middleware[In,Out]`'s `WithRequestProperty`/`WithResponseProperty`
axis is this mechanism's MERGE-capable sibling — required vs. optional
properties are a first-class choice there too
(`NewPropertyParam`/`NewOptionalPropertyParam`), the difference being
whether a decoded `In`/`Out` struct field exists to merge into.

**zeromq** — same `.Use()`/`HandleMW`/`ClientMW` declare/implement split,
but the paired Fn shape reads/writes the decoded `*Req` directly (no raw
message exists to operate on instead, unlike mqtt5's `*pahomqtt5.
Publish`) — mirrors zeromq's OWN pub/sub security-shaped
`SubscribeMW`/`PublishMW` Fn shape exactly (plain `error`, no scope-grant
map):

```go
type ComputeReq struct{ X, Y int; Token string }

securedRoute := ComputeRoute.Use(bearerAuth).
    HandleMW(&bearerAuth, func(ctx context.Context, req *ComputeReq, reqs []route.SecurityRequirement) error {
        return checkNotRevoked(req.Token, reqs)
    })

callRoute := ComputeRoute.Use(bearerAuth).
    ClientMW(&bearerAuth, func(ctx context.Context, req *ComputeReq, reqs []route.SecurityRequirement) error {
        token, err := fetchToken(ctx)
        if err != nil {
            return err
        }
        req.Token = token // written directly into the request payload
        return nil
    })
```

The application's own domain `Req` struct must carry a credential field
itself for zeromq's model to work (`Token string` above) — a documented,
accepted transport limitation (zeromq has no property/header side
channel), not a bug.

## Sharing a security SCHEME across REST/events/reqreply

Each of `rest`/`events`/`reqreply` has its own `SecurityMiddleware[In, Out
any](schemeName, scheme, scopes) Middleware[In, Out]` constructor — the one,
single vocabulary for declaring a security requirement in that pattern.
`rest.SecurityMiddleware` is generic over In/Out (docs/roadmap/
declarative-middleware-layering.md's Rollout Phase A — away from a
hardcoded `Middleware[struct{}, struct{}]`), so a caller needing the
credential-providing fn to ALSO decode request header/cookie/query merge
fields can do so directly; the pure credential-carrier pattern (no
merge fields) passes `[struct{}, struct{}]` explicitly (Go cannot infer
In/Out here — no parameter is typed by them):
`rest.SecurityMiddleware[struct{}, struct{}]("basicAuth", scheme, nil)`.
These constructors are pattern-specific BY DESIGN (their return types
differ: `rest.Middleware[In,Out]`, `events.Middleware[struct{},struct{}]`,
`reqreply.Middleware[struct{},struct{}]` are three distinct Go types, and a
value of one does NOT work if attached to another pattern's routes/channels —
each pattern's internal dispatch only recognizes its own concrete type; a
foreign-pattern value would compile but be silently dropped, contributing
nothing to that pattern's spec).

What genuinely IS shared, and is the actual mechanism behind "one OAuth2
scheme, usable everywhere": the underlying `route.SecurityScheme` value
(`BearerScheme`/`BasicScheme`/`APIKeyScheme`/`OAuth2Scheme`/
`OpenIDConnectScheme`) and the credential-format `codex.Codec[string]`, both
of which are already protocol-agnostic, ordinary Go values with no
per-pattern type at all. Declare these ONCE, then pass the SAME values into
each pattern's own `SecurityMiddleware` constructor — "one shared config,
one declaration per pattern":

```go
// ONE shared, protocol-agnostic config — not pattern-specific.
var oauthScheme = route.OAuth2Scheme(route.OAuthFlows{
    ClientCredentials: &route.OAuthFlow{
        TokenURL: "https://auth.example.com/oauth2/token",
        Scopes:   map[string]string{"compute:write": "Submit compute requests"},
    },
})
var oauthScopes = []string{"compute:write"}

// Three pattern-specific declarations, same underlying scheme + scopes:
restMw     := rest.SecurityMiddleware[struct{}, struct{}]("oauth2Compute", rest.SecurityScheme{SecurityScheme: oauthScheme}.WithCodec(oauthCodec), oauthScopes)
eventsMw   := events.SecurityMiddleware("oauth2Compute", events.SecurityScheme{SecurityScheme: oauthScheme}.WithCodec(oauthCodec), oauthScopes)
reqreplyMw := reqreply.SecurityMiddleware("oauth2Compute", reqreply.SecurityScheme{SecurityScheme: oauthScheme}.WithCodec(oauthCodec), oauthScopes)

restRoute := rest.NewRoute[Req, Resp]("POST", "/compute", reqCodec, respCodec, meta).Use(restMw)
channel := events.NewChannel[Msg]("compute/events", codec, meta).WithSubscribe(events.Subscribe{}).Use(eventsMw)
reqreplyRoute := reqreply.NewRoute[Req, Resp]("compute/add", reqCodec, respCodec, meta).Use(reqreplyMw)
```

Each of the three specs (`Server.OpenAPISpec()`/`Client.AsyncAPISpec()`/
`Server.AsyncAPISpec()`) renders an IDENTICAL `securitySchemes.
oauth2Compute` entry (same type, flows, scopes) — because all three
declarations were built from the SAME `oauthScheme`/`oauthScopes`/
`oauthCodec` values, even though each is its own distinct
`Middleware[struct{},struct{}]` instance. `examples/reqreply-api`'s Demo 9
(`demo_cross_api_oauth2_sharing.go`) demonstrates this concretely:
`routes.OAuthMwReqreply` is attached to a REAL, served, called zeromq
reqreply route, and `routes.OAuthMwREST` (same `route.SecurityScheme`
config) is attached to a locally-declared REST route (registered just to
print its `OpenAPISpec()` output) — the demo then prints both specs'
`oauth2Compute` entries side by side to show they're byte-for-byte
identical, even though the two `Middleware` values attached to get there
are not the same Go value.

**What is NOT shared: the paired implementation Fn.** `HandleMW`/
`ClientMW`/`SubscribeMW`/`PublishMW`'s attached Fn is adapter-specific —
each transport's paired security shape differs (REST: `func(ctx,
*http.Request, *Req) (map[string][]string, error)`; mqtt5: `func(ctx,
msg *pahomqtt5.Publish, ...) (map[string][]string, error)`; zeromq:
`func(ctx, *Req, reqs) error`, no scope-grant). You cannot pass the
literal same Go closure to `HandleMW` calls on two different transports.

**The recommended pattern**: factor the REAL verification logic (token
introspection, JWT validation, scope extraction) into ONE shared,
transport-agnostic helper taking a plain credential string, then write a
THIN, few-line wrapper Fn per transport that only differs in HOW it
extracts the raw credential from its own transport shape (HTTP header /
MQTT User Property / decoded request field) before calling the shared
helper:

```go
// ONE shared helper — the actual verification logic, transport-agnostic.
func VerifyOAuth2Scopes(token string) (map[string][]string, error) { /* ... */ }

// THIN, zeromq-shaped wrapper — extracts the token from *Req, delegates.
func zeromqOAuthFn(_ context.Context, req *ComputeReq, _ []route.SecurityRequirement) error {
    scopes, err := VerifyOAuth2Scopes(req.Token)
    if err != nil {
        return err
    }
    return middleware.CheckScopes(reqs, scopes)
}

// THIN, REST-shaped wrapper — extracts the token from *http.Request, delegates.
func restOAuthFn(_ context.Context, r *http.Request, _ *ComputeReq) (map[string][]string, error) {
    token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
    return VerifyOAuth2Scopes(token)
}
```

This is exactly `examples/reqreply-api/handlers/oauth.go`'s structure:
`VerifyOAuth2Scopes` is the one shared helper; `VerifyOAuthComputeZeroMQ`
is its zeromq-shaped wrapper.

## SecurityObserver — rejection metrics

Implement `stats.SecurityObserver` on your observer to receive rejection events. Adapters type-assert it — no breaking change to the `Observer` interface:

```go
type TelemetryObserver struct {
    stats.NoopObserver  // embed for Observer methods
}

func (o *TelemetryObserver) RecordSecurityRejection(location, scheme string) {
    // location = route path (HTTP) or topic (MQTT)
    // scheme   = first declared security scheme name for the operation
    metrics.SecurityRejections.WithLabelValues(location, scheme).Inc()
}
```

## OpenAPI / AsyncAPI output

Security schemes appear in `components/securitySchemes`; global security at document root (REST only — AsyncAPI 3.0 has no document-level global security field); per-operation security overrides inline — all generated automatically from each route/channel's own security declaration (REST: `middleware.SecurityScheme`/`rest.FromSecurityScheme` attached via `Route.Use()`; events: `events.FromSecurityScheme` attached via `Subscriber.Use()`/`Publisher.Use()` — mirrors REST exactly (`events.WithSecurityScheme` is the older, deprecated, channel-level mechanism, kept only for backward compatibility); reqreply: `middleware.SecurityScheme(...)` attached via `Route.Use()` — mirrors REST/events exactly (`reqreply.WithSecurityScheme` is the older, deprecated, route-level mechanism, kept only for backward compatibility, same treatment as `events.WithSecurityScheme`); aggregated by `Server.OpenAPISpec`/`Client.AsyncAPISpec`, last-registered-wins on name collision) / `AddGlobalSecurity` / `RouteMeta.Security` / `Subscribe.Security` / `Publish.Security`. No manual YAML needed.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — `SecurityFunc` wiring in the adapter
- [Guide: HTTP Client](../guides/http-client.md) — `CredentialFunc` for client-side credentials
- [Guide: Observer](../guides/observer.md) — `SecurityObserver` metrics
- [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) — bearer JWT + scopes + observer, both chi and net/http adapters
- [examples/reqreply-api](https://github.com/DaniDeer/go-codex/tree/main/examples/reqreply-api) — Demo 9 (`demo_cross_api_oauth2_sharing.go`) shows ONE `middleware.SecurityScheme` (OAuth2) declaration shared across a zeromq reqreply route AND a locally-declared REST route
