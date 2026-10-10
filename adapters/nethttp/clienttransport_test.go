package nethttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── rest.Client.Call via Attach (Decision 5 / d-0001 Addendum 5) ──────────

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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: "http://example.com"})); err != nil {
		t.Fatalf("first Attach: %v", err)
	}
	err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: "http://example.com"}))
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: "http://example.com"})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: "http://example.com"})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: "http://127.0.0.1:1"})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	s.AddGlobalSecurity(rest.Require("bearerAuth"))
	credCalled := false
	boundMW := rest.BoundSecurityClientMiddleware[getReq, securedAuthIn, securedAuthOut](
		"bearerAuth", rest.BearerScheme("JWT"), nil,
		func(ctx context.Context, req getReq) (securedAuthIn, error) {
			credCalled = true
			return securedAuthIn{Token: "test-bearer-token"}, nil
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in securedAuthIn) string { return in.Token },
		func(in *securedAuthIn, v string) { in.Token = v },
	))
	serverBm := boundBearerAuthMw[getReq]("bearerAuth", func(_ context.Context, auth string) (map[string][]string, error) {
		if auth != "test-bearer-token" {
			return nil, errors.New("unauthorized")
		}
		return map[string][]string{"bearerAuth": nil}, nil
	})
	// TWO SEPARATE route values, same path/method — a Security-carrying
	// HandleBoundMW (server) and BoundClientMiddleware (client) for the
	// SAME scheme name cannot share ONE route value (both default to the
	// SAME auto-generated Declaration.Name "declare-security:bearerAuth",
	// and D6(b)'s name-uniqueness check rejects the resulting duplicate)
	// — see bound_middleware.go's [BoundMiddleware] doc comment.
	serverRoute := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).HandleBoundMW(serverBm).WithHandler(func(ctx context.Context, req getReq) (userResp, error) {
		return userResp{ID: "me"}, nil
	})
	if err := serverRoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	clientRoute := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).ClientBoundMW(boundMW)

	mux := http.NewServeMux()
	if err := serve(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), clientRoute, getReq{})
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

// ── Call: GlobalSecurity-only dual-mode dispatch (docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 6) ──

// TestCall_GlobalSecurityOnly_RouteHandle_CredentialInvoked proves
// Client.Call's existing dual-mode dispatch (recoverClientRouteHandleValue)
// resolves a route protected ONLY by Server.AddGlobalSecurity (no
// per-route .Use()/Security declared at all) when passed an
// already-registered *RouteHandle — confirming the GlobalSecurity
// fallback in resolveClientSecurity actually fires end-to-end, not just
// in theory.
//
// The credential ClientMW is attached general-purpose (nil mw, no
// Satisfies) — attaching a SCHEME-gated ClientMW would itself require a
// matching .Use() declaration (UnknownMiddlewareImplementationError),
// which would populate per-route Security and defeat the point of this
// test; a general-purpose credential Fn only runs when secReqs is
// non-empty (mergeCredentialHeaders is gated by len(secReqs) > 0 in
// [clientTransport.Call]), which is exactly the GlobalSecurity fallback
// this test targets.
//
// The test server is built directly via [httptest.NewServer], NOT via
// [serve]/[rest.Server.Serve] — the real nethttp.Serve dispatch runs
// [rest.CheckCoverage] at Serve time, which requires a SERVER-side
// HandleMW satisfying any scheme in GlobalSecurity on EVERY route (a
// separate, server-side concern unrelated to what this test verifies);
// this test only exercises CLIENT-side credential resolution, mirroring
// how [TestCall_CredentialFunc_Invoked] (client_test.go) already does
// the same for the per-route-Security case.
func TestCall_GlobalSecurityOnly_RouteHandle_CredentialInvoked(t *testing.T) {
	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(rest.Require("bearerAuth"))
	credCalled := false
	// Deliberately NO .Use(...)/.HandleMW(...) — Descriptor.Security
	// stays nil, so secReqs can ONLY come from the GlobalSecurity
	// fallback, never from per-route Security.
	r := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).ClientMW(nil, func(ctx context.Context, reqs []rest.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	})
	handle, err := r.RegisterHandle(s)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "test-bearer-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	respAny, err := client.Call(context.Background(), handle, getReq{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !credCalled {
		t.Error("credential ClientMW was not invoked — GlobalSecurity fallback did not fire for a *RouteHandle")
	}
	if respAny.(userResp).ID != "me" {
		t.Errorf("unexpected response: %+v", respAny)
	}
}

