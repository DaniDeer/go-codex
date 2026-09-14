package zeromq

import (
	"context"
	"reflect"

	"github.com/DaniDeer/go-codex/api/events"
)

// middlewareDispatchError distinguishes [dispatchSubscribeMiddlewareHandlers]'s
// two failure kinds so the caller knows how to respond: a DecodeIn failure
// has no business error yet (isFnError false); a Fn failure IS
// ErrorPattern-eligible (D2), needing Name for [events.MiddlewareError]'s
// fallback. Mirrors adapters/mqtt5's identical type.
type middlewareDispatchError struct {
	err       error
	isFnError bool
	name      string
}

func (e middlewareDispatchError) Error() string { return e.err.Error() }
func (e middlewareDispatchError) Unwrap() error { return e.err }

// dispatchSubscribeMiddlewareHandlers dispatches every [events.MiddlewareHandler]
// attached to this channel (via Transform or a bundled .Use()) at the SAME
// pre-handler dispatch point runSubscribeSecurityImpls already runs at
// (D1) — msg is the SAME already-decoded *T the handler will also
// receive, so a bound mw's fn may read/enrich it. Mirrors
// adapters/mqtt5's identical function. propertyVars is ALWAYS nil/empty
// here — zeromq has no property mechanism at all; a channel declaring a
// REQUIRED property fails naturally with the SAME MiddlewareInputError a
// missing topic var would, no special-casing needed.
func dispatchSubscribeMiddlewareHandlers[T any](ctx context.Context, msg *T, handlers []events.MiddlewareHandler, topicVars, propertyVars map[string]string) error {
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

// dispatchPublishMiddlewareHandlers dispatches every [events.ClientMiddlewareHandler]
// attached to this channel (via ClientTransform or a bundled .Use()) at
// the SAME pre-publish dispatch point [runPublishSecurityImpls] already
// runs at — msg is the caller's OWN already-built value. Mirrors
// adapters/mqtt5's identical function. Returns TWO SEPARATE merged maps
// (topicVars, propertyVars) — zeromq has no write-target for propertyVars
// (no property mechanism), so the caller simply discards it; kept as a
// second return value purely for signature parity with mqtt5's identical
// function (a required property still fails naturally at DecodeIn time on
// the subscribe side, never here).
func dispatchPublishMiddlewareHandlers[T any](ctx context.Context, msg T, handlers []events.ClientMiddlewareHandler) (topicVars, propertyVars map[string]string, err error) {
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
			return nil, nil, middlewareDispatchError{err: fnErr, isFnError: true, name: h.Name}
		}
		out := results[0].Interface()
		mwTopicVars, mwPropertyVars, encErr := h.EncodeOut(out)
		if encErr != nil {
			return nil, nil, encErr
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

// overrideDerivedVars merges derived (middleware-produced) and explicit
// (caller/channel-own) topic var maps, with explicit taking precedence on
// key collision — mirrors adapters/mqtt5's identical helper.
func overrideDerivedVars(derived, explicit map[string]string) map[string]string {
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
