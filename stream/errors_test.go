package stream_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	stream "github.com/DaniDeer/go-codex/stream"
)

// ── StreamDecodeError ─────────────────────────────────────────────────────────

func TestStreamDecodeError_Error(t *testing.T) {
	inner := fmt.Errorf("json: unexpected end")
	e := stream.StreamDecodeError{Source: "mqtt/sensors", Err: inner}
	if e.Error() == "" {
		t.Fatal("Error() must not be empty")
	}
	if !containsAll(e.Error(), "mqtt/sensors", "json: unexpected end") {
		t.Errorf("Error() %q missing expected substrings", e.Error())
	}
}

func TestStreamDecodeError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("decode failure")
	e := stream.StreamDecodeError{Source: "mqtt", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner error via Unwrap")
	}
}

func TestStreamDecodeError_LogValue(t *testing.T) {
	e := stream.StreamDecodeError{Source: "mqtt/sensors", Err: fmt.Errorf("bad json")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeys(lv)
	for _, want := range []string{"source", "err"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestStreamDecodeError_ErrorsAs(t *testing.T) {
	inner := fmt.Errorf("json decode error")
	e := stream.StreamDecodeError{Source: "src", Err: inner}
	// Wrap it to simulate how errors travel through the pipeline.
	wrapped := fmt.Errorf("outer: %w", e)
	var sde stream.StreamDecodeError
	if !errors.As(wrapped, &sde) {
		t.Fatal("errors.As must reach StreamDecodeError through wrapping")
	}
	if sde.Source != "src" {
		t.Errorf("Source: want %q, got %q", "src", sde.Source)
	}
}

// ── StreamApplyError ──────────────────────────────────────────────────────────

func TestStreamApplyError_Error(t *testing.T) {
	inner := fmt.Errorf("input validation failed")
	e := stream.StreamApplyError{Function: "oeeCalc", Err: inner}
	if e.Error() == "" {
		t.Fatal("Error() must not be empty")
	}
	if !containsAll(e.Error(), "oeeCalc", "input validation failed") {
		t.Errorf("Error() %q missing expected substrings", e.Error())
	}
}

func TestStreamApplyError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("compute error")
	e := stream.StreamApplyError{Function: "gradeCalc", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner error via Unwrap")
	}
}

func TestStreamApplyError_LogValue(t *testing.T) {
	e := stream.StreamApplyError{Function: "oeeCalc", Err: fmt.Errorf("validation failed")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeys(lv)
	for _, want := range []string{"function", "err"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

func TestStreamApplyError_ErrorsAs(t *testing.T) {
	inner := fmt.Errorf("forge error")
	e := stream.StreamApplyError{Function: "fn", Err: inner}
	wrapped := fmt.Errorf("outer: %w", e)
	var sae stream.StreamApplyError
	if !errors.As(wrapped, &sae) {
		t.Fatal("errors.As must reach StreamApplyError through wrapping")
	}
	if sae.Function != "fn" {
		t.Errorf("Function: want %q, got %q", "fn", sae.Function)
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func attrKeys(lv slog.Value) map[string]bool {
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	return keys
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
