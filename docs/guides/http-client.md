# Guide: HTTP Client

This guide walks through the HTTP client example. For the full API reference, see the feature page.

**Feature:** [HTTP Client — typed HTTP calls](../features/http-client.md)

## examples/adapters-nethttp-client

The most comprehensive client demo. Every call in the example shares ONE
`(client, baseURL)` pair, so it builds a single `caller :=
nethttp.NewCaller(srv.Client(), srv.URL)` right after the server starts
and reuses it via `nethttp.Call` for every call thereafter, passing the
SAME `contract.Route` value the server registered directly — the
recommended pattern for every call. Demonstrates both usage patterns in
five numbered sections:

1. **Body** — POST /users with a shared contract: `contract.CreateUser.Register(builder)` (server) and `nethttp.Call(ctx, caller, contract.CreateUser, req, opts)` (client) both operate on the SAME `rest.Route` value
   - **1b. Client-side typed error decode** — `CreateUser` declares `rest.ErrorPattern[EmailConflictError, EmailConflictError](409, ...)`; calling `Call` with a duplicate email returns a decoded `nethttp.ErrorPatternResponse` instead of the untyped `UnexpectedStatusError` — see "Handling the response" below
2. **Path params** — GET /users/{id} with a path MERGE field (`rest.NewPathParam`) so `Call` derives the path value directly from the request struct, codec validated client-side before any HTTP call is sent
3. **Cookies + headers** — GET /profile with `CallOptions.CookieParams` + `CallOptions.HeaderParams`; empty or invalid values are rejected pre-flight
4. **Security** — GET /data with a credential-providing implementation attached via `Route.ClientMW(mw, fn)` (paired against the route's declared `middleware.Middleware`) injecting an Authorization header; demonstrates all three cases: happy path, no credentials (401), credential-Fn error (pre-flight abort)
5. **OpenAPI spec** — same `rest.Builder` used by the server generates the full spec

Observer pattern:
- `CountingObserver` records calls by HTTP status code (status 0 = pre-flight abort, no request sent)
- `RecordValidationError` fires per failing field with `location` = `"path"`, `"query"`, `"cookie"`, `"header"`

Structured error logging via `errors.As` + named `slog.Logger`:
```go
logger := slog.Default().With("transport", "http-client")
var pathErr rest.PathParamError
if errors.As(err, &pathErr) {
    logger.Warn("param rejected (no request sent)",
        "param", pathErr.Name,
        "cause", pathErr.Err,
    )
}
```

→ [examples/adapters-nethttp-client](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client)

## Call vs. CallWithHandle

`nethttp.Call` is the SOLE public client-side entry point and the
recommended pattern for every call — it takes a `rest.Route` value
directly:

```go
caller := nethttp.NewCaller(client, baseURL) // build once
user, err := nethttp.Call(ctx, caller, contract.CreateUser, req, nethttp.CallOptions{})
```

`nethttp.CallWithHandle` — the lower-level, handle-based primitive `Call`
wraps internally — remains public and is still needed directly for
callers that already have a `*rest.RouteHandle` but no `rest.Route`
value: `ports.Pattern`'s REST binding machinery (`DrainCallAdapter`/
`CallAdapter`), which owns its own client/baseURL via `PortOptions`, and
`adapters/mcprest`'s REST-to-MCP bridge.

## Handling the response: happy path vs error path

`nethttp.Call` (and the lower-level `nethttp.CallWithHandle` it wraps)
always return exactly `(Resp, error)` — the "one struct, one call"
contract holds for BOTH directions. There is no partial-success shape to
handle: either you get a fully-decoded, fully-merged `Resp`, or you get a
non-nil `error`.

### Happy path — use the returned value directly

```go
user, err := nethttp.Call(ctx, caller, contract.CreateUser, req, nethttp.CallOptions{})
if err != nil {
    // handle the error path — see below
    return err
}
// user is fully decoded: body + any response header/cookie merge fields
fmt.Println(user.ID, user.Name)
```

No status-code check is needed before using the value — any non-2xx
response, decode failure, or pre-flight validation failure is ALWAYS
returned as a non-nil `error` instead. A nil error guarantees a usable
`Resp`.

### Error path — walk the error chain with `errors.As`

Every failure mode `Call` can produce is a distinct,
`errors.As`-navigable typed error. Check them in the order they can occur
— pre-flight (no network call sent) first, then response-side:

