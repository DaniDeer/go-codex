# LLM Integration — `api/llm`, `adapters/openai`, `render/openaitools`

> **Status:** Design complete — not yet implemented.
> [← Back to Roadmap](index.md)
>
> See also: [MCP Server feature](../features/mcp.md) · [API Contracts concept](../concepts/api-contracts.md) · [Ports, Plugins, and Adapters concept](../concepts/ports-and-adapters.md)

---

## Motivation

go-codex already lets an LLM **discover and call** business logic declared in Go: `api/mcp` + `adapters/mcpgo` expose any `ToolPort`/`LatestPort` as an MCP tool, with the input/output JSON Schema derived automatically from the same codecs that validate the Go value. That direction — **LLM calls go-codex** — is done.

This roadmap covers the **other direction — go-codex calls an LLM** — plus making the schemas we already generate maximally easy for *any* agent framework to discover, not just MCP clients.

The concrete new capability: declare a **system prompt + typed input + typed output** exactly the way every other go-codex boundary is declared, then bind it to a concrete LLM provider via an adapter — so an LLM completion becomes a normal `IOPort` step in a pipeline, indistinguishable in shape from an HTTP call, a SQL query, or a cache lookup. The LLM's raw completion is constrained by the output codec's JSON Schema at the API level (OpenAI-style "strict structured outputs") **and** re-validated locally through the exact same `codex.Refine` constraints as anywhere else in the codec — cross-field invariants, formats, ranges — belt-and-suspenders validation that a bare JSON Schema alone cannot express.

---

## Landscape review — what already exists, what's genuinely new

| Standard / framework | What it is | go-codex status |
|---|---|---|
| **MCP (Model Context Protocol)** | Anthropic-originated, now broadly adopted wire protocol for exposing tools/resources/prompts to LLM clients (Claude Desktop, IDEs, etc.) over JSON-RPC (stdio or HTTP) | **Shipped** — `api/mcp` + `adapters/mcpgo`. `ToolHandle.InputSchema`/`OutputSchema` are already derived JSON Schema. Nothing to add for the inbound direction. |
| **OpenAI tool/function calling** | `tools: [{type:"function", function:{name, description, parameters: <JSON Schema>, strict}}]` on Chat Completions/Responses API | Same shape as MCP's `ToolSpec` (name + description + JSON Schema). A thin renderer converts existing `mcp.ToolSpec`/new `llm.CallSpec` into this array — no new declaration surface needed. **New in this roadmap: `render/openaitools`.** |
| **OpenAI structured outputs** | `response_format: {type:"json_schema", json_schema:{schema, strict:true}}` — model output is constrained to conform to the given JSON Schema | This is exactly what an output codec's `.Schema` already produces. **New in this roadmap: `adapters/openai`** uses it, then re-validates via `codex.Refine` locally. |
| **Agent2Agent (A2A) protocol** | Google-originated (2025) agent-to-agent interoperability protocol: JSON-RPC 2.0 (+ gRPC/SSE) task lifecycle, `AgentCard` published at `/.well-known/agent-card.json`, `AgentSkill{id, name, description, tags, input_modes, output_modes}` | **Deferred to Phase 2** (see below) — a full new wire protocol (task lifecycle, push notifications, its own auth model) is a much larger lift than the other rows. An `AgentCard` renderer reusing `mcp.ToolSpec`/`llm.CallSpec` (skills without the JSON-RPC server) is a reasonable smaller Phase 2 slice if a concrete use case appears. |
| **LangChain / LlamaIndex / similar orchestration frameworks** | Client-side Python/JS libraries; consume tools via the OpenAI function-calling shape or by talking to an MCP server directly (`langchain-mcp-adapters` exists) | **No go-codex code needed.** These frameworks are consumers, not a wire protocol go-codex must implement. Point users at the existing MCP server or at `render/openaitools`'s output — either already works with these frameworks unmodified. Document this explicitly so nobody proposes bespoke "LangChain support." |

---

## Scope decisions — Phase 1

