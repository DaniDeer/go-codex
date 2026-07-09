# SQL Adapter — goose migrations + sqlc structs + codec refinements — `adapters/sql`

## Motivation

go-codex adapters wrap external transport/storage libraries (`mqtt5`,
`zeromq`, `templ`, `mcpgo`) with a consistent declarative workflow: declare a
`Codec[T]`, register it against a handle, let the adapter do encode/decode +
observer lifecycle. SQL databases fit this pattern too, but only if each tool
in the SQL toolchain is used for the one job it's actually good at. This plan
deliberately pairs **three separate, single-purpose tools** instead of
inventing a go-codex ORM:

| Tool | Owns | Does NOT own |
|---|---|---|
| [`pressly/goose`](https://github.com/pressly/goose) | Schema evolution — versioned `.sql`/`.go` migration files, `goose_db_version` tracking | Struct generation, query execution, validation |
| [`sqlc`](https://sqlc.dev) | Struct generation + typed, compile-time-checked query methods, generated straight from the migrated schema + hand-written SQL query files | Business-rule validation, cross-field rules, format constraints |
| **go-codex** (`Codec[T]` + `Refine`) | Refinement validation on top of sqlc's generated structs — the rules SQL itself cannot express or that the team wants centralized and testable in Go | Schema DDL, SQL generation/execution |

This is a correction of an earlier draft of this plan, which proposed a
`Table[T]`/query-builder layer duplicating what `sqlc` already does well.
**sqlc already solves "generate a validated-at-compile-time struct + scan
loop from a `.sql` query file."** Re-implementing that inside go-codex would
be redundant scope creep and would fight sqlc's own compile-time query
checking. go-codex's real, additive value is exactly the same value it
already provides for HTTP/MQTT/file payloads: **`Refine`-based validation
that SQL/sqlc cannot express**, applied consistently through the same
`Codec[T]` model used everywhere else in the library.

---

## Toolchain roles, precisely

### 1. goose — schema migrations (unchanged from earlier draft)

`pressly/goose/v3` is a migration runner only — it does not generate Go
structs. It manages `.sql`/`.go` migration files, tracks applied versions,
and exposes `Up`/`Down`/`Status` as both CLI and Go library
(`goose.RunContext(ctx, "up", db, migrationsDir)`). go-codex wraps it with
structured errors and observer hooks, nothing more.

### 2. sqlc — generated structs + queries (the piece the earlier draft got wrong)

Workflow:

```
migrations/*.sql   (owned by goose)
        │
        ▼
sqlc.yaml + query/*.sql   (hand-written SELECT/INSERT/UPDATE/DELETE queries
                            with sqlc annotations, e.g. "-- name: GetUser :one")
        │
        ▼  `sqlc generate`
db/models.go   → type User struct { ID string; Name string; Email string; ... }
db/query.sql.go → func (q *Queries) GetUser(ctx, id string) (User, error)
                  func (q *Queries) InsertUser(ctx, InsertUserParams) (User, error)
```

sqlc already gives:
- Compile-time verification that every query matches the schema (column
  existence, type compatibility) — this **is** the "don't rely on SQL
  validation so much" win for structural correctness; sqlc catches typos and
  type mismatches at `sqlc generate` / build time, not at runtime.
- Generated Go structs with normal Go types (`string`, `int64`, `time.Time`,
  `sql.NullString`, etc.) — no `map[string]any` scanning step needed, no
  `[]byte`→`string` driver-shim code needed (sqlc's runtime, `pgx`/`database/sql`
  scanning, already handles this per-driver).
- Generated query methods callers invoke directly — no query builder needed
  from go-codex.

**go-codex does not touch this generation step.** `db/models.go` and
`db/query.sql.go` are sqlc output, checked in or generated in CI like any
other codegen artifact (parallel to how `templ generate` produces `_templ.go`
files that `adapters/templ` then wraps).

### 3. go-codex — refinement on top of the generated struct

The generated struct (e.g. `db.User`) is a completely ordinary Go struct.
Nothing prevents writing a normal go-codex `Codec[db.User]` against it, using
the exact same `codex.Struct` + `codex.RequiredField`/`OptionalField` +
`.Refine` machinery used everywhere else in the library — **no new `codex`
core API is required**:

```go
var userCodec = codex.Struct(
    codex.RequiredField("id", func(u db.User) string { return u.ID },
        func(u *db.User, v string) { u.ID = v },
        codex.String().Refine(validate.UUID)),

    codex.RequiredField("name", func(u db.User) string { return u.Name },
        func(u *db.User, v string) { u.Name = v },
        codex.String().Refine(validate.MinLen(1), validate.MaxLen(80))),

    codex.RequiredField("email", func(u db.User) string { return u.Email },
        func(u *db.User, v string) { u.Email = v },
        codex.String().Refine(validate.Email)),
).RefineFunc(func(u db.User) error {
    // Cross-field rule sqlc/SQL can't express cleanly:
    // e.g. "admin accounts must use a company email domain"
    if u.Role == "admin" && !strings.HasSuffix(u.Email, "@company.example") {
        return fmt.Errorf("admin accounts require a company.example email")
    }
    return nil
})
```

This is exactly the "shared field codecs" and "field factory functions"
pattern already documented in `docs/concepts/codec.md` — nothing SQL-specific
about it. The adapter's job is to make **applying** this codec to sqlc calls
convenient and consistent (structured errors, observer hooks), not to
reinvent field declaration.

---

## Adapter surface — validation wrapper, not a query layer

Since sqlc already produces the struct and the query method, `adapters/sql`
needs only a **thin, generic validation wrapper** around sqlc calls — no
`Table[T]`, no `DB` interface, no hand-rolled row scanning, no query builder.

```go
// Validate runs v through c's own encode→decode round trip, applying every
// Refine/RefineFunc constraint declared on c. Use it to validate a struct
// returned by a sqlc query method (post-query, e.g. defense-in-depth against
// data written by other, non-go-codex clients) or a struct about to be
// passed into a sqlc insert/update method (pre-query, so invalid data never
// reaches the database).
//
// The round trip (c.Encode then c.Decode) is the same technique already
// usable directly with any Codec[T] — Validate exists purely to attach
// SQL-adapter structured errors and observer hooks around it consistently.
func Validate[T any](c codex.Codec[T], v T, opts ValidateOptions) (T, error)

// ValidateOptions configures observer/error context for a single Validate call.
type ValidateOptions struct {
    // Table names the sqlc-generated table/struct for error and observer
    // context (e.g. "users"). Purely descriptive — Validate does not touch
    // the database itself.
    Table string

    // Op names the sqlc operation being wrapped, e.g. "get_user",
    // "insert_user" — matches sqlc's query name for easy correlation
    // between generated code and validation logs/metrics.
    Op string

    // Observer, when non-nil and also implementing [stats.SQLObserver],
    // receives [stats.SQLObserver.RecordValidation] for every call.
    // Defaults to [stats.NoopObserver] when nil.
    Observer stats.Observer
}
```

### Usage sketch — wrapping sqlc calls

```go
queries := db.New(conn) // sqlc-generated *Queries

// Post-query validation (defense in depth; data may have been written by
// another service or a raw SQL script that bypassed the codec).
u, err := queries.GetUser(ctx, "u-123")
if err != nil {
    return err // sql.ErrNoRows or a driver error — sqlc/database/sql's own error
}
u, err = sqladapter.Validate(userCodec, u, sqladapter.ValidateOptions{
    Table: "users", Op: "get_user", Observer: obs,
})
if err != nil {
    var ve *codex.ValidationErrors
    if errors.As(err, &ve) {
        logger.Warn("stored user row failed refinement", "errors", ve)
    }
}

// Pre-query validation (primary path — reject invalid data before it is
// ever written).
newUser := db.InsertUserParams{ID: uuid.NewString(), Name: name, Email: email}
if _, err := sqladapter.Validate(insertParamsCodec, newUser, sqladapter.ValidateOptions{
    Table: "users", Op: "insert_user", Observer: obs,
}); err != nil {
    return fmt.Errorf("invalid user: %w", err) // never reaches queries.InsertUser
}
result, err := queries.InsertUser(ctx, newUser)
```

This keeps the three tools' responsibilities crisp: **goose** moved the
schema forward, **sqlc** generated the compile-time-checked query and
struct, **go-codex** enforced the business rules sqlc/SQL can't express — all
without go-codex ever touching a `*sql.Rows` or building SQL text itself.

---

## Migrations — unchanged from earlier draft

```go
// Migrator wraps goose's migration runner with go-codex structured errors
// and observer integration.
type Migrator struct {
    db      *sql.DB
    fs      fs.FS  // embedded migrations directory, e.g. via go:embed
    dir     string
    dialect string // one of goose's supported dialect strings
}

func NewMigrator(db *sql.DB, migrations fs.FS, dir string, dialect string) (*Migrator, error)

func (m *Migrator) Up(ctx context.Context, opts MigrateOptions) error
func (m *Migrator) Down(ctx context.Context, opts MigrateOptions) error
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error)

type MigrationStatus struct {
    Version   int64
    Name      string
    AppliedAt time.Time // zero value = pending
}

type MigrateOptions struct {
    // Observer, when non-nil and implementing [stats.SQLObserver], receives
    // [stats.SQLObserver.RecordMigration] per applied/rolled-back file.
    Observer stats.Observer
}
```

`Migrator` never sees a `Codec[T]` — migrations operate on DDL text, not
rows. This separation (schema evolution vs. row validation) stays exactly as
in the earlier draft; only the row-access layer changes.

---

## Structured errors (all implement `slog.LogValuer`)

```go
// RowValidationError wraps a codec Refine/decode failure surfaced by
// [Validate], preserving the original error (typically
// *codex.ValidationErrors) for errors.As inspection, plus SQL context.
type RowValidationError struct {
    Table string
    Op    string
    Err   error
}
func (e RowValidationError) Error() string { return fmt.Sprintf("sql: validate %s (%s): %v", e.Table, e.Op, e.Err) }
func (e RowValidationError) Unwrap() error { return e.Err }
func (e RowValidationError) LogValue() slog.Value // → {table, op, err}

// MigrationError wraps a goose Up/Down/Status failure.
type MigrationError struct {
    Op      string // "up" | "down" | "status"
    Version int64  // 0 when not applicable (e.g. Status)
    Err     error
}
func (e MigrationError) LogValue() slog.Value // → {op, version, err}
```

Both mirror `format`'s `EmbeddedDecodeError`/`EmbeddedEncodeError` shape
(`{context fields, Err}` + `LogValue`) — the same "codec failure at a named
boundary" convention used throughout the library. Notably **no** `QueryError`
or `ErrNoRows` type is proposed here (unlike the earlier draft) — sqlc's
generated methods return `sql.ErrNoRows` and driver errors directly, and
go-codex has no reason to wrap errors it never causes.

---

## Observer integration — `stats.SQLObserver`

Same type-assertion pattern as `stats.FileObserver`/`stats.SecurityObserver`:
a plain `stats.Observer` implementation still works (SQL-specific hooks are
skipped), richer telemetry is opt-in.

```go
// SQLObserver receives lifecycle events for codec validation and migrations
// in the SQL adapter. Implementations are type-asserted from the Observer
// passed in ValidateOptions/MigrateOptions.
type SQLObserver interface {
    // RecordValidation is called after every [Validate] call, success or
    // failure, with table/op context, duration, and error (nil on success).
    RecordValidation(table, op string, dur time.Duration, err error)

    // RecordMigration is called once per applied/rolled-back migration file
    // during Up/Down.
    RecordMigration(op, name string, version int64, dur time.Duration, err error)
}
```

Per-field validation failures inside a `RowValidationError` are additionally
reported via the existing `stats.Observer.RecordValidationError("sql_row",
constraint, field)` — consistent with `format.File`'s `"file"` location and
`mqtt`/`mqtt5`'s `"payload"`/`"topic_var"` locations.

---

## AsyncAPI / OpenAPI spec integration

**Scope decision: none.** A SQL table/query is not itself an API surface
go-codex's spec renderers describe — it's an internal persistence detail
behind whatever REST route or MQTT channel eventually exposes the data. The
`Codec[T]` bound to a sqlc struct can be reused as-is for a REST response
codec if the same shape is exposed over HTTP — its schema then appears in
the OpenAPI spec through that separate registration, same principle as the
`format.EmbeddedJSON` limitation already documented in `codec.md` (a codec
serves whichever boundary it's registered against; no automatic multi-boundary
propagation).

---

## Files to create

| File | Responsibility |
|---|---|
| `adapters/sql/migrate.go` | `Migrator`, `NewMigrator`, `Up`/`Down`/`Status`, `MigrationStatus`, `MigrateOptions` |
| `adapters/sql/validate.go` | `Validate[T]`, `ValidateOptions` — the encode→decode round-trip wrapper |
| `adapters/sql/errors.go` | `RowValidationError`, `MigrationError` — both with `LogValue()` |
| `adapters/sql/doc.go` | Package overview: goose (schema) + sqlc (structs/queries) + codec (refinement) split, usage sketch |
| `adapters/sql/migrate_test.go` | Tests against an in-memory `sqlite` DB (`modernc.org/sqlite`, pure-Go, no CGO) with embedded test migrations |
| `adapters/sql/validate_test.go` | Tests for `Validate` — happy path, Refine failure, RowValidationError unwrap/LogValue, works against a hand-written struct standing in for a sqlc-generated one |
| `.github/instructions/go-codex.instructions.md` | New `adapters/sql` row |

**Dependency choice:** `modernc.org/sqlite` (pure-Go, no CGO) for the test
suite only, consistent with the TCP adapter plan's "no CGO" preference.
`adapters/sql` itself has no compile-time dependency on any specific
database driver or on `sqlc`'s codegen binary — `sqlc generate` is a
developer-time step (like `templ generate`), not a runtime dependency.

---

## What's deferred / explicitly out of scope

- Any query builder or `WHERE` DSL — sqlc already owns query authoring
- Automatic struct↔column mapping or row scanning — sqlc already owns this
- Batch insert / `COPY`-style bulk loading
- Connection pooling / retry policy (user's `*sql.DB` responsibility, same
  stance as the AMQP plan's "adapter accepts a channel, not a connection")
- Multi-database transaction coordination — sqlc's generated `Queries`
  already accept a `DBTX` (works with `*sql.Tx`); go-codex adds nothing here
- Generating the `Codec[T]` automatically from the sqlc struct via
  reflection — rejected for the same reason "codec inheritance" was rejected
  in the field-factory-functions analysis: violates the no-reflection design
  principle and Go cannot safely infer per-field `Refine` rules from a struct
  definition alone

## Open questions before implementation

1. **Where does the sqlc-generated struct's field list boundary sit?**
   sqlc structs may include DB-only fields (e.g. `CreatedAt time.Time`,
   `UpdatedAt time.Time`) that the codec author may not want to validate at
   all. `codex.Struct` only needs `RequiredField`/`OptionalField` entries for
   fields that need validation — fields absent from the codec's field list
   are simply not round-tripped through `Encode`/`Decode`, so `Validate`
   would silently drop them on the encode→decode round trip. **This must be
   documented prominently**: `Validate` is only safe to use when the codec's
   field list matches the struct's field list 1:1 (as in every other
   go-codex `Struct` codec) — otherwise it acts as a lossy round trip. The
   practical guidance: declare one `Codec[T]` field per sqlc struct field,
   even for pass-through DB-managed columns, using no-op `Refine` (or none)
   when no validation is needed.
2. **sqlc `Params` structs vs result structs**: sqlc generates a separate
   `InsertUserParams` type from the result `User` type when columns differ
   (e.g. `ID` is server-generated). This likely means **two codecs** per
   table in practice (`insertUserParamsCodec`, `userCodec`) — acceptable, and
   consistent with go-codex's existing pattern of separate request/response
   codecs in `api/rest` and `api/reqreply`.
3. **goose dialect string vs sqlc engine string vs `database/sql` driver
   name**: three different tools, three different naming conventions for the
   same database engine (e.g. Postgres: goose `"postgres"`, sqlc `"postgresql"`,
   driver `"pgx"`). `NewMigrator` takes goose's dialect string explicitly;
   `Validate` takes no dialect parameter at all (it never touches the DB).
