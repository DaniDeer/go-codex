package nethttp

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// ServeSSE walks every [rest.SSERoute] registered into b (via
// [rest.SSERoute.Register]/[rest.SSERoute.RegisterHandle]) and wires each
// handler-bearing one onto mux — the SSE counterpart to [Serve], sharing
// its EXACT failure semantics (Part 1 skip-no-handler, Part 2
// all-or-nothing validation, Part 3 duplicate detection + recover safety
// net) and its reflect-based generic dispatch mechanism (see "Decision:
// Serve's generic dispatch mechanism" in
// docs/design/middleware-workflow-simplification.md) — SSERouteHandle's
// EncodeEvent/ValidateEvent/MergeEvent and the send func(Event) error
// callback shape are all invoked via reflect.Value.Call since Req/Event
// are erased at this call site.
func ServeSSE(mux *http.ServeMux, b *rest.Builder) error {
	entries := b.SSEEntries()

	var routeErrs []RouteError
	var toWire []wiredRoute
	seen := make(map[string]bool, len(entries))

	for _, e := range entries {
		if !e.HasHandler() {
			continue // Part 1: spec-only, never validated, never wired.
		}
		method, path := e.Method(), e.Path()
		if seen[method+" "+path] {
			routeErrs = append(routeErrs, RouteError{Method: method, Path: path,
				Err: DuplicateRouteError{Method: method, Path: path}})
			continue
		}
		seen[method+" "+path] = true

		h, err := buildSSERouteHandler(e.Handle())
		if err != nil {
			routeErrs = append(routeErrs, RouteError{Method: method, Path: path, Err: err})
			continue
		}
		toWire = append(toWire, wiredRoute{method: method, path: path, handler: h})
	}
	if len(routeErrs) > 0 {
		return MultiRouteError{Errors: routeErrs}
	}

	return wireRoutes(mux, toWire)
}

