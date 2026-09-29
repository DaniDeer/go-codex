package rest

import (
	"context"
	"reflect"

	"github.com/DaniDeer/go-codex/stats"
)

// ErrorResponseWriter is an OPTIONAL, type-asserted interface
// (docs/roadmap/capability-requirement-composition.md's Phase 7 —
// mirrors Phase 6's HeaderCapableTransport-style precedent: additive,
// NOT a required method on [ServerTransport]) an adapter's own
// response-writer type implements to realize a matched [ErrorPattern]
// onto the wire. [DispatchErrorResponse]/[CallDispatchErrorResponse]
// own the ENTIRE match→encode→validate step and call this interface's
// ONE method only for the final, genuinely protocol-specific write —
// headers/cookies/status/body onto whatever the adapter's underlying
// connection is (an `http.ResponseWriter` for nethttp/chi today).
type ErrorResponseWriter interface {
	WriteErrorResponse(headers map[string][]string, cookies []PendingCookie, status int, body []byte) error
}

// DispatchErrorResponse is the RECOMMENDED single call site for every
// Category-A failure point on a concrete Req/Resp route (replaces the
// former per-adapter `tryRespondErrorPatternGeneric`): consults a
// declared [ErrorPattern] via [RouteHandle.ObserveErrorResponseFor]
// (which ALSO reports match/miss/span-tag observability internally),
// encodes the matched value's response merge fields, validates the
// combined (pre-existing staged + newly-encoded) header/cookie set,
// and — on a match with the [ErrorRespond] action — calls w's
// [ErrorResponseWriter.WriteErrorResponse] to realize it onto the wire.
//
// respHeaders/pendingCookies carry whatever the caller's own request
// dispatch has ALREADY staged before this error occurred (e.g. a
// middleware's own EncodeOut) — this function MERGES the matched
// pattern's own encoded values into them before validating/writing,
// mirroring the pre-Phase-7 per-adapter function's identical contract.
//
// Returns handled=true when the response was fully written — the
// caller should return immediately without invoking its own
// fixed-shape error handler. Returns handled=false (unmatched, a
// non-Respond action, or a write failure) when the caller should fall
// through to its EXISTING fixed-shape error handling, using
// updatedErr (which may differ from the err passed in — a mapFn/encode
// failure or a response-write failure) for that subsequent call.
func (h *RouteHandle[Req, Resp]) DispatchErrorResponse(
	ctx context.Context, obs stats.Observer, w ErrorResponseWriter,
	respHeaders map[string][]string, pendingCookies []PendingCookie, err error,
) (handled bool, updatedErr error) {
	resp, matched, applyErr := h.ObserveErrorResponseFor(ctx, obs, err)
	if !matched {
		return false, err
	}
	if applyErr != nil {
		return false, applyErr
	}
	if resp.Action != "" && resp.Action != ErrorRespond {
		return false, err
	}
	if respVal, ok := resp.Value.(Resp); ok {
		headerValues, cookieValues, encErr := h.EncodeResponseMergeFields(respVal)
		if encErr != nil {
			ReportResponseHeaderErrors(ctx, encErr)
			ReportResponseCookieErrors(ctx, encErr)
			return false, encErr
		}
		for k, v := range headerValues {
			respHeaders[k] = []string{v}
		}
		cookieAttrs := h.EncodeResponseCookieAttributes(respVal)
		for k, v := range cookieValues {
			pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v, Attrs: cookieAttrs[k]})
		}
	}
	if verr := h.ValidateResponseHeaders(responseHeaderValuesFrom(respHeaders)); verr != nil {
		ReportResponseHeaderErrors(ctx, verr)
		return false, verr
	}
	if verr := h.ValidateResponseCookies(responseCookieValuesFrom(pendingCookies)); verr != nil {
		ReportResponseCookieErrors(ctx, verr)
		return false, verr
	}
	if writeErr := w.WriteErrorResponse(respHeaders, pendingCookies, resp.Status, resp.Body); writeErr != nil {
		return false, writeErr
	}
	return true, err
}

// CallDispatchErrorResponse is [RouteHandle.DispatchErrorResponse]'s
// reflection-based equivalent (mirrors [CallObserveErrorResponseFor]
// exactly) — replaces the former per-adapter `tryRespondErrorPattern`
// for a heterogeneous route collection's dispatch, where no concrete
// Resp type parameter is available at the call site. target must be an
// addressable *RouteHandle[Req, Resp] reflect.Value (Req/Resp erased at
// the caller's own call site).
func CallDispatchErrorResponse(
	target reflect.Value, ctx context.Context, obs stats.Observer, w ErrorResponseWriter,
	respHeaders map[string][]string, pendingCookies []PendingCookie, err error,
) (handled bool, updatedErr error) {
	results := target.MethodByName("DispatchErrorResponse").Call([]reflect.Value{
		reflect.ValueOf(ctx), reflect.ValueOf(&obs).Elem(), reflect.ValueOf(&w).Elem(),
		reflect.ValueOf(respHeaders), reflect.ValueOf(pendingCookies), reflect.ValueOf(&err).Elem(),
	})
	handled, _ = results[0].Interface().(bool)
	updatedErr, _ = results[1].Interface().(error)
	return handled, updatedErr
}
