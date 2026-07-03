# go-codex

**go-codex** is a Go port of the core ideas from Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec). A single `Codec[T]` value simultaneously describes how to encode, decode, and document a type. Write once; derive JSON, YAML, OpenAPI, AsyncAPI, and more from the same definition.

```go
var UserCodec = codex.Struct[User](
    codex.RequiredField("name",
        codex.String().Refine(validate.NonEmptyString).WithDescription("Display name."),
        func(u User) string { return u.Name },
        func(u *User, v string) { u.Name = v },
    ),
    codex.RequiredField("email",
        codex.String().Refine(validate.Email).WithDescription("Email address."),
        func(u User) string { return u.Email },
        func(u *User, v string) { u.Email = v },
    ),
)
// UserCodec knows how to encode, decode, validate, and describe User —
// use it for JSON APIs, YAML config, OpenAPI specs, and MQTT channels.
```

## What you get

- **One codec — four concerns** — encode, decode, validate, and schema from a single `Codec[T]` value; no struct tags, no reflection, no code generation
- **Multi-format** — JSON, YAML, TOML, and Gob from the same codec without any changes
- **Structured errors** — every failure type is a concrete struct (`ValidationErrors`, `ConstraintError`, `TypeMismatchError`, `ElementError`, `KeyError`, …); use `errors.As` or pass directly to `log/slog`
- **Builtin constraints** — `email`, `uuid`, `url`, `date`, `date-time`, `container-image`, ranges, lengths, binary file formats (`png`, `jpeg`, `pdf`, `zip`, …) — validated and reflected into OpenAPI/AsyncAPI schema automatically
- **Custom constraints** — `codex.Constraint[T]` with optional schema annotation; compose with `.Refine()` and `.RefineFunc()` for cross-field rules
- **OpenAPI 3.1** — complete spec (paths, operations, params, security) from the same codec definitions; no manual YAML, no drift
- **AsyncAPI 3.0** — complete event-driven spec from channel descriptors; same schemas, no duplication
- **REST + HTTP client** — typed `Decode`/`Encode` per route; `nethttp.Call` typed client; both share the same `Route` definition — shared contract, compiler-enforced
- **MQTT events** — typed subscribe/publish with topic template validation, wildcard support, `TopicVarsFromMessage`, and AsyncAPI spec
- **MCP server** — Tools, Resources, and Prompts follow declare → register → handle; codec drives `inputSchema` automatically
- **SSE (Server-Sent Events)** — codec-validated event streams; path params work identically to REST routes
- **templ SSR** — same route serves HTML (`Accept: text/html`) and JSON (`Accept: application/json`) via content negotiation; props validated by codec before render
- **Forge pipelines** — named, versioned, SHA-256 contract-hashed computation functions; governance metadata; `Compose` for type-safe chaining; pipeline YAML spec
- **Observer / metrics** — `stats.Observer` interface with zero dependencies; plug in Prometheus or OpenTelemetry without changing any library code

## The three layers

| Layer | What you declare | What you get |
|-------|-----------------|-------------|
| **Codec** (`codex/`) | Shape + constraints | Encode, decode, validate, schema — automatically |
| **API contract** (`api/rest`, `api/events`, `api/mcp`) | Routes and channels | Typed Decode/Encode helpers + OpenAPI / AsyncAPI spec |
| **Forge pipeline** (`forge/`) | Computation contract | Governed, signed, self-documenting KPI functions |

## Quick links

- [What is go-codex?](what-is-go-codex.md) — the problem it solves + the three layers
- [Get Started](get-started.md) — 5-minute quick start
- [Concepts](concepts/codec.md) — core ideas and mental models
- [Features](features/rest-api.md) — all feature API references (REST, events, MCP, formats, security, …)
- [Guides](guides/http-server.md) — example walkthroughs for `examples/`
- [Package Reference](reference/index.md) — import paths + links to pkg.go.dev
- [GitHub](https://github.com/DaniDeer/go-codex) — source + examples