// buildSSERouteHandler is [buildRouteHandler]'s SSE counterpart — see its
// doc comment for the shared reflect-based rationale. handle is a
// *rest.SSERouteHandle[Req, Event] stored as any.
func buildSSERouteHandler(handle any) (http.Handler, error) {
	hv := reflect.ValueOf(handle)
	if hv.Kind() != reflect.Pointer || hv.IsNil() {
		return nil, fmt.Errorf("nethttp.ServeSSE: expected non-nil *rest.SSERouteHandle[Req, Event], got %T", handle)
	}
	elem := hv.Elem()

	descriptor, _ := elem.FieldByName("Descriptor").Interface().(route.Route)
	secSchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]rest.SecurityScheme)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	handlerOptsAny := elem.FieldByName("HandlerOpts").Interface()
	handlerFnVal := elem.FieldByName("HandlerFn")
	if handlerFnVal.IsNil() {
		return nil, fmt.Errorf("nethttp.ServeSSE: route %s %s has no handler (internal error — HasHandler should have skipped it)", descriptor.Method, descriptor.Path)
	}
	handlerFn := handlerFnVal.Elem() // unwrap the `any` field to the concrete func value
	reqType := handlerFn.Type().In(1)
	eventType := handlerFn.Type().In(2).In(0) // send func(Event) error's Event param

	routeLabel := descriptor.Method + " " + descriptor.Path
	if err := validateImplementationShapesReflect(routeLabel, reqType, impls); err != nil {
		return nil, err
	}

	// Coverage check — see [buildRouteHandler]'s identical block for the
	// full rationale (SSE routes need this exactly as much as regular ones).
	coverageReqs := descriptor.Security
	if coverageReqs == nil {
		coverageReqs = globalSecurity
	}
	if err := rest.CheckCoverage(routeLabel, coverageReqs, impls); err != nil {
		return nil, err
	}

	opts, err := resolveOptions(descriptor.Method, descriptor.Path, handlerOptsAny)
	if err != nil {
		return nil, err
	}
	errFn := opts.ErrorHandler
	if errFn == nil {
		errFn = defaultErrorHandler
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		responseHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, responseHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)

		pathNames := elem.Addr().MethodByName("PathParamNames").Call(nil)[0].Interface().([]string)
		pathVars := pathValues(r, pathNames)
		queryVars := queryValues(r)
		headerVars := headerValues(r)
		cookieVars := cookieValues(r)

		if opts.MultiValueQueryParams {
			if errV := callErr(elem.Addr(), "ValidateQueryMulti", reflect.ValueOf(r.URL.Query())); errV != nil {
				reportQueryErrors(ctx, errV)
				errFn(sw, r, http.StatusBadRequest, errV)
				return
			}
		} else if errV := callErr(elem.Addr(), "ValidateQuery", reflect.ValueOf(queryVars)); errV != nil {
			reportQueryErrors(ctx, errV)
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
		if errV := callErr(elem.Addr(), "ValidateCookies", reflect.ValueOf(cookieVars)); errV != nil {
			reportCookieErrors(ctx, errV)
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
		if errV := callErr(elem.Addr(), "ValidateHeaders", reflect.ValueOf(headerVars)); errV != nil {
			reportHeaderErrors(ctx, errV)
			errFn(sw, r, http.StatusBadRequest, errV)
			return
		}
		if len(pathNames) > 0 {
			if errV := callErr(elem.Addr(), "ValidatePathParams", reflect.ValueOf(pathVars)); errV != nil {
				reportPathErrors(ctx, errV)
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
			if credErr := validateSecurityCredentials(r, secReqs, secSchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(descriptor.Path, firstScheme(secReqs))
				}
				errFn(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		if err := runSecurityMiddlewareReflect(ctx, r, reqPtr, impls, secReqs); err != nil {
			errFn(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
			return
		}

		// SSE headers — must be set before WriteHeader.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, canFlush := w.(http.Flusher)

		formatsVal := elem.FieldByName("Formats")
		var encodeEvent func(reflect.Value) ([]byte, error)
		if formatsVal.Len() > 0 {
			// Content negotiation across declared formats — resolved via
			// the format.Format[Event] interface's own reflect-callable
			// Marshal method, since Event is erased here.
			accept := r.Header.Get("Accept")
			chosen, ok := negotiateFormatReflect(formatsVal, accept)
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
					stats.ReportErrors(diagnosticObserver{ctx}, "response", mergeErrI.(error))
					return retErr(mergeErrI.(error))
				}
				e = mergedE
			}
			if !headersCommitted {
				headersCommitted = true
				if errV := callErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(responseHeaderValues(responseHeaders))); errV != nil {
					reportResponseHeaderErrors(ctx, errV)
					return retErr(errV)
				}
				if errV := callErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(responseCookieValues(pendingCookies))); errV != nil {
					reportResponseCookieErrors(ctx, errV)
					return retErr(errV)
				}
				for i := range pendingCookies {
					pc := &pendingCookies[i]
					writeOpts := pc.Opts
					writeOpts.Codec = nil
					if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
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
			if _, werr := fmt.Fprintf(sw, "data: %s\n\n", data); werr != nil {
				return retErr(werr)
			}
			if canFlush {
				flusher.Flush()
			}
			return retErr(nil)
		})

		handlerResults := handlerFn.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr.Elem(), send})
		if errI := handlerResults[0].Interface(); errI != nil {
			if sw.code == http.StatusOK {
				errFn(sw, r, http.StatusInternalServerError, errI.(error))
			}
		}
		_ = eventType
	})

	return applyGeneralMiddleware(inner, impls), nil
}

// negotiateFormatReflect is [negotiateFormat]'s reflect-based equivalent —
// formatsVal is a reflect.Value wrapping []format.Format[Event] (Event
// erased). Returns the chosen format.Format[Event] value (as
// reflect.Value) and whether a match was found.
func negotiateFormatReflect(formatsVal reflect.Value, accept string) (reflect.Value, bool) {
	if accept == "" || accept == "*/*" {
		return formatsVal.Index(0), true
	}
	for _, part := range strings.Split(accept, ",") {
		want := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if want == "*/*" {
			return formatsVal.Index(0), true
		}
		for i := 0; i < formatsVal.Len(); i++ {
			f := formatsVal.Index(i)
			ct := f.MethodByName("ContentType").Call(nil)[0].String()
			ct, _, _ = strings.Cut(ct, ";")
			if strings.TrimSpace(ct) == want {
				return f, true
			}
		}
	}
	return reflect.Value{}, false
}
