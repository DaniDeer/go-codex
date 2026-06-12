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