| In scope (Phase 1) | Out of scope (Phase 2+, see bottom) |
|---|---|
| New `api/llm` package: declarative system-prompt + input-codec + output-codec "Call" contract, mirroring `api/reqreply`'s declare → register → handle shape | Agent2Agent (A2A) protocol — full JSON-RPC agent server |
| New `adapters/openai` package: `ports.IOAdapter[Req,Resp]` calling an OpenAI-compatible Chat Completions endpoint, using strict `response_format: json_schema` | Streaming (SSE token-by-token) completions |
| Bounded retry-on-invalid-completion loop (re-prompt with the validation error, up to N attempts) | Multi-turn tool-calling loop (the LLM itself invoking our declared MCP tools mid-conversation) |
| New `render/openaitools` package: renders `mcp.ToolSpec`/`llm.CallSpec` → OpenAI `tools` JSON array | Vision/multimodal input |
| `ports.LLMPattern` + `IOPort.PluginLLMPattern` — same "Pattern is the primary declaration surface" model as REST/Event/ReqReply/MCP | Embeddings, moderation, or any non-chat-completions OpenAI endpoint |
| System prompt supplied as a plain string OR loaded from a Markdown file at Register time | A2A `AgentCard` renderer (reasonable smaller Phase 2 slice, needs a concrete use case first) |

---

## Toolchain / dependency decision

**Recommendation: zero external SDK dependency — plain `net/http` + `encoding/json`,** matching `adapters/nethttp`/`adapters/chi`'s existing stdlib-only precedent. The OpenAI Chat Completions wire format is a single JSON POST with Bearer auth — no need for `sashabaranov/go-openai` or the official `openai-go` SDK. This also means ANY OpenAI-compatible provider (Azure OpenAI, Ollama, vLLM, LM Studio, Groq, etc.) works by pointing `BaseURL` at a different host — no SDK lock-in. If a concrete need for provider-specific features (e.g. Azure AD auth, Anthropic's native — non-OpenAI-compatible — Messages API) appears later, that becomes its own adapter package, not a dependency added here.

---

## Architecture

```
domain/pipeline.go — zero adapter imports
    var Summarize = codex.Must(ports.NewIOPort[Article, Summary](
        "summarize", articleCodec, summaryCodec, ports.PortOptions{}))

    var SummarizePattern = ports.LLMPattern{
        Name:         "summarize",
        SystemPrompt: "You summarize news articles in exactly 3 sentences...",
    }

    summaries := Summarize.Connect(ctx, articles)   // or Summarize.Call(ctx, article) — plain-Go style

main.go — the only place that knows the concrete provider
    handle, _ := Summarize.PluginLLMPattern(SummarizePattern)
    Summarize.Bind(ctx, openai.CallAdapter(httpClient, handle, openai.CallAdapterOptions{
        Model:  "gpt-4o-mini",
        APIKey: os.Getenv("OPENAI_API_KEY"),
    }))
```

This is the exact same **declare → plug in Pattern → bind adapter** sequence as every other `ports` boundary (see [Ports, Plugins, and Adapters](../concepts/ports-and-adapters.md)) — an LLM completion is not a special case, it is an `IOAdapter[Req,Resp]` like `nethttp.CallAdapter` or `sql.QueryEachAdapter`. `Summarize.Call(ctx, article)` (the plain-Go, non-pipeline consumption style) works unchanged too.

---

## API surface — `api/llm`

