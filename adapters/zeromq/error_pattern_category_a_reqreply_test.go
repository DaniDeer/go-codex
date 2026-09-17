package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
)

// This file tests Topic 1's Category A full enumeration fix for reqreply
// (see docs/roadmap/error-handling-rest-events-reqreply.md): zeromq's
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
	if err := AttachServer(server, map[string]FramedSocket{"/compute-payload-decode-ep": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{"/compute-payload-decode-ep": reqSock}); err != nil {
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
