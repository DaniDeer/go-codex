package zeromq

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── AsPipelineFunc ────────────────────────────────────────────────────────────

func TestAsPipelineFunc_ReturnsFirstValue(t *testing.T) {
	fn := AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		return gstream.Single(ctx, computeResp{Sum: req.X + req.Y})
	})

	resp, err := fn(context.Background(), computeReq{X: 3, Y: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("want Sum=7, got %d", resp.Sum)
	}
}

func TestAsPipelineFunc_ErrorTakesPrecedence(t *testing.T) {
	fn := AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		errCh := make(chan error, 1)
		valCh := make(chan computeResp)
		errCh <- fmt.Errorf("compute failed")
		close(errCh)
		close(valCh)
		return gstream.Stream[computeResp]{Values: valCh, Errors: errCh}
	})

	_, err := fn(context.Background(), computeReq{X: 1, Y: 2})
	if err == nil {
		t.Error("want error from pipeline, got nil")
	}
}

func TestAsPipelineFunc_NoValueReturnsPipelineNoResponseError(t *testing.T) {
	fn := AsPipelineFunc(func(ctx context.Context, req computeReq) gstream.Stream[computeResp] {
		errCh := make(chan error)
		valCh := make(chan computeResp)
		close(errCh)
		close(valCh)
		return gstream.Stream[computeResp]{Values: valCh, Errors: errCh}
	})

	_, err := fn(context.Background(), computeReq{X: 1, Y: 2})
	var nre PipelineNoResponseError
	if !isErrorAs(err, &nre) {
		t.Errorf("want PipelineNoResponseError, got %T: %v", err, err)
	}
}

// ── ServeLatest ───────────────────────────────────────────────────────────────

func TestServeLatest_ReturnsLatestValue(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := newRouteHandle()

	valCh := make(chan computeResp, 1)
	valCh <- computeResp{Sum: 42}
	errCh := make(chan error)
	src := gstream.Stream[computeResp]{Values: valCh, Errors: errCh}

	reqPayload, _ := json.Marshal(map[string]any{"x": 1, "y": 2})
	sock := &mockSocket{inFrames: [][][]byte{{reqPayload}}}

	time.Sleep(10 * time.Millisecond)
	close(valCh)
	close(errCh)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_ = ServeLatest(ctx, sock, handle, src, ServeLatestOptions{})
}

func TestServeLatest_NoValueSendsNoLatestValueError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	handle := newRouteHandle()
	errCh := make(chan error)
	valCh := make(chan computeResp)
	close(errCh)
	close(valCh)
	src := gstream.Stream[computeResp]{Values: valCh, Errors: errCh}

	reqPayload, _ := json.Marshal(map[string]any{"x": 1, "y": 2})
	sock := &mockSocket{inFrames: [][][]byte{{reqPayload}}}

	var gotNoVal *NoLatestValueError
	ServeLatest(ctx, sock, handle, src, //nolint:errcheck
		ServeLatestOptions{
			OnError: func(e error) {
				var nv NoLatestValueError
				if isErrorAs(e, &nv) {
					gotNoVal = &nv
				}
			},
		})
	if gotNoVal == nil {
		t.Error("want NoLatestValueError when no value produced yet")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func isErrorAs[T any](err error, target *T) bool {
	if v, ok := err.(T); ok {
		*target = v
		return true
	}
	return false
}
