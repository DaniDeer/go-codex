# SQL Adapter — `adapters/sql`

> See also: [`adapters/sql` on pkg.go.dev](https://pkg.go.dev/github.com/DaniDeer/go-codex/adapters/sql) · [SQL Examples guide](../guides/sql.md) · [Observer Pattern](observer.md) · [Error Handling](error-handling.md)
>
> Runnable demo: [`examples/sensor-service`](https://github.com/DaniDeer/go-codex/tree/main/examples/sensor-service) — sensor readings service combining `adapters/nethttp`, `adapters/mqtt`, and `adapters/sql` with a shared `stats.NewFanout` observer; shows sqlc-generated structs, goose migrations, `Validate[T]` pre-insert and post-read, and field factory functions reusing `Refine` rules across `db.Reading` and `db.InsertReadingParams`.

`adapters/sql` brings go-codex's codec-based validation to SQL databases by
pairing three single-purpose tools, each doing exactly the job it is designed
for:

| Tool | Owns | Does NOT own |
|---|---|---|
| [`pressly/goose`](https://github.com/pressly/goose) | Schema migrations — versioned `.sql` files, `goose_db_version` table | Struct generation, query execution, validation |
| [`sqlc`](https://sqlc.dev) | Typed struct + query method generation from the schema and SQL query files | Business-rule validation, cross-field rules |
| **go-codex** (`Codec[T]` + `Refine`) | Refinement validation on top of sqlc's generated structs — rules SQL cannot express, testable in pure Go | Schema DDL, SQL generation/execution, row scanning |

The adapter adds **no query builder, no ORM, no row scanner**. Its sole job is
the `T → validate → T` boundary: ensuring a struct passes all its codec
constraints before it reaches the database or after it comes back from one.

---

## Validate

`Validate[T]` is the core function. It runs a value through its codec's
encode→decode round trip, applying every `Refine` and `RefineFunc` constraint.

```go
func Validate[T any](c codex.Codec[T], v T, opts ValidateOptions) (T, error)
```

The returned `T` is the **normalized value** — the result of `Decode` after
`Encode`. This may differ from `v` when a `Refine` step normalizes data (e.g.
trimming whitespace). This is the same round-trip semantics used by
`format.JSON.Read` throughout go-codex.

### Two usage modes

**Pre-query validation** — reject invalid data before it reaches the database:

```go
validated, err := sqladapter.Validate(insertParamsCodec, params,
    sqladapter.ValidateOptions{Table: "users", Op: "insert_user", Observer: obs})
if err != nil {
    return fmt.Errorf("invalid input: %w", err) // never calls queries.InsertUser
}
_ = queries.InsertUser(ctx, validated)
```

**Post-query validation** — defence in depth against rows written by other
clients that bypassed the codec:

```go
u, err := queries.GetUser(ctx, id)
if err != nil { return nil, err } // sql.ErrNoRows etc — not go-codex's concern

u, err = sqladapter.Validate(userCodec, u,
    sqladapter.ValidateOptions{Table: "users", Op: "get_user", Observer: obs})
```

### `ValidateOptions`

```go
type ValidateOptions struct {
    Table    string         // sqlc table name — for error context and observer
    Op       string         // sqlc query name — e.g. "get_user", "insert_user"
    Observer stats.Observer // nil → stats.NoopObserver
}
```

---

## Migrator

`Migrator` wraps `pressly/goose/v3` with go-codex structured errors and
observer hooks. It never inspects row data — schema evolution and row
validation are separate concerns by design.

```go
// NewMigrator validates the dialect and migrations directory at construction time.
func NewMigrator(db *sql.DB, migrations fs.FS, dir string, dialect string) (*Migrator, error)

func (m *Migrator) Up(ctx context.Context, opts MigrateOptions) error
func (m *Migrator) Down(ctx context.Context, opts MigrateOptions) error
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error)
```

**Supported dialect strings:** `"postgres"`, `"mysql"`, `"sqlite3"`, `"mssql"`,
`"redshift"`, `"tidb"`, `"clickhouse"`, `"vertica"`, `"ydb"`, `"spanner"`,
`"turso"`.

### Embedding migrations

```go
//go:embed migrations/*.sql
var migrationsFS embed.FS

migrator, err := sqladapter.NewMigrator(db, migrationsFS, "migrations", "postgres")
if err != nil { log.Fatal(err) }
if err := migrator.Up(ctx, sqladapter.MigrateOptions{Observer: obs}); err != nil {
    log.Fatal(err)
}
```

### `MigrationStatus`

```go
type MigrationStatus struct {
    Version   int64      // numeric prefix of the migration file
    Name      string     // file path
    AppliedAt time.Time  // zero value = pending
}
```

### `MigrateOptions`

```go
type MigrateOptions struct {
    Observer stats.Observer // nil → stats.NoopObserver
}
```

---

## Error types

All error types implement `error`, `Unwrap()`, and `slog.LogValuer`.

| Type | Returned by | Key fields |
|---|---|---|
| `RowValidationError` | `Validate` — codec Refine/decode failure | `Table string`, `Op string`, `Err error` |
| `MigrationError` | `NewMigrator`, `Up`, `Down`, `Status` — goose failure | `Op string` (`"init"/"up"/"down"/"status"`), `Version int64`, `Err error` |

`RowValidationError.Err` is typically `codex.ValidationErrors` — use
`errors.As` to reach per-field detail:

```go
var rve sqladapter.RowValidationError
if errors.As(err, &rve) {
    slog.Error("row invalid", "error", rve) // structured: {table, op, err}

    var ve codex.ValidationErrors
    if errors.As(err, &ve) {
        for _, fe := range ve {
            fmt.Printf("field %q: %v\n", fe.Field, fe.Err)
        }
    }
}
```

`sql.ErrNoRows` and driver errors from sqlc query methods are **not wrapped**
by go-codex — they pass through unchanged so `errors.Is(err, sql.ErrNoRows)`
continues to work.

---

## Observer integration — `stats.SQLObserver`

`stats.SQLObserver` is an optional extension to `stats.Observer`. The adapter
type-asserts the configured observer to `SQLObserver` before calling SQL-specific
hooks — existing observer implementations need not change.

```go
type SQLObserver interface {
    // Called after every Validate call — err is nil on success.
    RecordValidation(table, op string, dur time.Duration, err error)

    // Called once per applied or rolled-back migration file during Up/Down.
    RecordMigration(op, name string, version int64, dur time.Duration, err error)
}
```

Per-field validation failures are **always** reported via
`stats.Observer.RecordValidationError("sql_row", constraint, field)`, regardless
of whether `SQLObserver` is implemented — consistent with `"file"` (file
formats), `"payload"` (MQTT), and `"body"` (HTTP).

`stats.NoopObserver`, `stats.LoggingObserver`, and `stats.NewFanout` all
implement `SQLObserver`.

### Implementing `SQLObserver`

```go
type myMetrics struct {
    // embed NoopObserver so the base Observer interface is satisfied
    stats.NoopObserver
}

func (m *myMetrics) RecordValidation(table, op string, dur time.Duration, err error) {
    status := "ok"
    if err != nil { status = "error" }
    sqlValidations.With("table", table, "op", op, "status", status).
        Observe(dur.Seconds())
}

func (m *myMetrics) RecordMigration(op, name string, version int64, dur time.Duration, err error) {
    slog.Info("migration", "op", op, "name", name, "version", version,
        "ms", dur.Milliseconds(), "err", err)
}
```

---

## Sharing a `Codec[T]` across SQL and REST/MQTT

A codec declared against a sqlc-generated struct is an ordinary `Codec[T]` and
can be reused as the response codec for a REST route or MQTT channel — one
source of truth for what a valid value looks like, regardless of transport:

```go
// REST response uses the same codec:
userRoute := rest.NewRoute[GetUserReq, db.User](
    rest.WithResponseCodec(userCodec), ...)

// SQL validation uses the same codec:
u, err = sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{...})
```

Updating a `Refine` rule in `userCodec` propagates automatically to both
the HTTP response validation and the SQL row validation.

---

## What `adapters/sql` does NOT do

- **No query builder** — callers write SQL text directly in sqlc query files.
- **No row scanning** — sqlc's generated methods already handle this per-driver.
- **No ORM** — `Validate` is the only boundary between go-codex and the DB row.
- **No spec generation** — SQL tables are not API surfaces; the same `Codec[T]`
  contributes its schema when registered with a REST route or MQTT channel.
- **No reflection** — codecs are declared by hand with explicit get/set
  closures, consistent with the no-reflection design principle throughout go-codex.
