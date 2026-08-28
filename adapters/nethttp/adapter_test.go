package nethttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test types and codecs ---

type createReq struct{ Name string }
type userResp struct{ ID, Name string }

var createReqCodec = codex.Struct[createReq](
	codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
		func(r createReq) string { return r.Name },
		func(r *createReq, v string) { r.Name = v },
	),
)

var userRespCodec = codex.Struct[userResp](
	codex.OptionalField("id", codex.String(),
		func(u userResp) string { return u.ID },
		func(u *userResp, v string) { u.ID = v },
	),
	codex.OptionalField("name", codex.String(),
		func(u userResp) string { return u.Name },
		func(u *userResp, v string) { u.Name = v },
	),
)

type getReq struct{}
type handlerConflictError struct{ msg string }

func (e handlerConflictError) Error() string { return e.msg }

var getReqCodec = codex.Struct[getReq]()
var testInfo = rest.Info{Title: "Test API", Version: "1.0.0"}

// newCreateRoute is a helper that creates a POST /users route.
func newCreateRoute() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestHandler_PostValidBody(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	body := `{"name":"Alice"}`
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("want application/json, got %q", ct)
	}
	var got userResp
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Alice" {
		t.Fatalf("want Name=Alice, got %q", got.Name)
	}
}

func TestHandler_PostValidationError(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called on validation error")
		return userResp{}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":""}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" {
		t.Fatal("want non-empty error message")
	}
}

func TestHandler_PostMalformedJSON(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`not-json`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestHandler_PostHandlerError(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, errors.New("service unavailable")
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body["error"], "service unavailable") {
		t.Fatalf("want error to contain 'service unavailable', got %q", body["error"])
	}
}

func TestHandler_ErrorStatusRouteMapping(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.ErrorStatus[handlerConflictError](http.StatusConflict),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.Handler(handle, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, handlerConflictError{msg: "conflict"}
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d", rec.Code)
	}
}

func TestHandler_GetNonBody(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "getUser"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	h := nethttp.Handler(handle, func(_ context.Context, req getReq) (userResp, error) {
		called = true
		return userResp{ID: "42", Name: "Bob"}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	h.ServeHTTP(rec, r)

	if !called {
		t.Fatal("handler not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var got userResp
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "42" {
		t.Fatalf("want ID=42, got %q", got.ID)
	}
}

func TestRegister_WiresCorrectPattern(t *testing.T) {
	handle := newCreateRoute()
	mux := http.NewServeMux()
	nethttp.Register(mux, handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/users", "application/json", strings.NewReader(`{"name":"Charlie"}`)) //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	var got userResp
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Charlie" {
		t.Fatalf("want Name=Charlie, got %q", got.Name)
	}
}

func TestHandler_CustomStatus(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[createReq, userResp]("PUT", "/users/{id}",
		createReqCodec, userRespCodec, rest.RouteMeta{
			OperationID: "updateUser",
			RespStatus:  "204",
		}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/users/1", strings.NewReader(`{"name":"Dave"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != 204 {
		t.Fatalf("want 204, got %d", rec.Code)
	}
}

func TestHandler_RequestFromContext(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec, rest.RouteMeta{OperationID: "getUser"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	var gotID string
	h := nethttp.Handler(handle, func(ctx context.Context, _ getReq) (userResp, error) {
		r, ok := nethttp.RequestFromContext(ctx)
		if !ok {
			return userResp{}, errors.New("no request in context")
		}
		gotID = r.PathValue("id")
		return userResp{ID: gotID, Name: "Alice"}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	// Use a mux so PathValue is populated.
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h)
	r := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	mux.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotID != "42" {
		t.Fatalf("want PathValue id=42, got %q", gotID)
	}
}

func TestHandlerWithOptions_CustomErrorHandler(t *testing.T) {
	handle := newCreateRoute()
	var capturedStatus int
	var capturedMsg string

	opts := nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			capturedStatus = status
			capturedMsg = err.Error()
			http.Error(w, err.Error(), status)
		},
	}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, errors.New("custom error")
	}, opts)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	if capturedStatus != http.StatusInternalServerError {
		t.Fatalf("want capturedStatus=500, got %d", capturedStatus)
	}
	if !strings.Contains(capturedMsg, "custom error") {
		t.Fatalf("want 'custom error' in msg, got %q", capturedMsg)
	}
}

func TestHandler_QueryValidation_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/users", getReqCodec, userRespCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "1", Name: "Alice"}, nil
	}, nethttp.Options{})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users?id=f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_QueryValidation_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/users", getReqCodec, userRespCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users?id=not-a-uuid", nil)
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

// ── Observer tests ─────────────────────────────────────────────────────────────

type spyObserver struct {
	requests  []spyRequest
	valErrors []spyValError
}

type spyRequest struct {
	method     string
	path       string
	statusCode int
}

type spyValError struct {
	location       string
	constraintName string
	field          string
}

func (s *spyObserver) RecordRequest(method, path string, statusCode int, _ time.Duration) {
	s.requests = append(s.requests, spyRequest{method: method, path: path, statusCode: statusCode})
}

func (s *spyObserver) RecordSubscribe(_ string, _ bool, _ time.Duration) {}
func (s *spyObserver) RecordPublish(_ string, _ bool, _ time.Duration)   {}

func (s *spyObserver) RecordValidationError(location, constraintName, field string) {
	s.valErrors = append(s.valErrors, spyValError{location: location, constraintName: constraintName, field: field})
}

func TestObserver_RecordRequest_success(t *testing.T) {
	handle := newCreateRoute()
	obs := &spyObserver{}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(obs))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if len(obs.requests) != 1 {
		t.Fatalf("want 1 RecordRequest call, got %d", len(obs.requests))
	}
	got := obs.requests[0]
	if got.method != "POST" {
		t.Errorf("want method POST, got %q", got.method)
	}
	if got.path != "/users" {
		t.Errorf("want path /users, got %q", got.path)
	}
	if got.statusCode != http.StatusCreated {
		t.Errorf("want statusCode 201, got %d", got.statusCode)
	}
}

func TestObserver_RecordRequest_handlerError(t *testing.T) {
	handle := newCreateRoute()
	obs := &spyObserver{}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, errors.New("oops")
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(obs))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if len(obs.requests) != 1 {
		t.Fatalf("want 1 RecordRequest call, got %d", len(obs.requests))
	}
	if obs.requests[0].statusCode != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", obs.requests[0].statusCode)
	}
}

