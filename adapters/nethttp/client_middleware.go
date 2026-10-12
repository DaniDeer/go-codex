package nethttp

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/DaniDeer/go-codex/api/rest"
)

// clientMiddlewareOutKey is the context key for client-side middleware Out
// values decoded by a [rest.ClientMiddlewareHandler] — attached via
// [rest.Route.ClientMW]'s bound path or a bundled .Use(mw).
type clientMiddlewareOutKey struct{}

// WithClientMiddlewareOut decorates ctx so decoded client-side middleware Out
// values (see [ClientMiddlewareOutFromContext]) become retrievable after a
// [rest.Client.Call]/[rest.CallWithTransport] returns — mirrors
// [stats.WithDiagnostics]'s own sink-in-context technique: a
// [rest.ClientMiddlewareHandler]'s "how does the caller get Out back"
// return shape was deliberately left as a small signature detail to
// finalize during implementation, not a design blocker (see
// docs/design/d-0003-codec-declared-middlewares.md §5). A context-based
// accessor keeps [rest.Client.Call]'s/[rest.CallWithTransport]'s own
// signature completely unchanged. A no-op (nothing recorded) when ctx was
// never so decorated —
// mirrors [stats.RecordDiagnostic]'s same no-op-when-absent safety.
func WithClientMiddlewareOut(ctx context.Context) context.Context {
	sink := &map[string]any{}
	return context.WithValue(ctx, clientMiddlewareOutKey{}, sink)
}

// ClientMiddlewareOutFromContext returns every client-side middleware Out
// value decoded during the most recent [rest.Client.Call]/
// [rest.CallWithTransport] using this ctx, keyed by the middleware's own
// Declaration.Name. Returns nil if ctx
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
//
// contributed is PARALLEL to handlers — contributed[i] reports whether
// handlers[i] ITSELF produced at least one header/cookie/query value
// (as opposed to headers/cookies/query, which are already MERGED across
// every handler and cannot be attributed back to one specific handler).
// This lets a caller determine "did a SPECIFIC SUBSET of handlers
// (e.g. only the ones matching a security scheme) contribute anything"
// from this SAME single dispatch pass — see
// [securityRelevantContributed] — without a second invocation, which
// would call every handler's Fn TWICE per logical call (confirmed
// regression: [TestCall_ClientMWAppliedEveryCall] locks in Fn running
// EXACTLY once per Call).
func dispatchClientMiddlewareIn[Req any](ctx context.Context, req Req, handlers []rest.ClientMiddlewareHandler) (headers, cookies, query map[string]string, contributed []bool, err error) {
	if len(handlers) == 0 {
		return nil, nil, nil, nil, nil
	}
	headers = make(map[string]string)
	cookies = make(map[string]string)
	query = make(map[string]string)
	contributed = make([]bool, len(handlers))
	for i, h := range handlers {
		fnVal := reflect.ValueOf(h.Fn)
		var results []reflect.Value
		if h.Agnostic {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx)})
		} else {
			results = fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(req)})
		}
		if fnErr, _ := results[1].Interface().(error); fnErr != nil {
			return nil, nil, nil, nil, rest.MiddlewareError{Name: h.Name, Err: fnErr}
		}
		in := results[0].Interface()
		hh, cc, qq, encErr := h.EncodeIn(ctx, in)
		if encErr != nil {
			return nil, nil, nil, nil, encErr
		}
		contributed[i] = len(hh) > 0 || len(cc) > 0 || len(qq) > 0
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
	return headers, cookies, query, contributed, nil
}

// clientMiddlewareErrorLocation classifies a [dispatchClientMiddlewareIn]
// failure for [stats.ReportErrors]'s location argument, mirroring
// [DispatchMiddlewareHandlers]'s (server-side) own clean "middleware:in"
// vs "middleware:fn" split — a [rest.MiddlewareError] means a middleware's
// own Fn returned a business error; anything else (notably
// [rest.MiddlewareInputError], from [rest.Middleware]'s EncodeIn) is an
// input-derivation failure, reported as "middleware:in". Previously every
// [dispatchClientMiddlewareIn] failure was unconditionally reported as
// "middleware:fn", even an EncodeIn failure that never touched Fn at all.
func clientMiddlewareErrorLocation(err error) string {
	var fnErr rest.MiddlewareError
	if errors.As(err, &fnErr) {
		return "middleware:fn"
	}
	return "middleware:in"
}

