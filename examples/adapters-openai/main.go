// Package adapters-openai demonstrates go-codex CALLING an LLM — the other
// direction from examples/adapters-mcp (which lets an LLM call go-codex).
//
// Three steps, mirroring every other ports boundary in this library:
//
//  1. Declare — domain/pipeline.go, no adapter imports: an api/llm.Call
//     (system prompt + input/output codecs) wrapped in a ports.IOPort.
//  2. Plug in — main.go: ports.LLMPattern registers the Call (against an
//     llm.Builder, so its LLMSpec catalog is available afterward) and
//     returns a typed *llm.CallHandle.
//  3. Bind — main.go: openai.CallAdapter supplies the OpenAI wire format.
//
// This example runs against a local httptest.Server (no real API key or
// network access needed) that returns an intentionally INVALID completion
// on the first request and a valid one on the second — demonstrating
// CallAdapterOptions.MaxRetries' re-prompt-on-invalid-completion loop firing
// exactly once.
//
// It also demonstrates render/openaitools: once Summarize is plugged into
// SummarizePattern, llmBuilder.LLMSpec() returns the accumulated catalog of
// every declared Call, and openaitools.FromLLMSpec/Render converts it into
// the OpenAI "tools" JSON array shape — letting this SAME LLM-backed Call be
// exposed as a callable tool to a DIFFERENT orchestrating LLM/agent, with no
// additional declaration.
//
// # Running
//
// go run ./examples/adapters-openai
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"github.com/DaniDeer/go-codex/adapters/openai"
	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/render/openaitools"
	"github.com/DaniDeer/go-codex/validate"
)

// ── Domain types — no adapter imports ──────────────────────────────────────

// Article is the input to the Summarize LLM call.
type Article struct {
	Title string
	Body  string
}

var articleCodec = codex.Struct[Article](
	codex.RequiredField("title", codex.String(),
		func(a Article) string { return a.Title },
		func(a *Article, v string) { a.Title = v }),
	codex.RequiredField("body", codex.String(),
		func(a Article) string { return a.Body },
		func(a *Article, v string) { a.Body = v }),
)

// Summary is the output — a single non-empty summary sentence. Refine
// (validate.NonEmptyString) is what a bare JSON Schema cannot enforce on its
// own: the belt-and-suspenders local re-validation this example exercises.
type Summary struct {
	ThreeSentences string
}

var summaryCodec = codex.Struct[Summary](
	codex.RequiredField("threeSentences", codex.String().Refine(validate.NonEmptyString),
		func(s Summary) string { return s.ThreeSentences },
		func(s *Summary, v string) { s.ThreeSentences = v }),
)

// ── Port declaration — same shape as any other IOPort in this library ─────

var (
	// llmBuilder accumulates every Call plugged in via LLMPattern across
	// this catalog — passed through PortOptions.LLMBuilder below so its
	// LLMSpec() reflects every declared Call, the same role
	// RESTBuilder/EventBuilder/MCPBuilder play for their families.
	llmBuilder = llm.NewBuilder(llm.Info{Name: "Adapters OpenAI Example", Version: "1.0.0"})

	// Summarize is bound to openai.CallAdapter in main() below — a real
	// application would point CallAdapterOptions.BaseURL at
	// "https://api.openai.com/v1" (the default) instead of a local fake.
	Summarize = codex.Must(ports.NewIOPort[Article, Summary](
		"summarize", articleCodec, summaryCodec, ports.PortOptions{LLMBuilder: llmBuilder}))

	// SummarizePattern declares the system prompt inline — a real
	// application would likely use llm.SystemPromptFile("prompts/summarize.md")
	// instead, per docs/guides/llm-integration.md.
	SummarizePattern = ports.LLMPattern{
		Name: "summarize",
		Opts: []llm.CallOpt{
			llm.SystemPrompt("You summarize news articles in exactly three sentences."),
			llm.CallMeta{Description: "Summarizes a news article in exactly three sentences."},
		},
	}
)

// fakeCompletionServer returns an httptest.Server standing in for an
// OpenAI-compatible Chat Completions endpoint. The first request gets an
// INVALID completion (empty threeSentences, fails NonEmptyString); the
// second gets a valid one — demonstrating MaxRetries:1's re-prompt loop.
func fakeCompletionServer() *httptest.Server {
	attempt := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		content := `{"threeSentences":""}` // invalid: fails NonEmptyString
		if attempt > 1 {
			content = `{"threeSentences":"Wildfires spread across the region. Officials urged evacuation. Firefighters battled the blaze through the night."}`
		}
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": content}},
			},
		})
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
}

func main() {
	ctx := context.Background()

	// ── Plug in the pattern to get the typed handle ────────────────────────
	handle, err := Summarize.PluginLLMPattern(SummarizePattern)
	if err != nil {
		fmt.Fprintln(os.Stderr, "plugin LLM pattern:", err)
		os.Exit(1)
	}

	// ── render/openaitools: expose the declared Call as an OpenAI "tool" ───
	// llmBuilder.LLMSpec() reflects every Call plugged into it above (here,
	// just Summarize) — FromLLMSpec/Render convert that catalog into the
	// exact OpenAI tools-array JSON shape, ready to embed in a DIFFERENT
	// orchestrating LLM's "tools" request field (agent-calls-agent).
	llmSpec, err := llmBuilder.LLMSpec()
	if err != nil {
		fmt.Fprintln(os.Stderr, "build LLM spec:", err)
		os.Exit(1)
	}
	toolsJSON, err := openaitools.Render(openaitools.FromLLMSpec(llmSpec))
	if err != nil {
		fmt.Fprintln(os.Stderr, "render openai tools:", err)
		os.Exit(1)
	}
	fmt.Println("openai tools array:", string(toolsJSON))

	// ── Bind a local fake server standing in for an OpenAI-compatible API ──
	srv := fakeCompletionServer()
	defer srv.Close()

	if err := Summarize.Bind(ctx, openai.CallAdapter(srv.Client(), handle, openai.CallAdapterOptions{
		BaseURL:    srv.URL,
		Model:      "gpt-4o-mini",
		APIKey:     "unused-with-local-fake",
		MaxRetries: 1, // one re-prompt attempt if the completion fails validation
	})); err != nil {
		fmt.Fprintln(os.Stderr, "bind summarize port:", err)
		os.Exit(1)
	}

	// ── Plain-Go consumption style — no forge/gstream composition ─────────
	summary, err := Summarize.Call(ctx, Article{
		Title: "Wildfire Update",
		Body:  "A wildfire has spread across the region overnight...",
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "summarize:", err)
		os.Exit(1)
	}

	fmt.Println("summary:", summary.ThreeSentences)
	fmt.Println("(the first fake completion was intentionally invalid; MaxRetries:1 recovered on the second attempt)")
}
