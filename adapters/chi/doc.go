// Package chi adapts [api/rest] routes to [github.com/go-chi/chi/v5] routers.
//
// [AttachRouter] is the sole public server-side workflow (see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 6): it wires
// every handler-bearing [rest.Route]/[rest.SSERoute] registered into a
// [rest.Server] directly onto a [gochi.Router] using the route's method
// and path, then [rest.Server.Serve] blocks, owning its own
// [*http.Server], until ctx is cancelled. The lower-level `serve`/
// `serveSSE`/`serveOne` primitives this relies on internally are no
// longer publicly reachable — as of this scoping pass, no cross-package
// consumer depended on a bare [http.Handler] for a single chi route
// outside the Attach workflow (unlike adapters/nethttp's [nethttp.ServeOne],
// which stays public for adapters/mcprest/adapters/templ).
//
// Chi uses {param} placeholders identical to the go-codex path template syntax, so
// no path translation is needed. Path variables are extracted via [chi.URLParam].
//
// Typical usage:
//
//	b := rest.NewServer(rest.Info{Title: "User API", Version: "1.0.0"})
//	createUser := rest.NewRoute[CreateReq, User]("POST", "/users", ...).
//	    WithHandler(func(ctx context.Context, req CreateReq) (User, error) {
//	        rr, _ := chiadapter.RequestFromContext(ctx)
//	        id := chi.URLParam(rr, "id")
//	        return svc.CreateUser(ctx, req)
//	    })
//	if err := createUser.Register(b); err != nil { ... }
//
//	r := chi.NewRouter()
//	if err := chiadapter.AttachRouter(b, r, ":8080"); err != nil { ... }
//	if err := b.Serve(ctx); err != nil { ... } // blocks, owns its own http.Server
//
// Error responses use the JSON body {"error":"<message>"} by default: 400 for
// decode/validation failures, 500 for handler or encode errors. Override via
// [Options.ErrorHandler].
//
// For body-less methods (GET, HEAD, DELETE) the handler function is called
// with the zero value of Req. Access path and query parameters through
// [RequestFromContext] and [chi.URLParam].
package chi
