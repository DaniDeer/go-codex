package middleware_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
)

// ── Middleware/SecurityDeclaration construction ─────────────────────────────

func TestMiddleware_ZeroValue(t *testing.T) {
	var mw middleware.Middleware
	if mw.Name != "" || mw.Fn != nil || mw.Security != nil {
		t.Errorf("want zero-value Middleware to have empty fields, got %+v", mw)
	}
}

func TestClientMiddleware_ZeroValue(t *testing.T) {
	var cmw middleware.ClientMiddleware
	if cmw.Name != "" || cmw.Fn != nil {
		t.Errorf("want zero-value ClientMiddleware to have empty fields, got %+v", cmw)
	}
}

// ── DeclareSecurity ──────────────────────────────────────────────────────────

func TestDeclareSecurity_BuildsSpecOnlyMiddleware(t *testing.T) {
	scheme := route.BearerScheme("")
	codec := codex.String()
	mw := middleware.DeclareSecurity("bearerAuth", scheme, []string{"pull"}, &codec)

	if mw.Fn != nil {
		t.Errorf("want Fn nil (spec-only), got %v", mw.Fn)
	}
	if len(mw.Satisfies) != 0 {
		t.Errorf("want empty Satisfies (nothing in this codebase enforces it), got %v", mw.Satisfies)
	}
	if mw.Security == nil {
		t.Fatal("want non-nil Security")
	}
	if mw.Security.SchemeName != "bearerAuth" {
		t.Errorf("want SchemeName %q, got %q", "bearerAuth", mw.Security.SchemeName)
	}
	if mw.Security.Scheme != scheme {
		t.Errorf("want Scheme %+v, got %+v", scheme, mw.Security.Scheme)
	}
	if len(mw.Security.Scopes) != 1 || mw.Security.Scopes[0] != "pull" {
		t.Errorf("want Scopes [pull], got %v", mw.Security.Scopes)
	}
	if mw.Security.Codec != &codec {
		t.Errorf("want Codec pointer to match, got different pointer")
	}
}

// ── RequireScopes ────────────────────────────────────────────────────────────

type testReq struct{ Claims string }

