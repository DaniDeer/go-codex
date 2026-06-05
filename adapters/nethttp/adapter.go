// Package nethttp adapts [api/rest] route handles to [net/http] handlers.
//
// Each [RouteHandle] from api/rest becomes an [http.Handler] via [Handler].
// [Register] wires it directly onto an [http.ServeMux] using the Go 1.22+
// method-prefixed pattern ("POST /users", "GET /users/{id}", etc.).
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	createUser, _ := rest.NewRoute[CreateReq, User]("POST", "/users", ...).Register(b)
//
//	mux := http.NewServeMux()
//	nethttp.Register(mux, createUser, func(ctx context.Context, req CreateReq) (User, error) {
//	    // Access path params via the embedded request:
//	    r, _ := nethttp.RequestFromContext(ctx)
//	    id := r.PathValue("id")
//	    return svc.CreateUser(ctx, req)
//	}, nethttp.Options{})
//	http.ListenAndServe(":8080", mux)
//
// Error responses use the JSON body {"error":"<message>"} by default: 400 for
// decode/validation failures, 500 for handler or encode errors. Override via
// [Options.ErrorHandler].
//
// For body-less methods (GET, HEAD, DELETE) the handler function is called
// with the zero value of Req. Access path and query parameters through
// [RequestFromContext].
package nethttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/format"
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

// HandlerFunc is the typed application handler called by [Handler].
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
// Use [RequestFromContext] to access the underlying *http.Request for path
// parameters, headers, or other request metadata.
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// RequestFromContext retrieves the *http.Request stored in ctx by [Handler].
// Returns false if the context was not created by this package.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(contextKey{}).(*http.Request)
	return r, ok
}

// WithResponseHeaders copies the key-value pairs from h into the response
// header map stored in ctx (pre-allocated by [Handler] before calling the
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
// response header by [Handler]. Create one inside a [HandlerFunc] and deposit
// it via [WithResponseCookies].
type PendingCookie struct {
	Name  string
	Value string
	Opts  CookieOptions
}

// WithResponseCookies deposits one or more [PendingCookie] values into ctx.
// [Handler] validates their values against the route's [ResponseCookieParam]
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

// Options configures the behaviour of [Handler] and [Register].
type Options struct {
	// ErrorHandler, when non-nil, is called instead of the default JSON error
	// envelope when a request fails. status is the suggested HTTP status code
	// (400 or 500). Implementations must write the response header and body.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)

	// Observer, when non-nil, receives per-request lifecycle events: request
	// counts with latency and HTTP status, and per-field validation errors.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

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

	// SecurityFunc, when non-nil, is called for routes that declare a non-nil
	// Security field (via [rest.RouteMeta.Security] or global security), after
	// parameter validation but before the handler fn.
	//
	// Return a non-nil error to reject the request with 401 Unauthorized.
	// reqs contains the route's declared security requirements (scheme names +
	// scopes). The adapter has already extracted and codec-validated the credential
	// from the request before calling SecurityFunc.
	//
	// Example — JWT bearer verification:
	//
	//	opts.SecurityFunc = func(ctx context.Context, r *http.Request, reqs []route.SecurityRequirement) error {
	//	    token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	//	    return jwtlib.VerifyScopes(token, reqs)
	//	}
	SecurityFunc func(ctx context.Context, r *http.Request, reqs []route.SecurityRequirement) error
}

