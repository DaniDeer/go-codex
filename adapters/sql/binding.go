package sql

import (
	"context"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── QueryAdapter ──────────────────────────────────────────────────────────────

// QueryAdapter returns a [ports.SourceAdapter] that polls a SQL query at interval,
// emitting each row. Use with [ports.SourcePort.Bind]:
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
	s := QueryStream(ctx, a.codec, a.queryFn, a.interval, a.opts)
	valCh := s.Values
	errCh := s.Errors
	for valCh != nil || errCh != nil {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-valCh:
			if !ok {
				valCh = nil
				continue
			}
			select {
			case dst <- v:
			case <-ctx.Done():
				return
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
}

// ── DrainInsertAdapter ────────────────────────────────────────────────────────

// DrainInsertAdapter returns a [ports.SinkAdapter] that inserts each item via
// insertFn. Use with [ports.SinkPort.Bind]:
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
	DrainInsert(ctx, a.codec, src, a.insertFn, a.opts)
}

// ── QueryEachAdapter ──────────────────────────────────────────────────────────

// QueryEachAdapter returns a [ports.IOAdapter] that performs a parameterized SQL
// query for each In item, emitting each result row as a T item (1:N). Use with
// [ports.IOPort.Bind]:
//
//	domain.Calibration.Bind(ctx, sql.QueryEachAdapter(thresholdCodec,
//	    func(ctx context.Context, s SensorReading) ([]Threshold, error) {
//	        return db.GetThresholdBySensor(ctx, s.SensorID)
//	    }, sql.QueryEachStreamOptions{Table: "thresholds", Op: "get_by_sensor"}))
//
// One In item may produce N T items. Use [ports.IOPort] with [ports.NewIOPort][In, T].
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
	return QueryEachStream(ctx, a.codec, src, a.queryFn, a.opts)
}
