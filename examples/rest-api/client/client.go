// Package client builds the CLIENT-side half of this example — the
// "assemble" phase, client variant. It attaches, on the SAME routes/
// declarations chiserver/ and nethttpserver/ assemble server-side:
// per-identity credential-providing BoundClientMiddleware variants
// (mirrors examples/adapters-sse's securedClientRoute/securedBase
// pattern, docs/design/d-0003-codec-declared-middlewares.md's Addendum 7), the SAME
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
	"github.com/DaniDeer/go-codex/examples/rest-api/auth"
	"github.com/DaniDeer/go-codex/examples/rest-api/observer"
	"github.com/DaniDeer/go-codex/examples/rest-api/requestid"
	"github.com/DaniDeer/go-codex/examples/rest-api/routes"
)

// aliceCredFn supplies the "profile"-scoped bearer token — Alice has
// "profile" but NOT "admin".
func aliceCredFn[Req any](_ context.Context, _ Req) (auth.AuthIn, error) {
	return auth.AuthIn{Token: "valid-user-token"}, nil
}

// adminCredFn supplies the "profile"+"admin"-scoped bearer token — admin
// has both scopes (satisfies auth.ProfileScopes' requirement too,
// since the underlying token carries both).
func adminCredFn[Req any](_ context.Context, _ Req) (auth.AuthIn, error) {
	return auth.AuthIn{Token: "valid-admin-token"}, nil
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

// usersRouter composes the SAME "/users" prefix chiserver/server.go's/
// nethttpserver/server.go's own rest.NewRouter("/users") Mount applies
// server-side (docs/design/d-0008-declarative-router-groups.md) —
// zero coupling to either server package's internals needed:
// [rest.WithRouter] only needs the prefix+mws, not a shared Router
// VALUE reference. CreateUserRoute/GetUserRoute/UpdateUserRoute/
// ListUsersRoute now declare RELATIVE paths ("", "/{id}") — every
// client variant below composes usersRouter via
// .ClientHandle(rest.WithRouter(usersRouter)) to reach the correct
// absolute path ("/users", "/users/{id}"), becoming a
// *rest.RouteHandle instead of a bare Route value.
var usersRouter = rest.NewRouter("/users")

// LoginRoute is auth.LoginRoute unchanged — public, no credential
// needed — with ONLY the general-purpose timing ClientMW attached.
var LoginRoute = auth.LoginRoute.ClientMW(nil, observer.TimingClientMW[auth.LoginReq, auth.TokenResp](timingLogger))

// CreateUserRouteUnauthenticated attaches NO credential middleware at all
// — proves the server genuinely rejects an unauthenticated client.Call
// (mirrors examples/adapters-sse's securedBase negative demo).
var CreateUserRouteUnauthenticated = routes.CreateUserRoute.
	ClientMW(nil, observer.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger)).
	ClientHandle(rest.WithRouter(usersRouter))

// CreateUserRouteAsAlice attaches aliceCredFn — Alice has "profile" but
// NOT "admin", so calls through this variant are expected to be
// rejected with 403 (wrong scope), not 401 (no credential at all).
var CreateUserRouteAsAlice = routes.CreateUserRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.CreateUserReq](auth.AdminScopes, aliceCredFn[routes.CreateUserReq])).
	ClientMW(nil, observer.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger)).
	ClientHandle(rest.WithRouter(usersRouter))

// CreateUserRouteAsAdmin attaches adminCredFn — admin has both scopes,
// so calls through this variant succeed.
var CreateUserRouteAsAdmin = routes.CreateUserRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.CreateUserReq](auth.AdminScopes, adminCredFn[routes.CreateUserReq])).
	ClientMW(nil, observer.TimingClientMW[routes.CreateUserReq, routes.User](timingLogger)).
	ClientHandle(rest.WithRouter(usersRouter))

// GetUserRouteAsAlice attaches aliceCredFn — Alice has "profile", which
// is exactly what this route requires.
var GetUserRouteAsAlice = routes.GetUserRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.GetUserReq](auth.ProfileScopes, aliceCredFn[routes.GetUserReq])).
	ClientMW(nil, observer.TimingClientMW[routes.GetUserReq, routes.User](timingLogger)).
	ClientHandle(rest.WithRouter(usersRouter))

// UpdateUserRouteAsAdmin attaches adminCredFn — this route requires
// "admin".
var UpdateUserRouteAsAdmin = routes.UpdateUserRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.UpdateUserReq](auth.AdminScopes, adminCredFn[routes.UpdateUserReq])).
	ClientMW(nil, observer.TimingClientMW[routes.UpdateUserReq, routes.User](timingLogger)).
	ClientHandle(rest.WithRouter(usersRouter))

