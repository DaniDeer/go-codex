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

// ── ToolLatestAdapter ─────────────────────────────────────────────────────────

// ToolLatestAdapter returns a [ports.ToolAdapter] backed by a reactive cache
// stream. When [ports.ToolPort.Bind] is called, the tool is registered with
// the MCP server via [RegisterToolLatest]. Each tool call from the LLM returns
// the most recently emitted value from src.
//
// Note: ToolLatestAdapter ignores the pipeline function set on [ports.ToolPort]
// via [ports.ToolPort.SetPipeline] — the response always comes from src.
// The [ports.ToolPort.SetPipeline] call is still required (ToolPort validates
// it before Bind), but the fn is not used by this adapter.
//
// Use with [ports.ToolPort.Bind]:
//
//	domain.OEEToolPort.Bind(ctx,
//	    mcpgo.ToolLatestAdapter(mcpServer, toolHandle, oeeStream, mcpgo.Options{}))
func ToolLatestAdapter[In, Out any](
	s *server.MCPServer,
	handle *apimcp.ToolHandle[In, Out],
	src gstream.Stream[Out],
	opts Options,
) ports.ToolAdapter[In, Out] {
	return &mcpToolLatestAdapter[In, Out]{s: s, handle: handle, src: src, opts: opts}
}

type mcpToolLatestAdapter[In, Out any] struct {
	s      *server.MCPServer
	handle *apimcp.ToolHandle[In, Out]
	src    gstream.Stream[Out]
	opts   Options
}

func (a *mcpToolLatestAdapter[In, Out]) AdapterName() string { return "mcpgo.ToolLatestAdapter" }

func (a *mcpToolLatestAdapter[In, Out]) Bind(
	ctx context.Context,
	_ func(context.Context, In) gstream.Stream[Out], // ignored — response comes from src
) error {
	RegisterToolLatest(a.s, a.handle, a.src, a.opts)
	return nil
}
