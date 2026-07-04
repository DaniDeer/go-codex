package zeromq_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/api/events"
	"github.com/DaniDeer/go-codex/api/rest"
	"github.com/DaniDeer/go-codex/codex"
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
	// timeoutCount is how many ErrTimeout returns to emit before real messages.
	timeoutCount int
}

func (m *mockSocket) SendFrames(frames [][]byte) error {
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
	if m.timeoutCount > 0 {
		m.timeoutCount--
		return nil, zeromq.ErrTimeout
	}
	if len(m.inFrames) == 0 {
		if m.recvErr != nil {
			return nil, m.recvErr
		}
		return nil, zeromq.ErrTimeout
	}
	frames := m.inFrames[0]
	m.inFrames = m.inFrames[1:]
	return frames, nil
}

func (m *mockSocket) SetSubscription(topic string) error {
	m.subTopic = topic
	return nil
}

func (m *mockSocket) SetRecvTimeout(_ time.Duration) error { return nil }

// ── channel handle helpers ────────────────────────────────────────────────────

func newChannelHandle() *events.ChannelHandle[sensorReading] {
	b := events.NewBuilder(events.Info{Title: "Test", Version: "1.0.0"})
	h, err := events.NewChannel[sensorReading]("sensors/readings", sensorCodec,
		events.Subscribe{Summary: "Sensor reading received"},
		events.Publish{Summary: "Sensor reading sent"},
	).Register(b)
	if err != nil {
		panic(err)
	}
	return h
}

func newRouteHandle() *rest.RouteHandle[computeReq, computeResp] {
	b := rest.NewBuilder(rest.Info{Title: "Test", Version: "1.0.0"})
	h, err := rest.NewRoute[computeReq, computeResp](
		"POST", "/compute",
		computeReqCodec, computeRespCodec,
		rest.RouteMeta{OperationID: "compute"},
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
	validationErrors []string
	startSpanOps     []string
	endSpanErrs      []error
}

func (o *testObserver) RecordValidationError(_, constraint, _ string) {
	o.validationErrors = append(o.validationErrors, constraint)
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
func (o *testObserver) StartSpan(ctx context.Context, op, _ string) context.Context {
	o.startSpanOps = append(o.startSpanOps, op)
	return ctx
}
func (o *testObserver) EndSpan(_ context.Context, err error) {
	o.endSpanErrs = append(o.endSpanErrs, err)
}

// ── Subscribe tests ───────────────────────────────────────────────────────────

const validSensorJSON = `{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":22.5}`

func runSubscribe(sock *mockSocket, fn func(context.Context, sensorReading) error, opts zeromq.SubscribeOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return zeromq.Subscribe(ctx, sock, newChannelHandle(), fn, opts)
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
	}, zeromq.SubscribeOptions{})

	if received.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Fatalf("unexpected sensor ID: %q", received.SensorID)
	}
	if received.Value != 22.5 {
		t.Fatalf("unexpected value: %v", received.Value)
	}
}

func TestSubscribe_SetsSubscriptionFilter(t *testing.T) {
	sock := &mockSocket{inFrames: [][][]byte{}}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error { return nil }, zeromq.SubscribeOptions{})
	if sock.subTopic != "sensors/readings" {
		t.Fatalf("expected subscription to %q, got %q", "sensors/readings", sock.subTopic)
	}
}

func TestSubscribe_DecodeError(t *testing.T) {
	var gotErr zeromq.SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(`{"sensor_id":"not-a-uuid","value":0}`)},
		},
	}
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error {
		t.Fatal("fn must not be called on decode error")
		return nil
	}, zeromq.SubscribeOptions{
		OnError: func(e zeromq.SubscribeError) { gotErr = e },
	})

	if gotErr.Kind != zeromq.KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
	if gotErr.Topic != "sensors/readings" {
		t.Fatalf("unexpected topic: %q", gotErr.Topic)
	}
}

