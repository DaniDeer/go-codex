package nethttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// maxRequestBodyBytes is the maximum number of bytes read from a request body.
// Requests exceeding this limit are rejected with 400 Bad Request.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// contextKey is the unexported type for request values stored in context by this package.
type contextKey struct{}

// responseHeadersKey is the unexported type for response header values stored in context.
type responseHeadersKey struct{}

// responseCookiesKey is the unexported type for pending response cookies stored in context.
type responseCookiesKey struct{}

// HandlerFunc is the typed application handler called by [Serve]/[ServeOne]'s dispatched request pipeline.
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
// Use [RequestFromContext] to access the underlying *http.Request for path
// parameters, headers, or other request metadata.
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// RequestFromContext retrieves the *http.Request stored in ctx by the request pipeline (see [Serve]/[ServeOne]).
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
type PendingCookie struct {
	Name  string
	Value string
	Opts  CookieOptions
}

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

// Options configures the behaviour of [Serve]/[ServeOne]/[ServeSSE].
//
// BREAKING: Observer and SecurityFunc are REMOVED — replaced by
// [middleware.Middleware] (declare-time, attached via [rest.WithMiddleware]/
// [rest.Route.Use]) paired with a [middleware.ServerImplementation]
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
func applyGeneralMiddleware(h http.Handler, impls []middleware.ServerImplementation) http.Handler {
	for i := len(impls) - 1; i >= 0; i-- {
		fn, ok := impls[i].Fn.(func(http.Handler) http.Handler)
		if !ok {
			continue
		}
		h = fn(h)
	}
	return h
}

// runSecurityMiddleware runs every attached security-specific Fn IN
// ATTACHMENT ORDER (fail-fast on the FIRST one whose OWN credential
// extraction errors), merges their returned grants into ONE map, then
// performs a SINGLE [middleware.CheckScopes] call — see "L4" in
// docs/roadmap/declarative-middleware.md for why each Fn does NOT
// independently decide pass/fail against the route's full requirement set.
//
// An implementation with an EMPTY Satisfies (a pure presence/format check,
// e.g. an API-key format validator, contributing no scope grants) ALWAYS
// runs, regardless of whether the route declares any Security — that is
// its whole design point (see docs/roadmap/declarative-middleware.md's
// "Header/cookie param auto-contribution" section). An implementation
// with a NON-EMPTY Satisfies (e.g. a scope-checking implementation) only
// runs when the route actually declares a security requirement — an
// unsecured route must not authenticate credentials it never asked for.
func runSecurityMiddleware[Req any](ctx context.Context, r *http.Request, req *Req, impls []middleware.ServerImplementation, secReqs []route.SecurityRequirement) error {
	granted := make(map[string][]string)
	for _, impl := range impls {
		fn, ok := impl.Fn.(func(context.Context, *http.Request, *Req) (map[string][]string, error))
		if !ok {
			continue
		}
		if len(impl.Satisfies) > 0 && len(secReqs) == 0 {
			continue
		}
		g, err := fn(ctx, r, req)
		if err != nil {
			return err
		}
		for k, v := range g {
			granted[k] = v
		}
	}
	return middleware.CheckScopes(secReqs, granted)
}

