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
