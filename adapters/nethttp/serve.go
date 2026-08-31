package nethttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// RouteError pairs a route's identity with what went wrong on it — one
// entry of a [MultiRouteError].
type RouteError struct {
	Method string
	Path   string
	Err    error
}

func (e RouteError) Error() string {
	return fmt.Sprintf("%s %s: %v", e.Method, e.Path, e.Err)
}

func (e RouteError) Unwrap() error { return e.Err }

// MultiRouteError is returned by [Serve]/[ServeSSE] when one or more
// handler-bearing routes fail validation. Carries EVERY individual
// failure found during the pre-wiring validation pass — not just the
// first — so a caller sees the complete list of what's wrong in one
// error. Unwrap() []error (Go 1.20+ multi-error support) lets
// errors.As/Is reach into ANY individual route's error directly.
type MultiRouteError struct {
	Errors []RouteError
}

func (e MultiRouteError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d route(s) failed validation:", len(e.Errors))
	for _, re := range e.Errors {
		fmt.Fprintf(&b, "\n  - %s", re.Error())
	}
	return b.String()
}

func (e MultiRouteError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, re := range e.Errors {
		out[i] = re
	}
	return out
}

// DuplicateRouteError is returned (wrapped in a [RouteError] inside a
// [MultiRouteError]) when two handler-bearing routes declare the SAME
// Method+Path — a *http.ServeMux.Handle panic waiting to happen.
type DuplicateRouteError struct {
	Method string
	Path   string
}

func (e DuplicateRouteError) Error() string {
	return fmt.Sprintf("duplicate route: %s %s already registered", e.Method, e.Path)
}

// OptionsShapeError is returned when a [rest.Route.WithOptions]/
// [rest.SSERoute.WithOptions] value's concrete type is not [Options] —
// mirrors [rest.FormatOptError]'s pattern.
type OptionsShapeError struct {
	Method, Path string
	Got          any
}

func (e OptionsShapeError) Error() string {
	return fmt.Sprintf("nethttp: %s %s: WithOptions value has wrong type: want nethttp.Options, got %T", e.Method, e.Path, e.Got)
}

// resolveOptions type-asserts a route's type-erased HandlerOpts (from
// [rest.Route.WithOptions]) to [Options]. nil means WithOptions was never
// called — the adapter's zero-value Options apply.
func resolveOptions(method, path string, handlerOpts any) (Options, error) {
	if handlerOpts == nil {
		return Options{}, nil
	}
	opts, ok := handlerOpts.(Options)
	if !ok {
		return Options{}, OptionsShapeError{Method: method, Path: path, Got: handlerOpts}
	}
	return opts, nil
}

// wiredRoute pairs a built http.Handler with its Method+Path, staged for
// the final wiring loop after ALL routes have passed validation.
type wiredRoute struct {
	method, path string
	handler      http.Handler
}

// Serve walks every [rest.Route] registered into b (via [rest.Route.Register]/
// [rest.Route.RegisterHandle]) and wires each handler-bearing one onto mux
// — the SOLE public server-side entry point for regular (non-SSE) routes
// (see docs/design/middleware-workflow-simplification.md's "Decision:
// Serve is the only public server-side entry point").
//
// Part 1 — routes with NO [rest.Route.WithHandler] call are spec-only and
// SKIPPED entirely: no validation, no wiring, no error (see "Decision:
// Serve's whole-builder failure semantics").
//
// Part 2 — every handler-bearing route is validated FIRST (implementation
// shape + security coverage), collecting ALL failures into ONE
// [MultiRouteError] — Serve wires NOTHING (zero mux.Handle calls) if even
// one fails.
//
// Part 3 — duplicate Method+Path is detected proactively (folded into the
// SAME validation pass, surfacing as [DuplicateRouteError]); a defensive
// recover() around the wiring loop converts any other unanticipated
// mux.Handle panic into a returned error.
//
// Generic dispatch: Serve is non-generic and walks a HETEROGENEOUS
// collection of routes with different Req/Resp per route — building each
// one's http.Handler uses reflect.Value.Call against the route's
// ALREADY-CONCRETE exported closures (Decode/Encode/HandlerFn/each
// [middleware.ServerImplementation.Fn]), never reflective generic
// instantiation (which Go does not support) — see "Decision: Serve's
// generic dispatch mechanism" in the roadmap doc for the full rationale.
func Serve(mux *http.ServeMux, b *rest.Builder) error {
	entries := b.RouteEntries()

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

		h, err := buildRouteHandler(e.Handle())
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

func wireRoutes(mux *http.ServeMux, toWire []wiredRoute) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("nethttp.Serve: panic wiring routes: %v", r)
		}
	}()
	for _, rt := range toWire {
		mux.Handle(rt.method+" "+rt.path, rt.handler)
	}
	return nil
}

