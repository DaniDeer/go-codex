package mqtt5_test

import (
	"context"
	"errors"
	"strings"
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

// ── ReplyTopicBuilder unit tests ──────────────────────────────────────────────

func TestUUIDReplyTopic_DefaultPrefix(t *testing.T) {
	b := mqtt5.UUIDReplyTopic("")
	topic, filter := b()
	if !strings.HasPrefix(topic, "replies/") {
		t.Errorf("expected topic to start with %q, got %q", "replies/", topic)
	}
	if !strings.HasPrefix(filter, "replies/") {
		t.Errorf("expected filter to start with %q, got %q", "replies/", filter)
	}
}

func TestUUIDReplyTopic_CustomPrefix(t *testing.T) {
	b := mqtt5.UUIDReplyTopic("myns")
	topic, filter := b()
	if !strings.HasPrefix(topic, "myns/") {
		t.Errorf("expected topic prefix %q, got %q", "myns/", topic)
	}
	if !strings.HasPrefix(filter, "myns/") {
		t.Errorf("expected filter prefix %q, got %q", "myns/", filter)
	}
}

func TestUUIDReplyTopic_EqualTopicAndFilter(t *testing.T) {
	b := mqtt5.UUIDReplyTopic("replies")
	topic, filter := b()
	if topic != filter {
		t.Errorf("expected responseTopic == subscribeFilter, got %q vs %q", topic, filter)
	}
}

func TestUUIDReplyTopic_UniquePerCall(t *testing.T) {
	b := mqtt5.UUIDReplyTopic("replies")
	t1, _ := b()
	t2, _ := b()
	if t1 == t2 {
		t.Errorf("expected unique topics per call, both returned %q", t1)
	}
}

func TestSharedReplyTopic_SubscribeFilterHasSharePrefix(t *testing.T) {
	b := mqtt5.SharedReplyTopic("replies", "pool")
	_, filter := b()
	if !strings.HasPrefix(filter, "$share/pool/replies/") {
		t.Errorf("expected filter to start with %q, got %q", "$share/pool/replies/", filter)
	}
}

func TestSharedReplyTopic_ResponseTopicHasNoSharePrefix(t *testing.T) {
	b := mqtt5.SharedReplyTopic("replies", "pool")
	topic, _ := b()
	if strings.HasPrefix(topic, "$share/") {
		t.Errorf("responseTopic must not carry $share prefix, got %q", topic)
	}
	if !strings.HasPrefix(topic, "replies/") {
		t.Errorf("expected responseTopic to start with %q, got %q", "replies/", topic)
	}
}

func TestSharedReplyTopic_UniquePerCall(t *testing.T) {
	b := mqtt5.SharedReplyTopic("replies", "pool")
	t1, f1 := b()
	t2, f2 := b()
	if t1 == t2 {
		t.Errorf("expected unique response topics, both returned %q", t1)
	}
	if f1 == f2 {
		t.Errorf("expected unique subscribe filters, both returned %q", f1)
	}
}

func TestSharedReplyTopic_DefaultPrefix(t *testing.T) {
	b := mqtt5.SharedReplyTopic("", "pool")
	topic, filter := b()
	if !strings.HasPrefix(topic, "replies/") {
		t.Errorf("expected responseTopic prefix %q, got %q", "replies/", topic)
	}
	if !strings.HasPrefix(filter, "$share/pool/replies/") {
		t.Errorf("expected filter prefix %q, got %q", "$share/pool/replies/", filter)
	}
}

// ── Request integration tests for ReplyTopicBuilder ───────────────────────────

func TestRequest_ReplyTopicBuilder_UsesReturnedResponseTopic(t *testing.T) {
	// Verifies that the ResponseTopic property on the published request matches
	// the responseTopic returned by the builder.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	customTopic := "custom/replies/abc"
	_, _ = mqtt5.Request(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			ReplyTopicBuilder: func() (string, string) { return customTopic, customTopic },
			Timeout:           100 * time.Millisecond,
		})

	// Find the published request (not the reply-topic subscription).
	var reqPub *pahomqtt5.Publish
	client.mu.Lock()
	for _, p := range client.published {
		if p.Topic == "compute/add" {
			reqPub = p
			break
		}
	}
	client.mu.Unlock()

	if reqPub == nil {
		t.Fatal("expected a publish to compute/add")
	}
	if reqPub.Properties == nil || reqPub.Properties.ResponseTopic != customTopic {
		t.Errorf("expected ResponseTopic=%q, got %q",
			customTopic, reqPub.Properties.ResponseTopic)
	}
}

func TestRequest_ReplyTopicBuilder_UsesReturnedSubscribeFilter(t *testing.T) {
	// Verifies that client.Subscribe is called with the subscribeFilter returned
	// by the builder, not the responseTopic.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	responseTopic := "replies/abc"
	sharedFilter := "$share/mygroup/replies/abc"

	_, _ = mqtt5.Request(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			ReplyTopicBuilder: func() (string, string) { return responseTopic, sharedFilter },
			Timeout:           100 * time.Millisecond,
		})

	filters := client.subscribedFilters()
	var found bool
	for _, f := range filters {
		if f == sharedFilter {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected client.Subscribe with filter %q, subscribed to: %v", sharedFilter, filters)
	}
}

func TestRequest_ReplyTopicBuilder_EmptyResponseTopic_ReturnsRequestError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx := context.Background()

	b := reqreply.NewBuilder(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	_, err := mqtt5.Request(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			ReplyTopicBuilder: func() (string, string) { return "", "" },
		})

	var reqErr mqtt5.RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequestError, got %T: %v", err, err)
	}
	if reqErr.Kind != mqtt5.KindEncode {
		t.Errorf("expected KindEncode, got %v", reqErr.Kind)
	}
}

func TestRequest_ReplyTopicBuilder_EmptyFilter_FallsBackToResponseTopic(t *testing.T) {
	// When subscribeFilter is empty, Request must fall back to responseTopic.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	responseTopic := "replies/fallback"
	_, _ = mqtt5.Request(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			ReplyTopicBuilder: func() (string, string) { return responseTopic, "" },
			Timeout:           100 * time.Millisecond,
		})

	filters := client.subscribedFilters()
	var found bool
	for _, f := range filters {
		if f == responseTopic {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected subscribe fallback to responseTopic %q, subscribed to: %v", responseTopic, filters)
	}
}

func TestRequest_NilBuilder_UsesReplyTopicPrefix(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	_, _ = mqtt5.Request(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			ReplyTopicPrefix: "custom-prefix",
			Timeout:          100 * time.Millisecond,
		})

	// The reply subscription filter must start with the custom prefix.
	filters := client.subscribedFilters()
	var found bool
	for _, f := range filters {
		if strings.HasPrefix(f, "custom-prefix/") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected subscribe filter starting with %q, subscribed to: %v", "custom-prefix/", filters)
	}
}

func TestRequest_NilBuilder_DefaultPrefix(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewBuilder(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	_, _ = mqtt5.Request(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		mqtt5.RequestOptions{
			Timeout: 100 * time.Millisecond,
		})

	filters := client.subscribedFilters()
	var found bool
	for _, f := range filters {
		if strings.HasPrefix(f, "replies/") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected default subscribe filter starting with %q, subscribed to: %v", "replies/", filters)
	}
}