```go
package llm

// Call declares an LLM completion contract: a system prompt plus typed
// input/output codecs. Call is protocol-agnostic — it does not know about
// OpenAI, Azure, or any specific provider; adapters/openai (or a future
// provider-specific adapter) supplies the wire format.
type Call[Req, Resp any] struct { /* unexported */ }

// NewCall declares a Call. name is used for observability, error context,
// and spec generation (render/openaitools, future prompt-catalog docs).
func NewCall[Req, Resp any](
    name string,
    reqCodec codex.Codec[Req],
    respCodec codex.Codec[Resp],
    opts ...CallOpt,
) Call[Req, Resp]

// CallOpt configures a Call at declaration time.
type CallOpt interface{ applyCall(*callBuilder) }

// SystemPrompt sets the system prompt text directly.
func SystemPrompt(text string) CallOpt

// SystemPromptFile loads the system prompt from a file (e.g. a .md file)
// at Register/ClientHandle time. Register fails with SystemPromptFileError
// if the file cannot be read — same fallibility precedent as
// rest.WithPathConstraints/reqreply topic validation.
func SystemPromptFile(path string) CallOpt

// UserMessage overrides how Req is rendered into the LLM's user-turn
// content. Default: JSON-encode reqCodec's output verbatim
// (`format.JSON(reqCodec)`), no extra wrapping text.
func UserMessage[Req any](fn func(Req) (string, error)) CallOpt

// IncludeRequestSchema appends the input codec's JSON Schema to the system
// prompt (as a fenced code block) — useful when the raw JSON alone is
// ambiguous. Default false (keeps prompts lean; the model already receives
// the concrete data, not just its shape).
func IncludeRequestSchema() CallOpt

// CallMeta holds documentation metadata — mirrors RouteMeta/ChannelMeta/
// ToolMeta's role in the other API families.
type CallMeta struct {
    Description string   // human-readable purpose, surfaced in render/openaitools
    Tags        []string
}
func (m CallMeta) applyCall(cb *callBuilder)

// Builder accumulates CallSpec entries — same role as rest.Builder/
// events.Builder/reqreply.Builder/apimcp.Builder, minus the OpenAPI/
// AsyncAPI-specific spec assembly (there is no path/topic template to
// validate; LLMSpec is a flat catalog).
type Builder struct { /* unexported */ }
func NewBuilder(info Info) *Builder
func (b *Builder) LLMSpec() (*LLMSpec, error)

type Info struct {
    Name    string
    Version string
}

// LLMSpec is the static catalog of all declared Call contracts — the
// llm-family analogue of mcp.MCPSpec, rest's OpenAPI document, and events'
// AsyncAPI document. Feeds render/openaitools (and any future prompt-catalog
// renderer).
type LLMSpec struct {
    Name    string     `json:"name"`
    Version string     `json:"version"`
    Calls   []CallSpec `json:"calls,omitempty"`
}

// CallSpec is the spec entry for one declared Call in LLMSpec.
type CallSpec struct {
    Name          string          `json:"name"`
    Description   string          `json:"description,omitempty"`
    Tags          []string        `json:"tags,omitempty"`
    SystemPrompt  string          `json:"systemPrompt"`
    RequestSchema  json.RawMessage `json:"requestSchema,omitempty"`
    ResponseSchema json.RawMessage `json:"responseSchema,omitempty"`
}

// Register validates the Call, resolves the system prompt (reading
// SystemPromptFile if used), renders request/response JSON Schemas, adds a
// CallSpec entry to b, and returns a *CallHandle. Mirrors
// rest.Route.Register/events.Channel.Register/reqreply.Route.Register.
func (c Call[Req, Resp]) Register(b *Builder) (*CallHandle[Req, Resp], error)

// ClientHandle builds a *CallHandle without registering against a Builder
// — for a Call used standalone, with no shared spec accumulation. Mirrors
// rest.Route.ClientHandle/events.Channel.ClientHandle.
func (c Call[Req, Resp]) ClientHandle() (*CallHandle[Req, Resp], error)

// CallHandle is the protocol-agnostic runtime object adapters/openai (or
// any future provider adapter) uses. It never touches HTTP.
type CallHandle[Req, Resp any] struct {
    Name         string
    SystemPrompt string // fully resolved (file already read, if used)

    // EncodeRequest renders req into the LLM's user-turn content string —
    // the default JSON encoding, or the caller's UserMessage override.
    EncodeRequest func(req Req) (string, error)

    // ResponseSchema is the JSON Schema derived from respCodec — passed to
    // the provider as response_format/json_schema by the adapter.
    ResponseSchema json.RawMessage

    // DecodeResponse parses the LLM's raw completion content (already
    // constrained by ResponseSchema at the API level) through respCodec —
    // applying every Refine constraint exactly like any other boundary.
    // Errors are wrapped as ResponseDecodeError.
    DecodeResponse func(raw []byte) (Resp, error)
}
```

Reused, not reinvented: `format.JSON` for the default request encoding, `jsonschema.Schema` for `ResponseSchema`, `codex.Codec[T].Decode`/`.Refine` for response validation — `api/llm` adds zero new codec machinery, exactly like `api/rest`/`api/events`/`api/reqreply`/`api/mcp` before it.

---

## API surface — `adapters/openai`

