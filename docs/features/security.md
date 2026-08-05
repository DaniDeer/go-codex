# Security & Authentication

> See also: [`route` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/route)
>
> Runnable demos: [`examples/adapters-nethttp-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security) · [`examples/adapters-chi-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-chi-security) · [`examples/adapters-mqtt-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-security)

go-codex documents security requirements in the spec and provides declarative hooks for runtime enforcement — **the library does not import any crypto or JWT library**. For REST, a security scheme is declared ONCE, directly on the route (`rest.WithSecurityScheme`) — there is no builder-level scheme registry — and the SAME declaration is consumed identically by both the server (`Route.Register`) and the client (`Route.ClientHandle`), so one route definition gets IDENTICAL credential-format enforcement on both ends. Runtime credential validation itself is handled by adapters: server-side via a `SecurityFunc` hook, client-side automatically inside `nethttp.Call` (see "HTTP client — CredentialFunc" below).

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
`examples/go-edge-models/docker/registry`'s `NewAuthCredentialFunc`), remains
a deliberate non-error. The request is simply sent without the credential,
and it's up to the server to accept or reject it — symmetric with server-side
`SecurityFunc`.

## Security for event channels (AsyncAPI)

```go
b := events.NewBuilder(events.Info{Title: "User Events", Version: "1.0.0"})
b.AddServer("production", events.Server{
    URL:      "broker.example.com",
    Protocol: "mqtt",
    Security: []route.SecurityRequirement{route.Require("bearerAuth")},
})
b.AddSecurityScheme("bearerAuth", events.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken)))

userCreated, _ := events.NewChannel[UserCreated]("user/created", codec,
    events.Subscribe{
        Summary:  "Receive user created events",
        Security: []route.SecurityRequirement{route.Require("bearerAuth")},
    },
).Register(b)
```

MQTT adapter:

```go
mqtt.SubscribeHandler(ctx, userCreated, handler, mqtt.SubscribeOptions{
    SecurityFunc: func(ctx context.Context, msg pahomqtt.Message, reqs []route.SecurityRequirement) error {
        // Extract token from MQTT 5.0 User Properties or application headers.
        return verifyJWT(msg, reqs)
    },
})
```

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

Security schemes appear in `components/securitySchemes`; global security at document root; per-operation security overrides inline — all generated automatically from REST's `WithSecurityScheme` (route-level; aggregated by `Builder.OpenAPISpec`) / events' `AddSecurityScheme` (builder-level) / `AddGlobalSecurity` / `RouteMeta.Security`. No manual YAML needed.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — `SecurityFunc` wiring in the adapter
- [Guide: HTTP Client](../guides/http-client.md) — `CredentialFunc` for client-side credentials
- [Guide: Observer](../guides/observer.md) — `SecurityObserver` metrics
- [examples/adapters-nethttp-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security) — bearer JWT + scopes + observer
