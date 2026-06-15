# Guide: HTTP Client

This guide walks through the HTTP client example. For the full API reference, see the feature page.

**Feature:** [HTTP Client — typed HTTP calls](../features/http-client.md)

## examples/adapters-nethttp-client

The most comprehensive client demo. Demonstrates both usage patterns in five numbered sections:

1. **Body** — POST /users with a shared contract: `contract.CreateUser.Register(builder)` and `contract.CreateUser.ClientHandle()` both produce the same typed `RouteHandle`
2. **Path params** — GET /users/{id} with `PathParam.WithCodec(...)` codec validated client-side before any HTTP call is sent
3. **Cookies + headers** — GET /profile with `CallOptions.CookieParams` + `CallOptions.HeaderParams`; empty or invalid values are rejected pre-flight
4. **Security** — GET /data with `CallOptions.CredentialFunc` injecting `Authorization: Bearer <token>`; demonstrates all three cases: happy path, no credentials (401), CredentialFunc error (pre-flight abort)
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

## Binary requests and responses (PNG, JPEG, PDF…)

The client (`nethttp.Call`) supports binary request bodies and binary response bodies the same way as JSON — register `format.Binary` on the route handle and the client sets headers and validates automatically.

### Sending a binary request body

Register `format.Binary` via `WithRequestFormats`. The client calls `format.Binary.Marshal` (validates magic bytes and size), sets `Content-Type: image/png`, and sends the raw bytes as the request body:

```go
pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(5 * 1024 * 1024)).
    Refine(validate.PNG)

uploadHandle := uploadRoute.ClientHandle()
uploadHandle.WithRequestFormats(format.Binary(pngCodec).WithContentType("image/png"))

meta, err := nethttp.Call(ctx, client, baseURL, uploadHandle, pngBytes,
    map[string]string{"id": imageID},
    nethttp.CallOptions{Observer: obs},
)
```

The `Content-Type: image/png` header is set automatically from the registered format.

### Receiving a binary response body

Register `format.Binary` via `WithFormats`. The client sets `Accept: image/png`, reads the raw response body, and calls `format.Binary.Unmarshal` (validates magic bytes and size before returning):

```go
downloadHandle := downloadRoute.ClientHandle()
downloadHandle.WithFormats(format.Binary(pngCodec).WithContentType("image/png"))

png, err := nethttp.Call(ctx, client, baseURL, downloadHandle, downloadReq,
    map[string]string{"id": imageID},
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
