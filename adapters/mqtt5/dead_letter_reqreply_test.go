package mqtt5

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests Topic 4's DeadLetter fallback for reqreply's server
// side (see docs/roadmap/error-handling-rest-events-reqreply.md): a
// route that declares reqreply.DeadLetter dead-letters a request that
// fails before a successful reply can be produced.

func TestDeadLetter_ServeRequestPayloadDecode_Published(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handler := func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/dlq-decode", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("compute/dlq-decode/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
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
	serverRouter.waitHandler("compute/dlq-decode")

	serverRouter.dispatch("compute/dlq-decode", &pahomqtt5.Publish{
		Topic:   "compute/dlq-decode",
		Payload: []byte(`{}`), // missing required fields -> codex.ValidationErrors
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-decode"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	found := false
	for _, p := range serverClient.published {
		if p.Topic == "compute/dlq-decode/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic")
	}
}

// TestDeadLetter_ServeReplyTransmissionFailure_Published tests G1's fix
// (session review round 3 finding): a broker-level rejection of the
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
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/dlq-reply-fail", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("compute/dlq-reply-fail/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// The reply is published to "replies/client-1" (the ResponseTopic the
	// dispatched request carries) — fail exactly that publish while
	// letting the dead-letter publish (a DIFFERENT topic) succeed.
	serverClient := &topicFailingClient{failTopic: "replies/client-1", failErr: errors.New("broker rejected reply")}
	serverRouter := newMockRouter()
	if err := AttachServer(server, serverClient, serverRouter); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	go func() { _ = server.Serve(ctx) }()
	serverRouter.waitHandler("compute/dlq-reply-fail")

	serverRouter.dispatch("compute/dlq-reply-fail", &pahomqtt5.Publish{
		Topic:   "compute/dlq-reply-fail",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-reply-fail"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	found := false
	for _, p := range serverClient.published {
		if p.Topic == "compute/dlq-reply-fail/dlq" {
			found = true
		}
	}
	if !found {
		t.Error("want a publish to the declared dead-letter topic when the final reply transmission fails")
	}
}
