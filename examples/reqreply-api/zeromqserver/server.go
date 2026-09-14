// Package zeromqserver assembles routes/+handlers/ onto in-process mock ZMQ
// REQ/REP socket pairs via zeromq.AttachServer — the reqreply.Server/
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
	"github.com/DaniDeer/go-codex/examples/reqreply-api/handlers"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// Built bundles the assembled Server with the REQ-side sockets a caller's
// Client needs to zeromq.AttachClient against.
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
// reqreply.Server, then zeromq.AttachServer's it to 3 independent
// in-process REQ/REP socket pairs (one pair per route — REQ/REP is
// point-to-point, unlike pub/sub's topic-multiplexed SUB socket).
func Build() (*Built, error) {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API (zeromq REQ/REP)", Version: "1.0.0"})
	server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5556", Protocol: "zmq"})

	if _, err := routes.ComputeRoute.WithHandler(handlers.Add).Register(server); err != nil {
		return nil, err
	}
	if _, err := routes.DoubleRoute.WithHandler(handlers.Double).Register(server); err != nil {
		return nil, err
	}
	if _, err := routes.TripleRoute.WithHandler(handlers.Triple).Register(server); err != nil {
		return nil, err
	}
	// OAuthComputeRoute demonstrates zeromq's reqreply security Fn-shape
	// (docs/roadmap/zeromq-security.md, SHIPPED) AND the SAME OAuthMw
	// declaration shared across REST/reqreply — see Demo 9
	// (demo_cross_api_oauth2_sharing.go).
	oauthHandle, err := routes.OAuthComputeRoute.
		Use(routes.OAuthMw).
		HandleMW(&routes.OAuthMw, handlers.VerifyOAuthComputeZeroMQ).
		WithHandler(handlers.AddOAuth).
		Register(server)
	if err != nil {
		return nil, err
	}
	// PropertyAxisComputeRoute — the SAME routes.TenantPropertyMw
	// (declaration) + handlers.ProcessTenant (implementation) attached
	// via reqreply.Transform in mqtt5server.Build, registered here
	// UNCHANGED against a transport with NO property mechanism at all.
	// zeromq's REQ/REP frames carry only [status, payload] — there is
	// no side channel to carry a User-Property-equivalent value, so
	// TenantIn.TenantID is always absent here; TenantPropertyMw
	// deliberately declares that property OPTIONAL (see
	// routes/middleware.go) specifically so THIS registration succeeds
	// rather than every zeromq call failing with
	// reqreply.MiddlewareInputError.
	propertyAxisHandle, err := reqreply.Transform(
		routes.PropertyAxisComputeRoute,
		routes.TenantPropertyMw,
		handlers.ProcessTenant,
	).
		WithHandler(handlers.Add).
		Register(server)
	if err != nil {
		return nil, err
	}

	addRep, addReq := newChanSocketPair()
	doubleRep, doubleReq := newChanSocketPair()
	tripleRep, tripleReq := newChanSocketPair()
	oauthRep, oauthReq := newChanSocketPair()
	propertyAxisRep, propertyAxisReq := newChanSocketPair()

	serverSockets := map[string]zeromq.FramedSocket{
		"compute/add":               addRep,
		"compute/double":            doubleRep,
		"compute/triple":            tripleRep,
		"compute/oauth-add":         oauthRep,
		"compute/property-axis-add": propertyAxisRep,
	}
	if err := zeromq.AttachServer(server, serverSockets); err != nil {
		return nil, err
	}

	clientSockets := map[string]zeromq.FramedSocket{
		"compute/add":               addReq,
		"compute/double":            doubleReq,
		"compute/triple":            tripleReq,
		"compute/oauth-add":         oauthReq,
		"compute/property-axis-add": propertyAxisReq,
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
