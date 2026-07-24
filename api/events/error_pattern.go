package events

import (
	"errors"
	"fmt"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
)

// ErrorAction selects how a matched error pattern is realized by the
// adapter. Events/pub-sub have no synchronous caller to respond to — a
// matched pattern executes exactly ONE of these, never an implicit
// handle-then-respond chain:
type ErrorAction string

const (
	// ErrorRespond publishes the declared typed error payload to the
	// pattern's declared error-output topic. This is the pub/sub analogue
	// of a caller-facing error response: since there is no synchronous
	// caller, "respond" means publish to a dedicated error channel. This is
	// the default action when [ErrorChannel] is declared without an
	// explicit action.
	ErrorRespond ErrorAction = "respond"
	// ErrorHandle performs no automatic publish; the adapter's existing
	// OnError-style callback (already accepted by the adapter's options)
	// runs instead, receiving the original error.
	ErrorHandle ErrorAction = "handle"
	// ErrorLog performs no automatic publish; the adapter forwards the
	// error through its normal observability/error-reporting path only
	// (identical to the unmatched-error fallback).
	ErrorLog ErrorAction = "log"
)

// ErrorChannelResponse is the result of a matched [ErrorChannel] pattern —
// the typed payload to publish, already encoded, plus the declared
// error-output topic and resolved action.
type ErrorChannelResponse struct {
	// Topic is the declared error-output topic Body should be published to.
	Topic string
	// Body is the JSON-encoded typed error payload.
	Body []byte
	// Value is the typed payload before encoding — useful for adapters
	// that want to re-encode with a non-JSON [format.Format].
	Value any
	// Action is the resolved action for the matched pattern.
	Action ErrorAction
}

// errorChannelRule is the type-erased runtime form of a declared
// [ErrorChannel] pattern, stored on [channelBuilder]/[ChannelHandle].
type errorChannelRule struct {
	topic  string
	action ErrorAction
	match  func(error) (ErrorChannelResponse, bool, error)
}

// errorChannelOpt implements [ChannelOpt] for [ErrorChannel].
type errorChannelOpt[E error, B any] struct {
	topic  string
	codec  codex.Codec[B]
	mapper func(E) (B, error)
	action ErrorAction
}

// ErrorChannel declares, for a channel, how a matched subscribe/pipeline
// error type is realized — the pub/sub analogue of [rest.ErrorPattern],
// adapted to the fact that pub/sub channels have no synchronous caller to
// respond to. For the default [ErrorRespond] action, a codec-backed typed
// payload is published to topic when an error matching E occurs.
//
// Two modes, mirroring [rest.ErrorPattern]:
//   - Direct: no mapFn provided, E must be assignable to B.
//   - Mapped: mapFn(E) produces B.
//
// Matching is type-only via [errors.As]; the first declared ErrorChannel (in
// [NewChannel] option order) whose type matches wins — the same
// deterministic precedence used by REST error patterns.
//
//	events.NewChannel[Reading]("sensors/{id}/data", readingCodec,
//	    events.ErrorChannel[domain.ValidationError, ErrorPayload](
//	        "sensors/{id}/errors", errorPayloadCodec,
//	        func(e domain.ValidationError) (ErrorPayload, error) {
//	            return ErrorPayload{Code: "validation", Message: e.Error()}, nil
//	        },
//	    ),
//	)
//
// Use [ErrorChannelOpt.WithAction] to override the default respond action
// with [ErrorHandle] (existing adapter OnError callback runs instead, no
// publish) or [ErrorLog] (adapter's normal error-forwarding path only, no
// publish) — the same three-way action model used across go-codex adapters.
func ErrorChannel[E error, B any](
	topic string,
	codec codex.Codec[B],
	mapFn ...func(E) (B, error),
) ErrorChannelOpt[E, B] {
	var mapper func(E) (B, error)
	if len(mapFn) > 0 {
		mapper = mapFn[0]
	}
	return ErrorChannelOpt[E, B]{opt: errorChannelOpt[E, B]{
		topic:  topic,
		codec:  codec,
		mapper: mapper,
		action: ErrorRespond,
	}}
}

// ErrorChannelOpt is the [ChannelOpt] value returned by [ErrorChannel].
type ErrorChannelOpt[E error, B any] struct {
	opt errorChannelOpt[E, B]
}

// WithAction returns a copy of o with Action set to action, overriding the
// default [ErrorRespond]. A matched pattern executes exactly one action —
// never an implicit handle-then-respond chain.
func (o ErrorChannelOpt[E, B]) WithAction(action ErrorAction) ErrorChannelOpt[E, B] {
	o.opt.action = action
	return o
}

func (o ErrorChannelOpt[E, B]) applyChannel(cb *channelBuilder) {
	jsonCodec := format.JSON(o.opt.codec)
	mapper := o.opt.mapper
	topic := o.opt.topic
	action := o.opt.action
	if action == "" {
		action = ErrorRespond
	}
	rule := errorChannelRule{
		topic:  topic,
		action: action,
		match: func(err error) (ErrorChannelResponse, bool, error) {
			var target E
			if !errors.As(err, &target) {
				return ErrorChannelResponse{}, false, nil
			}

			var (
				payload B
				ok      bool
			)
			if mapper != nil {
				mapped, mapErr := mapper(target)
				if mapErr != nil {
					return ErrorChannelResponse{}, true, mapErr
				}
				payload = mapped
			} else {
				payload, ok = any(target).(B)
				if !ok {
					return ErrorChannelResponse{}, true,
						fmt.Errorf("api/events: ErrorChannel direct mode: %T not assignable to payload", target)
				}
			}

			body, encErr := jsonCodec.Marshal(payload)
			if encErr != nil {
				return ErrorChannelResponse{}, true, encErr
			}
			return ErrorChannelResponse{Topic: topic, Body: body, Value: payload, Action: action}, true, nil
		},
	}
	cb.errorChannelRules = append(cb.errorChannelRules, rule)
}

// ErrorResponseFor returns the first declared [ErrorChannel] pattern match
// for err (matching via [errors.As], in declaration order), or
// (ErrorChannelResponse{}, false, nil) when none match.
//
// A non-nil third return value indicates the matched pattern's mapping or
// encoding failed — callers should treat this as a terminal error for that
// pattern (do not fall through to other patterns).
func (h *ChannelHandle[T]) ErrorResponseFor(err error) (ErrorChannelResponse, bool, error) {
	if err == nil {
		return ErrorChannelResponse{}, false, nil
	}
	for _, rule := range h.errorChannelRules {
		if rule.match == nil {
			continue
		}
		resp, matched, matchErr := rule.match(err)
		if !matched {
			continue
		}
		return resp, true, matchErr
	}
	return ErrorChannelResponse{}, false, nil
}
