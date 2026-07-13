// Package adapters-zeromq-dealer-router demonstrates the ZeroMQ DEALER/ROUTER
// adapter pattern using go-codex's api/rest route declarations.
//
// Unlike REQ/REP (strict alternation), DEALER/ROUTER supports concurrent
// requests: the ROUTER server handles each request in its own goroutine, and
// the DEALER client can issue multiple requests without waiting for replies.
//
// This example uses in-process mock sockets to avoid requiring libzmq.
// In production, replace dealerSock/routerSock with pebbe/zmq4 sockets:
//
//	routerSock, _ := zmq.NewSocket(zmq.ROUTER)
//	routerSock.Bind("tcp://*:5557")
//	dealerSock, _ := zmq.NewSocket(zmq.DEALER)
//	dealerSock.Connect("tcp://localhost:5557")
//
// ROUTER message framing (server receives): [identity, "", payload]
// ROUTER reply framing (server sends):      [identity, "", "ok", response]
// DEALER send framing (client sends):       ["", payload]
// DEALER recv framing (client receives):    ["", "ok", response]
//
// The ROUTER automatically echoes the identity frame back to the sender.
// The DEALER prepends an empty delimiter frame for ROUTER compatibility.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// ── Layer 1: domain types and codecs ─────────────────────────────────────────

type ComputeReq struct{ X, Y int }
type ComputeResp struct{ Sum int }

var computeReqCodec = codex.Struct[ComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r ComputeReq) int { return r.X },
		func(r *ComputeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r ComputeReq) int { return r.Y },
		func(r *ComputeReq, v int) { r.Y = v },
	),
)

var computeRespCodec = codex.Struct[ComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r ComputeResp) int { return r.Sum },
		func(r *ComputeResp, v int) { r.Sum = v },
	),
)

// ── Layer 2: route declaration ────────────────────────────────────────────────

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "compute", Summary: "Add two integers."},
)

// ── in-process ROUTER/DEALER socket pair ─────────────────────────────────────
//
// routerPipe simulates a ROUTER socket. Each message it receives is expected
// to be [identity, "", payload]. Replies are sent as [identity, "", status, resp].
//
// dealerPipe simulates a DEALER socket. It sends ["", payload] and receives
// ["", status, resp].

type routerPipe struct {
	// inbox receives messages from the dealer: [identity, "", payload]
	inbox chan [][]byte
	// outbox receives replies from the router: [identity, "", status, resp]
	outbox chan [][]byte
}

type dealerPipe struct {
	identity []byte
	inbox    chan [][]byte // receives from routerPipe.outbox
	outbox   chan [][]byte // sends to routerPipe.inbox
}

func newRouterDealerPair(identity string) (*routerPipe, *dealerPipe) {
	inbox := make(chan [][]byte, 16)
	outbox := make(chan [][]byte, 16)
	router := &routerPipe{inbox: inbox, outbox: outbox}
	dealer := &dealerPipe{identity: []byte(identity), inbox: outbox, outbox: inbox}
	return router, dealer
}

// routerPipe implements FramedSocket (ROUTER side).
func (r *routerPipe) SendFrames(frames [][]byte) error {
	cp := copyFrames(frames)
	r.outbox <- cp
	return nil
}
func (r *routerPipe) RecvFrames() ([][]byte, error) {
	select {
	case f := <-r.inbox:
		return f, nil
	case <-time.After(100 * time.Millisecond):
		return nil, zeromq.ErrTimeout
	}
}
func (r *routerPipe) SetSubscription(_ string) error       { return nil }
func (r *routerPipe) SetRecvTimeout(_ time.Duration) error { return nil }

