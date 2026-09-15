package mqtt5

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/reqreply"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
	"github.com/DaniDeer/go-codex/middleware"
	"github.com/DaniDeer/go-codex/route"
	"github.com/DaniDeer/go-codex/validate"
	pahomqtt5 "github.com/eclipse/paho.golang/paho"
)

// ── shared types and codecs ───────────────────────────────────────────────────

type sensorReading struct {
	SensorID string
	Value    float64
}

var sensorCodec = codex.Struct[sensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		func(r sensorReading) string { return r.SensorID },
		func(r *sensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64(),
		func(r sensorReading) float64 { return r.Value },
		func(r *sensorReading, v float64) { r.Value = v },
	),
)

type computeReq struct{ X, Y int }
type computeResp struct{ Sum int }

var computeReqCodec = codex.Struct[computeReq](
	codex.RequiredField("x", codex.Int(),
		func(r computeReq) int { return r.X },
		func(r *computeReq, v int) { r.X = v },
	),
	codex.RequiredField("y", codex.Int(),
		func(r computeReq) int { return r.Y },
		func(r *computeReq, v int) { r.Y = v },
	),
)

var computeRespCodec = codex.Struct[computeResp](
	codex.RequiredField("sum", codex.Int(),
		func(r computeResp) int { return r.Sum },
		func(r *computeResp, v int) { r.Sum = v },
	),
)

var computeRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "compute"},
)

// ── mock MQTT client ──────────────────────────────────────────────────────────

type mockClient struct {
	mu             sync.Mutex
	published      []*pahomqtt5.Publish
	subscribed     []*pahomqtt5.Subscribe
	publishErr     error
	subscribeErr   error
	unsubscribeErr error
}

func (c *mockClient) Publish(_ context.Context, p *pahomqtt5.Publish) (*pahomqtt5.PublishResponse, error) {
	if c.publishErr != nil {
		return nil, c.publishErr
	}
	c.mu.Lock()
	c.published = append(c.published, p)
	c.mu.Unlock()
	return &pahomqtt5.PublishResponse{}, nil
}

func (c *mockClient) Subscribe(_ context.Context, s *pahomqtt5.Subscribe) (*pahomqtt5.Suback, error) {
	if c.subscribeErr != nil {
		return nil, c.subscribeErr
	}
	c.mu.Lock()
	c.subscribed = append(c.subscribed, s)
	c.mu.Unlock()
	return &pahomqtt5.Suback{}, nil
}

func (c *mockClient) subscribedFilters() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var filters []string
	for _, s := range c.subscribed {
		for _, sub := range s.Subscriptions {
			filters = append(filters, sub.Topic)
		}
	}
	return filters
}

func (c *mockClient) Unsubscribe(_ context.Context, _ *pahomqtt5.Unsubscribe) (*pahomqtt5.Unsuback, error) {
	return &pahomqtt5.Unsuback{}, c.unsubscribeErr
}

func (c *mockClient) lastPublished() *pahomqtt5.Publish {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.published) == 0 {
		return nil
	}
	return c.published[len(c.published)-1]
}

// ── mock router ───────────────────────────────────────────────────────────────

type mockRouter struct {
	mu       sync.Mutex
	handlers map[string]pahomqtt5.MessageHandler
}

func newMockRouter() *mockRouter {
	return &mockRouter{handlers: make(map[string]pahomqtt5.MessageHandler)}
}

func (r *mockRouter) RegisterHandler(topic string, h pahomqtt5.MessageHandler) {
	r.mu.Lock()
	r.handlers[topic] = h
	r.mu.Unlock()
}

func (r *mockRouter) UnregisterHandler(topic string) {
	r.mu.Lock()
	delete(r.handlers, topic)
	r.mu.Unlock()
}

// hasHandler reports whether a handler is registered under the exact topic key.
func (r *mockRouter) hasHandler(topic string) bool {
	r.mu.Lock()
	_, ok := r.handlers[topic]
	r.mu.Unlock()
	return ok
}

