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
	"github.com/DaniDeer/go-codex/examples/rest-api/auth"
	"github.com/DaniDeer/go-codex/examples/rest-api/handlers"
	"github.com/DaniDeer/go-codex/examples/rest-api/observer"
	"github.com/DaniDeer/go-codex/examples/rest-api/requestid"
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
// auth.BoundScopeServerMW, the shared Observer, and the
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
	timingFn := observer.TimingServerMW(logger.With("component", "chi"))

	// errorHandler is the shared, generic JSON error envelope used by
	// every route below (via WithOptions(opts)) when no route-declared
	// rest.ErrorPattern matches. LoginRoute's invalid-credentials case
	// USED TO be special-cased here via a manual errors.As(err, &credErr)
	// branch — that imperative dispatch was replaced by a declarative
	// rest.ErrorPattern directly on auth.LoginRoute (see routes.go),
	// so this handler no longer needs to know about
	// routes.InvalidCredentialsError at all.
	errorHandler := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	opts := chiadapter.Options{ErrorHandler: errorHandler}

	loginRoute := auth.LoginRoute.WithHandler(
		handlers.WithDomainLogging("user.login", auth.MakeLoginHandler(), domainLogger,
			func(_ auth.LoginReq, _ auth.TokenResp) []slog.Attr { return nil }),
	).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := loginRoute.Register(b); err != nil {
		return nil, err
	}

	// CreateUserRoute/GetUserRoute/UpdateUserRoute/ListUsersRoute declare
	// RELATIVE paths ("", "/{id}") — Mounted below under a REAL
	// docs/design/d-0008-declarative-router-groups.md Mount
	// (rest.NewRouter("/users")), composing each back to its SAME,
	// byte-identical absolute path ("/users", "/users/{id}") this
	// server has always served. getUserHandle/updateUserHandle are
	// recovered via .WithOpt(rest.WithHandleCallback(...)) — the
	// Router-agnostic way to get a leaf's typed handle back when
	// Mounted, since RegisterHandle's return value isn't available
	// through Router.Register (nethttpserver/server.go mirrors this
	// exactly).
	var getUserHandle *rest.RouteHandle[routes.GetUserReq, routes.User]
	var updateUserHandle *rest.RouteHandle[routes.UpdateUserReq, routes.User]

	createUserRoute := routes.CreateUserRoute.WithHandler(
		handlers.WithDomainLogging("user.create", handlers.MakeCreateUserHandler(store), domainLogger,
			func(_ routes.CreateUserReq, u routes.User) []slog.Attr {
				return []slog.Attr{slog.String("id", u.ID), slog.String("name", u.Name), slog.String("email", u.Email)}
			}),
	).HandleBoundMW(auth.BoundScopeServerMW[routes.CreateUserReq](auth.AdminScopes, auth.ScopesBoundFn[routes.CreateUserReq]("/users"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)

	getUserRoute := routes.GetUserRoute.WithHandler(
		handlers.WithDomainLogging("user.get", handlers.MakeGetUserHandler(store), domainLogger,
			func(_ routes.GetUserReq, u routes.User) []slog.Attr { return []slog.Attr{slog.String("id", u.ID)} }),
	).HandleBoundMW(auth.BoundScopeServerMW[routes.GetUserReq](auth.ProfileScopes, auth.ScopesBoundFn[routes.GetUserReq]("/users/{id}"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts).
		WithOpt(rest.WithHandleCallback(func(h *rest.RouteHandle[routes.GetUserReq, routes.User]) { getUserHandle = h }))

	updateUserRoute := routes.UpdateUserRoute.WithHandler(
		handlers.WithDomainLogging("user.update", handlers.MakeUpdateUserHandler(store), domainLogger,
			func(req routes.UpdateUserReq, u routes.User) []slog.Attr {
				return []slog.Attr{slog.String("id", req.ID), slog.String("name", u.Name)}
			}),
	).HandleBoundMW(auth.BoundScopeServerMW[routes.UpdateUserReq](auth.AdminScopes, auth.ScopesBoundFn[routes.UpdateUserReq]("/users/{id}"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts).
		WithOpt(rest.WithHandleCallback(func(h *rest.RouteHandle[routes.UpdateUserReq, routes.User]) { updateUserHandle = h }))

	listUsersRoute := routes.ListUsersRoute.WithHandler(
		handlers.WithDomainLogging("user.list", handlers.MakeListUsersHandler(), domainLogger,
			func(_ routes.ListUsersReq, _ routes.PagedUsersResp) []slog.Attr { return nil }),
	).HandleBoundMW(auth.BoundScopeServerMW[routes.ListUsersReq](auth.ProfileScopes, auth.ScopesBoundFn[routes.ListUsersReq]("/users"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)

	usersRouter := rest.NewRouter("/users").
		Route(createUserRoute).
		Route(getUserRoute).
		Route(updateUserRoute).
		Route(listUsersRoute)
	if err := usersRouter.Register(b); err != nil {
		return nil, err
	}

	profileRoute := routes.ProfileRoute.WithHandler(
		handlers.WithDomainLogging("user.profile", handlers.MakeProfileHandler(), domainLogger,
			func(_ routes.ProfileReq, u routes.User) []slog.Attr { return []slog.Attr{slog.String("id", u.ID)} }),
	).HandleBoundMW(auth.BoundScopeServerMW[routes.ProfileReq](auth.ProfileScopes, auth.ScopesBoundFn[routes.ProfileReq]("/profile"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := profileRoute.Register(b); err != nil {
		return nil, err
	}

	adminActionRoute := routes.AdminActionRoute.WithHandler(
		handlers.WithDomainLogging("admin.action", handlers.MakeAdminActionHandler(), domainLogger,
			func(_ routes.AdminActionReq, r routes.AdminActionResp) []slog.Attr {
				return []slog.Attr{slog.String("result", r.Result)}
			}),
	).HandleBoundMW(auth.BoundScopeServerMW[routes.AdminActionReq](auth.AdminScopes, auth.ScopesBoundFn[routes.AdminActionReq]("/admin/action"))).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := adminActionRoute.Register(b); err != nil {
		return nil, err
	}

	// computeGSRoute demonstrates the GrantedScopes + ContextField
	// mechanism (docs/design/d-0007-declarative-middleware-layering.md) —
	// HandleBoundMW attaches auth.VerifyBearerGS directly (already
	// shaped func(ctx, *auth.ComputeGSReq, auth.AuthIn) (auth.AuthOut,
	// error), no wrapper needed); auth.GrantedScopesComputeServerMW
	// supplies the merge field + ContextField wiring.
	computeGSRoute := auth.ComputeGSRoute.WithHandler(
		auth.MakeComputeGSHandler(),
	).HandleBoundMW(auth.GrantedScopesComputeServerMW(auth.VerifyBearerGS)).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := computeGSRoute.Register(b); err != nil {
		return nil, err
	}

	// reusableAloneRoute demonstrates Class 1 (reusable) attached ALONE
	// — no bound middleware, no security requirement
	// (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Final Phase demo).
	//
	// stackedDemoRoute demonstrates the reusable AND bound classes
	// attached TOGETHER on one route — the reusable class runs first
	// (generic request-ID logging), .HandleBoundMW(bound) runs second
	// (the route-specific "profile" scope check, reusing the SAME
	// BoundScopeServerMW helper the 6 bearerAuth-secured routes above
	// already use) — see docs/design/d-0003-codec-declared-middlewares.md's Addendum 7's Final
	// Phase demo.
	//
	// Both routes used to repeat `.Use(requestid.ReusableRequestIDMw)`
	// separately — a textbook case for docs/design/
	// d-0008-declarative-router-groups.md's Router: grouped below under
	// ONE Router (Group-style, empty prefix — neither route shares a
	// hierarchical path segment, so this is "a group of handlers sharing
	// extra middleware" rather than a path-prefix Mount), sharing
	// .Use(requestid.ReusableRequestIDMw) + .Tags(...) ONCE instead of
	// twice. Router-contributed middleware dispatches BEFORE each
	// route's own HandleMW/HandleBoundMW attachments, identical ORDERING
	// to the previous per-route .Use() calls — a non-breaking change,
	// same paths, same dispatch order.
	reusableAloneRoute := routes.ReusableAloneRoute.WithHandler(
		func(_ context.Context, req routes.StackedDemoReq) (routes.StackedDemoResp, error) {
			return routes.StackedDemoResp{Doubled: req.Value * 2}, nil
		},
	).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)

	stackedDemoRoute := routes.StackedDemoRoute.WithHandler(
		func(_ context.Context, req routes.StackedDemoReq) (routes.StackedDemoResp, error) {
			return routes.StackedDemoResp{Doubled: req.Value * 2}, nil
		},
	).HandleBoundMW(auth.BoundScopeServerMW[routes.StackedDemoReq](auth.ProfileScopes, auth.ScopesBoundFn[routes.StackedDemoReq]("/stacked-demo"))).
		HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)

	middlewareStackingRouter := rest.NewRouter("").
		Use(requestid.ReusableRequestIDMw).
		Tags("middleware-stacking-demo").
		Route(reusableAloneRoute).
		Route(stackedDemoRoute)
	if err := middlewareStackingRouter.Register(b); err != nil {
		return nil, err
	}

	router := gochi.NewRouter()

	// GET /openapi.yaml — serves this Server's own OpenAPI spec, natively:
	// rest.Server.ServeSpec registers a normal route (same middleware
	// capabilities as any other — here, the same timingFn attached to
	// every other route) that computes OpenAPISpec() lazily on first
	// request and caches it, negotiating YAML (default)/JSON via Accept.
	// Must be called BEFORE Attach, exactly like every other Register call.
	if err := b.ServeSpec("/openapi.yaml", rest.WithSpecMiddleware(nil, timingFn)); err != nil {
		return nil, err
	}

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