// TestCall_GlobalSecurityOnly_RawRoute_CredentialNotInvoked documents
// that the accepted limitation is preserved: a RAW, unregistered Route
// (not a *RouteHandle) still cannot see GlobalSecurity, since
// Route.ClientHandle() always builds a handle with GlobalSecurity nil —
// so even a general-purpose credential ClientMW (which would fire the
// instant secReqs is non-empty) never runs via the raw-Route path. See
// [TestCall_GlobalSecurityOnly_RouteHandle_CredentialInvoked]'s doc
// comment for why the test server is built directly rather than via
// [serve].
func TestCall_GlobalSecurityOnly_RawRoute_CredentialNotInvoked(t *testing.T) {
	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(rest.Require("bearerAuth"))
	credCalled := false
	r := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).ClientMW(nil, func(ctx context.Context, reqs []rest.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	})
	if _, err := r.RegisterHandle(s); err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	// Pass the RAW route, not the handle — GlobalSecurity must stay
	// invisible, same as always.
	respAny, err := client.Call(context.Background(), r, getReq{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if credCalled {
		t.Error("credential ClientMW was invoked — expected GlobalSecurity to stay invisible for a raw Route")
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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

// TestAttach_ClientCall_CodecBackedClientMW_EncodesInAndDecodesOut proves
// [clientTransport.Call] (the Attach+Call workflow, reached via reflection
// against *RouteHandle) dispatches a .Use()-attached codec-backed
// Middleware[In,Out]'s OWN EncodeIn into the outgoing request's header AND
// decodes its Out from the response header — a confirmed, previously
// missing gap (docs/design/d-0007-declarative-middleware-layering.md's Rollout
// Phase A): Call never dispatched handle.ClientMiddlewareHandlers at all,
// only the legacy handle.ClientImplementations (credential/general-purpose
// ClientMW).
func TestAttach_ClientCall_CodecBackedClientMW_EncodesInAndDecodesOut(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Version", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		)).
		WithSend(func(ctx context.Context) (tdIn, error) {
			return tdIn{Key: "secret-abc"}, nil
		})

	route := rest.NewRoute[getReq, userResp]("GET", "/me", getReqCodec, userRespCodec).Use(mw)

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Header().Set("X-Policy-Version", "v1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"id":"me","name":""}`)
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx := WithClientMiddlewareOut(context.Background())
	respAny, err := client.Call(ctx, route, getReq{})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if respAny.(userResp).ID != "me" {
		t.Errorf("unexpected response: %+v", respAny)
	}
	if gotHeader != "secret-abc" {
		t.Errorf("want X-Api-Key %q sent, got %q", "secret-abc", gotHeader)
	}

	outs := ClientMiddlewareOutFromContext(ctx)
	out, ok := outs["api-key-policy"].(tdOut)
	if !ok {
		t.Fatalf("want decoded tdOut for api-key-policy, got %+v", outs)
	}
	if out.Value != "v1" {
		t.Errorf("want decoded Out.Value %q, got %q", "v1", out.Value)
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: "http://localhost"})); err != nil {
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
	s.AddGlobalSecurity(rest.Require("bearerAuth"))
	credCalled := false
	boundMW := rest.BoundSecurityClientMiddleware[getReq, securedAuthIn, securedAuthOut](
		"bearerAuth", rest.BearerScheme("JWT"), nil,
		func(ctx context.Context, req getReq) (securedAuthIn, error) {
			credCalled = true
			return securedAuthIn{Token: "test-bearer-token"}, nil
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in securedAuthIn) string { return in.Token },
		func(in *securedAuthIn, v string) { in.Token = v },
	))
	serverBm := boundBearerAuthMw[getReq]("bearerAuth", func(_ context.Context, auth string) (map[string][]string, error) {
		if auth != "test-bearer-token" {
			return nil, errors.New("unauthorized")
		}
		return map[string][]string{"bearerAuth": nil}, nil
	})
	// TWO SEPARATE route values, same path — see
	// TestAttach_ClientCall_CredentialClientMW_Invoked's identical note.
	serverSSERoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/secured-counter", getReqCodec, counterSSEEventCodec).HandleBoundMW(serverBm).WithHandler(func(ctx context.Context, _ getReq, send func(counterSSEEvent) error) error {
		return send(counterSSEEvent{Count: 1})
	})
	if err := serverSSERoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/secured-counter", getReqCodec, counterSSEEventCodec).ClientBoundMW(boundMW)

	mux := http.NewServeMux()
	if err := serveSSE(mux, s); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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

// ── Consume: GlobalSecurity-only dual-mode dispatch (docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 6) ──

// TestConsume_GlobalSecurityOnly_RouteHandle_CredentialInvoked proves
// Client.Consume's dual-mode dispatch (recoverClientSSERouteHandleValue)
// resolves an SSE route protected ONLY by Server.AddGlobalSecurity (no
// per-route .Use()/Security declared at all) when passed an
// already-registered *SSERouteHandle — mirrors
// [TestCall_GlobalSecurityOnly_RouteHandle_CredentialInvoked] exactly,
// for the SSE side of the same fix. See that test's doc comment for why
// the credential ClientMW is general-purpose (nil mw) and why the test
// server is built directly rather than via [serveSSE] (CheckCoverage
// would otherwise require a server-side HandleMW, an orthogonal concern
// to what this test verifies).
func TestConsume_GlobalSecurityOnly_RouteHandle_CredentialInvoked(t *testing.T) {
	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(rest.Require("bearerAuth"))
	credCalled := false
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/counter", getReqCodec, counterSSEEventCodec).ClientMW(nil, func(ctx context.Context, reqs []rest.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	})
	handle, err := sseRoute.RegisterHandle(s)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"count\":1}\n\n")) //nolint:errcheck
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got int
	err = client.Consume(ctx, handle, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = e.Count
		cancel()
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if !credCalled {
		t.Error("credential ClientMW was not invoked — GlobalSecurity fallback did not fire for a *SSERouteHandle")
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

// TestConsume_GlobalSecurityOnly_RawRoute_CredentialNotInvoked documents
// that the accepted limitation is preserved: a RAW, unregistered
// SSERoute (not a *SSERouteHandle) still cannot see GlobalSecurity,
// since SSERoute.ClientHandle() always builds a handle with
// GlobalSecurity nil.
func TestConsume_GlobalSecurityOnly_RawRoute_CredentialNotInvoked(t *testing.T) {
	s := rest.NewServer(testInfo)
	s.AddGlobalSecurity(rest.Require("bearerAuth"))
	credCalled := false
	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/counter", getReqCodec, counterSSEEventCodec).ClientMW(nil, func(ctx context.Context, reqs []rest.SecurityRequirement) (http.Header, error) {
		credCalled = true
		h := make(http.Header)
		h.Set("Authorization", "test-bearer-token")
		return h, nil
	})
	if _, err := sseRoute.RegisterHandle(s); err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"count\":1}\n\n")) //nolint:errcheck
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var got int
	// Pass the RAW sseRoute, not the handle — GlobalSecurity must stay
	// invisible, same as Call's identical raw-Route limitation.
	err := client.Consume(ctx, sseRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
		got = e.Count
		cancel()
		return nil
	})
	if err != nil && ctx.Err() == nil {
		t.Fatalf("Consume: %v", err)
	}
	if credCalled {
		t.Error("credential ClientMW was invoked — expected GlobalSecurity to stay invisible for a raw SSERoute")
	}
	if got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