// ListUsersRouteAsAlice attaches aliceCredFn — this route requires
// "profile".
var ListUsersRouteAsAlice = routes.ListUsersRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.ListUsersReq](auth.ProfileScopes, aliceCredFn[routes.ListUsersReq])).
	ClientMW(nil, observer.TimingClientMW[routes.ListUsersReq, routes.PagedUsersResp](timingLogger)).
	ClientHandle(rest.WithRouter(usersRouter))

// ProfileRouteAsAlice attaches aliceCredFn — this route requires
// "profile" AND layers cookie/header params on the SAME route.
var ProfileRouteAsAlice = routes.ProfileRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.ProfileReq](auth.ProfileScopes, aliceCredFn[routes.ProfileReq])).
	ClientMW(nil, observer.TimingClientMW[routes.ProfileReq, routes.User](timingLogger))

// ProfileRouteUnauthenticated attaches NO credential middleware — proves
// the server rejects an unauthenticated call to a route that ALSO has
// its own cookie/header params declared.
var ProfileRouteUnauthenticated = routes.ProfileRoute.
	ClientMW(nil, observer.TimingClientMW[routes.ProfileReq, routes.User](timingLogger))

// AdminActionRouteAsAlice attaches aliceCredFn — Alice lacks "admin", so
// calls through this variant are expected to be rejected with 403.
var AdminActionRouteAsAlice = routes.AdminActionRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.AdminActionReq](auth.AdminScopes, aliceCredFn[routes.AdminActionReq])).
	ClientMW(nil, observer.TimingClientMW[routes.AdminActionReq, routes.AdminActionResp](timingLogger))

// AdminActionRouteAsAdmin attaches adminCredFn — succeeds.
var AdminActionRouteAsAdmin = routes.AdminActionRoute.
	ClientBoundMW(auth.BoundScopeClientMW[routes.AdminActionReq](auth.AdminScopes, adminCredFn[routes.AdminActionReq])).
	ClientMW(nil, observer.TimingClientMW[routes.AdminActionReq, routes.AdminActionResp](timingLogger))

// ── GrantedScopes + ContextField demo client variants ───────────────────
//
// These supply auth.AuthIn via auth.BoundScopeClientMW's sibling,
// auth.GrantedScopesComputeClientMW (docs/design/d-0003-codec-
// declared-middlewares.md's Addendum 7) — the bound ClientBoundMW shape
// func(ctx, req Req) (In, error), dispatched through the SAME merge-field
// mechanism AliceCredFn/AdminCredFn above use, instead of hand-building
// headers.

// computeGSCredFn returns a bound client Fn supplying token as
// auth.AuthIn.Token — GrantedScopesComputeClientMW's own declared
// WithRequestHeader merge field encodes it into the real outgoing
// Authorization header.
func computeGSCredFn(token string) func(ctx context.Context, req auth.ComputeGSReq) (auth.AuthIn, error) {
	return func(_ context.Context, _ auth.ComputeGSReq) (auth.AuthIn, error) {
		return auth.AuthIn{Token: token}, nil
	}
}

// ComputeGSRouteWithWriteScope supplies "valid-compute-token" — grants
// "compute:write", matching the route's declared requirement — expected
// to succeed.
var ComputeGSRouteWithWriteScope = auth.ComputeGSRoute.
	ClientBoundMW(auth.GrantedScopesComputeClientMW(computeGSCredFn("valid-compute-token")))

// ComputeGSRouteWithWrongScope supplies "valid-readonly-token" — a REAL,
// known credential (so the Fn itself succeeds), but gs.TokenScopes grants
// only "profile", never "compute:write" — proves middleware.CheckScopes
// rejects an insufficient GRANT, not just an invalid credential.
var ComputeGSRouteWithWrongScope = auth.ComputeGSRoute.
	ClientBoundMW(auth.GrantedScopesComputeClientMW(computeGSCredFn("valid-readonly-token")))

// ── Bound-middleware-split demo (docs/design/d-0003-codec-declared-middlewares.md's Addendum 7) ──

// ReusableAloneRoute attaches ONLY the reusable class (.Use()) — no
// credential middleware at all, since routes.ReusableAloneRoute declares
// no security requirement.
var ReusableAloneRoute = routes.ReusableAloneRoute.Use(requestid.ReusableRequestIDMw)

// StackedDemoRouteAsAlice attaches BOTH classes together: .Use(reusable)
// (generic request-ID logging) then .ClientBoundMW(bound) (supplies the
// "profile"-scoped credential) — Alice has "profile", so this is expected
// to succeed.
var StackedDemoRouteAsAlice = routes.StackedDemoRoute.
	Use(requestid.ReusableRequestIDMw).
	ClientBoundMW(auth.BoundScopeClientMW[routes.StackedDemoReq](auth.ProfileScopes, aliceCredFn[routes.StackedDemoReq]))
