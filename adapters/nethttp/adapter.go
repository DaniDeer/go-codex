package nethttp

import (
	"context"
	"net/http"
	"reflect"
	"strings"

	"github.com/DaniDeer/go-codex/adapters/internal/httpsecurity"
	"github.com/DaniDeer/go-codex/adapters/internal/restdispatch"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/internal/middleware"
	"github.com/DaniDeer/go-codex/internal/route"
	"github.com/DaniDeer/go-codex/stats"
)

// maxRequestBodyBytes is the maximum number of bytes read from a request body.
// Requests exceeding this limit are rejected with 400 Bad Request.
// maxRequestBodyBytes is PROMOTED (Phase 4) to
// [restdispatch.MaxRequestBodyBytes] — aliased here (not copied).
const maxRequestBodyBytes = restdispatch.MaxRequestBodyBytes

// contextKey is the unexported type for request values stored in context by this package.
type contextKey struct{}

// responseHeadersKey is the unexported type for response header values stored in context.
type responseHeadersKey struct{}

// responseCookiesKey is the unexported type for pending response cookies stored in context.
type responseCookiesKey struct{}

// HandlerFunc is the typed application handler called by [serve]/[ServeOne]'s dispatched request pipeline.
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
// Use [RequestFromContext] to access the underlying *http.Request for path
// parameters, headers, or other request metadata.
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// RequestFromContext retrieves the *http.Request stored in ctx by the request pipeline (see [serve]/[ServeOne]).
// Returns false if the context was not created by this package.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(contextKey{}).(*http.Request)
	return r, ok
}

// WithResponseHeaders copies the key-value pairs from h into the response
// header map stored in ctx (pre-allocated by the request pipeline before calling the
// [HandlerFunc]).  Call this inside a [HandlerFunc] to emit response headers
// such as Location, ETag, or custom headers without direct access to
// [http.ResponseWriter].
//
//	resp, _ := svc.Create(ctx, req)
//	h := make(http.Header)
//	h.Set("Location", "/users/"+resp.ID)
//	nethttp.WithResponseHeaders(ctx, h) // mutates the header map in ctx
func WithResponseHeaders(ctx context.Context, h http.Header) {
	if existing, ok := ctx.Value(responseHeadersKey{}).(http.Header); ok {
		for k, vs := range h {
			existing[k] = vs
		}
	}
}

// ResponseHeadersFromContext retrieves response headers previously stored by
// [WithResponseHeaders]. Returns false if no headers were set.
func ResponseHeadersFromContext(ctx context.Context) (http.Header, bool) {
	h, ok := ctx.Value(responseHeadersKey{}).(http.Header)
	return h, ok
}

// PendingCookie is a cookie queued to be validated and written as a Set-Cookie
// response header by the request pipeline. Create one inside a [HandlerFunc] and deposit
// it via [WithResponseCookies].
//
// Type alias (docs/design/d-0006-protocol-native-capabilities.md's
// Phase 7 — promoted to api/rest, since nethttp/chi's own copies were
// byte-for-byte structurally identical): [rest.PendingCookie]'s
// `Attrs CookieAttributes` field replaces this former `Opts
// CookieOptions` field — see [cookieOptionsFrom] for the write-time
// conversion back to this package's own [CookieOptions].
type PendingCookie = rest.PendingCookie

// WithResponseCookies deposits one or more [PendingCookie] values into ctx.
// The request pipeline validates their values against the route's [ResponseCookieParam]
// codecs and then writes Set-Cookie headers on success.
// Call this inside a [HandlerFunc] to emit response cookies.
func WithResponseCookies(ctx context.Context, cookies ...PendingCookie) {
	if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
		*pending = append(*pending, cookies...)
	}
}

// ResponseCookiesFromContext retrieves pending cookies previously stored by
// [WithResponseCookies]. Returns false if no cookies were queued.
func ResponseCookiesFromContext(ctx context.Context) ([]PendingCookie, bool) {
	pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie)
	if !ok || pending == nil {
		return nil, false
	}
	return *pending, true
}

