package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/adapters/openai"
	"github.com/DaniDeer/go-codex/api/llm"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared types and codecs ───────────────────────────────────────────────────

type question struct{ Text string }
type answer struct{ Text string }

var questionCodec = codex.Struct[question](
	codex.RequiredField("text", codex.String(),
		func(q question) string { return q.Text },
		func(q *question, v string) { q.Text = v }),
)

var answerCodec = codex.Struct[answer](
	codex.RequiredField("text", codex.String().Refine(validate.NonEmptyString),
		func(a answer) string { return a.Text },
		func(a *answer, v string) { a.Text = v }),
)

func testHandle(t *testing.T, opts ...llm.CallOpt) *llm.CallHandle[question, answer] {
	t.Helper()
	call := llm.NewCall[question, answer]("ask", questionCodec, answerCodec, opts...)
	h, err := call.ClientHandle()
	if err != nil {
		t.Fatalf("ClientHandle: %v", err)
	}
	return h
}

// chatCompletionBody writes a minimal valid Chat Completions JSON response
// with the given assistant message content.
func chatCompletionBody(content string) []byte {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": content}},
		},
	})
	return b
}

// roundTripFunc adapts a function to http.RoundTripper — used to simulate
// network failures without a real server.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// recordingObserver records every RecordRequest call (not just the last),
// so multi-attempt retry tests can assert the full attempt sequence.
type recordingObserver struct {
	stats.NoopObserver
	calls   []recordedCall
	valErrs []struct{ location, constraint, field string }
}

type recordedCall struct {
	method string
	path   string
	status int
}

func (o *recordingObserver) RecordRequest(method, path string, statusCode int, _ time.Duration) {
	o.calls = append(o.calls, recordedCall{method, path, statusCode})
}

func (o *recordingObserver) RecordValidationError(location, constraint, field string) {
	o.valErrs = append(o.valErrs, struct{ location, constraint, field string }{location, constraint, field})
}

func callOnce[Req, Resp any](t *testing.T, adapter ports.IOAdapter[Req, Resp], req Req) (Resp, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := adapter.Transform(ctx, gstream.Single(ctx, req))
	vals, errs := gstream.Collect(ctx, out)
	var zero Resp
	if len(errs) > 0 {
		return zero, errs[0]
	}
	if len(vals) == 0 {
		t.Fatal("expected exactly one value, got none")
	}
	return vals[0], nil
}

// ── happy path ────────────────────────────────────────────────────────────

