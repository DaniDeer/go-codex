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
//	    return svc.CreateUser(ctx, req)
//	})
//	http.ListenAndServe(":8080", mux)
//
// Error responses use the JSON body {"error":"<message>"}: 400 for
// decode/validation failures, 500 for handler or encode errors.
//
// For body-less methods (GET, HEAD, DELETE) the handler function is called
// with the zero value of Req. Path and query parameter extraction is the
// caller's responsibility via [context.Context] or middleware.
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

// HandlerFunc is the typed application handler called by [Handler].
// ctx is the request context. req is the decoded request value; for body-less
// methods it is the zero value of Req.
type HandlerFunc[Req, Resp any] func(ctx context.Context, req Req) (Resp, error)

// Handler wraps a [rest.RouteHandle] and a [HandlerFunc] into an [http.Handler].
//
// For body-bearing methods (POST, PUT, PATCH) the request body is read,
// decoded, and validated using the route's codec before fn is called.
// For other methods (GET, HEAD, DELETE) fn is called with the zero value of Req.
//
// On success the response is JSON-encoded and written with the HTTP status from
// the route descriptor's primary response (the first entry in Responses).
// On error a JSON {"error":"<message>"} body is written with status 400
// (decode/validation) or 500 (handler or encode failure).
func Handler[Req, Resp any](handle *rest.RouteHandle[Req, Resp], fn HandlerFunc[Req, Resp]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Req
		if handle.Descriptor.RequestBody != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, "cannot read request body")
				return
			}
			req, err = handle.Decode(body)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}

		resp, err := fn(r.Context(), req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		out, err := handle.Encode(resp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "cannot encode response")
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

// errorBody is the JSON error envelope.
type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(errorBody{Error: msg})
	_, _ = w.Write(body)
}