// waitHandler blocks until the handler for topic is registered or 1 second passes.
func (r *mockRouter) waitHandler(topic string) {
	for i := 0; i < 200; i++ {
		r.mu.Lock()
		_, ok := r.handlers[topic]
		r.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *mockRouter) dispatch(topic string, msg *pahomqtt5.Publish) {
	r.mu.Lock()
	h := r.handlers[topic]
	r.mu.Unlock()
	if h != nil {
		h(msg)
	}
}

// ── observer stub ─────────────────────────────────────────────────────────────

type validationEntry struct {
	location   string
	constraint string
	field      string
}

type testObserver struct {
	subscribes       []bool
	publishes        []bool
	requests         []int
	validationErrors []string
	validationFull   []validationEntry
	startSpanOps     []string
	endSpanErrs      []error
	secRejections    []string
}

func (o *testObserver) RecordValidationError(location, constraint, field string) {
	o.validationErrors = append(o.validationErrors, constraint)
	o.validationFull = append(o.validationFull, validationEntry{location, constraint, field})
}
func (o *testObserver) RecordRequest(_, _ string, code int, _ time.Duration) {
	o.requests = append(o.requests, code)
}
func (o *testObserver) RecordSubscribe(_ string, success bool, _ time.Duration) {
	o.subscribes = append(o.subscribes, success)
}
func (o *testObserver) RecordPublish(_ string, success bool, _ time.Duration) {
	o.publishes = append(o.publishes, success)
}
func (o *testObserver) RecordSecurityRejection(location, _ string) {
	o.secRejections = append(o.secRejections, location)
}
func (o *testObserver) StartSpan(ctx context.Context, op, _ string) context.Context {
	o.startSpanOps = append(o.startSpanOps, op)
	return ctx
}
func (o *testObserver) EndSpan(_ context.Context, err error) {
	o.endSpanErrs = append(o.endSpanErrs, err)
}

// ── channel / route handle helpers ───────────────────────────────────────────

func newChannelHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	h, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

// securedBearerScheme is a shared bearer scheme with a non-empty-string
// format Codec, used by both the Subscribe built-in check and the Publish
// CredentialFunc tests below.
var securedBearerScheme = events.SecurityScheme{SecurityScheme: route.BearerScheme("JWT")}.
	WithCodec(codex.String().Refine(validate.NonEmptyString))

// newSecuredSubscribeChannelHandle returns a channel handle whose Subscribe
// operation requires the "bearer" scheme declared above — used to test the
// built-in codec-based credential check + SecurityFunc ordering.
func newSecuredSubscribeChannelHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	// CheckCoverage (unconditional at Subscriber.Handle time) requires a
	// paired SubscribeMW implementation for every declared scheme. The
	// attached Fn is a no-op, always-granted implementation (correctly
	// shaped per mqtt5's own runSubscribeSecurityImpls) — it exists ONLY to
	// satisfy CheckCoverage's bookkeeping; the actual credential
	// enforcement this test exercises runs through the codec-based
	// built-in check / SubscribeOptions.SecurityFunc instead.
	mw := events.FromSecurityScheme("bearer", securedBearerScheme, nil)
	noopImpl := func(_ context.Context, _ *pahomqtt5.Publish, _ *sensorReading) (map[string][]string, error) {
		return map[string][]string{"bearer": {}}, nil
	}
	h, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test", Security: []route.SecurityRequirement{route.Require("bearer")}}).
		Use(mw).
		SubscribeMW(&mw, noopImpl).
		Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

func newRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := computeRoute.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// bearerAuthTestCodec validates a raw bearer token string's FORMAT
// (non-empty) — shared by every security declaration below.
var bearerAuthTestCodec = codex.String().Refine(validate.NonEmptyString)

// bearerAuthMw declares the "bearer" scheme via .Use() (Phase 1 of
// docs/design/d-0004-reqreply-workflow-simplification.md's Addendum) — REPLACES the OLD manual
// RouteMeta.Security + reqreply.WithSecurityScheme declaration pattern,
// which is incompatible with pairing a HandleMW/ClientMW implementation
// against it (checkImplementationsDeclared only recognizes schemes
// declared via .Use()).
var bearerAuthMw = middleware.SecurityScheme("bearer", route.BearerScheme("JWT"), nil, &bearerAuthTestCodec)

// acceptingSecurityImpl is a PAIRED server-side security Fn that always
// grants — used by tests that need [reqreply.CheckCoverage] to pass
// without exercising rejection behavior themselves (e.g. the built-in
// codec-format-check test, which rejects BEFORE this Fn is ever
// reached).
func acceptingSecurityImpl(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error) {
	return map[string][]string{"bearer": nil}, nil
}

// securedComputeRoute requires the "bearer" scheme declared via .Use() —
// used to test reqreply's built-in codec check, HandleMW, and ClientMW.
// A NEW variant is built per test via .HandleMW/.ClientMW (Route is
// immutable) where a test needs different Fn behavior.
var securedComputeRoute = reqreply.NewRoute[computeReq, computeResp](
	"compute/secured-add",
	computeReqCodec, computeRespCodec,
	reqreply.RouteMeta{OperationID: "securedCompute"},
).Use(bearerAuthMw)

func newSecuredRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	return newSecuredRouteHandleWithImpl(acceptingSecurityImpl)
}

