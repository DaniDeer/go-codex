package rest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/render/openapi"
)

// specTestRoute registers one plain route on b, for ServeSpec's own
// OpenAPISpec() aggregation to have something to serve.
func specTestRoute(t *testing.T, b *rest.Server) {
	t.Helper()
	_, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
}

// ExampleServer_ServeSpec demonstrates registering a self-serving
// OpenAPI spec endpoint — the route computes and caches b's own
// [rest.Server.OpenAPISpec] on first request, and negotiates YAML
// (default) vs JSON via a normal Accept header, exactly like any other
// route's [rest.RouteHandle.WithFormats] declaration.
func ExampleServer_ServeSpec() {
	b := rest.NewServer(rest.Info{Title: "User API", Version: "1.0.0"})

	_, err := rest.NewRoute[createReq, userResp]("POST", "/users",
		createReqCodec, userCodec,
		rest.RouteMeta{OperationID: "createUser"},
	).RegisterHandle(b)
	if err != nil {
		fmt.Println("register error:", err)
		return
	}

	if err := b.ServeSpec("/openapi.yaml"); err != nil {
		fmt.Println("ServeSpec error:", err)
		return
	}

	fmt.Println("spec endpoint registered")
	// Output:
	// spec endpoint registered
}

func TestServeSpec_HappyPath_BothFormats(t *testing.T) {
	b := rest.NewServer(testInfo)
	specTestRoute(t, b)

	if err := b.ServeSpec("/openapi.yaml"); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	entries := b.RouteEntries()
	var handle *rest.RouteHandle[struct{}, openapi.Document]
	for _, e := range entries {
		if e.Path() == "/openapi.yaml" {
			if h, ok := e.Handle().(*rest.RouteHandle[struct{}, openapi.Document]); ok {
				handle = h
			}
		}
	}
	if handle == nil {
		t.Fatalf("ServeSpec route not found among RouteEntries")
	}
	if len(handle.Formats) != 2 {
		t.Fatalf("want 2 formats (yaml, json), got %d", len(handle.Formats))
	}
	if ct := handle.Formats[0].ContentType(); ct != "application/yaml" {
		t.Errorf("want default (first) format application/yaml, got %q", ct)
	}
	if ct := handle.Formats[1].ContentType(); ct != "application/json" {
		t.Errorf("want second format application/json, got %q", ct)
	}

	fn, ok := handle.HandlerFn.(func(context.Context, struct{}) (openapi.Document, error))
	if !ok {
		t.Fatalf("HandlerFn has unexpected type %T", handle.HandlerFn)
	}
	doc, err := fn(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	yamlBytes, err := handle.Formats[0].Marshal(doc)
	if err != nil {
		t.Fatalf("yaml Marshal: %v", err)
	}
	if len(yamlBytes) == 0 {
		t.Error("yaml Marshal produced empty output")
	}
	jsonBytes, err := handle.Formats[1].Marshal(doc)
	if err != nil {
		t.Fatalf("json Marshal: %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Error("json Marshal produced empty output")
	}
}

func TestServeSpec_Formats_NotDecodable(t *testing.T) {
	b := rest.NewServer(testInfo)
	if err := b.ServeSpec("/openapi.yaml"); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	entries := b.RouteEntries()
	var handle *rest.RouteHandle[struct{}, openapi.Document]
	for _, e := range entries {
		if e.Path() == "/openapi.yaml" {
			if h, ok := e.Handle().(*rest.RouteHandle[struct{}, openapi.Document]); ok {
				handle = h
			}
		}
	}
	if handle == nil {
		t.Fatalf("ServeSpec route not found among RouteEntries")
	}

	wantFormats := []string{"yaml", "json"}
	for i, f := range handle.Formats {
		_, err := f.Unmarshal([]byte("irrelevant"))
		if err == nil {
			t.Fatalf("Formats[%d].Unmarshal: want error, got nil", i)
		}
		var notDecodable rest.SpecDocumentNotDecodableError
		if !errors.As(err, &notDecodable) {
			t.Fatalf("Formats[%d].Unmarshal: want SpecDocumentNotDecodableError, got %T: %v", i, err, err)
		}
		if notDecodable.Format != wantFormats[i] {
			t.Errorf("Formats[%d]: Format = %q, want %q", i, notDecodable.Format, wantFormats[i])
		}
		if got := notDecodable.LogValue().Kind().String(); got != "Group" {
			t.Errorf("Formats[%d]: LogValue kind = %q, want Group", i, got)
		}
	}
}

func TestServeSpec_EmptyPath_Error(t *testing.T) {
	b := rest.NewServer(testInfo)
	err := b.ServeSpec("")
	if err == nil {
		t.Fatal("want error for empty path, got nil")
	}
	var pathErr rest.SpecPathRequiredError
	if !errors.As(err, &pathErr) {
		t.Fatalf("want SpecPathRequiredError, got %T: %v", err, err)
	}
	if got := pathErr.LogValue().Kind().String(); got != "Group" {
		t.Errorf("LogValue kind = %q, want Group", got)
	}
}

func TestServeSpec_LazyCache_ReflectsLaterRoutes(t *testing.T) {
	b := rest.NewServer(testInfo)
	if err := b.ServeSpec("/openapi.yaml"); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	entries := b.RouteEntries()
	var handle *rest.RouteHandle[struct{}, openapi.Document]
	for _, e := range entries {
		if e.Path() == "/openapi.yaml" {
			if h, ok := e.Handle().(*rest.RouteHandle[struct{}, openapi.Document]); ok {
				handle = h
			}
		}
	}
	if handle == nil {
		t.Fatalf("ServeSpec route not found among RouteEntries")
	}
	fn := handle.HandlerFn.(func(context.Context, struct{}) (openapi.Document, error))

	// Register a route AFTER ServeSpec but BEFORE the first call — the
	// cache is lazy (filled on first request), so this route must still
	// appear in the served document.
	specTestRoute(t, b)

	doc, err := fn(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	jsonBytes, err := doc.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if !strings.Contains(string(jsonBytes), "/users") {
		t.Error("spec document does not reflect route registered before the first call")
	}

	// A SECOND call must reuse the cache: registering yet another route
	// now must NOT appear (cache is filled, not re-filled).
	_, err = rest.NewRoute[createReq, userResp]("GET", "/users/{id}",
		createReqCodec, userCodec,
		rest.RouteMeta{OperationID: "getUser"},
	).RegisterHandle(b)
	if err != nil {
		t.Fatalf("RegisterHandle: %v", err)
	}
	doc2, err := fn(context.Background(), struct{}{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	jsonBytes2, err := doc2.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if strings.Contains(string(jsonBytes2), "/users/{id}") {
		t.Error("spec document was recomputed after being cached — ServeSpec must cache on first call")
	}
}

// TestServeSpec_OptsOutOfGlobalSecurity guards a real, confirmed
// regression class: ServeSpec's internal route declares an explicit,
// non-nil EMPTY Security slice specifically so it does NOT inherit a
// Server-level AddGlobalSecurity requirement the way a route with nil
// Security would. This exact gap (forgetting the opt-out) caused a real
// end-to-end hang in api/reqreply's own ServeSpec during development —
// this test exists so the equivalent regression in api/rest is caught
// immediately, not rediscovered by hand.
func TestServeSpec_OptsOutOfGlobalSecurity(t *testing.T) {
	b := rest.NewServer(testInfo)
	b.AddGlobalSecurity(route.Require("bearerAuth"))
	if err := b.ServeSpec("/openapi.yaml"); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	entries := b.RouteEntries()
	var handle *rest.RouteHandle[struct{}, openapi.Document]
	for _, e := range entries {
		if e.Path() == "/openapi.yaml" {
			if h, ok := e.Handle().(*rest.RouteHandle[struct{}, openapi.Document]); ok {
				handle = h
			}
		}
	}
	if handle == nil {
		t.Fatalf("ServeSpec route not found among RouteEntries")
	}

	// GlobalSecurity still reflects the Server's own declaration (proving
	// AddGlobalSecurity really was set) ...
	if len(handle.GlobalSecurity) != 1 {
		t.Fatalf("want Server.GlobalSecurity to carry 1 requirement, got %d", len(handle.GlobalSecurity))
	}
	// ... but Descriptor.Security is a non-nil EMPTY slice, NOT nil — the
	// adapter-level resolution (`if reqs == nil { reqs = GlobalSecurity }`)
	// only falls back to GlobalSecurity when Security is nil. A nil slice
	// here would silently re-inherit the global requirement; this is the
	// exact bug this test exists to catch.
	if handle.Descriptor.Security == nil {
		t.Fatal("ServeSpec's route has nil Descriptor.Security — it will INHERIT AddGlobalSecurity instead of opting out")
	}
	if len(handle.Descriptor.Security) != 0 {
		t.Errorf("want Descriptor.Security to be empty (opt-out), got %d requirement(s)", len(handle.Descriptor.Security))
	}
}

func TestServeSpec_WithSpecMiddleware(t *testing.T) {
	b := rest.NewServer(testInfo)
	specTestRoute(t, b)

	marker := func() {}
	if err := b.ServeSpec("/openapi.yaml", rest.WithSpecMiddleware(nil, marker)); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	entries := b.RouteEntries()
	var handle *rest.RouteHandle[struct{}, openapi.Document]
	for _, e := range entries {
		if e.Path() == "/openapi.yaml" {
			if h, ok := e.Handle().(*rest.RouteHandle[struct{}, openapi.Document]); ok {
				handle = h
			}
		}
	}
	if handle == nil {
		t.Fatalf("ServeSpec route not found among RouteEntries")
	}
	if len(handle.Implementations) != 1 {
		t.Fatalf("want 1 Implementation, got %d", len(handle.Implementations))
	}
	if handle.Implementations[0].Name != "implement:general" {
		t.Errorf("want implement:general, got %q", handle.Implementations[0].Name)
	}
}
