# REST API — Routes, Params & OpenAPI

> See also: [`api/rest` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) · [`adapters/nethttp` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp) · [`adapters/chi` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/chi)

`api/rest` is a transport-agnostic REST API builder. The same builder that drives runtime decode/encode/validate also generates a complete OpenAPI 3.1 spec — one definition for both.

## Declaring routes

Routes are plain values — declare once, pass around, register anywhere:

```go
b := rest.NewBuilder(
    rest.Info{Title: "User API", Version: "1.0.0"},
    rest.WithPathConstraints(validate.HTTPPath),
)
b.AddServer("production", rest.Server{URL: "https://api.example.com/v1"})

// POST /users — request body validated, 201 response
createUser, _ := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    createUserReqCodec, userCodec,
    rest.RouteMeta{
        OperationID:    "createUser",
        Summary:        "Create a user",
        ReqSchemaName:  "CreateUserRequest",
        RespSchemaName: "User",
        RespStatus:     "201",
    },
    rest.ResponseMeta{Status: "400", Description: "Validation error."},
).Register(b)

// GET /users/{id} — path param validated as UUID
uuidCodec := codex.String().Refine(validate.UUID)
getUser, _ := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
    codex.Empty, userCodec,
    rest.RouteMeta{OperationID: "getUser", RespSchemaName: "User"},
    rest.PathParam{Name: "id", Description: "User UUID"}.WithCodec(uuidCodec),
).Register(b)

// GET /users — query params
pageCodec := codex.String().Refine(validate.NonNegativeIntString)
listUsers, _ := rest.NewRoute[struct{}, []User]("GET", "/users",
    codex.Empty, codex.SliceOf(userCodec),
    rest.RouteMeta{OperationID: "listUsers"},
    rest.QueryParam{Name: "page"}.WithCodec(pageCodec),
    rest.QueryParam{Name: "search"},
).Register(b)

// GET /profile — cookie + header params
sessionCodec   := codex.String().Refine(validate.NonEmptyString)
requestIDCodec := codex.String().Refine(validate.UUID)
profile, _ := rest.NewRoute[struct{}, User]("GET", "/profile",
    codex.Empty, userCodec,
    rest.RouteMeta{OperationID: "getProfile"},
    rest.CookieParam{Name: "session_token", Required: true}.WithCodec(sessionCodec),
    rest.HeaderParam{Name: "X-Request-Id",  Required: true}.WithCodec(requestIDCodec),
).Register(b)
```

## Parameter types

All param types have `.WithCodec(c codex.Codec[string])`:

| Type | Location | Auto-validated by | Schema in spec |
|---|---|---|---|
| `PathParam` | `{varName}` in path | `BuildPath` | `in: path` |
| `QueryParam` | `?key=value` | `ValidateQuery` | `in: query` |
| `CookieParam` | `Cookie:` header | `ValidateCookies` | `in: cookie` |
| `HeaderParam` | request header | `ValidateHeaders` | `in: header` |
| `ResponseHeaderParam` | response header | after handler returns | `responses[status].headers` |
| `ResponseCookieParam` | `Set-Cookie:` | after handler returns | `responses[status].headers["Set-Cookie"]` |

> **OpenAPI convention:** Do not declare `Accept`, `Content-Type`, or `Authorization` as `HeaderParam` entries — use request body and security schemes instead.

## BuildPath — type-safe URL construction

```go
path, err := getUser.BuildPath(map[string]string{"id": "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
// → "/users/f47ac10b-58cc-4372-a567-0e02b2c3d479"
// err: rest.PathParamError or rest.MissingPathVarError on failure
```

## net/http adapter

```go
import nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"

mux := http.NewServeMux()

// Register uses the Go 1.22+ "METHOD /path" ServeMux pattern automatically.
nethttp.Register(mux, createUser, func(ctx context.Context, req CreateUserReq) (User, error) {
    return svc.CreateUser(ctx, req)
}, nethttp.Options{Observer: obs})

http.ListenAndServe(":8080", mux)
```

**What the adapter handles automatically:**
- Body decode + validate → 400 on failure
- Query, cookie, header param validation before handler runs
- Content-Type enforcement (default `application/json`) → 415 on mismatch
- Body size limit (default 1 MiB) → 413 on overflow
- Response header/cookie validation after handler → 500 on contract violation
- Content negotiation via `Accept` header (when `WithFormats` is set) → 406 on mismatch

**Options:**

| Option | Default | Effect |
|---|---|---|
| `ErrorHandler` | JSON `{"error":"..."}` | Custom error response |
| `Observer` | `stats.NoopObserver{}` | Per-request metrics |
| `MaxBodyBytes` | 1 MiB | Body size limit |
| `ContentType` | `application/json` | Expected Content-Type for body methods |
| `MultiValueQueryParams` | false | Use `ValidateQueryMulti` for repeated keys |
| `SecurityFunc` | nil | Called after credential codec validation |