// Handler wraps a [rest.RouteHandle] and a [HandlerFunc] into an [http.Handler].
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
func Handler[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options) http.Handler {
	errFn := opts.ErrorHandler
	if errFn == nil {
		errFn = defaultErrorHandler
	}
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = maxRequestBodyBytes
	}
	expectedCT := opts.ContentType
	if expectedCT == "" {
		expectedCT = "application/json"
	}
	method := strings.ToUpper(handle.Descriptor.Method)
	path := handle.Descriptor.Path

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}
		defer func() {
			obs.RecordRequest(method, path, sw.code, time.Since(start))
		}()

		ctx := context.WithValue(r.Context(), contextKey{}, r)
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
				reportBodyErrors(decErr, obs)
				errFn(sw, r, http.StatusBadRequest, decErr)
				return
			}
		}

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(r.URL.Query()); err != nil {
				reportQueryErrors(err, obs)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryValues(r)); err != nil {
				reportQueryErrors(err, obs)
				errFn(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(err, obs)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerValues(r)); err != nil {
			reportHeaderErrors(err, obs)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
				}
				errFn(sw, r, http.StatusUnauthorized, credErr)
				return
			}
			if opts.SecurityFunc != nil {
				if err := opts.SecurityFunc(ctx, r, secReqs); err != nil {
					secErr := rest.SecurityError{Err: err}
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
					}
					errFn(sw, r, http.StatusUnauthorized, secErr)
					return
				}
			}
		}

		resp, err := fn(ctx, req)
		if err != nil {
			errFn(sw, r, http.StatusInternalServerError, err)
			return
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
					reportBodyErrors(valErr, obs)
					errFn(sw, r, http.StatusInternalServerError, valErr)
					return
				}
				respCT = chosen.ContentType()

				// Validate and write response headers/cookies before streaming.
				if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
					reportResponseHeaderErrors(err, obs)
					errFn(sw, r, http.StatusInternalServerError, err)
					return
				}
				if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
					reportResponseCookieErrors(err, obs)
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
					reportBodyErrors(streamErr, obs)
				}
				return
			}
			var encErr error
			out, encErr = chosen.Marshal(resp)
			if encErr != nil {
				reportBodyErrors(encErr, obs)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = chosen.ContentType()
		} else {
			var encErr error
			out, encErr = handle.Encode(resp)
			if encErr != nil {
				reportBodyErrors(encErr, obs)
				errFn(sw, r, http.StatusInternalServerError, encErr)
				return
			}
			respCT = "application/json"
		}

		// Validate response headers against registered ResponseHeaderParam codecs.
		if err := handle.ValidateResponseHeaders(responseHeaderValues(respHeaders)); err != nil {
			reportResponseHeaderErrors(err, obs)
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		// Validate response cookies against registered ResponseCookieParam codecs.
		if err := handle.ValidateResponseCookies(responseCookieValues(pendingCookies)); err != nil {
			reportResponseCookieErrors(err, obs)
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
}

// Register registers the route on mux using its method and path from the
// route descriptor. It uses the Go 1.22+ enhanced ServeMux pattern
// "METHOD /path" so each registration is scoped to a single method.
//
// Pass a zero-value [Options]{} for default behaviour.
func Register[Req, Resp any](mux *http.ServeMux, handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options) {
	pattern := strings.ToUpper(handle.Descriptor.Method) + " " + handle.Descriptor.Path
	mux.Handle(pattern, Handler(handle, fn, opts))
}

// SSEHandlerFunc is the typed application handler called by [SSEHandler].
// ctx is the request context (cancelled when the client disconnects).
// req is the decoded request (zero value for body-less GET requests).
// send encodes, validates, and writes one SSE event; it returns an error if
// the event fails codec validation or if the underlying write fails.
type SSEHandlerFunc[Req, Event any] func(ctx context.Context, req Req, send func(Event) error) error

// SSEHandler wraps an [rest.SSERouteHandle] and a user-supplied [SSEHandlerFunc]
// into an [http.Handler] that streams Server-Sent Events.
//
// The handler sets Content-Type: text/event-stream, Cache-Control: no-cache,
// and Connection: keep-alive, then calls fn. The send func provided to fn
// validates the event via the codec, encodes it as JSON, writes
// "data: <json>\n\n" to the response, and flushes. If the event fails
// validation, send returns an error without writing anything.
//
// fn should honour ctx.Done() for clean client-disconnect handling.
func SSEHandler[Req, Event any](handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options) http.Handler {
	if opts.ErrorHandler == nil {
		opts.ErrorHandler = defaultErrorHandler
	}
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusResponseWriter{ResponseWriter: w, code: http.StatusOK}

		var req Req
		ctx := context.WithValue(r.Context(), contextKey{}, r)
		responseHeaders := make(http.Header)
		ctx = context.WithValue(ctx, responseHeadersKey{}, responseHeaders)
		ctx = context.WithValue(ctx, responseCookiesKey{}, &[]PendingCookie{})

		defer func() {
			obs.RecordRequest(r.Method, handle.Descriptor.Path, sw.code, time.Since(start))
		}()

		// Validate query parameters against their registered codecs (if any).
		if opts.MultiValueQueryParams {
			if err := handle.ValidateQueryMulti(r.URL.Query()); err != nil {
				reportQueryErrors(err, obs)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		} else {
			if err := handle.ValidateQuery(queryValues(r)); err != nil {
				reportQueryErrors(err, obs)
				opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
				return
			}
		}

		// Validate cookie parameters against their registered codecs (if any).
		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(err, obs)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Validate header parameters against their registered codecs (if any).
		if err := handle.ValidateHeaders(headerValues(r)); err != nil {
			reportHeaderErrors(err, obs)
			opts.ErrorHandler(sw, r, http.StatusBadRequest, err)
			return
		}

		// Enforce security: per-route requirements take precedence; nil falls back
		// to global security declared via Builder.AddGlobalSecurity.
		secReqs := handle.Descriptor.Security
		if secReqs == nil {
			secReqs = handle.GlobalSecurity
		}
		if len(secReqs) > 0 {
			if credErr := validateSecurityCredentials(r, secReqs, handle.SecuritySchemes); credErr != nil {
				if secObs, ok := obs.(stats.SecurityObserver); ok {
					secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
				}
				opts.ErrorHandler(sw, r, http.StatusUnauthorized, credErr)
				return
			}
			if opts.SecurityFunc != nil {
				if err := opts.SecurityFunc(ctx, r, secReqs); err != nil {
					secErr := rest.SecurityError{Err: err}
					if secObs, ok := obs.(stats.SecurityObserver); ok {
						secObs.RecordSecurityRejection(handle.Descriptor.Path, firstScheme(secReqs))
					}
					opts.ErrorHandler(sw, r, http.StatusUnauthorized, secErr)
					return
				}
			}
		}

		// SSE headers — must be set before WriteHeader.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, canFlush := w.(http.Flusher)

		// pick event encoder: first EventFormat, or fallback to EncodeEvent (JSON)
		encode := handle.EncodeEvent
		if len(handle.Formats) > 0 {
			f := handle.Formats[0]
			encode = func(e Event) ([]byte, error) { return f.Marshal(e) }
		}

		validate := handle.ValidateEvent

		send := func(e Event) error {
			if err := validate(e); err != nil {
				obs.RecordValidationError("response", stats.ConstraintName(err), "event")
				return err
			}
			data, err := encode(e)
			if err != nil {
				return err
			}
			if _, werr := fmt.Fprintf(sw, "data: %s\n\n", data); werr != nil {
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
}

// RegisterSSE wires an [rest.SSERouteHandle] onto mux as a GET SSE endpoint.
func RegisterSSE[Req, Event any](mux *http.ServeMux, handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options) {
	mux.Handle("GET "+handle.Descriptor.Path, SSEHandler(handle, fn, opts))
}

// primaryStatus returns the HTTP status code for the primary success response.
// Falls back to 200 if the descriptor has no responses or the status is unparseable.
func primaryStatus[Req, Resp any](handle *rest.RouteHandle[Req, Resp]) int {
	if len(handle.Descriptor.Responses) == 0 {
		return http.StatusOK
	}
	code, err := strconv.Atoi(handle.Descriptor.Responses[0].Status)
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

// reportBodyErrors extracts per-field validation errors from a body decode error
// and reports them to obs with location "body".
func reportBodyErrors(err error, obs stats.Observer) {
	stats.ReportErrors(obs, "body", err)
}

// reportQueryErrors extracts the failing query parameter from a [rest.QueryParamError]
// and reports it to obs with location "query".
func reportQueryErrors(err error, obs stats.Observer) {
	var qe rest.QueryParamError
	if !errors.As(err, &qe) {
		return
	}
	obs.RecordValidationError("query", stats.ConstraintName(qe.Err), qe.Name)
}

// reportCookieErrors extracts the failing cookie parameter from a [rest.CookieParamError]
// and reports it to obs with location "cookie".
func reportCookieErrors(err error, obs stats.Observer) {
	var ce rest.CookieParamError
	if !errors.As(err, &ce) {
		return
	}
	obs.RecordValidationError("cookie", stats.ConstraintName(ce.Err), ce.Name)
}

// reportHeaderErrors extracts the failing header from a [rest.HeaderParamError]
// and reports it to obs with location "header".
func reportHeaderErrors(err error, obs stats.Observer) {
	var he rest.HeaderParamError
	if !errors.As(err, &he) {
		return
	}
	obs.RecordValidationError("header", stats.ConstraintName(he.Err), he.Name)
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
// [rest.ResponseHeaderParamError] and reports it to obs with location "response_header".
func reportResponseHeaderErrors(err error, obs stats.Observer) {
	var rhe rest.ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		return
	}
	obs.RecordValidationError("response_header", stats.ConstraintName(rhe.Err), rhe.Name)
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

// reportResponseCookieErrors extracts the failing response cookie from a
// [rest.ResponseCookieParamError] and reports it to obs with location "response_cookie".
func reportResponseCookieErrors(err error, obs stats.Observer) {
	var rce rest.ResponseCookieParamError
	if !errors.As(err, &rce) {
		return
	}
	obs.RecordValidationError("response_cookie", stats.ConstraintName(rce.Err), rce.Name)
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
