// Package chiserver assembles routes/ + handlers/ onto the chi router
// adapter — the "assemble" phase, chi variant. nethttpserver/ mirrors this
// package exactly, using adapters/nethttp instead, proving the SAME
// declarations (routes/) and business logic (handlers/) assemble onto
// EITHER adapter unchanged.
package chiserver

import (
	"context"
	"encoding/json"
	"errors"
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

// chiResponseDepositor implements handlers.ResponseDepositor using chi's
// OWN WithResponseHeaders/WithResponseCookies/PendingCookie — the ONE
// adapter-specific seam MakeCreateUserHandler needs (see
// handlers.ResponseDepositor's doc comment).
type chiResponseDepositor struct{}

func (chiResponseDepositor) SetHeader(ctx context.Context, h http.Header) {
	chiadapter.WithResponseHeaders(ctx, h)
}

func (chiResponseDepositor) SetCookie(ctx context.Context, name, value string, maxAgeSeconds int) {
	chiadapter.WithResponseCookies(ctx, chiadapter.PendingCookie{
		Name:  name,
		Value: value,
		Opts:  chiadapter.CookieOptions{MaxAge: maxAgeSeconds, Insecure: true},
	})
}

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
// routes.ProfileScopeMw/AdminScopeMw, the shared Observer, and the
// general-purpose timing middleware — then wires the router via
// chiadapter.AttachRouter and starts serving on addr (via the returned
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

	errorHandler := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		var credErr handlers.InvalidCredentialsError
		if errors.As(err, &credErr) {
			status = http.StatusUnauthorized
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
	}
	opts := chiadapter.Options{ErrorHandler: errorHandler}

	dep := chiResponseDepositor{}

	loginRoute := routes.LoginRoute.WithHandler(
		handlers.WithDomainLogging("user.login", handlers.MakeLoginHandler(), domainLogger,
			func(_ routes.LoginReq, _ routes.TokenResp) []slog.Attr { return nil }),
	).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := loginRoute.Register(b); err != nil {
		return nil, err
	}

	createUserImpl := handlers.ScopesImpl[routes.CreateUserReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *routes.CreateUserReq) (map[string][]string, error) {
			return handlers.ExtractScopes(ctx, r, "/users")
		},
	)
	createUserRoute := routes.CreateUserRoute.WithHandler(
		handlers.WithDomainLogging("user.create", handlers.MakeCreateUserHandler(store, dep), domainLogger,
			func(_ routes.CreateUserReq, u routes.User) []slog.Attr {
				return []slog.Attr{slog.String("id", u.ID), slog.String("name", u.Name), slog.String("email", u.Email)}
			}),
	).HandleMW(&routes.AdminScopeMw, createUserImpl.Fn).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := createUserRoute.Register(b); err != nil {
		return nil, err
	}

	getUserImpl := handlers.ScopesImpl[routes.GetUserReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *routes.GetUserReq) (map[string][]string, error) {
			return handlers.ExtractScopes(ctx, r, "/users/{id}")
		},
	)
	getUserRoute := routes.GetUserRoute.WithHandler(
		handlers.WithDomainLogging("user.get", handlers.MakeGetUserHandler(store), domainLogger,
			func(_ routes.GetUserReq, u routes.User) []slog.Attr { return []slog.Attr{slog.String("id", u.ID)} }),
	).HandleMW(&routes.ProfileScopeMw, getUserImpl.Fn).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	getUserHandle, err := getUserRoute.RegisterHandle(b)
	if err != nil {
		return nil, err
	}

	updateUserImpl := handlers.ScopesImpl[routes.UpdateUserReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *routes.UpdateUserReq) (map[string][]string, error) {
			return handlers.ExtractScopes(ctx, r, "/users/{id}")
		},
	)
	updateUserRoute := routes.UpdateUserRoute.WithHandler(
		handlers.WithDomainLogging("user.update", handlers.MakeUpdateUserHandler(store), domainLogger,
			func(req routes.UpdateUserReq, u routes.User) []slog.Attr {
				return []slog.Attr{slog.String("id", req.ID), slog.String("name", u.Name)}
			}),
	).HandleMW(&routes.AdminScopeMw, updateUserImpl.Fn).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	updateUserHandle, err := updateUserRoute.RegisterHandle(b)
	if err != nil {
		return nil, err
	}

	listUsersImpl := handlers.ScopesImpl[routes.ListUsersReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *routes.ListUsersReq) (map[string][]string, error) {
			return handlers.ExtractScopes(ctx, r, "/users")
		},
	)
	listUsersRoute := routes.ListUsersRoute.WithHandler(
		handlers.WithDomainLogging("user.list", handlers.MakeListUsersHandler(), domainLogger,
			func(_ routes.ListUsersReq, _ routes.PagedUsersResp) []slog.Attr { return nil }),
	).HandleMW(&routes.ProfileScopeMw, listUsersImpl.Fn).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := listUsersRoute.Register(b); err != nil {
		return nil, err
	}

	profileImpl := handlers.ScopesImpl[routes.ProfileReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *routes.ProfileReq) (map[string][]string, error) {
			return handlers.ExtractScopes(ctx, r, "/profile")
		},
	)
	profileRoute := routes.ProfileRoute.WithHandler(
		handlers.WithDomainLogging("user.profile", handlers.MakeProfileHandler(), domainLogger,
			func(_ routes.ProfileReq, u routes.User) []slog.Attr { return []slog.Attr{slog.String("id", u.ID)} }),
	).HandleMW(&routes.ProfileScopeMw, profileImpl.Fn).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := profileRoute.Register(b); err != nil {
		return nil, err
	}

	adminActionImpl := handlers.ScopesImpl[routes.AdminActionReq]("bearerAuth",
		func(ctx context.Context, r *http.Request, _ *routes.AdminActionReq) (map[string][]string, error) {
			return handlers.ExtractScopes(ctx, r, "/admin/action")
		},
	)
	adminActionRoute := routes.AdminActionRoute.WithHandler(
		handlers.WithDomainLogging("admin.action", handlers.MakeAdminActionHandler(), domainLogger,
			func(_ routes.AdminActionReq, r routes.AdminActionResp) []slog.Attr {
				return []slog.Attr{slog.String("result", r.Result)}
			}),
	).HandleMW(&routes.AdminScopeMw, adminActionImpl.Fn).HandleMW(nil, obsFn).HandleMW(nil, timingFn).WithOptions(opts)
	if err := adminActionRoute.Register(b); err != nil {
		return nil, err
	}

	router := gochi.NewRouter()

	// GET /openapi.yaml — a hand-rolled, manual escape hatch: go-codex has
	// no declarative "serve my own spec" convenience today (see
	// docs/roadmap/openapi-spec-endpoint.md for a captured future idea).
	// Registered BEFORE AttachRouter — chi's router cannot safely receive
	// new handlers once serving starts.
	router.Get("/openapi.yaml", specHandler(b))

	if err := chiadapter.AttachRouter(b, router, addr); err != nil {
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
