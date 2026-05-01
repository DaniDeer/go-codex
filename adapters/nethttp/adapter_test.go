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
	return rest.AddRoute[createReq, userResp](b, "POST", "/users",
		createReqCodec, userRespCodec, rest.RouteConfig{OperationID: "createUser"})
}

func TestHandler_PostValidBody(t *testing.T) {
	handle := newCreateRoute()
	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})

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
	})

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
	})

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
	})

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
	handle := rest.AddRoute[getReq, userResp](b, "GET", "/users/{id}",
		getReqCodec, userRespCodec, rest.RouteConfig{OperationID: "getUser"})

	called := false
	h := nethttp.Handler(handle, func(_ context.Context, req getReq) (userResp, error) {
		called = true
		return userResp{ID: "42", Name: "Bob"}, nil
	})

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
	})

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
	handle := rest.AddRoute[createReq, userResp](b, "PUT", "/users/{id}",
		createReqCodec, userRespCodec, rest.RouteConfig{
			OperationID: "updateUser",
			RespStatus:  "204",
		})

	h := nethttp.Handler(handle, func(_ context.Context, req createReq) (userResp, error) {
		return userResp{ID: "1", Name: req.Name}, nil
	})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/users/1", strings.NewReader(`{"name":"Dave"}`))
	h.ServeHTTP(rec, r)

	if rec.Code != 204 {
		t.Fatalf("want 204, got %d", rec.Code)
	}
}

func TestHandler_RequestFromContext(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	handle := rest.AddRoute[getReq, userResp](b, "GET", "/users/{id}",
		getReqCodec, userRespCodec, rest.RouteConfig{OperationID: "getUser"})

	var gotID string
	h := nethttp.Handler(handle, func(ctx context.Context, _ getReq) (userResp, error) {
		r, ok := nethttp.RequestFromContext(ctx)
		if !ok {
			return userResp{}, errors.New("no request in context")
		}
		gotID = r.PathValue("id")
		return userResp{ID: gotID, Name: "Alice"}, nil
	})

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
	h := nethttp.HandlerWithOptions(handle, func(_ context.Context, req createReq) (userResp, error) {
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
