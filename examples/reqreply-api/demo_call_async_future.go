package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/examples/reqreply-api/routes"
)

// demoCallAsyncFuture directly demonstrates the "send here, resolve
// elsewhere" async mechanism: client.CallAsync(ctx, route, req) returns a
// *reqreply.Future[ComputeResp] IMMEDIATELY. This demo explicitly does
// other independent work (issues a SECOND, independent CallAsync for a
// different route) before awaiting the first, then awaits BOTH futures
// from a call site distinct from the one that issued them — mirroring the
// confirmed prototype's actual "different call site/goroutine" test, not
// a same-line call-then-immediately-await that would look identical to a
// blocking Call renamed.
//
// A second scenario shows Future.Wait(ctx) against an already-cancelled
// ctx returning a timeout error, matching Call's own CallError{Kind:
// KindTimeout} semantics.
func demoCallAsyncFuture(ctx context.Context, mqtt5Client *reqreply.Client) {
	fmt.Println("\n── Demo 5: CallAsync + Future — send here, resolve elsewhere ──")

	// Issue TWO independent async calls, back to back, before awaiting either.
	future1Any, err := mqtt5Client.CallAsync(ctx, routes.ComputeRoute, routes.ComputeReq{X: 1, Y: 1})
	if err != nil {
		fmt.Fprintf(os.Stderr, "CallAsync #1 error: %v\n", err)
		os.Exit(1)
	}
	future1 := future1Any.(*reqreply.Future[routes.ComputeResp])

	fmt.Println("  → CallAsync #1 issued, doing other independent work before awaiting it...")

	future2Any, err := mqtt5Client.CallAsync(ctx, routes.ComputeRoute, routes.ComputeReq{X: 2, Y: 2})
	if err != nil {
		fmt.Fprintf(os.Stderr, "CallAsync #2 error: %v\n", err)
		os.Exit(1)
	}
	future2 := future2Any.(*reqreply.Future[routes.ComputeResp])

	fmt.Println("  → CallAsync #2 issued too — NEITHER has been awaited yet")

	// Await BOTH from a call site distinct from the one that issued them.
	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp1, err := future1.Wait(waitCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "future1.Wait error: %v\n", err)
		os.Exit(1)
	}
	resp2, err := future2.Wait(waitCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "future2.Wait error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  ✓ future1 resolved: Sum=%d\n", resp1.Sum)
	fmt.Printf("  ✓ future2 resolved: Sum=%d\n", resp2.Sum)

	// Timeout scenario: Future.Wait against an already-cancelled ctx.
	fmt.Println("\n  → Future.Wait against an already-cancelled ctx:")
	future3Any, err := mqtt5Client.CallAsync(ctx, routes.ComputeRoute, routes.ComputeReq{X: 3, Y: 3})
	if err != nil {
		fmt.Fprintf(os.Stderr, "CallAsync #3 error: %v\n", err)
		os.Exit(1)
	}
	future3 := future3Any.(*reqreply.Future[routes.ComputeResp])
	cancelledCtx, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	_, err = future3.Wait(cancelledCtx)
	if err != nil {
		fmt.Printf("  ✓ Wait against a cancelled ctx returned: %v\n", err)
	} else {
		fmt.Println("  (unexpectedly succeeded against a cancelled ctx)")
	}
}
