package sql

import (
	"fmt"
	"log/slog"
)

// RowValidationError is returned by [Validate] when the codec's Refine or
// RefineFunc constraints reject the value. It wraps the underlying codec error
// (typically [codex.ValidationErrors]) so callers can use errors.As to inspect
// individual field failures.
//
// RowValidationError implements [slog.LogValuer] for zero-effort structured
// logging:
//
//	slog.Error("row invalid", "error", rve)
//	// → {table:"users", op:"insert_user", err:{email:"invalid email format"}}
type RowValidationError struct {
	// Table names the sqlc-generated table, matching ValidateOptions.Table.
	Table string

	// Op names the sqlc operation, matching ValidateOptions.Op.
	Op string

	// Err is the underlying codec error. Use errors.As to reach
	// *codex.ValidationErrors for per-field detail.
	Err error
}

func (e RowValidationError) Error() string {
	return fmt.Sprintf("sql: validate %s (%s): %v", e.Table, e.Op, e.Err)
}

// Unwrap allows errors.Is and errors.As to traverse the wrapped codec error.
func (e RowValidationError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e RowValidationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("table", e.Table),
		slog.String("op", e.Op),
		slog.Any("err", e.Err),
	)
}

// MigrationError is returned by [Migrator.Up], [Migrator.Down], and
// [Migrator.Status] when goose fails. It wraps the original goose error and
// carries the operation name and version (when applicable) for structured
// logging and error handling.
//
// MigrationError implements [slog.LogValuer]:
//
//	slog.Error("migration failed", "error", me)
//	// → {op:"up", version:3, err:"..."}
type MigrationError struct {
	// Op is the migration operation: "up", "down", or "status".
	Op string

	// Version is the migration version number for Up/Down, or 0 for Status.
	Version int64

	// Err is the underlying goose error.
	Err error
}

func (e MigrationError) Error() string {
	if e.Version != 0 {
		return fmt.Sprintf("sql: migration %s (version %d): %v", e.Op, e.Version, e.Err)
	}
	return fmt.Sprintf("sql: migration %s: %v", e.Op, e.Err)
}

// Unwrap allows errors.Is and errors.As to traverse the wrapped goose error.
func (e MigrationError) Unwrap() error { return e.Err }

// LogValue implements [slog.LogValuer] for structured logging.
func (e MigrationError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("op", e.Op),
		slog.Int64("version", e.Version),
		slog.Any("err", e.Err),
	)
}
