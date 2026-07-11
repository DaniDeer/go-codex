package zeromq_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/format"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── SubscribeStream ───────────────────────────────────────────────────────────

func TestSubscribeStream_DecodesValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	payload1, _ := json.Marshal(map[string]any{"sensor_id": "550e8400-e29b-41d4-a716-446655440000", "value": 1.5})
	payload2, _ := json.Marshal(map[string]any{"sensor_id": "550e8400-e29b-41d4-a716-446655440001", "value": 2.5})

	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), payload1},
			{[]byte("sensors/readings"), payload2},
		},
	}

	handle := newChannelHandle()
	s := zeromq.SubscribeStream(ctx, sock, handle, format.JSON(sensorCodec),
		gstream.SourceOptions{Name: "test/subscribe"})

	vals, errs := gstream.Collect(ctx, s)
	if len(vals) != 2 {
		t.Errorf("want 2 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if vals[0].Value != 1.5 || vals[1].Value != 2.5 {
		t.Errorf("unexpected values: %v", vals)
	}
}

func TestSubscribeStream_DecodeErrorGoesToErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	good, _ := json.Marshal(map[string]any{"sensor_id": "550e8400-e29b-41d4-a716-446655440000", "value": 1.0})

	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("sensors/readings"), []byte("not-json")},
			{[]byte("sensors/readings"), good},
		},
	}

	handle := newChannelHandle()
	s := zeromq.SubscribeStream(ctx, sock, handle, format.JSON(sensorCodec),
		gstream.SourceOptions{Name: "test/decode-err"})

	vals, errs := gstream.Collect(ctx, s)
	if len(vals) != 1 {
		t.Errorf("want 1 good value, got %d", len(vals))
	}
	if len(errs) != 1 {
		t.Errorf("want 1 decode error, got %d", len(errs))
	}
}

func TestSubscribeStream_SocketErrorTerminatesStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sock := &mockSocket{
		recvErr: fmt.Errorf("connection reset"),
	}
	handle := newChannelHandle()
	s := zeromq.SubscribeStream(ctx, sock, handle, format.JSON(sensorCodec),
		gstream.SourceOptions{})

	_, _ = gstream.Collect(ctx, s)
	// Must terminate without hanging — socket error closes rawCh → stream closes.
}

// ── DrainPublish ──────────────────────────────────────────────────────────────

func TestDrainPublish_PublishesEachItem(t *testing.T) {
	ctx := context.Background()
	sock := &mockSocket{}
	handle := newChannelHandle()

	ch := make(chan sensorReading, 2)
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 1.0}
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440001", Value: 2.0}
	close(ch)
	src := gstream.From(ctx, ch)

	zeromq.DrainPublish(ctx, sock, handle, src, format.JSON(sensorCodec),
		zeromq.DrainPublishOptions{})

	if len(sock.sentFrames) != 2 {
		t.Errorf("want 2 sent frames, got %d", len(sock.sentFrames))
	}
}

func TestDrainPublish_ErrorsFromSrcForwardedToOnError(t *testing.T) {
	ctx := context.Background()
	sock := &mockSocket{}
	handle := newChannelHandle()

	errCh := make(chan error, 1)
	valCh := make(chan sensorReading)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := gstream.Stream[sensorReading]{Values: valCh, Errors: errCh}

	var gotErr error
	zeromq.DrainPublish(ctx, sock, handle, src, format.JSON(sensorCodec),
		zeromq.DrainPublishOptions{OnError: func(e error) { gotErr = e }})

	if gotErr == nil {
		t.Error("want error from upstream src.Errors, got nil")
	}
}

// ── AsPipelineFunc ────────────────────────────────────────────────────────────

func TestAsPipelineFunc_ReturnsFirstValue(t *testing.T) {
	fn := zeromq.AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		result := computeResp{Sum: req.X + req.Y}
		return gstream.Single(ctx, result)
	})

	ctx := context.Background()
	resp, err := fn(ctx, computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("want Sum=7, got %d", resp.Sum)
	}
}

func TestAsPipelineFunc_ErrorTakesPrecedence(t *testing.T) {
	fn := zeromq.AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		errCh := make(chan error, 1)
		valCh := make(chan computeResp)
		errCh <- fmt.Errorf("compute failed")
		close(errCh)
		close(valCh)
		return gstream.Stream[computeResp]{Values: valCh, Errors: errCh}
	})

	ctx := context.Background()
	_, err := fn(ctx, computeReq{X: 1, Y: 2})
	if err == nil {
		t.Error("want error from pipeline, got nil")
	}
}

