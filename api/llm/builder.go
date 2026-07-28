package llm

import "encoding/json"

// Info identifies the LLM call catalog for spec/documentation purposes —
// the llm-family analogue of [rest.Info]/[events.Info]/[reqreply.Info]/[mcp.Info].
type Info struct {
	Name    string
	Version string
}

// Builder accumulates [CallSpec] entries as [Call] values are registered
// against it — same role as [rest.Builder]/[events.Builder]/[reqreply.Builder]/
// [mcp.Builder], minus OpenAPI/AsyncAPI-specific spec assembly: there is no
// path/topic template to validate, so LLMSpec is a flat catalog with no
// document-rendering step of its own. [render/openaitools] consumes LLMSpec
// directly.
//
// Create a Builder with [NewBuilder], register calls via [Call.Register], and
// call [Builder.LLMSpec] to produce the catalog.
type Builder struct {
	info      Info
	calls     []CallSpec
	callNames map[string]struct{}
}

// NewBuilder returns a Builder initialised with the given Info.
func NewBuilder(info Info) *Builder {
	return &Builder{
		info:      info,
		callNames: make(map[string]struct{}),
	}
}

// LLMSpec returns the accumulated catalog of every [Call] registered with b.
func (b *Builder) LLMSpec() (*LLMSpec, error) {
	return &LLMSpec{
		Name:    b.info.Name,
		Version: b.info.Version,
		Calls:   b.calls,
	}, nil
}

// LLMSpec is the static catalog of all declared [Call] contracts — the
// llm-family analogue of [mcp.MCPSpec], REST's OpenAPI document, and events'
// AsyncAPI document. Feeds [render/openaitools] (and any future
// prompt-catalog renderer).
type LLMSpec struct {
	Name    string     `json:"name"`
	Version string     `json:"version"`
	Calls   []CallSpec `json:"calls,omitempty"`
}

// CallSpec is the spec entry for one declared [Call] in [LLMSpec].
type CallSpec struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	// SystemPrompt is included verbatim — useful for a human-readable prompt
	// catalog. NOTE: this means an LLMSpec-derived renderer handed to a
	// DIFFERENT LLM (e.g. render/openaitools.FromLLMSpec's agent-calls-agent
	// use) could leak prompt text into that other agent's context. Revisit
	// if this proves undesirable in practice — flagged as an open design
	// decision in docs/roadmap/llm-integration.md.
	SystemPrompt string `json:"systemPrompt"`

	RequestSchema  json.RawMessage `json:"requestSchema,omitempty"`
	ResponseSchema json.RawMessage `json:"responseSchema,omitempty"`
}