```go
package openai

// CallAdapter returns a ports.IOAdapter[Req,Resp] that fulfills the port's
// Connect/Call by completing a Chat Completions request against an
// OpenAI-compatible endpoint. Use with ports.IOPort.Bind:
//
//	handle, _ := domain.Summarize.PluginLLMPattern(domain.SummarizePattern)
//	domain.Summarize.Bind(ctx, openai.CallAdapter(httpClient, handle, openai.CallAdapterOptions{
//	    Model: "gpt-4o-mini", APIKey: os.Getenv("OPENAI_API_KEY"),
//	}))
func CallAdapter[Req, Resp any](
    client *http.Client,
    handle *llm.CallHandle[Req, Resp],
    opts CallAdapterOptions,
) ports.IOAdapter[Req, Resp]

// CallAdapterOptions configures the adapter.
type CallAdapterOptions struct {
    // BaseURL defaults to "https://api.openai.com/v1". Point at any
    // OpenAI-compatible endpoint (Azure OpenAI, Ollama, vLLM, LM Studio, ...).
    BaseURL string

    // Model is the model identifier sent on every request (e.g. "gpt-4o-mini").
    Model string

    // APIKey is sent as "Authorization: Bearer <APIKey>". Use CredentialFunc
    // instead for per-request/rotating credentials.
    APIKey string
    // CredentialFunc, if set, is called per request and takes priority over
    // APIKey — mirrors nethttp.Call's CredentialFunc option exactly.
    CredentialFunc func(ctx context.Context) (string, error)

    // Temperature, MaxTokens: optional, nil/0 = provider default.
    Temperature *float64
    MaxTokens   *int

    // MaxRetries bounds the re-prompt-on-invalid-completion loop (default 0
    // = no retry; the first codec-validation failure is returned as-is).
    // On failure, the adapter appends the validation error as an additional
    // user message ("Your last response did not match the required
    // schema: <error>. Please try again.") and re-sends the conversation.
    MaxRetries int

    Observer stats.Observer
}

// Structured errors — all implement slog.LogValuer, Unwrap where an inner
// Err field exists.
type RequestBuildError struct{ Err error }        // building the HTTP request failed
type RequestError struct{ Model string; Err error } // http.Client.Do failed (network/DNS/TLS/timeout)
type UnexpectedStatusError struct{ Model string; StatusCode int; Body string } // non-2xx response
type ResponseBodyError struct{ Err error }         // io.ReadAll failed after a successful connection
type NoChoicesError struct{ Model string }         // API returned zero completion choices
type RetriesExhaustedError struct{ Model string; Attempts int; LastErr error } // MaxRetries exhausted
```

Same typed-error shape as `adapters/nethttp.Call` (`RequestBuildError`/`RequestError`/`UnexpectedStatusError`/`ResponseBodyError` names and roles are intentionally identical) — an LLM completion is "just another outbound HTTP call" from the error-handling perspective, with two new LLM-specific terminal cases (`NoChoicesError`, `RetriesExhaustedError`).

`llm.ResponseDecodeError{Name, Raw, Err}` (in `api/llm`, not `adapters/openai` — it wraps `CallHandle.DecodeResponse`, which is protocol-agnostic) is what `MaxRetries` inspects to build the re-prompt message.

---

## `ports` integration — `LLMPattern`

```go
// LLMPattern declares an LLM completion boundary — reuses api/llm's exact
// option vocabulary, same role as RESTPattern/EventPattern/ReqReplyPattern/
// MCPPattern for their respective api/* families.
type LLMPattern struct {
    Name string
    Opts []llm.CallOpt
}

// PluginLLMPattern registers pattern and returns the resulting
// *llm.CallHandle[Req,Resp] directly — bind adapters/openai.CallAdapter to it.
// Only IOPort supports LLMPattern in Phase 1 (an LLM completion is an
// outbound call the pipeline makes, the same category as nethttp.CallAdapter/
// sql.QueryEachAdapter — not a transport that RECEIVES external requests, so
// ToolPort/SourcePort/SinkPort do not accept it, mirroring how SQLPattern is
// rejected outside its applicable port types).
func (p *IOPort[Req, Resp]) PluginLLMPattern(pattern LLMPattern) (*llm.CallHandle[Req, Resp], error)
```

