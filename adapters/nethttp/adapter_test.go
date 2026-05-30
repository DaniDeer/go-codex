package nethttp_test

import (
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
	"github.com/DaniDeer/go-codex/validate"
)

// --- shared test types and codecs ---

type createReq struct{ Name string }
type userResp struct{ ID, Name string }

var createReqCodec = codex.Struct[createReq](
	codex.Field[createReq, string]{
		Name:     "name",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Get:      func(r createReq) string { return r.Name },
		Set:      func(r *createReq, v string) { r.Name = v },
		Required: true,
	},
)

var userRespCodec = codex.Struct[userResp](
	codex.Field[userResp, string]{
		Name:  "id",
		Codec: codex.String(),
		Get:   func(u userResp) string { return u.ID },
		Set:   func(u *userResp, v string) { u.ID = v },
	},
	codex.Field[userResp, string]{
		Name:  "name",
		Codec: codex.String(),
		Get:   func(u userResp) string { return u.Name },
		Set:   func(u *userResp, v string) { u.Name = v },
	},
)

type getReq struct{}

var getReqCodec = codex.Struct[getReq]()
var testInfo = rest.Info{Title: "Test API", Version: "1.0.0"}

// newCreateRoute is a helper that creates a POST /users route.
func newCreateRoute() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users",
		createReqCodec, userRespCodec, rest.RouteConfig{OperationID: "createUser"})
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

func TestHandler_GetNonBody(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.AddRoute[getReq, userResp](b, "GET", "/users/{id}",
		getReqCodec, userRespCodec, rest.RouteConfig{OperationID: "getUser"})
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
	handle, err := rest.AddRoute[createReq, userResp](b, "PUT", "/users/{id}",
		createReqCodec, userRespCodec, rest.RouteConfig{
			OperationID: "updateUser",
			RespStatus:  "204",
		})
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
	handle, err := rest.AddRoute[getReq, userResp](b, "GET", "/users/{id}",
		getReqCodec, userRespCodec, rest.RouteConfig{OperationID: "getUser"})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/users", getReqCodec, userRespCodec, rest.RouteConfig{
		QueryParams: []rest.QueryParam{
			{Name: "id", Codec: &uuidCodec},
		},
	})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/users", getReqCodec, userRespCodec, rest.RouteConfig{
		QueryParams: []rest.QueryParam{
			{Name: "id", Codec: &uuidCodec},
		},
	})
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
	}, nethttp.Options{Observer: obs})

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
	}, nethttp.Options{Observer: obs})

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
	}, nethttp.Options{Observer: obs})

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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/users", getReqCodec, userRespCodec, rest.RouteConfig{
		QueryParams: []rest.QueryParam{{Name: "id", Codec: &uuidCodec}},
	})
	if err != nil {
		t.Fatal(err)
	}
	obs := &spyObserver{}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{Observer: obs})

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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/protected", getReqCodec, userRespCodec, rest.RouteConfig{
		CookieParams: []rest.CookieParam{{Name: "session_token", Required: true, Codec: &tokenCodec}},
	})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/protected", getReqCodec, userRespCodec, rest.RouteConfig{
		CookieParams: []rest.CookieParam{{Name: "session_token", Required: true, Codec: &tokenCodec}},
	})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/items", getReqCodec, userRespCodec, rest.RouteConfig{
		HeaderParams: []rest.HeaderParam{{Name: "X-Request-Id", Required: true, Codec: &uuidCodec}},
	})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/items", getReqCodec, userRespCodec, rest.RouteConfig{
		HeaderParams: []rest.HeaderParam{{Name: "X-Request-Id", Required: true, Codec: &uuidCodec}},
	})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/protected", getReqCodec, userRespCodec, rest.RouteConfig{
		CookieParams: []rest.CookieParam{{Name: "session_token", Codec: &tokenCodec}},
	})
	if err != nil {
		t.Fatal(err)
	}
	obs := &spyObserver{}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{Observer: obs})

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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/items", getReqCodec, userRespCodec, rest.RouteConfig{
		HeaderParams: []rest.HeaderParam{{Name: "X-Request-Id", Codec: &uuidCodec}},
	})
	if err != nil {
		t.Fatal(err)
	}
	obs := &spyObserver{}
	handler := nethttp.Handler(h, func(_ context.Context, _ getReq) (userResp, error) {
		return userResp{}, nil
	}, nethttp.Options{Observer: obs})

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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec, rest.RouteConfig{})
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec, rest.RouteConfig{})
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
	if ctErr.Expected != "application/json" {
		t.Errorf("want Expected=%q, got %q", "application/json", ctErr.Expected)
	}
}

