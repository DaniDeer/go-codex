# HTTP Client

> See also: [`adapters/nethttp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp)
>
> Runnable demo: [`examples/adapters-nethttp-client`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client)

The same `adapters/nethttp` package that drives the server provides a typed
HTTP client that reuses the same `Route` definition, codecs, and parameter
constraints. **No duplication between client and server.**

Two entry points exist:

- **`rest.Client.Call`** (bound via `nethttp.Attach`) — the single-workflow
  entry point (Decision 6), and the RECOMMENDED pattern for every call.
  Build a `rest.Client` once, `nethttp.Attach` it to an `*http.Client` +
  baseURL, then call `client.Call(ctx, route, req)` with a `rest.Route`
  value directly (the SAME value the server registers) — no separate
  "build a client copy" step needed at all.
- **`nethttp.CallWithHandle`** — the lower-level, handle-based primitive
  `Attach`'s internal transport wraps. Stays public for callers that
  already have a `*rest.RouteHandle` but no `rest.Route` value, or that
  need a v1-scope feature `Attach`'s reflection shim doesn't cover yet
  (path/query/header/cookie params, security/credential handling,
  per-call format override, error-pattern decoding) — e.g. `ports.Pattern`'s
  REST binding machinery (`DrainCallAdapter`/`CallAdapter`), which owns
  its own client/baseURL via `PortOptions`, or `adapters/mcprest`'s
  REST-to-MCP bridge.

The package's former `Caller`/`NewCaller`/`Call[Req,Resp]` (a two-tier
value-based convenience predating `Attach`) are now an unexported internal
`caller`/`newCaller`/`call[Req,Resp]`, reachable only through `Attach`.

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
server/main.go  ← imports contract/, registers routes, calls AttachMux+Serve
client/main.go  ← imports contract/, calls via rest.Client.Call
```

```go
// contract/contract.go
var CreateUser = rest.NewRoute[CreateUserReq, User](
    "POST", "/users", createUserReqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser"},
)

// server.go — declare the handler, register, and wire the whole builder
err := contract.CreateUser.WithHandler(myHandler).Register(builder)
err = nethttp.AttachMux(builder, mux, addr)
go func() { _ = builder.Serve(ctx) }()

// client.go — reuse the SAME Route value directly, no separate registration
client := rest.NewClient()
err = nethttp.Attach(client, http.DefaultClient, "https://api.example.com")

userAny, err := client.Call(ctx, contract.CreateUser,
    CreateUserReq{Name: "Alice", Email: "alice@example.com"})
user := userAny.(User)
```

### Pattern 2 — Client-only (ClientHandle / no builder)

When the client has no server role, `rest.Client.Call` still works directly
with a `rest.Route` value — no `Builder`, no server-side `Register` call
needed at all. Internally, the reflection shim derives the handle via
`Route.ClientHandle()`.

```go
var getUser = rest.NewRoute[GetUserReq, User]("GET", "/users/{id}",
    getReqCodec, userCodec,
    rest.NewPathParam("id", uuidCodec,
        func(r GetUserReq) string { return r.ID },
        func(r *GetUserReq, v string) { r.ID = v }),
)

client := rest.NewClient()
err := nethttp.Attach(client, http.DefaultClient, "https://api.example.com")
userAny, err := client.Call(ctx, getUser, GetUserReq{ID: userID})
user := userAny.(User)
```

Note the path value is derived from `req.ID` automatically via the
declared merge field ([`rest.NewPathParam`](rest-api.md)) — the reflection
shim always auto-derives path/query/header/cookie values from a route's
declared merge fields; there is no manual `vars map[string]string` escape
hatch. A route intended for client use must declare a merge field for
every path/query/header/cookie value it needs. This v1-scope shim covers
the CORE common case only (JSON body, no per-call format override, no
security/credential handling) — use `nethttp.CallWithHandle` directly for
anything beyond that.

## nethttp.Attach

```go
func Attach(client *rest.Client, httpClient *http.Client, baseURL string) error
```

`Attach` binds `httpClient`+`baseURL` (via an internal, unexported
`caller`/`newCaller` — a pure `(client, baseURL)` holder, NOT a
spec-accumulating `Builder` equivalent) as `client`'s `rest.ClientTransport`,
giving `client` its `Call(ctx, route, req)` call shape. Credential
fulfillment lives on the `Route` itself via `ClientMW`, not on the
internal caller.

## nethttp.CallWithHandle

```go
func CallWithHandle[Req, Resp any](
    ctx     context.Context,
    client  *http.Client,
    baseURL string,
    handle  *rest.RouteHandle[Req, Resp],
    req     Req,
    opts    CallOptions,
) (Resp, error)
```

`CallWithHandle` derives `vars`/`QueryParams`/`HeaderParams`/`CookieParams`
from `req` automatically via the route's declared merge fields, and merges
any response merge fields (e.g. a declared response header) back into the
returned value — the full request+response, single-call story, with full
type safety (no `any` cast) and access to every `CallOptions` field.
`rest.Client.Call` (via `Attach`) performs the identical derivation
internally for the common case, trading some of these features for a
uniform `Call(ctx, route, req)` shape across REST/pub-sub.

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
handle := securedRoute.ClientHandle()
resp, err := nethttp.CallWithHandle(ctx, httpClient, baseURL, handle, struct{}{}, opts)
```

## See also

- [Concept: Go Library as Contract](../concepts/codec-as-contract.md) — shared contract pattern
- [Guide: HTTP Server](../guides/http-server.md) — server-side setup
- [Guide: HTTP Client](../guides/http-client.md) — full walkthrough of the runnable demo
- [Guide: Error Handling](error-handling.md) — all typed errors
- [Guide: Observer](observer.md) — metrics wiring
- [examples/adapters-nethttp-client](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client) — full demo with shared contract, cookies, headers, security, credential caching, Observer + slog, all via `nethttp.CallWithHandle`
