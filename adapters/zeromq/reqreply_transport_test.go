package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// ── chanSocket: an in-memory FramedSocket pair for REQ/REP round-trip tests ──

// chanSocket implements FramedSocket over a channel, simulating one side of
// a connected REQ/REP socket pair — SendFrames on one side delivers to the
// peer's RecvFrames, exactly like a real bound/connected ZMQ socket pair,
// without needing a real ZMQ library.
type chanSocket struct {
	peer    *chanSocket
	in      chan [][]byte
	timeout time.Duration
}

func newChanSocketPair() (*chanSocket, *chanSocket) {
	a := &chanSocket{in: make(chan [][]byte, 8), timeout: 50 * time.Millisecond}
	b := &chanSocket{in: make(chan [][]byte, 8), timeout: 50 * time.Millisecond}
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
		return nil, ErrTimeout
	}
}

func (c *chanSocket) SetSubscription(string) error { return nil }

func (c *chanSocket) SetRecvTimeout(d time.Duration) error {
	c.timeout = d
	return nil
}

var _ FramedSocket = (*chanSocket)(nil)

// ── dealerSocket/routerSocket: a ROUTER/DEALER envelope-aware pair ───────────

// dealerSocket simulates one DEALER peer: SendFrames([delimiter, payload])
// is delivered to the paired routerSocket with an identity frame PREPENDED
// (mirroring what a real ROUTER socket automatically does on receive);
// RecvFrames strips the identity the paired routerSocket includes on send,
// mirroring what a real DEALER socket automatically does.
type dealerSocket struct {
	peer     *routerSocket
	in       chan [][]byte
	timeout  time.Duration
	identity []byte
}

// routerSocket simulates a ROUTER peer bound to exactly one dealerSocket
// (sufficient for these tests — real ROUTER sockets serve many DEALER
// peers, distinguished by identity, which is unnecessary to model here).
type routerSocket struct {
	peer    *dealerSocket
	in      chan [][]byte
	timeout time.Duration
}

func newDealerRouterPair(identity []byte) (*dealerSocket, *routerSocket) {
	d := &dealerSocket{in: make(chan [][]byte, 8), timeout: 50 * time.Millisecond, identity: identity}
	r := &routerSocket{in: make(chan [][]byte, 8), timeout: 50 * time.Millisecond}
	d.peer = r
	r.peer = d
	return d, r
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
		return nil, ErrTimeout
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
	r.peer.in <- cloneFrames(frames[1:]) // strip identity — real DEALER never sees it
	return nil
}

func (r *routerSocket) RecvFrames() ([][]byte, error) {
	select {
	case f := <-r.in:
		return f, nil
	case <-time.After(r.timeout):
		return nil, ErrTimeout
	}
}

func (r *routerSocket) SetSubscription(string) error { return nil }

func (r *routerSocket) SetRecvTimeout(dur time.Duration) error {
	r.timeout = dur
	return nil
}

var _ FramedSocket = (*dealerSocket)(nil)
var _ FramedSocket = (*routerSocket)(nil)

// ── shared route fixture ──────────────────────────────────────────────────

func newComputeServerAndHandler(t *testing.T) (*reqreply.Server, func(context.Context, computeReq) (computeResp, error)) {
	t.Helper()
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	if _, err := route.WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return server, fn
}

// ── REQ/REP: AttachServer + AttachClient ─────────────────────────────────

func TestAttachServer_AttachClient_RoundTrip(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)

	repSock, reqSock := newChanSocketPair()

	if err := AttachServer(server, map[string]FramedSocket{"/compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{"/compute": reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	respAny, err := client.Call(callCtx, route, computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("expected computeResp, got %T", respAny)
	}
	if resp.Sum != 7 {
		t.Fatalf("expected Sum=7, got %d", resp.Sum)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestAttachServer_MissingSocketError(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	err := AttachServer(server, map[string]FramedSocket{}) // no socket for "/compute"
	var missing MissingSocketError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingSocketError, got %v (%T)", err, err)
	}
	if missing.Topic != "/compute" {
		t.Fatalf("expected topic /compute, got %q", missing.Topic)
	}
}

func TestAttachClient_MissingSocketError(t *testing.T) {
	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	_, err := client.Call(context.Background(), route, computeReq{X: 1, Y: 2})
	var missing MissingSocketError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingSocketError, got %v (%T)", err, err)
	}
}

// ── REQ/REP: CallAsync/Future ─────────────────────────────────────────────

func TestAttachClient_CallAsync_RoundTrip(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{"/compute": reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	server2 := reqreply.NewServer(reqreply.Info{Title: "Test2", Version: "1.0.0"})
	handle, err := reqreply.NewRoute[computeReq, computeResp](
		"/compute",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	).Register(server2)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	futureAny, err := client.CallAsync(context.Background(), handle, computeReq{X: 10, Y: 20})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	future, ok := futureAny.(*reqreply.Future[computeResp])
	if !ok {
		t.Fatalf("expected *reqreply.Future[computeResp], got %T", futureAny)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	resp, err := future.Wait(waitCtx)
	if err != nil {
		t.Fatalf("Future.Wait: %v", err)
	}
	if resp.Sum != 30 {
		t.Fatalf("expected Sum=30, got %d", resp.Sum)
	}
}

// ── ROUTER/DEALER: AttachRouterServer + AttachDealerClient ────────────────

func TestAttachRouterServer_AttachDealerClient_RoundTrip(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))

	if err := AttachRouterServer(server, map[string]FramedSocket{"/compute": routerSock}); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	client := reqreply.NewClient()
	if err := AttachDealerClient(client, map[string]FramedSocket{"/compute": dealerSock}); err != nil {
		t.Fatalf("AttachDealerClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	)
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	respAny, err := client.Call(callCtx, route, computeReq{X: 5, Y: 6})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("expected computeResp, got %T", respAny)
	}
	if resp.Sum != 11 {
		t.Fatalf("expected Sum=11, got %d", resp.Sum)
	}

	cancel()
	select {
	case err := <-serveErrCh:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
}

func TestAttachRouterServer_MissingSocketError(t *testing.T) {
	server, _ := newComputeServerAndHandler(t)
	err := AttachRouterServer(server, map[string]FramedSocket{})
	var missing MissingSocketError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingSocketError, got %v (%T)", err, err)
	}
}
