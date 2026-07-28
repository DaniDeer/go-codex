// Package llm provides a transport-agnostic declaration for an LLM
// completion contract: a system prompt plus typed input/output codecs.
//
// It follows the same declare → register → handle pattern as [api/rest],
// [api/events], [api/reqreply], and [api/mcp]. [Call] is the llm analogue of
// [reqreply.Route]: a typed request/response declaration, but for a
// completion request to a large language model instead of a network peer.
// Call is protocol-agnostic — it does not know about OpenAI, Azure, or any
// specific provider; [adapters/openai] (or a future provider-specific
// adapter) supplies the wire format and implements [ports.IOAdapter].
//
// # Usage
//
//	// Declare once — no HTTP method, no topic, just a system prompt and codecs.
//	var Summarize = llm.NewCall[Article, Summary]("summarize", articleCodec, summaryCodec,
//	    llm.SystemPromptFile("prompts/summarize.md"),
//	    llm.CallMeta{Description: "Summarizes a news article in exactly three sentences."},
//	)
//
//	// Register with a Builder to get a CallHandle and an LLMSpec catalog.
//	builder := llm.NewBuilder(llm.Info{Name: "My Service", Version: "1.0.0"})
//	handle, err := Summarize.Register(builder)
//
//	// The handle is protocol-agnostic — pass it to adapters/openai.CallAdapter
//	// (or any future provider adapter) via a ports.IOPort:
//	domain.Summarize.Bind(ctx, openai.CallAdapter(httpClient, handle, openai.CallAdapterOptions{
//	    Model: "gpt-4o-mini", APIKey: os.Getenv("OPENAI_API_KEY"),
//	}))
//
// # Standalone use — no shared Builder
//
// Use [Call.ClientHandle] instead of [Call.Register] when a Call is used
// standalone, with no shared spec accumulation:
//
//	handle, err := Summarize.ClientHandle()
//
// # The one-struct, one-call promise
//
// An LLM completion has no path/topic/header/query var-boundary concept —
// [CallHandle.EncodeRequest] renders exactly one Req value into the user-turn
// content, and [CallHandle.DecodeResponse] parses exactly one raw completion
// back into exactly one Resp value, running every [codex.Codec.Refine]
// constraint declared on the response codec. The response is ALSO
// constrained at the API level via [CallHandle.ResponseSchema] (OpenAI-style
// "strict structured outputs") — belt-and-suspenders validation: the JSON
// Schema constrains the shape at generation time, [codex.Refine] catches
// what a bare schema cannot express (cross-field invariants, custom
// constraints).
package llm
