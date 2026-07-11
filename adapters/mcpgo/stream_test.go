package mcpgo_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── RegisterToolPipeline ──────────────────────────────────────────────────────

func TestRegisterToolPipeline_AddsTool(t *testing.T) {
	// RegisterToolPipeline is a convenience wrapper over ToolPipelineHandler
	// + s.AddTool. Verify it registers without panic and the tool is accessible.
	handle := buildHandle(addInputCodec, addOutputCodec)
	s := server.NewMCPServer("test", "1.0.0")
	mcpgo.RegisterToolPipeline(s, handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			return gstream.Single(ctx, addOutput{Sum: in.A + in.B})
		}, mcpgo.Options{})
	// If registration succeeded, we can call the tool via a request.
	// (No panic = registration worked correctly.)
}

func TestRegisterToolLatest_AddsTool(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	valCh := make(chan addOutput)
	errCh := make(chan error)
	close(valCh)
	close(errCh)
	src := gstream.Stream[addOutput]{Values: valCh, Errors: errCh}

	s := server.NewMCPServer("test", "1.0.0")
	mcpgo.RegisterToolLatest(s, handle, src, mcpgo.Options{})
	// No panic = registration worked correctly.
}

// ── ToolPipelineHandler ───────────────────────────────────────────────────────

func TestToolPipelineHandler_ReturnsFirstValue(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolPipelineHandler(handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			return gstream.Single(ctx, addOutput{Sum: in.A + in.B})
		}, mcpgo.Options{})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 3.0, "b": 4.0},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if result.IsError {
		t.Fatalf("want success, got IsError=true: %+v", result.Content)
	}
	var foundSum bool
	for _, c := range result.Content {
		if ct, ok := c.(mcp.TextContent); ok && strings.Contains(ct.Text, "7") {
			foundSum = true
		}
	}
	if !foundSum {
		t.Errorf("expected Sum=7 in result, got: %+v", result.Content)
	}
}

func TestToolPipelineHandler_PipelineErrorIsToolError(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolPipelineHandler(handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			errCh := make(chan error, 1)
			valCh := make(chan addOutput)
			errCh <- fmt.Errorf("compute failed")
			close(errCh)
			close(valCh)
			return gstream.Stream[addOutput]{Values: valCh, Errors: errCh}
		}, mcpgo.Options{})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 1.0, "b": 2.0},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("want IsError=true for pipeline error, got IsError=false")
	}
}

func TestToolPipelineHandler_NoValueIsToolError(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolPipelineHandler(handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			errCh := make(chan error)
			valCh := make(chan addOutput)
			close(errCh)
			close(valCh)
			return gstream.Stream[addOutput]{Values: valCh, Errors: errCh}
		}, mcpgo.Options{})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 1.0, "b": 2.0},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("want IsError=true when no value produced, got IsError=false")
	}
}

func TestToolPipelineHandler_TapObservationFires(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	var tapFired bool
	_, handler := mcpgo.ToolPipelineHandler(handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			s := gstream.Single(ctx, in)
			s = gstream.Tap(ctx, s, func(v addInput) { tapFired = true })
			return gstream.FlatMapSlice(ctx, s, func(v addInput) []addOutput {
				return []addOutput{{Sum: v.A + v.B}}
			})
		}, mcpgo.Options{})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 2.0, "b": 3.0},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("unexpected error: err=%v isError=%v", err, result != nil && result.IsError)
	}
	if !tapFired {
		t.Error("Tap should have fired during pipeline execution")
	}
}

func TestToolPipelineHandler_InputValidationStillRuns(t *testing.T) {
	handle := buildHandle(constrainedInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolPipelineHandler(handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			return gstream.Single(ctx, addOutput{Sum: in.A + in.B})
		}, mcpgo.Options{})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": -1.0, "b": 2.0}, // fails MinFloat(0)
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Error("want IsError=true for invalid input (a < 0), got IsError=false")
	}
}

func TestToolPipelineHandler_ObserverReceivesRequest(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	obs := &recordingObserver{}
	_, handler := mcpgo.ToolPipelineHandler(handle,
		func(ctx context.Context, in addInput) gstream.Stream[addOutput] {
			return gstream.Single(ctx, addOutput{Sum: in.A + in.B})
		}, mcpgo.Options{Observer: obs})

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 1.0, "b": 2.0},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil || result.IsError {
		t.Fatalf("unexpected error: err=%v isError=%v", err, result != nil && result.IsError)
	}
	if len(obs.calls) != 1 || obs.calls[0].statusCode != 200 {
		t.Errorf("want 1 observer call with status 200, got %+v", obs.calls)
	}
}