// TestAttach_ClientConsume_CodecBackedClientMW_EncodesInAndDecodesOut is
// [TestAttach_ClientCall_CodecBackedClientMW_EncodesInAndDecodesOut]'s SSE
// sibling — proves [clientTransport.Consume] (the Attach+Consume workflow)
// dispatches a .Use()-attached codec-backed Middleware[In,Out]'s EncodeIn
// into the SSE connect request's header AND decodes its Out from the
// response header at connection-open time — the identical, previously
// missing gap confirmed for Consume too (Consume never dispatched
// handle.ClientMiddlewareHandlers at all).
func TestAttach_ClientConsume_CodecBackedClientMW_EncodesInAndDecodesOut(t *testing.T) {
	mw := rest.NewMiddleware(newTDDeclaration("api-key-policy")).
		WithRequestHeader(rest.NewRequiredHeaderParam("X-Api-Key", codex.String(),
			func(in tdIn) string { return in.Key },
			func(in *tdIn, v string) { in.Key = v },
		)).
		WithResponseHeader(rest.NewRequiredResponseHeaderParam("X-Policy-Version", codex.String(),
			func(out tdOut) string { return out.Value },
			func(out *tdOut, v string) { out.Value = v },
		)).
		WithSend(func(ctx context.Context) (tdIn, error) {
			return tdIn{Key: "secret-xyz"}, nil
		})

	sseRoute := rest.NewSSERoute[getReq, counterSSEEvent]("/sse/counter", getReqCodec, counterSSEEventCodec).Use(mw)

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Policy-Version", "v1")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: {\"count\":1}\n\n")
	}))
	defer srv.Close()

	client := rest.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = WithClientMiddlewareOut(ctx)
	var got int
	err := client.Consume(ctx, sseRoute, getReq{}, func(_ context.Context, e counterSSEEvent) error {
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
	if gotHeader != "secret-xyz" {
		t.Errorf("want X-Api-Key %q sent, got %q", "secret-xyz", gotHeader)
	}

	outs := ClientMiddlewareOutFromContext(ctx)
	out, ok := outs["api-key-policy"].(tdOut)
	if !ok {
		t.Fatalf("want decoded tdOut for api-key-policy, got %+v", outs)
	}
	if out.Value != "v1" {
		t.Errorf("want decoded Out.Value %q, got %q", "v1", out.Value)
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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
	if err := client.Attach(NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})); err != nil {
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

// ── Phase 5a: rest.CallWithTransport (bare *RouteHandle acceptance +
// grown rest.ClientCallOptions field parity with the deleted
// CallWithHandle/nethttp.CallOptions) ──────────────────────────────────

// TestCallWithTransport_BareRouteHandle_Accepted confirms
// clientTransport.Call accepts a bare *rest.RouteHandle (built via
// Route.ClientHandle(), no rest.Client/Attach ceremony) directly —
// the dual-mode acceptance this phase added, mirroring
// [reqreply.CallWithTransport]'s identical, already-shipped behavior.
func TestCallWithTransport_BareRouteHandle_Accepted(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	resp, err := rest.CallWithTransport(context.Background(), transport, handle, getReq{})
	if err != nil {
		t.Fatalf("CallWithTransport: %v", err)
	}
	if resp.ID != "me" {
		t.Errorf("resp.ID = %q, want %q", resp.ID, "me")
	}
}

func TestCallWithTransport_QueryParams_AppendedToURL(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("limit"); q != "10" {
			t.Errorf("query param limit = %q, want '10'", q)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"x"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	_, err := rest.CallWithTransport(context.Background(), transport, handle, getReq{},
		rest.ClientCallOptions{QueryParams: map[string]string{"limit": "10"}})
	if err != nil {
		t.Fatalf("CallWithTransport: %v", err)
	}
}

func TestCallWithTransport_ExtraHeaders_Sent(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") != "req-123" {
			t.Errorf("X-Request-ID header missing or wrong")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	_, err := rest.CallWithTransport(context.Background(), transport, handle, getReq{},
		rest.ClientCallOptions{ExtraHeaders: map[string][]string{"X-Request-ID": {"req-123"}}})
	if err != nil {
		t.Fatalf("CallWithTransport: %v", err)
	}
}

func TestCallWithTransport_OnCredentialRejected_FiresOn401(t *testing.T) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(rest.Require("bearerAuth"))
	boundMW := rest.BoundSecurityClientMiddleware[getReq, securedAuthIn, securedAuthOut](
		"bearerAuth", rest.BearerScheme("JWT"), nil,
		func(ctx context.Context, req getReq) (securedAuthIn, error) {
			return securedAuthIn{Token: "test-bearer-token"}, nil
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in securedAuthIn) string { return in.Token },
		func(in *securedAuthIn, v string) { in.Token = v },
	))
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientBoundMW(boundMW).RegisterHandle(b)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rejectedCalls := 0
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	_, err = rest.CallWithTransport(context.Background(), transport, handle, getReq{},
		rest.ClientCallOptions{OnCredentialRejected: func() { rejectedCalls++ }})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected UnexpectedStatusError{StatusCode:401}, got %v", err)
	}
	if rejectedCalls != 1 {
		t.Errorf("want OnCredentialRejected called exactly once, got %d", rejectedCalls)
	}
}

