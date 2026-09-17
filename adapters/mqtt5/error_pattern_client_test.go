package mqtt5

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

func setUpErrorPatternRoundTrip(t *testing.T, topic string) (clientRoute reqreply.Route[computeReq, computeResp], client *reqreply.Client) {
	t.Helper()
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp](topic, computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	t.Cleanup(cancel)
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler(topic)

	clientRoute = reqreply.NewRoute[computeReq, computeResp](topic, computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec).WithCode("conflict"),
	)
	client = reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)
	return clientRoute, client
}

func TestErrorPatternAs_MatchedReply_ExtractsTypedPayload(t *testing.T) {
	clientRoute, client := setUpErrorPatternRoundTrip(t, "compute/add-ep-as")

	_, callErr := client.Call(context.Background(), clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}
	payload, ok := reqreply.ErrorPatternAs[serveErrPayload](callErr)
	if !ok {
		t.Fatalf("want ErrorPatternAs to match, got ok=false for err=%v", callErr)
	}
	if payload.Code != "conflict" {
		t.Errorf("code = %q, want conflict", payload.Code)
	}
}

func TestHandleErrorPattern_MatchedReply_DispatchesCase(t *testing.T) {
	clientRoute, client := setUpErrorPatternRoundTrip(t, "compute/add-ep-handle")

	_, callErr := client.Call(context.Background(), clientRoute, computeReq{X: 1, Y: 2})
	if callErr == nil {
		t.Fatal("want an error, got nil")
	}

	var gotCode string
	handled := reqreply.HandleErrorPattern(callErr,
		reqreply.Case(func(p serveErrPayload) { gotCode = p.Code }),
	)
	if !handled {
		t.Fatal("want HandleErrorPattern to report handled=true")
	}
	if gotCode != "conflict" {
		t.Errorf("gotCode = %q, want conflict", gotCode)
	}
}

func TestErrorPatternOpt_Match_MatchedReply_ExtractsTypedPayload(t *testing.T) {
	pattern := reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec).WithCode("conflict")
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, serveConflictErr{msg: "duplicate"}
	}
	epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-match", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		).WithCode("conflict"),
	)
	if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-ep-match")

	clientRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-match", computeReqCodec, computeRespCodec, pattern)
	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := AttachClient(client, clientClient, clientRouter); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)

	_, callErr := client.Call(context.Background(), clientRoute, computeReq{X: 1, Y: 2})
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
