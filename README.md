# go-codex

[![CI](https://github.com/DaniDeer/go-codex/actions/workflows/ci.yml/badge.svg)](https://github.com/DaniDeer/go-codex/actions/workflows/ci.yml)
[![pkg.go.dev](https://pkg.go.dev/badge/github.com/DaniDeer/go-codex.svg)](https://pkg.go.dev/github.com/DaniDeer/go-codex)
[![Docs](https://img.shields.io/badge/docs-zensical-blue)](https://danideer.github.io/go-codex/)

A self-documenting codec library for Go inspired by Haskell's [autodocodec](https://hackage.haskell.org/package/autodocodec).
A single `Codec[T]` value simultaneously describes how to **encode**, **decode**, **validate**, and **document** a type.
Write the codec once — derive JSON, YAML, OpenAPI, AsyncAPI, and more from the same definition.

**No struct tags. No reflection. No code generation.**

---

## 📚 Documentation

| | |
|---|---|
| **Full docs** | [danideer.github.io/go-codex](https://danideer.github.io/go-codex/) |
| **API reference** | [pkg.go.dev/github.com/DaniDeer/go-codex](https://pkg.go.dev/github.com/DaniDeer/go-codex) |
| **Examples** | [examples/](./examples/) — 35+ runnable demos |
| **Get started** | [docs/get-started.md](./docs/get-started.md) |

---

## The three layers

go-codex grows with your system. Use only what you need:

| Layer | Package | What you declare | What you get |
|-------|---------|-----------------|-------------|
| **1 — Codec** | `codex/` | Shape + constraints | Encode, decode, validate, schema — once, for free |
| **2 — API contract** | `api/rest`, `api/events`, `api/mcp` | Routes and channels | Typed helpers + OpenAPI / AsyncAPI / MCP spec |
| **3 — Pipeline** | `forge/` | Computation contract | Governed, signed, self-documenting KPI functions |

All three follow the same pattern: **declare → register → handle**.

```go
// Layer 1 — define a codec once; constraints run on both encode and decode
var userCodec = codex.Struct[User](
    codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
        func(u User) string { return u.Name },
        func(u *User, v string) { u.Name = v },
    ),
)

// Layer 2 — declare a typed route; same spec drives runtime + OpenAPI
var createUser = rest.NewRoute[CreateUserReq, User]("POST", "/users",
    reqCodec, userCodec,
    rest.RouteMeta{OperationID: "createUser"},
)
handle, _ := createUser.Register(builder)
req, _    := handle.Decode(body)           // validates automatically

// Layer 2 (client) — reuse the same route spec on the client side
user, _ := nethttp.Call(ctx, http.DefaultClient, serverURL, handle, req, nil, opts)

// Layer 3 — governed computation with automatic input/output validation
fn := forge.NewFunction[OEEInput, OEEResult]("oee", "1.0.0",
    inputCodec, outputCodec,
    func(in OEEInput) (OEEResult, error) { ... },
    forge.FunctionMeta{Author: "engineering@example.com"},
)
result, _ := fn.Apply(input)
```

---

## Quick Start

```bash
go get github.com/DaniDeer/go-codex@latest
```

```go
package main

import (
    "fmt"
    "github.com/DaniDeer/go-codex/codex"
    "github.com/DaniDeer/go-codex/format"
    "github.com/DaniDeer/go-codex/validate"
)

type User struct{ Name, Email string }

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

func main() {
    json := format.JSON(UserCodec)

    // Encode
    data, _ := json.Marshal(User{Name: "Alice", Email: "alice@example.com"})
    fmt.Println(string(data))
    // {"email":"alice@example.com","name":"Alice"}

    // Decode + validate
    _, err := json.Unmarshal([]byte(`{"name":"","email":"not-an-email"}`))
    fmt.Println(err)
    // validation errors: [name: expected non-empty string] [email: invalid email]
}
```

→ See [docs/get-started.md](./docs/get-started.md) for the next steps.

---

## What you get

- **One codec — four concerns** — encode, decode, validate, and schema from a single `Codec[T]` value; no struct tags, no reflection, no code generation
- **Multi-format** — the same codec reads and writes JSON, YAML, TOML, and Gob unchanged
- **Structured errors** — all failures are concrete types (`ValidationErrors`, `ConstraintError`, `TypeMismatchError`, …); use `errors.As` or pass directly to `log/slog`
- **Builtin constraints** — `email`, `uuid`, `url`, `date`, `date-time`, ranges, lengths — validated and reflected into OpenAPI/AsyncAPI schema automatically
- **OpenAPI 3.1 + AsyncAPI 3.0** — complete specs derived from the same codec; no manual YAML, no drift
- **REST + HTTP client** — typed `Decode`/`Encode` per route; `nethttp.Call` for typed client calls; both share the same `Route` definition
- **MQTT events** — typed subscribe/publish with topic validation, wildcard support, and AsyncAPI spec
- **MCP server** — Tools, Resources, and Prompts follow the same declare → register → handle pattern; codec drives the `inputSchema` automatically
- **SSE + templ SSR** — codec-validated event streams; same route serves HTML and JSON via content negotiation
- **Forge pipelines** — named, versioned, governed KPI computation with SHA-256 contract hash and pipeline YAML spec

---

## Import paths

```bash
go get github.com/DaniDeer/go-codex@latest
```

| What | Import path |
|------|------------|
| Core codecs | `github.com/DaniDeer/go-codex/codex` |
| Format bridges (JSON, YAML, TOML, Gob) | `github.com/DaniDeer/go-codex/format` |
| Built-in constraints | `github.com/DaniDeer/go-codex/validate` |
| REST API builder | `github.com/DaniDeer/go-codex/api/rest` |
| Event channel builder | `github.com/DaniDeer/go-codex/api/events` |
| MCP server builder | `github.com/DaniDeer/go-codex/api/mcp` |
| net/http adapter (server + client) | `github.com/DaniDeer/go-codex/adapters/nethttp` |
| chi adapter | `github.com/DaniDeer/go-codex/adapters/chi` |
| Paho MQTT adapter | `github.com/DaniDeer/go-codex/adapters/mqtt` |
| mark3labs/mcp-go adapter | `github.com/DaniDeer/go-codex/adapters/mcpgo` |
| templ SSR format plug-in | `github.com/DaniDeer/go-codex/adapters/templ` |
| OpenAPI 3.1 renderer | `github.com/DaniDeer/go-codex/render/openapi` |
| AsyncAPI 3.0 renderer | `github.com/DaniDeer/go-codex/render/asyncapi/v3` |
| Forge pipelines | `github.com/DaniDeer/go-codex/forge` |
| HTTP route descriptors | `github.com/DaniDeer/go-codex/route` |
| Schema model | `github.com/DaniDeer/go-codex/schema` |
| Observer interfaces | `github.com/DaniDeer/go-codex/stats` |

---

## Go library as contract

Codecs are plain Go values — put them in a shared package. The Go compiler enforces the contract:
a field rename breaks both the server and the client at compile time.

```
examples/adapters-nethttp-client/contract/  ← shared Route specs, codecs, types
examples/adapters-mqtt-contract/contract/   ← shared Channel specs, codecs, types
examples/gob-contract/contract/             ← shared Gob format contract
```

→ [docs/concepts/codec-as-contract.md](./docs/concepts/codec-as-contract.md)

---

## Project Structure

```TEXT
go-codex/
├── go.mod
├── README.md

├── codex/                  # ⭐ PUBLIC API: codecs, primitives, struct, union, slice
│   ├── codec.go            # Codec[T], WithDescription, WithTitle, WithExample, WithDeprecated, Validate, New
│   ├── either.go           # Either[A,B] type, Either2 codec
│   ├── errors.go           # ValidationError, ValidationErrors, EitherError
│   ├── map.go              # MapCodecSafe, MapCodecValidated, Downcast
│   ├── must.go             # Must[T] — generic panic-on-error helper
│   ├── nullable.go         # Nullable[T]
│   ├── object.go           # Field[T,F], RequiredField, OptionalField, DefaultField, Struct[T]
│   ├── primitives.go       # Int, Int32, Int64, Uint, Uint64, Float32, Float64, String, Bool, Bytes, Any, Pure
│   ├── refine.go           # Constraint[T], Refine, RefineFunc, Eq (Constraint.Schema for schema reflection)
│   ├── slice.go            # SliceOf[T]
│   ├── stringmap.go        # StringMap[V], Map[K, V]
│   ├── time.go             # Time(), Date(), Duration()
│   └── union.go            # TaggedUnion[T], UntaggedUnion[T], UntaggedVariant[T]
│
├── format/                 # format bridges: JSON, YAML, TOML, Gob, streaming
│   └── format.go           # Format[T], JSON(), YAML(), TOML(), Gob(), New(), NewTyped(), NewStreamed(), FromEnv()
│
├── route/                  # HTTP route descriptors (no renderer logic)
│   └── route.go            # Route, Param, Body, Response, SecurityScheme, SecurityRequirement
│
├── api/                    # transport-agnostic API builders
│   ├── internal/           # shared helpers (not public API)
│   │   └── template.go     # ParseTemplateVars, BuildFromTemplate, StripTemplateVars
│   ├── rest/               # REST API builder: typed Decode/Encode + OpenAPI spec
│   │   └── builder.go      # Builder, Route[Req,Resp]/NewRoute, SSERoute[Req,Event]/NewSSERoute,
│   │                       #   RouteHandle (Decode, Encode, EncodeRequest, DecodeResponse, ClientHandle),
│   │                       #   SSERouteHandle, BuildPath, AddServer, AddSchema, AddSecurityScheme,
│   │                       #   AddGlobalSecurity, PathParam, QueryParam, CookieParam, HeaderParam,
│   │                       #   ResponseHeaderParam, ResponseCookieParam, RouteMeta, SecurityScheme
│   ├── events/             # Event channel builder: typed Decode/Encode + AsyncAPI spec
│   │   └── builder.go      # Builder, Channel[T]/NewChannel, ChannelHandle, BuildTopic,
│   │                       #   AddServer, AddSchema, AddSecurityScheme, AddGlobalSecurity,
│   │                       #   TopicParam, ChannelMeta, Subscribe, Publish, SecurityScheme
│   └── mcp/                # MCP server builder: Tools, Resources, Prompts
│       ├── builder.go      # Builder, NewTool[In,Out], NewResource[T], NewPrompt,
│       │                   #   ToolHandle, ResourceHandle, PromptHandle, MCPSpec
│       └── errors.go       # ToolInputError, ToolOutputError, ResourceEncodeError,
│                           #   ResourceParamError, MissingResourceVarError, PromptArgError, …
│
├── adapters/               # transport-specific adapters
│   ├── nethttp/            # net/http adapter — server + client
│   │   ├── adapter.go      # Handler, Register, SSEHandler, RegisterSSE, RequestFromContext,
│   │   │                   #   WithResponseHeaders, ResponseHeadersFromContext,
│   │   │                   #   WithResponseCookies, ResponseCookiesFromContext, Options
│   │   ├── client.go       # Call[Req,Resp], CallOptions, UnexpectedStatusError,
│   │   │                   #   RequestBuildError, RequestError, ResponseBodyError
│   │   └── cookie.go       # SetCookie, CookieOptions, PendingCookie
│   ├── chi/                # chi adapter for api/rest RouteHandles (github.com/go-chi/chi/v5)
│   │   └── adapter.go      # Handler, Register, SSEHandler, RegisterSSE, RequestFromContext,
│   │                       #   WithResponseHeaders, WithResponseCookies, SetCookie, CookieOptions, Options
│   ├── mqtt/               # Paho MQTT adapter for api/events ChannelHandles
│   │   ├── adapter.go      # SubscribeHandler, SubscribeOptions, Publish, PublishOptions,
│   │   │                   #   SubscribeError, ErrorKind, MessageFromContext
│   │   └── topicvars.go    # TopicVarsFromMessage, TopicMismatchError
│   ├── mcpgo/              # mark3labs/mcp-go adapter for api/mcp handles
│   │   └── adapter.go      # ToolHandler, ResourceHandler, PromptHandler,
│   │                       #   RegisterTool, RegisterResource, RegisterPrompt, Options
│   └── templ/              # templ SSR format plug-in for api/rest RouteHandles
│       └── adapter.go      # Format[Props], StreamingFormat[Props], DecodeNotSupportedError
│
├── forge/                  # governed KPI computation pipeline (Layer 3)
│   ├── forge.go            # Measured[T], MeasuredCodec[T], Function[In,Out], NewFunction,
│   │                       #   Compose, Registry, PipelineSpec, PipelineInfo, FunctionMeta
│   ├── collection.go       # Map, Filter, Reduce, MapValues, MapValuesK collection ops
│   └── compose.go          # Compose — type-safe function chaining
│
├── render/                 # spec renderers (no runtime codec logic)
│   ├── internal/
│   │   └── schemarender/   # shared schema-to-map renderer (used by openapi + asyncapi)
│   │       └── schemarender.go  # SchemaObject
│   ├── openapi/            # OpenAPI 3.1 renderer
│   │   ├── openapi.go      # SchemaObject, ComponentsSchemas, MarshalJSON, MarshalYAML
│   │   └── document.go     # DocumentBuilder, Document, Info, Server — full 3.1 spec
│   ├── asyncapi/
│   │   ├── v2/             # AsyncAPI 2.6 renderer (frozen)
│   │   │   └── document.go # DocumentBuilder, Document, ChannelItem, Operation, Message
│   │   └── v3/             # AsyncAPI 3.0 renderer
│   │       └── document.go # DocumentBuilder, Document, Server, Operation, ChannelItem (Address)
│   ├── jsonschema/         # plain JSON Schema renderer (used by api/mcp)
│   │   └── jsonschema.go   # Schema(s schema.Schema) json.RawMessage
│   └── pipeline/           # pipeline YAML renderer (for forge.PipelineSpec)
│       └── pipeline.go     # Render(spec) []byte
│
├── schema/                 # schema model (pure data, zero dependencies)
│   └── schema.go           # Schema, Property, DiscriminatorSchema
│
├── validate/               # reusable constraints (reflect into schema automatically)
│   ├── bytes.go            # MaxBytes(n), MinBytes(n)
│   ├── duration.go         # PositiveDuration, NonNegativeDuration, MinDuration, MaxDuration
│   ├── float.go            # PositiveFloat, NegativeFloat, NonZeroFloat, MinFloat, MaxFloat, RangeFloat
│   ├── format.go           # Email, UUID, URL, URLWithSchemes, URI, Hostname, IPv4, IPv6, IP,
│   │                       #   Date, Time, DateTime, SemVer, Slug, CIDR, BearerToken
│   ├── int.go              # PositiveInt, NegativeInt, NonZeroInt, MinInt, MaxInt, RangeInt; int32 + int64 variants
│   ├── uint.go             # PositiveUint, MinUint, MaxUint, RangeUint; uint64 variants
│   └── string.go           # NonEmptyString, MinLen, MaxLen, Pattern, OneOf, HTTPPath,
│                           #   MQTTTopic, MQTTPublishTopic, IntString, PositiveIntString,
│                           #   NonNegativeIntString, IntStringInRange
│
├── stats/                  # dependency-free metrics observer interfaces
│   └── observer.go         # ValidationObserver, Observer, PipelineObserver, SecurityObserver, NoopObserver
│
└── examples/               # usage demonstrations — not importable by library packages
    │
    │   # ── Codec (Layer 1) ────────────────────────────────────────────────────
    ├── construction/       # New + Must: construction-time validation demo
    ├── decode-errors/      # multi-field ValidationErrors + errors.As demo
    ├── error-types/        # every structured error type: ValidationError, TypeMismatch, etc.
    ├── codec-mapping/      # shared field codecs, sub-codec reuse, MapCodecSafe, MapCodecValidated
    ├── formats/            # builtin format constraints demo (Email, UUID, URL, …)
    ├── html-sanitize/      # sanitizing untrusted HTML input with a codec
    ├── multiformat/        # JSON / YAML / TOML with one codec
    ├── order/              # nested structs, SliceOf, Time, Nullable, StringMap demo
    ├── shape/              # tagged union + Downcast demo
    └── validate/           # explicit Validate before marshal
    │
    │   # ── REST / HTTP (Layer 2) ───────────────────────────────────────────────
    ├── api-rest/               # REST API builder: typed helpers + OpenAPI spec
    ├── openapi/                # OpenAPI components/schemas generation from a Codec
    ├── rest-api/               # full OpenAPI 3.1 document from route descriptors
    ├── adapters-nethttp/       # net/http adapter: three-layer pipeline, multi-format bodies, observer
    ├── adapters-nethttp-security/   # net/http adapter: bearer JWT, scopes, SecurityFunc, observer
    ├── adapters-nethttp-client/     # codec-as-contract HTTP client: shared contract/, Call, CredentialFunc
    │   └── contract/               #   shared Route specs, codecs, types (importable by both sides)
    ├── adapters-chi/           # chi adapter: wiring api/rest to chi.Router
    ├── adapters-chi-security/  # chi adapter: bearer JWT security, per-route scopes
    ├── adapters-sse/           # SSE: NewSSERoute, SSEHandler, path codec, OpenAPI spec
    ├── adapters-streaming-sse-templ/ # chunked streaming + SSE HTML fragments via templ components
    ├── adapters-templ/         # templ SSR: same route serves HTML and JSON; observer wired
    └── png-upload/             # binary payload upload: Bytes codec, content-type enforcement
    │
    │   # ── Events / MQTT (Layer 2) ─────────────────────────────────────────────
    ├── api-events/             # Event channel builder: typed helpers + AsyncAPI spec
    ├── event-driven/           # full AsyncAPI 2.6 document from channel descriptors
    ├── adapters-mqtt/          # Paho MQTT: three-layer pipeline, multi-format pub/sub, wildcard
    ├── adapters-mqtt-security/ # Paho MQTT: security credentials, SecurityFunc, observer
    ├── adapters-mqtt-contract/ # codec-as-contract MQTT: shared contract/, producer + consumer
    │   └── contract/           #   shared Channel specs, codecs, types (importable by both sides)
    └── gob-contract/           # Go library as contract: gob wire encoding, no code-gen
        └── contract/           #   shared Channel, codec, Gob format — compiler-enforced contract
    │
    │   # ── MCP (Layer 2) ────────────────────────────────────────────────────────
    └── adapters-mcp/           # MCP server: Tools, Resources, Prompts, MCPSpec, observer
    │
    │   # ── Forge / Pipeline (Layer 3) ──────────────────────────────────────────
    ├── forge-oee/          # forge pipeline: OEE KPI computation, governance, Compose, MeasuredCodec
    ├── forge-collection/   # forge collection ops: Map, Filter, Reduce, MapValues on MQTT sensor batches
    └── oee-chain/          # full three-layer chain: codex + api/events + forge with AsyncAPI + pipeline spec
    │
    │   # ── Config / CLI ────────────────────────────────────────────────────────
    ├── cli-config/         # CLI tool config: TOML file + env var overlay with codecs
    ├── env-config/         # format.FromEnv: schema-driven env var loading with defaults
    └── stats-observer/     # stats.ValidationObserver wired to codecs directly (no adapter)
```