// ── ToolLatestHandler ─────────────────────────────────────────────────────────

func callToolLatest(
	t *testing.T,
	src gstream.Stream[addOutput],
	opts mcpgo.Options,
	args any,
) (*mcp.CallToolResult, error) {
	t.Helper()
	handle := buildHandle(addInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolLatestHandler(handle, src, opts)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: args,
		},
	}
	return handler(context.Background(), req)
}

func TestToolLatestHandler_ReturnsLatestValue(t *testing.T) {
	valCh := make(chan addOutput, 1)
	errCh := make(chan error)
	close(errCh)
	src := gstream.Stream[addOutput]{Values: valCh, Errors: errCh}

	// Create handler first (starts background goroutine), then send value.
	handle := buildHandle(addInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolLatestHandler(handle, src, mcpgo.Options{})
	valCh <- addOutput{Sum: 42}
	close(valCh)

	// Give background goroutine time to read and store the value.
	time.Sleep(30 * time.Millisecond)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 1.0, "b": 2.0},
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if result.IsError {
		t.Fatalf("want success result, got IsError=true")
	}
	// Result should contain the serialised addOutput{Sum:42}
	var foundSum bool
	for _, c := range result.Content {
		if ct, ok := c.(mcp.TextContent); ok && strings.Contains(ct.Text, "42") {
			foundSum = true
		}
	}
	if !foundSum {
		t.Errorf("expected Sum=42 in result content, got: %+v", result.Content)
	}
}

func TestToolLatestHandler_NoValueIsErrorResult(t *testing.T) {
	// Empty stream — no values ever.
	valCh := make(chan addOutput)
	errCh := make(chan error)
	close(valCh)
	close(errCh)
	src := gstream.Stream[addOutput]{Values: valCh, Errors: errCh}

	result, err := callToolLatest(t, src,
		mcpgo.Options{},
		map[string]any{"a": 1.0, "b": 2.0},
	)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("want IsError=true when no value computed yet, got IsError=false")
	}
	// LLM should see "no value computed yet"
	var foundMsg bool
	for _, c := range result.Content {
		if ct, ok := c.(mcp.TextContent); ok && strings.Contains(ct.Text, "no value") {
			foundMsg = true
		}
	}
	if !foundMsg {
		t.Errorf("expected 'no value computed yet' in error content, got: %+v", result.Content)
	}
}

func TestToolLatestHandler_InputValidationStillRuns(t *testing.T) {
	// The input codec (constrainedInputCodec requires a >= 0) should still validate.
	valCh := make(chan addOutput, 1)
	errCh := make(chan error)
	close(errCh)
	src := gstream.Stream[addOutput]{Values: valCh, Errors: errCh}

	handle := buildHandle(constrainedInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolLatestHandler(handle, src, mcpgo.Options{})
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": -1.0, "b": 2.0}, // fails MinFloat(0)
		},
	}
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected protocol error: %v", err)
	}
	// Invalid input → IsError=true (input validation, not a value error)
	if !result.IsError {
		t.Error("want IsError=true for invalid input (a < 0), got IsError=false")
	}
}

func TestToolLatestHandler_ObserverReceivesRequest(t *testing.T) {
	valCh := make(chan addOutput, 1)
	errCh := make(chan error)
	close(errCh)
	src := gstream.Stream[addOutput]{Values: valCh, Errors: errCh}

	obs := &recordingObserver{}
	handle := buildHandle(addInputCodec, addOutputCodec)
	_, handler := mcpgo.ToolLatestHandler(handle, src, mcpgo.Options{Observer: obs})
	valCh <- addOutput{Sum: 5}
	close(valCh)
	time.Sleep(30 * time.Millisecond)

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: map[string]any{"a": 1.0, "b": 2.0},
		},
	}
	result, err := handler(context.Background(), req)

	if err != nil || result.IsError {
		t.Fatalf("unexpected error: err=%v isError=%v", err, result != nil && result.IsError)
	}
	if len(obs.calls) != 1 {
		t.Errorf("want 1 observer call, got %d", len(obs.calls))
	}
	if obs.calls[0].statusCode != 200 {
		t.Errorf("want status 200, got %d", obs.calls[0].statusCode)
	}
}