// handlerFunc wraps a [rest.RouteHandle] and a [HandlerFunc] into an
// [http.Handler] — the shared implementation behind [Serve]/[ServeOne]'s
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
func handlerFunc[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options, impls ...middleware.ServerImplementation) http.Handler {
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

		var req Req
		if handle.Descriptor.RequestBody != nil {
			ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
			ct = strings.TrimSpace(ct)
			r.Body = http.MaxBytesReader(sw, r.Body, maxBody)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					errFn(sw, r, http.StatusRequestEntityTooLarge, rest.BodyTooLargeError{Limit: maxBody})
				} else {
					errFn(sw, r, http.StatusBadRequest, err)
				}
				return
			}
			var decErr error
			if len(handle.RequestFormats) > 0 {
				// Multi-format: negotiate by Content-Type, 415 on no match.
				chosen, ok := negotiateRequestFormat(handle.RequestFormats, ct)
				if !ok {
					var supported []string
					for _, f := range handle.RequestFormats {
						supported = append(supported, f.ContentType())
					}
					errFn(sw, r, http.StatusUnsupportedMediaType,
						rest.UnsupportedMediaTypeError{Got: ct, Supported: supported})
					return
				}
				var v Req
				v, decErr = chosen.Unmarshal(body)
				req = v
			} else {
				// Single-format: enforce opts.ContentType (default application/json).
				if ct != expectedCT {
					errFn(sw, r, http.StatusUnsupportedMediaType,
						rest.UnsupportedMediaTypeError{Got: ct, Supported: []string{expectedCT}})
					return
				}
				req, decErr = handle.Decode(body)
			}
			if decErr != nil {
				reportBodyErrors(ctx, decErr)
				errFn(sw, r, http.StatusBadRequest, decErr)
				return
			}
		}

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(r.URL.Query()); err != nil {
				reportQueryErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryValues(r)); err != nil {
				reportQueryErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(ctx, err)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerValues(r)); err != nil {
			reportHeaderErrors(ctx, err)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		names := handle.PathParamNames()
		if len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				reportPathErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Merge path/query/header/cookie values declared via
		// rest.NewPathParam/NewRequiredQueryParam/etc. into req — additive,
		// only runs when the route has merge-capable params (backward
		// compatible: identical behavior to the block above when none are
		// declared). Values were already validated by the block above;
		// DecodeVars re-validates as a byproduct of decoding, which is
		// harmless (same codec, same value).
		if mergeFields := handle.MergeFields(); len(mergeFields) > 0 {
			vars := pathValues(r, names)
			for k, v := range queryValues(r) {
				vars[k] = v
			}
			for k, v := range headerValues(r) {
				vars[k] = v
			}
			for k, v := range cookieValues(r) {
				vars[k] = v
			}
			if err := codex.DecodeVars(&req, vars, mergeFields...); err != nil {
				reportBodyErrors(ctx, err)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
				}
				errFn(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Run every attached security-specific Fn, merge grants, ONE
		// middleware.CheckScopes call — see "L4" in
		// docs/roadmap/declarative-middleware.md. Called even when secReqs
		// is empty: a middleware with an EMPTY Satisfies (a pure
		// presence/format check, e.g. RequireAPIKey) must still run — see
		// runSecurityMiddleware's own doc comment.
		if err := runSecurityMiddleware(ctx, r, &req, impls, secReqs); err != nil {
			secErr := rest.SecurityError{Err: err}
			errFn(sw, r, http.StatusUnauthorized, secErr)
			return
		}

		var (
			resp Resp
			err  error
		)

		resp, err = fn(ctx, req)
		if err != nil {
			if patternResp, matched, applyErr := handle.ErrorResponseFor(err); matched {
				if applyErr == nil {
					// ErrorRespond (default): write the typed body directly.
					// ErrorHandle/ErrorLog: skip the auto-write and fall
					// through to errFn below (Options.ErrorHandler), same
					// as an unmatched error, but still using this pattern's
					// declared status via ErrorStatusFor.
					if patternResp.Action == "" || patternResp.Action == rest.ErrorRespond {
						if writeErr := writeErrorPatternResponse(ctx, sw, handle, patternResp, respHeaders, pendingCookies); writeErr == nil {
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
			if mappedStatus, ok := handle.ErrorStatusFor(err); ok {
				status = mappedStatus
			}
			errFn(sw, r, status, err)
			return
		}

		// Encode response header/cookie values declared via
		// rest.NewRequiredResponseHeaderParam/etc. into the SAME
		// respHeaders/pendingCookies values WithResponseHeaders/
		// WithResponseCookies already write to — additive, only runs
		// when the route has response merge-capable params. The
		// existing ValidateResponseHeaders/ValidateResponseCookies +
		// write loop below picks these up unchanged, exactly like the
		// manual ResponseHeadersFromContext/WithResponseCookies escape
		// hatch.
		if headerFields := handle.ResponseHeaderMergeFields(); len(headerFields) > 0 {
			values, encErr := codex.EncodeVars(resp, headerFields...)
			if encErr != nil {
				reportResponseHeaderErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range values {
				respHeaders.Set(k, v)
			}
		}
		if cookieFields := handle.ResponseCookieMergeFields(); len(cookieFields) > 0 {
			values, encErr := codex.EncodeVars(resp, cookieFields...)
			if encErr != nil {
				reportResponseCookieErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			for k, v := range values {
				pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v})
			}
		}

		var out []byte
		var respCT string
		if len(handle.Formats) > 0 {
			chosen, ok := negotiateFormat(handle.Formats, r.Header.Get("Accept"))
			if !ok {
				supported := make([]string, 0, len(handle.Formats))
				for _, f := range handle.Formats {
					if ct := f.ContentType(); ct != "" {
						supported = append(supported, ct)
					}
				}
				errFn(sw, r, http.StatusNotAcceptable,
					rest.NotAcceptableError{Accept: r.Header.Get("Accept"), Supported: supported})
				return
			}
			if chosen.IsStreamable() {
				// Pre-validate before committing response headers so we can still
				// return an error response if the value violates codec constraints.
				if valErr := chosen.Validate(resp); valErr != nil {
					reportBodyErrors(ctx, valErr)
					errFn(sw, r, http.StatusInternalServerError, valErr)
					return
				}
				respCT = chosen.ContentType()

				// Validate and write response headers/cookies before streaming.
				if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
					reportResponseHeaderErrors(ctx, err)
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
				if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
					reportResponseCookieErrors(ctx, err)
					errFn(sw, r, http.StatusInternalServerError, err)
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
				sw.WriteHeader(primaryStatus(handle))
				// MarshalTo re-validates then streams. Headers are committed at
				// this point; streaming errors are logged but cannot be returned
				// as HTTP error responses.
				if streamErr := chosen.MarshalTo(resp, sw); streamErr != nil {
					reportBodyErrors(ctx, streamErr)
				}
				return
			}
			var encErr error
			out, encErr = chosen.Marshal(resp)
			if encErr != nil {
				reportBodyErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = chosen.ContentType()
		} else {
			var encErr error
			out, encErr = handle.Encode(resp)
			if encErr != nil {
				reportBodyErrors(ctx, encErr)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = "application/json"
		}

		// Validate response headers against registered ResponseHeaderParam codecs.
		if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
			reportResponseHeaderErrors(ctx, err)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		// Validate response cookies against registered ResponseCookieParam codecs.
		if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
			reportResponseCookieErrors(ctx, err)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		for key, vals := range respHeaders {
			for _, v := range vals {
				sw.Header().Add(key, v)
			}
		}
		for i := range pendingCookies {
			pc := &pendingCookies[i]
			// Adapter already ran ValidateResponseCookies; clear Opts.Codec to
			// avoid double validation inside SetCookie.
			writeOpts := pc.Opts
			writeOpts.Codec = nil
			if err := SetCookie(sw, pc.Name, pc.Value, writeOpts); err != nil {
				errFn(sw, r, http.StatusInternalServerError, err)
				return
			}
		}
		status := primaryStatus(handle)
		sw.Header().Set("Content-Type", respCT)
		sw.WriteHeader(status)
		_, _ = sw.Write(out)
	})

	// General-purpose middlewares (e.g. [Observability]) wrap the
	// WHOLE call, outermost-in, in attachment order — see "Two attachment
	// points" in docs/roadmap/declarative-middleware.md.
	return applyGeneralMiddleware(inner, impls)
}

// SSEHandlerFunc is the typed application handler called by SSE dispatch.
// ctx is the request context (cancelled when the client disconnects).
// req is the decoded request (zero value for body-less GET requests).
// send encodes, validates, and writes one SSE event; it returns an error if
// the event fails codec validation or if the underlying write fails.
type SSEHandlerFunc[Req, Event any] func(ctx context.Context, req Req, send func(Event) error) error

// sseHandlerFunc wraps a [rest.SSERouteHandle] and a user-supplied
// [SSEHandlerFunc] into an [http.Handler] that streams Server-Sent Events —
// the shared implementation behind [ServeSSE]'s reflect dispatch and
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
func sseHandlerFunc[Req, Event any](handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options, impls ...middleware.ServerImplementation) http.Handler {
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
		ctx = context.WithValue(ctx, responseCookiesKey{}, &[]PendingCookie{})

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(r.URL.Query()); err != nil {
				reportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryValues(r)); err != nil {
				reportQueryErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerValues(r)); err != nil {
			reportHeaderErrors(ctx, err)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate path parameters against their registered codecs (if any).
		if names := handle.PathParamNames(); len(names) > 0 {
			if err := handle.ValidatePathParams(pathValues(r, names)); err != nil {
				reportPathErrors(ctx, err)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}
		pathVars := pathValues(r, handle.PathParamNames())
		queryVars := queryValues(r)
		headerVars := headerValues(r)
		cookieVars := cookieValues(r)

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := stats.ObserverFromContext(ctx).(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
				}
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, credErr)
				return
			}
		}
		// Called even when secReqs is empty — see runSecurityMiddleware's
		// own doc comment (a middleware with an EMPTY Satisfies must still
		// run).
		if err := runSecurityMiddleware(ctx, r, &req, impls, secReqs); err != nil {
			secErr := rest.SecurityError{Err: err}
			opts.ErrorHandler(sw, r, http.StatusUnauthorized, secErr)
			return
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
					stats.ReportErrors(diagnosticObserver{ctx}, "response", err)
					return err
				}
			}
			if !headersCommitted {
				headersCommitted = true
				// Commit staged response headers/cookies on first send, before
				// any data is written (headers are not yet sent to the client).
				if err := handle.ValidateResponseHeaders(responseHeaderValues(responseHeaders)); err != nil {
					reportResponseHeaderErrors(ctx, err)
					return err
				}
				if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
					if err := handle.ValidateResponseCookies(responseCookieValues(*pending)); err != nil {
						reportResponseCookieErrors(ctx, err)
						return err
					}
					for i := range *pending {
						pc := &(*pending)[i]
						writeOpts := pc.Opts
						writeOpts.Codec = nil
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

// primaryStatus returns the HTTP status code for the primary success response.
// Falls back to 200 if the descriptor has no responses or the status is unparseable.
func primaryStatus[Req, Resp any](handle *rest.RouteHandle[Req, Resp]) int {
	return primaryStatusFor(handle.Descriptor)
}

// primaryStatusFor is [primaryStatus]'s non-generic-signature equivalent —
// used by [Serve], which only has a route.Route descriptor (via reflect),
// never a concrete *rest.RouteHandle[Req, Resp].
func primaryStatusFor(descriptor route.Route) int {
	if len(descriptor.Responses) == 0 {
		return http.StatusOK
	}
	code, err := strconv.Atoi(descriptor.Responses[0].Status)
	if err != nil {
		return http.StatusOK
	}
	return code
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

// errorBody is the JSON error envelope used by defaultErrorHandler.
type errorBody struct {
	Error string `json:"error"`
}

func defaultErrorHandler(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorBody{Error: err.Error()})
	_, _ = w.Write(body)
}

// pathValues extracts the path variable values named in names from r using
// the stdlib net/http r.PathValue method (available since Go 1.22).
func pathValues(r *http.Request, names []string) map[string]string {
	m := make(map[string]string, len(names))
	for _, name := range names {
		m[name] = r.PathValue(name)
	}
	return m
}

// queryValues extracts all query parameters from r into a flat map[string]string.
// When a key appears multiple times, the first value is used.
func queryValues(r *http.Request) map[string]string {
	q := r.URL.Query()
	m := make(map[string]string, len(q))
	for k, vs := range q {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// cookieValues extracts all cookies from r into a flat map[string]string.
// When a cookie name appears multiple times, the first value is used.
func cookieValues(r *http.Request) map[string]string {
	cookies := r.Cookies()
	m := make(map[string]string, len(cookies))
	for _, c := range cookies {
		if _, exists := m[c.Name]; !exists {
			m[c.Name] = c.Value
		}
	}
	return m
}

// headerValues extracts HTTP headers from r into a flat map[string]string.
// When a header has multiple values, only the first is kept.
func headerValues(r *http.Request) map[string]string {
	m := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// diagnosticObserver adapts ctx-based [stats.RecordDiagnostic] to the
// [stats.ValidationObserver] interface, so [stats.ReportErrors]'s existing
// per-field error-walking logic can be reused UNCHANGED — the SAME data,
// just ferried out via ctx instead of a direct Observer call. See "Class B"
// in docs/roadmap/declarative-middleware.md.
type diagnosticObserver struct{ ctx context.Context }

func (d diagnosticObserver) RecordValidationError(location, constraintName, field string) {
	stats.RecordDiagnostic(d.ctx, stats.Diagnostic{Location: location, ConstraintName: constraintName, Field: field})
}

// reportBodyErrors extracts per-field validation errors from a body decode
// error and ferries them out via [stats.RecordDiagnostic] with location "body".
func reportBodyErrors(ctx context.Context, err error) {
	stats.ReportErrors(diagnosticObserver{ctx}, "body", err)
}

// reportQueryErrors extracts the failing query parameter from a [rest.QueryParamError]
// and ferries it out via [stats.RecordDiagnostic] with location "query".
func reportQueryErrors(ctx context.Context, err error) {
	var qe rest.QueryParamError
	if !errors.As(err, &qe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "query", ConstraintName: stats.ConstraintName(qe.Err), Field: qe.Name})
}

// reportCookieErrors extracts the failing cookie parameter from a [rest.CookieParamError]
// and ferries it out via [stats.RecordDiagnostic] with location "cookie".
func reportCookieErrors(ctx context.Context, err error) {
	var ce rest.CookieParamError
	if !errors.As(err, &ce) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "cookie", ConstraintName: stats.ConstraintName(ce.Err), Field: ce.Name})
}

// reportHeaderErrors extracts the failing header from a [rest.HeaderParamError]
// and ferries it out via [stats.RecordDiagnostic] with location "header".
func reportHeaderErrors(ctx context.Context, err error) {
	var he rest.HeaderParamError
	if !errors.As(err, &he) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "header", ConstraintName: stats.ConstraintName(he.Err), Field: he.Name})
}

// responseHeaderValues converts an http.Header into a flat map[string]string
// for use with [rest.RouteHandle.ValidateResponseHeaders].
// When a header has multiple values, only the first is kept.
func responseHeaderValues(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// reportResponseHeaderErrors extracts the failing response header from a
// [rest.ResponseHeaderParamError] and ferries it out via
// [stats.RecordDiagnostic] with location "response_header".
func reportResponseHeaderErrors(ctx context.Context, err error) {
	var rhe rest.ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response_header", ConstraintName: stats.ConstraintName(rhe.Err), Field: rhe.Name})
}

// responseCookieValues extracts name→value from a slice of PendingCookie for use
// with [rest.RouteHandle.ValidateResponseCookies]. When a name appears more than
// once, the last value wins.
func responseCookieValues(cookies []PendingCookie) map[string]string {
	m := make(map[string]string, len(cookies))
	for _, pc := range cookies {
		m[pc.Name] = pc.Value
	}
	return m
}

func writeErrorPatternResponse[Req, Resp any](
	ctx context.Context,
	w http.ResponseWriter,
	handle *rest.RouteHandle[Req, Resp],
	pattern rest.ErrorPatternResponse,
	respHeaders http.Header,
	pendingCookies []PendingCookie,
) error {
	if respVal, ok := pattern.Value.(Resp); ok {
		headerValues, cookieValues, encErr := handle.EncodeResponseMergeFields(respVal)
		if encErr != nil {
			reportResponseHeaderErrors(ctx, encErr)
			reportResponseCookieErrors(ctx, encErr)
			return encErr
		}
		for k, v := range headerValues {
			respHeaders.Set(k, v)
		}
		for k, v := range cookieValues {
			pendingCookies = append(pendingCookies, PendingCookie{Name: k, Value: v})
		}
	}

	if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
		reportResponseHeaderErrors(ctx, err)
		return err
	}
	if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
		reportResponseCookieErrors(ctx, err)
		return err
	}

	for key, vals := range respHeaders {
		for _, v := range vals {
			w.Header().Add(key, v)
		}
	}
	for i := range pendingCookies {
		pc := &pendingCookies[i]
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

// reportResponseCookieErrors extracts the failing response cookie from a
// [rest.ResponseCookieParamError] and ferries it out via
// [stats.RecordDiagnostic] with location "response_cookie".
func reportResponseCookieErrors(ctx context.Context, err error) {
	var rce rest.ResponseCookieParamError
	if !errors.As(err, &rce) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "response_cookie", ConstraintName: stats.ConstraintName(rce.Err), Field: rce.Name})
}

// reportPathErrors extracts the failing path variable from a [rest.PathParamError]
// and ferries it out via [stats.RecordDiagnostic] with location "path".
func reportPathErrors(ctx context.Context, err error) {
	var pe rest.PathParamError
	if !errors.As(err, &pe) {
		return
	}
	stats.RecordDiagnostic(ctx, stats.Diagnostic{Location: "path", ConstraintName: stats.ConstraintName(pe.Err), Field: pe.Name})
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

// negotiateRequestFormat picks the format whose ContentType matches the given
// Content-Type header value (exact match after stripping parameters).
func negotiateRequestFormat[T any](formats []format.Format[T], contentType string) (format.Format[T], bool) {
	for _, f := range formats {
		fmtMediaType, _, _ := strings.Cut(f.ContentType(), ";")
		if strings.TrimSpace(fmtMediaType) == contentType {
			return f, true
		}
	}
	return format.Format[T]{}, false
}

// validateSecurityCredentials extracts credentials from the request and validates
// them against the registered SecurityScheme codecs for the declared requirements.
// Returns a [rest.SecurityCredentialError] if any codec check fails.
func validateSecurityCredentials(r *http.Request, reqs []route.SecurityRequirement, schemes map[string]rest.SecurityScheme) error {
	for _, req := range reqs {
		for name := range req {
			s, ok := schemes[name]
			if !ok || s.Codec == nil {
				continue
			}
			cred := extractCredential(r, s)
			if err := s.Codec.Validate(cred); err != nil {
				return rest.SecurityCredentialError{Scheme: name, Err: err}
			}
		}
	}
	return nil
}

// extractCredential returns the raw credential string from the request based
// on the scheme type and location.
func extractCredential(r *http.Request, s rest.SecurityScheme) string {
	switch s.Type {
	case route.SecuritySchemeHTTP:
		auth := r.Header.Get("Authorization")
		switch strings.ToLower(s.Scheme) {
		case "bearer":
			// RFC 7235 §2.1: scheme names are case-insensitive.
			if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
				return auth[7:]
			}
			return auth
		case "basic":
			if len(auth) >= 6 && strings.EqualFold(auth[:6], "Basic ") {
				return auth[6:]
			}
			return auth
		}
		return auth
	case route.SecuritySchemeOAuth2, route.SecuritySchemeOpenIDConnect:
		auth := r.Header.Get("Authorization")
		if len(auth) >= 7 && strings.EqualFold(auth[:7], "Bearer ") {
			return auth[7:]
		}
		return auth
	case route.SecuritySchemeAPIKey:
		switch strings.ToLower(s.In) {
		case "header":
			return r.Header.Get(s.Name)
		case "query":
			return r.URL.Query().Get(s.Name)
		case "cookie":
			if c, err := r.Cookie(s.Name); err == nil {
				return c.Value
			}
		}
	}
	return ""
}

// firstScheme returns the first scheme name from the security requirements,
// or an empty string if there are none. Used for observer reporting.
func firstScheme(reqs []route.SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}