func newSecuredRouteHandleWithImpl(fn func(context.Context, *pahomqtt5.Publish, []route.SecurityRequirement) (map[string][]string, error)) *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := securedComputeRoute.HandleMW(&bearerAuthMw, fn).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newSecuredRouteHandleNoImpl returns a handle for securedComputeRoute
// with NO HandleMW attached — used to test
// [reqreply.CheckCoverage]/[reqreply.MissingSecurityMiddlewareError].
func newSecuredRouteHandleNoImpl() *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewServer(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := securedComputeRoute.Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

const validSensorJSON = `{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`

// ── Subscribe tests ───────────────────────────────────────────────────────────

func TestSubscribe_ValidPayload(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.Value != 22.5 {
		t.Fatalf("unexpected value: %v", received.Value)
	}
}

func TestSubscribe_DecodeError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{"sensor_id":"not-uuid","value":1}`),
	})

	if gotErr.Kind != KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
}

func TestSubscribe_HandlerError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr SubscribeError
	handlerErr := errors.New("storage unavailable")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return handlerErr },
		SubscribeOptions{OnError: func(e SubscribeError) { gotErr = e }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if gotErr.Kind != KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
}

// TestSubscribe_HandlerError_MatchedErrorChannel_PublishesTypedPayload
// confirms subscribeWithHandle's message handler consults a declared
// events.ErrorChannel when fn returns a matching domain error, publishing
// the typed payload to the declared error-output topic and SKIPPING
// OnError — mirrors mqtt5PublishAdapter.handleUpstreamError's action
// dispatch, extended to the subscribe side (Decision 8).
func TestSubscribe_HandlerError_MatchedErrorChannel_PublishesTypedPayload(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.ErrorChannel[sensorValidationErr, sensorErrPayload](
			"sensors/readings/errors", sensorErrPayloadCodec,
			func(e sensorValidationErr) (sensorErrPayload, error) {
				return sensorErrPayload{Code: "out_of_range", Message: e.msg}, nil
			},
		),
	).WithSubscribe(events.Subscribe{Summary: "test"}).Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	onErrorCalled := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error {
			return sensorValidationErr{msg: "value too high"}
		},
		SubscribeOptions{OnError: func(SubscribeError) { onErrorCalled = true }})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if onErrorCalled {
		t.Error("OnError should NOT be called when an ErrorChannel matches")
	}
	found := false
	for _, p := range client.published {
		if p.Topic == "sensors/readings/errors" {
			found = true
			if !strings.Contains(string(p.Payload), "out_of_range") {
				t.Errorf("error payload = %s, want it to contain out_of_range", p.Payload)
			}
		}
	}
	if !found {
		t.Fatal("expected a message published to the declared error-output topic")
	}
}

func TestSubscribe_UserProperties_InContext(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotProps pahomqtt5.UserProperties

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(ctx context.Context, _ sensorReading) error {
			props, ok := UserPropertiesFromContext(ctx)
			if ok {
				gotProps = props
			}
			return nil
		},
		SubscribeOptions{})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "Authorization", Value: "Bearer token"}},
		},
	})

	if len(gotProps) != 1 || gotProps[0].Key != "Authorization" {
		t.Fatalf("expected Authorization property, got %v", gotProps)
	}
}

func TestSubscribe_BuiltInCredentialCheck_RejectsMalformedCredential(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	obs := &testObserver{}
	var gotErr SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newSecuredSubscribeChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		SubscribeOptions{
			Observer: obs,
			OnError:  func(e SubscribeError) { gotErr = e },
		})

	// No "Authorization" User Property at all -> extracted credential is
	// "" -> fails the non-empty-string Codec, BEFORE any custom SecurityFunc
	// would run (none is registered here).
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:      "sensors/readings",
		Payload:    []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{},
	})

	if gotErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var credErr events.SecurityCredentialError
	if !errors.As(gotErr, &credErr) {
		t.Fatalf("expected events.SecurityCredentialError, got %v", gotErr.Err)
	}
	if credErr.Scheme != "bearer" {
		t.Errorf("want Scheme=bearer, got %q", credErr.Scheme)
	}
	if len(obs.secRejections) != 1 {
		t.Errorf("want 1 security rejection recorded, got %d", len(obs.secRejections))
	}
}

func TestSubscribe_SecurityImpl_StillRunsAfterBuiltInCheck_OnValidCredential(t *testing.T) {
	// REPLACES the OLD TestSubscribe_SecurityFunc_StillRunsAfterBuiltInCheck_
	// OnValidCredential (SubscribeOptions.SecurityFunc was removed entirely,
	// Phase 2 of docs/design/d-0002-pubsub-workflow-simplification.md's Addendum) -- the SAME
	// scenario (custom enforcement runs AFTER the built-in codec-based
	// credential check passes) now runs through a real, non-decorative
	// SubscribeMW-paired implementation Fn instead of the old imperative
	// escape hatch.
	client := &mockClient{}
	router := newMockRouter()
	implCalled := false
	fnCalled := false

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	mw := events.FromSecurityScheme("bearer", securedBearerScheme, nil)
	impl := func(_ context.Context, _ *pahomqtt5.Publish, _ *sensorReading) (map[string][]string, error) {
		implCalled = true
		return map[string][]string{"bearer": {}}, nil
	}
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test", Security: []route.SecurityRequirement{route.Require("bearer")}}).
		Use(mw).
		SubscribeMW(&mw, impl).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { fnCalled = true; return nil },
		SubscribeOptions{})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			User: pahomqtt5.UserProperties{{Key: "Authorization", Value: "x"}},
		},
	})

	if !implCalled {
		t.Error("want the SubscribeMW-paired implementation called after the built-in check passes")
	}
	if !fnCalled {
		t.Error("want fn called after the implementation passes")
	}
}

func TestSubscribe_ContentTypeAutoFormat(t *testing.T) {
	// Encode with the default JSON format, then dispatch with ContentType set.
	// The adapter should select the matching format by ContentType.
	client := &mockClient{}
	router := newMockRouter()
	handle := newChannelHandle()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	jsonFmt := handle.Formats
	if len(jsonFmt) == 0 {
		// No explicit format means JSON is the fallback — test passes implicitly.
		// ContentType="" on the message → uses default decode path.
		return
	}

	_ = subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{})

	// Dispatch a message with ContentType header matching the format.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
		Properties: &pahomqtt5.PublishProperties{
			ContentType: "application/json",
		},
	})

	if received.Value != 22.5 {
		t.Fatalf("expected value 22.5, got %v", received.Value)
	}
}

func TestSubscribe_ObserverRecordSubscribeSuccess(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.subscribes) != 1 || !obs.subscribes[0] {
		t.Fatalf("expected successful RecordSubscribe, got %v", obs.subscribes)
	}
}

func TestSubscribe_ObserverRecordSubscribeFailure(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`bad json`),
	})

	if len(obs.subscribes) != 1 || obs.subscribes[0] {
		t.Fatalf("expected failed RecordSubscribe, got %v", obs.subscribes)
	}
}

func TestSubscribe_ValidationErrorReported(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(`{"sensor_id":"not-uuid","value":1}`),
	})

	if len(obs.validationErrors) == 0 {
		t.Fatal("expected at least one RecordValidationError call")
	}
}

// TestObserver_RecordValidationError_topicMismatch_subscribe is a genuine
// end-to-end test (not handler-simulated) — exercises TopicVarsFromMessage's
// real TopicMismatchError path through the full subscribe dispatch.
//
// mockRouter.dispatch does an EXACT-key lookup, unlike a real MQTT
// broker's own wildcard-based delivery (which routes ANY concrete topic
// matching the registered wildcard FILTER to the same handler, regardless
// of whether it also matches the go-codex TEMPLATE structurally — the two
// can diverge, e.g. a "#" multi-level filter accepting a different total
// segment count than the template expects). To faithfully simulate that
// real-broker behavior (impossible via dispatch's exact-string shortcut),
// this test grabs the registered [pahomqtt5.MessageHandler] directly from
// the router's own handler map and invokes it with an arbitrary message
// topic — exactly what a real broker's wildcard delivery would do.
func TestObserver_RecordValidationError_topicMismatch_subscribe(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newMergeChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obs})
	router.waitHandler("sensors/+/readings")

	router.mu.Lock()
	h := router.handlers["sensors/+/readings"]
	router.mu.Unlock()
	if h == nil {
		t.Fatal("expected a registered handler for sensors/+/readings")
	}
	// "sensors/readings" has 2 segments; the template
	// "sensors/{sensorID}/readings" requires 3 — a genuine structural
	// mismatch a real broker's wildcard delivery could still produce.
	h(&pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	if len(obs.validationFull) != 1 {
		t.Fatalf("want 1 RecordValidationError call, got %d", len(obs.validationFull))
	}
	ve := obs.validationFull[0]
	if ve.location != "topic" {
		t.Errorf("want location=%q, got %q", "topic", ve.location)
	}
	if ve.constraint != "topic-mismatch" {
		t.Errorf("want constraintName=%q, got %q", "topic-mismatch", ve.constraint)
	}
}

// newTopicConstraintMergeHandle returns a merge-capable channel handle
// (like newMergeChannelHandle) whose Client ALSO declares a builder-level
// topic codec via events.WithTopicConstraints — needed to exercise
// InvalidTopicError through the full subscribe dispatch (newMergeChannelHandle
// alone has no topic codec, only a per-var TopicParam codec).
func newTopicConstraintMergeHandle() *events.ChannelHandle[sensorReading] {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewClient(
		events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}),
		events.WithTopicConstraints(validate.MQTTPublishTopic),
	)
	h, err := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

// TestObserver_RecordValidationError_invalidTopic_subscribe is a genuine
// end-to-end test — dispatches a message on a concrete WILDCARD topic
// (structurally matches the "{sensorID}" template, since "+" normalizes
// like a template var, but fails the builder-level MQTTPublishTopic
// codec), exercising InvalidTopicError through the full subscribe
// dispatch. Mirrors adapters/mqtt/topicvars_test.go's
// TestTopicVarsFromMessage_TopicCodecFails_InvalidTopicError, but through
// the OBSERVER-integrated dispatch path rather than calling
// TopicVarsFromMessage directly.
func TestObserver_RecordValidationError_invalidTopic_subscribe(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newTopicConstraintMergeHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obs})

	router.dispatch("sensors/+/readings", &pahomqtt5.Publish{
		Topic: "sensors/+/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.validationFull) != 1 {
		t.Fatalf("want 1 RecordValidationError call, got %d", len(obs.validationFull))
	}
	ve := obs.validationFull[0]
	if ve.location != "topic" {
		t.Errorf("want location=%q, got %q", "topic", ve.location)
	}
}

func TestSubscribe_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obs})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.subscribe" {
		t.Fatalf("expected StartSpan 'subscribe', got %v", obs.startSpanOps)
	}
}

func TestSubscribe_MessageFromContext(t *testing.T) {
	// Verify the raw *paho.Publish is accessible via MessageFromContext inside fn.
	client := &mockClient{}
	router := newMockRouter()
	var gotTopic string

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(ctx context.Context, _ sensorReading) error {
			if msg, ok := MessageFromContext(ctx); ok {
				gotTopic = msg.Topic
			}
			return nil
		},
		SubscribeOptions{})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if gotTopic != "sensors/readings" {
		t.Fatalf("expected topic 'sensors/readings', got %q", gotTopic)
	}
}

// ── Publish tests ─────────────────────────────────────────────────────────────

func TestPublish_ValidMessage(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	err := publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil, true,
		PublishOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(client.published))
	}
}

func TestPublish_ContentTypeProperty(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	_ = publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil, true,
		PublishOptions[sensorReading]{ContentType: "application/json"})

	pub := client.lastPublished()
	if pub == nil || pub.Properties == nil || pub.Properties.ContentType != "application/json" {
		t.Fatalf("expected ContentType=application/json on published message, got %v", pub)
	}
}

func TestPublish_UserProperties(t *testing.T) {
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	_ = publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil, true,
		PublishOptions[sensorReading]{
			UserProperties: []UserProperty{{Key: "TenantID", Value: "acme"}},
		})

	pub := client.lastPublished()
	if pub == nil || pub.Properties == nil || len(pub.Properties.User) == 0 {
		t.Fatal("expected UserProperties on published message")
	}
	if pub.Properties.User[0].Key != "TenantID" {
		t.Fatalf("unexpected User property: %v", pub.Properties.User)
	}
}

func TestPublish_SecurityImpl_ValidFormat_Passes(t *testing.T) {
	// REPLACES the OLD TestPublish_CredentialFunc_ValidFormat_Passes
	// (PublishOptions.CredentialFunc was removed entirely, Phase 2 of
	// docs/design/d-0002-pubsub-workflow-simplification.md's Addendum) -- the SAME scenario now
	// runs through a real PublishMW-paired security implementation Fn.
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	mw := events.FromSecurityScheme("bearer", securedBearerScheme, nil)
	impl := func(context.Context, *sensorReading, []route.SecurityRequirement) ([]UserProperty, error) {
		return []UserProperty{{Key: "Authorization", Value: "x"}}, nil
	}
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []route.SecurityRequirement{route.Require("bearer")}}).
		Use(mw).
		PublishMW(&mw, impl).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pubErr := publish(context.Background(), client, handle, 1, false, reading, nil, true,
		PublishOptions[sensorReading]{})
	if pubErr != nil {
		t.Fatalf("unexpected error: %v", pubErr)
	}
	pub := client.lastPublished()
	if pub == nil || pub.Properties == nil {
		t.Fatal("expected a published message with properties")
	}
	if got := pub.Properties.User.Get("Authorization"); got != "x" {
		t.Errorf("want Authorization=%q, got %q", "x", got)
	}
}

func TestPublish_SecurityImpl_MalformedFormat_ReturnsSecurityCredentialError(t *testing.T) {
	// REPLACES the OLD TestPublish_CredentialFunc_MalformedFormat_
	// ReturnsSecurityCredentialError (PublishOptions.CredentialFunc was
	// removed entirely, Phase 2 of
	// docs/design/d-0002-pubsub-workflow-simplification.md's Addendum).
	client := &mockClient{}
	obs := &testObserver{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	mw := events.FromSecurityScheme("bearer", securedBearerScheme, nil)
	impl := func(context.Context, *sensorReading, []route.SecurityRequirement) ([]UserProperty, error) {
		// Empty credential value -> fails the non-empty-string Codec.
		return []UserProperty{{Key: "Authorization", Value: ""}}, nil
	}
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []route.SecurityRequirement{route.Require("bearer")}}).
		Use(mw).
		PublishMW(&mw, impl).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pubErr := publish(context.Background(), client, handle, 1, false, reading, nil, true,
		PublishOptions[sensorReading]{Observer: obs})
	var credErr events.SecurityCredentialError
	if !errors.As(pubErr, &credErr) {
		t.Fatalf("want events.SecurityCredentialError, got %v", pubErr)
	}
	if credErr.Scheme != "bearer" {
		t.Errorf("want Scheme=bearer, got %q", credErr.Scheme)
	}
	if len(client.published) != 0 {
		t.Error("want no message actually published when credential format is malformed")
	}
	if len(obs.secRejections) != 1 {
		t.Errorf("want 1 security rejection recorded, got %d", len(obs.secRejections))
	}
}

func TestPublish_SecurityImpl_ReturnsNilProperties_SkipsValidation(t *testing.T) {
	// REPLACES the OLD TestPublish_CredentialFunc_ReturnsNilProperties_
	// SkipsValidation (PublishOptions.CredentialFunc was removed entirely,
	// Phase 2 of docs/design/d-0002-pubsub-workflow-simplification.md's Addendum).
	//
	// A security implementation deliberately returning (nil, nil) for "no
	// credential needed" must NOT be treated as a malformed-empty-credential
	// error -- the Round-93 regression class, mirrored here from day one.
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	mw := events.FromSecurityScheme("bearer", securedBearerScheme, nil)
	impl := func(context.Context, *sensorReading, []route.SecurityRequirement) ([]UserProperty, error) {
		return nil, nil
	}
	handle, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "test", Security: []route.SecurityRequirement{route.Require("bearer")}}).
		Use(mw).
		PublishMW(&mw, impl).
		Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pubErr := publish(context.Background(), client, handle, 1, false, reading, nil, true,
		PublishOptions[sensorReading]{})
	if pubErr != nil {
		t.Fatalf("unexpected error: %v", pubErr)
	}
	if len(client.published) != 1 {
		t.Fatalf("expected 1 published message, got %d", len(client.published))
	}
}

func TestPublish_EncodeError(t *testing.T) {
	client := &mockClient{}
	// Use empty UUID (invalid) to trigger codec validation error
	invalid := sensorReading{SensorID: "not-a-uuid", Value: 1.0}

	err := publish(context.Background(), client, newChannelHandle(), 1, false, invalid, nil, true,
		PublishOptions[sensorReading]{})

	var encErr PublishEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected PublishEncodeError, got %T: %v", err, err)
	}
}

func TestPublish_ObserverRecordPublishSuccess(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	_ = publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil, true,
		PublishOptions[sensorReading]{Observer: obs})

	if len(obs.publishes) != 1 || !obs.publishes[0] {
		t.Fatalf("expected successful RecordPublish, got %v", obs.publishes)
	}
}

func TestPublish_TraceSpan(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	_ = publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil, true,
		PublishOptions[sensorReading]{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "mqtt5.publish" {
		t.Fatalf("expected StartSpan 'publish', got %v", obs.startSpanOps)
	}
}

// ── error LogValue + Unwrap tests ─────────────────────────────────────────────

func TestSubscribeError_LogValue(t *testing.T) {
	e := SubscribeError{Kind: KindDecode, Topic: "sensors/t", Err: errors.New("fail")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestPublishEncodeError_LogValue(t *testing.T) {
	e := PublishEncodeError{Topic: "t", Err: errors.New("fail")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestCallError_LogValue(t *testing.T) {
	e := CallError{Kind: KindTimeout, Err: errors.New("timeout")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestServeError_LogValue(t *testing.T) {
	e := ServeError{Kind: KindHandler, Err: errors.New("fail")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestSubscribeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := SubscribeError{Kind: KindDecode, Topic: "t", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

func TestCallError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := CallError{Kind: KindTimeout, Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

func TestServeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := ServeError{Kind: KindHandler, Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap")
	}
}

// ── UserPropertyParam tests ───────────────────────────────────────────────────

func mqttMsgWithUserProps(payload string, userProps map[string]string) *pahomqtt5.Publish {
	props := &pahomqtt5.PublishProperties{}
	for k, v := range userProps {
		props.User = append(props.User, pahomqtt5.UserProperty{Key: k, Value: v})
	}
	return &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(payload), Properties: props}
}

func TestUserPropertyParam_ValidProperties_MessageDelivered(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{
			UserPropertyParams: []UserPropertyParam{
				UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	router.dispatch("sensors/readings",
		mqttMsgWithUserProps(validSensorJSON, map[string]string{"TenantID": "acme"}))

	if received.Value != 22.5 {
		t.Fatalf("expected message delivered, got value %v", received.Value)
	}
}

func TestUserPropertyParam_MissingRequired_SecurityError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		SubscribeOptions{
			OnError: func(e SubscribeError) { gotErr = e },
			UserPropertyParams: []UserPropertyParam{
				{Name: "Authorization", Required: true},
			},
		})

	// Dispatch a message with NO User Properties.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if gotErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var missing MissingUserPropertyError
	if !errors.As(gotErr, &missing) {
		t.Fatalf("expected MissingUserPropertyError via errors.As, got %T", gotErr.Err)
	}
	if missing.Name != "Authorization" {
		t.Fatalf("expected Name=Authorization, got %q", missing.Name)
	}
}

func TestUserPropertyParam_CodecFailure_SecurityError(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var gotErr SubscribeError

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { t.Fatal("fn must not be called"); return nil },
		SubscribeOptions{
			OnError: func(e SubscribeError) { gotErr = e },
			UserPropertyParams: []UserPropertyParam{
				UserPropertyParam{Name: "TenantID", Required: true}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	// Dispatch with empty TenantID → codec failure.
	router.dispatch("sensors/readings",
		mqttMsgWithUserProps(validSensorJSON, map[string]string{"TenantID": ""}))

	if gotErr.Kind != KindSecurity {
		t.Fatalf("expected KindSecurity, got %v", gotErr.Kind)
	}
	var propErr UserPropertyError
	if !errors.As(gotErr, &propErr) {
		t.Fatalf("expected UserPropertyError via errors.As, got %T", gotErr.Err)
	}
	if propErr.Name != "TenantID" {
		t.Fatalf("expected Name=TenantID, got %q", propErr.Name)
	}
}

func TestUserPropertyParam_Optional_AbsentOk(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{
			UserPropertyParams: []UserPropertyParam{
				UserPropertyParam{Name: "OptionalHeader", Required: false}.WithCodec(
					codex.String().Refine(validate.NonEmptyString),
				),
			},
		})

	// Dispatch without OptionalHeader — should succeed.
	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if received.Value != 22.5 {
		t.Fatalf("expected message delivered when optional property absent, got %v", received.Value)
	}
}

func TestUserPropertyParam_ObserverValidationErrorReported(t *testing.T) {
	obs := &testObserver{}
	client := &mockClient{}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_ = subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{
			Observer: obs,
			UserPropertyParams: []UserPropertyParam{
				{Name: "Authorization", Required: true},
			},
		})

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic: "sensors/readings", Payload: []byte(validSensorJSON),
	})

	if len(obs.validationErrors) == 0 {
		t.Fatal("expected RecordValidationError called for missing required property")
	}
}

func TestUserPropertyError_LogValue(t *testing.T) {
	e := UserPropertyError{Name: "TenantID", Value: "", Err: errors.New("must not be empty")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestMissingUserPropertyError_LogValue(t *testing.T) {
	e := MissingUserPropertyError{Name: "Authorization"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestUserPropertyError_ErrorsAs(t *testing.T) {
	inner := errors.New("constraint failed")
	outer := UserPropertyError{Name: "TenantID", Value: "bad", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner error")
	}
}

func TestUserPropertyParam_WithCodec_ReturnsCopy(t *testing.T) {
	p := UserPropertyParam{Name: "TenantID", Required: true}
	codec := codex.String().Refine(validate.NonEmptyString)
	p2 := p.WithCodec(codec)
	if p.Codec != nil {
		t.Fatal("WithCodec must not mutate the original")
	}
	if p2.Codec == nil {
		t.Fatal("WithCodec must set Codec on the copy")
	}
}

// ── BrokerError tests ─────────────────────────────────────────────────────────

func TestBrokerError_LogValue(t *testing.T) {
	inner := errors.New("connection refused")
	e := BrokerError{Op: "subscribe", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestBrokerError_ErrorsAs(t *testing.T) {
	inner := errors.New("network error")
	outer := BrokerError{Op: "publish", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner error")
	}
}

func TestBrokerError_ErrorString(t *testing.T) {
	e := BrokerError{Op: "subscribe", Err: errors.New("timeout")}
	if e.Error() != "mqtt5 broker subscribe: timeout" {
		t.Fatalf("unexpected Error() string: %q", e.Error())
	}
}

func TestSubscribe_BrokerError_OnSubscribeFail(t *testing.T) {
	brokerErr := errors.New("broker unavailable")
	client := &mockClient{subscribeErr: brokerErr}
	router := newMockRouter()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{})

	var be BrokerError
	if !errors.As(err, &be) {
		t.Fatalf("expected BrokerError, got %T: %v", err, err)
	}
	if be.Op != "subscribe" {
		t.Fatalf("expected Op=subscribe, got %q", be.Op)
	}
	if !errors.Is(be, brokerErr) {
		t.Fatal("errors.Is must find brokerErr via Unwrap")
	}
}

func TestPublish_BrokerError_OnPublishFail(t *testing.T) {
	brokerErr := errors.New("broker unavailable")
	client := &mockClient{publishErr: brokerErr}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	err := publish(context.Background(), client, newChannelHandle(), 1, false, reading, nil, true,
		PublishOptions[sensorReading]{})

	var be BrokerError
	if !errors.As(err, &be) {
		t.Fatalf("expected BrokerError, got %T: %v", err, err)
	}
	if be.Op != "publish" {
		t.Fatalf("expected Op=publish, got %q", be.Op)
	}
}

// ── Publish topic vars observer reporting ─────────────────────────────────────

func newTemplateChannelHandle() *events.ChannelHandle[sensorReading] {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	h, err := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.TopicParam{Name: "sensorID"}.WithCodec(uuidCodec),
	).WithPublish(events.Publish{Summary: "test"}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

func TestPublish_Vars_MissingVar_ReportsRequiredConstraintWithVarName(t *testing.T) {
	// Verifies that a missing topic variable is reported as constraint "required"
	// with the variable name as the field — matching the mqtt adapter's behaviour.
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	err := publish(context.Background(), client, newTemplateChannelHandle(), 1, false,
		reading, map[string]string{}, true, // sensorID missing
		PublishOptions[sensorReading]{Observer: obs})

	var missingErr events.MissingTopicVarError
	if !errors.As(err, &missingErr) {
		t.Fatalf("expected MissingTopicVarError, got %T: %v", err, err)
	}

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
	if got.field != "sensorID" {
		t.Errorf("expected field=%q, got %q", "sensorID", got.field)
	}
}

func TestPublish_Vars_CodecFailure_ReportsVarNameAsField(t *testing.T) {
	// Verifies that a topic variable that fails its codec is reported with the
	// variable name as the field — matching the mqtt adapter's behaviour.
	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}

	err := publish(context.Background(), client, newTemplateChannelHandle(), 1, false,
		reading, map[string]string{"sensorID": "not-a-uuid"}, true,
		PublishOptions[sensorReading]{Observer: obs})

	var paramErr events.TopicParamError
	if !errors.As(err, &paramErr) {
		t.Fatalf("expected TopicParamError, got %T: %v", err, err)
	}

	if len(obs.validationFull) == 0 {
		t.Fatal("expected RecordValidationError to be called for failing topic variable")
	}
	got := obs.validationFull[0]
	if got.location != "topic_var" {
		t.Errorf("expected location=%q, got %q", "topic_var", got.location)
	}
	if got.field != "sensorID" {
		t.Errorf("expected field=%q (variable name), got %q", "sensorID", got.field)
	}
}

// ── Phase 2: events.NewTopicParam merge fields — Subscribe/PublishHandle ──────

// newMergeChannelHandle returns a channel whose sensorID topic var is
// merge-capable (events.NewTopicParam), unlike newTemplateChannelHandle's
// validate-only events.TopicParam.
func newMergeChannelHandle() *events.ChannelHandle[sensorReading] {
	uuidCodec := codex.String().Refine(validate.UUID)
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	h, err := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings",
		sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

// EV5: Subscribe auto-merges topic vars into the payload when the
// channel declares merge fields — no manual TopicVarsFromMessage call
// needed in the handler function.
func TestSubscribe_MergeFields_AutoMergesTopicVars(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	handle := newMergeChannelHandle()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	// Payload JSON deliberately carries a DIFFERENT sensor_id — merge must
	// OVERWRITE it with the value extracted from the concrete topic.
	// mockRouter.RegisterHandler now keys on the DERIVED WILDCARD FILTER
	// ("sensors/+/readings", from [deriveWildcardFilter]) — the bug fix
	// this pass applied to [SubscribeWithHandle] — not the raw
	// "{sensorID}" template; the dispatched message's OWN .Topic field
	// still carries the CONCRETE topic used for var extraction.
	router.dispatch("sensors/+/readings", &pahomqtt5.Publish{
		Topic:   "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings",
		Payload: []byte(`{"sensor_id":"00000000-0000-0000-0000-000000000000","value":22.5}`),
	})

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("SensorID: want merged from topic, got %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", received.Value)
	}
}

// EV5b: Subscribe route WITHOUT merge fields behaves identically to
// today — regression guard (mirrors REST's P6).
func TestSubscribe_NoMergeFieldsIsUnaffected(t *testing.T) {
	client := &mockClient{}
	router := newMockRouter()
	var received sensorReading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := subscribeWithHandle(ctx, client, router, newChannelHandle(), 1,
		func(_ context.Context, r sensorReading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	router.dispatch("sensors/readings", &pahomqtt5.Publish{
		Topic:   "sensors/readings",
		Payload: []byte(validSensorJSON),
	})

	if received.Value != 22.5 {
		t.Fatalf("unexpected value: %v", received.Value)
	}
}

// EV6: PublishHandle derives topic vars from msg automatically — one
// struct in, no manual vars map needed.
func TestPublishHandle_DerivesVarsFromMsg(t *testing.T) {
	client := &mockClient{}
	handle := newMergeChannelHandle()
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}

	err := publishHandle(context.Background(), client, handle, 1, false, reading, PublishOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message, got %d", len(client.published))
	}
	wantTopic := "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"
	if client.published[0].Topic != wantTopic {
		t.Errorf("Topic: want %q, got %q", wantTopic, client.published[0].Topic)
	}
}

// EV7: full publish->subscribe round trip with a NESTED payload struct and
// a non-JSON (Gob, via format.NewTyped projection) format — proves the
// Round 4 mandate holds for events too, not just REST.
func TestPublishHandleSubscribe_NestedGobPayload_RoundTrip(t *testing.T) {
	type meta struct {
		SensorID string
	}
	type reading struct {
		Meta  meta
		Value float64
	}

	readingCodec := codex.Struct[reading]()
	gobFmt := format.NewTyped[reading](
		readingCodec,
		func(r reading) ([]byte, error) {
			var buf bytes.Buffer
			err := gob.NewEncoder(&buf).Encode(r.Value)
			return buf.Bytes(), err
		},
		func(data []byte) (reading, error) {
			var v float64
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
				return reading{}, err
			}
			return reading{Value: v}, nil
		},
		"application/gob",
	)

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := events.NewChannel[reading]("sensors/{sensorID}/readings", readingCodec,
		events.NewTopicParam("sensorID", codex.String().Refine(validate.UUID),
			func(r reading) string { return r.Meta.SensorID },
			func(r *reading, v string) { r.Meta.SensorID = v }),
	).WithSubscribe(events.Subscribe{}).Handle(b)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	handle.WithFormats(gobFmt)

	client := &mockClient{}
	router := newMockRouter()
	var received reading

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, r reading) error { received = r; return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}

	msg := reading{Meta: meta{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479"}, Value: 42.5}
	if err := publishHandle(ctx, client, handle, 1, false, msg, PublishOptions[reading]{}); err != nil {
		t.Fatalf("PublishHandle: %v", err)
	}
	if len(client.published) != 1 {
		t.Fatalf("want 1 published message, got %d", len(client.published))
	}
	// mockRouter.RegisterHandler now keys on the DERIVED WILDCARD FILTER
	// ("sensors/+/readings"), not the raw "{sensorID}" template; the
	// published message's own .Topic field already carries the CONCRETE,
	// resolved topic (used for var extraction on receive).
	router.dispatch("sensors/+/readings", client.published[0])

	if received.Meta.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("Meta.SensorID: want merged from topic, got %q", received.Meta.SensorID)
	}
	if received.Value != 42.5 {
		t.Errorf("Value: want 42.5 (from Gob body), got %v", received.Value)
	}
}

// ── Phase 9: events property vocabulary axis + Bug 1/Bug 2 side-track ───────

// mqttMdIn/mqttMdOut are minimal In/Out shapes (NO required fields, so
// mw.InCodec/OutCodec.Validate never fails on a zero value) — used across
// the property axis/precedence/Observer tests below.
type mqttMdIn struct{ TenantID string }

var mqttMdInCodec = codex.Struct[mqttMdIn]()

type mqttMdOut struct{ TenantID string }

var mqttMdOutCodec = codex.Struct[mqttMdOut]()

func newMqttMdDeclaration(name string) middleware.Declaration[mqttMdIn, mqttMdOut] {
	return middleware.NewDeclaration(name, mqttMdInCodec, mqttMdOutCodec)
}

// D1: codec-backed middleware dispatch (Transform) runs AFTER the paired
// security Fn, both pre-handler — confirms mqtt5's dispatch order matches
// REST's/reqreply's established order.
func TestSubscribe_MiddlewareDispatch_RunsAfterPairedSecurity(t *testing.T) {
	var order []string
	emw := events.NewMiddleware(newMqttMdDeclaration("order-policy"))

	sub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "test"}).
		SubscribeMW(nil, func(_ context.Context, _ *pahomqtt5.Publish, _ *sensorReading) (map[string][]string, error) {
			order = append(order, "security")
			return nil, nil
		})
	sub = events.Transform(sub, emw, func(ctx context.Context, msg *sensorReading, in mqttMdIn) error {
		order = append(order, "middleware")
		return nil
	})

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := sub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	client := &mockClient{}
	router := newMockRouter()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := subscribeWithHandle(ctx, client, router, handle, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}
	router.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	if len(order) != 2 || order[0] != "security" || order[1] != "middleware" {
		t.Errorf("want dispatch order [security, middleware], got %v", order)
	}
}

// Bug 1 fix verification: with ALL THREE tiers present on the SAME var
// name (explicit PublishOptions.Vars, a Middleware's WithPublishTopic, AND
// the channel's own NewTopicParam-derived value), the final resolved
// value follows explicit > middleware-derived > channel-own-derived — the
// CORRECTED order (previously backwards: channel-own beat middleware).
func TestPublish_MiddlewareDispatch_ValuePrecedence_ExplicitBeatsMiddlewareBeatsChannelOwn(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	emw := events.NewMiddleware(newMqttMdDeclaration("sensor-override-policy")).
		WithPublishTopic(events.NewTopicParam("sensorID", uuidCodec,
			func(o mqttMdOut) string { return o.TenantID },
			func(o *mqttMdOut, v string) { o.TenantID = v }))

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	pub := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, emw, func(ctx context.Context, msg sensorReading) (mqttMdOut, error) {
		return mqttMdOut{TenantID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}, nil
	})
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Value: 1}
	ctx := context.Background()

	// (a) explicit ALWAYS wins over middleware-derived.
	client := &mockClient{}
	if err := publish(ctx, client, handle, 1, false, reading,
		map[string]string{"sensorID": "cccccccc-cccc-cccc-cccc-cccccccccccc"}, true, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("publish (explicit): %v", err)
	}
	if !strings.Contains(client.lastPublished().Topic, "cccccccc-cccc-cccc-cccc-cccccccccccc") {
		t.Errorf("want explicit value to win, got topic %q", client.lastPublished().Topic)
	}

	// (b) middleware-derived wins over channel-own-derived (the Bug 1 fix).
	client2 := &mockClient{}
	if err := publishHandle(ctx, client2, handle, 1, false, reading, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("publishHandle: %v", err)
	}
	if !strings.Contains(client2.lastPublished().Topic, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb") {
		t.Errorf("want middleware-derived value to beat channel-own, got topic %q", client2.lastPublished().Topic)
	}
}

// Bug 2 fix verification: a DecodeIn failure calls
// stats.ReportErrors(obs, "middleware:in", err), and the fn's own business
// error calls stats.ReportErrors(obs, "middleware:fn", err) — NOT the flat
// mechanism's existing "topic_var" string.
func TestSubscribe_Observer_ReportsMiddlewareInAndFnLocations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// "middleware:in": a REQUIRED WithSubscribeProperty absent from the
	// message's real User Properties fails at DecodeIn time.
	inMW := events.NewMiddleware(newMqttMdDeclaration("tenant-required-policy")).
		WithSubscribeProperty(events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(in mqttMdIn) string { return in.TenantID },
			func(in *mqttMdIn, v string) { in.TenantID = v }))
	subIn := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).WithSubscribe(events.Subscribe{Summary: "test"})
	subIn = events.Transform(subIn, inMW, func(ctx context.Context, msg *sensorReading, in mqttMdIn) error { return nil })
	bIn := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handleIn, err := subIn.Handle(bIn)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obsIn := &testObserver{}
	clientIn := &mockClient{}
	routerIn := newMockRouter()
	if err := subscribeWithHandle(ctx, clientIn, routerIn, handleIn, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obsIn}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}
	routerIn.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	foundIn := false
	for _, e := range obsIn.validationFull {
		if e.location == "middleware:in" {
			foundIn = true
		}
	}
	if !foundIn {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:in", obsIn.validationFull)
	}

	// "middleware:fn": the fn's own business error, shaped as
	// codex.ValidationErrors so it flows through stats.ReportErrors'
	// walkErrors and is observable via RecordValidationError.
	fnMW := events.NewMiddleware(newMqttMdDeclaration("fn-error-policy"))
	subFn := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).WithSubscribe(events.Subscribe{Summary: "test"})
	subFn = events.Transform(subFn, fnMW, func(ctx context.Context, msg *sensorReading, in mqttMdIn) error {
		return codex.ValidationErrors{{Field: "tenantID", Err: errors.New("boom")}}
	})
	bFn := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handleFn, err := subFn.Handle(bFn)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obsFn := &testObserver{}
	clientFn := &mockClient{}
	routerFn := newMockRouter()
	if err := subscribeWithHandle(ctx, clientFn, routerFn, handleFn, 1,
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions{Observer: obsFn}); err != nil {
		t.Fatalf("Subscribe setup failed: %v", err)
	}
	routerFn.dispatch("sensors/readings", &pahomqtt5.Publish{Topic: "sensors/readings", Payload: []byte(validSensorJSON)})

	foundFn := false
	for _, e := range obsFn.validationFull {
		if e.location == "middleware:fn" {
			foundFn = true
		}
	}
	if !foundFn {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obsFn.validationFull)
	}
}

// Bug 2 fix verification, publish side. Publish-side dispatch only ever
// returns a fn business error (no separate DecodeIn step exists on this
// side) — mirrors REST's client dispatch, which ALSO only ever reports
// "middleware:fn" on its own encode side; a faithfully-mirrored asymmetry
// (see docs/design/d-0003-codec-declared-middlewares.md's Addendum's "Observer
// integration"), not a gap.
func TestPublish_Observer_ReportsMiddlewareInAndFnLocations(t *testing.T) {
	mw := events.NewMiddleware(newMqttMdDeclaration("fn-error-policy"))
	pub := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg sensorReading) (mqttMdOut, error) {
		return mqttMdOut{}, codex.ValidationErrors{{Field: "tenantID", Err: errors.New("boom")}}
	})
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	obs := &testObserver{}
	client := &mockClient{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}
	_ = publishHandle(context.Background(), client, handle, 1, false, reading, PublishOptions[sensorReading]{Observer: obs})

	found := false
	for _, e := range obs.validationFull {
		if e.location == "middleware:fn" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a RecordValidationError call with location %q, got %v", "middleware:fn", obs.validationFull)
	}
}

// Write-side wiring Case 2: a Middleware's WithPublishProperty-declared
// value appears in the outgoing message's real MQTT5 User Properties,
// confirmed SEPARATE from a WithPublishTopic-declared value on the SAME
// message (no cross-contamination between the two var kinds after
// dispatchPublishMiddlewareHandlers' 2-map split).
func TestPublish_WithPublishProperty_WritesOutgoingUserProperty_SeparateFromTopicVars(t *testing.T) {
	uuidCodec := codex.String().Refine(validate.UUID)
	mw := events.NewMiddleware(newMqttMdDeclaration("tenant-policy")).
		WithPublishProperty(events.NewPropertyParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(o mqttMdOut) string { return o.TenantID },
			func(o *mqttMdOut, v string) { o.TenantID = v }))

	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	pub := events.NewChannel[sensorReading](
		"sensors/{sensorID}/readings", sensorCodec,
		events.NewTopicParam("sensorID", uuidCodec,
			func(r sensorReading) string { return r.SensorID },
			func(r *sensorReading, v string) { r.SensorID = v }),
	).WithPublish(events.Publish{})
	pub = events.ClientTransform(pub, mw, func(ctx context.Context, msg sensorReading) (mqttMdOut, error) {
		return mqttMdOut{TenantID: "acme"}, nil
	})
	handle, err := pub.Handle(b)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1}
	client := &mockClient{}
	if err := publishHandle(context.Background(), client, handle, 1, false, reading, PublishOptions[sensorReading]{}); err != nil {
		t.Fatalf("publishHandle: %v", err)
	}

	published := client.lastPublished()
	if !strings.Contains(published.Topic, "f47ac10b-58cc-4372-a567-0e02b2c3d479") {
		t.Errorf("want topic to carry the topic var, got %q", published.Topic)
	}
	var gotTenant string
	var userPropCount int
	if published.Properties != nil {
		userPropCount = len(published.Properties.User)
		for _, p := range published.Properties.User {
			if p.Key == "tenantID" {
				gotTenant = p.Value
			}
		}
	}
	if gotTenant != "acme" {
		t.Errorf("want tenantID User Property %q, got %q (all: %d props)", "acme", gotTenant, userPropCount)
	}
}