func TestObserver_RecordValidationError_body(t *testing.T) {
	handle := newCreateRoute()
	obs := &spyObserver{}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(obs))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":""}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if len(obs.valErrors) == 0 {
		t.Fatal("want at least one RecordValidationError call, got none")
	}
	if obs.valErrors[0].location != "body" {
		t.Errorf("want location 'body', got %q", obs.valErrors[0].location)
	}
	if obs.valErrors[0].field != "name" {
		t.Errorf("want field 'name', got %q", obs.valErrors[0].field)
	}
}

func TestObserver_RecordValidationError_query(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/users", getReqCodec, userRespCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	obs := &spyObserver{}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(obs))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/users?id=not-a-uuid", nil)
	handler.ServeHTTP(rec, r)

	if len(obs.valErrors) == 0 {
		t.Fatal("want at least one RecordValidationError call, got none")
	}
	if obs.valErrors[0].location != "query" {
		t.Errorf("want location 'query', got %q", obs.valErrors[0].location)
	}
	if obs.valErrors[0].field != "id" {
		t.Errorf("want field 'id', got %q", obs.valErrors[0].field)
	}
}

func TestHandler_CookieValidation_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/protected", getReqCodec, userRespCodec, rest.CookieParam{Name: "session_token", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "1", Name: "Alice"}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.AddCookie(&http.Cookie{Name: "session_token", Value: "abc123"})
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_CookieValidation_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/protected", getReqCodec, userRespCodec, rest.CookieParam{Name: "session_token", Required: true, Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		t.Fatal("handler must not be called on cookie validation error")
		return userResp{}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.AddCookie(&http.Cookie{Name: "session_token", Value: ""})
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_HeaderValidation_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/items", getReqCodec, userRespCodec, rest.HeaderParam{Name: "X-Request-Id", Required: true, Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "1", Name: "Alice"}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/items", nil)
	r.Header.Set("X-Request-Id", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandler_HeaderValidation_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/items", getReqCodec, userRespCodec, rest.HeaderParam{Name: "X-Request-Id", Required: true, Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		t.Fatal("handler must not be called on header validation error")
		return userResp{}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/items", nil)
	r.Header.Set("X-Request-Id", "not-a-uuid")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestObserver_RecordValidationError_cookie(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/protected", getReqCodec, userRespCodec, rest.CookieParam{Name: "session_token", Codec: &tokenCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	obs := &spyObserver{}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(obs))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/protected", nil)
	r.AddCookie(&http.Cookie{Name: "session_token", Value: ""})
	handler.ServeHTTP(rec, r)

	if len(obs.valErrors) == 0 {
		t.Fatal("want at least one RecordValidationError call, got none")
	}
	if obs.valErrors[0].location != "cookie" {
		t.Errorf("want location 'cookie', got %q", obs.valErrors[0].location)
	}
	if obs.valErrors[0].field != "session_token" {
		t.Errorf("want field 'session_token', got %q", obs.valErrors[0].field)
	}
}

func TestObserver_RecordValidationError_header(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/items", getReqCodec, userRespCodec, rest.HeaderParam{Name: "X-Request-Id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	obs := &spyObserver{}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(obs))

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/items", nil)
	r.Header.Set("X-Request-Id", "not-a-uuid")
	handler.ServeHTTP(rec, r)

	if len(obs.valErrors) == 0 {
		t.Fatal("want at least one RecordValidationError call, got none")
	}
	if obs.valErrors[0].location != "header" {
		t.Errorf("want location 'header', got %q", obs.valErrors[0].location)
	}
	if obs.valErrors[0].field != "X-Request-Id" {
		t.Errorf("want field 'X-Request-Id', got %q", obs.valErrors[0].field)
	}
}

func TestOptions_MaxBodyBytes_rejectOversized(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{MaxBodyBytes: 10}) // 10 bytes — tiny

	var capturedErr error
	captureHandler := nethttp.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{
		MaxBodyBytes: 10,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			capturedErr = err
			w.WriteHeader(status)
		},
	})

	rec := httptest.NewRecorder()
	body := `{"name":"Alice","age":30}` // 24 bytes > 10
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("want 413, got %d", rec.Code)
	}

	// Verify typed error via errors.As.
	rec2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	r2.Header.Set("Content-Type", "application/json")
	captureHandler.ServeHTTP(rec2, r2)

	var sizeErr rest.BodyTooLargeError
	if !errors.As(capturedErr, &sizeErr) {
		t.Fatalf("want BodyTooLargeError, got %T: %v", capturedErr, capturedErr)
	}
	if sizeErr.Limit != 10 {
		t.Errorf("want Limit=10, got %d", sizeErr.Limit)
	}
}

func TestOptions_ContentType_415onWrongType(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	var capturedErr error
	handler := nethttp.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			capturedErr = err
			w.WriteHeader(status)
		},
	})

	rec := httptest.NewRecorder()
	body := `{"name":"Alice"}`
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/xml")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("want 415, got %d", rec.Code)
	}

	var ctErr rest.UnsupportedMediaTypeError
	if !errors.As(capturedErr, &ctErr) {
		t.Fatalf("want UnsupportedMediaTypeError, got %T: %v", capturedErr, capturedErr)
	}
	if ctErr.Got != "application/xml" {
		t.Errorf("want Got=%q, got %q", "application/xml", ctErr.Got)
	}
	if len(ctErr.Supported) != 1 || ctErr.Supported[0] != "application/json" {
		t.Errorf("want Supported=[application/json], got %v", ctErr.Supported)
	}
}

