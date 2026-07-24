package websocket

import (
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/api/events"
)

// ErrorFrame declares, for a [DuplexSocketAdapter], how a matched upstream
// pipeline error (received on the port's outbound stream Errors channel) is
// realized on the socket — the WebSocket analogue of [events.ErrorChannel],
// adapted to a persistent multi-session transport that has no dedicated
// error-output topic: broadcast to every connected session IS the
// notification path.
//
// Two modes, mirroring [events.ErrorChannel]:
//   - Direct: no mapFn provided, E must be assignable to Out.
//   - Mapped: mapFn(E) produces Out.
//
// Matching is type-only via [errors.As]; the first declared ErrorFrame (in
// the [DuplexSocketAdapterOptions.ErrorFrames] slice) whose type matches
// wins — the same deterministic precedence used by REST/events error
// patterns.
//
// The default action is [events.ErrorRespond]: the mapped Out value is
// broadcast to every connected session. Use [ErrorFrameRule.WithAction] to
// select [events.ErrorHandle] (Handle runs instead, no broadcast) or
// [events.ErrorLog] (the error is forwarded to the port's Errors channel
// unchanged — the same as when no rule matches).
//
//	websocket.DuplexSocketAdapterOptions{
//	    ErrorFrames: []websocket.ErrorFrameRule[OutFrame]{
//	        websocket.ErrorFrame[domain.ValidationError, OutFrame](
//	            func(e domain.ValidationError) (OutFrame, error) {
//	                return OutFrame{Kind: "error", Message: e.Error()}, nil
//	            },
//	        ),
//	    },
//	}
func ErrorFrame[E error, Out any](mapFn ...func(E) (Out, error)) ErrorFrameRule[Out] {
	var mapper func(E) (Out, error)
	if len(mapFn) > 0 {
		mapper = mapFn[0]
	}
	return ErrorFrameRule[Out]{
		action: events.ErrorRespond,
		match: func(err error) (Out, bool, error) {
			var target E
			if !errors.As(err, &target) {
				var zero Out
				return zero, false, nil
			}
			if mapper != nil {
				v, mapErr := mapper(target)
				return v, true, mapErr
			}
			v, ok := any(target).(Out)
			if !ok {
				var zero Out
				return zero, true,
					fmt.Errorf("adapters/websocket: ErrorFrame direct mode: %T not assignable to frame", target)
			}
			return v, true, nil
		},
	}
}

// ErrorFrameRule is the [ChannelOpt]-equivalent value returned by
// [ErrorFrame] — pass a slice of these to
// [DuplexSocketAdapterOptions.ErrorFrames].
type ErrorFrameRule[Out any] struct {
	action  events.ErrorAction
	match   func(error) (Out, bool, error)
	onMatch func(error)
}

// WithAction returns a copy of r with Action set to action, overriding the
// default [events.ErrorRespond]. A matched pattern executes exactly one
// action — never an implicit handle-then-respond chain.
func (r ErrorFrameRule[Out]) WithAction(action events.ErrorAction) ErrorFrameRule[Out] {
	r.action = action
	return r
}

// WithHandle sets the callback invoked (with the original error) when this
// rule matches and its Action is [events.ErrorHandle]. No broadcast occurs
// in that case — the callback fully owns the error.
func (r ErrorFrameRule[Out]) WithHandle(handle func(error)) ErrorFrameRule[Out] {
	r.onMatch = handle
	return r
}
