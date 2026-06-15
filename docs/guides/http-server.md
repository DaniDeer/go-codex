# Guide: HTTP Server

This guide walks through the HTTP server examples in `examples/`. For the full API reference, see the feature pages.

**Features used:**
- [REST API — Routes, Params & OpenAPI](../features/rest-api.md) — NewRoute, params, BuildPath, OpenAPI spec
- [Security & Auth](../features/security.md) — SecurityFunc, bearer JWT, global/per-route
- [SSE & Streaming](../features/sse-streaming.md) — NewSSERoute, templ SSR, chunked streaming
- [Formats & Serialization](../features/formats.md) — multi-format request/response

## examples/adapters-nethttp

The most comprehensive HTTP server demo. Shows the **three-layer codec pipeline**:

- Layer 1: shared field codecs (`emailFieldCodec`, `nameFieldCodec`) propagate constraints to all three boundary codecs (request, database, response)
- Layer 2: pure domain functions (`buildUserRecord`, `buildUserResponse`) with zero IO — independently unit-testable
- Layer 3: infrastructure (`UserStore` uses codec for all DB IO; `nethttp.Register` is the only HTTP line)

Key patterns:
- `createUserRoute.WithRequestFormats(format.JSON(...), format.YAML(...))` — JSON + YAML bodies
- `ResponseHeaderParam` + `ResponseCookieParam` — server-side contract validation on outgoing headers/cookies
- `nethttp.SetCookie` with `.WithCodec()` — symmetric read/write validation using the same codec
- `CountingObserver` — in-memory metrics (swap for Prometheus in production)
- `withDomainLogging` decorator — separates logging concern from handler body

→ [examples/adapters-nethttp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp)

## examples/adapters-nethttp-security

Demonstrates bearer JWT authentication with per-route scope enforcement:

- `b.AddSecurityScheme("bearerAuth", ...)` with `validate.BearerToken` codec
- `b.AddGlobalSecurity(route.Require("bearerAuth"))`
- Per-route scopes: `route.Require("bearerAuth", "profile")` vs `route.Require("bearerAuth", "admin")`
- Custom `ErrorHandler` mapping `invalidCredentialsError` → 401
- `SecurityFunc` that calls `verifyToken()` after codec format validation

→ [examples/adapters-nethttp-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-security)

## examples/adapters-chi

Same patterns as `adapters-nethttp` but using chi router. Demonstrates path vars via `chi.URLParam(r, "id")`.

→ [examples/adapters-chi](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-chi) · [examples/adapters-chi-security](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-chi-security)

## examples/adapters-sse

SSE with path param codec validation, invalid event rejection (nothing written on codec failure), stats observer, and OpenAPI spec showing `Content-Type: text/event-stream`.

→ [examples/adapters-sse](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-sse)

## examples/adapters-templ + examples/adapters-streaming-sse-templ

Same route serves HTML (`Accept: text/html`) and JSON (`Accept: application/json`) via content negotiation. The streaming example adds chunked HTML pages and SSE HTML fragments (HTMX `sse-swap` pattern).

→ [examples/adapters-templ](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-templ) · [examples/adapters-streaming-sse-templ](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-streaming-sse-templ)

## Binary payloads (PNG, JPEG, PDF…)

Binary request and response bodies — image uploads, document downloads, file transfers — work with the nethttp and chi adapters without any adapter changes. The adapter reads and writes raw `[]byte` bodies, and `format.Binary` validates the content via magic-byte and size constraints.

### Incoming binary request body

Use `handle.WithRequestFormats` to register binary as an accepted `Content-Type`. The adapter reads the body, negotiates by the request's `Content-Type` header, then calls `format.Binary.Unmarshal` which validates and returns the raw bytes:

```go
pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(5 * 1024 * 1024)).
    Refine(validate.PNG)

uploadRoute, _ := rest.NewRoute[[]byte, ImageMeta]("PUT", "/images/{id}",
    pngCodec, imageMetaCodec, ...,
).Register(b)

// Accept binary PNG bodies; returns 415 if Content-Type doesn't match
uploadRoute.WithRequestFormats(format.Binary(pngCodec).WithContentType("image/png"))
```

The adapter returns HTTP **415 Unsupported Media Type** if the client sends a `Content-Type` not in the registered set.

### Outgoing binary response body

Use `handle.WithFormats` to register binary as a producible format. The adapter negotiates by the `Accept` header, calls `format.Binary.Marshal` (validates constraints), and sets the response `Content-Type`:

```go
downloadRoute, _ := rest.NewRoute[DownloadReq, []byte]("POST", "/images/{id}/download",
    downloadCodec, pngCodec, ...,
).Register(b)

// Serve binary PNG responses; Accept: */* or Accept: image/png both match
downloadRoute.WithFormats(format.Binary(pngCodec).WithContentType("image/png"))
```

`Accept: */*` (or no Accept header — browsers, curl) matches the first registered format.

### `MaxBodyBytes` and `validate.MaxBytes`

The adapter applies `opts.MaxBodyBytes` via `http.MaxBytesReader` **before** the codec runs:

| What fires | When | HTTP response |
|-----------|------|---------------|
| `opts.MaxBodyBytes` exceeded | Body read fails | HTTP 413 `BodyTooLargeError` |
| Codec `validate.MaxBytes` exceeded | Body already read, constraint fails | HTTP 400 `ConstraintError` |

Recommendation: set `opts.MaxBodyBytes` ≥ the codec's `validate.MaxBytes` limit so the codec's typed error reaches the client rather than a generic HTTP 413.

```go
const maxPNG = 5 * 1024 * 1024 // 5 MiB

pngCodec := codex.Bytes().
    Refine(validate.MaxBytes(maxPNG)).
    Refine(validate.PNG)

// MaxBodyBytes matches the codec limit — codec error fires, not generic 413
nethttp.Register(mux, uploadRoute, handler, nethttp.Options{
    MaxBodyBytes: maxPNG,
})
```

See [`examples/png-upload`](https://github.com/DaniDeer/go-codex/tree/main/examples/png-upload) for a full upload + download route pair with path params, cookie validation, and OpenAPI spec generation.

## examples/api-rest + examples/rest-api + examples/openapi

Standalone REST builder and OpenAPI spec generation demos without an HTTP server.

→ [examples/api-rest](https://github.com/DaniDeer/go-codex/tree/main/examples/api-rest) · [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) · [examples/openapi](https://github.com/DaniDeer/go-codex/tree/main/examples/openapi)
