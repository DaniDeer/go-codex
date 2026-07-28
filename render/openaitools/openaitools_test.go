package openaitools_test

import (
	"encoding/json"
	"testing"

	"github.com/DaniDeer/go-codex/api/llm"
	apimcp "github.com/DaniDeer/go-codex/api/mcp"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/render/openaitools"
)

type addReq struct{ X, Y int }
type addResp struct{ Sum int }

var addReqCodec = codex.Struct[addReq](
	codex.RequiredField("x", codex.Int(), func(r addReq) int { return r.X }, func(r *addReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(), func(r addReq) int { return r.Y }, func(r *addReq, v int) { r.Y = v }),
)
var addRespCodec = codex.Struct[addResp](
	codex.RequiredField("sum", codex.Int(), func(r addResp) int { return r.Sum }, func(r *addResp, v int) { r.Sum = v }),
)

func TestRender_HappyPath(t *testing.T) {
	tools := []openaitools.Tool{
		{Name: "add", Description: "Adds two numbers.", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	raw, err := openaitools.Render(tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if got[0]["type"] != "function" {
		t.Errorf("type = %v, want %q", got[0]["type"], "function")
	}
	fn, ok := got[0]["function"].(map[string]any)
	if !ok {
		t.Fatalf("function missing or wrong type: %v", got[0]["function"])
	}
	if fn["name"] != "add" || fn["description"] != "Adds two numbers." {
		t.Errorf("function = %v", fn)
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok || params["type"] != "object" {
		t.Errorf("parameters = %v", fn["parameters"])
	}
}

func TestRender_EmptySlice(t *testing.T) {
	raw, err := openaitools.Render(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(raw) != "[]" {
		t.Errorf("got %s, want []", raw)
	}
}

func TestFromMCPSpec(t *testing.T) {
	b := apimcp.NewBuilder(apimcp.Info{Name: "svc", Version: "1.0.0"})
	tool := apimcp.NewTool[addReq, addResp]("add", addReqCodec, addRespCodec,
		apimcp.ToolMeta{Description: "Adds two numbers."})
	if _, err := tool.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	spec, err := b.MCPSpec()
	if err != nil {
		t.Fatalf("MCPSpec: %v", err)
	}

	tools := openaitools.FromMCPSpec(spec)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "add" || tools[0].Description != "Adds two numbers." {
		t.Errorf("got %+v", tools[0])
	}
	if len(tools[0].Parameters) == 0 {
		t.Error("Parameters should not be empty")
	}

	// End-to-end: renders correctly too.
	raw, err := openaitools.Render(tools)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 rendered entry, got %d", len(got))
	}
}

func TestFromLLMSpec(t *testing.T) {
	b := llm.NewBuilder(llm.Info{Name: "svc", Version: "1.0.0"})
	call := llm.NewCall[addReq, addResp]("add", addReqCodec, addRespCodec,
		llm.CallMeta{Description: "Adds two numbers via an LLM."})
	if _, err := call.Register(b); err != nil {
		t.Fatalf("Register: %v", err)
	}
	spec, err := b.LLMSpec()
	if err != nil {
		t.Fatalf("LLMSpec: %v", err)
	}

	tools := openaitools.FromLLMSpec(spec)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "add" || tools[0].Description != "Adds two numbers via an LLM." {
		t.Errorf("got %+v", tools[0])
	}
	if len(tools[0].Parameters) == 0 {
		t.Error("Parameters should not be empty")
	}
}

func TestFromMCPSpec_MultipleTools(t *testing.T) {
	b := apimcp.NewBuilder(apimcp.Info{Name: "svc", Version: "1.0.0"})
	tool1 := apimcp.NewTool[addReq, addResp]("add", addReqCodec, addRespCodec)
	tool2 := apimcp.NewTool[addResp, addReq]("subtract", addRespCodec, addReqCodec)
	if _, err := tool1.Register(b); err != nil {
		t.Fatalf("Register tool1: %v", err)
	}
	if _, err := tool2.Register(b); err != nil {
		t.Fatalf("Register tool2: %v", err)
	}
	spec, err := b.MCPSpec()
	if err != nil {
		t.Fatalf("MCPSpec: %v", err)
	}

	tools := openaitools.FromMCPSpec(spec)
	if len(tools) != 2 {
		t.Fatalf("want 2 tools, got %d", len(tools))
	}
}
