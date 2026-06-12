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
