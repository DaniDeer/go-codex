// Package openaitools renders existing go-codex tool/call declarations into
// the OpenAI "tools" array JSON shape used by Chat Completions/Responses API
// function calling (and by extension, most OpenAI-compatible providers and
// client frameworks such as LangChain that accept the same convention).
//
// It is a pure renderer, the same category as [render/openapi]/
// [render/asyncapi]/[render/jsonschema]/[render/pipeline] — no new
// declaration surface. [FromMCPSpec] and [FromLLMSpec] convert an EXISTING
// [mcp.MCPSpec]/[llm.LLMSpec] catalog (already accumulated by registering
// tools/calls against their respective Builders) into the OpenAI shape with
// zero additional declaration.
//
// # Usage
//
//	mcpSpec, _ := mcpBuilder.MCPSpec()
//	tools := openaitools.FromMCPSpec(mcpSpec)
//	toolsJSON, err := openaitools.Render(tools)
//	// toolsJSON is ready to embed directly in an OpenAI-style "tools" request field.
package openaitools
