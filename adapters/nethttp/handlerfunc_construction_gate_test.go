package nethttp

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// TestHandlerFunc_MissingSecurityCoverage_Panics is Phase 4 (REST family
// step 6)'s regression test: handlerFunc (the generic, port/stream-
// binding dispatch function behind IngestAdapter/HandlerLatest/
// PipelineHandler/etc.) previously ran NO construction-time check at
// all — a route declaring a security scheme via .Use() but never
// attaching any implementation for it would silently serve, failing
// closed on every request at runtime with no clear signal why (the EXACT
// gap TestServeOne_MissingSecurityCoverage_RejectedAtServeTime already
// guards for buildRouteHandler/serve.go's path). Confirms handlerFunc now
// PANICS with a restdispatch.ConstructionError wrapping the SAME
// rest.MissingSecurityMiddlewareError buildRouteHandler would have
// returned as a plain error — the only mechanism available given
// handlerFunc's signature has no error-return path to extend.
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
	// ...but NO .HandleBoundMW() call ever attaches an implementation for
	// it — handle.Implementations is empty, mirroring
	// TestServeOne_MissingSecurityCoverage_RejectedAtServeTime's setup
	// exactly.

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

// TestHandlerFunc_ValidConstruction_DoesNotPanic is a companion
// regression guard: a route with NO construction-time issues must NOT
// panic — the gate is additive, not a behavior change for the common
// case.
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
