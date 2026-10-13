package mqtt5

import (
	"context"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// This file tests docs/design/d-0004-reqreply-workflow-simplification.md's own
// Phase 2.1 end to end, against the REAL mqtt5 wire — the single most
// important proof the whole wire-level properties addendum (Phase 1.5 +
// Phase 2.0) exists for: a dead-lettered request's header-delivered
// credential is NOT permanently lost — it survives capture into the
// published DeadLetterEnvelope AND republish via Client.ReplayDeadLetter,
// reaching the target a SECOND time as a real MQTT5 User Property.
//
// Decodes the real published DLQ payload via [reqreply.
// DecodeDeadLetterEnvelope] — `deep-dive-bug-hunt` Round 206's own fix
// (this test previously hand-rolled a JSON-field-name-mirroring struct,
// an undocumented, compiler-unenforced coupling to api/reqreply's own
// private codec; DecodeDeadLetterEnvelope is now the sanctioned path,
// guaranteed to stay in sync since it shares the SAME codec
// [RouteHandle.DeadLetterFor] encodes with).

// TestClient_ReplayDeadLetter_EndToEndViaRealMQTT5Adapter_CredentialSurvivesReplay
// is Phase 2.1's own real end-to-end test: a route's handler fails on
// its FIRST invocation (a business error, unrelated to the credential),
// dead-lettering the request — captures the real incoming "Authorization"
// User Property into the published envelope's Properties (Phase 2.0).
// An ops tool decodes that envelope off the wire and calls
// Client.ReplayDeadLetter — the SAME credential must reach the target a
// SECOND time as a real MQTT5 User Property, and the handler (now
// succeeding on its second invocation) must actually receive it.
func TestClient_ReplayDeadLetter_EndToEndViaRealMQTT5Adapter_CredentialSurvivesReplay(t *testing.T) {
	server := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})

	var attempt int
	var gotCred string
	handler := func(_ context.Context, req computeReq) (computeResp, error) {
		attempt++
		if attempt == 1 {
			return computeResp{}, serveConflictErr{msg: "transient failure"}
		}
		return computeResp{Sum: req.X + req.Y}, nil
	}
	dlRoute := reqreply.NewRoute[computeReq, computeResp]("compute/dlq-replay-e2e", computeReqCodec, computeRespCodec,
		reqreply.DeadLetter("compute/dlq-replay-e2e/dlq"),
	)
	if _, err := dlRoute.WithHandler(handler).Register(server); err != nil {
		t.Fatalf("Register: %v", err)
	}

	serverClient := &mockClient{}
	serverRouter := newMockRouter()
	if err := server.Attach(NewServerTransport(ServerTransportOptions{Client: serverClient, Router: serverRouter})); err != nil {
		t.Fatalf("AttachServer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	serverRouter.waitHandler("compute/dlq-replay-e2e")

	// Original dispatch: carries the credential as a real User Property,
	// hits the handler's FIRST (failing) invocation, dead-letters.
	serverRouter.dispatch("compute/dlq-replay-e2e", &pahomqtt5.Publish{
		Topic:   "compute/dlq-replay-e2e",
		Payload: []byte(`{"x":3,"y":4}`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/original",
			CorrelationData: []byte("corr-original"),
			User:            pahomqtt5.UserProperties{{Key: "Authorization", Value: "Bearer secret-token"}},
		},
	})
	time.Sleep(50 * time.Millisecond)

	var dlqPayload []byte
	var found bool
	serverClient.mu.Lock()
	for _, p := range serverClient.published {
		if p.Topic == "compute/dlq-replay-e2e/dlq" {
			dlqPayload = p.Payload
			found = true
		}
	}
	serverClient.mu.Unlock()
	if !found {
		t.Fatal("want a publish to the declared dead-letter topic")
	}

	// An ops/replay tool decodes the real published envelope and
	// replays it via a REAL client, over the SAME real mqtt5 wire.
	envelope, decErr := reqreply.DecodeDeadLetterEnvelope(dlqPayload)
	if decErr != nil {
		t.Fatalf("DecodeDeadLetterEnvelope: %v", decErr)
	}
	if envelope.Properties["Authorization"] != "Bearer secret-token" {
		t.Fatalf("DeadLetterEnvelope.Properties = %+v, want {Authorization: Bearer secret-token}", envelope.Properties)
	}

	client := reqreply.NewClient()
	clientClient := &mockClient{}
	clientRouter := newMockRouter()
	if err := client.Attach(NewClientTransport(ClientTransportOptions{Client: clientClient, Router: clientRouter})); err != nil {
		t.Fatalf("AttachClient: %v", err)
	}
	wireBrokers(t, serverClient, clientRouter)
	wireBrokers(t, clientClient, serverRouter)
	if err := client.RegisterRoute(dlRoute); err != nil {
		t.Fatalf("RegisterRoute: %v", err)
	}

	respAny, err := client.ReplayDeadLetter(context.Background(), envelope)
	if err != nil {
		t.Fatalf("ReplayDeadLetter: want the handler's SECOND invocation to succeed, got error: %v", err)
	}
	resp, ok := respAny.(computeResp)
	if !ok {
		t.Fatalf("ReplayDeadLetter resp type = %T, want computeResp", respAny)
	}
	if resp.Sum != 7 {
		t.Errorf("resp.Sum = %d, want 7 (3+4, the SAME original request bytes replayed verbatim)", resp.Sum)
	}
	if attempt != 2 {
		t.Fatalf("handler invocations = %d, want 2 (1 original failure + 1 replay success)", attempt)
	}

	// The credential-survives-replay proof: the REPUBLISHED request (the
	// one ReplayDeadLetter's CallRaw actually sent over the real wire)
	// carries the SAME Authorization User Property — not just the
	// Payload bytes.
	clientClient.mu.Lock()
	defer clientClient.mu.Unlock()
	var replayCred string
	for _, p := range clientClient.published {
		if p.Topic != "compute/dlq-replay-e2e" {
			continue
		}
		gotCred = propertyVarsFromUserProperties(p)["Authorization"]
		replayCred = gotCred
	}
	if replayCred != "Bearer secret-token" {
		t.Errorf("republished request's Authorization User Property = %q, want %q", replayCred, "Bearer secret-token")
	}

	cancel()
	<-errCh
}