func TestOptions_ContentType_acceptsWithCharset(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec, rest.RouteConfig{})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/items", getReqCodec, userRespCodec, rest.RouteConfig{
		QueryParams: []rest.QueryParam{{Name: "id", Codec: &uuidCodec}},
	})
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
	h, err := rest.AddRoute[getReq, userResp](b, "GET", "/items", getReqCodec, userRespCodec, rest.RouteConfig{
		QueryParams: []rest.QueryParam{{Name: "id", Codec: &uuidCodec}},
	})
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{
			OperationID: "createUser",
			ResponseHeaderParams: []rest.ResponseHeaderParam{
				{Name: "Location", Codec: &uuidCodec},
			},
		})
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{
			OperationID: "createUser",
			ResponseHeaderParams: []rest.ResponseHeaderParam{
				{Name: "Location", Codec: &uuidCodec},
			},
		})
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{OperationID: "createUser"}) // no ResponseHeaderParams
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{
			ResponseCookieParams: []rest.ResponseCookieParam{
				{Name: "session", Required: true, Codec: &sessionCodec},
			},
		})
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{
			ResponseCookieParams: []rest.ResponseCookieParam{
				{Name: "session", Required: true, Codec: &sessionCodec},
			},
		})
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

// --- Content negotiation tests ---

func TestContentNegotiation_acceptJSON(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{},
		format.JSON(userRespCodec),
		format.YAML(userRespCodec),
	)
	if err != nil {
		t.Fatal(err)
	}
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{},
		format.JSON(userRespCodec),
		format.YAML(userRespCodec),
	)
	if err != nil {
		t.Fatal(err)
	}
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{},
		format.JSON(userRespCodec),
		format.YAML(userRespCodec),
	)
	if err != nil {
		t.Fatal(err)
	}
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{},
		format.JSON(userRespCodec),
	)
	if err != nil {
		t.Fatal(err)
	}
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{}) // no responseFormats
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, userRespCodec,
		rest.RouteConfig{},
		streamFmt,
		format.JSON(userRespCodec),
	)
	if err != nil {
		t.Fatal(err)
	}
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
		codex.Field[userResp, string]{
			Name:  "id",
			Codec: codex.String(),
			Get:   func(u userResp) string { return u.ID },
			Set:   func(u *userResp, v string) { u.ID = v },
		},
		codex.Field[userResp, string]{
			Name:  "name",
			Codec: codex.String().Refine(validate.NonEmptyString),
			Get:   func(u userResp) string { return u.Name },
			Set:   func(u *userResp, v string) { u.Name = v },
		},
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
	h, err := rest.AddRoute[createReq, userResp](b, "POST", "/users", createReqCodec, strictRespCodec,
		rest.RouteConfig{},
		streamFmt,
	)
	if err != nil {
		t.Fatal(err)
	}
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

// --- SSE tests ---

type sseEvent struct{ Message string }

var sseEventCodec = codex.Struct[sseEvent](
	codex.Field[sseEvent, string]{
		Name:     "message",
		Codec:    codex.String().Refine(validate.NonEmptyString),
		Required: true,
		Get:      func(e sseEvent) string { return e.Message },
		Set:      func(e *sseEvent, v string) { e.Message = v },
	},
)

func TestSSEHandler_streamEvents(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamEvents"})
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
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamEventsValidate"})
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
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamDisconnect"})
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
	handle, err := rest.AddSSERoute[createReq, sseEvent](b, "/events",
		createReqCodec, sseEventCodec, rest.RouteConfig{OperationID: "streamRegister"})
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
