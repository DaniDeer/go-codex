# OpenAPI Spec Generation

> See also: [`render/openapi` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/openapi) · [`api/rest` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest)
>
> Runnable demos: [`examples/openapi`](https://github.com/DaniDeer/go-codex/tree/main/examples/openapi) · [`examples/rest-api`](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) · [`examples/api-rest`](https://github.com/DaniDeer/go-codex/tree/main/examples/api-rest)

go-codex generates OpenAPI 3.1 documents from the same codec definitions that drive your runtime encode/decode — no separate YAML authoring, no drift.

## Components/schemas only

Use `render/openapi` to render codec schemas directly:

```go
import (
    "github.com/DaniDeer/go-codex/render/openapi"
    "github.com/DaniDeer/go-codex/validate"
)

var UserCodec = codex.Struct[User](
    codex.RequiredField("name",
        codex.String().
            Refine(validate.NonEmptyString).
            Refine(validate.MaxLen(100)).
            WithTitle("Full Name").
            WithDescription("The user's full display name."),
        func(u User) string { return u.Name },
        func(u *User, v string) { u.Name = v },
    ),
    codex.RequiredField("age",
        codex.Int().Refine(validate.RangeInt(0, 150)).WithDescription("Age in years."),
        func(u User) int { return u.Age },
        func(u *User, v int) { u.Age = v },
    ),
)

// Render components/schemas as YAML — ready to paste into openapi.yaml.
yamlBytes, err := openapi.MarshalYAML(map[string]schema.Schema{
    "User": UserCodec.Schema,
})
```

Output:
```yaml
User:
  type: object
  properties:
    name:
      type: string
      title: Full Name
      description: The user's full display name.
      minLength: 1
      maxLength: 100
    age:
      type: integer
      description: Age in years.
      minimum: 0
      maximum: 150
  required: [name, age]
```

Constraint schema reflection is automatic: `validate.*` constraints (`MinLen`, `RangeInt`, `OneOf`, `Pattern`, `Email`, `UUID`, etc.) annotate the schema. Custom constraints can do the same by setting `Constraint.Schema`.

## Full OpenAPI 3.1 document via `api/rest`

The `api/rest` builder generates a complete OpenAPI 3.1 document from all registered routes. The same builder that drives runtime decode/encode/validate also produces the spec:

```go
b := rest.NewBuilder(
    rest.Info{Title: "User API", Version: "1.0.0"},
    rest.WithPathConstraints(validate.HTTPPath),
)
b.AddServer("production", rest.Server{URL: "https://api.example.com/v1"})

// Register routes — path, request/response codecs, and parameter codecs all
// flow into the spec automatically.
createUser, _ := rest.NewRoute[CreateUserReq, User]("POST", "/users",
    createUserCodec, userCodec,
    rest.RouteMeta{
        OperationID:    "createUser",
        Summary:        "Create a user",
        ReqSchemaName:  "CreateUserRequest",  // → $ref in spec
        RespSchemaName: "User",
        RespStatus:     "201",
    },
).RegisterHandle(b)

uuidCodec := codex.String().Refine(validate.UUID)
getUser, _ := rest.NewRoute[struct{}, User]("GET", "/users/{id}",
    codex.Empty, userCodec,
    rest.RouteMeta{OperationID: "getUser", RespSchemaName: "User"},
    // PathParam.Codec validates {id} at BuildPath time + UUID schema → spec
    rest.PathParam{Name: "id", Description: "User UUID"}.WithCodec(uuidCodec),
).RegisterHandle(b)

// Query params flow into spec as in: query parameters
pageCodec := codex.String().Refine(validate.NonNegativeIntString)
listUsers, _ := rest.NewRoute[struct{}, []User]("GET", "/users",
    codex.Empty, codex.SliceOf(userCodec),
    rest.RouteMeta{OperationID: "listUsers"},
    rest.QueryParam{Name: "page", Description: "Page number (0-based)"}.WithCodec(pageCodec),
    rest.QueryParam{Name: "search", Description: "Name filter"},
).RegisterHandle(b)

// Cookie + header params flow into spec as in: cookie / in: header parameters
sessionCodec  := codex.String().Refine(validate.NonEmptyString)
requestIDCodec := codex.String().Refine(validate.UUID)
profile, _ := rest.NewRoute[struct{}, User]("GET", "/profile",
    codex.Empty, userCodec,
    rest.RouteMeta{OperationID: "getProfile"},
    rest.CookieParam{Name: "session_token", Required: true}.WithCodec(sessionCodec),
    rest.HeaderParam{Name: "X-Request-Id",  Required: true}.WithCodec(requestIDCodec),
).RegisterHandle(b)

// Generate the full OpenAPI 3.1 spec from all registered routes:
doc, err := b.OpenAPISpec()
yamlBytes, _ := doc.MarshalYAML()
```

## Security schemes

`middleware.SecurityScheme(schemeName, scheme, scopes, codec)`/
`rest.FromSecurityScheme(schemeName, rest.SecurityScheme, scopes)`, attached
via `Route.Use(mw)`, is the ONLY way to declare a security scheme — directly
on the route, no builder-level registry (`rest.WithSecurityScheme` was
REMOVED — there is no metadata-only registration anymore). `Builder.OpenAPISpec()`
aggregates `components.securitySchemes` from every registered route
automatically:

```go
bearerAuth := rest.SecurityScheme{
    SecurityScheme: route.BearerScheme("JWT"),
}.WithCodec(codex.String().Refine(validate.BearerToken))

b.AddGlobalSecurity(route.Require("bearerAuth"))

// Per-route override — empty slice = no auth required for this route
createUser, _ := rest.NewRoute[CreateUserReq, User]("POST", "/users", ...,
    rest.RouteMeta{
        Security: []route.SecurityRequirement{
            route.Require("bearerAuth", "write:users"),
        },
    },
).Use(rest.FromSecurityScheme("bearerAuth", bearerAuth, []string{"write:users"})).RegisterHandle(b)
```

Security schemes appear in `components/securitySchemes`; global security at document root; per-operation security overrides inline — all generated automatically.

## Using the lower-level DocumentBuilder directly

For cases where you build route descriptors manually (without `api/rest`):

```go
import (
    "github.com/DaniDeer/go-codex/render/openapi"
    "github.com/DaniDeer/go-codex/route"
)

doc, err := openapi.NewDocumentBuilder(openapi.Info{
    Title:   "User API",
    Version: "1.0.0",
}).
    AddServer(openapi.Server{URL: "https://api.example.com/v1"}).
    AddRoute(route.Route{
        Method:      "POST",
        Path:        "/users",
        OperationID: "createUser",
        RequestBody: &route.Body{
            Required:   true,
            Schema:     CreateUserCodec.Schema,
            SchemaName: "CreateUserRequest",  // → $ref + registered in components
        },
        Responses: []route.Response{
            {Status: "201", Schema: &UserCodec.Schema, SchemaName: "User"},
            {Status: "400", Description: "Validation error."},
        },
    }).
    Build()
```

`Build()` validates:
- No duplicate `(method, path)` pairs.
- `PathParams` names exactly match `{placeholder}` segments in the path.
- Path parameters are always `required: true` in the output.

## See also

- [Guide: HTTP Server](../guides/http-server.md) — full `api/rest` + `adapters/nethttp` example
- [examples/openapi](https://github.com/DaniDeer/go-codex/tree/main/examples/openapi) — components/schemas generation
- [examples/rest-api](https://github.com/DaniDeer/go-codex/tree/main/examples/rest-api) — full OpenAPI 3.1 document
- [examples/api-rest](https://github.com/DaniDeer/go-codex/tree/main/examples/api-rest) — REST builder + spec
