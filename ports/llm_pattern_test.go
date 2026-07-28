package ports_test

import (
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/ports"
)

func TestIOPort_PluginLLMPattern_HappyPath(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("summarize", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	handle, err := p.PluginLLMPattern(ports.LLMPattern{
		Name: "summarize",
		Opts: []llm.CallOpt{llm.SystemPrompt("Summarize the number.")},
	})
	if err != nil {
		t.Fatalf("PluginLLMPattern: %v", err)
	}
	if handle.Name != "summarize" {
		t.Errorf("Name = %q, want %q", handle.Name, "summarize")
	}
	if handle.SystemPrompt != "Summarize the number." {
		t.Errorf("SystemPrompt = %q, want %q", handle.SystemPrompt, "Summarize the number.")
	}
	if len(handle.ResponseSchema) == 0 {
		t.Error("ResponseSchema should not be empty")
	}
}

func TestIOPort_PluginLLMPattern_DuplicatePluginRejected(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("summarize", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	pattern := ports.LLMPattern{Name: "summarize", Opts: []llm.CallOpt{llm.SystemPrompt("x")}}
	if _, err := p.PluginLLMPattern(pattern); err != nil {
		t.Fatalf("first PluginLLMPattern: %v", err)
	}
	if _, err := p.PluginLLMPattern(pattern); err == nil {
		t.Fatal("expected error on second PluginLLMPattern call, got nil")
	}
}

func TestIOPort_PluginLLMPattern_SystemPromptFileError_WrappedAsPatternRegisterError(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("summarize", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	_, err = p.PluginLLMPattern(ports.LLMPattern{
		Name: "summarize",
		Opts: []llm.CallOpt{llm.SystemPromptFile("/nonexistent/prompt.md")},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pre ports.PatternRegisterError
	if !errors.As(err, &pre) {
		t.Fatalf("expected PatternRegisterError, got %T: %v", err, err)
	}
	if pre.Kind != "llm" {
		t.Errorf("Kind = %q, want %q", pre.Kind, "llm")
	}
	var spfe llm.SystemPromptFileError
	if !errors.As(err, &spfe) {
		t.Fatalf("expected wrapped SystemPromptFileError, got: %v", err)
	}
}

// TestLLMPattern_ParityWithDirectCallDeclaration locks that a Pattern-declared
// llm.Call (via PluginLLMPattern) behaves identically to one declared
// directly via llm.NewCall(...).Register(b) — no ports-specific wiring
// needed since LLMPattern.Opts is a thin llm.CallOpt pass-through. Mirrors
// TestRESTPattern_ErrorStatus_ParityWithDirectRouteDeclaration's rationale.
func TestLLMPattern_ParityWithDirectCallDeclaration(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("summarize", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	opts := []llm.CallOpt{
		llm.SystemPrompt("Summarize the number."),
		llm.CallMeta{Description: "test"},
	}

	viaPattern, err := p.PluginLLMPattern(ports.LLMPattern{Name: "summarize", Opts: opts})
	if err != nil {
		t.Fatalf("PluginLLMPattern: %v", err)
	}

	direct, err := llm.NewCall[int, string]("summarize", intCodec, strCodec, opts...).ClientHandle()
	if err != nil {
		t.Fatalf("direct ClientHandle: %v", err)
	}

	if viaPattern.Name != direct.Name || viaPattern.SystemPrompt != direct.SystemPrompt {
		t.Errorf("Pattern-built handle %+v does not match direct handle %+v", viaPattern, direct)
	}
	if string(viaPattern.ResponseSchema) != string(direct.ResponseSchema) {
		t.Errorf("ResponseSchema mismatch:\n  pattern: %s\n  direct:  %s", viaPattern.ResponseSchema, direct.ResponseSchema)
	}
}

func TestRegisterLLM_ReplaysPatternAgainstDifferentBuilder(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("summarize", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if _, err := p.PluginLLMPattern(ports.LLMPattern{
		Name: "summarize",
		Opts: []llm.CallOpt{llm.SystemPrompt("Summarize the number.")},
	}); err != nil {
		t.Fatalf("PluginLLMPattern: %v", err)
	}

	b := llm.NewBuilder(llm.Info{Name: "svc", Version: "1.0.0"})
	if err := ports.RegisterLLM[int, string](b, p); err != nil {
		t.Fatalf("RegisterLLM: %v", err)
	}
	spec, err := b.LLMSpec()
	if err != nil {
		t.Fatalf("LLMSpec: %v", err)
	}
	if len(spec.Calls) != 1 || spec.Calls[0].Name != "summarize" {
		t.Errorf("want 1 call named 'summarize', got %+v", spec.Calls)
	}
}

func TestRegisterLLM_MissingPattern_ReturnsError(t *testing.T) {
	p, err := ports.NewIOPort[int, string]("no-llm", intCodec, strCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	b := llm.NewBuilder(llm.Info{Name: "svc", Version: "1.0.0"})
	err = ports.RegisterLLM[int, string](b, p)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var mpe ports.MissingPatternError
	if !errors.As(err, &mpe) {
		t.Fatalf("expected MissingPatternError, got %T: %v", err, err)
	}
	if mpe.Kind != "llm" {
		t.Errorf("Kind = %q, want %q", mpe.Kind, "llm")
	}
}

func TestIOPort_LLMBuilder_SharedAcrossCalls(t *testing.T) {
	b := llm.NewBuilder(llm.Info{Name: "svc", Version: "1.0.0"})

	p1, err := ports.NewIOPort[int, string]("summarize", intCodec, strCodec, ports.PortOptions{LLMBuilder: b})
	if err != nil {
		t.Fatalf("construct port1: %v", err)
	}
	p2, err := ports.NewIOPort[string, int]("count-words", strCodec, intCodec, ports.PortOptions{LLMBuilder: b})
	if err != nil {
		t.Fatalf("construct port2: %v", err)
	}

	if _, err := p1.PluginLLMPattern(ports.LLMPattern{Name: "summarize", Opts: []llm.CallOpt{llm.SystemPrompt("s")}}); err != nil {
		t.Fatalf("p1 PluginLLMPattern: %v", err)
	}
	if _, err := p2.PluginLLMPattern(ports.LLMPattern{Name: "count-words", Opts: []llm.CallOpt{llm.SystemPrompt("c")}}); err != nil {
		t.Fatalf("p2 PluginLLMPattern: %v", err)
	}

	spec, err := b.LLMSpec()
	if err != nil {
		t.Fatalf("LLMSpec: %v", err)
	}
	if len(spec.Calls) != 2 {
		t.Fatalf("want 2 calls accumulated on the shared builder, got %d: %+v", len(spec.Calls), spec.Calls)
	}
}
