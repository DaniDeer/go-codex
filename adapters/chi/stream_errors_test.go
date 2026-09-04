package chi

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

func attrKeysChi(lv slog.Value) map[string]bool {
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	return keys
}

func TestChiNoLatestValueError_LogValue(t *testing.T) {
	e := NoLatestValueError{Path: "/oee"}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	if !attrKeysChi(lv)["path"] {
		t.Error("LogValue missing 'path'")
	}
}

func TestChiPipelineFullError_LogValue(t *testing.T) {
	e := PipelineFullError{Path: "/ingest", Capacity: 128}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysChi(lv)
	for _, k := range []string{"path", "capacity"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestChiPipelineNoResponseError_LogValue(t *testing.T) {
	e := PipelineNoResponseError{Path: "/compute"}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	if !attrKeysChi(lv)["path"] {
		t.Error("LogValue missing 'path'")
	}
}

func TestChiSSEWriteError_LogValue(t *testing.T) {
	e := SSEWriteError{Path: "/events", Err: fmt.Errorf("broken pipe")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysChi(lv)
	for _, k := range []string{"path", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestChiSSEWriteError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("pipe broken")
	e := SSEWriteError{Path: "/events", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}