// ServeOne builds a bare [http.Handler] for exactly ONE route — pure
// sugar, implemented as "build a scratch single-route Builder, register
// route into it, call Serve, return the resulting mux." Reuses Serve's
// exact validation path; not a bypass. Takes no [Options] parameter —
// call route.WithOptions(opts) first for a custom Options on this route.
func ServeOne[Req, Resp any](r rest.Route[Req, Resp]) (http.Handler, error) {
	b := rest.NewBuilder(rest.Info{})
	if err := r.Register(b); err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	if err := Serve(mux, b); err != nil {
		return nil, err
	}
	return mux, nil
}

// buildRouteHandler validates handle's attached implementation shapes
// EAGERLY (returning an error immediately for a malformed Fn, never
// silently) and returns the [http.Handler] that runs the full
// decode/validate/security/handle/encode pipeline per request — the
// SAME pipeline `handlerFunc` runs, invoked via reflect.Value.Call since
// Req/Resp are erased at this call site. handle is a *rest.RouteHandle[Req, Resp]
// stored as any.
func buildRouteHandler(handle any) (http.Handler, error) {
	hv := reflect.ValueOf(handle)
	if hv.Kind() != reflect.Pointer || hv.IsNil() {
		return nil, fmt.Errorf("nethttp.Serve: expected non-nil *rest.RouteHandle[Req, Resp], got %T", handle)
	}
	elem := hv.Elem()

	descriptor, _ := elem.FieldByName("Descriptor").Interface().(route.Route)
	secSchemes, _ := elem.FieldByName("SecuritySchemes").Interface().(map[string]rest.SecurityScheme)
	globalSecurity, _ := elem.FieldByName("GlobalSecurity").Interface().([]route.SecurityRequirement)
	impls, _ := elem.FieldByName("Implementations").Interface().([]middleware.ServerImplementation)
	handlerOptsAny := elem.FieldByName("HandlerOpts").Interface()
	handlerFnVal := elem.FieldByName("HandlerFn")
	if handlerFnVal.IsNil() {
		return nil, fmt.Errorf("nethttp.Serve: route %s %s has no handler (internal error — HasHandler should have skipped it)", descriptor.Method, descriptor.Path)
	}
	handlerFn := handlerFnVal.Elem() // unwrap the `any` field to the concrete func value
	reqType := handlerFn.Type().In(1)
	respType := handlerFn.Type().Out(0)

	routeLabel := descriptor.Method + " " + descriptor.Path
	if err := validateImplementationShapesReflect(routeLabel, reqType, impls); err != nil {
		return nil, err
	}

	// Coverage check: every scheme named in descriptor.Security (falling back
	// to GlobalSecurity, same resolution rule the runtime path uses) must
	// have a matching impls[i].Satisfies entry — catches a route that
	// declares a security requirement via .Use() but never attaches a
	// matching .HandleMW() implementation, EAGERLY at Serve time instead of
	// letting every request fail closed at runtime with no clear signal why.
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
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = maxRequestBodyBytes
	}
	expectedCT := opts.ContentType
	if expectedCT == "" {
		expectedCT = "application/json"
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		respHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, respHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)

		var body []byte
		// reqFormatValue/haveReqFormatValue: when the route declares
		// RequestFormats, the body is decoded HERE via the negotiated
		// format (not DecodeMerged's plain Decode below) — reqFormatValue
		// carries that already-decoded Req value across to the
		// DecodeMerged replacement further down.
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
					reportBodyErrors(ctx, err)
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

		// reqPtr: an addressable *Req so security impl Fns (which take
		// *Req) and the eventual HandlerFn call can share the SAME value.
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
				reportBodyErrors(ctx, err)
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
				reportBodyErrors(ctx, err)
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

		handlerResults := handlerFn.Call([]reflect.Value{reflect.ValueOf(ctx), reqPtr.Elem()})
		respValue, handlerErrV := handlerResults[0], handlerResults[1]
		if err, _ := handlerErrV.Interface().(error); err != nil {
			errResults := elem.Addr().MethodByName("ErrorResponseFor").Call([]reflect.Value{reflect.ValueOf(&err).Elem()})
			patternResp, _ := errResults[0].Interface().(rest.ErrorPatternResponse)
			matched, _ := errResults[1].Interface().(bool)
			applyErr, _ := errResults[2].Interface().(error)
			if matched {
				if applyErr == nil {
					if patternResp.Action == "" || patternResp.Action == rest.ErrorRespond {
						if writeErr := writeErrorPatternResponseReflect(ctx, sw, elem, respType, patternResp, respHeaders, &pendingCookies); writeErr == nil {
							return
						} else {
							err = writeErr
						}
					}
				} else {
					err = applyErr
				}
			}
			status := http.StatusInternalServerError
			if mappedStatus, ok := callErrStatusFor(elem.Addr(), err); ok {
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
			reportResponseHeaderErrors(ctx, err)
			reportResponseCookieErrors(ctx, err)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}
		for k, v := range mergedHeaders {
			respHeaders.Set(k, v)
		}
		for k, v := range mergedCookies {
			pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v})
		}

		var outBytes []byte
		respCT := "application/json"
		respFormats := elem.FieldByName("Formats")
		if respFormats.Len() > 0 {
			chosen, ok := negotiateFormatReflect(respFormats, r.Header.Get("Accept"))
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
					reportBodyErrors(ctx, err)
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
				respCT = chosen.MethodByName("ContentType").Call(nil)[0].String()

				if errV := callErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(responseHeaderValues(respHeaders))); errV != nil {
					reportResponseHeaderErrors(ctx, errV)
					errFn(sw, r, http.StatusInternalServerError, errV)
					return
				}
				if errV := callErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(responseCookieValues(pendingCookies))); errV != nil {
					reportResponseCookieErrors(ctx, errV)
					errFn(sw, r, http.StatusInternalServerError, errV)
					return
				}
				for key, vals := range respHeaders {
					for _, v := range vals {
						sw.Header().Add(key, v)
					}
				}
				for i := range pendingCookies {
					pc := &pendingCookies[i]
					writeOpts := pc.Opts
					writeOpts.Codec = nil
					if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
						errFn(sw, r, http.StatusInternalServerError, err)
						return
					}
				}
				sw.Header().Set("Content-Type", respCT)
				sw.WriteHeader(primaryStatusFor(descriptor))
				// MarshalTo re-validates then streams. Headers are
				// committed at this point; streaming errors are logged
				// but cannot be returned as HTTP error responses.
				streamResults := chosen.MethodByName("MarshalTo").Call([]reflect.Value{respValue, reflect.ValueOf(sw)})
				if err, _ := streamResults[0].Interface().(error); err != nil {
					reportBodyErrors(ctx, err)
				}
				return
			}
			marshalResults := chosen.MethodByName("Marshal").Call([]reflect.Value{respValue})
			outBytes, _ = marshalResults[0].Interface().([]byte)
			if err, _ := marshalResults[1].Interface().(error); err != nil {
				reportBodyErrors(ctx, err)
				errFn(sw, r, http.StatusInternalServerError, err)
				return
			}
			respCT = chosen.MethodByName("ContentType").Call(nil)[0].String()
		} else {
			encodeResults := elem.FieldByName("Encode").Call([]reflect.Value{respValue})
			outBytes, _ = encodeResults[0].Interface().([]byte)
			if err, _ := encodeResults[1].Interface().(error); err != nil {
				reportBodyErrors(ctx, err)
				errFn(sw, r, http.StatusInternalServerError, err)
				return
			}
		}

		if errV := callErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(responseHeaderValues(respHeaders))); errV != nil {
			reportResponseHeaderErrors(ctx, errV)
			errFn(sw, r, http.StatusInternalServerError, errV)
			return
		}
		if errV := callErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(responseCookieValues(pendingCookies))); errV != nil {
			reportResponseCookieErrors(ctx, errV)
			errFn(sw, r, http.StatusInternalServerError, errV)
			return
		}

		for key, vals := range respHeaders {
			for _, v := range vals {
				sw.Header().Add(key, v)
			}
		}
		for i := range pendingCookies {
			pc := &pendingCookies[i]
			writeOpts := pc.Opts
			writeOpts.Codec = nil
			if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
				errFn(sw, r, http.StatusInternalServerError, err)
				return
			}
		}
		sw.Header().Set("Content-Type", respCT)
		sw.WriteHeader(primaryStatusFor(descriptor))
		_, _ = sw.Write(outBytes)
	})

	return applyGeneralMiddleware(inner, impls), nil
}

