package rest_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
)

type dispatchReq struct{ Val string }

func TestDispatchMiddlewareHandlers_success(t *testing.T) {
	h := rest.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(headerVars, cookieVars, queryVars map[string]string) (any, error) {
			return headerVars["X-Key"], nil
		},
		Fn: func(ctx context.Context, req *dispatchReq, in string) (string, error) {
			return "out-" + in, nil
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{Val: "x"})
	outs, err := rest.DispatchMiddlewareHandlers(context.Background(), reqPtr, []rest.MiddlewareHandler{h}, map[string]string{"X-Key": "42"}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outs) != 1 || outs[0].(string) != "out-42" {
		t.Fatalf("unexpected outs: %v", outs)
	}
}

func TestDispatchMiddlewareHandlers_emptyHandlers(t *testing.T) {
	reqPtr := reflect.ValueOf(&dispatchReq{})
	outs, err := rest.DispatchMiddlewareHandlers(context.Background(), reqPtr, nil, nil, nil, nil)
	if err != nil || outs != nil {
		t.Fatalf("expected nil, nil for empty handlers, got %v, %v", outs, err)
	}
}

func TestDispatchMiddlewareHandlers_decodeInFailure(t *testing.T) {
	wantErr := errors.New("decode failed")
	h := rest.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(headerVars, cookieVars, queryVars map[string]string) (any, error) {
			return nil, wantErr
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{})
	_, err := rest.DispatchMiddlewareHandlers(context.Background(), reqPtr, []rest.MiddlewareHandler{h}, nil, nil, nil)
	info, ok := rest.AsMiddlewareDispatchError(err)
	if !ok || info.IsFnError || info.Name != "mw" || !errors.Is(info.Err, wantErr) {
		t.Fatalf("unexpected dispatch info: %+v ok=%v", info, ok)
	}
}

func TestDispatchMiddlewareHandlers_fnFailure(t *testing.T) {
	wantErr := errors.New("fn failed")
	h := rest.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(headerVars, cookieVars, queryVars map[string]string) (any, error) {
			return "in", nil
		},
		Fn: func(ctx context.Context, req *dispatchReq, in string) (string, error) {
			return "", wantErr
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{})
	_, err := rest.DispatchMiddlewareHandlers(context.Background(), reqPtr, []rest.MiddlewareHandler{h}, nil, nil, nil)
	info, ok := rest.AsMiddlewareDispatchError(err)
	if !ok || !info.IsFnError || info.Name != "mw" || !errors.Is(info.Err, wantErr) {
		t.Fatalf("unexpected dispatch info: %+v ok=%v", info, ok)
	}
}

func TestAsMiddlewareDispatchError_notADispatchError(t *testing.T) {
	if _, ok := rest.AsMiddlewareDispatchError(errors.New("plain")); ok {
		t.Fatal("expected ok=false for a plain error")
	}
}

func TestCallObserveErrorResponseFor(t *testing.T) {
	h := newObserveTestRoute(t)
	spy := &errorPatternObserverSpy{}
	resp, matched, applyErr := rest.CallObserveErrorResponseFor(reflect.ValueOf(h), context.Background(), spy, directPatternError{Code: "boom"})
	if applyErr != nil {
		t.Fatalf("applyErr: %v", applyErr)
	}
	if !matched {
		t.Fatal("expected a match")
	}
	if resp.Status != 409 {
		t.Fatalf("unexpected status: %d", resp.Status)
	}
}
