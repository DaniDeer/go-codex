# Guide: HTTP Client

This guide walks through the HTTP client example. For the full API reference, see the feature page.

**Feature:** [HTTP Client — typed HTTP calls](../features/http-client.md)

## examples/adapters-nethttp-client

The most comprehensive client demo. Every call in the example shares ONE
`transport` (`nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient,
BaseURL})`, built once) against the SAME `contract.Route` value the server
registered. It uses `rest.CallWithTransport` throughout — building each
`contract.Route`'s `*rest.RouteHandle` ONCE via `route.ClientHandle()`
right after the server starts, then reusing that handle for every call —
alongside `rest.NewClient()` + `Client.Attach(transport)`/`Client.Call`'s
uniform `Call(ctx, route, req)` shape. Both are full-featured (path/query/
header/cookie params, security/credential `ClientMW`, per-call format
overrides, error-pattern decoding, and per-call `stats.Observer` overrides
via `ClientCallOptions.Observer` are all supported by either path); this
example deliberately demonstrates the pre-built-handle pattern because it
is the lower-level primitive `Client.Call` itself is built on. Demonstrates
both usage patterns in six numbered sections:

0. **`Client.Attach` — the PREFERRED workflow** — one `Attach` call, then every route call is just `client.Call(ctx, route, req)`
1. **Body** — POST /users with a shared contract: `contract.CreateUser.Register(builder)` (server) and `rest.CallWithTransport(ctx, transport, createUserHandle, req, opts)` (client) both operate on the SAME `rest.Route` value
   - **1b. Client-side typed error decode** — `CreateUser` declares `rest.ErrorPattern[EmailConflictError, EmailConflictError](409, ...)`; calling `CallWithTransport` with a duplicate email returns a decoded `nethttp.ErrorPatternResponse` instead of the untyped `UnexpectedStatusError` — see "Handling the response" below
