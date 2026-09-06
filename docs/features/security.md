# Security & Authentication

> See also: [`route` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/route)
>
> Runnable demos: [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) (bearer JWT + scopes, both chi and net/http servers) · [`examples/adapters-mqtt-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-security) · [`examples/adapters-mqtt5`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt5) (Demo 3: message-level; Demo 3b: connect-level `SecuredClient`)
>
> `api/mcp` has NO security methods by deliberate, permanent design — MCP
> security is handled by the host application (its own OAuth flow, or
> transport-level auth on the SSE/stdio transport itself), not by go-codex.
> This is the one place go-codex's security model is intentionally
> asymmetric with REST/events/reqreply.

go-codex documents security requirements in the spec and provides declarative hooks for runtime enforcement — **the library does not import any crypto or JWT library**. For REST, a security scheme is declared ONCE, via `middleware.SecurityScheme(schemeName, scheme, scopes, codec)`/`rest.FromSecurityScheme(schemeName, rest.SecurityScheme, scopes)` (bridging an existing `rest.SecurityScheme` value), attached to the route via `Route.Use(mw)` — there is no builder-level scheme registry, and `rest.WithSecurityScheme` was REMOVED (there is no metadata-only registration anymore; every declared scheme is a real requirement) — and the SAME declaration is consumed identically by both the server (`Route.Register`/`RegisterHandle`) and the client (`Route.ClientHandle`), so one route definition gets IDENTICAL credential-format enforcement on both ends. Runtime credential validation itself is attached PER-ROUTE, paired against the declared `middleware.Middleware`: server-side via `Route.HandleMW(mw, fn)`, client-side via `Route.ClientMW(mw, fn)` (see "HTTP client — credential-providing ClientMW" below).

## Connection-level vs message-level security

REST is stateless per-request — every `nethttp.CallWithHandle`/incoming request IS a
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
   transport := mqtt5.NewSubscribeTransport[T](secured, router, 1, opts)
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
2. **Message-level** (`WithSecurityScheme` + `SecurityFunc`/
   `CredentialFunc`, shipped — MQTT5-only, see below) — opt-in, per-message
   codec-validated credentials, needed when ONE connection carries traffic
   for MULTIPLE logical identities (e.g. a gateway relaying many devices'
   messages over one shared broker connection) and broker ACLs alone can't
   distinguish between them.

Both layers are independent and composable — use connection-level ACLs for
coarse-grained, per-connection authorization, and message-level
`SecurityFunc`/`CredentialFunc` for fine-grained, per-message claims within
a single shared connection. Neither requires the other.

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
nethttp.AttachMux(b, mux, addr)
go func() { _ = b.Serve(ctx) }()
```

The adapter enforcement sequence:
1. Extracts the raw credential per scheme type (`Authorization: Bearer <token>`, `X-API-Key: <key>`, etc.)
2. Validates it via `SecurityScheme.Codec` → returns `rest.SecurityCredentialError` + 401 on failure
3. Calls `SecurityFunc` → returns `rest.SecurityError` + 401 on rejection

Routes with `nil Security` (default) trigger enforcement when global security is set.

`nethttp.CallWithHandle` (client-side) runs the SAME sequence, symmetrically, on the OUTGOING request before it is sent — see "HTTP client — credential-providing ClientMW" below. **`rest.Client.Call` (the `nethttp.Attach`-based reflection shim) does NOT** — it is v1-scoped to the core JSON encode/decode case with no path/query/header/cookie params and no `ClientMW` of any shape (credential or general-purpose); a route relying on `ClientMW` for credentials must use `CallWithHandle` directly (see `adapters/nethttp/clienttransport.go`'s own doc comment for the full v1-scope note).

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

For `nethttp.CallWithHandle` (NOT `rest.Client.Call` — see the "Runtime
enforcement" section above for why the `nethttp.Attach`-based reflection
shim doesn't honor this), provide credentials via a credential-providing
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
data, err := nethttp.CallWithHandle(ctx, http.DefaultClient, serverURL, handle, GetDataReq{}, nethttp.CallOptions{})
```

The credential-providing `Fn` is GATED by `Satisfies` (derived from
`mw.Security.SchemeName`) vs. the route's declared requirements — it only
runs when the route actually declares that scheme. For static
credentials, use `CallOptions.ExtraHeaders` instead.

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
  the network call). Wire `invalidate` to `CallOptions.OnCredentialRejected`
  and retry once, explicitly:

```go
callOpts := nethttp.CallOptions{
    OnCredentialRejected: invalidate, // purges the cache; does NOT retry
}
handle := securedRoute.ClientHandle()
resp, err := nethttp.CallWithHandle(ctx, http.DefaultClient, serverURL, handle, req, callOpts)

var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
    resp, err = nethttp.CallWithHandle(ctx, http.DefaultClient, serverURL, handle, req, callOpts) // fresh credential now
}
```

`OnCredentialRejected` is purely a notification hook — `CallWithHandle`
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
// NOTE: mqtt5.Attach + Client.Subscribe is v1-scoped and does NOT enforce
// SubscribeMW of any shape (see adapters/mqtt5/transport.go's Attach doc
// comment) — use events.SubscribeHandle + mqtt5.NewSubscribeTransport
// directly, which delegates to the SAME full-featured internal logic
// mqtt5.Serve's dispatch uses, to get SubscribeMW enforcement.
transport := mqtt5.NewSubscribeTransport[UserCreated](client, router, 1, mqtt5.SubscribeOptions{})
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
`nethttp.CallWithHandle`'s `CredentialFunc` handling exactly.

MQTT 3.1.1 (`adapters/mqtt`) has no per-message metadata channel, so
User-Property-style codec extraction only applies to MQTT5 — but message-level
security is NOT absent for MQTT 3.1.1: `SubscribeOptions.SecurityFunc`
(subscribe side, `func(ctx, msg pahomqtt.Message, reqs) error`) and
`PublishOptions.CredentialFunc` (publish side, `func(ctx, msg *T, reqs) error`,
gaining WRITE-ACCESS to the outgoing payload) both exist — the publish-side
credential is embedded as an ordinary field in the codec-decoded payload
itself, rather than a protocol-native side channel, closing what was
originally a real MQTT 3.1.1 publish-side gap. Use connection-level
`mqtt.SecuredClient` for connection-level (not message-level) enforcement.

**ZeroMQ pub/sub (`adapters/zeromq`) now has a message-level security
mechanism too**, via the SAME in-payload write-access pattern as MQTT 3.1.1:
`SubscribeOptions.SecurityFunc`/`PublishOptions.CredentialFunc`
(`func(ctx, msg *T, reqs) error`, both directions — ZeroMQ's `[topic,
payload]` frames carry nothing beyond what's already decoded into `T`, so
there's no separate raw-message parameter unlike `mqtt`/`mqtt5`'s subscribe
side). There is still no connection-level `SecuredClient` equivalent for
ZeroMQ (its base REQ/REP/PUB/SUB patterns have no CONNECT-time credential
handshake to validate) — an optional additional out-of-band frame-based
mechanism and the connection-level/CURVE question remain open, narrower
gaps, tracked in [ZeroMQ Security Mechanism](../roadmap/zeromq-security.md).

## Security for request-reply routes (reqreply)

`api/reqreply` (MQTT5 only — `adapters/zeromq`'s reqreply `Call`/`Serve` have
no security mechanism today, confirmed: `Descriptor.Security`/
`GlobalSecurity` are never even read, since ZeroMQ carries no per-message
metadata; same class of gap as pub/sub above — see
[ReqReply Workflow Simplification](../roadmap/reqreply-workflow-simplification.md))
mirrors the exact same declare-once, enforce-symmetrically model:

```go
var bearerAuth = reqreply.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken))

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
    "compute/add", computeReqCodec, computeRespCodec,
    reqreply.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth")}},
    reqreply.WithSecurityScheme("bearerAuth", bearerAuth),
)
```

Server (`Serve`) — built-in codec check first, then the optional custom
`SecurityFunc`:

```go
mqtt5.Serve(ctx, client, router, handle, fn, mqtt5.ServeOptions{
    SecurityFunc: func(ctx context.Context, msg *paho.Publish, reqs []route.SecurityRequirement) error {
        return checkNotRevoked(msg, reqs)
    },
})
```

Client (`Call`) — `CredentialFunc` supplies the credential, validated
client-side before the request is published:

```go
resp, err := mqtt5.Call(ctx, client, router, handle, req, mqtt5.CallOptions{
    CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error) {
        token, err := fetchToken(ctx)
        if err != nil {
            return nil, err
        }
        return []mqtt5.UserProperty{{Key: "Authorization", Value: "Bearer " + token}}, nil
    },
})
```

`Builder.AddGlobalSecurity(reqs...)` and per-route `RouteMeta.Security`
(nil=inherit global, empty=no auth) work identically to REST/events.
`reqreply.SecurityCredentialError`/`reqreply.SecurityError` are the
request-reply analogues of REST's error types — same fields, same
`errors.As`/`slog.LogValuer` shape.

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

Security schemes appear in `components/securitySchemes`; global security at document root (REST only — AsyncAPI 3.0 has no document-level global security field); per-operation security overrides inline — all generated automatically from each route/channel's own security declaration (REST: `middleware.SecurityScheme`/`rest.FromSecurityScheme` attached via `Route.Use()`; events: `events.FromSecurityScheme` attached via `Subscriber.Use()`/`Publisher.Use()` — mirrors REST exactly (`events.WithSecurityScheme` is the older, deprecated, channel-level mechanism, kept only for backward compatibility); reqreply: `reqreply.WithSecurityScheme` — route-level, still the ONLY declaration mechanism there (no redesign has happened for `api/reqreply` yet); aggregated by `Server.OpenAPISpec`/`Client.AsyncAPISpec`, last-registered-wins on name collision) / `AddGlobalSecurity` / `RouteMeta.Security` / `Subscribe.Security` / `Publish.Security`. No manual YAML needed.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — `SecurityFunc` wiring in the adapter
- [Guide: HTTP Client](../guides/http-client.md) — `CredentialFunc` for client-side credentials
- [Guide: Observer](../guides/observer.md) — `SecurityObserver` metrics
- [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) — bearer JWT + scopes + observer, both chi and net/http adapters
