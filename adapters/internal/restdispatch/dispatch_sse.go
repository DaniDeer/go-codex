package restdispatch

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/internal/httpsecurity"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// WriteSSEData writes data as one or more spec-compliant SSE "data:"
// lines to w, terminated by the blank line that dispatches the event.
// Per the WHATWG Server-Sent Events spec, EVERY line of a multi-line
// data value must carry its OWN "data: " prefix — a single "data: "
// prefix in front of a raw multi-line payload is NOT spec-compliant: any
// standards-conformant reader would misread everything after the first
// embedded newline as un-prefixed noise, silently losing it. Promoted
// (Phase 4, SSE family dispatch-core consolidation) verbatim from
// adapters/nethttp's/adapters/chi's own byte-for-byte-identical
// function of the same name (confirmed via direct diff).
func WriteSSEData(w io.Writer, data []byte) (int, error) {
	var buf bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		buf.WriteString("data: ")
		buf.Write(line)
		buf.WriteByte('\n')
	}
	buf.WriteByte('\n')
	return w.Write(buf.Bytes())
}

// DispatchSSERoute runs the full validate/security/middleware/handle
// per-event streaming pipeline for one SSE route, built from cr (the
// result of a prior, successful [ValidateRouteConstruction] call — the
// SAME construction-time core plain routes use: confirmed, via direct
// field-by-field comparison, that [rest.SSERouteHandle]'s field names/
// types exactly match [rest.RouteHandle]'s for every field
// [ValidateRouteConstruction] reads, so no separate SSE construction
// entry point was needed; only cr.RespType means something different —
// the bare `error` type, since an SSE handler's shape is
// func(ctx, Req, send func(Event) error) error, not func(ctx, Req)
// (Resp, error) — harmless, since this function never reads
// cr.RespType).
//
// This is the shared, byte-for-byte-identical logic PREVIOUSLY
// independently duplicated in adapters/nethttp's and adapters/chi's own
// buildSSERouteHandler (confirmed via direct diff during Phase 4's SSE
// family dispatch-core consolidation — see
// docs/roadmap/adapter-integration-architecture.md). Unlike
// [DispatchRoute], SSE has NO [rest.ErrorPattern] dispatch at all (a
// pre-existing, separate gap this consolidation does not silently add —
// confirmed in the pre-consolidation code's own comments).
//
// ctx is the caller's ALREADY-FULLY-WRAPPED request context, exactly
// like [DispatchRoute] — see that function's own doc comment for the
// full rationale (package-private context-key sentinels cannot be
// shared). responseHeaders/pendingCookies are likewise passed explicitly
// rather than re-derived from ctx.
func DispatchSSERoute(
	ctx context.Context,
	cr ConstructedRoute,
	sw ResponseWriter,
	r *http.Request,
	responseHeaders http.Header,
	pendingCookies *[]rest.PendingCookie,
	pathValues func(*http.Request, []string) map[string]string,
	writeCookie func(w http.ResponseWriter, name, value string, attrs rest.CookieAttributes) error,
	errFn func(http.ResponseWriter, *http.Request, int, error),
	multiValueQueryParams bool,
) {
	elem := cr.Elem
	descriptor, secSchemes, globalSecurity := cr.Descriptor, cr.SecSchemes, cr.GlobalSecurity
	impls, middlewareHandlers := cr.Impls, cr.MiddlewareHandlers
	handlerFn := cr.HandlerFn
	reqType := cr.ReqType

	pathNames := elem.Addr().MethodByName("PathParamNames").Call(nil)[0].Interface().([]string)
	pathVars := pathValues(r, pathNames)
	carrier := HTTPCarrier{R: r}
	queryVars := carrier.ExtractQuery()
	headerNames := elem.Addr().MethodByName("HeaderParamNames").Call(nil)[0].Interface().([]string)
	headerVars := rest.NormalizeHeaderVars(carrier.ExtractHeaders(), headerNames)
	cookieVars := carrier.ExtractCookies()

	if multiValueQueryParams {
		if errV := CallErr(elem.Addr(), "ValidateQueryMulti", reflect.ValueOf(carrier.ExtractQueryMulti())); errV != nil {
			rest.ReportQueryErrors(ctx, errV)
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
	} else if errV := CallErr(elem.Addr(), "ValidateQuery", reflect.ValueOf(queryVars)); errV != nil {
		rest.ReportQueryErrors(ctx, errV)
		errFn(sw, r, http.StatusBadRequest, errV)
		return
	}
	if errV := CallErr(elem.Addr(), "ValidateCookies", reflect.ValueOf(cookieVars)); errV != nil {
		rest.ReportCookieErrors(ctx, errV)
		errFn(sw, r, http.StatusBadRequest, errV)
		return
	}
	if errV := CallErr(elem.Addr(), "ValidateHeaders", reflect.ValueOf(headerVars)); errV != nil {
		rest.ReportHeaderErrors(ctx, errV)
		errFn(sw, r, http.StatusBadRequest, errV)
		return
	}
	if len(pathNames) > 0 {
		if errV := CallErr(elem.Addr(), "ValidatePathParams", reflect.ValueOf(pathVars)); errV != nil {
			rest.ReportPathErrors(ctx, errV)
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
	}

	reqPtr := reflect.New(reqType) // zero-value Req — SSE (GET) routes never decode a body

	secReqs := descriptor.Security
	if secReqs == nil {
		secReqs = globalSecurity
	}
	if len(secReqs) > 0 {
		if credErr := rest.ValidateSecurityCredentials(CredentialExtractorFor(r), secReqs, secSchemes); credErr != nil {
			if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
				secObs.RecordSecurityRejection(descriptor.Path, route.FirstSchemeName(secReqs))
			}
			errFn(sw, r, http.StatusUnauthorized, credErr)
			return
		}
	}
	// docs/design/d-0007-declarative-middleware-layering.md's Rollout
	// Phase A: runSecurityMiddleware's separate code path is RETIRED —
	// see DispatchRoute's identical rewrite for the full rationale.
	granted, grantErr := httpsecurity.CollectGrantsReflect(ctx, r, reqPtr, impls, secReqs)
	if grantErr != nil {
		errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: grantErr})
		return
	}

	// Codec-backed middleware dispatch (HandleBoundMW/ClientBoundMW and
	// bundled .Use()) — SAME pre-handler dispatch point plain Route uses.
	// SSE has no ErrorResponseFor (no declared ErrorPattern concept
	// exists for SSE at all today), so a fn error always falls back to
	// rest.MiddlewareError directly (or rest.SecurityError, for a
	// Security-Satisfying handler's own Fn failure).
	middlewareOuts, mwErr := rest.DispatchMiddlewareHandlers(ctx, reqPtr, middlewareHandlers, headerVars, cookieVars, queryVars)
	if mwErr != nil {
		dispatchErr, _ := rest.AsMiddlewareDispatchError(mwErr)
		if !dispatchErr.IsFnError {
			errFn(sw, r, http.StatusBadRequest, dispatchErr.Err)
			return
		}
		if IsSecuritySatisfyingHandler(middlewareHandlers, dispatchErr.Name) {
			errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: dispatchErr.Err})
			return
		}
		errFn(sw, r, http.StatusBadRequest, rest.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
		return
	}
	httpsecurity.MergeMiddlewareHandlerGrants(granted, SatisfiesPerHandler(middlewareHandlers), middlewareOuts)
	if err := rest.CheckScopes(secReqs, granted); err != nil {
		errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
		return
	}
	// Compose every middleware's OWN response header/cookie values
	// (derived from its Out, already produced pre-handler above) —
	// BEFORE SSE's own headers are committed below, per
	// docs/design/d-0003-codec-declared-middlewares.md's "Out-side" SSE
	// migration note.
	for i, mh := range middlewareHandlers {
		mwHeaders, mwCookies, encErr := mh.EncodeOut(ctx, middlewareOuts[i])
		if encErr != nil {
			stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
			errFn(sw, r, http.StatusInternalServerError, encErr)
			return
		}
		for k, v := range mwHeaders {
			responseHeaders.Set(k, v)
		}
		var mwCookieAttrs map[string]rest.CookieAttributes
		if mh.EncodeOutCookieAttrs != nil {
			mwCookieAttrs, encErr = mh.EncodeOutCookieAttrs(middlewareOuts[i])
			if encErr != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
		}
		for k, v := range mwCookies {
			*pendingCookies = append(*pendingCookies, rest.PendingCookie{Name: k, Value: v, Attrs: mwCookieAttrs[k]})
		}
	}

	// SSE headers — must be set before WriteHeader.
	sw.Header().Set("Content-Type", "text/event-stream")
	sw.Header().Set("Cache-Control", "no-cache")
	sw.Header().Set("Connection", "keep-alive")
	sw.Header().Set("X-Accel-Buffering", "no")

	flusher, canFlush := sw.(http.Flusher)

	formatsVal := elem.FieldByName("Formats")
	var encodeEvent func(reflect.Value) ([]byte, error)
	if formatsVal.Len() > 0 {
		// Content negotiation across declared formats — resolved via the
		// format.Format[Event] interface's own reflect-callable Marshal
		// method, since Event is erased here.
		accept := r.Header.Get("Accept")
		chosen, ok := NegotiateFormatReflect(formatsVal, accept)
		if !ok {
			if strings.Contains(accept, "text/event-stream") {
				chosen = formatsVal.Index(0)
			} else {
				var supported []string
				for i := 0; i < formatsVal.Len(); i++ {
					ct := formatsVal.Index(i).MethodByName("ContentType").Call(nil)[0].String()
					supported = append(supported, ct)
				}
				errFn(sw, r, http.StatusNotAcceptable,
					rest.NotAcceptableError{Accept: accept, Supported: supported})
				return
			}
		}
		encodeEvent = func(e reflect.Value) ([]byte, error) {
			results := chosen.MethodByName("Marshal").Call([]reflect.Value{e})
			body, _ := results[0].Interface().([]byte)
			errI := results[1].Interface()
			if errI != nil {
				return nil, errI.(error)
			}
			return body, nil
		}
	} else {
		encodeEventFn := elem.FieldByName("EncodeEvent")
		encodeEvent = func(e reflect.Value) ([]byte, error) {
			results := encodeEventFn.Call([]reflect.Value{e})
			body, _ := results[0].Interface().([]byte)
			errI := results[1].Interface()
			if errI != nil {
				return nil, errI.(error)
			}
			return body, nil
		}
	}
	validateEventFn := elem.FieldByName("ValidateEvent")

	headersCommitted := false
	mergeFields := elem.Addr().MethodByName("MergeFields").Call(nil)[0]
	send := reflect.MakeFunc(handlerFn.Type().In(2), func(args []reflect.Value) []reflect.Value {
		e := args[0]
		errType := reflect.TypeOf((*error)(nil)).Elem()
		retErr := func(err error) []reflect.Value {
			v := reflect.New(errType).Elem()
			if err != nil {
				v.Set(reflect.ValueOf(err))
			}
			return []reflect.Value{v}
		}

		if mergeFields.Len() > 0 {
			mergeResults := elem.Addr().MethodByName("MergeEvent").Call([]reflect.Value{
				e, reflect.ValueOf(pathVars), reflect.ValueOf(queryVars),
				reflect.ValueOf(headerVars), reflect.ValueOf(cookieVars),
			})
			mergedE, mergeErrI := mergeResults[0], mergeResults[1].Interface()
			if mergeErrI != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "response", mergeErrI.(error))
				return retErr(mergeErrI.(error))
			}
			e = mergedE
		}
		if !headersCommitted {
			headersCommitted = true
			// Derive the SSE route's OWN response header/cookie
			// merge-field values (NewRequiredResponseHeaderParam/
			// NewRequiredResponseCookieParam declared directly on the
			// route, not via HandleBoundMW) from the FIRST event sent —
			// mirrors plain Route's EncodeResponseMergeFields dispatch,
			// composing alongside (registration-order, before) the
			// middleware-derived values already gathered above.
			ownMergeResults := elem.Addr().MethodByName("EncodeResponseMergeFields").Call([]reflect.Value{e})
			ownHeaders, _ := ownMergeResults[0].Interface().(map[string]string)
			ownCookies, _ := ownMergeResults[1].Interface().(map[string]string)
			if errI := ownMergeResults[2].Interface(); errI != nil {
				err := errI.(error)
				rest.ReportResponseHeaderErrors(ctx, err)
				rest.ReportResponseCookieErrors(ctx, err)
				return retErr(err)
			}
			for k, v := range ownHeaders {
				responseHeaders.Set(k, v)
			}
			ownCookieAttrsResults := elem.Addr().MethodByName("EncodeResponseCookieAttributes").Call([]reflect.Value{e})
			ownCookieAttrs, _ := ownCookieAttrsResults[0].Interface().(map[string]rest.CookieAttributes)
			for k, v := range ownCookies {
				*pendingCookies = append(*pendingCookies, rest.PendingCookie{Name: k, Value: v, Attrs: ownCookieAttrs[k]})
			}
			respHeaderNames := elem.Addr().MethodByName("ResponseHeaderParamNames").Call(nil)[0].Interface().([]string)
			if errV := CallErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(rest.NormalizeHeaderVars(ResponseHeaderValues(responseHeaders), respHeaderNames))); errV != nil {
				rest.ReportResponseHeaderErrors(ctx, errV)
				return retErr(errV)
			}
			if errV := CallErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(ResponseCookieValues(*pendingCookies))); errV != nil {
				rest.ReportResponseCookieErrors(ctx, errV)
				return retErr(errV)
			}
			for i := range *pendingCookies {
				pc := &(*pendingCookies)[i]
				if err := writeCookie(sw, pc.Name, pc.Value, pc.Attrs); err != nil {
					return retErr(err)
				}
			}
			for key, vals := range responseHeaders {
				for _, v := range vals {
					sw.Header().Add(key, v)
				}
			}
		}
		valResults := validateEventFn.Call([]reflect.Value{e})
		if errI := valResults[0].Interface(); errI != nil {
			err := errI.(error)
			stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response", ConstraintName: stats.ConstraintName(err), Field: "event"})
			return retErr(err)
		}
		data, err := encodeEvent(e)
		if err != nil {
			return retErr(err)
		}
		if _, werr := WriteSSEData(sw, data); werr != nil {
			return retErr(werr)
		}
		if canFlush {
			flusher.Flush()
		}
		return retErr(nil)
	})

	handlerResults := handlerFn.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr.Elem(), send})
	if errI := handlerResults[0].Interface(); errI != nil {
		err, _ := errI.(error)
		// docs/features/rest-redirects.md round 2 finding E: gated on
		// headersCommitted, not sw.StatusCode().
		if !headersCommitted {
			if redirErr, ok := AsRedirectError(err); ok {
				sw.Header().Set("Location", redirErr.Location)
				sw.WriteHeader(redirErr.Status)
				return
			}
		}
		if sw.StatusCode() == http.StatusOK {
			errFn(sw, r, http.StatusInternalServerError, err)
		}
	}
}