`PortOptions.LLMBuilder *llm.Builder` follows the exact `RESTBuilder`/`EventBuilder`/`ReqReplyBuilder`/`MCPBuilder` precedent — nil uses a private single-use builder (same zero-ceremony default), a shared builder accumulates every declared `Call` into one `LLMSpec` for a service-wide prompt catalog.

---

## API surface — `render/openaitools`

```go
package openaitools

// Tool is one entry in the OpenAI "tools" array.
type Tool struct {
    Name        string
    Description string
    Parameters  json.RawMessage // JSON Schema
}

// Render produces the exact OpenAI tools-array JSON shape:
//   [{"type":"function","function":{"name":...,"description":...,"parameters":...}}]
func Render(tools []Tool) (json.RawMessage, error)

// FromMCPSpec converts every tool in an mcp.MCPSpec into Tool entries,
// using each ToolSpec's InputSchema as Parameters. Lets an application
// expose its EXISTING MCP tool declarations to a raw OpenAI-style
// tool-calling loop with zero additional declaration.
func FromMCPSpec(spec *mcp.MCPSpec) []Tool

// FromLLMSpec converts every declared llm.Call in an llm.LLMSpec into Tool
// entries, using each CallSpec's RequestSchema as Parameters — lets one
// LLM-backed Call be exposed as a callable "tool" to a DIFFERENT
// orchestrating LLM (agent-calls-agent via the tool-calling convention,
// without needing full A2A).
func FromLLMSpec(spec *llm.LLMSpec) []Tool
```

Zero new declaration surface — this package is a pure renderer, the same category as `render/openapi`/`render/asyncapi`/`render/jsonschema`/`render/pipeline`, just targeting the OpenAI tools-array shape instead of OpenAPI/AsyncAPI/JSON Schema/pipeline YAML.

---

## Structured errors summary (all implement `slog.LogValuer`, `Unwrap()` where an inner `Err` exists)

| Package | Type | Fields | When |
|---|---|---|---|
| `api/llm` | `SystemPromptFileError` | `Path, Err` | `SystemPromptFile` path unreadable at Register time |
| `api/llm` | `ResponseDecodeError` | `Name, Raw, Err` | `CallHandle.DecodeResponse` — codec Decode/Refine failure on the raw completion |
| `adapters/openai` | `RequestBuildError` | `Err` | Building the HTTP request failed |
| `adapters/openai` | `RequestError` | `Model, Err` | `http.Client.Do` failed (network/DNS/TLS/timeout) |
| `adapters/openai` | `UnexpectedStatusError` | `Model, StatusCode, Body` | Non-2xx HTTP response |
| `adapters/openai` | `ResponseBodyError` | `Err` | `io.ReadAll` failed after a successful connection |
| `adapters/openai` | `NoChoicesError` | `Model` | API returned zero completion choices |
| `adapters/openai` | `RetriesExhaustedError` | `Model, Attempts, LastErr` | `MaxRetries` exhausted without a valid completion |

---

## Observer integration

`adapters/openai.CallAdapter` follows `nethttp.Call`'s exact observer convention:

- `stats.Observer.RecordRequest("llm", handle.Name, statusCode, duration)` on **every** code path — status `0` when no HTTP call reached the network (pre-flight `EncodeRequest` failure, `CredentialFunc` error, `RequestBuildError`).
- `stats.ReportErrors(obs, "response", err)` called before the final `RecordRequest` when `DecodeResponse` fails (fires `RecordValidationError` per field via the existing `ValidationErrors` mechanism) — location string `"response"`, a new but self-explanatory addition to the existing vocabulary (`"path"`, `"query"`, `"body"`, `"sql_row"`, `"file"`, ...).
- Each retry attempt (when `MaxRetries > 0`) fires its own `RecordRequest` call — callers can see the full attempt sequence, not just the final outcome.
- `obs` resolution: `CallAdapter` receives `ctx` at `Transform` time (an `IOAdapter` method), so the standard nil-guard applies: `if obs == nil { obs = stats.ObserverFromContext(ctx) }`.
- No `SecurityObserver` — authentication is a single Bearer token/`CredentialFunc`, not a pluggable security scheme the way REST/events model it; same rationale as `adapters/mcpgo` having no `SecurityObserver`.

