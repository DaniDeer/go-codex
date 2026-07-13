package sql_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	adaptersql "github.com/DaniDeer/go-codex/adapters/sql"
	"github.com/DaniDeer/go-codex/codex"
	gstream "github.com/DaniDeer/go-codex/stream"
	"github.com/DaniDeer/go-codex/validate"
)

var testRowCodec = codex.Struct[testRow](
	codex.RequiredField("id", codex.String().Refine(validate.NonEmptyString),
		func(r testRow) string { return r.ID },
		func(r *testRow, v string) { r.ID = v },
	),
	codex.RequiredField("value", codex.Float64().Refine(validate.PositiveFloat),
		func(r testRow) float64 { return r.Value },
		func(r *testRow, v float64) { r.Value = v },
	),
)

type testRow struct {
	ID    string
	Value float64
}

// ── QueryStream ───────────────────────────────────────────────────────────────

func TestQueryStream_EmitsRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	rows := []testRow{
		{ID: "r1", Value: 1.0},
		{ID: "r2", Value: 2.0},
	}
	callCount := 0
	queryFn := func(_ context.Context) ([]testRow, error) {
		callCount++
		if callCount == 1 {
			return rows, nil
		}
		return nil, nil // subsequent polls return nothing
	}

	s := adaptersql.QueryStream(ctx, testRowCodec, queryFn, 10*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "rows", Op: "list_rows"})

	vals, errs := gstream.Collect(ctx, s)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) < 2 {
		t.Errorf("want at least 2 rows, got %d", len(vals))
	}
}

func TestQueryStream_DatabaseErrorGoesToErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	queryFn := func(_ context.Context) ([]testRow, error) {
		return nil, fmt.Errorf("db connection refused")
	}

	s := adaptersql.QueryStream(ctx, testRowCodec, queryFn, 10*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "rows", Op: "list_rows"})

	_, errs := gstream.Collect(ctx, s)
	if len(errs) == 0 {
		t.Error("want at least 1 QueryStreamError, got 0")
	}
	var qse adaptersql.QueryStreamError
	if !errors.As(errs[0], &qse) {
		t.Errorf("want QueryStreamError, got %T", errs[0])
	}
}

func TestQueryStream_ValidationErrorGoesToErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	callCount := 0
	queryFn := func(_ context.Context) ([]testRow, error) {
		callCount++
		if callCount == 1 {
			return []testRow{{ID: "", Value: 1.0}}, nil // invalid: empty ID
		}
		return nil, nil
	}

	s := adaptersql.QueryStream(ctx, testRowCodec, queryFn, 10*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "rows", Op: "list_rows"})

	_, errs := gstream.Collect(ctx, s)
	if len(errs) == 0 {
		t.Error("want at least 1 RowValidationError, got 0")
	}
	var rve adaptersql.RowValidationError
	if !errors.As(errs[0], &rve) {
		t.Errorf("want RowValidationError, got %T", errs[0])
	}
}

// ── DrainInsert ───────────────────────────────────────────────────────────────

func TestDrainInsert_InsertsValidRows(t *testing.T) {
	ctx := context.Background()
	ch := make(chan testRow, 2)
	ch <- testRow{ID: "r1", Value: 1.0}
	ch <- testRow{ID: "r2", Value: 2.0}
	close(ch)
	src := gstream.From(ctx, ch)

	var inserted []testRow
	adaptersql.DrainInsert(ctx, testRowCodec, src,
		func(_ context.Context, r testRow) error {
			inserted = append(inserted, r)
			return nil
		},
		adaptersql.DrainInsertOptions{Table: "rows", Op: "insert_row"})

	if len(inserted) != 2 {
		t.Errorf("want 2 inserted, got %d", len(inserted))
	}
}

func TestDrainInsert_ValidationFailureGoesToOnError(t *testing.T) {
	ctx := context.Background()
	ch := make(chan testRow, 1)
	ch <- testRow{ID: "", Value: 1.0} // invalid
	close(ch)
	src := gstream.From(ctx, ch)

	var gotErr error
	adaptersql.DrainInsert(ctx, testRowCodec, src,
		func(_ context.Context, r testRow) error { return nil },
		adaptersql.DrainInsertOptions{
			Table:   "rows",
			Op:      "insert_row",
			OnError: func(e error) { gotErr = e },
		})

	var rve adaptersql.RowValidationError
	if !errors.As(gotErr, &rve) {
		t.Errorf("want RowValidationError, got %T", gotErr)
	}
}

