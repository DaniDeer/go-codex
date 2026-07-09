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
- **Multi-format** — the same codec reads and writes JSON, YAML, TOML, Gob, and Binary (raw bytes) unchanged
- **Structured errors** — all failures are concrete types (`ValidationErrors`, `ConstraintError`, `TypeMismatchError`, …); use `errors.As` or pass directly to `log/slog`
- **Builtin constraints** — `email`, `uuid`, `url`, `date`, `date-time`, `container-image`, ranges, lengths, binary file formats (`png`, `jpeg`, `pdf`, `zip`, …) — validated and reflected into OpenAPI/AsyncAPI schema automatically
- **OpenAPI 3.1 + AsyncAPI 3.0** — complete specs derived from the same codec; no manual YAML, no drift
- **REST + HTTP client** — typed `Decode`/`Encode` per route; `nethttp.Call` for typed client calls; both share the same `Route` definition
- **MQTT events** — typed subscribe/publish with topic validation, wildcard support, and AsyncAPI spec
- **ZeroMQ** — typed REQ/REP and PUB/SUB via the same codec declarations as REST and MQTT; AsyncAPI 3.0 with request-reply (`api/zeromq`); DEALER/ROUTER for concurrent patterns; transport-agnostic `FramedSocket` interface (no CGO in the adapter)
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
| Paho MQTT 3.1.1 adapter | `github.com/DaniDeer/go-codex/adapters/mqtt` |
| MQTT 5.0 adapter (paho.golang) | `github.com/DaniDeer/go-codex/adapters/mqtt5` |
| ZeroMQ adapter (PUB/SUB, REQ/REP, DEALER/ROUTER) | `github.com/DaniDeer/go-codex/adapters/zeromq` |
| SQL adapter (goose migrations + codec validation) | `github.com/DaniDeer/go-codex/adapters/sql` |
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

→ [Full annotated directory tree](https://zensical.org/docs/reference/project-structure) in the docs, or browse [docs/reference/project-structure.md](docs/reference/project-structure.md) in the repo.

Key top-level directories:

```text
codex/       — ⭐ PUBLIC API: Codec[T], primitives, struct, union, slice, constraints
format/      — format bridges: JSON, YAML, TOML, Gob, Binary, File I/O, embedded formats
api/         — transport-agnostic API builders (rest/, events/, mcp/, reqreply/)
adapters/    — transport adapters (nethttp, chi, mqtt, mqtt5, zeromq, sql, mcpgo, templ)
forge/       — governed KPI computation pipelines
render/      — spec renderers (openapi/, asyncapi/v2, asyncapi/v3, jsonschema/, pipeline/)
validate/    — reusable constraints (Email, UUID, URL, ranges, MQTT topics, …)
stats/       — observer interfaces (ValidationObserver → SQLObserver, LoggingObserver, NewFanout)
schema/      — schema model (pure data, zero dependencies)
route/       — HTTP route descriptors
examples/    — 40+ runnable demos (not importable by library packages)
```
