package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file closes docs/roadmap/capability-requirement-composition.md's
// Phase 2 plumbing gap: a supplied ServeOptions/CallOptions.Capabilities
// QoS/Retained value must be honored on EVERY reply/request publish
// path, not just one of them — previously all three server-side reply
// paths (success, error-pattern-matched, dead-letter) hardcoded QoS 1
// and never set Retained, and the client/Call side never set Retained
// at all.

// TestServe_QoSAppliedToAllReplyPaths verifies a supplied QoS capability
// is honored on ALL THREE server-side reply publish paths.
func TestServe_QoSAppliedToAllReplyPaths(t *testing.T) {
	t.Run("success_reply", func(t *testing.T) {
		server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
		handler := func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		}
		if _, err := computeRoute.WithHandler(handler).Register(server); err != nil {
			t.Fatalf("Register: %v", err)
		}
		serverClient := &mockClient{}
		serverRouter := newMockRouter()
		if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter, Serve: ServeOptions{Capabilities: []Capability{QoS(2)}}})); err != nil {
			t.Fatalf("AttachServer: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		go func() { _ = server.Serve(ctx) }()
		serverRouter.waitHandler("compute/add")

		serverRouter.dispatch("compute/add", &pahomqtt5.Publish{
			Topic:   "compute/add",
			Payload: []byte(validComputeJSON),
			Properties: &pahomqtt5.PublishProperties{
				ResponseTopic:   "replies/client-1",
				CorrelationData: []byte("corr-success-qos"),
			},
		})
		time.Sleep(50 * time.Millisecond)

		pub := serverClient.lastPublished()
		if pub == nil {
			t.Fatal("expected a reply to be published")
		}
		if pub.QoS != 2 {
			t.Errorf("want QoS=2 on success reply, got %d", pub.QoS)
		}
	})

	t.Run("error_pattern_matched_reply", func(t *testing.T) {
		server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
		handler := func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, serveConflictErr{msg: "duplicate"}
		}
		epRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-ep-qos", computeReqCodec, computeRespCodec,
			reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
				func(e serveConflictErr) (serveErrPayload, error) {
					return serveErrPayload{Code: "conflict", Message: e.msg}, nil
				},
			),
		)
		if _, err := epRoute.WithHandler(handler).Register(server); err != nil {
			t.Fatalf("Register: %v", err)
		}
		serverClient := &mockClient{}
		serverRouter := newMockRouter()
		if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter, Serve: ServeOptions{Capabilities: []Capability{QoS(2)}}})); err != nil {
			t.Fatalf("AttachServer: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		go func() { _ = server.Serve(ctx) }()
		serverRouter.waitHandler("compute/add-ep-qos")

		serverRouter.dispatch("compute/add-ep-qos", &pahomqtt5.Publish{
			Topic:   "compute/add-ep-qos",
			Payload: []byte(validComputeJSON),
			Properties: &pahomqtt5.PublishProperties{
				ResponseTopic:   "replies/client-1",
				CorrelationData: []byte("corr-ep-qos"),
			},
		})
		time.Sleep(50 * time.Millisecond)

		pub := serverClient.lastPublished()
		if pub == nil {
			t.Fatal("expected an error-pattern-matched reply to be published")
		}
		if pub.QoS != 2 {
			t.Errorf("want QoS=2 on error-pattern-matched reply, got %d", pub.QoS)
		}
	})

	t.Run("dead_letter_reply", func(t *testing.T) {
		server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
		handler := func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, nil
		}
		dlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/dlq-qos", computeReqCodec, computeRespCodec,
			reqreply.DeadLetter("compute/dlq-qos/dlq"),
		)
		if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
			t.Fatalf("Register: %v", err)
		}
		serverClient := &mockClient{}
		serverRouter := newMockRouter()
		if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter, Serve: ServeOptions{Capabilities: []Capability{QoS(2)}}})); err != nil {
			t.Fatalf("AttachServer: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		go func() { _ = server.Serve(ctx) }()
		serverRouter.waitHandler("compute/dlq-qos")

		serverRouter.dispatch("compute/dlq-qos", &pahomqtt5.Publish{
			Topic:   "compute/dlq-qos",
			Payload: []byte(`{}`), // missing required fields -> decode error -> dead-lettered
			Properties: &pahomqtt5.PublishProperties{
				ResponseTopic:   "replies/client-1",
				CorrelationData: []byte("corr-dlq-qos"),
			},
		})
		time.Sleep(50 * time.Millisecond)

		var dlqPub *pahomqtt5.Publish
		for _, p := range serverClient.published {
			if p.Topic == "compute/dlq-qos/dlq" {
				dlqPub = p
			}
		}
		if dlqPub == nil {
			t.Fatal("expected a dead-letter publish")
		}
		if dlqPub.QoS != 2 {
			t.Errorf("want QoS=2 on dead-letter reply, got %d", dlqPub.QoS)
		}
	})
}

// TestServe_RetainedAppliedToReplyPublishes verifies a supplied Retained
// capability sets Retain=true on the success reply — previously never
// set anywhere on the server side.
func TestServe_RetainedAppliedToReplyPublishes(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		return computeResp{Sum: req.X + req.Y}, nil
	}
	retRoute := reqreply.NewRoute[computeReq, computeResp]("compute/add-retained", computeReqCodec, computeRespCodec)
	if _, err := retRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}
	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter, Serve: ServeOptions{Capabilities: []Capability{Retained(true)}}})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/add-retained")

	serverRouter.dispatch("compute/add-retained", &pahomqtt5.Publish{
		Topic:   "compute/add-retained",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-retained"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := serverClient.lastPublished()
	if pub == nil {
		t.Fatal("expected a reply to be published")
	}
	if !pub.Retain {
		t.Error("want Retain=true on success reply")
	}
}

// TestCall_RetainedAppliedToRequestPublish verifies a supplied Retained
// capability sets Retain=true on the OUTGOING REQUEST publish — a gap
// that existed on the client/Call side regardless of QoS handling
// (Retained had no threading there at all before this fix).
func TestCall_RetainedAppliedToRequestPublish(t *testing.T) {
	clientClient := &mockClient{}
	clientRouter := newMockRouter()

	go func() {
		_, _ = testCall(context.Background(), clientClient, clientRouter, computeRoute.ClientHandle(), computeReq{X: 1, Y: 2},
			CallOptions{Timeout: 100 * time.Millisecond, Capabilities: []Capability{Retained(true)}})
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	var pub *pahomqtt5.Publish
	for time.Now().Before(deadline) {
		if p := clientClient.lastPublished(); p != nil && p.Topic == "compute/add" {
			pub = p
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pub == nil {
		t.Fatal("expected the outgoing request to be published")
	}
	if !pub.Retain {
		t.Error("want Retain=true on the outgoing request publish")
	}
}