func TestSubscribe_HandlerError(t *testing.T) {
	var gotErr zeromq.SubscribeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte(validSensorJSON)},
		},
	}
	handlerErr := errors.New("store unavailable")
	_ = runSubscribe(sock, func(_ context.Context, _ sensorReading) error {
		return handlerErr
	}, zeromq.SubscribeOptions{
		OnError: func(e zeromq.SubscribeError) { gotErr = e },
	})

	if gotErr.Kind != zeromq.KindHandler {
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
		zeromq.SubscribeOptions{Observer: obs})

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
		zeromq.SubscribeOptions{Observer: obs})

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
		zeromq.SubscribeOptions{Observer: obs})

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
		zeromq.SubscribeOptions{Observer: obs})

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
	}, zeromq.SubscribeOptions{})

	if called != 1 {
		t.Fatalf("expected fn called once (malformed frame skipped), got %d", called)
	}
}

// ── Publish tests ─────────────────────────────────────────────────────────────

func TestPublish_ValidMessage(t *testing.T) {
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 22.5}
	handle := newChannelHandle()
	err := zeromq.Publish(context.Background(), sock, handle, reading, nil, zeromq.PublishOptions{})
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
	handle := newChannelHandle()
	err := zeromq.Publish(context.Background(), sock, handle, invalid, nil, zeromq.PublishOptions{})

	var encErr zeromq.PublishEncodeError
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
	_ = zeromq.Publish(context.Background(), sock, newChannelHandle(), reading, nil,
		zeromq.PublishOptions{Observer: obs})

	if len(obs.publishes) != 1 || !obs.publishes[0] {
		t.Fatalf("expected successful RecordPublish, got %v", obs.publishes)
	}
}

func TestPublish_ObserverRecordPublishFailure(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{}
	invalid := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 0}
	_ = zeromq.Publish(context.Background(), sock, newChannelHandle(), invalid, nil,
		zeromq.PublishOptions{Observer: obs})

	if len(obs.publishes) != 1 || obs.publishes[0] {
		t.Fatalf("expected failed RecordPublish, got %v", obs.publishes)
	}
}

func TestPublish_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{}
	reading := sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	_ = zeromq.Publish(context.Background(), sock, newChannelHandle(), reading, nil,
		zeromq.PublishOptions{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.publish" {
		t.Fatalf("expected StartSpan with 'zmq.publish', got %v", obs.startSpanOps)
	}
}

// ── Serve tests ───────────────────────────────────────────────────────────────

const validComputeJSON = `{"x":3,"y":4}`

func runServe(sock *mockSocket, fn func(context.Context, computeReq) (computeResp, error), opts zeromq.ServeOptions) error {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return zeromq.Serve(ctx, sock, newRouteHandle(), fn, opts)
}

func TestServe_ValidRoundTrip(t *testing.T) {
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte(validComputeJSON)},
		},
	}
	_ = runServe(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, zeromq.ServeOptions{})

	if len(sock.sentFrames) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sock.sentFrames))
	}
	if string(sock.sentFrames[0][0]) != "ok" {
		t.Fatalf("expected status frame 'ok', got %q", sock.sentFrames[0][0])
	}
}

func TestServe_DecodeError(t *testing.T) {
	var gotErr zeromq.ServeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte(`bad json`)},
		},
	}
	_ = runServe(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		t.Fatal("fn must not be called on decode error")
		return computeResp{}, nil
	}, zeromq.ServeOptions{
		OnError: func(e zeromq.ServeError) { gotErr = e },
	})

	if gotErr.Kind != zeromq.KindDecode {
		t.Fatalf("expected KindDecode, got %v", gotErr.Kind)
	}
	// error reply must be sent to unblock REQ peer
	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "error" {
		t.Fatalf("expected error reply frame, got %v", sock.sentFrames)
	}
}

func TestServe_HandlerError(t *testing.T) {
	var gotErr zeromq.ServeError
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte(validComputeJSON)},
		},
	}
	handlerErr := errors.New("overflow")
	_ = runServe(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, handlerErr
	}, zeromq.ServeOptions{
		OnError: func(e zeromq.ServeError) { gotErr = e },
	})

	if gotErr.Kind != zeromq.KindHandler {
		t.Fatalf("expected KindHandler, got %v", gotErr.Kind)
	}
	if !errors.Is(gotErr, handlerErr) {
		t.Fatal("errors.Is must find handlerErr via Unwrap")
	}
	if len(sock.sentFrames) != 1 || string(sock.sentFrames[0][0]) != "error" {
		t.Fatalf("expected error reply frame, got %v", sock.sentFrames)
	}
}