func TestCallWithTransport_OnCredentialRejected_NotCalledWithoutEngagedCredential(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rejectedCalls := 0
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	_, err := rest.CallWithTransport(context.Background(), transport, handle, getReq{},
		rest.ClientCallOptions{OnCredentialRejected: func() { rejectedCalls++ }})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected UnexpectedStatusError{StatusCode:401}, got %v", err)
	}
	if rejectedCalls != 0 {
		t.Errorf("want OnCredentialRejected never called without a credential-providing ClientMW, got %d calls", rejectedCalls)
	}
}

// TestCallWithTransport_OnCredentialRejected_FiresOn401_CodecBackedClientMW
// confirms a codec-backed [rest.Middleware.WithSend] credential middleware
// (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7 — the REPLACEMENT for the legacy
// ClientMW pairing [TestCallWithTransport_OnCredentialRejected_FiresOn401]
// above exercises) also triggers [rest.ClientCallOptions.OnCredentialRejected]
// on a 401 — regression test for a confirmed gap where credentialFnRan was
// derived ONLY from the legacy mergeCredentialHeaders/clientImpls path,
// never from handle.ClientMiddlewareHandlers.
func TestCallWithTransport_OnCredentialRejected_FiresOn401_CodecBackedClientMW(t *testing.T) {
	type authIn struct{ Authorization string }
	type authOut struct{ GrantedScopes map[string][]string }

	mw := rest.SecurityMiddleware[authIn, authOut]("bearerAuth", rest.BearerScheme("JWT"), nil).
		WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
			func(in authIn) string { return in.Authorization },
			func(in *authIn, v string) { in.Authorization = v },
		)).
		WithSend(func(_ context.Context) (authIn, error) {
			return authIn{Authorization: "test-bearer-token"}, nil
		})

	// ClientHandle (not RegisterHandle) — mirrors the real client-side
	// usage pattern (contract.GetSecuredData(mw).ClientHandle() in
	// examples/adapters-nethttp-client): ClientHandle intentionally
	// never runs applyParamDeclarations (see its own doc comment), so
	// mw's required Authorization header param is NOT layered into
	// h.headerParams — ValidateHeaders(opts.HeaderParams) has nothing to
	// require, and the value is supplied entirely via WithSend's merge
	// field at clientMW-dispatch time instead.
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).Use(mw).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	rejectedCalls := 0
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	_, err := rest.CallWithTransport(context.Background(), transport, handle, getReq{},
		rest.ClientCallOptions{OnCredentialRejected: func() { rejectedCalls++ }})

	var statusErr UnexpectedStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected UnexpectedStatusError{StatusCode:401}, got %v", err)
	}
	if rejectedCalls != 1 {
		t.Errorf("want OnCredentialRejected called exactly once for a codec-backed WithSend credential middleware, got %d", rejectedCalls)
	}
}

