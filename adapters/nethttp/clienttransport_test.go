package nethttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── rest.Client.Call via Attach (Decision 5 / transport-agnostic-serve-interface) ──────────

func TestAttach_ClientCall_RoundTrip(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(userResp)
	if !ok {
		t.Fatalf("Call returned %T, want userResp", respAny)
	}
	if resp.Name != "Alice" || resp.ID != "1" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestAttach_SecondCall_ReturnsClientTransportAlreadyAttachedError(t *testing.T) {
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://example.com"); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	err := Attach(client, http.DefaultClient, "http://example.com")
	var alreadyErr rest.ClientTransportAlreadyAttachedError
	if !errors.As(err, &alreadyErr) {
		t.Fatalf("want ClientTransportAlreadyAttachedError, got %v (%T)", err, err)
	}
}

func TestClientCall_NoTransportAttached_ReturnsNoClientTransportAttachedError(t *testing.T) {
	client := rest.NewClient()
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	_, err := client.Call(context.Background(), route, createReq{})
	var noTransportErr rest.NoClientTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoClientTransportAttachedError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientCall_WrongRouteType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://example.com"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), "not-a-route", createReq{})
	var mismatchErr rest.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientCall_WrongReqType_ReturnsTransportTypeMismatchError(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://example.com"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), route, "wrong-type")
	var mismatchErr rest.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v (%T)", err, err)
	}
}

func TestAttach_ClientCall_NonSuccessStatus_ReturnsUnexpectedStatusError(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{}, errors.New("boom")
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("want UnexpectedStatusError, got %v (%T)", err, err)
	}
}

// ── Observer + ErrorPattern parity for Client.Attach ────────────────────────
//
// Confirmed gap (see docs/design/d-0001-rest-middleware-workflow-simplification.md's
// addendum): the reflection-based clientTransport.Call used to call
// neither stats.Observer NOR consult a declared ErrorPattern on non-2xx —
// these tests lock in the fix.

// TestAttach_ClientCall_RecordsObserver_Success confirms RecordRequest is
// called with the real status code on a successful round trip — mirrors
// TestCall_POST_HappyPath's escape-hatch equivalent.
func TestAttach_ClientCall_RecordsObserver_Success(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obs := &testObserver{}
	ctx := stats.WithObserver(context.Background(), obs)
	if _, err := client.Call(ctx, route, createReq{Name: "Alice"}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !obs.called {
		t.Fatal("RecordRequest was never called — Observer wiring regressed")
	}
	if obs.status != http.StatusCreated {
		t.Errorf("status = %d, want 201 (POST success)", obs.status)
	}
	if obs.method != http.MethodPost || obs.path != "/users" {
		t.Errorf("method/path = %q/%q, want POST//users", obs.method, obs.path)
	}
}

// TestAttach_ClientCall_RecordsObserver_NetworkFailure confirms RecordRequest
// is called with status 0 when the request never reaches a server —
// mirrors [callWithVars]'s own "status 0 = no HTTP request reached the
// network" convention.
func TestAttach_ClientCall_RecordsObserver_NetworkFailure(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	)
	client := rest.NewClient()
	// Port 0 on localhost — connection refused, no server listening.
	if err := Attach(client, http.DefaultClient, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	obs := &testObserver{}
	ctx := stats.WithObserver(context.Background(), obs)
	_, err := client.Call(ctx, route, createReq{Name: "Alice"})
	if err == nil {
		t.Fatal("expected a network error, got nil")
	}
	if !obs.called {
		t.Fatal("RecordRequest was never called on network failure")
	}
	if obs.status != 0 {
		t.Errorf("status = %d, want 0 (no request reached the network)", obs.status)
	}
}

// clientTransportErrPayload/clientTransportErrPayloadCodec mirror
// client_test.go's clientErrPayload/clientErrPayloadCodec for a route
// value usable directly with Client.Attach (a Route value, not a
// pre-built handle from a throwaway builder).
type clientTransportErrPayload struct {
	Code string `json:"code"`
}

func (e clientTransportErrPayload) Error() string { return "client error " + e.Code }

var clientTransportErrPayloadCodec = codex.Struct[clientTransportErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e clientTransportErrPayload) string { return e.Code },
		func(e *clientTransportErrPayload, v string) { e.Code = v },
	),
)

