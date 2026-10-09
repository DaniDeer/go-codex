package reqreply_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/validate"
)

type dispatchReq struct{ Val string }

func TestDispatchServerMiddlewareHandlers_success(t *testing.T) {
	h := reqreply.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(_ context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return topicVars["id"], nil
		},
		Fn: func(ctx context.Context, req *dispatchReq, in string) (string, error) {
			return "ack-" + in, nil
		},
		EncodeOut: func(_ context.Context, out any) (topicVars, propertyVars map[string]string, err error) {
			return map[string]string{"ack": out.(string)}, nil, nil
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{Val: "x"})
	outTopicVars, _, _, name, failKind, err := reqreply.DispatchServerMiddlewareHandlers(context.Background(), reqPtr, []reqreply.MiddlewareHandler{h}, map[string]string{"id": "7"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "" || failKind != "" {
		t.Fatalf("expected empty name/failKind on success, got name=%q failKind=%q", name, failKind)
	}
	if outTopicVars["ack"] != "ack-7" {
		t.Fatalf("unexpected outTopicVars: %v", outTopicVars)
	}
}

func TestDispatchServerMiddlewareHandlers_decodeInFailure(t *testing.T) {
	wantErr := errors.New("decode failed")
	h := reqreply.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(_ context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return nil, wantErr
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{})
	_, _, _, name, failKind, err := reqreply.DispatchServerMiddlewareHandlers(context.Background(), reqPtr, []reqreply.MiddlewareHandler{h}, nil, nil)
	if failKind != "in" || name != "mw" || !errors.Is(err, wantErr) {
		t.Fatalf("unexpected result: name=%q failKind=%q err=%v", name, failKind, err)
	}
}

func TestDispatchServerMiddlewareHandlers_fnFailureWrapsMiddlewareError(t *testing.T) {
	wantErr := errors.New("boom")
	h := reqreply.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(_ context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return "in", nil
		},
		Fn: func(ctx context.Context, req *dispatchReq, in string) (string, error) {
			return "", wantErr
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{})
	_, _, _, name, failKind, err := reqreply.DispatchServerMiddlewareHandlers(context.Background(), reqPtr, []reqreply.MiddlewareHandler{h}, nil, nil)
	if failKind != "fn" || name != "mw" {
		t.Fatalf("unexpected classification: name=%q failKind=%q", name, failKind)
	}
	var mwErr reqreply.MiddlewareError
	if !errors.As(err, &mwErr) || !errors.Is(mwErr.Err, wantErr) {
		t.Fatalf("expected wrapped MiddlewareError, got %v", err)
	}
}

func TestDispatchServerMiddlewareHandlers_encodeOutFailure(t *testing.T) {
	wantErr := errors.New("encode failed")
	h := reqreply.MiddlewareHandler{
		Name: "mw",
		DecodeIn: func(_ context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return "in", nil
		},
		Fn: func(ctx context.Context, req *dispatchReq, in string) (string, error) {
			return "out", nil
		},
		EncodeOut: func(_ context.Context, out any) (topicVars, propertyVars map[string]string, err error) {
			return nil, nil, wantErr
		},
	}
	reqPtr := reflect.ValueOf(&dispatchReq{})
	_, _, _, name, failKind, err := reqreply.DispatchServerMiddlewareHandlers(context.Background(), reqPtr, []reqreply.MiddlewareHandler{h}, nil, nil)
	if failKind != "out" || name != "mw" || !errors.Is(err, wantErr) {
		t.Fatalf("unexpected result: name=%q failKind=%q err=%v", name, failKind, err)
	}
}

func TestDispatchClientMiddlewareIn_success(t *testing.T) {
	h := reqreply.ClientMiddlewareHandler{
		Name: "mw",
		Fn: func(ctx context.Context, req dispatchReq) (string, error) {
			return req.Val, nil
		},
		EncodeIn: func(_ context.Context, in any) (topicVars, propertyVars map[string]string, err error) {
			return map[string]string{"id": in.(string)}, nil, nil
		},
	}
	topicVars, _, name, err := reqreply.DispatchClientMiddlewareIn(context.Background(), reflect.ValueOf(dispatchReq{Val: "9"}), []reqreply.ClientMiddlewareHandler{h})
	if err != nil || name != "" {
		t.Fatalf("unexpected result: name=%q err=%v", name, err)
	}
	if topicVars["id"] != "9" {
		t.Fatalf("unexpected topicVars: %v", topicVars)
	}
}

func TestDispatchClientMiddlewareOut_decodeFailurePropagates(t *testing.T) {
	wantErr := errors.New("decode out failed")
	h := reqreply.ClientMiddlewareHandler{
		Name: "mw",
		DecodeOut: func(_ context.Context, topicVars, propertyVars map[string]string) (any, error) {
			return nil, wantErr
		},
	}
	err := reqreply.DispatchClientMiddlewareOut(context.Background(), nil, nil, []reqreply.ClientMiddlewareHandler{h})
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr, got %v", err)
	}
}

func TestMergeVarsOverride(t *testing.T) {
	dst := map[string]string{"a": "1", "b": "2"}
	src := map[string]string{"b": "override"}
	got := reqreply.MergeVarsOverride(dst, src)
	if got["a"] != "1" || got["b"] != "override" {
		t.Fatalf("unexpected merge: %v", got)
	}
	if got := reqreply.MergeVarsOverride(dst, nil); got["a"] != "1" || got["b"] != "2" {
		t.Fatalf("expected dst unchanged when src is empty, got %v", got)
	}
}

// TestDispatchClientMiddlewareIn_EncodeInFailure_WrapsMiddlewareInputError
// is a REGRESSION GUARD: buildEncodeIn (transform.go) — the client-side,
// request-ENCODE-direction sibling of buildDecodeIn/buildEncodeOut/
// buildDecodeOut — was the ONLY one of the 4 build* functions that did
// NOT wrap its errors in a typed reqreply.Middleware*Error. A failing
// InCodec.Validate (or merge-field encode failure) on the client's own
// middleware-contributed In value propagated as a bare, unwrapped error,
// with no way for a caller to recover which middleware failed via
// errors.As — unlike buildDecodeIn (wraps MiddlewareInputError),
// buildEncodeOut/buildDecodeOut (both wrap MiddlewareOutputError).
func TestDispatchClientMiddlewareIn_EncodeInFailure_WrapsMiddlewareInputError(t *testing.T) {
	type mdIn struct{ Val string }
	mw := reqreply.NewMiddleware(middleware.Declaration[mdIn, struct{}]{
		Name:    "mw",
		InCodec: codex.Struct[mdIn](codex.RequiredField("val", codex.String().Refine(validate.NonEmptyString), func(i mdIn) string { return i.Val }, func(i *mdIn, v string) { i.Val = v })),
	}).WithSend(func(ctx context.Context) (mdIn, error) { return mdIn{Val: ""}, nil })

	r := newMWTestRoute().Use(mw)
	b := reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"})
	h, err := r.Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(h.ClientMiddlewareHandlers) != 1 {
		t.Fatalf("want 1 ClientMiddlewareHandler, got %d", len(h.ClientMiddlewareHandlers))
	}

	_, _, name, dispatchErr := reqreply.DispatchClientMiddlewareIn(context.Background(), reflect.ValueOf(computeReq{}), h.ClientMiddlewareHandlers)
	if dispatchErr == nil {
		t.Fatal("want an error from the failing InCodec.Validate, got nil")
	}
	var inputErr reqreply.MiddlewareInputError
	if !errors.As(dispatchErr, &inputErr) {
		t.Fatalf("want errors.As to reqreply.MiddlewareInputError, got %T: %v", dispatchErr, dispatchErr)
	}
	if inputErr.Name != "mw" || name != "mw" {
		t.Errorf("want middleware name %q, got inputErr.Name=%q name=%q", "mw", inputErr.Name, name)
	}
}
