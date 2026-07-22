package zeromq_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	zeromq "github.com/DaniDeer/go-codex/adapters/zeromq"
	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/format"
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

// ── G1: per-item vars derivation (shipped) ───────────────────────────────────

// G1-3 (zeromq CallAdapter): derives request-topic vars PER-ITEM from each
// item's own merge fields when opts.Vars is nil.
func TestCallAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	handle := newMergeRouteHandle()
	sock := &mockSocket{inFrames: [][][]byte{
		{[]byte("ok"), []byte(`{"sum":3}`)},
		{[]byte("ok"), []byte(`{"sum":7}`)},
	}}

	reqCh := make(chan tenantComputeReq, 2)
	reqCh <- tenantComputeReq{TenantID: "acme", X: 1, Y: 2}
	reqCh <- tenantComputeReq{TenantID: "globex", X: 3, Y: 4}
	close(reqCh)
	src := gstream.From(ctx, reqCh)

	p, err := ports.NewIOPort[tenantComputeReq, computeResp]("test", tenantComputeReqCodec, computeRespCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	// opts.Vars left nil -> per-item derivation via CallHandle.
	p.Bind(ctx, zeromq.CallAdapter(sock, handle, zeromq.CallStreamOptions{})) //nolint:errcheck
	out := p.Connect(ctx, src)
	vals, errs := gstream.Collect(ctx, out)
	if len(errs) != 0 {
		t.Fatalf("want 0 errors, got %v", errs)
	}
	if len(vals) != 2 || vals[0].Sum != 3 || vals[1].Sum != 7 {
		t.Errorf("want [{Sum:3} {Sum:7}], got %v", vals)
	}
}

// G1-3 (zeromq PublishAdapter): derives topic vars PER-ITEM from each item's
// own merge fields when opts.Vars is nil — two items with different sensor
// IDs must publish to two different concrete topics.
func TestZeromqPublishAdapter_DerivesVarsPerItem_WhenOptsVarsNil(t *testing.T) {
	ctx := context.Background()
	sock := &mockSocket{}
	handle := newMergeChannelHandle()

	ch := make(chan sensorReading, 2)
	ch <- sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 2.0}
	close(ch)

	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, zeromq.PublishAdapter(sock, handle, format.JSON(sensorCodec), zeromq.DrainPublishOptions{}))
	p.Feed(ctx, gstream.From(ctx, ch))

	sent := sock.sentSnapshot()
	if len(sent) != 2 {
		t.Fatalf("want 2 published, got %d", len(sent))
	}
	if string(sent[0][0]) != "sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings" ||
		string(sent[1][0]) != "sensors/550e8400-e29b-41d4-a716-446655440000/readings" {
		t.Errorf("want per-item resolved topics, got %q, %q", sent[0][0], sent[1][0])
	}
}

// Explicit (non-nil) DrainPublishOptions.Vars still wins — regression guard.
func TestZeromqPublishAdapter_ExplicitVarsStillWins(t *testing.T) {
	ctx := context.Background()
	sock := &mockSocket{}
	handle := newMergeChannelHandle()

	ch := make(chan sensorReading, 2)
	ch <- sensorReading{SensorID: "f47ac10b-58cc-4372-a567-0e02b2c3d479", Value: 1.0}
	ch <- sensorReading{SensorID: "550e8400-e29b-41d4-a716-446655440000", Value: 2.0}
	close(ch)

	p, err := ports.NewSinkPort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, zeromq.PublishAdapter(sock, handle, format.JSON(sensorCodec),
		zeromq.DrainPublishOptions{Vars: map[string]string{"sensorID": "static-sensor"}}))
	p.Feed(ctx, gstream.From(ctx, ch))

	sent := sock.sentSnapshot()
	for _, frames := range sent {
		if string(frames[0]) != "sensors/static-sensor/readings" {
			t.Errorf("want static topic for every item, got %q", frames[0])
		}
	}
}

// G2 regression guard: SubscribeAdapter (SourceAdapter) still forwards
// decode errors and delivers plain values when the channel declares no
// merge fields — verifies the Activate rewrite (now delegating to
// [zeromq.Subscribe]) preserved existing behavior.
func TestSubscribeAdapter_DeliversDecodedValues(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newChannelHandle()
	sock := &mockSocket{inFrames: [][][]byte{
		{[]byte("sensors/readings"), []byte(`{"sensor_id":"f47ac10b-58cc-4372-a567-0e02b2c3d479","value":1.5}`)},
	}}

	p, err := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, zeromq.SubscribeAdapter(sock, handle, format.JSON(sensorCodec), zeromq.SubscribeAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)

	var got sensorReading
	select {
	case v, ok := <-s.Values:
		if ok {
			got = v
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timeout waiting for item in stream")
	}
	cancel()
	if got.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" || got.Value != 1.5 {
		t.Errorf("want decoded value, got %+v", got)
	}
}

