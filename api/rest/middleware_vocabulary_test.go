package rest_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests the 3 public error aliases middleware_vocabulary.go adds
// onto internal/middleware's own error types (MiddlewareShapeError,
// ContextFieldNotPreparedError, UnsatisfiedScopesError) -- confirming
// errors.As works against each WITHOUT ever importing internal/middleware.
// MiddlewareShapeError already has end-to-end adapter-dispatch coverage
// (see e.g. adapters/nethttp/binding_test.go); these 2 are directly
// testable via the public surface alone (ContextField.Set / CheckScopes),
// so no adapter is needed here.

func TestContextField_Set_NotPrepared_ReturnsPublicAlias(t *testing.T) {
	field := rest.NewContextField(codex.String())
	// A bare context.Background() never went through the dispatch-internal
	// EnsureContextFields preparation step a real adapter always performs
	// before invoking a middleware's own Fn.
	err := field.Set(context.Background(), "value")
	var notPrepared rest.ContextFieldNotPreparedError
	if !errors.As(err, &notPrepared) {
		t.Fatalf("want rest.ContextFieldNotPreparedError, got %v (%T)", err, err)
	}
}

func TestCheckScopes_Unsatisfied_ReturnsPublicAlias(t *testing.T) {
	reqs := []rest.SecurityRequirement{rest.Require("bearer", "read:users")}
	granted := map[string][]string{"bearer": {"write:users"}} // missing read:users
	err := rest.CheckScopes(reqs, granted)
	var unsatisfied rest.UnsatisfiedScopesError
	if !errors.As(err, &unsatisfied) {
		t.Fatalf("want rest.UnsatisfiedScopesError, got %v (%T)", err, err)
	}
}
