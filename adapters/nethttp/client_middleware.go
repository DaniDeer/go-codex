package nethttp

import (
	"context"
	"net/http"
	"reflect"

	"github.com/DaniDeer/go-codex/api/rest"
)

// clientMiddlewareOutKey is the context key for client-side middleware Out
// values decoded by [ClientTransform]-attached [rest.ClientMiddlewareHandler]s.
type clientMiddlewareOutKey struct{}

// WithClientMiddlewareOut decorates ctx so decoded client-side middleware Out
// values (see [ClientMiddlewareOutFromContext]) become retrievable after a
// [Call]/[CallWithHandle] returns — mirrors [stats.WithDiagnostics]'s own
// sink-in-context technique, since [rest.ClientTransform]'s exact "how does
// the caller get Out back" return shape was deliberately left as a small
// signature detail to finalize during implementation, not a design blocker
// (see docs/design/d-0003-codec-declared-middlewares.md §5). A context-based
// accessor keeps [Call]'s/[CallWithHandle]'s own signature completely
// unchanged. A no-op (nothing recorded) when ctx was never so decorated —
// mirrors [stats.RecordDiagnostic]'s same no-op-when-absent safety.
func WithClientMiddlewareOut(ctx context.Context) context.Context {
	sink := &map[string]any{}
	return context.WithValue(ctx, clientMiddlewareOutKey{}, sink)
}

// ClientMiddlewareOutFromContext returns every client-side middleware Out
// value decoded during the most recent [Call]/[CallWithHandle] using this
// ctx, keyed by the middleware's own Declaration.Name. Returns nil if ctx
// was never decorated by [WithClientMiddlewareOut].
func ClientMiddlewareOutFromContext(ctx context.Context) map[string]any {
	if sink, ok := ctx.Value(clientMiddlewareOutKey{}).(*map[string]any); ok {
		return *sink
	}
	return nil
}

func recordClientMiddlewareOut(ctx context.Context, name string, out any) {
	sink, ok := ctx.Value(clientMiddlewareOutKey{}).(*map[string]any)
	if !ok {
		return
	}
	if *sink == nil {
		*sink = make(map[string]any)
	}
	(*sink)[name] = out
}

// dispatchClientMiddlewareIn runs every [rest.ClientMiddlewareHandler]'s Fn
// then EncodeIn (the SENDING role) for one call, in attachment order —
// mirrors adapters/nethttp/serve.go's runMiddlewareHandlersReflect technique
// for the client side. Returns merged header/cookie/query maps
// (registration-order, last-applied-wins across multiple middlewares,
// D6(c)) and a nil error on success; a Fn error aborts BEFORE any network
// activity (status 0 per [stats.Observer]'s existing convention), wrapped in
// [rest.MiddlewareError] — client-side fn errors are NOT run through
// ErrorResponseFor (that mechanism shapes SERVER responses; a client-side
// credential-style Fn error is simply returned to the caller directly,
// mirroring [mergeCredentialHeaders]'s own established convention).
func dispatchClientMiddlewareIn[Req any](ctx context.Context, req Req, handlers []rest.ClientMiddlewareHandler) (headers, cookies, query map[string]string, err error) {
	if len(handlers) == 0 {
		return nil, nil, nil, nil
	}
	headers = make(map[string]string)
	cookies = make(map[string]string)
	query = make(map[string]string)
	for _, h := range handlers {
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(req)})
		}
		if fnErr, _ := results[1].Interface().(error); fnErr != nil {
			return nil, nil, nil, rest.MiddlewareError{Name: h.Name, Err: fnErr}
		}
		in := results[0].Interface()
		hh, cc, qq, encErr := h.EncodeIn(in)
		if encErr != nil {
			return nil, nil, nil, encErr
		}
		for k, v := range hh {
			headers[k] = v
		}
		for k, v := range cc {
			cookies[k] = v
		}
		for k, v := range qq {
			query[k] = v
		}
	}
	return headers, cookies, query, nil
}

// dispatchClientMiddlewareOut runs every [rest.ClientMiddlewareHandler]'s
// DecodeOut (mechanical, no Fn) against resp's actual headers/cookies — the
// RECEIVING half of the client-side round trip, always attempted regardless
// of whether [dispatchClientMiddlewareIn] ran (response decode composes; no
// precedence conflict since each middleware owns an independent Out).
// Recorded via [recordClientMiddlewareOut] for later retrieval via
// [ClientMiddlewareOutFromContext].
func dispatchClientMiddlewareOut(ctx context.Context, resp *http.Response, handlers []rest.ClientMiddlewareHandler) error {
	if len(handlers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[k] = resp.Header.Get(k)
	}
	cookies := make(map[string]string, len(resp.Cookies()))
	for _, c := range resp.Cookies() {
		cookies[c.Name] = c.Value
	}
	for _, h := range handlers {
		out, err := h.DecodeOut(headers, cookies)
		if err != nil {
			return err
		}
		recordClientMiddlewareOut(ctx, h.Name, out)
	}
	return nil
}
