package zeromq

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

func TestServeLatestError_LogValue(t *testing.T) {
	e := ServeLatestError{Op: "recv", Err: fmt.Errorf("connection reset")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysZMQ(lv)
	for _, k := range []string{"op", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestServeLatestError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("io error")
	e := ServeLatestError{Op: "recv", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}

func TestNoLatestValueError_LogValue(t *testing.T) {
	e := NoLatestValueError{Topic: "compute/oee"}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysZMQ(lv)
	if !keys["topic"] {
		t.Error("LogValue missing 'topic'")
	}
}

func TestNoLatestValueError_NoUnwrap(t *testing.T) {
	e := NoLatestValueError{Topic: "x"}
	// No Unwrap — wrapping it should still work but not chain further
	wrapped := fmt.Errorf("outer: %w", e)
	var got NoLatestValueError
	if !errors.As(wrapped, &got) {
		t.Error("errors.As must reach NoLatestValueError")
	}
}

func TestCorrelationError_LogValue(t *testing.T) {
	e := CorrelationError{Seq: 42, Err: fmt.Errorf("stale reply")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysZMQ(lv)
	for _, k := range []string{"seq", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestCorrelationError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("stale")
	e := CorrelationError{Seq: 1, Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}

func TestPipelineNoResponseError_LogValue(t *testing.T) {
	e := PipelineNoResponseError{Topic: "compute/oee"}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	if !attrKeysZMQ(lv)["topic"] {
		t.Error("LogValue missing 'topic'")
	}
}

func attrKeysZMQ(lv slog.Value) map[string]bool {
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	return keys
}