func TestServe_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	_ = runServe(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, zeromq.ServeOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestServe_ObserverRecordRequestFailure(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(`bad`)}}}
	_ = runServe(sock, func(_ context.Context, _ computeReq) (computeResp, error) {
		return computeResp{}, nil
	}, zeromq.ServeOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 0 {
		t.Fatalf("expected RecordRequest(0), got %v", obs.requests)
	}
}

func TestServe_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	_ = runServe(sock, func(_ context.Context, r computeReq) (computeResp, error) {
		return computeResp{Sum: r.X + r.Y}, nil
	}, zeromq.ServeOptions{Observer: obs})

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
	result, err := zeromq.Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, zeromq.CallOptions{})
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
	_, err := zeromq.Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, zeromq.CallOptions{})

	var callErr zeromq.CallError
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
	_, err := zeromq.Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, zeromq.CallOptions{})

	var callErr zeromq.CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError on malformed reply, got %T: %v", err, err)
	}
}

func TestCall_ObserverRecordRequestSuccess(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}
	_, _ = zeromq.Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, zeromq.CallOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 200 {
		t.Fatalf("expected RecordRequest(200), got %v", obs.requests)
	}
}

func TestCall_ObserverRecordRequestFailure_ServerError(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("error"), []byte("internal")}}}
	_, _ = zeromq.Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, zeromq.CallOptions{Observer: obs})

	if len(obs.requests) != 1 || obs.requests[0] != 500 {
		t.Fatalf("expected RecordRequest(500), got %v", obs.requests)
	}
}

func TestCall_TraceObserver(t *testing.T) {
	obs := &testObserver{}
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), []byte(`{"sum":7}`)}}}
	_, _ = zeromq.Call(context.Background(), sock, newRouteHandle(),
		computeReq{X: 3, Y: 4}, zeromq.CallOptions{Observer: obs})

	if len(obs.startSpanOps) != 1 || obs.startSpanOps[0] != "zmq.request" {
		t.Fatalf("expected StartSpan with 'zmq.request', got %v", obs.startSpanOps)
	}
}

func TestCall_ContextCancelled(t *testing.T) {
	// sock returns only ErrTimeout forever → ctx cancels the loop
	sock := &mockSocket{timeoutCount: 100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := zeromq.Call(ctx, sock, newRouteHandle(),
		computeReq{X: 1, Y: 2}, zeromq.CallOptions{})

	var callErr zeromq.CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("expected CallError on context cancel, got %T: %v", err, err)
	}
}

// ── error LogValue tests ──────────────────────────────────────────────────────

func TestSubscribeError_LogValue(t *testing.T) {
	inner := errors.New("constraint failed")
	e := zeromq.SubscribeError{Kind: zeromq.KindDecode, Topic: "sensors/t1", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestPublishEncodeError_LogValue(t *testing.T) {
	inner := errors.New("bad value")
	e := zeromq.PublishEncodeError{Topic: "sensors/t1", Err: inner}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestServeError_LogValue(t *testing.T) {
	e := zeromq.ServeError{Kind: zeromq.KindHandler, Err: errors.New("overflow")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

func TestCallError_LogValue(t *testing.T) {
	e := zeromq.CallError{Err: errors.New("timeout")}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("expected Group log value, got %v", v.Kind())
	}
}

// ── errors.As / Unwrap tests ──────────────────────────────────────────────────

func TestSubscribeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := zeromq.SubscribeError{Kind: zeromq.KindDecode, Topic: "t", Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}

func TestServeError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := zeromq.ServeError{Kind: zeromq.KindHandler, Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}

func TestCallError_ErrorsAs(t *testing.T) {
	inner := errors.New("inner")
	outer := zeromq.CallError{Err: inner}
	if !errors.Is(outer, inner) {
		t.Fatal("errors.Is must traverse Unwrap to find inner")
	}
}
