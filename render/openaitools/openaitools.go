package openaitools

import (
	"encoding/json"

	"github.com/DaniDeer/go-codex/api/llm"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
)

// Tool is one entry in the OpenAI "tools" array.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

// toolFunction and toolEntry mirror the exact OpenAI wire shape:
//
//	[{"type":"function","function":{"name":...,"description":...,"parameters":...}}]
type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type toolEntry struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

// Render produces the exact OpenAI tools-array JSON shape for tools.
func Render(tools []Tool) (json.RawMessage, error) {
	entries := make([]toolEntry, len(tools))
	for i, t := range tools {
		entries[i] = toolEntry{
			Type:     "function",
			Function: toolFunction(t),
		}
	}
	return json.Marshal(entries)
}

// FromMCPSpec converts every tool in spec into [Tool] entries, using each
// [apimcp.ToolSpec]'s InputSchema as Parameters. Lets an application expose
// its EXISTING MCP tool declarations to a raw OpenAI-style tool-calling loop
// with zero additional declaration.
func FromMCPSpec(spec *apimcp.MCPSpec) []Tool {
	tools := make([]Tool, len(spec.Tools))
	for i, ts := range spec.Tools {
		tools[i] = Tool{
			Name:        ts.Name,
			Description: ts.Description,
			Parameters:  ts.InputSchema,
		}
	}
	return tools
}

// FromLLMSpec converts every declared [llm.Call] in spec into [Tool] entries,
// using each [llm.CallSpec]'s RequestSchema as Parameters — lets one
// LLM-backed Call be exposed as a callable "tool" to a DIFFERENT
// orchestrating LLM (agent-calls-agent via the tool-calling convention,
// without needing a full Agent2Agent protocol implementation).
func FromLLMSpec(spec *llm.LLMSpec) []Tool {
	tools := make([]Tool, len(spec.Calls))
	for i, cs := range spec.Calls {
		tools[i] = Tool{
			Name:        cs.Name,
			Description: cs.Description,
			Parameters:  cs.RequestSchema,
		}
	}
	return tools
}
