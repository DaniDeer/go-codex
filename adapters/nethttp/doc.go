// Package nethttp adapts [api/rest] routes to [net/http] handlers.
//
// [Serve] walks every handler-bearing [rest.Route] registered into a
// [rest.Builder] and wires it directly onto an [http.ServeMux] using the
// Go 1.22+ method-prefixed pattern ("POST /users", "GET /users/{id}",
// etc.). [ServeOne] is sugar for wiring a single route without an
// explicit Builder.
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	createUser := rest.NewRoute[CreateReq, User]("POST", "/users", ...).
//	    WithHandler(func(ctx context.Context, req CreateReq) (User, error) {
//	        // Access path params via the embedded request:
//	        r, _ := nethttp.RequestFromContext(ctx)
//	        id := r.PathValue("id")
//	        return svc.CreateUser(ctx, req)
//	    })
//	if err := createUser.Register(b); err != nil { ... }
//
//	mux := http.NewServeMux()
//	if err := nethttp.Serve(mux, b); err != nil { ... }
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
