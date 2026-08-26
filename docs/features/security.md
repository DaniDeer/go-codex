# Security & Authentication

> See also: [`route` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/route)
>
> Runnable demos: [`examples/adapters-nethttp-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security) · [`examples/adapters-chi-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-chi-security) · [`examples/adapters-mqtt-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-security) · [`examples/adapters-mqtt5`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt5) (Demo 3: message-level; Demo 3b: connect-level `SecuredClient`)
>
> `api/mcp` has NO security methods by deliberate, permanent design — MCP
> security is handled by the host application (its own OAuth flow, or
> transport-level auth on the SSE/stdio transport itself), not by go-codex.
> This is the one place go-codex's security model is intentionally
> asymmetric with REST/events/reqreply.

go-codex documents security requirements in the spec and provides declarative hooks for runtime enforcement — **the library does not import any crypto or JWT library**. For REST, a security scheme is declared ONCE, directly on the route (`rest.WithSecurityScheme`) — there is no builder-level scheme registry — and the SAME declaration is consumed identically by both the server (`Route.Register`) and the client (`Route.ClientHandle`), so one route definition gets IDENTICAL credential-format enforcement on both ends. Runtime credential validation itself is handled by adapters: server-side via a `SecurityFunc` hook, client-side automatically inside `nethttp.Call` (see "HTTP client — CredentialFunc" below).

## Connection-level vs message-level security

REST is stateless per-request — every `nethttp.Call`/incoming request IS a
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

   // secured is a drop-in replacement — every Subscribe/Publish/Serve/Call
   // call site below is UNCHANGED from how it would look with the raw client.
   mqtt5.Subscribe(ctx, secured, router, handle, fn, opts)
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
    "github.com/DaniDeer/go-codex/route"
    "github.com/DaniDeer/go-codex/validate"
)

// Declare each scheme ONCE as a shared value — Go's ordinary "declare once,
// reference everywhere" idiom, no builder-level registry needed.
var bearerAuth = rest.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken)) // format check before SecurityFunc/before send

var apiKeyAuth = rest.SecurityScheme{
    SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
}

b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})

// Attach the scheme directly to every route that needs it, via WithSecurityScheme.
createUser, _ := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec,
    rest.RouteMeta{
        OperationID: "createUser",
        Security:    []route.SecurityRequirement{route.Require("bearerAuth", "write:users")},
    },
    rest.WithSecurityScheme("bearerAuth", bearerAuth),
).Register(b)
```

`Builder.OpenAPISpec()` aggregates `components.securitySchemes` automatically from every registered route's own `WithSecurityScheme` declarations — no separate builder-level step needed. When two routes declare the same scheme name with different values, the last-registered route wins (no error); define the scheme once as a shared value (as above) to avoid relying on this.

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

`Builder.AddGlobalSecurity`/`RouteMeta.Security` answer "which routes require auth" — a SEPARATE, unchanged concern from `WithSecurityScheme` ("what does a named scheme look like"):

```go
// Global security — applies to all operations by default.
b.AddGlobalSecurity(route.Require("bearerAuth"))

// Per-route override — nil inherits global; empty slice = no auth required.
// bearerAuth is the SAME shared SecurityScheme value from the section above.
createUser, _ := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec,
    rest.RouteMeta{
        OperationID: "createUser",
        // Requires bearerAuth with write:users scope
        Security: []route.SecurityRequirement{
            route.Require("bearerAuth", "write:users"),
        },
    },
    rest.WithSecurityScheme("bearerAuth", bearerAuth),
).Register(b)

// Explicitly public — empty slice overrides global security
publicRoute, _ := rest.NewRoute[struct{}, Info]("GET", "/health",
    codex.Empty, infoCodec,
    rest.RouteMeta{
        Security: []route.SecurityRequirement{}, // no auth required
    },
).Register(b)
```

## Security requirement shapes — single, OR, AND, opt-out, inherit

`route.SecurityRequirement` is `map[string][]string` (scheme name → required
OAuth2 scopes) — a single map can hold MULTIPLE scheme names as keys, and
`RouteMeta.Security` is a SLICE of these maps. This gives two independent
axes of combination:

- **Within one map**: every key (scheme name) must be satisfied together — **AND**.
- **Across the slice**: any one map fully satisfied is enough — **OR**.

`route.Require(name, scopes...)` (used throughout this doc so far) is a
convenience that only ever builds a SINGLE-KEY map — reach for the map
literal directly whenever a requirement needs more than one scheme name at
once (the AND case below). Every scheme name referenced ANYWHERE in
`Security` still needs a matching `rest.WithSecurityScheme(name, ...)`
declaration on the same route for its `Codec` to be enforced — a
referenced name with no matching declaration still gates
`SecurityFunc`/`CredentialFunc` invocation correctly, but gets no
credential-FORMAT validation (matches the "no adapter enforces a
SecurityScheme unless explicitly declared" convention used throughout this
doc).

**1. Single scheme required** (the baseline shown above):

```go
rest.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth", "write:users")}},
rest.WithSecurityScheme("bearerAuth", bearerAuth),
```

**2. OR — either scheme suffices** (two elements in the slice; each
`route.Require(...)` call already produces one single-key map, so calling
it twice at the slice level is all that's needed):

```go
rest.RouteMeta{
    Security: []route.SecurityRequirement{
        route.Require("bearerAuth"),
        route.Require("apiKey"),
    },
},
rest.WithSecurityScheme("bearerAuth", bearerAuth),
rest.WithSecurityScheme("apiKey", apiKeyAuth),
```

**3. AND — both required together** (one map with multiple keys — build
the literal directly, since `route.Require` only ever builds a single-key
map):

```go
rest.RouteMeta{
    Security: []route.SecurityRequirement{
        {"bearerAuth": nil, "apiKey": nil},
    },
},
rest.WithSecurityScheme("bearerAuth", bearerAuth),
rest.WithSecurityScheme("apiKey", apiKeyAuth),
```

**4. Mixed — OR of ANDs** ("(bearerAuth AND apiKey) OR (oauth2 with a
scope)"):

```go
rest.RouteMeta{
    Security: []route.SecurityRequirement{
        {"bearerAuth": nil, "apiKey": nil},
        {"oauth2": {"read:users"}},
    },
},
```

**5. Explicit opt-out** (empty slice, NOT nil — overrides global security
for just this route):

```go
rest.RouteMeta{Security: []route.SecurityRequirement{}},
```

**6. Inherit global** (omit `Security` entirely — the default):

```go
rest.RouteMeta{OperationID: "health"}, // no Security field at all
```

**7. Same scheme, different scopes per route** (the actual reason
`WithSecurityScheme` and `RouteMeta.Security` stay two separate
declarations rather than one combined call — see the "Why two mechanisms"
note below):

```go
// route A — needs the "profile" scope:
rest.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth", "profile")}},
rest.WithSecurityScheme("bearerAuth", bearerAuth), // SAME shared value

// route B — needs the "admin" scope:
rest.RouteMeta{Security: []route.SecurityRequirement{route.Require("bearerAuth", "admin")}},
rest.WithSecurityScheme("bearerAuth", bearerAuth), // SAME shared value, different required scope
```

**Why two mechanisms, not one:** `WithSecurityScheme` answers "what does
scheme X look like" — a 1:1 mapping (name → shape), reusable across routes.
`RouteMeta.Security` answers "which combination of schemes does THIS
operation need" — an N:M relationship (routes → AND/OR combinations of
scheme names + scopes), as shapes 2–4 above demonstrate. Folding them into
one declaration would lose that combinatorial expressiveness — there would
be no way to say "requires bearerAuth AND apiKey together" or "requires
bearerAuth with scope A on this route but scope B on that one" without
duplicating the entire scheme definition per combination.

## Runtime enforcement (nethttp / chi adapters)

```go
nethttp.Register(mux, createUser, handler, nethttp.Options{
    // SecurityFunc is called after Codec format validation passes.
    // Receives the *http.Request and the route's declared security requirements.
    SecurityFunc: func(ctx context.Context, r *http.Request, reqs []route.SecurityRequirement) error {
        token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
        return jwtlib.VerifyScopes(token, reqs)
    },
})
```

The adapter enforcement sequence:
1. Extracts the raw credential per scheme type (`Authorization: Bearer <token>`, `X-API-Key: <key>`, etc.)
2. Validates it via `SecurityScheme.Codec` → returns `rest.SecurityCredentialError` + 401 on failure
3. Calls `SecurityFunc` → returns `rest.SecurityError` + 401 on rejection

Routes with `nil Security` (default) trigger enforcement when global security is set.

`nethttp.Call` (client-side) runs the SAME sequence, symmetrically, on the OUTGOING request before it is sent — see "HTTP client — CredentialFunc" below.

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

## HTTP client — CredentialFunc

For the `nethttp.Call` client, provide credentials via `CredentialFunc`:

```go
user, err := nethttp.Call(ctx, http.DefaultClient, serverURL, handle, req, nil,
    nethttp.CallOptions{
        CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
            h := make(http.Header)
            h.Set("Authorization", "Bearer "+getToken(ctx))
            return h, nil
        },
    })
```

`CredentialFunc` is called only when the route declares security requirements. For static credentials, use `CallOptions.ExtraHeaders` instead.

**Symmetric credential-format validation.** If `handle` was built from a route
declaring `rest.WithSecurityScheme(name, scheme)` with a non-nil `Codec` —
whether via `Route.Register` or `Route.ClientHandle`, same declaration either
way — `nethttp.Call` validates `CredentialFunc`'s returned header against that
SAME `Codec` before sending, reusing the identical extraction/validation logic
the server `Handler` uses on an incoming request. A malformed credential
returns `rest.SecurityCredentialError` locally, before any network call, and
fires `stats.SecurityObserver.RecordSecurityRejection` — catching a
`CredentialFunc` bug immediately instead of after a round trip and a generic
401.

This does NOT require `CredentialFunc` to be non-nil on a secured route, and
the check only fires when `CredentialFunc` actually returns something: a nil
`CredentialFunc`, or one that deliberately returns `(nil, nil)` to mean "this
call needs no credential" (e.g. an auth flow that first probes whether the
specific server instance requires auth at all — see
`examples/go-edge-models/app/registry`'s `newAuthCredentialFunc`), remains
a deliberate non-error. The request is simply sent without the credential,
and it's up to the server to accept or reject it — symmetric with server-side
`SecurityFunc`.

### Caching a CredentialFunc

Re-authenticating on every `Call` is wasteful when the underlying `inner`
credential fetch is itself an HTTP round trip (e.g. an OAuth2 token
endpoint, or `examples/go-edge-models/app/registry`'s registry token
exchange). `nethttp.NewCachingCredentialFunc` wraps any `CredentialFunc`
with TTL-based caching:

```go
credFn, invalidate := nethttp.NewCachingCredentialFunc(inner, nethttp.CachingCredentialFuncOptions{
    TTL: time.Hour,
})
```

- `inner` is invoked at most once per TTL window; concurrent callers during
  a cache miss share the SAME in-flight call (hand-rolled single-flight —
  no thundering herd on the auth server, no external dependency).
- The returned `invalidate func()` immediately expires the cached
  credential. `NewCachingCredentialFunc` does NOT know when a credential is
  rejected — the server only reveals that via a 401 response, which is
  observed by `Call`, not by `CredentialFunc` (which runs before the
  network call). Wire `invalidate` to `CallOptions.OnCredentialRejected` and
  retry once, explicitly:

```go
callOpts := nethttp.CallOptions{
    CredentialFunc:       credFn,
    OnCredentialRejected: invalidate, // purges the cache; does NOT retry
}
resp, err := nethttp.CallHandle(ctx, client, baseURL, handle, req, callOpts)

var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusUnauthorized {
    resp, err = nethttp.CallHandle(ctx, client, baseURL, handle, req, callOpts) // fresh credential now
}
```

`OnCredentialRejected` is purely a notification hook — `Call` never retries
automatically, keeping control flow explicit and in the caller's hands.

`CachingCredentialFuncOptions.Observer`, when set, receives
`stats.CredentialCacheObserver` hit/refresh events
(`RecordCredentialCacheHit`/`RecordCredentialCacheRefresh`), each including a
`location` string derived from the security scheme names of the request's
`[]route.SecurityRequirement`.

One `NewCachingCredentialFunc` instance is one cache entry — construct a
separate instance per credential scope (e.g. per host/registry) when
different routes need independently-cached credentials.

### Live-reloadable credentials — `codex.Mutable[T]`/`codex.Cacheable[T]`

`SecurityFunc`/`CredentialFunc` are plain closures — nothing above
requires them to close over a static value. A rotating signing key
(server-side `SecurityFunc`) or a TTL-bearing credential cache
(client-side `CredentialFunc`) can capture a `*codex.Mutable[T]`/
`*codex.Cacheable[T]` and call `.Get()` INSIDE the closure body, exactly
like any other variable — no dedicated wrapper API exists or is planned
for this; a plain static value, `NewCachingCredentialFunc`'s own
hand-rolled cache, and these two containers are all equally valid
choices for the SAME closure field. See `docs/concepts/codec.md`'s
"Composing with adapters" subsection (under `Getter`/`Setter`) for the
full pattern, including the one real gotcha (`.Get()` must never be
hoisted out of the closure), and `examples/mutable-security-keys` for a
runnable end-to-end demo wiring both containers into real `nethttp`
server/client hooks.

## Security for event channels (AsyncAPI)

Just like REST, an events security scheme is declared ONCE, directly on the
channel (`events.WithSecurityScheme`) — there is no builder-level scheme
registry — and the SAME declaration is consumed identically by both the
server (`Channel.Register`) and the client (`Channel.ClientHandle`):

```go
var bearerAuth = events.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken))

b := events.NewBuilder(events.Info{Title: "User Events", Version: "1.0.0"})
b.AddServer("production", events.Server{
    URL:      "broker.example.com",
    Protocol: "mqtt5",
    Security: []route.SecurityRequirement{route.Require("bearerAuth")},
})

userCreated, _ := events.NewChannel[UserCreated]("user/created", codec,
    events.Subscribe{
        Summary:  "Receive user created events",
        Security: []route.SecurityRequirement{route.Require("bearerAuth")},
    },
    events.WithSecurityScheme("bearerAuth", bearerAuth),
).Register(b)
```

MQTT5 adapter — the server side runs a BUILT-IN codec-based credential
check (extracting the "Authorization" MQTT5 User Property for `http`/`oauth2`/
`openIdConnect` schemes, or the User Property named `scheme.Name` for
`apiKey` schemes) BEFORE the optional custom `SecurityFunc` — same two-step
order as `nethttp.Handler`:

```go
mqtt5.Subscribe(ctx, client, router, userCreated, handler, mqtt5.SubscribeOptions{
    SecurityFunc: func(ctx context.Context, msg *paho.Publish, reqs []route.SecurityRequirement) error {
        // Runs AFTER the built-in Codec check passes — add extra business
        // logic here (e.g. a database revocation check) if needed.
        return checkNotRevoked(msg, reqs)
    },
})
```

The client (publish) side is symmetric: `PublishOptions.CredentialFunc`
supplies the credential as MQTT5 User Properties, and the SAME built-in
codec check runs BEFORE the message is actually published — mirroring
`nethttp.Call`'s `CredentialFunc` handling exactly, including the
"a `CredentialFunc` returning `(nil, nil)` for 'no credential needed' is not
an error" contract:

```go
mqtt5.Publish(ctx, client, userCreated, 1, false, event, nil, mqtt5.PublishOptions{
    CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) ([]mqtt5.UserProperty, error) {
        token, err := fetchToken(ctx)
        if err != nil {
            return nil, err
        }
        return []mqtt5.UserProperty{{Key: "Authorization", Value: "Bearer " + token}}, nil
    },
})
```

MQTT 3.1.1 (`adapters/mqtt`) and ZeroMQ pub/sub have no per-message metadata
channel — Codec-level extraction (and `CredentialFunc`) only applies to
MQTT5; use `SecurityFunc` + a closure over connection-time credentials for
runtime enforcement on those transports instead.

## Security for request-reply routes (reqreply)

`api/reqreply` (MQTT5 only — `adapters/zeromq`'s reqreply `Call`/`Serve` have
no security mechanism today, since ZeroMQ carries no per-message metadata;
documented future gap) mirrors the exact same declare-once,
enforce-symmetrically model:

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

Security schemes appear in `components/securitySchemes`; global security at document root (REST only — AsyncAPI 3.0 has no document-level global security field); per-operation security overrides inline — all generated automatically from `WithSecurityScheme` (route/channel-level for REST/events/reqreply — the ONLY declaration mechanism for all three; aggregated by `Builder.OpenAPISpec`/`Builder.AsyncAPISpec`, last-registered-wins on name collision) / `AddGlobalSecurity` / `RouteMeta.Security` / `Subscribe.Security` / `Publish.Security`. No manual YAML needed.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — `SecurityFunc` wiring in the adapter
- [Guide: HTTP Client](../guides/http-client.md) — `CredentialFunc` for client-side credentials
- [Guide: Observer](../guides/observer.md) — `SecurityObserver` metrics
- [examples/adapters-nethttp-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security) — bearer JWT + scopes + observer
