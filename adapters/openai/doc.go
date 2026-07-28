// Package openai implements [ports.IOAdapter] against any OpenAI-compatible
// Chat Completions endpoint (OpenAI itself, Azure OpenAI, Ollama, vLLM, LM
// Studio, Groq, and others that speak the same wire format).
//
// It is the client-adapter half of [api/llm]'s declare → register → handle
// pattern: [api/llm.Call] declares a system prompt plus typed input/output
// codecs, protocol-agnostically; [CallAdapter] supplies the OpenAI wire
// format and implements [ports.IOAdapter], so an LLM completion becomes a
// normal [ports.IOPort] step in a pipeline — indistinguishable in shape from
// an HTTP call, a SQL query, or a cache lookup.
//
// Zero external SDK dependency: plain net/http + encoding/json, matching
// [adapters/nethttp]/[adapters/chi]'s stdlib-only precedent. Any
// OpenAI-compatible provider works by pointing [CallAdapterOptions.BaseURL]
// at a different host.
//
// # Usage
//
//	handle, err := domain.Summarize.PluginLLMPattern(domain.SummarizePattern)
//	domain.Summarize.Bind(ctx, openai.CallAdapter(http.DefaultClient, handle, openai.CallAdapterOptions{
//	    Model:      "gpt-4o-mini",
//	    APIKey:     os.Getenv("OPENAI_API_KEY"),
//	    MaxRetries: 1,
//	}))
//
//	// Plain-Go consumption style — no forge/gstream:
//	summary, err := domain.Summarize.Call(ctx, article)
//
// # Structured outputs + local re-validation
//
// Every completion request sets response_format to
// {"type":"json_schema","json_schema":{"schema":...,"strict":true}} using
// [llm.CallHandle.ResponseSchema] — OpenAI's structured-outputs guarantee
// that the raw completion conforms to the given JSON Schema. The adapter
// then ALSO decodes the completion through
// [llm.CallHandle.DecodeResponse], which runs every [codex.Codec.Refine]
// constraint on the response codec — cross-field invariants, custom
// formats, and other checks a JSON Schema alone cannot express.
//
// # Retry on invalid completion
//
// [CallAdapterOptions.MaxRetries] bounds a re-prompt loop: when
// DecodeResponse fails (the provider's structured-outputs guarantee did not
// hold, or a Refine constraint rejected an otherwise schema-valid value),
// the adapter appends the invalid assistant response plus a new user
// message describing the validation error, then re-sends the full
// conversation. With MaxRetries: 0 (the default) the first decode failure
// is returned as-is (a plain [llm.ResponseDecodeError]); once retries are
// exhausted, [RetriesExhaustedError] is returned instead.
package openai
