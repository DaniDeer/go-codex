package reqreply_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// capturingServerTransport records the fn passed to Serve for each
// registered route/topic, keyed by the route's Topic field (read via a
// type switch over the handful of route/handle shapes this test needs) —
// letting a test invoke the real dispatch handler directly, without any
// real network transport.
type capturingServerTransport struct {
	fns     map[string]any
	handles map[string]*reqreply.RouteHandle[reqreply.SpecReq, []byte]
}

func (t *capturingServerTransport) Serve(ctx context.Context, route any, fn any) error {
	if t.fns == nil {
		t.fns = make(map[string]any)
		t.handles = make(map[string]*reqreply.RouteHandle[reqreply.SpecReq, []byte])
	}
	switch v := route.(type) {
	case *reqreply.RouteHandle[reqreply.SpecReq, []byte]:
		t.fns[v.Topic] = fn
		t.handles[v.Topic] = v
	}
	<-ctx.Done()
	return nil
}

func TestServeSpec_HappyPath_BothFormats(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "Test API", Version: "1.0.0"})
	if _, err := ComputeRoute.Register(s); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := s.ServeSpec("spec"); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	ft := &capturingServerTransport{}
	if err := s.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = s.Serve(ctx) // blocks until ctx's timeout, capturing every route's fn along the way

	fnAny, ok := ft.fns["spec"]
	if !ok {
		t.Fatalf("ServeSpec's handler was never captured — topic %q not dispatched", "spec")
	}
	fn, ok := fnAny.(func(context.Context, reqreply.SpecReq) ([]byte, error))
	if !ok {
		t.Fatalf("handler has unexpected type %T", fnAny)
	}

	yamlBytes, err := fn(context.Background(), reqreply.SpecReq{})
	if err != nil {
		t.Fatalf("handler (yaml default): %v", err)
	}
	if len(yamlBytes) == 0 {
		t.Error("yaml output is empty")
	}
	if !strings.Contains(string(yamlBytes), "compute") {
		t.Error("yaml output does not reference the registered route")
	}

	jsonBytes, err := fn(context.Background(), reqreply.SpecReq{Format: "json"})
	if err != nil {
		t.Fatalf("handler (json): %v", err)
	}
	if len(jsonBytes) == 0 {
		t.Error("json output is empty")
	}
	if !strings.Contains(string(jsonBytes), "compute") {
		t.Error("json output does not reference the registered route")
	}
}

func TestServeSpec_EmptyTopic_Error(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "Test API", Version: "1.0.0"})
	_, err := s.ServeSpec("")
	if err == nil {
		t.Fatal("want error for empty topic, got nil")
	}
	var topicErr reqreply.SpecTopicRequiredError
	if !errors.As(err, &topicErr) {
		t.Fatalf("want SpecTopicRequiredError, got %T: %v", err, err)
	}
	if got := topicErr.LogValue().Kind().String(); got != "Group" {
		t.Errorf("LogValue kind = %q, want Group", got)
	}
}

func TestServeSpec_DuplicateTopic_Error(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "Test API", Version: "1.0.0"})
	if _, err := s.ServeSpec("spec"); err != nil {
		t.Fatalf("first ServeSpec: %v", err)
	}
	_, err := s.ServeSpec("spec")
	if err == nil {
		t.Fatal("want DuplicateRouteError for a second ServeSpec on the same topic, got nil")
	}
	var dup reqreply.DuplicateRouteError
	if !errors.As(err, &dup) {
		t.Fatalf("want DuplicateRouteError, got %T: %v", err, err)
	}
	if dup.Topic != "spec" {
		t.Errorf("DuplicateRouteError.Topic = %q, want %q", dup.Topic, "spec")
	}
}

func TestServeSpec_WithSpecMiddleware(t *testing.T) {
	s := reqreply.NewServer(reqreply.Info{Title: "Test API", Version: "1.0.0"})
	marker := func() {}
	if _, err := s.ServeSpec("spec", reqreply.WithSpecMiddleware(nil, marker)); err != nil {
		t.Fatalf("ServeSpec: %v", err)
	}

	ft := &capturingServerTransport{}
	if err := s.Attach(ft); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = s.Serve(ctx)

	handle, ok := ft.handles["spec"]
	if !ok {
		t.Fatalf("ServeSpec's handle was never captured")
	}
	if len(handle.Implementations) != 1 {
		t.Fatalf("want 1 Implementation, got %d", len(handle.Implementations))
	}
	if handle.Implementations[0].Name != "implement:general" {
		t.Errorf("want implement:general, got %q", handle.Implementations[0].Name)
	}
}

// ExampleServer_ServeSpec demonstrates registering a self-serving
// AsyncAPI spec topic — format selection travels in the request body
// ([reqreply.SpecReq.Format]), since reqreply has no `Accept`-header
// equivalent and must stay portable across adapters (mqtt5, zeromq)
// that do not all support message properties.
func ExampleServer_ServeSpec() {
	s := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})

	if _, err := ComputeRoute.Register(s); err != nil {
		fmt.Println("register error:", err)
		return
	}
	if _, err := s.ServeSpec("spec"); err != nil {
		fmt.Println("ServeSpec error:", err)
		return
	}

	fmt.Println("spec topic registered")
	// Output:
	// spec topic registered
}
