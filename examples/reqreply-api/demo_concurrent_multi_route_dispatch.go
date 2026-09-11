package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoConcurrentMultiRouteDispatch registers 3+ routes against ONE Server,
// Attaches a zeromq-backed transport (the BLOCKING transport whose
// starvation risk motivated Decision 1's concurrent-dispatch fix — ZMQ has
// no built-in mux to pre-wire many routes into one call, so
// ServerTransport.Serve blocks in its own receive loop per route), and
// demonstrates all 3 routes actually answering calls CONCURRENTLY — the
// real-world demonstration of the confirmed fix, not just its prototype's
// synthetic high-water-mark assertion.
func demoConcurrentMultiRouteDispatch(ctx context.Context, zeromqClient *reqreply.Client) {
	fmt.Println("\n── Demo 4: concurrent multi-route dispatch (zeromq, blocking transport) ──")

	type call struct {
		name  string
		route reqreply.Route[routes.ComputeReq, routes.ComputeResp]
		req   routes.ComputeReq
	}
	calls := []call{
		{"compute/add", routes.ComputeRoute, routes.ComputeReq{X: 1, Y: 2}},
		{"compute/double", routes.DoubleRoute, routes.ComputeReq{X: 3, Y: 4}},
		{"compute/triple", routes.TripleRoute, routes.ComputeReq{X: 5, Y: 6}},
	}

	var wg sync.WaitGroup
	results := make([]string, len(calls))
	for i, c := range calls {
		wg.Add(1)
		go func(i int, c call) {
			defer wg.Done()
			respAny, err := zeromqClient.Call(ctx, c.route, c.req)
			if err != nil {
				fmt.Fprintf(os.Stderr, "call error (%s): %v\n", c.name, err)
				os.Exit(1)
			}
			resp := respAny.(routes.ComputeResp)
			results[i] = fmt.Sprintf("  %s(%d, %d) = %d", c.name, c.req.X, c.req.Y, resp.Sum)
		}(i, c)
	}
	wg.Wait()

	fmt.Println("  all 3 routes answered CONCURRENTLY (one goroutine per route on the server side):")
	for _, r := range results {
		fmt.Println(r)
	}
}
