package sql

import (
	"context"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// DecorateInput wraps an exec-style sqlc-generated function (a `:exec` query,
// shaped `func(context.Context, Req) error`) with pre-query validation: arg
// is validated via codec BEFORE fn is called — fn is never invoked on
// invalid input. Returns a function with the identical signature, callable
// in place of the sqlc method everywhere:
//
//	insertUser := sqladapter.DecorateInput(queries.InsertUser, insertParamsCodec,
//	    sqladapter.ValidateOptions{Table: "users", Op: "insert_user"})
//	err := insertUser(ctx, params) // codec-validated automatically
//
// This is the declare-once counterpart to calling [Validate] by hand before
// every sqlc call (sql.md's "pre-query validation" usage mode) — it bundles
// the codec, Table, and Op into one reusable value the same way
// [ports.Cache]/[ports.NewCache] bundle a cache's key/format/TTL, so callers
// don't repeat them at every call site.
//
// Unlike bare [Validate] (which has no ctx parameter), the returned closure
// resolves [stats.ObserverFromContext] when opts.Observer is nil, since it
// wraps a ctx-taking function.
//
// Returns [RowValidationError] without calling fn when validation fails.
func DecorateInput[Req any](
	fn func(context.Context, Req) error,
	codec codex.Codec[Req],
	opts ValidateOptions,
) func(context.Context, Req) error {
	return func(ctx context.Context, req Req) error {
		callOpts := opts
		if callOpts.Observer == nil {
			callOpts.Observer = stats.ObserverFromContext(ctx)
		}
		validated, err := Validate(codec, req, callOpts)
		if err != nil {
			return err
		}
		return fn(ctx, validated)
	}
}

// DecorateOutput wraps a query-style sqlc-generated function (a `:one`/
// `:many` query, shaped `func(context.Context, Req) (Resp, error)`) with
// post-query validation: fn's returned Resp is validated via codec AFTER fn
// returns — defense in depth against rows written by other clients that
// bypassed the codec. Returns a function with the identical signature,
// callable in place of the sqlc method everywhere:
//
//	getUser := sqladapter.DecorateOutput(queries.GetUser, userCodec,
//	    sqladapter.ValidateOptions{Table: "users", Op: "get_user"})
//	u, err := getUser(ctx, id) // codec-validated automatically
//
// This is the declare-once counterpart to calling [Validate] by hand after
// every sqlc call (sql.md's "post-query validation" usage mode).
//
// Unlike bare [Validate] (which has no ctx parameter), the returned closure
// resolves [stats.ObserverFromContext] when opts.Observer is nil, since it
// wraps a ctx-taking function.
//
// fn's own error (e.g. sql.ErrNoRows) is returned unchanged — validation
// only runs on a successful call. Returns [RowValidationError] when fn
// succeeds but the returned value fails validation.
func DecorateOutput[Req, Resp any](
	fn func(context.Context, Req) (Resp, error),
	codec codex.Codec[Resp],
	opts ValidateOptions,
) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, req Req) (Resp, error) {
		resp, err := fn(ctx, req)
		if err != nil {
			var zero Resp
			return zero, err
		}
		callOpts := opts
		if callOpts.Observer == nil {
			callOpts.Observer = stats.ObserverFromContext(ctx)
		}
		return Validate(codec, resp, callOpts)
	}
}