// securityRelevantClientMiddlewareHandlers filters handlers to only
// those with a NON-EMPTY Satisfies matching one of secReqs' schemes —
// i.e., genuinely security-scoped handlers, built from a
// [rest.SecurityMiddleware]. A general-purpose [rest.Middleware] with NO
// Security declaration ALWAYS has an EMPTY Satisfies (see
// [satisfiesOf]'s own derivation: `if sec := mw.SecurityDeclaration();
// sec != nil { return []string{sec.SchemeName} }; return nil`) — so,
// for this population, empty Satisfies unambiguously means "not
// security-related," unlike the LEGACY [mergeCredentialHeaders]
// convention (a bare http.Header-returning credential Fn with no scheme
// restriction, which predates [rest.ClientMiddlewareHandler] entirely
// and genuinely does mean "applies broadly"). Confirmed, fixed bug:
// treating empty Satisfies as "matches any" for THIS population made
// attaching ANY non-security Middleware (e.g. a constant, always-
// present Accept-header declaration) alongside an anonymous-capable
// security middleware wrongly count as a security-credential
// contribution — see [securityRelevantClientMiddlewareContributed]'s own
// doc comment for the full, concrete failure this caused.
func securityRelevantClientMiddlewareHandlers(handlers []rest.ClientMiddlewareHandler, secReqs []rest.SecurityRequirement) []rest.ClientMiddlewareHandler {
	if len(handlers) == 0 || len(secReqs) == 0 {
		return nil
	}
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	var relevant []rest.ClientMiddlewareHandler
	for _, h := range handlers {
		for _, s := range h.Satisfies {
			if reqSchemes[s] {
				relevant = append(relevant, h)
				break
			}
		}
	}
	return relevant
}

// clientMiddlewareSatisfiesAny reports whether at least one of handlers
// declares a Satisfies scheme name matching one of secReqs' schemes —
// see [securityRelevantClientMiddlewareHandlers]'s own doc comment for
// why empty Satisfies does NOT count as a match here (unlike the
// unrelated, LEGACY [mergeCredentialHeaders] convention). Used by
// [clientTransport.Call]/[consumeOnce]/[consumeSSEOnce] to decide
// whether a codec-backed ClientMW (attached via
// [rest.Middleware.WithSend]) counts as "a credential Fn ran" for
// [rest.ClientCallOptions.OnCredentialRejected] purposes, since
// [dispatchClientMiddlewareIn] itself (unlike [mergeCredentialHeaders])
// runs every handler unconditionally and has no notion of "ran".
func clientMiddlewareSatisfiesAny(handlers []rest.ClientMiddlewareHandler, secReqs []rest.SecurityRequirement) bool {
	return len(securityRelevantClientMiddlewareHandlers(handlers, secReqs)) > 0
}

// securityRelevantContributed reports whether at least one
// SECURITY-RELEVANT handler (one whose Satisfies matches a scheme in
// secReqs -- see securityRelevantClientMiddlewareHandlers's own doc
// comment for why empty Satisfies never counts) ACTUALLY produced a
// header/cookie/query value -- gating rest.ValidateSecurityCredentials
// specifically. contributed is dispatchClientMiddlewareIn's own
// PARALLEL per-handler output from the SAME, SINGLE dispatch pass
// already performed for header/cookie/query MERGING -- this function
// performs NO additional dispatch/Fn invocation of its own (a confirmed,
// fixed regression: an earlier version of this fix re-dispatched the
// filtered subset separately, calling each security-relevant handler's
// Fn TWICE per logical Call -- see TestCall_ClientMWAppliedEveryCall,
// which locks in exactly ONE Fn invocation per Call).
//
// This is a DELIBERATELY narrower signal than "did ANY attached
// middleware contribute a header" (which the mwHeaders/mwCookies/
// mwQuery merge already computes, correctly including EVERY attached
// middleware regardless of security-relevance, for MERGING purposes) --
// conflating the two was the confirmed original bug: a non-security
// Middleware that always contributes a header (e.g. a constant
// Accept-header declaration, attached directly to a route alongside
// a Bearer-scheme credential) made that broader signal true even when
// the REAL security credential was legitimately empty/anonymous (its
// OWN header correctly omitted via rest.NewOmitEmptyHeaderParam),
// wrongly triggering ValidateSecurityCredentials and rejecting
// anonymous access entirely.
func securityRelevantContributed(handlers []rest.ClientMiddlewareHandler, contributed []bool, secReqs []rest.SecurityRequirement) bool {
	if len(handlers) == 0 || len(secReqs) == 0 {
		return false
	}
	reqSchemes := make(map[string]bool, len(secReqs))
	for _, req := range secReqs {
		for scheme := range req {
			reqSchemes[scheme] = true
		}
	}
	for i, h := range handlers {
		if i >= len(contributed) || !contributed[i] {
			continue
		}
		for _, s := range h.Satisfies {
			if reqSchemes[s] {
				return true
			}
		}
	}
	return false
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
	rawHeaders := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		rawHeaders[k] = resp.Header.Get(k)
	}
	cookies := make(map[string]string, len(resp.Cookies()))
	for _, c := range resp.Cookies() {
		cookies[c.Name] = c.Value
	}
	for _, h := range handlers {
		headers := rest.NormalizeHeaderVars(rawHeaders, h.ResponseHeaderNames)
		out, err := h.DecodeOut(ctx, headers, cookies)
		if err != nil {
			return err
		}
		recordClientMiddlewareOut(ctx, h.Name, out)
	}
	return nil
}