func TestOptions_ContentType_acceptsWithCharset(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	body := `{"name":"Alice"}`
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestOptions_MultiValueQueryParams_valid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/items", getReqCodec, userRespCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "1", Name: "Alice"}, nil
	}, nethttp.Options{MultiValueQueryParams: true})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/items?id=550e8400-e29b-41d4-a716-446655440000&id=ignored", nil)
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestOptions_MultiValueQueryParams_invalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[getReq, userResp]("GET", "/items", getReqCodec, userRespCodec, rest.QueryParam{Name: "id", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{MultiValueQueryParams: true})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/items?id=not-a-uuid", nil)
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

// ── WithResponseHeaders tests ───────────────────────────────────────────────

func TestWithResponseHeaders_setsHeaderOnSuccess(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(ctx context.Context, req createReq) (userResp, error) {
		extra := make(http.Header)
		extra.Set("Location", "/users/42")
		nethttp.WithResponseHeaders(ctx, extra)
		return userResp{ID: "42", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Bob"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users/42" {
		t.Errorf("want Location=/users/42, got %q", loc)
	}
}

func TestWithResponseHeaders_multiValueHeader(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(ctx context.Context, req createReq) (userResp, error) {
		extra := make(http.Header)
		extra.Add("X-Tag", "alpha")
		extra.Add("X-Tag", "beta")
		nethttp.WithResponseHeaders(ctx, extra)
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Carol"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d", rec.Code)
	}
	tags := rec.Header()["X-Tag"]
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Errorf("want X-Tag=[alpha beta], got %v", tags)
	}
}

func TestResponseHeadersFromContext_falseWhenAbsent(t *testing.T) {
	ctx := context.Background()
	if _, ok := nethttp.ResponseHeadersFromContext(ctx); ok {
		t.Error("want false for empty context, got true")
	}
}

func TestWithResponseHeaders_notAppliedOnError(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(ctx context.Context, req createReq) (userResp, error) {
		extra := make(http.Header)
		extra.Set("Location", "/users/99")
		nethttp.WithResponseHeaders(ctx, extra) // headers must NOT appear on error path
		return userResp{}, errors.New("handler failed")
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Dave"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("want no Location header on error, got %q", loc)
	}
}

// ── ResponseHeaderParam adapter tests ──────────────────────────────────────

func TestResponseHeaderParam_validHeader_appearsInResponse(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.ResponseHeaderParam{Name: "Location", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		extra := make(http.Header)
		extra.Set("Location", "f47ac10b-58cc-4372-a567-0e02b2c3d479")
		nethttp.WithResponseHeaders(ctx, extra)
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("want Location=f47ac10b-..., got %q", loc)
	}
}

func TestResponseHeaderParam_codecViolation_returns500(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.ResponseHeaderParam{Name: "Location", Codec: &uuidCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	var capturedErr error
	handler := nethttp.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		extra := make(http.Header)
		extra.Set("Location", "not-a-uuid") // violates UUID codec
		nethttp.WithResponseHeaders(ctx, extra)
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			capturedErr = err
			w.WriteHeader(status)
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
	var rhe rest.ResponseHeaderParamError
	if !errors.As(capturedErr, &rhe) {
		t.Fatalf("want ResponseHeaderParamError, got %T: %v", capturedErr, capturedErr)
	}
	if rhe.Name != "Location" {
		t.Errorf("want Name=Location, got %q", rhe.Name)
	}
}

func TestResponseHeaderParam_unregisteredHeaderPassesThrough(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"}).Register(b) // no ResponseHeaderParams
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		extra := make(http.Header)
		extra.Set("X-Custom", "whatever")
		nethttp.WithResponseHeaders(ctx, extra)
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if v := rec.Header().Get("X-Custom"); v != "whatever" {
		t.Errorf("want X-Custom=whatever, got %q", v)
	}
}

// --- WithResponseCookies tests ---

func TestWithResponseCookies_setsCookieOnSuccess(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	sessionCodec := codex.String().Refine(validate.MinLen(8))
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.ResponseCookieParam{Name: "session", Required: true, Codec: &sessionCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		nethttp.WithResponseCookies(ctx, nethttp.PendingCookie{
			Name:  "session",
			Value: "tok_abcdefgh",
			Opts:  nethttp.CookieOptions{MaxAge: 3600},
		})
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "session" && c.Value == "tok_abcdefgh" {
			found = true
		}
	}
	if !found {
		t.Errorf("Set-Cookie session not found in response; cookies: %v", cookies)
	}
}

func TestWithResponseCookies_codecViolationReturns500(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	sessionCodec := codex.String().Refine(validate.MinLen(32))
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.ResponseCookieParam{Name: "session", Required: true, Codec: &sessionCodec}).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(ctx context.Context, req createReq) (userResp, error) {
		nethttp.WithResponseCookies(ctx, nethttp.PendingCookie{
			Name:  "session",
			Value: "tooshort", // violates MinLen(32)
		})
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Response merge fields (Round 3) ---

// userRespWithMeta carries response header/cookie merge fields alongside
// the JSON body fields.
type userRespWithMeta struct {
	ID        string
	Name      string
	RequestID string
	Session   string
}

func (u userRespWithMeta) Error() string { return "error user response " + u.ID }

var userRespWithMetaBodyCodec = codex.Struct[userRespWithMeta](
	codex.RequiredField("id", codex.String(),
		func(u userRespWithMeta) string { return u.ID },
		func(u *userRespWithMeta, v string) { u.ID = v },
	),
	codex.RequiredField("name", codex.String(),
		func(u userRespWithMeta) string { return u.Name },
		func(u *userRespWithMeta, v string) { u.Name = v },
	),
)

// R6: nethttp.Handler route WITH response header/cookie merge fields —
// server sets the header/cookie automatically from the handler's returned
// Resp, no WithResponseHeaders/WithResponseCookies call needed.
func TestHandler_ResponseMergeFields_AutoAppliesFromResp(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userRespWithMeta, error) {
		// No WithResponseHeaders/WithResponseCookies call — the adapter
		// derives the header/cookie values from the returned struct fields.
		return userRespWithMeta{ID: "1", Name: req.Name, RequestID: "req-999", Session: "sess-xyz"}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Request-Id"); got != "req-999" {
		t.Errorf("want X-Request-Id=req-999, got %q", got)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" && c.Value == "sess-xyz" {
			found = true
		}
	}
	if !found {
		t.Errorf("Set-Cookie session=sess-xyz not found; cookies: %v", rec.Result().Cookies())
	}
}

func TestHandler_ErrorPattern_DirectWithResponseHeaderCookieParity(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.NonEmptyString),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
		rest.NewOptionalResponseCookieParam("session", codex.String(),
			func(u userRespWithMeta) string { return u.Session },
			func(u *userRespWithMeta, v string) { u.Session = v }),
		rest.ErrorPattern[userRespWithMeta, userRespWithMeta](http.StatusConflict, userRespWithMetaBodyCodec),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userRespWithMeta, error) {
		return userRespWithMeta{}, userRespWithMeta{
			ID: "e1", Name: req.Name, RequestID: "req-error-1", Session: "sess-error-1",
		}
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Request-Id"); got != "req-error-1" {
		t.Fatalf("want X-Request-Id=req-error-1, got %q", got)
	}
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" && c.Value == "sess-error-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want session cookie from error payload, got %v", rec.Result().Cookies())
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != "e1" || body["name"] != "Alice" {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestHandler_ErrorPattern_MappedPayload(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.ErrorPattern[handlerConflictError, userResp](http.StatusUnprocessableEntity, userRespCodec,
			func(e handlerConflictError) (userResp, error) {
				return userResp{ID: "mapped", Name: e.msg}, nil
			}),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, handlerConflictError{msg: "mapped-message"}
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rec.Code)
	}
	var got userResp
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "mapped" || got.Name != "mapped-message" {
		t.Fatalf("unexpected mapped response: %+v", got)
	}
}

