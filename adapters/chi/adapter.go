// Package chi adapts [api/rest] route handles to [github.com/go-chi/chi/v5] routers.
//
// Each [RouteHandle] from api/rest becomes an [http.HandlerFunc] via [Handler].
// [Register] wires it directly onto a chi.Router using the route's method and path.
//
// Chi uses {param} placeholders identical to the go-codex path template syntax, so
// no path translation is needed. Path variables are extracted via [chi.URLParam].
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	createUser, _ := rest.NewRoute[CreateReq, User]("POST", "/users", ...).Register(b)
//
//	r := chi.NewRouter()
//	chiadapter.Register(r, createUser, func(ctx context.Context, req CreateReq) (User, error) {
//	    rr, _ := chiadapter.RequestFromContext(ctx)
//	    id := chi.URLParam(rr, "id")
//	    return svc.CreateUser(ctx, req)
//	}, chiadapter.Options{})
//	http.ListenAndServe(":8080", r)
//
// Error responses use the JSON body {"error":"<message>"} by default: 400 for
// decode/validation failures, 500 for handler or encode errors. Override via
// [Options.ErrorHandler].
//
// For body-less methods (GET, HEAD, DELETE) the handler function is called
// with the zero value of Req. Access path and query parameters through
// [RequestFromContext] and [chi.URLParam].
package chi

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

	gochi "github.com/go-chi/chi/v5"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/stats"
)

// maxRequestBodyBytes is the maximum number of bytes read from a request body.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// contextKey is the unexported type for request values stored in context by this package.
type contextKey struct{}

// responseHeadersKey is the unexported type for response header values stored in context.
type responseHeadersKey struct{}

// responseCookiesKey is the unexported type for pending response cookies stored in context.
type responseCookiesKey struct{}

// CookieOptions configures the security attributes and optional value validation
// applied by [SetCookie].
//
// Safe defaults: Secure=true, HttpOnly=true, SameSite=Strict, Path="/".
// Use the opt-in fields to relax specific attributes for legitimate use cases.
type CookieOptions struct {
	// Path is the cookie path. Defaults to "/" when empty.
	Path string

	// Domain scopes the cookie to a specific host. Defaults to the current host.
	Domain string

	// MaxAge is the cookie lifetime in seconds.
	// 0 means session cookie (deleted when browser closes).
	// Negative means delete the cookie immediately.
	MaxAge int

	// SameSite controls the cross-site request behaviour.
	// Defaults to [http.SameSiteStrictMode] when zero.
	SameSite http.SameSite

	// Insecure, when true, omits the Secure attribute.
	// Use only for non-TLS environments such as localhost development.
	Insecure bool

	// AllowJS, when true, omits the HttpOnly attribute.
	AllowJS bool

	// Codec, when non-nil, validates value before the Set-Cookie header is written.
	Codec *codex.Codec[string]
}

// SetCookie writes a Set-Cookie header on w with secure defaults:
// Secure, HttpOnly, SameSite=Strict, Path="/".
//
// If [CookieOptions.Codec] is non-nil, value is validated first.
// A validation failure returns [rest.CookieParamError] without writing any header.
func SetCookie(w http.ResponseWriter, name, value string, opts CookieOptions) error {
	if opts.Codec != nil {
		if err := opts.Codec.Validate(value); err != nil {
			return rest.CookieParamError{Name: name, Value: value, Err: err}
		}
	}
	path := opts.Path
	if path == "" {
		path = "/"
	}
	sameSite := opts.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteStrictMode
	}
	// #nosec G124 -- Secure/HttpOnly/SameSite enforced by default; Insecure/AllowJS are intentional opt-outs.
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   opts.Domain,
		MaxAge:   opts.MaxAge,
		Secure:   !opts.Insecure,
		HttpOnly: !opts.AllowJS,
		SameSite: sameSite,
	})
	return nil
}

// PendingCookie is a cookie queued to be validated and written as a Set-Cookie
// response header by [Handler].
type PendingCookie struct {
	Name  string
	Value string
	Opts  CookieOptions
}