func TestCallWithTransport_Observer_PerCallOverride(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	obs := &testObserver{}
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})
	_, err := rest.CallWithTransport(context.Background(), transport, handle, getReq{},
		rest.ClientCallOptions{Observer: obs})
	if err != nil {
		t.Fatalf("CallWithTransport: %v", err)
	}
	if !obs.called {
		t.Error("want the per-call Observer override to be used, but it was never called")
	}
}

// reqWithRequiredHeader carries a header merge field for
// TestCallWithTransport_RequiredHeaderMergeField_NotFalselyRejected.
type reqWithRequiredHeader struct{ Token string }

var reqWithRequiredHeaderCodec = codex.Struct[reqWithRequiredHeader]()

// TestCallWithTransport_RequiredHeaderMergeField_NotFalselyRejected is a
// regression test for a confirmed, severe bug this review round found:
// [clientTransport.Call] validated ONLY the caller's explicit
// opts.HeaderParams/CookieParams/QueryParams overrides — NOT the
// merge-field-DERIVED values (from EncodeHeaderVars/EncodeCookieVars/
// EncodeQueryVars, called earlier in the SAME function) — so a route
// declaring a REQUIRED header/cookie/query merge field (the FLAGSHIP
// "auto-derive from Req, one struct, one call" convenience this package
// advertises) was ALWAYS rejected with a false "required parameter
// missing" error, UNLESS the caller ALSO redundantly passed the
// identical value via opts, defeating the entire point of
// auto-derivation. Confirmed broken in the shipped
// examples/rest-api (demoProfile's "valid cookie+header (auto-derived)"
// assertion). Covers BOTH RegisterHandle- and ClientHandle-derived
// handles — both exhibited the bug identically.
func TestCallWithTransport_RequiredHeaderMergeField_NotFalselyRejected(t *testing.T) {
	route := rest.NewRoute[reqWithRequiredHeader, userResp]("GET", "/with-required-header",
		reqWithRequiredHeaderCodec, userRespCodec,
		rest.NewRequiredHeaderParam("X-Token", codex.String(),
			func(r reqWithRequiredHeader) string { return r.Token },
			func(r *reqWithRequiredHeader, v string) { r.Token = v },
		),
	)

	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-Token")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})

	b := rest.NewServer(testInfo)
	registerHandle, err := route.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	if _, err := rest.CallWithTransport(context.Background(), transport, registerHandle,
		reqWithRequiredHeader{Token: "valid-token"}); err != nil {
		t.Fatalf("CallWithTransport (RegisterHandle-derived): %v", err)
	}
	if gotToken != "valid-token" {
		t.Errorf("server received X-Token = %q, want %q (RegisterHandle-derived)", gotToken, "valid-token")
	}

	gotToken = ""
	clientHandle := route.ClientHandle()
	if _, err := rest.CallWithTransport(context.Background(), transport, clientHandle,
		reqWithRequiredHeader{Token: "valid-token"}); err != nil {
		t.Fatalf("CallWithTransport (ClientHandle-derived): %v", err)
	}
	if gotToken != "valid-token" {
		t.Errorf("server received X-Token = %q, want %q (ClientHandle-derived)", gotToken, "valid-token")
	}
}