// Options configures the behaviour of [serve]/[ServeOne]/[serveSSE].
//
// BREAKING: Observer and SecurityFunc are REMOVED — replaced by
// [middleware.Middleware] (declare-time, attached via [rest.WithMiddleware]/
// [rest.Route.Use]) paired with a [rest.ServerImplementation]
// (register-time, attached via [rest.Route.HandleMW]). See
// [Observability] for the observer replacement.
type Options struct {
	// ErrorHandler, when non-nil, is called instead of the default JSON error
	// envelope when a request fails. status is the suggested HTTP status code
	// (400 or 500). Implementations must write the response header and body.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)

	// MaxBodyBytes limits the number of bytes read from the request body for
	// body-bearing methods (POST, PUT, PATCH). Zero means the default (1 MiB).
	// Requests exceeding the limit are rejected with 400 Bad Request.
	MaxBodyBytes int64

	// ContentType is the expected Content-Type for body-bearing methods (POST,
	// PUT, PATCH). When non-empty, requests whose Content-Type does not match
	// (ignoring parameters such as "; charset=utf-8") are rejected with
	// 415 Unsupported Media Type. Defaults to "application/json".
	ContentType string

	// MultiValueQueryParams, when true, passes the raw multi-value query map
	// (map[string][]string from r.URL.Query()) to [rest.RouteHandle.ValidateQueryMulti]
	// instead of the flat single-value map. Use when your routes use repeated query
	// keys such as "?tags=a&tags=b". When false (default), the first value per key
	// is validated via [rest.RouteHandle.ValidateQuery].
	MultiValueQueryParams bool
}

// applyGeneralMiddleware wraps h with every general-purpose Fn found in
// impls, OUTERMOST-in, in attachment order (impls[0] is outermost — the
// first attached implementation runs first and returns last).
func applyGeneralMiddleware(h http.Handler, impls []rest.ServerImplementation) http.Handler {
	for i := len(impls) - 1; i >= 0; i-- {
		fn, ok := impls[i].Fn.(func(http.Handler) http.Handler)
		if !ok {
			continue
		}
		h = fn(h)
	}
	return h
}

// handlerFunc wraps a [rest.RouteHandle] and a [HandlerFunc] into an
// [http.Handler] — the shared implementation behind [serve]/[ServeOne]'s
// reflect dispatch (via [buildRouteHandler]) and [HandlerLatest]/
// [PipelineHandler] (in stream.go), which call it directly since Req/Resp
// are concrete at those call sites.
//
// For body-bearing methods (POST, PUT, PATCH) the request body is read,
// decoded, and validated using the route's codec before fn is called.
// For other methods (GET, HEAD, DELETE) fn is called with the zero value of Req.
//
// On success the response is JSON-encoded and written with the HTTP status from
// the route descriptor's primary response (the first entry in Responses).
//
// Pass a zero-value [Options]{} for default behaviour (JSON error envelope, 1 MiB
// body limit, application/json Content-Type check, no-op observer).
func handlerFunc[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options, impls ...rest.ServerImplementation) http.Handler {
	// Phase 4 (REST family step 6): construction-time gate — closes the
	// confirmed gap (Phase 1 audit) that this port/stream-binding
	// dispatch function never ran shape validation/security coverage/
	// capability coverage/param-kind coverage, unlike serve.go's
	// buildRouteHandler. handle.HandlerFn/handle.Implementations are
	// NOT used here — fn/impls are this function's OWN call-time
	// parameters (confirmed: no caller ever calls handle.WithHandler) —
	// so ValidateRouteConstructionForHandler resolves them explicitly
	// instead of reading the handle's own (unset) fields. No
	// error-return path exists on this function's signature to surface
	// a failure through normally, and backward compatibility is
	// explicitly a non-goal of this roadmap — a failed check PANICS with
	// a named restdispatch.ConstructionError (never a bare panic(err)),
	// mirroring buildRouteHandler's "fail loudly, never silently"
	// principle via the only mechanism available at this call shape.
	//
	// Phase 4 (REST family step 7): the per-request body below is now
	// ALSO shared with buildRouteHandler via restdispatch.DispatchRoute
	// — confirmed (by exhaustive line-by-line comparison) that every
	// mechanism this generic dispatch used (MergeFields+DecodeVars,
	// ResponseHeaderMergeFields/ResponseCookieMergeFields,
	// negotiateFormat/negotiateRequestFormat, primaryStatus,
	// tryRespondErrorPatternGeneric) is the SAME canonical RouteHandle
	// method DispatchRoute already calls via reflection, just reached
	// generically instead — no behavior change.
	cr, err := restdispatch.ValidateRouteConstructionForHandler("nethttp", handle, fn, impls)
	if err != nil {
		panic(restdispatch.ConstructionError{
			Adapter: "nethttp",
			Route:   handle.Descriptor.Method + " " + handle.Descriptor.Path,
			Err:     err,
		})
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
	writeCookie := func(w http.ResponseWriter, name, value string, attrs rest.CookieAttributes) error {
		return SetCookie(w, name, value, cookieOptionsFrom(attrs))
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		respHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, respHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)
		restdispatch.DispatchRoute(ctx, cr, sw, r, respHeaders, &pendingCookies, pathValues, writeCookie, errFn, maxBody, expectedCT, opts.MultiValueQueryParams)
	})

	return applyGeneralMiddleware(inner, cr.Impls)
}

