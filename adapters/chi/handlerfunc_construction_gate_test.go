package chi

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// TestHandlerFunc_MissingSecurityCoverage_Panics mirrors
// adapters/nethttp's identically-named test — see its own doc comment
// for the full rationale: handlerFunc (the generic, port/stream-binding
// dispatch function) previously ran NO construction-time check at all;
// it now PANICS with a restdispatch.ConstructionError wrapping the SAME
// rest.MissingSecurityMiddlewareError buildRouteHandler would have
// returned as a plain error.
func TestHandlerFunc_MissingSecurityCoverage_Panics(t *testing.T) {
	b := rest.NewServer(testInfo)
	secMw := rest.SecurityMiddleware[struct{}, struct{}]("bearerAuth", rest.BearerScheme("JWT"), nil)
	handle, err := rest.NewRoute[createReq, struct{}]("POST", "/ingest",
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "ingest"}).
		Use(secMw). // declares "bearerAuth" is required...
		RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	// ...but NO .HandleBoundMW() call ever attaches an implementation for it.

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("want handlerFunc to panic on missing security coverage, it did not")
		}
		constructErr, ok := r.(restdispatch.ConstructionError)
		if !ok {
			t.Fatalf("want restdispatch.ConstructionError, got %T: %v", r, r)
		}
		var missingErr rest.MissingSecurityMiddlewareError
		if !errors.As(constructErr, &missingErr) {
			t.Fatalf("want ConstructionError wrapping MissingSecurityMiddlewareError, got %v (%T)", constructErr.Err, constructErr.Err)
		}
	}()

	_ = handlerFunc(handle, func(_ context.Context, _ createReq) (struct{}, error) {
		t.Fatal("handler must never run — construction-time gate should have panicked first")
		return struct{}{}, nil
	}, Options{}, handle.Implementations...)
}

// TestHandlerFunc_ValidConstruction_DoesNotPanic mirrors
// adapters/nethttp's identically-named companion regression guard.
func TestHandlerFunc_ValidConstruction_DoesNotPanic(t *testing.T) {
	b := rest.NewServer(testInfo)
	handle, err := rest.NewRoute[createReq, struct{}]("POST", "/ingest",
		createReqCodec, codex.Struct[struct{}](), rest.RouteMeta{OperationID: "ingest"}).
		RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("want no panic for a valid route, got %v", r)
		}
	}()

	_ = handlerFunc(handle, func(_ context.Context, _ createReq) (struct{}, error) {
		return struct{}{}, nil
	}, Options{}, handle.Implementations...)
}
