package websocket

import (
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// ErrorFrameResponse is the adapter-ready payload produced by a matched
// [ErrorFrame] rule — the typed payload to broadcast, already encoded, plus
// the resolved action.
type ErrorFrameResponse struct {
	// Body is the JSON-encoded typed error payload.
	Body []byte
	// Value is the typed payload before encoding.
	Value any
	// Action is the resolved action for the matched rule.
	Action events.ErrorAction
}

// ErrorFrame declares, for a [DuplexSocketAdapter], how a matched upstream
// pipeline error (received on the port's outbound stream Errors channel) is
// realized on the socket — the WebSocket analogue of [events.ErrorChannel],
// adapted to a persistent multi-session transport that has no dedicated
// error-output topic: broadcast to every connected session IS the
// notification path.
//
// ErrorFrame declares its OWN codec-backed payload type B — independent of
// the socket's happy-path Out frame type — the same "one-struct-one-call"
// guarantee [rest.ErrorPattern]/[events.ErrorChannel]/[reqreply.ErrorPattern]/
// [mcp.ErrorPattern] all provide: B is validated via its declared codec
// (all Refine constraints run) before being broadcast, exactly like the
// happy path.
//
// Two modes, mirroring [events.ErrorChannel]:
//   - Direct: no mapFn provided, E must be assignable to B.
//   - Mapped: mapFn(E) produces B.
//
// Matching is type-only via [errors.As]; the first declared ErrorFrame (in
// the [DuplexSocketAdapterOptions.ErrorFrames] slice) whose type matches
// wins — the same deterministic precedence used by REST/events error
// patterns.
//
// The default action is [events.ErrorRespond]: the mapped B value is
// broadcast to every connected session. Use [ErrorFrameRule.WithAction] to
// select [events.ErrorHandle] (Handle runs instead, no broadcast) or
// [events.ErrorLog] (the error is forwarded to the port's Errors channel
// unchanged — the same as when no rule matches).
//
//	websocket.DuplexSocketAdapterOptions{
//	    ErrorFrames: []websocket.ErrorFrameRule{
//	        websocket.ErrorFrame[domain.ValidationError, ErrorPayload](errorPayloadCodec,
//	            func(e domain.ValidationError) (ErrorPayload, error) {
//	                return ErrorPayload{Code: "validation", Message: e.Error()}, nil
//	            },
//	        ),
//	    },
//	}
func ErrorFrame[E error, B any](
	codec codex.Codec[B],
	mapFn ...func(E) (B, error),
) ErrorFrameRule {
	jsonCodec := format.JSON(codec)
	var mapper func(E) (B, error)
	if len(mapFn) > 0 {
		mapper = mapFn[0]
	}
	action := events.ErrorRespond
	return ErrorFrameRule{
		action: action,
		match: func(err error) (ErrorFrameResponse, bool, error) {
			var target E
			if !errors.As(err, &target) {
				return ErrorFrameResponse{}, false, nil
			}

			var (
				payload B
				ok      bool
			)
			if mapper != nil {
				mapped, mapErr := mapper(target)
				if mapErr != nil {
					return ErrorFrameResponse{}, true, mapErr
				}
				payload = mapped
			} else {
				payload, ok = any(target).(B)
				if !ok {
					return ErrorFrameResponse{}, true,
						fmt.Errorf("adapters/websocket: ErrorFrame direct mode: %T not assignable to payload", target)
				}
			}

			body, encErr := jsonCodec.Marshal(payload)
			if encErr != nil {
				return ErrorFrameResponse{}, true, encErr
			}
			return ErrorFrameResponse{Body: body, Value: payload, Action: action}, true, nil
		},
	}
}

// ErrorFrameRule is the value returned by [ErrorFrame] — pass a slice of
// these to [DuplexSocketAdapterOptions.ErrorFrames]. Unlike the pre-codec
// design, ErrorFrameRule is NOT parameterized by the socket's Out type — its
// payload is independently codec-validated and pre-encoded, so it needs no
// type-erased storage or runtime type assertion at Activate time.
type ErrorFrameRule struct {
	action  events.ErrorAction
	match   func(error) (ErrorFrameResponse, bool, error)
	onMatch func(error)
}

// WithAction returns a copy of r with Action set to action, overriding the
// default [events.ErrorRespond]. A matched pattern executes exactly one
// action — never an implicit handle-then-respond chain.
func (r ErrorFrameRule) WithAction(action events.ErrorAction) ErrorFrameRule {
	// action is captured inside the original match closure's returned
	// response — wrap it so the override is reflected without re-deriving
	// the whole rule.
	prevMatch := r.match
	r.action = action
	r.match = func(err error) (ErrorFrameResponse, bool, error) {
		resp, matched, mapErr := prevMatch(err)
		if !matched {
			return resp, false, nil
		}
		resp.Action = action
		return resp, true, mapErr
	}
	return r
}

// WithHandle sets the callback invoked (with the original error) when this
// rule matches and its Action is [events.ErrorHandle]. No broadcast occurs
// in that case — the callback fully owns the error.
func (r ErrorFrameRule) WithHandle(handle func(error)) ErrorFrameRule {
	r.onMatch = handle
	return r
}
