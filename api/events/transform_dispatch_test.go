package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
)

type dispatchMsg struct{ Val string }

func TestDispatchSubscribeMiddlewareHandlers_success(t *testing.T) {
	called := false
	h := events.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return topicVars["id"], nil
		},
		Fn: func(ctx context.Context, msg *dispatchMsg, in string) error {
			called = true
			msg.Val = in
			return nil
		},
	}
	msg := &dispatchMsg{}
	_, err := events.DispatchSubscribeMiddlewareHandlers(context.Background(), msg, []events.MiddlewareHandler{h}, map[string]string{"id": "42"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called || msg.Val != "42" {
		t.Fatalf("handler not invoked correctly: called=%v val=%q", called, msg.Val)
	}
}

func TestDispatchSubscribeMiddlewareHandlers_decodeInError(t *testing.T) {
	wantErr := errors.New("decode failed")
	h := events.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return nil, wantErr
		},
	}
	msg := &dispatchMsg{}
	_, err := events.DispatchSubscribeMiddlewareHandlers(context.Background(), msg, []events.MiddlewareHandler{h}, nil, nil)
	info, ok := events.AsMiddlewareDispatchError(err)
	if !ok {
		t.Fatalf("expected a dispatch error, got %v", err)
	}
	if info.IsFnError || info.Name != "mw" || !errors.Is(info.Err, wantErr) {
		t.Fatalf("unexpected dispatch info: %+v", info)
	}
}

func TestDispatchSubscribeMiddlewareHandlers_fnError(t *testing.T) {
	wantErr := errors.New("fn failed")
	h := events.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return "in", nil
		},
		Fn: func(ctx context.Context, msg *dispatchMsg, in string) error {
			return wantErr
		},
	}
	msg := &dispatchMsg{}
	_, err := events.DispatchSubscribeMiddlewareHandlers(context.Background(), msg, []events.MiddlewareHandler{h}, nil, nil)
	info, ok := events.AsMiddlewareDispatchError(err)
	if !ok || !info.IsFnError || !errors.Is(info.Err, wantErr) {
		t.Fatalf("unexpected dispatch info: %+v ok=%v", info, ok)
	}
}

func TestDispatchPublishMiddlewareHandlers_success(t *testing.T) {
	h := events.ClientMiddlewareHandler{
		Name: "mw",
		Fn: func(ctx context.Context, msg dispatchMsg) (string, error) {
			return msg.Val, nil
		},
		EncodeOut: func(ctx context.Context, out any) (topicVars, propertyVars map[string]string, err error) {
			return map[string]string{"id": out.(string)}, map[string]string{"prop": "x"}, nil
		},
	}
	topicVars, propertyVars, err := events.DispatchPublishMiddlewareHandlers(context.Background(), dispatchMsg{Val: "7"}, []events.ClientMiddlewareHandler{h})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if topicVars["id"] != "7" || propertyVars["prop"] != "x" {
		t.Fatalf("unexpected vars: topic=%v property=%v", topicVars, propertyVars)
	}
}

func TestDispatchPublishMiddlewareHandlers_fnErrorWrapsMiddlewareError(t *testing.T) {
	wantErr := errors.New("boom")
	h := events.ClientMiddlewareHandler{
		Name: "mw",
		Fn: func(ctx context.Context, msg dispatchMsg) (string, error) {
			return "", wantErr
		},
	}
	_, _, err := events.DispatchPublishMiddlewareHandlers(context.Background(), dispatchMsg{}, []events.ClientMiddlewareHandler{h})
	var mwErr events.MiddlewareError
	if !errors.As(err, &mwErr) {
		t.Fatalf("expected events.MiddlewareError, got %v", err)
	}
	if mwErr.Name != "mw" || !errors.Is(mwErr.Err, wantErr) {
		t.Fatalf("unexpected MiddlewareError: %+v", mwErr)
	}
}

