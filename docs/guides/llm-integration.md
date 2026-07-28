# Wiring an LLM Completion into a Pipeline

> See also: [`api/llm` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/api/llm) · [`adapters/openai` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/openai) · [LLM Integration feature page](../features/llm-integration.md) · [Wiring Pipelines with Ports](ports.md)

This guide walks through wiring an LLM completion as a normal `ports.IOPort` step — declare the contract once, bind the concrete provider in `main.go`, and consume it either as part of a forge pipeline or as a plain Go function call.

## Step 1 — Declare domain types and codecs

No adapter imports at this stage:

```go
// domain/pipeline.go
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
```

`validate.NonEmptyString` on the response field matters: it's exactly the kind of constraint a bare JSON Schema cannot express well, and it will be enforced on every completion regardless of what the provider's own structured-outputs guarantee does.

## Step 2 — Declare the port and the LLM pattern

```go
var Summarize = codex.Must(ports.NewIOPort[Article, Summary](
    "summarize", articleCodec, summaryCodec, ports.PortOptions{}))

var SummarizePattern = ports.LLMPattern{
    Name: "summarize",
    Opts: []llm.CallOpt{
        llm.SystemPromptFile("prompts/summarize.md"),
        llm.CallMeta{Description: "Summarizes a news article in exactly three sentences."},
    },
}
```

`llm.SystemPromptFile` reads the file at Plugin/Register time — store your prompts as plain Markdown files under version control, next to your domain code. Use `llm.SystemPrompt("...")` for a short inline prompt instead.

## Step 3 — Plug in the pattern and bind the provider (main.go only)

```go
handle, err := Summarize.PluginLLMPattern(SummarizePattern)
if err != nil {
    log.Fatal(err)
}

err = Summarize.Bind(ctx, openai.CallAdapter(http.DefaultClient, handle, openai.CallAdapterOptions{
    Model:      "gpt-4o-mini",
    APIKey:     os.Getenv("OPENAI_API_KEY"),
    MaxRetries: 1,
}))
```

Point `CallAdapterOptions.BaseURL` at a different host to use Azure OpenAI, Ollama, vLLM, LM Studio, Groq, or any other OpenAI-compatible endpoint — nothing else changes.

## Step 4 — Consume it: two styles, same declaration

**Plain Go — no forge/gstream:**

```go
summary, err := Summarize.Call(ctx, Article{Title: "...", Body: "..."})
```

**Forge-pipeline style — compose with other stream stages:**

```go
articles := SomeSourcePort.Stream(ctx)
summaries := Summarize.Connect(ctx, articles) // gstream.Stream[Summary]
```

Both consume the exact same bound adapter and codec — see [Ports, Plugins, and Adapters](../concepts/ports-and-adapters.md) for the full "two consumption styles, one declaration mechanism" rationale.

## Handling the happy path and the error path

```go
summary, err := Summarize.Call(ctx, article)
if err != nil {
    var rde llm.ResponseDecodeError
    if errors.As(err, &rde) {
        // The completion never became codec-valid — log the raw content for debugging.
        slog.Error("llm response invalid", "raw", string(rde.Raw), "cause", rde.Err)
        return
    }
    var status openai.UnexpectedStatusError
    if errors.As(err, &status) {
        slog.Error("llm provider error", "status", status.StatusCode, "body", status.Body)
        return
    }
    var exhausted openai.RetriesExhaustedError
    if errors.As(err, &exhausted) {
        slog.Error("llm retries exhausted", "attempts", exhausted.Attempts, "cause", exhausted.LastErr)
        return
    }
    slog.Error("llm call failed", "cause", err)
    return
}
// Happy path — summary is fully validated (JSON Schema at the API level,
// plus every codex.Refine constraint locally).
fmt.Println(summary.ThreeSentences)
```

## Testing without a real API key

Bind against a local `httptest.Server` that returns a canned Chat Completions response — no network access or API key required. See [`examples/adapters-openai`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-openai) for a complete runnable demo, including the retry loop firing once against an intentionally invalid first completion.

## Exposing the same declarations to a raw tool-calling loop

If a separate orchestrating LLM (or a framework like LangChain) needs to call your declared tools/LLM calls directly via the OpenAI tool-calling convention:

```go
mcpSpec, _ := mcpBuilder.MCPSpec()
llmSpec, _ := llmBuilder.LLMSpec()

tools := append(openaitools.FromMCPSpec(mcpSpec), openaitools.FromLLMSpec(llmSpec)...)
toolsJSON, err := openaitools.Render(tools)
```

No new declarations — this reuses whatever you already registered against `mcp.Builder`/`llm.Builder`. [`examples/adapters-openai`](https://github.com/DaniDeer/go-codex/tree/main/examples/adapters-openai) demonstrates this end-to-end: it passes an `llm.Builder` via `PortOptions.LLMBuilder`, plugs `Summarize` into `LLMPattern`, then calls `llmBuilder.LLMSpec()` + `openaitools.FromLLMSpec`/`Render` to print the resulting OpenAI tools-array JSON before making the actual completion call.

## See also

- [LLM Integration feature page](../features/llm-integration.md) — full API reference, structured errors, observer integration.
- [MCP Server](../features/mcp.md) — the inbound direction (LLM calls go-codex).
- [Wiring Pipelines with Ports](ports.md) — the general `ports` wiring guide this page specializes.
