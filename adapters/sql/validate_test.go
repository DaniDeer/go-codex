package sql_test

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared test types ─────────────────────────────────────────────────────────

type testUser struct {
	ID    string
	Name  string
	Email string
	Role  string
}

var userCodec = codex.Struct(
	codex.RequiredField("id",
		codex.String().Refine(validate.UUID),
		func(u testUser) string { return u.ID },
		func(u *testUser, v string) { u.ID = v }),
	codex.RequiredField("name",
		codex.String().Refine(validate.MinLen(1), validate.MaxLen(80)),
		func(u testUser) string { return u.Name },
		func(u *testUser, v string) { u.Name = v }),
	codex.RequiredField("email",
		codex.String().Refine(validate.Email),
		func(u testUser) string { return u.Email },
		func(u *testUser, v string) { u.Email = v }),
	codex.RequiredField("role",
		codex.String(),
		func(u testUser) string { return u.Role },
		func(u *testUser, v string) { u.Role = v }),
)

// userWithCrossFieldCodec adds a cross-field RefineFunc rule.
var userWithCrossFieldCodec = userCodec.RefineFunc(func(u testUser) error {
	if u.Role == "admin" && u.Email != "admin@company.example" {
		return fmt.Errorf("admin accounts require admin@company.example email")
	}
	return nil
})

// ── V1: happy path ────────────────────────────────────────────────────────────

func TestValidate_HappyPath(t *testing.T) {
	u := testUser{
		ID:    "550e8400-e29b-41d4-a716-446655440000",
		Name:  "Ada Lovelace",
		Email: "ada@example.com",
		Role:  "member",
	}
	got, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
		Table: "users", Op: "get_user",
	})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if got != u {
		t.Errorf("want %+v, got %+v", u, got)
	}
}

// ── V2: Refine failure → RowValidationError ───────────────────────────────────

func TestValidate_RefineFailure(t *testing.T) {
	u := testUser{
		ID:    "not-a-uuid",
		Name:  "Ada",
		Email: "ada@example.com",
		Role:  "member",
	}
	_, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
		Table: "users", Op: "insert_user",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rve sqladapter.RowValidationError
	if !errors.As(err, &rve) {
		t.Fatalf("expected RowValidationError, got %T: %v", err, err)
	}
	if rve.Table != "users" {
		t.Errorf("Table: want %q, got %q", "users", rve.Table)
	}
	if rve.Op != "insert_user" {
		t.Errorf("Op: want %q, got %q", "insert_user", rve.Op)
	}
}

// ── V3: RefineFunc cross-field failure → RowValidationError ──────────────────

func TestValidate_RefineFuncCrossField(t *testing.T) {
	u := testUser{
		ID:    "550e8400-e29b-41d4-a716-446655440000",
		Name:  "Admin",
		Email: "wrong@example.com",
		Role:  "admin",
	}
	_, err := sqladapter.Validate(userWithCrossFieldCodec, u, sqladapter.ValidateOptions{
		Table: "users", Op: "insert_user",
	})
	if err == nil {
		t.Fatal("expected cross-field error, got nil")
	}
	var rve sqladapter.RowValidationError
	if !errors.As(err, &rve) {
		t.Fatalf("expected RowValidationError, got %T", err)
	}
}

// ── V4: errors.As reaches inner *codex.ValidationErrors ──────────────────────

func TestValidate_ErrorsAs_ValidationErrors(t *testing.T) {
	u := testUser{
		ID:    "not-a-uuid",
		Name:  "",
		Email: "not-an-email",
		Role:  "member",
	}
	_, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ve codex.ValidationErrors
	if !errors.As(err, &ve) {
		t.Fatalf("expected codex.ValidationErrors via Unwrap, got %T: %v", err, err)
	}
	if len(ve) == 0 {
		t.Error("expected at least one ValidationError")
	}
}

// ── V5: RowValidationError.LogValue returns slog group ────────────────────────

