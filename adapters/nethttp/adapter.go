// Package nethttp adapts [api/rest] route handles to [net/http] handlers.
//
// Each [RouteHandle] from api/rest becomes an [http.Handler] via [Handler].
// [Register] wires it directly onto an [http.ServeMux] using the Go 1.22+
// method-prefixed pattern ("POST /users", "GET /users/{id}", etc.).
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	createUser := rest.AddRoute[CreateReq, User](b, "POST", "/users", ...)
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
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/stats"
)

// maxRequestBodyBytes is the maximum number of bytes read from a request body.
// Requests exceeding this limit are rejected with 400 Bad Request.
const maxRequestBodyBytes = 1 << 20 // 1 MiB

// contextKey is the unexported type for values stored in context by this package.
type contextKey struct{}

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

		var req Req
		if handle.Descriptor.RequestBody != nil {
			ct, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
			if strings.TrimSpace(ct) != expectedCT {
				errFn(sw, r, http.StatusUnsupportedMediaType,
					rest.UnsupportedMediaTypeError{Got: strings.TrimSpace(ct), Expected: expectedCT})
				return
			}
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
			req, decErr = handle.Decode(body)
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

		resp, err := fn(ctx, req)
		if err != nil {
			errFn(sw, r, http.StatusInternalServerError, err)
			return
		}

		out, encErr := handle.Encode(resp)
		if encErr != nil {
			errFn(sw, r, http.StatusInternalServerError, encErr)
			return
		}

		status := primaryStatus(handle)
		sw.Header().Set("Content-Type", "application/json")
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
