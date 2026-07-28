# LLM Integration — `api/llm`, `adapters/openai`, `render/openaitools`

> See also: [`api/llm` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/llm) · [`adapters/openai` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/openai) · [`render/openaitools` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/render/openaitools) · [MCP Server feature](mcp.md) · [Ports, Plugins, and Adapters concept](../concepts/ports-and-adapters.md)
>
> Runnable demo: [`examples/adapters-openai`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-openai)

[MCP Server](mcp.md) covers the direction where an **LLM calls go-codex** — every codec-declared tool's JSON Schema comes for free. This page covers the other direction: **go-codex calling an LLM**. An LLM completion is declared like every other boundary in this library — a system prompt plus typed input/output codecs — then bound to a concrete provider through `ports.IOPort`, exactly like an HTTP call, a SQL query, or a cache lookup.

## Quick start

```go
import (
    "github.com/DaniDeer/go-codex/adapters/openai"
    "github.com/DaniDeer/go-codex/api/llm"
    "github.com/DaniDeer/go-codex/ports"
)

// domain/pipeline.go — zero adapter imports
var Summarize = codex.Must(ports.NewIOPort[Article, Summary](
    "summarize", articleCodec, summaryCodec, ports.PortOptions{}))

var SummarizePattern = ports.LLMPattern{
    Name: "summarize",
    Opts: []llm.CallOpt{
        llm.SystemPromptFile("prompts/summarize.md"),
        llm.CallMeta{Description: "Summarizes a news article in exactly three sentences."},
    },
}

// main.go — the only place that knows the concrete provider
handle, err := Summarize.PluginLLMPattern(SummarizePattern)
Summarize.Bind(ctx, openai.CallAdapter(http.DefaultClient, handle, openai.CallAdapterOptions{
    Model:      "gpt-4o-mini",
    APIKey:     os.Getenv("OPENAI_API_KEY"),
    MaxRetries: 1,
}))

// Plain-Go consumption style — no forge/gstream:
summary, err := Summarize.Call(ctx, article)
```

This is the exact same **declare → plug in Pattern → bind adapter** sequence as every other `ports` boundary — see [Ports, Plugins, and Adapters](../concepts/ports-and-adapters.md). Only `IOPort` accepts `LLMPattern`: an LLM completion is an outbound call the pipeline makes, the same category as `nethttp.CallAdapter`/`sql.QueryEachAdapter`, not a transport that receives external requests.

## `api/llm` — declaring the contract

```go
var Summarize = llm.NewCall[Article, Summary]("summarize", articleCodec, summaryCodec,
    llm.SystemPrompt("You summarize news articles in exactly three sentences."),
    // or: llm.SystemPromptFile("prompts/summarize.md"),
    llm.CallMeta{Description: "Summarizes a news article."},
)

builder := llm.NewBuilder(llm.Info{Name: "My Service", Version: "1.0.0"})
handle, err := Summarize.Register(builder)
// or, for standalone use with no shared spec accumulation:
handle, err := Summarize.ClientHandle()
```

- `llm.SystemPrompt(text)` sets the prompt directly; `llm.SystemPromptFile(path)` loads it from a file (e.g. Markdown) at `Register`/`ClientHandle` time — fails with `llm.SystemPromptFileError` if unreadable.
- `llm.UserMessage(fn)` overrides how the request value is rendered into the LLM's user-turn content; the default JSON-encodes it verbatim.
- `llm.IncludeRequestSchema()` appends the input codec's JSON Schema to the system prompt as a fenced code block (off by default — keeps prompts lean).
- `llm.CallMeta{Description, Tags}` — documentation metadata, surfaced in `render/openaitools` and any future prompt-catalog rendering.

`llm.Builder.LLMSpec()` returns the accumulated catalog of every registered `Call` — the llm-family analogue of `mcp.MCPSpec`, REST's OpenAPI document, and events' AsyncAPI document.

## `adapters/openai` — the wire adapter

`openai.CallAdapter` implements `ports.IOAdapter[Req,Resp]` against any OpenAI-compatible Chat Completions endpoint (OpenAI itself, Azure OpenAI, Ollama, vLLM, LM Studio, Groq, ...) — stdlib-only (`net/http` + `encoding/json`), no SDK dependency. Point `CallAdapterOptions.BaseURL` at a different host to switch providers.

```go
type CallAdapterOptions struct {
    BaseURL        string  // defaults to "https://api.openai.com/v1"
    Model          string
    APIKey         string
    CredentialFunc func(ctx context.Context) (string, error) // takes priority over APIKey
    Temperature    *float64
    MaxTokens      *int
    MaxRetries     int     // bounds a re-prompt-on-invalid-completion loop
    Observer       stats.Observer
}
```

