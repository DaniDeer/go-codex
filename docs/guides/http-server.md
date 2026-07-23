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
- `rest.NewPathParam` — declares the path param's spec/validation AND a merge field in one call; the handler receives an already-merged, validated request (`req.ID`) instead of manually calling `r.PathValue("id")` — see [REST API — Path/query/header params with automatic merge](../features/rest-api.md#pathqueryheader-params-with-automatic-merge)
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

Same patterns as `adapters-nethttp` but using chi router. Demonstrates both
the low-level `chi.URLParam(r, "id")` extraction (for validate-only params)
and `rest.NewPathParam`'s automatic merge (chi's `Handler` calls
`RouteHandle.DecodeMerged` internally exactly like `nethttp.Handler` does).

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

### Same thing, declared through `ports.RESTPattern`

`rest.RequestFormats(...)` and `rest.Formats(...)` are `RouteOpt`s — they slot
directly into `RESTPattern.Opts`, giving the same one-step declaration when
the route is a `ports`-wired boundary instead of a hand-built route:

```go
var Images = codex.Must(ports.NewIOPort[[]byte, ImageMeta]("images", pngCodec, imageMetaCodec, ports.PortOptions{
    RESTBuilder: restBuilder,
}))

imageHandle, err := Images.PluginRESTPattern(ports.RESTPattern{
    Method: "PUT", Path: "/images/{id}",
    Opts: []rest.RouteOpt{
        rest.RequestFormats(format.Binary(pngCodec).WithContentType("image/png")),
    },
})
if err != nil {
    panic(err)
}
```

A type mismatch (formats declared for the wrong type) returns
`rest.FormatOptError` when the pattern is plugged in.

## Pipeline handlers: mapping stream errors to HTTP status

For `nethttp.PipelineHandler` / `chi.PipelineHandler`, declare per-route error
status mapping once on the route:

```go
route, _ := rest.NewRoute[Req, Resp]("POST", "/jobs", reqCodec, respCodec,
    rest.PipelineErrorStatus[domain.ConflictError](http.StatusConflict),
).Register(b)
```

- First matching rule wins.
- Unmatched pipeline errors stay `500`.
- No pipeline value defaults to `503` (`PipelineNoResponseError`), overridable
  with `PipelineErrorStatus[...](status)`.

### Ergonomics: same domain error, no-pipeline vs pipeline

Use one domain error type in both paths; only the mapping locus changes.

```go
type domainConflictError struct {
    Resource string
    Value    string
}

func (e domainConflictError) Error() string {
    return fmt.Sprintf("%s %q already exists", e.Resource, e.Value)
}

baseErrorHandler := func(w http.ResponseWriter, _ *http.Request, status int, err error) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(status)
    _ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
```

No-pipeline (`Handler`): map status in custom `ErrorHandler`.

```go
noPipelineErrHandler := func(w http.ResponseWriter, r *http.Request, status int, err error) {
    var conflict domainConflictError
    if errors.As(err, &conflict) {
        status = http.StatusConflict
    }
    baseErrorHandler(w, r, status, err)
}

nethttp.Register(mux, noPipelineRoute, noPipelineFn,
    nethttp.Options{ErrorHandler: noPipelineErrHandler})
```

Pipeline (`PipelineHandler`): map status in route declaration; keep custom
`ErrorHandler` for response-body shaping.

```go
pipelineRoute, _ := rest.NewRoute[Req, Resp]("POST", "/erg/pipeline", reqCodec, respCodec,
    rest.PipelineErrorStatus[domainConflictError](http.StatusConflict),
).Register(b)

nethttp.RegisterPipeline(mux, pipelineRoute, pipelineFn,
    nethttp.Options{ErrorHandler: baseErrorHandler})
```

### Current possibilities matrix (today)

| Capability | No-pipeline (`Handler`) | Pipeline (`PipelineHandler`) |
|-----------|--------------------------|------------------------------|
| One-struct-one-call request/response | Yes (`Req` decode + `Resp` encode via route codecs) | Yes (same route codec path; pipeline fn gets typed `Req`, returns typed `Resp` stream) |
| Custom error body/envelope | Yes (`Options.ErrorHandler`) | Yes (`Options.ErrorHandler`) |
| Where status code mapping lives | `ErrorHandler` (or default adapter status) | Route declaration via `PipelineErrorStatus[...]` for stream errors; then `ErrorHandler` writes body |
| Typed route-level error mapping | No (handled in `ErrorHandler`) | Yes (`rest.PipelineErrorStatus[E](status)`) |
| No emitted pipeline value handling | N/A | `PipelineNoResponseError` default `503` (overridable by route mapping) |
| Redirect success path (`3xx` + `Location`) | `RespStatus` + response header merge / `WithResponseHeaders` | Same (pipeline does not change redirect mechanics) |

`examples/adapters-nethttp` now includes both routes (`/ergonomics/no-pipeline`
and `/ergonomics/pipeline`) with the same domain error type so you can compare
the two styles directly.

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