2. **Path params** — GET /users/{id} with a path MERGE field (`rest.NewPathParam`) so `CallWithTransport` derives the path value directly from the request struct, codec validated client-side before any HTTP call is sent
3. **Cookies + headers** — GET /profile with `ClientCallOptions.CookieParams` + `ClientCallOptions.HeaderParams`; empty or invalid values are rejected pre-flight
4. **Security** — GET /data with a credential-providing implementation attached via `Route.ClientMW(mw, fn)` (paired against the route's declared `middleware.Middleware`) injecting an Authorization header; demonstrates all three cases: happy path, no credentials (401), credential-Fn error (pre-flight abort)
5. **OpenAPI spec** — same `rest.Server` used by the server generates the full spec

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

## CallWithTransport vs. rest.Client.Call

`rest.CallWithTransport` is the lower-level, handle-based, full-featured
primitive — it takes an adapter-built `rest.ClientTransport` plus a
`*rest.RouteHandle` directly (built once via `Route.ClientHandle()`) and
is the recommended pattern for every call in this example (see the
reasoning above):

```go
transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: httpClient, BaseURL: baseURL})
handle := contract.CreateUser.ClientHandle() // build once
user, err := rest.CallWithTransport(ctx, transport, handle, req, rest.ClientCallOptions{})
```

`rest.Client.Call` (bound via `Client.Attach(transport)`) — the single-workflow
entry point `CallWithTransport`'s internal derivation logic also
powers — is the RECOMMENDED pattern for a simpler, uniform `Call(ctx,
route, req)` shape across REST/pub-sub. Both are full-featured (path/query/
header/cookie params, security/credential `ClientMW`, per-call format
overrides, error-pattern decoding — see below). `CallWithTransport` remains public and is still
needed directly for callers that already have a `*rest.RouteHandle` but no
`rest.Route` value: `ports.Pattern`'s REST binding machinery
(`DrainCallAdapter`/`CallAdapter`), which owns its own transport via
`PortOptions`, and `adapters/mcprest`'s REST-to-MCP bridge.

## Handling the response: happy path vs error path

`rest.CallWithTransport` always returns exactly `(Resp, error)` — the "one
struct, one call" contract holds for BOTH directions. There is no
partial-success shape to handle: either you get a fully-decoded,
fully-merged `Resp`, or you get a non-nil `error`.

### Happy path — use the returned value directly

```go
user, err := rest.CallWithTransport(ctx, transport, handle, req, rest.ClientCallOptions{})
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

Every failure mode `CallWithTransport` can produce is a distinct,
`errors.As`-navigable typed error. Check them in the order they can occur
— pre-flight (no network call sent) first, then response-side:

```go
_, err := rest.CallWithTransport(ctx, transport, handle, req, opts)
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
// business error, decide what to do per Value's concrete type. The
// manual errors.As + type-switch dance shown here still works, but see
// "Convenient client-side matching" below for 3 shorter alternatives.
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
  [REST API feature page](../features/rest-api.md#client-side-decode--restcallwithtransport-and-errorpatternresponse)).
  **Give each `ErrorPattern` its own status code** — matching is status-only,
  so two patterns sharing a status make the client always decode via the
  FIRST-declared one, regardless of which the server actually sent (see the
  feature page's callout for the full caveat and the shared-interface
  workaround for legitimately-shared statuses).
- **`nethttp.UnexpectedStatusError`** is the universal fallback for any
  status/body the route didn't declare a typed pattern for — log the raw
  status + body, do not assume a specific shape.
- **A matched `ErrorPatternResponse` is a business DECISION the server
  made deliberately — never safe to blindly retry.** This is the OPPOSITE
  of `nethttp.RequestError` above: retrying an `EmailConflictError`
  verbatim just reproduces the SAME rejection. The caller needs to change
  something (a different email, a refreshed credential) before retrying
  makes sense, if it ever does.

### Convenient client-side matching — `ErrorPatternAs`, `.Match`, `HandleErrorPattern`

The manual `errors.As` + type-switch shown above works for any number of
declared patterns, but 3 shorter alternatives are available — pick
whichever fits the call site (see
[Feature: REST API — client-side decode](../features/rest-api.md#client-side-decode--restcallwithtransport-and-errorpatternresponse)
for the full reference):

All 3 live in `api/rest` (transport-independent — the same helpers work
regardless of which client adapter produced `err`, since every adapter's
own `ErrorPatternResponse` implements the shared `rest.ErrorPatternValuer`
interface):

```go
// 1. ErrorPatternAs — a generic one-line decode helper.
if conflict, ok := rest.ErrorPatternAs[domain.EmailConflictError](err); ok {
    return promptDifferentEmail(conflict.Email)
}

// 2. ErrorPatternOpt.Match — the SAME value declares the pattern
//    (server) AND matches it (client); keep the declaration as a
//    package-level var to use this style.
var emailConflictPattern = rest.ErrorPattern[domain.EmailConflictError, domain.EmailConflictError](409, conflictCodec)
if conflict, ok := emailConflictPattern.Match(err); ok {
    return promptDifferentEmail(conflict.Email)
}

// 3. HandleErrorPattern/Case — closest visual parity to a switch
//    expression; each Case's type is inferred from its closure.
handled := rest.HandleErrorPattern(err,
    rest.Case(func(e domain.EmailConflictError) { promptDifferentEmail(e.Email) }),
    rest.Case(func(e domain.ValidationError) { showValidationErrors(e) }),
)
```

All 3 return `false`/`ok=false` when `err` carries no matched
`ErrorPatternResponse` at all, or when the payload's concrete type
doesn't match — never panics, never assumes a specific shape.

## Binary requests and responses (PNG, JPEG, PDF…)

The client (`rest.CallWithTransport`/`rest.Client.Call`) supports binary request bodies and binary response bodies the same way as JSON — register `format.Binary` on the route handle and the client sets headers and validates automatically.

### Sending a binary request body

Register `format.Binary` via `WithRequestFormats`. The client calls `format.Binary.Marshal` (validates magic bytes and size), sets `Content-Type: image/png`, and sends the raw bytes as the request body. The route's path variable must be declared as a MERGE field (`rest.NewPathParam`, not a plain `PathParam`) since `CallWithTransport`/`rest.Client.Call` derive path values ONLY from merge fields — there is no manual `vars map[string]string` escape hatch:

```go
pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(5 * 1024 * 1024)).
    Refine(validate.PNG)

uploadHandle := uploadRoute.ClientHandle()
uploadHandle.WithRequestFormats(format.Binary(pngCodec).WithContentType("image/png"))

transport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: client, BaseURL: baseURL})
meta, err := rest.CallWithTransport(ctx, transport, uploadHandle, pngBytes,
    rest.ClientCallOptions{Observer: obs},
)
```

The `Content-Type: image/png` header is set automatically from the registered format. `examples/png-upload`'s own routes currently declare a plain `rest.PathParam`/`rest.CookieParam` (server-only, no client-side call in that example) — switch to `rest.NewPathParam`/`rest.NewRequiredCookieParam` if you need this route to also be client-callable.

### Receiving a binary response body

Register `format.Binary` via `WithFormats`. The client sets `Accept: image/png`, reads the raw response body, and calls `format.Binary.Unmarshal` (validates magic bytes and size before returning):

```go
downloadHandle := downloadRoute.ClientHandle()
downloadHandle.WithFormats(format.Binary(pngCodec).WithContentType("image/png"))

png, err := rest.CallWithTransport(ctx, transport, downloadHandle, downloadReq,
    rest.ClientCallOptions{Observer: obs},
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

## `rest.Client`/`nethttp.NewClientTransport` — the single-workflow entry point

`rest.Client` (mirrors `events.Client`'s design exactly) gains a `.Call(ctx, route, req)` method
once an HTTP connection is attached via `Client.Attach(nethttp.NewClientTransport(...))` — this is the single-workflow entry point
(Decision 6) — call it directly on the `*rest.Client` value:

```go
client := rest.NewClient()
if err := client.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: httpClient, BaseURL: baseURL})); err != nil { ... }
respAny, err := client.Call(ctx, getUserRoute, GetUserReq{ID: "f47ac10b"})
resp := respAny.(GetUserResp) // type-assert the result
```

Since `Client.Call` is an ordinary Go method (not generic — Go forbids a method from introducing
its own type parameters), `route`/`req` are passed as `any` and `Req`/`Resp` are recovered
internally via reflection (inside the `clientTransport` `NewClientTransport` returns, wrapping an unexported,
internal `caller`/`call[Req,Resp]` — the package's former public `Caller`/`NewCaller`/
`Call[Req,Resp]`); a mismatch surfaces as `rest.TransportTypeMismatchError`
at CALL time, not a compile error. `Client.Call` is FULL-FEATURED: path/query/header/
cookie params, security/credential `ClientMW`, per-call format override
(`ClientCallOptions.RequestFormats`/`ResponseFormats`), and error-pattern decoding are all
supported — there is no remaining "v1 scope" limitation. `rest.CallWithTransport` (the lower-level,
handle-based primitive built directly on the SAME `ClientTransport` `Client.Attach` wraps) remains fully featured and
unaffected; use it directly for anything beyond `Client.Call`'s `route`/`req`-as-`any` shape
(e.g. a pre-built `*rest.RouteHandle` with no `rest.Route` value, or finer per-call control
`ClientCallOptions` doesn't expose). `Client.Attach` is exclusive —
a second `Attach` call returns `rest.ClientTransportAlreadyAttachedError`. See
`docs/design/d-0001-rest-middleware-workflow-simplification.md`'s Addendum 5 for the full design.
