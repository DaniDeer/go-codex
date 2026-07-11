package mcpgo

import (
	"context"
	"errors"
	"sync/atomic"

	mcp "github.com/mark3labs/mcp-go/mcp"
	server "github.com/mark3labs/mcp-go/server"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	gstream "github.com/DaniDeer/go-codex/stream"
)

var errNoLatestValue = errors.New("no value computed yet")

// ToolLatestHandler creates an MCP tool handler that replies to every LLM tool
// call with the most recently emitted value from src.
//
// A background goroutine reads src.Values and atomically stores each new value.
// When the tool is called but no value has been produced yet, the handler returns
// [mcp.NewToolResultError]("no value computed yet") with IsError: true — visible
// to the LLM but not a Go error. This is consistent with [ToolHandler] input
// error behaviour.
//
// The In (tool arguments) are decoded and validated by handle.Decode; the result
// is always the latest Out from the stream, not a function of In.
//
// Use ToolLatestHandler for "get current OEE", "get latest sensor reading", or any
// "current state" MCP tool backed by a continuous stream computation:
//
//	oeeStream := gstream.Apply(ctx, sensorStream, oeeCalcFn, gstream.ApplyOptions{Observer: obs})
//	tool, handler := mcpgo.ToolLatestHandler(getOEEHandle, oeeStream, mcpgo.Options{Observer: obs})
//	s.AddTool(tool, handler)
func ToolLatestHandler[In, Out any](
	handle *apimcp.ToolHandle[In, Out],
	src gstream.Stream[Out],
	opts Options,
) (mcp.Tool, server.ToolHandlerFunc) {
	var latest atomic.Pointer[Out]
	go func() {
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case v, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				v2 := v
				latest.Store(&v2)
			case _, ok := <-errCh:
				if !ok {
					errCh = nil
				}
				// errors from src are silently dropped
			}
		}
	}()

	return ToolHandler[In, Out](handle, func(ctx context.Context, _ In) (Out, error) {
		ptr := latest.Load()
		var zero Out
		if ptr == nil {
			// Return as tool error (not Go error) per MCP protocol contract.
			// ToolHandler converts Go errors to mcp.NewToolResultError(...) with IsError:true.
			return zero, apimcp.ToolInputError{Name: handle.Name, Err: errNoLatestValue}
		}
		return *ptr, nil
	}, opts)
}

// RegisterToolLatest wires [ToolLatestHandler] onto an MCPServer. Mirrors [RegisterTool].
func RegisterToolLatest[In, Out any](
	s *server.MCPServer,
	handle *apimcp.ToolHandle[In, Out],
	src gstream.Stream[Out],
	opts Options,
) {
	tool, handler := ToolLatestHandler(handle, src, opts)
	s.AddTool(tool, handler)
}
