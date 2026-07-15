package adapters

import (
	"context"
	"fmt"

	sqladapter "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/examples/sensor-service/db"
	"github.com/DaniDeer/go-codex/examples/sensor-service/domain"
	"github.com/DaniDeer/go-codex/stats"
)

// ReadingStore wraps the sqlc-generated *db.Queries. It backs the HTTP
// handlers and the save closure main() binds to the ioports.Readings
// persistence port — the pipeline itself never sees it.
//
// Every write path calls sqladapter.Validate(domain.InsertParamsCodec, ...)
// before reaching the DB — the codec rejects invalid data so it never reaches
// SQL.
//
// Every read path calls sqladapter.Validate(domain.ReadingCodec, ...) after
// the DB returns a row — defence in depth against data written by other
// clients that bypassed the codec.
type ReadingStore struct {
	queries *db.Queries
}

// NewReadingStore wraps the sqlc-generated queries value.
func NewReadingStore(queries *db.Queries) *ReadingStore {
	return &ReadingStore{queries: queries}
}

// Queries exposes the raw sqlc queries for read-only demo access
// (e.g. sql.QueryAdapter's ListReadings poll).
func (s *ReadingStore) Queries() *db.Queries { return s.queries }

// Save validates params against domain.InsertParamsCodec and inserts the row.
func (s *ReadingStore) Save(ctx context.Context, params db.InsertReadingParams) error {
	validated, err := sqladapter.Validate(domain.InsertParamsCodec, params, sqladapter.ValidateOptions{
		Table: "readings", Op: "insert_reading",
		Observer: stats.ObserverFromContext(ctx), // sql.Validate has no ctx; read from ctx
	})
	if err != nil {
		return fmt.Errorf("pre-insert validation: %w", err)
	}
	return s.queries.InsertReading(ctx, validated)
}

// Get fetches a row and validates it against domain.ReadingCodec.
func (s *ReadingStore) Get(ctx context.Context, id string) (db.Reading, error) {
	row, err := s.queries.GetReading(ctx, id)
	if err != nil {
		return db.Reading{}, err
	}
	return sqladapter.Validate(domain.ReadingCodec, row, sqladapter.ValidateOptions{
		Table: "readings", Op: "get_reading",
		Observer: stats.ObserverFromContext(ctx), // sql.Validate has no ctx; read from ctx
	})
}
