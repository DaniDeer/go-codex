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
).RegisterHandle(b)

// GET /users/{id} — path param validated as UUID
uuidCodec := codex.String().Refine(validate.UUID)
getUser, _ := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
    codex.Empty, userCodec,
    rest.RouteMeta{OperationID: "getUser", RespSchemaName: "User"},
    rest.PathParam{Name: "id", Description: "User UUID"}.WithCodec(uuidCodec),
).RegisterHandle(b)

// GET /users — query params
pageCodec := codex.String().Refine(validate.NonNegativeIntString)
listUsers, _ := rest.NewRoute[struct{}, []User]("GET", "/users",
    codex.Empty, codex.SliceOf(userCodec),
    rest.RouteMeta{OperationID: "listUsers"},
    rest.QueryParam{Name: "page"}.WithCodec(pageCodec),
    rest.QueryParam{Name: "search"},
).RegisterHandle(b)

// GET /profile — cookie + header params
sessionCodec   := codex.String().Refine(validate.NonEmptyString)
requestIDCodec := codex.String().Refine(validate.UUID)
profile, _ := rest.NewRoute[struct{}, User]("GET", "/profile",
    codex.Empty, userCodec,
    rest.RouteMeta{OperationID: "getProfile"},
    rest.CookieParam{Name: "session_token", Required: true}.WithCodec(sessionCodec),
    rest.HeaderParam{Name: "X-Request-Id",  Required: true}.WithCodec(requestIDCodec),
).RegisterHandle(b)
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

## Path/query/header params with automatic merge

The plain param types above are validate-only: they check a value against
a codec but leave extracting it into your request struct to hand-written
code (`r.PathValue("id")` + manual field assignment). `NewPathParam`/
`NewRequiredQueryParam`/`NewOptionalQueryParam` (+ Header/Cookie
equivalents) declare the SAME spec param AND a merge field in one call —
the handler receives an already-merged, already-validated request, no
manual extraction:

```go
type GetUserReq struct{ ID string }

var getUser = rest.NewRoute[GetUserReq, User]("GET", "/users/{id}", reqCodec, userCodec,
    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
        func(r GetUserReq) string { return r.ID },
        func(r *GetUserReq, v string) { r.ID = v },
    ),
)
getUser.Register(builder)

// nethttp.Serve / chi.Serve apply RouteHandle.MergeFields() automatically
// whenever it is non-empty — the handler function just
// receives a fully populated, validated GetUserReq:
func(ctx context.Context, req GetUserReq) (User, error) {
    record, ok := store.Get(req.ID) // req.ID already validated as a UUID
    ...
}
```

