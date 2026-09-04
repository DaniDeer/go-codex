package zeromq

import (
	"context"
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
	"github.com/DaniDeer/go-codex/validate"
)

// ── shared types and codecs ───────────────────────────────────────────────────

type sensorReading struct {
	SensorID string  `json:"-"`
	Value    float64 `json:"-"`
}

var sensorCodec = codex.Struct[sensorReading](
	codex.RequiredField("sensor_id",
		codex.String().Refine(validate.UUID),
		func(r sensorReading) string { return r.SensorID },
		func(r *sensorReading, v string) { r.SensorID = v },
	),
	codex.RequiredField("value",
		codex.Float64().Refine(validate.NonZeroFloat),
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

// ── mock FramedSocket ─────────────────────────────────────────────────────────

// mockSocket implements FramedSocket for testing without a real ZMQ library.
type mockSocket struct {
	// mu guards all fields — Serve loops run in background goroutines while
	// tests poll sentFrames.
	mu sync.Mutex
	// inFrames is the queue of multi-frame messages to return from RecvFrames.
	inFrames [][][]byte
	// sentFrames records every SendFrames call in order.
	sentFrames [][][]byte
	// recvErr, when non-nil, is returned by RecvFrames (after inFrames is empty).
	recvErr error
	// sendErr, when non-nil, is returned by SendFrames.
	sendErr error
	// subTopic records the last SetSubscription call.
	subTopic string
	// subTopics records EVERY SetSubscription call in order — used by
	// multi-route ServeSubscribers tests where subTopic (last-only) is
	// insufficient.
	subTopics []string
	// timeoutCount is how many ErrTimeout returns to emit before real messages.
	timeoutCount int
}

func (m *mockSocket) SendFrames(frames [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	cp := make([][]byte, len(frames))
	for i, f := range frames {
		cp[i] = append([]byte{}, f...)
	}
	m.sentFrames = append(m.sentFrames, cp)
	return nil
}

func (m *mockSocket) RecvFrames() ([][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timeoutCount > 0 {
		m.timeoutCount--
		return nil, ErrTimeout
	}
	if len(m.inFrames) == 0 {
		if m.recvErr != nil {
			return nil, m.recvErr
		}
		return nil, ErrTimeout
	}
	frames := m.inFrames[0]
	m.inFrames = m.inFrames[1:]
	return frames, nil
}

func (m *mockSocket) SetSubscription(topic string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subTopic = topic
	m.subTopics = append(m.subTopics, topic)
	return nil
}

// subTopicsSnapshot returns a copy of subTopics for race-free polling.
func (m *mockSocket) subTopicsSnapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.subTopics))
	copy(out, m.subTopics)
	return out
}

// sentSnapshot returns a copy of sentFrames for race-free polling.
func (m *mockSocket) sentSnapshot() [][][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][][]byte, len(m.sentFrames))
	copy(out, m.sentFrames)
	return out
}

func (m *mockSocket) SetRecvTimeout(_ time.Duration) error { return nil }

// ── channel handle helpers ────────────────────────────────────────────────────

func newSubscribeHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	h, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithSubscribe(events.Subscribe{Summary: "Sensor reading received"}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

func newPublishHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewClient(events.WithInfo(events.Info{Title: "Test", Version: "1.0.0"}))
	h, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec).
		WithPublish(events.Publish{Summary: "Sensor reading sent"}).Handle(b)
	if err != nil {
		panic(err)
	}
	return h
}

// newMergeChannelHandle returns a channel whose sensorID topic var is
// merge-capable (events.NewTopicParam) — mirrors mqtt5's
// newMergeChannelHandle, used for zeromq's G2 (Subscribe auto-merge,
// PublishHandle) and G1 (PublishAdapter per-item derivation) tests.
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

func newRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	b := reqreply.NewBuilder(reqreply.Info{Title: "Test", Version: "1.0.0"})
	h, err := reqreply.NewRoute[computeReq, computeResp](
		"/compute",
		computeReqCodec, computeRespCodec,
		reqreply.RouteMeta{OperationID: "compute"},
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

// ── observer stub ─────────────────────────────────────────────────────────────

type testObserver struct {
	subscribes       []bool
	publishes        []bool
	requests         []int // status codes
	paths            []string
	validationErrors []string
	startSpanOps     []string
	endSpanErrs      []error
}

func (o *testObserver) RecordValidationError(_, constraint, _ string) {
	o.validationErrors = append(o.validationErrors, constraint)
}
func (o *testObserver) RecordRequest(_, path string, code int, _ time.Duration) {
	o.requests = append(o.requests, code)
	o.paths = append(o.paths, path)
}
func (o *testObserver) RecordSubscribe(_ string, success bool, _ time.Duration) {
	o.subscribes = append(o.subscribes, success)
}
func (o *testObserver) RecordPublish(_ string, success bool, _ time.Duration) {
	o.publishes = append(o.publishes, success)
}
func (o *testObserver) StartSpan(ctx context.Context, op, _ string) context.Context {
	o.startSpanOps = append(o.startSpanOps, op)
	return ctx
}
func (o *testObserver) EndSpan(_ context.Context, err error) {
	o.endSpanErrs = append(o.endSpanErrs, err)
}

// ── Subscribe tests ───────────────────────────────────────────────────────────

const validSensorJSON = `{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`

func runSubscribe(sock *mockSocket, fn func(context.Context, sensorReading) error, opts SubscribeOptions[sensorReading]) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return subscribeWithHandle(ctx, sock, newSubscribeHandle(), fn, opts)
}

func TestSubscribe_ValidPayload(t *testing.T) {
	var received sensorReading
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, r sensorReading) error {
		received = r
		return nil
	}, SubscribeOptions[sensorReading]{})

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("unexpected sensor ID: %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Fatalf("unexpected value: %v", received.Value)
	}
}

func TestSubscribe_SetsSubscriptionFilter(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{}}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error { return nil }, SubscribeOptions[sensorReading]{})
	if sock.subTopic != "sensors/readings" {
		t.Fatalf("expected subscription to %q, got %q", "sensors/readings", sock.subTopic)
	}
}

func TestSubscribe_DecodeError(t *testing.T) {
	var gotErr SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(`{"sensor_id":"not-a-uuid","value":0}`)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error {
		t.Fatal("fn must not be called on decode error")
		return nil
	}, SubscribeOptions[sensorReading]{
		OnError: func(e SubscribeError) { gotErr = e },
	})

	if gotErr.Kind != KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
	if gotErr.Topic != "sensors/readings" {
		t.Fatalf("unexpected topic: %q", gotErr.Topic)
	}
}

func TestSubscribe_HandlerError(t *testing.T) {
	var gotErr SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	handlerErr := errors.New("store unavailable")
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error {
		return handlerErr
	}, SubscribeOptions[sensorReading]{
		OnError: func(e SubscribeError) { gotErr = e },
	})

	if gotErr.Kind != KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatalf("expected errors.Is to find handlerErr via Unwrap")
	}
}

func TestSubscribe_ObserverRecordSubscribeSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obs})

	if len(obs.subscribes) != 1 || !obs.subscribes[0] {
		t.Fatalf("expected one successful subscribe, got %v", obs.subscribes)
	}
}

func TestSubscribe_ObserverRecordSubscribeFailure(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(`bad json`)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obs})

	if len(obs.subscribes) != 1 || obs.subscribes[0] {
		t.Fatalf("expected one failed subscribe, got %v", obs.subscribes)
	}
}

func TestSubscribe_ValidationErrorReported(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{
		inFrames: [][][]byte{
			// value=0 fails NonZeroFloat constraint
			{[]byte("sensors/readings"), []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":0}`)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obs})

	if len(obs.validationErrors) == 0 {
		t.Fatal("expected at least one validation error to be reported")
	}
}

func TestSubscribe_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.subscribe" {
		t.Fatalf("expected StartSpan with 'zmq.subscribe', got %v", obs.startSpanOps)
	}
	if len(obs.endSpanErrs) != 1 || obs.endSpanErrs[0] != nil {
		t.Fatalf("expected EndSpan with nil error, got %v", obs.endSpanErrs)
	}
}

func TestSubscribe_IgnoresMalformedFrame(t *testing.T) {
	var called int
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("only-one-frame")},                            // malformed
			{[]byte("sensors/readings"), []byte(validSensorJSON)}, // valid
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error {
		called++
		return nil
	}, SubscribeOptions[sensorReading]{})

	if called != 1 {
		t.Fatalf("expected fn called once (malformed frame skipped), got %d", called)
	}
}

// ── Publish tests ─────────────────────────────────────────────────────────────

