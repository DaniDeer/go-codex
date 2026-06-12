# HTTP Client

> See also: [`adapters/nethttp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp)
>
> Runnable demo: [`examples/adapters-nethttp-client`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client)

The same `adapters/nethttp` package that drives the server provides `nethttp.Call` — a typed HTTP client that reuses the same `Route` definition, codecs, and parameter constraints. **No duplication between client and server.**

## Two usage patterns

### Pattern 1 — Shared contract (import pattern)

Define routes, codecs, and types in a shared Go package. Both server and client import it. The compiler enforces the contract: any change breaks compilation on both sides immediately — no stale YAML, no code generation.

```
contract/
  contract.go   ← shared Route specs, codecs, types
server/main.go  ← imports contract/, registers routes
client/main.go  ← imports contract/, calls via nethttp.Call
```

```go
// contract/contract.go
var CreateUser = rest.NewRoute[CreateUserReq, User](
    "POST", "/users", createUserReqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser"},
)

// server.go — register and serve
handle, _ := contract.CreateUser.Register(builder)
nethttp.Register(mux, handle, myHandler, nethttp.Options{})

// client.go — reuse the same Route
handle, _ := contract.CreateUser.Register(rest.NewBuilder(...))
// or, if no OpenAPI spec is needed:
handle = contract.CreateUser.ClientHandle()

user, err := nethttp.Call(ctx, http.DefaultClient, "https://api.example.com",
    handle, CreateUserReq{Name: "Alice", Email: "alice@example.com"}, nil,
    nethttp.CallOptions{Observer: obs})
```

### Pattern 2 — Client-only (ClientHandle)

When the client has no server role, use `Route.ClientHandle()` — no `Builder` needed. All codec validation, path building, and parameter checks work identically.

```go
handle := rest.NewRoute[GetUserReq, User]("GET", "/users/{id}",
    getReqCodec, userCodec,
    rest.PathParam{Name: "id"}.WithCodec(uuidCodec),
).ClientHandle()  // no builder, no spec

user, err := nethttp.Call(ctx, http.DefaultClient, "https://api.example.com",
    handle, GetUserReq{}, map[string]string{"id": userID},
    nethttp.CallOptions{})
```

## nethttp.Call

```go
func Call[Req, Resp any](
    ctx     context.Context,
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    req     Req,
    vars    map[string]string,  // path variable substitution
    opts    CallOptions,
) (Resp, error)
```

**What Call does before sending the request:**
1. `BuildPath(vars)` — substitutes `{varName}` placeholders, validates each against its codec
2. `ValidateQuery(opts.QueryParams)` — validates each query param against its codec
3. `ValidateCookies(opts.CookieParams)` — validates each cookie against its codec
4. `ValidateHeaders(opts.HeaderParams)` — validates each header against its codec
5. Resolves security requirements → calls `opts.CredentialFunc` if set
6. Encodes request body (POST/PUT/PATCH only)
7. Executes `client.Do`

A validation failure aborts the call and returns the typed error — no HTTP request is sent.

## CallOptions

```go
nethttp.CallOptions{
    // Codec-validated params (pre-flight)
    QueryParams:  map[string]string{"page": "2"},
    CookieParams: map[string]string{"session_token": token},
    HeaderParams: map[string]string{"X-Tenant-ID": tenantID},

    // Extra headers — no codec validation (User-Agent, X-Request-ID, etc.)
    ExtraHeaders: http.Header{"User-Agent": {"my-client/1.0"}},

    // CredentialFunc — injects Authorization or other credential headers.
    // Called only for routes with Security requirements declared.
    CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
        h := make(http.Header)
        h.Set("Authorization", "Bearer "+getToken(ctx))
        return h, nil
    },

    // Observer — records per-call metrics (RecordRequest, RecordValidationError).
    // Status 0 = pre-flight validation failed, no HTTP call was sent.
    Observer: obs,
}
```

## Error handling

```go
// Non-2xx response
var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) {
    logger.Error("api call failed",
        "method", statusErr.Method,
        "path",   statusErr.Path,
        "status", statusErr.StatusCode,
        "body",   string(statusErr.Body),
    )
}

// Pre-flight path param validation failure — no request was sent
var pathErr rest.PathParamError
if errors.As(err, &pathErr) {
    logger.Warn("path param rejected (no request sent)",
        "param", pathErr.Name,
        "value", pathErr.Value,
        "cause", pathErr.Err,
    )
}

// Pre-flight query param validation failure
var qpErr rest.QueryParamError
if errors.As(err, &qpErr) {
    logger.Warn("query param rejected (no request sent)",
        "param", qpErr.Name,
        "cause", qpErr.Err,
    )
}

// CredentialFunc returned an error — no request was sent
if errors.Is(err, tokenExpiredErr) {
    logger.Warn("credential error (no request sent)", "cause", err)
}

// Transport failures
var reqErr nethttp.RequestError
if errors.As(err, &reqErr) {
    logger.Error("transport error", "method", reqErr.Method, "cause", reqErr.Err)
}
```

## Observer (metrics)

```go
type CountingObserver struct{ ... }

func (o *CountingObserver) RecordRequest(method, path string, statusCode int, d time.Duration) {
    // statusCode = 0 means pre-flight validation failure — no HTTP call was sent.
    o.byStatus[statusCode]++
    o.latencies = append(o.latencies, d)
}

func (o *CountingObserver) RecordValidationError(location, constraint, field string) {
    // location: "path", "query", "cookie", "header", "body"
    o.valErrorsByLoc[location]++
}
```

Pass `nethttp.CallOptions{Observer: obs}` to every `Call` to collect metrics. In production, replace the in-memory counters with Prometheus or OpenTelemetry instruments.

## See also

- [Concept: Go Library as Contract](../concepts/codec-as-contract.md) — shared contract pattern
- [Guide: HTTP Server](../guides/http-server.md) — server-side setup
- [Guide: Error Handling](error-handling.md) — all typed errors
- [Guide: Observer](observer.md) — metrics wiring
- [examples/adapters-nethttp-client](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client) — full demo with shared contract, cookies, headers, CredentialFunc, Observer + slog
