package sql_test

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/stats"
)

//go:embed testdata/migrations/*.sql
var testMigrationsFS embed.FS

const testDialect = "sqlite3"

// openTestDB returns a fresh in-memory SQLite database and closes it on test
// cleanup. Each test uses a unique DSN to avoid shared state.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=private", t.Name())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ── M1: Up applies migrations ─────────────────────────────────────────────────

func TestMigrator_Up(t *testing.T) {
	db := openTestDB(t)
	m, err := sqladapter.NewMigrator(db, testMigrationsFS, "testdata/migrations", testDialect)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}

	if err := m.Up(context.Background(), sqladapter.MigrateOptions{}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, s := range statuses {
		if s.AppliedAt.IsZero() {
			t.Errorf("migration %q should be applied, but AppliedAt is zero", s.Name)
		}
	}
}

// ── M2: Down rolls back one migration ─────────────────────────────────────────

func TestMigrator_Down(t *testing.T) {
	db := openTestDB(t)
	m, err := sqladapter.NewMigrator(db, testMigrationsFS, "testdata/migrations", testDialect)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}

	if err := m.Up(context.Background(), sqladapter.MigrateOptions{}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	if err := m.Down(context.Background(), sqladapter.MigrateOptions{}); err != nil {
		t.Fatalf("Down: %v", err)
	}

	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status after Down: %v", err)
	}

	// After one Down, at least one migration should be pending.
	pending := 0
	for _, s := range statuses {
		if s.AppliedAt.IsZero() {
			pending++
		}
	}
	if pending == 0 {
		t.Error("expected at least one pending migration after Down, but all are applied")
	}
}

// ── M3: Status on fresh DB shows all pending ──────────────────────────────────

func TestMigrator_Status_Empty(t *testing.T) {
	db := openTestDB(t)
	m, err := sqladapter.NewMigrator(db, testMigrationsFS, "testdata/migrations", testDialect)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}

	statuses, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("expected at least one migration status entry")
	}
	for _, s := range statuses {
		if !s.AppliedAt.IsZero() {
			t.Errorf("migration %q should be pending (AppliedAt zero), got %v", s.Name, s.AppliedAt)
		}
	}
}

// ── M4: invalid dialect → MigrationError ─────────────────────────────────────

func TestMigrator_MigrationError(t *testing.T) {
	db := openTestDB(t)
	_, err := sqladapter.NewMigrator(db, testMigrationsFS, "testdata/migrations", "not-a-dialect")
	if err == nil {
		t.Fatal("expected error for invalid dialect, got nil")
	}
	var me sqladapter.MigrationError
	if !errors.As(err, &me) {
		t.Fatalf("expected MigrationError, got %T: %v", err, err)
	}
	if me.Op != "init" {
		t.Errorf("Op: want %q, got %q", "init", me.Op)
	}
}

// ── M5: Observer.RecordMigration called per applied migration ─────────────────

type migrationSpy struct {
	stats.NoopObserver
	migrations []migrationRecord
}

type migrationRecord struct {
	op, name string
	version  int64
	dur      time.Duration
	err      error
}

func (s *migrationSpy) RecordMigration(op, name string, version int64, dur time.Duration, err error) {
	s.migrations = append(s.migrations, migrationRecord{op, name, version, dur, err})
}

func TestMigrator_Observer(t *testing.T) {
	db := openTestDB(t)
	m, err := sqladapter.NewMigrator(db, testMigrationsFS, "testdata/migrations", testDialect)
	if err != nil {
		t.Fatalf("NewMigrator: %v", err)
	}

	spy := &migrationSpy{}
	if err := m.Up(context.Background(), sqladapter.MigrateOptions{Observer: spy}); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Two migration files → two RecordMigration calls.
	if len(spy.migrations) != 2 {
		t.Fatalf("RecordMigration: want 2 calls, got %d", len(spy.migrations))
	}
	for _, r := range spy.migrations {
		if r.op != "up" {
			t.Errorf("RecordMigration: op want %q, got %q", "up", r.op)
		}
		if r.version == 0 {
			t.Error("RecordMigration: version should be non-zero")
		}
	}
}

// ── MigrationError LogValue ───────────────────────────────────────────────────

func TestMigrationError_LogValue(t *testing.T) {
	me := sqladapter.MigrationError{Op: "up", Version: 3, Err: fmt.Errorf("db error")}
	lv := me.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("LogValue: want KindGroup, got %v", lv.Kind())
	}
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	for _, want := range []string{"op", "version", "err"} {
		if !keys[want] {
			t.Errorf("LogValue missing attribute %q", want)
		}
	}
}

// ── MigrationError Unwrap ─────────────────────────────────────────────────────

func TestMigrationError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("original goose error")
	me := sqladapter.MigrationError{Op: "up", Version: 1, Err: inner}

	if !errors.Is(me, inner) {
		t.Error("errors.Is should reach the inner error via Unwrap")
	}
	if errors.Unwrap(me) != inner {
		t.Errorf("Unwrap: want inner error, got %v", errors.Unwrap(me))
	}
}

// ── Example ───────────────────────────────────────────────────────────────────

func ExampleNewMigrator() {
	db, err := sql.Open("sqlite", "file::memory:?cache=shared&_example=1")
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer db.Close()

	migrator, err := sqladapter.NewMigrator(db, testMigrationsFS, "testdata/migrations", "sqlite3")
	if err != nil {
		fmt.Println("migrator:", err)
		return
	}

	if err := migrator.Up(context.Background(), sqladapter.MigrateOptions{}); err != nil {
		fmt.Println("up:", err)
		return
	}
	fmt.Println("migrations applied")
	// Output:
	// migrations applied
}
