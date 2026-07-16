package mcpgo

import (
	"context"

	"github.com/mark3labs/mcp-go/server"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── ToolPipelineAdapter ───────────────────────────────────────────────────────

// ToolPipelineAdapter returns a [ports.ToolAdapter] that registers the pipeline
// function as an MCP tool. When [ports.ToolPort.Bind] is called, the pipeline
// function is registered with the MCP server via [RegisterToolPipeline].
//
// Each tool call from the LLM starts a fresh pipeline run and returns the
// first emitted value. Use with [ports.ToolPort.Bind]:
//
//	domain.OEEToolPort.Bind(ctx,
//	    mcpgo.ToolPipelineAdapter(mcpServer, toolHandle, mcpgo.Options{Observer: obs}))
func ToolPipelineAdapter[In, Out any](
	s *server.MCPServer,
	handle *apimcp.ToolHandle[In, Out],
	opts Options,
) ports.ToolAdapter[In, Out] {
	return &mcpToolPipelineAdapter[In, Out]{s: s, handle: handle, opts: opts}
}

type mcpToolPipelineAdapter[In, Out any] struct {
	s      *server.MCPServer
	handle *apimcp.ToolHandle[In, Out]
	opts   Options
}

func (a *mcpToolPipelineAdapter[In, Out]) AdapterName() string { return "mcpgo.ToolPipelineAdapter" }

func (a *mcpToolPipelineAdapter[In, Out]) Bind(
	_ context.Context,
	fn func(context.Context, In) gstream.Stream[Out],
) error {
	RegisterToolPipeline(a.s, a.handle, func(ctx context.Context, in In) gstream.Stream[Out] {
		return fn(ctx, in)
	}, a.opts)
	return nil
}

// ── LatestAdapter ─────────────────────────────────────────────────────────────

// LatestAdapter returns a [ports.LatestAdapter] that serves a
// [ports.LatestPort]'s cached value as an MCP tool — the port-based successor
// to the removed ToolLatestAdapter (which ignored the ToolPort pipeline
// function and owned its own cache cell; the port owns it here, and no
// pipeline argument exists to ignore). Use with [ports.LatestPort.Bind]:
//
//	handle, _ := ports.MCPHandle[struct{}, OEE](domain.Latest)
//	must(domain.Latest.Bind(ctx, mcpgo.LatestAdapter(srv, handle, mcpgo.Options{})))
//	go domain.Latest.Feed(ctx, oeeStream)
//
// Before the first value arrives, tool calls return an MCP error result
// ("no value computed yet") — same semantics as [ToolLatestHandler].
func LatestAdapter[Out any](
	s *server.MCPServer,
	handle *apimcp.ToolHandle[struct{}, Out],
	opts Options,
) ports.LatestAdapter[Out] {
	return &mcpLatestAdapter[Out]{s: s, handle: handle, opts: opts}
}

type mcpLatestAdapter[Out any] struct {
	s      *server.MCPServer
	handle *apimcp.ToolHandle[struct{}, Out]
	opts   Options
}

func (a *mcpLatestAdapter[Out]) AdapterName() string { return "mcpgo.LatestAdapter" }

func (a *mcpLatestAdapter[Out]) Serve(_ context.Context, latest func() (Out, bool)) error {
	tool, handler := ToolHandler(a.handle, func(_ context.Context, _ struct{}) (Out, error) {
		v, ok := latest()
		if !ok {
			var zero Out
			return zero, errNoLatestValue
		}
		return v, nil
	}, a.opts)
	a.s.AddTool(tool, handler)
	return nil // registration-style Serve: returns immediately
}