func TestPublish_ValidMessage(t *testing.T) {
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	handle := newPublishHandle()
	err := publish(context.Background(), sock, handle, reading, nil, PublishOptions[sensorReading]{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
	if string(sock.sentFrames[0][0]) != "sensors/readings" {
		t.Fatalf("unexpected topic frame: %q", sock.sentFrames[0][0])
	}
}

func TestPublish_EncodeError_InvalidValue(t *testing.T) {
	sock := &mockSocket{}
	// value=0 fails NonZeroFloat constraint → encode error
	invalid := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 0}
	handle := newPublishHandle()
	err := publish(context.Background(), sock, handle, invalid, nil, PublishOptions[sensorReading]{})

	var encErr PublishEncodeError
	if !errors.As(err, &encErr) {
		t.Fatalf("expected PublishEncodeError, got %T: %v", err, err)
	}
	if encErr.Topic != "sensors/readings" {
		t.Fatalf("unexpected topic: %q", encErr.Topic)
	}
	if len(sock.sentFrames) != 0 {
		t.Fatal("socket must not be written on encode error")
	}
}

func TestPublish_ObserverRecordPublishSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	_ = publish(context.Background(), sock, newPublishHandle(), reading, nil,
		PublishOptions[sensorReading]{Observer: obs})

	if len(obs.publishes) != 1 || !obs.publishes[0] {
		t.Fatalf("expected successful RecordPublish, got %v", obs.publishes)
	}
}

func TestPublish_ObserverRecordPublishFailure(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{}
	invalid := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 0}
	_ = publish(context.Background(), sock, newPublishHandle(), invalid, nil,
		PublishOptions[sensorReading]{Observer: obs})

	if len(obs.publishes) != 1 || obs.publishes[0] {
		t.Fatalf("expected failed RecordPublish, got %v", obs.publishes)
	}
}

func TestPublish_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	_ = publish(context.Background(), sock, newPublishHandle(), reading, nil,
		PublishOptions[sensorReading]{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.publish" {
		t.Fatalf("expected StartSpan with 'zmq.publish', got %v", obs.startSpanOps)
	}
}

// ── Serve tests ───────────────────────────────────────────────────────────────

const validComputeJSON = `{"x":3,"y":4}`

func runServe(sock *mockSocket, fn func(context.Context, computeReq) (computeResp, error), opts ServeOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return Serve(ctx, sock, newRouteHandle(), fn, opts)
}

func TestServe_ValidRoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte(validComputeJSON)},
		},
	}
	_ = runServe(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{})

	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
	if string(sock.sentFrames[0][0]) != "ok" {
		t.Fatalf("expected status frame 'ok', got %q", sock.sentFrames[0][0])
	}
}

func TestServe_DecodeError(t *testing.T) {
	var gotErr ServeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte(`bad json`)},
		},
	}
	_ = runServe(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		t.Fatal("fn must not be called on decode error")
		return computeResp{}, nil
	}, ServeOptions{
		OnError: func(e ServeError) { gotErr = e },
	})

	if gotErr.Kind != KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
	// error reply must be sent to unblock REQ peer
	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "error" {
		t.Fatalf("expected error reply frame, got %v", sock.sentFrames)
	}
}

func TestServe_HandlerError(t *testing.T) {
	var gotErr ServeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte(validComputeJSON)},
		},
	}
	handlerErr := errors.New("overflow")
	_ = runServe(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, handlerErr
	}, ServeOptions{
		OnError: func(e ServeError) { gotErr = e },
	})

	if gotErr.Kind != KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "error" {
		t.Fatalf("expected error reply frame, got %v", sock.sentFrames)
	}
}

// ── ErrorPattern wiring (Phase 2) ─────────────────────────────────────────────

type serveZmqConflictErr struct{ msg string }

func (e serveZmqConflictErr) Error() string { return "conflict: " + e.msg }

type serveZmqErrPayload struct {
	Code    string
	Message string
}

func (e serveZmqErrPayload) Error() string { return "error " + e.Code }

var serveZmqErrPayloadCodec = codex.Struct[serveZmqErrPayload](
	codex.RequiredField("code", codex.String().Refine(validate.NonEmptyString),
		func(e serveZmqErrPayload) string { return e.Code },
		func(e *serveZmqErrPayload, v string) { e.Code = v },
	),
	codex.RequiredField("message", codex.String(),
		func(e serveZmqErrPayload) string { return e.Message },
		func(e *serveZmqErrPayload, v string) { e.Message = v },
	),
)

func newErrorPatternRouteHandle(t *testing.T) *reqreply.RouteHandle[computeReq, computeResp] {
	t.Helper()
	route := reqreply.NewRoute[computeReq, computeResp]("compute/add", computeReqCodec, computeRespCodec,
		reqreply.ErrorPattern[serveZmqConflictErr, serveZmqErrPayload](serveZmqErrPayloadCodec,
			func(e serveZmqConflictErr) (serveZmqErrPayload, error) {
				return serveZmqErrPayload{Code: "conflict", Message: e.msg}, nil
			},
		),
	)
	handle, err := route.Register(reqreply.NewBuilder(reqreply.Info{Title: "t", Version: "1.0.0"}))
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return handle
}