func TestDrainInsert_InsertErrorGoesToOnError(t *testing.T) {
	ctx := context.Background()
	ch := make(chan testRow, 1)
	ch <- testRow{ID: "r1", Value: 1.0}
	close(ch)
	src := gstream.From(ctx, ch)

	var gotErr error
	adaptersql.DrainInsert(ctx, testRowCodec, src,
		func(_ context.Context, _ testRow) error { return fmt.Errorf("constraint violation") },
		adaptersql.DrainInsertOptions{
			Table:   "rows",
			Op:      "insert_row",
			OnError: func(e error) { gotErr = e },
		})

	var ise adaptersql.InsertStreamError
	if !errors.As(gotErr, &ise) {
		t.Errorf("want InsertStreamError, got %T: %v", gotErr, gotErr)
	}
	if ise.Table != "rows" {
		t.Errorf("Table: want %q, got %q", "rows", ise.Table)
	}
}

// ── Error type tests ──────────────────────────────────────────────────────────

func attrKeysSql(lv slog.Value) map[string]bool {
	keys := make(map[string]bool)
	for _, a := range lv.Group() {
		keys[a.Key] = true
	}
	return keys
}

func TestQueryStreamError_LogValue(t *testing.T) {
	e := adaptersql.QueryStreamError{Table: "t", Op: "op", Err: fmt.Errorf("db error")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysSql(lv)
	for _, k := range []string{"table", "op", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

func TestInsertStreamError_LogValue(t *testing.T) {
	e := adaptersql.InsertStreamError{Table: "t", Op: "insert", Err: fmt.Errorf("constraint")}
	lv := e.LogValue()
	if lv.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", lv.Kind())
	}
	keys := attrKeysSql(lv)
	for _, k := range []string{"table", "op", "err"} {
		if !keys[k] {
			t.Errorf("LogValue missing %q", k)
		}
	}
}

// ── QueryEachStream ───────────────────────────────────────────────────────────

func TestQueryEachStream_EmitsRowsPerItem(t *testing.T) {
	ctx := context.Background()

	// "database": given a sensor ID, return its readings
	db := map[string][]testRow{
		"s1": {{ID: "r1", Value: 1.0}, {ID: "r2", Value: 2.0}},
		"s2": {{ID: "r3", Value: 3.0}},
	}

	ch := make(chan testRow, 2)
	ch <- testRow{ID: "s1", Value: 10.0}
	ch <- testRow{ID: "s2", Value: 20.0}
	close(ch)

	s := adaptersql.QueryEachStream(ctx, testRowCodec,
		gstream.From(ctx, ch),
		func(ctx context.Context, in testRow) ([]testRow, error) {
			return db[in.ID], nil
		},
		adaptersql.QueryEachStreamOptions{Table: "readings", Op: "get_by_sensor", Buffer: 4})

	vals, errs := gstream.Collect(ctx, s)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 3 { // s1→2 rows + s2→1 row
		t.Errorf("want 3 rows total, got %d", len(vals))
	}
}

func TestQueryEachStream_DatabaseErrorGoesToErrors(t *testing.T) {
	ctx := context.Background()

	ch := make(chan testRow, 1)
	ch <- testRow{ID: "s1", Value: 5.0}
	close(ch)

	s := adaptersql.QueryEachStream(ctx, testRowCodec,
		gstream.From(ctx, ch),
		func(ctx context.Context, in testRow) ([]testRow, error) {
			return nil, fmt.Errorf("db connection lost")
		},
		adaptersql.QueryEachStreamOptions{Table: "readings", Op: "get_by_sensor"})

	_, errs := gstream.Collect(ctx, s)
	if len(errs) == 0 {
		t.Error("want QueryStreamError, got 0 errors")
	}
	var qse adaptersql.QueryStreamError
	if !errors.As(errs[0], &qse) {
		t.Errorf("want QueryStreamError, got %T", errs[0])
	}
}

func TestQueryEachStream_ValidationErrorGoesToErrors(t *testing.T) {
	ctx := context.Background()

	// Row with empty ID violates NonEmptyString
	badRow := testRow{ID: "", Value: 1.0}

	ch := make(chan testRow, 1)
	ch <- testRow{ID: "s1", Value: 5.0}
	close(ch)

	s := adaptersql.QueryEachStream(ctx, testRowCodec,
		gstream.From(ctx, ch),
		func(ctx context.Context, in testRow) ([]testRow, error) {
			return []testRow{badRow}, nil
		},
		adaptersql.QueryEachStreamOptions{Table: "readings", Op: "get_by_sensor"})

	_, errs := gstream.Collect(ctx, s)
	if len(errs) == 0 {
		t.Error("want RowValidationError, got 0 errors")
	}
}

func TestQueryEachStream_UpstreamErrorsForwarded(t *testing.T) {
	ctx := context.Background()

	valCh := make(chan testRow)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("upstream failure")
	close(errCh)
	close(valCh)

	s := adaptersql.QueryEachStream(ctx, testRowCodec,
		gstream.Stream[testRow]{Values: valCh, Errors: errCh},
		func(ctx context.Context, in testRow) ([]testRow, error) {
			return nil, nil
		},
		adaptersql.QueryEachStreamOptions{})

	_, errs := gstream.Collect(ctx, s)
	if len(errs) == 0 {
		t.Error("want upstream error forwarded, got 0")
	}
}