---

## Unit test plan

| Test | Verifies |
|---|---|
| `TestCall_Register_HappyPath` | `Register` returns a `*CallHandle` with resolved `SystemPrompt`, correct `ResponseSchema` |
| `TestCall_SystemPromptFile_HappyPath` | File contents loaded verbatim into `SystemPrompt` |
| `TestCall_SystemPromptFile_UnreadablePath_ReturnsError` | `SystemPromptFileError`, `errors.As`-navigable, `LogValue` shape |
| `TestCall_ClientHandle_NoBuilder` | Standalone handle construction works without a `Builder` |
| `TestCall_Builder_AccumulatesLLMSpec` | Multiple `Register` calls against one `Builder` produce all `CallSpec` entries in `LLMSpec.Calls` |
| `TestCallHandle_EncodeRequest_DefaultJSON` | Default encoding matches `format.JSON(reqCodec)` output |
| `TestCallHandle_EncodeRequest_UserMessageOverride` | Custom `UserMessage` fn is used instead of the default |
| `TestCallHandle_DecodeResponse_HappyPath` | Valid raw JSON decodes through `respCodec`, including `Refine` constraints |
| `TestCallHandle_DecodeResponse_InvalidJSON_ReturnsResponseDecodeError` | Malformed/schema-violating raw content → `ResponseDecodeError`, `errors.As`-navigable |
| `TestCallAdapter_Transform_HappyPath` | Fake HTTP transport returns a valid completion → decoded `Resp`, `RecordRequest` called with 200 |
| `TestCallAdapter_Transform_NonJSONBody_ReturnsRequestBuildError` (n/a — request body is always well-formed JSON built internally; replaced by) `TestCallAdapter_Transform_EncodeRequestFailure` | `EncodeRequest` error short-circuits before any HTTP call, `RecordRequest` status 0 |
| `TestCallAdapter_Transform_NetworkFailure_ReturnsRequestError` | Fake transport returns a transport error → `RequestError`, `Unwrap` reaches it |
| `TestCallAdapter_Transform_NonOKStatus_ReturnsUnexpectedStatusError` | Fake 4xx/5xx response → `UnexpectedStatusError` with body captured |
| `TestCallAdapter_Transform_EmptyChoices_ReturnsNoChoicesError` | Fake response with `choices: []` → `NoChoicesError` |
| `TestCallAdapter_Transform_InvalidCompletion_NoRetry_ReturnsResponseDecodeError` | `MaxRetries: 0` (default) → first decode failure returned as-is |
| `TestCallAdapter_Transform_InvalidCompletion_RetrySucceeds` | Fake transport returns invalid then valid completion, `MaxRetries: 1` → succeeds on 2nd attempt, both attempts recorded |
| `TestCallAdapter_Transform_RetriesExhausted_ReturnsRetriesExhaustedError` | Fake transport always returns invalid completions → `RetriesExhaustedError{Attempts: N}` after `MaxRetries` |
| `TestCallAdapter_CredentialFunc_TakesPriorityOverAPIKey` | Both set → `CredentialFunc`'s value is sent |
| `TestCallAdapter_ResponseFormat_UsesStrictJSONSchema` | Request body's `response_format.json_schema.schema` matches `handle.ResponseSchema` byte-for-byte, `strict: true` |
| `TestIOPort_PluginLLMPattern_HappyPath` | Same `PluginRESTPattern`-parity test shape — `ports/port_test.go` |
| `TestIOPort_PluginLLMPattern_RejectedOnOtherPortTypes` (compile-time — no method exists on ToolPort/SourcePort/SinkPort) | N/A — enforced by the type system, not a runtime test |
| `TestOpenAITools_Render_HappyPath` | Output matches the exact OpenAI tools-array shape |
| `TestOpenAITools_FromMCPSpec` | Converts an `mcp.MCPSpec` fixture correctly |
| `TestOpenAITools_FromLLMSpec` | Converts an `llm.LLMSpec` fixture correctly |
| `ExampleNewCall` / `ExampleCallAdapter` | pkg.go.dev runnable snippets |

---

## Files to create