func TestValidate_LogValue(t *testing.T) {
	rve := sqladapter.RowValidationError{
		Table: "users",
		Op:    "insert_user",
		Err:   fmt.Errorf("bad value"),
	}
	lv := rve.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	attrs := lv.Group()
	keys := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"table", "op", "err"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

// ── V6: SQLObserver.RecordValidation called ───────────────────────────────────

type sqlSpy struct {
	stats.NoopObserver
	validations []validationRecord
	valErrors   []string
}

type validationRecord struct {
	table, op string
	dur       time.Duration
	err       error
}

func (s *sqlSpy) RecordValidation(table, op string, dur time.Duration, err error) {
	s.validations = append(s.validations, validationRecord{table, op, dur, err})
}

func (s *sqlSpy) RecordValidationError(location, constraint, field string) {
	s.valErrors = append(s.valErrors, location+"/"+constraint+"/"+field)
}

func TestValidate_ObserverCalled(t *testing.T) {
	u := testUser{
		ID:    "550e8400-e29b-41d4-a716-446655440000",
		Name:  "Ada",
		Email: "ada@example.com",
		Role:  "member",
	}
	spy := &sqlSpy{}
	_, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
		Table: "users", Op: "get_user", Observer: spy,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spy.validations) != 1 {
		t.Fatalf("RecordValidation: want 1 call, got %d", len(spy.validations))
	}
	rec := spy.validations[0]
	if rec.table != "users" || rec.op != "get_user" || rec.err != nil {
		t.Errorf("RecordValidation: got %+v", rec)
	}
}

func TestValidate_ObserverCalledOnFailure(t *testing.T) {
	u := testUser{ID: "bad", Name: "", Email: "bad", Role: "member"}
	spy := &sqlSpy{}
	_, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
		Table: "users", Op: "insert_user", Observer: spy,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if len(spy.validations) != 1 {
		t.Fatalf("RecordValidation: want 1 call on failure, got %d", len(spy.validations))
	}
	if spy.validations[0].err == nil {
		t.Error("RecordValidation: want non-nil err on failure")
	}
	if len(spy.valErrors) == 0 {
		t.Error("RecordValidationError: want at least one field error reported")
	}
}

// ── V7: nil Observer → no panic ───────────────────────────────────────────────

func TestValidate_NilObserver(t *testing.T) {
	u := testUser{
		ID:    "550e8400-e29b-41d4-a716-446655440000",
		Name:  "Ada",
		Email: "ada@example.com",
		Role:  "member",
	}
	// Must not panic.
	_, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{Observer: nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── V8: plain Observer (no SQLObserver) → type assertion falls back safely ────

type plainSpy struct {
	stats.NoopObserver
	valErrors int
}

func (s *plainSpy) RecordValidationError(_, _, _ string) { s.valErrors++ }

func TestValidate_PlainObserver(t *testing.T) {
	u := testUser{ID: "bad", Name: "", Email: "bad", Role: "member"}
	spy := &plainSpy{}
	_, err := sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
		Table: "users", Op: "insert_user", Observer: spy,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// plainSpy does not implement SQLObserver — must not panic.
	if spy.valErrors == 0 {
		t.Error("RecordValidationError should still be called even without SQLObserver")
	}
}

// ── Example ───────────────────────────────────────────────────────────────────

func ExampleValidate() {
	type Item struct {
		ID   string
		Name string
	}
	itemCodec := codex.Struct(
		codex.RequiredField("id",
			codex.String(),
			func(i Item) string { return i.ID },
			func(i *Item, v string) { i.ID = v }),
		codex.RequiredField("name",
			codex.String().Refine(validate.NonEmptyString),
			func(i Item) string { return i.Name },
			func(i *Item, v string) { i.Name = v }),
	)

	// Pre-query: validate before writing to the database.
	params := Item{ID: "1", Name: "Widget"}
	validated, err := sqladapter.Validate(itemCodec, params, sqladapter.ValidateOptions{
		Table: "items", Op: "insert_item",
	})
	if err != nil {
		fmt.Println("invalid:", err)
		return
	}
	fmt.Println("validated:", validated.Name)
	// Output:
	// validated: Widget
}