// HandlerFunc is the typed application handler called by [Handler].
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
// Use [RequestFromContext] to access the underlying *http.Request for path
// parameters and headers. Chi path params are available via chi.URLParam(r, "name").
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// RequestFromContext retrieves the *http.Request stored in ctx by [Handler].
// Returns false if the context was not created by this package.
func RequestFromContext(ctx context.Context) (*http.Request, bool) {
	r, ok := ctx.Value(contextKey{}).(*http.Request)
	return r, ok
}

// WithResponseHeaders copies the key-value pairs from h into the response
// header map stored in ctx (pre-allocated by [Handler] before calling the
// [HandlerFunc]). Call this inside a [HandlerFunc] to emit response headers
// such as Location, ETag, or custom headers without direct access to
// [http.ResponseWriter].
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

// WithResponseCookies deposits one or more [PendingCookie] values into ctx.
// [Handler] validates their values against the route's [ResponseCookieParam]
// codecs and writes Set-Cookie headers on success.
func WithResponseCookies(ctx context.Context, cookies ...PendingCookie) {
	if pending, ok := ctx.Value(responseCookiesKey{}).(*[]PendingCookie); ok {
		*pending = append(*pending, cookies...)
	}
}

// ResponseCookiesFromContext retrieves pending cookies previously stored by
// [WithResponseCookies].
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
	// envelope when a request fails. status is the suggested HTTP status code.
	// Implementations must write the response header and body.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)

	// Observer, when non-nil, receives per-request lifecycle events.
	// Defaults to [stats.NoopObserver] when nil.
	Observer stats.Observer

	// MaxBodyBytes limits the number of bytes read from the request body.
	// Zero means the default (1 MiB).
	MaxBodyBytes int64

	// ContentType is the expected Content-Type for body-bearing methods.
	// Defaults to "application/json".
	ContentType string

	// MultiValueQueryParams, when true, uses [rest.RouteHandle.ValidateQueryMulti].
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

