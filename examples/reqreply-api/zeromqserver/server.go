// Package zeromqserver assembles routes/+handlers/ onto in-process mock ZMQ
// REQ/REP socket pairs via zeromq.NewServerTransport + Server.Attach — the reqreply.Server/
// Client+Attach counterpart for the REQ/REP socket family. Registers 3
// routes (ComputeRoute, DoubleRoute, TripleRoute) against ONE Server so
// Demo 4 (concurrent multi-route dispatch) can exercise them all
// simultaneously — the real-world demonstration of the concurrent-dispatch
// fix a BLOCKING transport like zeromq's motivated (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Decision 1).
package zeromqserver

import (
	"time"

	"github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/auth"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/handlers"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/propertyaxis"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/stats"
)

// Built bundles the assembled Server with the REQ-side sockets a caller's
// Client needs to Attach (via zeromq.NewClientTransport) against.
type Built struct {
	Server        *reqreply.Server
	ClientSockets map[string]zeromq.FramedSocket
	// OAuthHandle is the registered *reqreply.RouteHandle for
	// routes.OAuthComputeRoute — needed by Demo 9's cross-API OAuth2
	// sharing demo to print the route's own AsyncAPI-contributed
	// security scheme entry.
	OAuthHandle *reqreply.RouteHandle[routes.OAuthComputeReq, routes.OAuthComputeResp]
	// PropertyAxisHandle is the registered *reqreply.RouteHandle for
	// routes.PropertyAxisComputeRoute — the SAME route+
	// routes.TenantPropertyMw+handlers.ProcessTenant triple
	// mqtt5server.Build also registers, proving the declaration is
	// genuinely transport-agnostic (see demo_property_axis_middleware.go).
	PropertyAxisHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]
}

