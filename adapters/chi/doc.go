// Package chi adapts [api/rest] routes to [github.com/go-chi/chi/v5] routers.
//
// [Serve] walks every handler-bearing [rest.Route] registered into a
// [rest.Builder] and wires it directly onto a chi.Router using the route's
// method and path. [ServeOne] is sugar for wiring a single route without
// an explicit Builder.
//
// Chi uses {param} placeholders identical to the go-codex path template syntax, so
// no path translation is needed. Path variables are extracted via [chi.URLParam].
//
// Typical usage:
//
//	b := rest.NewBuilder(rest.Info{Title: "User API", Version: "1.0.0"})
//	createUser := rest.NewRoute[CreateReq, User]("POST", "/users", ...).
//	    WithHandler(func(ctx context.Context, req CreateReq) (User, error) {
//	        rr, _ := chiadapter.RequestFromContext(ctx)
//	        id := chi.URLParam(rr, "id")
//	        return svc.CreateUser(ctx, req)
//	    })
//	if err := createUser.Register(b); err != nil { ... }
//
//	r := chi.NewRouter()
//	if err := chiadapter.Serve(r, b); err != nil { ... }
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