## chi adapter

```go
import (
    gochi      "github.com/go-chi/chi/v5"
    chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
)

r := gochi.NewRouter()
chiadapter.Register(r, getUser, func(ctx context.Context, _ struct{}) (User, error) {
    rr, _ := chiadapter.RequestFromContext(ctx)
    return svc.GetUser(ctx, gochi.URLParam(rr, "id"))
}, chiadapter.Options{})
```

Same feature set as `adapters/nethttp` — all param validation, response headers/cookies, content negotiation, observer.

## Multi-format request/response

```go
// Accept JSON or YAML request bodies
createUser = createUser.WithRequestFormats(
    format.JSON(createUserReqCodec),
    format.YAML(createUserReqCodec),
)

// Serve HTML or JSON responses
articleRoute = articleRoute.WithFormats(
    adapttempl.Format(propsCodec, ArticleCard), // Accept: text/html
    format.JSON(propsCodec),                    // Accept: application/json
)
```

## Response headers and cookies

```go
// Inside a handler: deposit headers via ctx
nethttp.Register(mux, createUser, func(ctx context.Context, req CreateUserReq) (User, error) {
    u := svc.CreateUser(ctx, req)
    if h, ok := nethttp.ResponseHeadersFromContext(ctx); ok {
        h.Set("Location", "/users/"+u.ID)
    }
    return u, nil
}, nethttp.Options{})

// Declare response header + codec — validated after handler returns
locationCodec := codex.String().Refine(validate.NonEmptyString)
createUser, _ = rest.NewRoute[CreateUserReq, User]("POST", "/users", ...,
    rest.ResponseHeaderParam{Name: "Location", Required: true}.WithCodec(locationCodec),
).Register(b)
```

Secure cookie writes:

```go
sessionCodec := codex.String().Refine(validate.NonEmptyString)
if err := nethttp.SetCookie(w, "session_token", newToken, nethttp.CookieOptions{
    Codec:  sessionCodec,
    MaxAge: 3600,
}); err != nil { /* rest.CookieParamError */ }
```

## Builder options

| Option | Effect |
|---|---|
| `WithPathCodec(c)` | Validates every registered path against codec `c` at `Register` time |
| `WithPathConstraints(cs...)` | Validates every path against one or more constraints at `Register` time |

**Template-transparent validation:** constraints run on the structural shape of the path, not the literal template. `{varName}` placeholders are replaced with `x` before validation — `/users/{id}` → `/users/x`. The stored `Descriptor.Path` is always the original template.

**Final path re-validation:** `BuildPath` re-validates the fully assembled path (e.g. `/users/hello world`) against the builder-level codec after substitution. This catches variable values that pass their `PathParam.Codec` individually but violate the global path constraint. Returns `rest.InvalidPathError{Path, Err}` with the concrete path (not the template).

## OpenAPI spec generation

```go
doc, err := b.OpenAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

All param codecs, security schemes, response headers/cookies, and content types flow into the spec automatically. To render codec schemas without a builder:

```go
yamlBytes, _ := openapi.MarshalYAML(map[string]schema.Schema{
    "User": UserCodec.Schema,
})
```

## Error types

| Error | When returned |
|---|---|
| `rest.InvalidPathError{Path, Err}` | Path fails builder-level validation |
| `rest.PathParamError{Name, Value, Err}` | Path variable fails its codec |
| `rest.MissingPathVarError{Name}` | Path variable absent from vars map |
| `rest.QueryParamError{Name, Value, Err}` | Query param fails its codec |
| `rest.CookieParamError{Name, Value, Err}` | Cookie fails its codec |
| `rest.HeaderParamError{Name, Value, Err}` | Header fails its codec |
| `rest.UnsupportedMediaTypeError{Got, Supported}` | Wrong Content-Type → 415 |
| `rest.NotAcceptableError{Accept, Supported}` | Accept has no match → 406 |
| `rest.BodyTooLargeError{Limit}` | Body exceeds MaxBodyBytes → 413 |

## Security

See [Feature: Security & Auth](security.md) for full security documentation.

## See also

- [Feature: Security & Auth](security.md) — bearer JWT, SecurityFunc, per-route scopes
- [Feature: SSE & Streaming](sse-streaming.md) — SSE routes, streaming, templ SSR
- [Feature: HTTP Client](http-client.md) — typed HTTP client reusing the same Route
- [examples/adapters-nethttp](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp) — three-layer pipeline demo
- [examples/adapters-chi](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-chi) — chi router demo
- [examples/api-rest](https://github.com/DaniDeer/go-codex/tree/main/examples/api-rest) — REST builder + OpenAPI spec
