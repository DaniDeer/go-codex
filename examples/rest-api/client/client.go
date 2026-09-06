// Package client builds the CLIENT-side half of this example — the
// "assemble" phase, client variant. It attaches, on the SAME routes/
// declarations chiserver/ and nethttpserver/ assemble server-side:
// per-identity credential-providing ClientMW variants (mirrors
// examples/adapters-sse's securedRoute/securedBase pattern), the SAME
// general-purpose timing middleware shown server-side (attached via
// ClientMW instead of HandleMW), and a rest.Client attached via
// nethttp.Attach — deliberately the SAME adapters/nethttp client used to
// call BOTH the chi-routed AND the net/http-routed server, since a
// declared rest.Route and its rest.Client are transport-agnostic on the
// wire: chi is just an http.Handler-producing router underneath.
package client

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
	"github.com/DaniDeer/go-codex/route"
)

// AliceCredFn supplies the "profile"-scoped bearer token — paired against
// routes.ProfileScopeMw. Mirrors adapters/nethttp.CredentialFunc's shape.
func AliceCredFn(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
	h := make(http.Header)
	h.Set("Authorization", "Bearer valid-user-token")
	return h, nil
}

// AdminCredFn supplies the "profile"+"admin"-scoped bearer token — paired
// against routes.AdminScopeMw (also satisfies routes.ProfileScopeMw's
// requirement, since the underlying token carries both scopes).
func AdminCredFn(_ context.Context, _ []route.SecurityRequirement) (http.Header, error) {
	h := make(http.Header)
	h.Set("Authorization", "Bearer valid-admin-token")
	return h, nil
}

// Build attaches httpClient+baseURL as client's rest.ClientTransport via
// nethttp.Attach — the SAME adapters/nethttp client works against EITHER
// server (chi-routed or net/http-routed), proving the wire protocol is
// identical either way.
func Build(httpClient *http.Client, baseURL string) (*rest.Client, error) {
	c := rest.NewClient()
	if err := nethttp.Attach(c, httpClient, baseURL); err != nil {
		return nil, err
	}
	return c, nil
}

// timingLogger is shared by every ClientMW-attached general-purpose
// timing variant below.
var timingLogger = slog.Default().With("component", "client")

// LoginRoute is routes.LoginRoute unchanged — public, no credential
// needed — with ONLY the general-purpose timing ClientMW attached.
var LoginRoute = routes.LoginRoute.ClientMW(nil, routes.TimingClientMW[routes.LoginReq, routes.TokenResp](timingLogger))

// CreateUserRouteUnauthenticated attaches NO credential ClientMW at all —
// proves the server genuinely rejects an unauthenticated client.Call
// (mirrors examples/adapters-sse's securedBase negative demo).
var CreateUserRouteUnauthenticated = routes.CreateUserRoute.ClientMW(nil, routes.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger))

// CreateUserRouteAsAlice attaches AliceCredFn — Alice has "profile" but
// NOT "admin", so calls through this variant are expected to be
// rejected with 403 (wrong scope), not 401 (no credential at all).
var CreateUserRouteAsAlice = routes.CreateUserRoute.
	ClientMW(&routes.AdminScopeMw, AliceCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger))

// CreateUserRouteAsAdmin attaches AdminCredFn — admin has both scopes,
// so calls through this variant succeed.
var CreateUserRouteAsAdmin = routes.CreateUserRoute.
	ClientMW(&routes.AdminScopeMw, AdminCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger))

// GetUserRouteAsAlice attaches AliceCredFn — Alice has "profile", which
// is exactly what this route requires.
var GetUserRouteAsAlice = routes.GetUserRoute.
	ClientMW(&routes.ProfileScopeMw, AliceCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.GetUserReq, routes.User](timingLogger))

// UpdateUserRouteAsAdmin attaches AdminCredFn — this route requires
// "admin".
var UpdateUserRouteAsAdmin = routes.UpdateUserRoute.
	ClientMW(&routes.AdminScopeMw, AdminCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.UpdateUserReq, routes.User](timingLogger))

// ListUsersRouteAsAlice attaches AliceCredFn — this route requires
// "profile".
var ListUsersRouteAsAlice = routes.ListUsersRoute.
	ClientMW(&routes.ProfileScopeMw, AliceCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.ListUsersReq, routes.PagedUsersResp](timingLogger))

// ProfileRouteAsAlice attaches AliceCredFn — this route requires
// "profile" AND layers cookie/header params on the SAME route.
var ProfileRouteAsAlice = routes.ProfileRoute.
	ClientMW(&routes.ProfileScopeMw, AliceCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.ProfileReq, routes.User](timingLogger))

// ProfileRouteUnauthenticated attaches NO credential ClientMW — proves
// the server rejects an unauthenticated call to a route that ALSO has
// its own cookie/header params declared.
var ProfileRouteUnauthenticated = routes.ProfileRoute.
	ClientMW(nil, routes.TimingClientMW[routes.ProfileReq, routes.User](timingLogger))

// AdminActionRouteAsAlice attaches AliceCredFn — Alice lacks "admin", so
// calls through this variant are expected to be rejected with 403.
var AdminActionRouteAsAlice = routes.AdminActionRoute.
	ClientMW(&routes.AdminScopeMw, AliceCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.AdminActionReq, routes.AdminActionResp](timingLogger))

// AdminActionRouteAsAdmin attaches AdminCredFn — succeeds.
var AdminActionRouteAsAdmin = routes.AdminActionRoute.
	ClientMW(&routes.AdminScopeMw, AdminCredFn).
	ClientMW(nil, routes.TimingClientMW[routes.AdminActionReq, routes.AdminActionResp](timingLogger))
