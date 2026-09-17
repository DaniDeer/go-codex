package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// This file tests Topic 4's DeadLetter fallback for reqreply's server
// side (see docs/design/d-0005-error-handling.md).
//
// UNLIKE mqtt5 (one shared client can Publish to ANY topic), zeromq's
// REQ/REP is point-to-point — the declared dead-letter topic MUST have
// its OWN entry in the sockets map passed to AttachServer (a standalone
// destination socket, e.g. a PUSH socket feeding a dead-letter consumer)
// for the dead-letter to be reachable at all.

func TestDeadLetter_ServeRequestPayloadDecode_Published(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-dlq-decode", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-dlq-decode/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	dlqSock := &mockSocket{}
	if err := AttachServer(server, map[string]FramedSocket{
		"/compute-dlq-decode":     repSock,
		"/compute-dlq-decode/dlq": dlqSock,
	}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{}`)}); err != nil { // missing required fields
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	dlqSock.mu.Lock()
	defer dlqSock.mu.Unlock()
	found := false
	for _, frames := range dlqSock.sentFrames {
		if len(frames) > 0 && string(frames[0]) == "/compute-dlq-decode/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter socket")
	}
}

func TestDeadLetter_RouterServeRequestPayloadDecode_Published(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-router-dlq-decode", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-router-dlq-decode/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	dlqSock := &mockSocket{}
	if err := AttachRouterServer(server, map[string]FramedSocket{
		"/compute-router-dlq-decode":     routerSock,
		"/compute-router-dlq-decode/dlq": dlqSock,
	}); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := dealerSock.SendFrames([][]byte{emptyDelimiter, []byte(`{}`)}); err != nil { // missing required fields
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	dlqSock.mu.Lock()
	defer dlqSock.mu.Unlock()
	found := false
	for _, frames := range dlqSock.sentFrames {
		if len(frames) > 0 && string(frames[0]) == "/compute-router-dlq-decode/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter socket")
	}
}

// TestDeadLetter_ServeRequestPayloadDecode_NoDLQSocket_NoOp verifies the
// documented "silent no-op" behavior when a route declares a dead-letter
// topic but the caller never gave AttachServer a socket for it — sending
// on the route's OWN REP socket instead would violate REQ/REP's
// one-reply-per-request invariant, so this must NOT happen.
func TestDeadLetter_ServeRequestPayloadDecode_NoDLQSocket_NoOp(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-dlq-decode-missing", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-dlq-decode-missing/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{
		"/compute-dlq-decode-missing": repSock,
	}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{}`)}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// The route's own REP socket must have received exactly ONE reply
	// (the plain-text error fallback) — no extra dead-letter frames.
	frames, recvErr := reqSock.RecvFrames()
	if recvErr != nil {
		t.Fatalf("RecvFrames: %v", recvErr)
	}
	if len(frames) == 0 {
		t.Fatal("want the error reply frame(s)")
	}
	// A second RecvFrames call must time out — confirms no extra
	// dead-letter message was also queued on this socket.
	if _, recvErr := reqSock.RecvFrames(); recvErr != ErrTimeout {
		t.Errorf("want ErrTimeout (no extra message), got: %v", recvErr)
	}
}

// replySendFailingSocket wraps a FramedSocket and fails SendFrames only
// for a SUCCESS reply (any frame equal to statusOK — REQ/REP's reply is
// [statusOK, payload], ROUTER/DEALER's is [id, delim, statusOK,
// payload]) — used to simulate a broker/socket-level rejection of an
// otherwise successfully-processed-and-encoded reply, while leaving
// OTHER sends (recv-loop internals, a SEPARATE dead-letter socket)
// unaffected.
type replySendFailingSocket struct {
	FramedSocket
	failErr error
}

func (s *replySendFailingSocket) SendFrames(frames [][]byte) error {
	for _, f := range frames {
		if string(f) == string(statusOK) {
			return s.failErr
		}
	}
	return s.FramedSocket.SendFrames(frames)
}

// TestDeadLetter_ServeReplyTransmissionFailure_Published tests G1's fix
// (session review round 3 finding): a socket-level rejection of the
// FINAL, successfully-encoded reply (i.e. the request was processed
// correctly, but the send itself failed) is ALSO dead-letterable —
// mirrors events' pub/sub publish side's own broker-rejection handling
// (Topic 4's explicit design decision), previously missing on this
// specific reqreply server-side code path even though every OTHER
// Category-A failure point in the same dispatch already consulted
// DeadLetterFor.
func TestDeadLetter_ServeReplyTransmissionFailure_Published(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{Sum: 7}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-dlq-reply-fail", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-dlq-reply-fail/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	failingRepSock := &replySendFailingSocket{FramedSocket: repSock, failErr: errors.New("socket rejected reply")}
	dlqSock := &mockSocket{}
	if err := AttachServer(server, map[string]FramedSocket{
		"/compute-dlq-reply-fail":     failingRepSock,
		"/compute-dlq-reply-fail/dlq": dlqSock,
	}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := reqSock.SendFrames([][]byte{[]byte(`{"x":3,"y":4}`)}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	dlqSock.mu.Lock()
	defer dlqSock.mu.Unlock()
	found := false
	for _, frames := range dlqSock.sentFrames {
		if len(frames) > 0 && string(frames[0]) == "/compute-dlq-reply-fail/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic when the final reply transmission fails")
	}
}

// TestDeadLetter_RouterServeReplyTransmissionFailure_Published is the
// ROUTER/DEALER variant of the test above.
func TestDeadLetter_RouterServeReplyTransmissionFailure_Published(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{Sum: 7}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-router-dlq-reply-fail", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("/compute-router-dlq-reply-fail/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	dealerSock, routerSock := newDealerRouterPair([]byte("client-1"))
	failingRouterSock := &replySendFailingSocket{FramedSocket: routerSock, failErr: errors.New("socket rejected reply")}
	dlqSock := &mockSocket{}
	if err := AttachRouterServer(server, map[string]FramedSocket{
		"/compute-router-dlq-reply-fail":     failingRouterSock,
		"/compute-router-dlq-reply-fail/dlq": dlqSock,
	}); err != nil {
		t.Fatalf("AttachRouterServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	if err := dealerSock.SendFrames([][]byte{emptyDelimiter, []byte(`{"x":3,"y":4}`)}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	dlqSock.mu.Lock()
	defer dlqSock.mu.Unlock()
	found := false
	for _, frames := range dlqSock.sentFrames {
		if len(frames) > 0 && string(frames[0]) == "/compute-router-dlq-reply-fail/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic when the final reply transmission fails")
	}
}