// callErr reflect-calls method(arg) on target (a *rest.RouteHandle[Req,Resp]
// reflect.Value) and returns its single error result (nil on success).
// Used for the family of RouteHandle methods whose signature never names
// Req/Resp (map[string]string -> error, etc.) — reflect is only needed
// here because target's static type is unknown to Serve, not because the
// METHOD itself is generic.
func callErr(target reflect.Value, method string, arg reflect.Value) error {
	results := target.MethodByName(method).Call([]reflect.Value{arg})
	err, _ := results[0].Interface().(error)
	return err
}

// callErrStatusFor reflect-calls ErrorStatusFor(err) on target.
func callErrStatusFor(target reflect.Value, err error) (int, bool) {
	results := target.MethodByName("ErrorStatusFor").Call([]reflect.Value{reflect.ValueOf(&err).Elem()})
	status, _ := results[0].Interface().(int)
	ok, _ := results[1].Interface().(bool)
	return status, ok
}

// formatContentTypesReflect returns the ContentType() of every entry in
// formats (a reflect.Value of type []format.Format[T] for an erased T) —
// used to build the Supported list on [rest.UnsupportedMediaTypeError]/
// [rest.NotAcceptableError].
func formatContentTypesReflect(formats reflect.Value) []string {
	n := formats.Len()
	if n == 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		if ct := formats.Index(i).MethodByName("ContentType").Call(nil)[0].String(); ct != "" {
			out = append(out, ct)
		}
	}
	return out
}

