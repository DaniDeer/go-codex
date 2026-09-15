package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
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

// ── Security Fn-shape tests (docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) ─────────────

// securedComputeReq carries an in-payload Token field — zeromq's paired
// security/credential Fn reads/writes THIS field directly (no raw-message
// side channel exists, unlike mqtt5's User Properties), mirroring zeromq
// pub/sub's own SubscribeMW/PublishMW-paired security implementation
// contract exactly.
type securedComputeReq struct {
	X, Y  int
	Token string
}
type securedComputeResp struct{ Sum int }

var securedComputeReqCodec = codex.Struct[securedComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r securedComputeReq) int { return r.X },
		func(r *securedComputeReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(),
		func(r securedComputeReq) int { return r.Y },
		func(r *securedComputeReq, v int) { r.Y = v }),
	codex.OptionalField("token", codex.String(),
		func(r securedComputeReq) string { return r.Token },
		func(r *securedComputeReq, v string) { r.Token = v }),
)

var securedComputeRespCodec = codex.Struct[securedComputeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r securedComputeResp) int { return r.Sum },
		func(r *securedComputeResp, v int) { r.Sum = v }),
)

var zmqBearerAuthCodec = codex.String().Refine(validate.NonEmptyString)
var zmqBearerAuthMw = middleware.SecurityScheme("zmqBearer", route.BearerScheme("JWT"), nil, &zmqBearerAuthCodec)

func newSecuredComputeRoute() reqreply.Route[securedComputeReq, securedComputeResp] {
	return reqreply.NewRoute[securedComputeReq, securedComputeResp](
		"/secured-compute",
		securedComputeReqCodec, securedComputeRespCodec,
		reqreply.RouteMeta{OperationID: "securedCompute"},
	)
}

// acceptingSecurityFn is a PAIRED server-side security Fn that always
// grants (reads req.Token, accepts any non-empty value — format already
// validated by declaring the scheme, so this Fn just simulates a
// revocation check).
func acceptingSecurityFn(_ context.Context, req *securedComputeReq, _ []route.SecurityRequirement) error {
	if req.Token == "" {
		return errors.New("token required")
	}
	return nil
}

func TestAttachServer_HandleMW_PairedSecurityFn_Verifies(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		HandleMW(&zmqBearerAuthMw, acceptingSecurityFn).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/secured-compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	// Reject: missing token.
	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":3,"y":4}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	frames, err := reqSock.RecvFrames()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if string(frames[0]) != "error" {
		t.Fatalf("expected error status for missing token, got %q: %s", frames[0], frames[1])
	}

	// Accept: token present.
	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":3,"y":4,"token":"abc"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	frames, err = reqSock.RecvFrames()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if string(frames[0]) != "ok" {
		t.Fatalf("expected ok status, got %q: %s", frames[0], frames[1])
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

func TestAttachServer_HandleMW_PairedSecurityFn_MutatesReq(t *testing.T) {
	// The security Fn WRITES an enrichment field (here: forces Y to a
	// fixed value) — proving the WRITE half of *Req access, not just
	// read/reject, mirrors pub/sub's identical read/write contract.
	enrichFn := func(_ context.Context, req *securedComputeReq, _ []route.SecurityRequirement) error {
		if req.Token == "" {
			return errors.New("token required")
		}
		req.Y = 100 // enrichment — handler must observe THIS value, not the wire value
		return nil
	}

	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	var gotY int
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		gotY = r.Y
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		HandleMW(&zmqBearerAuthMw, enrichFn).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/secured-compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":3,"y":4,"token":"abc"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := reqSock.RecvFrames(); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if gotY != 100 {
		t.Fatalf("expected handler to observe enriched Y=100, got %d", gotY)
	}
}

