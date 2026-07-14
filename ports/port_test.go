package ports_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/DaniDeer/go-codex/codex"
	"github.com/DaniDeer/go-codex/ports"
	gstream "github.com/DaniDeer/go-codex/stream"
)

// ── shared helpers ────────────────────────────────────────────────────────────

var intCodec = codex.Int()
var strCodec = codex.String()

func intPort(name string, buf int) *ports.SourcePort[int] {
	return ports.NewSourcePort[int](name, intCodec, ports.PortOptions{Buffer: buf})
}

func intSinkPort(name string, buf int) *ports.SinkPort[int] {
	return ports.NewSinkPort[int](name, intCodec, ports.PortOptions{Buffer: buf})
}

func feedChan(vals ...int) <-chan int {
	ch := make(chan int, len(vals))
	for _, v := range vals {
		ch <- v
	}
	close(ch)
	return ch
}

func collectStream[T any](ctx context.Context, s gstream.Stream[T]) ([]T, []error) {
	return gstream.Collect(ctx, s)
}

// ── T01: SourcePort single adapter ───────────────────────────────────────────

func TestSourcePort_SingleAdapter(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 4)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2, 3)))
	vals, errs := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 3 {
		t.Errorf("want 3 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
}

// ── T02: SourcePort fan-in (two adapters) ────────────────────────────────────

func TestSourcePort_FanIn(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 8)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(1, 2)))
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(3, 4)))
	vals, errs := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 4 {
		t.Errorf("want 4 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

// ── T03: SourcePort with no adapters closes immediately ──────────────────────

func TestSourcePort_NoAdapters(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 4)
	// No Bind calls — stream should close immediately
	vals, errs := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 0 {
		t.Errorf("want 0 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d", len(errs))
	}
}

// ── T04: SourcePort ctx cancel stops adapter ─────────────────────────────────

func TestSourcePort_CtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := intPort("test", 8)

	// Infinite source channel (never closes)
	infinite := make(chan int, 4)
	infinite <- 1
	infinite <- 2
	p.Bind(ctx, ports.ChanSourceAdapter(infinite))
	s := p.Stream(ctx)
	cancel()
	// After cancel, stream should terminate
	vals, _ := collectStream(context.Background(), s)
	if len(vals) > 2 {
		t.Errorf("want at most 2 values after cancel, got %d", len(vals))
	}
}

// ── T05: SourcePort error from adapter reaches Stream.Errors ─────────────────

func TestSourcePort_AdapterErrorReachesStreamErrors(t *testing.T) {
	ctx := context.Background()

	// Custom adapter that emits one error
	errAdapter := &errSourceAdapter{err: errors.New("sensor offline")}
	p := intPort("test", 4)
	p.Bind(ctx, errAdapter)
	_, errs := collectStream(ctx, p.Stream(ctx))
	if len(errs) == 0 {
		t.Fatal("want error in Stream.Errors, got none")
	}
}

type errSourceAdapter struct{ err error }

func (a *errSourceAdapter) AdapterName() string { return "errSourceAdapter" }
func (a *errSourceAdapter) Activate(_ context.Context, _ chan<- int, errs chan<- error) {
	errs <- a.err
}

// ── T06: SinkPort single adapter ─────────────────────────────────────────────

func TestSinkPort_SingleAdapter(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("test", 4)
	out := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out))

	src := gstream.From(ctx, feedChan(10, 20, 30))
	p.Feed(ctx, src) // blocks until adapter goroutine finishes

	var got []int
	for len(out) > 0 {
		got = append(got, <-out)
	}
	if len(got) != 3 {
		t.Errorf("want 3 values in sink, got %d: %v", len(got), got)
	}
}

// ── T07: SinkPort fan-out (two adapters) ─────────────────────────────────────

func TestSinkPort_FanOut(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("test", 8)
	out1 := make(chan int, 8)
	out2 := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out1))
	p.Bind(ctx, ports.ChanSinkAdapter(out2))

	src := gstream.From(ctx, feedChan(1, 2, 3))
	p.Feed(ctx, src) // blocks until both adapter goroutines finish

	var got1, got2 []int
	for len(out1) > 0 {
		got1 = append(got1, <-out1)
	}
	for len(out2) > 0 {
		got2 = append(got2, <-out2)
	}
	if len(got1) != 3 || len(got2) != 3 {
		t.Errorf("want 3 in each sink, got %d and %d", len(got1), len(got2))
	}
}

// ── T08: IOPort happy path ────────────────────────────────────────────────────

