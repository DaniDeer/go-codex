package nethttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
)

// ── HandleBoundMW (SSE): happy path, response header composition ────────

func TestHandleBoundMW_SSE_HappyPath_SetsResponseHeader(t *testing.T) {
	bm := rest.NewBoundMiddleware[createReq](newTDDeclaration("api-key-policy"),
		func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
			return tdOut{Value: "applied:" + in.Key}, nil
		}).
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
	route = route.HandleBoundMW(bm)
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

// ── HandleBoundMW (SSE): In-decode failure short-circuits with 400 ──────

func TestHandleBoundMW_SSE_InDecodeFailure_Returns400(t *testing.T) {
	handlerCalled := false
	bm := rest.NewBoundMiddleware[createReq](newTDDeclaration("api-key-policy"),
		func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
			return tdOut{Value: in.Key}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	route := rest.NewSSERoute[createReq, sseEvent]("/events2", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents2"},
	)
	route = route.HandleBoundMW(bm)
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

// ── HandleBoundMW (SSE): fn error falls back to rest.MiddlewareError ────

func TestHandleBoundMW_SSE_FnError_FallsBackToMiddlewareError(t *testing.T) {
	handlerCalled := false
	bm := rest.NewBoundMiddleware[createReq](newTDDeclaration("api-key-policy"),
		func(ctx context.Context, req *createReq, in tdIn) (tdOut, error) {
			return tdOut{}, errBadAPIKey
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	route := rest.NewSSERoute[createReq, sseEvent]("/events3", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents3"},
	)
	route = route.HandleBoundMW(bm)
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

// Rest-middleware-conflict-detection-improvements Candidate 3: the SSE
// middleware output-encode (EncodeOut) failure path now reports
// "middleware:out" via stats.ReportErrors, mirroring
// adapters/nethttp/transform_dispatch_test.go's non-SSE equivalent.
func TestHandleBoundMW_SSE_OutEncodeFailure_ReportsMiddlewareOutLocation(t *testing.T) {
	// In is tdEmpty (no required fields, so DecodeIn/InCodec.Validate
	// always succeeds) — isolating the failure to Out's EncodeOut path.
	decl := middleware.NewDeclaration("api-key-policy", tdEmptyCodec, tdOutCodec)
	bm := rest.NewBoundMiddleware[createReq](decl,
		func(ctx context.Context, req *createReq, in tdEmpty) (tdOut, error) {
			// Empty Value fails tdOutCodec's NonEmptyString refinement at
			// EncodeOut/OutCodec.Validate time.
			return tdOut{Value: ""}, nil
		}).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Applied", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		))
	route := rest.NewSSERoute[createReq, sseEvent]("/events-out-fail", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEventsOutFail"},
	)
	route = route.HandleBoundMW(bm)
	spy := &spyValidationObserver{}
	route = route.WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello"})
	}).HandleMW(nil, Observability(spy))
	mux := mustServeSSE(t, route, rest.NewServer(testInfo))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events-out-fail", nil)
	mux.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
	found := false
	for _, loc := range spy.locations {
		if loc == "middleware:out" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:out", spy.locations)
	}
	// The response body should now embed the failing middleware's Name
	// (rest.MiddlewareOutputError, wrapping the raw encode error).
	if !strings.Contains(rec.Body.String(), "api-key-policy") {
		t.Errorf("want response body to embed the middleware Name via MiddlewareOutputError, got %s", rec.Body.String())
	}
}

// ── D6(c): two SSE middlewares both writing the SAME *Req field —
// attachment-order, last-applied-wins ──

func TestHandleBoundMW_SSE_TwoMiddlewaresEnrichSameField_LastAttachedWins(t *testing.T) {
	bmFirst := rest.NewBoundMiddleware[createReq](newTDEmptyDeclaration("first-sse-policy"),
		func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
			req.Name = "first"
			return tdEmpty{}, nil
		})
	bmSecond := rest.NewBoundMiddleware[createReq](newTDEmptyDeclaration("second-sse-policy"),
		func(ctx context.Context, req *createReq, in tdEmpty) (tdEmpty, error) {
			req.Name = "second"
			return tdEmpty{}, nil
		})

	route := rest.NewSSERoute[createReq, sseEvent]("/events5", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEvents5"},
	)
	route = route.HandleBoundMW(bmFirst)
	route = route.HandleBoundMW(bmSecond)

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

// TestSSERoute_Use_AgnosticMiddleware_DispatchesOnBothRoutes exercises
// docs/design/d-0003-codec-declared-middlewares.md's route-agnostic
// reuse requirement for SSE: the SAME bundled `mw` value (WithReceive)
// attached via .Use() to TWO SSERoutes with DIFFERENT Req types, both
// dispatching correctly and independently — mirrors plain Route's own
// TestUse_AgnosticMiddleware_DispatchesOnBothRoutes exactly.
func TestSSERoute_Use_AgnosticMiddleware_DispatchesOnBothRoutes(t *testing.T) {
	callCount := 0
	mw := rest.NewMiddleware(newTDDeclaration("agnostic-sse-reuse-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithReceive(func(ctx context.Context, in tdIn) (tdOut, error) {
			callCount++
			return tdOut{Value: in.Key}, nil
		})

	routeA := rest.NewSSERoute[createReq, sseEvent]("/events-agnostic-a", createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEventsAgnosticA"},
	).Use(mw).WithHandler(func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello-a"})
	})
	routeB := rest.NewSSERoute[getReq, sseEvent]("/events-agnostic-b", getReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamEventsAgnosticB"},
	).Use(mw).WithHandler(func(ctx context.Context, _ getReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hello-b"})
	})

	hA := mustServeSSE(t, routeA, rest.NewServer(testInfo))
	recA := httptest.NewRecorder()
	rA := httptest.NewRequest(http.MethodGet, "/events-agnostic-a", nil)
	rA.Header.Set("X-Api-Key", "key-a")
	hA.ServeHTTP(recA, rA)
	if recA.Code != http.StatusOK {
		t.Fatalf("route A: want 200, got %d: %s", recA.Code, recA.Body.String())
	}

	hB := mustServeSSE(t, routeB, rest.NewServer(testInfo))
	recB := httptest.NewRecorder()
	rB := httptest.NewRequest(http.MethodGet, "/events-agnostic-b", nil)
	rB.Header.Set("X-Api-Key", "key-b")
	hB.ServeHTTP(recB, rB)
	if recB.Code != http.StatusOK {
		t.Fatalf("route B: want 200, got %d: %s", recB.Code, recB.Body.String())
	}

	if callCount != 2 {
		t.Errorf("want mw's bundled receiveFn called once per SSERoute (2 total), got %d", callCount)
	}
}

// ── docs/design/d-0007-declarative-middleware-layering.md's Rollout Phase A:
// SSERoute.HandleMW's bound path + CheckCoverage + GrantedScopes end to
// end — the SSE mirror of
// TestHandleMW_CodecBackedMiddleware_Satisfies_CoversGlobalSecurity
// (transform_dispatch_test.go).

func TestSSERoute_HandleMW_CodecBackedMiddleware_Satisfies_CoversGlobalSecurity(t *testing.T) {
	decl := middleware.NewDeclaration("bearer-handlemw-sse-policy", tdInCodec, bearerAuthOutCodec)
	decl.Security = middleware.NewSecurityDeclaration("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	bm := rest.NewBoundMiddleware[getReq](decl,
		func(ctx context.Context, req *getReq, in tdIn) (bearerAuthOut, error) {
			if in.Key != "valid-token" {
				return bearerAuthOut{}, errors.New("invalid bearer token")
			}
			return bearerAuthOut{GrantedScopes: map[string][]string{"bearerAuth": nil}}, nil
		}).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		))

	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(route.Require("bearerAuth"))
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/secure-counter", getReqCodec, counterSSEEventCodec).
		HandleBoundMW(bm).
		WithHandler(func(ctx context.Context, req getReq, send func(counterSSEEvent) error) error {
			return send(counterSSEEvent{Count: 1})
		})

	mux := mustServeSSE(t, sseRoute, s)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/sse/secure-counter", nil)
	r.Header.Set("Authorization", "valid-token")
	mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 for a valid token, got %d: %s", rec.Code, rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/sse/secure-counter", nil)
	r2.Header.Set("Authorization", "wrong-token")
	mux.ServeHTTP(rec2, r2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for an invalid token, got %d: %s", rec2.Code, rec2.Body.String())
	}
}
