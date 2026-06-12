package mcp_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared types and codecs ---

type calcInput struct {
	A float64
	B float64
}

type calcOutput struct {
	Result float64
}

var calcInputCodec = codex.Struct[calcInput](
	codex.RequiredField("a", codex.Float64(),
		func(c calcInput) float64 { return c.A },
		func(c *calcInput, v float64) { c.A = v },
	),
	codex.RequiredField("b", codex.Float64(),
		func(c calcInput) float64 { return c.B },
		func(c *calcInput, v float64) { c.B = v },
	),
)

var calcOutputCodec = codex.Struct[calcOutput](
	codex.RequiredField("result", codex.Float64(),
		func(c calcOutput) float64 { return c.Result },
		func(c *calcOutput, v float64) { c.Result = v },
	),
)

var nonNegFloat = codex.Float64().Refine(validate.MinFloat(0.0))

var constrainedInputCodec = codex.Struct[calcInput](
	codex.RequiredField("a", nonNegFloat,
		func(c calcInput) float64 { return c.A },
		func(c *calcInput, v float64) { c.A = v },
	),
	codex.RequiredField("b", nonNegFloat,
		func(c calcInput) float64 { return c.B },
		func(c *calcInput, v float64) { c.B = v },
	),
)

func newCalcTool() apimcp.Tool[calcInput, calcOutput] {
	return apimcp.NewTool[calcInput, calcOutput]("calculate", calcInputCodec, calcOutputCodec,
		apimcp.ToolMeta{Description: "Add two numbers"},
	)
}

func newBuilder() *apimcp.Builder {
	return apimcp.NewBuilder(apimcp.Info{Name: "Test Server", Version: "1.0.0"})
}

// ---------------------------------------------------------------------------
// Tool tests
// ---------------------------------------------------------------------------

func TestTool_Register_happyPath(t *testing.T) {
	b := newBuilder()
	handle, err := newCalcTool().Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle.Name != "calculate" {
		t.Errorf("Name: got %q, want %q", handle.Name, "calculate")
	}
	if handle.Description != "Add two numbers" {
		t.Errorf("Description: got %q", handle.Description)
	}
	if len(handle.InputSchema) == 0 {
		t.Error("InputSchema must be non-empty")
	}
}

