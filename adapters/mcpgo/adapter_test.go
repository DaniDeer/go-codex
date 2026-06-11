package mcpgo_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	mcpgo "github.com/DaniDeer/go-codex/adapters/mcpgo"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test types and codecs ---

type addInput struct {
	A float64
	B float64
}
type addOutput struct {
	Sum float64
}

var addInputCodec = codex.Struct[addInput](
	codex.RequiredField("a", codex.Float64(),
		func(v addInput) float64 { return v.A },
		func(v *addInput, x float64) { v.A = x },
	),
	codex.RequiredField("b", codex.Float64(),
		func(v addInput) float64 { return v.B },
		func(v *addInput, x float64) { v.B = x },
	),
)

var addOutputCodec = codex.Struct[addOutput](
	codex.RequiredField("sum", codex.Float64(),
		func(v addOutput) float64 { return v.Sum },
		func(v *addOutput, x float64) { v.Sum = x },
	),
)

var constrainedInputCodec = codex.Struct[addInput](
	codex.RequiredField("a", codex.Float64().Refine(validate.MinFloat(0.0)),
		func(v addInput) float64 { return v.A },
		func(v *addInput, x float64) { v.A = x },
	),
	codex.RequiredField("b", codex.Float64(),
		func(v addInput) float64 { return v.B },
		func(v *addInput, x float64) { v.B = x },
	),
)

func buildHandle(inputCodec codex.Codec[addInput], outputCodec codex.Codec[addOutput]) *apimcp.ToolHandle[addInput, addOutput] {
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	tool := apimcp.NewTool[addInput, addOutput]("add", inputCodec, outputCodec,
		apimcp.ToolMeta{Description: "Add two numbers"},
	)
	h, err := tool.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func callTool(t *testing.T, handle *apimcp.ToolHandle[addInput, addOutput], fn mcpgo.HandlerFunc[addInput, addOutput], opts mcpgo.Options, args any) (*mcp.CallToolResult, error) {
	t.Helper()
	_, handler := mcpgo.ToolHandler(handle, fn, opts)
	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      handle.Name,
			Arguments: args,
		},
	}
	return handler(context.Background(), req)
}

// --- observer for testing ---

type recordingObserver struct {
	stats.NoopObserver
	calls []observerCall
}

type observerCall struct {
	method     string
	path       string
	statusCode int
}

func (o *recordingObserver) RecordRequest(method, path string, statusCode int, _ time.Duration) {
	o.calls = append(o.calls, observerCall{method: method, path: path, statusCode: statusCode})
}

// ---------------------------------------------------------------------------
// Tool handler tests
// ---------------------------------------------------------------------------

func TestToolHandler_success(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	fn := func(_ context.Context, in addInput) (addOutput, error) {
		return addOutput{Sum: in.A + in.B}, nil
	}

	result, err := callTool(t, handle, fn, mcpgo.Options{}, map[string]any{"a": 3.0, "b": 4.0})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}
}

func TestToolHandler_inputDecodeError_returnsIsError(t *testing.T) {
	handle := buildHandle(constrainedInputCodec, addOutputCodec)
	fn := func(_ context.Context, in addInput) (addOutput, error) {
		return addOutput{Sum: in.A + in.B}, nil
	}

	// a = -5 violates MinFloat(0.0)
	result, err := callTool(t, handle, fn, mcpgo.Options{}, map[string]any{"a": -5.0, "b": 4.0})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for input constraint violation")
	}
}

func TestToolHandler_handlerError_returnsIsError(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	fn := func(_ context.Context, _ addInput) (addOutput, error) {
		return addOutput{}, errors.New("division by zero")
	}

	result, err := callTool(t, handle, fn, mcpgo.Options{}, map[string]any{"a": 1.0, "b": 2.0})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for handler error")
	}
}

func TestToolHandler_outputEncodeError_returnsProtocolError(t *testing.T) {
	constrainedOutputCodec := codex.Struct[addOutput](
		codex.RequiredField("sum", codex.Float64().Refine(validate.MinFloat(0.0)),
			func(v addOutput) float64 { return v.Sum },
			func(v *addOutput, x float64) { v.Sum = x },
		),
	)
	handle := buildHandle(addInputCodec, constrainedOutputCodec)
	fn := func(_ context.Context, in addInput) (addOutput, error) {
		// Returns negative output which violates the output codec constraint.
		return addOutput{Sum: -1.0}, nil
	}

	result, err := callTool(t, handle, fn, mcpgo.Options{}, map[string]any{"a": 1.0, "b": 2.0})
	// Output encode error should return a protocol error (non-nil err), not a tool error.
	if err == nil {
		t.Fatalf("expected protocol error for output contract violation, got result: %v", result)
	}
}

func TestToolHandler_observerRecordsSuccess(t *testing.T) {
	obs := &recordingObserver{}
	handle := buildHandle(addInputCodec, addOutputCodec)
	fn := func(_ context.Context, in addInput) (addOutput, error) {
		return addOutput{Sum: in.A + in.B}, nil
	}

	_, _ = callTool(t, handle, fn, mcpgo.Options{Observer: obs}, map[string]any{"a": 1.0, "b": 2.0})

	if len(obs.calls) != 1 {
		t.Fatalf("expected 1 RecordRequest call, got %d", len(obs.calls))
	}
	if obs.calls[0].method != "tool" {
		t.Errorf("method: got %q, want %q", obs.calls[0].method, "tool")
	}
	if obs.calls[0].path != "add" {
		t.Errorf("path: got %q, want %q", obs.calls[0].path, "add")
	}
	if obs.calls[0].statusCode != 200 {
		t.Errorf("statusCode: got %d, want 200", obs.calls[0].statusCode)
	}
}

func TestToolHandler_observerRecords400ForInputError(t *testing.T) {
	obs := &recordingObserver{}
	handle := buildHandle(constrainedInputCodec, addOutputCodec)
	fn := func(_ context.Context, in addInput) (addOutput, error) {
		return addOutput{Sum: in.A + in.B}, nil
	}

	_, _ = callTool(t, handle, fn, mcpgo.Options{Observer: obs}, map[string]any{"a": -1.0, "b": 2.0})

	if len(obs.calls) == 0 {
		t.Fatal("expected at least one RecordRequest call")
	}
	last := obs.calls[len(obs.calls)-1]
	if last.statusCode != 400 {
		t.Errorf("statusCode: got %d, want 400", last.statusCode)
	}
}

// ---------------------------------------------------------------------------
// ToolHandler creates mcp.Tool with correct metadata
// ---------------------------------------------------------------------------

func TestToolHandler_toolDescriptor_hasName(t *testing.T) {
	handle := buildHandle(addInputCodec, addOutputCodec)
	tool, _ := mcpgo.ToolHandler(handle, func(_ context.Context, in addInput) (addOutput, error) {
		return addOutput{}, nil
	}, mcpgo.Options{})

	if tool.Name != "add" {
		t.Errorf("tool.Name: got %q, want %q", tool.Name, "add")
	}
	if tool.Description != "Add two numbers" {
		t.Errorf("tool.Description: got %q", tool.Description)
	}
}
