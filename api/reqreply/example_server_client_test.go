package reqreply_test

import (
	"context"
	"fmt"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// exampleInProcessTransport is a minimal in-process ServerTransport/
// ClientTransport pair — NOT a real adapter, just enough to demonstrate
// the Server/Client + Attach workflow end-to-end without a real broker.
// Real transports (mqtt5.Attach, zeromq.Attach) land per-adapter — see
// docs/design/d-0004-reqreply-workflow-simplification.md.
type exampleInProcessTransport struct {
	handlers map[string]func(context.Context, any) (any, error)
	// ready is closed once Serve has registered its handler — a real
	// adapter's Attach/Serve sequencing (subscribe-then-signal-ready)
	// makes this unnecessary; this in-process stub needs an explicit
	// signal since there's no real connection handshake to wait on.
	ready chan struct{}
}

func (t *exampleInProcessTransport) Serve(ctx context.Context, routeAny any, fnAny any) error {
	h := routeAny.(*reqreply.RouteHandle[computeReq, computeResp])
	fn := fnAny.(func(context.Context, computeReq) (computeResp, error))
	t.handlers[h.Topic] = func(ctx context.Context, reqAny any) (any, error) {
		return fn(ctx, reqAny.(computeReq))
	}
	close(t.ready)
	<-ctx.Done()
	return nil
}

func (t *exampleInProcessTransport) Call(ctx context.Context, routeAny any, reqAny any) (any, error) {
	var topic string
	switch v := routeAny.(type) {
	case reqreply.Route[computeReq, computeResp]:
		topic = v.ClientHandle().Topic
	case *reqreply.RouteHandle[computeReq, computeResp]:
		topic = v.Topic
	}
	h, ok := t.handlers[topic]
	if !ok {
		return nil, fmt.Errorf("no handler registered for topic %q", topic)
	}
	return h(ctx, reqAny)
}

func (t *exampleInProcessTransport) CallAsync(ctx context.Context, routeAny any, reqAny any) (any, error) {
	resp, err := t.Call(ctx, routeAny, reqAny)
	ff := routeAny.(reqreply.FutureFactory)
	future, resolve := ff.NewFutureAny()
	resolve(resp, err)
	return future, nil
}

// Example demonstrates the confirmed Server/Client + Attach workflow: a
// fluent WithHandler+Register server-side declaration, dispatched via
// Server.Serve, and a blocking Client.Call against the SAME in-process
// transport.
func Example() {
	server := reqreply.NewServer(reqreply.Info{Title: "Compute API", Version: "1.0.0"})
	transport := &exampleInProcessTransport{
		handlers: make(map[string]func(context.Context, any) (any, error)),
		ready:    make(chan struct{}),
	}

	handler := func(ctx context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	computeRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add", reqCodec, respCodec)
	if _, err := computeRoute.WithHandler(handler).Register(server); err != nil {
		fmt.Println("register error:", err)
		return
	}
	if err := server.Attach(transport); err != nil {
		fmt.Println("attach error:", err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	<-transport.ready // wait for Serve to register its handler

	client := reqreply.NewClient()
	_ = client.Attach(transport)
	respAny, err := client.Call(context.Background(), computeRoute, computeReq{X: 2, Y: 3})
	if err != nil {
		fmt.Println("call error:", err)
		return
	}
	fmt.Println(respAny.(computeResp).Sum)
	// Output: 5
}
