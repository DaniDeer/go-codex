package nethttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
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
