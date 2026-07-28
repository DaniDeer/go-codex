package llm_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared types and codecs ───────────────────────────────────────────────────

type article struct{ Title, Body string }
type summary struct{ ThreeSentences string }

var articleCodec = codex.Struct[article](
	codex.RequiredField("title", codex.String(),
		func(a article) string { return a.Title },
		func(a *article, v string) { a.Title = v }),
	codex.RequiredField("body", codex.String(),
		func(a article) string { return a.Body },
		func(a *article, v string) { a.Body = v }),
)

var summaryCodec = codex.Struct[summary](
	codex.RequiredField("threeSentences", codex.String().Refine(validate.NonEmptyString),
		func(s summary) string { return s.ThreeSentences },
		func(s *summary, v string) { s.ThreeSentences = v }),
)

// ── Register / ClientHandle ────────────────────────────────────────────────

func TestCall_Register_HappyPath(t *testing.T) {
	b := llm.NewBuilder(llm.Info{Name: "test", Version: "1.0.0"})
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPrompt("You summarize articles."))

	h, err := call.Register(b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Name != "summarize" {
		t.Errorf("Name = %q, want %q", h.Name, "summarize")
	}
	if h.SystemPrompt != "You summarize articles." {
		t.Errorf("SystemPrompt = %q, want %q", h.SystemPrompt, "You summarize articles.")
	}
	if len(h.ResponseSchema) == 0 {
		t.Error("ResponseSchema should not be empty")
	}
}

func TestCall_Register_EmptyName_ReturnsError(t *testing.T) {
	b := llm.NewBuilder(llm.Info{Name: "test", Version: "1.0.0"})
	call := llm.NewCall[article, summary]("", articleCodec, summaryCodec)
	if _, err := call.Register(b); err == nil {
		t.Fatal("expected error for empty call name, got nil")
	}
}

func TestCall_Register_DuplicateName_ReturnsError(t *testing.T) {
	b := llm.NewBuilder(llm.Info{Name: "test", Version: "1.0.0"})
	call1 := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec)
	call2 := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec)

	if _, err := call1.Register(b); err != nil {
		t.Fatalf("first Register: unexpected error: %v", err)
	}
	if _, err := call2.Register(b); err == nil {
		t.Fatal("expected error for duplicate call name, got nil")
	}
}

func TestCall_SystemPromptFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt.md")
	want := "You are a precise summarizer.\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPromptFile(path))
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.SystemPrompt != want {
		t.Errorf("SystemPrompt = %q, want %q", h.SystemPrompt, want)
	}
}

func TestCall_SystemPromptFile_UnreadablePath_ReturnsError(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPromptFile("/nonexistent/path/prompt.md"))

	_, err := call.ClientHandle()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var spfe llm.SystemPromptFileError
	if !errors.As(err, &spfe) {
		t.Fatalf("expected SystemPromptFileError, got %T: %v", err, err)
	}
	if spfe.Path != "/nonexistent/path/prompt.md" {
		t.Errorf("Path = %q, want %q", spfe.Path, "/nonexistent/path/prompt.md")
	}
	if spfe.Err == nil {
		t.Error("Err should not be nil")
	}
}

func TestSystemPromptFileError_LogValue(t *testing.T) {
	e := llm.SystemPromptFileError{Path: "prompts/x.md", Err: errors.New("boom")}
	if e.Error() == "" {
		t.Error("Error() should not be empty")
	}
	v := e.LogValue()
	attrs := v.Group()
	if len(attrs) < 2 || attrs[0].Key != "path" || attrs[0].Value.String() != "prompts/x.md" {
		t.Errorf("want 'path' attribute, got %v", attrs)
	}
}

func TestCall_ClientHandle_NoBuilder(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPrompt("You summarize articles."))
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil handle")
	}
}

