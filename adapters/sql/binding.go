// Package sql provides protocol-agnostic SQL adapter bindings for the ports package.
//
// All adapters implement the [ports.SourceAdapter], [ports.SinkAdapter], and
// [ports.IOAdapter] interfaces and are wired to pipelines via
// [ports.SourcePort.Bind], [ports.SinkPort.Bind], and [ports.IOPort.Bind].
//
// Sources (use with [ports.SourcePort]):
//   - [QueryAdapter] — polls a SQL query at interval, emitting each validated row
//
// Intermediate (use with [ports.IOPort]):
//   - [QueryEachAdapter] — per-item parameterized SQL query (1:N rows per item)
//
// Sinks (use with [ports.SinkPort]):
//   - [DrainInsertAdapter] — validates and inserts each item via insertFn
package sql

import (
	"context"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// resolveTableOp defaults table and op from the bound port's declared
// [ports.SQLPattern] (via [ports.SQLMetaFromContext]) when the explicit
// option fields are empty. Explicit values always win.
func resolveTableOp(ctx context.Context, table, op string) (string, string) {
	if table != "" && op != "" {
		return table, op
	}
	m, ok := ports.SQLMetaFromContext(ctx)
	if !ok {
		return table, op
	}
	if table == "" {
		table = m.Table
	}
	if op == "" {
		op = m.Op
	}
	return table, op
}

// ── QueryAdapter ──────────────────────────────────────────────────────────────

// QueryStreamOptions configures [QueryAdapter].
type QueryStreamOptions struct {
	// Table names the table being queried. Used in [QueryStreamError] context.
	// When empty, defaults from the bound port's [ports.SQLPattern] declaration.
	Table string
	// Op names the query operation. Used in [QueryStreamError] context.
	// When empty, defaults from the bound port's [ports.SQLPattern] declaration.
	Op string
	// Observer receives per-row lifecycle events.
	Observer stats.Observer
	// Buffer is the output stream channel buffer size. Default 0.
	Buffer int
}

// QueryAdapter returns a [ports.SourceAdapter] that polls a SQL query at interval,
// emitting each validated row. Use with [ports.SourcePort.Bind]:
//
//	domain.Configs.Bind(ctx, sql.QueryAdapter(configCodec,
//	    func(ctx context.Context) ([]Config, error) { return db.ListConfigs(ctx) },
//	    5*time.Minute, sql.QueryStreamOptions{}))
func QueryAdapter[T any](
	codec codex.Codec[T],
	queryFn func(context.Context) ([]T, error),
	interval time.Duration,
	opts QueryStreamOptions,
) ports.SourceAdapter[T] {
	return &sqlQueryAdapter[T]{codec: codec, queryFn: queryFn, interval: interval, opts: opts}
}

type sqlQueryAdapter[T any] struct {
	codec    codex.Codec[T]
	queryFn  func(context.Context) ([]T, error)
	interval time.Duration
	opts     QueryStreamOptions
}

func (a *sqlQueryAdapter[T]) AdapterName() string { return "sql.QueryAdapter" }

func (a *sqlQueryAdapter[T]) Activate(ctx context.Context, dst chan<- T, errs chan<- error) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	table, op := resolveTableOp(ctx, a.opts.Table, a.opts.Op)
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := a.queryFn(ctx)
			if err != nil {
				qse := QueryStreamError{Table: table, Op: op, Err: err}
				select {
				case errs <- qse:
				case <-ctx.Done():
					return
				}
				continue
			}
			for _, row := range rows {
				validated, valErr := Validate(a.codec, row, ValidateOptions{
					Table:    table,
					Op:       op,
					Observer: obs,
				})
				if valErr != nil {
					select {
					case errs <- valErr:
					case <-ctx.Done():
						return
					}
					continue
				}
				select {
				case dst <- validated:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

// ── DrainInsertAdapter ────────────────────────────────────────────────────────

// DrainInsertOptions configures [DrainInsertAdapter].
type DrainInsertOptions struct {
	// Table and Op provide error/observability context. When empty, they
	// default from the bound port's [ports.SQLPattern] declaration.
	Table    string
	Op       string
	OnError  func(error)
	Observer stats.Observer
}

// DrainInsertAdapter returns a [ports.SinkAdapter] that validates and inserts
// each item via insertFn. Use with [ports.SinkPort.Bind]:
//
//	domain.Readings.Bind(ctx, sql.DrainInsertAdapter(readingCodec,
//	    func(ctx context.Context, r Reading) error { return db.Insert(ctx, r) },
//	    sql.DrainInsertOptions{}))
func DrainInsertAdapter[T any](
	codec codex.Codec[T],
	insertFn func(context.Context, T) error,
	opts DrainInsertOptions,
) ports.SinkAdapter[T] {
	return &sqlDrainInsertAdapter[T]{codec: codec, insertFn: insertFn, opts: opts}
}

type sqlDrainInsertAdapter[T any] struct {
	codec    codex.Codec[T]
	insertFn func(context.Context, T) error
	opts     DrainInsertOptions
}

func (a *sqlDrainInsertAdapter[T]) AdapterName() string { return "sql.DrainInsertAdapter" }

func (a *sqlDrainInsertAdapter[T]) Activate(ctx context.Context, src gstream.Stream[T]) {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	onErr := a.opts.OnError
	table, op := resolveTableOp(ctx, a.opts.Table, a.opts.Op)
	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			validated, valErr := Validate(a.codec, v, ValidateOptions{
				Table:    table,
				Op:       op,
				Observer: obs,
			})
			if valErr != nil {
				if onErr != nil {
					onErr(valErr)
				}
				return nil
			}
			if err := a.insertFn(ctx, validated); err != nil {
				ise := InsertStreamError{Table: table, Op: op, Err: err}
				if onErr != nil {
					onErr(ise)
				}
			}
			return nil
		},
		func(e error) {
			if onErr != nil {
				onErr(e)
			}
		},
		gstream.DrainOptions{Observer: obs},
	)
}

// ── QueryEachAdapter ──────────────────────────────────────────────────────────

// QueryEachStreamOptions configures [QueryEachAdapter].
type QueryEachStreamOptions struct {
	// Table and Op provide error/observability context. When empty, they
	// default from the bound port's [ports.SQLPattern] declaration.
	Table    string
	Op       string
	Observer stats.Observer
	Buffer   int
}

// QueryEachAdapter returns a [ports.IOAdapter] that performs a parameterized SQL
// query for each In item, emitting each result row as a T item (1:N). Use with
// [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, sql.QueryEachAdapter(thresholdCodec,
//	    func(ctx context.Context, s SensorReading) ([]Threshold, error) {
//	        return db.GetThresholdBySensor(ctx, s.SensorID)
//	    }, sql.QueryEachStreamOptions{Table: "thresholds", Op: "get_by_sensor"}))
func QueryEachAdapter[In, T any](
	codec codex.Codec[T],
	queryFn func(context.Context, In) ([]T, error),
	opts QueryEachStreamOptions,
) ports.IOAdapter[In, T] {
	return &sqlQueryEachAdapter[In, T]{codec: codec, queryFn: queryFn, opts: opts}
}

type sqlQueryEachAdapter[In, T any] struct {
	codec   codex.Codec[T]
	queryFn func(context.Context, In) ([]T, error)
	opts    QueryEachStreamOptions
}

func (a *sqlQueryEachAdapter[In, T]) AdapterName() string { return "sql.QueryEachAdapter" }

func (a *sqlQueryEachAdapter[In, T]) Transform(ctx context.Context, src gstream.Stream[In]) gstream.Stream[T] {
	obs := a.opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	table, op := resolveTableOp(ctx, a.opts.Table, a.opts.Op)
	values := make(chan T, a.opts.Buffer)
	errs := make(chan error, a.opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		valCh := src.Values
		errCh := src.Errors
		for valCh != nil || errCh != nil {
			select {
			case <-ctx.Done():
				return
			case item, ok := <-valCh:
				if !ok {
					valCh = nil
					continue
				}
				rows, err := a.queryFn(ctx, item)
				if err != nil {
					qse := QueryStreamError{Table: table, Op: op, Err: err}
					select {
					case errs <- qse:
					case <-ctx.Done():
						return
					}
					continue
				}
				for _, row := range rows {
					validated, valErr := Validate(a.codec, row, ValidateOptions{
						Table:    table,
						Op:       op,
						Observer: obs,
					})
					if valErr != nil {
						select {
						case errs <- valErr:
						case <-ctx.Done():
							return
						}
						continue
					}
					select {
					case values <- validated:
					case <-ctx.Done():
						return
					}
				}
			case e, ok := <-errCh:
				if !ok {
					errCh = nil
					continue
				}
				select {
				case errs <- e:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return gstream.Stream[T]{Values: values, Errors: errs}
}
