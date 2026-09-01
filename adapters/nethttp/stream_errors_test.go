package nethttp_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
)

func attrKeysNH(lv slog.Value) map[string]bool {
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	return keys
}

func TestNoLatestValueError_LogValue(t *testing.T) {
	e := nethttp.NoLatestValueError{Path: "/oee/current"}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	if !attrKeysNH(lv)["path"] {
		t.Error("LogValue missing 'path'")
	}
}

func TestNoLatestValueError_ErrorsAs(t *testing.T) {
	e := nethttp.NoLatestValueError{Path: "/oee"}
	wrapped := fmt.Errorf("wrap: %w", e)
	var got nethttp.NoLatestValueError
	if !errors.As(wrapped, &got) {
		t.Error("errors.As must reach NoLatestValueError")
	}
	if got.Path != "/oee" {
		t.Errorf("Path: want %q, got %q", "/oee", got.Path)
	}
}

func TestPipelineFullError_LogValue(t *testing.T) {
	e := nethttp.PipelineFullError{Path: "/ingest", Capacity: 256}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysNH(lv)
	for _, k := range []string{"path", "capacity"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestPipelineNoResponseError_LogValue(t *testing.T) {
	e := nethttp.PipelineNoResponseError{Path: "/compute"}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	if !attrKeysNH(lv)["path"] {
		t.Error("LogValue missing 'path'")
	}
}

func TestSSEWriteError_LogValue(t *testing.T) {
	e := nethttp.SSEWriteError{Path: "/events", Err: fmt.Errorf("write: broken pipe")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysNH(lv)
	for _, k := range []string{"path", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestSSEWriteError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("broken pipe")
	e := nethttp.SSEWriteError{Path: "/events", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}

func TestSSEConnectError_LogValue(t *testing.T) {
	e := nethttp.SSEConnectError{URL: "http://svc/events", Attempt: 3, Err: fmt.Errorf("connection refused")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysNH(lv)
	for _, k := range []string{"url", "attempt", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestSSEConnectError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("conn refused")
	e := nethttp.SSEConnectError{URL: "http://x", Attempt: 1, Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}

func TestSSEParseError_LogValue(t *testing.T) {
	e := nethttp.SSEParseError{URL: "http://svc/events", Line: "{bad}", Err: fmt.Errorf("json error")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysNH(lv)
	for _, k := range []string{"url", "line", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestSSEParseError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("json: unexpected")
	e := nethttp.SSEParseError{URL: "http://x", Line: "{}", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}

func TestSSEHandlerError_LogValue(t *testing.T) {
	e := nethttp.SSEHandlerError{URL: "http://svc/events", Err: fmt.Errorf("handler failed")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysNH(lv)
	for _, k := range []string{"url", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestSSEHandlerError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("handler boom")
	e := nethttp.SSEHandlerError{URL: "http://x", Err: inner}
	if !errors.Is(e, inner) {
		t.Error("errors.Is must reach inner via Unwrap")
	}
}
