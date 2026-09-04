// Package nethttp adapts [api/rest] routes to [net/http] handlers.
//
// [AttachMux] is the sole public server-side workflow (see
// docs/roadmap/pubsub-workflow-simplification.md's Decision 6): it wires
// every handler-bearing [rest.Route]/[rest.SSERoute] registered into a
// [rest.Server] directly onto an [http.ServeMux] using the Go 1.22+
// method-prefixed pattern ("POST /users", "GET /users/{id}", etc.), then
// [rest.Server.Serve] blocks, owning its own [*http.Server], until ctx is
// cancelled. The lower-level `serve`/`serveSSE` primitives this relies on
// internally are no longer publicly reachable. [ServeOne] (sugar for
// wiring a single route without an explicit [rest.Server]) stays public —
// it remains a load-bearing building block for callers (e.g.
// adapters/mcprest, adapters/templ) that need a bare [http.Handler] for
// one route outside the Attach workflow.
//
// Typical usage:
//
//	b := rest.NewServer(rest.Info{Title: "User API", Version: "1.0.0"})
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
//	if err := nethttp.AttachMux(b, mux, ":8080"); err != nil { ... }
//	if err := b.Serve(ctx); err != nil { ... } // blocks, owns its own http.Server
//
// Error responses use the JSON body {"error":"<message>"} by default: 400 for
// decode/validation failures, 500 for handler or encode errors. Override via
// [Options.ErrorHandler].
//
// For body-less methods (GET, HEAD, DELETE) the handler function is called
// with the zero value of Req. Access path and query parameters through
// [RequestFromContext].
package nethttp
