package nethttp

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
)

// sseRedirectFollowUpInfo captures what [clientTransport.followSSERedirect]
// needs from the ORIGINATING [clientTransport.consumeOnce] connection
// attempt to build a follow-up request — reusing the ORIGINATING
// attempt's own already-computed credentials/cookies/Accept header
// (round 4 finding A, applies identically to Consume per
// docs/roadmap/rest-typed-redirects.md's finding D), never the
// registered target route's own.
type sseRedirectFollowUpInfo struct {
	credHeaders http.Header
	cookieVars  map[string]string
	accept      string
}

// followSSERedirect resolves and follows an SSE redirect chain starting
// from current (the 3xx just received by [clientTransport.consumeOnce])
// — transparently reconnecting into a registered sseRoutes target
// (round 2 finding D) and continuing to dispatch events through the
// SAME dispatchFn/eventType the caller's Consume call was typed for. A
// match in routes-only (Call-decodable, not streamable) is
// [rest.RedirectTargetNotStreamableError]; no match is
// [rest.UnrecognizedRedirectError]; exceeding remaining hops is
// [rest.RedirectChainTooDeepError] (Depth reports configuredMax,
// mirroring [followRedirectHop]'s identical convention). This is a
// PARALLEL path to consumeOnce's normal connect logic — NOT a recursive
// call into [rest.Client.Consume] (round 4 finding H2, applies
// identically here).
func (t *clientTransport) followSSERedirect(ctx context.Context, current rest.RedirectError, info sseRedirectFollowUpInfo, eventType reflect.Type, dispatchFn reflect.Value, remaining, configuredMax int) (hadTraffic bool, err error) {
	if remaining <= 0 {
		return false, rest.RedirectChainTooDeepError{Location: current.Location, Depth: configuredMax}
	}

	if handle, ok := t.client.MatchRedirectSSERoute(http.MethodGet, current.Location); ok {
		return t.doFollowSSERequest(ctx, current, info, eventType, dispatchFn, handle, remaining, configuredMax)
	}
	if _, ok := t.client.MatchRedirectRoute(http.MethodGet, current.Location); ok {
		return false, rest.RedirectTargetNotStreamableError{Location: current.Location}
	}
	return false, rest.UnrecognizedRedirectError{Location: current.Location, Status: current.Status}
}

// doFollowSSERequest issues ONE follow-up SSE connection against handle
// (the matched target's *SSERouteHandle[TargetReq,TargetEvent], as
// `any`, recovered via reflection) — verifies the target's own Event
// type is IDENTICAL to the originating eventType first (dispatchFn is
// monomorphized to it via reflect.FuncOf; a mismatched type cannot be
// reflect-called without panicking, so a mismatch is treated exactly
// like "no usable match": [rest.UnrecognizedRedirectError]), then
// decodes+dispatches via [scanAndDispatchSSE], continuing the chain via
// [clientTransport.followSSERedirect] with one fewer hop remaining if
// the follow-up response is ITSELF a redirect.
func (t *clientTransport) doFollowSSERequest(ctx context.Context, current rest.RedirectError, info sseRedirectFollowUpInfo, eventType reflect.Type, dispatchFn reflect.Value, handle any, remaining, configuredMax int) (bool, error) {
	handleVal := reflect.ValueOf(handle)
	resolveEventDecoderMethod := handleVal.MethodByName("ResolveEventDecoder") // func(string, ...format.Format[Event]) func([]byte) (Event, error)
	effectiveFormatsMethod := handleVal.MethodByName("EffectiveEventFormats")  // func(...format.Format[Event]) []format.Format[Event]
	decodeEventFuncType := resolveEventDecoderMethod.Type().Out(0)
	targetEventType := decodeEventFuncType.Out(0)
	if targetEventType != eventType {
		return false, rest.UnrecognizedRedirectError{Location: current.Location, Status: current.Status}
	}

	rawURL := strings.TrimRight(t.caller.baseURL, "/") + current.Location
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, RequestBuildError{Err: err}
	}
	if info.accept != "" {
		httpReq.Header.Set("Accept", info.accept)
	}
	for k, vs := range info.credHeaders {
		for _, v := range vs {
			httpReq.Header.Add(k, v)
		}
	}
	for k, v := range info.cookieVars {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	resp, err := t.caller.client.Do(httpReq)
	if err != nil {
		return false, RequestError{Method: http.MethodGet, Path: current.Location, Err: err}
	}
	defer resp.Body.Close()

	if isRedirectStatus(resp.StatusCode) {
		next := rest.RedirectError{Status: resp.StatusCode, Location: resp.Header.Get("Location")}
		return t.followSSERedirect(ctx, next, info, eventType, dispatchFn, remaining-1, configuredMax)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return false, UnexpectedStatusError{Method: http.MethodGet, Path: current.Location, StatusCode: resp.StatusCode, Body: body, Header: resp.Header}
	}

	// formatsVal is the target's own default (nil variadic) — a
	// redirected SSE connection negotiates via the TARGET route's own
	// declared Formats, not the originating route's (which may not even
	// apply to a structurally different route).
	formatsVal := reflect.Zero(effectiveFormatsMethod.Type().In(0))
	decode := resolveEventDecoderMethod.CallSlice([]reflect.Value{reflect.ValueOf(httpReq.Header.Get("Accept")), formatsVal})[0]

	return scanAndDispatchSSE(ctx, resp.Body, decode, dispatchFn), nil
}