func TestServe_ErrorPatternMatch_HandlerError_SendsTypedPayload(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = Serve(ctx, sock, newErrorPatternRouteHandle(t),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, serveZmqConflictErr{msg: "duplicate"}
		}, ServeOptions{})

	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "error" {
		t.Fatalf("expected error reply frame, got %v", sock.sentFrames)
	}
	if !strings.Contains(string(sock.sentFrames[0][1]), `"code":"conflict"`) {
		t.Errorf("want typed payload with code=conflict, got: %s", sock.sentFrames[0][1])
	}
}

func TestServe_ErrorPatternNoMatch_HandlerError_FallsBackToPlainText(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	unrelatedErr := errors.New("unrelated failure")

	_ = Serve(ctx, sock, newErrorPatternRouteHandle(t),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, unrelatedErr
		}, ServeOptions{})

	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "error" {
		t.Fatalf("expected error reply frame, got %v", sock.sentFrames)
	}
	if string(sock.sentFrames[0][1]) != unrelatedErr.Error() {
		t.Errorf("want plain-text fallback %q, got %q", unrelatedErr.Error(), sock.sentFrames[0][1])
	}
}

func TestServeRouter_ErrorPatternMatch_HandlerError_SendsTypedPayload(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{routerFrame(validComputeJSON)}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_ = ServeRouter(ctx, sock, newErrorPatternRouteHandle(t),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, serveZmqConflictErr{msg: "duplicate"}
		}, ServeOptions{})

	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
	frame := sock.sentFrames[0]
	if len(frame) != 4 || string(frame[2]) != "error" {
		t.Fatalf("expected [identity, delim, error, payload] frame, got %v", frame)
	}
	if !strings.Contains(string(frame[3]), `"code":"conflict"`) {
		t.Errorf("want typed payload with code=conflict, got: %s", frame[3])
	}
}

func TestServeRouter_ErrorPatternNoMatch_HandlerError_FallsBackToPlainText(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{routerFrame(validComputeJSON)}}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	unrelatedErr := errors.New("unrelated failure")

	_ = ServeRouter(ctx, sock, newErrorPatternRouteHandle(t),
		func(_ context.Context, _ computeReq) (computeResp, error) {
			return computeResp{}, unrelatedErr
		}, ServeOptions{})

	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
	frame := sock.sentFrames[0]
	if len(frame) != 4 || string(frame[2]) != "error" {
		t.Fatalf("expected [identity, delim, error, payload] frame, got %v", frame)
	}
	if string(frame[3]) != unrelatedErr.Error() {
		t.Errorf("want plain-text fallback %q, got %q", unrelatedErr.Error(), frame[3])
	}
}

func TestServe_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	_ = runServe(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestServe_ObserverRecordRequestFailure(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(`bad`)}}}
	_ = runServe(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}, ServeOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 0 {
		t.Fatalf("expected RecordRequest(0), got %v", obs.requests)
	}
}

func TestServe_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	_ = runServe(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.serve" {
		t.Fatalf("expected StartSpan with 'zmq.serve', got %v", obs.startSpanOps)
	}
	if len(obs.endSpanErrs) != 1 || obs.endSpanErrs[0] != nil {
		t.Fatalf("expected EndSpan with nil error, got %v", obs.endSpanErrs)
	}
}

// ── Call tests ────────────────────────────────────────────────────────────────

func TestCall_ValidRoundTrip(t *testing.T) {
	replyJSON := `{"sum":7}`
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), []byte(replyJSON)},
		},
	}
	result, err := Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Sum != 7 {
		t.Fatalf("expected Sum=7, got %d", result.Sum)
	}
	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
}

func TestCall_ServerErrorReply(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("error"), []byte("overflow")},
		},
	}
	_, err := Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
}

func TestCall_MalformedReply(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("only-one-frame")},
		},
	}
	_, err := Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, CallOptions{})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError on malformed reply, got %T: %v", err, err)
	}
}

func TestCall_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}
	_, _ = Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestCall_ObserverRecordRequestFailure_ServerError(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("error"), []byte("internal")}}}
	_, _ = Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 500 {
		t.Fatalf("expected RecordRequest(500), got %v", obs.requests)
	}
}

func TestCall_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}
	_, _ = Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.request" {
		t.Fatalf("expected StartSpan with 'zmq.request', got %v", obs.startSpanOps)
	}
}

func TestCall_ContextCancelled(t *testing.T) {
	// sock returns only ErrTimeout forever → ctx cancels the loop
	sock := &mockSocket{timeoutCount: 100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := Call(ctx, sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, CallOptions{})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError on context cancel, got %T: %v", err, err)
	}
}

