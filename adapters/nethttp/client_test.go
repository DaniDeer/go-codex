package nethttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// --- helpers ---

// newClientCreateRoute returns a POST /users route handle via Register (shared contract path).
func newClientCreateRoute() *rest.RouteHandle[createReq, userResp] {
	b := rest.NewBuilder(testInfo)
	h, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userRespCodec, rest.RouteMeta{OperationID: "createUser"}).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newClientGetRoute returns a GET /users/{id} route handle via ClientHandle (client-only path).
func newClientGetRoute() *rest.RouteHandle[getReq, userResp] {
	return rest.NewRoute[getReq, userResp]("GET", "/users/{id}",
		getReqCodec, userRespCodec,
		rest.PathParam{Name: "id"}.WithCodec(codex.String().Refine(validate.NonEmptyString)),
	).ClientHandle()
}

// testObserver records RecordRequest calls for assertion.
type testObserver struct {
	stats.NoopObserver
	method   string
	path     string
	status   int
	duration time.Duration
	called   bool
	valErrs  []struct{ location, constraint, field string }
}

func (o *testObserver) RecordRequest(method, path string, statusCode int, duration time.Duration) {
	o.method = method
	o.path = path
	o.status = statusCode
	o.duration = duration
	o.called = true
}

func (o *testObserver) RecordValidationError(location, constraint, field string) {
	o.valErrs = append(o.valErrs, struct{ location, constraint, field string }{location, constraint, field})
}

// --- happy path ---

func TestCall_POST_HappyPath(t *testing.T) {
	handle := newClientCreateRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/users" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": body["name"].(string)}) //nolint:errcheck
	}))
	defer srv.Close()

	obs := &testObserver{}
	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil,
		nethttp.CallOptions{Observer: obs})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Alice" || resp.ID != "1" {
		t.Errorf("unexpected response: %+v", resp)
	}
	if !obs.called {
		t.Error("observer RecordRequest not called")
	}
	if obs.method != "POST" {
		t.Errorf("observer method = %q, want POST", obs.method)
	}
	if obs.status != http.StatusOK {
		t.Errorf("observer status = %d, want 200", obs.status)
	}
}

func TestCall_GET_HappyPath(t *testing.T) {
	handle := newClientGetRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "abc", "name": "Bob"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, map[string]string{"id": "abc"},
		nethttp.CallOptions{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "abc" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// --- non-2xx response ---

func TestCall_UnexpectedStatus(t *testing.T) {
	handle := newClientCreateRoute()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	obs := &testObserver{}
	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Alice"}, nil,
		nethttp.CallOptions{Observer: obs})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected UnexpectedStatusError, got %T: %v", err, err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", statusErr.StatusCode)
	}
	if statusErr.Method != "POST" {
		t.Errorf("method = %q, want POST", statusErr.Method)
	}
	if statusErr.Path != "/users" {
		t.Errorf("path = %q, want /users", statusErr.Path)
	}
	if !strings.Contains(string(statusErr.Body), "not found") {
		t.Errorf("body = %q, want to contain 'not found'", statusErr.Body)
	}
	if obs.status != http.StatusNotFound {
		t.Errorf("observer status = %d, want 404", obs.status)
	}
}

// --- path parameter validation ---

func TestCall_PathParamValidation_EmptyID(t *testing.T) {
	handle := newClientGetRoute() // id param requires NonEmptyString

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, map[string]string{"id": ""},
		nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected path param error, got nil")
	}
	var pathErr rest.PathParamError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected rest.PathParamError, got %T: %v", err, err)
	}
	if pathErr.Name != "id" {
		t.Errorf("param name = %q, want 'id'", pathErr.Name)
	}
}

func TestCall_PathParamValidation_MissingVar(t *testing.T) {
	handle := newClientGetRoute()

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil, // nil vars — {id} missing
		nethttp.CallOptions{})

	if err == nil {
		t.Fatal("expected missing path var error, got nil")
	}
	var missingErr rest.MissingPathVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected rest.MissingPathVarError, got %T: %v", err, err)
	}
}

// --- query parameter validation ---

func TestCall_QueryParamValidation(t *testing.T) {
	qpCodec := codex.String().Refine(validate.NonEmptyString)
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
		rest.QueryParam{Name: "filter", Required: true}.WithCodec(qpCodec),
	).ClientHandle()

	obs := &testObserver{}
	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			QueryParams: map[string]string{"filter": ""},
			Observer:    obs,
		})

	if err == nil {
		t.Fatal("expected query param error, got nil")
	}
	var qpErr rest.QueryParamError
	if !errors.As(err, &qpErr) {
		t.Fatalf("expected rest.QueryParamError, got %T: %v", err, err)
	}
	if qpErr.Name != "filter" {
		t.Errorf("param name = %q, want 'filter'", qpErr.Name)
	}
	if len(obs.valErrs) == 0 {
		t.Error("expected observer to receive validation error")
	}
}

// --- cookie parameter validation ---

func TestCall_CookieParamValidation(t *testing.T) {
	cookieCodec := codex.String().Refine(validate.NonEmptyString)
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
		rest.CookieParam{Name: "session", Required: true}.WithCodec(cookieCodec),
	).ClientHandle()

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			CookieParams: map[string]string{"session": ""},
		})

	if err == nil {
		t.Fatal("expected cookie param error, got nil")
	}
	var cpErr rest.CookieParamError
	if !errors.As(err, &cpErr) {
		t.Fatalf("expected rest.CookieParamError, got %T: %v", err, err)
	}
	if cpErr.Name != "session" {
		t.Errorf("param name = %q, want 'session'", cpErr.Name)
	}
}

// --- header parameter validation ---