func TestCallAdapter_Transform_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompletionBody(`{"text":"42"}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	obs := &recordingObserver{}
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{
		BaseURL: srv.URL, Model: "gpt-4o-mini", APIKey: "test-key", Observer: obs,
	})

	got, err := callOnce[question, answer](t, adapter, question{Text: "What is 6*7?"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Text != "42" {
		t.Errorf("got %+v", got)
	}
	if len(obs.calls) != 1 || obs.calls[0].status != 200 {
		t.Errorf("want 1 recorded call with status 200, got %+v", obs.calls)
	}
}

// ── encode-request failure short-circuit ───────────────────────────────────

func TestCallAdapter_Transform_EncodeRequestFailure(t *testing.T) {
	wantErr := errors.New("encode boom")
	h := testHandle(t, llm.UserMessage(func(question) (string, error) { return "", wantErr }))
	obs := &recordingObserver{}
	adapter := openai.CallAdapter(&http.Client{}, h, openai.CallAdapterOptions{
		Model: "gpt-4o-mini", Observer: obs,
	})

	_, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	if !errors.Is(err, wantErr) {
		t.Fatalf("want %v, got %v", wantErr, err)
	}
	if len(obs.calls) != 1 || obs.calls[0].status != 0 {
		t.Errorf("want 1 recorded call with status 0, got %+v", obs.calls)
	}
}

// ── network failure ─────────────────────────────────────────────────────────

func TestCallAdapter_Transform_NetworkFailure_ReturnsRequestError(t *testing.T) {
	wantErr := errors.New("network boom")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, wantErr
	})}
	h := testHandle(t)
	adapter := openai.CallAdapter(client, h, openai.CallAdapterOptions{Model: "gpt-4o-mini"})

	_, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	var reqErr openai.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T: %v", err, err)
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Unwrap should reach %v, got %v", wantErr, err)
	}
}

// ── non-2xx status ──────────────────────────────────────────────────────────

func TestCallAdapter_Transform_NonOKStatus_ReturnsUnexpectedStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{BaseURL: srv.URL, Model: "gpt-4o-mini"})

	_, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	var statusErr openai.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusTooManyRequests)
	}
	if statusErr.Body == "" {
		t.Error("Body should not be empty")
	}
}

// ── empty choices ────────────────────────────────────────────────────────────

func TestCallAdapter_Transform_EmptyChoices_ReturnsNoChoicesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{BaseURL: srv.URL, Model: "gpt-4o-mini"})

	_, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	var noChoices openai.NoChoicesError
	if !errors.As(err, &noChoices) {
		t.Fatalf("expected NoChoicesError, got %T: %v", err, err)
	}
}

// ── retry loop ───────────────────────────────────────────────────────────────

func TestCallAdapter_Transform_InvalidCompletion_NoRetry_ReturnsResponseDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompletionBody(`{"text":""}`)) // empty text fails NonEmptyString
	}))
	defer srv.Close()

	h := testHandle(t)
	obs := &recordingObserver{}
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{
		BaseURL: srv.URL, Model: "gpt-4o-mini", Observer: obs, // MaxRetries: 0 (default)
	})

	_, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	var rde llm.ResponseDecodeError
	if !errors.As(err, &rde) {
		t.Fatalf("expected ResponseDecodeError, got %T: %v", err, err)
	}
	var retriesExhausted openai.RetriesExhaustedError
	if errors.As(err, &retriesExhausted) {
		t.Error("with MaxRetries:0, error should NOT be wrapped as RetriesExhaustedError")
	}
	if len(obs.calls) != 1 {
		t.Errorf("want exactly 1 recorded attempt, got %d: %+v", len(obs.calls), obs.calls)
	}
}

func TestCallAdapter_Transform_InvalidCompletion_RetrySucceeds(t *testing.T) {
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.WriteHeader(http.StatusOK)
		if attempt == 1 {
			_, _ = w.Write(chatCompletionBody(`{"text":""}`)) // invalid
			return
		}
		_, _ = w.Write(chatCompletionBody(`{"text":"valid now"}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	obs := &recordingObserver{}
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{
		BaseURL: srv.URL, Model: "gpt-4o-mini", MaxRetries: 1, Observer: obs,
	})

	got, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Text != "valid now" {
		t.Errorf("got %+v", got)
	}
	if attempt != 2 {
		t.Errorf("want 2 HTTP attempts, got %d", attempt)
	}
	if len(obs.calls) != 2 {
		t.Errorf("want 2 recorded attempts, got %d: %+v", len(obs.calls), obs.calls)
	}
}

func TestCallAdapter_Transform_RetriesExhausted_ReturnsRetriesExhaustedError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompletionBody(`{"text":""}`)) // always invalid
	}))
	defer srv.Close()

	h := testHandle(t)
	obs := &recordingObserver{}
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{
		BaseURL: srv.URL, Model: "gpt-4o-mini", MaxRetries: 2, Observer: obs,
	})

	_, err := callOnce[question, answer](t, adapter, question{Text: "x"})
	var re openai.RetriesExhaustedError
	if !errors.As(err, &re) {
		t.Fatalf("expected RetriesExhaustedError, got %T: %v", err, err)
	}
	if re.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3 (1 + MaxRetries)", re.Attempts)
	}
	if attempts != 3 {
		t.Errorf("want 3 HTTP attempts, got %d", attempts)
	}
	if len(obs.calls) != 3 {
		t.Errorf("want 3 recorded attempts, got %d: %+v", len(obs.calls), obs.calls)
	}
}

// ── credential priority ──────────────────────────────────────────────────────

