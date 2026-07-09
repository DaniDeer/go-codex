package sql

import (
	"context"
	"database/sql"
	"io/fs"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/DaniDeer/go-codex/stats"
)

// MigrationStatus describes the applied or pending state of one migration file.
type MigrationStatus struct {
	// Version is the numeric prefix of the migration file (e.g. 1 for
	// "00001_create_users.sql").
	Version int64

	// Name is the migration file path.
	Name string

	// AppliedAt is when this migration was applied. The zero value means the
	// migration is pending.
	AppliedAt time.Time
}

// MigrateOptions configures a single [Migrator.Up] or [Migrator.Down] call.
type MigrateOptions struct {
	// Observer, when non-nil and implementing [stats.SQLObserver], receives
	// [stats.SQLObserver.RecordMigration] once per applied or rolled-back
	// migration file. Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// Migrator wraps pressly/goose with go-codex structured errors and observer
// integration. Construct one with [NewMigrator].
//
// Migrator never inspects or validates row data — it operates only on schema
// DDL text. Schema evolution and codec-level row validation are separate
// concerns by design.
type Migrator struct {
	db      *sql.DB
	fs      fs.FS
	dir     string
	dialect goose.Dialect
}

// NewMigrator constructs a Migrator for the given database connection and
// embedded migrations filesystem.
//
//   - db is the open database connection. The caller owns its lifecycle.
//   - migrations is the [fs.FS] containing migration files, typically an
//     [embed.FS] declared with //go:embed.
//   - dir is the sub-path within migrations where the .sql files live
//     (e.g. "migrations").
//   - dialect is one of goose's supported dialect constants as a string:
//     "postgres", "mysql", "sqlite3", "mssql", "redshift", "tidb",
//     "clickhouse", "vertica", "ydb", "spanner", or "turso".
func NewMigrator(db *sql.DB, migrations fs.FS, dir string, dialect string) (*Migrator, error) {
	m := &Migrator{db: db, fs: migrations, dir: dir, dialect: goose.Dialect(dialect)}
	// Validate the dialect and directory by attempting to build a provider.
	if _, err := m.newProvider(); err != nil {
		return nil, err
	}
	return m, nil
}

// Up applies all pending migrations. Each successfully applied migration file
// triggers [stats.SQLObserver.RecordMigration] on opts.Observer (when the
// observer implements that interface).
//
// Returns a [MigrationError] with Op "up" on failure.
func (m *Migrator) Up(ctx context.Context, opts MigrateOptions) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	provider, err := m.newProvider()
	if err != nil {
		return err
	}

	results, err := provider.Up(ctx)
	if so, ok := obs.(stats.SQLObserver); ok {
		for _, r := range results {
			so.RecordMigration("up", r.Source.Path, r.Source.Version, r.Duration, r.Error)
		}
	}
	if err != nil {
		return MigrationError{Op: "up", Err: err}
	}
	return nil
}

// Down rolls back the most recently applied migration. Triggers
// [stats.SQLObserver.RecordMigration] on opts.Observer when implemented.
//
// Returns a [MigrationError] with Op "down" on failure.
func (m *Migrator) Down(ctx context.Context, opts MigrateOptions) error {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	provider, err := m.newProvider()
	if err != nil {
		return err
	}

	result, err := provider.Down(ctx)
	if err != nil {
		return MigrationError{Op: "down", Err: err}
	}
	if result != nil {
		if so, ok := obs.(stats.SQLObserver); ok {
			so.RecordMigration("down", result.Source.Path, result.Source.Version, result.Duration, result.Error)
		}
	}
	return nil
}

// Status returns the applied or pending state of every migration file.
// Returns a [MigrationError] with Op "status" on failure.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	provider, err := m.newProvider()
	if err != nil {
		return nil, err
	}

	sources, err := provider.Status(ctx)
	if err != nil {
		return nil, MigrationError{Op: "status", Err: err}
	}

	out := make([]MigrationStatus, len(sources))
	for i, s := range sources {
		out[i] = MigrationStatus{
			Version:   s.Source.Version,
			Name:      s.Source.Path,
			AppliedAt: s.AppliedAt,
		}
	}
	return out, nil
}

func (m *Migrator) newProvider() (*goose.Provider, error) {
	sub, err := fs.Sub(m.fs, m.dir)
	if err != nil {
		return nil, MigrationError{Op: "init", Err: err}
	}
	p, err := goose.NewProvider(m.dialect, m.db, sub)
	if err != nil {
		return nil, MigrationError{Op: "init", Err: err}
	}
	return p, nil
}