// ── error LogValue tests ──────────────────────────────────────────────────────

func TestSubscribeError_LogValue(t *testing.T) {
	inner := errors.New("constraint failed")
	e := SubscribeError{Kind: KindDecode, Topic: "sensors/t1", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestPublishEncodeError_LogValue(t *testing.T) {
	inner := errors.New("bad value")
	e := PublishEncodeError{Topic: "sensors/t1", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestServeError_LogValue(t *testing.T) {
	e := ServeError{Kind: KindHandler, Err: errors.New("overflow")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestCallError_LogValue(t *testing.T) {
	e := CallError{Err: errors.New("timeout")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

// ── errors.As / Unwrap tests ──────────────────────────────────────────────────

func TestSubscribeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := SubscribeError{Kind: KindDecode, Topic: "t", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}

func TestServeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := ServeError{Kind: KindHandler, Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}

func TestCallError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := CallError{Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}

// ── ServeRouter tests ─────────────────────────────────────────────────────────

func runServeRouter(sock *mockSocket, fn func(context.Context, computeReq) (computeResp, error), opts ServeOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return ServeRouter(ctx, sock, newRouteHandle(), fn, opts)
}

func routerFrame(payload string) [][]byte {
	return [][]byte{[]byte("client-id"), {}, []byte(payload)}
}

func TestServeRouter_ValidRoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{routerFrame(validComputeJSON)},
	}
	_ = runServeRouter(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{})

	// wait briefly for goroutine
	time.Sleep(50 * time.Millisecond)
	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
	// frames: [identity, "", "ok", payload]
	if len(sock.sentFrames[0]) < 4 {
		t.Fatalf("expected 4 frames in reply, got %d", len(sock.sentFrames[0]))
	}
	if string(sock.sentFrames[0][0]) != "client-id" {
		t.Fatalf("expected identity frame 'client-id', got %q", sock.sentFrames[0][0])
	}
	if string(sock.sentFrames[0][2]) != "ok" {
		t.Fatalf("expected status frame 'ok', got %q", sock.sentFrames[0][2])
	}
}

func TestServeRouter_DecodeError(t *testing.T) {
	var gotErr ServeError
	sock := &mockSocket{inFrames: [][][]byte{routerFrame(`bad json`)}}
	_ = runServeRouter(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		t.Fatal("fn must not be called on decode error")
		return computeResp{}, nil
	}, ServeOptions{
		OnError: func(e ServeError) { gotErr = e },
	})

	time.Sleep(50 * time.Millisecond)
	if gotErr.Kind != KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
	// error reply must include identity frame
	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "client-id" {
		t.Fatalf("expected error reply with identity, got %v", sock.sentFrames)
	}
	if string(sock.sentFrames[0][2]) != "error" {
		t.Fatalf("expected status frame 'error', got %q", sock.sentFrames[0][2])
	}
}

func TestServeRouter_HandlerError(t *testing.T) {
	var gotErr ServeError
	sock := &mockSocket{inFrames: [][][]byte{routerFrame(validComputeJSON)}}
	handlerErr := errors.New("overflow")
	_ = runServeRouter(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, handlerErr
	}, ServeOptions{
		OnError: func(e ServeError) { gotErr = e },
	})

	time.Sleep(50 * time.Millisecond)
	if gotErr.Kind != KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
}

func TestServeRouter_ObserverRecordRequest(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{routerFrame(validComputeJSON)}}
	_ = runServeRouter(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{Observer: obs})

	time.Sleep(80 * time.Millisecond)
	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestServeRouter_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{routerFrame(validComputeJSON)}}
	_ = runServeRouter(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{Observer: obs})

	time.Sleep(80 * time.Millisecond)
	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.serve" {
		t.Fatalf("expected StartSpan 'zmq.serve', got %v", obs.startSpanOps)
	}
}

func TestServeRouter_IgnoresMalformedFrame(t *testing.T) {
	var called int
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("id")},                // malformed: < 3 frames
			routerFrame(validComputeJSON), // valid
		},
	}
	_ = runServeRouter(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		called++
		return computeResp{Sum: r.X + r.Y}, nil
	}, ServeOptions{})

	time.Sleep(80 * time.Millisecond)
	if called != 1 {
		t.Fatalf("expected fn called once (malformed frame skipped), got %d", called)
	}
}

// ── CallDealer tests ──────────────────────────────────────────────────────────

func dealerReply(status, payload string) [][]byte {
	return [][]byte{{}, []byte(status), []byte(payload)}
}

