// Package zeromqrouterserver assembles routes.RouterComputeRoute onto an
// in-process mock ZMQ ROUTER/DEALER socket pair via
// zeromq.AttachRouterServer — the DEALER/ROUTER socket-topology variant
// carried over from the deleted examples/adapters-zeromq-dealer-router.
package zeromqrouterserver

import (
	"errors"
	"time"

	"github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/handlers"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
	"github.com/DaniDeer/go-codex/stats"
)

// Built bundles the assembled Server with the DEALER-side socket a caller's
// Client needs to zeromq.AttachDealerClient against.
type Built struct {
	Server       *reqreply.Server
	ClientSocket zeromq.FramedSocket
}

// BuildWithMissingSocket registers BOTH routes.RouterComputeRoute AND
// routes.MissingSocketRoute against a fresh reqreply.Server, but wires a
// socket for ONLY routes.RouterComputeRoute — deliberately leaving
// MissingSocketRoute uncovered, to demonstrate zeromq.AttachRouterServer's
// upfront [zeromq.MissingSocketError] (returned at Attach time, before
// [reqreply.Server.Serve] ever runs).
func BuildWithMissingSocket() (*reqreply.Server, error) {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API (zeromq ROUTER/DEALER)", Version: "1.0.0"})
	server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5557", Protocol: "zmq"})

	if _, err := routes.RouterComputeRoute.WithHandler(handlers.Add).Register(server); err != nil {
		return nil, err
	}
	if _, err := routes.MissingSocketRoute.WithHandler(handlers.Add).Register(server); err != nil {
		return nil, err
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	_ = dealerSock
	incompleteSockets := map[string]zeromq.FramedSocket{
		"compute/router-add": routerSock, // MissingSocketRoute's topic has no entry
	}
	if err := zeromq.AttachRouterServer(server, incompleteSockets); err != nil {
		return nil, err // expected: zeromq.MissingSocketError
	}
	return server, errors.New("expected MissingSocketError, got none")
}

// Build registers ONLY routes.RouterComputeRoute (the working, complete
// configuration used by the rest of Demo 8) against a fresh reqreply.Server,
// then zeromq.AttachRouterServer's it to an in-process ROUTER/DEALER socket
// pair. Also attaches the shipped [reqreply.Observability] via
// .HandleMW(nil, ...) — mirrors zeromqserver.Build's identical
// declare-time Observer attachment (see its doc comment).
func Build(obs stats.Observer) (*Built, error) {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API (zeromq ROUTER/DEALER)", Version: "1.0.0"})
	server.AddServer("zmq", reqreply.ServerEntry{URL: "tcp://localhost:5557", Protocol: "zmq"})

	if _, err := routes.RouterComputeRoute.
		HandleMW(nil, reqreply.Observability[routes.ComputeReq, routes.ComputeResp](obs)).
		WithHandler(handlers.Add).
		Register(server); err != nil {
		return nil, err
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	sockets := map[string]zeromq.FramedSocket{"compute/router-add": routerSock}
	if err := zeromq.AttachRouterServer(server, sockets); err != nil {
		return nil, err
	}
	return &Built{Server: server, ClientSocket: dealerSock}, nil
}

// ── in-process dealerSocket/routerSocket pair (self-contained) ─────────────
//
// Mirrors adapters/zeromq's own reqreply_transport_test.go
// dealerSocket/routerSocket pair exactly (kept here, duplicated).

type dealerSocket struct {
	peer     *routerSocket
	in       chan [][]byte
	timeout  time.Duration
	identity []byte
}

type routerSocket struct {
	peer    *dealerSocket
	in      chan [][]byte
	timeout time.Duration
}

func newDealerRouterPair(identity []byte) (*dealerSocket, *routerSocket) {
	d := &dealerSocket{in: make(chan [][]byte, 8), timeout: 100 * time.Millisecond, identity: identity}
	r := &routerSocket{in: make(chan [][]byte, 8), timeout: 100 * time.Millisecond}
	d.peer = r
	r.peer = d
	return d, r
}

func cloneFrames(frames [][]byte) [][]byte {
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	return cp
}

func (d *dealerSocket) SendFrames(frames [][]byte) error {
	full := append([][]byte{append([]byte{}, d.identity...)}, cloneFrames(frames)...)
	d.peer.in <- full
	return nil
}

func (d *dealerSocket) RecvFrames() ([][]byte, error) {
	select {
	case f := <-d.in:
		return f, nil
	case <-time.After(d.timeout):
		return nil, zeromq.ErrTimeout
	}
}

func (d *dealerSocket) SetSubscription(string) error { return nil }

func (d *dealerSocket) SetRecvTimeout(dur time.Duration) error {
	d.timeout = dur
	return nil
}

func (r *routerSocket) SendFrames(frames [][]byte) error {
	if len(frames) < 1 {
		return errors.New("router: empty frames")
	}
	r.peer.in <- cloneFrames(frames[1:])
	return nil
}

func (r *routerSocket) RecvFrames() ([][]byte, error) {
	select {
	case f := <-r.in:
		return f, nil
	case <-time.After(r.timeout):
		return nil, zeromq.ErrTimeout
	}
}

func (r *routerSocket) SetSubscription(string) error { return nil }

func (r *routerSocket) SetRecvTimeout(dur time.Duration) error {
	r.timeout = dur
	return nil
}

var _ zeromq.FramedSocket = (*dealerSocket)(nil)
var _ zeromq.FramedSocket = (*routerSocket)(nil)
