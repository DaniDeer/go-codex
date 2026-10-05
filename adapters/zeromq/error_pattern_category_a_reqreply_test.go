package zeromq

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/route"
)

// This file tests Topic 1's Category A full enumeration fix for reqreply
// (see docs/design/d-0005-error-handling.md): zeromq's
// server-side failure points beyond handler/middleware-Fn errors are now
// reqreply.ErrorPattern-eligible too — mirrors mqtt5's own equivalent
// test file, one representative row (request payload decode) since the
// underlying mechanism (ObserveErrorResponseFor) is identical, already
// fully proven there.

func TestErrorPattern_RequestPayloadDecode_Matched_DecodesTypedError(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-payload-decode-ep", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[codex.ValidationErrors, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e codex.ValidationErrors) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "decode_error", Message: "bad payload"}, nil
			},
		).WithCode("decode_error"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute-payload-decode-ep": repSock}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	client := reqreply.NewClient()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{Sockets: map[string]FramedSocket{"/compute-payload-decode-ep": reqSock}})); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	// Bypass the client's own codec-based encode to send a deliberately
	// malformed payload straight over the wire.
	if err := reqSock.SendFrames([][]byte{[]byte(`{}`)}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	frames, recvErr := reqSock.RecvFrames()
	if recvErr != nil {
		t.Fatalf("RecvFrames: %v", recvErr)
	}
	if len(frames) < 3 {
		t.Fatalf("want a 3-frame typed error reply [status, code, body], got %d frames", len(frames))
	}
	if string(frames[1]) != "decode_error" {
		t.Errorf("want code=decode_error, got %q", frames[1])
	}
}

// TestErrorPattern_BoundSecurityMiddlewareFn_Matched_Publishes_ReqReply
// covers the Middleware-dispatched (`.Use()`-attached, reusable class)
// Security Fn failure case — mirrors adapters/mqtt5's own, identically-
// named test exactly (same SecurityMiddleware[struct{},struct{}].
// WithReceive(fn) + .Use() + ErrorPattern[reqreply.SecurityError,...]
// pattern), adapted to zeromq's socket-pair wiring. Added specifically
// to close THIS adapter's own test-coverage blind spot: the
// Satisfies-gated reqreply.SecurityError wrapping fix (this session's
// 4th Phase C review round) touches adapters/mqtt5 AND adapters/zeromq
// via 2 SEPARATE, non-shared dispatch implementations — mqtt5's own
// test does not exercise zeromq's code path at all, so a zeromq-specific
// regression here would otherwise go undetected by `go build`/`go vet`
// alone. Also asserts the previously-missing
// stats.SecurityObserver.RecordSecurityRejection call now fires for this
// specific failure mode on zeromq too.
func TestErrorPattern_BoundSecurityMiddlewareFn_Matched_Publishes_ReqReply(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	rejectingMw := reqreply.SecurityMiddleware[struct{}, struct{}]("bearer3",
		reqreply.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}, nil,
	).WithReceive(func(context.Context, struct{}) (struct{}, error) {
		return struct{}{}, errors.New("rejected by security impl")
	})
	epRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-bound-security-mw-ep", computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "computeBoundSecurityMwZmq", Security: []route.SecurityRequirement{route.Require("bearer3")}},
		reqreply.ErrorPattern[reqreply.SecurityError, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e reqreply.SecurityError) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "security_rejected", Message: e.Error()}, nil
			},
		).WithCode("security_rejected"),
	).Use(rejectingMw)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	obs := &testObserver{}
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Sockets: map[string]FramedSocket{"/compute-bound-security-mw-ep": repSock}, Serve: ServeOptions{Observer: obs}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	// Raw SendFrames (bypassing reqreply.Client) to inspect the reply's
	// wire frames directly — mirrors
	// TestErrorPattern_RequestPayloadDecode_Matched_DecodesTypedError's
	// own approach above. The security Fn ALWAYS rejects regardless of
	// payload content, so any well-formed compute payload triggers it.
	if err := reqSock.SendFrames([][]byte{[]byte(validComputeJSON)}); err != nil {
		t.Fatalf("SendFrames: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	frames, recvErr := reqSock.RecvFrames()
	if recvErr != nil {
		t.Fatalf("RecvFrames: %v", recvErr)
	}
	if len(frames) < 3 {
		t.Fatalf("want a 3-frame typed error reply [status, code, body], got %d frames", len(frames))
	}
	if string(frames[1]) != "security_rejected" {
		t.Errorf("want code=security_rejected, got %q", frames[1])
	}
	if len(obs.securityRejections) != 1 {
		t.Errorf("want 1 RecordSecurityRejection call, got %d", len(obs.securityRejections))
	}
}