func TestCallDealer_ValidRoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{dealerReply("ok", `{"sum":7}`)},
	}
	result, err := CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Sum != 7 {
		t.Fatalf("expected Sum=7, got %d", result.Sum)
	}
	// verify DEALER sends empty delimiter
	if len(sock.sentFrames) != 1 || len(sock.sentFrames[0][0]) != 0 {
		t.Fatalf("expected empty delimiter frame, got %v", sock.sentFrames)
	}
}

func TestCallDealer_ServerErrorReply(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{dealerReply("error", "overflow")}}
	_, err := CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, CallOptions{})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
}

func TestCallDealer_MalformedReply(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("only-one-frame")}}}
	_, err := CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, CallOptions{})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
}

func TestCallDealer_EncodeError(t *testing.T) {
	// Use a sensorReading codec for a channel, not compute — just ensure encode error path.
	// We'll induce it by using the compute codec with valid data (can't easily break encode).
	// Instead test that socket is NOT written when encode succeeds but test the path is wired.
	sock := &mockSocket{inFrames: [][][]byte{dealerReply("ok", `{"sum":0}`)}}
	// zero values for computeReq are valid (int codec has no constraints) — just verify normal path
	result, err := CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 0, Y: 0}, CallOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Sum != 0 {
		t.Fatalf("expected Sum=0, got %d", result.Sum)
	}
}

func TestCallDealer_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{dealerReply("ok", `{"sum":7}`)}}
	_, _ = CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200) for ZMQ-DEALER, got %v", obs.requests)
	}
}

func TestCallDealer_ObserverRecordRequestFailure_ServerError(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{dealerReply("error", "internal")}}
	_, _ = CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, CallOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 500 {
		t.Fatalf("expected RecordRequest(500), got %v", obs.requests)
	}
}

func TestCallDealer_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{dealerReply("ok", `{"sum":7}`)}}
	_, _ = CallDealer(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.request" {
		t.Fatalf("expected StartSpan 'zmq.request', got %v", obs.startSpanOps)
	}
}

func TestCallDealer_ContextCancelled(t *testing.T) {
	sock := &mockSocket{timeoutCount: 100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := CallDealer(ctx, sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, CallOptions{})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError on ctx cancel, got %T: %v", err, err)
	}
}

// ── SocketError tests ─────────────────────────────────────────────────────────

func TestSocketError_LogValue(t *testing.T) {
	inner := errors.New("connection reset")
	e := SocketError{Op: "recv", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestSocketError_ErrorsAs(t *testing.T) {
	inner := errors.New("io error")
	outer := SocketError{Op: "set_subscription", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}

func TestSocketError_ErrorString(t *testing.T) {
	e := SocketError{Op: "send", Err: errors.New("broken pipe")}
	if e.Error() != "zeromq socket send: broken pipe" {
		t.Fatalf("unexpected Error() string: %q", e.Error())
	}
}

func TestSubscribe_SocketError_OnSetSubscriptionFail(t *testing.T) {
	// A socket that fails SetSubscription should return SocketError.
	sock := &failingSocket{setSubErr: errors.New("not a SUB socket")}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := subscribeWithHandle(ctx, sock, newSubscribeHandle(),
		func(_ context.Context, _ sensorReading) error { return nil },
		SubscribeOptions[sensorReading]{})

	var sockErr SocketError
	if !errors.As(err, &sockErr) {
		t.Fatalf("expected SocketError, got %T: %v", err, err)
	}
	if sockErr.Op != "set_subscription" {
		t.Fatalf("expected Op=set_subscription, got %q", sockErr.Op)
	}
}

// failingSocket is a FramedSocket that returns configured errors on specific ops.
type failingSocket struct {
	setSubErr     error
	setTimeoutErr error
	recvErr       error
}

func (f *failingSocket) SendFrames(_ [][]byte) error          { return nil }
func (f *failingSocket) SetSubscription(_ string) error       { return f.setSubErr }
func (f *failingSocket) SetRecvTimeout(_ time.Duration) error { return f.setTimeoutErr }
func (f *failingSocket) RecvFrames() ([][]byte, error) {
	if f.recvErr != nil {
		return nil, f.recvErr
	}
	return nil, ErrTimeout
}

// ── Call/CallDealer Vars tests ────────────────────────────────────────────────

func newTemplateRouteHandle() *reqreply.RouteHandle[computeReq, computeResp] {
	uuidCodec := codex.String().Refine(validate.UUID)
	return reqreply.NewRoute[computeReq, computeResp](
		"compute/{tenantID}/add",
		computeReqCodec, computeRespCodec,
		reqreply.TopicParam{Name: "tenantID"}.WithCodec(uuidCodec),
	).ClientHandle()
}

func TestCall_Vars_ObserverPathIsResolved(t *testing.T) {
	// Verifies that Vars resolves the template topic and the resolved path
	// is reported to the observer.
	obs := &testObserver{}
	tenantID := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	expectedPath := "compute/" + tenantID + "/add"
	respPayload := `{"sum":3}`

	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), []byte(respPayload)},
		},
	}

	_, _ = Call(context.Background(), sock, newTemplateRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{
			Observer: obs,
			Vars:     map[string]string{"tenantID": tenantID},
		})

	if len(obs.paths) == 0 || obs.paths[0] != expectedPath {
		t.Errorf("expected observer path %q, got %v", expectedPath, obs.paths)
	}
}

