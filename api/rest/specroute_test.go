package rest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
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