// SSEHandlerFunc is the typed application handler called by SSE dispatch.
// ctx is the request context (cancelled when the client disconnects).
// req is the decoded request (zero value for body-less GET requests).
// send encodes, validates, and writes one SSE event; it returns an error if
// the event fails codec validation or if the underlying write fails.
type SSEHandlerFunc[Req, Event any] func(ctx context.Context, req Req, send func(Event) error) error

// sseHandlerFunc wraps a [rest.SSERouteHandle] and a user-supplied
// [SSEHandlerFunc] into an [http.Handler] that streams Server-Sent Events —
// the shared implementation behind [serveSSE]'s reflect dispatch and
// [binding.go]'s SSEAdapter, which calls it directly since Req/Event are
// concrete at that call site.
//
// The handler sets Content-Type: text/event-stream, Cache-Control: no-cache,
// and Connection: keep-alive, then calls fn. The send func provided to fn
// validates the event via the codec, encodes it as JSON, writes
// "data: <json>\n\n" to the response, and flushes. If the event fails
// validation, send returns an error without writing anything.
//
// fn should honour ctx.Done() for clean client-disconnect handling.
func sseHandlerFunc[Req, Event any](handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options, impls ...rest.ServerImplementation) http.Handler {
	if opts.ErrorHandler == nil {
		opts.ErrorHandler = defaultErrorHandler
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}

		var req Req
		ctx := middleware.EnsureContextFields(r.Context())
		ctx = context.WithValue(ctx, contextKey{}, r)
		responseHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, responseHeaders)
		pendingCookies := make([]PendingCookie, 0)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &pendingCookies)

		// carrier is constructed ONCE and its extracted maps reused for
		// BOTH the validation calls below AND the per-event MergeEvent
		// closure captured further down (docs/design/
		// d-0006-protocol-native-capabilities.md's Phase 6 "Decision A"
		// — eliminates the former double extraction).
		carrier := httpCarrier{R: r}
		queryVars := carrier.ExtractQuery()
		headerVars := rest.NormalizeHeaderVars(carrier.ExtractHeaders(), handle.HeaderParamNames())
		cookieVars := carrier.ExtractCookies()

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(carrier.ExtractQueryMulti()); err != nil {
				rest.ReportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryVars); err != nil {
				rest.ReportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieVars); err != nil {
			rest.ReportCookieErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerVars); err != nil {
			rest.ReportHeaderErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		if names := handle.PathParamNames(); len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				rest.ReportPathErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}
		pathVars := pathValues(r, handle.PathParamNames())

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := rest.ValidateSecurityCredentials(credentialExtractorFor(r), secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, route.FirstSchemeName(secReqs))
				}
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Gap-1 review fix (docs/design/d-0007-declarative-middleware-layering.md's
		// Rollout Phase A review): this ports-facing sseHandlerFunc
		// previously never dispatched handle.MiddlewareHandlers at all —
		// only serve_sse.go's Attach path did. Mirrors serve_sse.go's
		// identical unified dispatch sequence (see handlerFunc's twin
		// rewrite for the full rationale).
		reqPtr := reflect.ValueOf(&req)
		granted, grantErr := httpsecurity.CollectGrantsReflect(ctx, r, reqPtr, impls, secReqs)
		if grantErr != nil {
			opts.ErrorHandler(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: grantErr})
			return
		}
		middlewareOuts, mwErr := rest.DispatchMiddlewareHandlers(ctx, reqPtr, handle.MiddlewareHandlers, headerVars, cookieVars, queryVars)
		if mwErr != nil {
			dispatchErr, _ := rest.AsMiddlewareDispatchError(mwErr)
			if !dispatchErr.IsFnError {
				opts.ErrorHandler(sw, r, http.StatusBadRequest, dispatchErr.Err)
				return
			}
			if isSecuritySatisfyingHandler(handle.MiddlewareHandlers, dispatchErr.Name) {
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: dispatchErr.Err})
				return
			}
			opts.ErrorHandler(sw, r, http.StatusBadRequest, rest.MiddlewareError{Name: dispatchErr.Name, Err: dispatchErr.Err})
			return
		}
		httpsecurity.MergeMiddlewareHandlerGrants(granted, satisfiesPerHandler(handle.MiddlewareHandlers), middlewareOuts)
		if err := rest.CheckScopes(secReqs, granted); err != nil {
			opts.ErrorHandler(sw, r, http.StatusUnauthorized, rest.SecurityError{Err: err})
			return
		}
		// Compose every middleware's OWN response header/cookie values
		// (derived from its Out, already produced pre-handler above) —
		// BEFORE SSE's own headers are committed below, mirroring
		// serve_sse.go's identical "Out-side" composition point.
		for i, h := range handle.MiddlewareHandlers {
			mwHeaders, mwCookies, encErr := h.EncodeOut(ctx, middlewareOuts[i])
			if encErr != nil {
				stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
				opts.ErrorHandler(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range mwHeaders {
				responseHeaders.Set(k, v)
			}
			var mwCookieAttrs map[string]rest.CookieAttributes
			if h.EncodeOutCookieAttrs != nil {
				mwCookieAttrs, encErr = h.EncodeOutCookieAttrs(middlewareOuts[i])
				if encErr != nil {
					stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "middleware:out", encErr)
					opts.ErrorHandler(sw, r, http.StatusInternalServerError, encErr)
					return
				}
			}
			for k, v := range mwCookies {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v, Attrs: mwCookieAttrs[k]})
			}
		}

		// SSE headers — must be set before WriteHeader.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, canFlush := w.(http.Flusher)

		// pick event encoder: first EventFormat or fallback to EncodeEvent (JSON).
		// When Formats is non-empty, negotiate using the Accept header; return 406
		// if the client requests a specific format not offered. Accept: text/event-stream
		// or */* falls back to the first registered format.
		encode := handle.EncodeEvent
		if len(handle.Formats) > 0 {
			accept := r.Header.Get("Accept")
			f, ok := negotiateFormat(handle.Formats, accept)
			if !ok {
				// text/event-stream is the stream transport type, not a data format —
				// treat it as "use the default".
				if strings.Contains(accept, "text/event-stream") {
					f = handle.Formats[0]
				} else {
					var supported []string
					for _, fmt := range handle.Formats {
						supported = append(supported, fmt.ContentType())
					}
					opts.ErrorHandler(sw, r, http.StatusNotAcceptable,
						rest.NotAcceptableError{Accept: accept, Supported: supported})
					return
				}
			}
			encode = func(e Event) ([]byte, error) { return f.Marshal(e) }
		}

		validate := handle.ValidateEvent

		headersCommitted := false
		send := func(e Event) error {
			if len(handle.MergeFields()) > 0 {
				var err error
				e, err = handle.MergeEvent(e, pathVars, queryVars, headerVars, cookieVars)
				if err != nil {
					stats.ReportErrors(rest.DiagnosticObserver{Ctx: ctx}, "response", err)
					return err
				}
			}
			if !headersCommitted {
				headersCommitted = true
				// Commit staged response headers/cookies on first send, before
				// any data is written (headers are not yet sent to the client).
				if err := handle.ValidateResponseHeaders(rest.NormalizeHeaderVars(responseHeaderValues(responseHeaders), handle.ResponseHeaderParamNames())); err != nil {
					rest.ReportResponseHeaderErrors(ctx, err)
					return err
				}
				if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
					if err := handle.ValidateResponseCookies(responseCookieValues(*pending)); err != nil {
						rest.ReportResponseCookieErrors(ctx, err)
						return err
					}
					for i := range *pending {
						pc := &(*pending)[i]
						writeOpts := cookieOptionsFrom(pc.Attrs) // Codec is always nil here — cookieOptionsFrom never sets it
						if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
							return err
						}
					}
				}
				for key, vals := range responseHeaders {
					for _, v := range vals {
						sw.Header().Add(key, v)
					}
				}
			}
			if err := validate(e); err != nil {
				stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response", ConstraintName: stats.ConstraintName(err), Field: "event"})
				return err
			}
			data, err := encode(e)
			if err != nil {
				return err
			}
			if _, werr := writeSSEData(sw, data); werr != nil {
				return werr
			}
			if canFlush {
				flusher.Flush()
			}
			return nil
		}

		if err := fn(ctx, req, send); err != nil {
			// docs/features/rest-redirects.md round 2 finding E: a
			// RedirectError (rest.RedirectToSSE) is only honored BEFORE
			// the stream has committed to 200 + event-stream — gated on
			// headersCommitted (NOT sw.code, which writeSSEData never
			// updates since it writes the embedded io.Writer directly).
			// A RedirectError returned AFTER commit is an ordinary late
			// error, handled by the existing fallback below.
			if !headersCommitted {
				if redirErr, ok := asRedirectError(err); ok {
					sw.Header().Set("Location", redirErr.Location)
					sw.WriteHeader(redirErr.Status)
					return
				}
			}
			// If headers not yet written (first call failed before any send),
			// we can still emit an error response.
			if sw.code == http.StatusOK {
				opts.ErrorHandler(sw, r, http.StatusInternalServerError, err)
			}
		}
	})

	// General-purpose middlewares wrap the WHOLE call, outermost-in, in
	// attachment order — see handlerFunc's equivalent wrapping.
	return applyGeneralMiddleware(inner, impls)
}

