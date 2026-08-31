# HTTP Client

> See also: [`adapters/nethttp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp)
>
> Runnable demo: [`examples/adapters-nethttp-client`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client)

The same `adapters/nethttp` package that drives the server provides a typed
HTTP client that reuses the same `Route` definition, codecs, and parameter
constraints. **No duplication between client and server.**

Two entry points exist:

- **`nethttp.Call`** — the SOLE public client-side entry point, and the
  RECOMMENDED pattern for every call. Takes a `rest.Route` value directly
  (the SAME value the server registers) plus a `*nethttp.Caller` (built
  once per `(client, baseURL)` pair) — no separate "build a client copy"
  step needed at all.
- **`nethttp.CallWithHandle`** — the lower-level, handle-based primitive
  `Call` wraps internally. Stays public for callers that already have a
  `*rest.RouteHandle` but no `rest.Route` value — e.g. `ports.Pattern`'s
  REST binding machinery (`DrainCallAdapter`/`CallAdapter`), which owns
  its own client/baseURL via `PortOptions`, or `adapters/mcprest`'s
  REST-to-MCP bridge.

Credential fulfillment is declared **per-route** via
[`Route.ClientMW`](../features/security.md), paired against the SAME
`middleware.Middleware` value the route's security requirement was
declared with — there is no per-call credential override; a caller
needing a different credential for one specific call builds a DIFFERENT
`Route` value via a fresh `.ClientMW(...)` call.

## Two usage patterns

### Pattern 1 — Shared contract (import pattern)

Define routes, codecs, and types in a shared Go package. Both server and client import it. The compiler enforces the contract: any change breaks compilation on both sides immediately — no stale YAML, no code generation.

```
contract/
  contract.go   ← shared Route specs, codecs, types
server/main.go  ← imports contract/, registers routes, calls Serve
client/main.go  ← imports contract/, calls via nethttp.Call
```

```go
// contract/contract.go
var CreateUser = rest.NewRoute[CreateUserReq, User](
    "POST", "/users", createUserReqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser"},
)

// server.go — declare the handler, register, and wire the whole builder
err := contract.CreateUser.WithHandler(myHandler).Register(builder)
err = nethttp.Serve(mux, builder)

// client.go — reuse the SAME Route value directly, no separate registration
caller := nethttp.NewCaller(http.DefaultClient, "https://api.example.com")

user, err := nethttp.Call(ctx, caller, contract.CreateUser,
    CreateUserReq{Name: "Alice", Email: "alice@example.com"},
    nethttp.CallOptions{Observer: obs})
```

### Pattern 2 — Client-only (ClientHandle / no builder)

When the client has no server role, `nethttp.Call` still works directly
with a `rest.Route` value — no `Builder`, no server-side `Register` call
needed at all. `Call` derives the handle internally via
`Route.ClientHandle()`.

```go
var getUser = rest.NewRoute[GetUserReq, User]("GET", "/users/{id}",
    getReqCodec, userCodec,
    rest.NewPathParam("id", uuidCodec,
        func(r GetUserReq) string { return r.ID },
        func(r *GetUserReq, v string) { r.ID = v }),
)

caller := nethttp.NewCaller(http.DefaultClient, "https://api.example.com")
user, err := nethttp.Call(ctx, caller, getUser, GetUserReq{ID: userID},
    nethttp.CallOptions{})
```

Note the path value is derived from `req.ID` automatically via the
declared merge field ([`rest.NewPathParam`](rest-api.md)) — `Call` always
auto-derives path/query/header/cookie values from a route's declared
merge fields; there is no manual `vars map[string]string` escape hatch.
A route intended for client use must declare a merge field for every
path/query/header/cookie value it needs.

## nethttp.Caller

```go
type Caller struct { /* unexported: client, baseURL */ }

func NewCaller(client *http.Client, baseURL string) *Caller
```

`Caller` is a pure `(client, baseURL)` holder — NOT a spec-accumulating
`Builder` equivalent (the server's `Builder` exists because OpenAPI needs
ONE document; the client has no equivalent accumulation need), and no
longer carries any default middleware list — credential fulfillment lives
on the `Route` itself via `ClientMW`, not on `Caller`.

## nethttp.Call / nethttp.CallWithHandle

```go
func Call[Req, Resp any](
    ctx   context.Context,
    c     *Caller,
    route rest.Route[Req, Resp],
    req   Req,
    opts  CallOptions,
) (Resp, error)

func CallWithHandle[Req, Resp any](
    ctx     context.Context,
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    req     Req,
    opts    CallOptions,
) (Resp, error)
```

`Call` derives `vars`/`QueryParams`/`HeaderParams`/`CookieParams` from
`req` automatically via the route's declared merge fields (internally via
`Route.ClientHandle()` + `CallWithHandle`), and merges any response merge
fields (e.g. a declared response header) back into the returned value —
the full request+response, single-call story. `CallWithHandle` performs
the identical derivation for callers that already have a
`*rest.RouteHandle`.

**What Call does before sending the request:**
1. `BuildPath(vars)` — derives path values from merge fields, validates each against its codec
2. `ValidateQuery(opts.QueryParams)` — validates each query param against its codec
3. `ValidateCookies(opts.CookieParams)` — validates each cookie against its codec
4. `ValidateHeaders(opts.HeaderParams)` — validates each header against its codec
5. Resolves the route's declared Security requirements → runs every
   attached credential-providing implementation declared via
   [`Route.ClientMW`](../features/security.md), GATED by `Satisfies` vs.
   the route's declared requirements, to obtain Authorization (or other
   credential) headers
6. Encodes request body (POST/PUT/PATCH only)
7. Executes `client.Do`

A validation failure aborts the call and returns the typed error — no HTTP request is sent. Note some merge-field constraint failures now surface as `codex.ValidationError` (caught at merge-derive time) rather than `rest.PathParamError` (which only fires from `BuildPath`'s own re-validation).

## CallOptions

```go
nethttp.CallOptions{
    // Codec-validated params (pre-flight) — override the value derived
    // from a merge field for the same key, or add ad-hoc params the
    // struct doesn't declare.
    QueryParams:  map[string]string{"page": "2"},
    CookieParams: map[string]string{"session_token": token},
    HeaderParams: map[string]string{"X-Tenant-ID": tenantID},

    // Extra headers — no codec validation (User-Agent, X-Request-ID, etc.)
    ExtraHeaders: http.Header{"User-Agent": {"my-client/1.0"}},

    // OnCredentialRejected fires on HTTP 401 when a credential-providing
    // ClientMW implementation was attached — wire
    // NewCachingCredentialFunc's returned invalidate func here for a
    // retry-once-on-401 pattern (Call never retries automatically).
    OnCredentialRejected: invalidateCred,

    // Observer — records per-call metrics (RecordRequest, RecordValidationError).
    // Status 0 = pre-flight validation failed, no HTTP call was sent.
    Observer: obs,
}
```

## Error handling

```go
// A response status matching a route-declared rest.ErrorPattern (default
// ErrorRespond action) is decoded automatically — check this BEFORE the
// generic UnexpectedStatusError fallback.
var patternResp nethttp.ErrorPatternResponse
if errors.As(err, &patternResp) {
    conflict := patternResp.Value.(domain.EmailConflictError) // decoded typed payload
    logger.Warn("declared error pattern matched",
        "status", patternResp.StatusCode,
        "value",  conflict,
    )
}

// Non-2xx response with no matching ErrorPattern (or its body failed to
// decode) — the universal fallback, raw status + bytes.
var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) {
    logger.Error("api call failed",
        "method", statusErr.Method,
        "path",   statusErr.Path,
        "status", statusErr.StatusCode,
        "body",   string(statusErr.Body),
    )
}