| File | Responsibility |
|---|---|
| `api/llm/call.go` | `Call[Req,Resp]`, `CallOpt`, `SystemPrompt`, `SystemPromptFile`, `UserMessage`, `IncludeRequestSchema`, `CallMeta` |
| `api/llm/builder.go` | `Builder`, `Info`, `LLMSpec`, `CallSpec` |
| `api/llm/handle.go` | `CallHandle[Req,Resp]`, `Register`, `ClientHandle` |
| `api/llm/errors.go` | `SystemPromptFileError`, `ResponseDecodeError` |
| `api/llm/doc.go` | Package overview, declare→register→handle example |
| `api/llm/*_test.go` | Per the unit test plan |
| `adapters/openai/client.go` | `CallAdapterOptions`, wire request/response structs (unexported), `CallAdapter` |
| `adapters/openai/binding.go` | `ports.IOAdapter[Req,Resp]` implementation (`Transform`, `AdapterName`) |
| `adapters/openai/errors.go` | `RequestBuildError`, `RequestError`, `UnexpectedStatusError`, `ResponseBodyError`, `NoChoicesError`, `RetriesExhaustedError` |
| `adapters/openai/doc.go` | Package overview |
| `adapters/openai/*_test.go` | Per the unit test plan, fake `http.RoundTripper` for HTTP-level tests |
| `render/openaitools/openaitools.go` | `Tool`, `Render`, `FromMCPSpec`, `FromLLMSpec` |
| `render/openaitools/doc.go` | Package overview |
| `render/openaitools/*_test.go` | Per the unit test plan |
| `ports/llm.go` | `LLMPattern`, `IOPort.PluginLLMPattern`, `PortOptions.LLMBuilder` field addition |
| `examples/adapters-openai/main.go` | Runnable demo: declare a `Call`, bind `adapters/openai.CallAdapter` (against a local fake/httptest server so the example needs no real API key), show the retry loop firing once |
| `docs/features/llm-integration.md` | Feature page (ships when implemented) |
| `docs/guides/llm-integration.md` | Wiring guide (ships when implemented) |

---

## Usage sketch — end to end

```go
// domain/pipeline.go — zero adapter imports
type Article struct{ Title, Body string }
type Summary struct{ ThreeSentences string }

var articleCodec = codex.Struct[Article](
    codex.RequiredField("title", codex.String(), func(a Article) string { return a.Title }, func(a *Article, v string) { a.Title = v }),
    codex.RequiredField("body", codex.String(), func(a Article) string { return a.Body }, func(a *Article, v string) { a.Body = v }),
)
var summaryCodec = codex.Struct[Summary](
    codex.RequiredField("threeSentences", codex.String().Refine(validate.NonEmptyString),
        func(s Summary) string { return s.ThreeSentences },
        func(s *Summary, v string) { s.ThreeSentences = v }),
)

var Summarize = codex.Must(ports.NewIOPort[Article, Summary]("summarize", articleCodec, summaryCodec, ports.PortOptions{}))

var SummarizePattern = ports.LLMPattern{
    Name: "summarize",
    Opts: []llm.CallOpt{
        llm.SystemPromptFile("prompts/summarize.md"),
        llm.CallMeta{Description: "Summarizes a news article in exactly three sentences."},
    },
}

// main.go — the only place that knows the concrete provider
handle, err := domain.Summarize.PluginLLMPattern(domain.SummarizePattern)
domain.Summarize.Bind(ctx, openai.CallAdapter(http.DefaultClient, handle, openai.CallAdapterOptions{
    Model:      "gpt-4o-mini",
    APIKey:     os.Getenv("OPENAI_API_KEY"),
    MaxRetries: 1,
}))

// plain-Go consumption style — no forge/gstream:
summary, err := domain.Summarize.Call(ctx, Article{Title: "...", Body: "..."})
```

`prompts/summarize.md`:
```markdown
You are a precise news summarizer. Given an article's title and body,
respond with EXACTLY three sentences that capture the key facts.
Do not add commentary, opinions, or a fourth sentence.
```

---

## Out of scope — Phase 2+ (deferred, with rationale)

