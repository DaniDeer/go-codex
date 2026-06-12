// Package mcpgo adapts [api/mcp] handles to [github.com/mark3labs/mcp-go] servers.
//
// Each [api/mcp.ToolHandle], [api/mcp.ResourceHandle], or [api/mcp.PromptHandle]
// becomes a [server.ToolHandlerFunc], [server.ResourceTemplateHandlerFunc], or
// [server.PromptHandlerFunc] via [ToolHandler], [ResourceHandler], or [PromptHandler].
// [RegisterTool], [RegisterResource], and [RegisterPrompt] wire the handles and
// handlers directly to an [server.MCPServer] in a single call.
//
// Typical usage:
//
//	b := mcp.NewBuilder(mcp.Info{Name: "My Server", Version: "1.0.0"})
//	toolHandle, _ := calcTool.Register(b)
//	resHandle, _  := itemResource.Register(b)
//	promHandle, _ := summaryPrompt.Register(b)
//
//	s := server.NewMCPServer(b.Info().Name, b.Info().Version)
//	mcpgo.RegisterTool(s, toolHandle, func(ctx context.Context, in CalcInput) (CalcOutput, error) {
//	    return svc.Calculate(ctx, in)
//	}, mcpgo.Options{Observer: obs})
//	mcpgo.RegisterResource(s, resHandle, func(ctx context.Context, uri string) (Item, error) {
//	    return svc.GetItem(ctx, uri)
//	}, mcpgo.Options{})
//	mcpgo.RegisterPrompt(s, promHandle, func(ctx context.Context, args map[string]string) ([]mcpgo.PromptMessage, error) {
//	    return []mcpgo.PromptMessage{{Role: "user", Content: "..."}}, nil
//	}, mcpgo.Options{})
//
//	if err := server.ServeStdio(s); err != nil {
//	    log.Fatal(err)
//	}
//
// Input validation errors are returned to the LLM client as tool errors
// (IsError: true in the result — the client sees the error text and can
// retry or report it). Output encode errors are returned as protocol-level
// errors (the server could not produce a valid response).
package mcpgo