// Build registers ComputeRoute/DoubleRoute/TripleRoute against a fresh
// reqreply.Server, then attaches it (via zeromq.NewServerTransport) to 3 independent
// in-process REQ/REP socket pairs (one pair per route — REQ/REP is
// point-to-point, unlike pub/sub's topic-multiplexed SUB socket).
//
// Every route additionally attaches the shipped
// [reqreply.Observability] via .HandleMW(nil, ...) (UNPAIRED,
// general-purpose — no [routes.middleware.Declaration] needed, see
// routes/middleware.go's doc comment) — demonstrating the declare-time
// attachment alternative to relying solely on the ctx-ambient Observer
// main.go injects via [stats.WithObserver] before calling
// [reqreply.Server.Serve].
func Build(obs stats.Observer) (*Built, error) {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API (zeromq REQ/REP)", Version: "1.0.0"})
	server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5556", Protocol: "zmq"})

	// EVERY route below shares the literal "compute/" topic prefix —
	// grouped under ONE REAL docs/design/d-0008-declarative-router-groups.md
	// Mount (`reqreply.NewRouter("compute")`), composing each leaf's own
	// RELATIVE topic ("add", "double", ...) back to the SAME,
	// byte-identical absolute topic ("compute/add", "compute/double",
	// ...) this server has always registered. mqtt5server.Build mounts a
	// SEPARATE but consistent "compute" Router over ITS OWN subset of
	// these same routes (ComputeRoute/PropertyAxisComputeRoute are
	// registered on BOTH servers) — see that file's own doc comment.
	var oauthHandle *reqreply.RouteHandle[routes.OAuthComputeReq, routes.OAuthComputeResp]
	var propertyAxisHandle *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]

	computeRoute := routes.ComputeRoute.
		HandleMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs)).
		WithHandler(handlers.Add)
	doubleRoute := routes.DoubleRoute.
		HandleMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs)).
		WithHandler(handlers.Double)
	tripleRoute := routes.TripleRoute.
		HandleMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs)).
		WithHandler(handlers.Triple)
	// OAuthComputeRoute demonstrates zeromq's reqreply in-payload
	// credential model — the BOUND class (docs/design/d-0003-codec-
	// declared-middlewares.md's Addendum 7, Phase C), since zeromq has no
	// property/header side channel to carry a credential merge field —
	// AND the oauth2Compute scheme ALSO declared for REST from the SAME
	// shared route.SecurityScheme config — see Demo 9
	// (demo_cross_api_oauth2_sharing.go) and auth.NewOAuthMwReqreply/
	// auth.OAuthMwREST's doc comments. The general-purpose observer
	// HandleMW(nil, ...) attaches ALONGSIDE the bound security
	// HandleBoundMW(...) below — proving the two mechanisms compose
	// freely on the same route.
	oauthRoute := routes.OAuthComputeRoute.
		HandleBoundMW(auth.NewOAuthMwReqreply(auth.VerifyOAuthComputeZeroMQ)).
		HandleMW(nil, reqreply.Observability[routes.OAuthComputeReq, routes.OAuthComputeResp](obs)).
		WithHandler(handlers.AddOAuth).
		WithOpt(reqreply.WithHandleCallback(func(h *reqreply.RouteHandle[routes.OAuthComputeReq, routes.OAuthComputeResp]) {
			oauthHandle = h
		}))
	// PropertyAxisComputeRoute — the SAME propertyaxis.NewTenantPropertyMw
	// (declaration) + propertyaxis.ProcessTenant (implementation),
	// bundled together in the propertyaxis/ package, attached via
	// .HandleMW in mqtt5server.Build, registered here UNCHANGED against
	// a transport with NO property mechanism at all. zeromq's REQ/REP
	// frames carry only [status, payload] — there is no side channel to
	// carry a User-Property-equivalent value, so TenantIn.TenantID is
	// always absent here; NewTenantPropertyMw deliberately declares that
	// property OPTIONAL (see propertyaxis/middleware.go) specifically so
	// THIS registration succeeds rather than every zeromq call failing
	// with reqreply.MiddlewareInputError.
	propertyAxisRoute := routes.PropertyAxisComputeRoute.
		HandleBoundMW(propertyaxis.NewTenantPropertyMw(propertyaxis.ProcessTenant)).
		HandleMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs)).
		WithHandler(handlers.Add).
		WithOpt(reqreply.WithHandleCallback(func(h *reqreply.RouteHandle[routes.ComputeReq, routes.ComputeResp]) {
			propertyAxisHandle = h
		}))
	// auth.ComputeGSRoute demonstrates the GENERALIZED reqreply.
	// SecurityMiddleware[In, Out] + GrantedScopes + ContextField
	// mechanism (docs/design/d-0007-declarative-middleware-layering.md)
	// — bound HandleMW directly, NO separate legacy credential Fn
	// needed; auth.VerifyBearerGS returns a GrantedScopes-carrying
	// auth.GrantedScopesAuthOut. This is an AUTH-FLOW demo route (lives
	// in auth/, not routes/) — still Router-groupable alongside
	// routes/-declared leaves in the SAME Mount, since Router doesn't
	// care which package a leaf comes from.
	gsRoute := auth.ComputeGSRoute.
		HandleBoundMW(auth.NewGrantedScopesComputeMw(auth.VerifyBearerGS)).
		HandleMW(nil, reqreply.Observability[auth.ComputeGSReq, routes.ComputeResp](obs)).
		WithHandler(auth.MakeComputeGSHandler())

	computeRouter := reqreply.NewRouter("compute").
		Tags("compute").
		Route(computeRoute).
		Route(doubleRoute).
		Route(tripleRoute).
		Route(oauthRoute).
		Route(propertyAxisRoute).
		Route(gsRoute)
	if err := computeRouter.Register(server); err != nil {
		return nil, err
	}

	addRep, addReq := newChanSocketPair()
	doubleRep, doubleReq := newChanSocketPair()
	tripleRep, tripleReq := newChanSocketPair()
	oauthRep, oauthReq := newChanSocketPair()
	propertyAxisRep, propertyAxisReq := newChanSocketPair()
	gsRep, gsReq := newChanSocketPair()

	serverSockets := map[string]zeromq.FramedSocket{
		"compute/add":               addRep,
		"compute/double":            doubleRep,
		"compute/triple":            tripleRep,
		"compute/oauth-add":         oauthRep,
		"compute/property-axis-add": propertyAxisRep,
		"compute/gs":                gsRep,
	}
	if err := server.Attach(zeromq.NewServerTransport(zeromq.ServerTransportOptions{Sockets: serverSockets})); err != nil {
		return nil, err
	}

	clientSockets := map[string]zeromq.FramedSocket{
		"compute/add":               addReq,
		"compute/double":            doubleReq,
		"compute/triple":            tripleReq,
		"compute/oauth-add":         oauthReq,
		"compute/property-axis-add": propertyAxisReq,
		"compute/gs":                gsReq,
	}
	return &Built{
		Server:             server,
		ClientSockets:      clientSockets,
		OAuthHandle:        oauthHandle,
		PropertyAxisHandle: propertyAxisHandle,
	}, nil
}

// ── in-process chanSocket pair (self-contained — no real ZMQ library needed) ─
//
// Mirrors adapters/zeromq's own reqreply_transport_test.go chanSocket
// exactly (kept here, duplicated, rather than exported from that
// internal test file).

type chanSocket struct {
	peer    *chanSocket
	in      chan [][]byte
	timeout time.Duration
}

func newChanSocketPair() (*chanSocket, *chanSocket) {
	a := &chanSocket{in: make(chan [][]byte, 8), timeout: 100 * time.Millisecond}
	b := &chanSocket{in: make(chan [][]byte, 8), timeout: 100 * time.Millisecond}
	a.peer = b
	b.peer = a
	return a, b
}

func cloneFrames(frames [][]byte) [][]byte {
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	return cp
}

func (c *chanSocket) SendFrames(frames [][]byte) error {
	c.peer.in <- cloneFrames(frames)
	return nil
}

func (c *chanSocket) RecvFrames() ([][]byte, error) {
	select {
	case f := <-c.in:
		return f, nil
	case <-time.After(c.timeout):
		return nil, zeromq.ErrTimeout
	}
}

func (c *chanSocket) SetSubscription(string) error { return nil }

func (c *chanSocket) SetRecvTimeout(d time.Duration) error {
	c.timeout = d
	return nil
}

var _ zeromq.FramedSocket = (*chanSocket)(nil)