func TestAttachRouterServer_HandleMW_PairedSecurityFn_Verifies(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		HandleMW(&zmqBearerAuthMw, acceptingSecurityFn).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	if err := AttachRouterServer(server, map[string]FramedSocket{"/secured-compute": routerSock}); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	client := reqreply.NewClient()
	if err := AttachDealerClient(client, map[string]FramedSocket{"/secured-compute": dealerSock}); err != nil {
		t.Fatalf("AttachDealerClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	// Reject: missing token.
	_, err := client.Call(context.Background(), newSecuredComputeRoute(), securedComputeReq{X: 1, Y: 2})
	if err == nil {
		t.Fatal("expected rejection for missing token")
	}

	// Accept: token present.
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	respAny, err := client.Call(callCtx, newSecuredComputeRoute(), securedComputeReq{X: 1, Y: 2, Token: "abc"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(securedComputeResp)
	if !ok || resp.Sum != 3 {
		t.Fatalf("expected securedComputeResp{Sum:3}, got %#v", respAny)
	}
}

func TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	// Declares the scheme via .Use() but attaches NO HandleMW implementation.
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	repSock, _ := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/secured-compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	err := server.Serve(context.Background())
	var missing reqreply.MissingSecurityMiddlewareError
	if !errors.As(err, &missing) {
		t.Fatalf("expected reqreply.MissingSecurityMiddlewareError, got %v (%T)", err, err)
	}
}

func TestAttachRouterServer_CheckCoverage_MissingSecurityMiddlewareError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	_, routerSock := newDealerRouterPair([]byte("client-1"))
	if err := AttachRouterServer(server, map[string]FramedSocket{"/secured-compute": routerSock}); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	err := server.Serve(context.Background())
	var missing reqreply.MissingSecurityMiddlewareError
	if !errors.As(err, &missing) {
		t.Fatalf("expected reqreply.MissingSecurityMiddlewareError, got %v (%T)", err, err)
	}
}

// acceptingCredentialFn is a PAIRED client-side credential Fn that always
// writes a valid token.
func acceptingCredentialFn(_ context.Context, req *securedComputeReq, _ []route.SecurityRequirement) error {
	req.Token = "valid-token"
	return nil
}

// rejectingCredentialFn is a PAIRED client-side credential Fn that always
// fails BEFORE anything is sent.
func rejectingCredentialFn(_ context.Context, _ *securedComputeReq, _ []route.SecurityRequirement) error {
	return errors.New("credential unavailable")
}

func TestAttachClient_ClientMW_PairedCredentialFn_WritesReq(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		if r.Token != "valid-token" {
			return securedComputeResp{}, errors.New("missing/invalid token")
		}
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		HandleMW(&zmqBearerAuthMw, acceptingSecurityFn).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/secured-compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{"/secured-compute": reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	callRoute := newSecuredComputeRoute().Use(zmqBearerAuthMw).ClientMW(&zmqBearerAuthMw, acceptingCredentialFn)
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	respAny, err := client.Call(callCtx, callRoute, securedComputeReq{X: 2, Y: 3})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(securedComputeResp)
	if !ok || resp.Sum != 5 {
		t.Fatalf("expected securedComputeResp{Sum:5}, got %#v", respAny)
	}
}

func TestAttachClient_ClientMW_PairedCredentialFn_RejectsBeforeSend(t *testing.T) {
	client := reqreply.NewClient()
	reqSock, _ := newChanSocketPair()
	if err := AttachClient(client, map[string]FramedSocket{"/secured-compute": reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	callRoute := newSecuredComputeRoute().Use(zmqBearerAuthMw).ClientMW(&zmqBearerAuthMw, rejectingCredentialFn)
	_, err := client.Call(context.Background(), callRoute, securedComputeReq{X: 1, Y: 1})
	var credErr reqreply.SecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("expected reqreply.SecurityCredentialError, got %v (%T)", err, err)
	}
}

func TestAttachDealerClient_ClientMW_PairedCredentialFn_WritesReq(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		if r.Token != "valid-token" {
			return securedComputeResp{}, errors.New("missing/invalid token")
		}
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		HandleMW(&zmqBearerAuthMw, acceptingSecurityFn).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	if err := AttachRouterServer(server, map[string]FramedSocket{"/secured-compute": routerSock}); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	client := reqreply.NewClient()
	if err := AttachDealerClient(client, map[string]FramedSocket{"/secured-compute": dealerSock}); err != nil {
		t.Fatalf("AttachDealerClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	callRoute := newSecuredComputeRoute().Use(zmqBearerAuthMw).ClientMW(&zmqBearerAuthMw, acceptingCredentialFn)
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	respAny, err := client.Call(callCtx, callRoute, securedComputeReq{X: 2, Y: 3})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	resp, ok := respAny.(securedComputeResp)
	if !ok || resp.Sum != 5 {
		t.Fatalf("expected securedComputeResp{Sum:5}, got %#v", respAny)
	}
}

// generalServerDecorator is an UNPAIRED, general-purpose HandleMW Fn —
// runs regardless of declared Security.
func generalServerDecorator(called *bool) func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
	return func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(ctx context.Context, req computeReq) (computeResp, error) {
			*called = true
			return next(ctx, req)
		}
	}
}

func TestAttachServer_HandleMW_GeneralPurpose_AlwaysRuns(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}
	var called bool
	route := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	).HandleMW(nil, generalServerDecorator(&called))
	if _, err := route.WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":1,"y":2}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := reqSock.RecvFrames(); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if !called {
		t.Fatal("expected general-purpose HandleMW to run")
	}
}

func TestAttachClient_MultipleGeneralPurposeClientMW_ComposeOutermostIn(t *testing.T) {
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

	var order []string
	dec := func(name string) func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
			return func(ctx context.Context, req computeReq) (computeResp, error) {
				order = append(order, name+":before")
				resp, err := next(ctx, req)
				order = append(order, name+":after")
				return resp, err
			}
		}
	}
	callRoute := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	).ClientMW(nil, dec("outer")).ClientMW(nil, dec("inner"))

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	if _, err := client.Call(callCtx, callRoute, computeReq{X: 1, Y: 2}); err != nil {
		t.Fatalf("Call: %v", err)
	}
	want := []string{"outer:before", "inner:before", "inner:after", "outer:after"}
	if len(order) != len(want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected order %v, got %v", want, order)
		}
	}
}