// statusResponseWriter wraps [http.ResponseWriter] to capture the written status code.
type statusResponseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *statusResponseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

// StatusCode implements [restdispatch.ResponseWriter] — exposes the
// captured status code (added for Phase 4's SSE family consolidation,
// restdispatch.DispatchSSERoute's own "has a response already been
// committed" check).
func (rw *statusResponseWriter) StatusCode() int {
	return rw.code
}

// defaultErrorHandler is PROMOTED (Phase 4, REST family dispatch-core
// consolidation) to [restdispatch.DefaultErrorHandler] — aliased here
// (not copied) so every existing call site in this package (handlerFunc,
// serve.go/serve_sse.go via buildRouteHandler/SSE dispatch) keeps
// compiling and behaving identically.
var defaultErrorHandler = restdispatch.DefaultErrorHandler

// pathValues extracts the path variable values named in names from r using
// the stdlib net/http r.PathValue method (available since Go 1.22).
func pathValues(r *http.Request, names []string) map[string]string {
	m := make(map[string]string, len(names))
	for _, name := range names {
		m[name] = r.PathValue(name)
	}
	return m
}

// responseHeaderValues/responseCookieValues are PROMOTED (Phase 4) to
// restdispatch's identical functions — aliased here (not copied) so
// every existing call site keeps compiling and behaving identically.
// restdispatch's responseCookieValues takes []rest.PendingCookie
// (PendingCookie's own underlying type, see the alias above), so this
// package's []PendingCookie values pass through unchanged.
func responseHeaderValues(h http.Header) map[string]string {
	return restdispatch.ResponseHeaderValues(h)
}

