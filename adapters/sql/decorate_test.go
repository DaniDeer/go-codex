package sql_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/stats"
)

// ── DecorateInput ─────────────────────────────────────────────────────────────

// D1: happy path — fn is called with the validated (normalized) value.
func TestDecorateInput_HappyPath(t *testing.T) {
	var called bool
	var gotArg testUser
	insertUser := sqladapter.DecorateInput(
		func(_ context.Context, u testUser) error {
			called = true
			gotArg = u
			return nil
		},
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "insert_user"},
	)

	u := testUser{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Ada", Email: "ada@example.com", Role: "user"}
	if err := insertUser(context.Background(), u); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("want fn to be called on valid input")
	}
	if gotArg != u {
		t.Errorf("want fn called with %+v, got %+v", u, gotArg)
	}
}

// D2: invalid input — fn is NEVER called; typed error returned.
func TestDecorateInput_RejectsInvalid_FnNeverCalled(t *testing.T) {
	var called bool
	insertUser := sqladapter.DecorateInput(
		func(_ context.Context, u testUser) error {
			called = true
			return nil
		},
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "insert_user"},
	)

	invalid := testUser{ID: "not-a-uuid", Name: "Ada", Email: "ada@example.com", Role: "user"}
	err := insertUser(context.Background(), invalid)
	if err == nil {
		t.Fatal("want error for invalid input")
	}
	if called {
		t.Error("want fn NEVER called on invalid input")
	}
	var rve sqladapter.RowValidationError
	if !errors.As(err, &rve) || rve.Table != "users" || rve.Op != "insert_user" {
		t.Errorf("want RowValidationError{users,insert_user}, got %+v", err)
	}
}

// D3: nil Observer resolves from ctx (DecorateInput has ctx, unlike bare Validate).
func TestDecorateInput_ResolvesObserverFromContext(t *testing.T) {
	obs := &sqlObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)

	insertUser := sqladapter.DecorateInput(
		func(_ context.Context, u testUser) error { return nil },
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "insert_user"}, // Observer left nil
	)
	u := testUser{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Ada", Email: "ada@example.com", Role: "user"}
	if err := insertUser(ctx, u); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.validations != 1 {
		t.Errorf("want 1 RecordValidation via ctx observer, got %d", obs.validations)
	}
}

// ── DecorateOutput ────────────────────────────────────────────────────────────

// D4: happy path — fn's result is validated and returned.
func TestDecorateOutput_HappyPath(t *testing.T) {
	want := testUser{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Ada", Email: "ada@example.com", Role: "user"}
	getUser := sqladapter.DecorateOutput(
		func(_ context.Context, id string) (testUser, error) { return want, nil },
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "get_user"},
	)

	got, err := getUser(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != want {
		t.Errorf("want %+v, got %+v", want, got)
	}
}

// D5: fn's own error is returned unchanged — validation never runs.
func TestDecorateOutput_FnError_PassedThrough(t *testing.T) {
	sentinel := errors.New("sql: no rows in result set")
	getUser := sqladapter.DecorateOutput(
		func(_ context.Context, id string) (testUser, error) { return testUser{}, sentinel },
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "get_user"},
	)

	_, err := getUser(context.Background(), "missing")
	if !errors.Is(err, sentinel) {
		t.Errorf("want sentinel error passed through unchanged, got %v", err)
	}
}

// D6: fn succeeds but returned value fails validation.
func TestDecorateOutput_InvalidResult(t *testing.T) {
	invalid := testUser{ID: "not-a-uuid", Name: "Ada", Email: "ada@example.com", Role: "user"}
	getUser := sqladapter.DecorateOutput(
		func(_ context.Context, id string) (testUser, error) { return invalid, nil },
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "get_user"},
	)

	_, err := getUser(context.Background(), "id")
	var rve sqladapter.RowValidationError
	if !errors.As(err, &rve) || rve.Table != "users" || rve.Op != "get_user" {
		t.Errorf("want RowValidationError{users,get_user}, got %v", err)
	}
}

// D7: nil Observer resolves from ctx.
func TestDecorateOutput_ResolvesObserverFromContext(t *testing.T) {
	obs := &sqlObserverSpy{}
	ctx := stats.WithObserver(context.Background(), obs)
	want := testUser{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Ada", Email: "ada@example.com", Role: "user"}

	getUser := sqladapter.DecorateOutput(
		func(_ context.Context, id string) (testUser, error) { return want, nil },
		userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "get_user"}, // Observer left nil
	)
	if _, err := getUser(ctx, want.ID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.validations != 1 {
		t.Errorf("want 1 RecordValidation via ctx observer, got %d", obs.validations)
	}
}

// ── Examples ──────────────────────────────────────────────────────────────────

// ExampleDecorateInput shows wrapping an exec-style sqlc function once,
// getting back a drop-in replacement that validates before every call.
func ExampleDecorateInput() {
	// Stand-in for an sqlc-generated ":exec" method.
	insertUserSQL := func(ctx context.Context, u testUser) error {
		fmt.Println("INSERT user:", u.Name)
		return nil
	}

	insertUser := sqladapter.DecorateInput(insertUserSQL, userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "insert_user"})

	// Call in place of the sqlc method everywhere — validated automatically.
	valid := testUser{ID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Name: "Ada", Email: "ada@example.com", Role: "user"}
	if err := insertUser(context.Background(), valid); err != nil {
		fmt.Println("unexpected error:", err)
	}

	// Invalid input never reaches the sqlc method.
	invalid := testUser{ID: "not-a-uuid", Name: "Ada", Email: "ada@example.com", Role: "user"}
	if err := insertUser(context.Background(), invalid); err != nil {
		fmt.Println("rejected before insert:", err != nil)
	}
	// Output:
	// INSERT user: Ada
	// rejected before insert: true
}

// ExampleDecorateOutput shows wrapping a query-style sqlc function once,
// getting back a drop-in replacement that validates every returned row.
func ExampleDecorateOutput() {
	// Stand-in for an sqlc-generated ":one" method.
	getUserSQL := func(ctx context.Context, id string) (testUser, error) {
		return testUser{ID: id, Name: "Ada", Email: "ada@example.com", Role: "user"}, nil
	}

	getUser := sqladapter.DecorateOutput(getUserSQL, userCodec,
		sqladapter.ValidateOptions{Table: "users", Op: "get_user"})

	u, err := getUser(context.Background(), "f47ac10b-58cc-4372-a567-0e02b2c3d479")
	if err != nil {
		fmt.Println("unexpected error:", err)
		return
	}
	fmt.Println("validated:", u.Name)
	// Output:
	// validated: Ada
}

// sqlObserverSpy is a minimal stats.SQLObserver spy for the ctx-resolution tests.
type sqlObserverSpy struct {
	stats.NoopObserver
	validations int
}

func (o *sqlObserverSpy) RecordValidation(_, _ string, _ time.Duration, _ error) {
	o.validations++
}