func TestCall_Builder_AccumulatesLLMSpec(t *testing.T) {
	b := llm.NewBuilder(llm.Info{Name: "test", Version: "1.0.0"})
	call1 := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.CallMeta{Description: "Summarizes articles."})
	call2 := llm.NewCall[summary, article]("expand", summaryCodec, articleCodec,
		llm.CallMeta{Description: "Expands a summary back into an article."})

	if _, err := call1.Register(b); err != nil {
		t.Fatalf("Register call1: %v", err)
	}
	if _, err := call2.Register(b); err != nil {
		t.Fatalf("Register call2: %v", err)
	}

	spec, err := b.LLMSpec()
	if err != nil {
		t.Fatalf("LLMSpec: %v", err)
	}
	if len(spec.Calls) != 2 {
		t.Fatalf("want 2 calls, got %d: %+v", len(spec.Calls), spec.Calls)
	}
	names := map[string]bool{spec.Calls[0].Name: true, spec.Calls[1].Name: true}
	if !names["summarize"] || !names["expand"] {
		t.Errorf("want names [summarize expand], got %v", names)
	}
}

// ── CallHandle.EncodeRequest ────────────────────────────────────────────────

func TestCallHandle_EncodeRequest_DefaultJSON(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec)
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := h.EncodeRequest(article{Title: "T", Body: "B"})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if got != `{"body":"B","title":"T"}` {
		t.Errorf("got %q", got)
	}
}

func TestCallHandle_EncodeRequest_UserMessageOverride(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.UserMessage(func(a article) (string, error) {
			return "Title: " + a.Title, nil
		}))
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := h.EncodeRequest(article{Title: "Hello", Body: "World"})
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if got != "Title: Hello" {
		t.Errorf("got %q, want %q", got, "Title: Hello")
	}
}

// ── CallHandle.DecodeResponse ───────────────────────────────────────────────

func TestCallHandle_DecodeResponse_HappyPath(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec)
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := h.DecodeResponse([]byte(`{"threeSentences":"A. B. C."}`))
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	if got.ThreeSentences != "A. B. C." {
		t.Errorf("got %+v", got)
	}
}

func TestCallHandle_DecodeResponse_InvalidJSON_ReturnsResponseDecodeError(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec)
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = h.DecodeResponse([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rde llm.ResponseDecodeError
	if !errors.As(err, &rde) {
		t.Fatalf("expected ResponseDecodeError, got %T: %v", err, err)
	}
	if rde.Name != "summarize" {
		t.Errorf("Name = %q, want %q", rde.Name, "summarize")
	}
}

func TestCallHandle_DecodeResponse_FailsRefineConstraint_ReturnsResponseDecodeError(t *testing.T) {
	// threeSentences is Refine(validate.NonEmptyString) — an empty string is
	// valid JSON but fails the codec's constraint.
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec)
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = h.DecodeResponse([]byte(`{"threeSentences":""}`))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rde llm.ResponseDecodeError
	if !errors.As(err, &rde) {
		t.Fatalf("expected ResponseDecodeError, got %T: %v", err, err)
	}
}

func TestResponseDecodeError_LogValue(t *testing.T) {
	e := llm.ResponseDecodeError{Name: "summarize", Raw: []byte(`bad`), Err: errors.New("boom")}
	if e.Error() == "" {
		t.Error("Error() should not be empty")
	}
	v := e.LogValue()
	attrs := v.Group()
	if len(attrs) < 3 || attrs[0].Key != "name" || attrs[0].Value.String() != "summarize" {
		t.Errorf("want 'name' attribute, got %v", attrs)
	}
}

// ── IncludeRequestSchema ────────────────────────────────────────────────────

func TestCall_IncludeRequestSchema_AppendsSchemaToPrompt(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPrompt("Base prompt."),
		llm.IncludeRequestSchema())
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.SystemPrompt == "Base prompt." {
		t.Error("expected schema to be appended to the system prompt")
	}
}

func TestCall_NoIncludeRequestSchema_LeavesPromptUnchanged(t *testing.T) {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPrompt("Base prompt."))
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.SystemPrompt != "Base prompt." {
		t.Errorf("SystemPrompt = %q, want unchanged %q", h.SystemPrompt, "Base prompt.")
	}
}

// ── Example ──────────────────────────────────────────────────────────────────

func ExampleNewCall() {
	call := llm.NewCall[article, summary]("summarize", articleCodec, summaryCodec,
		llm.SystemPrompt("You summarize articles in exactly three sentences."),
	)

	handle, err := call.ClientHandle()
	if err != nil {
		panic(err)
	}
	_ = handle
}
