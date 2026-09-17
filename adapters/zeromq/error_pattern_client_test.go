package zeromq

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
)

// This file tests Topic 6's client-side ergonomics (see docs/roadmap/
// error-handling-rest-events-reqreply.md): ErrorPatternAs, HandleErrorPattern/
// Case, and ErrorPatternOpt.Match — all 3 collapse the errors.As +
// type-switch dance into a single conditional. Reuses the SAME full
// round-trip setup as TestAttachClient_ErrorPattern_MatchedReply_DecodesTypedError.

func setUpZmqErrorPatternRoundTrip(t *testing.T, topic string) (clientRoute reqreply.Route[computeReq, computeResp], client *reqreply.Client) {
	t.Helper()
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveZmqConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp](topic, computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{topic: repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Serve(ctx) }()

	clientRoute = reqreply.NewRoute[computeReq, computeResp](topic, computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec).WithCode("conflict"),
	)
	client = reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{topic: reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	return clientRoute, client
}

func TestErrorPatternAs_MatchedReply_ExtractsTypedPayload(t *testing.T) {
	clientRoute, client := setUpZmqErrorPatternRoundTrip(t, "/compute-ep-as")

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := client.Call(callCtx, clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}
	payload, ok := reqreply.ErrorPatternAs[serveZmqErrPayload](callErr)
	if !ok {
		t.Fatalf("want ErrorPatternAs to match, got ok=false for err=%v", callErr)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

func TestHandleErrorPattern_MatchedReply_DispatchesCase(t *testing.T) {
	clientRoute, client := setUpZmqErrorPatternRoundTrip(t, "/compute-ep-handle")

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := client.Call(callCtx, clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}

	var gotCode string
	handled := reqreply.HandleErrorPattern(callErr,
		reqreply.Case(func(p serveZmqErrPayload) { gotCode = p.Code }),
	)
	if !handled {
		t.Fatal("want HandleErrorPattern to report handled=true")
	}
	if gotCode != "conflict" {
		t.Errorf("gotCode = %q, want conflict", gotCode)
	}
}

func TestErrorPatternOpt_Match_MatchedReply_ExtractsTypedPayload(t *testing.T) {
	pattern := reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec).WithCode("conflict")
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveZmqConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-ep-match", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	repSock, reqSock := newChanSocketPair()
	if err := AttachServer(server, map[string]FramedSocket{"/compute-ep-match": repSock}); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	clientRoute := reqreply.NewRoute[computeReq, computeResp]("/compute-ep-match", computeReqCodec, computeRespCodec, pattern)
	client := reqreply.NewClient()
	if err := AttachClient(client, map[string]FramedSocket{"/compute-ep-match": reqSock}); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	_, callErr := client.Call(callCtx, clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}
	payload, ok := pattern.Match(callErr)
	if !ok {
		t.Fatalf("want pattern.Match to succeed, got ok=false for err=%v", callErr)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}
