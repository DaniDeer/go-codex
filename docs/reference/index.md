# Package Reference

All packages are fully documented on [pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex).

## Installation

```bash
go get github.com/DaniDeer/go-codex@latest
```

**Go 1.21+** required.

## Quick import reference

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
| AsyncAPI 2.6 renderer (frozen) | `github.com/DaniDeer/go-codex/render/asyncapi/v2` |
| Forge pipelines | `github.com/DaniDeer/go-codex/forge` |
| HTTP route descriptors | `github.com/DaniDeer/go-codex/route` |
| Schema model | `github.com/DaniDeer/go-codex/schema` |
| Observer interfaces | `github.com/DaniDeer/go-codex/stats` |

## Core

| Package | Description | pkg.go.dev |
|---------|-------------|-----------|
| `codex` | ⭐ Public API: `Codec[T]`, primitives, struct, union, slice, constraints | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/codex) |
| `validate` | Reusable constraints: Email, UUID, URL, ranges, MQTT topics, … | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/validate) |
| `format` | Format bridges: JSON, YAML, TOML, Gob, streaming, env vars | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/format) |
| `schema` | Schema model (pure data, zero dependencies) | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/schema) |
| `route` | HTTP route descriptors: `Route`, `Param`, `SecurityScheme` | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/route) |
| `stats` | Observer interfaces: `ValidationObserver`, `Observer`, `PipelineObserver`, `SecurityObserver`, `FileObserver`; `NoopObserver` satisfies all five | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/stats) |

## API builders (Layer 2)

| Package | Description | pkg.go.dev |
|---------|-------------|-----------|
| `api/rest` | REST API builder: typed Decode/Encode + OpenAPI spec | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/rest) |
| `api/events` | Event channel builder: typed Decode/Encode + AsyncAPI spec | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/events) |
| `api/mcp` | MCP server builder: Tools, Resources, Prompts | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/mcp) |

## Adapters

| Package | Description | pkg.go.dev |
|---------|-------------|-----------|
| `adapters/nethttp` | net/http: server (Handler, Register) + client (Call) | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/nethttp) |
| `adapters/chi` | chi router adapter | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/chi) |
| `adapters/mqtt` | Paho MQTT: SubscribeHandler + Publish | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mqtt) |
| `adapters/mcpgo` | mark3labs/mcp-go adapter | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/mcpgo) |
| `adapters/templ` | templ SSR format plug-in | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/templ) |

## Forge pipelines (Layer 3)

| Package | Description | pkg.go.dev |
|---------|-------------|-----------|
| `forge` | Governed KPI functions: `NewFunction`, `Compose`, `Registry` | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/forge) |

## Renderers

| Package | Description | pkg.go.dev |
|---------|-------------|-----------|
| `render/openapi` | OpenAPI 3.1 spec renderer | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/openapi) |
| `render/asyncapi/v3` | AsyncAPI 3.0 spec renderer | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/asyncapi/v3) |
| `render/asyncapi/v2` | AsyncAPI 2.6 spec renderer (frozen) | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/asyncapi/v2) |
| `render/jsonschema` | JSON Schema renderer (used by api/mcp) | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/jsonschema) |
| `render/pipeline` | Pipeline YAML renderer (used by forge) | [→](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/pipeline) |
