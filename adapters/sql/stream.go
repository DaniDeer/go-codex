package sql

import (
	"context"
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── QueryStream ───────────────────────────────────────────────────────────────

// QueryStreamOptions configures [QueryStream].
type QueryStreamOptions struct {
	// Table names the table being queried. Used in [QueryStreamError] context.
	Table string
	// Op names the query operation. Used in [QueryStreamError] context.
	Op string
	// Observer receives per-row lifecycle events. If it also implements
	// [stats.SQLObserver], RecordValidation is called after each row validate.
	// Per-field failures are reported via RecordValidationError with location "sql_row".
	Observer stats.Observer
	// Buffer is the output stream channel buffer size. Default 0.
	Buffer int
}

// QueryStream polls queryFn at interval, validates each returned row with codec,
// and emits each validated row to the returned [gstream.Stream].
//
// Each poll cycle calls queryFn and iterates over the result slice. Rows that
// fail codec validation are sent to Stream.Errors as [RowValidationError].
// Database-level errors from queryFn are sent to Stream.Errors as [QueryStreamError].
// The stream terminates when ctx is cancelled.
//
// queryFn must implement cursor or timestamp filtering to avoid re-emitting old rows:
//
//	gstream.QueryStream(ctx, readingCodec,
//	    func(ctx context.Context) ([]Reading, error) {
//	        return db.ListReadingsSince(ctx, time.Now().Add(-interval))
//	    },
//	    30*time.Second,
//	    sql.QueryStreamOptions{Table: "readings", Op: "list_readings_since", Observer: obs})
func QueryStream[T any](
	ctx context.Context,
	c codex.Codec[T],
	queryFn func(context.Context) ([]T, error),
	interval time.Duration,
	opts QueryStreamOptions,
) gstream.Stream[T] {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	values := make(chan T, opts.Buffer)
	errs := make(chan error, opts.Buffer)
	go func() {
		defer close(values)
		defer close(errs)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rows, err := queryFn(ctx)
				if err != nil {
					qse := QueryStreamError{Table: opts.Table, Op: opts.Op, Err: err}
					select {
					case errs <- qse:
					case <-ctx.Done():
						return
					}
					continue
				}
				for _, row := range rows {
					validated, valErr := Validate(c, row, ValidateOptions{
						Table:    opts.Table,
						Op:       opts.Op,
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
			}
		}
	}()
	return gstream.Stream[T]{Values: values, Errors: errs}
}

// ── DrainInsert ───────────────────────────────────────────────────────────────

// DrainInsertOptions configures [DrainInsert].
type DrainInsertOptions struct {
	// Table names the table being written. Used in error context.
	Table string
	// Op names the insert operation. Used in error context.
	Op string
	// OnError, when non-nil, is called for codec validation failures
	// ([RowValidationError]) and insert function errors ([InsertStreamError]).
	// If nil, errors are silently discarded.
	OnError func(error)
	// Observer receives per-row lifecycle events.
	Observer stats.Observer
}

// DrainInsert validates each value item from src with codec, then calls insertFn.
// Codec validation failures are sent to opts.OnError as [RowValidationError].
// insertFn errors are sent to opts.OnError as [InsertStreamError].
// Upstream stream errors are forwarded to opts.OnError unchanged.
// Blocks until src terminates or ctx is cancelled.
func DrainInsert[T any](
	ctx context.Context,
	c codex.Codec[T],
	src gstream.Stream[T],
	insertFn func(context.Context, T) error,
	opts DrainInsertOptions,
) {
	onErr := opts.OnError

	gstream.Drain(ctx, src,
		func(ctx context.Context, v T) error {
			validated, err := Validate(c, v, ValidateOptions{
				Table:    opts.Table,
				Op:       opts.Op,
				Observer: opts.Observer,
			})
			if err != nil {
				if onErr != nil {
					onErr(err)
				}
				return nil
			}
			if err := insertFn(ctx, validated); err != nil {
				ise := InsertStreamError{Table: opts.Table, Op: opts.Op, Err: err}
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
		gstream.DrainOptions{Observer: opts.Observer},
	)
}

// ── QueryEachStream ───────────────────────────────────────────────────────────

// QueryEachStreamOptions configures [QueryEachStream].
type QueryEachStreamOptions struct {
	// Table names the table being queried. Used in [QueryStreamError] context.
	Table string
	// Op names the query operation. Used in [QueryStreamError] context.
	Op string
	// Observer receives per-row lifecycle events. If it also implements
	// [stats.SQLObserver], RecordValidation is called after each row validate.
	// Per-field failures are reported via RecordValidationError with location "sql_row".
	Observer stats.Observer
	// Buffer is the output stream channel buffer size. Default 0.
	Buffer int
}

// QueryEachStream calls queryFn for each item in src, validates each returned
// row via codec, and emits validated rows to the returned [gstream.Stream[T]].
//
// For each item in src.Values, queryFn is called and its results are validated
// row by row. Database-level errors go to Stream.Errors as [QueryStreamError].
// Row codec validation failures go to Stream.Errors as [RowValidationError].
//
// Use QueryEachStream for per-item parameterized lookups — e.g. fetch the
// threshold configuration row for each sensor ID arriving in the stream:
//
//	thresholds := sql.QueryEachStream(ctx, thresholdCodec, sensorStream,
//	    func(ctx context.Context, s Sensor) ([]Threshold, error) {
//	        return db.GetThresholdBySensor(ctx, s.ID)
//	    },
//	    sql.QueryEachStreamOptions{Table: "thresholds", Op: "get_by_sensor"})
//
// Upstream errors from src.Errors are forwarded to Stream.Errors unchanged.
// The stream terminates when src closes or ctx is cancelled.
func QueryEachStream[In, T any](
	ctx context.Context,
	c codex.Codec[T],
	src gstream.Stream[In],
	queryFn func(context.Context, In) ([]T, error),
	opts QueryEachStreamOptions,
) gstream.Stream[T] {
	obs := opts.Observer
	if obs == nil {
		obs = stats.ObserverFromContext(ctx)
	}
	values := make(chan T, opts.Buffer)
	errs := make(chan error, opts.Buffer)
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
				rows, err := queryFn(ctx, item)
				if err != nil {
					qse := QueryStreamError{Table: opts.Table, Op: opts.Op, Err: err}
					select {
					case errs <- qse:
					case <-ctx.Done():
						return
					}
					continue
				}
				for _, row := range rows {
					validated, valErr := Validate(c, row, ValidateOptions{
						Table:    opts.Table,
						Op:       opts.Op,
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
