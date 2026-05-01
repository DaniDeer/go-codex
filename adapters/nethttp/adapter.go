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
//	})
//	http.ListenAndServe(":8080", mux)
//
// Error responses use the JSON body {"error":"<message>"} by default: 400 for
// decode/validation failures, 500 for handler or encode errors. Override by
// supplying a custom [Options.ErrorHandler] via [HandlerWithOptions].
//
// For body-less methods (GET, HEAD, DELETE) the handler function is called
// with the zero value of Req. Access path and query parameters through
// [RequestFromContext].
package nethttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/DaniDeer/go-codex/api/rest"
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

// Options configures the behaviour of [HandlerWithOptions].
type Options struct {
	// ErrorHandler, when non-nil, is called instead of the default JSON error
	// envelope when a request fails. status is the suggested HTTP status code
	// (400 or 500). Implementations must write the response header and body.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)
}

// Handler wraps a [rest.RouteHandle] and a [HandlerFunc] into an [http.Handler]
// using default options (JSON error envelope, 1 MiB body limit).
//
// For body-bearing methods (POST, PUT, PATCH) the request body is read,
// decoded, and validated using the route's codec before fn is called.
// For other methods (GET, HEAD, DELETE) fn is called with the zero value of Req.
//
// On success the response is JSON-encoded and written with the HTTP status from
// the route descriptor's primary response (the first entry in Responses).
func Handler[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp]) http.Handler {
	return HandlerWithOptions(handle, fn, Options{})
}

// HandlerWithOptions is like [Handler] but accepts [Options] to customise error
// handling.
func HandlerWithOptions[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options) http.Handler {
	errFn := opts.ErrorHandler
	if errFn == nil {
		errFn = defaultErrorHandler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), contextKey{}, r)

		var req Req
		if handle.Descriptor.RequestBody != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				errFn(w, r, http.StatusBadRequest, err)
				return
			}
			var decErr error
			req, decErr = handle.Decode(body)
			if decErr != nil {
				errFn(w, r, http.StatusBadRequest, decErr)
				return
			}
		}

		resp, err := fn(ctx, req)
		if err != nil {
			errFn(w, r, http.StatusInternalServerError, err)
			return
		}

		out, encErr := handle.Encode(resp)
		if encErr != nil {
			errFn(w, r, http.StatusInternalServerError, encErr)
			return
		}

		status := primaryStatus(handle)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(out)
	})
}

// Register registers the route on mux using its method and path from the
// route descriptor. It uses the Go 1.22+ enhanced ServeMux pattern
// "METHOD /path" so each registration is scoped to a single method.
func Register[Req, Resp any](mux *http.ServeMux, handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp]) {
	pattern := strings.ToUpper(handle.Descriptor.Method) + " " + handle.Descriptor.Path
	mux.Handle(pattern, Handler(handle, fn))
}

// RegisterWithOptions is like [Register] but accepts [Options].
func RegisterWithOptions[Req, Resp any](mux *http.ServeMux, handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp], opts Options) {
	pattern := strings.ToUpper(handle.Descriptor.Method) + " " + handle.Descriptor.Path
	mux.Handle(pattern, HandlerWithOptions(handle, fn, opts))
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