// TestCallWithTransport_BoundClientMW_InvalidCredential_RejectedClientSide
// is a regression test for a second confirmed, severe bug found the SAME
// round: [rest.ValidateSecurityCredentials] (the SecurityScheme's OWN
// credential-FORMAT codec check) was gated on len(credHeaders) > 0, which
// ONLY ever reflected the LEGACY http.Header-returning ClientMW
// mechanism's contribution — NEVER the modern
// [rest.BoundSecurityClientMiddleware]/ClientBoundMW axis
// (dispatchClientMiddlewareIn). A credential failing its OWN declared
// SecurityScheme codec was silently sent to the server with ZERO
// client-side pre-flight validation when supplied exclusively via the
// modern, documented, recommended mechanism.
func TestCallWithTransport_BoundClientMW_InvalidCredential_RejectedClientSide(t *testing.T) {
	type authIn struct{ Token string }
	type authOut struct{}

	requestReachedServer := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReachedServer = true
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"me"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	bearerScheme := rest.BearerScheme("JWT")
	bearerScheme = bearerScheme.WithCodec(codex.String().Refine(validate.MinLen(10)))

	boundMW := rest.BoundSecurityClientMiddleware[getReq, authIn, authOut](
		"bearerAuth", bearerScheme, nil,
		func(ctx context.Context, req getReq) (authIn, error) {
			return authIn{Token: "short"}, nil // fails MinLen(10)
		},
	).WithRequestHeader(rest.NewRequiredHeaderParam("Authorization", codex.String(),
		func(in authIn) string { return in.Token },
		func(in *authIn, v string) { in.Token = v },
	))

	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(rest.Require("bearerAuth"))
	route := rest.NewRoute[getReq, userResp]("GET", "/bound-mw-invalid-cred", getReqCodec, userRespCodec).
		ClientBoundMW(boundMW)

	registerHandle, err := route.RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: srv.Client(), BaseURL: srv.URL})

	_, err = rest.CallWithTransport(context.Background(), transport, registerHandle, getReq{})
	var credErr rest.SecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("want rest.SecurityCredentialError (invalid credential rejected client-side), got %v", err)
	}
	if requestReachedServer {
		t.Error("want no network call when the credential fails its scheme codec, but the server was hit")
	}
}

// callWithHandle mirrors the exact signature of the now-DELETED
// CallWithHandle (docs/design/d-0006-protocol-native-capabilities.md's
// Phase 5a) — a thin, test-only shim reducing this package's existing
// call sites (testing behavior UNCHANGED by the deletion — path/query/
// header/cookie derivation, security, format overrides, Observer,
// QueryParams/ExtraHeaders/OnCredentialRejected, all still fully
// supported via the grown rest.ClientCallOptions) to a single
// mechanical rename instead of restructuring every call site's argument
// list into a build-transport-then-call shape.
func callWithHandle[Req, Resp any](
	ctx context.Context,
	client *http.Client,
	baseURL string,
	handle *rest.RouteHandle[Req, Resp],
	req Req,
	opts CallOptions,
) (Resp, error) {
	transport := NewClientTransport(ClientTransportOptions{HTTPClient: client, BaseURL: baseURL})
	return rest.CallWithTransport(ctx, transport, handle, req, rest.ClientCallOptions{
		RequestFormats:       opts.RequestFormats,
		ResponseFormats:      opts.ResponseFormats,
		QueryParams:          opts.QueryParams,
		CookieParams:         opts.CookieParams,
		HeaderParams:         opts.HeaderParams,
		ExtraHeaders:         map[string][]string(opts.ExtraHeaders),
		OnCredentialRejected: opts.OnCredentialRejected,
		Observer:             opts.Observer,
	})
}
