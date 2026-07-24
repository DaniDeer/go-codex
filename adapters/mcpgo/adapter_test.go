package mcpgo_test

import (
	"context"
	"encoding/json"
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

// ── ErrorPattern wiring (Phase 2) ─────────────────────────────────────────────

type addConflictErr struct{ msg string }

func (e addConflictErr) Error() string { return "conflict: " + e.msg }

type addErrPayload struct {
	Code    string
	Message string
}

func (e addErrPayload) Error() string { return "error " + e.Code }

var addErrPayloadCodec = codex.Struct[addErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e addErrPayload) string { return e.Code },
		func(e *addErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e addErrPayload) string { return e.Message },
		func(e *addErrPayload, v string) { e.Message = v },
	),
)

func buildErrorPatternHandle(t *testing.T) *apimcp.ToolHandle[addInput, addOutput] {
	t.Helper()
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	tool := apimcp.NewTool[addInput, addOutput]("add-ep", addInputCodec, addOutputCodec,
		apimcp.ToolMeta{Description: "Add two numbers"},
		apimcp.ErrorPattern[addConflictErr, addErrPayload](addErrPayloadCodec,
			func(e addConflictErr) (addErrPayload, error) {
				return addErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	h, err := tool.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return h
}

func TestToolHandler_ErrorPatternMatch_ReturnsStructuredResult(t *testing.T) {
	handle := buildErrorPatternHandle(t)
	fn := func(_ context.Context, _ addInput) (addOutput, error) {
		return addOutput{}, addConflictErr{msg: "duplicate"}
	}

	result, err := callTool(t, handle, fn, mcpgo.Options{}, map[string]any{"a": 1.0, "b": 2.0})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for matched error pattern")
	}
	structured, ok := result.StructuredContent.(json.RawMessage)
	if !ok {
		t.Fatalf("want StructuredContent to be json.RawMessage, got %T", result.StructuredContent)
	}
	var got addErrPayload
	if err := json.Unmarshal(structured, &got); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if got.Code != "conflict" || got.Message != "duplicate" {
		t.Errorf("unexpected structured payload: %+v", got)
	}
}

func TestToolHandler_ErrorPatternNoMatch_FallsBackToPlainText(t *testing.T) {
	handle := buildErrorPatternHandle(t)
	unrelatedErr := errors.New("unrelated failure")
	fn := func(_ context.Context, _ addInput) (addOutput, error) {
		return addOutput{}, unrelatedErr
	}

	result, err := callTool(t, handle, fn, mcpgo.Options{}, map[string]any{"a": 1.0, "b": 2.0})
	if err != nil {
		t.Fatalf("protocol error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true for unmatched handler error")
	}
	if result.StructuredContent != nil {
		t.Errorf("want no structured content for unmatched error, got %v", result.StructuredContent)
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

// ---------------------------------------------------------------------------
// ResourceHandler tests
// ---------------------------------------------------------------------------

type itemRes struct {
	ID   string
	Name string
}

var itemResCodec = codex.Struct[itemRes](
	codex.RequiredField("id", codex.String(),
		func(v itemRes) string { return v.ID },
		func(v *itemRes, s string) { v.ID = s },
	),
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(v itemRes) string { return v.Name },
		func(v *itemRes, s string) { v.Name = s },
	),
)

func buildItemHandle(uriTemplate string) *apimcp.ResourceHandle[itemRes] {
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	res := apimcp.NewResource[itemRes](uriTemplate, itemResCodec,
		apimcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
	)
	h, err := res.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func callResource(t *testing.T, handle *apimcp.ResourceHandle[itemRes], fn mcpgo.ResourceHandlerFunc[itemRes], opts mcpgo.Options, uri string) ([]mcp.ResourceContents, error) {
	t.Helper()
	_, _, _, handler := mcpgo.ResourceHandler(handle, fn, opts)
	req := mcp.ReadResourceRequest{}
	req.Params.URI = uri
	return handler(context.Background(), req)
}

func TestResourceHandler_success(t *testing.T) {
	handle := buildItemHandle("items://{id}")
	fn := func(_ context.Context, uri string) (itemRes, error) {
		return itemRes{ID: "1", Name: "Widget"}, nil
	}
	contents, err := callResource(t, handle, fn, mcpgo.Options{}, "items://1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contents))
	}
}

func TestResourceHandler_handlerError_returnsError(t *testing.T) {
	handle := buildItemHandle("items://{id}")
	fn := func(_ context.Context, _ string) (itemRes, error) {
		return itemRes{}, errors.New("not found")
	}
	_, err := callResource(t, handle, fn, mcpgo.Options{}, "items://999")
	if err == nil {
		t.Fatal("expected error from handler")
	}
}

func TestResourceHandler_encodeError_returnsError(t *testing.T) {
	// Codec with NonEmptyString on name — empty name will fail encode.
	handle := buildItemHandle("items://{id}")
	fn := func(_ context.Context, _ string) (itemRes, error) {
		// Empty Name violates NonEmptyString constraint on itemResCodec.
		return itemRes{ID: "1", Name: ""}, nil
	}
	_, err := callResource(t, handle, fn, mcpgo.Options{}, "items://1")
	if err == nil {
		t.Fatal("expected encode error for empty Name")
	}
}

func TestResourceHandler_isTemplate_forURIWithPlaceholder(t *testing.T) {
	handle := buildItemHandle("items://{id}")
	_, _, isTemplate, _ := mcpgo.ResourceHandler(handle, func(_ context.Context, _ string) (itemRes, error) {
		return itemRes{ID: "1", Name: "x"}, nil
	}, mcpgo.Options{})
	if !isTemplate {
		t.Error("expected isTemplate=true for URI with {id} placeholder")
	}
}

func TestResourceHandler_isNotTemplate_forLiteralURI(t *testing.T) {
	handle := buildItemHandle("items://featured")
	_, _, isTemplate, _ := mcpgo.ResourceHandler(handle, func(_ context.Context, _ string) (itemRes, error) {
		return itemRes{ID: "1", Name: "x"}, nil
	}, mcpgo.Options{})
	if isTemplate {
		t.Error("expected isTemplate=false for literal URI")
	}
}

func TestResourceHandler_observerRecordsSuccess(t *testing.T) {
	obs := &recordingObserver{}
	handle := buildItemHandle("items://{id}")
	fn := func(_ context.Context, _ string) (itemRes, error) {
		return itemRes{ID: "1", Name: "Widget"}, nil
	}
	_, _ = callResource(t, handle, fn, mcpgo.Options{Observer: obs}, "items://1")
	if len(obs.calls) != 1 || obs.calls[0].statusCode != 200 {
		t.Errorf("expected 1 call with status 200, got %v", obs.calls)
	}
}

// ---------------------------------------------------------------------------
// G4a: ResourceHandlerWithVars / RegisterResourceWithVars tests
// ---------------------------------------------------------------------------

// buildItemHandleWithIDParam registers a merge-capable {id} ResourceParam
// with a UUID codec, used to exercise ExtractURIVars' validation step.
func buildItemHandleWithIDParam(uriTemplate string) *apimcp.ResourceHandle[itemRes] {
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	res := apimcp.NewResource[itemRes](uriTemplate, itemResCodec,
		apimcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
		apimcp.ResourceParam{Name: "id"}.WithCodec(codex.String().Refine(validate.UUID)),
	)
	h, err := res.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func callResourceWithVars(t *testing.T, handle *apimcp.ResourceHandle[itemRes], fn mcpgo.ResourceVarsHandlerFunc[itemRes], opts mcpgo.Options, uri string) ([]mcp.ResourceContents, error) {
	t.Helper()
	_, _, _, handler := mcpgo.ResourceHandlerWithVars(handle, fn, opts)
	req := mcp.ReadResourceRequest{}
	req.Params.URI = uri
	return handler(context.Background(), req)
}

// G4a-1: fn receives the extracted+validated vars map — no manual parsing
// or ValidateURIVars call needed.
func TestResourceHandlerWithVars_receivesExtractedVars(t *testing.T) {
	handle := buildItemHandleWithIDParam("items://{id}")
	var gotVars map[string]string
	fn := func(_ context.Context, _ string, vars map[string]string) (itemRes, error) {
		gotVars = vars
		return itemRes{ID: vars["id"], Name: "Widget"}, nil
	}
	_, err := callResourceWithVars(t, handle, fn, mcpgo.Options{}, "items://550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotVars["id"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("vars[id]: got %q", gotVars["id"])
	}
}

// G4a-1: an extraction mismatch (extra path segment) surfaces
// ResourceURIMismatchError WITHOUT calling fn.
func TestResourceHandlerWithVars_mismatchNeverCallsFn(t *testing.T) {
	handle := buildItemHandleWithIDParam("items://{id}")
	called := false
	fn := func(_ context.Context, _ string, _ map[string]string) (itemRes, error) {
		called = true
		return itemRes{}, nil
	}
	_, err := callResourceWithVars(t, handle, fn, mcpgo.Options{}, "items://abc/extra")
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	var mm apimcp.ResourceURIMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("expected ResourceURIMismatchError, got %T: %v", err, err)
	}
	if called {
		t.Error("fn must not be called on extraction mismatch")
	}
}

// G4a-1: a codec-validation failure (not a UUID) surfaces ResourceParamError
// WITHOUT calling fn.
func TestResourceHandlerWithVars_codecFailureNeverCallsFn(t *testing.T) {
	handle := buildItemHandleWithIDParam("items://{id}")
	called := false
	fn := func(_ context.Context, _ string, _ map[string]string) (itemRes, error) {
		called = true
		return itemRes{}, nil
	}
	_, err := callResourceWithVars(t, handle, fn, mcpgo.Options{}, "items://not-a-uuid")
	if err == nil {
		t.Fatal("expected codec validation error")
	}
	var pe apimcp.ResourceParamError
	if !errors.As(err, &pe) {
		t.Fatalf("expected ResourceParamError, got %T: %v", err, err)
	}
	if called {
		t.Error("fn must not be called on codec validation failure")
	}
}

// G4a-2: a template with NO {var} placeholders behaves identically to
// ResourceHandler today (regression guard, empty vars map, fn still called).
func TestResourceHandlerWithVars_noPlaceholders_emptyVarsRegressionGuard(t *testing.T) {
	handle := buildItemHandle("items://featured")
	var gotVars map[string]string
	fn := func(_ context.Context, _ string, vars map[string]string) (itemRes, error) {
		gotVars = vars
		return itemRes{ID: "1", Name: "Widget"}, nil
	}
	contents, err := callResourceWithVars(t, handle, fn, mcpgo.Options{}, "items://featured")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(gotVars) != 0 {
		t.Errorf("expected empty vars map, got %+v", gotVars)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contents))
	}
}

func TestResourceHandlerWithVars_observerRecordsSuccess(t *testing.T) {
	obs := &recordingObserver{}
	handle := buildItemHandleWithIDParam("items://{id}")
	fn := func(_ context.Context, _ string, _ map[string]string) (itemRes, error) {
		return itemRes{ID: "1", Name: "Widget"}, nil
	}
	_, _ = callResourceWithVars(t, handle, fn, mcpgo.Options{Observer: obs}, "items://550e8400-e29b-41d4-a716-446655440000")
	if len(obs.calls) != 1 || obs.calls[0].statusCode != 200 {
		t.Errorf("expected 1 call with status 200, got %v", obs.calls)
	}
}

func TestResourceHandlerWithVars_observerRecordsMismatchAs500(t *testing.T) {
	obs := &recordingObserver{}
	handle := buildItemHandleWithIDParam("items://{id}")
	fn := func(_ context.Context, _ string, _ map[string]string) (itemRes, error) {
		return itemRes{ID: "1", Name: "Widget"}, nil
	}
	_, _ = callResourceWithVars(t, handle, fn, mcpgo.Options{Observer: obs}, "items://abc/extra")
	if len(obs.calls) != 1 || obs.calls[0].statusCode != 500 {
		t.Errorf("expected 1 call with status 500, got %v", obs.calls)
	}
}

// ---------------------------------------------------------------------------
// PromptHandler tests
// ---------------------------------------------------------------------------

func buildSummarizeHandle() *apimcp.PromptHandle {
	b := apimcp.NewBuilder(apimcp.Info{Name: "test", Version: "1.0.0"})
	p := apimcp.NewPrompt("summarize",
		apimcp.PromptMeta{Description: "Summarize content"},
		apimcp.PromptArg{Name: "content", Required: true},
	)
	h, err := p.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func callPrompt(t *testing.T, handle *apimcp.PromptHandle, fn mcpgo.PromptHandlerFunc, opts mcpgo.Options, args map[string]string) (*mcp.GetPromptResult, error) {
	t.Helper()
	_, handler := mcpgo.PromptHandler(handle, fn, opts)
	req := mcp.GetPromptRequest{}
	req.Params.Arguments = args
	return handler(context.Background(), req)
}

func TestPromptHandler_success(t *testing.T) {
	handle := buildSummarizeHandle()
	fn := func(_ context.Context, args map[string]string) ([]mcpgo.PromptMessage, error) {
		return []mcpgo.PromptMessage{{Role: "user", Content: "summarize: " + args["content"]}}, nil
	}
	result, err := callPrompt(t, handle, fn, mcpgo.Options{}, map[string]string{"content": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result.Messages))
	}
}

func TestPromptHandler_missingRequiredArg_returnsProtocolError(t *testing.T) {
	handle := buildSummarizeHandle()
	fn := func(_ context.Context, args map[string]string) ([]mcpgo.PromptMessage, error) {
		return []mcpgo.PromptMessage{{Role: "user", Content: "ok"}}, nil
	}
	// Missing required "content" arg.
	_, err := callPrompt(t, handle, fn, mcpgo.Options{}, map[string]string{})
	if err == nil {
		t.Fatal("expected protocol error for missing required arg")
	}
}

func TestPromptHandler_handlerError_returnsProtocolError(t *testing.T) {
	handle := buildSummarizeHandle()
	fn := func(_ context.Context, _ map[string]string) ([]mcpgo.PromptMessage, error) {
		return nil, errors.New("service unavailable")
	}
	_, err := callPrompt(t, handle, fn, mcpgo.Options{}, map[string]string{"content": "hello"})
	if err == nil {
		t.Fatal("expected error from handler")
	}
}

func TestPromptHandler_promptDescriptor_hasName(t *testing.T) {
	handle := buildSummarizeHandle()
	prompt, _ := mcpgo.PromptHandler(handle, func(_ context.Context, _ map[string]string) ([]mcpgo.PromptMessage, error) {
		return nil, nil
	}, mcpgo.Options{})
	if prompt.Name != "summarize" {
		t.Errorf("prompt.Name: got %q, want summarize", prompt.Name)
	}
}

func TestPromptHandler_observerRecordsSuccess(t *testing.T) {
	obs := &recordingObserver{}
	handle := buildSummarizeHandle()
	fn := func(_ context.Context, _ map[string]string) ([]mcpgo.PromptMessage, error) {
		return []mcpgo.PromptMessage{{Role: "user", Content: "ok"}}, nil
	}
	_, _ = callPrompt(t, handle, fn, mcpgo.Options{Observer: obs}, map[string]string{"content": "hello"})
	if len(obs.calls) != 1 || obs.calls[0].statusCode != 200 {
		t.Errorf("expected 1 call with status 200, got %v", obs.calls)
	}
}
