// Package adapters-zeromq-reqrep demonstrates the ZeroMQ REQ/REP adapter using
// go-codex's api/rest route declarations.
//
// The example wires together a Serve (REP server) and Call (REQ client) in-process
// using a mock FramedSocket to avoid requiring a real libzmq installation. In
// production, replace pipeSocketPair with the pebbe/zmq4 wrapper shown in
// docs/guides/zeromq.md.
//
// Pattern: REQ/REP compute service
//
//	ComputeReq / ComputeResp codecs  (Layer 1 — domain types)
//	rest.NewRoute                    (Layer 2 — route contract)
//	zeromq.Serve                     (Layer 3 — ZMQ REP socket adapter)
//	zeromq.Call                      (Layer 3 — ZMQ REQ socket adapter)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/stats"
)

// ── Layer 1: domain types and codecs ─────────────────────────────────────────

type ComputeReq struct {
	X int
	Y int
}

type ComputeResp struct {
	Sum int
}

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
//
// reqreply.NewRoute declares a typed request-reply contract.
// No HTTP method — just a topic/address. The same Route works with
// any request-reply adapter (ZMQ, MQTT 5, etc.).

var ComputeRoute = reqreply.NewRoute[ComputeReq, ComputeResp](
	"compute/add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{
		OperationID: "compute",
		Summary:     "Add two integers.",
	},
)

// ── in-process mock socket pair (replaces pebbe/zmq4 in this demo) ───────────

// halfPipe is one side of a bidirectional in-process pipe.
// A pair of halfPipes simulates a ZMQ REQ ↔ REP socket pair.
type halfPipe struct {
	out chan [][]byte // frames written by this side
	in  chan [][]byte // frames read by this side
}

func newSocketPair() (req *halfPipe, rep *halfPipe) {
	a := make(chan [][]byte, 4)
	b := make(chan [][]byte, 4)
	return &halfPipe{out: a, in: b}, &halfPipe{out: b, in: a}
}

func (p *halfPipe) SendFrames(frames [][]byte) error {
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	p.out <- cp
	return nil
}

func (p *halfPipe) RecvFrames() ([][]byte, error) {
	select {
	case frames := <-p.in:
		return frames, nil
	case <-time.After(100 * time.Millisecond):
		return nil, zeromq.ErrTimeout
	}
}

func (p *halfPipe) SetSubscription(_ string) error       { return nil }
func (p *halfPipe) SetRecvTimeout(_ time.Duration) error { return nil }

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	obs := stats.NoopObserver{}

	// Layer 2: register route with the ZMQ builder to get an AsyncAPI spec.
	// The returned handle is a plain *rest.RouteHandle — identical to Phase 1.
	zmqBuilder := reqreply.NewBuilder(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	zmqBuilder.AddServer("zmq", reqreply.Server{
		URL:         "tcp://localhost:5556",
		Protocol:    "zmq",
		Description: "ZeroMQ REQ/REP compute service",
	})
	// Register: Route.Register(builder) — consistent with rest.Route.Register and events.Channel.Register
	serverHandle, err := ComputeRoute.Register(zmqBuilder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "register: %v\n", err)
		os.Exit(1)
	}

	// Client side reuses serverHandle — no separate ClientHandle needed.
	clientHandle := serverHandle

	// In-process socket pair simulating ZMQ REQ ↔ REP.
	reqSock, repSock := newSocketPair()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Layer 3: REP server — blocking, run in a goroutine.
	// Adapter call is UNCHANGED from Phase 1: serverHandle IS *rest.RouteHandle.
	go func() {
		if err := zeromq.Serve(ctx, repSock, serverHandle,
			func(_ context.Context, req ComputeReq) (ComputeResp, error) {
				return ComputeResp{Sum: req.X + req.Y}, nil
			},
			zeromq.ServeOptions{
				Observer: obs,
				OnError:  func(e zeromq.ServeError) { fmt.Fprintf(os.Stderr, "serve error: %v\n", e) },
			},
		); err != nil {
			fmt.Fprintf(os.Stderr, "serve stopped: %v\n", err)
		}
	}()

	// Layer 3: REQ client — call the server.
	reqs := []ComputeReq{
		{X: 3, Y: 4},
		{X: 10, Y: 20},
		{X: -5, Y: 5},
	}
	for _, req := range reqs {
		resp, err := zeromq.Call(ctx, reqSock, clientHandle, req, zeromq.CallOptions{Observer: obs})
		if err != nil {
			var callErr zeromq.CallError
			if errors.As(err, &callErr) {
				fmt.Fprintf(os.Stderr, "call error: %v\n", callErr)
			}
			os.Exit(1)
		}
		fmt.Printf("compute(%d + %d) = %d\n", req.X, req.Y, resp.Sum)
	}

	// Print the AsyncAPI 3.0 spec with request-reply.
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
	fmt.Println("\n── AsyncAPI spec (zmq request-reply) ──")
	fmt.Println(string(specYAML))
}
