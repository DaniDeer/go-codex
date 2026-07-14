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

	p := ports.NewIOPort[computeReq, computeResp]("test", computeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 4})
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

	p := ports.NewIOPort[computeReq, computeResp]("test", computeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 4})
	p.Bind(ctx, zeromq.CallAdapter(sock, handle, zeromq.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, src)
	_, errs := gstream.Collect(ctx, out)
	if len(errs) != 1 {
		t.Errorf("want 1 forwarded error, got %d", len(errs))
	}
}