func TestRequireScopes_BuildsSecurityDeclaration(t *testing.T) {
	scheme := route.BearerScheme("JWT")
	mw := middleware.RequireScopes[string, testReq]("bearerAuth", scheme, []string{"read"}, nil,
		func(ctx context.Context, raw string, req *testReq) (map[string][]string, error) {
			return map[string][]string{"bearerAuth": {"read"}}, nil
		},
	)
	if mw.Name != "require-scopes:bearerAuth" {
		t.Errorf("unexpected Name: %q", mw.Name)
	}
	if len(mw.Satisfies) != 1 || mw.Satisfies[0] != "bearerAuth" {
		t.Errorf("unexpected Satisfies: %v", mw.Satisfies)
	}
	if mw.Security == nil || mw.Security.SchemeName != "bearerAuth" {
		t.Fatalf("unexpected Security: %+v", mw.Security)
	}
	fn, ok := mw.Fn.(func(context.Context, string, *testReq) (map[string][]string, error))
	if !ok {
		t.Fatalf("Fn has wrong shape: %T", mw.Fn)
	}
	grants, err := fn(context.Background(), "token", &testReq{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if grants["bearerAuth"][0] != "read" {
		t.Errorf("unexpected grants: %v", grants)
	}
}

func TestRequireScopes_ExtractError(t *testing.T) {
	wantErr := errors.New("invalid token")
	mw := middleware.RequireScopes[string, testReq]("bearerAuth", route.BearerScheme("JWT"), nil, nil,
		func(ctx context.Context, raw string, req *testReq) (map[string][]string, error) {
			return nil, wantErr
		},
	)
	fn := mw.Fn.(func(context.Context, string, *testReq) (map[string][]string, error))
	_, err := fn(context.Background(), "bad", &testReq{})
	if !errors.Is(err, wantErr) {
		t.Errorf("want extraction error to propagate, got %v", err)
	}
}

// ── CheckScopes ──────────────────────────────────────────────────────────────

func TestCheckScopes_Satisfied(t *testing.T) {
	reqs := []route.SecurityRequirement{{"bearerAuth": {"read"}}}
	if err := middleware.CheckScopes(reqs, map[string][]string{"bearerAuth": {"read", "write"}}); err != nil {
		t.Errorf("want satisfied requirement to pass, got %v", err)
	}
}

func TestCheckScopes_Unsatisfied(t *testing.T) {
	reqs := []route.SecurityRequirement{{"bearerAuth": {"admin"}}}
	err := middleware.CheckScopes(reqs, map[string][]string{"bearerAuth": {"read"}})
	var unsatisfied middleware.UnsatisfiedScopesError
	if !errors.As(err, &unsatisfied) {
		t.Fatalf("want UnsatisfiedScopesError, got %T: %v", err, err)
	}
	if unsatisfied.LogValue().String() == "" {
		t.Error("want non-empty LogValue")
	}
}

// ── MiddlewareShapeError ─────────────────────────────────────────────────────

func TestMiddlewareShapeError(t *testing.T) {
	err := middleware.MiddlewareShapeError{Name: "auth", Expected: "func(...)", Got: "func()"}
	if err.Error() == "" {
		t.Error("want non-empty Error()")
	}
	if err.LogValue().String() == "" {
		t.Error("want non-empty LogValue")
	}
}

// ── ContextField ─────────────────────────────────────────────────────────────

func TestContextField_SetGetRoundTrip(t *testing.T) {
	field := middleware.NewContextField[string](codex.String())
	ctx := middleware.EnsureContextFields(context.Background())

	if err := field.Set(ctx, "hello"); err != nil {
		t.Fatalf("unexpected Set error: %v", err)
	}
	got, ok := field.Get(ctx)
	if !ok || got != "hello" {
		t.Errorf("want (hello, true), got (%q, %v)", got, ok)
	}
}

func TestContextField_GetWithoutSet(t *testing.T) {
	field := middleware.NewContextField[string](codex.String())
	ctx := middleware.EnsureContextFields(context.Background())

	_, ok := field.Get(ctx)
	if ok {
		t.Error("want ok=false when never Set")
	}
}

func TestContextField_GetOnUndecoratedContext(t *testing.T) {
	field := middleware.NewContextField[string](codex.String())
	_, ok := field.Get(context.Background())
	if ok {
		t.Error("want ok=false when ctx was never decorated by EnsureContextFields")
	}
}

func TestContextField_SetWithoutEnsureContextFields(t *testing.T) {
	field := middleware.NewContextField[string](codex.String())
	err := field.Set(context.Background(), "hello")
	var notPrepared middleware.ContextFieldNotPreparedError
	if !errors.As(err, &notPrepared) {
		t.Fatalf("want ContextFieldNotPreparedError, got %T: %v", err, err)
	}
}

func TestContextField_SetDecodeError(t *testing.T) {
	field := middleware.NewContextField[string](codex.String())
	ctx := middleware.EnsureContextFields(context.Background())

	err := field.Set(ctx, 42) // not a string — codec.Decode fails
	if err == nil {
		t.Error("want decode error for wrong raw type")
	}
}

func TestContextField_VisibleFromDerivedContext(t *testing.T) {
	field := middleware.NewContextField[string](codex.String())
	ctx := middleware.EnsureContextFields(context.Background())

	// A "later" middleware/handler sees a DERIVED ctx, not necessarily the
	// exact same value — the box must still be reachable.
	type unrelatedKey struct{}
	child := context.WithValue(ctx, unrelatedKey{}, "unrelated")
	if err := field.Set(child, "from-child"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := field.Get(ctx) // read from the ORIGINAL ctx
	if !ok || got != "from-child" {
		t.Errorf("want value set from a derived ctx to be visible from the original ctx, got (%q, %v)", got, ok)
	}
}

func TestEnsureContextFields_Idempotent(t *testing.T) {
	ctx := middleware.EnsureContextFields(context.Background())
	ctx2 := middleware.EnsureContextFields(ctx)

	field := middleware.NewContextField[string](codex.String())
	if err := field.Set(ctx, "value"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, ok := field.Get(ctx2)
	if !ok || got != "value" {
		t.Errorf("want the SAME box after a second EnsureContextFields call, got (%q, %v)", got, ok)
	}
}

func TestContextField_DistinctFieldsDoNotCollide(t *testing.T) {
	a := middleware.NewContextField[string](codex.String())
	b := middleware.NewContextField[string](codex.String())
	ctx := middleware.EnsureContextFields(context.Background())

	if err := a.Set(ctx, "a-value"); err != nil {
		t.Fatal(err)
	}
	if err := b.Set(ctx, "b-value"); err != nil {
		t.Fatal(err)
	}
	gotA, _ := a.Get(ctx)
	gotB, _ := b.Get(ctx)
	if gotA != "a-value" || gotB != "b-value" {
		t.Errorf("want distinct fields to hold distinct values, got a=%q b=%q", gotA, gotB)
	}
}

func TestContextFieldNotPreparedError(t *testing.T) {
	err := middleware.ContextFieldNotPreparedError{}
	if err.Error() == "" {
		t.Error("want non-empty Error()")
	}
	if err.LogValue().String() == "" {
		t.Error("want non-empty LogValue")
	}
}
