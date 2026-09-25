package httpsecurity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/httpsecurity"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

type req struct{ Name string }

func TestRunSecurityMiddlewareReflect_grantsScopes(t *testing.T) {
	fn := func(ctx context.Context, r *http.Request, req *req) (map[string][]string, error) {
		return map[string][]string{"apiKey": {"read"}}, nil
	}
	impls := []middleware.ServerImplementation{{Name: "apiKey", Satisfies: []string{"apiKey"}, Fn: fn}}
	secReqs := []route.SecurityRequirement{{"apiKey": {"read"}}}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	if err := httpsecurity.RunSecurityMiddlewareReflect(context.Background(), r, reqPtr, impls, secReqs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunSecurityMiddlewareReflect_missingScope(t *testing.T) {
	fn := func(ctx context.Context, r *http.Request, req *req) (map[string][]string, error) {
		return map[string][]string{"apiKey": {"read"}}, nil
	}
	impls := []middleware.ServerImplementation{{Name: "apiKey", Satisfies: []string{"apiKey"}, Fn: fn}}
	secReqs := []route.SecurityRequirement{{"apiKey": {"write"}}}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	err := httpsecurity.RunSecurityMiddlewareReflect(context.Background(), r, reqPtr, impls, secReqs)
	if err == nil {
		t.Fatal("expected an error for missing scope, got nil")
	}
}

func TestRunSecurityMiddlewareReflect_implError(t *testing.T) {
	wantErr := errors.New("boom")
	fn := func(ctx context.Context, r *http.Request, req *req) (map[string][]string, error) {
		return nil, wantErr
	}
	impls := []middleware.ServerImplementation{{Name: "apiKey", Satisfies: []string{"apiKey"}, Fn: fn}}
	secReqs := []route.SecurityRequirement{{"apiKey": {"read"}}}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	err := httpsecurity.RunSecurityMiddlewareReflect(context.Background(), r, reqPtr, impls, secReqs)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr, got %v", err)
	}
}

func TestRunSecurityMiddlewareReflect_generalPurposeSkipped(t *testing.T) {
	// A general-purpose Fn (func(http.Handler) http.Handler, 1 arg) must
	// be skipped by the NumIn()!=3 guard, not misinterpreted as a security
	// Fn.
	generalFn := func(next http.Handler) http.Handler { return next }
	impls := []middleware.ServerImplementation{{Name: "general", Fn: generalFn}}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	if err := httpsecurity.RunSecurityMiddlewareReflect(context.Background(), r, reqPtr, impls, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