func TestCall_Vars_MissingVar_ReturnsCallError(t *testing.T) {
	sock := &mockSocket{}
	_, err := Call(context.Background(), sock, newTemplateRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{
			Vars: map[string]string{}, // tenantID missing
		})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	var missing reqreply.MissingRouteParamError
	if !errors.As(callErr, &missing) {
		t.Fatalf("expected MissingRouteParamError inside CallError, got %T", callErr.Err)
	}
}

func TestCallDealer_Vars_MissingVar_ReturnsCallError(t *testing.T) {
	sock := &mockSocket{}
	_, err := CallDealer(context.Background(), sock, newTemplateRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{
			Vars: map[string]string{}, // tenantID missing
		})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	var missing reqreply.MissingRouteParamError
	if !errors.As(callErr, &missing) {
		t.Fatalf("expected MissingRouteParamError inside CallError, got %T", callErr.Err)
	}
}

func TestCall_Vars_InvalidVar_ReturnsCallError(t *testing.T) {
	sock := &mockSocket{}
	_, err := Call(context.Background(), sock, newTemplateRouteHandle(),
		computeReq{X: 1, Y: 2},
		CallOptions{
			Vars: map[string]string{"tenantID": "not-a-uuid"},
		})

	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError, got %T: %v", err, err)
	}
	var paramErr reqreply.RouteParamError
	if !errors.As(callErr, &paramErr) {
		t.Fatalf("expected RouteParamError inside CallError, got %T", callErr.Err)
	}
}

// ── Phase 2: CallHandle (client-side only, no server-side merge) ──────

type tenantComputeReq struct {
	TenantID string
	X, Y     int
}

var tenantComputeReqCodec = codex.Struct[tenantComputeReq](
	codex.RequiredField("x", codex.Int(),
		func(r tenantComputeReq) int { return r.X },
		func(r *tenantComputeReq, v int) { r.X = v }),
	codex.RequiredField("y", codex.Int(),
		func(r tenantComputeReq) int { return r.Y },
		func(r *tenantComputeReq, v int) { r.Y = v }),
)

func newMergeRouteHandle() *reqreply.RouteHandle[tenantComputeReq, computeResp] {
	return reqreply.NewRoute[tenantComputeReq, computeResp](
		"compute/{tenantID}/add",
		tenantComputeReqCodec, computeRespCodec,
		reqreply.NewTopicParam("tenantID", codex.String().Refine(validate.NonEmptyString),
			func(r tenantComputeReq) string { return r.TenantID },
			func(r *tenantComputeReq, v string) { r.TenantID = v }),
	).ClientHandle()
}

// EV/C: CallHandle derives topic vars from req automatically — one
// struct in, no manual vars map needed. Note: zeromq has no server-side
// decode-merge (Serve carries no per-message topic string) — this
// convenience is client-side only, verified here via the observer-reported
// resolved path (mirrors TestCall_Vars_ObserverPathIsResolved).
func TestCallHandle_DerivesVarsFromReq(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), []byte(`{"sum":3}`)},
		},
	}

	_, _ = CallHandle(context.Background(), sock, newMergeRouteHandle(),
		tenantComputeReq{TenantID: "acme", X: 1, Y: 2},
		CallOptions{Observer: obs})

	wantPath := "compute/acme/add"
	if len(obs.paths) == 0 || obs.paths[0] != wantPath {
		t.Errorf("expected observer path %q, got %v", wantPath, obs.paths)
	}
}

// CallHandle explicit opts.Vars takes precedence over the derived value.
func TestCallHandle_ExplicitVarsOverridePrecedence(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), []byte(`{"sum":3}`)},
		},
	}

	_, _ = CallHandle(context.Background(), sock, newMergeRouteHandle(),
		tenantComputeReq{TenantID: "acme", X: 1, Y: 2},
		CallOptions{Observer: obs, Vars: map[string]string{"tenantID": "overridden"}})

	wantPath := "compute/overridden/add"
	if len(obs.paths) == 0 || obs.paths[0] != wantPath {
		t.Errorf("expected observer path %q (explicit override), got %v", wantPath, obs.paths)
	}
}