func TestTool_Register_emptyNameFails(t *testing.T) {
	b := newBuilder()
	tool := apimcp.NewTool[calcInput, calcOutput]("", calcInputCodec, calcOutputCodec)
	_, err := tool.Register(b)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestTool_Register_duplicateFails(t *testing.T) {
	b := newBuilder()
	_, err := newCalcTool().Register(b)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	_, err = newCalcTool().Register(b)
	if err == nil {
		t.Fatal("expected error for duplicate tool name")
	}
}

func TestToolHandle_Decode_validInput(t *testing.T) {
	b := newBuilder()
	handle, _ := newCalcTool().Register(b)

	args := map[string]any{"a": 3.0, "b": 4.0}
	result, err := handle.Decode(args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.A != 3.0 || result.B != 4.0 {
		t.Errorf("decoded: got %+v, want {A:3 B:4}", result)
	}
}

func TestToolHandle_Decode_invalidInput_returnsToolInputError(t *testing.T) {
	b := newBuilder()
	tool := apimcp.NewTool[calcInput, calcOutput]("calc", constrainedInputCodec, calcOutputCodec)
	handle, _ := tool.Register(b)

	args := map[string]any{"a": -1.0, "b": 2.0}
	_, err := handle.Decode(args)
	if err == nil {
		t.Fatal("expected error for constraint violation")
	}
	var tie apimcp.ToolInputError
	if !errors.As(err, &tie) {
		t.Fatalf("expected ToolInputError, got %T: %v", err, err)
	}
	if tie.Name != "calc" {
		t.Errorf("ToolInputError.Name: got %q, want %q", tie.Name, "calc")
	}
}

func TestToolHandle_Encode_validOutput(t *testing.T) {
	b := newBuilder()
	handle, _ := newCalcTool().Register(b)

	data, err := handle.Encode(calcOutput{Result: 7.0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if m["result"] != 7.0 {
		t.Errorf("encoded result: got %v, want 7", m["result"])
	}
}

func TestToolHandle_Encode_constraintViolation_returnsToolOutputError(t *testing.T) {
	constrainedOutputCodec := codex.Struct[calcOutput](
		codex.RequiredField("result", codex.Float64().Refine(validate.MinFloat(0.0)),
			func(c calcOutput) float64 { return c.Result },
			func(c *calcOutput, v float64) { c.Result = v },
		),
	)
	b := newBuilder()
	tool := apimcp.NewTool[calcInput, calcOutput]("calc2", calcInputCodec, constrainedOutputCodec)
	handle, _ := tool.Register(b)

	_, err := handle.Encode(calcOutput{Result: -1.0})
	if err == nil {
		t.Fatal("expected error for output constraint violation")
	}
	var toe apimcp.ToolOutputError
	if !errors.As(err, &toe) {
		t.Fatalf("expected ToolOutputError, got %T: %v", err, err)
	}
}

func TestToolHandle_InputSchema_isValidJSON(t *testing.T) {
	b := newBuilder()
	handle, _ := newCalcTool().Register(b)

	var obj map[string]any
	if err := json.Unmarshal(handle.InputSchema, &obj); err != nil {
		t.Fatalf("InputSchema is not valid JSON: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Resource tests
// ---------------------------------------------------------------------------

type itemData struct {
	ID   string
	Name string
}

var itemCodec = codex.Struct[itemData](
	codex.RequiredField("id", codex.String(),
		func(i itemData) string { return i.ID },
		func(i *itemData, v string) { i.ID = v },
	),
	codex.RequiredField("name", codex.String(),
		func(i itemData) string { return i.Name },
		func(i *itemData, v string) { i.Name = v },
	),
)

var uuidCodec = codex.String().Refine(validate.UUID)

func TestResource_Register_happyPath(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceMeta{Name: "Item", MimeType: "application/json"},
		apimcp.ResourceParam{Name: "id"}.WithCodec(uuidCodec),
	)
	handle, err := res.Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle.URITemplate != "items://{id}" {
		t.Errorf("URITemplate: got %q", handle.URITemplate)
	}
}

func TestResource_Register_unknownParamFails_returnsInvalidResourceParamError(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceParam{Name: "unknown"},
	)
	_, err := res.Register(b)
	if err == nil {
		t.Fatal("expected error for unknown URI param")
	}
	var pe apimcp.InvalidResourceParamError
	if !errors.As(err, &pe) {
		t.Fatalf("expected InvalidResourceParamError, got %T: %v", err, err)
	}
	if pe.Name != "unknown" {
		t.Errorf("Name: got %q, want %q", pe.Name, "unknown")
	}
	if pe.URITemplate != "items://{id}" {
		t.Errorf("URITemplate: got %q", pe.URITemplate)
	}
}

func TestResourceHandle_BuildURI_validVars(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceParam{Name: "id"},
	)
	handle, _ := res.Register(b)

	uri, err := handle.BuildURI(map[string]string{"id": "abc123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uri != "items://abc123" {
		t.Errorf("URI: got %q, want %q", uri, "items://abc123")
	}
}

func TestResourceHandle_BuildURI_missingVar(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceParam{Name: "id"},
	)
	handle, _ := res.Register(b)

	_, err := handle.BuildURI(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing var")
	}
	var me apimcp.MissingResourceVarError
	if !errors.As(err, &me) {
		t.Fatalf("expected MissingResourceVarError, got %T", err)
	}
}

func TestResourceHandle_BuildURI_codecFailure(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceParam{Name: "id"}.WithCodec(uuidCodec),
	)
	handle, _ := res.Register(b)

	_, err := handle.BuildURI(map[string]string{"id": "not-a-uuid"})
	if err == nil {
		t.Fatal("expected error for UUID validation failure")
	}
	var ve apimcp.ResourceParamError
	if !errors.As(err, &ve) {
		t.Fatalf("expected ResourceParamError, got %T", err)
	}
	if ve.Name != "id" {
		t.Errorf("ResourceParamError.Name: got %q, want %q", ve.Name, "id")
	}
}

func TestResourceHandle_ValidateURIVars_validVars(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceParam{Name: "id"}.WithCodec(uuidCodec),
	)
	handle, _ := res.Register(b)

	err := handle.ValidateURIVars(map[string]string{"id": "550e8400-e29b-41d4-a716-446655440000"})
	if err != nil {
		t.Errorf("unexpected error for valid UUID: %v", err)
	}
}

func TestResourceHandle_Encode_happy(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec)
	handle, _ := res.Register(b)

	data, err := handle.Encode(itemData{ID: "1", Name: "Widget"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Prompt tests
// ---------------------------------------------------------------------------

func TestPrompt_Register_happyPath(t *testing.T) {
	b := newBuilder()
	p := apimcp.NewPrompt("summarize",
		apimcp.PromptMeta{Description: "Summarize content"},
		apimcp.PromptArg{Name: "content", Required: true},
		apimcp.PromptArg{Name: "style"},
	)
	handle, err := p.Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle.Name != "summarize" {
		t.Errorf("Name: got %q", handle.Name)
	}
	if len(handle.Args) != 2 {
		t.Errorf("Args: got %d, want 2", len(handle.Args))
	}
}

func TestPromptHandle_ValidateArgs_missingRequired(t *testing.T) {
	b := newBuilder()
	p := apimcp.NewPrompt("test",
		apimcp.PromptArg{Name: "content", Required: true},
	)
	handle, _ := p.Register(b)

	err := handle.ValidateArgs(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing required arg")
	}
	var me apimcp.MissingPromptArgError
	if !errors.As(err, &me) {
		t.Fatalf("expected MissingPromptArgError, got %T", err)
	}
	if me.Name != "content" {
		t.Errorf("Name: got %q, want content", me.Name)
	}
}

func TestPromptHandle_ValidateArgs_codecFailure(t *testing.T) {
	enumCodec := codex.String().Refine(validate.OneOf("bullets", "paragraph"))
	b := newBuilder()
	p := apimcp.NewPrompt("test",
		apimcp.PromptArg{Name: "style"}.WithCodec(enumCodec),
	)
	handle, _ := p.Register(b)

	err := handle.ValidateArgs(map[string]string{"style": "invalid"})
	if err == nil {
		t.Fatal("expected error for codec constraint violation")
	}
	var pe apimcp.PromptArgError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PromptArgError, got %T", err)
	}
	if pe.Name != "style" {
		t.Errorf("Name: got %q, want style", pe.Name)
	}
}

func TestPromptHandle_ValidateArgs_optionalMissing_passes(t *testing.T) {
	b := newBuilder()
	p := apimcp.NewPrompt("test",
		apimcp.PromptArg{Name: "optional"},
	)
	handle, _ := p.Register(b)

	err := handle.ValidateArgs(map[string]string{})
	if err != nil {
		t.Errorf("unexpected error for optional missing arg: %v", err)
	}
}

func TestPromptHandle_ValidateArgs_emptyStringRunsCodec(t *testing.T) {
	// Empty string present should run the codec (not be silently skipped).
	enumCodec := codex.String().Refine(validate.NonEmptyString)
	b := newBuilder()
	p := apimcp.NewPrompt("test",
		apimcp.PromptArg{Name: "style"}.WithCodec(enumCodec),
	)
	handle, _ := p.Register(b)

	// Empty string is present in the map — codec must be called and should fail.
	err := handle.ValidateArgs(map[string]string{"style": ""})
	if err == nil {
		t.Fatal("expected codec error for empty string, got nil")
	}
	var pe apimcp.PromptArgError
	if !errors.As(err, &pe) {
		t.Fatalf("expected PromptArgError, got %T", err)
	}
}

func TestPromptHandle_ValidateArgs_requiredPresentEmpty_noErrorWithoutCodec(t *testing.T) {
	// Required arg present with "" and no codec — no error (presence check only).
	b := newBuilder()
	p := apimcp.NewPrompt("test",
		apimcp.PromptArg{Name: "content", Required: true},
	)
	handle, _ := p.Register(b)

	// "" is present — required check passes; no codec to enforce non-empty.
	err := handle.ValidateArgs(map[string]string{"content": ""})
	if err != nil {
		t.Errorf("unexpected error: %v (required but no codec — presence check only)", err)
	}
}

// ---------------------------------------------------------------------------
// MCPSpec tests
// ---------------------------------------------------------------------------

func TestBuilder_MCPSpec_containsAllRegistered(t *testing.T) {
	b := newBuilder()
	_, _ = newCalcTool().Register(b)

	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceMeta{Name: "Item"},
	)
	_, _ = res.Register(b)

	p := apimcp.NewPrompt("summarize",
		apimcp.PromptMeta{Description: "Summarize"},
		apimcp.PromptArg{Name: "content", Required: true},
	)
	_, _ = p.Register(b)

	spec, err := b.MCPSpec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.Name != "Test Server" {
		t.Errorf("Name: got %q", spec.Name)
	}
	if len(spec.Tools) != 1 || spec.Tools[0].Name != "calculate" {
		t.Errorf("Tools: got %v", spec.Tools)
	}
	if len(spec.Resources) != 1 || spec.Resources[0].URITemplate != "items://{id}" {
		t.Errorf("Resources: got %v", spec.Resources)
	}
	if len(spec.Prompts) != 1 || spec.Prompts[0].Name != "summarize" {
		t.Errorf("Prompts: got %v", spec.Prompts)
	}
	if len(spec.Prompts[0].Args) != 1 || !spec.Prompts[0].Args[0].Required {
		t.Errorf("PromptArgs: got %v", spec.Prompts[0].Args)
	}
}

func TestBuilder_MCPSpec_toolInputSchemaPresent(t *testing.T) {
	b := newBuilder()
	_, _ = newCalcTool().Register(b)

	spec, _ := b.MCPSpec()
	if len(spec.Tools[0].InputSchema) == 0 {
		t.Error("ToolSpec.InputSchema must be set")
	}
}

// ---------------------------------------------------------------------------
// WithCodec tests (G1 — mirrors TestPathParam_WithCodec_* in api/rest)
// ---------------------------------------------------------------------------

func TestResourceParam_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	p := apimcp.ResourceParam{Name: "id"}.WithCodec(uuidCodec)
	if p.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
	if err := p.Codec.Validate("550e8400-e29b-41d4-a716-446655440000"); err != nil {
		t.Errorf("expected valid UUID to pass: %v", err)
	}
}

func TestResourceParam_WithCodec_returnsDistinctCopy(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	original := apimcp.ResourceParam{Name: "id"}
	updated := original.WithCodec(uuidCodec)
	if original.Codec != nil {
		t.Error("original ResourceParam must not be mutated")
	}
	if updated.Codec == nil {
		t.Fatal("updated ResourceParam must have Codec set")
	}
}

func TestPromptArg_WithCodec_setsCodecWithoutAddressOf(t *testing.T) {
	enumCodec := codex.String().Refine(validate.OneOf("bullets", "paragraph"))
	a := apimcp.PromptArg{Name: "style"}.WithCodec(enumCodec)
	if a.Codec == nil {
		t.Fatal("expected Codec to be non-nil after WithCodec")
	}
	if err := a.Codec.Validate("bullets"); err != nil {
		t.Errorf("expected valid value to pass: %v", err)
	}
}

func TestPromptArg_WithCodec_returnsDistinctCopy(t *testing.T) {
	enumCodec := codex.String().Refine(validate.OneOf("bullets", "paragraph"))
	original := apimcp.PromptArg{Name: "style"}
	updated := original.WithCodec(enumCodec)
	if original.Codec != nil {
		t.Error("original PromptArg must not be mutated")
	}
	if updated.Codec == nil {
		t.Fatal("updated PromptArg must have Codec set")
	}
}

// ---------------------------------------------------------------------------
// Tags propagation tests (G2 — R16 added Tags but no test verified flow)
// ---------------------------------------------------------------------------

func TestToolMeta_Tags_flowToHandleAndSpec(t *testing.T) {
	b := newBuilder()
	tool := apimcp.NewTool[calcInput, calcOutput]("tag-tool", calcInputCodec, calcOutputCodec,
		apimcp.ToolMeta{Description: "desc", Tags: []string{"math", "v1"}},
	)
	handle, err := tool.Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handle.Tags) != 2 || handle.Tags[0] != "math" {
		t.Errorf("handle.Tags: got %v, want [math v1]", handle.Tags)
	}
	spec, _ := b.MCPSpec()
	if len(spec.Tools[0].Tags) != 2 || spec.Tools[0].Tags[1] != "v1" {
		t.Errorf("spec.Tools[0].Tags: got %v", spec.Tools[0].Tags)
	}
}

