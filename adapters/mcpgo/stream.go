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

// ── ToolLatestHandler ─────────────────────────────────────────────────────────

// errNoLatestValue is the sentinel returned by ToolLatestHandler when the
// background stream has not yet produced a value. ToolHandler converts any
// Go error from fn to mcp.NewToolResultError(err.Error()) — the LLM sees
// "no value computed yet" as a tool error result with IsError: true.
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
			// Return a plain error — ToolHandler converts any fn error to
			// mcp.NewToolResultError(err.Error()) with IsError:true (status 500).
			// The LLM sees "no value computed yet" without any misleading prefix.
			return zero, errNoLatestValue
		}
		return *ptr, nil
	}, opts)
}

// ── ToolPipelineHandler ───────────────────────────────────────────────────────

// errNoResult is returned by ToolPipelineHandler when the pipeline emits no value.
var errNoResult = errors.New("tool pipeline produced no result")

// ToolPipelineHandlerFunc is a handler function that implements an MCP tool's
// logic as a [gstream.Stream]. It must emit exactly one value (the tool
// response). Use [gstream.Single] to wrap the decoded In as the pipeline source.
//
// This is the MCP equivalent of [nethttp.PipelineHandlerFunc] — the same
// fn type works for both HTTP ([nethttp.PipelineHandler]) and MCP
// ([ToolPipelineHandler]).
//
// Error handling:
//   - If Stream.Errors fires, the first error becomes an IsError=true tool result.
//   - If no value is produced, "tool pipeline produced no result" is returned.
//   - If the pipeline emits more than one value, only the first is used.
type ToolPipelineHandlerFunc[In, Out any] func(ctx context.Context, in In) gstream.Stream[Out]

// ToolPipelineHandler wraps a [ToolPipelineHandlerFunc] into a
// (mcp.Tool, server.ToolHandlerFunc) pair. All input codec validation,
// security enforcement, and observer calls follow the same path as
// [ToolHandler] — ToolPipelineHandler is a thin wrapper that adapts the
// function signature and collects the result via [gstream.Collect].
//
// Use ToolPipelineHandler when the tool handler body benefits from:
//   - [gstream.Tap] for declarative intermediate observation (audit log, metrics)
//   - [gstream.Apply] for multi-step forge function composition
//   - [gstream.MapErr] for per-step typed error recovery
//
// For simple one-step handlers, use plain [ToolHandler].
//
// # Contrast with ToolLatestHandler
//
// [ToolLatestHandler] is a reactive CACHE: a background stream runs
// continuously and every tool call returns the most recently computed value.
//
// ToolPipelineHandler is a reactive TRIGGER: each tool call starts a fresh
// pipeline run. The pipeline runs per-call and the response is the first
// value it produces.
//
// # Usage
//
//	mcpgo.RegisterToolPipeline(s, getOEEHandle,
//	    func(ctx context.Context, in OEEQuery) stream.Stream[OEEResult] {
//	        s  := stream.Single(ctx, in)
//	        s   = stream.Apply(ctx, s, validateFn, stream.ApplyOptions{Observer: obs})
//	        s   = stream.Tap(ctx, s, func(v ValidatedQuery) { auditLog.Write(v) })
//	        return stream.Apply(ctx, s, oeeCalcFn, stream.ApplyOptions{Observer: obs})
//	    }, mcpgo.Options{Observer: obs})
func ToolPipelineHandler[In, Out any](
	handle *apimcp.ToolHandle[In, Out],
	fn ToolPipelineHandlerFunc[In, Out],
	opts Options,
) (mcp.Tool, server.ToolHandlerFunc) {
	return ToolHandler[In, Out](handle, func(ctx context.Context, in In) (Out, error) {
		pipeline := fn(ctx, in)
		vals, errs := gstream.Collect(ctx, pipeline)
		var zero Out
		// Errors take precedence — consistent with PipelineHandler behaviour.
		if len(errs) > 0 {
			return zero, errs[0]
		}
		if len(vals) == 0 {
			return zero, errNoResult
		}
		return vals[0], nil // multiple values: only first used; extras silently discarded
	}, opts)
}

// RegisterToolPipeline wires [ToolPipelineHandler] onto an MCPServer.
// Mirrors [RegisterTool] and [RegisterToolLatest].
func RegisterToolPipeline[In, Out any](
	s *server.MCPServer,
	handle *apimcp.ToolHandle[In, Out],
	fn ToolPipelineHandlerFunc[In, Out],
	opts Options,
) {
	tool, handler := ToolPipelineHandler(handle, fn, opts)
	s.AddTool(tool, handler)
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
