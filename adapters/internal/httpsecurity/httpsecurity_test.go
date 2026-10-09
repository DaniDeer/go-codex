package httpsecurity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/DaniDeer/go-codex/adapters/internal/httpsecurity"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
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

// ── CollectGrantsReflect / MergeMiddlewareHandlerGrants (docs/roadmap/
// declarative-middleware-layering.md's Rollout Phase A) ──────────────────

func TestCollectGrantsReflect_GrantsScopes_NoCheckScopesCall(t *testing.T) {
	fn := func(ctx context.Context, r *http.Request, req *req) (map[string][]string, error) {
		return map[string][]string{"apiKey": {"read"}}, nil
	}
	impls := []middleware.ServerImplementation{{Name: "apiKey", Satisfies: []string{"apiKey"}, Fn: fn}}
	secReqs := []route.SecurityRequirement{{"apiKey": {"write"}}} // deliberately NOT satisfied by "read"

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	granted, err := httpsecurity.CollectGrantsReflect(context.Background(), r, reqPtr, impls, secReqs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CollectGrantsReflect itself never calls CheckScopes — a mismatched
	// scope here must NOT surface as an error from this function.
	if got := granted["apiKey"]; len(got) != 1 || got[0] != "read" {
		t.Errorf("want granted[apiKey]=[read], got %v", granted)
	}
}

func TestCollectGrantsReflect_ImplError(t *testing.T) {
	wantErr := errors.New("boom")
	fn := func(ctx context.Context, r *http.Request, req *req) (map[string][]string, error) {
		return nil, wantErr
	}
	impls := []middleware.ServerImplementation{{Name: "apiKey", Satisfies: []string{"apiKey"}, Fn: fn}}
	secReqs := []route.SecurityRequirement{{"apiKey": {"read"}}}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	_, err := httpsecurity.CollectGrantsReflect(context.Background(), r, reqPtr, impls, secReqs)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected wantErr, got %v", err)
	}
}

type grantedScopesOut struct {
	GrantedScopes map[string][]string
}

func TestMergeMiddlewareHandlerGrants_MergesFromOut(t *testing.T) {
	granted := map[string][]string{}
	satisfiesPerHandler := [][]string{{"bearerAuth"}}
	outs := []any{grantedScopesOut{GrantedScopes: map[string][]string{"bearerAuth": {"profile"}}}}

	httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler, outs)

	if got := granted["bearerAuth"]; len(got) != 1 || got[0] != "profile" {
		t.Errorf("want granted[bearerAuth]=[profile], got %v", granted)
	}
}

func TestMergeMiddlewareHandlerGrants_EmptySatisfies_NoOp(t *testing.T) {
	granted := map[string][]string{}
	satisfiesPerHandler := [][]string{nil}
	outs := []any{grantedScopesOut{GrantedScopes: map[string][]string{"bearerAuth": {"profile"}}}}

	httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler, outs)

	if len(granted) != 0 {
		t.Errorf("want no-op for empty Satisfies, got %v", granted)
	}
}

func TestMergeMiddlewareHandlerGrants_NoGrantedScopesField_NoOp(t *testing.T) {
	type outWithoutGrants struct{ Value string }
	granted := map[string][]string{}
	satisfiesPerHandler := [][]string{{"bearerAuth"}}
	outs := []any{outWithoutGrants{Value: "x"}}

	httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler, outs)

	if len(granted) != 0 {
		t.Errorf("want no-op when Out carries no GrantedScopes field, got %v", granted)
	}
}

func TestMergeMiddlewareHandlerGrants_CombinesWithCollectGrantsReflect(t *testing.T) {
	// End-to-end: legacy impls contribute ONE scheme's grants, a
	// MiddlewareHandler-dispatched Out contributes ANOTHER — ONE final
	// middleware.CheckScopes call sees BOTH, satisfying an AND-combined
	// requirement spanning both schemes.
	fn := func(ctx context.Context, r *http.Request, req *req) (map[string][]string, error) {
		return map[string][]string{"apiKey": nil}, nil
	}
	impls := []middleware.ServerImplementation{{Name: "apiKey", Satisfies: []string{"apiKey"}, Fn: fn}}
	secReqs := []route.SecurityRequirement{{"apiKey": nil, "bearerAuth": {"profile"}}}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	reqPtr := reflect.ValueOf(&req{Name: "x"})

	granted, err := httpsecurity.CollectGrantsReflect(context.Background(), r, reqPtr, impls, secReqs)
	if err != nil {
		t.Fatalf("CollectGrantsReflect: %v", err)
	}

	satisfiesPerHandler := [][]string{{"bearerAuth"}}
	outs := []any{grantedScopesOut{GrantedScopes: map[string][]string{"bearerAuth": {"profile"}}}}
	httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler, outs)

	if err := middleware.CheckScopes(secReqs, granted); err != nil {
		t.Errorf("want CheckScopes to pass combining both sources, got %v", err)
	}
}
