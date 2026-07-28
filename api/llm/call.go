package llm

import "github.com/DaniDeer/go-codex/codex"

// CallOpt is the sealed interface for variadic [NewCall] options.
//
// The following types implement CallOpt:
//   - [SystemPromptOpt] (returned by [SystemPrompt]) — system prompt text
//   - [SystemPromptFileOpt] (returned by [SystemPromptFile]) — system prompt loaded from a file
//   - [UserMessageOpt] (returned by [UserMessage]) — custom request-encoding function
//   - [IncludeRequestSchemaOpt] (returned by [IncludeRequestSchema]) — append the input schema to the prompt
//   - [CallMeta] — documentation metadata (Description, Tags)
type CallOpt interface{ applyCall(*callBuilder) }

// callBuilder accumulates CallOpt values during [Call.Register]/[Call.ClientHandle].
type callBuilder struct {
	systemPrompt        string
	systemPromptFile    string
	hasSystemPrompt     bool
	hasSystemPromptFile bool
	userMessage         any // type-erased func(Req) (string, error); resolved generically at Register/ClientHandle time
	includeReqSchema    bool
	meta                CallMeta
}

// SystemPromptOpt is returned by [SystemPrompt]. Implements [CallOpt].
type SystemPromptOpt struct{ text string }

func (o SystemPromptOpt) applyCall(cb *callBuilder) {
	cb.systemPrompt = o.text
	cb.hasSystemPrompt = true
}

// SystemPrompt sets the system prompt text directly.
func SystemPrompt(text string) SystemPromptOpt { return SystemPromptOpt{text: text} }

// SystemPromptFileOpt is returned by [SystemPromptFile]. Implements [CallOpt].
type SystemPromptFileOpt struct{ path string }

func (o SystemPromptFileOpt) applyCall(cb *callBuilder) {
	cb.systemPromptFile = o.path
	cb.hasSystemPromptFile = true
}

// SystemPromptFile loads the system prompt from a file (e.g. a Markdown
// file) at [Call.Register]/[Call.ClientHandle] time. Register/ClientHandle
// fails with [SystemPromptFileError] if the file cannot be read — same
// fallibility precedent as rest.WithPathConstraints/reqreply topic
// validation running at registration time, not per-call.
func SystemPromptFile(path string) SystemPromptFileOpt { return SystemPromptFileOpt{path: path} }

// UserMessageOpt is returned by [UserMessage]. Implements [CallOpt].
type UserMessageOpt[Req any] struct{ fn func(Req) (string, error) }

func (o UserMessageOpt[Req]) applyCall(cb *callBuilder) {
	cb.userMessage = o.fn
}

// UserMessage overrides how Req is rendered into the LLM's user-turn
// content. Default: JSON-encode the request codec's output verbatim
// (equivalent to format.JSON(reqCodec).Marshal), no extra wrapping text.
func UserMessage[Req any](fn func(Req) (string, error)) UserMessageOpt[Req] {
	return UserMessageOpt[Req]{fn: fn}
}

// IncludeRequestSchemaOpt is returned by [IncludeRequestSchema]. Implements [CallOpt].
type IncludeRequestSchemaOpt struct{}

func (o IncludeRequestSchemaOpt) applyCall(cb *callBuilder) { cb.includeReqSchema = true }

// IncludeRequestSchema appends the input codec's JSON Schema to the system
// prompt (as a fenced code block) — useful when the raw JSON alone is
// ambiguous. Default false (keeps prompts lean; the model already receives
// the concrete data, not just its shape).
func IncludeRequestSchema() IncludeRequestSchemaOpt { return IncludeRequestSchemaOpt{} }

// CallMeta holds documentation metadata — mirrors RouteMeta/ChannelMeta/
// ToolMeta's role in the other API families. Implements [CallOpt].
type CallMeta struct {
	// Description is a human-readable purpose, surfaced in render/openaitools
	// and any future prompt-catalog rendering.
	Description string
	Tags        []string
}

func (m CallMeta) applyCall(cb *callBuilder) { cb.meta = m }

// Call declares an LLM completion contract: a system prompt plus typed
// input/output codecs. Call is protocol-agnostic — it does not know about
// OpenAI, Azure, or any specific provider; [adapters/openai] (or a future
// provider-specific adapter) supplies the wire format.
//
// NewCall is infallible — it only captures the spec. Validation (including
// reading a [SystemPromptFile] path) runs at [Call.Register]/[Call.ClientHandle]
// time.
//
//	var Summarize = llm.NewCall[Article, Summary]("summarize", articleCodec, summaryCodec,
//	    llm.SystemPromptFile("prompts/summarize.md"),
//	    llm.CallMeta{Description: "Summarizes a news article in exactly three sentences."},
//	)
type Call[Req, Resp any] struct {
	name      string
	reqCodec  codex.Codec[Req]
	respCodec codex.Codec[Resp]
	opts      []CallOpt
}

// NewCall creates a [Call] spec from a name, codecs, and variadic opts.
// name is used for observability, error context, and spec generation
// ([render/openaitools], future prompt-catalog docs).
//
// NewCall is a free function (not a method) because Go requires type
// parameters on free functions, not on method receivers.
func NewCall[Req, Resp any](
	name string,
	reqCodec codex.Codec[Req],
	respCodec codex.Codec[Resp],
	opts ...CallOpt,
) Call[Req, Resp] {
	return Call[Req, Resp]{
		name:      name,
		reqCodec:  reqCodec,
		respCodec: respCodec,
		opts:      opts,
	}
}
