// Package rest-redirect demonstrates go-codex's typed HTTP redirects
// feature (api/rest): a handler declares "redirect to THIS route" via
// rest.Redirect/rest.RedirectToSSE — never a raw URL string — and
// Client.Call/Client.Consume transparently follow it, decoding the
// TARGET route's own response type, as long as the target route has
// been seen by this *rest.Client (either via a prior Call/Consume, or
// explicitly via Client.RegisterRoute for a route that's ONLY ever
// reached as a redirect target).
//
// This models the common "POST-redirect-GET" pattern: creating an order
// (POST /orders) redirects (303 See Other) to the created resource
// (GET /orders/{id}) instead of returning its representation directly.
//
// See also docs/features/rest-redirects.md and docs/guides/rest-redirects.md.
//
// Run with: go run ./examples/rest-redirect
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/DaniDeer/go-codex/adapters/nethttp"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/validate"
)

// --- Domain types ---

type CreateOrderReq struct {
	Item string
}

type GetOrderReq struct {
	ID string
}

type Order struct {
	ID   string
	Item string
}

// --- Codecs ---

var createOrderReqCodec = codex.Struct[CreateOrderReq](
	codex.RequiredField("item", codex.String().Refine(validate.NonEmptyString),
		func(r CreateOrderReq) string { return r.Item },
		func(r *CreateOrderReq, v string) { r.Item = v },
	),
)

// getOrderReqCodec is empty: ID flows through the declared PATH MERGE
// field below, not the body.
var getOrderReqCodec = codex.Struct[GetOrderReq]()

var orderCodec = codex.Struct[Order](
	codex.RequiredField("id", codex.String(),
		func(o Order) string { return o.ID },
		func(o *Order, v string) { o.ID = v },
	),
	codex.RequiredField("item", codex.String(),
		func(o Order) string { return o.Item },
		func(o *Order, v string) { o.Item = v },
	),
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	// --- Declare routes (transport-agnostic) ---

	// getOrderRoute is the redirect TARGET — a plain GET returning the
	// created order.
	getOrderRoute := rest.NewRoute[GetOrderReq, Order]("GET", "/orders/{id}",
		getOrderReqCodec, orderCodec,
		rest.NewPathParam("id", codex.String(),
			func(r GetOrderReq) string { return r.ID },
			func(r *GetOrderReq, v string) { r.ID = v },
		))

	// createOrderRoute's handler NEVER returns an Order directly — it
	// redirects to getOrderRoute instead, via rest.Redirect. The target
	// is named as a ROUTE VALUE, never a raw "/orders/123" string — the
	// path is built from getOrderRoute's own declared template + vars.
	var createOrderRoute rest.Route[CreateOrderReq, Order]
	createOrderRoute = rest.NewRoute[CreateOrderReq, Order]("POST", "/orders",
		createOrderReqCodec, orderCodec)

	// --- Server: attach handlers, register, serve ---

	nextID := 1
	orders := map[string]Order{}

	createOrderRoute = createOrderRoute.WithHandler(func(ctx context.Context, req CreateOrderReq) (Order, error) {
		id := fmt.Sprintf("%d", nextID)
		nextID++
		orders[id] = Order{ID: id, Item: req.Item}
		var zero Order
		return zero, rest.Redirect(http.StatusSeeOther, createOrderRoute, getOrderRoute, map[string]string{"id": id})
	})
	getOrderRoute = getOrderRoute.WithHandler(func(ctx context.Context, req GetOrderReq) (Order, error) {
		o, ok := orders[req.ID]
		if !ok {
			return Order{}, fmt.Errorf("order %q not found", req.ID)
		}
		return o, nil
	})

	b := rest.NewServer(rest.Info{Title: "Orders API", Version: "1.0.0"})
	if err := createOrderRoute.Register(b); err != nil {
		return fmt.Errorf("register createOrderRoute: %w", err)
	}
	if err := getOrderRoute.Register(b); err != nil {
		return fmt.Errorf("register getOrderRoute: %w", err)
	}

	mux := http.NewServeMux()
	addr := mustFreeAddr()
	if err := b.Attach(nethttp.NewServerTransport(nethttp.ServerTransportOptions{Mux: mux, Addr: addr})); err != nil {
		return fmt.Errorf("attach server: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = b.Serve(ctx) }()
	waitForReady(addr)
	baseURL := "http://" + addr

	// --- Client: attach a rest.Client, auto-follow the redirect ---

	client := rest.NewClient()
	if err := client.Attach(nethttp.NewClientTransport(nethttp.ClientTransportOptions{
		HTTPClient: http.DefaultClient, BaseURL: baseURL,
	})); err != nil {
		return fmt.Errorf("attach client: %w", err)
	}

	// Primary workflow: warm the registry by registering getOrderRoute
	// EXPLICITLY — it's never called directly in this demo, only ever
	// reached as createOrderRoute's redirect target. A route that IS
	// called directly elsewhere needs no such explicit registration —
	// Call/Consume auto-populate the registry on first use.
	if err := client.RegisterRoute(getOrderRoute); err != nil {
		return fmt.Errorf("RegisterRoute: %w", err)
	}

	respAny, err := client.Call(context.Background(), createOrderRoute, CreateOrderReq{Item: "Widget"})
	if err != nil {
		return fmt.Errorf("Call(createOrderRoute): %w", err)
	}
	created, ok := respAny.(Order)
	if !ok {
		return fmt.Errorf("want Order (getOrderRoute's type), got %T", respAny)
	}
	fmt.Printf("created+redirected: order %s (%s)\n", created.ID, created.Item)

	// Escape hatch: CallWithTransport stays registry-free — it returns
	// the typed rest.RedirectError directly instead of auto-following,
	// for a caller that wants to inspect or manually resolve the
	// redirect itself.
	handle, err := createOrderRoute.RegisterHandle(rest.NewServer(rest.Info{}))
	if err != nil {
		return fmt.Errorf("RegisterHandle: %w", err)
	}
	escapeTransport := nethttp.NewClientTransport(nethttp.ClientTransportOptions{HTTPClient: http.DefaultClient, BaseURL: baseURL})
	_, callErr := rest.CallWithTransport(context.Background(), escapeTransport, handle, CreateOrderReq{Item: "Gadget"})
	var redirErr rest.RedirectError
	if !errors.As(callErr, &redirErr) {
		return fmt.Errorf("CallWithTransport: want rest.RedirectError, got %v", callErr)
	}
	fmt.Printf("CallWithTransport (no auto-follow): got %d redirect to %s\n", redirErr.Status, redirErr.Location)

	return nil
}

// mustFreeAddr reserves an OS-assigned free TCP port on localhost, then
// releases it immediately so the server's own *http.Server can bind to
// it (mirrors examples/adapters-templ's identical helper).
func mustFreeAddr() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintln(os.Stderr, "reserve free port failed:", err)
		os.Exit(1)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForReady polls addr until it accepts TCP connections — b.Serve
// wires routes synchronously before starting its listener goroutine, so
// requests below must wait for it to actually be listening first.
func waitForReady(addr string) {
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
