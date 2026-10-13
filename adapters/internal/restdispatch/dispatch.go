package restdispatch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/internal/httpsecurity"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// DispatchRoute runs the full decode/validate/security/middleware/
// handle/encode per-request pipeline for one route, built from cr (the
// result of a prior, successful [ValidateRouteConstruction] call) — the
// shared, byte-for-byte-identical body PREVIOUSLY independently
// duplicated in adapters/nethttp's and adapters/chi's own
// buildRouteHandler (confirmed via direct diff during Phase 4's REST
// family dispatch-core consolidation — see
// docs/roadmap/adapter-integration-architecture.md's "REST family
// dispatch-core consolidation design").
//
// ctx is the caller's ALREADY-FULLY-WRAPPED request context — this
// function never sets or reads back its own context keys. Each adapter
// wraps r.Context() with [middleware.EnsureContextFields] and its OWN
// package-private context-key sentinels (backing its own public
// RequestFromContext/ResponseHeadersFromContext/WithResponseCookies
// functions) BEFORE calling DispatchRoute — those sentinel types
// genuinely cannot be shared here without breaking each package's own
// public accessors (confirmed during this consolidation's investigation:
// this function itself never reads them back via ctx.Value, only the
// caller's wrapped handler code does, through the per-package public
// accessors).
//
// respHeaders/pendingCookies are the SAME maps/slices the caller already
// deposited into ctx via its own keys — passed here explicitly as plain
// values (not re-derived from ctx) since this function mutates them
// directly throughout, exactly like the pre-extraction code did.
//
// pathValues is the one genuine per-adapter dependency
// (r.PathValue vs. chi.URLParam(r, name)) — injected rather than shared.
func DispatchRoute(
	ctx context.Context,
	cr ConstructedRoute,
	sw ResponseWriter,
	r *http.Request,
	respHeaders http.Header,
	pendingCookies *[]rest.PendingCookie,
	pathValues func(*http.Request, []string) map[string]string,
	writeCookie func(w http.ResponseWriter, name, value string, attrs rest.CookieAttributes) error,
	errFn func(http.ResponseWriter, *http.Request, int, error),
	maxBody int64,
	expectedCT string,
	multiValueQueryParams bool,
) {
	elem := cr.Elem
	descriptor, secSchemes, globalSecurity := cr.Descriptor, cr.SecSchemes, cr.GlobalSecurity
	impls, middlewareHandlers := cr.Impls, cr.MiddlewareHandlers
	handlerFn := cr.HandlerFn
	reqType := cr.ReqType
	obs := stats.ObserverFromContext(ctx)

	var body []byte
	// reqFormatValue/haveReqFormatValue: when the route declares
	// RequestFormats, the body is decoded HERE via the negotiated format
	// (not DecodeMerged's plain Decode below) — reqFormatValue carries
	// that already-decoded Req value across to the DecodeMerged
	// replacement further down.
	var reqFormatValue reflect.Value
	haveReqFormatValue := false
	reqFormats := elem.FieldByName("RequestFormats")
	if descriptor.RequestBody != nil {
		ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
		ct = strings.TrimSpace(ct)

		if reqFormats.Len() > 0 {
			chosen, ok := negotiateRequestFormatReflect(reqFormats, ct)
			if !ok {
				supported := formatContentTypesReflect(reqFormats)
				errFn(sw, r, http.StatusUnsupportedMediaType,
					rest.UnsupportedMediaTypeError{Got: ct, Supported: supported})
				return
			}
			r.Body = http.MaxBytesReader(sw, r.Body, maxBody)
			b, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				var mbe *http.MaxBytesError
				if errors.As(readErr, &mbe) {
					errFn(sw, r, http.StatusRequestEntityTooLarge, rest.BodyTooLargeError{Limit: maxBody})
				} else {
					errFn(sw, r, http.StatusBadRequest, readErr)
				}
				return
			}
			body = b
			unmarshalResults := chosen.MethodByName("Unmarshal").Call([]reflect.Value{reflect.ValueOf(body)})
			if err, _ := unmarshalResults[1].Interface().(error); err != nil {
				rest.ReportBodyErrors(ctx, err)
				if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
					return
				}
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
			reqFormatValue = unmarshalResults[0]
			haveReqFormatValue = true
		} else {
			if ct != expectedCT {
				errFn(sw, r, http.StatusUnsupportedMediaType,
					rest.UnsupportedMediaTypeError{Got: ct, Supported: []string{expectedCT}})
				return
			}
			r.Body = http.MaxBytesReader(sw, r.Body, maxBody)
			b, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				var mbe *http.MaxBytesError
				if errors.As(readErr, &mbe) {
					errFn(sw, r, http.StatusRequestEntityTooLarge, rest.BodyTooLargeError{Limit: maxBody})
				} else {
					errFn(sw, r, http.StatusBadRequest, readErr)
				}
				return
			}
			body = b
		}
	}

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
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &errV) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
	} else if errV := CallErr(elem.Addr(), "ValidateQuery", reflect.ValueOf(queryVars)); errV != nil {
		rest.ReportQueryErrors(ctx, errV)
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &errV) {
			return
		}
		errFn(sw, r, http.StatusBadRequest, errV)
		return
	}
	if errV := CallErr(elem.Addr(), "ValidateCookies", reflect.ValueOf(cookieVars)); errV != nil {
		rest.ReportCookieErrors(ctx, errV)
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &errV) {
			return
		}
		errFn(sw, r, http.StatusBadRequest, errV)
		return
	}
	if errV := CallErr(elem.Addr(), "ValidateHeaders", reflect.ValueOf(headerVars)); errV != nil {
		rest.ReportHeaderErrors(ctx, errV)
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &errV) {
			return
		}
		errFn(sw, r, http.StatusBadRequest, errV)
		return
	}
	if len(pathNames) > 0 {
		if errV := CallErr(elem.Addr(), "ValidatePathParams", reflect.ValueOf(pathVars)); errV != nil {
			rest.ReportPathErrors(ctx, errV)
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &errV) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
	}

	// reqPtr: an addressable *Req so security impl Fns (which take *Req)
	// and the eventual HandlerFn call can share the SAME value.
	reqPtr := reflect.New(reqType)
	if haveReqFormatValue {
		// Body already decoded above via the negotiated RequestFormats
		// entry — only the var-merge half of DecodeMerged remains.
		reqPtr.Elem().Set(reqFormatValue)
		applyResults := elem.Addr().MethodByName("ApplyMergeFields").Call([]reflect.Value{
			reqPtr, reflect.ValueOf(pathVars), reflect.ValueOf(queryVars),
			reflect.ValueOf(headerVars), reflect.ValueOf(cookieVars),
		})
		if err, _ := applyResults[0].Interface().(error); err != nil {
			rest.ReportBodyErrors(ctx, err)
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}
	} else {
		// DecodeMerged(body, pathVars, query, headers, cookies) (Req, error)
		decodeResults := elem.Addr().MethodByName("DecodeMerged").Call([]reflect.Value{
			reflect.ValueOf(body), reflect.ValueOf(pathVars), reflect.ValueOf(queryVars),
			reflect.ValueOf(headerVars), reflect.ValueOf(cookieVars),
		})
		reqValue, decErr := decodeResults[0], decodeResults[1]
		if err, _ := decErr.Interface().(error); err != nil {
			rest.ReportBodyErrors(ctx, err)
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}
		reqPtr.Elem().Set(reqValue)
	}

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
	// Security now dispatches through the SAME unified MiddlewareHandler
	// mechanism every other middleware uses. CollectGrantsReflect runs
	// the legacy security-shaped impls WITHOUT calling rest.CheckScopes
	// yet (that now happens ONCE, below, after merging in any
	// GrantedScopes a MiddlewareHandler-dispatched Security middleware
	// contributes — its Out only becomes available once
	// DispatchMiddlewareHandlers runs).
	granted, grantErr := httpsecurity.CollectGrantsReflect(ctx, r, reqPtr, impls, secReqs)
	if grantErr != nil {
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &grantErr) {
			return
		}
		errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: grantErr})
		return
	}

	middlewareOuts, mwErr := rest.DispatchMiddlewareHandlers(ctx, reqPtr, middlewareHandlers, headerVars, cookieVars, queryVars)
	if mwErr != nil {
		dispatchErr, _ := rest.AsMiddlewareDispatchError(mwErr)
		if !dispatchErr.IsFnError {
			// Middleware DecodeIn failure IS ErrorPattern-eligible now
			// (Topic 1's Category A fix) — already wrapped in
			// MiddlewareInputError by rest.DispatchMiddlewareHandlers,
			// but previously never passed to ErrorResponseFor.
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &dispatchErr.Err) {
				return
			}
			errFn(sw, r, http.StatusBadRequest, dispatchErr.Err)
			return
		}
		// A FAILING handler whose OWN Satisfies is non-empty (i.e. a
		// Security-paired MiddlewareHandler, confirmed via Name lookup)
		// keeps Security's own, DISTINCT fallback (401,
		// rest.SecurityError) — mirrors runSecurityMiddleware's prior,
		// established behavior exactly, even though both now dispatch
		// through the SAME mechanism. An ordinary (non-Security) Fn
		// error keeps the generic fallback (400, rest.MiddlewareError).
		err := dispatchErr.Err
		if IsSecuritySatisfyingHandler(middlewareHandlers, dispatchErr.Name) {
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
			return
		}
		// fn's own business error IS ErrorPattern-eligible (D2) — run
		// through the SAME ObserveErrorResponseFor mechanism a handler
		// error uses, falling back to rest.MiddlewareError at status 400
		// when unmatched.
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
			return
		}
		errFn(sw, r, http.StatusBadRequest, rest.MiddlewareError{Name: dispatchErr.Name, Err: err})
		return
	}
	httpsecurity.MergeMiddlewareHandlerGrants(granted, SatisfiesPerHandler(middlewareHandlers), middlewareOuts)
	if err := rest.CheckScopes(secReqs, granted); err != nil {
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
			return
		}
		errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
		return
	}

	handlerResults := handlerFn.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr.Elem()})
	respValue, handlerErrV := handlerResults[0], handlerResults[1]
	if err, _ := handlerErrV.Interface().(error); err != nil {
		// docs/features/rest-redirects.md: a RedirectError (constructed
		// via rest.Redirect/rest.RedirectToSSE) is recognized BEFORE
		// ErrorPattern/ErrorStatus matching — renders status + Location,
		// no body. This is the reflect-based dispatch path
		// buildRouteHandler uses for every Register()-ed route; mirrors
		// handlerFunc's identical, non-reflect check in each package's
		// own adapter.go.
		if redirErr, ok := AsRedirectError(err); ok {
			sw.Header().Set("Location", redirErr.Location)
			sw.WriteHeader(redirErr.Status)
			return
		}
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
			return
		}
		status := http.StatusInternalServerError
		if mappedStatus, ok := CallErrStatusFor(elem.Addr(), err); ok {
			status = mappedStatus
		}
		errFn(sw, r, status, err)
		return
	}

	// Derive response header/cookie merge-field values BEFORE deciding
	// how to encode the body — mirrors handlerFunc's ordering, applies
	// regardless of whether Formats negotiation runs below.
	mergeResults := elem.Addr().MethodByName("EncodeResponseMergeFields").Call([]reflect.Value{respValue})
	mergedHeaders, _ := mergeResults[0].Interface().(map[string]string)
	mergedCookies, _ := mergeResults[1].Interface().(map[string]string)
	if err, _ := mergeResults[2].Interface().(error); err != nil {
		rest.ReportResponseHeaderErrors(ctx, err)
		rest.ReportResponseCookieErrors(ctx, err)
		if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
			return
		}
		errFn(sw, r, http.StatusInternalServerError, err)
		return
	}
	for k, v := range mergedHeaders {
		respHeaders.Set(k, v)
	}
	cookieAttrsResults := elem.Addr().MethodByName("EncodeResponseCookieAttributes").Call([]reflect.Value{respValue})
	cookieAttrs, _ := cookieAttrsResults[0].Interface().(map[string]rest.CookieAttributes)
	for k, v := range mergedCookies {
		*pendingCookies = append(*pendingCookies, rest.PendingCookie{Name: k, Value: v, Attrs: cookieAttrs[k]})
	}

	// Compose every middleware's OWN response header/cookie values
	// (derived from its Out, already produced pre-handler above) —
	// registration-order, last-applied-wins alongside the route's own
	// values just merged above.
	for i, h := range middlewareHandlers {
		mwHeaders, mwCookies, encErr := h.EncodeOut(ctx, middlewareOuts[i])
		if encErr != nil {
			stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &encErr) {
				return
			}
			errFn(sw, r, http.StatusInternalServerError, encErr)
			return
		}
		for k, v := range mwHeaders {
			respHeaders.Set(k, v)
		}
		var mwCookieAttrs map[string]rest.CookieAttributes
		if h.EncodeOutCookieAttrs != nil {
			mwCookieAttrs, encErr = h.EncodeOutCookieAttrs(middlewareOuts[i])
			if encErr != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
				if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &encErr) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
		}
		for k, v := range mwCookies {
			*pendingCookies = append(*pendingCookies, rest.PendingCookie{Name: k, Value: v, Attrs: mwCookieAttrs[k]})
		}
	}

	var outBytes []byte
	respCT := "application/json"
	respFormats := elem.FieldByName("Formats")
	if respFormats.Len() > 0 {
		chosen, ok := NegotiateFormatReflect(respFormats, r.Header.Get("Accept"))
		if !ok {
			supported := formatContentTypesReflect(respFormats)
			errFn(sw, r, http.StatusNotAcceptable,
				rest.NotAcceptableError{Accept: r.Header.Get("Accept"), Supported: supported})
			return
		}
		if chosen.MethodByName("IsStreamable").Call(nil)[0].Bool() {
			// Pre-validate before committing response headers so we can
			// still return an error response if the value violates
			// codec constraints.
			valResults := chosen.MethodByName("Validate").Call([]reflect.Value{respValue})
			if err, _ := valResults[0].Interface().(error); err != nil {
				rest.ReportBodyErrors(ctx, err)
				if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
					return
				}
				errFn(sw, r, http.StatusInternalServerError, err)
				return
			}
			respCT = chosen.MethodByName("ContentType").Call(nil)[0].String()

			respHeaderNames := elem.Addr().MethodByName("ResponseHeaderParamNames").Call(nil)[0].Interface().([]string)
			if errV := CallErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(rest.NormalizeHeaderVars(ResponseHeaderValues(respHeaders), respHeaderNames))); errV != nil {
				rest.ReportResponseHeaderErrors(ctx, errV)
				errFn(sw, r, http.StatusInternalServerError, errV)
				return
			}
			if errV := CallErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(ResponseCookieValues(*pendingCookies))); errV != nil {
				rest.ReportResponseCookieErrors(ctx, errV)
				errFn(sw, r, http.StatusInternalServerError, errV)
				return
			}
			for key, vals := range respHeaders {
				for _, v := range vals {
					sw.Header().Add(key, v)
				}
			}
			for i := range *pendingCookies {
				pc := &(*pendingCookies)[i]
				if err := writeCookie(sw, pc.Name, pc.Value, pc.Attrs); err != nil {
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
			}
			sw.Header().Set("Content-Type", respCT)
			sw.WriteHeader(PrimaryStatusFor(descriptor))
			// MarshalTo re-validates then streams. Headers are
			// committed at this point; streaming errors are logged but
			// cannot be returned as HTTP error responses.
			streamResults := chosen.MethodByName("MarshalTo").Call([]reflect.Value{respValue, reflect.ValueOf(sw)})
			if err, _ := streamResults[0].Interface().(error); err != nil {
				rest.ReportBodyErrors(ctx, err)
			}
			return
		}
		marshalResults := chosen.MethodByName("Marshal").Call([]reflect.Value{respValue})
		outBytes, _ = marshalResults[0].Interface().([]byte)
		if err, _ := marshalResults[1].Interface().(error); err != nil {
			rest.ReportBodyErrors(ctx, err)
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}
		respCT = chosen.MethodByName("ContentType").Call(nil)[0].String()
	} else {
		encodeResults := elem.FieldByName("Encode").Call([]reflect.Value{respValue})
		outBytes, _ = encodeResults[0].Interface().([]byte)
		if err, _ := encodeResults[1].Interface().(error); err != nil {
			rest.ReportBodyErrors(ctx, err)
			if tryRespondErrorPattern(ctx, sw, elem, obs, respHeaders, pendingCookies, &err) {
				return
			}
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}
	}

	respHeaderNames := elem.Addr().MethodByName("ResponseHeaderParamNames").Call(nil)[0].Interface().([]string)
	if errV := CallErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(rest.NormalizeHeaderVars(ResponseHeaderValues(respHeaders), respHeaderNames))); errV != nil {
		rest.ReportResponseHeaderErrors(ctx, errV)
		errFn(sw, r, http.StatusInternalServerError, errV)
		return
	}
	if errV := CallErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(ResponseCookieValues(*pendingCookies))); errV != nil {
		rest.ReportResponseCookieErrors(ctx, errV)
		errFn(sw, r, http.StatusInternalServerError, errV)
		return
	}

	for key, vals := range respHeaders {
		for _, v := range vals {
			sw.Header().Add(key, v)
		}
	}
	for i := range *pendingCookies {
		pc := &(*pendingCookies)[i]
		if err := writeCookie(sw, pc.Name, pc.Value, pc.Attrs); err != nil {
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}
	}
	sw.Header().Set("Content-Type", respCT)
	sw.WriteHeader(PrimaryStatusFor(descriptor))
	_, _ = sw.Write(outBytes)
}
