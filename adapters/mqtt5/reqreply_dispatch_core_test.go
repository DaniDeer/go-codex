package mqtt5

import (
	"context"
	"testing"

	"github.com/DaniDeer/go-codex/stats"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// TestReqreplyServerDispatchOne_DirectCall proves
// [reqreplyServerSetupCore]/[reqreplyServerDispatchOne] themselves —
// independent of (*serverTransport).Serve — correctly decode a request,
// dispatch to the handler, and encode/publish the reply. Phase 4
// events/reqreply family, Workstream A's step 8 (docs/roadmap/
// adapter-integration-architecture.md): a conformance seed exercising
// the shared dispatch core directly, mirroring adapters/zeromq's own
// reqreply_dispatch_core_test.go for this family's server side.
func TestReqreplyServerDispatchOne_DirectCall(t *testing.T) {
	var callCount int
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		callCount++
		return computeResp{Sum: r.X + r.Y}, nil
	}

	client := &mockClient{}
	setup, err := reqreplyServerSetupCore(newRouteHandle(), fn, nil, stats.NoopObserver{})
	if err != nil {
		t.Fatalf("reqreplyServerSetupCore: %v", err)
	}

	reqreplyServerDispatchOne(context.Background(), setup, client, stats.NoopObserver{}, nil, nil, &pahomqtt5.Publish{
		Topic:   setup.path,
		Payload: []byte(`{"x":3,"y":4}`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/compute",
			CorrelationData: []byte("corr-1"),
		},
	})

	if callCount != 1 {
		t.Fatalf("handler call count: want 1, got %d", callCount)
	}
	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("want a reply to be published, got none")
	}
	if pub.Topic != "replies/compute" {
		t.Errorf("reply topic: want %q, got %q", "replies/compute", pub.Topic)
	}
	wantPayload := `{"sum":7}`
	if string(pub.Payload) != wantPayload {
		t.Errorf("reply payload: want %q, got %q", wantPayload, string(pub.Payload))
	}
}

// TestReqreplyServerDispatchOne_DecodeFailure_InvokesOnError proves a
// decode failure reaches onError and the handler itself is never
// invoked — exercised directly against [reqreplyServerDispatchOne],
// independent of which Serve call built it.
func TestReqreplyServerDispatchOne_DecodeFailure_InvokesOnError(t *testing.T) {
	var handlerCalled bool
	fn := func(_ context.Context, r computeReq) (computeResp, error) {
		handlerCalled = true
		return computeResp{}, nil
	}

	client := &mockClient{}
	setup, err := reqreplyServerSetupCore(newRouteHandle(), fn, nil, stats.NoopObserver{})
	if err != nil {
		t.Fatalf("reqreplyServerSetupCore: %v", err)
	}

	var gotErr ServeError
	reqreplyServerDispatchOne(context.Background(), setup, client, stats.NoopObserver{}, nil,
		func(e ServeError) { gotErr = e },
		&pahomqtt5.Publish{Topic: setup.path, Payload: []byte(`not valid json`)},
	)

	if handlerCalled {
		t.Error("handler must NOT be called on a decode failure")
	}
	if gotErr.Kind != KindDecode {
		t.Errorf("want KindDecode, got %v", gotErr.Kind)
	}
	if gotErr.Err == nil {
		t.Error("want a non-nil decode error")
	}
}
