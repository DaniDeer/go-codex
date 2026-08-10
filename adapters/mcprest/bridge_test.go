package mcprest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	mcprest "github.com/DaniDeer/go-codex/adapters/mcprest"
	nethttp "github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// --- shared test fixtures ---

type greetReq struct {
	Name string
}

type greetResp struct {
	Greeting string
}

var greetReqCodec = codex.Struct[greetReq](
	codex.RequiredField("name", codex.String(),
		func(r greetReq) string { return r.Name },
		func(r *greetReq, v string) { r.Name = v }),
)

var greetRespCodec = codex.Struct[greetResp](
	codex.RequiredField("greeting", codex.String(),
		func(r greetResp) string { return r.Greeting },
		func(r *greetResp, v string) { r.Greeting = v }),
)

// newGreetRoute returns a client-only handle for a synthetic POST /greet
// route — used across every test in this file.
func newGreetRoute() *rest.RouteHandle[greetReq, greetResp] {
	return rest.NewRoute[greetReq, greetResp]("POST", "/greet",
		greetReqCodec, greetRespCodec, rest.RouteMeta{OperationID: "greet"},
	).ClientHandle()
}

// newGreetServer replies 200 with {"greeting": "Hello, <name>"} for any
// request whose body decodes to {"name": "..."}, and calls onRequest (if
// non-nil) with the request for header/count assertions.
func newGreetServer(t *testing.T, onRequest func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest(r)
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"greeting": "Hello, " + body.Name})
	}))
}

// newFailingServer always replies with status.
func newFailingServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
}

// --- ToolHandler (identity case) ---

func TestToolHandler_HappyPath_ForwardsToCallHandle(t *testing.T) {
	srv := newGreetServer(t, nil)
	defer srv.Close()

	handle := newGreetRoute()
	fn := mcprest.ToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{})

	out, err := fn(context.Background(), greetReq{Name: "Alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Greeting != "Hello, Alice" {
		t.Errorf("Greeting = %q, want %q", out.Greeting, "Hello, Alice")
	}
}

func TestToolHandler_ErrorPath_ForwardsUnderlyingErrorUnchanged(t *testing.T) {
	srv := newFailingServer(http.StatusInternalServerError)
	defer srv.Close()

	handle := newGreetRoute()
	fn := mcprest.ToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{})

	_, err := fn(context.Background(), greetReq{Name: "Alice"})
	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("want nethttp.UnexpectedStatusError, got %v (%T)", err, err)
	}
	if statusErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusInternalServerError)
	}
}

func TestToolHandler_FixedCallOptions_AppliedToEveryCall(t *testing.T) {
	var gotAuth []string
	srv := newGreetServer(t, func(r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
	})
	defer srv.Close()

	handle := newGreetRoute()
	fn := mcprest.ToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{
		ExtraHeaders: http.Header{"Authorization": []string{"Bearer fixed-token"}},
	})

	for i := 0; i < 2; i++ {
		if _, err := fn(context.Background(), greetReq{Name: "Bob"}); err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
	}

	if len(gotAuth) != 2 || gotAuth[0] != "Bearer fixed-token" || gotAuth[1] != "Bearer fixed-token" {
		t.Errorf("Authorization headers = %v, want [Bearer fixed-token, Bearer fixed-token]", gotAuth)
	}
}

// --- MappedToolHandler (general case) ---

type simpleIn struct {
	Who string
}

type simpleOut struct {
	Message string
}

func TestMappedToolHandler_HappyPath_MapsInputAndOutput(t *testing.T) {
	srv := newGreetServer(t, nil)
	defer srv.Close()

	handle := newGreetRoute()
	fn := mcprest.MappedToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{},
		func(in simpleIn) (greetReq, error) { return greetReq{Name: in.Who}, nil },
		func(resp greetResp) (simpleOut, error) { return simpleOut{Message: resp.Greeting}, nil },
	)

	out, err := fn(context.Background(), simpleIn{Who: "Carol"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Message != "Hello, Carol" {
		t.Errorf("Message = %q, want %q", out.Message, "Hello, Carol")
	}
}

func TestMappedToolHandler_ToReqError_WrapsAsToolRequestMapError(t *testing.T) {
	var calls int
	srv := newGreetServer(t, func(*http.Request) { calls++ })
	defer srv.Close()

	handle := newGreetRoute()
	wantErr := errors.New("cannot build request")
	fn := mcprest.MappedToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{},
		func(simpleIn) (greetReq, error) { return greetReq{}, wantErr },
		func(resp greetResp) (simpleOut, error) { return simpleOut{Message: resp.Greeting}, nil },
	)

	_, err := fn(context.Background(), simpleIn{Who: "Dave"})
	var mapErr mcprest.ToolRequestMapError
	if !errors.As(err, &mapErr) {
		t.Fatalf("want ToolRequestMapError, got %v (%T)", err, err)
	}
	if !errors.Is(mapErr.Err, wantErr) {
		t.Errorf("mapErr.Err = %v, want %v", mapErr.Err, wantErr)
	}
	if mapErr.Method != "POST" || mapErr.Path != "/greet" {
		t.Errorf("mapErr.Method/Path = %q/%q, want POST//greet", mapErr.Method, mapErr.Path)
	}
	if calls != 0 {
		t.Errorf("want 0 underlying REST calls when toReq fails, got %d", calls)
	}
}