// dealerPipe implements FramedSocket (DEALER side).
// CallDealer already sends ["", payload] — the empty delimiter is in the frames.
// We prepend the identity so ROUTER sees [identity, "", payload].
// On receive, ROUTER sends [identity, "", status, resp]; we strip the identity
// so CallDealer sees ["", status, resp].
func (d *dealerPipe) SendFrames(frames [][]byte) error {
	// frames = ["", payload] from CallDealer
	// ROUTER expects [identity, "", payload] → prepend identity only
	msg := make([][]byte, 0, 1+len(frames))
	msg = append(msg, d.identity)
	msg = append(msg, copyFrames(frames)...)
	d.outbox <- msg
	return nil
}
func (d *dealerPipe) RecvFrames() ([][]byte, error) {
	select {
	case f := <-d.inbox:
		// ROUTER sent [identity, "", status, resp] → strip identity → ["", status, resp]
		if len(f) < 2 {
			return f, nil
		}
		return f[1:], nil
	case <-time.After(100 * time.Millisecond):
		return nil, zeromq.ErrTimeout
	}
}
func (d *dealerPipe) SetSubscription(_ string) error       { return nil }
func (d *dealerPipe) SetRecvTimeout(_ time.Duration) error { return nil }

func copyFrames(frames [][]byte) [][]byte {
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	return cp
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	obs := stats.NoopObserver{}

	// Register with reqreply.Builder for AsyncAPI spec.
	// obs is stored in the context once — all adapter calls resolve it automatically.
	zmqBuilder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API (DEALER/ROUTER)", Version: "1.0.0"})
	zmqBuilder.AddServer("zmq", reqreply.Server{URL: "tcp://localhost:5557", Protocol: "zmq"})
	// Route.Register(builder) — consistent with rest.Route.Register and events.Channel.Register.
	serverHandle, err := ComputeRoute.Register(zmqBuilder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	ctx = stats.WithObserver(ctx, obs) // default observer for all adapter calls
	defer cancel()

	// In-process ROUTER/DEALER pairs (one per client).
	router, dealer1 := newRouterDealerPair("client-1")
	_, dealer2 := newRouterDealerPair("client-2")
	// dealer2 shares the router's inbox/outbox (same router, two clients).
	// For simplicity, use separate router pipes per client in this demo.
	router2, _ := newRouterDealerPair("client-2")
	_ = dealer2
	_ = router2

	// ROUTER server — handles all clients concurrently.
	go func() {
		if err := zeromq.ServeRouter(ctx, router, serverHandle,
			func(_ context.Context, req ComputeReq) (ComputeResp, error) {
				// Simulate some work.
				time.Sleep(10 * time.Millisecond)
				return ComputeResp{Sum: req.X + req.Y}, nil
			},
			zeromq.ServeOptions{ // Observer resolved from ctx
				OnError: func(e zeromq.ServeError) {
					fmt.Fprintf(os.Stderr, "serve error: %v\n", e)
				},
			},
		); err != nil {
			fmt.Fprintf(os.Stderr, "serve stopped: %v\n", err)
		}
	}()

	// DEALER client — serial calls (each call is still concurrent-safe when
	// called from separate goroutines with independent sockets).
	// Note: sharing one DEALER socket across goroutines requires external
	// synchronization; per-goroutine sockets are the idiomatic pattern.
	clientHandle := ComputeRoute.ClientHandle()
	reqs := []ComputeReq{{X: 3, Y: 4}, {X: 10, Y: 20}, {X: -5, Y: 5}}

	for _, req := range reqs {
		resp, err := zeromq.CallDealer(ctx, dealer1, clientHandle, req,
			zeromq.CallOptions{}) // observer from ctx
		if err != nil {
			fmt.Fprintf(os.Stderr, "call error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("compute(%d + %d) = %d\n", req.X, req.Y, resp.Sum)
	}

	// Print AsyncAPI spec.
	doc, err := zmqBuilder.AsyncAPISpec()
	if err != nil {
		fmt.Fprintf(os.Stderr, "spec: %v\n", err)
		os.Exit(1)
	}
	specYAML, err := doc.MarshalYAML()
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal spec: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("\n── AsyncAPI spec (zmq dealer/router) ──")
	fmt.Println(string(specYAML))
}