// Phase 2: rest.ErrorPattern.WithAction(rest.ErrorHandle) skips the automatic
// typed body write and falls through to Options.ErrorHandler instead — the
// resolved status (409, from this pattern's declared status) still applies.
func TestHandler_ErrorPattern_WithActionHandle_FallsThroughToErrorHandler(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec,
		rest.ErrorPattern[handlerConflictError, userResp](http.StatusConflict, userRespCodec,
			func(e handlerConflictError) (userResp, error) {
				return userResp{ID: "mapped", Name: e.msg}, nil
			}).WithAction(rest.ErrorHandle),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	var gotErrorHandlerStatus int
	var gotErrorHandlerErr error
	handler := nethttp.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{}, handlerConflictError{msg: "handled-not-responded"}
	}, nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			gotErrorHandlerStatus = status
			gotErrorHandlerErr = err
			w.WriteHeader(status)
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if gotErrorHandlerStatus != http.StatusConflict {
		t.Fatalf("want ErrorHandler called with 409, got %d", gotErrorHandlerStatus)
	}
	if gotErrorHandlerErr == nil {
		t.Fatal("want ErrorHandler called with the original error")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("want response status 409, got %d", rec.Code)
	}
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if _, hasID := body["id"]; hasID {
		t.Fatalf("want NO typed body auto-written for handle action, got %v", body)
	}
}

// R6b: a required response header merge field that fails codec validation
// returns 500, same as the validate-only ResponseHeaderParam path.
func TestHandler_ResponseMergeFields_CodecViolationReturns500(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userRespWithMeta]("POST", "/users", createReqCodec, userRespWithMetaBodyCodec,
		rest.NewRequiredResponseHeaderParam("X-Request-Id", codex.String().Refine(validate.UUID),
			func(u userRespWithMeta) string { return u.RequestID },
			func(u *userRespWithMeta, v string) { u.RequestID = v }),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userRespWithMeta, error) {
		return userRespWithMeta{ID: "1", Name: req.Name, RequestID: "not-a-uuid"}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

// R7: nethttp.Handler route WITHOUT response merge fields behaves
// byte-for-byte identically to today — regression guard.
func TestHandler_ResponseMergeFields_NoneDeclaredIsUnaffected(t *testing.T) {
	handle := newCreateRoute() // no response merge fields declared
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var got userResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "1" || got.Name != "Alice" {
		t.Errorf("unexpected body: %+v", got)
	}
}

// --- Content negotiation tests ---

func TestContentNegotiation_acceptJSON(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(format.JSON(userRespCodec), format.YAML(userRespCodec))
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want Content-Type application/json, got %q", ct)
	}
}

func TestContentNegotiation_acceptYAML(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(format.JSON(userRespCodec), format.YAML(userRespCodec))
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/yaml")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("want Content-Type application/yaml, got %q", ct)
	}
}

func TestContentNegotiation_wildcardAcceptPicksFirst(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(format.JSON(userRespCodec), format.YAML(userRespCodec))
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "*/*")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want Content-Type application/json (first format), got %q", ct)
	}
}

func TestContentNegotiation_unacceptableReturns406(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(format.JSON(userRespCodec))
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/xml")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("want 406, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestContentNegotiation_noFormatsUsesJSON(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b) // no responseFormats
	if err != nil {
		t.Fatal(err)
	}
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("want Content-Type application/json, got %q", ct)
	}
}

func TestContentNegotiation_streamedFormat_writesDirectly(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	streamFmt := format.NewStreamed(userRespCodec,
		func(v userResp, w io.Writer) error {
			_, err := fmt.Fprintf(w, "id=%s name=%s", v.ID, v.Name)
			return err
		},
		func([]byte) (userResp, error) { return userResp{}, errors.New("not decodable") },
		"text/plain",
	)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(streamFmt, format.JSON(userRespCodec))
	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "42", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Bob"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/plain")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("want Content-Type text/plain, got %q", ct)
	}
	if body := rec.Body.String(); body != "id=42 name=Bob" {
		t.Errorf("want body 'id=42 name=Bob', got %q", body)
	}
}

func TestContentNegotiation_streamedFormat_validationErrorBefore200(t *testing.T) {
	strictRespCodec := codex.Struct[userResp](
		codex.OptionalField("id", codex.String(),
			func(u userResp) string { return u.ID },
			func(u *userResp, v string) { u.ID = v },
		),
		codex.RequiredField("name", codex.String().Refine(validate.NonEmptyString),
			func(u userResp) string { return u.Name },
			func(u *userResp, v string) { u.Name = v },
		),
	)
	b := rest.NewBuilder(testInfo)
	streamFmt := format.NewStreamed(strictRespCodec,
		func(v userResp, w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s", v.ID)
			return err
		},
		func([]byte) (userResp, error) { return userResp{}, nil },
		"text/plain",
	)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, strictRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h.WithFormats(streamFmt)
	handler := nethttp.Handler(h, func(_ context.Context, _ createReq) (userResp, error) {
		return userResp{ID: "1", Name: ""}, nil // Name="" fails NonEmptyString
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/plain")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on validation failure, got %d: %s", rec.Code, rec.Body.String())
	}
}

// --- Request format negotiation tests ---

func TestRequestFormats_JSONBodyAccepted(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))

	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestRequestFormats_YAMLBodyAccepted(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))

	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader("name: Bob\n"))
	r.Header.Set("Content-Type", "application/yaml")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got userResp
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Bob" {
		t.Errorf("want Name=Bob, got %q", got.Name)
	}
}

