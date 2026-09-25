package events

import (
	"context"
	"errors"
	"reflect"
)

// middlewareDispatchError distinguishes a dispatch function's failure
// kinds so the caller knows how to respond AND which observer location to
// report: a DecodeIn failure has no business error yet (IsFnError AND
// IsEncodeErr both false, reported as "middleware:in"); a Fn failure IS
// ErrorPattern-eligible (D2), needing Name for [MiddlewareError]'s
// fallback (IsFnError true, reported as "middleware:fn"); an EncodeOut
// failure (a middleware's own Out fails to encode into topic/property
// vars — [DispatchPublishMiddlewareHandlers]'s own failure mode) is
// reported as "middleware:out", symmetric with REST's own
// "middleware:out". Kept unexported — callers use
// [AsMiddlewareDispatchError] to inspect it, mirroring
// [MiddlewareError]'s own already-wrapped shape rather than exposing this
// dispatch-classification type directly (no identified external consumer
// needs the raw type, only its fields).
type middlewareDispatchError struct {
	err         error
	isFnError   bool
	isEncodeErr bool
	name        string
}

func (e middlewareDispatchError) Error() string { return e.err.Error() }
func (e middlewareDispatchError) Unwrap() error { return e.err }

// MiddlewareDispatchInfo is the caller-visible view of a
// [middlewareDispatchError], recovered via [AsMiddlewareDispatchError].
type MiddlewareDispatchInfo struct {
	// Err is the underlying error — the DecodeIn/Fn/EncodeOut failure
	// itself.
	Err error
	// IsFnError is true when Err is a middleware Fn's own business error
	// (ErrorPattern-eligible, falls back to [MiddlewareError]).
	IsFnError bool
	// IsEncodeErr is true when Err came from a middleware's own Out
	// failing to encode into topic/property vars (publish-side only).
	IsEncodeErr bool
	// Name is the failing middleware's [Declaration.Name].
	Name string
}

// AsMiddlewareDispatchError reports whether err is (or wraps) a dispatch
// function's internal failure classification, returning the recovered
// [MiddlewareDispatchInfo] when true. Adapters use this to decide how to
// respond (DecodeIn vs. Fn vs. EncodeOut failure) without needing the
// unexported [middlewareDispatchError] type itself.
func AsMiddlewareDispatchError(err error) (MiddlewareDispatchInfo, bool) {
	var e middlewareDispatchError
	if !errors.As(err, &e) {
		return MiddlewareDispatchInfo{}, false
	}
	return MiddlewareDispatchInfo{
		Err:         e.err,
		IsFnError:   e.isFnError,
		IsEncodeErr: e.isEncodeErr,
		Name:        e.name,
	}, true
}

// DispatchSubscribeMiddlewareHandlers dispatches every [MiddlewareHandler]
// attached to a channel (via Transform or a bundled .Use()) at the SAME
// pre-handler dispatch point an adapter's own security enforcement
// already runs at (D1) — msg is the SAME already-decoded *T the handler
// will also receive, so a bound mw's fn may read/enrich it (subscribe has
// no Out to encode — see [Middleware]'s doc comment). propertyVars is the
// property vocabulary axis's OWN, SEPARATE map (real MQTT5 User
// Properties) — decoded independently from topicVars, never combined
// (mirrors reqreply's/REST's own multi-map-never-combined pattern). Pass
// nil for propertyVars on an adapter with no property mechanism (e.g.
// MQTT v3, ZeroMQ) — a channel declaring a REQUIRED property still fails
// naturally with the SAME [MiddlewareInputError] a missing topic var
// would, no special-casing needed by the caller.
func DispatchSubscribeMiddlewareHandlers[T any](ctx context.Context, msg *T, handlers []MiddlewareHandler, topicVars, propertyVars map[string]string) error {
	for _, h := range handlers {
		in, err := h.DecodeIn(topicVars, propertyVars)
		if err != nil {
			return middlewareDispatchError{err: err, name: h.Name}
		}
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(in)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(msg), reflect.ValueOf(in)})
		}
		if fnErr, _ := results[0].Interface().(error); fnErr != nil {
			return middlewareDispatchError{err: fnErr, isFnError: true, name: h.Name}
		}
	}
	return nil
}

// DispatchPublishMiddlewareHandlers dispatches every [ClientMiddlewareHandler]
// attached to a channel (via ClientTransform or a bundled .Use()) at the
// SAME pre-publish dispatch point an adapter's own security enforcement
// already runs at — msg is the caller's OWN already-built value (VALUE,
// not pointer — mirrors REST client's req Req). Returns TWO SEPARATE
// merged maps derived from every middleware's own Out (registration-
// order, last-applied-wins, D6(c)): topicVars feeds
// [ChannelHandle.BuildTopic], propertyVars merges into the outgoing
// message's real protocol-native metadata (e.g. MQTT5 User Properties) —
// kept separate since they have entirely different downstream consumers,
// never combined into one map. An adapter with no property mechanism
// simply discards the propertyVars return.
func DispatchPublishMiddlewareHandlers[T any](ctx context.Context, msg T, handlers []ClientMiddlewareHandler) (topicVars, propertyVars map[string]string, err error) {
	if len(handlers) == 0 {
		return nil, nil, nil
	}
	topicVars = make(map[string]string)
	propertyVars = make(map[string]string)
	for _, h := range handlers {
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(msg)})
		}
		if fnErr, _ := results[1].Interface().(error); fnErr != nil {
			// Wrapped in MiddlewareError HERE (dispatch-function level) —
			// mirroring rest/reqreply's own client-in dispatch, which both
			// already wrap their Fn error the SAME way.
			return nil, nil, middlewareDispatchError{err: MiddlewareError{Name: h.Name, Err: fnErr}, isFnError: true, name: h.Name}
		}
		out := results[0].Interface()
		mwTopicVars, mwPropertyVars, encErr := h.EncodeOut(out)
		if encErr != nil {
			return nil, nil, middlewareDispatchError{err: encErr, isEncodeErr: true, name: h.Name}
		}
		for k, v := range mwTopicVars {
			topicVars[k] = v
		}
		for k, v := range mwPropertyVars {
			propertyVars[k] = v
		}
	}
	return topicVars, propertyVars, nil
}

// OverrideDerivedVars merges derived (middleware-produced) and explicit
// (caller/channel-own) topic var maps, with explicit taking precedence on
// key collision — mirrors D3's real, shipped precedence rule. Returns nil
// when both inputs are empty.
func OverrideDerivedVars(derived, explicit map[string]string) map[string]string {
	if len(derived) == 0 {
		return explicit
	}
	if len(explicit) == 0 {
		return derived
	}
	out := make(map[string]string, len(derived)+len(explicit))
	for k, v := range derived {
		out[k] = v
	}
	for k, v := range explicit {
		out[k] = v
	}
	return out
}
