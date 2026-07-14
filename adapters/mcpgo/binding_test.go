package mcpgo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func newAddHandle() *apimcp.ToolHandle[addInput, addOutput] {
	return buildHandle(addInputCodec, addOutputCodec)
}

func newMCPServer() *server.MCPServer {
	return server.NewMCPServer("test", "1.0.0")
}

// ── ToolPipelineAdapter ───────────────────────────────────────────────────────

func TestToolPipelineAdapter_RegistersAndCallsTool(t *testing.T) {
	ctx := context.Background()
	s := newMCPServer()
	handle := newAddHandle()

	toolPort := ports.NewToolPort[addInput, addOutput]("add", addInputCodec, addOutputCodec, ports.PortOptions{})
	toolPort.SetPipeline(func(_ context.Context, in addInput) gstream.Stream[addOutput] {
		return gstream.Single(context.Background(), addOutput{Sum: in.A + in.B})
	})

	if err := toolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(s, handle, mcpgo.Options{})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// Call the tool via the handler directly (same approach as adapter_test.go).
	_, pipelineHandler := mcpgo.ToolPipelineHandler(handle, func(_ context.Context, in addInput) gstream.Stream[addOutput] {
		return gstream.Single(context.Background(), addOutput{Sum: in.A + in.B})
	}, mcpgo.Options{})
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      handle.Name,
		Arguments: map[string]any{"a": 3.0, "b": 4.0},
	}}
	result, err := pipelineHandler(ctx, req)
	if err != nil {
		t.Fatalf("tool handler: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %v", result.Content)
	}
	_ = s // server registered but we verify handler directly
}

func TestToolPipelineAdapter_NoPipelineError(t *testing.T) {
	ctx := context.Background()
	s := newMCPServer()
	handle := newAddHandle()

	toolPort := ports.NewToolPort[addInput, addOutput]("add", addInputCodec, addOutputCodec, ports.PortOptions{})
	// No SetPipeline call

	err := toolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(s, handle, mcpgo.Options{}))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T", err)
	}
	var npe ports.PortNoPipelineError
	if !errors.As(err, &npe) {
		t.Errorf("want PortNoPipelineError wrapped inside, got %T", err)
	}
}

func TestToolPipelineAdapter_MultipleBind(t *testing.T) {
	ctx := context.Background()
	s1 := newMCPServer()
	s2 := newMCPServer()
	handle := newAddHandle()

	toolPort := ports.NewToolPort[addInput, addOutput]("add", addInputCodec, addOutputCodec, ports.PortOptions{})
	toolPort.SetPipeline(func(_ context.Context, in addInput) gstream.Stream[addOutput] {
		return gstream.Single(context.Background(), addOutput{Sum: in.A + in.B})
	})

	// Bind same port to two different MCP servers.
	if err := toolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(s1, handle, mcpgo.Options{})); err != nil {
		t.Fatalf("first Bind: %v", err)
	}
	if err := toolPort.Bind(ctx, mcpgo.ToolPipelineAdapter(s2, handle, mcpgo.Options{})); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	// Both servers registered — verified by Bind succeeding without error.
}

// ── ToolLatestAdapter ─────────────────────────────────────────────────────────

func TestToolLatestAdapter_RegistersTool(t *testing.T) {
	ctx := context.Background()
	s := newMCPServer()
	handle := newAddHandle()

	// Source stream with one value.
	valCh := make(chan addOutput, 1)
	valCh <- addOutput{Sum: 99}
	close(valCh)
	src := gstream.From(ctx, valCh)

	toolPort := ports.NewToolPort[addInput, addOutput]("add", addInputCodec, addOutputCodec, ports.PortOptions{})
	toolPort.SetPipeline(func(_ context.Context, _ addInput) gstream.Stream[addOutput] {
		return gstream.Single(context.Background(), addOutput{Sum: 0}) // not used by ToolLatestAdapter
	})

	if err := toolPort.Bind(ctx, mcpgo.ToolLatestAdapter(s, handle, src, mcpgo.Options{})); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	// Registration succeeded — tool is on the server.
	// (Value availability is timing-dependent; we don't poll in the test.)
}
