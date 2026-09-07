package chi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// ── TransformSSE: happy path, response header composition ───────────────

func TestTransformSSE_HappyPath_SetsResponseHeader(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Applied", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))

	route := rest.NewSSERoute[createReq, sseEvent]("/events", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents"},
	)
	route = rest.TransformSSE(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: "applied:" + in.Key}, nil
	})
	route = route.WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	r.Header.Set("X-Api-Key", "secret123")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Policy-Applied"); got != "applied:secret123" {
		t.Errorf("want X-Policy-Applied %q, got %q", "applied:secret123", got)
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Errorf("want event body to contain hello, got %s", rec.Body.String())
	}
}

// ── TransformSSE: In-decode failure short-circuits with 400 ──────────────

func TestTransformSSE_InDecodeFailure_Returns400(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	handlerCalled := false
	route := rest.NewSSERoute[createReq, sseEvent]("/events2", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents2"},
	)
	route = rest.TransformSSE(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{Value: in.Key}, nil
	})
	route = route.WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		handlerCalled = true
		return send(sseEvent{Message: "hello"})
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events2", nil)
	// X-Api-Key deliberately omitted.
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if handlerCalled {
		t.Error("want handler NOT called when middleware In-decode fails")
	}
}

// ── TransformSSE: fn error falls back to rest.MiddlewareError ───────────

func TestTransformSSE_FnError_FallsBackToMiddlewareError(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	handlerCalled := false
	route := rest.NewSSERoute[createReq, sseEvent]("/events3", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents3"},
	)
	route = rest.TransformSSE(route, mw, func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
		return tdOut{}, errBadAPIKey
	})
	route = route.WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		handlerCalled = true
		return send(sseEvent{Message: "hello"})
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events3", nil)
	r.Header.Set("X-Api-Key", "bad-key")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 (rest.MiddlewareError default status), got %d: %s", rec.Code, rec.Body.String())
	}
	if handlerCalled {
		t.Error("want handler NOT called when middleware fn returns an error")
	}
}

// ── D6(c): two SSE middlewares both writing the SAME *Req field —
// attachment-order, last-applied-wins ──

func TestTransformSSE_TwoMiddlewaresEnrichSameField_LastAttachedWins(t *testing.T) {
	mwFirst := rest.NewMiddleware(newTDEmptyDeclaration("first-sse-policy"))
	mwSecond := rest.NewMiddleware(newTDEmptyDeclaration("second-sse-policy"))

	route := rest.NewSSERoute[createReq, sseEvent]("/events5", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents5"},
	)
	route = rest.TransformSSE(route, mwFirst, func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
		req.Name = "first"
		return tdEmpty{}, nil
	})
	route = rest.TransformSSE(route, mwSecond, func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
		req.Name = "second"
		return tdEmpty{}, nil
	})

	var receivedName string
	route = route.WithHandler(func(ctx context.Context, req createReq, send func(sseEvent) error) error {
		receivedName = req.Name
		return send(sseEvent{Message: "hello"})
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events5", nil)
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if receivedName != "second" {
		t.Errorf("want last-attached middleware's write to win (%q), got %q", "second", receivedName)
	}
}

// ── Route-agnostic .Use(mw) dispatch on SSERoute ─────────────────────────

func TestSSERoute_Use_AgnosticMiddleware_Dispatches(t *testing.T) {
	callCount := 0
	mw := rest.NewMiddleware(newTDDeclaration("agnostic-sse-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithReceive(func(ctx context.Context, in tdIn) (tdOut, error) {
			callCount++
			return tdOut{Value: in.Key}, nil
		})

	route := rest.NewSSERoute[createReq, sseEvent]("/events4", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents4"},
	).Use(mw).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	})
	handler := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events4", nil)
	r.Header.Set("X-Api-Key", "secret")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if callCount != 1 {
		t.Errorf("want bundled receiveFn called exactly once, got %d", callCount)
	}
}
