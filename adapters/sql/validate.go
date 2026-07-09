package sql

import (
	"time"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// ValidateOptions configures observer and error context for a single [Validate]
// call.
type ValidateOptions struct {
	// Table names the sqlc-generated table or struct for error and observer
	// context (e.g. "users"). Purely descriptive — Validate does not touch
	// the database itself.
	Table string

	// Op names the sqlc operation being wrapped, e.g. "get_user" or
	// "insert_user". Matches sqlc's query name for easy correlation between
	// generated code and validation logs or metrics.
	Op string

	// Observer, when non-nil, receives per-validation lifecycle events.
	// If it also implements [stats.SQLObserver], RecordValidation is called
	// after every Validate call. Per-field failures are always reported via
	// [stats.Observer.RecordValidationError] with location "sql_row",
	// regardless of whether SQLObserver is implemented.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer
}

// Validate runs v through c's encode→decode round trip, applying every
// Refine and RefineFunc constraint declared on c.
//
// The returned T is the normalized value — the result of Decode after Encode.
// This may differ from v when a Refine step normalizes values (e.g. trimming
// whitespace). This matches the behaviour of [format.JSON.Read] and is the
// same round-trip semantics used throughout go-codex.
//
// Use Validate to validate a struct returned by a sqlc query method
// (post-query defence in depth against rows written by other clients) or a
// struct about to be passed into a sqlc insert or update method (pre-query,
// so invalid data never reaches the database).
//
// Codec failures are wrapped in [RowValidationError]. Per-field constraint
// failures are additionally reported via
// [stats.Observer.RecordValidationError] with location "sql_row".
// If opts.Observer also implements [stats.SQLObserver], RecordValidation is
// called after every Validate call, success or failure.
func Validate[T any](c codex.Codec[T], v T, opts ValidateOptions) (T, error) {
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	start := time.Now()

	intermediate, err := c.Encode(v)
	if err != nil {
		rve := RowValidationError{Table: opts.Table, Op: opts.Op, Err: err}
		stats.ReportErrors(obs, "sql_row", err)
		if so, ok := obs.(stats.SQLObserver); ok {
			so.RecordValidation(opts.Table, opts.Op, time.Since(start), rve)
		}
		var zero T
		return zero, rve
	}

	result, err := c.Decode(intermediate)
	if err != nil {
		rve := RowValidationError{Table: opts.Table, Op: opts.Op, Err: err}
		stats.ReportErrors(obs, "sql_row", err)
		if so, ok := obs.(stats.SQLObserver); ok {
			so.RecordValidation(opts.Table, opts.Op, time.Since(start), rve)
		}
		var zero T
		return zero, rve
	}

	if so, ok := obs.(stats.SQLObserver); ok {
		so.RecordValidation(opts.Table, opts.Op, time.Since(start), nil)
	}
	return result, nil
}