func TestRequestFormats_WrongContentType_returns415(t *testing.T) {
	var capturedErr error
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))

	handler := nethttp.Handler(h, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, status int, e error) {
			capturedErr = e
			w.WriteHeader(status)
		},
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`<name>Alice</name>`))
	r.Header.Set("Content-Type", "application/xml")
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("want 415, got %d", rec.Code)
	}
	var ctErr rest.UnsupportedMediaTypeError
	if !errors.As(capturedErr, &ctErr) {
		t.Fatalf("want UnsupportedMediaTypeError, got %T: %v", capturedErr, capturedErr)
	}
	if ctErr.Got != "application/xml" {
		t.Errorf("want Got=application/xml, got %q", ctErr.Got)
	}
	if len(ctErr.Supported) != 2 {
		t.Errorf("want 2 supported types, got %v", ctErr.Supported)
	}
}

func TestRequestFormats_SpecContentTypesUpdated(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users", createReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	h = h.WithRequestFormats(format.JSON(createReqCodec), format.YAML(createReqCodec))
	_ = h

	doc, err := b.OpenAPISpec()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := doc.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	paths := spec["paths"].(map[string]any)
	post := paths["/users"].(map[string]any)["post"].(map[string]any)
	body := post["requestBody"].(map[string]any)
	content := body["content"].(map[string]any)
	if _, ok := content["application/json"]; !ok {
		t.Error("want application/json in requestBody content")
	}
	if _, ok := content["application/yaml"]; !ok {
		t.Error("want application/yaml in requestBody content")
	}
}

// --- SSE tests ---

type sseEvent struct{ Message string }

var sseEventCodec = codex.Struct[sseEvent](
	codex.RequiredField("message", codex.String().Refine(validate.NonEmptyString),
		func(e sseEvent) string { return e.Message },
		func(e *sseEvent, v string) { e.Message = v },
	),
)

func TestSSEHandler_streamEvents(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEvents"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	handler := nethttp.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		for _, msg := range []string{"hello", "world"} {
			if err := send(sseEvent{Message: msg}); err != nil {
				return err
			}
		}
		return nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want text/event-stream, got %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"hello"`) || !strings.Contains(body, `"world"`) {
		t.Errorf("expected both events in body, got: %s", body)
	}
	// Each event must be in SSE format: data: ...\n\n
	if !strings.Contains(body, "data:") {
		t.Errorf("expected SSE data: prefix, got: %s", body)
	}
}

func TestSSEHandler_validationRejectsEvent(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamEventsValidate"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	var sendErr error
	handler := nethttp.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		sendErr = send(sseEvent{Message: ""}) // empty message fails NonEmptyString
		return nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events", nil)
	handler.ServeHTTP(rec, r)

	if sendErr == nil {
		t.Fatal("expected send to return validation error for empty message")
	}
	// Body should be empty — no event written
	if body := rec.Body.String(); body != "" {
		t.Errorf("expected empty body on validation rejection, got: %s", body)
	}
}

func TestSSEHandler_clientDisconnect(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamDisconnect"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	cancelled := false
	handler := nethttp.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		// Simulate checking context cancellation
		select {
		case <-ctx.Done():
			cancelled = true
		default:
		}
		return nil
	}, nethttp.Options{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/events", nil).WithContext(ctx)
	handler.ServeHTTP(rec, r)

	if !cancelled {
		t.Error("expected handler to observe cancelled context")
	}
}

func TestSSEHandler_RegisterSSE_wiresOntoMux(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/events",
		createReqCodec, sseEventCodec, rest.RouteMeta{OperationID: "streamRegister"}).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	nethttp.RegisterSSE(mux, handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "registered"})
	}, nethttp.Options{})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("want text/event-stream, got %q", ct)
	}
	if !strings.Contains(string(body), "registered") {
		t.Errorf("expected 'registered' in body, got: %s", body)
	}
}

// --- Security tests ---

type mockSecurityObserver struct {
	stats.NoopObserver
	location string
	scheme   string
}

func (o *mockSecurityObserver) RecordSecurityRejection(location, scheme string) {
	o.location = location
	o.scheme = scheme
}

func newSecuredRoute(mw middleware.Middleware) (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	return rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.WithMiddleware(mw),
	).Register(b)
}

func TestHandler_SecurityFunc_calledForSecuredRoute(t *testing.T) {
	secFuncCalled := false
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, r *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "test-bearer-token" {
				return nil, errors.New("unauthorized")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	)
	handle, err := newSecuredRoute(mw)
	if err != nil {
		t.Fatal(err)
	}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	h.ServeHTTP(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for secured route")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_rejectsRequest(t *testing.T) {
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, errors.New("unauthorized")
		},
	)
	handle, err := newSecuredRoute(mw)
	if err != nil {
		t.Fatal(err)
	}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called when security rejects")
		return userResp{}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_notCalledForUnsecuredRoute(t *testing.T) {
	handle := newCreateRoute()
	secFuncCalled := false
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			return nil, nil
		},
	)
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{}, mw)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if secFuncCalled {
		t.Error("want SecurityFunc NOT called for unsecured route")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

// TestHandler_RequireAPIKey_RunsWithoutRouteSecurity locks in that
// RequireAPIKey's Fn — a pure presence/format-check middleware with an
// EMPTY Satisfies, contributing no scope grants — ALWAYS runs, even though
// the route declares NO Security at all. This is the opposite gating rule
// from RequireScopes (see TestHandler_SecurityFunc_notCalledForUnsecuredRoute
// right above): a scope-granting middleware only runs for a secured route,
// but a presence-check-only middleware must run unconditionally, otherwise
// it never actually enforces anything (a real bug found and fixed — see
// docs/roadmap/declarative-middleware.md's "G2" self-review finding).
func TestHandler_RequireAPIKey_RunsWithoutRouteSecurity(t *testing.T) {
	handle := newCreateRoute()
	verifyCalled := false
	mw := nethttp.RequireAPIKey[createReq]("X-API-Key", func(_ context.Context, key string) error {
		verifyCalled = true
		if key != "secret" {
			return errors.New("invalid api key")
		}
		return nil
	})
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{}, mw)

	// Missing/invalid key is rejected.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if !verifyCalled {
		t.Fatal("want RequireAPIKey's verify called even though the route declares no Security")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 for missing API key, got %d", rec.Code)
	}

	// Valid key is accepted.
	verifyCalled = false
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", "secret")
	h.ServeHTTP(rec, r)

	if !verifyCalled {
		t.Fatal("want RequireAPIKey's verify called")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201 for valid API key, got %d", rec.Code)
	}
}

func TestHandler_SecurityFunc_codecValidationFailure(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	jwtCodec := codex.String().Refine(validate.JWT)
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, &jwtCodec,
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			t.Fatal("Fn must not be called when credential fails codec format validation")
			return nil, nil
		},
	)
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.WithMiddleware(mw),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		t.Fatal("handler must not be called when credential fails codec")
		return userResp{}, nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	// No Authorization header — extracted credential will be empty → invalid JWT
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] == "" {
		t.Error("want non-empty error message")
	}
}

func TestHandler_SecurityObserver_calledOnRejection(t *testing.T) {
	obs := &mockSecurityObserver{}
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(ctx context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			// Fn-driven rejection recording is now the Fn author's own
			// responsibility (Class A moved into Fn) — the adapter no
			// longer calls RecordSecurityRejection automatically for
			// authorization (as opposed to credential-format) failures.
			if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection("/users", "bearerAuth")
			}
			return nil, errors.New("unauthorized")
		},
	)
	handle, err := newSecuredRoute(mw)
	if err != nil {
		t.Fatal(err)
	}
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{})

	withObsMiddleware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), obs))
		h.ServeHTTP(w, r)
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	withObsMiddleware.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", rec.Code)
	}
	if obs.location != "/users" {
		t.Errorf("want location=/users, got %q", obs.location)
	}
	if obs.scheme != "bearerAuth" {
		t.Errorf("want scheme=bearerAuth, got %q", obs.scheme)
	}
}

func newGlobalSecuredRoute() (*rest.RouteHandle[createReq, userResp], error) {
	b := rest.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	// No per-route Security — inherits global. Attach NO rest.WithMiddleware
	// here (global security is not checked by drift-closing validation,
	// see adapters/nethttp Stage 3 simplification) — the security
	// middleware is instead attached at Handler call time in each test.
	return rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{OperationID: "createUser"},
		rest.WithSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}),
	).Register(b)
}

func TestHandler_GlobalSecurity_enforcedWhenNoPerRouteSecurity(t *testing.T) {
	handle, err := newGlobalSecuredRoute()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, r *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "test-bearer-token" {
				return nil, errors.New("unauthorized")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	)
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{}, mw)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "test-bearer-token")
	h.ServeHTTP(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for route inheriting global security")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

func TestHandler_GlobalSecurity_rejectsWhenNoToken(t *testing.T) {
	handle, err := newGlobalSecuredRoute()
	if err != nil {
		t.Fatal(err)
	}
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, errors.New("missing token")
		},
	)
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{}, mw)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 from global security, got %d", rec.Code)
	}
}

func TestHandler_GlobalSecurity_notCalledWhenExplicitlyEmpty(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	// Empty Security slice = explicitly "no auth" on this route.
	handle, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec,
		rest.RouteMeta{
			OperationID: "createUser",
			Security:    []route.SecurityRequirement{},
		},
		rest.WithSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}),
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			return nil, nil
		},
	)
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	}, nethttp.Options{}, mw)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"name":"Alice"}`))
	r.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, r)

	if secFuncCalled {
		t.Error("want SecurityFunc NOT called for route with explicit empty Security")
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("want 201, got %d", rec.Code)
	}
}

