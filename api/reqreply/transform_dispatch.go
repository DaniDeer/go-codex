package reqreply

import (
	"context"
	"reflect"
)

// DispatchServerMiddlewareHandlers runs every attached [MiddlewareHandler]
// in registration order — AFTER the paired security Fn, mirroring D1's
// dispatch order. reqPtr is the route's own decoded *Req (addressable) —
// read AND potentially enriched by each bound handler's fn (Transform-
// attached; an Agnostic/bundled handler's fn never sees it). Returns the
// accumulated reply-side topic/property vars every handler's EncodeOut
// produced (later handlers win on a name conflict — D6(c),
// "last-applied-wins"). failKind distinguishes a DecodeIn failure ("in",
// wraps as [MiddlewareInputError]) from the fn's own business error
// ("fn", wraps as [MiddlewareError], D2's fallback) from an EncodeOut
// failure ("out", building the REPLY's Out struct) — the caller reports
// "middleware:in"/"middleware:fn"/"middleware:out" accordingly.
func DispatchServerMiddlewareHandlers(
	ctx context.Context,
	reqPtr reflect.Value,
	handlers []MiddlewareHandler,
	topicVars, propertyVars map[string]string,
) (outTopicVars, outPropertyVars map[string]string, name string, failKind string, err error) {
	for _, h := range handlers {
		inAny, decErr := h.DecodeIn(topicVars, propertyVars)
		if decErr != nil {
			return nil, nil, h.Name, "in", decErr
		}
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(inAny)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr, reflect.ValueOf(inAny)})
		}
		if errI, _ := results[1].Interface().(error); errI != nil {
			return nil, nil, h.Name, "fn", MiddlewareError{Name: h.Name, Err: errI}
		}
		outAny := results[0].Interface()
		tVars, pVars, encErr := h.EncodeOut(outAny)
		if encErr != nil {
			return nil, nil, h.Name, "out", encErr
		}
		outTopicVars = MergeVarsOverride(outTopicVars, tVars)
		outPropertyVars = MergeVarsOverride(outPropertyVars, pVars)
	}
	return outTopicVars, outPropertyVars, "", "", nil
}

// DispatchClientMiddlewareIn is [DispatchServerMiddlewareHandlers]'s
// CLIENT-side, request-encode-direction sibling — runs every attached
// [ClientMiddlewareHandler] in registration order, producing In (via Fn)
// then encoding it into topic/property vars — accumulated with later
// handlers winning on a name conflict, mirroring the server side.
func DispatchClientMiddlewareIn(
	ctx context.Context,
	reqVal reflect.Value,
	handlers []ClientMiddlewareHandler,
) (topicVars, propertyVars map[string]string, name string, err error) {
	for _, h := range handlers {
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqVal})
		}
		if errI, _ := results[1].Interface().(error); errI != nil {
			return nil, nil, h.Name, MiddlewareError{Name: h.Name, Err: errI}
		}
		inAny := results[0].Interface()
		tVars, pVars, encErr := h.EncodeIn(inAny)
		if encErr != nil {
			return nil, nil, h.Name, encErr
		}
		topicVars = MergeVarsOverride(topicVars, tVars)
		propertyVars = MergeVarsOverride(propertyVars, pVars)
	}
	return topicVars, propertyVars, "", nil
}

// DispatchClientMiddlewareOut is [DispatchClientMiddlewareIn]'s reply-
// decode-direction sibling — mechanically decodes every attached
// [ClientMiddlewareHandler]'s own Out value from the reply's actual
// topic/property vars, no Fn involved (mirrors `rest`'s identical
// "no Fn, no reply-inspection Fn needed" design). Only the first decode
// failure is reported — a malformed reply fails the call; the decoded
// values themselves are not currently surfaced further.
func DispatchClientMiddlewareOut(
	topicVars, propertyVars map[string]string,
	handlers []ClientMiddlewareHandler,
) error {
	for _, h := range handlers {
		// h.DecodeOut already returns a properly-wrapped
		// MiddlewareOutputError on failure (see
		// api/reqreply/transform.go's buildDecodeOut) — no re-wrap needed.
		if _, err := h.DecodeOut(topicVars, propertyVars); err != nil {
			return err
		}
	}
	return nil
}

// MergeVarsOverride merges src into dst, src's values WINNING on a key
// conflict — mirrors D3's real, shipped precedence rule (see
// docs/design/d-0003-codec-declared-middlewares.md's Addendum's "Value
// precedence" section: "middleware-derived ALWAYS wins over route-own-
// derived").
func MergeVarsOverride(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	out := make(map[string]string, len(dst)+len(src))
	for k, v := range dst {
		out[k] = v
	}
	for k, v := range src {
		out[k] = v
	}
	return out
}
