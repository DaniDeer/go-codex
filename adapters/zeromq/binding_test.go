package zeromq_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── CallAdapter ───────────────────────────────────────────────────────────────

func TestCallAdapter_EmitsResponses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := newRouteHandle()

	respPayload, _ := json.Marshal(map[string]any{"sum": 7})
	sock := &mockSocket{inFrames: [][][]byte{{[]byte("ok"), respPayload}}}

	reqCh := make(chan computeReq, 1)
	reqCh <- computeReq{X: 3, Y: 4}
	close(reqCh)
	src := gstream.From(ctx, reqCh)

	p, err := ports.NewIOPort[computeReq, computeResp]("test", computeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, zeromq.CallAdapter(sock, handle, zeromq.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, src)
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if len(vals) != 1 || vals[0].Sum != 7 {
		t.Errorf("want [{Sum:7}], got %v", vals)
	}
}

func TestCallAdapter_ErrorsForwardedFromSrc(t *testing.T) {
	ctx := context.Background()
	handle := newRouteHandle()
	sock := &mockSocket{}

	errCh := make(chan error, 1)
	valCh := make(chan computeReq)
	errCh <- fmt.Errorf("upstream error")
	close(errCh)
	close(valCh)
	src := gstream.Stream[computeReq]{Values: valCh, Errors: errCh}

	p, err := ports.NewIOPort[computeReq, computeResp]("test", computeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, zeromq.CallAdapter(sock, handle, zeromq.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, src)
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Errorf("want 1 forwarded error, got %d", len(errs))
	}
}

// ── ServeAdapter ──────────────────────────────────────────────────────────────

func TestServeAdapter_HandlesRequestViaToolPort(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	sock := &mockSocket{inFrames: [][][]byte{{[]byte(validComputeJSON)}}}
	handle := newRouteHandle()

	p, err := ports.NewToolPort[computeReq, computeResp]("compute", computeReqCodec, computeRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.SetPipeline(func(_ context.Context, req computeReq) gstream.Stream[computeResp] {
		return gstream.Single(context.Background(), computeResp{Sum: req.X + req.Y})
	})

	if err := p.Bind(ctx, zeromq.ServeAdapter(sock, handle, zeromq.ServeOptions{})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	// Wait for the background Serve goroutine to process the one queued message.
	deadline := time.Now().Add(400 * time.Millisecond)
	for len(sock.sentFrames) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(sock.sentFrames) == 0 {
		t.Fatal("timeout waiting for Serve to respond")
	}

	if string(sock.sentFrames[0][0]) != "ok" {
		t.Fatalf("want status frame 'ok', got %q", sock.sentFrames[0][0])
	}
	var resp computeResp
	if err := json.Unmarshal(sock.sentFrames[0][1], &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Sum != 7 {
		t.Errorf("want Sum=7, got %d", resp.Sum)
	}
}

func TestServeAdapter_NoPipelineError(t *testing.T) {
	ctx := context.Background()
	sock := &mockSocket{}
	handle := newRouteHandle()

	p, err := ports.NewToolPort[computeReq, computeResp]("compute-nopipeline", computeReqCodec, computeRespCodec, ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	if err := p.Bind(ctx, zeromq.ServeAdapter(sock, handle, zeromq.ServeOptions{})); err == nil {
		t.Fatal("want error when no pipeline set")
	}
}