### Structured outputs + local re-validation

Every request sets `response_format` to OpenAI's strict structured-outputs shape using `llm.CallHandle.ResponseSchema` — the response codec's JSON Schema, generated automatically. The completion is then **also** decoded through `llm.CallHandle.DecodeResponse`, which runs every `codex.Codec.Refine` constraint on the response codec — belt-and-suspenders validation: the JSON Schema constrains the shape at generation time; `Refine` catches what a bare schema cannot express (cross-field invariants, custom formats).

### Schema visibility — what the model actually sees

The two codecs are NOT forwarded symmetrically — this is intentional:

- **Response schema**: always sent, on *every* attempt, as `response_format.json_schema.schema` — `jsonschema.Schema(respCodec.Schema)`, rendered once at `Register`/`ClientHandle` time and cached on `CallHandle.ResponseSchema`. The provider contractually constrains its own output to this shape (OpenAI's strict structured-outputs mode).
- **Request schema**: NOT sent by default. Only the concrete JSON-encoded request *value* goes into the user message (`CallHandle.EncodeRequest`, which defaults to `format.JSON(reqCodec).Marshal` — override with `llm.UserMessage`). The request codec's *shape* is only forwarded if you opt in with `llm.IncludeRequestSchema()`, which appends the input codec's JSON Schema to the system prompt as a fenced code block.
- Rationale: concrete example data is usually more useful to a model than an abstract shape description, so schema-in-prompt is opt-in rather than default — it keeps prompts lean unless the raw JSON alone is ambiguous (e.g. sparse/optional fields, enum-like strings whose valid values aren't obvious from one example).

### Retry on invalid completion

When `DecodeResponse` fails, `CallAdapterOptions.MaxRetries` bounds a re-prompt loop: the adapter appends the invalid assistant response plus a new user message describing the validation error, then re-sends the full conversation.

- `MaxRetries: 0` (default) — the first decode failure is returned as-is, a plain `llm.ResponseDecodeError`.
- `MaxRetries: N > 0` — up to `N` additional attempts; if all fail, `openai.RetriesExhaustedError{Model, Attempts, LastErr}` is returned instead.

#### How the validation error is forwarded to the model

Only a codec `Decode`/`Refine` failure (`llm.ResponseDecodeError`) triggers a retry — transport errors, non-2xx status, malformed bodies, and zero-choice responses fail immediately without a re-prompt, since those are provider/transport problems a re-prompt can't fix. On a decode failure with `MaxRetries > 0`, exactly two messages are appended to the conversation before re-sending the full history:

```text
{role: "assistant", content: <the invalid completion, verbatim>}
{role: "user",      content: "Your last response did not match the required schema: <decodeErr>. Please try again."}
```

`<decodeErr>` is the `%v`-formatted `llm.ResponseDecodeError`, which wraps the underlying `codex.Codec` `Decode`/`Refine` error text — so the model receives the *actual reason* its output was rejected (e.g. a specific `Refine` constraint message like `"age must be >= 0"`), not a generic "invalid" notice. This gives the model concrete, actionable feedback for its next attempt rather than asking it to guess what was wrong.

## Structured errors

All implement `slog.LogValuer`; errors with an inner `Err`/`LastErr` field implement `Unwrap()`.

| Package | Type | Fields | When |
|---|---|---|---|
| `api/llm` | `SystemPromptFileError` | `Path, Err` | `SystemPromptFile` path unreadable at Register/ClientHandle time |
| `api/llm` | `ResponseDecodeError` | `Name, Raw, Err` | `CallHandle.DecodeResponse` — codec Decode/Refine failure on the raw completion |
| `adapters/openai` | `RequestBuildError` | `Err` | Building the HTTP request failed |
| `adapters/openai` | `RequestError` | `Model, Err` | `http.Client.Do` failed (network/DNS/TLS/timeout) |
| `adapters/openai` | `UnexpectedStatusError` | `Model, StatusCode, Body` | Non-2xx HTTP response |
| `adapters/openai` | `ResponseBodyError` | `Err` | Reading the response body failed after a successful connection |
| `adapters/openai` | `NoChoicesError` | `Model` | The provider's response contained zero completion choices |
| `adapters/openai` | `RetriesExhaustedError` | `Model, Attempts, LastErr` | `MaxRetries` exhausted without a valid completion |

## Observer

`adapters/openai.CallAdapter` follows `nethttp.Call`'s exact observer convention:

- `stats.Observer.RecordRequest("llm", handle.Name, statusCode, duration)` on **every** code path — status `0` when no HTTP call reached the network (pre-flight `EncodeRequest` failure, `CredentialFunc` error, request-build failure).
- `stats.ReportErrors(obs, "response", err)` fires `RecordValidationError` per field when `DecodeResponse` fails.
- Each retry attempt fires its own `RecordRequest` call — observers see the full attempt sequence, not just the final outcome.
- No `SecurityObserver` — authentication is a single Bearer token/`CredentialFunc`, not a pluggable security scheme.

## Current limitations — format and content type

- **JSON-only, no format selection.** `api/llm`'s `build()` hardcodes `format.JSON(reqCodec)`/`format.JSON(respCodec)` for both request encoding and response decoding. Unlike `FilePattern`/`CachePattern`/`SocketPattern` (which all expose `Format FileFormatKind` + a `CustomFormat any` escape hatch), neither `llm.CallOpt` nor `ports.LLMPattern` has a `Format`/`CustomFormat` option today — there is no way to request YAML or TOML encoding for the request/response bodies.
- **No multimodal/binary content.** The OpenAI wire message shape (`chatMessage{Role, Content string}`) always sends a plain string. OpenAI's actual Chat Completions API supports a multimodal `content` array (`[{"type":"text",...}, {"type":"image_url",...}, {"type":"input_audio",...}]`) for vision/audio-capable models, but `adapters/openai` never constructs that shape — there is currently no way to attach an image (PNG/JPEG), audio clip, or video frame to a `Call`. The adapter is text/JSON-only end-to-end, both directions.
- Tracked as a future enhancement, not yet designed in detail: see [OpenAI Multimodal Content (roadmap)](../roadmap/openai-multimodal-content.md).

## `render/openaitools` — exposing tools to a raw tool-calling loop

A pure renderer (same category as `render/openapi`/`render/asyncapi`/`render/jsonschema`) that converts an **existing** `mcp.MCPSpec` or `llm.LLMSpec` catalog into the OpenAI `tools` JSON array shape — zero additional declaration:

```go
mcpSpec, _ := mcpBuilder.MCPSpec()
tools := openaitools.FromMCPSpec(mcpSpec)
toolsJSON, err := openaitools.Render(tools)
// toolsJSON is ready to embed in an OpenAI-style "tools" request field.
```

`openaitools.FromLLMSpec` does the same for declared `llm.Call`s — letting one LLM-backed `Call` be exposed as a callable "tool" to a *different* orchestrating LLM (agent-calls-agent via the tool-calling convention). [`examples/adapters-openai`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-openai) demonstrates this: it passes an `llm.Builder` through `PortOptions.LLMBuilder`, then renders `llmBuilder.LLMSpec()` via `FromLLMSpec`/`Render` before making the actual completion call.

## Landscape — what else to consider

| Standard / framework | go-codex status |
|---|---|
| **MCP (Model Context Protocol)** | Shipped — see [MCP Server](mcp.md). |
| **OpenAI tool/function calling** | Covered by `render/openaitools`. |
| **OpenAI structured outputs** | Used by `adapters/openai`. |
| **Agent2Agent (A2A) protocol** | Not implemented — a full JSON-RPC agent server (task lifecycle, push notifications, its own auth model) is materially larger scope than this feature; revisit if a concrete driving use case appears. |
| **LangChain / LlamaIndex / similar frameworks** | No go-codex code needed — these are client-side consumers that already work with an MCP server or a plain tool-array JSON payload (either produced by this package). |
| **Multimodal input (images/audio/video)** | Not implemented — see [current limitations](#current-limitations--format-and-content-type) above and [OpenAI Multimodal Content (roadmap)](../roadmap/openai-multimodal-content.md). |

## See also

- [MCP Server](mcp.md) — the inbound direction (LLM calls go-codex).
- [Ports, Plugins, and Adapters](../concepts/ports-and-adapters.md) — the declare → plug in → bind model this feature follows.
- [Vector Store Adapter (RAG retrieval)](../roadmap/vector-store-adapter.md) — the retrieval half for building RAG agents on top of this feature (not yet implemented); see its [Orchestration section](../roadmap/vector-store-adapter.md#orchestration--how-retrieval-and-generation-compose) for how retrieval and generation compose today with zero new primitives.
- [OpenAI Multimodal Content](../roadmap/openai-multimodal-content.md) — planned future support for image/audio/video content in `adapters/openai` (not yet implemented).