func TestCallAdapter_CredentialFunc_TakesPriorityOverAPIKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompletionBody(`{"text":"ok"}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{
		BaseURL: srv.URL, Model: "gpt-4o-mini",
		APIKey: "static-key",
		CredentialFunc: func(context.Context) (string, error) {
			return "dynamic-token", nil
		},
	})

	if _, err := callOnce[question, answer](t, adapter, question{Text: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer dynamic-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer dynamic-token")
	}
}

// ── strict JSON Schema response_format shape ────────────────────────────────

func TestCallAdapter_ResponseFormat_UsesStrictJSONSchema(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompletionBody(`{"text":"ok"}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{BaseURL: srv.URL, Model: "gpt-4o-mini"})
	if _, err := callOnce[question, answer](t, adapter, question{Text: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rf, ok := gotBody["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format missing or wrong type: %v", gotBody["response_format"])
	}
	if rf["type"] != "json_schema" {
		t.Errorf("response_format.type = %v, want %q", rf["type"], "json_schema")
	}
	js, ok := rf["json_schema"].(map[string]any)
	if !ok {
		t.Fatalf("json_schema missing or wrong type: %v", rf["json_schema"])
	}
	if js["strict"] != true {
		t.Errorf("json_schema.strict = %v, want true", js["strict"])
	}
	gotSchema, err := json.Marshal(js["schema"])
	if err != nil {
		t.Fatalf("marshal got schema: %v", err)
	}
	if string(gotSchema) != string(h.ResponseSchema) {
		t.Errorf("json_schema.schema mismatch:\n  got:  %s\n  want: %s", gotSchema, h.ResponseSchema)
	}
}

// ── observer: response validation errors reported ───────────────────────────

func TestCallAdapter_Transform_DecodeFailure_ReportsValidationError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(chatCompletionBody(`{"text":""}`))
	}))
	defer srv.Close()

	h := testHandle(t)
	obs := &recordingObserver{}
	adapter := openai.CallAdapter(srv.Client(), h, openai.CallAdapterOptions{BaseURL: srv.URL, Model: "gpt-4o-mini", Observer: obs})

	if _, err := callOnce[question, answer](t, adapter, question{Text: "x"}); err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(obs.valErrs) == 0 {
		t.Error("expected at least one RecordValidationError call")
	}
}

// ── errors: LogValue coverage ────────────────────────────────────────────────

// logValueKeys asserts v is a KindGroup and returns its attribute keys as a
// set — the reference pattern from adapters/sql/validate_test.go's
// TestValidate_LogValue: check ALL expected keys are present by name, not
// fragile index-based positions that break on field reordering.
func logValueKeys(t *testing.T, v slog.Value) map[string]bool {
	t.Helper()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", v.Kind())
	}
	keys := make(map[string]bool)
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	return keys
}

func TestRequestBuildError_LogValue(t *testing.T) {
	e := openai.RequestBuildError{Name: "summarize", Err: errors.New("boom")}
	if e.Error() == "" {
		t.Error("Error() should not be empty")
	}
	keys := logValueKeys(t, e.LogValue())
	for _, want := range []string{"name", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestRequestError_LogValue(t *testing.T) {
	e := openai.RequestError{Name: "summarize", Model: "gpt-4o-mini", Err: errors.New("boom")}
	keys := logValueKeys(t, e.LogValue())
	for _, want := range []string{"name", "model", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestUnexpectedStatusError_LogValue(t *testing.T) {
	e := openai.UnexpectedStatusError{Name: "summarize", Model: "gpt-4o-mini", StatusCode: 429, Body: "rate limited"}
	keys := logValueKeys(t, e.LogValue())
	for _, want := range []string{"name", "model", "status"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestResponseBodyError_LogValue(t *testing.T) {
	e := openai.ResponseBodyError{Name: "summarize", Err: errors.New("boom")}
	keys := logValueKeys(t, e.LogValue())
	for _, want := range []string{"name", "cause"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestNoChoicesError_LogValue(t *testing.T) {
	e := openai.NoChoicesError{Name: "summarize", Model: "gpt-4o-mini"}
	keys := logValueKeys(t, e.LogValue())
	for _, want := range []string{"name", "model"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestRetriesExhaustedError_LogValue(t *testing.T) {
	e := openai.RetriesExhaustedError{Name: "summarize", Model: "gpt-4o-mini", Attempts: 3, LastErr: errors.New("boom")}
	if e.Error() == "" {
		t.Error("Error() should not be empty")
	}
	keys := logValueKeys(t, e.LogValue())
	for _, want := range []string{"name", "model", "attempts", "lastErr"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
	if !errors.Is(e, e.LastErr) {
		t.Error("Unwrap should reach LastErr")
	}
}