func TestAttachClient_ClientMW_AppliesToCallAsyncToo(t *testing.T) {
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

	var called bool
	dec := func(next func(context.Context, computeReq) (computeResp, error)) func(context.Context, computeReq) (computeResp, error) {
		return func(ctx context.Context, req computeReq) (computeResp, error) {
			called = true
			return next(ctx, req)
		}
	}
	callRoute := reqreply.NewRoute[computeReq, computeResp](
		"/compute", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	).ClientMW(nil, dec)

	futureAny, err := client.CallAsync(context.Background(), callRoute, computeReq{X: 4, Y: 5})
	if err != nil {
		t.Fatalf("CallAsync: %v", err)
	}
	future, ok := futureAny.(*reqreply.Future[computeResp])
	if !ok {
		t.Fatalf("expected *reqreply.Future[computeResp], got %T", futureAny)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	if _, err := future.Wait(waitCtx); err != nil {
		t.Fatalf("Future.Wait: %v", err)
	}
	if !called {
		t.Fatal("expected general-purpose ClientMW decorator to run through CallAsync too")
	}
}

func TestAttachServer_HandleMW_SecurityRejection_CallsSecurityObserver(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	if _, err := newSecuredComputeRoute().Use(zmqBearerAuthMw).
		HandleMW(&zmqBearerAuthMw, acceptingSecurityFn).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	repSock, reqSock := newChanSocketPair()
	obs := &testObserver{}
	if err := AttachServer(server, map[string]FramedSocket{"/secured-compute": repSock}, ServeOptions{Observer: obs}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":1,"y":2}`)}); err != nil { // missing token → rejected
		t.Fatalf("send: %v", err)
	}
	if _, err := reqSock.RecvFrames(); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if len(obs.securityRejections) != 1 || obs.securityRejections[0] != "zmqBearer" {
		t.Fatalf("expected 1 RecordSecurityRejection call for scheme zmqBearer, got %v", obs.securityRejections)
	}
}

// testCtxKeyType/testCtxKey is a private context key for
// TestAttachClient_ClientMW_ContextMutationPropagatesIntoInnerCall below —
// proves a general-purpose ClientMW decorator's context mutation reaches
// the reflect.MakeFunc-built innerCall closure's OWN body (specifically
// the paired credential Fn), not just the decorator chain itself. Mirrors
// adapters/mqtt5's identical regression test — a prior revision's
// innerCall ignored args[0] (the ctx actually passed by the decorator
// calling next) and used the STALE, pre-decorator ctx captured from the
// enclosing call — silently discarding any such mutation.
type testCtxKeyType struct{}

var testCtxKey = testCtxKeyType{}

func TestAttachClient_ClientMW_ContextMutationPropagatesIntoInnerCall(t *testing.T) {
	client := reqreply.NewClient()
	reqSock, _ := newChanSocketPair()
	if err := AttachClient(client, map[string]FramedSocket{"/ctx-propagation": reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	var observedValue any
	credFn := func(ctx context.Context, _ *securedComputeReq, _ []route.SecurityRequirement) error {
		observedValue = ctx.Value(testCtxKey)
		return nil
	}
	ctxInjectingMw := func(next func(context.Context, securedComputeReq) (securedComputeResp, error)) func(context.Context, securedComputeReq) (securedComputeResp, error) {
		return func(ctx context.Context, req securedComputeReq) (securedComputeResp, error) {
			ctx = context.WithValue(ctx, testCtxKey, "injected")
			return next(ctx, req)
		}
	}

	baseRoute := reqreply.NewRoute[securedComputeReq, securedComputeResp]("/ctx-propagation", securedComputeReqCodec, securedComputeRespCodec)
	clientRoute := baseRoute.Use(zmqBearerAuthMw).
		ClientMW(&zmqBearerAuthMw, credFn).
		ClientMW(nil, ctxInjectingMw)

	// No server wiring needed — the credential Fn runs and records
	// observedValue BEFORE send is ever attempted; a timeout waiting for
	// a reply (since no server answers) is expected and ignored, only
	// observedValue is asserted.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = client.Call(ctx, clientRoute, securedComputeReq{X: 1, Y: 2})

	if observedValue != "injected" {
		t.Fatalf("expected the ClientMW decorator's context mutation to propagate into the paired credential Fn, got %v", observedValue)
	}
}

// ── docs/design/d-0003-codec-declared-middlewares.md's Addendum adapter wiring ──────

type zmwPropIn struct{ TenantID string }
type zmwPropOut struct{ Ack string }

var zmwPropInCodec = codex.Struct[zmwPropIn]()
var zmwPropOutCodec = codex.Struct[zmwPropOut]()

// TestAttachServer_Transform_RunsAfterPairedSecurity confirms D1 for
// zeromq: Transform's declared middleware runs AFTER the paired security
// Fn — the mechanism is genuinely transport-agnostic, zero adapter-
// specific work beyond consulting the same RouteHandle field mqtt5 does.
func TestAttachServer_Transform_RunsAfterPairedSecurity(t *testing.T) {
	var order []string
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("zmq-order-check", zmwPropInCodec, zmwPropOutCodec))
	fn := func(_ context.Context, r securedComputeReq) (securedComputeResp, error) {
		order = append(order, "handler")
		return securedComputeResp{Sum: r.X + r.Y}, nil
	}
	secFn := func(_ context.Context, req *securedComputeReq, _ []route.SecurityRequirement) error {
		order = append(order, "security")
		return nil
	}
	baseRoute := newSecuredComputeRoute().Use(zmqBearerAuthMw).HandleMW(&zmqBearerAuthMw, secFn)
	rt := reqreply.Transform(baseRoute, mw,
		func(ctx context.Context, req *securedComputeReq, in zmwPropIn) (zmwPropOut, error) {
			order = append(order, "middleware")
			return zmwPropOut{}, nil
		})
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/secured-compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":3,"y":4,"token":"abc"}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := reqSock.RecvFrames(); err != nil {
		t.Fatalf("recv: %v", err)
	}

	if len(order) != 3 || order[0] != "security" || order[1] != "middleware" || order[2] != "handler" {
		t.Fatalf("want dispatch order [security middleware handler], got %v", order)
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

// TestAttachServer_MiddlewareError_WrapsAsKindMiddleware (zeromq)
// confirms decision #6: a Transform-attached fn's own business error
// surfaces through ServeError{Kind: KindMiddleware}, NOT KindHandler.
func TestAttachServer_MiddlewareError_WrapsAsKindMiddleware(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("zmq-fn-error", zmwPropInCodec, zmwPropOutCodec))
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("/mw-error-compute", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req *computeReq, in zmwPropIn) (zmwPropOut, error) {
			return zmwPropOut{}, errors.New("business failure")
		},
	)
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	var gotKind ErrorKind
	var kindSet bool
	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/mw-error-compute": repSock}, ServeOptions{
		OnError: func(e ServeError) {
			if !kindSet {
				gotKind = e.Kind
				kindSet = true
			}
		},
	}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":1,"y":2}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := reqSock.RecvFrames(); err != nil {
		t.Fatalf("recv: %v", err)
	}

	if !kindSet || gotKind != KindMiddleware {
		t.Fatalf("want ServeError{Kind: KindMiddleware}, got kindSet=%v kind=%v", kindSet, gotKind)
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

// TestAttachServer_Observer_ReportsMiddlewareOutLocation (Rest-middleware-
// conflict-detection-improvements' adapter-dispatch review): the reply's
// own EncodeOut failure (building the REPLY's Out struct, via
// WithResponseProperty — zeromq has no property mechanism to WRITE the
// value into, but OutCodec.Validate still runs the SAME refinement) was
// previously collapsed into "middleware:in" — now reported distinctly as
// "middleware:out". Mirrors adapters/mqtt5's identical test.
func TestAttachServer_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	mw := reqreply.NewMiddleware(middleware.NewDeclaration("zmq-obs-out-loc", zmwPropInCodec, zmwPropOutCodec)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String().Refine(validate.NonEmptyString),
			func(v zmwPropOut) string { return v.Ack },
			func(v *zmwPropOut, s string) { v.Ack = s }))
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}
	rt := reqreply.Transform(
		reqreply.NewRoute[computeReq, computeResp]("/obs-out-loc-compute", computeReqCodec, computeRespCodec),
		mw,
		func(ctx context.Context, req *computeReq, in zmwPropIn) (zmwPropOut, error) {
			// Empty Ack fails the NonEmptyString refinement at
			// EncodeOut/OutCodec.Validate time, building the reply.
			return zmwPropOut{Ack: ""}, nil
		},
	)
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := rt.WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	obs := &testObserver{}
	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/obs-out-loc-compute": repSock}, ServeOptions{Observer: obs}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":1,"y":2}`)}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := reqSock.RecvFrames(); err != nil {
		t.Fatalf("recv: %v", err)
	}

	found := false
	for _, loc := range obs.validationLocations {
		if loc == "middleware:out" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:out", obs.validationLocations)
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

// TestAttachClient_Observer_ReportsMiddlewareOutLocation (Rest-middleware-
// conflict-detection-improvements' adapter-dispatch review): a client-side
// DecodeOut failure (reading the REPLY's Out struct) was previously
// reported as "middleware:in" — now reported distinctly as
// "middleware:out". The server does NOT declare WithResponseProperty at
// all — its reply never carries the value the client's OWN mw requires
// (Required by default via [reqreply.NewPropertyParam]) — but zeromq has
// no property mechanism to CARRY it in the first place, so DecodeOut fails
// naturally on the missing value, same as a real missing property would
// mirroring mqtt5's identical test.
func TestAttachClient_Observer_ReportsMiddlewareOutLocation(t *testing.T) {
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	if _, err := reqreply.NewRoute[computeReq, computeResp]("/obs-client-out-compute", computeReqCodec, computeRespCodec).
		WithHandler(fn).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/obs-client-out-compute": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- server.Serve(ctx) }()

	clientMW := reqreply.NewMiddleware(middleware.NewDeclaration("zmq-obs-client-out-loc", zmwPropInCodec, zmwPropOutCodec)).
		WithResponseProperty(reqreply.NewPropertyParam("X-Ack", codex.String(),
			func(v zmwPropOut) string { return v.Ack },
			func(v *zmwPropOut, s string) { v.Ack = s }))
	rt := reqreply.ClientTransform(
		reqreply.NewRoute[computeReq, computeResp]("/obs-client-out-compute", computeReqCodec, computeRespCodec),
		clientMW,
		func(ctx context.Context, req computeReq) (zmwPropIn, error) {
			return zmwPropIn{}, nil
		},
	)

	obs := &testObserver{}
	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{"/obs-client-out-compute": reqSock}, CallOptions{Observer: obs}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, _ = client.Call(callCtx, rt, computeReq{X: 1, Y: 2})

	found := false
	for _, loc := range obs.validationLocations {
		if loc == "middleware:out" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:out", obs.validationLocations)
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