// G2: SubscribeAdapter auto-merges topic vars when the channel declares
// merge fields — proves the port-binding SourceAdapter (which now delegates
// to [zeromq.Subscribe] instead of hand-rolling frame decode) gets the same
// merge wiring [zeromq.Subscribe] callers get directly.
func TestSubscribeAdapter_MergeFields_AutoMergesTopicVars(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	handle := newMergeChannelHandle()
	sock := &mockSocket{inFrames: [][][]byte{
		{[]byte("sensors/f47ac10b-58cc-4372-a567-0e02b2c3d479/readings"),
			[]byte(`{"sensor_id":"00000000-0000-0000-0000-000000000000","value":22.5}`)},
	}}

	p, err := ports.NewSourcePort[sensorReading]("test", sensorCodec, ports.PortOptions{Buffer: 4})
	if err != nil {
		t.Fatalf("construct port: %v", err)
	}
	p.Bind(ctx, zeromq.SubscribeAdapter(sock, handle, format.JSON(sensorCodec), zeromq.SubscribeAdapterOptions{Buffer: 4}))
	s := p.Stream(ctx)

	var got sensorReading
	select {
	case v, ok := <-s.Values:
		if ok {
			got = v
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("timeout waiting for item in stream")
	}
	cancel()
	if got.SensorID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("SensorID: want merged from topic, got %q", got.SensorID)
	}
	if got.Value != 22.5 {
		t.Errorf("Value: want 22.5, got %v", got.Value)
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
	var sent [][][]byte
	for len(sent) == 0 && time.Now().Before(deadline) {
		sent = sock.sentSnapshot()
		time.Sleep(10 * time.Millisecond)
	}
	if len(sent) == 0 {
		t.Fatal("timeout waiting for Serve to respond")
	}

	if string(sent[0][0]) != "ok" {
		t.Fatalf("want status frame 'ok', got %q", sent[0][0])
	}
	var resp computeResp
	if err := json.Unmarshal(sent[0][1], &resp); err != nil {
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

// ── LatestAdapter (LatestPort) ────────────────────────────────────────────────

func TestZeromqLatestAdapter_ServesLatest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	port, err := ports.NewLatestPort[computeResp]("latest", respCodec(), ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, err := port.PluginReqReplyPattern(ports.ReqReplyPattern{Topic: "compute/latest"})
	if err != nil {
		t.Fatalf("PluginReqReplyPattern: %v", err)
	}

	// Seed the cache first, then bind — the REP loop answers from the cell.
	seed := make(chan computeResp, 1)
	seed <- computeResp{Sum: 42}
	close(seed)
	port.Feed(ctx, gstream.From(ctx, seed))

	sock := &mockSocket{inFrames: [][][]byte{{[]byte(`{}`)}}}
	if err := port.Bind(ctx, zeromq.LatestAdapter(sock, handle, zeromq.ServeLatestOptions{})); err != nil {
		t.Fatalf("bind: %v", err)
	}

	deadline := time.Now().Add(400 * time.Millisecond)
	var sent [][][]byte
	for len(sent) == 0 && time.Now().Before(deadline) {
		sent = sock.sentSnapshot()
		time.Sleep(5 * time.Millisecond)
	}
	if len(sent) == 0 {
		t.Fatal("want a reply frame, got none")
	}
	if !strings.Contains(string(sent[0][len(sent[0])-1]), "42") {
		t.Errorf("want reply containing 42, got %q", sent[0])
	}
}

func TestZeromqLatestAdapter_EmptyCache_ErrorReply(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	port, err := ports.NewLatestPort[computeResp]("latest-empty", respCodec(), ports.PortOptions{})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	handle, err := port.PluginReqReplyPattern(ports.ReqReplyPattern{Topic: "compute/latest"})
	if err != nil {
		t.Fatalf("PluginReqReplyPattern: %v", err)
	}

	errCh := make(chan error, 1)
	sock := &mockSocket{inFrames: [][][]byte{{[]byte(`{}`)}}}
	if err := port.Bind(ctx, zeromq.LatestAdapter(sock, handle, zeromq.ServeLatestOptions{
		OnError: func(e error) { errCh <- e },
	})); err != nil {
		t.Fatalf("bind: %v", err)
	}

	select {
	case e := <-errCh:
		var nv zeromq.NoLatestValueError
		if !errors.As(e, &nv) {
			t.Errorf("want NoLatestValueError, got %T: %v", e, e)
		}
	case <-time.After(400 * time.Millisecond):
		t.Fatal("timeout waiting for NoLatestValueError")
	}
}

func respCodec() codex.Codec[computeResp] {
	return codex.Struct[computeResp](
		codex.RequiredField("sum", codex.Int(),
			func(r computeResp) int { return r.Sum },
			func(r *computeResp, v int) { r.Sum = v }),
	)
}