func TestCall_HeaderParamValidation(t *testing.T) {
	headerCodec := codex.String().Refine(validate.NonEmptyString)
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
		rest.HeaderParam{Name: "X-Tenant-ID", Required: true}.WithCodec(headerCodec),
	).ClientHandle()

	_, err := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			HeaderParams: map[string]string{"X-Tenant-ID": ""},
		})

	if err == nil {
		t.Fatal("expected header param error, got nil")
	}
	var hErr rest.HeaderParamError
	if !errors.As(err, &hErr) {
		t.Fatalf("expected rest.HeaderParamError, got %T: %v", err, err)
	}
	if hErr.Name != "X-Tenant-ID" {
		t.Errorf("param name = %q, want 'X-Tenant-ID'", hErr.Name)
	}
}

// --- CredentialFunc ---

func TestCall_CredentialFunc_Invoked(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddSecurityScheme("bearerAuth", rest.SecurityScheme{
		SecurityScheme: route.BearerScheme("JWT"),
	})
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	credCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me", "name": "Alice"}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, nil,
		nethttp.CallOptions{
			CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
				credCalled = true
				h := make(http.Header)
				h.Set("Authorization", "Bearer test-token")
				return h, nil
			},
		})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !credCalled {
		t.Error("CredentialFunc was not called")
	}
	if resp.ID != "me" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestCall_CredentialFunc_Error(t *testing.T) {
	b := rest.NewBuilder(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	handle, err := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec).Register(b)
	if err != nil {
		t.Fatal(err)
	}

	credErr := errors.New("token expired")
	_, callErr := nethttp.Call(context.Background(), http.DefaultClient, "http://localhost",
		handle, getReq{}, nil,
		nethttp.CallOptions{
			CredentialFunc: func(ctx context.Context, reqs []route.SecurityRequirement) (http.Header, error) {
				return nil, credErr
			},
		})

	if !errors.Is(callErr, credErr) {
		t.Fatalf("expected credential error, got %v", callErr)
	}
}

// --- Observer RecordRequest on validation failure ---

func TestCall_Observer_RecordRequest_OnValidationFailure(t *testing.T) {
	handle := newClientGetRoute()
	obs := &testObserver{}

	// Missing path var → validation error before any request is sent.
	nethttp.Call(context.Background(), http.DefaultClient, "http://localhost", //nolint:errcheck
		handle, getReq{}, nil,
		nethttp.CallOptions{Observer: obs})

	if !obs.called {
		t.Error("observer RecordRequest not called on validation failure")
	}
	if obs.status != 0 {
		t.Errorf("observer status = %d, want 0 (no request sent)", obs.status)
	}
}

// --- ClientHandle (client-only, no builder) ---

func TestCall_ClientHandle_NoBuilder(t *testing.T) {
	handle := rest.NewRoute[createReq, userResp]("POST", "/items",
		createReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		name, _ := body["name"].(string)
		json.NewEncoder(w).Encode(map[string]string{"id": "42", "name": name}) //nolint:errcheck
	}))
	defer srv.Close()

	resp, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, createReq{Name: "Widget"}, nil,
		nethttp.CallOptions{})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Name != "Widget" {
		t.Errorf("unexpected response: %+v", resp)
	}
}

// --- query params appended to URL ---

func TestCall_QueryParams_AppendedToURL(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/users",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("limit")
		if q != "10" {
			t.Errorf("query param limit = %q, want '10'", q)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "x"}) //nolint:errcheck
	}))
	defer srv.Close()

	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, nil,
		nethttp.CallOptions{QueryParams: map[string]string{"limit": "10"}})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ExtraHeaders sent ---

func TestCall_ExtraHeaders_Sent(t *testing.T) {
	handle := rest.NewRoute[getReq, userResp]("GET", "/me",
		getReqCodec, userRespCodec,
	).ClientHandle()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Request-ID") != "req-123" {
			t.Errorf("X-Request-ID header missing or wrong")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "me"}) //nolint:errcheck
	}))
	defer srv.Close()

	extra := make(http.Header)
	extra.Set("X-Request-ID", "req-123")
	_, err := nethttp.Call(context.Background(), srv.Client(), srv.URL,
		handle, getReq{}, nil,
		nethttp.CallOptions{ExtraHeaders: extra})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Transport error types: Error() string and Unwrap() ---

func TestRequestBuildError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("invalid url")
	e := nethttp.RequestBuildError{Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted nethttp.RequestBuildError
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.As(wrapped, &extracted) {
		t.Error("errors.As must extract RequestBuildError through wrapping")
	}
	if extracted.Err != cause {
		t.Errorf("extracted.Err = %v, want %v", extracted.Err, cause)
	}
}

func TestRequestError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("connection refused")
	e := nethttp.RequestError{Method: "GET", Path: "/users/{id}", Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted nethttp.RequestError
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.As(wrapped, &extracted) {
		t.Error("errors.As must extract RequestError through wrapping")
	}
	if extracted.Method != "GET" || extracted.Path != "/users/{id}" {
		t.Errorf("extracted = {%s %s}, want {GET /users/{id}}", extracted.Method, extracted.Path)
	}
}

func TestResponseBodyError_ErrorAndUnwrap(t *testing.T) {
	cause := errors.New("unexpected EOF")
	e := nethttp.ResponseBodyError{Err: cause}

	if e.Error() == "" {
		t.Error("Error() must return a non-empty string")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must traverse Unwrap to cause")
	}
	var extracted nethttp.ResponseBodyError
	wrapped := fmt.Errorf("outer: %w", e)
	if !errors.As(wrapped, &extracted) {
		t.Error("errors.As must extract ResponseBodyError through wrapping")
	}
	if extracted.Err != cause {
		t.Errorf("extracted.Err = %v, want %v", extracted.Err, cause)
	}
}
