# SQL Examples — goose + sqlc + codec refinements

> See also: [`adapters/sql` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/sql) · [Feature: SQL Adapter](../features/sql.md) · [Observer Examples](observer.md) · [Error Handling Examples](error-handling.md)

`adapters/sql` brings go-codex's codec-based validation to SQL databases by
wiring three single-purpose tools together, each doing exactly what it's
designed for:

| Tool | Job | Does NOT do |
|---|---|---|
| [`pressly/goose`](https://github.com/pressly/goose) | Schema migrations — versioned `.sql` files, migration history table | Struct generation, query execution, validation |
| [`sqlc`](https://sqlc.dev) | Generate typed Go structs and query methods from the schema + SQL query files | Business-rule validation, cross-field rules |
| **go-codex** (`Codec[T]` + `Refine`) | Refinement validation on top of the generated struct — rules SQL can't express, testable in pure Go | Schema DDL, SQL generation, row scanning |

The result: **goose** moves the schema forward, **sqlc** catches structural
mistakes (typos, wrong column types) at compile time, and **go-codex**
enforces business rules the application actually cares about — all without any
of the three tools duplicating the others' work.

---

## Prerequisites

```bash
go get github.com/pressly/goose/v3
go get github.com/sqlc-dev/sqlc  # developer-time code generator — not a runtime dep
go get github.com/DaniDeer/go-codex/adapters/sql
```

For local development with SQLite (no external DB needed):

```bash
go get modernc.org/sqlite  # pure-Go SQLite driver, no CGO
```

---

## Step 1 — Write goose migrations

Create a `migrations/` directory and embed it into your binary:

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS
```

Example migration file `migrations/00001_create_users.sql`:

```sql
-- +goose Up
CREATE TABLE users (
    id    TEXT PRIMARY KEY,
    name  TEXT NOT NULL,
    email TEXT NOT NULL,
    role  TEXT NOT NULL DEFAULT 'member'
);

-- +goose Down
DROP TABLE users;
```

Apply migrations at startup with go-codex's `Migrator` wrapper:

```go
import (
    sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
)

migrator, err := sqladapter.NewMigrator(db, migrationsFS, "migrations", "sqlite3")
if err != nil {
    log.Fatal(err)
}
if err := migrator.Up(ctx, sqladapter.MigrateOptions{Observer: obs}); err != nil {
    var me sqladapter.MigrationError
    if errors.As(err, &me) {
        slog.Error("migration failed", "error", me) // me implements slog.LogValuer
    }
    log.Fatal(err)
}
```

`Migrator` wraps goose's `RunContext` and emits one
`stats.SQLObserver.RecordMigration` call per applied migration so your metrics
and traces capture exactly when the schema changed.

---

## Step 2 — Generate structs and queries with sqlc

Create `sqlc.yaml` pointing at the migration directory:

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "query/"
    schema: "migrations/"
    gen:
      go:
        package: "db"
        out: "db"
```

Write a query file `query/users.sql`:

```sql
-- name: GetUser :one
SELECT id, name, email, role FROM users WHERE id = $1;

-- name: InsertUser :exec
INSERT INTO users (id, name, email, role) VALUES ($1, $2, $3, $4);

-- name: ListUsers :many
SELECT id, name, email, role FROM users;
```

Run the generator (developer-time step, not a runtime dependency):

```bash
sqlc generate
```

sqlc produces `db/models.go` with:

```go
// db/models.go — auto-generated, do not edit
type User struct {
    ID    string
    Name  string
    Email string
    Role  string
}

type InsertUserParams struct {
    ID    string
    Name  string
    Email string
    Role  string
}
```

And `db/query.sql.go` with compile-time-checked query methods:

```go
func (q *Queries) GetUser(ctx context.Context, id string) (User, error)
func (q *Queries) InsertUser(ctx context.Context, arg InsertUserParams) error
func (q *Queries) ListUsers(ctx context.Context) ([]User, error)
```

At this point, any column typo or type mismatch in a query file fails `sqlc
generate` — the structural correctness of every SQL query is verified before
the code compiles.

---

## Step 3 — Declare a `Codec[T]` against the generated struct

The generated struct is an ordinary Go struct. Declare a codec against it
using the same `codex.Struct` + `RequiredField` + `Refine` pattern used for
HTTP bodies, MQTT payloads, and file formats — nothing SQL-specific:

```go
import (
    "github.com/DaniDeer/go-codex/codex"
    "github.com/DaniDeer/go-codex/validate"
    "your/module/db"
)

// userCodec validates a db.User row — business rules SQL can't express.
var userCodec = codex.Struct(
    codex.RequiredField("id",
        codex.String().Refine(validate.UUID),
        func(u db.User) string { return u.ID },
        func(u *db.User, v string) { u.ID = v }),

    codex.RequiredField("name",
        codex.String().Refine(validate.MinLen(1), validate.MaxLen(80)),
        func(u db.User) string { return u.Name },
        func(u *db.User, v string) { u.Name = v }),

    codex.RequiredField("email",
        codex.String().Refine(validate.Email),
        func(u db.User) string { return u.Email },
        func(u *db.User, v string) { u.Email = v }),

    codex.RequiredField("role",
        codex.String().Refine(validate.OneOf("member", "admin", "viewer")),
        func(u db.User) string { return u.Role },
        func(u *db.User, v string) { u.Role = v }),
).RefineFunc(func(u db.User) error {
    // Cross-field rule: SQL CHECK across columns is clunky and DB-engine-specific.
    // Here it's plain Go, testable in isolation.
    if u.Role == "admin" && !strings.HasSuffix(u.Email, "@company.example") {
        return fmt.Errorf("admin accounts require a company.example email")
    }
    return nil
})

// insertUserParamsCodec validates the INSERT shape — separate codec because
// sqlc generates a distinct InsertUserParams type for the INSERT query.
// This is the standard go-codex pattern of separate request/response codecs.
var insertUserParamsCodec = codex.Struct(
    codex.RequiredField("id",
        codex.String().Refine(validate.UUID),
        func(p db.InsertUserParams) string { return p.ID },
        func(p *db.InsertUserParams, v string) { p.ID = v }),
    // ... name, email, role — same Refine rules as userCodec
)
```

> **Tip:** Use field factory functions (documented in
> [`codec.md`](../concepts/codec.md)) to avoid repeating `Refine` rules across
> `userCodec` and `insertUserParamsCodec`:
>
> ```go
> func emailField[T any](get func(T) string, set func(*T, string)) codex.Field[T, string] {
>     return codex.RequiredField("email", codex.String().Refine(validate.Email), get, set)
> }
> ```

---

## Step 4 — Call `Validate` around sqlc operations

`Validate[T]` runs the codec's encode→decode round trip against a value,
applying every `Refine`/`RefineFunc` constraint. It returns the
**normalized** value — if a `Refine` step normalizes data (e.g. trims
whitespace), the returned struct reflects that. This matches the behaviour of
`format.JSON.Read`.

### Pre-query validation (primary path)

Reject invalid data before it ever reaches the database:

```go
queries := db.New(conn)

params := db.InsertUserParams{
    ID:    uuid.NewString(),
    Name:  name,
    Email: email,
    Role:  role,
}
validated, err := sqladapter.Validate(insertUserParamsCodec, params,
    sqladapter.ValidateOptions{Table: "users", Op: "insert_user", Observer: obs})
if err != nil {
    // codec rejected the data — do not call queries.InsertUser
    return fmt.Errorf("invalid user input: %w", err)
}
if err := queries.InsertUser(ctx, validated); err != nil {
    return err // driver error (connection, constraint) — not a codec concern
}
```

### Post-query validation (defence in depth)

Validate a row returned by sqlc when the database may contain data written by
other clients that bypassed the codec:

```go
u, err := queries.GetUser(ctx, id)
if err != nil {
    return nil, err // sql.ErrNoRows or driver error — not wrapped by go-codex
}
u, err = sqladapter.Validate(userCodec, u,
    sqladapter.ValidateOptions{Table: "users", Op: "get_user", Observer: obs})
if err != nil {
    // The stored row fails a business-rule Refine (e.g. stale data from a
    // migration that changed valid role values).
    slog.Warn("stored row failed business-rule validation", "error", err)
    return nil, err
}
return &u, nil
```

### Handling validation errors

`Validate` wraps codec failures in `RowValidationError`, which implements
`slog.LogValuer` for structured logging and `Unwrap()` for `errors.As`:

```go
if err != nil {
    // RowValidationError gives table/op context for logs and metrics.
    var rve sqladapter.RowValidationError
    if errors.As(err, &rve) {
        slog.Error("validation failed", "error", rve) // full {table, op, err} group

        // Inspect individual field failures.
        var ve *codex.ValidationErrors
        if errors.As(err, &ve) {
            for _, ke := range ve.Errors {
                slog.Debug("field error", "field", ke.Key, "err", ke.Err)
            }
        }
    }
}
```

---

## Sharing the `Codec[T]` across SQL and REST/MQTT

The same `userCodec` that validates SQL rows can be registered as the
response codec for a REST route — one source of truth for what a valid `User`
looks like, regardless of transport:

```go
// REST: the same userCodec validates the HTTP response body.
userRoute := rest.NewRoute[GetUserRequest, db.User](
    rest.WithResponseCodec(userCodec),
    // ...
)

// SQL: the same userCodec validates retrieved DB rows.
u, err = sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
    Table: "users", Op: "get_user",
})
```

If a business rule changes (e.g. the maximum name length increases from 80 to
120 characters), updating `userCodec` in one place propagates the change to
both the REST API validation and the SQL row validation automatically.

---

## Observer integration

Pass any `stats.Observer` implementation to `ValidateOptions.Observer` and
`MigrateOptions.Observer`. If the implementation also satisfies
`stats.SQLObserver`, the adapter calls the additional SQL-specific hooks:

```go
type stats.SQLObserver interface {
    RecordValidation(table, op string, dur time.Duration, err error)
    RecordMigration(op, name string, version int64, dur time.Duration, err error)
}
```

Per-field validation failures are additionally reported via the existing
`stats.Observer.RecordValidationError("sql_row", constraint, field)` hook —
consistent with how `format.File` uses location `"file"` and MQTT adapters
use `"payload"`.

```go
type myObs struct{}

func (o *myObs) RecordValidationError(location, constraint, field string) {
    metrics.ValidationErrors.With(
        "location", location,
        "constraint", constraint,
        "field", field,
    ).Inc()
}

func (o *myObs) RecordValidation(table, op string, dur time.Duration, err error) {
    status := "ok"
    if err != nil { status = "error" }
    metrics.SQLValidations.With("table", table, "op", op, "status", status).
        Observe(dur.Seconds())
}

func (o *myObs) RecordMigration(op, name string, version int64, dur time.Duration, err error) {
    slog.Info("migration applied",
        "op", op, "name", name, "version", version, "duration", dur, "err", err)
}
```

---

## Error reference

| Type | When returned | Key fields |
|------|--------------|-----------|
| `sqladapter.RowValidationError` | `Validate` — codec Refine/decode failure | `Table`, `Op`, `Err` (inner `*codex.ValidationErrors`) |
| `sqladapter.MigrationError` | `Migrator.Up`/`Down`/`Status` — goose failure | `Op`, `Version`, `Err` |
| `sql.ErrNoRows` | `sqlc`-generated `GetUser` returns no row | _(sqlc's own error — go-codex does not wrap it)_ |
| driver errors | `sqlc`-generated query methods on DB errors | _(driver's own error — go-codex does not wrap it)_ |

Both go-codex error types implement `slog.LogValuer` for zero-effort
structured logging and `Unwrap()` for `errors.As` traversal.