// --- SSE security + param validation tests ---

func newGlobalSecuredSSERoute() (*rest.SSERouteHandle[createReq, sseEvent], error) {
	b := rest.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	return rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.RouteMeta{OperationID: "streamSecured"},
		rest.WithSecurityScheme("bearerAuth", rest.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}),
	).Register(b)
}

func TestSSEHandler_GlobalSecurity_enforced(t *testing.T) {
	handle, err := newGlobalSecuredSSERoute()
	if err != nil {
		t.Fatal(err)
	}
	secFuncCalled := false
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, r *http.Request, _ *createReq) (map[string][]string, error) {
			secFuncCalled = true
			if r.Header.Get("Authorization") != "test-bearer-token" {
				return nil, errors.New("unauthorized")
			}
			return map[string][]string{"bearerAuth": nil}, nil
		},
	)
	h := nethttp.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, nethttp.Options{}, mw)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("Authorization", "test-bearer-token")
	h.ServeHTTP(rec, r)

	if !secFuncCalled {
		t.Error("want SecurityFunc called for SSE route inheriting global security")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestSSEHandler_GlobalSecurity_rejectsWhenNoToken(t *testing.T) {
	handle, err := newGlobalSecuredSSERoute()
	if err != nil {
		t.Fatal(err)
	}
	mw := nethttp.RequireScopes[createReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(_ context.Context, _ *http.Request, _ *createReq) (map[string][]string, error) {
			return nil, errors.New("missing token")
		},
	)
	h := nethttp.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, nethttp.Options{}, mw)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("want 401 from SSE global security, got %d", rec.Code)
	}
}

func TestSSEHandler_QueryParam_rejectsInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream?id=not-a-uuid", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE query param, got %d", rec.Code)
	}
}

func TestSSEHandler_QueryParam_allowsValid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.QueryParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.SSEHandler(handle, func(_ context.Context, _ createReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "ok"})
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream?id=f47ac10b-58cc-4372-a567-0e02b2c3d479", nil)
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("want 200 for valid SSE query param, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSSEHandler_CookieParam_rejectsInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	tokenCodec := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.CookieParam{Name: "session", Codec: &tokenCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.AddCookie(&http.Cookie{Name: "session", Value: ""})
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE cookie param, got %d", rec.Code)
	}
}

func TestSSEHandler_HeaderParam_rejectsInvalid(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream",
		createReqCodec, sseEventCodec,
		rest.HeaderParam{Name: "X-Request-Id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.SSEHandler(handle, func(_ context.Context, _ createReq, _ func(sseEvent) error) error {
		return nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("X-Request-Id", "not-a-uuid")
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid SSE header param, got %d", rec.Code)
	}
}

func TestSSEHandler_ResponseHeaderParam_appearsOnFirstSend(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	traceCodec := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream-rh",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &traceCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	h := nethttp.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		extra := make(http.Header)
		extra.Set("X-Trace-Id", "trace-abc-123")
		nethttp.WithResponseHeaders(ctx, extra)
		return send(sseEvent{Message: "hello"})
	}, nethttp.Options{})

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/stream-rh") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if v := resp.Header.Get("X-Trace-Id"); v != "trace-abc-123" {
		t.Errorf("want X-Trace-Id=trace-abc-123, got %q", v)
	}
}

