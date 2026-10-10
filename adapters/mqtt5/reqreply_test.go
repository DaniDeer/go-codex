package mqtt5

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

const validComputeJSON = `{"x":3,"y":4}`

// ── Serve tests ──────────────────────────────────────────────────────

func TestServe_ValidRoundTrip(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})
	if err != nil {
		t.Fatalf("Serve setup failed: %v", err)
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

func TestServe_DecodeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr ServeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			t.Fatal("fn must not be called on decode error")
			return computeResp{}, nil
		},
		ServeOptions{OnError: func(e ServeError) { gotErr = e }})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(`bad json`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-43"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
}

func TestServe_HandlerError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr ServeError
	handlerErr := errors.New("compute overflow")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, handlerErr
		},
		ServeOptions{OnError: func(e ServeError) { gotErr = e }})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-44"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
}

// ── Security (Phase 3) ────────────────────────────────────────────────────────

func TestServe_BuiltInCredentialCheck_RejectsMalformedCredential(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	obs := &testObserver{}
	var gotErr ServeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newSecuredRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			t.Fatal("fn must not be called on security rejection")
			return computeResp{}, nil
		},
		ServeOptions{
			Observer: obs,
			OnError:  func(e ServeError) { gotErr = e },
		})

	// No "Authorization" User Property -> extracted credential is "" ->
	// fails the non-empty-string Codec, before any SecurityFunc runs.
	router.dispatch("compute/secured-add", &pahomqtt5.Publish{
		Topic:   "compute/secured-add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-sec-1"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var credErr reqreply.SecurityCredentialError
	if !errors.As(gotErr, &credErr) {
		t.Fatalf("expected reqreply.SecurityCredentialError, got %v", gotErr.Err)
	}
	if credErr.Scheme != "bearer" {
		t.Errorf("want Scheme=bearer, got %q", credErr.Scheme)
	}
	if len(obs.secRejections) != 1 {
		t.Errorf("want 1 security rejection recorded, got %d", len(obs.secRejections))
	}
	// An error reply must still be published — the requester must not be
	// left waiting indefinitely.
	pub := client.lastPublished()
	if pub == nil || pub.Topic != "replies/client-1" {
		t.Fatalf("expected error reply published to replies/client-1, got %v", pub)
	}
}

// TestServe_HandleMW_PairedSecurityFn_Verifies confirms a PAIRED
// HandleMW security Fn actually gets called and CAN reject — REPLACES
// the OLD TestServe_SecurityFunc_RejectsRequest (ServeOptions.
// SecurityFunc was removed entirely, Phase 1 (see
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum).
func TestServe_HandleMW_PairedSecurityFn_Verifies(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr ServeError
	secErr := errors.New("token revoked")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	rejectingImpl := func(context.Context, *computeReq, mwSecIn) (mwSecOut, error) {
		return mwSecOut{}, secErr
	}
	_ = testServe(ctx, client, router, newSecuredRouteHandleWithImpl(rejectingImpl),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			t.Fatal("fn must not be called on HandleMW security rejection")
			return computeResp{}, nil
		},
		ServeOptions{OnError: func(e ServeError) { gotErr = e }})

	router.dispatch("compute/secured-add", &pahomqtt5.Publish{
		Topic:   "compute/secured-add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-sec-2"),
			User:            pahomqtt5.UserProperties{{Key: "Authorization", Value: "******"}},
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var wrapped reqreply.SecurityError
	if !errors.As(gotErr, &wrapped) {
		t.Fatalf("expected reqreply.SecurityError, got %v", gotErr.Err)
	}
	if !errors.Is(gotErr, secErr) {
		t.Fatal("errors.Is must find secErr via Unwrap chain")
	}
}

// TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError confirms
// a route declaring a security scheme (via .Use()) with NO attached
// HandleMW implementation fails Serve construction with
// [reqreply.MissingSecurityMiddlewareError] — REPLACES the OLD
// TestServe_NilSecurityFunc_NotAnError, whose premise (a declared scheme
// with no enforcement mechanism silently succeeding) no longer holds:
// Phase 1's mandatory coverage check closes exactly this latent gap.
func TestAttachServer_CheckCoverage_MissingSecurityMiddlewareError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := testServe(ctx, client, router, newSecuredRouteHandleNoImpl(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	var missing reqreply.MissingSecurityMiddlewareError
	if !errors.As(err, &missing) {
		t.Fatalf("want reqreply.MissingSecurityMiddlewareError, got %v", err)
	}
}

// serverOnlyCoverageMw is the server-coverage-only HandleBoundMW
// attachment newSecuredRouteHandleWithClientImpl uses below — a
// DIFFERENT scheme name ("bearerServerOnly") from the CLIENT-under-test
// scheme ("bearer"), purely to satisfy Register's fused declare+
// implement bookkeeping (a Security-carrying HandleBoundMW and
// ClientBoundMW for the SAME scheme name cannot share ONE route value,
// see docs/design/d-0001-rest-middleware-workflow-simplification.md's Addendum 8). Deliberately
// built WITHOUT bearerSecBoundMw's shared Codec (unlike "bearer" itself)
// — both "bearerServerOnly" and "bearer" end up merged into the SAME
// route's h.Security list, and a Codec on "bearerServerOnly" would make
// the client-side built-in credential check ALSO validate it (no
// credential for it is ever supplied), nondeterministically reporting
// IT as the failing scheme instead of "bearer" depending on map
// iteration order.
func serverOnlyCoverageMw() reqreply.BoundMiddleware[computeReq, mwSecIn, mwSecOut] {
	return reqreply.BoundSecurityMiddleware[computeReq, mwSecIn, mwSecOut](
		"bearerServerOnly", reqreply.BearerScheme("JWT"), nil, acceptingSecurityImpl,
	)
}

// newSecuredRouteHandleWithClientImpl builds a securedComputeRoute
// variant with BOTH a server-side HandleBoundMW (accepting,
// unconditional — these tests exercise CLIENT-side ClientBoundMW
// behavior, not server rejection) and a client-side ClientBoundMW
// attached, registered against a FRESH server so [Call] (the escape
// hatch, which delegates to clientTransport.call since Phase 0b) can
// dispatch through handle.ClientImplementations. Only the CLIENT half
// ("bearer") is actually under test here — see serverOnlyCoverageMw's
// own doc comment for why the server half uses a separate scheme name
// AND no Codec.
func newSecuredRouteHandleWithClientImpl(clientFn func(ctx context.Context, req computeReq) (mwSecIn, error)) *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	freshRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/secured-add",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "securedCompute", Security: []reqreply.SecurityRequirement{reqreply.Require("bearer")}},
	)
	h, err := freshRoute.
		HandleBoundMW(serverOnlyCoverageMw()).
		ClientBoundMW(bearerSecBoundClientMw(clientFn)).
		Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// TestCall_ClientMW_PairedCredentialFn_Supplies confirms a PAIRED
// ClientMW credential-supplying Fn actually gets called and supplies a
// credential — REPLACES the OLD TestCall_CredentialFunc_ValidFormat_Passes
// (CallOptions.CredentialFunc was removed entirely, Phase 1 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum).
func TestCall_ClientMW_PairedCredentialFn_Supplies(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		time.Sleep(10 * time.Millisecond)
		pub := client.lastPublished()
		if pub == nil {
			return
		}
		router.dispatch(pub.Properties.ResponseTopic, &pahomqtt5.Publish{
			Topic:   pub.Properties.ResponseTopic,
			Payload: []byte(`{"sum":7}`),
			Properties: &pahomqtt5.PublishProperties{
				CorrelationData: pub.Properties.CorrelationData,
			},
		})
	}()

	handle := newSecuredRouteHandleWithClientImpl(func(context.Context, computeReq) (mwSecIn, error) {
		return mwSecIn{Authorization: "******"}, nil
	})
	resp, err := testCall(ctx, client, router, handle, computeReq{X: 3, Y: 4}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("want Sum=7, got %d", resp.Sum)
	}
	pub := client.lastPublished()
	if pub == nil || pub.Properties == nil || pub.Properties.User.Get("Authorization") != "******" {
		t.Fatalf("expected Authorization user property on published request, got %v", pub)
	}
}

// TestCall_ClientMW_MalformedCredentialFormat_ReturnsSecurityCredentialError
// REPLACES the OLD TestCall_CredentialFunc_MalformedFormat_
// ReturnsSecurityCredentialError.
func TestCall_ClientMW_MalformedCredentialFormat_ReturnsSecurityCredentialError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	obs := &testObserver{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	handle := newSecuredRouteHandleWithClientImpl(func(context.Context, computeReq) (mwSecIn, error) {
		// Empty ****** -> fails the non-empty-string Codec.
		return mwSecIn{Authorization: "Bearer "}, nil
	})
	_, err := testCall(ctx, client, router, handle, computeReq{X: 3, Y: 4}, CallOptions{Observer: obs})
	var credErr reqreply.SecurityCredentialError
	if !errors.As(err, &credErr) {
		t.Fatalf("want reqreply.SecurityCredentialError, got %v", err)
	}
	if credErr.Scheme != "bearer" {
		t.Errorf("want Scheme=bearer, got %q", credErr.Scheme)
	}
	if len(client.published) != 0 {
		t.Error("want no request actually published when credential format is malformed")
	}
	if len(obs.secRejections) != 1 {
		t.Errorf("want 1 security rejection recorded, got %d", len(obs.secRejections))
	}
}

