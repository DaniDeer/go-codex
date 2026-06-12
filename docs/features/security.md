# Security & Authentication

> See also: [`route` package on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/route)
>
> Runnable demos: [`examples/adapters-nethttp-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security) · [`examples/adapters-chi-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-chi-security) · [`examples/adapters-mqtt-security`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-mqtt-security)

go-codex documents security requirements in the spec and provides declarative hooks for runtime enforcement. Security schemes are registered on the builder; runtime credential validation is handled by adapters via a `SecurityFunc` hook — **the library does not import any crypto or JWT library**.

## Security schemes (REST)

```go
import (
    "github.com/DaniDeer/go-codex/api/rest"
    "github.com/DaniDeer/go-codex/route"
    "github.com/DaniDeer/go-codex/validate"
)

b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})

// Register schemes — spec fields flow into OpenAPI; Codec validates the raw credential.
b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken))) // format check before SecurityFunc

b.AddSecurityScheme("apiKey", rest.SecurityScheme{
    SecurityScheme: route.APIKeyScheme("X-API-Key", "header"),
})
```

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

```go
// Global security — applies to all operations by default.
b.AddGlobalSecurity(route.Require("bearerAuth"))

// Per-route override — nil inherits global; empty slice = no auth required.
createUser, _ := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, respCodec,
    rest.RouteMeta{
        OperationID: "createUser",
        // Requires bearerAuth with write:users scope
        Security: []route.SecurityRequirement{
            route.Require("bearerAuth", "write:users"),
        },
    },
).Register(b)

// Explicitly public — empty slice overrides global security
publicRoute, _ := rest.NewRoute[struct{}, Info]("GET", "/health",
    codex.Empty, infoCodec,
    rest.RouteMeta{
        Security: []route.SecurityRequirement{}, // no auth required
    },
).Register(b)
```

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

Security schemes appear in `components/securitySchemes`; global security at document root; per-operation security overrides inline — all generated automatically from `AddSecurityScheme` / `AddGlobalSecurity` / `RouteMeta.Security`. No manual YAML needed.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — `SecurityFunc` wiring in the adapter
- [Guide: HTTP Client](../guides/http-client.md) — `CredentialFunc` for client-side credentials
- [Guide: Observer](../guides/observer.md) — `SecurityObserver` metrics
- [examples/adapters-nethttp-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security) — bearer JWT + scopes + observer