// negotiateFormatReflect (Resp-direction) is defined in serve_sse.go —
// shared with [Serve]/SSE's own reflect dispatch since both need to pick a
// format.Format[T] (T erased) by Accept header.

// negotiateRequestFormatReflect is [negotiateRequestFormat]'s reflect-based
// equivalent — formats is a reflect.Value of type []format.Format[Req]
// (Req erased at [Serve]'s call site). Picks the format whose ContentType
// exactly matches contentType (parameters stripped).
func negotiateRequestFormatReflect(formats reflect.Value, contentType string) (reflect.Value, bool) {
	n := formats.Len()
	for i := 0; i < n; i++ {
		f := formats.Index(i)
		fmtMediaType, _, _ := strings.Cut(f.MethodByName("ContentType").Call(nil)[0].String(), ";")
		if strings.TrimSpace(fmtMediaType) == contentType {
			return f, true
		}
	}
	return reflect.Value{}, false
}

// writeErrorPatternResponseReflect is [writeErrorPatternResponse]'s
// reflect-based equivalent — elem is the *rest.RouteHandle[Req, Resp]
// reflect.Value (Resp erased at [Serve]'s call site) and respType is
// Resp's reflect.Type (recovered from the route's HandlerFn — see
// [buildRouteHandler]). pattern.Value's response header/cookie
// merge-field values are applied ONLY when its concrete type equals Resp
// — same parity rule as [writeErrorPatternResponse].
func writeErrorPatternResponseReflect(
	ctx context.Context,
	w http.ResponseWriter,
	elem reflect.Value,
	respType reflect.Type,
	pattern rest.ErrorPatternResponse,
	respHeaders http.Header,
	pendingCookies *[]PendingCookie,
) error {
	if pattern.Value != nil && reflect.TypeOf(pattern.Value) == respType {
		respVal := reflect.ValueOf(pattern.Value)
		mergeResults := elem.Addr().MethodByName("EncodeResponseMergeFields").Call([]reflect.Value{respVal})
		headerValues, _ := mergeResults[0].Interface().(map[string]string)
		cookieValues, _ := mergeResults[1].Interface().(map[string]string)
		if err, _ := mergeResults[2].Interface().(error); err != nil {
			reportResponseHeaderErrors(ctx, err)
			reportResponseCookieErrors(ctx, err)
			return err
		}
		for k, v := range headerValues {
			respHeaders.Set(k, v)
		}
		for k, v := range cookieValues {
			*pendingCookies = append(*pendingCookies, PendingCookie{Name: k, Value: v})
		}
	}

	if err := callErr(elem.Addr(), "ValidateResponseHeaders", reflect.ValueOf(responseHeaderValues(respHeaders))); err != nil {
		reportResponseHeaderErrors(ctx, err)
		return err
	}
	if err := callErr(elem.Addr(), "ValidateResponseCookies", reflect.ValueOf(responseCookieValues(*pendingCookies))); err != nil {
		reportResponseCookieErrors(ctx, err)
		return err
	}

	for key, vals := range respHeaders {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	for i := range *pendingCookies {
		pc := &(*pendingCookies)[i]
		writeOpts := pc.Opts
		writeOpts.Codec = nil
		if err := SetCookie(w, pc.Name, pc.Value, writeOpts); err != nil {
			return err
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(pattern.Status)
	_, err := w.Write(pattern.Body)
	return err
}

// validateImplementationShapesReflect checks every attached impl.Fn against
// the two concrete shapes this package recognizes, EAGERLY at Serve
// construction time rather than deferring to the first incoming request —
// a malformed Fn fails loudly and immediately. reqType is the route's Req
// type, recovered at runtime via reflect (see [buildRouteHandler]), letting
// Serve build the EXACT expected security Fn shape
// (func(context.Context, *http.Request, *Req) (map[string][]string, error))
// dynamically instead of via a static type parameter.
func validateImplementationShapesReflect(routeLabel string, reqType reflect.Type, impls []middleware.ServerImplementation) error {
	generalType := reflect.TypeOf((func(http.Handler) http.Handler)(nil))
	securityType := reflect.FuncOf(
		[]reflect.Type{
			reflect.TypeOf((*context.Context)(nil)).Elem(),
			reflect.TypeOf((*http.Request)(nil)),
			reflect.PointerTo(reqType),
		},
		[]reflect.Type{
			reflect.TypeOf(map[string][]string(nil)),
			reflect.TypeOf((*error)(nil)).Elem(),
		},
		false,
	)
	for _, impl := range impls {
		if impl.Fn == nil {
			continue
		}
		fnType := reflect.TypeOf(impl.Fn)
		if fnType == generalType || fnType == securityType {
			continue
		}
		return middleware.MiddlewareShapeError{
			Name:     impl.Name,
			Expected: "func(http.Handler) http.Handler or func(context.Context, *http.Request, *Req) (map[string][]string, error)",
			Got:      fmt.Sprintf("%T", impl.Fn),
		}
	}
	return nil
}

// runSecurityMiddlewareReflect is [runSecurityMiddleware]'s reflect-based
// equivalent — reqPtr is an addressable *Req reflect.Value (Req erased).
func runSecurityMiddlewareReflect(ctx context.Context, r *http.Request, reqPtr reflect.Value, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error {
	granted := make(map[string][]string)
	for _, impl := range impls {
		fnVal := reflect.ValueOf(impl.Fn)
		if !fnVal.IsValid() || fnVal.Kind() != reflect.Func || fnVal.Type().NumIn() != 3 {
			continue // not the security shape (general-purpose or nil)
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		results := fnVal.Call([]reflect.Value{reflect.ValueOf(ctx), reflect.ValueOf(r), reqPtr})
		if err, _ := results[1].Interface().(error); err != nil {
			return err
		}
		g, _ := results[0].Interface().(map[string][]string)
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}
