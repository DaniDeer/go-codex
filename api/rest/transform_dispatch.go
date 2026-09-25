package rest

import (
	"context"
	"errors"
	"reflect"

	"github.com/DaniDeer/go-codex/stats"
)

// CallObserveErrorResponseFor reflect-calls
// ObserveErrorResponseFor(ctx, obs, err) on target — the RECOMMENDED
// single call site for every Category-A failure point (see
// docs/design/d-0005-error-handling.md's Topic 1/5): consults a declared
// [ErrorPattern] AND reports match/miss/span-tag observability internally,
// in one call. target must be an addressable *RouteHandle[Req, Resp]
// reflect.Value (Req/Resp erased at the caller's own call site).
func CallObserveErrorResponseFor(target reflect.Value, ctx context.Context, obs stats.Observer, err error) (ErrorPatternResponse, bool, error) {
	results := target.MethodByName("ObserveErrorResponseFor").Call([]reflect.Value{
		reflect.ValueOf(ctx), reflect.ValueOf(&obs).Elem(), reflect.ValueOf(&err).Elem(),
	})
	resp, _ := results[0].Interface().(ErrorPatternResponse)
	matched, _ := results[1].Interface().(bool)
	applyErr, _ := results[2].Interface().(error)
	return resp, matched, applyErr
}

// middlewareDispatchError distinguishes [DispatchMiddlewareHandlers]'s two
// failure kinds so the caller (an adapter's own request loop) knows how
// to respond: a DecodeIn failure is a plain param-validation-style
// failure (no ErrorPattern consultation — no business error exists yet);
// a Fn failure IS ErrorPattern-eligible (D2), needing Name for
// [MiddlewareError]'s fallback. Kept unexported — callers use
// [AsMiddlewareDispatchError] to inspect it, mirroring
// [MiddlewareError]'s own already-wrapped shape rather than exposing this
// dispatch-classification type directly.
type middlewareDispatchError struct {
	err       error
	isFnError bool
	name      string
}

func (e middlewareDispatchError) Error() string { return e.err.Error() }
func (e middlewareDispatchError) Unwrap() error { return e.err }

// MiddlewareDispatchInfo is the caller-visible view of a
// [middlewareDispatchError], recovered via [AsMiddlewareDispatchError].
type MiddlewareDispatchInfo struct {
	// Err is the underlying error — the DecodeIn/Fn failure itself.
	Err error
	// IsFnError is true when Err is a middleware Fn's own business error
	// (ErrorPattern-eligible, falls back to [MiddlewareError]).
	IsFnError bool
	// Name is the failing middleware's [middleware.Declaration.Name].
	Name string
}

// AsMiddlewareDispatchError reports whether err is (or wraps)
// [DispatchMiddlewareHandlers]'s internal failure classification,
// returning the recovered [MiddlewareDispatchInfo] when true. Adapters
// use this to decide how to respond (DecodeIn vs. Fn failure) without
// needing the unexported [middlewareDispatchError] type itself.
func AsMiddlewareDispatchError(err error) (MiddlewareDispatchInfo, bool) {
	var e middlewareDispatchError
	if !errors.As(err, &e) {
		return MiddlewareDispatchInfo{}, false
	}
	return MiddlewareDispatchInfo{Err: e.err, IsFnError: e.isFnError, Name: e.name}, true
}

// DispatchMiddlewareHandlers dispatches every [MiddlewareHandler] attached
// to a route (via Transform/ClientTransform OR a bundled .Use(mw)) at the
// SAME pre-handler dispatch point an adapter's own security enforcement
// already runs at (D1) — reqPtr is the SAME already-decoded *Req the
// handler will also receive, so a bound mw's fn may read/enrich it.
// Returns each handler's decoded Out (boxed `any`, in attachment order)
// for later [MiddlewareHandler.EncodeOut] composition into the response,
// once the route's own handler has produced its Resp.
func DispatchMiddlewareHandlers(ctx context.Context, reqPtr reflect.Value, handlers []MiddlewareHandler, headerVars, cookieVars, queryVars map[string]string) ([]any, error) {
	if len(handlers) == 0 {
		return nil, nil
	}
	outs := make([]any, len(handlers))
	for i, h := range handlers {
		in, err := h.DecodeIn(headerVars, cookieVars, queryVars)
		if err != nil {
			stats.ReportErrors(DiagnosticObserver{Ctx: ctx}, "middleware:in", err)
			return nil, middlewareDispatchError{err: err, name: h.Name}
		}
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(in)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr, reflect.ValueOf(in)})
		}
		if fnErr, _ := results[1].Interface().(error); fnErr != nil {
			stats.ReportErrors(DiagnosticObserver{Ctx: ctx}, "middleware:fn", fnErr)
			return nil, middlewareDispatchError{err: fnErr, isFnError: true, name: h.Name}
		}
		outs[i] = results[0].Interface()
	}
	return outs, nil
}
