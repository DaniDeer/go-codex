// Package chiserver assembles routes/ + handlers/ onto the chi router
// adapter — the "assemble" phase, chi variant. nethttpserver/ mirrors this
// package exactly, using adapters/nethttp instead, proving the SAME
// declarations (routes/) and business logic (handlers/) assemble onto
// EITHER adapter unchanged.
package chiserver

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	gochi "github.com/go-chi/chi/v5"

	chiadapter "github.com/DaniDeer/go-codex/adapters/chi"
	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/rest-api/handlers"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
	"github.com/DaniDeer/go-codex/stats"
)

// Built bundles everything main.go/demo files need to talk to this server.
type Built struct {
	Server           *rest.Server
	Router           gochi.Router
	Addr             string
	GetUserHandle    *rest.RouteHandle[routes.GetUserReq, routes.User]
	UpdateUserHandle *rest.RouteHandle[routes.UpdateUserReq, routes.User]
}

// Build assembles every route declared in routes/ onto a fresh chi router:
// business logic from handlers/, security enforcement paired against
// routes.BoundScopeServerMW, the shared Observer, and the
// general-purpose timing middleware — then wires the router via
// b.Attach(chiadapter.NewServerTransport(...)) and starts serving on addr (via the returned
// Server.Serve(ctx), left for the caller to run).
func Build(store *handlers.UserStore, obs stats.Observer, logger *slog.Logger, addr string) (*Built, error) {
	domainLogger := logger.With("layer", "domain")

	b := rest.NewServer(rest.Info{
		Title:       "User API (chi)",
		Version:     "1.0.0",
		Description: "Three-layer codec pipeline + bearer JWT security, served via chi.",
	})
	b.AddServer("local", rest.ServerEntry{URL: "http://" + addr})

	obsFn := nethttp.Observability(obs) // chi reuses nethttp's general-purpose Fn shape directly.
	timingFn := routes.TimingServerMW(logger.With("component", "chi"))

	// errorHandler is the shared, generic JSON error envelope used by
	// every route below (via WithOptions(opts)) when no route-declared
	// rest.ErrorPattern matches. LoginRoute's invalid-credentials case
	// USED TO be special-cased here via a manual errors.As(err, &credErr)
	// branch — that imperative dispatch was replaced by a declarative
	// rest.ErrorPattern directly on routes.LoginRoute (see routes.go),
	// so this handler no longer needs to know about
	// routes.InvalidCredentialsError at all.
	errorHandler := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	opts := chiadapter.Options{ErrorHandler: errorHandler}

	loginRoute := routes.LoginRoute.WithHandler(
		handlers.WithDomainLogging("user.login", handlers.MakeLoginHandler(), domainLogger,
			func(_ routes.LoginReq, _ routes.TokenResp) []slog.Attr { return nil }),
	).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := loginRoute.Register(b); err != nil {
		return nil, err
	}

	createUserRoute := routes.CreateUserRoute.WithHandler(
		handlers.WithDomainLogging("user.create", handlers.MakeCreateUserHandler(store), domainLogger,
			func(_ routes.CreateUserReq, u routes.User) []slog.Attr {
				return []slog.Attr{slog.String("id", u.ID), slog.String("name", u.Name), slog.String("email", u.Email)}
			}),
	).HandleBoundMW(routes.BoundScopeServerMW[routes.CreateUserReq](routes.AdminScopes, handlers.ScopesBoundFn[routes.CreateUserReq]("/users"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := createUserRoute.Register(b); err != nil {
		return nil, err
	}

	getUserRoute := routes.GetUserRoute.WithHandler(
		handlers.WithDomainLogging("user.get", handlers.MakeGetUserHandler(store), domainLogger,
			func(_ routes.GetUserReq, u routes.User) []slog.Attr { return []slog.Attr{slog.String("id", u.ID)} }),
	).HandleBoundMW(routes.BoundScopeServerMW[routes.GetUserReq](routes.ProfileScopes, handlers.ScopesBoundFn[routes.GetUserReq]("/users/{id}"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	getUserHandle, err := getUserRoute.RegisterHandle(b)
	if err != nil {
		return nil, err
	}

	updateUserRoute := routes.UpdateUserRoute.WithHandler(
		handlers.WithDomainLogging("user.update", handlers.MakeUpdateUserHandler(store), domainLogger,
			func(req routes.UpdateUserReq, u routes.User) []slog.Attr {
				return []slog.Attr{slog.String("id", req.ID), slog.String("name", u.Name)}
			}),
	).HandleBoundMW(routes.BoundScopeServerMW[routes.UpdateUserReq](routes.AdminScopes, handlers.ScopesBoundFn[routes.UpdateUserReq]("/users/{id}"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	updateUserHandle, err := updateUserRoute.RegisterHandle(b)
	if err != nil {
		return nil, err
	}

	listUsersRoute := routes.ListUsersRoute.WithHandler(
		handlers.WithDomainLogging("user.list", handlers.MakeListUsersHandler(), domainLogger,
			func(_ routes.ListUsersReq, _ routes.PagedUsersResp) []slog.Attr { return nil }),
	).HandleBoundMW(routes.BoundScopeServerMW[routes.ListUsersReq](routes.ProfileScopes, handlers.ScopesBoundFn[routes.ListUsersReq]("/users"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := listUsersRoute.Register(b); err != nil {
		return nil, err
	}

	profileRoute := routes.ProfileRoute.WithHandler(
		handlers.WithDomainLogging("user.profile", handlers.MakeProfileHandler(), domainLogger,
			func(_ routes.ProfileReq, u routes.User) []slog.Attr { return []slog.Attr{slog.String("id", u.ID)} }),
	).HandleBoundMW(routes.BoundScopeServerMW[routes.ProfileReq](routes.ProfileScopes, handlers.ScopesBoundFn[routes.ProfileReq]("/profile"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := profileRoute.Register(b); err != nil {
		return nil, err
	}

	adminActionRoute := routes.AdminActionRoute.WithHandler(
		handlers.WithDomainLogging("admin.action", handlers.MakeAdminActionHandler(), domainLogger,
			func(_ routes.AdminActionReq, r routes.AdminActionResp) []slog.Attr {
				return []slog.Attr{slog.String("result", r.Result)}
			}),
	).HandleBoundMW(routes.BoundScopeServerMW[routes.AdminActionReq](routes.AdminScopes, handlers.ScopesBoundFn[routes.AdminActionReq]("/admin/action"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := adminActionRoute.Register(b); err != nil {
		return nil, err
	}

	// computeGSRoute demonstrates the GrantedScopes + ContextField
	// mechanism (docs/design/d-0007-declarative-middleware-layering.md) —
	// HandleBoundMW attaches handlers.VerifyBearerGS directly (already
	// shaped func(ctx, *routes.ComputeGSReq, routes.AuthIn) (routes.AuthOut,
	// error), no wrapper needed); routes.GrantedScopesComputeServerMW
	// supplies the merge field + ContextField wiring.
	computeGSRoute := routes.ComputeGSRoute.WithHandler(
		handlers.MakeComputeGSHandler(),
	).HandleBoundMW(routes.GrantedScopesComputeServerMW(handlers.VerifyBearerGS)).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := computeGSRoute.Register(b); err != nil {
		return nil, err
	}

	// reusableAloneRoute demonstrates Class 1 (reusable) attached ALONE,
	// via plain .Use() — no bound middleware, no security requirement
	// (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Final Phase demo).
	reusableAloneRoute := routes.ReusableAloneRoute.WithHandler(
		func(_ context.Context, req routes.StackedDemoReq) (routes.StackedDemoResp, error) {
			return routes.StackedDemoResp{Doubled: req.Value * 2}, nil
		},
	).Use(routes.ReusableRequestIDMw).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := reusableAloneRoute.Register(b); err != nil {
		return nil, err
	}

	// stackedDemoRoute demonstrates the reusable AND bound classes
	// attached TOGETHER on one route — .Use(reusable) runs first
	// (generic request-ID logging), .HandleBoundMW(bound) runs second
	// (the route-specific "profile" scope check, reusing the SAME
	// BoundScopeServerMW helper the 6 bearerAuth-secured routes above
	// already use) — see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Final
	// Phase demo.
	stackedDemoRoute := routes.StackedDemoRoute.WithHandler(
		func(_ context.Context, req routes.StackedDemoReq) (routes.StackedDemoResp, error) {
			return routes.StackedDemoResp{Doubled: req.Value * 2}, nil
		},
	).Use(routes.ReusableRequestIDMw).
		HandleBoundMW(routes.BoundScopeServerMW[routes.StackedDemoReq](routes.ProfileScopes, handlers.ScopesBoundFn[routes.StackedDemoReq]("/stacked-demo"))).
		HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := stackedDemoRoute.Register(b); err != nil {
		return nil, err
	}

	router := gochi.NewRouter()

	// GET /openapi.yaml — a hand-rolled, manual escape hatch: go-codex has
	// no declarative "serve my own spec" convenience today (see
	// docs/roadmap/openapi-spec-endpoint.md for a captured future idea).
	// Registered BEFORE Attach — chi's router cannot safely receive
	// new handlers once serving starts.
	router.Get("/openapi.yaml", specHandler(b))

	if err := b.Attach(chiadapter.NewServerTransport(chiadapter.ServerTransportOptions{Router: router, Addr: addr})); err != nil {
		return nil, err
	}

	return &Built{
		Server:           b,
		Router:           router,
		Addr:             addr,
		GetUserHandle:    getUserHandle,
		UpdateUserHandle: updateUserHandle,
	}, nil
}

// specHandler builds the spec ONCE (static for this demo — no dynamic
// route changes after startup) and closes over the bytes, serving them
// with the right Content-Type on every request.
func specHandler(b *rest.Server) http.HandlerFunc {
	doc, err := b.OpenAPISpec()
	if err != nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "spec build failed: "+err.Error(), http.StatusInternalServerError)
		}
	}
	yamlBytes, err := doc.MarshalYAML()
	if err != nil {
		return func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "spec marshal failed: "+err.Error(), http.StatusInternalServerError)
		}
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(yamlBytes)
	}
}