// Pre-flight path/query/etc. merge-field constraint failure — no request
// was sent (caught at merge-derive time, before BuildPath ever runs)
var valErr codex.ValidationError
if errors.As(err, &valErr) {
    logger.Warn("merge field rejected (no request sent)",
        "field", valErr.Field,
        "cause", valErr.Err,
    )
}

// Pre-flight query param validation failure (from CallOptions.QueryParams, not a merge field)
var qpErr rest.QueryParamError
if errors.As(err, &qpErr) {
    logger.Warn("query param rejected (no request sent)",
        "param", qpErr.Name,
        "cause", qpErr.Err,
    )
}

// A credential-providing ClientMW implementation's Fn returned an
// error — no request was sent
if errors.Is(err, tokenExpiredErr) {
    logger.Warn("credential error (no request sent)", "cause", err)
}

// A credential-providing ClientMW implementation returned a
// MALFORMED credential — the route's declared Codec rejects it LOCALLY
if errors.As(err, &credErr) { // rest.SecurityCredentialError
    logger.Error("malformed credential rejected locally (no request sent)",
        "scheme", credErr.Scheme,
        "cause",  credErr.Err,
    )
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

Pass `nethttp.CallOptions{Observer: obs}` to every call to collect metrics.
In production, replace the in-memory counters with Prometheus or
OpenTelemetry instruments. `stats.WithObserver(ctx, obs)` stores an
observer in `ctx` once — every call that receives that `ctx` picks it up
automatically when `CallOptions.Observer` is nil.

## Credential caching

Re-authenticating on every call is wasteful when the credential fetch does
real work (an OAuth token endpoint, a registry token exchange, etc.).
`NewCachingCredentialFunc` wraps any [`CredentialFunc`](../features/security.md)
with TTL-based caching; concurrent callers during a cache miss share the
same in-flight call.

```go
cachedFn, invalidate := nethttp.NewCachingCredentialFunc(fetchToken,
    nethttp.CachingCredentialFuncOptions{TTL: time.Hour})

securedRoute := contract.GetSecuredData(securedMw).ClientMW(&securedMw, cachedFn)

// Wire invalidate into OnCredentialRejected for a retry-once-on-401 pattern.
opts := nethttp.CallOptions{OnCredentialRejected: invalidate}
resp, err := nethttp.Call(ctx, caller, securedRoute, struct{}{}, opts)
```

## See also

- [Concept: Go Library as Contract](../concepts/codec-as-contract.md) — shared contract pattern
- [Guide: HTTP Server](../guides/http-server.md) — server-side setup
- [Guide: HTTP Client](../guides/http-client.md) — full walkthrough of the runnable demo
- [Guide: Error Handling](error-handling.md) — all typed errors
- [Guide: Observer](observer.md) — metrics wiring
- [examples/adapters-nethttp-client](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client) — full demo with shared contract, cookies, headers, security, credential caching, Observer + slog, all via `nethttp.Call`