func TestDispatchPublishMiddlewareHandlers_encodeOutError(t *testing.T) {
	wantErr := errors.New("encode failed")
	h := events.ClientMiddlewareHandler{
		Name: "mw",
		Fn: func(ctx context.Context, msg dispatchMsg) (string, error) {
			return "out", nil
		},
		EncodeOut: func(ctx context.Context, out any) (topicVars, propertyVars map[string]string, err error) {
			return nil, nil, wantErr
		},
	}
	_, _, err := events.DispatchPublishMiddlewareHandlers(context.Background(), dispatchMsg{}, []events.ClientMiddlewareHandler{h})
	info, ok := events.AsMiddlewareDispatchError(err)
	if !ok || !info.IsEncodeErr || !errors.Is(info.Err, wantErr) {
		t.Fatalf("unexpected dispatch info: %+v ok=%v", info, ok)
	}
}

func TestOverrideDerivedVars(t *testing.T) {
	derived := map[string]string{"a": "1", "b": "2"}
	explicit := map[string]string{"b": "override"}
	got := events.OverrideDerivedVars(derived, explicit)
	if got["a"] != "1" || got["b"] != "override" {
		t.Fatalf("unexpected merge result: %v", got)
	}
	if got := events.OverrideDerivedVars(nil, explicit); got["b"] != "override" {
		t.Fatalf("expected explicit passthrough when derived is empty, got %v", got)
	}
	if got := events.OverrideDerivedVars(derived, nil); got["a"] != "1" {
		t.Fatalf("expected derived passthrough when explicit is empty, got %v", got)
	}
}

func TestAsMiddlewareDispatchError_notADispatchError(t *testing.T) {
	if _, ok := events.AsMiddlewareDispatchError(errors.New("plain error")); ok {
		t.Fatal("expected ok=false for a plain error")
	}
}

// TestDispatchSubscribeMiddlewareHandlers_FailFast_SecondHandlerNeverRuns
// confirms the layers-within-a-stack fail-fast behavior (docs/roadmap/
// declarative-middleware-layering.md's Phase B model-review round —
// RESOLVED: fail-fast, confirmed as what Phase A's rest.
// DispatchMiddlewareHandlers already does) also holds for events: the
// SECOND handler's DecodeIn is never even called after the first fails.
func TestDispatchSubscribeMiddlewareHandlers_FailFast_SecondHandlerNeverRuns(t *testing.T) {
	first := events.MiddlewareHandler{
		Name: "first",
		DecodeIn: func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return nil, errors.New("first failed")
		},
	}
	secondCalled := false
	second := events.MiddlewareHandler{
		Name: "second",
		DecodeIn: func(ctx context.Context, topicVars, propertyVars map[string]string) (any, error) {
			secondCalled = true
			return "in", nil
		},
		Fn: func(ctx context.Context, msg *dispatchMsg, in string) error { return nil },
	}
	msg := &dispatchMsg{}
	_, err := events.DispatchSubscribeMiddlewareHandlers(context.Background(), msg, []events.MiddlewareHandler{first, second}, nil, nil)
	if err == nil {
		t.Fatal("want an error from the first handler")
	}
	if secondCalled {
		t.Error("want the SECOND handler's DecodeIn never called after the FIRST fails (fail-fast)")
	}
}

// TestDispatchPublishMiddlewareHandlers_FailFast_SecondHandlerNeverRuns
// mirrors the subscribe-side test for the publish direction.
func TestDispatchPublishMiddlewareHandlers_FailFast_SecondHandlerNeverRuns(t *testing.T) {
	first := events.ClientMiddlewareHandler{
		Name: "first",
		Fn:   func(ctx context.Context, msg dispatchMsg) (string, error) { return "", errors.New("first failed") },
		EncodeOut: func(ctx context.Context, out any) (map[string]string, map[string]string, error) {
			return nil, nil, nil
		},
	}
	secondCalled := false
	second := events.ClientMiddlewareHandler{
		Name: "second",
		Fn:   func(ctx context.Context, msg dispatchMsg) (string, error) { secondCalled = true; return "out", nil },
		EncodeOut: func(ctx context.Context, out any) (map[string]string, map[string]string, error) {
			return nil, nil, nil
		},
	}
	_, _, err := events.DispatchPublishMiddlewareHandlers(context.Background(), dispatchMsg{}, []events.ClientMiddlewareHandler{first, second})
	if err == nil {
		t.Fatal("want an error from the first handler")
	}
	if secondCalled {
		t.Error("want the SECOND handler's Fn never called after the FIRST fails (fail-fast)")
	}
}