| Deferred capability | Why |
|---|---|
| **Agent2Agent (A2A) protocol** — full JSON-RPC 2.0 agent server, task lifecycle (`sendMessage`, `getTask`, `cancelTask`, push notifications), `AgentCard` at `/.well-known/agent-card.json` | Materially larger scope than the other rows: a new stateful task lifecycle (not a stateless request/response call), its own auth model, and streaming via SSE/gRPC bindings. Needs a concrete driving use case before design — same "awaiting use case" bar as `stream-flatmap.md`. |
| **A2A `AgentCard` renderer only** (skills catalog, no JSON-RPC server) | Smaller, plausible Phase 2 slice reusing `mcp.MCPSpec`/`llm.LLMSpec` — deferred only because Phase 1 already ships two renderers (`render/openaitools` + the underlying `LLMSpec`/`MCPSpec` catalogs); revisit once a real A2A-consuming client needs it. |
| **Streaming completions** (SSE token-by-token) | `adapters/openai.CallAdapter` is synchronous — `Transform` returns one `Resp` per `Req`, matching `IOAdapter`'s existing contract. Token streaming would need a new stream-shaped adapter concept (`SourceAdapter`-like) and doesn't fit the "one struct in, one struct out" completion model this roadmap targets. |
| **Multi-turn tool-calling loop** (the LLM invokes our declared MCP tools mid-conversation, receives results, continues) | A materially different feature — an agentic loop, not a single completion. `render/openaitools.FromMCPSpec` is a building block for this (the array a caller would hand to the LLM alongside messages), but the dispatch loop itself (parse `tool_calls`, run `ToolPort.SetFunc`, feed results back, repeat) is a new orchestration primitive, not an `IOAdapter`. |
| **Vision/multimodal input** | `EncodeRequest` produces a single text string; images/audio need a different content-part wire shape. Revisit if a driving use case appears. |
| **Non-chat-completions OpenAI endpoints** (embeddings, moderation, image generation) | Different wire shapes entirely; each would be its own `IOAdapter`/`ToolAdapter` if ever needed, not part of this roadmap. **Embeddings specifically are tracked in [Vector Store Adapter (RAG retrieval)](vector-store-adapter.md)** — RAG needs them, and that page is the right place to design `adapters/openai`'s embeddings support alongside the vector-store retrieval side it pairs with, rather than growing this page's scope. |
| **LangChain / LlamaIndex "support"** | Not applicable — these are client-side frameworks that already consume MCP servers or plain tool-array JSON. No go-codex code is needed; `docs/features/llm-integration.md` should say this explicitly so it isn't mistakenly re-proposed later. |

---

## Open design decisions (to resolve before/during implementation)

1. **Default `IncludeRequestSchema`** — ships `false` (lean prompts) in this draft. Reconsider if early usage shows models frequently misinterpret ambiguous JSON without the schema alongside it.
2. **Retry re-prompt message wording** — the exact text appended to the conversation on a validation failure (`"Your last response did not match the required schema: <error>. Please try again."`) is a first draft; may need tuning per-model during implementation. Consider making it a `CallOpt` (`RetryPromptTemplate(fn func(error) string) CallOpt`) if the default proves insufficient — not designed in this draft to avoid over-engineering before real usage data exists.
3. **`CallSpec.SystemPrompt` in the spec/catalog** — currently included verbatim (useful for a human-readable prompt catalog), but this means `LLMSpec`/`MCPSpec`-derived renderers could leak prompt text into a document handed to a DIFFERENT LLM (in the agent-calls-agent `FromLLMSpec` scenario). Revisit whether `CallSpec` should omit `SystemPrompt` by default and only include it in an explicit "human docs" rendering mode, separate from the "give this to another agent" rendering mode.
4. **Should `ports.LLMPattern` eventually also apply to `ToolPort`** (an LLM computing a ToolPort's response directly, bypassing `SetFunc`/`SetPipeline`)? Deferred — today, the idiomatic way to have an LLM fulfill a `ToolPort` is for the `SetFunc`/`SetPipeline` body to call an `IOPort` bound to `adapters/openai` itself (composition, no new ToolPort wiring needed). Revisit only if that composition proves awkward in practice.
5. **Provider-specific adapters beyond OpenAI-compatible** (e.g. Anthropic's native Messages API, which is not OpenAI-wire-compatible) — out of scope for this roadmap entirely; would be a separate `adapters/anthropic` roadmap if ever pursued.