// TestAttach_ClientCall_ErrorPatternResponse_MatchedPattern confirms
// Client.Attach's Call consults a declared ErrorPattern on a non-2xx
// response, returning the typed ErrorPatternResponse instead of a bare
// UnexpectedStatusError — mirrors TestCall_ErrorPatternResponse_MatchedPattern's
// escape-hatch equivalent.
func TestAttach_ClientCall_ErrorPatternResponse_MatchedPattern(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/attach-call",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[clientTransportErrPayload, clientTransportErrPayload](http.StatusConflict, clientTransportErrPayloadCodec),
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"conflict"}`))
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var epr ErrorPatternResponse
	if !errors.As(err, &epr) {
		t.Fatalf("expected ErrorPatternResponse, got %T: %v", err, err)
	}
	if epr.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", epr.StatusCode)
	}
	payload, ok := epr.Value.(clientTransportErrPayload)
	if !ok {
		t.Fatalf("Value type = %T, want clientTransportErrPayload", epr.Value)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

// TestAttach_ClientCall_ErrorPatternResponse_NoMatch_FallsBackToUnexpectedStatus
// confirms an UNDECLARED status still falls back to UnexpectedStatusError,
// same as before this fix — additive-only, no behavior change for routes
// with no ErrorPattern.
func TestAttach_ClientCall_ErrorPatternResponse_NoMatch_FallsBackToUnexpectedStatus(t *testing.T) {
	route := rest.NewRoute[createReq, userResp]("POST", "/errors/attach-call-nomatch",
		createReqCodec, userRespCodec,
		rest.ErrorPattern[clientTransportErrPayload, clientTransportErrPayload](http.StatusConflict, clientTransportErrPayloadCodec),
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // NOT the declared 409
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	_, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	var epr ErrorPatternResponse
	if errors.As(err, &epr) {
		t.Fatalf("expected fallback to UnexpectedStatusError, got ErrorPatternResponse: %+v", epr)
	}
	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("want UnexpectedStatusError, got %v (%T)", err, err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", statusErr.StatusCode)
	}
}

// ── Client.Call honors the route's declared format (d-0001-rest-middleware-workflow-simplification.md Addendum 2) ──
//
// Confirmed gap (see docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 2): Client.Call's
// reflection shim used to ALWAYS assume JSON, silently ignoring a route's
// declared RequestFormats/Formats — this test locks in the fix
// (round-trips YAML request+response through Client.Call via Attach).
func TestAttach_ClientCall_HonorsDeclaredYAMLFormat(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
		rest.RequestFormats(format.YAML(createReqCodec)),
		rest.Formats(format.YAML(userRespCodec)),
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), route, createReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(userResp)
	if !ok {
		t.Fatalf("Call returned %T, want userResp", respAny)
	}
	if resp.Name != "Alice" || resp.ID != "1" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// ── Client.Call/Consume full ClientTransport parity (docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4) ──

func TestAttach_ClientCall_DerivesPathVars(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[getByIDReq, userResp]("GET", "/users/{id}",
		getByIDReqCodec, userRespCodec, rest.RouteMeta{OperationID: "getUser"},
		rest.NewPathParam("id", codex.String(),
			func(r getByIDReq) string { return r.ID },
			func(r *getByIDReq, v string) { r.ID = v }),
	).WithHandler(func(ctx context.Context, req getByIDReq) (userResp, error) {
		return userResp{ID: req.ID, Name: "Alice"}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), route, getByIDReq{ID: "42"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp := respAny.(userResp)
	if resp.ID != "42" {
		t.Errorf("want ID=42 (derived path var), got %+v", resp)
	}
}

func TestAttach_ClientCall_CredentialClientMW_Invoked(t *testing.T) {
	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	credCalled := false
	r := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).Use(declMw).HandleMW(&declMw, func(_ context.Context, req *http.Request, _ *getReq) (map[string][]string, error) {
		if req.Header.Get("Authorization") != "test-bearer-token" {
			return nil, errors.New("unauthorized")
		}
		return map[string][]string{"bearerAuth": nil}, nil
	}).ClientMW(&declMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	}).WithHandler(func(ctx context.Context, req getReq) (userResp, error) {
		return userResp{ID: "me"}, nil
	})
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), r, getReq{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !credCalled {
		t.Error("credential ClientMW was not invoked")
	}
	if respAny.(userResp).ID != "me" {
		t.Errorf("unexpected response: %+v", respAny)
	}
}

func TestAttach_ClientCall_GeneralPurposeClientMW_Wraps(t *testing.T) {
	s := rest.NewServer(testInfo)
	var wrapperRan bool
	r := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).ClientMW(nil, func(next func(context.Context, getReq) (userResp, error)) func(context.Context, getReq) (userResp, error) {
		return func(ctx context.Context, req getReq) (userResp, error) {
			wrapperRan = true
			return next(ctx, req)
		}
	}).WithHandler(func(ctx context.Context, req getReq) (userResp, error) {
		return userResp{ID: "me"}, nil
	})
	if err := r.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), r, getReq{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !wrapperRan {
		t.Error("general-purpose ClientMW Fn did not run")
	}
	if respAny.(userResp).ID != "me" {
		t.Errorf("unexpected response: %+v", respAny)
	}
}

func TestAttach_ClientCall_WithClientRequestResponseFormats_Overrides(t *testing.T) {
	s := rest.NewServer(testInfo)
	// Route declares JSON first (the default) AND YAML — the client-side
	// override below picks YAML specifically for THIS call, proving the
	// override actually takes effect rather than silently falling back to
	// the route's own first-declared default.
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
		rest.RequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec)),
		rest.Formats(format.JSON(userRespCodec), format.YAML(userRespCodec)),
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), route, createReq{Name: "Alice"}, rest.ClientCallOptions{
		RequestFormats:  []format.Format[createReq]{format.YAML(createReqCodec)},
		ResponseFormats: []format.Format[userResp]{format.YAML(userRespCodec)},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp := respAny.(userResp)
	if resp.Name != "Alice" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestAttach_ClientCall_BackwardCompatible_NoOptsStillWorks(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"},
	).WithHandler(func(ctx context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// No opts argument at all — locks in the additive, non-breaking contract.
	respAny, err := client.Call(context.Background(), route, createReq{Name: "Bob"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if respAny.(userResp).Name != "Bob" {
		t.Errorf("unexpected response: %+v", respAny)
	}
}

// ── Client.Consume (docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 4) ──

func newSecuredSSERouteForClientConsume() (rest.SSERoute[getReq, counterSSEEvent], *rest.Server) {
	s := rest.NewServer(testInfo)
	return rest.NewSSERoute[getReq, counterSSEEvent]("/sse/counter", getReqCodec, counterSSEEventCodec,
		rest.RouteMeta{OperationID: "streamCounter"},
	), s
}

type counterSSEEvent struct{ Count int }

var counterSSEEventCodec = codex.Struct[counterSSEEvent](
	codex.RequiredField("count", codex.Int(),
		func(e counterSSEEvent) int { return e.Count },
		func(e *counterSSEEvent, v int) { e.Count = v }),
)

func TestAttach_ClientConsume_RoundTrip(t *testing.T) {
	route, s := newSecuredSSERouteForClientConsume()
	route = route.WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		for i := 1; i <= 2; i++ {
			if err := send(counterSSEEvent{Count: i}); err != nil {
				return err
			}
		}
		return nil
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got []int
	err := client.Consume(ctx, route, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = append(got, e.Count)
		if len(got) >= 2 {
			cancel()
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("want [1 2], got %v", got)
	}
}

func TestAttach_ClientConsume_DerivesPathVars(t *testing.T) {
	s := rest.NewServer(testInfo)
	route := rest.NewSSERoute[sensorSSEReq, counterSSEEvent]("/sse/sensor/{id}",
		sensorSSEReqCodec, counterSSEEventCodec,
		rest.RouteMeta{OperationID: "streamSensor"},
		rest.NewPathParam("id", codex.String(),
			func(r sensorSSEReq) string { return r.ID },
			func(r *sensorSSEReq, v string) { r.ID = v }),
	).WithHandler(func(ctx context.Context, _ sensorSSEReq, send func(counterSSEEvent) error) error {
		// SSE server handlers never merge path/query/header/cookie values
		// into req (a zero-value Req is always passed) — read the raw
		// request instead, same as examples/adapters-sse's
		// handleSensorManualEscape does.
		r, _ := RequestFromContext(ctx)
		if r == nil || r.PathValue("id") != "room-42" {
			got := ""
			if r != nil {
				got = r.PathValue("id")
			}
			return errors.New("unexpected id: " + got)
		}
		return send(counterSSEEvent{Count: 1})
	})
	if err := route.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got int
	err := client.Consume(ctx, route, sensorSSEReq{ID: "room-42"}, func(_ context.Context, e counterSSEEvent) error {
		got = e.Count
		cancel()
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

type sensorSSEReq struct{ ID string }

var sensorSSEReqCodec = codex.Struct[sensorSSEReq](
	codex.OptionalField("id", codex.String(),
		func(r sensorSSEReq) string { return r.ID },
		func(r *sensorSSEReq, v string) { r.ID = v }),
)

func TestAttach_ClientConsume_NoTransportAttached_ReturnsNoClientTransportAttachedError(t *testing.T) {
	client := rest.NewClient()
	route, _ := newSecuredSSERouteForClientConsume()
	err := client.Consume(context.Background(), route, getReq{}, func(_ context.Context, _ counterSSEEvent) error { return nil })
	var noTransportErr rest.NoClientTransportAttachedError
	if !errors.As(err, &noTransportErr) {
		t.Fatalf("want NoClientTransportAttachedError, got %v", err)
	}
}

func TestAttach_ClientConsume_WrongRouteType_ReturnsTransportTypeMismatchError(t *testing.T) {
	client := rest.NewClient()
	if err := Attach(client, http.DefaultClient, "http://localhost"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	err := client.Consume(context.Background(), "not-a-route", getReq{}, func(_ context.Context, _ counterSSEEvent) error { return nil })
	var mismatchErr rest.TransportTypeMismatchError
	if !errors.As(err, &mismatchErr) {
		t.Fatalf("want TransportTypeMismatchError, got %v", err)
	}
}

func TestAttach_ClientConsume_CredentialClientMW_Invoked(t *testing.T) {
	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(route.Require("bearerAuth"))
	declMw := middleware.SecurityScheme("bearerAuth", route.BearerScheme("JWT"), nil, nil)
	credCalled := false
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/secured-counter", getReqCodec, counterSSEEventCodec).Use(declMw).HandleMW(&declMw, func(_ context.Context, req *http.Request, _ *getReq) (map[string][]string, error) {
		if req.Header.Get("Authorization") != "test-bearer-token" {
			return nil, errors.New("unauthorized")
		}
		return map[string][]string{"bearerAuth": nil}, nil
	}).ClientMW(&declMw, func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	}).WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		return send(counterSSEEvent{Count: 1})
	})
	if err := sseRoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got int
	err := client.Consume(ctx, sseRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = e.Count
		cancel()
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if !credCalled {
		t.Error("credential ClientMW was not invoked")
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestAttach_ClientConsume_GeneralPurposeClientMW_Wraps(t *testing.T) {
	s := rest.NewServer(testInfo)
	var wrapperRan bool
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/wrapped-counter", getReqCodec, counterSSEEventCodec).ClientMW(nil, func(next func(context.Context, counterSSEEvent) error) func(context.Context, counterSSEEvent) error {
		return func(ctx context.Context, e counterSSEEvent) error {
			wrapperRan = true
			return next(ctx, e)
		}
	}).WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		return send(counterSSEEvent{Count: 1})
	})
	if err := sseRoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got int
	err := client.Consume(ctx, sseRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = e.Count
		cancel()
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if !wrapperRan {
		t.Error("general-purpose ClientMW Fn did not run")
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestAttach_ClientConsume_WithFormats_Overrides(t *testing.T) {
	s := rest.NewServer(testInfo)
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/multi-format-counter", getReqCodec, counterSSEEventCodec,
		rest.Formats(format.JSON(counterSSEEventCodec), format.YAML(counterSSEEventCodec)),
	).WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		return send(counterSSEEvent{Count: 1})
	})
	if err := sseRoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := Attach(client, srv.Client(), srv.URL); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got int
	err := client.Consume(ctx, sseRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = e.Count
		cancel()
		return nil
	}, rest.ClientConsumeOptions{
		Formats: []format.Format[counterSSEEvent]{format.YAML(counterSSEEventCodec)},
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}