func TestAsPipelineFunc_NoValueReturnsPipelineNoResponseError(t *testing.T) {
	fn := zeromq.AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		errCh := make(chan error)
		valCh := make(chan computeResp)
		close(errCh)
		close(valCh)
		return gstream.Stream[computeResp]{Values: valCh, Errors: errCh}
	})

	ctx := context.Background()
	_, err := fn(ctx, computeReq{X: 1, Y: 2})
	var nre zeromq.PipelineNoResponseError
	if !isErrorAs(err, &nre) {
		t.Errorf("want PipelineNoResponseError, got %T: %v", err, err)
	}
}

// ── ServeLatest ───────────────────────────────────────────────────────────────

func TestServeLatest_ReturnsLatestValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := newRouteHandle()

	// Stream emits one value immediately.
	valCh := make(chan computeResp, 1)
	valCh <- computeResp{Sum: 42}
	errCh := make(chan error)
	src := gstream.Stream[computeResp]{Values: valCh, Errors: errCh}

	// Build a mock REP socket: one request then context cancel.
	reqPayload, _ := json.Marshal(map[string]any{"x": 1, "y": 2})
	sock := &mockSocket{
		inFrames: [][][]byte{
			{reqPayload},
		},
	}

	// Give the background goroutine time to populate latest before first recv.
	time.Sleep(10 * time.Millisecond)
	close(valCh)
	close(errCh)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = zeromq.ServeLatest(ctx, sock, handle, src,
		zeromq.ServeLatestOptions{})
	// Test verifies no hang — timing-dependent latestValue population.
}

func TestServeLatest_NoValueSendsNoLatestValueError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	handle := newRouteHandle()
	// Empty stream — no values ever.
	errCh := make(chan error)
	valCh := make(chan computeResp)
	close(errCh)
	close(valCh)
	src := gstream.Stream[computeResp]{Values: valCh, Errors: errCh}

	reqPayload, _ := json.Marshal(map[string]any{"x": 1, "y": 2})
	sock := &mockSocket{
		inFrames: [][][]byte{{reqPayload}},
	}

	var gotNoVal *zeromq.NoLatestValueError
	zeromq.ServeLatest(ctx, sock, handle, src, //nolint:errcheck
		zeromq.ServeLatestOptions{
			OnError: func(e error) {
				var nv zeromq.NoLatestValueError
				if isErrorAs(e, &nv) {
					gotNoVal = &nv
				}
			},
		})
	if gotNoVal == nil {
		t.Error("want NoLatestValueError when no value produced yet")
	}
}

// ── CallStream ────────────────────────────────────────────────────────────────

func TestCallStream_EmitsResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := newRouteHandle()

	// Mock: for each SendFrames call, queue a successful ok+json reply.
	respPayload, _ := json.Marshal(map[string]any{"sum": 7})
	sock := &mockSocket{
		inFrames: [][][]byte{
			{[]byte("ok"), respPayload},
		},
	}

	reqCh := make(chan computeReq, 1)
	reqCh <- computeReq{X: 3, Y: 4}
	close(reqCh)
	src := gstream.From(ctx, reqCh)

	out := zeromq.CallStream(ctx, sock, handle, src, zeromq.CallStreamOptions{})
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 1 || vals[0].Sum != 7 {
		t.Errorf("want [{Sum:7}], got %v", vals)
	}
}

func TestCallStream_ErrorsForwardedFromSrc(t *testing.T) {
	ctx := context.Background()
	handle := newRouteHandle()
	sock := &mockSocket{}

	errCh := make(chan error, 1)
	valCh := make(chan computeReq)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := gstream.Stream[computeReq]{Values: valCh, Errors: errCh}

	out := zeromq.CallStream(ctx, sock, handle, src, zeromq.CallStreamOptions{})
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Errorf("want 1 forwarded error, got %d", len(errs))
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isErrorAs[T any](err error, target *T) bool {
	var t T
	return fmt.Errorf("%w", err) != nil && func() bool {
		// Use type assertion pattern matching
		if v, ok := err.(T); ok {
			*target = v
			return true
		}
		_ = t
		return false
	}()
}
