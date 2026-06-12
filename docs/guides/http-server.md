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

## examples/api-rest + examples/rest-api + examples/openapi

Standalone REST builder and OpenAPI spec generation demos without an HTTP server.

→ [examples/api-rest](https://github.com/DaniDeer/go-codex/tree/main/examples/api-rest) · [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) · [examples/openapi](https://github.com/DaniDeer/go-codex/tree/main/examples/openapi)