```go
_, err := nethttp.Call(ctx, caller, route, req, opts)
if err == nil {
    return // happy path handled above
}

// Pre-flight: param codec validation failed — no HTTP request was sent.
var pathErr rest.PathParamError
if errors.As(err, &pathErr) {
    return fmt.Errorf("invalid %s: %w", pathErr.Name, pathErr.Err)
}
var queryErr rest.QueryParamError
if errors.As(err, &queryErr) { /* ... */ }
var cookieErr rest.CookieParamError
if errors.As(err, &cookieErr) { /* ... */ }
var headerErr rest.HeaderParamError
if errors.As(err, &headerErr) { /* ... */ }

// Pre-flight: request construction/credential failure.
var buildErr nethttp.RequestBuildError
if errors.As(err, &buildErr) { /* malformed base URL, cancelled ctx, ... */ }

// Response-side: the request was sent but failed at the network layer.
var reqErr nethttp.RequestError
if errors.As(err, &reqErr) {
    return retry(req) // network/DNS/TLS/timeout — safe to retry
}

// Response-side: a declared rest.ErrorPattern matched and decoded — typed
// business error, decide what to do per Value's concrete type.
var patternResp nethttp.ErrorPatternResponse
if errors.As(err, &patternResp) {
    switch v := patternResp.Value.(type) {
    case domain.EmailConflictError:
        return promptDifferentEmail(v.Email)
    default:
        return fmt.Errorf("unexpected error payload: %+v", v)
    }
}

// Response-side: no ErrorPattern matched (or its body failed to decode) —
// raw status + bytes, the universal fallback.
var statusErr nethttp.UnexpectedStatusError
if errors.As(err, &statusErr) {
    return fmt.Errorf("unexpected status %d: %s", statusErr.StatusCode, statusErr.Body)
}

// Response-side: body could not even be read after a successful connection.
var bodyErr nethttp.ResponseBodyError
if errors.As(err, &bodyErr) { /* ... */ }
```

Rule of thumb for "continuing" after an error:
- **Pre-flight param errors** (`rest.PathParamError`/`QueryParamError`/
  `CookieParamError`/`HeaderParamError`) mean YOUR request was malformed —
  fix the input, never retry as-is.
- **`nethttp.RequestError`** is a transport-layer failure (network/DNS/TLS/
  timeout) — safe to retry with backoff.
- **`nethttp.ErrorPatternResponse`** is a decoded, typed BUSINESS error the
  server declared — branch on `.Value`'s concrete type and handle it like
  any other domain error (see the "Client-side decode" section in the
  [REST API feature page](../features/rest-api.md#client-side-decode--nethttpcall-and-errorpatternresponse)).
- **`nethttp.UnexpectedStatusError`** is the universal fallback for any
  status/body the route didn't declare a typed pattern for — log the raw
  status + body, do not assume a specific shape.

## Binary requests and responses (PNG, JPEG, PDF…)

The client (`nethttp.Call`/`CallWithHandle`) supports binary request bodies and binary response bodies the same way as JSON — register `format.Binary` on the route handle and the client sets headers and validates automatically.

### Sending a binary request body

Register `format.Binary` via `WithRequestFormats`. The client calls `format.Binary.Marshal` (validates magic bytes and size), sets `Content-Type: image/png`, and sends the raw bytes as the request body. The route's path variable must be declared as a MERGE field (`rest.NewPathParam`, not a plain `PathParam`) since `Call`/`CallWithHandle` derive path values ONLY from merge fields — there is no manual `vars map[string]string` escape hatch:

```go
pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(5 * 1024 * 1024)).
    Refine(validate.PNG)

uploadHandle := uploadRoute.ClientHandle()
uploadHandle.WithRequestFormats(format.Binary(pngCodec).WithContentType("image/png"))

meta, err := nethttp.CallWithHandle(ctx, client, baseURL, uploadHandle, pngBytes,
    nethttp.CallOptions{Observer: obs},
)
```

The `Content-Type: image/png` header is set automatically from the registered format. `examples/png-upload`'s own routes currently declare a plain `rest.PathParam`/`rest.CookieParam` (server-only, no client-side call in that example) — switch to `rest.NewPathParam`/`rest.NewRequiredCookieParam` if you need this route to also be client-callable.

### Receiving a binary response body

Register `format.Binary` via `WithFormats`. The client sets `Accept: image/png`, reads the raw response body, and calls `format.Binary.Unmarshal` (validates magic bytes and size before returning):

```go
downloadHandle := downloadRoute.ClientHandle()
downloadHandle.WithFormats(format.Binary(pngCodec).WithContentType("image/png"))

png, err := nethttp.CallWithHandle(ctx, client, baseURL, downloadHandle, downloadReq,
    nethttp.CallOptions{Observer: obs},
)
// png is validated (magic bytes + size) — safe to write to disk or display
```

The `Accept: image/png` header is set automatically. A server that returns a different `Content-Type` will cause `format.Binary.Unmarshal` to fail constraint validation (magic-byte mismatch).

### Both directions

A route that uploads binary and returns binary registers both:

```go
handle.WithRequestFormats(format.Binary(pngCodec).WithContentType("image/png"))
handle.WithFormats(format.Binary(pngCodec).WithContentType("image/png"))
```

See [`examples/png-upload`](https://github.com/DaniDeer/go-codex/tree/main/examples/png-upload) for upload (binary request → JSON response) and download (JSON request → binary response) routes with full codec validation.