func TestSSEHandler_ResponseHeaderParam_codecViolation_abortsStream(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	traceCodec := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[createReq, sseEvent]("/stream-rh2",
		createReqCodec, sseEventCodec,
		rest.ResponseHeaderParam{Name: "X-Trace-Id", Codec: &traceCodec},
	).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	sendCalled := false
	h := nethttp.SSEHandler(handle, func(ctx context.Context, _ createReq, send func(sseEvent) error) error {
		extra := make(http.Header)
		extra.Set("X-Trace-Id", "") // empty — codec rejects it
		nethttp.WithResponseHeaders(ctx, extra)
		err := send(sseEvent{Message: "should not appear"})
		if err != nil {
			sendCalled = true
			return err
		}
		return nil
	}, nethttp.Options{})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream-rh2", nil)
	h.ServeHTTP(rec, r)

	if !sendCalled {
		t.Error("want send to return error for invalid response header codec, but send was not called")
	}
	if strings.Contains(rec.Body.String(), "should not appear") {
		t.Errorf("want no event data written when response header codec fails, got: %s", rec.Body.String())
	}
}

func TestHandler_PathParam_codecValidated(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := nethttp.Handler(handle, func(_ context.Context, r getReq) (userResp, error) {
		return userResp{ID: "ok"}, nil
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h)

	// Invalid UUID → 400.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/not-a-uuid", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid path param, got %d", rec.Code)
	}

	// Valid UUID → 200.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/users/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("want 200 for valid path param, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// P5: nethttp.Handler for a route WITH merge fields — the handler function
// receives an already-merged, validated req; no manual r.PathValue needed.
func TestHandler_MergeFields_AutomaticMerge(t *testing.T) {
	type getUserReq struct{ ID string }
	getUserReqCodec := codex.Struct[getUserReq]()

	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewRoute[getUserReq, userResp]("GET", "/users/{id}",
		getUserReqCodec, userRespCodec,
		rest.NewPathParam("id", codex.String().Refine(validate.UUID),
			func(r getUserReq) string { return r.ID },
			func(r *getUserReq, v string) { r.ID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	var gotID string
	h := nethttp.Handler(handle, func(_ context.Context, r getUserReq) (userResp, error) {
		gotID = r.ID // no r.PathValue() call needed — already merged
		return userResp{ID: r.ID}, nil
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("handler did not receive merged ID: got %q", gotID)
	}
}

// P6: nethttp.Handler for a route WITHOUT merge fields — byte-for-byte
// identical behavior to before this feature (regression guard).
func TestHandler_NoMergeFields_UnchangedBehavior(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec}, // plain, validate-only — no merge
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if len(handle.MergeFields()) != 0 {
		t.Fatalf("expected no merge fields for a plain PathParam route, got %d", len(handle.MergeFields()))
	}
	h := nethttp.Handler(handle, func(_ context.Context, r getReq) (userResp, error) {
		return userResp{ID: "ok"}, nil
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /users/{id}", h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSSEHandler_PathParam_codecValidated(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[getReq, sseEvent]("/stream/{id}",
		getReqCodec, sseEventCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := nethttp.SSEHandler(handle, func(ctx context.Context, _ getReq, send func(sseEvent) error) error {
		return send(sseEvent{Message: "hi"})
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /stream/{id}", h)

	// Invalid UUID → 400.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/bad", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid path param, got %d", rec.Code)
	}

	// Valid UUID → 200.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/stream/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("want 200 for valid path param, got %d", rec2.Code)
	}
}

func TestSSEHandler_EventMerge_FromConnectionVars(t *testing.T) {
	type mergedEvent struct {
		ID     string
		Tenant string
		Trace  string
		SID    string
	}
	evtCodec := codex.Struct[mergedEvent](
		codex.OptionalField("id", codex.String(), func(e mergedEvent) string { return e.ID }, func(e *mergedEvent, v string) { e.ID = v }),
		codex.OptionalField("tenant", codex.String(), func(e mergedEvent) string { return e.Tenant }, func(e *mergedEvent, v string) { e.Tenant = v }),
		codex.OptionalField("trace", codex.String(), func(e mergedEvent) string { return e.Trace }, func(e *mergedEvent, v string) { e.Trace = v }),
		codex.OptionalField("sid", codex.String(), func(e mergedEvent) string { return e.SID }, func(e *mergedEvent, v string) { e.SID = v }),
	)
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	nonEmpty := codex.String().Refine(validate.NonEmptyString)
	handle, err := rest.NewSSERoute[getReq, mergedEvent]("/stream/{id}",
		getReqCodec, evtCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
		rest.QueryParam{Name: "tenant", Required: true, Codec: &nonEmpty},
		rest.HeaderParam{Name: "X-Trace", Required: true, Codec: &nonEmpty},
		rest.CookieParam{Name: "sid", Required: true, Codec: &nonEmpty},
		rest.NewRequiredSSEEventParam("id", codex.String(), func(e mergedEvent) string { return e.ID }, func(e *mergedEvent, v string) { e.ID = v }),
		rest.NewRequiredSSEEventParam("tenant", codex.String(), func(e mergedEvent) string { return e.Tenant }, func(e *mergedEvent, v string) { e.Tenant = v }),
		rest.NewRequiredSSEEventParam("X-Trace", codex.String(), func(e mergedEvent) string { return e.Trace }, func(e *mergedEvent, v string) { e.Trace = v }),
		rest.NewRequiredSSEEventParam("sid", codex.String(), func(e mergedEvent) string { return e.SID }, func(e *mergedEvent, v string) { e.SID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := nethttp.SSEHandler(handle, func(_ context.Context, _ getReq, send func(mergedEvent) error) error {
		return send(mergedEvent{ID: "wrong", Tenant: "wrong", Trace: "wrong", SID: "wrong"})
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /stream/{id}", h)

	req := httptest.NewRequest(http.MethodGet, "/stream/550e8400-e29b-41d4-a716-446655440000?tenant=acme", nil)
	req.Header.Set("X-Trace", "trace-1")
	req.AddCookie(&http.Cookie{Name: "sid", Value: "sid-1"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"550e8400-e29b-41d4-a716-446655440000"`) ||
		!strings.Contains(body, `"tenant":"acme"`) ||
		!strings.Contains(body, `"trace":"trace-1"`) ||
		!strings.Contains(body, `"sid":"sid-1"`) {
		t.Fatalf("expected merged event payload, got: %s", body)
	}
}

func TestSSEHandler_EventMerge_MissingRequiredFailsSend(t *testing.T) {
	type mergedEvent struct{ V string }
	evtCodec := codex.Struct[mergedEvent](
		codex.OptionalField("v", codex.String(), func(e mergedEvent) string { return e.V }, func(e *mergedEvent, v string) { e.V = v }),
	)
	b := rest.NewBuilder(testInfo)
	handle, err := rest.NewSSERoute[getReq, mergedEvent]("/stream",
		getReqCodec, evtCodec,
		rest.NewRequiredSSEEventParam("missing", codex.String(), func(e mergedEvent) string { return e.V }, func(e *mergedEvent, v string) { e.V = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := nethttp.SSEHandler(handle, func(_ context.Context, _ getReq, send func(mergedEvent) error) error {
		return send(mergedEvent{})
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /stream", h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSSEHandler_EventMerge_NestedGobFormat(t *testing.T) {
	type evtMeta struct {
		ID string
	}
	type evtPayload struct {
		Blob []byte
	}
	type mergedEvent struct {
		Meta    evtMeta
		Payload evtPayload
	}
	evtCodec := codex.Struct[mergedEvent](
		codex.OptionalField("id", codex.String(), func(e mergedEvent) string { return e.Meta.ID }, func(e *mergedEvent, v string) { e.Meta.ID = v }),
		codex.RequiredField("blob", codex.Bytes(), func(e mergedEvent) []byte { return e.Payload.Blob }, func(e *mergedEvent, v []byte) { e.Payload.Blob = v }),
	)
	b := rest.NewBuilder(testInfo)
	uuidCodec := codex.String().Refine(validate.UUID)
	handle, err := rest.NewSSERoute[getReq, mergedEvent]("/stream/{id}",
		getReqCodec, evtCodec,
		rest.PathParam{Name: "id", Codec: &uuidCodec},
		rest.NewRequiredSSEEventParam("id", codex.String(), func(e mergedEvent) string { return e.Meta.ID }, func(e *mergedEvent, v string) { e.Meta.ID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	gobFmt := format.Gob(evtCodec)
	handle = handle.WithFormats(gobFmt)
	h := nethttp.SSEHandler(handle, func(_ context.Context, _ getReq, send func(mergedEvent) error) error {
		return send(mergedEvent{
			Meta:    evtMeta{ID: "wrong"},
			Payload: evtPayload{Blob: []byte{0xCA, 0xFE, 0xBA, 0xBE}},
		})
	}, nethttp.Options{})
	mux := http.NewServeMux()
	mux.Handle("GET /stream/{id}", h)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stream/550e8400-e29b-41d4-a716-446655440000", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.Bytes()
	prefix := []byte("data: ")
	start := bytes.Index(body, prefix)
	if start < 0 {
		t.Fatalf("missing SSE data prefix in %q", string(body))
	}
	frame := body[start+len(prefix):]
	if i := bytes.Index(frame, []byte("\n\n")); i >= 0 {
		frame = frame[:i]
	}
	got, err := gobFmt.Unmarshal(frame)
	if err != nil {
		t.Fatalf("unmarshal gob frame: %v", err)
	}
	if got.Meta.ID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Fatalf("expected merged id, got %q", got.Meta.ID)
	}
	if !bytes.Equal(got.Payload.Blob, []byte{0xCA, 0xFE, 0xBA, 0xBE}) {
		t.Fatalf("blob mismatch: %v", got.Payload.Blob)
	}
}

// ── Context observer integration ──────────────────────────────────────────────

// T5: Handler with empty Options picks up context observer from r.Context().
// The context observer is server-side (set on the incoming request context);
// HTTP is stateless so client-side contexts do not propagate to the server.
// The typical integration is via server middleware: r.WithContext(stats.WithObserver(r.Context(), obs)).
// T5: Handler with NO attached ObservabilityMiddleware does NOT call
// RecordRequest at all, even when a context observer is present — an
// intentional behavior change (see docs/roadmap/declarative-middleware.md):
// RecordRequest/TraceObserver moved ENTIRELY into ObservabilityMiddleware,
// the sole remaining stats.Observer call site in this package. A caller
// wanting context-based observer resolution now attaches
// nethttp.ObservabilityMiddleware(stats.ObserverFromContext(ctx)) itself.
func TestHandler_ContextObserver_NotUsedWithoutObservabilityMiddleware(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, _ := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	var recorded bool
	spy := &testSpyObserver{onRequest: func() { recorded = true }}

	h := nethttp.Handler(handle, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "u1", Name: "Alice"}, nil
	}, nethttp.Options{}) // no ObservabilityMiddleware attached

	// Even with a context observer present, Handler itself never resolves
	// or calls it for RecordRequest anymore.
	withObsMiddleware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), spy))
		h.ServeHTTP(w, r)
	})

	rec := httptest.NewRecorder()
	withObsMiddleware.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
	if recorded {
		t.Error("want RecordRequest NOT called without an attached ObservabilityMiddleware")
	}
}

// T6: Explicit opts.Observer beats context observer
func TestHandler_ExplicitObserverBeatsContext(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, _ := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	var explicitCalled, contextCalled bool
	explicit := &testSpyObserver{onRequest: func() { explicitCalled = true }}
	ctxObs := &testSpyObserver{onRequest: func() { contextCalled = true }}

	h := nethttp.Handler(handle, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{}, nethttp.ObservabilityMiddleware(explicit)) // explicit wins

	// Even if context observer is present, explicit wins.
	withObsMiddleware := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(stats.WithObserver(r.Context(), ctxObs))
		h.ServeHTTP(w, r)
	})

	rec := httptest.NewRecorder()
	withObsMiddleware.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/users", nil))

	if !explicitCalled {
		t.Error("want explicit observer called")
	}
	if contextCalled {
		t.Error("want context observer NOT called when explicit is set")
	}
}

// T7: No opts.Observer, no context observer → noop, no panic
func TestHandler_NoObserver_NoContextObserver_IsNoop(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, _ := rest.NewRoute[getReq, userResp]("GET", "/noop",
		getReqCodec, userRespCodec, rest.RouteMeta{}).Register(b)

	h := nethttp.Handler(handle, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{ID: "ok"}, nil
	}, nethttp.Options{}) // no observer, no context observer

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/noop", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

// testSpyObserver captures whether RecordRequest was called.
type testSpyObserver struct {
	stats.NoopObserver
	onRequest func()
}

func (s *testSpyObserver) RecordRequest(_, _ string, _ int, _ time.Duration) {
	if s.onRequest != nil {
		s.onRequest()
	}
}