func responseCookieValues(cookies []PendingCookie) map[string]string {
	return restdispatch.ResponseCookieValues(cookies)
}

// WriteErrorResponse implements [rest.ErrorResponseWriter] — the ONLY
// genuinely protocol-specific remainder of the former
// `writeErrorPatternResponse` (docs/design/
// d-0006-protocol-native-capabilities.md's Phase 7): match, encode, and
// validate all now happen inside [rest.RouteHandle.DispatchErrorResponse]
// itself; this method only writes the already-validated headers,
// cookies, status, and body onto the wire.
func (sw *statusResponseWriter) WriteErrorResponse(headers map[string][]string, cookies []PendingCookie, status int, body []byte) error {
	for key, vals := range headers {
		for _, v := range vals {
			sw.Header().Add(key, v)
		}
	}
	for _, pc := range cookies {
		writeOpts := cookieOptionsFrom(pc.Attrs) // Codec is always nil here — cookieOptionsFrom never sets it
		if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
			return err
		}
	}
	sw.Header().Set("Content-Type", "application/json")
	sw.WriteHeader(status)
	_, err := sw.Write(body)
	return err
}

// negotiateFormat picks the first format whose ContentType matches an entry in
// the Accept header. "*/*" matches the first format in the list.
// Format content-type parameters (e.g. "; charset=utf-8") are stripped before
// comparison so "Accept: text/html" matches "text/html; charset=utf-8".
// Returns false if no format satisfies the Accept value.
func negotiateFormat[T any](formats []format.Format[T], accept string) (format.Format[T], bool) {
	if len(formats) == 0 {
		return format.Format[T]{}, false
	}
	if accept == "" || accept == "*/*" {
		return formats[0], true
	}
	for _, entry := range strings.Split(accept, ",") {
		mediaType, _, _ := strings.Cut(strings.TrimSpace(entry), ";")
		mediaType = strings.TrimSpace(mediaType)
		if mediaType == "*/*" {
			return formats[0], true
		}
		for _, f := range formats {
			fmtMediaType, _, _ := strings.Cut(f.ContentType(), ";")
			if strings.TrimSpace(fmtMediaType) == mediaType {
				return f, true
			}
		}
	}
	return format.Format[T]{}, false
}

// credentialExtractorFor is PROMOTED (Phase 4) to
// [restdispatch.CredentialExtractorFor] — aliased here (not copied).
var credentialExtractorFor = restdispatch.CredentialExtractorFor