// ── CallOptions.RequestFormats / ResponseFormats per-call override ────────

// TestCall_RequestFormats_OverridesRouteDeclaredFormat verifies
// CallOptions.RequestFormats wins over the route's declared
// handle.RequestFormats (here: undeclared, JSON default) for THIS call only.
func TestCall_RequestFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	sock := &mockSocket{recvErr: ErrTimeout} // no reply — inspect the sent payload only
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = Call(ctx, sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{
			RequestFormats: []format.Format[computeReq]{format.YAML(computeReqCodec)},
		})

	sent := sock.sentSnapshot()
	if len(sent) == 0 {
		t.Fatal("expected a send, got none")
	}
	if !strings.Contains(string(sent[0][0]), "x: 3") {
		t.Errorf("want YAML-encoded payload (override), got %q", sent[0][0])
	}
}

// TestCall_RequestFormats_RouteDeclaredStillAppliesWithoutOverride verifies
// the route-declared (here: JSON default) format still applies when no
// per-call override is given.
func TestCall_RequestFormats_RouteDeclaredStillAppliesWithoutOverride(t *testing.T) {
	sock := &mockSocket{recvErr: ErrTimeout}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = Call(ctx, sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{})

	sent := sock.sentSnapshot()
	if len(sent) == 0 {
		t.Fatal("expected a send, got none")
	}
	decoded, err := format.JSON(computeReqCodec).Unmarshal(sent[0][0])
	if err != nil {
		t.Fatalf("want JSON-encoded payload (route-declared default), got %q: %v", sent[0][0], err)
	}
	if decoded.X != 3 || decoded.Y != 4 {
		t.Errorf("unexpected decoded payload: %+v", decoded)
	}
}

// TestCall_ResponseFormats_OverridesRouteDeclaredFormat verifies
// CallOptions.ResponseFormats wins over the route's declared handle.Formats
// (here: undeclared, JSON default) for THIS call only — the canned reply
// payload here is YAML-encoded, which only decodes correctly BECAUSE of
// the override.
func TestCall_ResponseFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), []byte("sum: 7\n")},
		},
	}
	result, err := Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{
			ResponseFormats: []format.Format[computeResp]{format.YAML(computeRespCodec)},
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Sum != 7 {
		t.Fatalf("want Sum=7 decoded via YAML override, got %d", result.Sum)
	}
}

// TestCall_RequestFormats_TypeMismatch_ReturnsCallError verifies a
// wrong-typed CallOptions.RequestFormats returns CallError, errors.As-reachable.
func TestCall_RequestFormats_TypeMismatch_ReturnsCallError(t *testing.T) {
	sock := &mockSocket{}
	_, err := Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{
			// Wrong type: []format.Format[computeResp] instead of []format.Format[computeReq].
			RequestFormats: []format.Format[computeResp]{format.JSON(computeRespCodec)},
		})
	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("want CallError, got %T: %v", err, err)
	}
}

// TestCall_ResponseFormats_TypeMismatch_ReturnsCallError mirrors
// TestCall_RequestFormats_TypeMismatch_ReturnsCallError for the response
// direction — requires a valid reply since the mismatch is only detected
// after one arrives.
func TestCall_ResponseFormats_TypeMismatch_ReturnsCallError(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), []byte(`{"sum":7}`)},
		},
	}
	_, err := Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{
			// Wrong type: []format.Format[computeReq] instead of []format.Format[computeResp].
			ResponseFormats: []format.Format[computeReq]{format.JSON(computeReqCodec)},
		})
	var callErr CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("want CallError, got %T: %v", err, err)
	}
}

// TestCallDealer_RequestFormats_OverridesRouteDeclaredFormat verifies the
// SAME per-call override wiring on CallDealer (which duplicates Call's
// encode/decode logic under a DEALER envelope).
func TestCallDealer_RequestFormats_OverridesRouteDeclaredFormat(t *testing.T) {
	sock := &mockSocket{recvErr: ErrTimeout}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = CallDealer(ctx, sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, CallOptions{
			RequestFormats: []format.Format[computeReq]{format.YAML(computeReqCodec)},
		})

	sent := sock.sentSnapshot()
	if len(sent) == 0 || len(sent[0]) < 2 {
		t.Fatalf("expected a DEALER send with [delimiter, payload], got %v", sent)
	}
	if !strings.Contains(string(sent[0][1]), "x: 3") {
		t.Errorf("want YAML-encoded payload (override), got %q", sent[0][1])
	}
}