func TestIOPort_HappyPath(t *testing.T) {
	ctx := context.Background()
	p := ports.NewIOPort[int, string]("double-str", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	if err := p.Bind(ctx, ports.FuncIOAdapter(func(_ context.Context, v int) (string, error) {
		return fmt.Sprintf("%d", v*2), nil
	})); err != nil {
		t.Fatalf("Bind: %v", err)
	}

	src := gstream.From(ctx, feedChan(1, 2, 3))
	out := p.Connect(ctx, src)
	vals, errs := collectStream(ctx, out)
	if len(vals) != 3 {
		t.Errorf("want 3 values, got %d", len(vals))
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %d: %v", len(errs), errs)
	}
	if vals[0] != "2" || vals[1] != "4" || vals[2] != "6" {
		t.Errorf("want [2 4 6], got %v", vals)
	}
}

// ── T09: IOPort no adapter → PortNoAdapterError ──────────────────────────────

func TestIOPort_NoAdapterError(t *testing.T) {
	ctx := context.Background()
	p := ports.NewIOPort[int, string]("test", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	// No Bind

	src := gstream.From(ctx, feedChan(1))
	out := p.Connect(ctx, src)
	_, errs := collectStream(ctx, out)
	if len(errs) == 0 {
		t.Fatal("want PortNoAdapterError in Stream.Errors")
	}
	var nae ports.PortNoAdapterError
	if !errors.As(errs[0], &nae) {
		t.Errorf("want PortNoAdapterError, got %T: %v", errs[0], errs[0])
	}
	if nae.Port != "test" {
		t.Errorf("Port: want %q, got %q", "test", nae.Port)
	}
}

// ── T10: IOPort double bind → PortBindError ───────────────────────────────────

func TestIOPort_DoubleBind(t *testing.T) {
	ctx := context.Background()
	p := ports.NewIOPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	fn := ports.FuncIOAdapter(func(_ context.Context, v int) (string, error) { return "", nil })

	if err := p.Bind(ctx, fn); err != nil {
		t.Fatalf("first Bind: unexpected error: %v", err)
	}
	err := p.Bind(ctx, fn)
	if err == nil {
		t.Fatal("second Bind: want PortBindError, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T: %v", err, err)
	}
}

// ── T11: IOPort adapter error routes to Stream.Errors ────────────────────────

func TestIOPort_AdapterErrorInStreamErrors(t *testing.T) {
	ctx := context.Background()
	p := ports.NewIOPort[int, string]("test", intCodec, strCodec, ports.PortOptions{Buffer: 4})
	p.Bind(ctx, ports.FuncIOAdapter(func(_ context.Context, _ int) (string, error) { //nolint:errcheck
		return "", errors.New("enrichment failure")
	}))

	src := gstream.From(ctx, feedChan(1, 2))
	out := p.Connect(ctx, src)
	vals, errs := collectStream(ctx, out)
	if len(vals) != 0 {
		t.Errorf("want 0 values, got %d", len(vals))
	}
	if len(errs) != 2 {
		t.Errorf("want 2 errors, got %d: %v", len(errs), errs)
	}
}

// ── T12: ChanSourceAdapter pushes items ──────────────────────────────────────

func TestChanSourceAdapter(t *testing.T) {
	ctx := context.Background()
	p := intPort("test", 4)
	p.Bind(ctx, ports.ChanSourceAdapter(feedChan(7, 8, 9)))
	vals, _ := collectStream(ctx, p.Stream(ctx))
	if len(vals) != 3 {
		t.Errorf("want 3, got %d", len(vals))
	}
}

// ── T13: ChanSinkAdapter captures items ──────────────────────────────────────

func TestChanSinkAdapter(t *testing.T) {
	ctx := context.Background()
	p := intSinkPort("test", 4)
	out := make(chan int, 8)
	p.Bind(ctx, ports.ChanSinkAdapter(out))
	src := gstream.From(ctx, feedChan(1, 2, 3))
	p.Feed(ctx, src)
	var got []int
	for len(out) > 0 {
		got = append(got, <-out)
	}
	if len(got) != 3 {
		t.Errorf("want 3, got %d", len(got))
	}
}

// ── T14: FuncIOAdapter transforms correctly ───────────────────────────────────

func TestFuncIOAdapter(t *testing.T) {
	ctx := context.Background()
	adapter := ports.FuncIOAdapter(func(_ context.Context, v int) (int, error) {
		return v * 10, nil
	})
	src := gstream.From(ctx, feedChan(1, 2, 3))
	out := adapter.Transform(ctx, src)
	vals, errs := collectStream(ctx, out)
	if len(vals) != 3 || vals[0] != 10 || vals[1] != 20 || vals[2] != 30 {
		t.Errorf("want [10 20 30], got %v", vals)
	}
	if len(errs) != 0 {
		t.Errorf("want 0 errors, got %v", errs)
	}
}

// ── T15: PortBindError LogValue ───────────────────────────────────────────────

func TestPortBindError_LogValue(t *testing.T) {
	inner := errors.New("connect refused")
	e := ports.PortBindError{Port: "sensor-readings", Adapter: "mqtt5.SubscribeAdapter", Err: inner}

	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	keys := map[string]bool{}
	for _, a := range attrs {
		keys[a.Key] = true
	}
	for _, want := range []string{"port", "adapter", "err"} {
		if !keys[want] {
			t.Errorf("missing attribute %q", want)
		}
	}
	if errors.Unwrap(e) != inner {
		t.Error("Unwrap must return inner error")
	}
}

// ── T16: PortNoAdapterError LogValue ─────────────────────────────────────────

func TestPortNoAdapterError_LogValue(t *testing.T) {
	e := ports.PortNoAdapterError{Port: "calibration"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) == 0 || attrs[0].Key != "port" {
		t.Errorf("want 'port' attribute, got %v", attrs)
	}
}

// ── T17: IOParam WithCodec ────────────────────────────────────────────────────

func TestIOParam_WithCodec(t *testing.T) {
	original := ports.IOParam{Name: "sensorID", Required: true}
	if original.Codec != nil {
		t.Fatal("original should have nil Codec")
	}
	c := codex.String()
	updated := original.WithCodec(c)
	if updated.Codec == nil {
		t.Error("updated should have non-nil Codec")
	}
	if original.Codec != nil {
		t.Error("WithCodec must not mutate the original")
	}
	if updated.Name != "sensorID" {
		t.Error("WithCodec must preserve other fields")
	}
}

// ── ToolPort ──────────────────────────────────────────────────────────────────

// mockToolAdapter is a test ToolAdapter that stores the bound fn.
type mockToolAdapter struct {
	name string
	fn   func(context.Context, int) gstream.Stream[string]
	err  error
}

func (a *mockToolAdapter) AdapterName() string { return a.name }
func (a *mockToolAdapter) Bind(_ context.Context, fn func(context.Context, int) gstream.Stream[string]) error {
	if a.err != nil {
		return a.err
	}
	a.fn = fn
	return nil
}

func TestToolPort_HappyPath(t *testing.T) {
	ctx := context.Background()
	p := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	p.SetPipeline(func(_ context.Context, v int) gstream.Stream[string] {
		return gstream.Single(context.Background(), fmt.Sprintf("%d", v))
	})

	adapter := &mockToolAdapter{name: "mock.ToolAdapter"}
	if err := p.Bind(ctx, adapter); err != nil {
		t.Fatalf("Bind: unexpected error: %v", err)
	}
	if adapter.fn == nil {
		t.Fatal("Bind should have set fn on adapter")
	}

	out := adapter.fn(ctx, 42)
	vals, _ := gstream.Collect(ctx, out)
	if len(vals) != 1 || vals[0] != "42" {
		t.Errorf("want [42], got %v", vals)
	}
}

func TestToolPort_NoPipelineError(t *testing.T) {
	ctx := context.Background()
	p := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	// No SetPipeline call

	adapter := &mockToolAdapter{name: "mock.ToolAdapter"}
	err := p.Bind(ctx, adapter)
	if err == nil {
		t.Fatal("want error when pipeline not set, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T: %v", err, err)
	}
	var npe ports.PortNoPipelineError
	if !errors.As(err, &npe) {
		t.Errorf("want PortNoPipelineError wrapped in PortBindError, got %T", err)
	}
}

func TestToolPort_MultipleBind(t *testing.T) {
	ctx := context.Background()
	p := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	p.SetPipeline(func(_ context.Context, v int) gstream.Stream[string] {
		return gstream.Single(context.Background(), fmt.Sprintf("%d", v))
	})

	a1 := &mockToolAdapter{name: "adapter1"}
	a2 := &mockToolAdapter{name: "adapter2"}
	if err := p.Bind(ctx, a1); err != nil {
		t.Fatalf("first Bind: %v", err)
	}
	if err := p.Bind(ctx, a2); err != nil {
		t.Fatalf("second Bind: %v", err)
	}
	if a1.fn == nil || a2.fn == nil {
		t.Error("both adapters should have fn set")
	}
}

func TestToolPort_AdapterError(t *testing.T) {
	ctx := context.Background()
	p := ports.NewToolPort[int, string]("test", intCodec, strCodec, ports.PortOptions{})
	p.SetPipeline(func(_ context.Context, _ int) gstream.Stream[string] {
		return gstream.Single(context.Background(), "")
	})

	adapter := &mockToolAdapter{name: "failing", err: errors.New("route conflict")}
	err := p.Bind(ctx, adapter)
	if err == nil {
		t.Fatal("want error from failing adapter, got nil")
	}
	var pbe ports.PortBindError
	if !errors.As(err, &pbe) {
		t.Errorf("want PortBindError, got %T", err)
	}
}

func TestPortNoPipelineError_LogValue(t *testing.T) {
	e := ports.PortNoPipelineError{Port: "oee-tool"}
	v := e.LogValue()
	if v.Kind() != slog.KindGroup {
		t.Fatalf("want KindGroup, got %v", v.Kind())
	}
	attrs := v.Group()
	if len(attrs) == 0 || attrs[0].Key != "port" {
		t.Errorf("want 'port' attribute, got %v", attrs)
	}
}
