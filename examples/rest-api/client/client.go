// Package client builds the CLIENT-side half of this example — the
// "assemble" phase, client variant. It attaches, on the SAME routes/
// declarations chiserver/ and nethttpserver/ assemble server-side:
// per-identity credential-providing BoundClientMiddleware variants
// (mirrors examples/adapters-sse's securedClientRoute/securedBase
// pattern, docs/roadmap/bound-middleware-split.md), the SAME
// general-purpose timing middleware shown server-side (attached via
// ClientMW instead of HandleMW), and a rest.Client attached via
// Client.Attach(nethttp.NewClientTransport(...)) — deliberately the SAME adapters/nethttp client used to
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
)

// aliceCredFn supplies the "profile"-scoped bearer token — Alice has
// "profile" but NOT "admin".
func aliceCredFn[Req any](_ context.Context, _ Req) (routes.AuthIn, error) {
	return routes.AuthIn{Token: "valid-user-token"}, nil
}

// adminCredFn supplies the "profile"+"admin"-scoped bearer token — admin
// has both scopes (satisfies routes.ProfileScopes' requirement too,
// since the underlying token carries both).
func adminCredFn[Req any](_ context.Context, _ Req) (routes.AuthIn, error) {
	return routes.AuthIn{Token: "valid-admin-token"}, nil
}

// Build attaches httpClient+baseURL as client's rest.ClientTransport via
// Client.Attach(nethttp.NewClientTransport(...)) — the SAME adapters/nethttp client works against EITHER
// server (chi-routed or net/http-routed), proving the wire protocol is
// identical either way.
func Build(httpClient *http.Client, baseURL string) (*rest.Client, error) {
	c := rest.NewClient()
	if err := c.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: httpClient, BaseURL: baseURL})); err != nil {
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

// CreateUserRouteUnauthenticated attaches NO credential middleware at all
// — proves the server genuinely rejects an unauthenticated client.Call
// (mirrors examples/adapters-sse's securedBase negative demo).
var CreateUserRouteUnauthenticated = routes.CreateUserRoute.ClientMW(nil, routes.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger))

// CreateUserRouteAsAlice attaches aliceCredFn — Alice has "profile" but
// NOT "admin", so calls through this variant are expected to be
// rejected with 403 (wrong scope), not 401 (no credential at all).
var CreateUserRouteAsAlice = routes.CreateUserRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.CreateUserReq](routes.AdminScopes, aliceCredFn[routes.CreateUserReq])).
	ClientMW(nil, routes.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger))

// CreateUserRouteAsAdmin attaches adminCredFn — admin has both scopes,
// so calls through this variant succeed.
var CreateUserRouteAsAdmin = routes.CreateUserRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.CreateUserReq](routes.AdminScopes, adminCredFn[routes.CreateUserReq])).
	ClientMW(nil, routes.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger))

// GetUserRouteAsAlice attaches aliceCredFn — Alice has "profile", which
// is exactly what this route requires.
var GetUserRouteAsAlice = routes.GetUserRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.GetUserReq](routes.ProfileScopes, aliceCredFn[routes.GetUserReq])).
	ClientMW(nil, routes.TimingClientMW[routes.GetUserReq, routes.User](timingLogger))

// UpdateUserRouteAsAdmin attaches adminCredFn — this route requires
// "admin".
var UpdateUserRouteAsAdmin = routes.UpdateUserRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.UpdateUserReq](routes.AdminScopes, adminCredFn[routes.UpdateUserReq])).
	ClientMW(nil, routes.TimingClientMW[routes.UpdateUserReq, routes.User](timingLogger))

// ListUsersRouteAsAlice attaches aliceCredFn — this route requires
// "profile".
var ListUsersRouteAsAlice = routes.ListUsersRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.ListUsersReq](routes.ProfileScopes, aliceCredFn[routes.ListUsersReq])).
	ClientMW(nil, routes.TimingClientMW[routes.ListUsersReq, routes.PagedUsersResp](timingLogger))

// ProfileRouteAsAlice attaches aliceCredFn — this route requires
// "profile" AND layers cookie/header params on the SAME route.
var ProfileRouteAsAlice = routes.ProfileRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.ProfileReq](routes.ProfileScopes, aliceCredFn[routes.ProfileReq])).
	ClientMW(nil, routes.TimingClientMW[routes.ProfileReq, routes.User](timingLogger))

// ProfileRouteUnauthenticated attaches NO credential middleware — proves
// the server rejects an unauthenticated call to a route that ALSO has
// its own cookie/header params declared.
var ProfileRouteUnauthenticated = routes.ProfileRoute.
	ClientMW(nil, routes.TimingClientMW[routes.ProfileReq, routes.User](timingLogger))

// AdminActionRouteAsAlice attaches aliceCredFn — Alice lacks "admin", so
// calls through this variant are expected to be rejected with 403.
var AdminActionRouteAsAlice = routes.AdminActionRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.AdminActionReq](routes.AdminScopes, aliceCredFn[routes.AdminActionReq])).
	ClientMW(nil, routes.TimingClientMW[routes.AdminActionReq, routes.AdminActionResp](timingLogger))

// AdminActionRouteAsAdmin attaches adminCredFn — succeeds.
var AdminActionRouteAsAdmin = routes.AdminActionRoute.
	ClientBoundMW(routes.BoundScopeClientMW[routes.AdminActionReq](routes.AdminScopes, adminCredFn[routes.AdminActionReq])).
	ClientMW(nil, routes.TimingClientMW[routes.AdminActionReq, routes.AdminActionResp](timingLogger))

// ── GrantedScopes + ContextField demo client variants ───────────────────
//
// These supply routes.AuthIn via routes.BoundScopeClientMW's sibling,
// routes.GrantedScopesComputeClientMW (docs/roadmap/
// bound-middleware-split.md) — the bound ClientBoundMW shape
// func(ctx, req Req) (In, error), dispatched through the SAME merge-field
// mechanism AliceCredFn/AdminCredFn above use, instead of hand-building
// headers.

// computeGSCredFn returns a bound client Fn supplying token as
// routes.AuthIn.Token — GrantedScopesComputeClientMW's own declared
// WithRequestHeader merge field encodes it into the real outgoing
// Authorization header.
func computeGSCredFn(token string) func(ctx context.Context, req routes.ComputeGSReq) (routes.AuthIn, error) {
	return func(_ context.Context, _ routes.ComputeGSReq) (routes.AuthIn, error) {
		return routes.AuthIn{Token: token}, nil
	}
}

// ComputeGSRouteWithWriteScope supplies "valid-compute-token" — grants
// "compute:write", matching the route's declared requirement — expected
// to succeed.
var ComputeGSRouteWithWriteScope = routes.ComputeGSRoute.
	ClientBoundMW(routes.GrantedScopesComputeClientMW(computeGSCredFn("valid-compute-token")))

// ComputeGSRouteWithWrongScope supplies "valid-readonly-token" — a REAL,
// known credential (so the Fn itself succeeds), but gs.TokenScopes grants
// only "profile", never "compute:write" — proves middleware.CheckScopes
// rejects an insufficient GRANT, not just an invalid credential.
var ComputeGSRouteWithWrongScope = routes.ComputeGSRoute.
	ClientBoundMW(routes.GrantedScopesComputeClientMW(computeGSCredFn("valid-readonly-token")))
