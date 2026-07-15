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
	"github.com/DaniDeer/go-codex/ports"
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

// ── QueryAdapter ──────────────────────────────────────────────────────────────

func TestQueryAdapter_EmitsRows(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	rows := []testRow{{ID: "r1", Value: 1.0}, {ID: "r2", Value: 2.0}}
	queryFn := func(_ context.Context) ([]testRow, error) { return rows, nil }

	p, err := ports.NewSourcePort[testRow]("test", testRowCodec, ports.PortOptions{Buffer: 8})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptersql.QueryAdapter(testRowCodec, queryFn, 30*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "rows", Op: "list"}))
	vals, errs := gstream.Collect(ctx, p.Stream(ctx))
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) < 2 {
		t.Errorf("want ≥2 rows (from 1+ polls), got %d", len(vals))
	}
}

func TestQueryAdapter_DatabaseErrorGoesToErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	queryFn := func(_ context.Context) ([]testRow, error) {
		return nil, fmt.Errorf("connection refused")
	}

	p, err := ports.NewSourcePort[testRow]("test", testRowCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptersql.QueryAdapter(testRowCodec, queryFn, 20*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "rows", Op: "list"}))
	_, errs := gstream.Collect(ctx, p.Stream(ctx))
	if len(errs) == 0 {
		t.Fatal("want errors, got none")
	}
	var qse adaptersql.QueryStreamError
	if !errors.As(errs[0], &qse) {
		t.Errorf("want QueryStreamError, got %T", errs[0])
	}
}

func TestQueryAdapter_ValidationErrorGoesToErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Row with empty ID violates NonEmptyString constraint.
	queryFn := func(_ context.Context) ([]testRow, error) {
		return []testRow{{ID: "", Value: 1.0}}, nil
	}

	p, err := ports.NewSourcePort[testRow]("test", testRowCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptersql.QueryAdapter(testRowCodec, queryFn, 20*time.Millisecond,
		adaptersql.QueryStreamOptions{Table: "rows", Op: "list"}))
	_, errs := gstream.Collect(ctx, p.Stream(ctx))
	if len(errs) == 0 {
		t.Fatal("want validation error, got none")
	}
}

// ── DrainInsertAdapter ────────────────────────────────────────────────────────

func TestDrainInsertAdapter_InsertsValidRows(t *testing.T) {
	ctx := context.Background()
	var inserted []testRow
	insertFn := func(_ context.Context, r testRow) error {
		inserted = append(inserted, r)
		return nil
	}

	ch := make(chan testRow, 2)
	ch <- testRow{ID: "a", Value: 1.0}
	ch <- testRow{ID: "b", Value: 2.0}
	close(ch)

	p, err := ports.NewSinkPort[testRow]("test", testRowCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptersql.DrainInsertAdapter(testRowCodec, insertFn, adaptersql.DrainInsertOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if len(inserted) != 2 {
		t.Errorf("want 2 inserted, got %d", len(inserted))
	}
}

func TestDrainInsertAdapter_ValidationFailureGoesToOnError(t *testing.T) {
	ctx := context.Background()
	var gotErr error
	insertFn := func(_ context.Context, _ testRow) error { return nil }

	ch := make(chan testRow, 1)
	ch <- testRow{ID: "", Value: 1.0} // invalid: empty ID
	close(ch)

	p, err := ports.NewSinkPort[testRow]("test", testRowCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, adaptersql.DrainInsertAdapter(testRowCodec, insertFn,
		adaptersql.DrainInsertOptions{OnError: func(e error) { gotErr = e }}))
	p.Feed(ctx, gstream.From(ctx, ch))

	if gotErr == nil {
		t.Error("want validation error in OnError, got nil")
	}
}

// ── QueryEachAdapter ──────────────────────────────────────────────────────────

func TestQueryEachAdapter_EmitsRowsPerItem(t *testing.T) {
	ctx := context.Background()

	queryFn := func(_ context.Context, id string) ([]testRow, error) {
		return []testRow{{ID: id, Value: 42.0}}, nil
	}

	adapter := adaptersql.QueryEachAdapter(testRowCodec, queryFn, adaptersql.QueryEachStreamOptions{Table: "rows", Op: "get"})

	inCh := make(chan string, 2)
	inCh <- "r1"
	inCh <- "r2"
	close(inCh)
	src := gstream.From(ctx, inCh)

	out := adapter.Transform(ctx, src)
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 {
		t.Errorf("want 2 rows, got %d", len(vals))
	}
}

func TestQueryEachAdapter_DatabaseErrorGoesToErrors(t *testing.T) {
	ctx := context.Background()
	queryFn := func(_ context.Context, _ string) ([]testRow, error) {
		return nil, fmt.Errorf("db error")
	}

	adapter := adaptersql.QueryEachAdapter(testRowCodec, queryFn, adaptersql.QueryEachStreamOptions{})
	inCh := make(chan string, 1)
	inCh <- "r1"
	close(inCh)
	out := adapter.Transform(ctx, gstream.From(ctx, inCh))
	_, errs := gstream.Collect(ctx, out)
	if len(errs) == 0 {
		t.Fatal("want error, got none")
	}
	var qse adaptersql.QueryStreamError
	if !errors.As(errs[0], &qse) {
		t.Errorf("want QueryStreamError, got %T", errs[0])
	}
}

func TestQueryEachAdapter_UpstreamErrorsForwarded(t *testing.T) {
	ctx := context.Background()
	queryFn := func(_ context.Context, _ string) ([]testRow, error) { return nil, nil }

	adapter := adaptersql.QueryEachAdapter(testRowCodec, queryFn, adaptersql.QueryEachStreamOptions{})

	valCh := make(chan string)
	errCh := make(chan error, 1)
	errCh <- fmt.Errorf("upstream failure")
	close(errCh)
	close(valCh)
	src := gstream.Stream[string]{Values: valCh, Errors: errCh}

	out := adapter.Transform(ctx, src)
	_, errs := gstream.Collect(ctx, out)
	if len(errs) == 0 {
		t.Fatal("want upstream error forwarded")
	}
}

// ── Error LogValue tests ──────────────────────────────────────────────────────

func TestQueryStreamError_LogValue(t *testing.T) {
	e := adaptersql.QueryStreamError{Table: "readings", Op: "list", Err: errors.New("conn refused")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := groupKeys(v)
	for _, k := range []string{"table", "op", "err"} {
		if !keys[k] {
			t.Errorf("missing attribute %q", k)
		}
	}
	if errors.Unwrap(e) == nil {
		t.Error("Unwrap must return inner error")
	}
}

func TestInsertStreamError_LogValue(t *testing.T) {
	e := adaptersql.InsertStreamError{Table: "readings", Op: "insert", Err: errors.New("unique violation")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	keys := groupKeys(v)
	for _, k := range []string{"table", "op", "err"} {
		if !keys[k] {
			t.Errorf("missing attribute %q", k)
		}
	}
}

func groupKeys(v slog.Value) map[string]bool {
	keys := map[string]bool{}
	for _, a := range v.Group() {
		keys[a.Key] = true
	}
	return keys
}
