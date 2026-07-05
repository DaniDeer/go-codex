package mqtt5_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mqtt5 "github.com/DaniDeer/go-codex/adapters/mqtt5"
	"github.com/DaniDeer/go-codex/api/reqreply"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

const validComputeJSON = `{"x":3,"y":4}`

// ── ServeRequestReply tests ───────────────────────────────────────────────────

func TestServeRequestReply_ValidRoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{})
	if err != nil {
		t.Fatalf("ServeRequestReply setup failed: %v", err)
	}

	// Dispatch a request message with ResponseTopic set.
	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-42"),
		},
	})

	// Give the async handler time to process.
	time.Sleep(50 * time.Millisecond)

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if pub.Topic != "replies/client-1" {
		t.Fatalf("expected reply to 'replies/client-1', got %q", pub.Topic)
	}
	if pub.Properties == nil || string(pub.Properties.CorrelationData) != "corr-42" {
		t.Fatalf("expected CorrelationData 'corr-42' in reply, got %v", pub.Properties)
	}
}

func TestServeRequestReply_DecodeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr mqtt5.ServeRequestReplyError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			t.Fatal("fn must not be called on decode error")
			return computeResp{}, nil
		},
		mqtt5.ServeOptions{OnError: func(e mqtt5.ServeRequestReplyError) { gotErr = e }})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(`bad json`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-43"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != mqtt5.KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
}

func TestServeRequestReply_HandlerError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr mqtt5.ServeRequestReplyError
	handlerErr := errors.New("compute overflow")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, handlerErr
		},
		mqtt5.ServeOptions{OnError: func(e mqtt5.ServeRequestReplyError) { gotErr = e }})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-44"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != mqtt5.KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
}

func TestServeRequestReply_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{Observer: obs})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic: "replies/r1",
		},
	})
	time.Sleep(80 * time.Millisecond)

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestServeRequestReply_ObserverRecordRequestFailure(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) { return computeResp{}, nil },
		mqtt5.ServeOptions{Observer: obs})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:      "compute/add",
		Payload:    []byte(`bad`),
		Properties: &pahomqtt5.PublishProperties{ResponseTopic: "replies/r1"},
	})
	time.Sleep(80 * time.Millisecond)

	if len(obs.requests) != 1 || obs.requests[0] != 0 {
		t.Fatalf("expected RecordRequest(0), got %v", obs.requests)
	}
}

func TestServeRequestReply_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{Observer: obs})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:      "compute/add",
		Payload:    []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{ResponseTopic: "replies/r1"},
	})
	time.Sleep(80 * time.Millisecond)

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.serve" {
		t.Fatalf("expected StartSpan 'mqtt5.serve', got %v", obs.startSpanOps)
	}
}

// ── Request tests ─────────────────────────────────────────────────────────────

// brokerClient simulates a broker: when a message is published to a topic,
// it checks if any router handler exists for that topic and dispatches immediately.
type brokerClient struct {
	mockClient
	router *mockRouter
}

func (c *brokerClient) Publish(ctx context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	resp, err := c.mockClient.Publish(ctx, p)
	if err != nil {
		return nil, err
	}
	// Simulate broker routing: dispatch to any registered handler.
	go c.router.dispatch(p.Topic, p)
	return resp, nil
}

func TestRequest_ValidRoundTrip(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Set up the responder.
	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{})

	// Make a request.
	resp, err := mqtt5.Request(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		mqtt5.RequestOptions{
			ReplyTopicPrefix: "replies",
			Timeout:          2 * time.Second,
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Fatalf("expected Sum=7, got %d", resp.Sum)
	}
}

func TestRequest_Timeout(t *testing.T) {
	// Client that never triggers the reply.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := mqtt5.Request(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			ReplyTopicPrefix: "replies",
			Timeout:          100 * time.Millisecond,
		})

	var reqErr mqtt5.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T: %v", err, err)
	}
	if reqErr.Kind != mqtt5.KindTimeout {
		t.Fatalf("expected KindTimeout, got %v", reqErr.Kind)
	}
}

