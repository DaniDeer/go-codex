// Package sql brings go-codex's codec-based validation to SQL databases by
// combining three single-purpose tools — each doing exactly what it is
// designed for:
//
//   - [github.com/pressly/goose/v3] manages schema migrations (versioned .sql
//     files, migration history table). Wrapped by [Migrator].
//   - [sqlc](https://sqlc.dev) generates typed Go structs and query methods
//     from the migrated schema and hand-written SQL query files. This is a
//     developer-time step; no runtime dependency.
//   - go-codex Codec[T] + Refine applies business-rule validation on top of
//     the generated structs — rules SQL itself cannot express, centralized and
//     testable in pure Go. Executed by [Validate].
//
// # Toolchain split
//
// goose owns schema shape (column existence, types, indexes). sqlc catches
// structural query mistakes at compile time. go-codex validates business rules
// the application cares about. None of the three tools duplicates the others'
// work.
//
// # Validate
//
// [Validate] runs a struct through its [codex.Codec]'s encode→decode round
// trip, applying every Refine and RefineFunc constraint. Use it in two modes:
//
//   - Pre-query validation: reject invalid data before it is written to the
//     database. Invalid structs never reach queries.InsertUser (or equivalent).
//   - Post-query validation: validate a row returned by a sqlc query method as
//     defence in depth against data written by other clients that bypassed the
//     codec.
//
// The returned T is the normalized value — the same round-trip semantics used
// by [format.JSON.Read] throughout the library.
//
//	params := db.InsertUserParams{ID: uuid.NewString(), Name: name, Email: email}
//	validated, err := sqladapter.Validate(insertParamsCodec, params,
//	    sqladapter.ValidateOptions{Table: "users", Op: "insert_user", Observer: obs})
//	if err != nil {
//	    return fmt.Errorf("invalid input: %w", err) // never reaches the DB
//	}
//	err = queries.InsertUser(ctx, validated)
//
// # Migrator
//
// [Migrator] wraps goose's migration runner and emits structured observer
// events per applied or rolled-back migration file. Construct it with
// [NewMigrator], then call [Migrator.Up] at startup:
//
//	//go:embed migrations/*.sql
//	var migrationsFS embed.FS
//
//	migrator, err := sqladapter.NewMigrator(db, migrationsFS, "migrations", "sqlite3")
//	if err != nil { log.Fatal(err) }
//	if err := migrator.Up(ctx, sqladapter.MigrateOptions{Observer: obs}); err != nil {
//	    log.Fatal(err)
//	}
//
// # Structured errors
//
// All error types implement [slog.LogValuer] for zero-effort structured logging
// and [Unwrap] for errors.As traversal:
//
//   - [RowValidationError] — codec Refine failure from [Validate]; wraps the
//     underlying [codex.ValidationErrors] for per-field inspection.
//   - [MigrationError] — goose failure from [Migrator.Up], [Migrator.Down], or
//     [Migrator.Status]; carries Op and Version fields.
//
// # Observer integration
//
// Pass any [stats.Observer] to [ValidateOptions.Observer] and
// [MigrateOptions.Observer]. If the implementation also satisfies
// [stats.SQLObserver], the adapter calls:
//
//   - [stats.SQLObserver.RecordValidation] after every [Validate] call.
//   - [stats.SQLObserver.RecordMigration] once per applied or rolled-back file.
//
// Per-field validation failures are always reported via
// [stats.Observer.RecordValidationError] with location "sql_row", regardless
// of whether [stats.SQLObserver] is implemented.
package sql