func TestResourceMeta_Tags_flowToHandleAndSpec(t *testing.T) {
	b := newBuilder()
	res := apimcp.NewResource[itemData]("items://{id}", itemCodec,
		apimcp.ResourceMeta{Name: "Item", Tags: []string{"catalog", "v2"}},
	)
	handle, err := res.Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handle.Tags) != 2 || handle.Tags[0] != "catalog" {
		t.Errorf("handle.Tags: got %v, want [catalog v2]", handle.Tags)
	}
	spec, _ := b.MCPSpec()
	if len(spec.Resources[0].Tags) != 2 || spec.Resources[0].Tags[1] != "v2" {
		t.Errorf("spec.Resources[0].Tags: got %v", spec.Resources[0].Tags)
	}
}

func TestPromptMeta_Tags_flowToHandleAndSpec(t *testing.T) {
	b := newBuilder()
	p := apimcp.NewPrompt("tag-prompt",
		apimcp.PromptMeta{Description: "desc", Tags: []string{"editorial"}},
		apimcp.PromptArg{Name: "text", Required: true},
	)
	handle, err := p.Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(handle.Tags) != 1 || handle.Tags[0] != "editorial" {
		t.Errorf("handle.Tags: got %v, want [editorial]", handle.Tags)
	}
	spec, _ := b.MCPSpec()
	if len(spec.Prompts[0].Tags) != 1 || spec.Prompts[0].Tags[0] != "editorial" {
		t.Errorf("spec.Prompts[0].Tags: got %v", spec.Prompts[0].Tags)
	}
}