func TestRequest_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{})

	_, _ = mqtt5.Request(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{Timeout: 2 * time.Second, Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestRequest_ObserverRecordRequestTimeout(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _ = mqtt5.Request(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{Timeout: 50 * time.Millisecond, Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 0 {
		t.Fatalf("expected RecordRequest(0) on timeout, got %v", obs.requests)
	}
}

func TestRequest_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_ = mqtt5.ServeRequestReply(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		mqtt5.ServeOptions{})

	_, _ = mqtt5.Request(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{Timeout: 2 * time.Second, Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.request" {
		t.Fatalf("expected StartSpan 'mqtt5.request', got %v", obs.startSpanOps)
	}
}

func TestRequest_ContextCancelled(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := mqtt5.Request(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{Timeout: 5 * time.Second})

	var reqErr mqtt5.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError on ctx cancel, got %T: %v", err, err)
	}
}

// ── Request with topic vars ───────────────────────────────────────────────────

func TestRequest_WithVars_PublishesToResolvedTopic(t *testing.T) {
	// Verifies that Request resolves the template topic before publishing.
	// The resolved topic "compute/acme/add" must appear in the published message.
	templateRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/{tenantID}/add",
		computeReqCodec, computeRespCodec,
	)
	client := &mockClient{} // plain mock — no routing, just records publishes
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, _ := templateRoute.Register(b)

	// Request will timeout (no responder), but we only care that it published
	// to the resolved topic "compute/acme/add".
	_, _ = mqtt5.Request(ctx, client, router, handle,
		computeReq{X: 3, Y: 4},
		mqtt5.RequestOptions{
			Vars:    map[string]string{"tenantID": "acme"},
			Timeout: 100 * time.Millisecond, // fast timeout
		})

	// Find the outgoing request publish (not the reply-topic subscribe).
	var requestPub *pahomqtt5.Publish
	client.mu.Lock()
	for _, p := range client.published {
		if p.Topic == "compute/acme/add" {
			requestPub = p
			break
		}
	}
	client.mu.Unlock()

	if requestPub == nil {
		t.Fatal("expected a publish to 'compute/acme/add', got none")
	}
	if requestPub.Properties == nil || requestPub.Properties.ResponseTopic == "" {
		t.Fatal("published request must carry ResponseTopic property")
	}
}

func TestRequest_WithVars_MissingVar_ReturnsRequestError(t *testing.T) {
	templateRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/{tenantID}/add",
		computeReqCodec, computeRespCodec,
	)
	router := newMockRouter()
	client := &mockClient{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, _ := templateRoute.Register(b)

	_, err := mqtt5.Request(ctx, client, router, handle,
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			Vars:    map[string]string{}, // tenantID missing
			Timeout: time.Second,
		})

	var reqErr mqtt5.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T: %v", err, err)
	}
	var missing reqreply.MissingRouteParamError
	if !errors.As(reqErr, &missing) {
		t.Fatalf("expected MissingRouteParamError inside RequestError, got %T", reqErr.Err)
	}
}

func TestRequest_WithVars_MissingVar_ReportsRequiredConstraintWithVarName(t *testing.T) {
	// Verifies that a missing topic variable is reported to the observer with
	// constraint "required" and the variable name as the field — consistent with
	// the mqtt adapter's reportMissingTopicVarErrors behaviour.
	templateRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/{tenantID}/add",
		computeReqCodec, computeRespCodec,
	)
	obs := &testObserver{}
	router := newMockRouter()
	client := &mockClient{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, _ := templateRoute.Register(b)

	_, _ = mqtt5.Request(ctx, client, router, handle,
		computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			Vars:     map[string]string{}, // tenantID missing
			Observer: obs,
		})

	if len(obs.validationFull) == 0 {
		t.Fatal("expected RecordValidationError to be called for missing topic variable")
	}
	got := obs.validationFull[0]
	if got.location != "topic_var" {
		t.Errorf("expected location=%q, got %q", "topic_var", got.location)
	}
	if got.constraint != "required" {
		t.Errorf("expected constraint=%q, got %q", "required", got.constraint)
	}
	if got.field != "tenantID" {
		t.Errorf("expected field=%q, got %q", "tenantID", got.field)
	}
}
