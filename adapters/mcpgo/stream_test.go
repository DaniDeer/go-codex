package mcpgo_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	gstream "github.com/DaniDeer/go-codex/stream"
)

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