// Handler wraps a [rest.RouteHandle] and a [HandlerFunc] into an [http.HandlerFunc]
// suitable for use with a chi.Router.
func Handler[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options) http.HandlerFunc {
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

	return func(w http.ResponseWriter, r *http.Request) {
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

		if err := handle.ValidateCookies(cookieValues(r)); err != nil {
			reportCookieErrors(err, obs)
			errFn(sw, r, http.StatusBadRequest, err)
			return
		}

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
		if len(handle.ResponseFormats) > 0 {
			chosen, ok := negotiateFormat(handle.ResponseFormats, r.Header.Get("Accept"))
			if !ok {
				supported := make([]string, 0, len(handle.ResponseFormats))
				for _, f := range handle.ResponseFormats {
					if ct := f.ContentType(); ct != "" {
						supported = append(supported, ct)
					}
				}
				errFn(sw, r, http.StatusNotAcceptable,
					rest.NotAcceptableError{Accept: r.Header.Get("Accept"), Supported: supported})
				return
			}
			if chosen.IsStreamable() {
				if valErr := chosen.Validate(resp); valErr != nil {
					reportBodyErrors(valErr, obs)
					errFn(sw, r, http.StatusInternalServerError, valErr)
					return
				}
				respCT = chosen.ContentType()

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

		status := primaryStatus(handle)
		sw.Header().Set("Content-Type", respCT)
		sw.WriteHeader(status)
		_, _ = sw.Write(out) // #nosec G705 -- content is application-generated, not user-controlled
	}
}

// Register registers the route on r using its method and path from the route
// descriptor. Chi uses the same {param} placeholder syntax as go-codex path
// templates, so no translation is needed.
func Register[Req, Resp any](r gochi.Router, handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options) {
	method := strings.ToUpper(handle.Descriptor.Method)
	r.Method(method, handle.Descriptor.Path, Handler(handle, fn, opts))
}

// SSEHandlerFunc is the typed application handler called by [SSEHandler].
// ctx is the request context (cancelled when the client disconnects).
// req is the decoded request (zero value for body-less GET requests).
// send encodes, validates, and writes one SSE event; it returns an error if
// the event fails codec validation or if the underlying write fails.
type SSEHandlerFunc[Req, Event any] func(ctx context.Context, req Req, send func(Event) error) error

// SSEHandler wraps an [rest.SSERouteHandle] and a user-supplied [SSEHandlerFunc]
// into an [http.HandlerFunc] that streams Server-Sent Events.
//
// The handler sets Content-Type: text/event-stream, Cache-Control: no-cache,
// and Connection: keep-alive, then calls fn. The send func provided to fn
// validates the event via the codec, encodes it as JSON, writes
// "data: <json>\n\n" to the response, and flushes. If the event fails
// validation, send returns an error without writing anything.
//
// fn should honour ctx.Done() for clean client-disconnect handling.
func SSEHandler[Req, Event any](handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options) http.HandlerFunc {
	if opts.ErrorHandler == nil {
		opts.ErrorHandler = defaultErrorHandler
	}
	obs := opts.Observer
	if obs == nil {
		obs = stats.NoopObserver{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
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

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, canFlush := w.(http.Flusher)

		encode := handle.EncodeEvent
		if len(handle.EventFormats) > 0 {
			f := handle.EventFormats[0]
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
			if sw.code == http.StatusOK {
				opts.ErrorHandler(sw, r, http.StatusInternalServerError, err)
			}
		}
	}
}

// RegisterSSE wires an [rest.SSERouteHandle] onto a chi router as a GET SSE endpoint.
func RegisterSSE[Req, Event any](r gochi.Router, handle *rest.SSERouteHandle[Req, Event], fn SSEHandlerFunc[Req, Event], opts Options) {
	r.Get(handle.Descriptor.Path, SSEHandler(handle, fn, opts))
}

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

type statusResponseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *statusResponseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

type errorBody struct {
	Error string `json:"error"`
}

func defaultErrorHandler(w http.ResponseWriter, _ *http.Request, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorBody{Error: err.Error()})
	_, _ = w.Write(body)
}

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

func headerValues(r *http.Request) map[string]string {
	m := make(map[string]string, len(r.Header))
	for k, vs := range r.Header {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

func responseHeaderValues(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k, vs := range h {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

func responseCookieValues(cookies []PendingCookie) map[string]string {
	m := make(map[string]string, len(cookies))
	for _, pc := range cookies {
		m[pc.Name] = pc.Value
	}
	return m
}

func reportBodyErrors(err error, obs stats.Observer) {
	stats.ReportErrors(obs, "body", err)
}

func reportQueryErrors(err error, obs stats.Observer) {
	var qe rest.QueryParamError
	if !errors.As(err, &qe) {
		return
	}
	obs.RecordValidationError("query", stats.ConstraintName(qe.Err), qe.Name)
}

func reportCookieErrors(err error, obs stats.Observer) {
	var ce rest.CookieParamError
	if !errors.As(err, &ce) {
		return
	}
	obs.RecordValidationError("cookie", stats.ConstraintName(ce.Err), ce.Name)
}

func reportHeaderErrors(err error, obs stats.Observer) {
	var he rest.HeaderParamError
	if !errors.As(err, &he) {
		return
	}
	obs.RecordValidationError("header", stats.ConstraintName(he.Err), he.Name)
}

func reportResponseHeaderErrors(err error, obs stats.Observer) {
	var rhe rest.ResponseHeaderParamError
	if !errors.As(err, &rhe) {
		return
	}
	obs.RecordValidationError("response_header", stats.ConstraintName(rhe.Err), rhe.Name)
}

func reportResponseCookieErrors(err error, obs stats.Observer) {
	var rce rest.ResponseCookieParamError
	if !errors.As(err, &rce) {
		return
	}
	obs.RecordValidationError("response_cookie", stats.ConstraintName(rce.Err), rce.Name)
}

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

// firstScheme returns the first scheme name from the security requirements.
func firstScheme(reqs []route.SecurityRequirement) string {
	for _, req := range reqs {
		for name := range req {
			return name
		}
	}
	return ""
}
