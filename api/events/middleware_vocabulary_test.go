package events_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests the 3 public error aliases middleware_vocabulary.go adds
// onto internal/middleware's own error types (MiddlewareShapeError,
// ContextFieldNotPreparedError, UnsatisfiedScopesError) -- confirming
// errors.As works against each WITHOUT ever importing internal/middleware.
// MiddlewareShapeError already has end-to-end adapter-dispatch coverage
// (see e.g. adapters/mqtt5/caller_test.go); these 2 are directly testable
// via the public surface alone (ContextField.Set / CheckScopes), so no
// adapter is needed here.

func TestContextField_Set_NotPrepared_ReturnsPublicAlias(t *testing.T) {
	field := events.NewContextField(codex.String())
	// A bare context.Background() never went through the dispatch-internal
	// EnsureContextFields preparation step a real adapter always performs
	// before invoking a middleware's own Fn.
	err := field.Set(context.Background(), "value")
	var notPrepared events.ContextFieldNotPreparedError
	if !errors.As(err, &notPrepared) {
		t.Fatalf("want events.ContextFieldNotPreparedError, got %v (%T)", err, err)
	}
}

func TestCheckScopes_Unsatisfied_ReturnsPublicAlias(t *testing.T) {
	reqs := []events.SecurityRequirement{events.Require("bearer", "read:sensors")}
	granted := map[string][]string{"bearer": {"write:sensors"}} // missing read:sensors
	err := events.CheckScopes(reqs, granted)
	var unsatisfied events.UnsatisfiedScopesError
	if !errors.As(err, &unsatisfied) {
		t.Fatalf("want events.UnsatisfiedScopesError, got %v (%T)", err, err)
	}
}