// NOTE: the OLD TestCall_CredentialFunc_ReturnsNilProperties_
// SkipsValidation had no direct replacement — its premise ("a
// credential-providing Fn that ran but returned nil properties skips
// validation entirely") doesn't generalize cleanly to ClientMW's
// multi-implementation MERGE model (mergeCredentialUserProperties gates
// validation on whether ANY attached implementation's Satisfies matched
// and ran — "ran", not "returned non-nil" — since with several
// attachable implementations, "the merged result happens to be empty"
// is no longer a reliable signal that no credential was intended). A
// route that genuinely needs no client-side credential handling for a
// given call should simply not attach a paired ClientMW for it, rather
// than attach one that intentionally returns nothing.

// ── ErrorPattern wiring (Phase 2) ─────────────────────────────────────────────

type serveConflictErr struct{ msg string }

func (e serveConflictErr) Error() string { return "conflict: " + e.msg }

type serveErrPayload struct {
	Code    string
	Message string
}

func (e serveErrPayload) Error() string { return "error " + e.Code }

var serveErrPayloadCodec = codex.Struct[serveErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e serveErrPayload) string { return e.Code },
		func(e *serveErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e serveErrPayload) string { return e.Message },
		func(e *serveErrPayload, v string) { e.Message = v },
	),
)

func TestServe_ErrorPatternMatch_HandlerError_PublishesTypedPayload(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	handle, err := route.Register(reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"}))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, handle,
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, serveConflictErr{msg: "duplicate"}
		},
		ServeOptions{})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-ep1"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if strings.Contains(string(pub.Payload), "conflict:") {
		t.Errorf("want typed JSON payload, got plain-text-looking payload: %s", pub.Payload)
	}
	if !strings.Contains(string(pub.Payload), `"code":"conflict"`) {
		t.Errorf("want typed payload with code=conflict, got: %s", pub.Payload)
	}
}

func TestServe_ErrorPatternNoMatch_HandlerError_FallsBackToPlainText(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	unrelatedErr := errors.New("unrelated failure")

	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	handle, err := route.Register(reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"}))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, handle,
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, unrelatedErr
		},
		ServeOptions{})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-ep2"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if string(pub.Payload) != unrelatedErr.Error() {
		t.Errorf("want plain-text fallback %q, got %q", unrelatedErr.Error(), pub.Payload)
	}
}

func TestServe_ErrorPatternMatch_EncodeError_PublishesTypedPayload(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveConflictErr, serveErrPayload](serveErrPayloadCodec,
			func(e serveConflictErr) (serveErrPayload, error) {
				return serveErrPayload{Code: "encode_failed", Message: e.msg}, nil
			},
		),
	)
	handle, err := route.Register(reqreply.NewServer(reqreply.Info{Title: "t", Version: "1.0.0"}))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Force an encode failure by overriding Encode after Register.
	handle.Encode = func(_ computeResp) ([]byte, error) {
		return nil, serveConflictErr{msg: "encode boom"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, handle,
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:   "compute/add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-ep3"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
	if !strings.Contains(string(pub.Payload), `"code":"encode_failed"`) {
		t.Errorf("want typed payload with code=encode_failed, got: %s", pub.Payload)
	}
}

func TestServe_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{Observer: obs})

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

func TestServe_ObserverRecordRequestFailure(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) { return computeResp{}, nil },
		ServeOptions{Observer: obs})

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

func TestServe_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{Observer: obs})

	router.dispatch("compute/add", &pahomqtt5.Publish{
		Topic:      "compute/add",
		Payload:    []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{ResponseTopic: "replies/r1"},
	})
	time.Sleep(80 * time.Millisecond)

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.serve" {
		t.Fatalf("expected StartSpan 'serve', got %v", obs.startSpanOps)
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

func TestCall_ValidRoundTrip(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Set up the responder.
	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	// Make a request.
	resp, err := testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{
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

func TestCall_Timeout(t *testing.T) {
	// Client that never triggers the reply.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{
			ReplyTopicPrefix: "replies",
			Timeout:          100 * time.Millisecond,
		})

	var reqErr CallError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	if reqErr.Kind != KindTimeout {
		t.Fatalf("expected KindTimeout, got %v", reqErr.Kind)
	}
}