func TestMappedToolHandler_FromRespError_WrapsAsToolResponseMapError(t *testing.T) {
	srv := newGreetServer(t, nil)
	defer srv.Close()

	handle := newGreetRoute()
	wantErr := errors.New("cannot map response")
	fn := mcprest.MappedToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{},
		func(in simpleIn) (greetReq, error) { return greetReq{Name: in.Who}, nil },
		func(greetResp) (simpleOut, error) { return simpleOut{}, wantErr },
	)

	_, err := fn(context.Background(), simpleIn{Who: "Eve"})
	var mapErr mcprest.ToolResponseMapError
	if !errors.As(err, &mapErr) {
		t.Fatalf("want ToolResponseMapError, got %v (%T)", err, err)
	}
	if !errors.Is(mapErr.Err, wantErr) {
		t.Errorf("mapErr.Err = %v, want %v", mapErr.Err, wantErr)
	}
}

func TestMappedToolHandler_UnderlyingCallError_ForwardsUnchanged(t *testing.T) {
	srv := newFailingServer(http.StatusNotFound)
	defer srv.Close()

	var fromRespCalled bool
	handle := newGreetRoute()
	fn := mcprest.MappedToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{},
		func(in simpleIn) (greetReq, error) { return greetReq{Name: in.Who}, nil },
		func(resp greetResp) (simpleOut, error) {
			fromRespCalled = true
			return simpleOut{Message: resp.Greeting}, nil
		},
	)

	_, err := fn(context.Background(), simpleIn{Who: "Frank"})
	var statusErr nethttp.UnexpectedStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("want nethttp.UnexpectedStatusError, got %v (%T)", err, err)
	}
	if statusErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, http.StatusNotFound)
	}
	if fromRespCalled {
		t.Error("fromResp must NOT be called when the underlying REST call itself fails")
	}
}

// --- structured error LogValue ---

func TestToolRequestMapError_LogValue(t *testing.T) {
	inner := errors.New("boom")
	e := mcprest.ToolRequestMapError{Method: "POST", Path: "/greet", Err: inner}
	v := e.LogValue()
	if v.Kind().String() != "Group" {
		t.Fatalf("LogValue().Kind() = %v, want Group", v.Kind())
	}
	got := map[string]bool{}
	for _, a := range v.Group() {
		got[a.Key] = true
	}
	for _, key := range []string{"method", "path", "err"} {
		if !got[key] {
			t.Errorf("LogValue() missing key %q", key)
		}
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is(e, inner) = false, want true")
	}
}

func TestToolResponseMapError_LogValue(t *testing.T) {
	inner := errors.New("boom")
	e := mcprest.ToolResponseMapError{Method: "POST", Path: "/greet", Err: inner}
	v := e.LogValue()
	if v.Kind().String() != "Group" {
		t.Fatalf("LogValue().Kind() = %v, want Group", v.Kind())
	}
	got := map[string]bool{}
	for _, a := range v.Group() {
		got[a.Key] = true
	}
	for _, key := range []string{"method", "path", "err"} {
		if !got[key] {
			t.Errorf("LogValue() missing key %q", key)
		}
	}
	if !errors.Is(e, inner) {
		t.Error("errors.Is(e, inner) = false, want true")
	}
}

// --- ports.ToolPort composition ---

// fakeToolAdapter captures the pipeline function ToolPort.Bind receives,
// so the test can invoke it directly without a real transport server.
type fakeToolAdapter[In, Out any] struct {
	fn func(context.Context, In) gstream.Stream[Out]
}

func (a *fakeToolAdapter[In, Out]) Bind(_ context.Context, fn func(context.Context, In) gstream.Stream[Out]) error {
	a.fn = fn
	return nil
}

func (a *fakeToolAdapter[In, Out]) AdapterName() string { return "fakeToolAdapter" }

func TestToolHandler_ComposesWithToolPortSetFunc(t *testing.T) {
	srv := newGreetServer(t, nil)
	defer srv.Close()

	handle := newGreetRoute()
	domainPort, err := ports.NewToolPort[greetReq, greetResp]("greet", greetReqCodec, greetRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("NewToolPort: %v", err)
	}
	// The exact assertion this test exists for: SetFunc accepts
	// mcprest.ToolHandler's return value directly, with zero adaptation.
	domainPort.SetFunc(mcprest.ToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{}))

	adapter := &fakeToolAdapter[greetReq, greetResp]{}
	if err := domainPort.Bind(context.Background(), adapter); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	ctx := context.Background()
	values, errs := gstream.Collect(ctx, adapter.fn(ctx, greetReq{Name: "Grace"}))
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(values) != 1 || values[0].Greeting != "Hello, Grace" {
		t.Errorf("values = %+v, want one greetResp{Greeting: %q}", values, "Hello, Grace")
	}
}

// --- Examples ---

func Example_toolHandler() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"greeting": "Hello, Example"})
	}))
	defer srv.Close()

	handle := newGreetRoute()
	fn := mcprest.ToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{})

	out, err := fn(context.Background(), greetReq{Name: "Example"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(out.Greeting)
	// Output:
	// Hello, Example
}

func Example_mappedToolHandler() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"greeting": "Hello, Mapped"})
	}))
	defer srv.Close()

	handle := newGreetRoute()
	fn := mcprest.MappedToolHandler(srv.Client(), srv.URL, handle, nethttp.CallOptions{},
		func(in simpleIn) (greetReq, error) { return greetReq{Name: in.Who}, nil },
		func(resp greetResp) (simpleOut, error) { return simpleOut{Message: resp.Greeting}, nil },
	)

	out, err := fn(context.Background(), simpleIn{Who: "Mapped"})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(out.Message)
	// Output:
	// Hello, Mapped
}
