// Package mcp provides a transport-agnostic MCP server builder for go-codex.
//
// Define tools, resources, and prompts declaratively with codec-backed types;
// register them with a [Builder] to obtain typed handles. Pass those handles to
// an MCP adapter (e.g. adapters/mcpgo) to wire them to a running server.
//
// This package does not import any MCP SDK — it is framework-agnostic.
// The workflow is the same declare → register → handle pattern as [api/rest]
// and [api/events]:
//
//	// Layer 1: define codecs
//	inputCodec  := codex.Struct[CalcInput](...)
//	outputCodec := codex.Struct[CalcOutput](...)
//
//	// Layer 2: declare tools/resources/prompts as values
//	var calcTool = mcp.NewTool[CalcInput, CalcOutput]("calculate",
//	    inputCodec, outputCodec,
//	    mcp.ToolMeta{Description: "Perform arithmetic"},
//	)
//
//	// V=string serves as its own field container for this single-var
//	// template — codex.IdentityField supplies the identity get/set.
//	var itemResource = mcp.NewResource[string](
//	    "items://{id}", itemCodec,
//	    mcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
//	    mcp.URIParam(codex.IdentityField("id", uuidCodec)),
//	)
//
//	var summaryPrompt = mcp.NewPrompt("summarize",
//	    mcp.PromptMeta{Description: "Summarize content"},
//	    mcp.PromptArg{Name: "content", Required: true},
//	)
//
//	// Register with a builder
//	b := mcp.NewBuilder(mcp.Info{Name: "My Server", Version: "1.0.0"})
//	toolHandle, _     := calcTool.Register(b)
//	resHandle, _      := itemResource.Register(b)
//	promptHandle, _   := summaryPrompt.Register(b)
//
//	// Spec generation (analogous to OpenAPISpec / AsyncAPISpec)
//	spec, _ := b.MCPSpec()
//
//	// Adapter layer (adapters/mcpgo):
//	// mcpgo.RegisterTool(s, toolHandle, fn, opts)
//	// mcpgo.RegisterResource(s, resHandle, fn, opts)
//	// mcpgo.RegisterPrompt(s, promptHandle, fn, opts)
package mcp