func TestCall_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	_, _ = testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{Timeout: 2 * time.Second, Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestCall_ObserverRecordRequestTimeout(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, _ = testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{Timeout: 50 * time.Millisecond, Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 0 {
		t.Fatalf("expected RecordRequest(0) on timeout, got %v", obs.requests)
	}
}

func TestCall_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	_, _ = testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{Timeout: 2 * time.Second, Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.request" {
		t.Fatalf("expected StartSpan 'request', got %v", obs.startSpanOps)
	}
}

func TestCall_ContextCancelled(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{Timeout: 5 * time.Second})

	var reqErr CallError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected CallError on ctx cancel, got %T: %v", err, err)
	}
}

// ── Request with topic vars ───────────────────────────────────────────────────

func TestCall_WithVars_PublishesToResolvedTopic(t *testing.T) {
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

	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, _ := templateRoute.Register(b)

	// Request will timeout (no responder), but we only care that it published
	// to the resolved topic "compute/acme/add".
	_, _ = testCall(ctx, client, router, handle,
		computeReq{X: 3, Y: 4},
		CallOptions{
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

func TestCall_WithVars_MissingVar_ReturnsRequestError(t *testing.T) {
	templateRoute := reqreply.NewRoute[computeReq, computeResp](
		"compute/{tenantID}/add",
		computeReqCodec, computeRespCodec,
	)
	router := newMockRouter()
	client := &mockClient{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, _ := templateRoute.Register(b)

	_, err := testCall(ctx, client, router, handle,
		computeReq{X: 1, Y: 2},
		CallOptions{
			Vars:    map[string]string{}, // tenantID missing
			Timeout: time.Second,
		})

	var reqErr CallError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	var missing reqreply.MissingRouteParamError
	if !errors.As(reqErr, &missing) {
		t.Fatalf("expected MissingRouteParamError inside CallError, got %T", reqErr.Err)
	}
}

func TestCall_WithVars_MissingVar_ReportsRequiredConstraintWithVarName(t *testing.T) {
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

	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, _ := templateRoute.Register(b)

	_, _ = testCall(ctx, client, router, handle,
		computeReq{X: 1, Y: 2},
		CallOptions{
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
	b := UUIDReplyTopic("")
	topic, filter := b()
	if !strings.HasPrefix(topic, "replies/") {
		t.Errorf("expected topic to start with %q, got %q", "replies/", topic)
	}
	if !strings.HasPrefix(filter, "replies/") {
		t.Errorf("expected filter to start with %q, got %q", "replies/", filter)
	}
}

func TestUUIDReplyTopic_CustomPrefix(t *testing.T) {
	b := UUIDReplyTopic("myns")
	topic, filter := b()
	if !strings.HasPrefix(topic, "myns/") {
		t.Errorf("expected topic prefix %q, got %q", "myns/", topic)
	}
	if !strings.HasPrefix(filter, "myns/") {
		t.Errorf("expected filter prefix %q, got %q", "myns/", filter)
	}
}

func TestUUIDReplyTopic_EqualTopicAndFilter(t *testing.T) {
	b := UUIDReplyTopic("replies")
	topic, filter := b()
	if topic != filter {
		t.Errorf("expected responseTopic == subscribeFilter, got %q vs %q", topic, filter)
	}
}

func TestUUIDReplyTopic_UniquePerCall(t *testing.T) {
	b := UUIDReplyTopic("replies")
	t1, _ := b()
	t2, _ := b()
	if t1 == t2 {
		t.Errorf("expected unique topics per call, both returned %q", t1)
	}
}

func TestSharedReplyTopic_SubscribeFilterHasSharePrefix(t *testing.T) {
	b := SharedReplyTopic("replies", "pool")
	_, filter := b()
	if !strings.HasPrefix(filter, "$share/pool/replies/") {
		t.Errorf("expected filter to start with %q, got %q", "$share/pool/replies/", filter)
	}
}

func TestSharedReplyTopic_ResponseTopicHasNoSharePrefix(t *testing.T) {
	b := SharedReplyTopic("replies", "pool")
	topic, _ := b()
	if strings.HasPrefix(topic, "$share/") {
		t.Errorf("responseTopic must not carry $share prefix, got %q", topic)
	}
	if !strings.HasPrefix(topic, "replies/") {
		t.Errorf("expected responseTopic to start with %q, got %q", "replies/", topic)
	}
}

func TestSharedReplyTopic_UniquePerCall(t *testing.T) {
	b := SharedReplyTopic("replies", "pool")
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
	b := SharedReplyTopic("", "pool")
	topic, filter := b()
	if !strings.HasPrefix(topic, "replies/") {
		t.Errorf("expected responseTopic prefix %q, got %q", "replies/", topic)
	}
	if !strings.HasPrefix(filter, "$share/pool/replies/") {
		t.Errorf("expected filter prefix %q, got %q", "$share/pool/replies/", filter)
	}
}

// ── Request integration tests for ReplyTopicBuilder ───────────────────────────

func TestCall_ReplyTopicBuilder_UsesReturnedResponseTopic(t *testing.T) {
	// Verifies that the ResponseTopic property on the published request matches
	// the responseTopic returned by the builder.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	customTopic := "custom/replies/abc"
	_, _ = testCall(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		CallOptions{
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

func TestCall_ReplyTopicBuilder_UsesReturnedSubscribeFilter(t *testing.T) {
	// Verifies that client.Subscribe is called with the subscribeFilter returned
	// by the builder, not the responseTopic.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	responseTopic := "replies/abc"
	sharedFilter := "$share/mygroup/replies/abc"

	_, _ = testCall(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		CallOptions{
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

func TestCall_ReplyTopicBuilder_EmptyResponseTopic_ReturnsRequestError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx := context.Background()

	b := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	_, err := testCall(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		CallOptions{
			ReplyTopicBuilder: func() (string, string) { return "", "" },
		})

	var reqErr CallError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	if reqErr.Kind != KindEncode {
		t.Errorf("expected KindEncode, got %v", reqErr.Kind)
	}
}

func TestCall_ReplyTopicBuilder_EmptyFilter_FallsBackToResponseTopic(t *testing.T) {
	// When subscribeFilter is empty, Request must fall back to responseTopic.
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	responseTopic := "replies/fallback"
	_, _ = testCall(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		CallOptions{
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

func TestCall_NilBuilder_UsesReplyTopicPrefix(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	_, _ = testCall(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		CallOptions{
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

func TestCall_NilBuilder_DefaultPrefix(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	b := reqreply.NewServer(reqreply.Info{Title: "T", Version: "1"})
	handle, _ := computeRoute.Register(b)

	_, _ = testCall(ctx, client, router, handle, computeReq{X: 1, Y: 2},
		CallOptions{
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

// ── Phase 2: reqreply.NewTopicParam merge fields — Serve/CallHandle ───────────

type tenantReq struct {
	TenantID string
	X, Y     int
}
type tenantResp struct{ Sum int }

var tenantReqCodec = codex.Struct[tenantReq](
	codex.RequiredField("x", codex.Int(),
		func(r tenantReq) int { return r.X },
		func(r *tenantReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(),
		func(r tenantReq) int { return r.Y },
		func(r *tenantReq, v int) { r.Y = v }),
)

var tenantRespCodec = codex.Struct[tenantResp](
	codex.RequiredField("sum", codex.Int(),
		func(r tenantResp) int { return r.Sum },
		func(r *tenantResp, v int) { r.Sum = v }),
)

func newTenantRouteHandle() *reqreply.RouteHandle[tenantReq, tenantResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := reqreply.NewRoute[tenantReq, tenantResp]("compute/{tenantID}/add",
		tenantReqCodec, tenantRespCodec,
		reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(r tenantReq) string { return r.TenantID },
			func(r *tenantReq, v string) { r.TenantID = v }),
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// RR4: Serve auto-merges topic vars into the decoded req when the
// route declares merge fields.
func TestServe_MergeFields_AutoMergesTopicVars(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	handle := newTenantRouteHandle()

	var received tenantReq
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := testServe(ctx, client, router, handle,
		func(_ context.Context, req tenantReq) (tenantResp, error) {
			received = req
			return tenantResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})
	if err != nil {
		t.Fatalf("Serve setup failed: %v", err)
	}

	router.dispatch("compute/{tenantID}/add", &pahomqtt5.Publish{
		Topic:   "compute/acme/add",
		Payload: []byte(`{"x":3,"y":4}`),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-1"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if received.TenantID != "acme" {
		t.Errorf("TenantID: want merged from topic, got %q", received.TenantID)
	}
	if received.X != 3 || received.Y != 4 {
		t.Errorf("unexpected req: %+v", received)
	}
}

// RR5: CallHandle derives opts.Vars from req automatically — one
// struct in, no manual vars map needed.
func TestCallHandle_DerivesVarsFromReq(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	handle := newTenantRouteHandle()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _ = testCall(ctx, client, router, handle,
		tenantReq{TenantID: "acme", X: 1, Y: 2},
		CallOptions{Timeout: 100 * time.Millisecond})

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected a request to be published")
	}
	if pub.Topic != "compute/acme/add" {
		t.Errorf("Topic: want %q, got %q", "compute/acme/add", pub.Topic)
	}
}

// RR6: CallHandle explicit opts.Vars takes precedence over the
// derived value for the same key.
func TestCallHandle_ExplicitVarsOverridePrecedence(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	handle := newTenantRouteHandle()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _ = testCall(ctx, client, router, handle,
		tenantReq{TenantID: "acme", X: 1, Y: 2},
		CallOptions{
			Timeout: 100 * time.Millisecond,
			Vars:    map[string]string{"tenantID": "overridden"},
		})

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected a request to be published")
	}
	if pub.Topic != "compute/overridden/add" {
		t.Errorf("Topic: want explicit override %q, got %q", "compute/overridden/add", pub.Topic)
	}
}

// RR7: full Serve/CallHandle round trip with a NESTED Req struct — proves
// the Round 4 mandate holds for reqreply too, not just REST/events.
func TestServeCallHandle_NestedReq_RoundTrip(t *testing.T) {
	type meta struct {
		TenantID string
	}
	type nestedReq struct {
		Meta meta
		X, Y int
	}
	nestedReqCodec := codex.Struct[nestedReq](
		codex.RequiredField("x", codex.Int(),
			func(r nestedReq) int { return r.X },
			func(r *nestedReq, v int) { r.X = v }),
		codex.RequiredField("y", codex.Int(),
			func(r nestedReq) int { return r.Y },
			func(r *nestedReq, v int) { r.Y = v }),
	)

	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	handle, err := reqreply.NewRoute[nestedReq, tenantResp]("compute/{tenantID}/add",
		nestedReqCodec, tenantRespCodec,
		reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(r nestedReq) string { return r.Meta.TenantID },
			func(r *nestedReq, v string) { r.Meta.TenantID = v }),
	).Register(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	client := &mockClient{}
	router := newMockRouter()
	var received nestedReq
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := testServe(ctx, client, router, handle,
		func(_ context.Context, req nestedReq) (tenantResp, error) {
			received = req
			return tenantResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{}); err != nil {
		t.Fatalf("Serve setup failed: %v", err)
	}

	req := nestedReq{Meta: meta{TenantID: "acme"}, X: 5, Y: 6}
	go func() {
		_, _ = testCall(ctx, client, router, handle, req, CallOptions{Timeout: 500 * time.Millisecond})
	}()

	// Wait for the request to be published, then dispatch it to Serve's
	// registered handler (mockRouter keys on the TEMPLATE topic).
	deadline := time.Now().Add(time.Second)
	var reqPub *pahomqtt5.Publish
	for time.Now().Before(deadline) {
		if pub := client.lastPublished(); pub != nil && pub.Properties != nil && pub.Properties.ResponseTopic != "" {
			reqPub = pub
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if reqPub == nil {
		t.Fatal("expected CallHandle to publish a request")
	}
	router.dispatch("compute/{tenantID}/add", reqPub)
	time.Sleep(50 * time.Millisecond)

	if received.Meta.TenantID != "acme" {
		t.Errorf("Meta.TenantID: want merged from topic, got %q", received.Meta.TenantID)
	}
	if received.X != 5 || received.Y != 6 {
		t.Errorf("unexpected req: %+v", received)
	}
}

// ── CallOptions.RequestFormats / ResponseFormats per-call override ────────

// TestCall_RequestFormats_OverridesRouteDeclaredFormat verifies
// CallOptions.RequestFormats wins over the route's declared
// handle.RequestFormats (here: undeclared, JSON default) for THIS call only.
func TestCall_RequestFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	client := &mockClient{} // plain mock — no routing, just records publishes
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Request will timeout (no responder) — we only care about the
	// encoded payload bytes.
	_, _ = testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{
			Timeout:        100 * time.Millisecond,
			RequestFormats: []format.Format[computeReq]{format.YAML(computeReqCodec)},
		})

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected a publish, got none")
	}
	if !strings.Contains(string(pub.Payload), "x: 3") {
		t.Errorf("want YAML-encoded payload (override), got %q", pub.Payload)
	}
}

// TestCall_RequestFormats_RouteDeclaredStillAppliesWithoutOverride verifies
// the route-declared (here: JSON default) format still applies when no
// per-call override is given.
func TestCall_RequestFormats_RouteDeclaredStillAppliesWithoutOverride(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, _ = testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{Timeout: 100 * time.Millisecond})

	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected a publish, got none")
	}
	decoded, err := format.JSON(computeReqCodec).Unmarshal(pub.Payload)
	if err != nil {
		t.Fatalf("want JSON-encoded payload (route-declared default), got %q: %v", pub.Payload, err)
	}
	if decoded.X != 3 || decoded.Y != 4 {
		t.Errorf("unexpected decoded payload: %+v", decoded)
	}
}

// TestCall_ResponseFormats_OverridesRouteDeclaredFormat verifies
// CallOptions.ResponseFormats wins over the route's declared handle.Formats
// for THIS call only — the SERVER'S handle here declares YAML (so Serve
// replies with YAML bytes), while the CLIENT'S handle declares NOTHING
// (defaults to JSON); the override is what lets Call correctly decode the
// YAML reply anyway.
func TestCall_ResponseFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	serverHandle := newRouteHandle()
	serverHandle.WithFormats(format.YAML(computeRespCodec))
	_ = testServe(ctx, client, router, serverHandle,
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	resp, err := testCall(ctx, client, router, newRouteHandle(), // client handle: no Formats declared
		computeReq{X: 3, Y: 4},
		CallOptions{
			Timeout:         2 * time.Second,
			ResponseFormats: []format.Format[computeResp]{format.YAML(computeRespCodec)},
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Fatalf("want Sum=7 decoded via YAML override, got %d", resp.Sum)
	}
}

// TestCall_RequestFormats_TypeMismatch_ReturnsCallError verifies a
// wrong-typed CallOptions.RequestFormats returns CallError{Kind: KindEncode},
// errors.As-reachable.
func TestCall_RequestFormats_TypeMismatch_ReturnsCallError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{
			Timeout: time.Second,
			// Wrong type: []format.Format[computeResp] instead of []format.Format[computeReq].
			RequestFormats: []format.Format[computeResp]{format.JSON(computeRespCodec)},
		})
	var callErr CallError
	if !errors.As(err, &callErr) || callErr.Kind != KindEncode {
		t.Fatalf("want CallError{Kind: KindEncode}, got %v", err)
	}
}

// TestCall_ResponseFormats_TypeMismatch_ReturnsCallError mirrors
// TestCall_RequestFormats_TypeMismatch_ReturnsCallError for the response
// direction — requires a real round trip since the mismatch is only
// detected after a reply arrives.
func TestCall_ResponseFormats_TypeMismatch_ReturnsCallError(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	_, err := testCall(ctx, client, router, newRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{
			Timeout: 2 * time.Second,
			// Wrong type: []format.Format[computeReq] instead of []format.Format[computeResp].
			ResponseFormats: []format.Format[computeReq]{format.JSON(computeReqCodec)},
		})
	var callErr CallError
	if !errors.As(err, &callErr) || callErr.Kind != KindDecode {
		t.Fatalf("want CallError{Kind: KindDecode}, got %v", err)
	}
}

// ── Phase D0: codec-backed presence-only property spec enforcement ─────
//
// Regression test for docs/design/d-0006-protocol-native-capabilities.md's
// middleware-consolidation Phase D0 gap: a presence-only property
// declared via the codec-backed Middleware[In,Out] axis
// (WithRequestPropertySpec) must be ENFORCED at runtime (missing required
// property rejected), not merely rendered into the AsyncAPI schema — this
// is now the ONLY request-side header/property-param-as-middleware
// mechanism (the legacy FromUserPropertyParam bridge it originally backed
// up against was deleted once this coverage was proven equivalent).

var codecBackedPropertySpecRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/codec-backed-property-spec-add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "codecBackedPropertySpecCompute"},
).Use(reqreply.NewMiddleware[struct{}, struct{}](reqreply.Declaration[struct{}, struct{}]{Name: "declare-api-key-property"}).
	WithRequestPropertySpec(reqreply.PropertyParam{Param: codex.Param{Name: "X-API-Key"}, Required: true}))

func newCodecBackedPropertySpecRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := codecBackedPropertySpecRoute.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestServe_CodecBackedPropertySpec_MissingRequired_Rejects(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr ServeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = testServe(ctx, client, router, newCodecBackedPropertySpecRouteHandle(),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			t.Fatal("fn must not be called when a required codec-backed presence-only property is missing")
			return computeResp{}, nil
		},
		ServeOptions{OnError: func(e ServeError) { gotErr = e }})

	// No X-API-Key User Property attached — must be rejected, exactly
	// like the legacy FromUserPropertyParam-declared case above.
	router.dispatch("compute/codec-backed-property-spec-add", &pahomqtt5.Publish{
		Topic:   "compute/codec-backed-property-spec-add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-cbps-1"),
		},
	})
	time.Sleep(50 * time.Millisecond)

	if gotErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var missing MissingUserPropertyError
	if !errors.As(gotErr, &missing) {
		t.Fatalf("expected MissingUserPropertyError, got %v", gotErr.Err)
	}
	if missing.Name != "X-API-Key" {
		t.Fatalf("expected missing property Name=X-API-Key, got %q", missing.Name)
	}
}

func TestServe_CodecBackedPropertySpec_Present_Succeeds(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	called := false
	_ = testServe(ctx, client, router, newCodecBackedPropertySpecRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			called = true
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	router.dispatch("compute/codec-backed-property-spec-add", &pahomqtt5.Publish{
		Topic:   "compute/codec-backed-property-spec-add",
		Payload: []byte(validComputeJSON),
		Properties: &pahomqtt5.PublishProperties{
			ResponseTopic:   "replies/client-1",
			CorrelationData: []byte("corr-cbps-2"),
			User:            pahomqtt5.UserProperties{{Key: "X-API-Key", Value: "secret"}},
		},
	})
	time.Sleep(50 * time.Millisecond)

	if !called {
		t.Fatal("expected fn to be called when the required codec-backed presence-only property is present")
	}
	pub := client.lastPublished()
	if pub == nil {
		t.Fatal("expected reply to be published")
	}
}

// ── Phase D0: codec-backed presence-only property spec enforcement ─────
// (reply/response side) — mirrors the request-side
// codecBackedPropertySpecRoute block above; replaces the now-deleted
// FromResponseUserPropertyParam-declared responseHeaderParamComputeRoute
// (same scenario, ported onto the codec-backed WithResponsePropertySpec
// mechanism instead of the legacy middleware.Middleware bridge).

var codecBackedResponsePropertySpecRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/codec-backed-response-property-spec-add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "codecBackedResponsePropertySpecCompute"},
).Use(reqreply.NewMiddleware[struct{}, struct{}](reqreply.Declaration[struct{}, struct{}]{Name: "declare-trace-id-response-property"}).
	WithResponsePropertySpec(reqreply.PropertyParam{Param: codex.Param{Name: "X-Trace-Id"}, Required: true}))

func newCodecBackedResponsePropertySpecRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := codecBackedResponsePropertySpecRoute.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestCall_ClientMW_ResponsePropertySpec_MissingRequired_Rejects(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}

	// Plain Serve responder — attaches NO extra reply User Properties, so
	// the declared X-Trace-Id requirement is unmet.
	_ = testServe(context.Background(), client, router, newCodecBackedResponsePropertySpecRouteHandle(),
		func(_ context.Context, req computeReq) (computeResp, error) {
			return computeResp{Sum: req.X + req.Y}, nil
		},
		ServeOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := testCall(ctx, client, router, newCodecBackedResponsePropertySpecRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{Timeout: 2 * time.Second})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	if callErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", callErr.Kind)
	}
	var missing MissingUserPropertyError
	if !errors.As(err, &missing) {
		t.Fatalf("expected MissingUserPropertyError, got %v", callErr.Err)
	}
	if missing.Name != "X-Trace-Id" {
		t.Fatalf("expected missing property Name=X-Trace-Id, got %q", missing.Name)
	}
}

func TestCall_ClientMW_ResponsePropertySpec_Present_Succeeds(t *testing.T) {
	router := newMockRouter()
	client := &brokerClient{router: router}

	// Manually register a "server" handler that attaches the declared
	// X-Trace-Id User Property on its reply — the codec-backed mechanism
	// has no server-side declaration mechanism to generate reply-side
	// User Properties via Serve itself (only the request side does), so
	// this test constructs the reply directly, mirroring
	// TestServe_ValidRoundTrip's own raw-dispatch style.
	router.RegisterHandler("compute/codec-backed-response-property-spec-add", func(msg *pahomqtt5.Publish) {
		_, _ = client.Publish(context.Background(), &pahomqtt5.Publish{
			Topic:   msg.Properties.ResponseTopic,
			Payload: []byte(`{"sum":7}`),
			Properties: &pahomqtt5.PublishProperties{
				CorrelationData: msg.Properties.CorrelationData,
				User:            pahomqtt5.UserProperties{{Key: "X-Trace-Id", Value: "trace-123"}},
			},
		})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	resp, err := testCall(ctx, client, router, newCodecBackedResponsePropertySpecRouteHandle(),
		computeReq{X: 3, Y: 4},
		CallOptions{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Fatalf("expected Sum=7, got %d", resp.Sum)
	}
}