// --- Example functions (shown on pkg.go.dev as runnable snippets) ---

func ExampleNewTool() {
	type SearchReq struct{ Query string }
	type SearchResp struct{ Count int }

	reqCodec := codex.Struct[SearchReq](
		codex.RequiredField("query", codex.String().Refine(validate.NonEmptyString),
			func(r SearchReq) string { return r.Query },
			func(r *SearchReq, v string) { r.Query = v },
		),
	)
	respCodec := codex.Struct[SearchResp](
		codex.RequiredField("count", codex.Int(),
			func(r SearchResp) int { return r.Count },
			func(r *SearchResp, v int) { r.Count = v },
		),
	)

	// Declare the tool as a value — define once, register anywhere.
	searchTool := apimcp.NewTool[SearchReq, SearchResp]("search",
		reqCodec, respCodec,
		apimcp.ToolMeta{Description: "Search the knowledge base."},
	)

	b := apimcp.NewBuilder(apimcp.Info{Name: "My Server", Version: "1.0.0"})
	handle, err := searchTool.Register(b)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	// Decode tool arguments — validated against codec constraints.
	req, err := handle.Decode(map[string]any{"query": "go-codex"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(req.Query)

	// Missing required field returns a typed error.
	_, err = handle.Decode(map[string]any{})
	fmt.Println(err != nil)
	// Output:
	// go-codex
	// true
}