This is the PRIMARY, recommended way to declare a param — but not the
SOLE way: plain `PathParam`/`QueryParam`/`HeaderParam`/`CookieParam` struct
literals remain available for validate-only params a handler never reads
directly (avoids forcing a `get`/`set` pair with nothing to return). A
route can freely mix both styles. Set the parameter-level description
(distinct from the codec's own schema-level description) via
`.WithDescription(...)`:

```go
rest.NewPathParam("id",
    codex.String().Refine(validate.UUID).WithDescription("Must be a valid UUID v4"), // schema-level
    func(r GetUserReq) string { return r.ID },
    func(r *GetUserReq, v string) { r.ID = v },
).WithDescription("The user's unique identifier") // param-level
```

For manual control (e.g. merging from a source other than the HTTP
request, or reusing the SAME field declarations elsewhere), call
`RouteHandle.MergeFields()` directly with `codex.DecodeVars`:

```go
var req GetUserReq
vars := map[string]string{"id": r.PathValue("id")}
err := codex.DecodeVars(&req, vars, handle.MergeFields()...)
```

### Mixing body fields and merged params on one struct

A single `Req` struct can have SOME fields decoded from the JSON body and
OTHER fields merged from path/query/header/cookie — `DecodeMerged` decodes
the body first, then merges vars into the SAME value (a partial merge —
only the declared merge fields are touched, everything the body decoded
stays intact):

```go
type UpdateUserReq struct {
    ID    string // from path — merged
    Name  string // from JSON body
    Email string // from JSON body
}

// updateUserReqCodec deliberately does NOT declare "id" — it's populated
// exclusively via the path-var merge below.
var updateUserReqCodec = codex.Struct[UpdateUserReq](
    codex.RequiredField("name", nameFieldCodec,
        func(r UpdateUserReq) string { return r.Name },
        func(r *UpdateUserReq, v string) { r.Name = v }),
    codex.RequiredField("email", emailFieldCodec,
        func(r UpdateUserReq) string { return r.Email },
        func(r *UpdateUserReq, v string) { r.Email = v }),
)

rest.NewRoute[UpdateUserReq, User]("PUT", "/users/{id}", updateUserReqCodec, userCodec,
    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
        func(r UpdateUserReq) string { return r.ID },
        func(r *UpdateUserReq, v string) { r.ID = v }),
)
// The handler receives req.ID (path), req.Name and req.Email (body) all
// populated on the same UpdateUserReq — no manual merging in the handler.
```

The only rule: the body codec and the merge-field declarations must not
declare the SAME field name — otherwise whichever runs second silently
overwrites the first. See `examples/adapters-nethttp`/`examples/adapters-chi`
(`PUT /users/{id}`) for the full runnable version.

See [Concepts: Codec — Reusing Field declarations](../concepts/codec.md#reusing-field-declarations-for-pathtopicheaderquery-vars)
for the underlying mechanism.

### Client-side encode — role-aware merge fields

The merge fields declared via `NewPathParam`/`NewRequiredQueryParam`/etc.
also benefit the CLIENT (encode) direction: `nethttp.Call` takes the
`rest.Route` value directly and ALWAYS auto-derives path/query/header/cookie
values from its declared merge fields internally — there is no manual
`vars map[string]string`/`codex.EncodeVars` step for the caller to perform;
a route intended for client use must simply declare merge fields for every
value it needs.

Internally, `Call` uses `RouteHandle.PathMergeFields()`/`QueryMergeFields()`/
`HeaderMergeFields()`/`CookieMergeFields()` — role-specific accessors, each
returning only that role's fields — never the flat, aggregate
`RouteHandle.MergeFields()` (safe for the DECODE direction, where the
source vars are already correctly scoped before merging, but unsafe for
ENCODE: a flat map built from all roles could leak a path value into the
query string). This role separation is why the path value can never end
up in the query string, and vice versa, even though both come from the
same `req`:

```go
type GetUserActivityReq struct {
    ID     string // path
    Filter string // query
}

var getUserActivity = rest.NewRoute[GetUserActivityReq, User](
    "GET", "/users/{id}/activity", reqCodec, userCodec,
    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
        func(r GetUserActivityReq) string { return r.ID },
        func(r *GetUserActivityReq, v string) { r.ID = v }),
    rest.NewOptionalQueryParam("filter", codex.String(),
        func(r GetUserActivityReq) string { return r.Filter },
        func(r *GetUserActivityReq, v string) { r.Filter = v }),
)

caller := nethttp.NewCaller(client, baseURL)
req := GetUserActivityReq{ID: userID, Filter: "logins"}

// nethttp.Call takes the rest.Route value directly and ALWAYS auto-derives
// path/query/header/cookie values from its declared merge fields — no
// manual codex.EncodeVars call, no vars map. req.ID merges into {id},
// req.Filter merges into ?filter=... automatically.
user, err := nethttp.Call(ctx, caller, getUserActivity, req, nethttp.CallOptions{})
```

The path value can never end up in the query string, and vice versa, even
though both come from the same `req` — each merge field is tagged with its
own role (`NewPathParam`/`NewOptionalQueryParam`) at declaration time. See
`examples/adapters-nethttp-client` (section 2b) for the full runnable
version.

### Response merge fields

The merge-field pattern also applies to the RESPONSE side — response
headers and Set-Cookie values can be declared as regular struct fields on
`Resp` and flow automatically in BOTH directions, mirroring the request
side:

```go
var getUserActivity = rest.NewRoute[GetUserActivityReq, User]("GET", "/users/{id}/activity", reqCodec, userCodec,
    rest.NewPathParam("id", codex.String().Refine(validate.UUID),
        func(r GetUserActivityReq) string { return r.ID },
        func(r *GetUserActivityReq, v string) { r.ID = v }),
    rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.UUID),
        func(u User) string { return u.RequestID },
        func(u *User, v string) { u.RequestID = v },
    ),
)
```

On the **server**, `nethttp`/`chi`'s `Serve` (via `Route.WithHandler`)
automatically encode `User.RequestID` into the actual `X-Request-Id` HTTP
response header after your handler returns — no
`nethttp.WithResponseHeaders` call needed:

```go
route.WithHandler(func(ctx context.Context, req GetUserActivityReq) (User, error) {
    u := lookup(req.ID)
    u.RequestID = generateTraceID() // adapter sets the X-Request-Id header from this automatically
    return u, nil
}).Register(builder)
// ... nethttp.Serve(mux, builder) wires it
```

On the **client**, `nethttp.Call` automatically merges the HTTP response's
`X-Request-Id` header back into the decoded `User.RequestID` field — no
`resp.Header.Get(...)` call needed. `NewRequiredResponseCookieParam`/
`NewOptionalResponseCookieParam` work identically for Set-Cookie values.
"Required"/"Optional" governs the DECODE (client) direction only — the
server always encodes the field (the getter is always called).

Merge-derived response cookies get default cookie attributes (no
Path/Secure/SameSite override) — use `nethttp.WithResponseCookies`
directly for custom attributes, the same escape hatch that remains for any
field not modeled as a struct field.

### One-line client calls — nethttp.Call

`nethttp.Call` derives `vars`/`QueryParams`/`HeaderParams`/`CookieParams`
from `req` automatically, using the route's role-aware merge-field
accessors — no `codex.EncodeVars` calls needed at the call site. `Call`
is the SOLE public client-side entry point (see
[the HTTP Client feature page](http-client.md) for the full picture):

```go
caller := nethttp.NewCaller(client, baseURL)
activity, err := nethttp.Call(ctx, caller, getUserActivity,
    GetUserActivityReq{ID: userID, Filter: "logins"}, nethttp.CallOptions{})
// activity.RequestID is already populated from the response header.
```

Any entry explicitly set in `opts.QueryParams`/`HeaderParams`/`CookieParams`
takes PRECEDENCE over the value `Call` derives from `req` for the
same key — this lets you override a field's value or add an ad-hoc param
the struct doesn't declare, without losing the one-line convenience for
the common case. `nethttp.CallWithHandle` remains available as the
lower-level, handle-based escape hatch for callers that already have a
`*rest.RouteHandle` but no `rest.Route` value.

Together, this closes the full loop the merge-field feature set targets:
one codec/route definition, a single `Req`/`Resp` struct per side, and
every REST aspect (body, path, query, header, cookie) flows automatically
in both directions on both client and server — manual `PathParam`/
`QueryParam`/etc. and `ResponseHeadersFromContext` remain available as the
escape hatch for anything that doesn't fit the struct-field model. See
`examples/adapters-nethttp-client` (sections 2b/2c) for the full runnable
version.

This convenience also runs through the `ports` binding layer:
`nethttp.DrainCallAdapter` (`ports.SinkAdapter`) and `nethttp.CallAdapter`
(`ports.IOAdapter`) delegate to `CallWithHandle` and derive path/query/header/
cookie vars PER-ITEM from each streamed item's own merge fields whenever
their `Vars` option is left `nil` — every item may resolve to a different
concrete request. Set `Vars` to a non-nil map to keep the same, static vars
for every item in the stream instead (the pre-existing behavior). See
[Ports: merge-field per-item vars derivation](ports.md#available-adapters-by-transport)
for the same behavior across the other transport adapters (MQTT 5, MQTT,
ZeroMQ).

### Nested structs & binary body formats

The merge-field convenience is not JSON-specific or flat-struct-specific.
Two things are worth calling out explicitly, since they're easy to assume
don't work without checking:

**Non-JSON body formats compose for free.** Body decode/encode is
completely orthogonal to var-merge — `codex.DecodeVars`/`EncodeVars` only
ever touch a `map[string]string`, never body bytes. `format.Gob[T]`/
`format.Binary`/any custom `format.NewTyped`/`format.NewStreamed` format
plug into `WithRequestFormats`/`WithFormats` exactly like JSON/YAML/TOML —
merge fields keep working unchanged regardless of which format the body
uses.

**Nested struct composition works out of the box.** Every merge-field
constructor takes plain `get`/`set` closures — there is no reflection over
`Req`'s direct fields — so a closure can reach into a nested sub-struct
exactly as easily as a top-level field:

```go
type UploadMeta struct {
    ContentHash string
    Compress    string
}

type UploadReq struct {
    ID      string
    Meta    UploadMeta    // header + query merge fields target THIS sub-struct
    Payload UploadPayload // Gob body
}

rest.NewOptionalHeaderParam("X-Content-Hash", codex.String(),
    func(r UploadReq) string { return r.Meta.ContentHash },   // nested — no framework change needed
    func(r *UploadReq, v string) { r.Meta.ContentHash = v },
)
```

**One subtlety for whole-value binary formats (Gob, protobuf, custom binary
layouts):** `format.Gob(codec)` serialises the WHOLE typed value directly
via `encoding/gob`'s own reflection, bypassing the codec's `Encode`/`Decode`
entirely for the wire bytes (the codec is only used for `Validate`). That
means `format.Gob(reqCodec)` on a nested `UploadReq` would gob-encode `ID`
and `Meta` too, not just `Payload` — harmless (`DecodeMerged` always merges
path/header/query AFTER body decode, so the authoritative HTTP values win
regardless), but wasteful. When the wire bytes should represent ONLY the
nested `Payload` sub-field, use `format.NewTyped` with a custom marshal/
unmarshal that projects onto/from that sub-field manually:

```go
var uploadGobFormat = format.NewTyped[UploadReq](
    reqCodec,
    func(r UploadReq) ([]byte, error) {
        var buf bytes.Buffer
        err := gob.NewEncoder(&buf).Encode(r.Payload) // ONLY Payload on the wire
        return buf.Bytes(), err
    },
    func(data []byte) (UploadReq, error) {
        var p UploadPayload
        err := gob.NewDecoder(bytes.NewReader(data)).Decode(&p)
        return UploadReq{Payload: p}, err // ID/Meta populated by merge, not here
    },
    "application/gob",
)
```

This is a general, already-existing primitive (`format.NewTyped`) — no new
API. See `examples/rest-nested-binary` for the full runnable version:
nested `Meta`/`Payload` sub-structs, Gob body projected onto `Payload`,
header/query merged into `Meta`, and a response header merge field
(`Resp.Meta.TraceID`) — one struct in, one struct out, on both
`nethttp.CallWithHandle` (client) and `nethttp.Serve` (server).

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

// Serve uses the Go 1.22+ "METHOD /path" ServeMux pattern automatically.
route := createUser.WithHandler(func(ctx context.Context, req CreateUserReq) (User, error) {
    return svc.CreateUser(ctx, req)
}).WithOptions(nethttp.Options{Observer: obs})
route.Register(builder)
nethttp.Serve(mux, builder)

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

### Error-path ergonomics — `ErrorStatus` / `ErrorPattern`

When using `PipelineHandler` / `RegisterPipeline`, the adapter takes the
first `stream.Errors` entry as the handler error. `Handler` (no pipeline)
consults the same route-level mapping too. Declare typed error → HTTP status
(and optionally a codec-backed response body) **per route**:

```go
route, _ := rest.NewRoute[CreateJobReq, JobResp]("POST", "/jobs", reqCodec, respCodec,
    // Status-only mapping — body still goes through Options.ErrorHandler.
    rest.ErrorStatus[domain.ConflictError](http.StatusConflict), // 409

    // Status + codec-backed typed body (direct or mapped payload).
    rest.ErrorPattern[domain.ValidationError, ErrorBody](http.StatusUnprocessableEntity, errorBodyCodec,
        func(e domain.ValidationError) (ErrorBody, error) {
            return ErrorBody{Code: "validation", Message: e.Error()}, nil
        },
    ),
).RegisterHandle(b)
```

- `rest.ErrorStatus[E](status)` — status-only mapping; body still flows
  through `Options.ErrorHandler`.
- `rest.ErrorPattern[E, B](status, codec, mapFn...)` — status **and** a
  codec-backed typed error body. Two modes:
  - **Direct** (no `mapFn`): `E` must itself be assignable to `B`.
  - **Mapped** (`mapFn` provided): `mapFn(E)` produces `B`.
  - On match, the adapter writes the encoded body directly — `Options.ErrorHandler`
    is not consulted for that response (the default `rest.ErrorRespond` action —
    see below for `.WithAction`).
- Error responses support the **same one-struct-one-call header/cookie parity**
  as the happy path — compose `rest.NewRequiredResponseHeaderParam`/
  `rest.NewOptionalResponseCookieParam` merge fields alongside `ErrorPattern`
  on the same route; matched error responses populate them from the mapped
  payload exactly like a successful response does.
- `RouteHandle.ErrorStatusFor(err) (int, bool)` and
  `RouteHandle.ErrorResponseFor(err) (ErrorPatternResponse, bool, error)` are
  the lookup accessors adapters call.

### Action selector — `WithAction`

A matched `ErrorPattern` executes exactly **one** action, mirroring the
three-way action model used by `events.ErrorChannel`/`websocket.ErrorFrame`:

| Action | Behavior | Default |
|---|---|---|
| `rest.ErrorRespond` | write the typed body (+status) directly | ✅ default |
| `rest.ErrorHandle` | skip the auto-write; fall through to `Options.ErrorHandler` (using this pattern's declared status) | opt-in via `.WithAction(rest.ErrorHandle)` |
| `rest.ErrorLog` | same as `ErrorHandle` for REST — kept as a distinct value for vocabulary parity across boundaries | opt-in via `.WithAction(rest.ErrorLog)` |

```go
rest.ErrorPattern[domain.ConflictError, ErrorBody](http.StatusConflict, errorBodyCodec, mapFn).
    WithAction(rest.ErrorHandle)
```

REST always has a caller to respond to (unlike Events/WebSocket, which
default to `respond` via a declared channel/broadcast, or SQL/Cache/File,
which default to `handle`/`log`) — `ErrorHandle`/`ErrorLog` both fall
through to the SAME `Options.ErrorHandler` escape hatch REST already
provides, since REST has only one such hook (unlike adapters with a
separate `OnError` callback).

Rules:
- Candidate set = all declared `ErrorStatus`/`ErrorPattern` rules, in
  declaration order. First matching rule wins (`errors.As` match).
- If no rule matches, pipeline errors keep default `500`.
- If pipeline emits no value, adapters return `PipelineNoResponseError` with
  default `503 Service Unavailable` (override by declaring
  `rest.ErrorStatus[nethttp.PipelineNoResponseError](...)` on the route).
- `Options.ErrorHandler` remains the final envelope/serialization escape
  hatch for anything not covered by a matched `ErrorPattern`.

The same codec-first error-pattern model is used across every other
boundary, adapted to its transport: [`events.ErrorChannel`](events.md#error-path-ergonomics-errorchannel)
(pub/sub), [`websocket.ErrorFrame`](websocket.md#error-path-ergonomics-errorframe)
(duplex/broadcast sockets), [`mcp.ErrorPattern`](mcp.md#error-path-ergonomics-errorpattern)
(MCP tools), and the [Store/IO boundaries](../guides/error-handling.md#storeio-boundaries-sql-cache-file--handlelog-by-default)
composition pattern (SQL/Cache/File).

### Client-side decode — `nethttp.Call` and `ErrorPatternResponse`

The declarative `ErrorPattern` data is not server-only — `Route.ClientHandle()`/
`Register()` carry the SAME declared patterns onto the client-side
`RouteHandle`. `nethttp.Call` consults them automatically on any non-2xx
response:

```go
_, err := nethttp.Call(ctx, caller, clientCreate, req, nethttp.CallOptions{})
if err != nil {
    var conflict nethttp.ErrorPatternResponse
    if errors.As(err, &conflict) {
        payload := conflict.Value.(domain.EmailConflictError) // decoded automatically
        // ... handle the typed conflict ...
    }
    var statusErr nethttp.UnexpectedStatusError
    if errors.As(err, &statusErr) {
        // no matching pattern (or its body failed to decode) — raw bytes
    }
}
```

- `RouteHandle.DecodeErrorFor(status, body) (ErrorPatternResponse, bool, error)`
  is the lookup accessor `Call` uses — status-only matching (the client has
  no Go error to match via `errors.As`, only the wire status code and body).
- **Only `ErrorRespond`-tagged patterns are eligible.** A pattern declared
  with `.WithAction(rest.ErrorHandle)`/`.WithAction(rest.ErrorLog)` is
  skipped during client-side lookup — the server does not guarantee writing
  that pattern's typed body to the wire for those actions (it falls through
  to `Options.ErrorHandler` instead, which may write anything).
- **Unmatched status, or a matched status whose body fails to decode**
  (e.g. schema drift between client/server versions) both fall back to the
  unchanged `nethttp.UnexpectedStatusError{Method, Path, StatusCode, Body}`
  — zero behavior change for callers who don't declare any `ErrorPattern`.
- Same codec, same declaration, both directions: the pattern that drives
  the server's typed body write is the ONLY thing needed for the client to
  decode it back — no separate client-side declaration.
- See [`examples/adapters-nethttp-client`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-nethttp-client)
  section "1b" for a full runnable round trip.

## chi adapter

```go
import (
    gochi      "github.com/go-chi/chi/v5"
    chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
)

r := gochi.NewRouter()
chiRoute := getUser.WithHandler(func(ctx context.Context, _ struct{}) (User, error) {
    rr, _ := chiadapter.RequestFromContext(ctx)
    return svc.GetUser(ctx, gochi.URLParam(rr, "id"))
})
chiRoute.Register(builder)
chiadapter.Serve(r, builder)
```

Same feature set as `adapters/nethttp` — all param validation, response headers/cookies, content negotiation, observer.

## Multi-format request/response

Declare `rest.RequestFormats`/`rest.Formats` inline as `NewRoute` opts (or
call `.WithRequestFormats`/`.WithFormats` on the `*RouteHandle` returned by
`RegisterHandle` for post-registration configuration):

```go
// Accept JSON or YAML request bodies; serve HTML or JSON responses.
createUser := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    createUserReqCodec, userCodec,
    rest.RequestFormats(format.JSON(createUserReqCodec), format.YAML(createUserReqCodec)),
)

articleRoute := rest.NewRoute[struct{}, ArticleProps]("GET", "/article",
    codex.Empty, propsCodec,
    rest.Formats(
        adapttempl.Format(propsCodec, ArticleCard), // Accept: text/html
        format.JSON(propsCodec),                    // Accept: application/json
    ),
)
```

`Route.ClientHandle()` applies these SAME declared formats identically to
`Register`/`RegisterHandle` — `nethttp.Call` picks up a declared
`RequestFormats`/`Formats` automatically, whether the route was registered
with a `Builder` or built client-only via `ClientHandle()`.

To override the format for ONE specific call without changing the route's
declaration, set `CallOptions.RequestFormats`/`ResponseFormats` (type-erased
`[]format.Format[Req]`/`[]format.Format[Resp]`) — wins over the
route-declared format for that call only:

```go
resp, err := nethttp.Call(ctx, caller, createUser, req, nethttp.CallOptions{
    ResponseFormats: []format.Format[User]{format.YAML(userCodec)},
})
```

## Response headers and cookies

```go
// Inside a handler: deposit headers via ctx
route := createUser.WithHandler(func(ctx context.Context, req CreateUserReq) (User, error) {
    u := svc.CreateUser(ctx, req)
    if h, ok := nethttp.ResponseHeadersFromContext(ctx); ok {
        h.Set("Location", "/users/"+u.ID)
    }
    return u, nil
})
route.Register(builder)
nethttp.Serve(mux, builder)

// Declare response header + codec — validated after handler returns
locationCodec := codex.String().Refine(validate.NonEmptyString)
createUser, _ = rest.NewRoute[CreateUserReq, User]("POST", "/users", ...,
    rest.ResponseHeaderParam{Name: "Location", Required: true}.WithCodec(locationCodec),
).RegisterHandle(b)
```

Redirect pattern: declare a 3xx `RespStatus` and set `Location` (via response
merge fields or `WithResponseHeaders`). This is separate from
`ErrorStatus[...]`, which only applies to error-channel mapping.

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
